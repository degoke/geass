package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A function's schedule is a CronJob per trigger, in the project's namespace. Each run is one HTTP request to
// the function, so a trigger needs nothing from the function but that it answers: a web request, a
// scheduled run and a handler's event all arrive the same way. A function that is asleep is woken by the
// request, because it goes through the scale-to-zero proxy like any other.

const (
	labelFunctionTrigger = "geass.dev/function-trigger"
	// annotationTriggerHash is what a trigger's CronJob was made from, so an unchanged one is not written again.
	annotationTriggerHash = "geass.dev/trigger-hash"
	// maxCronJobName is the longest name a CronJob can have: Kubernetes adds a run number to it for the Jobs.
	maxCronJobName = 52

	// A run that has not answered within this long has failed. It covers a cold start, which is the slow case.
	triggerRequestSeconds = 120
	triggerJobDeadline    = int64(triggerRequestSeconds + 60)
	// A run that could not start within this long of its time is skipped, not run late.
	triggerStartingDeadline = int64(300)
)

// triggerSecretName is the Secret that holds a function's schedule token. See function_shims.go for what the
// token proves and how it is used.
func triggerSecretName(app *geassv1alpha1.GeassApp) string { return app.Name + "-trigger" }

const triggerSecretKey = "token"

// annotationTriggerTokenFingerprint is on a function's pods: which token they were started with.
const annotationTriggerTokenFingerprint = "geass.dev/trigger-token-fingerprint"

// triggerTokenFingerprint fingerprints a function's schedule token, or is empty when it has none yet.
func (r *GeassAppReconciler) triggerTokenFingerprint(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) string {
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Name: triggerSecretName(app), Namespace: wsNS}, &secret); err != nil || len(secret.Data[triggerSecretKey]) == 0 {
		return ""
	}
	sum := sha256.Sum256(secret.Data[triggerSecretKey])
	return hex.EncodeToString(sum[:])[:16]
}

// functionHasTriggerToken is whether a function is given a schedule token: one that is a handler reads it to
// tell a schedule from a visitor, and one that has schedules gets it so its own code can do the same.
func functionHasTriggerToken(fn *geassv1alpha1.GeassAppFunctionSource) bool {
	return fn != nil && (platform.FunctionMode(fn) == platform.FunctionModeEvent || len(fn.Triggers) > 0)
}

// triggerTokenEnv is the schedule token as an environment variable, read from its Secret when the pod starts.
func triggerTokenEnv(app *geassv1alpha1.GeassApp) corev1.EnvVar {
	return corev1.EnvVar{Name: platform.TriggerTokenEnv, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: triggerSecretName(app)}, Key: triggerSecretKey,
	}}}
}

// reconcileTriggerSecret makes a function's schedule token, once: a token that already exists is kept, so the
// pods and the CronJobs keep agreeing on it. A function that no longer needs one has it removed.
func (r *GeassAppReconciler) reconcileTriggerSecret(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: triggerSecretName(app), Namespace: wsNS}}
	if !functionHasTriggerToken(app.Spec.Source.Function) {
		return r.deleteIfPresent(ctx, secret)
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(secret), secret)
	if err == nil && len(secret.Data[triggerSecretKey]) > 0 {
		return nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return fmt.Errorf("could not make the schedule token: %w", err)
	}
	applyGeassLabels(secret, app, "GeassApp")
	secret.Data = map[string][]byte{triggerSecretKey: []byte(hex.EncodeToString(token))}
	if err := setSameNamespaceOwner(app, secret, r.Scheme); err != nil {
		return err
	}
	if secret.ResourceVersion == "" {
		return r.Create(ctx, secret)
	}
	return r.Update(ctx, secret)
}

// triggerCronJobName is the name of a trigger's CronJob, kept within the length a CronJob may have. It always
// ends in a fingerprint of the app and the trigger, because both may contain dashes, and without it app "a"
// with trigger "b-c" and app "a-b" with trigger "c" would be the same CronJob.
func triggerCronJobName(app *geassv1alpha1.GeassApp, trigger string) string {
	sum := sha256.Sum256([]byte(app.Name + "\x00" + trigger))
	suffix := "-" + hex.EncodeToString(sum[:])[:8]
	name := app.Name + "-" + trigger
	if limit := maxCronJobName - len(suffix); len(name) > limit {
		name = name[:limit]
	}
	return name + suffix
}

// triggerRequest is the URL a trigger calls and the host it asks for, which is only set when the call goes
// through the scale-to-zero proxy: the proxy tells services apart by host.
func triggerRequest(app *geassv1alpha1.GeassApp, wsNS string, plan scaleToZeroPlan, trigger geassv1alpha1.GeassFunctionTrigger) (target, host string) {
	path := trigger.Path
	if path == "" {
		path = "/"
	}
	if plan.active && len(plan.hosts) > 0 {
		return fmt.Sprintf("http://%s:%d%s", platform.ServerlessInterceptorHost, platform.ServerlessInterceptorPort, path), plan.hosts[0]
	}
	port := app.Spec.Port
	if port == 0 {
		port = 8080
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s", app.Name, wsNS, port, path), ""
}

// triggerPayload is the body of a trigger's request.
func triggerPayload(trigger geassv1alpha1.GeassFunctionTrigger) string {
	if trigger.Payload != "" {
		return trigger.Payload
	}
	body, _ := json.Marshal(map[string]string{"trigger": "cron", "name": trigger.Name})
	return string(body)
}

// triggerCronJobSpec is the CronJob for one trigger. Its pod is an ordinary short-lived job: it runs where
// the project's jobs run, so a project that must stay on certain nodes keeps its schedule there too.
func triggerCronJobSpec(app *geassv1alpha1.GeassApp, wsNS string, plan scaleToZeroPlan, placement platform.Placement, trigger geassv1alpha1.GeassFunctionTrigger) batchv1.CronJobSpec {
	target, host := triggerRequest(app, wsNS, plan, trigger)
	args := []string{
		"--fail", "--silent", "--show-error", "--max-time", strconv.Itoa(triggerRequestSeconds),
		"--request", "POST",
		"--header", "content-type: application/json",
		// $(NAME) is filled in from the container's environment by Kubernetes, so the token is never in the spec.
		"--header", "x-geass-trigger-token: $(" + platform.TriggerTokenEnv + ")",
		"--header", "x-geass-trigger-name: " + trigger.Name,
	}
	if host != "" {
		args = append(args, "--header", "Host: "+host)
	}
	// The payload is passed as its own argument, never through a shell, so it cannot be read as anything but data.
	args = append(args, "--data-raw", triggerPayload(trigger), target)

	history, failedHistory, deadline, startingDeadline, retries := int32(1), int32(3), triggerJobDeadline, triggerStartingDeadline, int32(1)
	notAllowed, noToken := false, false
	timeZone := "Etc/UTC"
	return batchv1.CronJobSpec{
		Schedule: trigger.Schedule,
		TimeZone: &timeZone,
		// A run that is still going when the next is due is not doubled.
		ConcurrencyPolicy:          batchv1.ForbidConcurrent,
		StartingDeadlineSeconds:    &startingDeadline,
		SuccessfulJobsHistoryLimit: &history,
		FailedJobsHistoryLimit:     &failedHistory,
		JobTemplate: batchv1.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelFunctionTrigger: trigger.Name}},
			Spec: batchv1.JobSpec{
				BackoffLimit:          &retries,
				ActiveDeadlineSeconds: &deadline,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelFunctionTrigger: trigger.Name}},
					Spec: corev1.PodSpec{
						RestartPolicy:                corev1.RestartPolicyNever,
						AutomountServiceAccountToken: &noToken,
						NodeSelector:                 placement.NodeSelector(),
						Tolerations:                  placement.Tolerations(),
						Containers: []corev1.Container{{
							Name: "trigger", Image: platform.BackupTransferImage, Command: []string{"curl"}, Args: args,
							Env: []corev1.EnvVar{triggerTokenEnv(app)},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
								Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
							},
							SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &notAllowed, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
						}},
					},
				},
			},
		},
	}
}

// reconcileFunctionTriggers makes a CronJob for each of a function's triggers, and removes the ones it no
// longer has. A CronJob that is already what it should be is left alone, not written again.
func (r *GeassAppReconciler) reconcileFunctionTriggers(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, plan scaleToZeroPlan) error {
	fn := app.Spec.Source.Function
	if fn == nil || len(fn.Triggers) == 0 {
		return r.deleteFunctionTriggers(ctx, app, wsNS, nil)
	}
	placement, err := platform.ProjectJobPlacement(ctx, r.Client, app.Spec.Project)
	if err != nil {
		return err
	}
	// A schedule's pod is an organization's like the service's are, and runs under the same sandboxed runtime. Without the
	// runtime no schedule is written.
	sandbox, err := platform.ResolveSandbox(ctx, r.Client, app.Spec.Project, placement.NodeSelector())
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, trigger := range fn.Triggers {
		cron := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: triggerCronJobName(app, trigger.Name), Namespace: wsNS}}
		keep[cron.Name] = true
		spec := triggerCronJobSpec(app, wsNS, plan, placement, trigger)
		sandbox.Apply(&spec.JobTemplate.Spec.Template.Spec)
		encoded, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(encoded)
		hash := hex.EncodeToString(sum[:])[:16]

		existing := &batchv1.CronJob{}
		switch err := r.Get(ctx, client.ObjectKeyFromObject(cron), existing); {
		case err == nil && existing.Annotations[annotationTriggerHash] == hash:
			continue
		case err == nil:
			cron = existing
		case client.IgnoreNotFound(err) != nil:
			return err
		}
		applyGeassLabels(cron, app, "GeassApp")
		cron.Labels[labelFunctionTrigger] = trigger.Name
		if cron.Annotations == nil {
			cron.Annotations = map[string]string{}
		}
		cron.Annotations[annotationTriggerHash] = hash
		cron.Spec = spec
		if err := setSameNamespaceOwner(app, cron, r.Scheme); err != nil {
			return err
		}
		if cron.ResourceVersion == "" {
			err = r.Create(ctx, cron)
		} else {
			err = r.Update(ctx, cron)
		}
		if err != nil {
			return fmt.Errorf("could not schedule trigger %q: %w", trigger.Name, err)
		}
	}
	return r.deleteFunctionTriggers(ctx, app, wsNS, keep)
}

// deleteFunctionTriggers removes the CronJobs of a function's triggers, except the ones named in keep.
func (r *GeassAppReconciler) deleteFunctionTriggers(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, keep map[string]bool) error {
	var crons batchv1.CronJobList
	if err := r.List(ctx, &crons, client.InNamespace(wsNS), client.MatchingLabels{labelGeassKind: "GeassApp", labelGeassName: app.Name, labelGeassSystemNS: app.Namespace},
		client.HasLabels{labelFunctionTrigger}); err != nil {
		return err
	}
	for i := range crons.Items {
		if keep[crons.Items[i].Name] {
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, &crons.Items[i], client.PropagationPolicy(metav1.DeletePropagationBackground))); err != nil {
			return err
		}
	}
	return nil
}
