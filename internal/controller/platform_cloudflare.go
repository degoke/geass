package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloudflare"
	"github.com/degoke/geass/pkg/platform"
)

const cloudflaredImage = "cloudflare/cloudflared:2024.10.0"

// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

func (r *GeassPlatformConfigReconciler) reconcileCloudflare(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig) (time.Duration, error) {
	logger := log.FromContext(ctx)
	if !platform.CloudflareConfigured(*config) {
		if err := r.deleteCloudflared(ctx); err != nil {
			return platform.RequeueAfterDomainVerify, err
		}
		return 0, nil
	}
	if platform.DashboardExposureFromConfig(*config) != geassv1alpha1.DashboardExposureCloudflareTunnel {
		if err := r.deleteCloudflared(ctx); err != nil {
			return platform.RequeueAfterDomainVerify, err
		}
		message := "Cloudflare API connected; dashboard DNS is managed via Cloudflare when a domain is saved"
		if platform.RootDomainFromConfig(*config) != "" {
			if err := r.ensureCloudflareDashboardIngressDNS(ctx, config); err != nil {
				logger.Info("Could not ensure Cloudflare dashboard DNS record", "error", err)
				_ = r.setCloudflareReady(ctx, config, metav1.ConditionFalse, "DNSError", "Could not create dashboard DNS record in Cloudflare")
				return platform.RequeueAfterDomainVerify, err
			}
			message = "Cloudflare API connected; dashboard DNS points at cluster ingress"
		}
		_ = r.setCloudflareReady(ctx, config, metav1.ConditionTrue, "Connected", message)
		return 0, nil
	}

	creds, err := r.loadCloudflareCredentials(ctx, config)
	if err != nil {
		return platform.RequeueAfterDomainVerify, err
	}
	if !creds.Valid() {
		_ = r.setCloudflareReady(ctx, config, metav1.ConditionFalse, "CredentialsIncomplete", "Cloudflare API token, account ID, and zone ID are required")
		return platform.RequeueAfterDomainVerify, nil
	}

	// What would be sent is worked out first: it needs nothing from Cloudflare. A pass that would send what
	// the last one did is skipped, so a tunnel that is in sync costs no Cloudflare requests and no writes.
	rules, dnsHosts, err := r.cloudflareIngressRules(ctx, config)
	if err != nil {
		return platform.RequeueAfterDomainVerify, err
	}
	fingerprint := tunnelFingerprint(creds, rules, dnsHosts)
	if wait, inSync := r.tunnel.inSync(fingerprint, r.clock()); inSync {
		return wait, nil
	}
	r.tunnel.forget()

	cf := &cloudflare.Client{AccountID: creds.AccountID, APIToken: creds.APIToken, HTTP: r.HTTPClient}
	if err := cf.Validate(ctx); err != nil {
		_ = r.setCloudflareReady(ctx, config, metav1.ConditionFalse, "CredentialsInvalid", cloudflare.TokenValidationMessage(err))
		return platform.RequeueAfterDomainVerify, err
	}

	tunnel, err := cf.FindOrCreateTunnel(ctx, platform.CloudflareTunnelName)
	if err != nil {
		_ = r.setCloudflareReady(ctx, config, metav1.ConditionFalse, "TunnelError", "Could not create Cloudflare tunnel")
		return platform.RequeueAfterDomainVerify, err
	}
	tunnelCNAME := cloudflare.TunnelCNAMETarget(tunnel.ID)

	token, err := cf.TunnelToken(ctx, tunnel.ID)
	if err != nil {
		_ = r.setCloudflareReady(ctx, config, metav1.ConditionFalse, "TunnelTokenError", "Could not fetch Cloudflare tunnel token")
		return platform.RequeueAfterDomainVerify, err
	}
	if err := r.ensureCloudflaredTokenSecret(ctx, token); err != nil {
		return platform.RequeueAfterDomainVerify, err
	}
	if err := r.ensureCloudflaredDeployment(ctx); err != nil {
		return platform.RequeueAfterDomainVerify, err
	}

	if err := cf.PutIngressConfiguration(ctx, tunnel.ID, rules); err != nil {
		_ = r.setCloudflareReady(ctx, config, metav1.ConditionFalse, "TunnelConfigError", "Could not update Cloudflare tunnel routes")
		return platform.RequeueAfterDomainVerify, err
	}
	dnsComplete := true
	for _, host := range dnsHosts {
		if err := cf.EnsureCNAMERecord(ctx, creds.ZoneID, host, tunnelCNAME); err != nil {
			logger.Info("Could not ensure Cloudflare DNS record", "host", host, "error", err)
			dnsComplete = false
		}
	}

	if err := r.patchCloudflareTunnelSpec(ctx, config, tunnel.ID, tunnelCNAME); err != nil {
		return platform.RequeueAfterDomainVerify, err
	}
	_ = r.setCloudflareReady(ctx, config, metav1.ConditionTrue, "Ready", "Cloudflare tunnel connector is running")
	if !dnsComplete {
		// A record that could not be made is tried again soon, not at the next resync.
		return platform.RequeueAfterDomainVerify, nil
	}
	r.tunnel.synced(fingerprint, r.clock())
	return platform.RequeueAfterCloudflareResync, nil
}

// tunnelSync remembers the last tunnel configuration that was sent to Cloudflare in full.
type tunnelSync struct {
	mu          sync.Mutex
	fingerprint string
	at          time.Time
}

// inSync reports whether the configuration is the one last sent and still fresh, and how long until it
// should be checked again.
func (t *tunnelSync) inSync(fingerprint string, now time.Time) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fingerprint == "" || t.fingerprint != fingerprint {
		return 0, false
	}
	remaining := platform.RequeueAfterCloudflareResync - now.Sub(t.at)
	if remaining <= 0 {
		return 0, false
	}
	return remaining, true
}

func (t *tunnelSync) synced(fingerprint string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fingerprint, t.at = fingerprint, now
}

func (t *tunnelSync) forget() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fingerprint = ""
}

// tunnelFingerprint identifies everything a tunnel sync sends: whose account and zone it is for, the
// token (hashed, so nothing secret is kept), and every route and DNS name.
func tunnelFingerprint(creds platform.CloudflareCredentials, rules []cloudflare.IngressRule, dnsHosts []string) string {
	hash := sha256.New()
	_ = json.NewEncoder(hash).Encode(struct {
		Account, Zone, Token string
		Rules                []cloudflare.IngressRule
		Hosts                []string
	}{creds.AccountID, creds.ZoneID, creds.APIToken, rules, dnsHosts})
	return hex.EncodeToString(hash.Sum(nil))
}

func (r *GeassPlatformConfigReconciler) ensureCloudflareDashboardIngressDNS(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig) error {
	rootDomain := platform.RootDomainFromConfig(*config)
	if rootDomain == "" {
		return nil
	}
	dashboardHost := platform.DashboardHostFromConfig(*config)
	if dashboardHost == "" {
		return nil
	}
	creds, err := r.loadCloudflareCredentials(ctx, config)
	if err != nil || !creds.Valid() {
		return fmt.Errorf("cloudflare credentials are incomplete")
	}
	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes); err != nil {
		return err
	}
	externalIP, err := platform.ClusterExternalIP(ctx, nodes.Items, r.HTTPClient)
	if err != nil {
		return err
	}
	cf := &cloudflare.Client{AccountID: creds.AccountID, APIToken: creds.APIToken, HTTP: r.HTTPClient}
	return cf.EnsureARecord(ctx, creds.ZoneID, dashboardHost, externalIP, true)
}

func (r *GeassPlatformConfigReconciler) loadCloudflareCredentials(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig) (platform.CloudflareCredentials, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: config.Spec.CloudflareConnectionRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return platform.CloudflareCredentials{}, err
	}
	zoneID := platform.ResolveCloudflareZoneID(*config, secret)
	if zoneID != "" && strings.TrimSpace(config.Spec.CloudflareZoneID) == "" {
		latest := &geassv1alpha1.GeassPlatformConfig{}
		if err := r.Get(ctx, client.ObjectKey{Name: config.Name, Namespace: config.Namespace}, latest); err == nil {
			if strings.TrimSpace(latest.Spec.CloudflareZoneID) == "" {
				latest.Spec.CloudflareZoneID = zoneID
				if err := r.Update(ctx, latest); err == nil {
					config.Spec.CloudflareZoneID = zoneID
				}
			} else {
				zoneID = latest.Spec.CloudflareZoneID
				config.Spec.CloudflareZoneID = zoneID
			}
		}
	}
	return platform.CloudflareCredentialsFromSecret(secret, zoneID), nil
}

func (r *GeassPlatformConfigReconciler) cloudflareIngressRules(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig) ([]cloudflare.IngressRule, []string, error) {
	rules := []cloudflare.IngressRule{}
	dnsHosts := []string{}

	rootDomain := platform.RootDomainFromConfig(*config)
	// The dashboard's own route needs a dashboard Service that has pods behind it, which it does not when the dashboard
	// runs elsewhere. The services' routes below are unaffected.
	if rootDomain != "" && !r.DashboardElsewhere {
		dashboardHost := platform.DashboardHostFromRoot(rootDomain)
		serviceName, err := r.dashboardServiceName(ctx)
		if err != nil {
			return nil, nil, err
		}
		rules = append(rules, cloudflare.IngressRule{
			Hostname: dashboardHost,
			Service:  fmt.Sprintf("http://%s.%s.svc.cluster.local:8085", serviceName, platform.SystemNamespace),
		})
		dnsHosts = append(dnsHosts, dashboardHost)
	}

	apps := &geassv1alpha1.GeassAppList{}
	if err := r.List(ctx, apps, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, nil, err
	}
	for _, app := range apps.Items {
		host := strings.TrimSpace(app.Spec.Ingress.Host)
		if host == "" {
			continue
		}
		wsNS, err := resourceNamespace(app.Spec.Project, string(app.Spec.Environment))
		if err != nil {
			continue
		}
		port := app.Spec.Port
		if port == 0 {
			port = 8080
		}
		path := app.Spec.Ingress.Path
		if path == "" {
			path = "/"
		}
		service := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", app.Name, wsNS, port)
		// The tunnel reaches a service without going through Traefik, so one that sleeps when idle has to be
		// sent to the proxy that wakes it. The proxy tells services apart by host, which the tunnel keeps.
		if r.sleepsWhenIdle(ctx, &app) {
			service = fmt.Sprintf("http://%s:%d", platform.ServerlessInterceptorHost, platform.ServerlessInterceptorPort)
		}
		rules = append(rules, cloudflare.IngressRule{
			Hostname: host,
			Path:     path,
			Service:  service,
		})
		dnsHosts = append(dnsHosts, host)
	}
	return rules, dnsHosts, nil
}

// sleepsWhenIdle is whether the service is put behind the wake proxy. It is worked out from the service's own
// settings and from the add-on being installed, as the service's reconcile does, and not from the proxy's
// object for it: that is made a moment after the setting changes, and a route decided before it exists would
// stay until the next resync, sending requests to a service with no copies.
func (r *GeassPlatformConfigReconciler) sleepsWhenIdle(ctx context.Context, app *geassv1alpha1.GeassApp) bool {
	if !scaleToZeroRequested(app) || app.Spec.Autoscaling != nil {
		return false
	}
	installed, err := httpScaledObjectsServed(ctx, r.Client)
	return err == nil && installed
}

func (r *GeassPlatformConfigReconciler) patchCloudflareTunnelSpec(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig, tunnelID, tunnelCNAME string) error {
	latest := &geassv1alpha1.GeassPlatformConfig{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(config), latest); err != nil {
		return err
	}
	if latest.Spec.TunnelCNAMETarget != tunnelCNAME {
		latest.Spec.TunnelCNAMETarget = tunnelCNAME
		if err := r.Update(ctx, latest); err != nil {
			return err
		}
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.CloudflareTunnelID = tunnelID
	})
	return err
}

func (r *GeassPlatformConfigReconciler) setCloudflareReady(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig, status metav1.ConditionStatus, reason, message string) error {
	latest := &geassv1alpha1.GeassPlatformConfig{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(config), latest); err != nil {
		return err
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Conditions = platform.SetConditionForGeneration(
			latest.Status.Conditions,
			platform.ConditionCloudflareReady,
			status,
			reason,
			message,
			latest.Generation,
		)
	})
	return err
}

func (r *GeassPlatformConfigReconciler) ensureCloudflaredTokenSecret(ctx context.Context, token string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.CloudflaredTokenSecretName, Namespace: platform.SystemNamespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data["token"] = []byte(token)
		secret.Type = corev1.SecretTypeOpaque
		return nil
	})
	return err
}

func (r *GeassPlatformConfigReconciler) ensureCloudflaredDeployment(ctx context.Context) error {
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: platform.CloudflaredDeploymentName, Namespace: platform.SystemNamespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		deploy.Labels = map[string]string{
			platform.K8sLabelAppName:      "geass",
			"app.kubernetes.io/component": "cloudflared",
		}
		replicas := int32(1)
		deploy.Spec.Replicas = &replicas
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": platform.CloudflaredDeploymentName}}
		deploy.Spec.Template.Labels = map[string]string{"app": platform.CloudflaredDeploymentName}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "cloudflared",
			Image: cloudflaredImage,
			Args:  []string{"tunnel", "--no-autoupdate", "run"},
			Env: []corev1.EnvVar{{
				Name: "TUNNEL_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: platform.CloudflaredTokenSecretName},
						Key:                  "token",
					},
				},
			}},
		}}
		return nil
	})
	return err
}

func (r *GeassPlatformConfigReconciler) deleteCloudflared(ctx context.Context) error {
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: platform.CloudflaredDeploymentName, Namespace: platform.SystemNamespace}}
	if err := r.Delete(ctx, deploy); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platform.CloudflaredTokenSecretName, Namespace: platform.SystemNamespace}}
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
