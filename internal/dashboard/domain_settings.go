package dashboard

import (
	"context"
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

func normalizeDashboardExposure(raw string) geassv1alpha1.DashboardExposure {
	switch strings.TrimSpace(raw) {
	case string(geassv1alpha1.DashboardExposureCloudflareTunnel):
		return geassv1alpha1.DashboardExposureCloudflareTunnel
	default:
		return geassv1alpha1.DashboardExposureIngress
	}
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
		redirectDomainError("could not load platform config")
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
		redirectDomainError("could not save domain settings")
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
	if isHXRequest(r) || isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": result.State == "success", "state": result.State, "message": result.Message})
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
		return domainVerifyResult{State: "error", Message: "could not load platform config"}
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
		return domainVerifyResult{State: "error", Message: "could not reload platform config"}
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
		return domainVerifyResult{State: "error", Message: "could not save verification status"}
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
