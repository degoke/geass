package controller

import (
	"context"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// longhornMetricsMonitorName is the monitor that has Prometheus scrape Longhorn.
const longhornMetricsMonitorName = "geass-longhorn"

// longhornMetricsMonitor has Prometheus scrape the Longhorn managers, one on every node. They say whether each volume has
// all of its copies (longhorn_volume_robustness), which is what the alerts for a volume that has lost one are made from.
// Longhorn's chart can make this monitor itself, but only when the monitoring chart's kinds exist already, and Longhorn is
// installed first: a chart that asks for a kind that is not there fails to install.
func longhornMetricsMonitor() *monitoringv1.PodMonitor {
	port := "manager"
	return &monitoringv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      longhornMetricsMonitorName,
			Namespace: platform.MonitoringTargetNS,
			Labels:    map[string]string{platform.LabelManagedBy: platform.ManagedByValue},
		},
		Spec: monitoringv1.PodMonitorSpec{
			NamespaceSelector:   monitoringv1.NamespaceSelector{MatchNames: []string{platform.LonghornTargetNS}},
			Selector:            metav1.LabelSelector{MatchLabels: map[string]string{"app": "longhorn-manager"}},
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{{Port: &port, Path: "/metrics"}},
		},
	}
}

// reconcileLonghornMetrics keeps the Longhorn monitor in place while monitoring and Longhorn are both on. Like the other
// extras it is background work: a problem is logged and retried, never held against the cluster. It returns how soon to
// look again, or zero when there is nothing left to do.
func (r *GeassClusterReconciler) reconcileLonghornMetrics(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	if !geassv1alpha1.AddonEnabled(cluster.Spec.Addons.Monitoring.Enabled) || !geassv1alpha1.LonghornEnabled(cluster.Spec.Addons) {
		return 0
	}
	desired := longhornMetricsMonitor()
	monitor := &monitoringv1.PodMonitor{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, monitor, func() error {
		monitor.Labels = desired.Labels
		monitor.Spec = desired.Spec
		return nil
	})
	if err == nil {
		return 0
	}
	// The kind, or the namespace, does not exist until the monitoring chart has been installed.
	if !meta.IsNoMatchError(err) && client.IgnoreNotFound(err) != nil {
		logf.FromContext(ctx).Error(err, "Failed to apply Longhorn metrics monitor")
	}
	return telemetryPolicyRetry
}
