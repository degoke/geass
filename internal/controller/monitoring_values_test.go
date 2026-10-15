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
	require.Equal(t, false, parsed["defaultRules"].(map[string]any)["create"])
}

// The chart silences a warning when a critical alert has the same name and namespace. Geass's alerts have no namespace
// label, so that was true of a critical failure in production and the warning about the same failure in staging, and in
// every other project: the second was never sent. The rules are kept for everything else, and Geass's alerts are no
// longer what they silence.
func TestNoInhibitRuleCanSilenceAGeassAlert(t *testing.T) {
	for name, values := range map[string]string{"lite": monitoringLiteValues, "full": monitoringPrometheusValues} {
		t.Run(name, func(t *testing.T) {
			var parsed struct {
				Alertmanager struct {
					Config struct {
						InhibitRules []struct {
							SourceMatchers []string `json:"source_matchers"`
							TargetMatchers []string `json:"target_matchers"`
							Equal          []string `json:"equal"`
						} `json:"inhibit_rules"`
					} `json:"config"`
				} `json:"alertmanager"`
			}
			require.NoError(t, yaml.Unmarshal([]byte(values), &parsed))
			rules := parsed.Alertmanager.Config.InhibitRules
			require.Len(t, rules, 4, "the chart's four, which other alerts still rely on")
			for _, rule := range rules {
				if len(rule.SourceMatchers) == 0 && len(rule.TargetMatchers) == 1 && rule.TargetMatchers[0] == `alertname = InfoInhibitor` {
					continue // it hides the helper alert itself, which is not Geass's
				}
				require.Contains(t, rule.TargetMatchers, `geass_kind = ""`, "%v would silence Geass's alerts", rule)
			}
		})
	}
}

// Prometheus loads only the rules its selector picks, and the chart's default picks only rules with the release
// label. Geass's rules have none, so without this the operator's alerts and the alerts on services are written
// and never evaluated.
func TestEveryMonitoringProfileLoadsRulesWithoutTheReleaseLabel(t *testing.T) {
	for name, values := range map[string]string{"lite": monitoringLiteValues, "full": monitoringPrometheusValues} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, false, prometheusSpecOf(t, values)["ruleSelectorNilUsesHelmValues"])
		})
	}
}

func TestEveryMonitoringProfileRunsAlertmanagerForTheChannelsAdminsSetUp(t *testing.T) {
	for name, values := range map[string]string{"lite": monitoringLiteValues, "full": monitoringPrometheusValues} {
		t.Run(name, func(t *testing.T) {
			var parsed map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(values), &parsed), "a key defined twice would be an error here, and in Helm")
			alertmanager, ok := parsed["alertmanager"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, true, alertmanager["enabled"])
			spec := alertmanager["alertmanagerSpec"].(map[string]any)
			require.Equal(t, map[string]any{"type": "None"}, spec["alertmanagerConfigMatcherStrategy"],
				"the alerts are not made in the namespace of the config that routes them")
			require.NotNil(t, spec["storage"], "silences and what was notified would be lost when the pod moves")
		})
	}
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
