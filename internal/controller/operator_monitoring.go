package controller

import (
	"context"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;prometheusrules,verbs=get;list;watch;create;update;patch;delete

// The operator serves its own metrics: how long each reconcile takes and how often it fails, how deep the work
// queues are, how long the API server takes to answer it, and how many status writes it made or skipped. They
// are worth nothing unless something collects them and something notices when they go wrong, so with the
// monitoring add-on the operator makes the monitor and the alert rules for itself.

const (
	operatorMonitorName = "geass-operator"
	operatorRulesName   = "geass-operator"
)

// operatorMetricsMonitor has Prometheus scrape the operator's metrics Service. The endpoint is HTTPS and
// authenticated, and Prometheus is let in by a role binding that ships with the operator
// (config/rbac/metrics_reader_role_binding.yaml). The operator's certificate is the self-signed one it makes
// for itself, so it cannot be verified.
func operatorMetricsMonitor() *monitoringv1.ServiceMonitor {
	return &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorMonitorName,
			Namespace: platform.MonitoringTargetNS,
			Labels:    map[string]string{platform.LabelManagedBy: platform.ManagedByValue},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			NamespaceSelector: monitoringv1.NamespaceSelector{MatchNames: []string{platform.SystemNamespace}},
			Selector: metav1.LabelSelector{MatchLabels: map[string]string{
				"control-plane":          "controller-manager",
				"app.kubernetes.io/name": "geass",
			}},
			Endpoints: []monitoringv1.Endpoint{{
				Port:   "https",
				Path:   "/metrics",
				Scheme: "https",
				// Prometheus presents its own service account's token. The field is deprecated in favour of one that
				// can only name a Secret, which would mean copying that token; the monitoring chart this ships with
				// (and its operator) still honours it.
				BearerTokenFile: "/var/run/secrets/kubernetes.io/serviceaccount/token", //nolint:staticcheck
				TLSConfig:       &monitoringv1.TLSConfig{SafeTLSConfig: monitoringv1.SafeTLSConfig{InsecureSkipVerify: ptrBool(true)}},
				Interval:        "30s",
			}},
		},
	}
}

func ptrBool(b bool) *bool { return &b }

// operatorRules are the alerts on the operator's own health. Each says what is wrong in the terms of what
// the operator does, and none fires on a blip: they wait for the problem to last.
func operatorRules() *monitoringv1.PrometheusRule {
	rule := func(alert, expr, duration, severity, summary string) monitoringv1.Rule {
		forDuration := monitoringv1.Duration(duration)
		return monitoringv1.Rule{
			Alert:       alert,
			Expr:        intstr.FromString(expr),
			For:         &forDuration,
			Labels:      map[string]string{"severity": severity},
			Annotations: map[string]string{"summary": summary},
		}
	}
	return &monitoringv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorRulesName,
			Namespace: platform.MonitoringTargetNS,
			Labels:    map[string]string{platform.LabelManagedBy: platform.ManagedByValue},
		},
		Spec: monitoringv1.PrometheusRuleSpec{Groups: []monitoringv1.RuleGroup{{
			Name: "geass-operator",
			Rules: []monitoringv1.Rule{
				rule("GeassOperatorDown",
					`absent(controller_runtime_active_workers)`,
					"5m", "critical", "The Geass operator is not reporting metrics, so nothing is being reconciled."),
				rule("GeassReconcileErrors",
					`sum by (controller) (rate(controller_runtime_reconcile_errors_total[10m])) > 0.05`,
					"15m", "warning", "A Geass controller keeps failing its reconciles."),
				rule("GeassSlowReconciles",
					`histogram_quantile(0.99, sum by (le, controller) (rate(controller_runtime_reconcile_time_seconds_bucket[10m]))) > 30`,
					"15m", "warning", "A Geass controller takes more than 30 seconds to reconcile."),
				rule("GeassWorkqueueBacklog",
					`max by (name) (workqueue_depth{name=~"geass.*"}) > 25`,
					"15m", "warning", "A Geass controller has a backlog of work it is not getting through."),
				rule("GeassOperatorMemoryHigh",
					`max(process_resident_memory_bytes and on(job, instance) controller_runtime_active_workers) > 700 * 1024 * 1024`,
					"15m", "warning", "The Geass operator is using most of the 1 GiB it is allowed. Raise its memory limit before it is killed: see Cluster size in the documentation."),
				rule("GeassAPIServerSlow",
					`histogram_quantile(0.99, sum by (le) (rate(geass_apiserver_client_request_duration_seconds_bucket{verb!="WATCH"}[10m]))) > 1`,
					"10m", "warning", "The Kubernetes API server takes more than a second to answer the operator."),
				rule("GeassAPIServerErrors",
					`sum(rate(geass_apiserver_client_request_duration_seconds_count{code=~"5..|error"}[10m])) / sum(rate(geass_apiserver_client_request_duration_seconds_count[10m])) > 0.05`,
					"10m", "warning", "More than one in twenty of the operator's requests to the Kubernetes API server fail."),
			},
		}}},
	}
}

// reconcileOperatorMonitoring keeps the operator's monitor and alert rules in place while monitoring is on.
// It is background work like the other add-on extras: a problem is logged and retried, never held against
// the cluster. It returns how soon to look again, or zero when there is nothing left to do.
func (r *GeassClusterReconciler) reconcileOperatorMonitoring(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	if !geassv1alpha1.AddonEnabled(cluster.Spec.Addons.Monitoring.Enabled) {
		return 0
	}
	log := logf.FromContext(ctx)

	desiredMonitor := operatorMetricsMonitor()
	monitor := &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: desiredMonitor.Name, Namespace: desiredMonitor.Namespace}}
	_, monitorErr := controllerutil.CreateOrUpdate(ctx, r.Client, monitor, func() error {
		monitor.Labels = desiredMonitor.Labels
		monitor.Spec = desiredMonitor.Spec
		return nil
	})

	desiredRules := operatorRules()
	rules := &monitoringv1.PrometheusRule{ObjectMeta: metav1.ObjectMeta{Name: desiredRules.Name, Namespace: desiredRules.Namespace}}
	_, rulesErr := controllerutil.CreateOrUpdate(ctx, r.Client, rules, func() error {
		rules.Labels = desiredRules.Labels
		rules.Spec = desiredRules.Spec
		return nil
	})

	if monitorErr == nil && rulesErr == nil {
		return 0
	}
	for _, err := range []error{monitorErr, rulesErr} {
		// The kinds, and the namespace, do not exist until the monitoring chart has been installed.
		if err != nil && !meta.IsNoMatchError(err) && client.IgnoreNotFound(err) != nil {
			log.Error(err, "Failed to apply the operator's monitor or alert rules")
		}
	}
	return telemetryPolicyRetry
}
