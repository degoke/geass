package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const sleepyNS = "payments-production"

func sleepyApp() *geassv1alpha1.GeassApp {
	app := functionApp("node", "console.log(1)")
	app.Spec.Ingress.Host = "hello.example.com"
	app.Spec.ScaleToZero = &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, IdleSeconds: 120, MaxReplicas: 4}
	return app
}

// sleepyReconciler has the add-on's resource served, or not served when installed is false.
func sleepyReconciler(t *testing.T, installed bool) *GeassAppReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&geassv1alpha1.GeassApp{})
	if !installed {
		builder = builder.WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if u, ok := list.(*unstructured.UnstructuredList); ok && u.GroupVersionKind().Group == "http.keda.sh" {
					return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "http.keda.sh", Kind: "HTTPScaledObject"}}
				}
				return c.List(ctx, list, opts...)
			},
		})
	}
	return &GeassAppReconciler{Client: builder.Build(), Scheme: scheme}
}

func getScaled(t *testing.T, r *GeassAppReconciler, name string) *unstructured.Unstructured {
	t.Helper()
	scaled := &unstructured.Unstructured{}
	scaled.SetGroupVersionKind(httpScaledObjectKind)
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: name, Namespace: sleepyNS}, scaled))
	return scaled
}

func TestScaleToZeroRoutesThroughTheWakeProxy(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()

	plan, err := r.planScaleToZero(ctx, app)
	require.NoError(t, err)
	require.True(t, plan.active)
	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS, withScaleToZero(plan)))
	require.NoError(t, r.reconcileScaleToZero(ctx, app, sleepyNS, &plan))
	require.NoError(t, r.reconcileIngress(ctx, app, sleepyNS, withScaleToZero(plan)))

	scaled := getScaled(t, r, "hello")
	spec, _, _ := unstructured.NestedMap(scaled.Object, "spec")
	require.Equal(t, []any{"hello.example.com"}, spec["hosts"])
	require.Equal(t, int64(120), spec["scaledownPeriod"])
	require.Equal(t, map[string]any{"min": int64(1), "max": int64(4)}, spec["replicas"], "held at one copy until this version has been seen to start")
	require.Equal(t, map[string]any{"name": "hello", "kind": "Deployment", "apiVersion": "apps/v1", "service": "hello", "port": int64(9000)}, spec["scaleTargetRef"])

	var wake corev1.Service
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-wake", Namespace: sleepyNS}, &wake))
	require.Equal(t, corev1.ServiceTypeExternalName, wake.Spec.Type)
	require.Equal(t, platform.ServerlessInterceptorHost, wake.Spec.ExternalName)

	var ing networkingv1.Ingress
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &ing))
	backend := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service
	require.Equal(t, "hello-wake", backend.Name)
	require.EqualValues(t, platform.ServerlessInterceptorPort, backend.Port.Number)
}

func TestScaleToZeroLeavesTheReplicaCountToTheAddon(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	plan, err := r.planScaleToZero(ctx, app)
	require.NoError(t, err)
	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS, withScaleToZero(plan)))

	// The add-on puts the idle service to sleep; a later reconcile must not wake it.
	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	zero := int32(0)
	deploy.Spec.Replicas = &zero
	require.NoError(t, r.Update(ctx, &deploy))

	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS, withScaleToZero(plan)))
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	require.Equal(t, int32(0), *deploy.Spec.Replicas)
	require.Equal(t, int32(0), effectiveReplicas(app, &deploy, plan), "a sleeping service is ready, not starting")
}

func TestScaleToZeroFallsBackToAlwaysOn(t *testing.T) {
	ctx := context.Background()
	t.Run("the add-on is not installed", func(t *testing.T) {
		r := sleepyReconciler(t, false)
		plan, err := r.planScaleToZero(ctx, sleepyApp())
		require.NoError(t, err)
		require.False(t, plan.active)
		require.Equal(t, reasonScaleToZeroNeedsAddon, plan.reason)

		app := sleepyApp()
		require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS, withScaleToZero(plan)))
		var deploy appsv1.Deployment
		require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
		require.NotNil(t, deploy.Spec.Replicas, "it runs as it did before")
		require.NoError(t, r.reconcileScaleToZero(ctx, app, sleepyNS, &plan), "nothing to remove is not an error")
	})
	t.Run("it has no public host", func(t *testing.T) {
		r := sleepyReconciler(t, true)
		app := sleepyApp()
		app.Spec.Ingress.Host = ""
		plan, err := r.planScaleToZero(ctx, app)
		require.NoError(t, err)
		require.Equal(t, reasonScaleToZeroNeedsHost, plan.reason)
	})
	t.Run("it also asks for CPU autoscaling", func(t *testing.T) {
		r := sleepyReconciler(t, true)
		app := sleepyApp()
		app.Spec.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{}
		plan, err := r.planScaleToZero(ctx, app)
		require.NoError(t, err)
		require.Equal(t, reasonScaleToZeroUsesAutoscaler, plan.reason)
	})
}

func TestTurningScaleToZeroOffRemovesWhatRoutesThroughTheProxy(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	plan, err := r.planScaleToZero(ctx, app)
	require.NoError(t, err)
	require.NoError(t, r.reconcileScaleToZero(ctx, app, sleepyNS, &plan))

	app.Spec.ScaleToZero.Enabled = false
	plan, err = r.planScaleToZero(ctx, app)
	require.NoError(t, err)
	require.NoError(t, r.reconcileScaleToZero(ctx, app, sleepyNS, &plan))

	scaled := &unstructured.Unstructured{}
	scaled.SetGroupVersionKind(httpScaledObjectKind)
	require.Error(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, scaled))
	require.Error(t, r.Get(ctx, client.ObjectKey{Name: "hello-wake", Namespace: sleepyNS}, &corev1.Service{}))

	// And the ingress goes straight to the service again.
	require.NoError(t, r.reconcileIngress(ctx, app, sleepyNS, withScaleToZero(plan)))
	var ing networkingv1.Ingress
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &ing))
	require.Equal(t, "hello", ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name)
}

func TestScaleToZeroCondition(t *testing.T) {
	require.Empty(t, scaleToZeroCondition(scaleToZeroPlan{}, []metav1.Condition{{Type: conditionScaleToZero}}), "a service that never asked shows nothing")
	active := scaleToZeroCondition(scaleToZeroPlan{requested: true, active: true, reason: reasonScaleToZeroActive}, nil)
	require.Equal(t, metav1.ConditionTrue, active[0].Status)
	blocked := scaleToZeroCondition(scaleToZeroPlan{requested: true, reason: reasonScaleToZeroNeedsAddon, message: "m"}, nil)
	require.Equal(t, metav1.ConditionFalse, blocked[0].Status)
	require.Equal(t, reasonScaleToZeroNeedsAddon, blocked[0].Reason)
}

func TestTunnelSendsASleepingServiceToTheWakeProxy(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	app := sleepyApp()
	r := &GeassPlatformConfigReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}

	// Decided from the service's settings, not from the proxy's object for it, which is made a moment later.
	require.True(t, r.sleepsWhenIdle(ctx, app))
	app.Spec.ScaleToZero.Enabled = false
	require.False(t, r.sleepsWhenIdle(ctx, app), "a service that stopped asking goes direct again")
	app.Spec.ScaleToZero.Enabled = true
	app.Spec.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{}
	require.False(t, r.sleepsWhenIdle(ctx, app), "one that also autoscales is not put to sleep, so it goes direct")

	missing := &GeassPlatformConfigReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "http.keda.sh", Kind: "HTTPScaledObject"}}
		},
	}).Build(), Scheme: scheme}
	require.False(t, missing.sleepsWhenIdle(ctx, sleepyApp()), "without the add-on there is no proxy to send it to")
}

// deployAsleep reconciles a sleeping service and returns what it needs to be driven from a test: its plan and
// its Deployment, which the test can give a status.
func deployAsleep(t *testing.T, r *GeassAppReconciler, app *geassv1alpha1.GeassApp) (scaleToZeroPlan, *appsv1.Deployment) {
	t.Helper()
	ctx := context.Background()
	plan, err := r.planScaleToZero(ctx, app)
	require.NoError(t, err)
	require.NoError(t, r.reconcileDeployment(ctx, app, sleepyNS, withScaleToZero(plan)))
	require.NoError(t, r.reconcileScaleToZero(ctx, app, sleepyNS, &plan))
	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, &deploy))
	return plan, &deploy
}

func scaledMin(t *testing.T, r *GeassAppReconciler) int64 {
	t.Helper()
	spec, _, _ := unstructured.NestedMap(getScaled(t, r, "hello").Object, "spec")
	return spec["replicas"].(map[string]any)["min"].(int64)
}

func startedCopy(t *testing.T, r *GeassAppReconciler, deploy *appsv1.Deployment) {
	t.Helper()
	deploy.Status = appsv1.DeploymentStatus{ObservedGeneration: deploy.Generation, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1, ReadyReplicas: 1}
	require.NoError(t, r.Status().Update(context.Background(), deploy))
}

func TestANewVersionIsHeldAtOneCopyUntilItHasStarted(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	require.NoError(t, r.Create(ctx, app))

	plan, deploy := deployAsleep(t, r, app)
	require.True(t, plan.verifying, "a version that has never started has not been seen to")
	require.EqualValues(t, 1, scaledMin(t, r), "the add-on may not stop it while that is so")
	require.EqualValues(t, 1, effectiveReplicas(app, deploy, plan), "so it is not ready until a copy is up")
	require.False(t, deploymentVerified(deploy))

	// While it is not up and nothing is wrong, it is left to start.
	settled, _, err := r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
	require.NoError(t, err)
	require.False(t, settled)
	require.EqualValues(t, 1, scaledMin(t, r))

	// Once a copy of this version is up, it is verified and the add-on may stop it when idle.
	startedCopy(t, r, deploy)
	settled, _, err = r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
	require.NoError(t, err)
	require.False(t, settled)
	require.EqualValues(t, 0, scaledMin(t, r))
	plan, deploy = deployAsleep(t, r, app)
	require.False(t, plan.verifying)
	require.EqualValues(t, 0, scaledMin(t, r), "a reconcile that changes nothing does not hold it up again")

	// Asleep, with no copies, it is ready: the version was verified.
	zero := int32(0)
	deploy.Spec.Replicas = &zero
	require.NoError(t, r.Update(ctx, deploy))
	deploy.Status = appsv1.DeploymentStatus{}
	require.NoError(t, r.Status().Update(ctx, deploy))
	require.Zero(t, effectiveReplicas(app, deploy, plan))

	// A new version has to be seen to start too.
	app.Spec.Port = 9100
	app.Spec.Env = append(app.Spec.Env, corev1.EnvVar{Name: "CHANGED", Value: "yes"})
	plan, deploy = deployAsleep(t, r, app)
	require.True(t, plan.verifying, "a changed version is checked again")
	require.EqualValues(t, 1, scaledMin(t, r))
	require.EqualValues(t, 1, effectiveReplicas(app, deploy, plan), "even though the old one was asleep")
}

func TestAVersionThatNeverStartsIsNotVerified(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	plan, deploy := deployAsleep(t, r, app)

	// An old copy that is up while the new version's copy is not does not count for the new version.
	deploy.Status = appsv1.DeploymentStatus{ObservedGeneration: deploy.Generation, Replicas: 2, UpdatedReplicas: 1, AvailableReplicas: 1}
	require.False(t, deploymentVerified(deploy))
	settled, _, err := r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
	require.NoError(t, err)
	require.False(t, settled)
	require.EqualValues(t, 1, scaledMin(t, r), "it stays held, and the deploy fails the way any service that cannot start does")
}

func TestAWokenServiceThatCannotStartIsReportedAndNotLeftWaking(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	require.NoError(t, r.Create(ctx, app))
	plan, deploy := deployAsleep(t, r, app)
	startedCopy(t, r, deploy)
	_, _, err := r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
	require.NoError(t, err)
	plan, deploy = deployAsleep(t, r, app)
	require.False(t, plan.verifying)

	// A request wakes it, and its copy is crash-looping.
	one := int32(1)
	deploy.Spec.Replicas = &one
	require.NoError(t, r.Update(ctx, deploy))
	deploy.Status = appsv1.DeploymentStatus{ObservedGeneration: deploy.Generation}
	require.NoError(t, r.Status().Update(ctx, deploy))
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: sleepyNS}, deploy))
	selector := deploy.Spec.Selector.MatchLabels
	require.NotEmpty(t, selector)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "hello-abc", Namespace: sleepyNS, Labels: selector},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "back-off 40s restarting failed container"}}}}},
	}
	require.NoError(t, r.Create(ctx, pod))

	settled, _, err := r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
	require.NoError(t, err)
	require.True(t, settled)
	var latest geassv1alpha1.GeassApp
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(app), &latest))
	ready := meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Contains(t, ready.Message, "CrashLoopBackOff", "the failure says what is wrong, instead of waking for ever")
	require.Equal(t, int32(1), latest.Status.ReconcileRetry.ReconcileFailures, "and it counts, so it ends as a failed deploy like any other")

	// A copy that is only slow (still pulling) is waking, not failed.
	require.NoError(t, r.Delete(ctx, pod))
	pulling := pod.DeepCopy()
	pulling.ResourceVersion = ""
	pulling.Status.ContainerStatuses[0].State.Waiting = &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}
	require.NoError(t, r.Create(ctx, pulling))
	require.Empty(t, r.wakeProblem(ctx, &latest, sleepyNS, deploy))
	_, _, err = r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
	require.NoError(t, err)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(app), &latest))
	require.Equal(t, reasonWaking, platform.ReadyConditionReason(latest.Status.Conditions))
}

func TestAServiceThatStaysWakingForTooLongHasFailedToStart(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	one := int32(1)
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "hello", Namespace: sleepyNS}, Spec: appsv1.DeploymentSpec{Replicas: &one}}
	require.Empty(t, r.wakeProblem(ctx, app, sleepyNS, deploy))

	app.Status.Conditions = []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionFalse, Reason: reasonWaking,
		LastTransitionTime: metav1.NewTime(time.Now().Add(-wakeDeadline - time.Minute))}}
	require.Contains(t, r.wakeProblem(ctx, app, sleepyNS, deploy), "has not started within")
}

func TestAWakingServiceIsNotAFailedDeploy(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	app.Status.ReconcileRetry = geassv1alpha1.GeassReconcileRetryStatus{}
	require.NoError(t, r.Create(ctx, app))

	for range 10 {
		result, err := r.setWaking(ctx, app)
		require.NoError(t, err)
		require.Equal(t, wakingRequeue, result.RequeueAfter)
	}
	var latest geassv1alpha1.GeassApp
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(app), &latest))
	require.Equal(t, "Waking", platform.ReadyConditionReason(latest.Status.Conditions))
	require.Zero(t, latest.Status.ReconcileRetry.ReconcileFailures, "however long it takes, it does not run out of retries and fail")
}

func TestAVersionThatCannotStartFailsTheDeployInsteadOfWaitingForAVisitor(t *testing.T) {
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := sleepyApp()
	require.NoError(t, r.Create(ctx, app))
	plan, deploy := deployAsleep(t, r, app)
	require.True(t, plan.verifying)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "hello-abc", Namespace: sleepyNS, Labels: deploy.Spec.Selector.MatchLabels},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "pull access denied, repository does not exist"}}}}},
	}
	require.NoError(t, r.Create(ctx, pod))

	settled, _, err := r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
	require.NoError(t, err)
	require.True(t, settled)
	var latest geassv1alpha1.GeassApp
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(app), &latest))
	ready := meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionReady)
	require.Contains(t, ready.Message, "The new version of the service cannot start")
	require.Contains(t, ready.Message, "ImagePullBackOff", "and it says why")
	require.EqualValues(t, 1, latest.Status.ReconcileRetry.ReconcileFailures, "it counts, like any failure to deploy")

	// Kept up for as long as retries last, it ends as a failed deploy.
	for range platform.ReconcileFailureThreshold {
		_, _, err = r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
		require.NoError(t, err)
	}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(app), &latest))
	require.Equal(t, platform.ConditionReasonProvisionFailed, platform.ReadyConditionReason(latest.Status.Conditions))
	require.EqualValues(t, 1, scaledMin(t, r), "and it stays held at one copy")

	// The Deployment giving up on it is the same.
	require.NoError(t, r.Delete(ctx, pod))
	deploy.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
	require.Contains(t, r.startProblem(ctx, sleepyNS, deploy), "did not become ready in time")
}
