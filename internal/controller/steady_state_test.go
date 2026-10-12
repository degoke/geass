package controller

import (
	"context"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// writeLog records every write sent to the API server, of any kind, so a test can say what a reconcile did.
type writeLog struct{ writes []string }

func (w *writeLog) record(verb string, obj client.Object) {
	w.writes = append(w.writes, verb+" "+obj.GetObjectKind().GroupVersionKind().Kind+"/"+obj.GetName())
}

func (w *writeLog) reset() { w.writes = nil }

// steadyStateReconciler is a ready cluster and project with one deployed service, and a client that logs
// the writes made to it.
func steadyStateReconciler(t *testing.T) (*GeassAppReconciler, *geassv1alpha1.GeassApp, *writeLog, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, autoscalingv2.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	ready := []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{"production"}},
		Status:     geassv1alpha1.GeassProjectStatus{Conditions: ready},
	}
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Status:     geassv1alpha1.GeassClusterStatus{Conditions: ready},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("16Gi")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace, Finalizers: []string{appFinalizer}},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "shop", Environment: "production", Port: 8080,
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx:alpine"}},
		},
	}
	log := &writeLog{}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, cluster, node, app).
		WithStatusSubresource(&geassv1alpha1.GeassApp{}, &geassv1alpha1.GeassDeployment{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				log.record("create", obj)
				return c.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				log.record("update", obj)
				return c.Update(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				log.record("patch", obj)
				return c.Patch(ctx, obj, patch, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				log.record("update "+sub+" of", obj)
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				log.record("patch "+sub+" of", obj)
				return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	return &GeassAppReconciler{Client: c, Scheme: scheme}, app, log, c
}

// markDeploymentAvailable plays the part of the Deployment controller, which a fake client has none of.
func markDeploymentAvailable(t *testing.T, c client.Client) {
	t.Helper()
	var deploy appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: "shop-production"}, &deploy))
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas, deploy.Status.ReadyReplicas, deploy.Status.AvailableReplicas, deploy.Status.UpdatedReplicas = 1, 1, 1, 1
	require.NoError(t, c.Status().Update(context.Background(), &deploy))
}

func reconcileApp(t *testing.T, r *GeassAppReconciler, app *geassv1alpha1.GeassApp) {
	t.Helper()
	_, err := r.Reconcile(context.Background(), reconcileRequest(app))
	require.NoError(t, err)
}

// A service that is up and unchanged must cost the API server nothing: no writes of any kind, status
// included. This is what keeps the operator from rewriting every object it owns, every time anything wakes it.
func TestAReadyServiceIsNotWrittenToAgain(t *testing.T) {
	r, app, log, c := steadyStateReconciler(t)

	reconcileApp(t, r, app) // makes everything
	markDeploymentAvailable(t, c)
	reconcileApp(t, r, app) // notices it is up, and says so
	var stored geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(app), &stored))
	require.Equal(t, metav1.ConditionTrue, conditionOf(stored.Status.Conditions, platform.ConditionReady), "the service is ready: %v", stored.Status.Conditions)
	log.reset()

	for range 3 {
		reconcileApp(t, r, app)
	}

	require.Empty(t, log.writes, "three more reconciles of an unchanged, ready service wrote: %v", log.writes)
}

func conditionOf(conditions []metav1.Condition, conditionType string) metav1.ConditionStatus {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status
		}
	}
	return ""
}
