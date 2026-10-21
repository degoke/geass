package dashboard

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

// tenancyFixture is the identity-provider fixture with organizations turned on: two organizations set up in Geass (one with
// two workspaces), a project in each place, and a provider whose tokens list the organizations a person is in.
type tenancyFixture struct {
	ssoFixture
}

func newTenancyFixture(t *testing.T) tenancyFixture {
	t.Helper()
	f := tenancyFixture{newSSOFixture(t, platform.AuthModeExternal)}
	// The provider's own claim does not matter for organizations: the role is read from each membership.
	f.srv.Client = ScopeClient(f.srv.Client)
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.OIDC.RequireClaim = nil
		c.Spec.Auth.Tenancy = &geassv1alpha1.GeassPlatformTenancySpec{
			Enabled: true, OrgsClaim: "orgs", PlatformClaim: "geass_platform", PlatformValues: []string{"operator"},
		}
	})
	ctx := context.Background()
	for _, object := range []client.Object{
		matrixOrg(orgA, "Acme", "org_acme", geassv1alpha1.GeassOrganizationWorkspace{ID: wsA1, DisplayName: "First", IdPID: "team_1"},
			geassv1alpha1.GeassOrganizationWorkspace{ID: wsA2, DisplayName: "Second", IdPID: "team_2"}),
		matrixOrg(orgB, "Beta", "org_beta"),
		tenantProject("pa0", orgA, ""), tenantProject("pa1", orgA, wsA1), tenantProject("pa2", orgA, wsA2), tenantProject("pb1", orgB, ""),
		tenantApp("app-a0", "pa0"), tenantApp("app-a1", "pa1"), tenantApp("app-a2", "pa2"), tenantApp("app-b1", "pb1"),
	} {
		require.NoError(t, f.srv.Client.Create(asSystem(ctx), object))
	}
	return f
}

// member is a person the provider puts in organizations: its entries are {id, role, workspaces}.
func member(subject, email string, orgs ...map[string]any) map[string]any {
	list := make([]any, len(orgs))
	for i, org := range orgs {
		list[i] = org
	}
	return map[string]any{"sub": subject, "email": email, "email_verified": true, "orgs": list}
}

func inOrg(id, role string, workspaces ...any) map[string]any {
	return map[string]any{"id": id, "role": role, "workspaces": workspaces}
}

// visibleApps are the services a cookie's holder is shown.
func (f tenancyFixture) visibleApps(t *testing.T, cookie *http.Cookie) []string {
	t.Helper()
	rec := f.do(browserRequest(http.MethodGet, "/api/v1/apps", cookie))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return decodeNames(t, rec.Body.Bytes(), "items")
}

func TestAnOrganizationAdministratorSignsInToItsOwnOrganization(t *testing.T) {
	f := newTenancyFixture(t)

	cookie := f.signedInAs(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner")))

	session := f.session(t, cookie)
	require.Equal(t, true, session["authenticated"])
	require.Equal(t, "admin", session["role"])
	require.Equal(t, orgA, session["org"])
	require.Empty(t, session["workspace"], "an administrator starts in all of the organization")
	require.Empty(t, session["platformAdmin"])
	require.ElementsMatch(t, []any{
		map[string]any{"org": orgA, "orgName": "Acme", "role": "admin"},
		map[string]any{"org": orgA, "orgName": "Acme", "workspace": wsA1, "workspaceName": "First", "role": "admin"},
		map[string]any{"org": orgA, "orgName": "Acme", "workspace": wsA2, "workspaceName": "Second", "role": "admin"},
	}, session["scopes"])
	require.ElementsMatch(t, []string{"app-a0", "app-a1", "app-a2"}, f.visibleApps(t, cookie), "and sees what is the organization's, and nothing of the other")
}

func TestAMemberWorksInTheirWorkspaceWithTheirRole(t *testing.T) {
	f := newTenancyFixture(t)

	cookie := f.signedInAs(t, member("u-bob", "bob@example.com", inOrg("org_acme", "member", "team_1")))

	session := f.session(t, cookie)
	require.Equal(t, orgA, session["org"])
	require.Equal(t, wsA1, session["workspace"], "a member starts in a workspace they are in")
	require.Equal(t, "viewer", session["role"])
	require.Equal(t, false, session["canMutate"])
	require.Equal(t, []string{"app-a1"}, f.visibleApps(t, cookie), "one workspace's projects only")
	require.Len(t, session["scopes"], 1, "and is offered only the workspace they are in")

	writes := f.do(withSession(browserRequest(http.MethodPost, "/api/apps/app-a1/update", cookie)))
	require.Equal(t, http.StatusForbidden, writes.Code, "a viewer cannot change anything")

	// A role in the workspace itself lifts them there.
	lead := f.signedInAs(t, member("u-lee", "lee@example.com", inOrg("org_acme", "member", map[string]any{"id": "team_2", "role": "admin"})))
	leadSession := f.session(t, lead)
	require.Equal(t, wsA2, leadSession["workspace"])
	require.Equal(t, "admin", leadSession["role"], "the role of the workspace counts")
	require.Equal(t, []string{"app-a2"}, f.visibleApps(t, lead))
}

func withSession(req *http.Request) *http.Request {
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return req
}

func TestAPersonWhoseOrganizationsAreNotSetUpIsRefused(t *testing.T) {
	f := newTenancyFixture(t)

	for name, person := range map[string]map[string]any{
		"an organization Geass does not know":          member("u-x", "x@example.com", inOrg("org_unknown", "owner")),
		"no organization at all":                       {"sub": "u-y", "email": "y@example.com", "email_verified": true},
		"a member of no workspace of the organization": member("u-z", "z@example.com", inOrg("org_acme", "member")),
		"a workspace Geass does not declare":           member("u-w", "w@example.com", inOrg("org_acme", "member", "team_unknown")),
	} {
		f.idp.mu.Lock()
		f.idp.person = person
		f.idp.mu.Unlock()
		rec := f.signIn(t)
		require.Equal(t, oidcauth.DeniedNoOrg, failureOf(t, rec), name)
		require.Nil(t, cookieNamed(rec, testSessionCookie), "%s: no session was started", name)
	}

	suspended := f.signedInAs(t, member("u-ok", "ok@example.com", inOrg("org_acme", "owner")))
	require.True(t, f.authenticated(t, suspended))
	f.updateConfigOf(t, orgA, func(o *geassv1alpha1.GeassOrganization) { o.Spec.Suspended = true })
	f.idp.mu.Lock()
	f.idp.person = member("u-ok", "ok@example.com", inOrg("org_acme", "owner"))
	f.idp.mu.Unlock()
	require.Equal(t, oidcauth.DeniedNoOrg, failureOf(t, f.signIn(t)), "nobody signs in to an organization that is suspended")
}

func (f tenancyFixture) updateConfigOf(t *testing.T, name string, change func(*geassv1alpha1.GeassOrganization)) {
	t.Helper()
	var org geassv1alpha1.GeassOrganization
	require.NoError(t, f.srv.Client.Get(asSystem(context.Background()), key(name), &org))
	change(&org)
	require.NoError(t, f.srv.Client.Update(asSystem(context.Background()), &org))
}

func TestAPlatformAdministratorStartsAsThePlatformAndMayGoIntoAnOrganization(t *testing.T) {
	f := newTenancyFixture(t)
	person := member("u-root", "root@example.com")
	person["geass_platform"] = "operator"

	cookie := f.signedInAs(t, person)

	session := f.session(t, cookie)
	require.Equal(t, true, session["platformAdmin"])
	require.Empty(t, session["org"], "they start as the platform")
	// Everything is theirs to see, the apps the base fixture holds that belong to no organization among them.
	require.Subset(t, f.visibleApps(t, cookie), []string{"app-a0", "app-a1", "app-a2", "app-b1", "api-prod", "other-app"})
	settings := f.do(browserRequest(http.MethodGet, "/api/settings/auth", cookie))
	require.Equal(t, http.StatusOK, settings.Code, "the platform's own pages are theirs")
	scopes, _ := session["scopes"].([]any)
	require.Len(t, scopes, 1+1+2+1, "the platform, and each organization as a whole and by workspace")

	switched := f.switchScope(t, cookie, orgB, "")
	require.Equal(t, http.StatusOK, switched.Code, switched.Body.String())
	inside := cookieNamed(switched, testSessionCookie)
	require.Equal(t, []string{"app-b1"}, f.visibleApps(t, inside), "in an organization they see what it sees")
	require.Equal(t, http.StatusForbidden, f.do(browserRequest(http.MethodGet, "/api/settings/auth", inside)).Code, "and not the platform's pages")

	back := f.switchScope(t, inside, "", "")
	require.Equal(t, http.StatusOK, back.Code, back.Body.String())
	require.Subset(t, f.visibleApps(t, cookieNamed(back, testSessionCookie)), []string{"app-a0", "app-b1", "api-prod"})
}

func (f tenancyFixture) switchScope(t *testing.T, cookie *http.Cookie, org, workspace string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"org": {org}, "workspace": {workspace}}
	req := withSession(httptest.NewRequest(http.MethodPost, "/api/session/scope", strings.NewReader(form.Encode())))
	req.AddCookie(cookie)
	return f.do(req)
}

func TestAPersonChoosesWhereToWorkAmongTheirOwnPlacesOnly(t *testing.T) {
	f := newTenancyFixture(t)
	cookie := f.signedInAs(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner"), inOrg("org_beta", "member", "team_x")))

	rec := f.switchScope(t, cookie, orgA, wsA2)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	moved := cookieNamed(rec, testSessionCookie)
	require.Equal(t, []string{"app-a2"}, f.visibleApps(t, moved), "now in the second workspace")
	require.Equal(t, wsA2, f.session(t, moved)["workspace"])

	for name, target := range map[string][2]string{
		"an organization they are not in":      {"org-nobody", ""},
		"a workspace of theirs that is gone":   {orgA, "ws-ffffff"},
		"a workspace without its organization": {"", wsA1},
		"the platform":                         {"", ""},
	} {
		rec := f.switchScope(t, moved, target[0], target[1])
		require.GreaterOrEqual(t, rec.Code, 400, "%s: %s", name, rec.Body.String())
		require.Nil(t, cookieNamed(rec, testSessionCookie), "%s: the cookie is left as it was", name)
	}

	// An organization in which they are only a member of a workspace Geass declares nowhere is not theirs to choose.
	require.GreaterOrEqual(t, f.switchScope(t, moved, orgB, "").Code, 400, "all of an organization is for its administrators")
}

func TestChangingWhereYouWorkNeitherExtendsTheSessionNorOutlivesASignOut(t *testing.T) {
	f := newTenancyFixture(t)
	person := member("u-ada", "ada@example.com", inOrg("org_acme", "owner"))
	cookie := f.signedInAs(t, person)
	auth, err := f.srv.dashboardAuth(browserRequest(http.MethodGet, "/", cookie))
	require.NoError(t, err)
	before, ok := parseSSOSession(auth.sessionKey, cookie.Value, time.Now())
	require.True(t, ok)

	rec := f.switchScope(t, cookie, orgA, wsA1)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	moved := cookieNamed(rec, testSessionCookie)
	after, ok := parseSSOSession(auth.sessionKey, moved.Value, time.Now())
	require.True(t, ok)
	require.Equal(t, before.Expires, after.Expires, "the session ends when it was going to")
	require.Equal(t, before.IssuedMillis, after.IssuedMillis, "and began when it did, so a sign-out after it still ends it")
	require.Equal(t, before.Identity, after.Identity)

	// The provider says the person signed out: the session they switched, and the one before it, both end.
	rec = f.backchannel(f.idp.logoutToken("u-ada", nil), "203.0.113.9:1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.False(t, f.authenticated(t, moved))
	require.False(t, f.authenticated(t, cookie))
}

func TestACookieThatSaysWhereYouMayWorkCannotBeEdited(t *testing.T) {
	f := newTenancyFixture(t)
	cookie := f.signedInAs(t, member("u-bob", "bob@example.com", inOrg("org_acme", "member", "team_1")))
	encoded, _, found := strings.Cut(cookie.Value, ".")
	require.True(t, found)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	parts := strings.Split(string(payload), ":")
	require.Len(t, parts, ssoSessionTenantFields)

	// A member who rewrites the cookie to administer the organization, or to work in another, is not believed.
	for name, edit := range map[string]func([]string){
		"the organization": func(p []string) { p[6] = encodeSessionField(orgB) },
		"the workspace":    func(p []string) { p[7] = encodeSessionField("") },
		"the platform":     func(p []string) { p[8] = "1" },
		"the grants":       func(p []string) { p[9] = encodeSessionField(encodeGrants([]sessionGrant{{Org: orgB, Admin: true}})) },
		"the role":         func(p []string) { p[3] = encodeSessionField("admin") },
	} {
		forged := append([]string(nil), parts...)
		edit(forged)
		tampered := base64.RawURLEncoding.EncodeToString([]byte(strings.Join(forged, ":"))) + "." + strings.SplitN(cookie.Value, ".", 2)[1]
		require.False(t, f.authenticated(t, &http.Cookie{Name: cookie.Name, Value: tampered}), name)
	}
}

func TestAnOrganizationThatIsSuspendedOrRemovedStopsAtOnceForSessionsAlreadyOpen(t *testing.T) {
	f := newTenancyFixture(t)
	cookie := f.signedInAs(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner")))
	require.Len(t, f.visibleApps(t, cookie), 3)

	f.updateConfigOf(t, orgA, func(o *geassv1alpha1.GeassOrganization) { o.Spec.Suspended = true })
	require.Empty(t, f.visibleApps(t, cookie), "a suspended organization sees nothing, without waiting for the cookie to run out")

	f.updateConfigOf(t, orgA, func(o *geassv1alpha1.GeassOrganization) { o.Spec.Suspended = false })
	require.Len(t, f.visibleApps(t, cookie), 3, "and is back when it is open again")

	f.updateConfigOf(t, orgA, func(o *geassv1alpha1.GeassOrganization) {
		o.Spec.Workspaces = o.Spec.Workspaces[:1]
	})
	moved := cookieNamed(f.switchScope(t, cookie, orgA, wsA1), testSessionCookie)
	f.updateConfigOf(t, orgA, func(o *geassv1alpha1.GeassOrganization) { o.Spec.Workspaces = nil })
	require.Empty(t, f.visibleApps(t, moved), "a workspace that is removed has nothing to show")

	require.NoError(t, f.srv.Client.Delete(asSystem(context.Background()), matrixOrg(orgA, "", "")))
	require.Empty(t, f.visibleApps(t, cookie), "nor does an organization that is deleted")
}

func TestWithOrganizationsOffAPersonFromTheProviderSeesEverythingAsBefore(t *testing.T) {
	f := newTenancyFixture(t)
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.srv.Client.Get(asSystem(context.Background()), key(platform.HAReadinessName), &config))
	config.Spec.Auth.Tenancy.Enabled = false
	require.NoError(t, f.srv.Client.Update(asSystem(context.Background()), &config))

	cookie := f.signedInAs(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner")))

	require.Empty(t, f.session(t, cookie)["org"], "no place is chosen while organizations are off")
	require.Subset(t, f.visibleApps(t, cookie), []string{"app-a0", "app-a1", "app-a2", "app-b1", "api-prod"}, "everything is seen, as it was before organizations existed")
	require.Equal(t, http.StatusBadRequest, f.switchScope(t, cookie, orgA, "").Code, "and there is nowhere to switch to")
}

func TestTheCookieOfAPersonWithoutOrganizationsIsTheOneItAlwaysWas(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	token, err := issueSSOSession(key, ssoSessionClaims{Email: "a@example.com", Role: "admin", Identity: "sso:1", IssuedMillis: 5}, time.Hour, time.Now())
	require.NoError(t, err)
	encoded, _, _ := strings.Cut(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	require.Len(t, strings.Split(string(payload), ":"), ssoSessionFields, "nothing is added to a cookie that has no organization to carry")
	claims, ok := parseSSOSession(key, token, time.Now())
	require.True(t, ok)
	require.False(t, claims.hasTenancy())
}

func TestGrantsAreWhatTheTokenAndTheSetUpOrganizationsAgreeOn(t *testing.T) {
	suspended := matrixOrg("org-s", "Suspended", "idp_s")
	suspended.Spec.Suspended = true
	orgs := []geassv1alpha1.GeassOrganization{
		*matrixOrg("org-b", "Beta", "idp_b", geassv1alpha1.GeassOrganizationWorkspace{ID: "ws-bbbbbb", DisplayName: "B", IdPID: "tb"}),
		*matrixOrg("org-a", "Acme", "idp_a", geassv1alpha1.GeassOrganizationWorkspace{ID: "ws-aaaaaa", DisplayName: "A", IdPID: "ta"},
			geassv1alpha1.GeassOrganizationWorkspace{ID: "ws-aaaaab", DisplayName: "A2", IdPID: "ta2"}),
		*matrixOrg("org-c", "Gamma", "idp_c"),
		*suspended,
	}

	grants := grantsOf([]oidcauth.Membership{
		{Org: "idp_a", Role: oidcauth.RoleViewer, Workspaces: []oidcauth.WorkspaceMembership{{ID: "ta2", Role: oidcauth.RoleAdmin}, {ID: "ta"}, {ID: "unknown"}}},
		{Org: "idp_b", Role: oidcauth.RoleAdmin},
		{Org: "idp_c", Role: oidcauth.RoleViewer},
		{Org: "idp_s", Role: oidcauth.RoleAdmin},
		{Org: "idp_unknown", Role: oidcauth.RoleAdmin},
	}, orgs)
	require.Equal(t, []sessionGrant{
		{Org: "org-a", Workspaces: []workspaceGrant{{ID: "ws-aaaaaa"}, {ID: "ws-aaaaab", Role: "admin"}}},
		{Org: "org-b", Admin: true},
	}, grants, "in Geass's names, sorted, without the organization that is suspended, unknown, or that a member has no way into")

	require.Equal(t, "admin", must(roleIn(grants, "org-b", "")))
	require.Equal(t, "admin", must(roleIn(grants, "org-b", "ws-bbbbbb")), "an administrator works in any workspace")
	require.Equal(t, "viewer", must(roleIn(grants, "org-a", "ws-aaaaaa")))
	require.Equal(t, "admin", must(roleIn(grants, "org-a", "ws-aaaaab")), "the higher of the two roles")
	for _, target := range [][2]string{{"org-a", ""}, {"org-a", "ws-zzzzzz"}, {"org-x", ""}, {"org-c", ""}} {
		_, ok := roleIn(grants, target[0], target[1])
		require.False(t, ok, "%v", target)
	}

	round, ok := decodeGrants(encodeGrants(grants))
	require.True(t, ok)
	require.Equal(t, grants, round)
	for _, bad := range []string{"org-a", "org-a~x~", "org-a~a~ws:z", "~a~", "org-a~a~:a", "org-a~a~ws-1"} {
		_, ok := decodeGrants(bad)
		require.False(t, ok, bad)
	}
	require.Equal(t, hex.EncodeToString([]byte("")), "")
}

func must(role string, _ bool) string { return role }

var _ = metav1.ObjectMeta{}

func TestTheCallerIsToldWhereTheyWork(t *testing.T) {
	f := newTenancyFixture(t)
	me := func(cookie *http.Cookie) map[string]any {
		rec := f.do(browserRequest(http.MethodGet, "/api/v1/me", cookie))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return decodeBody(t, rec)
	}

	admin := me(f.signedInAs(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner"))))
	require.Equal(t, orgA, admin["org"])
	require.Equal(t, "Acme", admin["orgName"], "by the name it is shown under, not only by its id")
	require.Nil(t, admin["workspace"], "all of the organization")

	inWorkspace := me(f.signedInAs(t, member("u-bob", "bob@example.com", inOrg("org_acme", "member", "team_1"))))
	require.Equal(t, wsA1, inWorkspace["workspace"])
	require.Equal(t, "First", inWorkspace["workspaceName"])

	root := member("u-root", "root@example.com")
	root["geass_platform"] = "operator"
	platformAdmin := me(f.signedInAs(t, root))
	require.Equal(t, true, platformAdmin["platformAdmin"])
	require.Nil(t, platformAdmin["org"])
}

func TestWithOrganizationsOffTheCallerIsNotToldAPlace(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	rec := f.do(browserRequest(http.MethodGet, "/api/v1/me", f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))))
	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	for _, field := range []string{"org", "orgName", "workspace", "platformAdmin"} {
		require.NotContains(t, body, field, "the answer is the one it always was")
	}
}
