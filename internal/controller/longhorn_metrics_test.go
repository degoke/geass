package controller

import (
	"context"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

func TestMonitoringScrapesLonghornSoAVolumeThatLostACopyIsSeen(t *testing.T) {
	c := ingressMetricsClient(t, true)
	r := &GeassClusterReconciler{Client: c}
	cluster := telemetryCluster(true, false)
	on := true
	cluster.Spec.Addons.Longhorn.Enabled = &on

	require.Zero(t, r.reconcileLonghornMetrics(context.Background(), cluster))

	var monitor monitoringv1.PodMonitor
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: longhornMetricsMonitorName, Namespace: platform.MonitoringTargetNS}, &monitor))
	require.Equal(t, []string{platform.LonghornTargetNS}, monitor.Spec.NamespaceSelector.MatchNames)
	require.Equal(t, map[string]string{"app": "longhorn-manager"}, monitor.Spec.Selector.MatchLabels)
	require.Equal(t, "manager", *monitor.Spec.PodMetricsEndpoints[0].Port, "the port Longhorn's manager names for its metrics")
	require.Zero(t, r.reconcileLonghornMetrics(context.Background(), cluster), "doing it again is not an error")
}

func TestLonghornIsOnlyMonitoredWhereThereIsLonghornAndMonitoring(t *testing.T) {
	for name, cluster := range map[string]func() (monitoring, longhorn bool){
		"no Longhorn":     func() (bool, bool) { return true, false },
		"no monitoring":   func() (bool, bool) { return false, true },
		"neither of them": func() (bool, bool) { return false, false },
	} {
		monitoring, longhorn := cluster()
		c := ingressMetricsClient(t, true)
		r := &GeassClusterReconciler{Client: c}
		spec := telemetryCluster(monitoring, false)
		spec.Spec.Addons.Longhorn.Enabled = &longhorn

		require.Zero(t, r.reconcileLonghornMetrics(context.Background(), spec), name)
		require.Error(t, c.Get(context.Background(), client.ObjectKey{Name: longhornMetricsMonitorName, Namespace: platform.MonitoringTargetNS}, &monitoringv1.PodMonitor{}), name)
	}
}

func TestLonghornMetricsAreRetriedUntilThePrometheusOperatorIsInstalled(t *testing.T) {
	r := &GeassClusterReconciler{Client: ingressMetricsClient(t, false)}
	cluster := telemetryCluster(true, false)
	on := true
	cluster.Spec.Addons.Longhorn.Enabled = &on

	require.Equal(t, telemetryPolicyRetry, r.reconcileLonghornMetrics(context.Background(), cluster))
}
