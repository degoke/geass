package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

// An organization's own identity provider. The platform's provider says who is in which organization; an organization that
// would rather use its own (its Okta, its Entra ID) is given one, and its people sign in to it from a link that names it. What
// that provider says of a person is all that is asked: it maps one claim to a role, and the person works in this organization,
// in all of it, with that role. It cannot make anyone an administrator of the platform, or put them in another organization,
// because the organization's provider is not the one that decides those.
//
// It is set up by a platform administrator, who tests it before it is turned on. Saving, turning it on or off, or removing it
// ends the sessions that were made through it.

const (
	orgIdentityPrefix = "org-sso:"
	// orgIdPSecretPrefix names the Secret that holds an organization's client secret: org-idp-<organization>.
	orgIdPSecretPrefix = "org-idp-"
	// annotationIdPChanged is when the provider of an organization was last changed, in Unix nanoseconds. A session is made
	// for one state of it and counts only while that is still the state.
	annotationIdPChanged = "geass.dev/idp-changed"
	// maxOrgProviders bounds how many discovery documents are kept for organizations' providers.
	maxOrgProviders = 64
)

// errOrgSignIn is why a sign-in to an organization cannot start: there is no such organization, or it has no provider that is
// turned on. It does not say which, so that who has one is not something to be found by asking.
var errOrgSignIn = errors.New("that organization cannot be signed in to")

func orgIdPSecretName(org string) string { return orgIdPSecretPrefix + org }

// orgIdentity is the identity of a person who signed in through an organization's provider: it names the organization and the
// state of its provider, so that the session ends when either changes, and the person, as the provider names them.
func orgIdentity(org, state, key string) string {
	return orgIdentityPrefix + org + ":" + state + ":" + strings.TrimPrefix(key, "sso:")
}

// parseOrgIdentity is the organization and the provider state an identity was made for, and false for an identity of anyone
// else.
func parseOrgIdentity(identity string) (org, state string, ok bool) {
	rest, found := strings.CutPrefix(identity, orgIdentityPrefix)
	if !found {
		return "", "", false
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// idpState is what the sessions made through the provider of an organization are tied to: what it is set up as, and when it
// was last changed. It does not include the client secret, which is read only when somebody signs in; a new secret is saved
// as a change, which ends the sessions made with the old one.
func idpState(org *geassv1alpha1.GeassOrganization) string {
	if org.Spec.IdP == nil {
		return ""
	}
	raw, _ := json.Marshal(struct {
		Spec    geassv1alpha1.GeassOrganizationIdPSpec
		Changed string
	}{*org.Spec.IdP, org.Annotations[annotationIdPChanged]})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}

// orgIdPSettings is the sign-in setup of an organization's provider, in the shape the rest of sign-in uses. The session length
// and how often people are checked again are the platform's.
func (s *Server) orgIdPSettings(ctx context.Context, org *geassv1alpha1.GeassOrganization) (ssoSettings, error) {
	idp := org.Spec.IdP
	if idp == nil {
		return ssoSettings{}, errOrgSignIn
	}
	config, err := s.platformConfig(ctx)
	if err != nil {
		return ssoSettings{}, err
	}
	base := ssoSettingsOf(config)
	settings := ssoSettings{
		Mode: platform.AuthModeExternal, DisplayName: strings.TrimSpace(idp.DisplayName), SessionMinutes: base.SessionMinutes,
		RevokeTokensOnLogout: base.RevokeTokensOnLogout, DashboardURL: base.DashboardURL, TenancyOn: true,
		RefreshMinutes: base.RefreshMinutes, StaleGrace: base.StaleGrace,
		Config: oidcauth.Config{
			Issuer: strings.TrimSpace(idp.Issuer), ClientID: strings.TrimSpace(idp.ClientID), Scopes: slices.Clone(idp.Scopes),
			RoleClaim: strings.TrimSpace(idp.RoleClaim), RoleMap: idp.RoleMap, RequireClaim: idp.RequireClaim,
		},
	}
	if settings.RefreshMinutes > 0 {
		scopes := settings.Config.Scopes
		if len(scopes) == 0 {
			scopes = slices.Clone(oidcauth.DefaultScopes)
		}
		if !slices.Contains(scopes, oidcauth.OfflineAccessScope) {
			scopes = append(scopes, oidcauth.OfflineAccessScope)
		}
		settings.Config.Scopes = scopes
	}
	if idp.CredentialRef != nil && idp.CredentialRef.Name != "" {
		var secret corev1.Secret
		switch err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: idp.CredentialRef.Name, Namespace: systemNamespace}, &secret); {
		case err == nil:
			settings.Config.ClientSecret = string(secret.Data[platform.PlatformAuthClientSecretKey])
		case !apierrors.IsNotFound(err):
			return ssoSettings{}, err
		}
	}
	settings.HasSecret = settings.Config.ClientSecret != ""
	return settings, nil
}

// orgSigninSettings is the setup of the provider an organization's people sign in through, or why they cannot: for a sign-in the
// organization has to be open and its provider turned on, and organizations have to be on; for a test, only a provider.
func (s *Server) orgSigninSettings(ctx context.Context, name, purpose string) (ssoSettings, *geassv1alpha1.GeassOrganization, error) {
	org, err := s.organization(ctx, name)
	if err != nil {
		return ssoSettings{}, nil, err
	}
	if org == nil || org.Spec.IdP == nil {
		return ssoSettings{}, nil, errOrgSignIn
	}
	if purpose == purposeLogin && (org.Spec.Suspended || !org.Spec.IdP.Enabled || !s.tenancyOn(ctx)) {
		return ssoSettings{}, nil, errOrgSignIn
	}
	settings, err := s.orgIdPSettings(ctx, org)
	if err != nil {
		return ssoSettings{}, nil, err
	}
	if settings.Config.Issuer == "" || settings.Config.ClientID == "" || !settings.HasSecret && org.Spec.IdP.CredentialRef != nil {
		return ssoSettings{}, nil, errOrgSignIn
	}
	return settings, org, nil
}

// orgIdPStillStands is whether a session made through an organization's provider still counts: the organization is open, and its
// provider is turned on and is as it was.
func (s *Server) orgIdPStillStands(ctx context.Context, identity string) bool {
	name, state, ok := parseOrgIdentity(identity)
	if !ok {
		return true
	}
	org, err := s.organization(ctx, name)
	return err == nil && org != nil && !org.Spec.Suspended && org.Spec.IdP != nil && org.Spec.IdP.Enabled && idpState(org) == state
}

// orgProvider is the discovery of an organization's provider, kept between sign-ins as the platform's is.
func (s *Server) orgProvider(ctx context.Context, settings ssoSettings) (*oidcauth.Provider, error) {
	fingerprint := settings.Config.Fingerprint()
	s.sso.mu.Lock()
	entry := s.sso.orgProviders[fingerprint]
	if entry != nil && time.Since(entry.fetched) < oidcProviderTTL {
		provider := entry.provider
		s.sso.mu.Unlock()
		return provider, nil
	}
	if entry != nil && entry.failure != nil && time.Since(entry.failedAt) < oidcDiscoveryRetry {
		kept, failure := entry.provider, entry.failure
		s.sso.mu.Unlock()
		if kept != nil {
			return kept, nil
		}
		return nil, failure
	}
	var kept *oidcauth.Provider
	if entry != nil {
		kept = entry.provider
	}
	s.sso.mu.Unlock()
	provider, err := oidcauth.Discover(ctx, settings.Config)
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	if s.sso.orgProviders == nil || len(s.sso.orgProviders) >= maxOrgProviders && s.sso.orgProviders[fingerprint] == nil {
		s.sso.orgProviders = map[string]*orgProviderEntry{}
	}
	if err != nil {
		s.sso.orgProviders[fingerprint] = &orgProviderEntry{provider: kept, fetched: time.Time{}, failedAt: time.Now(), failure: err}
		if kept != nil {
			return kept, nil
		}
		return nil, err
	}
	s.sso.orgProviders[fingerprint] = &orgProviderEntry{provider: provider, fetched: time.Now()}
	return provider, nil
}

// orgProviderEntry is what is kept of one organization's provider.
type orgProviderEntry struct {
	provider *oidcauth.Provider
	fetched  time.Time
	failedAt time.Time
	failure  error
}

// providerFor is the provider a sign-in goes to: the platform's, or the organization's it names.
func (s *Server) providerFor(ctx context.Context, org string, settings ssoSettings) (*oidcauth.Provider, error) {
	if org == "" {
		return s.ssoProvider(ctx, settings)
	}
	return s.orgProvider(ctx, settings)
}

func (s *Server) recordOrgIdPTest(org, state string, result ssoTestResult) {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	if s.sso.orgTests == nil {
		s.sso.orgTests = map[string]*ssoTestResult{}
	}
	result.Fingerprint = state
	s.sso.orgTests[org] = &result
}

// orgIdPTest is the last test sign-in of an organization's provider, if it was of the provider as it is now.
func (s *Server) orgIdPTest(org *geassv1alpha1.GeassOrganization) *ssoTestResult {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	result := s.sso.orgTests[org.Name]
	if result == nil || result.Fingerprint != idpState(org) {
		return nil
	}
	copied := *result
	return &copied
}

func (s *Server) orgIdPTestedOK(org *geassv1alpha1.GeassOrganization, now time.Time) bool {
	result := s.orgIdPTest(org)
	return result != nil && result.OK && result.Role == oidcauth.RoleAdmin && now.Sub(result.At) < oidcTestValidity
}

// finishOrgSSO ends a sign-in or a test of an organization's provider.
func (s *Server) finishOrgSSO(w http.ResponseWriter, r *http.Request, attempt ssoAttempt, identity *oidcauth.Identity, exchangeErr error) {
	logger := ctrllog.FromContext(r.Context())
	reason, detail := ssoOutcomeReason(exchangeErr)
	org, err := s.organization(r.Context(), attempt.state.Org)
	if err != nil || org == nil || org.Spec.IdP == nil {
		ssoFailure(w, r, attempt.state.Purpose, "disabled")
		return
	}
	state := idpState(org)
	if attempt.state.Purpose == purposeTest {
		result := ssoTestResult{At: time.Now(), OK: exchangeErr == nil, Reason: reason, Detail: detail}
		if identity != nil {
			result.Email, result.Role = identity.Email, identity.Role
			if identity.Role != oidcauth.RoleAdmin {
				result.OK, result.Reason = false, "not_admin"
				result.Detail = "you signed in as " + identity.Email + ", who maps to " + identity.Role + "; sign in as someone who maps to admin, so that the organization has someone who can run it"
			}
		}
		s.recordOrgIdPTest(org.Name, state, result)
		http.Redirect(w, r, "/settings/organizations?org_idp_test="+url.QueryEscape(org.Name), http.StatusFound)
		return
	}
	if exchangeErr != nil {
		logger.Info("An organization's identity provider did not sign the person in", "organization", org.Name, "reason", reason)
		ssoFailure(w, r, purposeLogin, reason)
		return
	}
	now := time.Now()
	ttl := attempt.settings.sessionTTL()
	sessionIdentity := orgIdentity(org.Name, state, identity.Key)
	grant := sessionGrant{Org: org.Name, Admin: identity.Role == oidcauth.RoleAdmin}
	grant.All = !grant.Admin
	claims := ssoSessionClaims{
		Email: identity.Email, Role: identity.Role, Identity: sessionIdentity, IssuedMillis: now.UnixMilli(),
		Org: org.Name, Grants: []sessionGrant{grant},
	}
	token, err := issueSSOSession(attempt.auth.sessionKey, claims, ttl, now)
	if err != nil {
		ssoFailure(w, r, purposeLogin, ssoUnavailable)
		return
	}
	http.SetCookie(w, dashboardSessionCookie(r, token, int(ttl.Seconds())))
	s.rememberRefresh(r.Context(), attempt.settings, attempt.auth.sessionKey, identity, now.Add(ttl), sessionIdentity, org.Name)
	logger.Info("Signed a person in through an organization's identity provider", "organization", org.Name, "user", identity.Email, "access", identity.Role)
	http.Redirect(w, r, attempt.state.Next, http.StatusFound)
}

// ---- what the platform sets up ----

const orgIdPWhat = "An organization's identity provider"

// orgIdPView is what the platform is told of an organization's provider. The client secret is never in it.
type orgIdPView struct {
	DisplayName  string            `json:"displayName,omitempty"`
	Issuer       string            `json:"issuer"`
	ClientID     string            `json:"clientID"`
	HasSecret    bool              `json:"hasSecret"`
	Scopes       []string          `json:"scopes"`
	RoleClaim    string            `json:"roleClaim"`
	RoleMap      map[string]string `json:"roleMap"`
	RequireClaim map[string]string `json:"requireClaim"`
	Enabled      bool              `json:"enabled"`
	// LoginPath is where the organization's people sign in, and CallbackPath what the provider is told to send them back to.
	LoginPath    string         `json:"loginPath"`
	CallbackPath string         `json:"callbackPath"`
	Test         *ssoTestResult `json:"test,omitempty"`
	CanEnable    bool           `json:"canEnable"`
}

func (s *Server) orgIdPViewOf(org *geassv1alpha1.GeassOrganization) *orgIdPView {
	idp := org.Spec.IdP
	if idp == nil {
		return nil
	}
	test := s.orgIdPTest(org)
	view := &orgIdPView{
		DisplayName: idp.DisplayName, Issuer: idp.Issuer, ClientID: idp.ClientID,
		HasSecret: idp.CredentialRef != nil && idp.CredentialRef.Name != "", Scopes: nonNil(idp.Scopes), RoleClaim: idp.RoleClaim,
		RoleMap: idp.RoleMap, RequireClaim: idp.RequireClaim, Enabled: idp.Enabled,
		LoginPath: oidcLoginPath + "?org=" + url.QueryEscape(org.Name), CallbackPath: oidcCallbackPath, Test: test,
		CanEnable: s.orgIdPTestedOK(org, time.Now()),
	}
	if view.RoleMap == nil {
		view.RoleMap = map[string]string{}
	}
	if view.RequireClaim == nil {
		view.RequireClaim = map[string]string{}
	}
	return view
}

// handleOrgIdP serves the platform's changes to an organization's provider: save, turn on or off, remove.
func (s *Server) handleOrgIdP(w http.ResponseWriter, r *http.Request, name, action string) {
	if !s.requireSessionAdmin(w, r, orgIdPWhat) {
		return
	}
	switch action {
	case verbSave:
		s.orgIdPSave(w, r, name)
	case "enable":
		s.orgIdPEnable(w, r, name)
	case "clear":
		s.orgIdPClear(w, r, name)
	default:
		orgFail(w, http.StatusNotFound, "not found")
	}
}

// touchIdP marks the provider of an organization as changed, which ends the sessions made through it.
func touchIdP(org *geassv1alpha1.GeassOrganization) {
	if org.Annotations == nil {
		org.Annotations = map[string]string{}
	}
	org.Annotations[annotationIdPChanged] = strconv.FormatInt(time.Now().UnixNano(), 10)
}

func (s *Server) orgIdPSave(w http.ResponseWriter, r *http.Request, name string) {
	roleMap, err := parsePairs(r.FormValue("roleMap"))
	if err != nil {
		orgFail(w, http.StatusBadRequest, "Role mapping: "+err.Error())
		return
	}
	requireClaim, err := parsePairs(r.FormValue("requireClaim"))
	if err != nil {
		orgFail(w, http.StatusBadRequest, "Required claims: "+err.Error())
		return
	}
	displayName := strings.TrimSpace(r.FormValue("displayName"))
	if len(displayName) > maxDisplayName {
		orgFail(w, http.StatusBadRequest, fmt.Sprintf("The provider's name can be at most %d characters.", maxDisplayName))
		return
	}
	candidate := oidcauth.Config{
		Issuer: strings.TrimSpace(r.FormValue("issuer")), ClientID: strings.TrimSpace(r.FormValue("clientID")),
		RoleClaim: strings.TrimSpace(r.FormValue("roleClaim")), RoleMap: roleMap, RequireClaim: requireClaim, Scopes: splitList(r.FormValue("scopes")),
	}
	if err := candidate.Validate(); err != nil {
		orgFail(w, http.StatusBadRequest, err.Error())
		return
	}
	org, err := s.organization(r.Context(), name)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	if org == nil {
		orgFail(w, http.StatusNotFound, "no such organization")
		return
	}
	// The client secret is kept only for the provider and client it was saved for: reusing it for another would hand the secret
	// of one to whoever runs the other.
	secretValue := r.FormValue("clientSecret")
	keep := formFlag(r, "keepSecret")
	previous := org.Spec.IdP
	hadSecret := previous != nil && previous.CredentialRef != nil && previous.CredentialRef.Name != ""
	if secretValue == "" && keep && hadSecret && (strings.TrimSpace(previous.Issuer) != candidate.Issuer || strings.TrimSpace(previous.ClientID) != candidate.ClientID) {
		orgFail(w, http.StatusBadRequest, "Enter the client secret again: it is kept only for the provider and client it was saved for.")
		return
	}
	secretName := orgIdPSecretName(name)
	var credential *corev1.LocalObjectReference
	rollback := func() {}
	staleSecret := ""
	switch {
	case secretValue != "":
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: systemNamespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{platform.PlatformAuthClientSecretKey: []byte(secretValue)},
		}
		undo, err := s.persistSecretWithRollback(asSystem(r.Context()), secret)
		if err != nil {
			orgFail(w, http.StatusInternalServerError, "could not save the client secret")
			return
		}
		rollback, credential = undo, &corev1.LocalObjectReference{Name: secretName}
	case keep && hadSecret:
		credential = previous.CredentialRef
	case hadSecret:
		// A public client: no secret, so the one that was saved goes once the organization stops naming it.
		staleSecret = secretName
	}
	spec := &geassv1alpha1.GeassOrganizationIdPSpec{
		DisplayName: displayName, Issuer: candidate.Issuer, ClientID: candidate.ClientID, CredentialRef: credential,
		Scopes: candidate.Scopes, RoleClaim: candidate.RoleClaim, RoleMap: candidate.RoleMap, RequireClaim: candidate.RequireClaim,
	}
	err = s.mutateOrganization(r, name, func(org *geassv1alpha1.GeassOrganization) error {
		// Whatever is saved has to be tested before it is turned on, and what was made through the old one is ended.
		org.Spec.IdP = spec
		touchIdP(org)
		return nil
	})
	if err != nil {
		rollback()
		s.finishOrgChange(w, err, nil)
		return
	}
	if staleSecret != "" {
		_ = s.deleteSystemSecret(asSystem(r.Context()), staleSecret)
	}
	ctrllog.FromContext(r.Context()).Info("Saved an organization's identity provider", "organization", name, "by", s.currentSession(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) orgIdPEnable(w http.ResponseWriter, r *http.Request, name string) {
	on := formFlag(r, "enabled")
	org, err := s.organization(r.Context(), name)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	if org == nil || org.Spec.IdP == nil {
		orgFail(w, http.StatusNotFound, "that organization has no identity provider")
		return
	}
	if on {
		if !s.tenancyOn(r.Context()) {
			orgFail(w, http.StatusBadRequest, "Turn organizations on first: they are what a person signs in to.")
			return
		}
		if !s.orgIdPTestedOK(org, time.Now()) {
			orgFail(w, http.StatusBadRequest, "Run the test sign-in with these settings first, as someone who maps to admin. The test has to be of the provider as it is saved, and no older than "+oidcTestValidity.String()+".")
			return
		}
	}
	err = s.mutateOrganization(r, name, func(org *geassv1alpha1.GeassOrganization) error {
		if org.Spec.IdP == nil {
			return apierrors.NewNotFound(geassv1alpha1.GroupVersion.WithResource("geassorganizations").GroupResource(), name)
		}
		org.Spec.IdP.Enabled = on
		touchIdP(org)
		return nil
	})
	if err != nil {
		s.finishOrgChange(w, err, nil)
		return
	}
	// The test was of the provider before this change; turning it on again later needs a new one.
	ctrllog.FromContext(r.Context()).Info("Changed whether an organization's identity provider is on", "organization", name, "enabled", on, "by", s.currentSession(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) orgIdPClear(w http.ResponseWriter, r *http.Request, name string) {
	err := s.mutateOrganization(r, name, func(org *geassv1alpha1.GeassOrganization) error {
		org.Spec.IdP = nil
		touchIdP(org)
		return nil
	})
	if err != nil {
		s.finishOrgChange(w, err, nil)
		return
	}
	_ = s.deleteSystemSecret(asSystem(r.Context()), orgIdPSecretName(name))
	s.forgetOrgIdP(name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// forgetOrgIdP drops what was kept of an organization's provider and its last test.
func (s *Server) forgetOrgIdP(name string) {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	delete(s.sso.orgTests, name)
	s.sso.orgProviders = nil
}
