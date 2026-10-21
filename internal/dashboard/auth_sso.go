package dashboard

import (
	"context"
	"crypto/hmac"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

// Signing in through an identity provider (spec.auth on the platform configuration).
//
// The provider owns the people: Geass sends the browser there, takes back a signed ID token, and turns one claim of it
// into admin or viewer. What Geass keeps of the person is a signed cookie like a local sign-in's, with two differences:
// the role is the one the provider gave at sign-in (it is not looked up again, so it changes when the person next signs
// in, which is why these sessions are short), and the cookie names the person by a stable identity key, so that signing
// out, and the provider saying the person signed out or was removed, can end every session they have.

const (
	oidcLoginPath    = "/auth/oidc/login"
	oidcCallbackPath = "/auth/oidc/callback"
	oidcLogoutPath   = "/auth/oidc/backchannel-logout"
	// oidcStateCookie carries what a sign-in needs between sending the browser away and its coming back. It is good for
	// oidcStateTTL, once (see cookieName for the form it takes).
	oidcStateCookie = "geass_oidc"
	oidcStateTTL    = 10 * time.Minute

	purposeLogin = "login"
	purposeTest  = "test"

	// ssoSessionFields is how many fields a session cookie of a person from the provider has; a local one has five. When
	// organizations are on there are four more: the organization and the workspace the person works in, whether they act for
	// the platform, and the grants they may switch between.
	ssoSessionFields       = 6
	ssoSessionTenantFields = 10
	// ssoRevocationsKey is the key of the users Secret that holds, for each person, the moment before which their
	// sessions no longer count.
	ssoRevocationsKey = "sso-revoked"
	// ssoNotBeforeKey is the key of the users Secret that holds the moment (Unix milliseconds) before which no session of a
	// person from the provider counts, whoever they are. It is raised when the provider is turned on, so that a session
	// from an earlier time that the provider decided (with other settings, or another provider) cannot come back.
	ssoNotBeforeKey = "sso-not-before"
	// ssoRevocationKeep is how long a revocation is remembered: no session lasts longer than that, so a revocation
	// older than this protects nothing and is dropped, which keeps the table from growing for ever.
	ssoRevocationKeep = 24 * time.Hour
	ssoRevocationCap  = 5000

	ssoDefaultSessionMinutes = 60
	// oidcProviderTTL is how long the provider's discovery document is kept. Its keys are kept longer, and fetched
	// again when a token arrives signed with one that is not known.
	oidcProviderTTL = 10 * time.Minute
	// oidcTestValidity is how long a successful test sign-in lets the mode be switched to external.
	oidcTestValidity = 30 * time.Minute
	// oidcLogoutMemory is how long a logout token's identifier is remembered, to recognise one sent twice.
	oidcLogoutMemory = 10 * time.Minute
	// oidcDiscoveryRetry is how long a provider that could not be reached is left alone, so that a provider that is down
	// is not asked again by every request that arrives meanwhile.
	oidcDiscoveryRetry = 5 * time.Second
	// oidcUsedStateCap bounds the memory of sign-ins that were already finished.
	oidcUsedStateCap = 8192

	ssoSettingsPath = "/settings/auth"
)

// ssoState is what the server keeps in memory about signing in through a provider.
type ssoState struct {
	mu          sync.Mutex
	provider    *oidcauth.Provider
	fingerprint string
	fetched     time.Time
	// failedAt, failedFor and failure are the last time the provider could not be reached, for which settings, and why.
	failedAt  time.Time
	failedFor string
	failure   error
	test      *ssoTestResult
	logouts   map[string]time.Time
	// usedStates are the sign-ins that came back already, by their state, so that a state cookie is good for one
	// callback and not for as many as its ten minutes allow.
	usedStates map[string]time.Time
	// orgTests are the last test sign-ins of organizations' own providers, by organization, and orgProviders what was kept of
	// them (see org_idp.go).
	orgTests     map[string]*ssoTestResult
	orgProviders map[string]*orgProviderEntry
}

// ssoTestResult is what the last test sign-in found, for the settings page.
type ssoTestResult struct {
	Fingerprint string    `json:"-"`
	At          time.Time `json:"at"`
	OK          bool      `json:"ok"`
	Email       string    `json:"email,omitempty"`
	Role        string    `json:"role,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	// Platform and Orgs say, when organizations are read, whether the person is a platform administrator and which of
	// the organizations their token names Geass knows.
	Platform bool         `json:"platformAdmin,omitempty"`
	Orgs     []ssoTestOrg `json:"orgs,omitempty"`
}

// ssoTestOrg is an organization the test sign-in's token put the person in.
type ssoTestOrg struct {
	// IdPID is the organization's id in the token.
	IdPID string `json:"idpID"`
	// Name is the GeassOrganization it is set up as, or empty when it is not set up.
	Name       string `json:"name,omitempty"`
	Role       string `json:"role"`
	Suspended  bool   `json:"suspended,omitempty"`
	Workspaces int    `json:"workspaces"`
}

// ssoSettings is the platform's sign-in setup as it is read for one request.
type ssoSettings struct {
	Mode           string
	DisplayName    string
	SessionMinutes int
	Config         oidcauth.Config
	HasSecret      bool
	// RevokeTokensOnLogout is whether a logout from the provider also revokes the tokens the person made.
	RevokeTokensOnLogout bool
	// DashboardURL is the address the dashboard is published at, when it is (its domain is verified), and "" otherwise.
	DashboardURL string
	// TenancyOn is whether people belong to organizations: the platform says so, and the provider is how they sign in.
	TenancyOn bool
	// AutoProvision is when a sign-in makes the organization of an administrator that Geass does not know, or nil for never.
	AutoProvision *geassv1alpha1.GeassPlatformAutoProvisionSpec
	// RefreshMinutes is how often the provider is asked again about each person who signed in, and StaleGrace how long it
	// can be unreachable before what could not be checked is ended. Zero RefreshMinutes is never.
	RefreshMinutes int
	StaleGrace     time.Duration
}

// External is whether people sign in through the provider now.
func (c ssoSettings) External() bool { return c.Mode == platform.AuthModeExternal && c.Configured() }

// Configured is whether there is enough set up to try the provider.
func (c ssoSettings) Configured() bool {
	return c.Config.Issuer != "" && c.Config.ClientID != "" && (c.Config.RoleClaim != "" || c.Config.Tenancy.On()) && len(c.Config.RoleMap) > 0
}

func (c ssoSettings) sessionTTL() time.Duration { return time.Duration(c.SessionMinutes) * time.Minute }

// ssoSettingsOf reads the sign-in setup from the platform configuration. The client secret is not in it: see
// readSSOSecret.
func ssoSettingsOf(config geassv1alpha1.GeassPlatformConfig) ssoSettings {
	settings := ssoSettings{Mode: platform.AuthModeLocal, SessionMinutes: ssoDefaultSessionMinutes, RevokeTokensOnLogout: true}
	if platform.DashboardDomainReady(config) {
		settings.DashboardURL = platform.DashboardURLFromConfig(config)
	}
	spec := config.Spec.Auth
	if spec == nil {
		return settings
	}
	if spec.Mode == platform.AuthModeExternal {
		settings.Mode = platform.AuthModeExternal
	}
	oidc := spec.OIDC
	if oidc == nil {
		return settings
	}
	if oidc.SessionMinutes > 0 {
		settings.SessionMinutes = int(oidc.SessionMinutes)
	}
	settings.DisplayName = strings.TrimSpace(oidc.DisplayName)
	if oidc.RevokeTokensOnLogout != nil {
		settings.RevokeTokensOnLogout = *oidc.RevokeTokensOnLogout
	}
	settings.HasSecret = oidc.CredentialRef != nil && oidc.CredentialRef.Name != ""
	settings.Config = oidcauth.Config{
		Issuer: strings.TrimSpace(oidc.Issuer), ClientID: strings.TrimSpace(oidc.ClientID), Scopes: oidc.Scopes,
		RoleClaim: strings.TrimSpace(oidc.RoleClaim), RoleMap: oidc.RoleMap, RequireClaim: oidc.RequireClaim,
	}
	if spec.Tenancy != nil {
		settings.Config.Tenancy = oidcauth.Tenancy{
			OrgsClaim: strings.TrimSpace(spec.Tenancy.OrgsClaim), PlatformClaim: strings.TrimSpace(spec.Tenancy.PlatformClaim), PlatformValues: spec.Tenancy.PlatformValues,
		}
	}
	if oidc.RefreshMinutes > 0 {
		settings.RefreshMinutes = int(oidc.RefreshMinutes)
		settings.StaleGrace = defaultStaleGrace
		if oidc.StaleGraceMinutes > 0 {
			settings.StaleGrace = time.Duration(oidc.StaleGraceMinutes) * time.Minute
		}
		// A refresh token is asked for with the offline_access scope. The scopes the provider was set up with (or the defaults,
		// which are not written down) are kept, and it is added.
		scopes := settings.Config.Scopes
		if len(scopes) == 0 {
			scopes = slices.Clone(oidcauth.DefaultScopes)
		}
		if !slices.Contains(scopes, oidcauth.OfflineAccessScope) {
			scopes = append(slices.Clone(scopes), oidcauth.OfflineAccessScope)
		}
		settings.Config.Scopes = scopes
	}
	settings.TenancyOn = platform.TenancyEnabled(config) && settings.Config.Tenancy.On()
	if settings.TenancyOn && spec.Tenancy != nil {
		settings.AutoProvision = spec.Tenancy.AutoProvision
	}
	return settings
}

// ssoName is what the sign-in button calls the provider.
func (c ssoSettings) ssoName() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return "single sign-on"
}

// ssoSettings reads the sign-in setup and the client secret.
func (s *Server) ssoSettings(ctx context.Context) (ssoSettings, error) {
	config, err := s.platformConfig(ctx)
	if err != nil {
		return ssoSettings{}, err
	}
	settings := ssoSettingsOf(config)
	if settings.HasSecret {
		secret := &corev1.Secret{}
		if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: config.Spec.Auth.OIDC.CredentialRef.Name, Namespace: systemNamespace}, secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return ssoSettings{}, err
			}
			settings.HasSecret = false
		} else {
			settings.Config.ClientSecret = string(secret.Data[platform.PlatformAuthClientSecretKey])
			settings.HasSecret = settings.Config.ClientSecret != ""
		}
	}
	return settings, nil
}

// externalAuthActive is whether people sign in through the provider now. It reads only the platform configuration,
// so it is cheap enough to ask for every request of a person who did. An error counts as no: a session of the
// provider is not honoured while it cannot be told whether the provider is still the one that decides.
func (s *Server) externalAuthActive(ctx context.Context) bool {
	config, err := s.platformConfig(ctx)
	return err == nil && ssoSettingsOf(config).External()
}

// ssoProvider is the provider for these settings, with its discovery document and keys kept between sign-ins. When
// the document cannot be fetched again, the one that was kept is used rather than failing everyone, and a provider that
// could not be reached is not asked again for a few seconds.
func (s *Server) ssoProvider(ctx context.Context, settings ssoSettings) (*oidcauth.Provider, error) {
	fingerprint := settings.Config.Fingerprint()
	s.sso.mu.Lock()
	var kept *oidcauth.Provider
	if s.sso.provider != nil && s.sso.fingerprint == fingerprint {
		kept = s.sso.provider
		if time.Since(s.sso.fetched) < oidcProviderTTL {
			s.sso.mu.Unlock()
			return kept, nil
		}
	}
	if s.sso.failure != nil && s.sso.failedFor == fingerprint && time.Since(s.sso.failedAt) < oidcDiscoveryRetry {
		failure := s.sso.failure
		s.sso.mu.Unlock()
		if kept != nil {
			return kept, nil
		}
		return nil, failure
	}
	s.sso.mu.Unlock()
	// Not under the lock: a provider that does not answer takes up to ten seconds, and must not hold up the others.
	provider, err := oidcauth.Discover(ctx, settings.Config)
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	if err != nil {
		s.sso.failedAt, s.sso.failedFor, s.sso.failure = time.Now(), fingerprint, err
		if kept != nil {
			return kept, nil
		}
		return nil, err
	}
	s.sso.provider, s.sso.fingerprint, s.sso.fetched, s.sso.failure = provider, fingerprint, time.Now(), nil
	return provider, nil
}

// forgetSSOProvider drops what was kept for the provider and what the last test found, for settings that changed.
func (s *Server) forgetSSOProvider() {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	s.sso.provider, s.sso.fingerprint, s.sso.test, s.sso.failure = nil, "", nil, nil
}

func (s *Server) recordSSOTest(result ssoTestResult) {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	s.sso.test = &result
}

// ssoTest is the last test sign-in if it was of these settings.
func (s *Server) ssoTest(fingerprint string) *ssoTestResult {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	if s.sso.test == nil || s.sso.test.Fingerprint != fingerprint {
		return nil
	}
	result := *s.sso.test
	return &result
}

// ssoTestedOK is whether these settings were tested lately by someone who signed in and maps to admin. It is what the
// mode needs before it can be switched to external: the person switching has seen the provider work, with a role that
// can run Geass.
func (s *Server) ssoTestedOK(fingerprint string, now time.Time) bool {
	result := s.ssoTest(fingerprint)
	return result != nil && result.OK && result.Role == oidcauth.RoleAdmin && now.Sub(result.At) < oidcTestValidity
}

// logoutSeen says whether this logout token was already acted on.
func (s *Server) logoutSeen(id string, now time.Time) bool {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	for key, at := range s.sso.logouts {
		if now.Sub(at) > oidcLogoutMemory {
			delete(s.sso.logouts, key)
		}
	}
	_, seen := s.sso.logouts[id]
	return seen
}

// rememberLogout records that a logout token was acted on. It is called only once the sessions and tokens are ended:
// a token that failed to do its work must be able to be sent again and do it.
func (s *Server) rememberLogout(id string, now time.Time) {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	if s.sso.logouts == nil {
		s.sso.logouts = map[string]time.Time{}
	}
	if len(s.sso.logouts) < 4096 {
		s.sso.logouts[id] = now
	}
}

// stateUsed records that the sign-in with this state came back, and says whether it already had.
func (s *Server) stateUsed(state string, now time.Time) bool {
	s.sso.mu.Lock()
	defer s.sso.mu.Unlock()
	for key, at := range s.sso.usedStates {
		if now.Sub(at) > oidcStateTTL {
			delete(s.sso.usedStates, key)
		}
	}
	if _, used := s.sso.usedStates[state]; used {
		return true
	}
	if len(s.sso.usedStates) >= oidcUsedStateCap {
		// Only a flood of sign-ins gets here. Forgetting them costs a replay that also needs a code the provider has not used.
		s.sso.usedStates = nil
	}
	if s.sso.usedStates == nil {
		s.sso.usedStates = map[string]time.Time{}
	}
	s.sso.usedStates[state] = now
	return false
}

// ---- the cookie of a person from the provider ----

// ssoSessionClaims is what a session cookie of a person from the provider says.
type ssoSessionClaims struct {
	Email    string
	Role     string
	Identity string
	// IssuedMillis is when the person signed in, in Unix milliseconds. A session counts only if it is later than the
	// moment their sessions were last revoked.
	IssuedMillis int64
	// Org and Workspace are what the person works in, Platform is whether they act for the platform, and Grants are where
	// they may work. They are set only when organizations are on.
	Org, Workspace string
	Platform       bool
	Grants         []sessionGrant
	// Expires is when the cookie ends, in Unix seconds. It is read from a cookie and not written to one.
	Expires int64
}

// hasTenancy is whether the cookie carries what organizations need.
func (c ssoSessionClaims) hasTenancy() bool {
	return c.Org != "" || c.Workspace != "" || c.Platform || len(c.Grants) > 0
}

func issueSSOSession(sessionKey []byte, claims ssoSessionClaims, ttl time.Duration, now time.Time) (string, error) {
	nonce, err := randomDashboardBytes(16)
	if err != nil {
		return "", err
	}
	fields := []string{
		strconv.FormatInt(now.Add(ttl).Unix(), 10), hex.EncodeToString(nonce),
		encodeSessionField(claims.Email), encodeSessionField(claims.Role),
		strconv.FormatInt(claims.IssuedMillis, 10), encodeSessionField(claims.Identity),
	}
	if claims.hasTenancy() {
		platformFlag := "0"
		if claims.Platform {
			platformFlag = "1"
		}
		fields = append(fields, encodeSessionField(claims.Org), encodeSessionField(claims.Workspace), platformFlag, encodeSessionField(encodeGrants(claims.Grants)))
	}
	payload := strings.Join(fields, ":")
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(hmacSHA256Bytes(sessionKey, payload)), nil
}

// parseSSOSession reads the cookie of a person from the provider. It is false for a cookie that is not signed with the
// key, has expired, or is a local sign-in's.
func parseSSOSession(sessionKey []byte, token string, now time.Time) (ssoSessionClaims, bool) {
	encoded, macHex, found := strings.Cut(token, ".")
	if !found {
		return ssoSessionClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !hmac.Equal([]byte(hex.EncodeToString(hmacSHA256Bytes(sessionKey, string(payload)))), []byte(macHex)) {
		return ssoSessionClaims{}, false
	}
	parts := strings.Split(string(payload), ":")
	if len(parts) != ssoSessionFields && len(parts) != ssoSessionTenantFields {
		return ssoSessionClaims{}, false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() >= expiry {
		return ssoSessionClaims{}, false
	}
	email, emailOK := decodeSessionField(parts[2])
	role, roleOK := decodeSessionField(parts[3])
	identity, identityOK := decodeSessionField(parts[5])
	issued, issuedErr := strconv.ParseInt(parts[4], 10, 64)
	if !emailOK || !roleOK || !identityOK || issuedErr != nil || email == "" || identity == "" {
		return ssoSessionClaims{}, false
	}
	claims := ssoSessionClaims{Email: email, Role: role, Identity: identity, IssuedMillis: issued, Expires: expiry}
	if len(parts) == ssoSessionTenantFields {
		org, orgOK := decodeSessionField(parts[6])
		workspace, workspaceOK := decodeSessionField(parts[7])
		encodedGrants, grantsOK := decodeSessionField(parts[9])
		grants, grantsParsed := decodeGrants(encodedGrants)
		if !orgOK || !workspaceOK || !grantsOK || !grantsParsed || (parts[8] != "0" && parts[8] != "1") {
			return ssoSessionClaims{}, false
		}
		claims.Org, claims.Workspace, claims.Platform, claims.Grants = org, workspace, parts[8] == "1", grants
	}
	return claims, true
}

// ssoSession is the session of a person from the provider, or nil when it no longer counts: the mode is not external
// any more, or the person signed out (or the provider said they did) after this one began.
func (s *Server) ssoSession(r *http.Request, auth *dashboardAuth, claims ssoSessionClaims) *dashboardSessionInfo {
	if claims.IssuedMillis <= auth.ssoNotBefore || claims.IssuedMillis <= auth.ssoRevoked[claims.Identity] || !s.externalActiveForRequest(r) {
		return nil
	}
	// A session made through an organization's own provider counts only while that provider is turned on and as it was when the
	// person signed in. It is never the platform's.
	_, _, throughOrgIdP := parseOrgIdentity(claims.Identity)
	if throughOrgIdP && (!s.orgIdPStillStands(r.Context(), claims.Identity) || claims.Platform) {
		return nil
	}
	role := normalizeDashboardRole(claims.Role)
	if role == "" {
		role = dashboardRoleViewer
	}
	return &dashboardSessionInfo{
		Username: claims.Email, Role: role, CanMutate: role == dashboardRoleAdmin, CanReadSensitive: role == dashboardRoleAdmin,
		Identity: claims.Identity, PlatformAdmin: claims.Platform, Org: claims.Org, Workspace: claims.Workspace, Grants: claims.Grants,
	}
}

// parseSSORevocations reads when each person's sessions were last revoked, in Unix milliseconds.
func parseSSORevocations(secret *corev1.Secret) map[string]int64 {
	if secret == nil {
		return map[string]int64{}
	}
	return parseSSORevocationData(secret.Data[ssoRevocationsKey])
}

func parseSSORevocationData(data []byte) map[string]int64 {
	revoked := map[string]int64{}
	if len(data) > 0 {
		// A table that cannot be read is an empty one: what it protects is only the moment of a sign-out.
		_ = json.Unmarshal(data, &revoked)
	}
	return revoked
}

// parseSSONotBefore reads the moment before which no session of a person from the provider counts, or zero.
func parseSSONotBefore(secret *corev1.Secret) int64 {
	if secret == nil {
		return 0
	}
	moment, _ := strconv.ParseInt(strings.TrimSpace(string(secret.Data[ssoNotBeforeKey])), 10, 64)
	return moment
}

// mutateAuthSecret changes the users Secret under the lock the other writers of it take, trying again when someone
// else wrote it in between, and making it when there is none (a dashboard whose users are in the environment has no
// Secret until something needs one).
func (s *Server) mutateAuthSecret(ctx context.Context, mutate func(data map[string][]byte) error) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	key := client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}
	retryable := func(err error) bool { return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) }
	return retry.OnError(writeRetry, retryable, func() error {
		secret := &corev1.Secret{}
		err := s.Client.Get(asSystem(ctx), key, secret)
		if apierrors.IsNotFound(err) {
			data := map[string][]byte{}
			if err := mutate(data); err != nil {
				return err
			}
			return s.Client.Create(asSystem(ctx), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Type: corev1.SecretTypeOpaque, Data: data})
		}
		if err != nil {
			return err
		}
		latest := secret.DeepCopy()
		if latest.Data == nil {
			latest.Data = map[string][]byte{}
		}
		if err := mutate(latest.Data); err != nil {
			return err
		}
		return s.Client.Update(asSystem(ctx), latest)
	})
}

// revokeSSOIdentity ends every session a person has, in every browser: only those that began after now count.
func (s *Server) revokeSSOIdentity(ctx context.Context, identity string) error {
	s.forgetRefresh(ctx, identity)
	return s.mutateAuthSecret(ctx, func(data map[string][]byte) error {
		revoked := parseSSORevocationData(data[ssoRevocationsKey])
		now := time.Now()
		for key, at := range revoked {
			if now.Sub(time.UnixMilli(at)) > ssoRevocationKeep {
				delete(revoked, key)
			}
		}
		if len(revoked) >= ssoRevocationCap {
			// Only reached by a flood of real sign-outs; the oldest go first.
			keys := make([]string, 0, len(revoked))
			for key := range revoked {
				keys = append(keys, key)
			}
			slices.SortFunc(keys, func(a, b string) int { return int(revoked[a] - revoked[b]) })
			for _, key := range keys[:len(keys)-ssoRevocationCap+1] {
				delete(revoked, key)
			}
		}
		revoked[identity] = now.UnixMilli()
		payload, err := json.Marshal(revoked)
		if err != nil {
			return err
		}
		data[ssoRevocationsKey] = payload
		return nil
	})
}

// revokeAllSSOSessions ends the session of every person who signed in through the provider.
func (s *Server) revokeAllSSOSessions(ctx context.Context) error {
	s.forgetAllRefresh(ctx)
	return s.mutateAuthSecret(ctx, func(data map[string][]byte) error {
		data[ssoNotBeforeKey] = []byte(strconv.FormatInt(time.Now().UnixMilli(), 10))
		return nil
	})
}

// revokeAPITokensCreatedByID revokes the tokens and deploy hooks a person from the provider made, which is what a
// person who is gone must not keep.
func (s *Server) revokeAPITokensCreatedByID(ctx context.Context, identity string) (int, error) {
	count := 0
	err := s.mutateAPITokens(ctx, func(records map[string]apitokens.Record) (bool, error) {
		count = 0
		for id, record := range records {
			if record.CreatedByID == identity {
				delete(records, id)
				s.tokenUsed.Delete(id)
				s.releaseHook(id)
				count++
			}
		}
		return count > 0, nil
	})
	return count, err
}

// ---- the state cookie ----

type oidcStateClaims struct {
	State, Nonce, Verifier, Purpose, Next string
	// Org is the organization whose own provider the sign-in goes to, or empty for the platform's.
	Org string
}

// signOIDCState makes the state cookie's value. It is signed with the session key under its own label, so a session
// cookie cannot be passed off as one or the other way round.
func signOIDCState(sessionKey []byte, claims oidcStateClaims, now time.Time) string {
	fields := []string{
		strconv.FormatInt(now.Add(oidcStateTTL).Unix(), 10), encodeSessionField(claims.State), encodeSessionField(claims.Nonce),
		encodeSessionField(claims.Verifier), encodeSessionField(claims.Purpose), encodeSessionField(claims.Next),
	}
	if claims.Org != "" {
		fields = append(fields, encodeSessionField(claims.Org))
	}
	payload := strings.Join(fields, ":")
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(hmacSHA256Bytes(sessionKey, "oidc-state\x00"+payload))
}

func parseOIDCState(sessionKey []byte, value string, now time.Time) (oidcStateClaims, bool) {
	encoded, macHex, found := strings.Cut(value, ".")
	if !found {
		return oidcStateClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !hmac.Equal([]byte(hex.EncodeToString(hmacSHA256Bytes(sessionKey, "oidc-state\x00"+string(payload)))), []byte(macHex)) {
		return oidcStateClaims{}, false
	}
	parts := strings.Split(string(payload), ":")
	if len(parts) != 6 && len(parts) != 7 {
		return oidcStateClaims{}, false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() >= expiry {
		return oidcStateClaims{}, false
	}
	var fields [5]string
	for i := range fields {
		decoded, ok := decodeSessionField(parts[i+1])
		if !ok {
			return oidcStateClaims{}, false
		}
		fields[i] = decoded
	}
	claims := oidcStateClaims{State: fields[0], Nonce: fields[1], Verifier: fields[2], Purpose: fields[3], Next: fields[4]}
	if len(parts) == 7 {
		org, ok := decodeSessionField(parts[6])
		if !ok || org == "" {
			return oidcStateClaims{}, false
		}
		claims.Org = org
	}
	if claims.State == "" || claims.Nonce == "" || claims.Verifier == "" || (claims.Purpose != purposeLogin && claims.Purpose != purposeTest) {
		return oidcStateClaims{}, false
	}
	return claims, true
}

// stateCookiePath is where the state cookie is sent. A "__Host-" cookie has to be for the whole site, so it is; the plain
// one, for a dashboard on this machine over http, is only sent to /auth/oidc.
func stateCookiePath(r *http.Request) string {
	if dashboardCookieSecure(r) {
		return "/"
	}
	return "/auth/oidc"
}

func oidcStateCookieFor(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: cookieName(r, oidcStateCookie), Value: value, Path: stateCookiePath(r), MaxAge: maxAge,
		HttpOnly: true,
		// The provider sends the browser back with a top-level navigation, which Lax allows and Strict would not.
		SameSite: http.SameSiteLaxMode,
		Secure:   dashboardCookieSecure(r),
	}
}

// ssoRedirectURL is where the provider sends the browser back to. When the dashboard is published at an address (its
// domain is verified) that is the address, and not the Host the request happened to carry: a provider whose redirect list
// is permissive (a wildcard host) would otherwise deliver a code to whatever host an attacker got the dashboard to be
// asked under. Otherwise (no domain yet, a dashboard reached on this machine) it is the address the browser used, so that
// the state cookie set at the start is sent to the callback; the provider then only redirects to what was registered.
func ssoRedirectURL(r *http.Request, dashboardURL string) string {
	if dashboardURL != "" {
		return strings.TrimRight(dashboardURL, "/") + oidcCallbackPath
	}
	scheme := "http"
	if requestIsHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + oidcCallbackPath
}

// safeNextPath is where to land after signing in: a path on this dashboard, never an address elsewhere.
func safeNextPath(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") ||
		strings.HasPrefix(next, "/auth/") || strings.HasPrefix(next, "/api/") || strings.ContainsAny(next, "\r\n\t\\") || len(next) > 512 {
		return "/"
	}
	return next
}

// ssoFailure sends the browser to where the person can see what went wrong: the sign-in page, or the settings page for
// a test. The code is one of a fixed few, never text the provider supplied.
func ssoFailure(w http.ResponseWriter, r *http.Request, purpose, code string) {
	page := "/"
	if purpose == purposeTest {
		page = ssoSettingsPath
	}
	http.Redirect(w, r, page+"?sso_error="+code, http.StatusFound)
}

// ---- the three routes ----

// handleOIDCLogin sends the browser to the provider. Anyone may start a sign-in while the mode is external; a test of
// the settings may only be started by a signed-in admin, and works in any mode.
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	logger := ctrllog.FromContext(r.Context())
	purpose := purposeLogin
	if r.URL.Query().Get("purpose") == purposeTest {
		purpose = purposeTest
		if tokenFromRequest(r) != nil || !s.sessionCanMutate(r) {
			ssoFailure(w, r, purpose, "forbidden")
			return
		}
	}
	// A sign-in that names an organization goes to that organization's own provider, when it has one that is turned on.
	orgName := strings.TrimSpace(r.URL.Query().Get("org"))
	var settings ssoSettings
	var err error
	if orgName != "" {
		// A provider is tested by the platform's administrators, who are the ones that turn it on.
		if session := s.currentSession(r); purpose == purposeTest && (session == nil || !session.PlatformAdmin && s.tenancyOn(r.Context())) {
			ssoFailure(w, r, purpose, "forbidden")
			return
		}
		if settings, _, err = s.orgSigninSettings(r.Context(), orgName, purpose); err != nil {
			code := "disabled"
			if !errors.Is(err, errOrgSignIn) {
				logger.Error(err, "Could not read the sign-in settings of an organization")
				code = ssoUnavailable
			}
			ssoFailure(w, r, purpose, code)
			return
		}
	} else {
		if settings, err = s.ssoSettings(r.Context()); err != nil {
			logger.Error(err, "Could not read the sign-in settings")
			ssoFailure(w, r, purpose, ssoUnavailable)
			return
		}
		if (purpose == purposeLogin && !settings.External()) || (purpose == purposeTest && !settings.Configured()) {
			ssoFailure(w, r, purpose, "disabled")
			return
		}
	}
	provider, err := s.providerFor(r.Context(), orgName, settings)
	if err != nil {
		logger.Error(err, "Could not reach the identity provider", "issuer", settings.Config.Issuer)
		ssoFailure(w, r, purpose, "provider")
		return
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		ssoFailure(w, r, purpose, ssoUnavailable)
		return
	}
	random, err := randomDashboardBytes(32)
	if err != nil {
		ssoFailure(w, r, purpose, ssoUnavailable)
		return
	}
	claims := oidcStateClaims{
		State: hex.EncodeToString(random[:16]), Nonce: hex.EncodeToString(random[16:]), Verifier: oidcauth.NewVerifier(),
		Purpose: purpose, Next: safeNextPath(r.URL.Query().Get("next")), Org: orgName,
	}
	http.SetCookie(w, oidcStateCookieFor(r, signOIDCState(auth.sessionKey, claims, time.Now()), int(oidcStateTTL.Seconds())))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, provider.AuthCodeURL(ssoRedirectURL(r, settings.DashboardURL), claims.State, claims.Nonce, claims.Verifier), http.StatusFound)
}

// handleOIDCCallback is where the provider sends the browser back with a code. It checks that the browser is the one
// that started the sign-in, exchanges the code, and either starts a session or, for a test, records what was found.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	logger := ctrllog.FromContext(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	// The state is good once: it is cleared whatever happens next.
	http.SetCookie(w, oidcStateCookieFor(r, "", -1))
	cookie, cookieErr := r.Cookie(cookieName(r, oidcStateCookie))
	auth, authErr := s.dashboardAuth(r)
	if cookieErr != nil || authErr != nil {
		ssoFailure(w, r, purposeLogin, "state")
		return
	}
	state, ok := parseOIDCState(auth.sessionKey, cookie.Value, time.Now())
	query := r.URL.Query()
	if !ok || subtle.ConstantTimeCompare([]byte(state.State), []byte(query.Get("state"))) != 1 {
		ssoFailure(w, r, purposeLogin, "state")
		return
	}
	var settings ssoSettings
	if state.Org != "" {
		var orgErr error
		if settings, _, orgErr = s.orgSigninSettings(r.Context(), state.Org, state.Purpose); orgErr != nil {
			code := "disabled"
			if !errors.Is(orgErr, errOrgSignIn) {
				code = ssoUnavailable
			}
			ssoFailure(w, r, state.Purpose, code)
			return
		}
	} else {
		var settingsErr error
		if settings, settingsErr = s.ssoSettings(r.Context()); settingsErr != nil {
			ssoFailure(w, r, state.Purpose, ssoUnavailable)
			return
		}
		if (state.Purpose == purposeLogin && !settings.External()) || (state.Purpose == purposeTest && !settings.Configured()) {
			ssoFailure(w, r, state.Purpose, "disabled")
			return
		}
	}
	// The state cookie is good for one callback: a replay of it is refused before anything is asked of the provider.
	if s.stateUsed(state.State, time.Now()) {
		ssoFailure(w, r, state.Purpose, "state")
		return
	}
	attempt := ssoAttempt{state: state, settings: settings, auth: auth}
	if refused := query.Get("error"); refused != "" {
		logger.Info("Identity provider did not sign the person in", "error", truncate(refused, 80))
		s.finishSSO(w, r, attempt, nil, &oidcauth.Denied{Reason: "provider_denied", Detail: "the provider refused the sign-in (" + truncate(refused, 80) + ")"})
		return
	}
	// A provider that says who answered (RFC 9207) must be the one this sign-in was sent to, which is what stops one
	// provider's answer being passed off as another's.
	if issuer := query.Get("iss"); issuer != "" && issuer != settings.Config.Issuer {
		s.finishSSO(w, r, attempt, nil, errors.New("the answer came from "+truncate(issuer, 200)+", not from the configured issuer"))
		return
	}
	if query.Get("code") == "" {
		s.finishSSO(w, r, attempt, nil, &oidcauth.Denied{Reason: "provider_denied", Detail: "the provider sent no code"})
		return
	}
	provider, err := s.providerFor(r.Context(), state.Org, settings)
	if err != nil {
		logger.Error(err, "Could not reach the identity provider", "issuer", settings.Config.Issuer)
		s.finishSSO(w, r, attempt, nil, err)
		return
	}
	identity, err := provider.Exchange(r.Context(), ssoRedirectURL(r, settings.DashboardURL), query.Get("code"), state.Verifier, state.Nonce)
	s.finishSSO(w, r, attempt, identity, err)
}

// ssoAttempt is what a sign-in or a test knows when the provider has answered.
type ssoAttempt struct {
	state    oidcStateClaims
	settings ssoSettings
	auth     *dashboardAuth
}

// finishSSO ends a sign-in or a test, given what the exchange found.
func (s *Server) finishSSO(w http.ResponseWriter, r *http.Request, attempt ssoAttempt, identity *oidcauth.Identity, exchangeErr error) {
	if attempt.state.Org != "" {
		s.finishOrgSSO(w, r, attempt, identity, exchangeErr)
		return
	}
	logger := ctrllog.FromContext(r.Context())
	reason, detail := ssoOutcomeReason(exchangeErr)
	if exchangeErr != nil && reason == "provider" {
		logger.Error(exchangeErr, "Could not finish signing in through the identity provider")
	}
	if attempt.state.Purpose == purposeTest {
		result := ssoTestResult{Fingerprint: attempt.settings.Config.Fingerprint(), At: time.Now(), OK: exchangeErr == nil, Reason: reason, Detail: detail}
		if identity != nil {
			result.Email, result.Role = identity.Email, identity.Role
			if attempt.settings.Config.Tenancy.On() {
				result.Platform = identity.Platform
				result.Orgs = s.testOrganizations(r.Context(), identity)
			}
			if identity.Role != oidcauth.RoleAdmin {
				result.OK, result.Reason = false, "not_admin"
				result.Detail = "you signed in as " + identity.Email + ", who maps to " + identity.Role + "; sign in as someone who maps to admin, so that the provider can run Geass"
			}
		}
		s.recordSSOTest(result)
		http.Redirect(w, r, ssoSettingsPath+"?sso_test=done", http.StatusFound)
		return
	}
	if exchangeErr != nil {
		logger.Info("Identity provider sign-in was not allowed", "reason", reason)
		ssoFailure(w, r, purposeLogin, reason)
		return
	}
	now := time.Now()
	ttl := attempt.settings.sessionTTL()
	claims := ssoSessionClaims{Email: identity.Email, Role: identity.Role, Identity: identity.Key, IssuedMillis: now.UnixMilli()}
	if attempt.settings.TenancyOn {
		if refused := s.placeInOrganization(r.Context(), &claims, identity, attempt.settings); refused != "" {
			logger.Info("Identity provider sign-in has no organization to work in", "user", identity.Email)
			ssoFailure(w, r, purposeLogin, refused)
			return
		}
	}
	token, err := issueSSOSession(attempt.auth.sessionKey, claims, ttl, now)
	if err != nil {
		ssoFailure(w, r, purposeLogin, ssoUnavailable)
		return
	}
	http.SetCookie(w, dashboardSessionCookie(r, token, int(ttl.Seconds())))
	s.rememberRefresh(r.Context(), attempt.settings, attempt.auth.sessionKey, identity, now.Add(ttl), identity.Key, "")
	logger.Info("Signed a person in through the identity provider", "user", identity.Email, "access", identity.Role)
	http.Redirect(w, r, attempt.state.Next, http.StatusFound)
}

// ssoOutcomeReason is the short code for what went wrong, safe for a URL, and the longer text for an admin.
func ssoOutcomeReason(err error) (reason, detail string) {
	if err == nil {
		return "", ""
	}
	var denied *oidcauth.Denied
	if errors.As(err, &denied) {
		return denied.Reason, denied.Detail
	}
	return "provider", err.Error()
}

// handleOIDCBackchannelLogout is called by the provider, not by a browser, when a person signed out there or was
// removed: it ends their sessions here and revokes the tokens they made. Its credential is the provider's signature
// on the logout token. Anything else is a 400, and a source that keeps sending them is slowed down.
func (s *Server) handleOIDCBackchannelLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{dashboardLiteralError: "method not allowed"})
		return
	}
	logger := ctrllog.FromContext(r.Context())
	source := loginSourceKey(r)
	if s.loginSourceBlocked(source) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{dashboardLiteralError: "too many requests"})
		return
	}
	reject := func() {
		s.noteLoginSourceFailure(source)
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "invalid_request"})
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		reject()
		return
	}
	settings, err := s.ssoSettings(r.Context())
	if err != nil || !settings.Configured() {
		reject()
		return
	}
	provider, err := s.ssoProvider(r.Context(), settings)
	if err != nil {
		logger.Error(err, "Could not reach the identity provider", "issuer", settings.Config.Issuer)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{dashboardLiteralError: "identity provider unavailable"})
		return
	}
	logout, err := provider.VerifyLogoutToken(r.Context(), r.PostForm.Get("logout_token"), time.Now())
	if err != nil {
		logger.Info("Refused a logout token", "reason", truncate(err.Error(), 120))
		reject()
		return
	}
	// A token sent twice is answered the same and acted on once.
	if s.logoutSeen(logout.ID, time.Now()) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if err := s.revokeSSOIdentity(r.Context(), logout.Key); err != nil {
		logger.Error(err, "Could not end the sessions of a person the identity provider signed out")
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not end the sessions"})
		return
	}
	// Whether what the person made goes with their sessions is the admin's choice (see GeassPlatformOIDCSpec).
	revoked := 0
	if settings.RevokeTokensOnLogout {
		revoked, err = s.revokeAPITokensCreatedByID(r.Context(), logout.Key)
		if err != nil {
			logger.Error(err, "Could not revoke the tokens of a person the identity provider signed out")
			writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not revoke the tokens"})
			return
		}
	}
	// Only now: a token that could not do its work must be able to be sent again.
	s.rememberLogout(logout.ID, time.Now())
	logger.Info("Ended the sessions of a person the identity provider signed out", "tokensRevoked", revoked, "revokeTokens", settings.RevokeTokensOnLogout)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// truncate cuts text to at most limit bytes, at the start of a character: a value the provider sent is not cut in the
// middle of one, which would leave bytes that are not text.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
