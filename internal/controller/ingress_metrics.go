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

// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete

// ingressMetricsMonitorName is the monitor that has Prometheus scrape the ingress controller.
const ingressMetricsMonitorName = "geass-ingress"

// ingressMetricsMonitor has Prometheus scrape the ingress controller (Traefik, which k3s installs).
// Every service's request rate is measured there, so without it the request chart of every service stays
// empty: Traefik serves its metrics on a port named "metrics", but nothing in the monitoring chart asks for them.
// On a cluster whose ingress controller is something else the selector matches no pod, which is harmless.
func ingressMetricsMonitor() *monitoringv1.PodMonitor {
	port := "metrics"
	return &monitoringv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ingressMetricsMonitorName,
			Namespace: platform.MonitoringTargetNS,
			Labels:    map[string]string{platform.LabelManagedBy: platform.ManagedByValue},
		},
		Spec: monitoringv1.PodMonitorSpec{
			NamespaceSelector:   monitoringv1.NamespaceSelector{MatchNames: []string{"kube-system"}},
			Selector:            metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "traefik"}},
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{{Port: &port, Path: "/metrics"}},
		},
	}
}

// reconcileIngressMetrics keeps the ingress monitor in place while monitoring is on. It is background work
// like the other add-on extras: a problem is logged and retried, never held against the cluster. It
// returns how soon to look again, or zero when there is nothing left to do.
func (r *GeassClusterReconciler) reconcileIngressMetrics(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	if !geassv1alpha1.AddonEnabled(cluster.Spec.Addons.Monitoring.Enabled) {
		return 0
	}
	desired := ingressMetricsMonitor()
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
		logf.FromContext(ctx).Error(err, "Failed to apply ingress metrics monitor")
	}
	return telemetryPolicyRetry
}
