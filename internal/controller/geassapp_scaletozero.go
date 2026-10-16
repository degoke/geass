package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A service that asks to scale to zero is put behind the KEDA HTTP add-on's proxy.
//
//	visitor -> Traefik -> <app>-wake (a name that points at the proxy) -> proxy -> <app> -> pods
//
// The proxy knows the service by its host. While the service has pods it passes requests straight on; when it
// has none it holds the request, asks KEDA to start one, and passes the request on once it is ready. KEDA
// stops the service again after it has been idle for the chosen time. The HTTPScaledObject is what tells the
// add-on all this; the add-on's own scaler owns the Deployment's replica count from then on, so Geass must
// stop setting it.

// +kubebuilder:rbac:groups=http.keda.sh,resources=httpscaledobjects,verbs=get;list;watch;create;update;patch;delete

var (
	httpScaledObjectKind     = schema.GroupVersionKind{Group: "http.keda.sh", Version: "v1alpha1", Kind: "HTTPScaledObject"}
	httpScaledObjectListKind = httpScaledObjectKind.GroupVersion().WithKind("HTTPScaledObjectList")
)

const (
	// conditionScaleToZero says whether a service that asked to sleep when idle is doing so, and why not when it is
	// not. It is only on services that asked.
	conditionScaleToZero = "ScaleToZero"

	reasonWaking                    = "Waking"
	reasonScaleToZeroActive         = "Active"
	reasonScaleToZeroNeedsAddon     = "AddonNotInstalled"
	reasonScaleToZeroNeedsHost      = "NoPublicHost"
	reasonScaleToZeroUsesAutoscaler = "AutoscalingSet"

	// annotationTemplateFingerprint is on a service's Deployment: what its pods are made from, so a change of
	// version is known when it happens.
	annotationTemplateFingerprint = "geass.dev/template-fingerprint"
	// annotationVerifiedTemplate is on its HTTPScaledObject: the version that has been seen to start. The add-on
	// may only stop the service once this is the version that is deployed.
	annotationVerifiedTemplate = "geass.dev/verified-template"

	defaultIdleSeconds = int32(300)
	defaultMaxReplicas = int32(3)
)

func wakeServiceName(app *geassv1alpha1.GeassApp) string { return app.Name + "-wake" }

// scaleToZeroPlan is whether a service is to sleep when idle, and if it asked to but cannot, why.
type scaleToZeroPlan struct {
	// requested is whether the service asked.
	requested bool
	// active is whether it will: it asked, it can, and the add-on that makes it work is installed.
	active bool
	// reason and message say why not, when it asked and will not.
	reason, message string
	hosts           []string
	idleSeconds     int32
	maxReplicas     int32
	// verifying is whether the version of the service now deployed has yet to be seen to start. Until it has, one
	// copy is kept running so a version that cannot start is found out by the deploy, and not by the first
	// visitor. It is set while the service is reconciled.
	verifying bool
}

// scaleToZeroRequested is whether a service asked to sleep when idle.
func scaleToZeroRequested(app *geassv1alpha1.GeassApp) bool {
	return app.Spec.ScaleToZero != nil && app.Spec.ScaleToZero.Enabled
}

// scaleToZeroHosts are the public hosts the proxy routes for a service.
func scaleToZeroHosts(app *geassv1alpha1.GeassApp) []string {
	var hosts []string
	for _, rule := range app.Spec.Ingress.Rules {
		if rule.Host != "" && !slices.Contains(hosts, rule.Host) {
			hosts = append(hosts, rule.Host)
		}
	}
	if app.Spec.Ingress.Host != "" && !slices.Contains(hosts, app.Spec.Ingress.Host) {
		hosts = append(hosts, app.Spec.Ingress.Host)
	}
	return hosts
}

// scaleToZeroPrefixes are the paths the proxy routes by: those of the service's ingress, and of its schedules.
func scaleToZeroPrefixes(app *geassv1alpha1.GeassApp) []string {
	var prefixes []string
	rules := app.Spec.Ingress.Rules
	if len(rules) == 0 {
		rules = []geassv1alpha1.GeassAppIngressRule{{Path: app.Spec.Ingress.Path}}
	}
	paths := make([]string, 0, len(rules))
	for _, rule := range rules {
		paths = append(paths, rule.Path)
	}
	// A schedule calls the service through the proxy too, which only routes the paths it is given.
	if fn := app.Spec.Source.Function; fn != nil {
		for _, trigger := range fn.Triggers {
			paths = append(paths, trigger.Path)
		}
	}
	for _, path := range paths {
		if path == "" {
			path = "/"
		}
		if !slices.Contains(prefixes, path) {
			prefixes = append(prefixes, path)
		}
	}
	return prefixes
}

// planScaleToZero decides whether a service sleeps when idle. A service that cannot, because the add-on is not
// installed, it has no public host (a request from that host is what wakes it), or it asks for CPU autoscaling
// as well (which would fight the add-on over the replica count), runs always on as it did before, and the
// reason is shown on it.
func (r *GeassAppReconciler) planScaleToZero(ctx context.Context, app *geassv1alpha1.GeassApp) (scaleToZeroPlan, error) {
	plan := scaleToZeroPlan{requested: scaleToZeroRequested(app), hosts: scaleToZeroHosts(app)}
	if !plan.requested {
		return plan, nil
	}
	plan.idleSeconds, plan.maxReplicas = app.Spec.ScaleToZero.IdleSeconds, app.Spec.ScaleToZero.MaxReplicas
	if plan.idleSeconds <= 0 {
		plan.idleSeconds = defaultIdleSeconds
	}
	if plan.maxReplicas <= 0 {
		plan.maxReplicas = defaultMaxReplicas
	}
	switch {
	case app.Spec.Autoscaling != nil:
		plan.reason, plan.message = reasonScaleToZeroUsesAutoscaler, "Scale to zero decides how many copies run, so CPU autoscaling has to be turned off"
		return plan, nil
	case len(plan.hosts) == 0:
		plan.reason, plan.message = reasonScaleToZeroNeedsHost, "Scale to zero needs a public host: it is the requests to it that wake the service"
		return plan, nil
	}
	installed, err := r.httpScaledObjectsServed(ctx)
	if err != nil {
		return plan, err
	}
	if !installed {
		plan.reason, plan.message = reasonScaleToZeroNeedsAddon, "Scale to zero needs the serverless add-on: set spec.addons.serverless.enabled on the GeassCluster"
		return plan, nil
	}
	plan.active, plan.reason, plan.message = true, reasonScaleToZeroActive, "Sleeps after idle, wakes on the next request"
	return plan, nil
}

// httpScaledObjectsServed reports whether the add-on's resource exists in this cluster, which is how the
// add-on is known to be installed: its CRD is what the proxy's controller works from.
func (r *GeassAppReconciler) httpScaledObjectsServed(ctx context.Context) (bool, error) {
	return httpScaledObjectsServed(ctx, r.Client)
}

func httpScaledObjectsServed(ctx context.Context, c client.Reader) (bool, error) {
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(httpScaledObjectListKind)
	err := c.List(ctx, &list, client.Limit(1))
	switch {
	case err == nil:
		return true, nil
	case meta.IsNoMatchError(err):
		return false, nil
	default:
		return false, err
	}
}

// reconcileScaleToZero makes the pieces that route through the proxy when the service sleeps when idle, and
// removes them when it does not. It also says, in plan.verifying, whether the deployed version still has to be
// seen to start: until it has, the service is held at one copy.
func (r *GeassAppReconciler) reconcileScaleToZero(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, plan *scaleToZeroPlan) error {
	plan.verifying = false
	if !plan.active {
		return r.deleteScaleToZero(ctx, app, wsNS)
	}
	port := app.Spec.Port
	if port == 0 {
		port = 8080
	}
	wake := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: wakeServiceName(app), Namespace: wsNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, wake, func() error {
		applyGeassLabels(wake, app, "GeassApp")
		wake.Spec.Type = corev1.ServiceTypeExternalName
		wake.Spec.ExternalName = platform.ServerlessInterceptorHost
		wake.Spec.Ports = []corev1.ServicePort{{Name: portNameHTTP, Port: platform.ServerlessInterceptorPort, TargetPort: intstr.FromInt32(platform.ServerlessInterceptorPort)}}
		return setSameNamespaceOwner(app, wake, r.Scheme)
	}); err != nil {
		return fmt.Errorf("could not make the service that routes through the scale-to-zero proxy: %w", err)
	}

	fingerprint, err := r.templateFingerprint(ctx, app, wsNS)
	if err != nil {
		return err
	}
	scaled := &unstructured.Unstructured{}
	scaled.SetGroupVersionKind(httpScaledObjectKind)
	scaled.SetName(app.Name)
	scaled.SetNamespace(wsNS)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, scaled, func() error {
		applyGeassLabels(scaled, app, "GeassApp")
		plan.verifying = fingerprint == "" || scaled.GetAnnotations()[annotationVerifiedTemplate] != fingerprint
		// Only the fields Geass owns are set, so the defaults the add-on fills in are left as they are and a
		// reconcile that changes nothing writes nothing.
		for path, value := range map[string]any{
			"hosts":        toAnySlice(plan.hosts),
			"pathPrefixes": toAnySlice(scaleToZeroPrefixes(app)),
			"scaleTargetRef": map[string]any{
				"name": app.Name, "kind": "Deployment", "apiVersion": "apps/v1", "service": app.Name, "port": int64(port),
			},
			"replicas":        map[string]any{"min": minReplicas(plan.verifying), "max": int64(plan.maxReplicas)},
			"scaledownPeriod": int64(plan.idleSeconds),
		} {
			if err := unstructured.SetNestedField(scaled.Object, value, "spec", path); err != nil {
				return err
			}
		}
		return setSameNamespaceOwner(app, scaled, r.Scheme)
	})
	return err
}

// minReplicas is the fewest copies the add-on may leave a service with: none once its version has been seen to
// start, and one until then.
func minReplicas(verifying bool) int64 {
	if verifying {
		return 1
	}
	return 0
}

// templateFingerprint is what the service's pods are made from, as recorded on its Deployment. It is empty
// when there is no Deployment yet.
func (r *GeassAppReconciler) templateFingerprint(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) (string, error) {
	var deploy appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: wsNS}, &deploy); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	return deploy.Annotations[annotationTemplateFingerprint], nil
}

// fingerprintOf fingerprints what a pod is made from.
func fingerprintOf(template *corev1.PodTemplateSpec) string {
	encoded, _ := json.Marshal(template)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:16]
}

// deploymentVerified is whether the current version of a service has a copy that is up, which is what it takes
// for the version to count as one that starts. It is stricter than ready: a copy of the previous version that is
// still up while the new one fails does not count.
func deploymentVerified(deploy *appsv1.Deployment) bool {
	return deploy.Status.ObservedGeneration >= deploy.Generation &&
		deploy.Status.UpdatedReplicas >= 1 && deploy.Status.AvailableReplicas >= 1 &&
		deploy.Status.Replicas == deploy.Status.UpdatedReplicas
}

// markScaleToZeroVerified records that the deployed version has started, and lets the add-on stop it when idle.
func (r *GeassAppReconciler) markScaleToZeroVerified(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, deploy *appsv1.Deployment) error {
	scaled := &unstructured.Unstructured{}
	scaled.SetGroupVersionKind(httpScaledObjectKind)
	if err := r.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: wsNS}, scaled); err != nil {
		return client.IgnoreNotFound(err)
	}
	annotations := scaled.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[annotationVerifiedTemplate] = deploy.Annotations[annotationTemplateFingerprint]
	scaled.SetAnnotations(annotations)
	if err := unstructured.SetNestedField(scaled.Object, minReplicas(false), "spec", "replicas", "min"); err != nil {
		return err
	}
	return r.Update(ctx, scaled)
}

// wakeDeadline is how long a woken service may take to start before that is a failure of its own.
const wakeDeadline = 10 * time.Minute

// startProblem says why a copy of a service cannot start, or "" when it is only taking its time. A copy that is
// crashing or cannot be pulled will not get better by waiting, and the Deployment itself gives up on one that
// makes no progress.
func (r *GeassAppReconciler) startProblem(ctx context.Context, wsNS string, deploy *appsv1.Deployment) string {
	var pods corev1.PodList
	if deploy.Spec.Selector != nil {
		_ = r.List(ctx, &pods, client.InNamespace(wsNS), client.MatchingLabels(deploy.Spec.Selector.MatchLabels))
	}
	for i := range pods.Items {
		statuses := slices.Concat(pods.Items[i].Status.InitContainerStatuses, pods.Items[i].Status.ContainerStatuses)
		for _, status := range statuses {
			if waiting := status.State.Waiting; waiting != nil && slices.Contains(startFailureReasons, waiting.Reason) {
				// The reason only: the registry's own words are long, and are read as a platform fault by the code that
				// summarises failures for users. They are on the pod's events.
				return fmt.Sprintf("%s is %s", status.Name, waiting.Reason)
			}
		}
	}
	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse && cond.Reason == "ProgressDeadlineExceeded" {
			return "it did not become ready in time"
		}
	}
	return ""
}

// startFailureMessage is what a service says when a copy of it cannot start. A copy that is in a crash loop because the
// kernel keeps stopping it for memory says that, which is what can be done something about, and not only that it is
// crash looping, which is what every cause looks like from outside.
func (r *GeassAppReconciler) startFailureMessage(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS, problem string) string {
	if strings.HasSuffix(problem, "is CrashLoopBackOff") {
		if kill, found := r.recentOOMKill(ctx, app, wsNS); found {
			return kill.Message()
		}
	}
	return "The service cannot start: " + problem
}

// wakeProblem says why a service that was woken cannot start, or "" when it is only taking its time. A service
// that sleeps is otherwise ready whenever it has no copies, so nothing else would say so.
func (r *GeassAppReconciler) wakeProblem(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, deploy *appsv1.Deployment) string {
	if problem := r.startProblem(ctx, wsNS, deploy); problem != "" {
		return "The service was woken but its copy cannot start: " + problem
	}
	if cond := meta.FindStatusCondition(app.Status.Conditions, platform.ConditionReady); cond != nil && cond.Reason == reasonWaking && time.Since(cond.LastTransitionTime.Time) > wakeDeadline {
		return fmt.Sprintf("The service was woken but has not started within %s", wakeDeadline)
	}
	return ""
}

// startFailureReasons are the states of a container that mean it is not going to start without a change.
var startFailureReasons = []string{"CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError"}

// startGate decides what a service that is not ready is, from what its copies are doing: failed, if one cannot
// start; otherwise starting. For a service that sleeps it also handles the moments particular to it: a version
// that is starting for the first time is checked and then allowed to sleep, and a service that has been woken
// is given time to start but not forever. It reports whether it has settled this reconcile.
func (r *GeassAppReconciler) startGate(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, deploy *appsv1.Deployment, plan scaleToZeroPlan, replicas int32) (bool, ctrl.Result, error) {
	switch {
	case !plan.active:
		// An ordinary service whose copy cannot start is a failed deploy too, and not "starting" for ever.
		if !deploymentReady(deploy, replicas) {
			if problem := r.startProblem(ctx, wsNS, deploy); problem != "" {
				result, err := r.setNotReady(ctx, app, r.startFailureMessage(ctx, app, wsNS, problem))
				return true, result, err
			}
		}
		return false, ctrl.Result{}, nil
	case plan.verifying:
		if deploymentVerified(deploy) {
			return false, ctrl.Result{}, r.markScaleToZeroVerified(ctx, app, wsNS, deploy)
		}
		// Not up yet. A version that cannot start is a failed deploy, said so now and not when the first visitor
		// finds out; one that is only slow is left to start.
		if problem := r.startProblem(ctx, wsNS, deploy); problem != "" {
			result, err := r.setNotReady(ctx, app, "The new version of the service cannot start: "+problem)
			return true, result, err
		}
		return false, ctrl.Result{}, nil
	case replicas > 0 && !deploymentReady(deploy, replicas):
		if problem := r.wakeProblem(ctx, app, wsNS, deploy); problem != "" {
			result, err := r.setNotReady(ctx, app, problem)
			return true, result, err
		}
		result, err := r.setWaking(ctx, app)
		return true, result, err
	}
	return false, ctrl.Result{}, nil
}

func toAnySlice(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

// deleteScaleToZero removes what routes through the proxy. A cluster without the add-on has nothing to remove.
func (r *GeassAppReconciler) deleteScaleToZero(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	scaled := &unstructured.Unstructured{}
	scaled.SetGroupVersionKind(httpScaledObjectKind)
	scaled.SetName(app.Name)
	scaled.SetNamespace(wsNS)
	if err := r.Delete(ctx, scaled); err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
		return err
	}
	return client.IgnoreNotFound(r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: wakeServiceName(app), Namespace: wsNS}}))
}

// scaleToZeroCondition is the condition that shows a service's scale to zero, or nothing for a service that
// never asked.
func scaleToZeroCondition(plan scaleToZeroPlan, conditions []metav1.Condition) []metav1.Condition {
	if !plan.requested {
		return slices.DeleteFunc(conditions, func(c metav1.Condition) bool { return c.Type == conditionScaleToZero })
	}
	status := metav1.ConditionFalse
	if plan.active {
		status = metav1.ConditionTrue
	}
	return platform.SetCondition(conditions, conditionScaleToZero, status, plan.reason, plan.message)
}

// effectiveReplicas is how many copies a service is meant to have right now. For a service that sleeps when idle
// that is whatever its Deployment has been scaled to: an idle one is at zero, and that is as it should be, so
// it is ready, not starting.
func effectiveReplicas(app *geassv1alpha1.GeassApp, deploy *appsv1.Deployment, plan scaleToZeroPlan) int32 {
	if plan.verifying {
		return max(1, deploymentReplicasOf(deploy))
	}
	if plan.active && deploy.Spec.Replicas != nil {
		return *deploy.Spec.Replicas
	}
	return deploymentReplicas(app)
}

func deploymentReplicasOf(deploy *appsv1.Deployment) int32 {
	if deploy.Spec.Replicas == nil {
		return 1
	}
	return *deploy.Spec.Replicas
}

// wakingRequeue is how soon to look again at a service that is starting after a request.
const wakingRequeue = 10 * time.Second

// setWaking says a sleeping service is starting, without counting it as a failure to start.
func (r *GeassAppReconciler) setWaking(ctx context.Context, app *geassv1alpha1.GeassApp) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassApp{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(app), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, reasonWaking, "Starting after a request")
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: wakingRequeue}, nil
}
