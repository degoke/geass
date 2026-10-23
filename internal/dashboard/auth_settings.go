package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

// sessionAdminOnly finishes the message for a request that needs a person, not a token.
const sessionAdminOnly = "can only be changed from the dashboard by a signed-in admin, not with an API token"

const (
	maxDisplayName = 60
	minSessionMins = 5
	// minRefreshMins and maxRefreshMins bound how often the provider is asked again about people, and how long it may be out of reach.
	minRefreshMins = 5
	maxRefreshMins = 1440
	maxSessionMins = 720
)

// requireSessionAdmin allows only a signed-in admin, never an API token. What it guards decides who can sign in to
// the dashboard, so a token that could change it could always get back in as a person after it was revoked. The same
// reason keeps tokens from minting tokens.
func (s *Server) requireSessionAdmin(w http.ResponseWriter, r *http.Request, what string) bool {
	if credentialIsBearer(r) || !s.sessionCanMutate(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: what + " " + sessionAdminOnly})
		return false
	}
	return true
}

// adminHasPassword is whether some admin can sign in with a password: the way in that does not depend on the
// identity provider.
func adminHasPassword(users []dashboardUser) bool {
	return slices.ContainsFunc(users, func(user dashboardUser) bool {
		return normalizeDashboardRole(user.Role) == dashboardRoleAdmin && user.Password != ""
	})
}

// splitList reads a list a person typed: items separated by commas, spaces or lines.
func splitList(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' })
}

// parsePairs reads "name=value" entries separated by commas or lines.
func parsePairs(text string) (map[string]string, error) {
	pairs := map[string]string{}
	for entry := range strings.FieldsFuncSeq(text, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		name, value, found := strings.Cut(entry, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !found || name == "" || value == "" {
			return nil, fmt.Errorf("%q is not written as name=value", strings.TrimSpace(entry))
		}
		pairs[name] = value
	}
	return pairs, nil
}

// authSettingsView is the settings page's answer: everything but the client secret.
type authSettingsView struct {
	Mode           string            `json:"mode"`
	Configured     bool              `json:"configured"`
	DisplayName    string            `json:"displayName"`
	Issuer         string            `json:"issuer"`
	ClientID       string            `json:"clientID"`
	HasSecret      bool              `json:"hasSecret"`
	Scopes         []string          `json:"scopes"`
	RoleClaim      string            `json:"roleClaim"`
	RoleMap        map[string]string `json:"roleMap"`
	RequireClaim   map[string]string `json:"requireClaim"`
	SessionMinutes int               `json:"sessionMinutes"`
	// RefreshMinutes is how often the provider is asked again about people who signed in (0 is never), and StaleGraceMinutes how
	// long it can be unreachable before what could not be checked is ended.
	RefreshMinutes    int    `json:"refreshMinutes"`
	StaleGraceMinutes int    `json:"staleGraceMinutes"`
	CallbackURL       string `json:"callbackURL"`
	LogoutURL         string `json:"backchannelLogoutURL"`
	// RevokeTokensOnLogout is whether a logout from the provider also revokes the tokens the person made.
	RevokeTokensOnLogout bool `json:"revokeTokensOnLogout"`
	// AccessTokens is whether the provider's access tokens work as a bearer credential, and AccessTokenAudience the audience
	// they must name (the client ID when empty). Both are set in the platform configuration, not on the settings page.
	AccessTokens        bool           `json:"accessTokens"`
	AccessTokenAudience string         `json:"accessTokenAudience"`
	Test                *ssoTestResult `json:"test"`
	HasPasswordAdmin    bool           `json:"hasPasswordAdmin"`
	CanEnable           bool           `json:"canEnable"`
	// Tenancy is how the token names organizations, and whether they are on.
	Tenancy tenancyView `json:"tenancy"`
}

// handleAPIAuthSettings says how people sign in and how the provider is set up. A signed-in admin only: the roles
// and the required claims say who can get in.
func (s *Server) handleAPIAuthSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessionAdmin(w, r, "Authentication settings") {
		return
	}
	settings, err := s.ssoSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load the sign-in settings"})
		return
	}
	hasPasswordAdmin := false
	if auth, authErr := s.dashboardAuth(r); authErr == nil {
		hasPasswordAdmin = adminHasPassword(auth.users)
	}
	fingerprint := settings.Config.Fingerprint()
	view := authSettingsView{
		Mode: settings.Mode, Configured: settings.Configured(), DisplayName: settings.DisplayName, Issuer: settings.Config.Issuer,
		ClientID: settings.Config.ClientID, HasSecret: settings.HasSecret, Scopes: settings.Config.Scopes, RoleClaim: settings.Config.RoleClaim,
		RoleMap: settings.Config.RoleMap, RequireClaim: settings.Config.RequireClaim, SessionMinutes: settings.SessionMinutes,
		RefreshMinutes: settings.RefreshMinutes, StaleGraceMinutes: int(settings.StaleGrace / time.Minute),
		CallbackURL: ssoRedirectURL(r, settings.DashboardURL), LogoutURL: strings.TrimSuffix(ssoRedirectURL(r, settings.DashboardURL), oidcCallbackPath) + oidcLogoutPath,
		RevokeTokensOnLogout: settings.RevokeTokensOnLogout, AccessTokens: settings.AccessTokens, AccessTokenAudience: settings.AccessTokenAudience,
		Test: s.ssoTest(fingerprint), HasPasswordAdmin: hasPasswordAdmin,
		CanEnable: settings.Configured() && hasPasswordAdmin && s.ssoTestedOK(fingerprint, time.Now()),
	}
	if view.Scopes == nil {
		view.Scopes = []string{}
	}
	view.Tenancy = tenancyView{
		Enabled: settings.TenancyOn, OrgsClaim: settings.Config.Tenancy.OrgsClaim, PlatformClaim: settings.Config.Tenancy.PlatformClaim,
		PlatformValues: settings.Config.Tenancy.PlatformValues,
	}
	if view.Tenancy.PlatformValues == nil {
		view.Tenancy.PlatformValues = []string{}
	}
	view.Tenancy.AutoProvision, view.Tenancy.AutoProvisionAllow, view.Tenancy.AutoProvisionMax = "off", []string{}, defaultAutoProvisionMax
	if config, err := s.platformConfig(r.Context()); err == nil {
		if spec := tenancySpecOf(config).AutoProvision; spec != nil {
			view.Tenancy.AutoProvision = strings.ToLower(spec.Mode)
			view.Tenancy.AutoProvisionAllow = nonNil(spec.Allow)
			if spec.Max != nil {
				view.Tenancy.AutoProvisionMax = int(*spec.Max)
			}
		}
	}
	if orgs, orgsErr := s.listOrganizations(r.Context()); orgsErr == nil {
		view.Tenancy.Organizations = len(orgs)
		if !view.Tenancy.Enabled {
			view.Tenancy.EnableProblem = s.tenancyEnableProblem(r.Context(), settings, hasPasswordAdmin, len(orgs))
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// handlePlatformAuthSave stores the identity provider's settings. It does not connect to the provider (the test does,
// and only for an admin who asks), and it does not turn the provider on: that is a separate, checked step. It is
// refused while people are signing in through the provider, so that no edit can lock them out unseen.
func (s *Server) handlePlatformAuthSave(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, ssoSettingsPath) || !parseFormOrRedirect(w, r, ssoSettingsPath) || !s.requireSessionAdmin(w, r, "Authentication settings") {
		return
	}
	fail := func(message string) { redirectProbe(w, r, ssoSettingsPath, dashboardLiteralError, message) }
	config, err := s.platformConfig(r.Context())
	if err != nil {
		fail("could not load platform config")
		return
	}
	if ssoSettingsOf(config).External() {
		fail("Switch to local sign-in before changing the identity provider: people are signing in through it now.")
		return
	}

	roleMap, err := parsePairs(r.FormValue("roleMap"))
	if err != nil {
		fail("Role mapping: " + err.Error())
		return
	}
	requireClaim, err := parsePairs(r.FormValue("requireClaim"))
	if err != nil {
		fail("Required claims: " + err.Error())
		return
	}
	minutes := ssoDefaultSessionMinutes
	if raw := strings.TrimSpace(r.FormValue("sessionMinutes")); raw != "" {
		if minutes, err = strconv.Atoi(raw); err != nil || minutes < minSessionMins || minutes > maxSessionMins {
			fail(fmt.Sprintf("Session length must be between %d and %d minutes.", minSessionMins, maxSessionMins))
			return
		}
	}
	refreshMinutes, staleMinutes := 0, 0
	if raw := strings.TrimSpace(r.FormValue("refreshMinutes")); raw != "" {
		if refreshMinutes, err = strconv.Atoi(raw); err != nil || (refreshMinutes != 0 && (refreshMinutes < minRefreshMins || refreshMinutes > maxRefreshMins)) {
			fail(fmt.Sprintf("How often the provider is asked again is 0 (never), or between %d and %d minutes.", minRefreshMins, maxRefreshMins))
			return
		}
	}
	if raw := strings.TrimSpace(r.FormValue("staleGraceMinutes")); raw != "" {
		if staleMinutes, err = strconv.Atoi(raw); err != nil || staleMinutes < minRefreshMins || staleMinutes > maxRefreshMins {
			fail(fmt.Sprintf("How long the provider can be out of reach is between %d and %d minutes.", minRefreshMins, maxRefreshMins))
			return
		}
	}
	displayName := strings.TrimSpace(r.FormValue("displayName"))
	if len(displayName) > maxDisplayName {
		fail(fmt.Sprintf("The provider's name can be at most %d characters.", maxDisplayName))
		return
	}
	issuer := strings.TrimSpace(r.FormValue("issuer"))
	candidate := oidcauth.Config{
		Issuer: issuer, ClientID: strings.TrimSpace(r.FormValue("clientID")), RoleClaim: strings.TrimSpace(r.FormValue("roleClaim")),
		RoleMap: roleMap, RequireClaim: requireClaim, Scopes: splitList(r.FormValue("scopes")),
		// How the token names organizations is saved on its own, and is kept: without it there would be no role claim to
		// validate for a setup that reads the role from each organization.
		Tenancy: ssoSettingsOf(config).Config.Tenancy,
	}
	// The claim that lists the organizations may come with the provider, because a setup that reads the role from each
	// organization has no role claim to save the provider with, and the claim cannot be saved on its own before there is a
	// provider. While organizations are on it cannot change: people are working in them.
	orgsClaim := ""
	if formHasValue(r, "orgsClaim") {
		orgsClaim = strings.TrimSpace(r.FormValue("orgsClaim"))
		if current := tenancySpecOf(config); orgsClaim != current.OrgsClaim {
			if current.Enabled {
				fail("Turn organizations off before changing how the token names them: people are working in them now.")
				return
			}
			candidate.Tenancy.OrgsClaim = orgsClaim
		}
	}
	if err := candidate.Validate(); err != nil {
		fail(err.Error())
		return
	}

	// The client secret is kept only for the provider and client it was saved for: reusing it for another would hand
	// the secret of one to whoever runs the other.
	previous := (*geassv1alpha1.GeassPlatformOIDCSpec)(nil)
	if config.Spec.Auth != nil {
		previous = config.Spec.Auth.OIDC
	}
	secretValue := r.FormValue("clientSecret")
	keepSecret := r.FormValue("keepSecret") == "on" || r.FormValue("keepSecret") == boolTrue
	hadSecret := previous != nil && previous.CredentialRef != nil && previous.CredentialRef.Name != ""
	switch {
	case secretValue != "":
	case keepSecret && hadSecret:
		if strings.TrimSpace(previous.Issuer) != candidate.Issuer || strings.TrimSpace(previous.ClientID) != candidate.ClientID {
			fail("Enter the client secret again: it is kept only for the provider and client it was saved for.")
			return
		}
	}

	creating := config.Name == ""
	if creating {
		config = geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: systemNamespace}}
	}
	spec := &geassv1alpha1.GeassPlatformOIDCSpec{
		DisplayName: displayName, Issuer: candidate.Issuer, ClientID: candidate.ClientID, Scopes: candidate.Scopes,
		RoleClaim: candidate.RoleClaim, RoleMap: candidate.RoleMap, RequireClaim: candidate.RequireClaim, SessionMinutes: int32(minutes),
		RefreshMinutes: int32(refreshMinutes), StaleGraceMinutes: int32(staleMinutes),
	}
	// What a logout does to the tokens is set on its own (see handlePlatformAuthLogoutPolicy), so saving the provider keeps it.
	if previous != nil {
		spec.RevokeTokensOnLogout = previous.RevokeTokensOnLogout
		// Whether access tokens are accepted is set in the configuration, and saving the provider keeps it.
		spec.AccessTokens = previous.AccessTokens
	}

	// A secret that was just written is put back as it was if the configuration cannot be saved, so the two agree.
	rollback := func() {}
	staleSecret := ""
	switch {
	case secretValue != "":
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformAuthSecretName, Namespace: systemNamespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{platform.PlatformAuthClientSecretKey: []byte(secretValue)},
		}
		restore, err := s.persistSecretWithRollback(r.Context(), secret)
		if err != nil {
			fail("could not save the client secret")
			return
		}
		rollback = restore
		spec.CredentialRef = &corev1.LocalObjectReference{Name: platform.PlatformAuthSecretName}
	case keepSecret && hadSecret:
		spec.CredentialRef = previous.CredentialRef
	default:
		// A public client: no secret, so the one that was saved goes once the configuration stops naming it.
		if hadSecret && previous.CredentialRef.Name == platform.PlatformAuthSecretName {
			staleSecret = platform.PlatformAuthSecretName
		}
	}
	setProvider := func(c *geassv1alpha1.GeassPlatformConfig) {
		if c.Spec.Auth == nil {
			c.Spec.Auth = &geassv1alpha1.GeassPlatformAuthSpec{Mode: platform.AuthModeLocal}
		}
		c.Spec.Auth.OIDC = spec
		if formHasValue(r, "orgsClaim") && orgsClaim != tenancySpecOf(*c).OrgsClaim {
			tenancy := tenancySpecOf(*c)
			tenancy.OrgsClaim = orgsClaim
			c.Spec.Auth.Tenancy = &tenancy
		}
	}
	if creating {
		setProvider(&config)
		err = s.Client.Create(r.Context(), &config)
	} else {
		err = s.updatePlatformConfig(r.Context(), &config, setProvider)
	}
	if err != nil {
		rollback()
		fail("could not save the identity provider settings")
		return
	}
	s.forgetSSOProvider()
	if staleSecret != "" {
		if err := s.deleteSystemSecret(r.Context(), staleSecret); err != nil {
			fail("the settings were saved, but the old client secret could not be removed")
			return
		}
	}
	ctrllog.FromContext(r.Context()).Info("Saved identity provider settings", "issuer", candidate.Issuer, "clientID", candidate.ClientID, "login", s.currentSession(r).Username)
	redirect(w, r, ssoSettingsPath)
}

// handlePlatformAuthMode switches how people sign in. Back to local is always allowed. To external needs three
// things, so that nobody is locked out by a typo: the provider is set up, a test sign-in of exactly these settings
// went through lately as someone who maps to admin, and an admin has a password to come back with.
func (s *Server) handlePlatformAuthMode(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, ssoSettingsPath) || !parseFormOrRedirect(w, r, ssoSettingsPath) || !s.requireSessionAdmin(w, r, "Authentication settings") {
		return
	}
	fail := func(message string) { redirectProbe(w, r, ssoSettingsPath, dashboardLiteralError, message) }
	mode := strings.TrimSpace(r.FormValue("mode"))
	if mode != platform.AuthModeLocal && mode != platform.AuthModeExternal {
		fail("The mode must be local or external.")
		return
	}
	config, err := s.platformConfig(r.Context())
	if err != nil || config.Name == "" || config.Spec.Auth == nil {
		if mode == platform.AuthModeLocal && err == nil {
			redirect(w, r, ssoSettingsPath)
			return
		}
		fail("Set up the identity provider first.")
		return
	}
	// The users of the Secret who are not admins, whose sessions end when the provider is turned on.
	var passwordUsers []string
	if mode == platform.AuthModeExternal {
		settings, err := s.ssoSettings(r.Context())
		switch {
		case err != nil:
			fail("could not load the sign-in settings")
			return
		case !settings.Configured():
			fail("Set up the identity provider first.")
			return
		case !s.ssoTestedOK(settings.Config.Fingerprint(), time.Now()):
			fail("Run Test sign-in first and sign in as someone who maps to admin. The test has to be of these exact settings and no older than " + oidcTestValidity.String() + ".")
			return
		}
		auth, err := s.dashboardAuth(r)
		if err != nil {
			fail("could not load the dashboard users")
			return
		}
		if !adminHasPassword(auth.users) {
			fail("Keep an admin who signs in with a password: it is the way back in when the identity provider is down or set up wrong.")
			return
		}
		for _, user := range auth.users {
			if normalizeDashboardRole(user.Role) != dashboardRoleAdmin {
				passwordUsers = append(passwordUsers, user.Username)
			}
		}
		// While the provider decides, a password works only for an admin (see handleDashboardLogin), so the sessions of
		// the users who are not admins end now, once. Checking it on every request would make every viewer's request read
		// the platform configuration.
		if err := s.endPasswordSessions(r, passwordUsers); err != nil {
			fail("could not end the sessions of the users who sign in with a password")
			return
		}
		// No session of the provider that ended before now may come back: one that was made while the provider decided
		// in the past, perhaps with other settings or another provider, would otherwise count again.
		if err := s.revokeAllSSOSessions(r.Context()); err != nil {
			fail("could not end the earlier sessions of the identity provider")
			return
		}
	}
	if err := s.updatePlatformConfig(r.Context(), &config, func(c *geassv1alpha1.GeassPlatformConfig) {
		if c.Spec.Auth != nil {
			c.Spec.Auth.Mode = mode
		}
	}); err != nil {
		fail("could not change how people sign in")
		return
	}
	// Once more now that the mode is changed: someone who signed in with a password between the first time and now has
	// a session that began after it. The mode is already switched, so a failure here is reported, not undone.
	if err := s.endPasswordSessions(r, passwordUsers); err != nil {
		fail("the sign-in method was changed, but the sessions of the users who sign in with a password could not all be ended")
		return
	}
	ctrllog.FromContext(r.Context()).Info("Changed how people sign in to the dashboard", "mode", mode, "login", s.currentSession(r).Username)
	redirect(w, r, ssoSettingsPath)
}

// handlePlatformAuthClear removes the identity provider and goes back to local sign-in.
func (s *Server) handlePlatformAuthClear(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, ssoSettingsPath) || !parseFormOrRedirect(w, r, ssoSettingsPath) || !s.requireSessionAdmin(w, r, "Authentication settings") {
		return
	}
	config, err := s.platformConfig(r.Context())
	if err != nil {
		redirectProbe(w, r, ssoSettingsPath, dashboardLiteralError, "could not load platform config")
		return
	}
	if config.Name != "" && config.Spec.Auth != nil {
		stale := ""
		if oidc := config.Spec.Auth.OIDC; oidc != nil && oidc.CredentialRef != nil && oidc.CredentialRef.Name == platform.PlatformAuthSecretName {
			stale = oidc.CredentialRef.Name
		}
		if err := s.updatePlatformConfig(r.Context(), &config, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth = nil }); err != nil {
			redirectProbe(w, r, ssoSettingsPath, dashboardLiteralError, "could not remove the identity provider")
			return
		}
		if stale != "" {
			if err := s.deleteSystemSecret(r.Context(), stale); err != nil {
				redirectProbe(w, r, ssoSettingsPath, dashboardLiteralError, "the identity provider was removed, but its client secret could not be deleted")
				return
			}
		}
	}
	s.forgetSSOProvider()
	ctrllog.FromContext(r.Context()).Info("Removed the identity provider", "login", s.currentSession(r).Username)
	redirect(w, r, ssoSettingsPath)
}

// handlePlatformAuthLogoutPolicy sets whether a logout from the identity provider also revokes the API tokens and deploy hooks
// the person made (their sessions always end). It does not change who can sign in, so unlike the provider's settings it can be
// changed while people sign in through the provider: it is a policy about leaving, and having to switch everyone back to
// passwords to change it would be a reason to leave it wrong.
func (s *Server) handlePlatformAuthLogoutPolicy(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, ssoSettingsPath) || !parseFormOrRedirect(w, r, ssoSettingsPath) || !s.requireSessionAdmin(w, r, "Authentication settings") {
		return
	}
	fail := func(message string) { redirectProbe(w, r, ssoSettingsPath, dashboardLiteralError, message) }
	var revoke bool
	switch strings.ToLower(strings.TrimSpace(r.FormValue("revokeTokens"))) {
	case "on", boolTrue:
		revoke = true
	case "off", "false":
		revoke = false
	default:
		fail("revokeTokens must be on or off.")
		return
	}
	config, err := s.platformConfig(r.Context())
	if err != nil {
		fail("could not load platform config")
		return
	}
	if config.Name == "" || config.Spec.Auth == nil || config.Spec.Auth.OIDC == nil {
		fail("Set up the identity provider first.")
		return
	}
	if err := s.updatePlatformConfig(r.Context(), &config, func(c *geassv1alpha1.GeassPlatformConfig) {
		if c.Spec.Auth != nil && c.Spec.Auth.OIDC != nil {
			c.Spec.Auth.OIDC.RevokeTokensOnLogout = &revoke
		}
	}); err != nil {
		fail("could not save the setting")
		return
	}
	ctrllog.FromContext(r.Context()).Info("Changed what a logout from the identity provider does to the tokens a person made", "revokeTokens", revoke, "login", s.currentSession(r).Username)
	redirect(w, r, ssoSettingsPath)
}

// endPasswordSessions ends the sessions of these users of the Secret.
func (s *Server) endPasswordSessions(r *http.Request, usernames []string) error {
	for _, username := range usernames {
		if err := s.revokeDashboardSession(r, username); err != nil {
			return err
		}
	}
	return nil
}

// updatePlatformConfig applies change to the platform configuration and saves it. The platform controller writes the same
// object, so a save can meet a conflict that has nothing to do with what is being changed: it is then tried again from a
// fresh read of the object, with the change applied to that, and what the controller wrote is kept.
func (s *Server) updatePlatformConfig(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig, change func(*geassv1alpha1.GeassPlatformConfig)) error {
	change(config)
	err := s.Client.Update(ctx, config)
	if !apierrors.IsConflict(err) {
		return err
	}
	return retry.RetryOnConflict(writeRetry, func() error {
		fresh, getErr := s.platformConfig(ctx)
		if getErr != nil {
			return getErr
		}
		change(&fresh)
		if updateErr := s.Client.Update(ctx, &fresh); updateErr != nil {
			return updateErr
		}
		*config = fresh
		return nil
	})
}
