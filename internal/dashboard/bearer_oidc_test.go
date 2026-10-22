package dashboard

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

// resourceAudience is what an API of the cluster is called at the provider, which is what its access tokens name as their
// audience when the platform's configuration says so. Without that the audience is the client ID.
const resourceAudience = "https://cluster-a.example.com/api"

// signTyped signs claims as the provider does an access token: the header says so (RFC 9068), and typ "JWT" is what its ID
// tokens say.
func (p *testIdP) signTyped(typ string, claims map[string]any) string {
	options := &jose.SignerOptions{}
	if typ != "" {
		options = options.WithType(jose.ContentType(typ))
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: jose.JSONWebKey{Key: p.key, KeyID: "k1"}}, options)
	require.NoError(p.t, err)
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(p.t, err)
	return token
}

// accessClaims are the claims of an access token of a person: what the provider is asked to add to it (the same the ID token
// has), and those it adds itself.
func (p *testIdP) accessClaims(person map[string]any, change map[string]any) map[string]any {
	now := time.Now()
	claims := map[string]any{
		"iss": p.server.URL, "aud": ssoTestClient, "client_id": ssoTestClient, "scope": "openid email",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "jti": "j-" + now.Format("150405.000000"),
	}
	maps.Copy(claims, person)
	for name, value := range change {
		if value == nil {
			delete(claims, name)
		} else {
			claims[name] = value
		}
	}
	return claims
}

func (p *testIdP) accessToken(person map[string]any, change map[string]any) string {
	return p.signTyped("at+jwt", p.accessClaims(person, change))
}

// acceptAccessTokens turns the setting on, for tokens whose audience is audience ("" is the client ID).
func (f ssoFixture) acceptAccessTokens(t *testing.T, audience string) {
	t.Helper()
	f.updateConfigDirect(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.OIDC.AccessTokens = &geassv1alpha1.GeassPlatformOIDCAccessTokensSpec{Enabled: true, Audience: audience}
	})
}

func bearerGet(token, path string) *http.Request { return bearer(token, http.MethodGet, path, nil) }

func (f ssoFixture) body(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	return decodeBody(t, rec)
}

var alice = testPerson("u-alice", "alice@example.com", "owner")

func TestAnAccessTokenOfTheProviderIsABearerCredentialForThePersonItWasIssuedTo(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")

	rec := f.do(bearerGet(f.idp.accessToken(alice, nil), "/api/v1/me"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	me := f.body(t, rec)
	require.Equal(t, "alice@example.com", me["username"], "the cluster knows the person, not a token")
	require.Equal(t, "admin", me["role"])
	require.Equal(t, true, me["canMutate"])
	require.Equal(t, "oidc", me["authentication"])
	require.NotEmpty(t, me["expiresAt"])

	// What it can do is what the person can: read the services.
	require.Equal(t, http.StatusOK, f.do(bearerGet(f.idp.accessToken(alice, nil), "/api/v1/apps")).Code)
	require.Equal(t, http.StatusOK, f.do(bearerGet(f.idp.accessToken(alice, nil), "/api/bootstrap")).Code)

	// A person the role map makes a viewer reads, and changes nothing.
	viewer := testPerson("u-vera", "vera@example.com", "member")
	token := f.idp.accessToken(viewer, nil)
	require.Equal(t, http.StatusOK, f.do(bearerGet(token, "/api/v1/apps")).Code)
	require.Equal(t, "viewer", f.body(t, f.do(bearerGet(token, "/api/v1/me")))["role"])
	deploy := f.do(bearer(token, http.MethodPost, "/api/apps/api-prod/deploy", url.Values{}))
	require.Equal(t, http.StatusForbidden, deploy.Code, "a viewer cannot change anything with an access token either")
	require.Equal(t, http.StatusForbidden, f.do(bearerGet(token, "/api/apps/api-prod/logs")).Code, "nor read what is kept from viewers")
}

func TestAnAccessTokenActsAsThePersonInWhatIsRecorded(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")

	// The actor of every record that names one is the name of the request's session.
	actorOf := func(token string) (string, int) {
		actor := ""
		handler := f.srv.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor = f.srv.currentSession(r).Username
			w.WriteHeader(http.StatusNoContent)
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, bearerGet(token, "/api/anything"))
		return actor, rec.Code
	}
	actor, code := actorOf(f.idp.accessToken(alice, nil))
	require.Equal(t, http.StatusNoContent, code)
	require.Equal(t, "alice@example.com", actor)

	// A token that carries no address names the person by their subject.
	noAddress := map[string]any{"sub": "u-nina", "org_role": "owner", "organization_id": "org_acme"}
	actor, code = actorOf(f.idp.accessToken(noAddress, nil))
	require.Equal(t, http.StatusNoContent, code)
	require.Equal(t, "u-nina", actor)

	// An API token is still "token:<name>".
	apiToken := f.workspaceToken(t, apitokens.RoleAdmin)
	actor, code = actorOf(apiToken)
	require.Equal(t, http.StatusNoContent, code)
	require.Equal(t, "token:ci", actor)
}

func TestAccessTokensAreOffUntilTheConfigurationTurnsThemOn(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	token := f.idp.accessToken(alice, nil)

	require.Equal(t, http.StatusUnauthorized, f.do(bearerGet(token, "/api/v1/me")).Code, "off by default: a token of the provider is not a credential")

	f.acceptAccessTokens(t, "")
	require.Equal(t, http.StatusOK, f.do(bearerGet(token, "/api/v1/me")).Code)

	f.updateConfigDirect(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth.OIDC.AccessTokens.Enabled = false })
	require.Equal(t, http.StatusUnauthorized, f.do(bearerGet(token, "/api/v1/me")).Code, "turned off again")

	f.acceptAccessTokens(t, "")
	f.setMode(t, platform.AuthModeLocal)
	require.Equal(t, http.StatusUnauthorized, f.do(bearerGet(token, "/api/v1/me")).Code, "the provider does not decide who signs in, so its tokens are nothing here")
}

func TestAnAPITokenWorksAsItDidWhateverTheSettingIs(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	token := f.workspaceToken(t, apitokens.RoleAdmin)
	require.Equal(t, http.StatusOK, f.do(bearerGet(token, "/api/v1/me")).Code)
	f.acceptAccessTokens(t, "")
	require.Equal(t, http.StatusOK, f.do(bearerGet(token, "/api/v1/me")).Code)

	// What is shaped as a Geass token is looked up as one, and never taken for something else.
	for name, bad := range map[string]string{
		"a token that is not there":              "gsw_nothing_here",
		"a deploy hook's secret":                 "gsh_deploy_hook_secret",
		"a provider token behind a Geass prefix": "gsp_" + f.idp.accessToken(alice, nil),
	} {
		require.Equal(t, http.StatusUnauthorized, f.do(bearerGet(bad, "/api/v1/me")).Code, name)
	}
}

func TestAnAccessTokenThatIsNotRightIsRefused(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")
	idp := f.idp
	now := time.Now()
	unsigned := func() string {
		encode := func(value any) string {
			raw, _ := json.Marshal(value)
			return base64.RawURLEncoding.EncodeToString(raw)
		}
		return encode(map[string]any{"alg": "none", "typ": "at+jwt"}) + "." + encode(idp.accessClaims(alice, nil)) + "."
	}
	other := newTestIdP(t)
	for name, token := range map[string]string{
		"a good one":                             idp.accessToken(alice, nil),
		"another issuer":                         idp.accessToken(alice, map[string]any{"iss": "https://evil.example.com"}),
		"another audience":                       idp.accessToken(alice, map[string]any{"aud": "someone-else"}),
		"an audience list that does not hold it": idp.accessToken(alice, map[string]any{"aud": []string{"someone-else", "https://auth.example.com/userinfo"}}),
		"expired":                                idp.accessToken(alice, map[string]any{"exp": now.Add(-time.Hour).Unix(), "iat": now.Add(-2 * time.Hour).Unix()}),
		"not valid yet":                          idp.accessToken(alice, map[string]any{"nbf": now.Add(time.Hour).Unix()}),
		"signed by another key":                  other.signTyped("at+jwt", idp.accessClaims(alice, map[string]any{"iss": idp.server.URL})),
		"alg none":                               unsigned(),
		// An ID token is signed by the same keys and addressed to this client: it is refused because it is not an access token.
		"an ID token":                     idp.signTyped("JWT", idp.accessClaims(alice, map[string]any{"nonce": "n", "at_hash": "h"})),
		"an ID token without its nonce":   idp.signTyped("JWT", idp.accessClaims(alice, nil)),
		"an ID token that says it is one": idp.signTyped("at+jwt", idp.accessClaims(alice, map[string]any{"nonce": "n"})),
		"a person's ID token as it is sent to the browser": idp.sign(map[string]any{
			"iss": idp.server.URL, "aud": ssoTestClient, "sub": "u-alice", "email": "alice@example.com", "org_role": "owner",
			"organization_id": "org_acme", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": "n",
		}),
		"no subject": idp.accessToken(alice, map[string]any{"sub": nil}),
		"nonsense":   "not-a-token",
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(bearerGet(token, "/api/v1/me"))
			if name == "a good one" {
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				return
			}
			require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "alice", "the answer says nothing of why")
		})
	}
}

func TestAPersonWhoSigningInWouldTurnAwayIsTurnedAwayWithTheirToken(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")
	for name, tc := range map[string]struct {
		person map[string]any
		change map[string]any
		reason string
	}{
		"a role that maps to nothing":     {testPerson("u-g", "g@example.com", "guest"), nil, oidcauth.DeniedNoRole},
		"no role":                         {alice, map[string]any{"org_role": nil}, oidcauth.DeniedNoRole},
		"another organization":            {alice, map[string]any{"organization_id": "org_other"}, oidcauth.DeniedRequiredClaim},
		"an address that is not verified": {alice, map[string]any{"email_verified": false}, oidcauth.DeniedEmailUnverified},
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(bearerGet(f.idp.accessToken(tc.person, tc.change), "/api/v1/me"))
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.reason)
		})
	}
	// An organization of the provider's own that is not this cluster's is refused whatever the role.
	require.Equal(t, http.StatusOK, f.do(bearerGet(f.idp.accessToken(alice, nil), "/api/v1/me")).Code)
}

func TestTheAudienceOfAnAccessTokenIsTheClusterSOwn(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, resourceAudience)

	require.Equal(t, http.StatusOK, f.do(bearerGet(f.idp.accessToken(alice, map[string]any{"aud": resourceAudience}), "/api/v1/me")).Code)
	// Better Auth adds the address of its userinfo endpoint to an access token that has the openid scope.
	both := []string{resourceAudience, "https://auth.example.com/api/auth/oauth2/userinfo"}
	require.Equal(t, http.StatusOK, f.do(bearerGet(f.idp.accessToken(alice, map[string]any{"aud": both}), "/api/v1/me")).Code)
	// The client ID is no longer the audience, once another is named: a token for another cluster of the provider is no good here.
	require.Equal(t, http.StatusUnauthorized, f.do(bearerGet(f.idp.accessToken(alice, nil), "/api/v1/me")).Code)
	require.Equal(t, http.StatusUnauthorized, f.do(bearerGet(f.idp.accessToken(alice, map[string]any{"aud": "https://cluster-b.example.com/api"}), "/api/v1/me")).Code)
}

func TestAnAccessTokenIsRefusedWhatOnlyASignedInAdminMayDo(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")
	token := f.idp.accessToken(alice, nil)
	require.Equal(t, http.StatusOK, f.do(bearerGet(token, "/api/v1/me")).Code, "the token is an admin's")

	for _, request := range []struct {
		method, path string
		form         url.Values
	}{
		{http.MethodGet, "/api/settings/auth", nil},
		{http.MethodGet, "/api/tokens", nil},
		{http.MethodPost, "/api/tokens/create", url.Values{"name": {"mine"}, "scope": {"workspace"}, "role": {"admin"}}},
		{http.MethodPost, "/api/tokens/revoke", url.Values{"id": {"x"}}},
		{http.MethodPost, "/api/settings/auth/save", f.saveForm(nil)},
		{http.MethodPost, "/api/settings/auth/mode", url.Values{"mode": {"local"}}},
		{http.MethodPost, "/api/settings/auth/clear", url.Values{}},
		{http.MethodPost, "/api/settings/auth/logout-policy", url.Values{"revokeTokens": {"off"}}},
		{http.MethodPost, "/api/settings/auth/tenancy", url.Values{}},
		{http.MethodPost, "/api/auth/device/approve", url.Values{"userCode": {"ABCD-EFGH"}}},
		{http.MethodGet, "/api/v1/auth/device/ABCD-EFGH", nil},
		{http.MethodPost, "/api/session/scope", url.Values{"org": {"x"}}},
	} {
		rec := f.do(bearer(token, request.method, request.path, request.form))
		require.Contains(t, []int{http.StatusForbidden, http.StatusBadRequest}, rec.Code, "%s %s: %s", request.method, request.path, rec.Body.String())
		require.NotEqual(t, http.StatusOK, rec.Code, "%s %s", request.method, request.path)
	}
	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	require.Empty(t, records, "no token was made with an access token")
	require.Equal(t, platform.AuthModeExternal, f.config(t).Spec.Auth.Mode)
	require.Equal(t, http.StatusOK, f.do(bearerGet(f.idp.accessToken(alice, nil), "/api/v1/me")).Code)

	// The same routes are refused the same way to an API token, which is what this keeps.
	apiToken := f.workspaceToken(t, apitokens.RoleAdmin)
	require.Equal(t, http.StatusForbidden, f.do(bearerGet(apiToken, "/api/settings/auth")).Code)
}

func TestAnAccessTokenIsRefusedOnceThePersonSignedOutOrWasRemoved(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")
	token := f.idp.accessToken(alice, nil)
	require.Equal(t, http.StatusOK, f.do(bearerGet(token, "/api/v1/me")).Code)

	// A logout from the provider ends the sessions of the person, and so the tokens it issued before.
	require.NoError(t, f.srv.revokeSSOIdentity(context.Background(), oidcauth.Key(f.idp.server.URL, "u-alice")))
	require.Equal(t, http.StatusUnauthorized, f.do(bearerGet(token, "/api/v1/me")).Code)
	require.Equal(t, http.StatusOK, f.do(bearerGet(f.idp.accessToken(alice, map[string]any{"iat": time.Now().Add(5 * time.Second).Unix()}), "/api/v1/me")).Code,
		"a token issued after it is another matter")
	require.Equal(t, http.StatusOK, f.do(bearerGet(f.idp.accessToken(testPerson("u-bob", "bob@example.com", "owner"), nil), "/api/v1/me")).Code, "and so is another person")
}

func TestAnAccessTokenIsMappedToOrganizationsAsSigningInIs(t *testing.T) {
	f := newTenancyFixture(t)
	f.updateConfigDirect(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.OIDC.AccessTokens = &geassv1alpha1.GeassPlatformOIDCAccessTokensSpec{Enabled: true}
	})
	apps := func(person map[string]any, change map[string]any) ([]string, *httptest.ResponseRecorder) {
		rec := f.do(bearerGet(f.idp.accessToken(person, change), "/api/v1/apps"))
		if rec.Code != http.StatusOK {
			return nil, rec
		}
		return decodeNames(t, rec.Body.Bytes(), "items"), rec
	}

	// An administrator of an organization sees that organization, and none of the other.
	names, _ := apps(member("u-ada", "ada@example.com", inOrg("org_acme", "owner")), nil)
	require.ElementsMatch(t, []string{"app-a0", "app-a1", "app-a2"}, names)
	rec := f.do(bearerGet(f.idp.accessToken(member("u-ada", "ada@example.com", inOrg("org_acme", "owner")), nil), "/api/v1/me"))
	me := f.body(t, rec)
	require.Equal(t, orgA, me["org"])
	require.Equal(t, "admin", me["role"])

	// A member works in their workspace, with the role they have there.
	names, _ = apps(member("u-bob", "bob@example.com", inOrg("org_acme", "member", "team_1")), nil)
	require.Equal(t, []string{"app-a1"}, names)
	bob := f.idp.accessToken(member("u-bob", "bob@example.com", inOrg("org_acme", "member", "team_1")), nil)
	require.Equal(t, http.StatusForbidden, f.do(bearer(bob, http.MethodPost, "/api/apps/app-a1/update", url.Values{})).Code, "a viewer changes nothing")
	names, _ = apps(member("u-lee", "lee@example.com", inOrg("org_acme", "member", map[string]any{"id": "team_2", "role": "admin"})), nil)
	require.Equal(t, []string{"app-a2"}, names, "the role of the workspace counts")

	// An organization the cluster does not know, or a person in none, is refused as at sign-in.
	_, rec = apps(member("u-zed", "zed@example.com", inOrg("org_unknown", "owner")), nil)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), oidcauth.DeniedNoOrg)
	_, rec = apps(member("u-nobody", "nobody@example.com"), nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	_, rec = apps(member("u-guest", "guest@example.com", inOrg("org_acme", "guest")), nil)
	require.Equal(t, http.StatusForbidden, rec.Code, "a role that maps to nothing")

	// A platform administrator is the platform's, and sees every organization's work.
	names, _ = apps(member("u-root", "root@example.com"), map[string]any{"geass_platform": "operator"})
	require.Subset(t, names, []string{"app-a0", "app-a1", "app-a2", "app-b1"})
	require.Greater(t, len(names), 4, "and what belongs to no organization")
	// And a claim that does not make one does not.
	_, rec = apps(member("u-fake", "fake@example.com"), map[string]any{"geass_platform": "viewer"})
	require.Equal(t, http.StatusForbidden, rec.Code)

	// A token never makes an organization, whatever the platform lets a sign-in do.
	f.updateConfigDirect(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.Tenancy.AutoProvision = &geassv1alpha1.GeassPlatformAutoProvisionSpec{Allow: []string{"*"}}
	})
	_, rec = apps(member("u-new", "new@example.com", inOrg("org_new", "owner")), nil)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	var orgs geassv1alpha1.GeassOrganizationList
	require.NoError(t, f.srv.Client.List(asSystem(context.Background()), &orgs))
	require.Len(t, orgs.Items, 2, "an organization was made by a bearer token")
}

func TestAStreamOpenedWithAnAccessTokenEndsWhenTheTokenExpires(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	now := time.Now()
	token := f.idp.accessToken(alice, map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(2 * time.Second).Unix()})

	stream := openEvents(t, server, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })

	require.False(t, stream.endsWithin(200*time.Millisecond), "a stream of a token that is still good was ended")
	// The checks of a stream come every 15 seconds: it is the token's own end that closes it.
	require.True(t, stream.endsWithin(4*time.Second), "a stream stayed open after its token expired")
}

func TestAStreamOpenedWithAnAccessTokenEndsWhenThePersonIsSignedOut(t *testing.T) {
	quickRecheck(t)
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	token := f.idp.accessToken(alice, nil)
	stream := openEvents(t, server, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
	require.False(t, stream.endsWithin(300*time.Millisecond))

	require.NoError(t, f.srv.revokeSSOIdentity(context.Background(), oidcauth.Key(f.idp.server.URL, "u-alice")))

	require.True(t, stream.endsWithin(3*time.Second), "a stream stayed open after the person it was opened for was signed out")
}

func TestAStreamIsRefusedAnAccessTokenThatIsNotGood(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	for name, token := range map[string]string{
		"another audience": f.idp.accessToken(alice, map[string]any{"aud": "someone-else"}),
		"an ID token":      f.idp.signTyped("JWT", f.idp.accessClaims(alice, map[string]any{"nonce": "n"})),
	} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, server.URL+"/api/events", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+token)
			response, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = response.Body.Close() }()
			require.Equal(t, http.StatusUnauthorized, response.StatusCode)
		})
	}
}

// terminalAfterSSO is the provider fixture with a terminal behind the service "web", served over a real socket.
func terminalAfterSSO(t *testing.T) (ssoFixture, *httptest.Server) {
	t.Helper()
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.acceptAccessTokens(t, "")
	f.srv.consoleExec = echoUntilContextEnds
	require.NoError(t, f.srv.Client.Create(context.Background(), &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}))
	require.NoError(t, f.srv.Client.Create(context.Background(), activeConsoleSession("sess")))
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	return f, server
}

func TestATerminalOpensWithAnAccessTokenOfferedAsASubprotocol(t *testing.T) {
	f, server := terminalAfterSSO(t)
	token := f.idp.accessToken(alice, nil)

	conn, response, err := dialTerminal(server, websocketProtocol, websocketBearerPrefix+token)
	require.NoError(t, err, "an admin's access token offered as a subprotocol should open the terminal")
	t.Cleanup(func() { _ = conn.Close() })
	require.Equal(t, websocketProtocol, conn.Subprotocol())
	require.NotContains(t, response.Header.Get("Sec-WebSocket-Protocol"), token)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("hi\n")))
	_, data := readFrame(t, conn)
	require.Equal(t, "HI\n", string(data))
}

func TestATerminalIsRefusedToAnAccessTokenThatMayNotHaveOne(t *testing.T) {
	f, server := terminalAfterSSO(t)
	for name, token := range map[string]string{
		"a viewer":         f.idp.accessToken(testPerson("u-vera", "vera@example.com", "member"), nil),
		"another audience": f.idp.accessToken(alice, map[string]any{"aud": "someone-else"}),
		"an ID token":      f.idp.signTyped("JWT", f.idp.accessClaims(alice, map[string]any{"nonce": "n"})),
		"expired":          f.idp.accessToken(alice, map[string]any{"exp": time.Now().Add(-time.Hour).Unix(), "iat": time.Now().Add(-2 * time.Hour).Unix()}),
	} {
		t.Run(name, func(t *testing.T) {
			_, response, err := dialTerminal(server, websocketProtocol, websocketBearerPrefix+token)
			require.Error(t, err, "the terminal must stay shut: it is a shell in the service's container")
			require.NotNil(t, response)
			require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, response.StatusCode)
		})
	}
}

func TestATerminalOpenedWithAnAccessTokenEndsWhenTheTokenExpires(t *testing.T) {
	f, server := terminalAfterSSO(t)
	now := time.Now()
	token := f.idp.accessToken(alice, map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(2 * time.Second).Unix()})
	conn, _, err := dialTerminal(server, websocketProtocol, websocketBearerPrefix+token)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ended := make(chan struct{})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(ended)
				return
			}
		}
	}()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("a terminal stayed open after its token expired")
	}
}

func TestSavingTheProviderKeepsWhetherAccessTokensAreAccepted(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	f.acceptAccessTokens(t, resourceAudience)

	rec := f.save(t, func(v url.Values) {
		v.Set("clientSecret", "")
		v.Set("keepSecret", "on")
		v.Set("roleMap", "owner=admin")
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	view := f.view(t)
	require.Equal(t, true, view["accessTokens"], "saving the provider turned the setting off")
	require.Equal(t, resourceAudience, view["accessTokenAudience"])
}

func TestAccessTokensAreOffInAProviderThatWasNeverSetUpForThem(t *testing.T) {
	var config geassv1alpha1.GeassPlatformConfig
	require.False(t, ssoSettingsOf(config).AccessTokens)
	config.Spec.Auth = &geassv1alpha1.GeassPlatformAuthSpec{OIDC: &geassv1alpha1.GeassPlatformOIDCSpec{Issuer: "https://auth.example.com"}}
	require.False(t, ssoSettingsOf(config).AccessTokens, "a provider whose setting was never made")
	config.Spec.Auth.OIDC.AccessTokens = &geassv1alpha1.GeassPlatformOIDCAccessTokensSpec{Enabled: true, Audience: " " + resourceAudience + " "}
	settings := ssoSettingsOf(config)
	require.True(t, settings.AccessTokens)
	require.Equal(t, resourceAudience, settings.AccessTokenAudience)
	require.False(t, strings.Contains(settings.AccessTokenAudience, " "))
}
