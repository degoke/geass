package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func prometheusSpecOf(t *testing.T, values string) map[string]any {
	t.Helper()
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(values), &parsed), values)
	prometheus, ok := parsed["prometheus"].(map[string]any)
	require.True(t, ok, "values must configure prometheus")
	spec, ok := prometheus["prometheusSpec"].(map[string]any)
	require.True(t, ok, "values must configure prometheusSpec")
	return spec
}

func TestEveryMonitoringProfileKeepsMetricsOnAVolumeWithBoundedRetention(t *testing.T) {
	for name, values := range map[string]string{"lite": monitoringLiteValues, "full": monitoringPrometheusValues} {
		t.Run(name, func(t *testing.T) {
			spec := prometheusSpecOf(t, values)
			require.Equal(t, "15d", spec["retention"])
			require.Equal(t, "4GB", spec["retentionSize"], "the size cap must stay under the 5Gi volume")
			require.NotNil(t, spec["storageSpec"], "without a volume, metrics history is lost when the pod moves")
		})
	}
}

func TestEveryMonitoringProfileScrapesMonitorsWithoutTheReleaseLabel(t *testing.T) {
	for name, values := range map[string]string{"lite": monitoringLiteValues, "full": monitoringPrometheusValues} {
		t.Run(name, func(t *testing.T) {
			spec := prometheusSpecOf(t, values)
			require.Equal(t, false, spec["serviceMonitorSelectorNilUsesHelmValues"], "Geass ServiceMonitors carry no release label")
			require.Equal(t, false, spec["podMonitorSelectorNilUsesHelmValues"])
		})
	}
}

func TestLiteMonitoringStaysLite(t *testing.T) {
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(monitoringLiteValues), &parsed))
	require.Equal(t, false, parsed["grafana"].(map[string]any)["enabled"])
	require.Equal(t, false, parsed["alertmanager"].(map[string]any)["enabled"])
}

// The dashboard queries Prometheus at a fixed Service name. Left to the chart, that name is the release
// name cut to 26 characters, which nothing in Geass related to the name the dashboard asked for, so on a
// fresh install the hostname did not resolve and no metric was ever shown.
func TestEveryMonitoringProfilePinsTheNamesTheDashboardRelies(t *testing.T) {
	for name, values := range map[string]string{"lite": monitoringLiteValues, "full": monitoringPrometheusValues} {
		t.Run(name, func(t *testing.T) {
			var parsed map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(values), &parsed))
			require.Equal(t, platform.MonitoringFullName, parsed["fullnameOverride"])
		})
	}
}

func TestThePinnedMonitoringNameIsWhatTheChartAlreadyProducedForRunningClusters(t *testing.T) {
	// Helm cuts the release name to 26 characters. Running clusters have Services with exactly that
	// name, so the pin must equal it or an upgrade would rename (and restart) their monitoring.
	require.Equal(t, platform.MonitoringChartName[:26], platform.MonitoringFullName)
	require.Equal(t, platform.MonitoringFullName+"-prometheus", platform.MonitoringPrometheusService)
}

func monitoringCluster(multiSite bool, profile string) *geassv1alpha1.GeassCluster {
	cluster := replicaTestCluster(false)
	cluster.Spec.Network.MultiSite = multiSite
	cluster.Spec.Addons.Monitoring.Profile = profile
	return cluster
}

func operatorArgsOf(t *testing.T, values string) []any {
	t.Helper()
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(values), &parsed))
	operator, _ := parsed["prometheusOperator"].(map[string]any)
	args, _ := operator["extraArgs"].([]any)
	return args
}

// Private addresses do not route between sites, so Prometheus could scrape only the kubelet on its own node.
func TestAMultiSiteClusterHasPrometheusScrapeEveryKubeletAtItsExternalAddress(t *testing.T) {
	for _, profile := range []string{"", "full"} {
		t.Run("profile="+profile, func(t *testing.T) {
			values, err := monitoringChartValues(monitoringCluster(true, profile))
			require.NoError(t, err)

			require.Equal(t, []any{"--kubelet-node-address-priority=external"}, operatorArgsOf(t, values))
			spec := prometheusSpecOf(t, values)
			require.Equal(t, "15d", spec["retention"], "the rest of the values are untouched")
			var parsed map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(values), &parsed))
			require.Equal(t, platform.MonitoringFullName, parsed["fullnameOverride"])
		})
	}
}

func TestASingleSiteClusterKeepsTheDefaultKubeletAddresses(t *testing.T) {
	values, err := monitoringChartValues(monitoringCluster(false, ""))
	require.NoError(t, err)
	require.Equal(t, monitoringLiteValues, values, "nothing changes for the common case")
	require.Empty(t, operatorArgsOf(t, values))
}

func TestMultiSiteKeepsTheLiteProfileSwitchedOffPartsOff(t *testing.T) {
	values, err := monitoringChartValues(monitoringCluster(true, ""))
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(values), &parsed))
	require.Equal(t, false, parsed["grafana"].(map[string]any)["enabled"])
	require.Equal(t, true, parsed["prometheusOperator"].(map[string]any)["enabled"], "the operator's own settings survive the merge")
}
