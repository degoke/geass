package controller

import (
	"fmt"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
)

const (
	alertRulesName = "geass-alerts"

	// keepFiringFor is how long an alert stays firing after the metric behind it has gone. The operator is the only
	// thing that says what is wrong, and for a minute or so, when it restarts or another one takes the lease, it says
	// nothing. Without this every problem still there would be announced as resolved and then announced again.
	keepFiringFor = "5m"
)

// alertProblemLabels are what a problem keeps when it becomes an alert: where it is, so that a channel limited to a project or
// an environment hears it, and what it is called, so that a message can say.
var alertProblemLabels = "geass_kind, severity, project, project_name, environment, resource, resource_kind, path, detail"

// projectNamespaces says which project and environment own each namespace. The environments made for a pull request are
// left out: they fail as a matter of course, and nothing in one is announced.
const projectNamespaces = `max by (namespace, project, project_name, environment) (` + alerts.MetricProjectNamespace + `{` + alerts.LabelPullRequest + `!="` + alerts.PullRequestYes + `"})`

// everyProjectNamespace is every namespace that belongs to a project, those of pull requests included. What is not one of
// these is the platform's.
const everyProjectNamespace = `max by (namespace) (` + alerts.MetricProjectNamespace + `)`

// alertRules are the alerts that Alertmanager sends to the channels an admin has set up.
//
// The first group is made from the operator's geass_problem metric: the operator says what is wrong, and each rule waits
// for it to last. The second is made from what Kubernetes and the nodes already report, which is where most things that
// go wrong without Geass doing anything are found: a pod that keeps crashing, a volume or a disk that fills.
// Both carry the labels the routes of the channels match on.
func alertRules() *monitoringv1.PrometheusRule {
	keep := monitoringv1.NonEmptyDuration(keepFiringFor)
	build := func(alert, expr, wait string, labels, annotations map[string]string) monitoringv1.Rule {
		rule := monitoringv1.Rule{
			Alert: alert, Expr: intstr.FromString(expr), KeepFiringFor: &keep,
			Labels: labels, Annotations: annotations,
		}
		if wait != "" {
			duration := monitoringv1.Duration(wait)
			rule.For = &duration
		}
		return rule
	}
	where := `{{ $labels.resource }}{{ if $labels.project_name }} ({{ $labels.project_name }}{{ if $labels.environment }} / {{ $labels.environment }}{{ end }}){{ end }}`
	// problem is an alert made from the operator's metric.
	problem := func(alert, kind, wait, title, summary, description string) monitoringv1.Rule {
		expr := fmt.Sprintf(`max by (%s) (%s{%s=%q})`, alertProblemLabels, alerts.MetricProblem, alerts.LabelKind, kind)
		return build(alert, expr, wait, nil, map[string]string{"title": title, "summary": summary, "description": description})
	}
	// kubernetes is an alert made from a Kubernetes metric, given the labels a problem has.
	kubernetes := func(alert, kind, severity, resourceKind, wait, expr, title, summary, description string) monitoringv1.Rule {
		return build(alert, expr, wait,
			map[string]string{alerts.LabelKind: kind, alerts.LabelSeverity: severity, alerts.LabelResourceKind: resourceKind},
			map[string]string{"title": title, "summary": summary, "description": description})
	}

	// A pod that crashes, found in a project's namespaces, and in the rest. The first is said to belong to the project.
	crashing := `max by (namespace, pod) (max_over_time(kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff"}[5m])) >= 1`
	inProject := func(expr, nameLabel string) string {
		return `label_replace(label_replace((` + expr + `) * on (namespace) group_left (project, project_name, environment) ` + projectNamespaces +
			`, "resource", "$1", "` + nameLabel + `", "(.+)"), "path", "/projects/$1", "project", "(.+)")`
	}
	// What is in no project's namespace is the platform's, but only while the operator is saying which namespaces are
	// projects': when it is not, every project's pod would look like the platform's.
	elsewhere := func(expr, nameLabel string) string {
		return `label_replace(((` + expr + `) and on () ` + alerts.MetricProblemsEvaluated + `) unless on (namespace) ` + everyProjectNamespace +
			`, "resource", "$1", "` + nameLabel + `", "(.+)")`
	}
	// A volume's free space as a fraction of its size.
	free := `max by (namespace, persistentvolumeclaim) (kubelet_volume_stats_available_bytes / kubelet_volume_stats_capacity_bytes)`
	// A node's least free filesystem that holds data (not the container layers, which come and go). The node exporter knows a
	// node by its address, so its name is joined from node_uname_info, which is what the node's other alerts are named by.
	nodeFree := `min by (instance) (node_filesystem_avail_bytes{fstype=~"ext4|xfs|btrfs|zfs"} / node_filesystem_size_bytes{fstype=~"ext4|xfs|btrfs|zfs"})`

	// A node's disk that has gone read-only, which is what the kernel does to a filesystem that has met errors it cannot go on
	// past: a failing disk, or a corrupted filesystem. Only the places that hold the system and its data are looked at: a
	// read-only mount of a file or a firmware partition is somebody's choice.
	nodeReadOnly := `max by (instance) (node_filesystem_readonly{fstype=~"ext4|xfs|btrfs|zfs", mountpoint=~"/|/var|/var/lib|/var/lib/rancher|/var/lib/longhorn"}) == 1`
	// A replicated volume's health, as Longhorn measures it: 2 has lost a copy, 3 has lost them all. Longhorn says which claim and
	// namespace a volume belongs to in labels of its own, because the namespace label of what it exports is Longhorn's.
	volumeIs := func(robustness int) string {
		return fmt.Sprintf(`max by (namespace, pvc) (label_replace(longhorn_volume_robustness{pvc!=""}, "namespace", "$1", "pvc_namespace", "(.+)") == %d)`, robustness)
	}

	const cluster = `{{ if eq $labels.detail "upgrade" }}The upgrade of the workers stopped{{ else if eq $labels.detail "issuer" }}Certificates cannot be issued{{ else if eq $labels.detail "netpol" }}Network policies are not enforced{{ else if eq $labels.detail "snapshots" }}Copies of the cluster are not reaching their bucket{{ else if eq $labels.detail "ready" }}Geass gave up getting the cluster ready{{ else }}{{ $labels.resource }}{{ end }}`

	return &monitoringv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      alertRulesName,
			Namespace: platform.MonitoringTargetNS,
			Labels:    map[string]string{platform.LabelManagedBy: platform.ManagedByValue},
		},
		Spec: monitoringv1.PrometheusRuleSpec{Groups: []monitoringv1.RuleGroup{
			{
				Name: "geass-problems",
				Rules: []monitoringv1.Rule{
					problem("GeassBuildFailed", geassv1alpha1.AlertBuildFailed, "",
						"Build failed", where, "The build of this service failed. Open its build log in the dashboard for the reason."),
					problem("GeassDeployFailed", geassv1alpha1.AlertDeployFailed, "",
						"Deploy failed", where, "Geass gave up on deploying this after repeated errors. Open it in the dashboard, fix what it says, then save or redeploy to try again."),
					problem("GeassServiceDown", geassv1alpha1.AlertServiceDown, "5m",
						"Service down", where, "No replica of this service has been ready for five minutes."),
					problem("GeassBackupsFailing", geassv1alpha1.AlertBackupFailed, "",
						"Backups failing", where, "This database's backups are failing, so its recent changes may not be recoverable. Open it in the dashboard to see why."),
					problem("GeassNodeNotReady", geassv1alpha1.AlertNodeUnhealthy, "5m",
						"Node not ready", `{{ $labels.resource }}`, "This node has not been ready for five minutes, or could not be set up. Workloads on it are not running."),
					problem("GeassClusterProblem", geassv1alpha1.AlertClusterIssue, "10m",
						"Cluster problem", cluster, "Open the cluster page in the dashboard for the details."),
					problem("GeassAbuseSuspected", geassv1alpha1.AlertAbuseSuspected, "",
						"Abuse suspected", `{{ $labels.resource }} ({{ $labels.detail }})`, "Geass's abuse detection suspects this service of an organization of abusing the platform's machines, such as by mining. It is a signal, not proof: read the evidence (GET /api/v1/abuse/suspects), then clear or release it."),
				},
			},
			{
				Name: "geass-infrastructure",
				Rules: []monitoringv1.Rule{
					kubernetes("GeassPodCrashLooping", geassv1alpha1.AlertWorkloadCrashLooping, "warning", "pod", "15m",
						inProject(crashing, "pod"), "Crash loop", where, "A container of this pod keeps crashing and being restarted. Open the service's logs in the dashboard."),
					kubernetes("GeassSystemPodCrashLooping", geassv1alpha1.AlertWorkloadCrashLooping, "warning", "pod", "15m",
						elsewhere(crashing, "pod"), "Crash loop", `{{ $labels.resource }} ({{ $labels.namespace }})`, "A container of this pod keeps crashing and being restarted. It is part of the platform and not of a project."),
					kubernetes("GeassVolumeFillingUp", geassv1alpha1.AlertStorageFilling, "warning", "volume", "10m",
						inProject(`(`+free+` < 0.15) and (`+free+` >= 0.05)`, "persistentvolumeclaim"), "Storage filling", where, "This volume is more than 85% full. Make it bigger before it fills."),
					kubernetes("GeassVolumeAlmostFull", geassv1alpha1.AlertStorageFilling, "critical", "volume", "5m",
						inProject(free+` < 0.05`, "persistentvolumeclaim"), "Storage filling", where, "This volume is more than 95% full, and what writes to it will fail when it is."),
					kubernetes("GeassSystemVolumeFillingUp", geassv1alpha1.AlertStorageFilling, "warning", "volume", "10m",
						elsewhere(free+` < 0.10`, "persistentvolumeclaim"), "Storage filling", `{{ $labels.resource }} ({{ $labels.namespace }})`, "This volume, which belongs to the platform, is more than 90% full."),
					kubernetes("GeassNodeDiskFilling", geassv1alpha1.AlertStorageFilling, "warning", "node", "15m",
						`label_replace(label_replace((`+nodeFree+` < 0.10) * on (instance) group_left (nodename) max by (instance, nodename) (node_uname_info), "resource", "$1", "nodename", "(.+)"), "path", "/cluster", "instance", ".*")`,
						"Storage filling", `{{ $labels.resource }}`, "A disk of this node is more than 90% full. Free some space, or the node will start failing."),
					kubernetes("GeassNodeDiskReadOnly", geassv1alpha1.AlertStorageFailing, "critical", "node", "2m",
						`label_replace(label_replace((`+nodeReadOnly+`) * on (instance) group_left (nodename) max by (instance, nodename) (node_uname_info), "resource", "$1", "nodename", "(.+)"), "path", "/cluster", "instance", ".*")`,
						"Storage failing", `{{ $labels.resource }}`, "A disk of this node has gone read-only, which is what the system does to a disk that is failing or a filesystem that is corrupted. What writes to it fails. Look at `dmesg` on the machine, and move what matters to another node."),
					kubernetes("GeassVolumeDegraded", geassv1alpha1.AlertStorageFailing, "warning", "volume", "30m",
						inProject(volumeIs(2), "pvc"), "Storage failing", where, "This volume has lost a copy of its data and has not got it back. It still works, but losing another copy loses the data. Bring the node that held the copy back, or add a node for it."),
					kubernetes("GeassVolumeUnavailable", geassv1alpha1.AlertStorageFailing, "critical", "volume", "5m",
						inProject(volumeIs(3), "pvc"), "Storage failing", where, "Every copy of this volume is lost or cannot be read, so what uses it is down. Bring the node that holds a copy back, or restore the database from its backup."),
					kubernetes("GeassSystemVolumeDegraded", geassv1alpha1.AlertStorageFailing, "warning", "volume", "30m",
						elsewhere(volumeIs(2), "pvc"), "Storage failing", `{{ $labels.resource }} ({{ $labels.namespace }})`, "This volume, which belongs to the platform, has lost a copy of its data and has not got it back."),
					kubernetes("GeassSystemVolumeUnavailable", geassv1alpha1.AlertStorageFailing, "critical", "volume", "5m",
						elsewhere(volumeIs(3), "pvc"), "Storage failing", `{{ $labels.resource }} ({{ $labels.namespace }})`, "Every copy of this volume, which belongs to the platform, is lost or cannot be read."),
				},
			},
		}},
	}
}
