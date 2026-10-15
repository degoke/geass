package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

// newBareSSOFixture is a platform that has never been given an identity provider, with one running to set up.
func newBareSSOFixture(t *testing.T) ssoFixture {
	t.Helper()
	return ssoFixture{tokenFixture: newTokenFixture(t), idp: newTestIdP(t)}
}

func (f ssoFixture) saveForm(change func(url.Values)) url.Values {
	form := url.Values{
		"displayName": {"Acme"}, "issuer": {f.idp.server.URL}, "clientID": {ssoTestClient}, "clientSecret": {ssoTestSecret},
		"roleClaim": {"org_role"}, "roleMap": {"owner=admin\nmember=viewer"}, "requireClaim": {"organization_id=org_acme"}, "sessionMinutes": {"60"},
	}
	if change != nil {
		change(form)
	}
	return form
}

func (f ssoFixture) save(t *testing.T, change func(url.Values)) *httptest.ResponseRecorder {
	t.Helper()
	return f.asAdmin(t, postForm("/api/settings/auth/save", f.saveForm(change)))
}

// switchMode asks the settings API to change how people sign in, as a signed-in admin.
func (f ssoFixture) switchMode(t *testing.T, mode string) *httptest.ResponseRecorder {
	t.Helper()
	return f.asAdmin(t, postForm("/api/settings/auth/mode", url.Values{"mode": {mode}}))
}

// adminCookie is a local admin's session cookie.
func (f ssoFixture) adminCookie(t *testing.T) *http.Cookie {
	t.Helper()
	req := withAdminSession(t, f.srv, browserRequest(http.MethodGet, "/"))
	cookie, err := req.Cookie(testSessionCookie)
	require.NoError(t, err)
	return cookie
}

// testSignIn runs the test sign-in as the person the provider has at the keyboard, the way a browser does it.
func (f ssoFixture) testSignIn(t *testing.T, person map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	f.idp.mu.Lock()
	f.idp.person = person
	f.idp.mu.Unlock()
	start, state := f.startSignIn(t, oidcLoginPath+"?purpose=test", f.adminCookie(t))
	require.Equal(t, http.StatusFound, start.Code)
	require.NotNil(t, state, start.Header().Get("Location"))
	back, err := url.Parse(f.atProvider(t, start.Header().Get("Location")))
	require.NoError(t, err)
	return f.do(browserRequest(http.MethodGet, back.RequestURI(), state))
}

func (f ssoFixture) view(t *testing.T) map[string]any {
	t.Helper()
	rec := f.asAdmin(t, browserRequest(http.MethodGet, "/api/settings/auth"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return decodeBody(t, rec)
}

func (f ssoFixture) secretExists(t *testing.T, name string) bool {
	t.Helper()
	err := f.srv.Client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &corev1.Secret{})
	if apierrors.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

func (f ssoFixture) hasConfig(t *testing.T) bool {
	t.Helper()
	err := f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassPlatformConfig{})
	if apierrors.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

func TestOnlyASignedInAdminSeesOrChangesHowPeopleSignIn(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	viewer := withDashboardSession(t, f.srv, browserRequest(http.MethodGet, "/"), "viewer", dashboardRoleViewer)
	viewerCookie, err := viewer.Cookie(testSessionCookie)
	require.NoError(t, err)

	type caller struct {
		name  string
		apply func(*http.Request)
		codes []int
	}
	for _, c := range []caller{
		{"nobody", func(*http.Request) {}, []int{http.StatusUnauthorized}},
		{"a viewer", func(r *http.Request) { r.AddCookie(viewerCookie) }, []int{http.StatusForbidden}},
		{"an admin token", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+f.workspaceToken(t, apitokens.RoleAdmin))
		}, []int{http.StatusForbidden}},
		{"an observer token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.observerToken(t)) }, []int{http.StatusForbidden}},
	} {
		t.Run(c.name, func(t *testing.T) {
			read := browserRequest(http.MethodGet, "/api/settings/auth")
			read.Header.Set("Accept", "application/json")
			c.apply(read)
			require.Contains(t, c.codes, f.do(read).Code, "read")
			for _, path := range []string{"/api/settings/auth/save", "/api/settings/auth/mode", "/api/settings/auth/clear", "/api/settings/auth/logout-policy"} {
				write := postForm(path, f.saveForm(func(v url.Values) { v.Set("mode", platform.AuthModeLocal) }))
				c.apply(write)
				require.Contains(t, c.codes, f.do(write).Code, path)
			}
		})
	}
	require.Equal(t, f.idp.server.URL, f.config(t).Spec.Auth.OIDC.Issuer, "a caller who should have been refused changed the settings")
}

func TestSavingTheProviderStoresItAndDoesNotTurnItOn(t *testing.T) {
	f := newBareSSOFixture(t)

	rec := f.save(t, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	config := f.config(t)
	require.NotEqual(t, platform.AuthModeExternal, config.Spec.Auth.Mode, "saving must not switch how people sign in")
	oidc := config.Spec.Auth.OIDC
	require.Equal(t, f.idp.server.URL, oidc.Issuer)
	require.Equal(t, ssoTestClient, oidc.ClientID)
	require.Equal(t, "org_role", oidc.RoleClaim)
	require.Equal(t, map[string]string{"owner": "admin", "member": "viewer"}, oidc.RoleMap)
	require.Equal(t, map[string]string{"organization_id": "org_acme"}, oidc.RequireClaim)
	require.Equal(t, int32(60), oidc.SessionMinutes)
	require.Equal(t, platform.PlatformAuthSecretName, oidc.CredentialRef.Name)

	secret := &corev1.Secret{}
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.PlatformAuthSecretName, Namespace: platform.SystemNamespace}, secret))
	require.Equal(t, ssoTestSecret, string(secret.Data[platform.PlatformAuthClientSecretKey]))

	body := f.asAdmin(t, browserRequest(http.MethodGet, "/api/settings/auth")).Body.String()
	require.NotContains(t, body, ssoTestSecret, "the client secret is shown by the settings")
	view := f.view(t)
	require.Equal(t, "local", view["mode"])
	require.Equal(t, true, view["hasSecret"])
	require.Equal(t, "http://example.com/auth/oidc/callback", view["callbackURL"])
	require.Equal(t, "http://example.com/auth/oidc/backchannel-logout", view["backchannelLogoutURL"])
	require.Equal(t, false, view["canEnable"], "nothing was tested yet")
}

func TestSavingRefusesWhatCouldNotWork(t *testing.T) {
	for name, change := range map[string]func(url.Values){
		"an issuer over plain http":        func(v url.Values) { v.Set("issuer", "http://auth.example.com") },
		"an issuer that is not a URL":      func(v url.Values) { v.Set("issuer", "auth.example.com") },
		"the metadata address":             func(v url.Values) { v.Set("issuer", "https://169.254.169.254") },
		"no client":                        func(v url.Values) { v.Set("clientID", "") },
		"no role claim":                    func(v url.Values) { v.Set("roleClaim", "") },
		"no role mapping":                  func(v url.Values) { v.Set("roleMap", "") },
		"a role that is not one":           func(v url.Values) { v.Set("roleMap", "owner=superuser") },
		"observer, which is for tokens":    func(v url.Values) { v.Set("roleMap", "owner=observer") },
		"a mapping that is not a pair":     func(v url.Values) { v.Set("roleMap", "owner") },
		"a required claim without a value": func(v url.Values) { v.Set("requireClaim", "organization_id=") },
		"a session of a minute":            func(v url.Values) { v.Set("sessionMinutes", "1") },
		"a session of a week":              func(v url.Values) { v.Set("sessionMinutes", "10080") },
		"a session that is not a number":   func(v url.Values) { v.Set("sessionMinutes", "an hour") },
		"a very long name":                 func(v url.Values) { v.Set("displayName", strings.Repeat("a", 61)) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newBareSSOFixture(t)
			rec := f.save(t, change)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.False(t, f.hasConfig(t), "settings that were refused were stored")
			require.False(t, f.secretExists(t, platform.PlatformAuthSecretName), "a secret was stored for settings that were refused")
		})
	}

	t.Run("a local issuer without the operator's opt-in", func(t *testing.T) {
		f := newBareSSOFixture(t)
		t.Setenv("GEASS_ALLOW_LOOPBACK", "false")
		rec := f.save(t, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.False(t, f.hasConfig(t))
	})
}

func TestTheClientSecretIsKeptOnlyForTheProviderItWasSavedFor(t *testing.T) {
	f := newBareSSOFixture(t)
	require.Equal(t, http.StatusOK, f.save(t, nil).Code)
	keep := func(v url.Values) { v.Set("clientSecret", ""); v.Set("keepSecret", "on") }
	storedSecret := func() string {
		secret := &corev1.Secret{}
		require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.PlatformAuthSecretName, Namespace: platform.SystemNamespace}, secret))
		return string(secret.Data[platform.PlatformAuthClientSecretKey])
	}

	t.Run("kept for a change that leaves the provider and client alone", func(t *testing.T) {
		rec := f.save(t, func(v url.Values) { keep(v); v.Set("roleMap", "owner=admin") })
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, ssoTestSecret, storedSecret())
		require.Equal(t, map[string]string{"owner": "admin"}, f.config(t).Spec.Auth.OIDC.RoleMap)
	})
	t.Run("not carried to another issuer", func(t *testing.T) {
		rec := f.save(t, func(v url.Values) { keep(v); v.Set("issuer", "https://evil.example.com") })
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Equal(t, f.idp.server.URL, f.config(t).Spec.Auth.OIDC.Issuer)
	})
	t.Run("not carried to another client", func(t *testing.T) {
		rec := f.save(t, func(v url.Values) { keep(v); v.Set("clientID", "another") })
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	})
	t.Run("replaced by a new one", func(t *testing.T) {
		require.Equal(t, http.StatusOK, f.save(t, func(v url.Values) { v.Set("clientSecret", "rotated") }).Code)
		require.Equal(t, "rotated", storedSecret())
	})
	t.Run("dropped for a public client", func(t *testing.T) {
		require.Equal(t, http.StatusOK, f.save(t, func(v url.Values) { v.Set("clientSecret", "") }).Code)
		require.False(t, f.secretExists(t, platform.PlatformAuthSecretName))
		require.Nil(t, f.config(t).Spec.Auth.OIDC.CredentialRef)
		require.Equal(t, false, f.view(t)["hasSecret"])
	})
}

func TestSettingsCannotBeEditedWhilePeopleSignInThroughTheProvider(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)

	rec := f.save(t, func(v url.Values) { v.Set("roleMap", "owner=viewer") })

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "local")
	require.Equal(t, "admin", f.config(t).Spec.Auth.OIDC.RoleMap["owner"], "settings changed under the people signing in")
}

func TestTheProviderCannotBeTurnedOnUntilATestSignInWentThrough(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	turnOn := func() *httptest.ResponseRecorder { return f.switchMode(t, platform.AuthModeExternal) }
	require.Equal(t, http.StatusBadRequest, turnOn().Code, "turned on without a test")
	require.Equal(t, platform.AuthModeLocal, f.config(t).Spec.Auth.Mode)

	t.Run("a test as someone who is only a viewer does not count", func(t *testing.T) {
		rec := f.testSignIn(t, testPerson("u-bob", "bob@example.com", "member"))
		require.Equal(t, "/settings/auth?sso_test=done", rec.Header().Get("Location"))
		test := f.view(t)["test"].(map[string]any)
		require.Equal(t, false, test["ok"])
		require.Equal(t, "not_admin", test["reason"])
		require.Equal(t, http.StatusBadRequest, turnOn().Code)
	})
	t.Run("a test that was refused does not count", func(t *testing.T) {
		f.testSignIn(t, map[string]any{"sub": "u-x", "email": "x@example.com", "org_role": "owner", "organization_id": "org_other"})
		test := f.view(t)["test"].(map[string]any)
		require.Equal(t, "required_claim", test["reason"])
		require.Equal(t, http.StatusBadRequest, turnOn().Code)
	})
	t.Run("a test as an admin of the provider does", func(t *testing.T) {
		rec := f.testSignIn(t, testPerson("u-alice", "alice@example.com", "owner"))
		require.Nil(t, cookieNamed(rec, testSessionCookie), "a test sign-in started a session")
		view := f.view(t)
		require.Equal(t, true, view["canEnable"])
		require.Equal(t, "alice@example.com", view["test"].(map[string]any)["email"])
		require.Equal(t, http.StatusOK, turnOn().Code)
		require.Equal(t, platform.AuthModeExternal, f.config(t).Spec.Auth.Mode)
	})
	t.Run("and back again, whenever", func(t *testing.T) {
		require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeLocal).Code)
		require.Equal(t, platform.AuthModeLocal, f.config(t).Spec.Auth.Mode)
	})
}

func TestATestOnlyCountsForTheSettingsItTestedAndForAWhile(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	f.testSignIn(t, testPerson("u-alice", "alice@example.com", "owner"))
	require.True(t, f.view(t)["canEnable"].(bool))

	t.Run("other settings need their own test", func(t *testing.T) {
		require.Equal(t, http.StatusOK, f.save(t, func(v url.Values) {
			v.Set("clientSecret", "")
			v.Set("keepSecret", "on")
			v.Set("roleMap", "owner=admin,member=viewer")
		}).Code)
		require.False(t, f.view(t)["canEnable"].(bool))
		require.Equal(t, http.StatusBadRequest, f.switchMode(t, platform.AuthModeExternal).Code)
	})
	t.Run("a test goes stale", func(t *testing.T) {
		f.testSignIn(t, testPerson("u-alice", "alice@example.com", "owner"))
		require.True(t, f.srv.ssoTestedOK(f.srv.ssoTest(f.currentFingerprint(t)).Fingerprint, time.Now()))
		require.False(t, f.srv.ssoTestedOK(f.currentFingerprint(t), time.Now().Add(oidcTestValidity+time.Minute)))
	})
}

func (f ssoFixture) currentFingerprint(t *testing.T) string {
	t.Helper()
	settings, err := f.srv.ssoSettings(context.Background())
	require.NoError(t, err)
	return settings.Config.Fingerprint()
}

func TestOnlyASignedInAdminCanStartATestSignIn(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	viewerReq := withDashboardSession(t, f.srv, browserRequest(http.MethodGet, "/"), "viewer", dashboardRoleViewer)
	viewerCookie, _ := viewerReq.Cookie(testSessionCookie)

	for name, cookies := range map[string][]*http.Cookie{"nobody": nil, "a viewer": {viewerCookie}} {
		rec, state := f.startSignIn(t, oidcLoginPath+"?purpose=test", cookies...)
		require.Equal(t, "/settings/auth?sso_error=forbidden", rec.Header().Get("Location"), name)
		require.Nil(t, state, name)
	}
	req := browserRequest(http.MethodGet, oidcLoginPath+"?purpose=test")
	req.Header.Set("Authorization", "Bearer "+f.workspaceToken(t, apitokens.RoleAdmin))
	require.Equal(t, "/settings/auth?sso_error=forbidden", f.do(req).Header().Get("Location"), "a token")
}

func TestATestSignInSaysWhenTheProviderCannotBeReached(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	f.idp.server.Close()

	rec, state := f.startSignIn(t, oidcLoginPath+"?purpose=test", f.adminCookie(t))

	require.Equal(t, "/settings/auth?sso_error=provider", rec.Header().Get("Location"))
	require.Nil(t, state)
}

func TestRemovingTheProviderRemovesItsSecretAndGoesBackToLocal(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	person := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))

	rec := f.asAdmin(t, postForm("/api/settings/auth/clear", url.Values{}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, f.config(t).Spec.Auth)
	require.False(t, f.secretExists(t, platform.PlatformAuthSecretName), "the client secret was left behind")
	require.False(t, f.authenticated(t, person), "a session of the provider counted after it was removed")
}

func TestASecretAnOperatorMadeIsNotDeletedWithTheProvider(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	require.NoError(t, f.srv.Client.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-own-secret", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.PlatformAuthClientSecretKey: []byte("theirs")},
	}))
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.OIDC.CredentialRef = &corev1.LocalObjectReference{Name: "my-own-secret"}
	})
	settings, err := f.srv.ssoSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "theirs", settings.Config.ClientSecret, "a secret the operator named is the one used")

	require.Equal(t, http.StatusOK, f.asAdmin(t, postForm("/api/settings/auth/clear", url.Values{})).Code)

	require.True(t, f.secretExists(t, "my-own-secret"))
}

func TestSwitchingBackToLocalNeedsNothing(t *testing.T) {
	f := newBareSSOFixture(t)
	rec := f.switchMode(t, platform.AuthModeLocal)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.False(t, f.hasConfig(t), "nothing needed to be written")

	require.Equal(t, http.StatusBadRequest, f.switchMode(t, platform.AuthModeExternal).Code, "external with no provider set up")
	require.Equal(t, http.StatusBadRequest, f.switchMode(t, "sideways").Code)
}

func TestAdminHasPassword(t *testing.T) {
	admin := dashboardUser{Username: "a", Password: "x", Role: dashboardRoleAdmin}
	viewer := dashboardUser{Username: "v", Password: "x", Role: dashboardRoleViewer}
	require.True(t, adminHasPassword([]dashboardUser{viewer, admin}))
	require.False(t, adminHasPassword([]dashboardUser{viewer}))
	require.False(t, adminHasPassword([]dashboardUser{{Username: "a", Role: dashboardRoleAdmin}}))
	require.False(t, adminHasPassword(nil))
}

func TestReadingPairsAndLists(t *testing.T) {
	pairs, err := parsePairs(" owner = admin ,\nmember=viewer\r\n")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"owner": "admin", "member": "viewer"}, pairs)
	for _, bad := range []string{"owner", "=admin", "owner=", "owner=admin,member"} {
		_, err := parsePairs(bad)
		require.Error(t, err, bad)
	}
	require.Equal(t, []string{"email", "profile", "groups"}, splitList("email, profile\ngroups"))
	require.Empty(t, splitList("  "))
}

func TestTheKeyOfAPersonIsStableAcrossAnEmailChange(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	first := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	renamed := f.signedInAs(t, testPerson("u-alice", "alice.smith@example.com", "owner"))

	// Signing out of one ends the other: they are the same person, whatever the email says.
	out := postForm("/api/logout", nil)
	out.AddCookie(renamed)
	require.Equal(t, http.StatusOK, f.do(out).Code)
	require.False(t, f.authenticated(t, first))
	require.Equal(t, oidcauth.Key(f.idp.server.URL, "u-alice"), oidcauth.Key(f.idp.server.URL+"/", "u-alice"))
}

// setLogoutPolicy asks the settings API, as a signed-in admin, whether a logout from the provider revokes tokens.
func (f ssoFixture) setLogoutPolicy(t *testing.T, value string) *httptest.ResponseRecorder {
	t.Helper()
	return f.asAdmin(t, postForm("/api/settings/auth/logout-policy", url.Values{"revokeTokens": {value}}))
}

func TestAProviderThatIsNotSetUpHasNoLogoutPolicyToChange(t *testing.T) {
	f := newBareSSOFixture(t)
	rec := f.setLogoutPolicy(t, "off")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.False(t, f.hasConfig(t), "a setting about a provider that does not exist created the platform configuration")
}

func TestTheLogoutPolicyTakesOnOrOffAndNothingElse(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	for _, bad := range []string{"", "maybe", "2", "yes please"} {
		require.Equal(t, http.StatusBadRequest, f.setLogoutPolicy(t, bad).Code, bad)
	}
	require.Equal(t, true, f.view(t)["revokeTokensOnLogout"], "a refused value changed the setting")
	for value, want := range map[string]bool{"off": false, "ON": true, "false": false, "true": true} {
		require.Equal(t, http.StatusOK, f.setLogoutPolicy(t, value).Code, value)
		require.Equal(t, want, f.view(t)["revokeTokensOnLogout"], value)
	}
}

func TestTheLogoutPolicyCanBeChangedWhilePeopleSignInThroughTheProvider(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)

	require.Equal(t, http.StatusOK, f.setLogoutPolicy(t, "off").Code, "the policy could not be changed while the provider decides")

	require.Equal(t, false, f.view(t)["revokeTokensOnLogout"])
	require.Equal(t, platform.AuthModeExternal, f.config(t).Spec.Auth.Mode, "changing the policy changed how people sign in")
	require.Equal(t, http.StatusBadRequest, f.save(t, func(v url.Values) { v.Set("roleMap", "owner=viewer") }).Code, "the provider's own settings are still locked while it decides")
}

func TestSavingTheProviderKeepsTheLogoutPolicy(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	require.Equal(t, http.StatusOK, f.setLogoutPolicy(t, "off").Code)

	rec := f.save(t, func(v url.Values) {
		v.Set("clientSecret", "")
		v.Set("keepSecret", "on")
		v.Set("roleMap", "owner=admin")
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, false, f.view(t)["revokeTokensOnLogout"], "saving the provider put the policy back")
}

func TestARevocationPolicyThatWasNeverSetIsTheSafeOne(t *testing.T) {
	var config geassv1alpha1.GeassPlatformConfig
	require.True(t, ssoSettingsOf(config).RevokeTokensOnLogout, "no provider at all")
	config.Spec.Auth = &geassv1alpha1.GeassPlatformAuthSpec{OIDC: &geassv1alpha1.GeassPlatformOIDCSpec{Issuer: "https://auth.example.com"}}
	require.True(t, ssoSettingsOf(config).RevokeTokensOnLogout, "a provider whose policy was never set")
	off := false
	config.Spec.Auth.OIDC.RevokeTokensOnLogout = &off
	require.False(t, ssoSettingsOf(config).RevokeTokensOnLogout)
}

func TestALogoutKeepsTheTokensOfThePersonWhenToldTo(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	alice := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	makeToken := func(name string) {
		req := postForm("/api/tokens/create", url.Values{"name": {name}, "scope": {"workspace"}, "role": {"viewer"}})
		req.AddCookie(alice)
		require.Equal(t, http.StatusOK, f.do(req).Code)
	}
	tokenNames := func() []string {
		records, err := f.srv.listAPITokens(context.Background())
		require.NoError(t, err)
		names := make([]string, 0, len(records))
		for _, record := range records {
			names = append(names, record.Name)
		}
		return names
	}
	makeToken("ci")
	require.Equal(t, http.StatusOK, f.setLogoutPolicy(t, "off").Code)

	require.Equal(t, http.StatusOK, f.backchannel(f.idp.logoutToken("u-alice", nil), "198.51.100.7:4000").Code)

	require.False(t, f.authenticated(t, alice), "the sessions of the person must end whatever the policy says")
	require.Equal(t, []string{"ci"}, tokenNames(), "a token was revoked although the policy says to keep them")

	// Turned back on, the next logout takes what the person made, the token that was kept included.
	require.Equal(t, http.StatusOK, f.setLogoutPolicy(t, "on").Code)
	time.Sleep(3 * time.Millisecond)
	again := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	req := postForm("/api/tokens/create", url.Values{"name": {"later"}, "scope": {"workspace"}, "role": {"viewer"}})
	req.AddCookie(again)
	require.Equal(t, http.StatusOK, f.do(req).Code)
	require.ElementsMatch(t, []string{"ci", "later"}, tokenNames())

	require.Equal(t, http.StatusOK, f.backchannel(f.idp.logoutToken("u-alice", nil), "198.51.100.7:4000").Code)

	require.False(t, f.authenticated(t, again))
	require.Empty(t, tokenNames(), "with the policy on, a logout leaves tokens the person made")
}

// aControllerWrite makes the next saves of the platform configuration meet a conflict because the controller wrote the
// object (here, an annotation) in between.
func (f ssoFixture) aControllerWrite(t *testing.T, conflicts int32) *flakyClient {
	t.Helper()
	flaky := f.flaky()
	flaky.concurrentWriter = func() {
		var config geassv1alpha1.GeassPlatformConfig
		key := client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}
		require.NoError(t, flaky.Client.Get(context.Background(), key, &config))
		if config.Annotations == nil {
			config.Annotations = map[string]string{}
		}
		config.Annotations["written-by"] = "the controller"
		require.NoError(t, flaky.Client.Update(context.Background(), &config))
	}
	flaky.configConflicts.Store(conflicts)
	return flaky
}

func TestSettingsSurviveAControllerWritingTheConfigurationInBetween(t *testing.T) {
	for name, do := range map[string]func(f ssoFixture) *httptest.ResponseRecorder{
		"saving the provider": func(f ssoFixture) *httptest.ResponseRecorder {
			return f.save(t, func(v url.Values) {
				v.Set("clientSecret", "")
				v.Set("keepSecret", "on")
				v.Set("roleMap", "owner=admin")
			})
		},
		"changing the logout policy": func(f ssoFixture) *httptest.ResponseRecorder { return f.setLogoutPolicy(t, "off") },
		"removing the provider": func(f ssoFixture) *httptest.ResponseRecorder {
			return f.asAdmin(t, postForm("/api/settings/auth/clear", url.Values{}))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSSOFixture(t, platform.AuthModeLocal)
			f.aControllerWrite(t, 2)

			rec := do(f)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			config := f.config(t)
			require.Equal(t, "the controller", config.Annotations["written-by"], "what the controller wrote was lost to the save")
			switch name {
			case "saving the provider":
				require.Equal(t, map[string]string{"owner": "admin"}, config.Spec.Auth.OIDC.RoleMap)
			case "changing the logout policy":
				require.False(t, *config.Spec.Auth.OIDC.RevokeTokensOnLogout)
			case "removing the provider":
				require.Nil(t, config.Spec.Auth)
			}
		})
	}
}

func TestTurningTheProviderOnSurvivesAControllerWritingTheConfigurationInBetween(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	f.testSignIn(t, testPerson("u-alice", "alice@example.com", "owner"))
	f.aControllerWrite(t, 2)

	rec := f.switchMode(t, platform.AuthModeExternal)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	config := f.config(t)
	require.Equal(t, platform.AuthModeExternal, config.Spec.Auth.Mode)
	require.Equal(t, "the controller", config.Annotations["written-by"])
}

func TestAConflictThatKeepsHappeningIsReportedNotHidden(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	f.aControllerWrite(t, 1000)

	rec := f.setLogoutPolicy(t, "off")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "could not save the setting")
	require.Equal(t, true, f.view(t)["revokeTokensOnLogout"], "a save that failed changed the setting")
}

func TestASecretIsPutBackAsItWasWhenWhatNamesItCannotBeSaved(t *testing.T) {
	f := newBareSSOFixture(t)
	ctx := context.Background()
	key := client.ObjectKey{Name: "some-secret", Namespace: platform.SystemNamespace}
	read := func() (*corev1.Secret, error) {
		secret := &corev1.Secret{}
		return secret, f.srv.Client.Get(ctx, key, secret)
	}
	write := func(value string) *corev1.Secret {
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Data: map[string][]byte{"k": []byte(value)}}
	}

	t.Run("one that was new is deleted", func(t *testing.T) {
		rollback, err := f.srv.persistSecretWithRollback(ctx, write("new"))
		require.NoError(t, err)
		_, err = read()
		require.NoError(t, err, "the Secret was not written")
		rollback()
		_, err = read()
		require.True(t, apierrors.IsNotFound(err), "a Secret that did not exist before was left behind")
	})
	t.Run("one that existed gets its earlier contents back", func(t *testing.T) {
		require.NoError(t, f.srv.Client.Create(ctx, write("old")))
		rollback, err := f.srv.persistSecretWithRollback(ctx, write("new"))
		require.NoError(t, err)
		current, _ := read()
		require.Equal(t, "new", string(current.Data["k"]))
		rollback()
		restored, err := read()
		require.NoError(t, err)
		require.Equal(t, "old", string(restored.Data["k"]))
	})
	t.Run("deleting one that is not there is not an error, and an empty name is nothing", func(t *testing.T) {
		require.NoError(t, f.srv.deleteSystemSecret(ctx, "never-existed"))
		require.NoError(t, f.srv.deleteSystemSecret(ctx, ""))
	})
}

func TestTheRedirectAddressIsTheDashboardsOwnWhenItIsPublishedAndNotWhateverHostAskedForIt(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.DashboardURL = "https://geass.example.org"
		c.Spec.RootDomain = "example.org"
	})
	// The domain is verified: a condition, which lives in the status.
	verified := f.config(t)
	verified.Status.Conditions = platform.SetConditionForGeneration(nil, platform.ConditionDashboardDomainReady, metav1.ConditionTrue, "Verified", "ok", verified.Generation)
	require.NoError(t, f.srv.Client.Status().Update(context.Background(), &verified))
	redirectURI := func(host string) string {
		req := browserRequest(http.MethodGet, oidcLoginPath)
		req.Host = host
		rec := f.do(req)
		require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
		return mustQuery(t, rec.Header().Get("Location")).Get("redirect_uri")
	}

	for _, host := range []string{"geass.example.org", "evil.example.net", "127.0.0.1:8085"} {
		require.Equal(t, "https://geass.example.org/auth/oidc/callback", redirectURI(host), "a request for "+host+" decided where the code goes")
	}
	view := f.view(t)
	require.Equal(t, "https://geass.example.org/auth/oidc/callback", view["callbackURL"])
	require.Equal(t, "https://geass.example.org/auth/oidc/backchannel-logout", view["backchannelLogoutURL"])
}

func TestWithNoPublishedAddressTheRedirectIsTheOneTheBrowserUsed(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	for name, mutate := range map[string]func(*geassv1alpha1.GeassPlatformConfig){
		"no domain at all": func(*geassv1alpha1.GeassPlatformConfig) {},
		"a domain that is not verified yet": func(c *geassv1alpha1.GeassPlatformConfig) {
			c.Spec.DashboardURL = "https://geass.example.org"
			c.Spec.RootDomain = "example.org"
		},
	} {
		t.Run(name, func(t *testing.T) {
			f.updateConfig(t, mutate)
			req := browserRequest(http.MethodGet, oidcLoginPath)
			req.Host = "localhost:8085"
			rec := f.do(req)
			require.Equal(t, "http://localhost:8085/auth/oidc/callback", mustQuery(t, rec.Header().Get("Location")).Get("redirect_uri"))
		})
	}
}
