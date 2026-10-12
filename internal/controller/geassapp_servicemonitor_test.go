package controller

import (
	"context"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func metricsApp(name string) *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "geass-system"},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "payments", Environment: "production", Port: 8080,
			Metrics: geassv1alpha1.GeassAppMetricsSpec{Enabled: true},
		},
	}
}

// The ServiceMonitor must match the Service the operator really creates. Matching on the pod labels
// (which are not on the Service) selected nothing, so no app was ever scraped.
func TestTheServiceMonitorSelectsTheAppsOwnServiceAndNoOtherApps(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	r := &GeassAppReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
	ctx := context.Background()
	const namespace = "payments-production"

	web, worker := metricsApp("web"), metricsApp("worker")
	for _, app := range []*geassv1alpha1.GeassApp{web, worker} {
		require.NoError(t, r.reconcileService(ctx, app, namespace))
	}
	require.NoError(t, r.reconcileServiceMonitor(ctx, web, namespace))

	var monitor monitoringv1.ServiceMonitor
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "web-metrics", Namespace: namespace}, &monitor))
	selector, err := metav1.LabelSelectorAsSelector(&monitor.Spec.Selector)
	require.NoError(t, err)

	var webService, workerService corev1.Service
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "web", Namespace: namespace}, &webService))
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "worker", Namespace: namespace}, &workerService))
	require.True(t, selector.Matches(labels.Set(webService.Labels)), "the app's own Service is scraped; labels were %v", webService.Labels)
	require.False(t, selector.Matches(labels.Set(workerService.Labels)), "another app in the same project is not scraped by this monitor")
	require.Equal(t, "http", monitor.Spec.Endpoints[0].Port, "the Service's named port")
}

func TestTheServiceMonitorFollowsTheMetricsPathAndPort(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	r := &GeassAppReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
	app := metricsApp("web")
	app.Spec.Metrics.Path = "/internal/metrics"

	require.NoError(t, r.reconcileServiceMonitor(context.Background(), app, "payments-production"))

	var monitor monitoringv1.ServiceMonitor
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: "web-metrics", Namespace: "payments-production"}, &monitor))
	require.Equal(t, "/internal/metrics", monitor.Spec.Endpoints[0].Path)
}
