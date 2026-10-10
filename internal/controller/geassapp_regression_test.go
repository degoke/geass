package controller

import (
	"context"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// clusterWithoutPrometheusOperator behaves like an API server that has no ServiceMonitor kind.
func clusterWithoutPrometheusOperator(t *testing.T) *GeassAppReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	noMatch := &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "monitoring.coreos.com", Kind: "ServiceMonitor"}, SearchedVersions: []string{"v1"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, isServiceMonitor := obj.(*monitoringv1.ServiceMonitor); isServiceMonitor {
				return noMatch
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
	return &GeassAppReconciler{Client: c, Scheme: scheme}
}

func TestServiceMonitorIsSkippedWhenMetricsAreOffAndOperatorIsMissing(t *testing.T) {
	r := clusterWithoutPrometheusOperator(t)
	app := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "geass-system"}}

	require.NoError(t, r.reconcileServiceMonitor(context.Background(), app, "proj-test-production"),
		"a cluster without Prometheus Operator must not fail apps that do not use metrics")
}

func TestServiceMonitorExplainsMissingOperatorWhenMetricsAreOn(t *testing.T) {
	r := clusterWithoutPrometheusOperator(t)
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "geass-system"},
		Spec:       geassv1alpha1.GeassAppSpec{Metrics: geassv1alpha1.GeassAppMetricsSpec{Enabled: true}},
	}

	err := r.reconcileServiceMonitor(context.Background(), app, "proj-test-production")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Prometheus Operator is not installed")
}

func TestDeletingAServiceRemovesItsDeploymentHistory(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	now := metav1.Now()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "geass-system", Finalizers: []string{appFinalizer}, DeletionTimestamp: &now},
		Spec:       geassv1alpha1.GeassAppSpec{Project: "payments", Environment: "production"},
	}
	record := func(name, appName string) *geassv1alpha1.GeassDeployment {
		return &geassv1alpha1.GeassDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "geass-system", Labels: map[string]string{platform.LabelApp: appName}},
			Spec:       geassv1alpha1.GeassDeploymentSpec{App: appName, Project: "payments", Environment: "production"},
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(app, record("web-1", "web"), record("web-2", "web"), record("worker-1", "worker")).
		WithStatusSubresource(&geassv1alpha1.GeassApp{}).Build()
	r := &GeassAppReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reconcileRequest(app))
	require.NoError(t, err)

	var remaining geassv1alpha1.GeassDeploymentList
	require.NoError(t, c.List(context.Background(), &remaining, client.InNamespace("geass-system")))
	require.Len(t, remaining.Items, 1, "only the other service's history may remain")
	require.Equal(t, "worker-1", remaining.Items[0].Name)
}
