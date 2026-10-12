package controller

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// Logging keeps what pods write after the pods are gone. A Fluent Bit pod on every node reads the container
// logs and sends them to a VictoriaLogs server, which keeps them for the retention period and answers
// searches. The dashboard reads the live logs from the nodes for "now" and from VictoriaLogs for before.
//
// Namespaces that are the platform's own plumbing are not collected: they are noisy, and nobody searches
// them here. Everything else is, and the dashboard only ever asks for a project's own namespaces.

// logShipperExcludedNamespaces are not collected.
var logShipperExcludedNamespaces = []string{"kube-system", "kube-public", "kube-node-lease", "local-path-storage", "monitoring", "longhorn-system", "cnpg-system", "cert-manager", platform.LoggingTargetNS}

// logStoreValues are the VictoriaLogs chart values. The server is bounded by age and by disk, and by memory,
// so a chatty cluster cannot use more than a small node can spare.
func logStoreValues(retentionDays int32) string {
	return fmt.Sprintf(`server:
  fullnameOverride: %s
  retentionPeriod: %dd
  retentionDiskSpaceUsage: 4GiB
  persistentVolume:
    enabled: true
    size: 6Gi
  resources:
    requests:
      cpu: 20m
      memory: 64Mi
    limits:
      memory: 384Mi
`, platform.LogStoreServiceName, retentionDays)
}

// logShipperValues are the Fluent Bit chart values. The shipper runs on every node, reserved ones included
// (its toleration matches everything), and is kept small: logs are not worth starving a workload for.
//
// It reads each file from its start the first time, so logs appear as soon as the add-on is installed
// instead of only what is written afterwards, and remembers how far it got in a database on the node, so a
// restart neither sends everything again nor loses what was written while it was down.
func logShipperValues() string {
	paths := make([]string, len(logShipperExcludedNamespaces))
	for i, namespace := range logShipperExcludedNamespaces {
		paths[i] = "/var/log/containers/*_" + namespace + "_*.log"
	}
	exclude := strings.Join(paths, ",")
	return fmt.Sprintf(`tolerations:
  - operator: Exists
resources:
  requests:
    cpu: 10m
    memory: 24Mi
  limits:
    memory: 96Mi
config:
  service: |
    [SERVICE]
        Daemon Off
        Flush 1
        Log_Level warn
        Parsers_File /fluent-bit/etc/parsers.conf
        HTTP_Server On
        HTTP_Listen 0.0.0.0
        HTTP_Port 2020
        Health_Check On
  inputs: |
    [INPUT]
        Name tail
        Path /var/log/containers/*.log
        Exclude_Path %s
        multiline.parser docker, cri
        Tag kube.*
        Mem_Buf_Limit 8MB
        Skip_Long_Lines On
        Refresh_Interval 5
        Read_from_Head On
        DB /var/log/flb_geass.db
        DB.sync normal
  filters: |
    [FILTER]
        Name kubernetes
        Match kube.*
        Kube_URL https://kubernetes.default.svc:443
        Kube_Tag_Prefix kube.var.log.containers.
        Merge_Log Off
        Keep_Log On
        Labels On
        Annotations Off
        K8S-Logging.Exclude On
    [FILTER]
        Name nest
        Match kube.*
        Operation lift
        Nested_under kubernetes
        Add_prefix kubernetes_
  outputs: |
    [OUTPUT]
        Name http
        Match kube.*
        Host %s.%s.svc
        Port %s
        URI /insert/jsonline?_stream_fields=kubernetes_namespace_name,kubernetes_pod_name,kubernetes_container_name&_msg_field=log&_time_field=date
        Format json_lines
        Json_date_key date
        Json_date_format iso8601
        Retry_Limit 5
`, exclude, platform.LogStoreServiceName, platform.LoggingTargetNS, platform.LogStorePort)
}

// reconcileLogging installs or removes log collection. It is background work: a problem here is logged and
// never returned, so a cluster whose logging is unwell is still a ready cluster that runs workloads.
func (r *GeassClusterReconciler) reconcileLogging(ctx context.Context, cluster *geassv1alpha1.GeassCluster) {
	log := logf.FromContext(ctx)
	if !geassv1alpha1.LoggingEnabled(cluster.Spec.Addons) {
		// Turning logging off removes the collectors and the server; what they kept goes with them. Unlike
		// storage it is not user data, so this is allowed to uninstall.
		for _, name := range []string{platform.LogShipperChartName, platform.LogStoreChartName} {
			if err := helmchart.Delete(ctx, r.Client, name); err != nil && !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to remove the logging add-on", "chart", name)
			}
		}
		return
	}
	store := helmv1.HelmChartSpec{
		Chart:           platform.LogStoreReleaseChart,
		Repo:            platform.LogStoreChartRepo,
		Version:         platform.LogStoreChartVersion,
		TargetNamespace: platform.LoggingTargetNS,
		CreateNamespace: true,
		ValuesContent:   logStoreValues(geassv1alpha1.LogRetentionDays(cluster.Spec.Addons)),
	}
	if err := helmchart.Ensure(ctx, r.Client, platform.LogStoreChartName, store); err != nil {
		log.Error(err, "Failed to install the log store")
		return
	}
	shipper := helmv1.HelmChartSpec{
		Chart:           platform.LogShipperReleaseChart,
		Repo:            platform.LogShipperChartRepo,
		Version:         platform.LogShipperChartVersion,
		TargetNamespace: platform.LoggingTargetNS,
		// The store's chart creates the namespace; both install into it.
		CreateNamespace: true,
		ValuesContent:   logShipperValues(),
	}
	if err := helmchart.Ensure(ctx, r.Client, platform.LogShipperChartName, shipper); err != nil {
		log.Error(err, "Failed to install the log shipper")
	}
}
