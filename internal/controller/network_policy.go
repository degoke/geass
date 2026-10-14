package controller

import (
	"context"
	"maps"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

// telemetryPolicyRetry is how soon to look again for a monitoring or logging namespace that a chart is
// still creating.
const telemetryPolicyRetry = 30 * time.Second

// applyNetworkPolicy makes the cluster's copy of a policy match the wanted one.
func applyNetworkPolicy(ctx context.Context, c client.Client, desired *networkingv1.NetworkPolicy) error {
	current := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, c, current, func() error {
		current.Labels = desired.Labels
		current.Spec = desired.Spec
		return nil
	})
	return err
}

func deleteNetworkPolicy(ctx context.Context, c client.Client, name, namespace string) error {
	return client.IgnoreNotFound(c.Delete(ctx, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}))
}

// ensureNamespaceLabels adds the labels a namespace should carry when it lacks them. The isolation
// policy selects a project's namespaces by label, so a namespace without its project label would be
// cut off from its own project.
func ensureNamespaceLabels(ctx context.Context, c client.Client, name string, want map[string]string) error {
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, client.ObjectKey{Name: name}, ns); err != nil {
		return err
	}
	missing := false
	for key, value := range want {
		if ns.Labels[key] != value {
			missing = true
			break
		}
	}
	if !missing {
		return nil
	}
	patch := client.MergeFrom(ns.DeepCopy())
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	maps.Copy(ns.Labels, want)
	return c.Patch(ctx, ns, patch)
}

// reconcileProjectNetwork applies the network policies of one project environment. nodes describes the
// cluster's machines for a project that keeps its services away from them, and is nil for one that does not.
func (r *GeassProjectReconciler) reconcileProjectNetwork(ctx context.Context, project *geassv1alpha1.GeassProject, namespace string, nodes *platform.NodeNetwork) error {
	if err := ensureNamespaceLabels(ctx, r.Client, namespace, map[string]string{platform.LabelProject: project.Name}); err != nil {
		return err
	}
	if geassv1alpha1.ProjectNetworkIsolation(project) == geassv1alpha1.NetworkIsolationProject {
		if err := applyNetworkPolicy(ctx, r.Client, platform.ProjectIsolationPolicy(project, namespace)); err != nil {
			return err
		}
	} else if err := deleteNetworkPolicy(ctx, r.Client, platform.ProjectIsolationPolicyName, namespace); err != nil {
		return err
	}
	return applyNetworkPolicy(ctx, r.Client, platform.CloudMetadataPolicy(project.Name, namespace, nodes))
}

// projectsForNetworkPolicy maps a policy the operator owns back to its project, so that a policy that
// is changed or deleted by hand is put back.
func (r *GeassProjectReconciler) projectsForNetworkPolicy(ctx context.Context, obj client.Object) []ctrl.Request {
	if !slices.Contains(platform.ManagedNetworkPolicyNames(), obj.GetName()) {
		return nil
	}
	project := obj.GetLabels()[platform.LabelProject]
	if project == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: project, Namespace: platform.SystemNamespace}}}
}

// reconcileTelemetryIsolation closes the metrics and log stores to everything but the platform. They
// have no login and hold every project's data, so without it any project's pod could read the rest. It
// is background work: a problem is logged and retried, and never holds the cluster back. It returns
// how soon to look again, or zero when there is nothing left to do.
func (r *GeassClusterReconciler) reconcileTelemetryIsolation(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	log := logf.FromContext(ctx)
	wanted := make([]*networkingv1.NetworkPolicy, 0, 2)
	if geassv1alpha1.AddonEnabled(cluster.Spec.Addons.Monitoring.Enabled) {
		wanted = append(wanted, platform.MetricsStorePolicy())
	}
	if geassv1alpha1.LoggingEnabled(cluster.Spec.Addons) {
		wanted = append(wanted, platform.LogStorePolicy())
	}
	var retry time.Duration
	for _, policy := range wanted {
		if err := r.Get(ctx, client.ObjectKey{Name: policy.Namespace}, &corev1.Namespace{}); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to look up namespace for network policy", "namespace", policy.Namespace)
			}
			// The chart that creates the namespace has not run yet.
			retry = telemetryPolicyRetry
			continue
		}
		if err := applyNetworkPolicy(ctx, r.Client, policy); err != nil {
			log.Error(err, "Failed to apply network policy", "namespace", policy.Namespace, "name", policy.Name)
			retry = telemetryPolicyRetry
		}
	}
	return retry
}
