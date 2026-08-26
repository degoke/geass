package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func (r *GeassPlatformConfigReconciler) reconcileDashboardDomain(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig) (platform.DashboardDomainReconcileResult, time.Duration) {
	rootDomain := platform.RootDomainFromConfig(*config)
	if rootDomain == "" {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "NoDomain",
			Message: "Dashboard domain is not configured",
		}, 0
	}

	if platform.DashboardDomainReady(*config) {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionTrue,
			Reason:  "Verified",
			Message: "DNS is configured for the dashboard domain",
		}, 0
	}

	if platform.DashboardExposureFromConfig(*config) == geassv1alpha1.DashboardExposureCloudflareTunnel {
		return r.reconcileTunnelDashboardDomain(ctx, config)
	}
	return r.reconcileIngressDashboardDomain(ctx, config, rootDomain)
}

func (r *GeassPlatformConfigReconciler) reconcileTunnelDashboardDomain(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig) (platform.DashboardDomainReconcileResult, time.Duration) {
	if strings.TrimSpace(config.Spec.TunnelCNAMETarget) == "" {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "PendingTunnel",
			Message: "Cloudflare tunnel ID is not configured",
		}, platform.RequeueAfterDomainVerify
	}
	dashboardHost := platform.DashboardHostFromRoot(platform.RootDomainFromConfig(*config))
	// A successful HTTPS probe proves that the public hostname resolves through
	// the tunnel and serves the dashboard. Cloudflare proxied records commonly
	// flatten the configured CNAME into A records, so the CNAME is not always
	// observable from the controller's resolver.
	if platform.ProbeDashboardURL(ctx, r.HTTPClient, platform.DashboardURLFromRoot(platform.RootDomainFromConfig(*config))) {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionTrue,
			Reason:  "Verified",
			Message: "Dashboard HTTPS endpoint is reachable",
		}, 0
	}
	if ok, message := platform.VerifyHostCNAMETarget(ctx, dashboardHost, config.Spec.TunnelCNAMETarget); !ok {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "PendingDNS",
			Message: message,
		}, platform.RequeueAfterDomainVerify
	}
	return platform.DashboardDomainReconcileResult{
		Status:  metav1.ConditionFalse,
		Reason:  "PendingHTTPS",
		Message: "Dashboard HTTPS endpoint is not reachable yet",
	}, platform.RequeueAfterDomainVerify
}

func (r *GeassPlatformConfigReconciler) reconcileIngressDashboardDomain(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig, rootDomain string) (platform.DashboardDomainReconcileResult, time.Duration) {
	dashboardHost := platform.DashboardHostFromRoot(rootDomain)

	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes); err != nil {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "Error",
			Message: "could not list cluster nodes",
		}, platform.RequeueAfterDomainVerify
	}
	externalIP, err := platform.ClusterExternalIP(ctx, nodes.Items, r.HTTPClient)
	if err != nil {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "PendingDNS",
			Message: err.Error(),
		}, platform.RequeueAfterDomainVerify
	}
	if ok, message := platform.VerifyHostResolvesToIP(ctx, dashboardHost, externalIP); !ok {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "PendingDNS",
			Message: message,
		}, platform.RequeueAfterDomainVerify
	}

	serviceName, err := r.dashboardServiceName(ctx)
	if err != nil {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "Error",
			Message: err.Error(),
		}, platform.RequeueAfterDomainVerify
	}
	if err := r.reconcileDashboardIngress(ctx, dashboardHost, serviceName); err != nil {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "Error",
			Message: err.Error(),
		}, platform.RequeueAfterDomainVerify
	}
	if !platform.ProbeDashboardURL(ctx, r.HTTPClient, platform.DashboardURLFromRoot(rootDomain)) {
		return platform.DashboardDomainReconcileResult{
			Status:  metav1.ConditionFalse,
			Reason:  "PendingHTTPS",
			Message: "Dashboard HTTPS endpoint is not reachable yet",
		}, platform.RequeueAfterDomainVerify
	}
	return platform.DashboardDomainReconcileResult{
		Status:  metav1.ConditionTrue,
		Reason:  "Verified",
		Message: "DNS is configured for the dashboard domain",
	}, 0
}

func (r *GeassPlatformConfigReconciler) dashboardServiceName(ctx context.Context) (string, error) {
	services := &corev1.ServiceList{}
	if err := r.List(ctx, services, client.InNamespace(platform.SystemNamespace), client.MatchingLabels{
		"app.kubernetes.io/component": "dashboard",
	}); err != nil {
		return "", fmt.Errorf("dashboard service was not found")
	}
	if len(services.Items) == 0 {
		return "", fmt.Errorf("dashboard service was not found")
	}
	return services.Items[0].Name, nil
}

func (r *GeassPlatformConfigReconciler) reconcileDashboardIngress(ctx context.Context, host, serviceName string) error {
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      platform.DashboardIngressName,
			Namespace: platform.SystemNamespace,
			Annotations: map[string]string{
				"cert-manager.io/cluster-issuer":                   "letsencrypt-prod",
				"traefik.ingress.kubernetes.io/router.entrypoints": "websecure",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("traefik"),
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: serviceName,
									Port: networkingv1.ServiceBackendPort{Number: 8082},
								},
							},
						}},
					},
				},
			}},
			TLS: []networkingv1.IngressTLS{{
				Hosts:      []string{host},
				SecretName: platform.DashboardIngressName + "-tls",
			}},
		},
	}
	existing := &networkingv1.Ingress{}
	err := r.Get(ctx, client.ObjectKeyFromObject(ingress), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, ingress)
	}
	if err != nil {
		return err
	}
	existing.Annotations = ingress.Annotations
	existing.Spec = ingress.Spec
	return r.Update(ctx, existing)
}
