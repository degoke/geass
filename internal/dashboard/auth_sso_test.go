package dashboard

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

const (
	ssoTestClient = "geass"
	ssoTestSecret = "client-secret-1"
)

// testIdP is an identity provider that is only as much as sign-in needs: discovery, keys, an authorize step that
// signs in whoever the test says is at the keyboard, and a token endpoint that checks what a real one checks.
type testIdP struct {
	t      *testing.T
	server *httptest.Server
	key    ed25519.PrivateKey
	public ed25519.PublicKey

	mu    sync.Mutex
	codes map[string]testCode
	// person is who signs in at the next authorize step: the claims of their ID token.
	person map[string]any
	// tokenCalls and discoveryCalls count what the dashboard asked of the provider; discoveryDown makes discovery fail.
	tokenCalls, discoveryCalls atomic.Int32
	discoveryDown              atomic.Bool
}

type testCode struct {
	challenge, nonce, redirect string
	person                     map[string]any
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	t.Setenv("GEASS_ALLOW_LOOPBACK", "true")
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	p := &testIdP{t: t, key: private, public: public, codes: map[string]testCode{}, person: testPerson("u-alice", "alice@example.com", "owner")}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		p.discoveryCalls.Add(1)
		if p.discoveryDown.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.server.URL, "authorization_endpoint": p.server.URL + "/authorize", "token_endpoint": p.server.URL + "/token",
			"jwks_uri": p.server.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"EdDSA"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: p.public, KeyID: "k1", Algorithm: "EdDSA", Use: "sig"}}})
	})
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.token)
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func testPerson(subject, email, role string) map[string]any {
	return map[string]any{"sub": subject, "email": email, "email_verified": true, "org_role": role, "organization_id": "org_acme"}
}

// authorize is the provider's end of the redirect: it signs the person in and sends the browser back with a code.
func (p *testIdP) authorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Get("client_id") != ssoTestClient || query.Get("response_type") != "code" || query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") == "" || query.Get("nonce") == "" || !strings.HasPrefix(query.Get("redirect_uri"), "http://example.com/auth/oidc/callback") {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	code := "code-" + query.Get("state")
	p.mu.Lock()
	p.codes[code] = testCode{challenge: query.Get("code_challenge"), nonce: query.Get("nonce"), redirect: query.Get("redirect_uri"), person: maps.Clone(p.person)}
	p.mu.Unlock()
	back := url.Values{"code": {code}, "state": {query.Get("state")}, "iss": {p.server.URL}}
	http.Redirect(w, r, query.Get("redirect_uri")+"?"+back.Encode(), http.StatusFound)
}

func (p *testIdP) token(w http.ResponseWriter, r *http.Request) {
	p.tokenCalls.Add(1)
	w.Header().Set("Content-Type", "application/json")
	_ = r.ParseForm()
	user, password, basic := r.BasicAuth()
	if !basic {
		user, password = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	p.mu.Lock()
	issued, ok := p.codes[r.PostForm.Get("code")]
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if user != ssoTestClient || password != ssoTestSecret || !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != issued.challenge || r.PostForm.Get("redirect_uri") != issued.redirect {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	p.mu.Lock()
	delete(p.codes, r.PostForm.Get("code"))
	p.mu.Unlock()
	claims := map[string]any{"iss": p.server.URL, "aud": ssoTestClient, "nonce": issued.nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	maps.Copy(claims, issued.person)
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": p.sign(claims)})
}

func (p *testIdP) sign(claims map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: jose.JSONWebKey{Key: p.key, KeyID: "k1"}}, (&jose.SignerOptions{}).WithType("JWT"))
	require.NoError(p.t, err)
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(p.t, err)
	return token
}

// logoutToken is what the provider sends to the back-channel logout address when someone signed out there.
func (p *testIdP) logoutToken(subject string, extra map[string]any) string {
	claims := map[string]any{
		"iss": p.server.URL, "aud": ssoTestClient, "sub": subject, "jti": "jti-" + subject + "-" + time.Now().Format("150405.000000"),
		"iat": time.Now().Unix(), "exp": time.Now().Add(2 * time.Minute).Unix(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}},
	}
	for key, value := range extra {
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}
	}
	return p.sign(claims)
}

type ssoFixture struct {
	tokenFixture
	idp *testIdP
}

// newSSOFixture is the token fixture (an admin and a viewer with passwords) with an identity provider set up. mode is
// the one the platform starts in.
func newSSOFixture(t *testing.T, mode string) ssoFixture {
	t.Helper()
	f := ssoFixture{tokenFixture: newTokenFixture(t), idp: newTestIdP(t)}
	ctx := context.Background()
	require.NoError(t, f.srv.Client.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformAuthSecretName, Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.PlatformAuthClientSecretKey: []byte(ssoTestSecret)},
	}))
	require.NoError(t, f.srv.Client.Create(ctx, &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Auth: &geassv1alpha1.GeassPlatformAuthSpec{
			Mode: mode,
			OIDC: &geassv1alpha1.GeassPlatformOIDCSpec{
				DisplayName: "Acme", Issuer: f.idp.server.URL, ClientID: ssoTestClient, SessionMinutes: 60,
				CredentialRef: &corev1.LocalObjectReference{Name: platform.PlatformAuthSecretName},
				RoleClaim:     "org_role", RoleMap: map[string]string{"owner": "admin", "admin": "admin", "member": "viewer"},
				RequireClaim: map[string]string{"organization_id": "org_acme"},
			},
		}},
	}))
	return f
}

func (f ssoFixture) config(t *testing.T) geassv1alpha1.GeassPlatformConfig {
	t.Helper()
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	return config
}

func (f ssoFixture) updateConfig(t *testing.T, change func(*geassv1alpha1.GeassPlatformConfig)) {
	t.Helper()
	config := f.config(t)
	change(&config)
	require.NoError(t, f.srv.Client.Update(context.Background(), &config))
}

func (f ssoFixture) setMode(t *testing.T, mode string) {
	t.Helper()
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth.Mode = mode })
}

func browserRequest(method, target string, cookies ...*http.Cookie) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	return req
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// startSignIn is the first leg: the browser asks Geass to sign in, and is sent to the provider.
func (f ssoFixture) startSignIn(t *testing.T, target string, cookies ...*http.Cookie) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	rec := f.do(browserRequest(http.MethodGet, target, cookies...))
	return rec, cookieNamed(rec, testStateCookie)
}

// atProvider is the second leg: the browser goes to the provider, which signs the person in and sends it back.
func (f ssoFixture) atProvider(t *testing.T, location string) string {
	t.Helper()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(location)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	return resp.Header.Get("Location")
}

// signIn does a whole sign-in as the person the provider has at the keyboard, and returns the callback's answer.
func (f ssoFixture) signIn(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	start, state := f.startSignIn(t, oidcLoginPath)
	require.Equal(t, http.StatusFound, start.Code, start.Body.String())
	require.NotNil(t, state, "the sign-in set no state cookie")
	back := f.atProvider(t, start.Header().Get("Location"))
	target, err := url.Parse(back)
	require.NoError(t, err)
	return f.do(browserRequest(http.MethodGet, target.RequestURI(), state))
}

// signedInAs signs a person in and returns their session cookie.
func (f ssoFixture) signedInAs(t *testing.T, person map[string]any) *http.Cookie {
	t.Helper()
	f.idp.mu.Lock()
	f.idp.person = person
	f.idp.mu.Unlock()
	rec := f.signIn(t)
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "/", rec.Header().Get("Location"), "a successful sign-in lands on the dashboard")
	session := cookieNamed(rec, testSessionCookie)
	require.NotNil(t, session, "a successful sign-in sets a session cookie")
	return session
}

func (f ssoFixture) session(t *testing.T, cookies ...*http.Cookie) map[string]any {
	t.Helper()
	rec := f.do(browserRequest(http.MethodGet, "/api/session", cookies...))
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func (f ssoFixture) authenticated(t *testing.T, cookie *http.Cookie) bool {
	t.Helper()
	return f.session(t, cookie)["authenticated"] == true
}

// failureOf is the code the sign-in page is sent back with.
func failureOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	require.Equal(t, http.StatusFound, rec.Code)
	location, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	return location.Query().Get("sso_error")
}

func TestSigningInThroughTheProviderStartsASession(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)

	cookie := f.signedInAs(t, testPerson("u-alice", "Alice@Example.com", "owner"))

	body := f.session(t, cookie)
	require.Equal(t, true, body["authenticated"])
	require.Equal(t, "alice@example.com", body["username"], "the person is named by their email")
	require.Equal(t, "admin", body["role"])
	require.Equal(t, true, body["canMutate"])
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	require.Equal(t, 3600, cookie.MaxAge, "a sign-in lasts as long as the settings say")
}

func TestTheProvidersRoleBecomesTheDashboardRole(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	viewer := f.signedInAs(t, testPerson("u-bob", "bob@example.com", "member"))

	body := f.session(t, viewer)
	require.Equal(t, "viewer", body["role"])
	require.Equal(t, false, body["canMutate"])

	// What the viewer cannot do is refused as for any viewer.
	rec := f.do(browserRequest(http.MethodGet, "/api/tokens", viewer))
	require.Equal(t, http.StatusForbidden, rec.Code)
	post := postForm("/api/tokens/create", url.Values{"name": {"x"}, "scope": {"workspace"}})
	post.AddCookie(viewer)
	require.Equal(t, http.StatusForbidden, f.do(post).Code)
}

func TestAPersonTheInstallationDoesNotLetInIsTurnedAway(t *testing.T) {
	for name, tc := range map[string]struct {
		person map[string]any
		code   string
	}{
		"another organization": {map[string]any{"sub": "u-x", "email": "x@example.com", "org_role": "owner", "organization_id": "org_other"}, "required_claim"},
		"no role here":         {map[string]any{"sub": "u-x", "email": "x@example.com", "org_role": "guest", "organization_id": "org_acme"}, "no_role"},
		"no email":             {map[string]any{"sub": "u-x", "org_role": "owner", "organization_id": "org_acme"}, "no_email"},
		"an unverified email":  {map[string]any{"sub": "u-x", "email": "x@example.com", "email_verified": false, "org_role": "owner", "organization_id": "org_acme"}, "email_unverified"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSSOFixture(t, platform.AuthModeExternal)
			f.idp.person = tc.person

			rec := f.signIn(t)

			require.Equal(t, tc.code, failureOf(t, rec))
			require.Nil(t, cookieNamed(rec, testSessionCookie), "a person who was turned away got a session")
		})
	}
}

func TestSigningInNeedsTheBrowserThatStartedIt(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	start, state := f.startSignIn(t, oidcLoginPath)
	back, err := url.Parse(f.atProvider(t, start.Header().Get("Location")))
	require.NoError(t, err)

	t.Run("no state cookie", func(t *testing.T) {
		require.Equal(t, "state", failureOf(t, f.do(browserRequest(http.MethodGet, back.RequestURI()))))
	})
	t.Run("the cookie of another sign-in", func(t *testing.T) {
		_, otherState := f.startSignIn(t, oidcLoginPath)
		require.Equal(t, "state", failureOf(t, f.do(browserRequest(http.MethodGet, back.RequestURI(), otherState))))
	})
	t.Run("a cookie that was changed", func(t *testing.T) {
		forged := *state
		forged.Value = strings.Replace(state.Value, ".", "x.", 1)
		require.Equal(t, "state", failureOf(t, f.do(browserRequest(http.MethodGet, back.RequestURI(), &forged))))
	})
	t.Run("a state that is not the one in the cookie", func(t *testing.T) {
		query := back.Query()
		query.Set("state", "someone-elses")
		require.Equal(t, "state", failureOf(t, f.do(browserRequest(http.MethodGet, "/auth/oidc/callback?"+query.Encode(), state))))
	})
	t.Run("a cookie that has expired", func(t *testing.T) {
		auth, err := f.srv.dashboardAuth(browserRequest(http.MethodGet, "/"))
		require.NoError(t, err)
		claims, ok := parseOIDCState(auth.sessionKey, state.Value, time.Now())
		require.True(t, ok)
		stale := *state
		stale.Value = signOIDCState(auth.sessionKey, claims, time.Now().Add(-time.Hour))
		require.Equal(t, "state", failureOf(t, f.do(browserRequest(http.MethodGet, back.RequestURI(), &stale))))
	})
	t.Run("a session cookie passed off as the state", func(t *testing.T) {
		session := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
		forged := &http.Cookie{Name: testStateCookie, Value: session.Value}
		require.Equal(t, "state", failureOf(t, f.do(browserRequest(http.MethodGet, back.RequestURI(), forged))))
	})
	t.Run("the right one works, once", func(t *testing.T) {
		first := f.do(browserRequest(http.MethodGet, back.RequestURI(), state))
		require.Equal(t, "/", first.Header().Get("Location"))
		require.NotNil(t, cookieNamed(first, testSessionCookie))
		require.Less(t, cookieNamed(first, testStateCookie).MaxAge, 0, "the state cookie is cleared")
		replay := f.do(browserRequest(http.MethodGet, back.RequestURI(), state))
		require.NotEqual(t, "/", replay.Header().Get("Location"), "the same answer was accepted twice")
		require.Nil(t, cookieNamed(replay, testSessionCookie))
	})
}

func TestTheAnswerMustComeFromTheConfiguredProvider(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	start, state := f.startSignIn(t, oidcLoginPath)
	back, err := url.Parse(f.atProvider(t, start.Header().Get("Location")))
	require.NoError(t, err)
	query := back.Query()
	query.Set("iss", "https://evil.example.com")

	rec := f.do(browserRequest(http.MethodGet, "/auth/oidc/callback?"+query.Encode(), state))

	require.Equal(t, "provider", failureOf(t, rec))
	require.Nil(t, cookieNamed(rec, testSessionCookie))
}

func TestAProviderThatRefusesIsReportedWithoutItsWords(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	start, state := f.startSignIn(t, oidcLoginPath)
	query := mustQuery(t, start.Header().Get("Location"))
	denied := url.Values{"error": {"access_denied<script>"}, "state": {query.Get("state")}}

	rec := f.do(browserRequest(http.MethodGet, "/auth/oidc/callback?"+denied.Encode(), state))

	require.Equal(t, "provider_denied", failureOf(t, rec))
	require.NotContains(t, rec.Header().Get("Location"), "script", "text from the provider was put in the page address")
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	return parsed.Query()
}

func TestSigningInLeadsOnlyToAPlaceOnThisDashboard(t *testing.T) {
	for next, want := range map[string]string{
		"/projects/payments?tab=services": "/projects/payments?tab=services",
		"":                                "/",
		"//evil.example.com":              "/",
		"/\\evil.example.com":             "/",
		"https://evil.example.com":        "/",
		"javascript:alert(1)":             "/",
		"/api/tokens":                     "/",
		"/auth/oidc/login":                "/",
		"/ok\r\nSet-Cookie: x=1":          "/",
	} {
		require.Equal(t, want, safeNextPath(next), next)
	}

	f := newSSOFixture(t, platform.AuthModeExternal)
	start, state := f.startSignIn(t, oidcLoginPath+"?next=%2Fprojects%2Fpayments")
	back := f.atProvider(t, start.Header().Get("Location"))
	target, _ := url.Parse(back)
	rec := f.do(browserRequest(http.MethodGet, target.RequestURI(), state))
	require.Equal(t, "/projects/payments", rec.Header().Get("Location"))
}

func TestThePersonsSessionEndsWhenTheModeIsLocalAgain(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	require.True(t, f.authenticated(t, cookie))

	f.setMode(t, platform.AuthModeLocal)

	require.False(t, f.authenticated(t, cookie), "a session of the provider counted after the provider stopped deciding")
	rec, _ := f.startSignIn(t, oidcLoginPath)
	require.Equal(t, "disabled", failureOf(t, rec), "nobody can start signing in through the provider while it is off")
}

func TestASessionOfTheProviderCannotBeForged(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	auth, err := f.srv.dashboardAuth(browserRequest(http.MethodGet, "/"))
	require.NoError(t, err)
	claims := ssoSessionClaims{Email: "mallory@example.com", Role: "admin", Identity: "sso:x", IssuedMillis: time.Now().UnixMilli()}

	good, err := issueSSOSession(auth.sessionKey, claims, time.Hour, time.Now())
	require.NoError(t, err)
	require.True(t, f.authenticated(t, &http.Cookie{Name: testSessionCookie, Value: good}), "the fixture's own cookie should work")

	otherKey, err := issueSSOSession([]byte("a-key-that-is-not-the-dashboards-own"), claims, time.Hour, time.Now())
	require.NoError(t, err)
	require.False(t, f.authenticated(t, &http.Cookie{Name: testSessionCookie, Value: otherKey}), "a cookie signed with another key counted")

	expired, err := issueSSOSession(auth.sessionKey, claims, time.Hour, time.Now().Add(-2*time.Hour))
	require.NoError(t, err)
	require.False(t, f.authenticated(t, &http.Cookie{Name: testSessionCookie, Value: expired}), "an expired cookie counted")

	encoded, mac, _ := strings.Cut(good, ".")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	fields := strings.Split(string(payload), ":")
	require.Len(t, fields, ssoSessionFields)
	for index, replacement := range map[int]string{
		2: base64.RawURLEncoding.EncodeToString([]byte("trudy@example.com")), // who
		3: base64.RawURLEncoding.EncodeToString([]byte("viewer")),            // the role
		4: "1",                                                               // when the session began
		5: base64.RawURLEncoding.EncodeToString([]byte("sso:someone-else")),  // which person
	} {
		changed := slices.Clone(fields)
		changed[index] = replacement
		require.NotEqual(t, fields, changed)
		cookie := &http.Cookie{Name: testSessionCookie, Value: base64.RawURLEncoding.EncodeToString([]byte(strings.Join(changed, ":"))) + "." + mac}
		require.False(t, f.authenticated(t, cookie), "a cookie with field %d changed counted", index)
	}
}

func TestSigningOutEndsEveryBrowserOfThatPersonAndNobodyElses(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	alice := testPerson("u-alice", "alice@example.com", "owner")
	laptop := f.signedInAs(t, alice)
	phone := f.signedInAs(t, alice)
	bob := f.signedInAs(t, testPerson("u-bob", "bob@example.com", "member"))

	out := postForm("/api/logout", nil)
	out.AddCookie(laptop)
	require.Equal(t, http.StatusOK, f.do(out).Code)

	require.False(t, f.authenticated(t, laptop))
	require.False(t, f.authenticated(t, phone), "another browser of the same person kept its session")
	require.True(t, f.authenticated(t, bob), "signing out ended somebody else's session")

	time.Sleep(3 * time.Millisecond)
	require.True(t, f.authenticated(t, f.signedInAs(t, alice)), "the person could not sign in again")
}

func TestTheLocalAdminStillGetsInWhileTheProviderDecides(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)

	t.Run("by password", func(t *testing.T) {
		rec := f.do(postForm("/api/login", url.Values{"username": {"admin"}, "password": {"test-password"}}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, cookieNamed(rec, testSessionCookie))
	})
	t.Run("with a session from before", func(t *testing.T) {
		req := withDashboardSession(t, f.srv, browserRequest(http.MethodGet, "/api/session"), "admin", dashboardRoleAdmin)
		require.Equal(t, true, decodeBody(t, f.do(req))["authenticated"])
	})
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func TestALocalViewerIsNotLetInWhileTheProviderDecides(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	login := func() *httptest.ResponseRecorder {
		return f.do(postForm("/api/login", url.Values{"username": {"viewer"}, "password": {"test-password"}}))
	}
	require.Equal(t, http.StatusOK, login().Code, "a viewer signs in with a password in local mode")
	viewerSession := withDashboardSession(t, f.srv, browserRequest(http.MethodGet, "/api/session"), "viewer", dashboardRoleViewer)
	require.Equal(t, true, decodeBody(t, f.do(viewerSession))["authenticated"])

	f.testSignIn(t, testPerson("u-alice", "alice@example.com", "owner"))
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeExternal).Code)

	rec := login()
	require.Equal(t, http.StatusBadRequest, rec.Code, "a viewer got in by password while the provider decides")
	require.Nil(t, cookieNamed(rec, testSessionCookie))
	require.Equal(t, false, decodeBody(t, f.do(viewerCookieRequest(viewerSession)))["authenticated"], "a viewer's session from before counted after the provider took over")
	adminAfter := withAdminSession(t, f.srv, browserRequest(http.MethodGet, "/api/session"))
	require.Equal(t, true, decodeBody(t, f.do(adminAfter))["authenticated"], "turning the provider on ended an admin's session")

	f.setMode(t, platform.AuthModeLocal)
	require.Equal(t, http.StatusOK, login().Code, "going back to local brings the viewer back")
}

// viewerCookieRequest is a request that carries the session cookie of a request that was built with one.
func viewerCookieRequest(withSession *http.Request) *http.Request {
	cookie, _ := withSession.Cookie(testSessionCookie)
	return browserRequest(http.MethodGet, "/api/session", cookie)
}

func TestTheSignInPageIsToldWhenToOfferTheProvider(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	_, off := decodeBody(t, f.do(browserRequest(http.MethodGet, "/api/session")))["sso"]
	require.False(t, off, "the provider was offered while it is off")

	f.setMode(t, platform.AuthModeExternal)
	sso, on := decodeBody(t, f.do(browserRequest(http.MethodGet, "/api/session")))["sso"].(map[string]any)
	require.True(t, on)
	require.Equal(t, "Acme", sso["name"])
	for _, secret := range []string{ssoTestSecret, f.idp.server.URL} {
		require.NotContains(t, f.do(browserRequest(http.MethodGet, "/api/session")).Body.String(), secret, "the public answer leaks the setup")
	}
}

func TestATokenRemembersWhichPersonOfTheProviderMadeIt(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))

	req := postForm("/api/tokens/create", url.Values{"name": {"ci"}, "scope": {"workspace"}, "role": {"viewer"}})
	req.AddCookie(cookie)
	require.Equal(t, http.StatusOK, f.do(req).Code)

	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	for _, record := range records {
		require.Equal(t, "alice@example.com", record.CreatedBy)
		require.Equal(t, oidcauth.Key(f.idp.server.URL, "u-alice"), record.CreatedByID)
	}
}

func (f ssoFixture) backchannel(token string, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, oidcLogoutPath, strings.NewReader(url.Values{"logout_token": {token}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remote
	return f.do(req)
}

func TestTheProviderSigningSomeoneOutEndsTheirSessionsAndRevokesTheirTokens(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	alice := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	bob := f.signedInAs(t, testPerson("u-bob", "bob@example.com", "owner"))
	makeToken := func(cookie *http.Cookie, name string) {
		req := postForm("/api/tokens/create", url.Values{"name": {name}, "scope": {"workspace"}, "role": {"viewer"}})
		req.AddCookie(cookie)
		require.Equal(t, http.StatusOK, f.do(req).Code)
	}
	makeToken(alice, "alices")
	makeToken(bob, "bobs")

	rec := f.backchannel(f.idp.logoutToken("u-alice", nil), "198.51.100.7:4000")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.False(t, f.authenticated(t, alice), "the person the provider signed out kept their session")
	require.True(t, f.authenticated(t, bob), "somebody else was signed out")
	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.Name)
	}
	require.Equal(t, []string{"bobs"}, names, "the tokens of the person who was signed out must go, and only theirs")

	t.Run("a token sent twice is answered the same and acted on once", func(t *testing.T) {
		token := f.idp.logoutToken("u-bob", nil)
		require.Equal(t, http.StatusOK, f.backchannel(token, "198.51.100.7:4000").Code)
		require.False(t, f.authenticated(t, bob), "the logout token did nothing the first time")

		time.Sleep(3 * time.Millisecond)
		again := f.signedInAs(t, testPerson("u-bob", "bob@example.com", "owner"))
		require.Equal(t, http.StatusOK, f.backchannel(token, "198.51.100.7:4000").Code)
		require.True(t, f.authenticated(t, again), "a logout token that was already used signed the person out of a later session")
	})
}

func TestOnlyTheProvidersSignedLogoutTokenEndsSessions(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	alice := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	otherIdP := newTestIdP(t)
	for name, token := range map[string]string{
		"nothing":            "",
		"not a token":        "abc.def.ghi",
		"another key":        otherIdP.logoutToken("u-alice", nil),
		"an ID token":        f.idp.logoutToken("u-alice", map[string]any{"nonce": "n"}),
		"no logout event":    f.idp.logoutToken("u-alice", map[string]any{"events": nil}),
		"another audience":   f.idp.logoutToken("u-alice", map[string]any{"aud": "someone-else"}),
		"an old token":       f.idp.logoutToken("u-alice", map[string]any{"iat": time.Now().Add(-time.Hour).Unix()}),
		"no subject":         f.idp.logoutToken("", map[string]any{"sub": nil}),
		"another issuer":     f.idp.logoutToken("u-alice", map[string]any{"iss": "https://evil.example.com"}),
		"an unsigned header": base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u-alice"}`)) + ".",
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.backchannel(token, "198.51.100.8:4000")
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.True(t, f.authenticated(t, alice), "a logout token that should have been refused ended a session")
		})
	}

	t.Run("only POST", func(t *testing.T) {
		rec := f.do(browserRequest(http.MethodGet, oidcLogoutPath))
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
	t.Run("not while the provider is off", func(t *testing.T) {
		f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth = nil })
		require.Equal(t, http.StatusBadRequest, f.backchannel(f.idp.logoutToken("u-alice", nil), "198.51.100.9:4000").Code)
	})
}

func TestASourceThatKeepsSendingBadLogoutTokensIsSlowedDown(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	var last int
	for range 40 {
		last = f.backchannel("garbage", "203.0.113.50:4000").Code
	}
	require.Equal(t, http.StatusTooManyRequests, last)
}

func TestTheStateCookieAndSessionsAreNotInterchangeable(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	auth, err := f.srv.dashboardAuth(browserRequest(http.MethodGet, "/"))
	require.NoError(t, err)
	state := signOIDCState(auth.sessionKey, oidcStateClaims{State: "s", Nonce: "n", Verifier: "v", Purpose: purposeLogin, Next: "/"}, time.Now())
	require.False(t, f.authenticated(t, &http.Cookie{Name: testSessionCookie, Value: state}), "a state cookie counted as a session")
	_, ok := parseSSOSession(auth.sessionKey, state, time.Now())
	require.False(t, ok)
}

func TestRevocationsAreForgottenOnceNoSessionCouldBeOlder(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	ctx := context.Background()
	require.NoError(t, f.srv.revokeSSOIdentity(ctx, "sso:old"))
	secret := &corev1.Secret{}
	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret))
	revoked := parseSSORevocations(secret)
	revoked["sso:old"] = time.Now().Add(-ssoRevocationKeep - time.Hour).UnixMilli()
	payload, _ := json.Marshal(revoked)
	secret.Data[ssoRevocationsKey] = payload
	require.NoError(t, f.srv.Client.Update(ctx, secret))

	require.NoError(t, f.srv.revokeSSOIdentity(ctx, "sso:new"))

	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret))
	after := parseSSORevocations(secret)
	require.Contains(t, after, "sso:new")
	require.NotContains(t, after, "sso:old", "a revocation older than any session was kept for ever")
	require.Contains(t, string(secret.Data["users"]), "admin", "revoking must not disturb the users in the same Secret")
}

func TestAPIClientsKeepTheirOwnTokensWhateverTheModeIs(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	token := f.workspaceToken(t, apitokens.RoleViewer)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	require.Equal(t, http.StatusOK, f.do(req).Code, "a token stopped working when the provider took over sign-in")
}

// flakyClient is a client whose writes of the users Secret fail the way a busy API server's do, and whose reads of the
// platform configuration can be made to fail.
type flakyClient struct {
	client.Client
	failUpdates, conflicts atomic.Int32
	failConfigReads        atomic.Bool
	// configConflicts is how many saves of the platform configuration fail with a conflict, and concurrentWriter, when it
	// is set, is what another writer (the platform controller) does to the object just before each of them.
	configConflicts  atomic.Int32
	concurrentWriter func()
}

func (c *flakyClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if secret, ok := obj.(*corev1.Secret); ok && secret.Name == platform.DashboardAuthSecretName {
		if c.failUpdates.Load() > 0 {
			c.failUpdates.Add(-1)
			return errors.New("the API server is not answering")
		}
		if c.conflicts.Load() > 0 {
			c.conflicts.Add(-1)
			return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, secret.Name, nil)
		}
	}
	if _, ok := obj.(*geassv1alpha1.GeassPlatformConfig); ok && c.configConflicts.Load() > 0 {
		c.configConflicts.Add(-1)
		if c.concurrentWriter != nil {
			c.concurrentWriter()
		}
		return apierrors.NewConflict(schema.GroupResource{Resource: "geassplatformconfigs"}, obj.GetName(), nil)
	}
	return c.Client.Update(ctx, obj, opts...)
}

func (c *flakyClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*geassv1alpha1.GeassPlatformConfig); ok && c.failConfigReads.Load() {
		return errors.New("the API server is not answering")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (f ssoFixture) flaky() *flakyClient {
	flaky := &flakyClient{Client: f.srv.Client}
	f.srv.Client = flaky
	return flaky
}

func TestALogoutTokenThatCouldNotDoItsWorkCanBeSentAgain(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	alice := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	flaky := f.flaky()
	token := f.idp.logoutToken("u-alice", nil)

	flaky.failUpdates.Store(1)
	require.Equal(t, http.StatusInternalServerError, f.backchannel(token, "198.51.100.7:4000").Code)
	require.True(t, f.authenticated(t, alice), "the first try could not write, so nothing was ended")

	require.Equal(t, http.StatusOK, f.backchannel(token, "198.51.100.7:4000").Code)
	require.False(t, f.authenticated(t, alice), "the same token, sent again, was taken for one already acted on and did nothing")
}

func TestALogoutThatMeetsAWriteConflictStillEndsTheSession(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	alice := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	f.flaky().conflicts.Store(2)

	require.Equal(t, http.StatusOK, f.backchannel(f.idp.logoutToken("u-alice", nil), "198.51.100.7:4000").Code)

	require.False(t, f.authenticated(t, alice))
}

func TestSigningOutWorksWhenTheUsersAreInTheEnvironmentAndThereIsNoSecret(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	ctx := context.Background()
	t.Setenv("GEASS_DASHBOARD_SESSION_KEY", "0123456789abcdef0123456789abcdef")
	// A hash, not a password: a plain password is replaced by its hash the first time it is read, which writes the Secret.
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	require.NoError(t, err)
	t.Setenv("GEASS_DASHBOARD_USERS", "admin:"+string(hash)+":admin")
	usersSecret := client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}
	require.NoError(t, f.srv.Client.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: usersSecret.Name, Namespace: usersSecret.Namespace}}))
	alice := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	bob := f.signedInAs(t, testPerson("u-bob", "bob@example.com", "owner"))
	require.True(t, apierrors.IsNotFound(f.srv.Client.Get(ctx, usersSecret, &corev1.Secret{})), "the situation under test needs there to be no Secret yet")

	out := postForm("/api/logout", nil)
	out.AddCookie(alice)
	require.Equal(t, http.StatusOK, f.do(out).Code, "a person could not sign out because there was no Secret to write")
	require.False(t, f.authenticated(t, alice))
	require.True(t, f.authenticated(t, bob))

	require.Equal(t, http.StatusOK, f.backchannel(f.idp.logoutToken("u-bob", nil), "198.51.100.7:4000").Code)
	require.False(t, f.authenticated(t, bob))
}

func TestASessionOfTheProviderDoesNotComeBackWhenTheProviderIsTurnedOnAgain(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	person := testPerson("u-alice", "alice@example.com", "owner")
	earlier := f.signedInAs(t, person)
	require.True(t, f.authenticated(t, earlier))

	// The provider is turned off, its mapping changes so that she would map to nothing, and it is turned on again.
	f.setMode(t, platform.AuthModeLocal)
	require.False(t, f.authenticated(t, earlier))
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.OIDC.RoleMap = map[string]string{"owner": "admin", "member": "viewer"}
	})
	f.testSignIn(t, person)
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeExternal).Code)

	require.False(t, f.authenticated(t, earlier), "a session from before the provider was turned on again counted")
	time.Sleep(3 * time.Millisecond)
	require.True(t, f.authenticated(t, f.signedInAs(t, person)), "signing in afresh does not work")
}

func TestAReplayedCallbackIsRefusedBeforeTheProviderIsAsked(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	start, state := f.startSignIn(t, oidcLoginPath)
	back, err := url.Parse(f.atProvider(t, start.Header().Get("Location")))
	require.NoError(t, err)

	first := f.do(browserRequest(http.MethodGet, back.RequestURI(), state))
	require.Equal(t, "/", first.Header().Get("Location"))
	asked := f.idp.tokenCalls.Load()
	require.Positive(t, asked)

	for range 5 {
		replay := f.do(browserRequest(http.MethodGet, back.RequestURI(), state))
		require.Equal(t, "state", failureOf(t, replay))
	}
	require.Equal(t, asked, f.idp.tokenCalls.Load(), "a replayed callback made the dashboard ask the provider to trade the code")
}

func TestACallbackWithoutACodeDoesNotAskTheProvider(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	start, state := f.startSignIn(t, oidcLoginPath)
	query := mustQuery(t, start.Header().Get("Location"))

	rec := f.do(browserRequest(http.MethodGet, "/auth/oidc/callback?"+url.Values{"state": {query.Get("state")}}.Encode(), state))

	require.Equal(t, "provider_denied", failureOf(t, rec))
	require.Zero(t, f.idp.tokenCalls.Load())
}

func TestAProviderThatIsDownIsNotAskedAgainAtOnce(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.idp.discoveryDown.Store(true)

	for range 6 {
		rec, _ := f.startSignIn(t, oidcLoginPath)
		require.Equal(t, "provider", failureOf(t, rec))
	}

	require.Equal(t, int32(1), f.idp.discoveryCalls.Load(), "every sign-in asked a provider that was down")
}

func TestAProviderThatCannotBeReachedForAFreshDocumentIsStillUsedFromTheOneKept(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	alice := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	f.srv.sso.mu.Lock()
	f.srv.sso.fetched = time.Now().Add(-2 * oidcProviderTTL)
	f.srv.sso.mu.Unlock()
	f.idp.discoveryDown.Store(true)

	require.True(t, f.authenticated(t, f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))), "signing in failed because the document could not be fetched again")
	require.Equal(t, http.StatusOK, f.backchannel(f.idp.logoutToken("u-alice", nil), "198.51.100.7:4000").Code)
	require.False(t, f.authenticated(t, alice))
}

func TestAPasswordIsRefusedWhenItCannotBeToldWhetherTheProviderDecides(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	flaky := f.flaky()
	login := func(user string) *httptest.ResponseRecorder {
		return f.do(postForm("/api/login", url.Values{"username": {user}, "password": {"test-password"}}))
	}
	require.Equal(t, http.StatusOK, login("viewer").Code)

	flaky.failConfigReads.Store(true)

	require.Equal(t, http.StatusBadRequest, login("viewer").Code, "a viewer got in by password while it could not be told whether the provider decides")
	require.Equal(t, http.StatusOK, login("admin").Code, "the admin's way in must not depend on that read")
}

func TestAValueFromTheProviderIsNeverCutInTheMiddleOfACharacter(t *testing.T) {
	require.Equal(t, "abc", truncate("abc", 3))
	require.Equal(t, "ab", truncate("abc", 2))
	require.Equal(t, "", truncate("abc", 0))
	// "é" is two bytes, "日" three, "😀" four: wherever the cut falls, what is left is whole characters.
	text := strings.Repeat("é日😀", 20)
	for limit := range len(text) + 2 {
		cut := truncate(text, limit)
		require.True(t, utf8.ValidString(cut), "a cut at %d left bytes that are not text", limit)
		require.LessOrEqual(t, len(cut), limit)
		require.True(t, strings.HasPrefix(text, cut))
		if limit < len(text) {
			require.Greater(t, len(cut)+4, limit, "more was dropped at %d than one character", limit)
		}
	}
}
