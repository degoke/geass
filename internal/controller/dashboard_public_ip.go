package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// The dashboard at the public IP address (spec.dashboard.publicIP) is how a new cluster is opened before it has a domain:
// an issuer with Let's Encrypt's short-lived profile, a certificate for the address, the TLSStore that makes it Traefik's
// default certificate, and an IngressRoute for the address, with a redirect from plain HTTP. They all go away when the setting is off or the dashboard
// domain is verified. A TLSStore named default that Geass did not create is left alone, and the address is then not served.

// +kubebuilder:rbac:groups=cert-manager.io,resources=clusterissuers,verbs=delete
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=traefik.io,resources=ingressroutes;tlsstores;middlewares,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;nodes,verbs=get;list;watch

const (
	publicIPReasonReady       = "Ready"
	publicIPReasonWaiting     = "WaitingForCertManager"
	publicIPReasonNoAddress   = "NoPublicAddress"
	publicIPReasonIssuing     = "IssuingCertificate"
	publicIPReasonDomain      = "DomainVerified"
	publicIPReasonConflict    = "TLSStoreInUse"
	publicIPReasonNoTraefik   = "TraefikMissing"
	publicIPReasonApplyFailed = "ApplyFailed"

	// publicIPRetry is how soon to look again while the address is being set up, and publicIPRecheck while it is served:
	// the node's address can change, and a verified domain ends it.
	publicIPRetry   = 30 * time.Second
	publicIPRecheck = 10 * time.Minute
)

// publicIPOutcome is what the condition says, and the address that answers once it does.
type publicIPOutcome struct {
	status  metav1.ConditionStatus
	reason  string
	message string
	url     string
}

// reconcileDashboardPublicIP keeps the dashboard at the public IP address while the setting asks for it. Like the issuer,
// it is background work: a problem is written to the condition and retried, never held against the cluster's Ready.
func (r *GeassClusterReconciler) reconcileDashboardPublicIP(ctx context.Context, cluster *geassv1alpha1.GeassCluster, certManagerReady bool) time.Duration {
	log := logf.FromContext(ctx)
	outcome, requeue := r.ensureDashboardPublicIP(ctx, cluster, certManagerReady)
	if outcome == nil || outcome.reason == publicIPReasonDomain {
		if err := r.removeDashboardPublicIP(ctx, cluster); err != nil {
			log.Error(err, "Failed to remove the dashboard at the public IP address")
			requeue = shortestRequeue(requeue, publicIPRetry)
		}
	}
	if err := r.setDashboardPublicIPStatus(ctx, client.ObjectKeyFromObject(cluster), outcome); err != nil {
		log.Error(err, "Failed to record the dashboard at the public IP address", "cluster", cluster.Name)
		requeue = shortestRequeue(requeue, publicIPRetry)
	}
	return requeue
}

// ensureDashboardPublicIP creates what serves the address, and says how it is going. A nil outcome means the setting is off.
func (r *GeassClusterReconciler) ensureDashboardPublicIP(ctx context.Context, cluster *geassv1alpha1.GeassCluster, certManagerReady bool) (*publicIPOutcome, time.Duration) {
	if r.DashboardElsewhere || cluster.Spec.Dashboard.PublicIP == nil || !*cluster.Spec.Dashboard.PublicIP ||
		!geassv1alpha1.AddonEnabled(cluster.Spec.Addons.CertManager.Enabled) {
		return nil, 0
	}
	waiting := func(reason, message string) (*publicIPOutcome, time.Duration) {
		return &publicIPOutcome{status: metav1.ConditionFalse, reason: reason, message: message}, publicIPRetry
	}
	if config, err := r.platformConfig(ctx); err != nil {
		return waiting(publicIPReasonApplyFailed, "Could not read the platform configuration")
	} else if config != nil && platform.DashboardDomainReady(*config) {
		return &publicIPOutcome{status: metav1.ConditionFalse, reason: publicIPReasonDomain,
			message: "The dashboard has a verified domain, so it is not served at the public IP address"}, publicIPRecheck
	}
	if !certManagerReady {
		return waiting(publicIPReasonWaiting, "Waiting for cert-manager to be ready")
	}

	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes); err != nil {
		return waiting(publicIPReasonApplyFailed, "Could not list the nodes")
	}
	lookup := r.dashboardAddress
	if lookup == nil {
		lookup = func(ctx context.Context, nodes []corev1.Node) (string, error) {
			return platform.ClusterExternalIP(ctx, nodes, nil)
		}
	}
	address, _ := lookup(ctx, nodes.Items)
	ip := platform.DashboardPublicIPv4(address)
	if ip == "" {
		return waiting(publicIPReasonNoAddress, "The cluster has no public IPv4 address, so Let's Encrypt cannot reach it")
	}
	services := &corev1.ServiceList{}
	if err := r.List(ctx, services, client.InNamespace(platform.SystemNamespace),
		client.MatchingLabels{"app.kubernetes.io/component": "dashboard"}); err != nil || len(services.Items) == 0 {
		return waiting(publicIPReasonApplyFailed, "The dashboard Service was not found")
	}

	store := platform.DashboardPublicIPTLSStore(cluster.Name)
	switch theirs, err := r.foreignObject(ctx, store); {
	case meta.IsNoMatchError(err):
		return waiting(publicIPReasonNoTraefik, "Traefik's IngressRoute and TLSStore kinds are not installed")
	case err != nil:
		return waiting(publicIPReasonApplyFailed, "Could not read the TLSStore")
	case theirs:
		return waiting(publicIPReasonConflict, "A TLSStore named default that Geass did not create is in "+platform.SystemNamespace)
	}
	for _, desired := range []*unstructured.Unstructured{
		platform.ClusterIssuerIP(cluster.Name),
		platform.DashboardPublicIPCertificate(ip, cluster.Name),
		store,
		platform.DashboardPublicIPRoute(ip, services.Items[0].Name, cluster.Name),
		platform.DashboardPublicIPRedirect(cluster.Name),
		platform.DashboardPublicIPHTTPRoute(ip, services.Items[0].Name, cluster.Name),
	} {
		if err := r.applyManagedObject(ctx, desired); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to apply object", "kind", desired.GetKind(), "name", desired.GetName())
			if meta.IsNoMatchError(err) {
				return waiting(publicIPReasonNoTraefik, "Traefik's IngressRoute and TLSStore kinds are not installed")
			}
			return waiting(publicIPReasonApplyFailed, "Could not create the "+desired.GetKind()+" "+desired.GetName()+"; the operator log says why")
		}
	}

	certificate := &unstructured.Unstructured{}
	certificate.SetGroupVersionKind(platform.CertificateGVK)
	if err := r.Get(ctx, client.ObjectKey{Namespace: platform.SystemNamespace, Name: platform.DashboardPublicIPName}, certificate); err != nil {
		return waiting(publicIPReasonIssuing, "Waiting for the certificate")
	}
	// cert-manager's own message ("Issuing certificate as Secret does not exist") says nothing a person can act on.
	if status, _, found := platform.ClusterIssuerReadyCondition(certificate); !found || status != metav1.ConditionTrue {
		return waiting(publicIPReasonIssuing, "Waiting for Let's Encrypt to issue the certificate for "+ip+
			". Ports 80 and 443 must be open to the internet")
	}
	url := platform.DashboardPublicURL(ip)
	return &publicIPOutcome{status: metav1.ConditionTrue, reason: publicIPReasonReady, message: "The dashboard answers at " + url, url: url}, publicIPRecheck
}

// foreignObject reports whether an object of that name exists and was not created by Geass.
func (r *GeassClusterReconciler) foreignObject(ctx context.Context, desired *unstructured.Unstructured) (bool, error) {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(desired.GroupVersionKind())
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !platform.ClusterIssuerManaged(existing), nil
}

// applyManagedObject creates the object, or sets its spec and labels when Geass created it. One that Geass did not create
// is left as it is.
func (r *GeassClusterReconciler) applyManagedObject(ctx context.Context, desired *unstructured.Unstructured) error {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(desired.GroupVersionKind())
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil || !platform.ClusterIssuerManaged(existing) {
		return err
	}
	if equality.Semantic.DeepEqual(existing.Object["spec"], desired.Object["spec"]) &&
		equality.Semantic.DeepEqual(existing.GetLabels(), desired.GetLabels()) {
		return nil
	}
	existing.Object["spec"] = desired.Object["spec"]
	existing.SetLabels(desired.GetLabels())
	return r.Update(ctx, existing)
}

// removeDashboardPublicIP deletes what serves the address, when this cluster created it.
func (r *GeassClusterReconciler) removeDashboardPublicIP(ctx context.Context, cluster *geassv1alpha1.GeassCluster) error {
	targets := []struct {
		gvk       schema.GroupVersionKind
		namespace string
		name      string
	}{
		{platform.TraefikIngressRouteGVK, platform.SystemNamespace, platform.DashboardPublicIPName},
		{platform.TraefikIngressRouteGVK, platform.SystemNamespace, platform.DashboardPublicIPRedirectName},
		{platform.TraefikMiddlewareGVK, platform.SystemNamespace, platform.DashboardPublicIPRedirectName},
		{platform.TraefikTLSStoreGVK, platform.SystemNamespace, platform.TraefikDefaultTLSStore},
		{platform.CertificateGVK, platform.SystemNamespace, platform.DashboardPublicIPName},
		{platform.ClusterIssuerGVK, "", platform.ClusterIssuerIPName},
	}
	for _, target := range targets {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(target.gvk)
		err := r.Get(ctx, client.ObjectKey{Namespace: target.namespace, Name: target.name}, existing)
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !platform.ClusterIssuerManaged(existing) || platform.ClusterIssuerOwner(existing) != cluster.Name {
			continue
		}
		if err := r.Delete(ctx, existing); client.IgnoreNotFound(err) != nil {
			return err
		}
		logf.FromContext(ctx).Info("Deleted object of the dashboard at the public IP address", "kind", target.gvk.Kind, "name", target.name)
	}
	return nil
}

// setDashboardPublicIPStatus writes the condition and the address, only when they changed. A nil outcome removes both.
func (r *GeassClusterReconciler) setDashboardPublicIPStatus(ctx context.Context, key client.ObjectKey, outcome *publicIPOutcome) error {
	latest := &geassv1alpha1.GeassCluster{}
	if err := r.Get(ctx, key, latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		if outcome == nil {
			meta.RemoveStatusCondition(&latest.Status.Conditions, platform.ConditionDashboardPublicIP)
			latest.Status.DashboardPublicURL = ""
			return
		}
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type: platform.ConditionDashboardPublicIP, Status: outcome.status, Reason: outcome.reason, Message: outcome.message,
		})
		latest.Status.DashboardPublicURL = outcome.url
	})
	return client.IgnoreNotFound(err)
}
