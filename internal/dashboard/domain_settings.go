package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

type domainVerifyResult struct {
	State      string
	Message    string
	TunnelMode bool
}

func selectOptionsWithValue(options []SelectOption, value string) []SelectOption {
	out := make([]SelectOption, len(options))
	for i, option := range options {
		out[i] = option
		out[i].Selected = option.Value == value
	}
	return out
}

func normalizeDashboardExposure(raw string) geassv1alpha1.DashboardExposure {
	switch strings.TrimSpace(raw) {
	case string(geassv1alpha1.DashboardExposureCloudflareTunnel):
		return geassv1alpha1.DashboardExposureCloudflareTunnel
	default:
		return geassv1alpha1.DashboardExposureIngress
	}
}

func tunnelIDForForm(stored string) string {
	stored = strings.TrimSpace(stored)
	if strings.HasSuffix(strings.ToLower(stored), ".cfargotunnel.com") {
		return stored[:len(stored)-len(".cfargotunnel.com")]
	}
	return stored
}

func domainPendingMessage(tunnelMode bool) string {
	if tunnelMode {
		return "Add the CNAME in Cloudflare, then wait for DNS."
	}
	return "Add the DNS record at your registrar, then wait for DNS."
}

func domainVerifyIdleHTML(tunnelMode bool) string {
	return `<p class="text-secondary text-sm">` + template.HTMLEscapeString(domainPendingMessage(tunnelMode)) + `</p>` +
		domainVerifyButtonHTML()
}

func domainVerifyButtonHTML() string {
	return `<form class="row-wrap mt-2" method="post" action="/settings/domain/verify" data-native-submit><button class="btn btn-primary" type="submit">Verify</button></form>`
}

func domainVerifyResultHTML(result domainVerifyResult) string {
	switch result.State {
	case "success":
		return `<div id="domain-verify-status">` + Alert("success", result.Message) + `</div>`
	case "pending":
		message := result.Message
		if message == "" {
			message = "Checking DNS..."
		}
		return `<div id="domain-verify-status" hx-post="/settings/domain/verify" hx-trigger="every 5s" hx-swap="outerHTML">` +
			`<p class="text-secondary text-sm">` + template.HTMLEscapeString(message) + `</p>` +
			domainVerifyButtonHTML() +
			`</div>`
	default:
		return `<div id="domain-verify-status">` + Alert("error", result.Message) +
			domainVerifyButtonHTML() + `</div>`
	}
}

func domainIngressDNSCard(dashboardHost, externalIP string, ipErr error) string {
	if ipErr != nil || externalIP == "" {
		return Card(`<p class="text-secondary text-sm">Could not detect this server's public IP. Ensure nodes expose an external address, or that outbound network access is available.</p>`)
	}
	return Card(`<p class="text-secondary text-sm mb-2">Add this A record at your registrar for <code>` + template.HTMLEscapeString(dashboardHost) + `</code>:</p>` +
		`<div class="domain-dns-record"><div><span class="meta-label">Type</span><code>A</code></div>` +
		`<div><span class="meta-label">Name</span><code>` + template.HTMLEscapeString(platform.DashboardSubdomainLabel) + `</code></div>` +
		`<div><span class="meta-label">Value</span><code class="copy-value">` + template.HTMLEscapeString(externalIP) + `</code></div></div>`)
}

func domainCloudflareDNSCard(dashboardHost, tunnelTarget string) string {
	if strings.TrimSpace(tunnelTarget) == "" {
		return Card(`<p class="text-secondary text-sm">Save your Cloudflare tunnel ID above to see DNS instructions.</p>`)
	}
	return Card(`<p class="text-secondary text-sm mb-2">Add this CNAME record in Cloudflare for <code>` + template.HTMLEscapeString(dashboardHost) + `</code>:</p>` +
		`<div class="domain-dns-record"><div><span class="meta-label">Type</span><code>CNAME</code></div>` +
		`<div><span class="meta-label">Name</span><code>` + template.HTMLEscapeString(platform.DashboardSubdomainLabel) + `</code></div>` +
		`<div><span class="meta-label">Target</span><code class="copy-value">` + template.HTMLEscapeString(tunnelTarget) + `</code></div></div>` +
		`<p class="text-secondary text-sm mt-2">Run cloudflared with your tunnel config and route traffic to <code>http://127.0.0.1:8082</code>. Use <code>protocol: http2</code> if QUIC on port 7844 is blocked.</p>`)
}

func (s *Server) handlePlatformDomainSave(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/domain") || !parseFormOrRedirect(w, r, "/settings/domain") {
		return
	}
	redirectDomainError := func(message string) {
		redirectProbe(w, r, "/settings/domain", "error", message)
	}
	rootDomain := platform.NormalizeRootDomainInput(r.FormValue("domain"))
	if rootDomain == "" {
		redirectDomainError("enter your main domain, like example.com")
		return
	}
	exposure := normalizeDashboardExposure(r.FormValue("exposure"))
	tunnelTarget := platform.NormalizeTunnelCNAMETarget(r.FormValue("tunnelCNAMETarget"))
	if exposure == geassv1alpha1.DashboardExposureCloudflareTunnel && tunnelTarget == "" {
		redirectDomainError("enter your Cloudflare tunnel ID from cloudflared tunnel list")
		return
	}
	config := &geassv1alpha1.GeassPlatformConfig{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: systemNamespace}, config)
	creating := apierrors.IsNotFound(err)
	if creating {
		config = &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: systemNamespace}}
	} else if err != nil {
		redirectDomainError("could not load platform config: " + err.Error())
		return
	}
	config.Spec.RootDomain = rootDomain
	config.Spec.DashboardURL = platform.DashboardURLFromRoot(rootDomain)
	config.Spec.DashboardExposure = exposure
	config.Spec.TunnelCNAMETarget = tunnelTarget
	if creating {
		err = s.Client.Create(r.Context(), config)
	} else {
		err = s.Client.Update(r.Context(), config)
	}
	if err != nil {
		redirectDomainError("could not save domain settings: " + err.Error())
		return
	}
	redirect(w, r, "/settings/domain")
}

// handlePlatformDomainVerify probes the configured public endpoint directly.
func (s *Server) handlePlatformDomainVerify(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/domain") {
		return
	}
	result := s.verifyDashboardDomain(r.Context())
	html := domainVerifyResultHTML(result)
	if isHXRequest(r) {
		s.render(w, html)
		return
	}
	switch result.State {
	case "success":
		redirectProbe(w, r, "/settings/domain", "success", "")
	case "pending":
		redirectProbe(w, r, "/settings/domain", "warning", result.Message)
	default:
		redirectProbe(w, r, "/settings/domain", "error", result.Message)
	}
}

func (s *Server) verifyDashboardDomain(ctx context.Context) domainVerifyResult {
	readiness, err := s.platformReadiness(ctx)
	if err != nil {
		return domainVerifyResult{State: "error", Message: err.Error()}
	}
	if readiness.DashboardURL == "" {
		return domainVerifyResult{State: "error", Message: "save your domain first"}
	}

	message, result := s.verifyDashboardURL(ctx, readiness.DashboardURL)
	if result != "success" {
		if message == "" {
			message = "Dashboard HTTPS endpoint is not reachable yet"
		}
		return domainVerifyResult{State: "pending", Message: message}
	}

	latest := &geassv1alpha1.GeassPlatformConfig{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: systemNamespace}, latest); err != nil {
		return domainVerifyResult{State: "error", Message: fmt.Sprintf("could not reload platform config: %v", err)}
	}
	if latest.Generation != readiness.Config.Generation {
		return domainVerifyResult{State: "pending", Message: "Domain settings changed; checking again..."}
	}
	latest.Status.Conditions = platform.SetConditionForGeneration(
		latest.Status.Conditions,
		platform.ConditionDashboardDomainReady,
		metav1.ConditionTrue,
		"Verified",
		"Dashboard HTTPS endpoint is reachable",
		latest.Generation,
	)
	if err := s.Client.Status().Update(ctx, latest); err != nil {
		return domainVerifyResult{State: "error", Message: fmt.Sprintf("could not save verification status: %v", err)}
	}
	return domainVerifyResult{State: "success", Message: "Dashboard HTTPS endpoint is reachable"}
}

func (s *Server) ensureDashboardDomainVerified(ctx context.Context, readiness platformReadiness) bool {
	if platform.DashboardDomainReady(readiness.Config) {
		return true
	}
	if readiness.DashboardURL == "" {
		return false
	}
	if _, result := s.verifyDashboardURL(ctx, readiness.DashboardURL); result != "success" {
		return false
	}

	latest := &geassv1alpha1.GeassPlatformConfig{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: systemNamespace}, latest); err != nil {
		return false
	}
	latest.Status.Conditions = platform.SetConditionForGeneration(
		latest.Status.Conditions,
		platform.ConditionDashboardDomainReady,
		metav1.ConditionTrue,
		"Verified",
		"Dashboard HTTPS endpoint is reachable",
		latest.Generation,
	)
	return s.Client.Status().Update(ctx, latest) == nil
}

func (s *Server) clusterExternalIP(ctx context.Context) (string, error) {
	if s.Kube != nil {
		nodes, err := s.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", fmt.Errorf("could not list cluster nodes")
		}
		return platform.ClusterExternalIP(ctx, nodes.Items, s.HTTPClient)
	}
	return platform.ClusterExternalIP(ctx, nil, s.HTTPClient)
}
