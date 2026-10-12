package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

func loggingCluster(enabled bool, days int32) *geassv1alpha1.GeassCluster {
	cluster := replicaTestCluster(false)
	cluster.Spec.Addons.Logging = geassv1alpha1.GeassClusterLoggingAddon{Enabled: &enabled, RetentionDays: days}
	return cluster
}

func chartValues(t *testing.T, raw string) map[string]any {
	t.Helper()
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(raw), &parsed), raw)
	return parsed
}

func TestLogStoreIsBoundedByAgeDiskAndMemory(t *testing.T) {
	values := chartValues(t, logStoreValues(14))
	server := values["server"].(map[string]any)

	require.Equal(t, "14d", server["retentionPeriod"], "the chosen retention is what the server keeps")
	require.NotEmpty(t, server["retentionDiskSpaceUsage"], "a busy cluster must not fill the volume")
	require.Equal(t, platform.LogStoreServiceName, server["fullnameOverride"], "the shipper and the dashboard find it by this name")
	limits := server["resources"].(map[string]any)["limits"].(map[string]any)
	require.NotEmpty(t, limits["memory"], "a memory limit, so it cannot starve a small node")
	require.Equal(t, true, server["persistentVolume"].(map[string]any)["enabled"], "history that vanishes with the pod is not history")
}

func TestLogShipperRunsOnEveryNodeAndStaysSmall(t *testing.T) {
	values := chartValues(t, logShipperValues())

	tolerations := values["tolerations"].([]any)
	require.Equal(t, map[string]any{"operator": "Exists"}, tolerations[0], "a reserved node's logs are collected too")
	limits := values["resources"].(map[string]any)["limits"].(map[string]any)
	require.NotEmpty(t, limits["memory"])
}

func TestLogShipperSendsToTheStoreWithEveryFieldTheDashboardQueriesBy(t *testing.T) {
	config := chartValues(t, logShipperValues())["config"].(map[string]any)
	outputs := config["outputs"].(string)

	require.Contains(t, outputs, platform.LogStoreServiceName+"."+platform.LoggingTargetNS+".svc")
	require.Contains(t, outputs, "_stream_fields=kubernetes_namespace_name,kubernetes_pod_name,kubernetes_container_name",
		"the namespace is a stream field, so a project's logs are found without scanning everyone's")
	require.Contains(t, outputs, "_msg_field=log", "the message is the raw line")
	require.Contains(t, config["filters"].(string), "Labels On", "services are told apart by the labels on their pods")
	require.Contains(t, config["filters"].(string), "Merge_Log Off", "the line is kept as written, not rewritten as JSON fields")
}

func TestLogShipperReadsExistingLogsOnceAndRemembersItsPlace(t *testing.T) {
	input := chartValues(t, logShipperValues())["config"].(map[string]any)["inputs"].(string)
	require.Contains(t, input, "Read_from_Head On", "history appears when the add-on is installed, not only what is written after")
	require.Contains(t, input, "DB /var/log/flb_geass.db", "without remembered offsets, reading from the head would send everything again after every restart")
}

func TestPlatformNamespacesAreNotCollected(t *testing.T) {
	input := chartValues(t, logShipperValues())["config"].(map[string]any)["inputs"].(string)
	for _, namespace := range []string{"kube-system", "monitoring", "longhorn-system", "cnpg-system", "cert-manager", platform.LoggingTargetNS} {
		require.Contains(t, input, "_"+namespace+"_", namespace)
	}
	require.NotContains(t, input, "_geass-system_", "geass-system holds the shared databases, which are worth searching")
	exclude := ""
	for line := range strings.SplitSeq(input, "\n") {
		if strings.Contains(line, "Exclude_Path") {
			exclude = line
		}
	}
	require.NotEmpty(t, exclude, "excluded at the source, so those files are never even read")
}

func TestEnablingLoggingInstallsTheStoreAndTheShipper(t *testing.T) {
	r, c := longhornTestReconciler(t)
	r.reconcileLogging(context.Background(), loggingCluster(true, 3))

	store, err := getHelmChartNamed(c, platform.LogStoreChartName)
	require.NoError(t, err)
	require.Equal(t, platform.LogStoreChartVersion, store.Spec.Version)
	require.Equal(t, platform.LoggingTargetNS, store.Spec.TargetNamespace)
	require.Contains(t, store.Spec.ValuesContent, "retentionPeriod: 3d")

	shipper, err := getHelmChartNamed(c, platform.LogShipperChartName)
	require.NoError(t, err)
	require.Equal(t, platform.LogShipperReleaseChart, shipper.Spec.Chart)
}

func TestLoggingDefaultsToAWeekOfRetention(t *testing.T) {
	r, c := longhornTestReconciler(t)
	r.reconcileLogging(context.Background(), loggingCluster(true, 0))
	store, err := getHelmChartNamed(c, platform.LogStoreChartName)
	require.NoError(t, err)
	require.Contains(t, store.Spec.ValuesContent, "retentionPeriod: 7d")
}

func TestLoggingIsOffUnlessAskedFor(t *testing.T) {
	r, c := longhornTestReconciler(t)
	unset := replicaTestCluster(false)
	r.reconcileLogging(context.Background(), unset)

	_, err := getHelmChartNamed(c, platform.LogStoreChartName)
	require.Error(t, err, "a cluster that never asked for logging gets none, however it was created")
}

func TestTurningLoggingOffRemovesIt(t *testing.T) {
	r, c := longhornTestReconciler(t)
	r.reconcileLogging(context.Background(), loggingCluster(true, 7))
	_, err := getHelmChartNamed(c, platform.LogShipperChartName)
	require.NoError(t, err)

	r.reconcileLogging(context.Background(), loggingCluster(false, 7))

	_, err = getHelmChartNamed(c, platform.LogShipperChartName)
	require.Error(t, err)
	_, err = getHelmChartNamed(c, platform.LogStoreChartName)
	require.Error(t, err)
}

func TestLoggingNeverHoldsTheClusterBack(t *testing.T) {
	// A cluster with logging on and nothing installed yet is still ready: logging is not needed to run workloads.
	r, _ := longhornTestReconciler(t, testNode("n1"))
	off := false
	cluster := loggingCluster(true, 7)
	cluster.Spec.Addons.CertManager.Enabled = &off
	cluster.Spec.Addons.Monitoring.Enabled = &off
	ready, err := r.reconcileAddons(context.Background(), cluster)
	require.NoError(t, err)
	require.True(t, ready, "waiting for the log store to start must not make the cluster not ready")
}

func getHelmChartNamed(c client.Client, name string) (*helmv1.HelmChart, error) {
	chart := &helmv1.HelmChart{}
	err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.HelmChartNamespace}, chart)
	return chart, err
}
