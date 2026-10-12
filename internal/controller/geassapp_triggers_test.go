package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func triggerApp(triggers ...geassv1alpha1.GeassFunctionTrigger) *geassv1alpha1.GeassApp {
	app := functionApp("node", "export function handler() {}")
	app.Spec.Source.Function.Mode = platform.FunctionModeEvent
	app.Spec.Source.Function.Triggers = triggers
	return app
}

func triggerReconciler(t *testing.T) (*GeassAppReconciler, *writeLog) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	writes := &writeLog{}
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			writes.record("create", obj)
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			writes.record("update", obj)
			return c.Update(ctx, obj, opts...)
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			writes.record("delete", obj)
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()
	return &GeassAppReconciler{Client: c, Scheme: scheme}, writes
}

func getCron(t *testing.T, r *GeassAppReconciler, trigger string) *batchv1.CronJob {
	t.Helper()
	var cron batchv1.CronJob
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: triggerCronJobName(functionApp("node", "x"), trigger), Namespace: sleepyNS}, &cron))
	return &cron
}

func TestEachTriggerBecomesACronJobThatCallsTheFunction(t *testing.T) {
	ctx := context.Background()
	r, _ := triggerReconciler(t)
	app := triggerApp(
		geassv1alpha1.GeassFunctionTrigger{Name: "nightly", Schedule: "0 3 * * *"},
		geassv1alpha1.GeassFunctionTrigger{Name: "sync", Schedule: "*/15 * * * *", Path: "/sync", Payload: `{"full":true}`},
	)
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))

	nightly := getCron(t, r, "nightly")
	require.Equal(t, "0 3 * * *", nightly.Spec.Schedule)
	require.Equal(t, "Etc/UTC", *nightly.Spec.TimeZone)
	require.Equal(t, batchv1.ForbidConcurrent, nightly.Spec.ConcurrencyPolicy)
	pod := nightly.Spec.JobTemplate.Spec.Template.Spec
	require.Equal(t, corev1.RestartPolicyNever, pod.RestartPolicy)
	require.False(t, *pod.AutomountServiceAccountToken)
	args := pod.Containers[0].Args
	require.Equal(t, "http://hello.payments-production.svc.cluster.local:9000/", args[len(args)-1])
	require.Contains(t, args, `{"name":"nightly","trigger":"cron"}`, "the default body says which trigger ran")
	require.Contains(t, args, "x-geass-trigger-token: $(GEASS_TRIGGER_TOKEN)", "the token is read from the environment, never written into the spec")
	container := pod.Containers[0]
	require.Equal(t, triggerSecretName(app), container.Env[0].ValueFrom.SecretKeyRef.Name)
	for _, arg := range args {
		require.NotContains(t, arg, "s3cr3t")
	}
	require.NotContains(t, args, "Host: hello.example.com")

	sync := getCron(t, r, "sync")
	args = sync.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Args
	require.Equal(t, "http://hello.payments-production.svc.cluster.local:9000/sync", args[len(args)-1])
	require.Contains(t, args, `{"full":true}`)
}

func TestATriggerWakesASleepingFunctionThroughTheProxy(t *testing.T) {
	ctx := context.Background()
	r, _ := triggerReconciler(t)
	app := triggerApp(geassv1alpha1.GeassFunctionTrigger{Name: "nightly", Schedule: "@daily"})
	plan := scaleToZeroPlan{requested: true, active: true, hosts: []string{"hello.example.com"}}
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, plan))

	args := getCron(t, r, "nightly").Spec.JobTemplate.Spec.Template.Spec.Containers[0].Args
	require.Equal(t, "http://"+platform.ServerlessInterceptorHost+":8080/", args[len(args)-1])
	require.Contains(t, args, "Host: hello.example.com", "the proxy finds the service by host")
}

func TestTriggersFollowTheProjectsNodeRequirements(t *testing.T) {
	ctx := context.Background()
	r, _ := triggerReconciler(t)
	require.NoError(t, r.Create(ctx, &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{RequiredTags: []string{"hipaa"}},
	}))
	app := triggerApp(geassv1alpha1.GeassFunctionTrigger{Name: "nightly", Schedule: "@daily"})
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))

	pod := getCron(t, r, "nightly").Spec.JobTemplate.Spec.Template.Spec
	require.Equal(t, "true", pod.NodeSelector[platform.TagKey("hipaa")], "a compliant project's schedule runs on its nodes too")
	require.NotEmpty(t, pod.Tolerations)
}

func TestTriggersAreNotWrittenAgainWhenNothingChanged(t *testing.T) {
	ctx := context.Background()
	r, writes := triggerReconciler(t)
	app := triggerApp(geassv1alpha1.GeassFunctionTrigger{Name: "nightly", Schedule: "@daily"})
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))
	require.NotEmpty(t, writes.writes)

	writes.reset()
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))
	require.Empty(t, writes.writes)

	app.Spec.Source.Function.Triggers[0].Schedule = "@hourly"
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))
	require.Equal(t, "@hourly", getCron(t, r, "nightly").Spec.Schedule)
}

func TestRemovedTriggersAreRemoved(t *testing.T) {
	ctx := context.Background()
	r, _ := triggerReconciler(t)
	app := triggerApp(
		geassv1alpha1.GeassFunctionTrigger{Name: "keep", Schedule: "@daily"},
		geassv1alpha1.GeassFunctionTrigger{Name: "drop", Schedule: "@daily"},
	)
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))
	other := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "someone-elses", Namespace: sleepyNS}}
	require.NoError(t, r.Create(ctx, other))

	app.Spec.Source.Function.Triggers = app.Spec.Source.Function.Triggers[:1]
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))
	require.Error(t, r.Get(ctx, client.ObjectKey{Name: triggerCronJobName(app, "drop"), Namespace: sleepyNS}, &batchv1.CronJob{}))
	getCron(t, r, "keep")
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "someone-elses", Namespace: sleepyNS}, &batchv1.CronJob{}))

	app.Spec.Source.Function = nil
	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))
	require.Error(t, r.Get(ctx, client.ObjectKey{Name: triggerCronJobName(app, "keep"), Namespace: sleepyNS}, &batchv1.CronJob{}), "a service that is no longer a function has no schedule")
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "someone-elses", Namespace: sleepyNS}, &batchv1.CronJob{}))
}

func TestLongTriggerNamesStayWithinWhatACronJobMayBe(t *testing.T) {
	app := triggerApp()
	app.Name = "a-service-with-a-rather-long-name"
	a := triggerCronJobName(app, "nightly-reconciliation-of-everything")
	b := triggerCronJobName(app, "nightly-reconciliation-of-everythinG")
	require.LessOrEqual(t, len(a), maxCronJobName)
	require.NotEqual(t, a, b, "two names that share a start do not collide")

	// Dashes in either part do not make two different schedules the same CronJob.
	first, second := functionApp("node", "x"), functionApp("node", "x")
	first.Name, second.Name = "a", "a-b"
	require.NotEqual(t, triggerCronJobName(first, "b-c"), triggerCronJobName(second, "c"))
}

func TestAnEventFunctionRunsItsShim(t *testing.T) {
	ctx := context.Background()
	r, _ := triggerReconciler(t)
	app := triggerApp()
	require.NoError(t, r.reconcileFunctionCode(ctx, app, sleepyNS))
	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS))

	var cm corev1.ConfigMap
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-function", Namespace: sleepyNS}, &cm))
	require.Contains(t, cm.Data, "index.mjs")
	require.Contains(t, cm.Data, "geass-shim.mjs")

	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	container := deploy.Spec.Template.Spec.Containers[0]
	require.Equal(t, []string{"node", "/function/geass-shim.mjs"}, container.Command)
	items := deploy.Spec.Template.Spec.Volumes[len(deploy.Spec.Template.Spec.Volumes)-1].ConfigMap.Items
	require.Len(t, items, 2, "both the code and the shim are mounted")
	before := deploy.Spec.Template.Annotations[platform.AnnotationFunctionHash]

	app.Spec.Source.Function.Mode = ""
	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS))
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	require.Equal(t, []string{"node", "/function/index.mjs"}, deploy.Spec.Template.Spec.Containers[0].Command)
	require.NotEqual(t, before, deploy.Spec.Template.Annotations[platform.AnnotationFunctionHash])
}

func TestAFunctionWithSchedulesGetsOneSecretTokenThatIsKept(t *testing.T) {
	ctx := context.Background()
	r, _ := triggerReconciler(t)
	app := triggerApp(geassv1alpha1.GeassFunctionTrigger{Name: "nightly", Schedule: "@daily"})
	require.NoError(t, r.reconcileFunctionCode(ctx, app, sleepyNS))

	var secret corev1.Secret
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-trigger", Namespace: sleepyNS}, &secret))
	token := string(secret.Data["token"])
	require.Len(t, token, 64)

	require.NoError(t, r.reconcileFunctionCode(ctx, app, sleepyNS))
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-trigger", Namespace: sleepyNS}, &secret))
	require.Equal(t, token, string(secret.Data["token"]), "the pods and the CronJobs keep agreeing on it")

	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS))
	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	var found bool
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "GEASS_TRIGGER_TOKEN" {
			found = true
			require.Equal(t, "hello-trigger", env.ValueFrom.SecretKeyRef.Name)
		}
	}
	require.True(t, found, "the function's pods have it, so its shim can check it")
}

func TestAPlainHTTPFunctionHasNoToken(t *testing.T) {
	ctx := context.Background()
	r, _ := triggerReconciler(t)
	app := functionApp("node", "x")
	require.NoError(t, r.reconcileFunctionCode(ctx, app, sleepyNS))
	require.Error(t, r.Get(ctx, client.ObjectKey{Name: "hello-trigger", Namespace: sleepyNS}, &corev1.Secret{}))
	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS))
	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		require.NotEqual(t, "GEASS_TRIGGER_TOKEN", env.Name, "existing functions are not changed, so they do not restart")
	}

	// Once it has a schedule it gets one, and when it stops being one that needs it the token goes.
	app.Spec.Source.Function.Triggers = []geassv1alpha1.GeassFunctionTrigger{{Name: "n", Schedule: "@daily"}}
	require.NoError(t, r.reconcileFunctionCode(ctx, app, sleepyNS))
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-trigger", Namespace: sleepyNS}, &corev1.Secret{}))
	app.Spec.Source.Function.Triggers = nil
	require.NoError(t, r.reconcileFunctionCode(ctx, app, sleepyNS))
	require.Error(t, r.Get(ctx, client.ObjectKey{Name: "hello-trigger", Namespace: sleepyNS}, &corev1.Secret{}))
}

func TestSchedulesAreRoutedByTheProxyOnlyWhenTheirPathIs(t *testing.T) {
	app := sleepyApp()
	app.Spec.Ingress = geassv1alpha1.GeassAppIngressSpec{Host: "hello.example.com", Path: "/api"}
	app.Spec.Source.Function.Triggers = []geassv1alpha1.GeassFunctionTrigger{{Name: "sync", Schedule: "@daily", Path: "/sync"}, {Name: "root", Schedule: "@daily"}}
	require.ElementsMatch(t, []string{"/api", "/sync", "/"}, scaleToZeroPrefixes(app), "the proxy has a route for every path a schedule calls")
}

func TestAServiceThatNeverHadSchedulesCostsNoDeletes(t *testing.T) {
	ctx := context.Background()
	r, writes := triggerReconciler(t)
	app := functionApp("node", "x")
	app.Spec.Source.Function = nil
	app.Spec.Source.Image = &geassv1alpha1.GeassAppImageSource{Image: "nginx"}
	for range 3 {
		require.NoError(t, r.reconcileFunctionCode(ctx, app, sleepyNS))
		require.NoError(t, r.reconcileFunctionTriggers(ctx, app, sleepyNS, scaleToZeroPlan{}))
	}
	require.Empty(t, writes.writes, "the cache says there is nothing to remove, so nothing is asked of the API server")
}

func TestARecreatedTokenRollsTheFunctionPods(t *testing.T) {
	ctx := context.Background()
	r, _ := triggerReconciler(t)
	app := triggerApp(geassv1alpha1.GeassFunctionTrigger{Name: "nightly", Schedule: "@daily"})
	require.NoError(t, r.reconcileFunctionCode(ctx, app, sleepyNS))
	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS))
	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	before := deploy.Spec.Template.Annotations[annotationTriggerTokenFingerprint]
	require.NotEmpty(t, before)

	var secret corev1.Secret
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-trigger", Namespace: sleepyNS}, &secret))
	secret.Data["token"] = []byte("a-token-that-was-made-again")
	require.NoError(t, r.Update(ctx, &secret))
	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS))
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	require.NotEqual(t, before, deploy.Spec.Template.Annotations[annotationTriggerTokenFingerprint], "so the pods and the schedules go on agreeing on it")
}

func TestAnOrdinaryServiceThatCannotStartFailsToo(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	app.Spec.ScaleToZero = nil
	require.NoError(t, r.Create(ctx, app))
	plan, deploy := deployAsleep(t, r, app)
	require.False(t, plan.active)
	require.NoError(t, r.Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "hello-abc", Namespace: sleepyNS, Labels: deploy.Spec.Selector.MatchLabels},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "pull access denied, dial tcp"}}}}},
	}))

	settled, _, err := r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
	require.NoError(t, err)
	require.True(t, settled)
	var latest geassv1alpha1.GeassApp
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(app), &latest))
	require.Contains(t, meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionReady).Message, "The service cannot start: app is ImagePullBackOff")
	require.Equal(t, "The service cannot start: app is ImagePullBackOff", platform.SanitizeProvisionMessage("The service cannot start: app is ImagePullBackOff"),
		"a start failure is not taken for a fault of the platform, whatever words it has in it")
	require.Contains(t, platform.SanitizeProvisionMessage("The service cannot start: access denied"), "cannot start")
}

func TestOnlyTheScaleChangesOfADeploymentWakeItsService(t *testing.T) {
	two, three := int32(2), int32(3)
	deploy := func(replicas int32, available int32) *appsv1.Deployment {
		d := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: &replicas}}
		d.Status.AvailableReplicas = available
		d.Status.Replicas = replicas
		return d
	}
	predicate := deploymentScaleChanged()
	update := func(a, b *appsv1.Deployment) bool {
		return predicate.Update(event.UpdateEvent{ObjectOld: a, ObjectNew: b})
	}
	require.True(t, update(deploy(two, 2), deploy(three, 2)), "copies wanted")
	require.True(t, update(deploy(two, 1), deploy(two, 2)), "copies up")
	require.False(t, update(deploy(two, 2), deploy(two, 2)), "nothing about the scale")

	app := sleepyApp()
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: geassResourceLabels(app, "GeassApp")}}
	requests := appForDeployment(context.Background(), d)
	require.Len(t, requests, 1)
	require.Equal(t, "hello", requests[0].Name)
	require.Empty(t, appForDeployment(context.Background(), &appsv1.Deployment{}), "a Deployment that is not a service's is not mapped")
}
