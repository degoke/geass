package controller

import (
	"context"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func ingressMetricsClient(t *testing.T, withOperator bool) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if !withOperator {
		noMatch := &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "monitoring.coreos.com", Kind: "PodMonitor"}, SearchedVersions: []string{"v1"}}
		builder = builder.WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*monitoringv1.PodMonitor); ok {
					return noMatch
				}
				return c.Get(ctx, key, obj, opts...)
			},
		})
	}
	return builder.Build()
}

func TestMonitoringScrapesTheIngressControllerSoServicesHaveARequestRate(t *testing.T) {
	c := ingressMetricsClient(t, true)
	r := &GeassClusterReconciler{Client: c}

	require.Zero(t, r.reconcileIngressMetrics(context.Background(), telemetryCluster(true, false)))

	var monitor monitoringv1.PodMonitor
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: ingressMetricsMonitorName, Namespace: platform.MonitoringTargetNS}, &monitor))
	require.Equal(t, []string{"kube-system"}, monitor.Spec.NamespaceSelector.MatchNames, "Traefik lives in kube-system")
	require.Equal(t, map[string]string{"app.kubernetes.io/name": "traefik"}, monitor.Spec.Selector.MatchLabels)
	require.Equal(t, "metrics", *monitor.Spec.PodMetricsEndpoints[0].Port, "the port Traefik names for its metrics")

	require.Zero(t, r.reconcileIngressMetrics(context.Background(), telemetryCluster(true, false)), "doing it again is not an error")
}

func TestIngressMetricsAreRetriedUntilThePrometheusOperatorIsInstalled(t *testing.T) {
	r := &GeassClusterReconciler{Client: ingressMetricsClient(t, false)}

	require.Equal(t, telemetryPolicyRetry, r.reconcileIngressMetrics(context.Background(), telemetryCluster(true, false)),
		"the monitoring chart is still installing, so look again soon instead of failing the cluster")
}

func TestNothingIsMonitoredWhenMonitoringIsOff(t *testing.T) {
	c := ingressMetricsClient(t, true)
	r := &GeassClusterReconciler{Client: c}

	require.Zero(t, r.reconcileIngressMetrics(context.Background(), telemetryCluster(false, false)))

	err := c.Get(context.Background(), client.ObjectKey{Name: ingressMetricsMonitorName, Namespace: platform.MonitoringTargetNS}, &monitoringv1.PodMonitor{})
	require.Error(t, err, "no monitoring means no monitor, and no retry loop")
}
