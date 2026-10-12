package controller

import (
	"fmt"

	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// kubeletExternalAddresses has the monitoring operator list each node's kubelet at its external address.
// By default it uses the private address. In a multi-site cluster private addresses are not routable
// between sites, so Prometheus reached the kubelet on its own node and no other: the container metrics
// (CPU, memory, network) behind every per-service and per-project chart were missing for every pod
// running anywhere else. The external address is the one every node already advertises to the others.
const kubeletExternalAddresses = "--kubelet-node-address-priority=external"

// monitoringChartValues is the values the monitoring chart is installed with for this cluster.
func monitoringChartValues(cluster *geassv1alpha1.GeassCluster) (string, error) {
	values := monitoringLiteValues
	if cluster.Spec.Addons.Monitoring.Profile == "full" {
		values = monitoringPrometheusValues
	}
	if !cluster.Spec.Network.MultiSite {
		return values, nil
	}
	// Merged as data: the base values already configure the operator, and a second top-level
	// prometheusOperator key would silently replace the first.
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(values), &parsed); err != nil {
		return "", fmt.Errorf("monitoring values: %w", err)
	}
	operator, _ := parsed["prometheusOperator"].(map[string]any)
	if operator == nil {
		operator = map[string]any{}
	}
	operator["extraArgs"] = []any{kubeletExternalAddresses}
	parsed["prometheusOperator"] = operator
	merged, err := yaml.Marshal(parsed)
	if err != nil {
		return "", fmt.Errorf("monitoring values: %w", err)
	}
	return string(merged), nil
}
