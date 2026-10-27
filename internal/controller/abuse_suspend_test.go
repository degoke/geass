package controller

import (
	"context"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/abuse"
	"github.com/degoke/geass/pkg/platform"
)

// suspendableAppReconciler is sandboxAppReconciler with the status of the deploy records too, so that a reconcile goes
// all the way to the service's Ready condition.
func suspendableAppReconciler(t *testing.T, objects ...client.Object) *GeassAppReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassApp{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassCluster{}, &geassv1alpha1.GeassDeployment{}).Build()
	return &GeassAppReconciler{Client: c, Scheme: scheme, Recorder: events.NewFakeRecorder(10)}
}

// suspendTenantService records a suspicion on the tenant service, suspended or not, as the detector or the dashboard would.
func suspendTenantService(t *testing.T, r *GeassAppReconciler, suspended bool) {
	t.Helper()
	_, err := abuse.Annotate(context.Background(), r.Client, "hello", func(app *geassv1alpha1.GeassApp) bool {
		app.Annotations[abuse.AnnotationSuspect] = abuse.Suspect{Source: abuse.SourceReport, Reporter: "falco", Suspended: suspended}.Encode()
		return true
	})
	require.NoError(t, err)
}

func clearTenantService(t *testing.T, r *GeassAppReconciler) {
	t.Helper()
	_, err := abuse.Annotate(context.Background(), r.Client, "hello", func(app *geassv1alpha1.GeassApp) bool {
		delete(app.Annotations, abuse.AnnotationSuspect)
		return true
	})
	require.NoError(t, err)
}

func TestASuspendedServiceIsScaledToZeroAndGivenItsCopiesBackWithNothingDeleted(t *testing.T) {
	ctx := context.Background()
	service := tenantService()
	service.Spec.Replicas = utilptr.To(int32(3))
	service.Spec.ConfigData = map[string]string{"MODE": "batch"}
	r := suspendableAppReconciler(t, append(readyTenantProject(tenantOrg), service)...)
	reconcileTenantService(t, r)
	before, made := tenantDeployment(t, r)
	require.True(t, made)
	require.Equal(t, int32(3), *before.Spec.Replicas)

	suspendTenantService(t, r, true)
	reconcileTenantService(t, r)
	suspended, _ := tenantDeployment(t, r)
	require.Equal(t, int32(0), *suspended.Spec.Replicas)
	require.Equal(t, before.Spec.Template, suspended.Spec.Template, "only the number of copies changes")
	ready := meta.FindStatusCondition(storedService(t, r).Status.Conditions, platform.ConditionReady)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, abuse.ReasonSuspended, ready.Reason, "said as a suspension, not as a failure: %s", ready.Message)
	require.Zero(t, storedService(t, r).Status.ReconcileRetry.ReconcileFailures, "and not counted as one")
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: tenantNS}, &corev1.Service{}), "the Service stays")
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-config", Namespace: tenantNS}, &corev1.ConfigMap{}), "and so does its configuration")

	suspendTenantService(t, r, false)
	reconcileTenantService(t, r)
	released, _ := tenantDeployment(t, r)
	require.Equal(t, int32(3), *released.Spec.Replicas, "a release lets it run, though it is still suspected")

	suspendTenantService(t, r, true)
	reconcileTenantService(t, r)
	clearTenantService(t, r)
	reconcileTenantService(t, r)
	cleared, _ := tenantDeployment(t, r)
	require.Equal(t, int32(3), *cleared.Spec.Replicas)
	ready = meta.FindStatusCondition(storedService(t, r).Status.Conditions, platform.ConditionReady)
	require.NotEqual(t, abuse.ReasonSuspended, ready.Reason)
	require.Equal(t, messageServiceStarting, ready.Message, "its copies start again (a fake API server never makes them ready)")
}

func TestASuspendedServiceDoesNotWakeAndItsAutoscalerStaysToFindItAtZero(t *testing.T) {
	ctx := context.Background()
	asleep := tenantService()
	asleep.Spec.Ingress.Host = "hello.example.com"
	asleep.Spec.ScaleToZero = &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true}
	r := suspendableAppReconciler(t, append(readyTenantProject(tenantOrg), asleep)...)
	suspendTenantService(t, r, true)
	reconcileTenantService(t, r)
	deploy, _ := tenantDeployment(t, r)
	require.Equal(t, int32(0), *deploy.Spec.Replicas, "set by Geass, not left to the scale-to-zero add-on, which a request would wake")
	scaleToZero := meta.FindStatusCondition(storedService(t, r).Status.Conditions, conditionScaleToZero)
	require.Equal(t, metav1.ConditionFalse, scaleToZero.Status)
	require.Equal(t, reasonScaleToZeroSuspended, scaleToZero.Reason)

	autoscaled := tenantService()
	autoscaled.Spec.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{MaxReplicas: 5, TargetCPUUtilization: utilptr.To(int32(80))}
	r = suspendableAppReconciler(t, append(readyTenantProject(tenantOrg), autoscaled)...)
	suspendTenantService(t, r, true)
	reconcileTenantService(t, r)
	deploy, _ = tenantDeployment(t, r)
	require.Equal(t, int32(0), *deploy.Spec.Replicas)
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: tenantNS}, &autoscalingv2.HorizontalPodAutoscaler{}),
		"the autoscaler is kept: Kubernetes does not scale a Deployment that is at zero")
}

func TestAServiceWithoutASuspicionIsWrittenAsBefore(t *testing.T) {
	r := suspendableAppReconciler(t, append(readyTenantProject(tenantOrg), tenantService())...)
	reconcileTenantService(t, r)
	before, _ := tenantDeployment(t, r)
	_, err := abuse.Annotate(context.Background(), r.Client, "hello", func(app *geassv1alpha1.GeassApp) bool {
		app.Annotations[abuse.AnnotationReview] = abuse.Review{Until: abuseStart}.Encode()
		return true
	})
	require.NoError(t, err)
	reconcileTenantService(t, r)
	after, _ := tenantDeployment(t, r)
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "a review changes nothing of how the service runs")
}
