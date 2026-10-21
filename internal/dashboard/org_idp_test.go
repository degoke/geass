package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// orgIdPFixture is the organizations fixture with a second provider, the one organization A brings.
type orgIdPFixture struct {
	tenancyFixture
	own *testIdP
}

func newOrgIdPFixture(t *testing.T) orgIdPFixture {
	t.Helper()
	return orgIdPFixture{tenancyFixture: newTenancyFixture(t), own: newTestIdP(t)}
}

func (f orgIdPFixture) saveIdP(t *testing.T, org string, extra ...string) *httptest.ResponseRecorder {
	t.Helper()
	values := form("displayName", "Acme SSO", "issuer", f.own.server.URL, "clientID", ssoTestClient, "clientSecret", ssoTestSecret,
		"roleClaim", "org_role", "roleMap", "owner=admin\nmember=viewer")
	for i := 0; i+1 < len(extra); i += 2 {
		values.Set(extra[i], extra[i+1])
	}
	return f.asAdmin(t, postForm("/api/orgs/"+org+"/idp/save", values))
}

func (f orgIdPFixture) organization(t *testing.T, name string) geassv1alpha1.GeassOrganization {
	t.Helper()
	var org geassv1alpha1.GeassOrganization
	require.NoError(t, f.srv.Client.Get(asSystem(context.Background()), key(name), &org))
	return org
}

// testIdP runs the test sign-in of an organization's provider as the platform's administrator, with the person the
// organization's provider has at the keyboard.
func (f orgIdPFixture) testIdP(t *testing.T, org string, person map[string]any) {
	t.Helper()
	f.own.mu.Lock()
	f.own.person = person
	f.own.mu.Unlock()
	start, state := f.startSignIn(t, oidcLoginPath+"?purpose=test&org="+org, f.adminCookie(t))
	require.Equal(t, http.StatusFound, start.Code, start.Body.String())
	require.NotNil(t, state, start.Header().Get("Location"))
	back, err := url.Parse(f.atProvider(t, start.Header().Get("Location")))
	require.NoError(t, err)
	rec := f.do(browserRequest(http.MethodGet, back.RequestURI(), state))
	require.Equal(t, http.StatusFound, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "org_idp_test="+org)
}

func (f orgIdPFixture) enable(t *testing.T, org string, on bool) *httptest.ResponseRecorder {
	t.Helper()
	value := "off"
	if on {
		value = "on"
	}
	return f.asAdmin(t, postForm("/api/orgs/"+org+"/idp/enable", form("enabled", value)))
}

// readyIdP sets the organization's provider up, tests it and turns it on.
func (f orgIdPFixture) readyIdP(t *testing.T) {
	t.Helper()
	require.Equal(t, http.StatusOK, f.saveIdP(t, orgA).Code)
	f.testIdP(t, orgA, testPerson("u-admin", "admin@acme.example", "owner"))
	require.Equal(t, http.StatusOK, f.enable(t, orgA, true).Code)
}

func (f orgIdPFixture) signInThrough(t *testing.T, org string, person map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	f.own.mu.Lock()
	f.own.person = person
	f.own.mu.Unlock()
	start, state := f.startSignIn(t, oidcLoginPath+"?org="+org)
	if start.Code != http.StatusFound || state == nil {
		return start
	}
	back, err := url.Parse(f.atProvider(t, start.Header().Get("Location")))
	require.NoError(t, err)
	return f.do(browserRequest(http.MethodGet, back.RequestURI(), state))
}

func (f orgIdPFixture) cookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "/", rec.Header().Get("Location"), "it lands on the dashboard: %s", rec.Header().Get("Location"))
	session := cookieNamed(rec, testSessionCookie)
	require.NotNil(t, session)
	return session
}

func TestAnOrganizationsOwnProviderIsSavedTestedAndOnlyThenTurnedOn(t *testing.T) {
	f := newOrgIdPFixture(t)

	rec := f.saveIdP(t, orgA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	org := f.organization(t, orgA)
	require.NotNil(t, org.Spec.IdP)
	require.False(t, org.Spec.IdP.Enabled, "saved, not on")
	require.Equal(t, orgIdPSecretName(orgA), org.Spec.IdP.CredentialRef.Name)
	var secret corev1.Secret
	require.NoError(t, f.srv.Client.Get(asSystem(context.Background()), key(orgIdPSecretName(orgA)), &secret))
	require.Equal(t, ssoTestSecret, string(secret.Data[platform.PlatformAuthClientSecretKey]))

	listed := f.asAdmin(t, browserRequest(http.MethodGet, "/api/orgs"))
	require.Equal(t, http.StatusOK, listed.Code)
	require.NotContains(t, listed.Body.String(), ssoTestSecret, "the client secret never comes back")
	require.Contains(t, listed.Body.String(), `"loginPath":"/auth/oidc/login?org=`+orgA+`"`)

	require.Equal(t, http.StatusBadRequest, f.enable(t, orgA, true).Code, "not before a test sign-in")
	f.testIdP(t, orgA, testPerson("u-member", "m@acme.example", "member"))
	rec = f.enable(t, orgA, true)
	require.Equal(t, http.StatusBadRequest, rec.Code, "a test as someone who maps to viewer cannot run the organization: "+rec.Body.String())
	f.testIdP(t, orgA, testPerson("u-admin", "admin@acme.example", "owner"))
	require.Equal(t, http.StatusOK, f.enable(t, orgA, true).Code)
	require.True(t, f.organization(t, orgA).Spec.IdP.Enabled)

	// Saving anything again turns it off and needs a new test.
	require.Equal(t, http.StatusOK, f.saveIdP(t, orgA, "displayName", "Acme again").Code)
	require.False(t, f.organization(t, orgA).Spec.IdP.Enabled)
	require.Equal(t, http.StatusBadRequest, f.enable(t, orgA, true).Code)
}

func TestAPersonSignsInToAnOrganizationThroughItsOwnProviderAndWorksOnlyThere(t *testing.T) {
	f := newOrgIdPFixture(t)
	f.readyIdP(t)

	cookie := f.cookieOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner")))
	session := f.session(t, cookie)
	require.Equal(t, true, session["authenticated"])
	require.Equal(t, "admin", session["role"])
	require.Equal(t, orgA, session["org"])
	require.Empty(t, session["platformAdmin"])
	require.ElementsMatch(t, []string{"app-a0", "app-a1", "app-a2"}, f.visibleApps(t, cookie), "all of the organization, and nothing of the other")

	// A person who maps to viewer reads all of the organization, and cannot change it.
	viewer := f.cookieOf(t, f.signInThrough(t, orgA, testPerson("u-vic", "vic@acme.example", "member")))
	viewerSession := f.session(t, viewer)
	require.Equal(t, "viewer", viewerSession["role"])
	require.Equal(t, false, viewerSession["canMutate"])
	require.ElementsMatch(t, []string{"app-a0", "app-a1", "app-a2"}, f.visibleApps(t, viewer))
	writes := f.do(withSession(browserRequest(http.MethodPost, "/api/apps/app-a1/update", viewer)))
	require.Equal(t, http.StatusForbidden, writes.Code)
	require.ElementsMatch(t, []any{
		map[string]any{"org": orgA, "orgName": "Acme", "role": "viewer"},
		map[string]any{"org": orgA, "orgName": "Acme", "workspace": wsA1, "workspaceName": "First", "role": "viewer"},
		map[string]any{"org": orgA, "orgName": "Acme", "workspace": wsA2, "workspaceName": "Second", "role": "viewer"},
	}, viewerSession["scopes"], "it may look at all of the organization, or at one workspace, and only read")

	// Nothing the provider says can make them an administrator of the platform, or put them in the other organization.
	grand := testPerson("u-ada", "ada@acme.example", "owner")
	grand["geass_platform"] = "operator"
	grand["orgs"] = []any{map[string]any{"id": "org_beta", "role": "owner"}}
	grandCookie := f.cookieOf(t, f.signInThrough(t, orgA, grand))
	grandSession := f.session(t, grandCookie)
	require.Empty(t, grandSession["platformAdmin"])
	require.Equal(t, orgA, grandSession["org"])
	require.ElementsMatch(t, []string{"app-a0", "app-a1", "app-a2"}, f.visibleApps(t, grandCookie))
	require.Equal(t, http.StatusForbidden, f.do(browserRequest(http.MethodGet, "/api/settings/registry", grandCookie)).Code, "the platform's pages are not theirs")
	require.Equal(t, http.StatusForbidden, f.do(browserRequest(http.MethodGet, "/api/orgs", grandCookie)).Code)
}

func TestAPersonTheOrganizationsProviderDoesNotMapToARoleIsTurnedAway(t *testing.T) {
	f := newOrgIdPFixture(t)
	f.readyIdP(t)

	rec := f.signInThrough(t, orgA, testPerson("u-x", "x@acme.example", "guest"))
	require.Equal(t, "no_role", failureOf(t, rec))
	require.Nil(t, cookieNamed(rec, testSessionCookie))
	stranger := testPerson("u-y", "y@acme.example", "owner")
	stranger["organization_id"] = "org_other"
	require.NoError(t, f.srv.Client.Get(asSystem(context.Background()), key(orgA), &geassv1alpha1.GeassOrganization{}))
	require.Equal(t, http.StatusOK, f.saveIdP(t, orgA, "requireClaim", "organization_id=org_acme").Code)
	f.testIdP(t, orgA, testPerson("u-admin", "admin@acme.example", "owner"))
	require.Equal(t, http.StatusOK, f.enable(t, orgA, true).Code)
	require.Equal(t, "required_claim", failureOf(t, f.signInThrough(t, orgA, stranger)))
}

func TestAnOrganizationWhoseProviderIsOffOrUnknownCannotBeSignedInTo(t *testing.T) {
	f := newOrgIdPFixture(t)

	// Unknown, and known with no provider: the same answer, so that which have one is not found by asking.
	for _, name := range []string{"org-00000000", orgA, orgB} {
		rec := f.signInThrough(t, name, testPerson("u-ada", "ada@acme.example", "owner"))
		require.Equal(t, "disabled", failureOf(t, rec), name)
	}
	require.Equal(t, http.StatusOK, f.saveIdP(t, orgA).Code)
	require.Equal(t, "disabled", failureOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner"))), "saved but not turned on")

	f.testIdP(t, orgA, testPerson("u-admin", "admin@acme.example", "owner"))
	require.Equal(t, http.StatusOK, f.enable(t, orgA, true).Code)
	org := f.organization(t, orgA)
	org.Spec.Suspended = true
	require.NoError(t, f.srv.Client.Update(asSystem(context.Background()), &org))
	require.Equal(t, "disabled", failureOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner"))), "a suspended organization")
}

func TestTheSessionsMadeThroughAnOrganizationsProviderEndWhenItChangesOrIsTurnedOff(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, f orgIdPFixture){
		"it is saved again": func(t *testing.T, f orgIdPFixture) { require.Equal(t, http.StatusOK, f.saveIdP(t, orgA).Code) },
		"it is turned off":  func(t *testing.T, f orgIdPFixture) { require.Equal(t, http.StatusOK, f.enable(t, orgA, false).Code) },
		"it is removed": func(t *testing.T, f orgIdPFixture) {
			require.Equal(t, http.StatusOK, f.asAdmin(t, postForm("/api/orgs/"+orgA+"/idp/clear", form())).Code)
		},
		"the organization closes": func(t *testing.T, f orgIdPFixture) {
			org := f.organization(t, orgA)
			org.Spec.Suspended = true
			require.NoError(t, f.srv.Client.Update(asSystem(context.Background()), &org))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newOrgIdPFixture(t)
			f.readyIdP(t)
			cookie := f.cookieOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner")))
			require.True(t, f.authenticated(t, cookie))

			change(t, f)

			require.False(t, f.authenticated(t, cookie), "the session was made for a provider that is not the one now")
		})
	}
}

func TestTheSessionOfOneOrganizationsProviderDoesNotSignInToAnother(t *testing.T) {
	f := newOrgIdPFixture(t)
	f.readyIdP(t)
	cookie := f.cookieOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner")))

	// The cookie claims another organization: it is signed, so it cannot be changed, but a session made for A is A's alone.
	require.Equal(t, orgA, f.session(t, cookie)["org"])
	require.Equal(t, http.StatusNotFound, f.do(browserRequest(http.MethodGet, "/api/v1/apps/app-b1", cookie)).Code)
}

func TestOnlyThePlatformSetsAnOrganizationsProviderUp(t *testing.T) {
	f := newOrgIdPFixture(t)
	for _, path := range []string{"/api/orgs/" + orgA + "/idp/save", "/api/orgs/" + orgA + "/idp/enable", "/api/orgs/" + orgA + "/idp/clear"} {
		rec := f.do(withSession(browserRequest(http.MethodPost, path, f.signedInAs(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner"))))))
		require.Equal(t, http.StatusForbidden, rec.Code, path)
	}
	require.Nil(t, f.organization(t, orgA).Spec.IdP)
}

func TestAnOrganizationsProviderIsCheckedBeforeItIsSaved(t *testing.T) {
	f := newOrgIdPFixture(t)
	for name, extra := range map[string][]string{
		"an address that is not https": {"issuer", "http://idp.example.com"},
		"no client":                    {"clientID", ""},
		"no role map":                  {"roleMap", ""},
		"a role that is not one":       {"roleMap", "owner=emperor"},
		"no role claim":                {"roleClaim", ""},
	} {
		require.Equal(t, http.StatusBadRequest, f.saveIdP(t, orgA, extra...).Code, name)
	}
	require.Equal(t, http.StatusNotFound, f.saveIdP(t, "org-00000000").Code, "an organization that is not there")
	require.Nil(t, f.organization(t, orgA).Spec.IdP)
	require.True(t, apierrors.IsNotFound(f.srv.Client.Get(asSystem(context.Background()), key(orgIdPSecretName(orgA)), &corev1.Secret{})))
}

func TestAnOrganizationsClientSecretIsKeptOnlyForTheProviderItWasSavedFor(t *testing.T) {
	f := newOrgIdPFixture(t)
	require.Equal(t, http.StatusOK, f.saveIdP(t, orgA).Code)
	// Another client, asking to keep the secret that was saved for the first one.
	rec := f.saveIdP(t, orgA, "clientID", "someone-elses", "clientSecret", "", "keepSecret", "on")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Enter the client secret again")
	// The same client keeps it.
	require.Equal(t, http.StatusOK, f.saveIdP(t, orgA, "clientSecret", "", "keepSecret", "on").Code)
	var secret corev1.Secret
	require.NoError(t, f.srv.Client.Get(asSystem(context.Background()), key(orgIdPSecretName(orgA)), &secret))
	require.Equal(t, ssoTestSecret, string(secret.Data[platform.PlatformAuthClientSecretKey]))
}

func TestTheOrganizationsProviderIsOnlyUsedWhileOrganizationsAreOn(t *testing.T) {
	f := newOrgIdPFixture(t)
	f.readyIdP(t)
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth.Tenancy.Enabled = false })
	require.Equal(t, "disabled", failureOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner"))))
}

func TestAnOrganizationsRefreshedPersonWhoseRoleChangedIsSignedOut(t *testing.T) {
	f := newOrgIdPFixture(t)
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth.OIDC.RefreshMinutes = 10 })
	f.readyIdP(t)
	cookie := f.cookieOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner")))
	require.True(t, f.authenticated(t, cookie))

	age := func() {
		require.NoError(t, f.srv.mutateRefreshSecret(context.Background(), func(entries map[string][]byte) bool {
			for name, raw := range entries {
				entries[name] = rewindRefresh(t, raw, 11*time.Minute)
			}
			return true
		}))
	}
	age()
	f.srv.refreshSessionsOnce(context.Background())
	require.True(t, f.authenticated(t, cookie), "nothing changed")

	f.own.mu.Lock()
	f.own.refreshed["u-ada"] = testPerson("u-ada", "ada@acme.example", "member")
	f.own.mu.Unlock()
	age()
	f.srv.refreshSessionsOnce(context.Background())
	require.False(t, f.authenticated(t, cookie), "a role that changed at the organization's provider is noticed")
}

func TestAnIdentityMadeThroughAnOrganizationsProviderIsToldFromAnyOther(t *testing.T) {
	identity := orgIdentity("org-1a2b3c4d", "abcdef012345", "sso:0123456789abcdef")
	org, state, ok := parseOrgIdentity(identity)
	require.True(t, ok)
	require.Equal(t, "org-1a2b3c4d", org)
	require.Equal(t, "abcdef012345", state)
	for _, other := range []string{"sso:0123", "", "org-sso:", "org-sso:a:b", "org-sso::b:c", "org-sso:a::c"} {
		_, _, ok := parseOrgIdentity(other)
		require.False(t, ok, other)
	}
	require.NotEqual(t, orgIdentity("org-a", "s1", "sso:x"), orgIdentity("org-b", "s1", "sso:x"), "the same person at two organizations' providers is two identities")
}

// rewindRefresh makes an entry's last check older by d.
func rewindRefresh(t *testing.T, raw []byte, d time.Duration) []byte {
	t.Helper()
	var record refreshRecord
	require.NoError(t, json.Unmarshal(raw, &record))
	record.Checked = time.Now().Add(-d).Unix()
	out, err := json.Marshal(record)
	require.NoError(t, err)
	return out
}

func TestAnOrganizationsProviderThatWasTurnedOffAndOnAgainDoesNotRevivePastSessions(t *testing.T) {
	f := newOrgIdPFixture(t)
	f.readyIdP(t)
	cookie := f.cookieOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner")))
	require.True(t, f.authenticated(t, cookie))

	require.Equal(t, http.StatusOK, f.enable(t, orgA, false).Code)
	f.testIdP(t, orgA, testPerson("u-admin", "admin@acme.example", "owner"))
	require.Equal(t, http.StatusOK, f.enable(t, orgA, true).Code)

	require.False(t, f.authenticated(t, cookie), "a session made before it was turned off stays ended")
	fresh := f.cookieOf(t, f.signInThrough(t, orgA, testPerson("u-ada", "ada@acme.example", "owner")))
	require.True(t, f.authenticated(t, fresh), "and signing in again works")
}

func TestOnlyThePlatformsAdministratorsStartATestOfAnOrganizationsProvider(t *testing.T) {
	f := newOrgIdPFixture(t)
	require.Equal(t, http.StatusOK, f.saveIdP(t, orgA).Code)
	orgAdmin := f.signedInAs(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner")))

	rec := f.do(browserRequest(http.MethodGet, oidcLoginPath+"?purpose=test&org="+orgA, orgAdmin))
	require.Equal(t, http.StatusFound, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "sso_error=forbidden", "an organization's own administrator is not the one who turns its provider on")
	require.Nil(t, cookieNamed(rec, testStateCookie))
}
