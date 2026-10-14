package controller

import (
	"context"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/degoke/geass/pkg/platform"
)

func TestTheOperatorIsScrapedOverItsAuthenticatedEndpoint(t *testing.T) {
	c := ingressMetricsClient(t, true)
	r := &GeassClusterReconciler{Client: c}

	require.Zero(t, r.reconcileOperatorMonitoring(context.Background(), telemetryCluster(true, false)))

	var monitor monitoringv1.ServiceMonitor
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: operatorMonitorName, Namespace: platform.MonitoringTargetNS}, &monitor))
	require.Equal(t, []string{platform.SystemNamespace}, monitor.Spec.NamespaceSelector.MatchNames)
	require.Equal(t, map[string]string{"control-plane": "controller-manager", "app.kubernetes.io/name": "geass"}, monitor.Spec.Selector.MatchLabels,
		"the labels of the operator's metrics Service")
	endpoint := monitor.Spec.Endpoints[0]
	require.Equal(t, "https", endpoint.Port, "the name of the Service's metrics port")
	require.Equal(t, "https", endpoint.Scheme)
	require.NotEmpty(t, endpoint.BearerTokenFile, "the endpoint checks who is asking") //nolint:staticcheck

	require.Zero(t, r.reconcileOperatorMonitoring(context.Background(), telemetryCluster(true, false)), "doing it again is not an error")
}

func TestOperatorAlertsAreCreatedAndSayWhatIsWrong(t *testing.T) {
	c := ingressMetricsClient(t, true)
	r := &GeassClusterReconciler{Client: c}
	require.Zero(t, r.reconcileOperatorMonitoring(context.Background(), telemetryCluster(true, false)))

	var rules monitoringv1.PrometheusRule
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: operatorRulesName, Namespace: platform.MonitoringTargetNS}, &rules))

	alerts := map[string]monitoringv1.Rule{}
	for _, rule := range rules.Spec.Groups[0].Rules {
		alerts[rule.Alert] = rule
	}
	for _, name := range []string{"GeassOperatorDown", "GeassReconcileErrors", "GeassSlowReconciles", "GeassWorkqueueBacklog", "GeassOperatorMemoryHigh", "GeassAPIServerSlow", "GeassAPIServerErrors"} {
		rule, ok := alerts[name]
		require.True(t, ok, name)
		require.NotNil(t, rule.For, "%s waits for the problem to last", name)
		require.NotEmpty(t, rule.Annotations["summary"], name)
		require.NotEmpty(t, rule.Expr.StrVal, name)
	}
	require.Contains(t, alerts["GeassAPIServerSlow"].Expr.StrVal, "geass_apiserver_client_request_duration_seconds_bucket", "the alert reads the metric this operator exports")
	require.Contains(t, alerts["GeassAPIServerSlow"].Expr.StrVal, `verb!="WATCH"`, "a watch stays open for minutes, which is not slowness")
	require.True(t, strings.HasPrefix(alerts["GeassOperatorDown"].Expr.StrVal, "absent("))
}

func TestOperatorMonitoringWaitsForThePrometheusOperatorAndStopsWhenMonitoringIsOff(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	missing := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "monitoring.coreos.com", Kind: "ServiceMonitor"}, SearchedVersions: []string{"v1"}}
		},
	}).Build()
	waiting := &GeassClusterReconciler{Client: missing}
	require.Equal(t, telemetryPolicyRetry, waiting.reconcileOperatorMonitoring(context.Background(), telemetryCluster(true, false)))

	c := ingressMetricsClient(t, true)
	off := &GeassClusterReconciler{Client: c}
	require.Zero(t, off.reconcileOperatorMonitoring(context.Background(), telemetryCluster(false, false)))
	require.Error(t, c.Get(context.Background(), client.ObjectKey{Name: operatorMonitorName, Namespace: platform.MonitoringTargetNS}, &monitoringv1.ServiceMonitor{}))
}
