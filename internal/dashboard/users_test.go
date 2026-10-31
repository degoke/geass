package dashboard

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// signIn signs in through the real login route and returns the session cookie, or nil when the sign-in was refused.
func (f tokenFixture) signIn(t *testing.T, username, password string) *http.Cookie {
	t.Helper()
	rec := f.do(postForm("/api/login", url.Values{"username": {username}, "password": {password}}))
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == testSessionCookie && cookie.MaxAge >= 0 && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

// as sends a request as a session that was made by signing in.
func (f tokenFixture) as(cookie *http.Cookie, req *http.Request) *httptest.ResponseRecorder {
	req.AddCookie(cookie)
	return f.do(req)
}

func (f tokenFixture) storedUsers(t *testing.T) []dashboardUser {
	t.Helper()
	secret := &corev1.Secret{}
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret))
	var users []dashboardUser
	require.NoError(t, json.Unmarshal(secret.Data["users"], &users))
	return users
}

func userForm(pairs ...string) url.Values {
	form := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		form.Set(pairs[i], pairs[i+1])
	}
	return form
}

func TestUsersAreListedForAdminsWithoutPasswords(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")
	require.NotNil(t, admin)

	rec := f.as(admin, httptest.NewRequest(http.MethodGet, "/api/users", nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.NotContains(t, rec.Body.String(), "password")
	require.NotContains(t, rec.Body.String(), "$2a$")
	body := decodeBody(t, rec)
	require.Equal(t, []any{
		map[string]any{"username": "admin", "role": "admin", "current": true},
		map[string]any{"username": "viewer", "role": "viewer"},
	}, body["items"])
}

func TestOnlyAnAdminSessionManagesUsers(t *testing.T) {
	f := newTokenFixture(t)
	viewer := f.signIn(t, "viewer", "test-password")
	require.NotNil(t, viewer)
	observer := f.create(t, url.Values{"name": {"watch"}, "scope": {"workspace"}, "role": {"observer"}})

	for name, send := range map[string]func(*http.Request) *httptest.ResponseRecorder{
		"a viewer": func(r *http.Request) *httptest.ResponseRecorder { return f.as(viewer, r) },
		"an observer's token": func(r *http.Request) *httptest.ResponseRecorder {
			r.Header.Set("Authorization", "Bearer "+observer)
			return f.do(r)
		},
		"nobody signed in": f.do,
	} {
		t.Run(name, func(t *testing.T) {
			require.GreaterOrEqual(t, send(httptest.NewRequest(http.MethodGet, "/api/users", nil)).Code, http.StatusUnauthorized)
			for _, path := range []string{"create", "role", "password", "remove", "sign-out-all"} {
				rec := send(postForm("/api/users/"+path, userForm("username", "viewer", "role", "admin")))
				require.GreaterOrEqual(t, rec.Code, http.StatusUnauthorized, path)
				require.Less(t, rec.Code, http.StatusInternalServerError, path)
			}
		})
	}
	require.Len(t, f.storedUsers(t), 2, "nothing changed")
}

func TestAnAdminMakesAUserWithAGeneratedPasswordShownOnce(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")

	rec := f.as(admin, postForm("/api/users/create", userForm("username", "Mara", "role", "viewer")))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	body := decodeBody(t, rec)
	password, _ := body["password"].(string)
	require.Regexp(t, `^[A-HJ-NP-Z2-9]{5}(-[A-HJ-NP-Z2-9]{5}){3}$`, password)

	stored := f.storedUsers(t)
	require.Len(t, stored, 3)
	require.Equal(t, "Mara", stored[2].Username)
	require.Equal(t, dashboardRoleViewer, stored[2].Role)
	require.True(t, isBcryptHash(stored[2].Password), "the Secret holds a hash")
	require.NotContains(t, stored[2].Password, password)

	require.NotNil(t, f.signIn(t, "mara", password), "the password works, whatever the case of the name")
	list := f.as(admin, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	require.NotContains(t, list.Body.String(), password)
}

func TestAnAdminMakesAUserWithAPasswordOfTheirOwn(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")

	rec := f.as(admin, postForm("/api/users/create", userForm("username", "ops", "role", "admin", "password", "a-long-enough-one")))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, decodeBody(t, rec), "password", "a password the admin typed is not sent back")
	require.NotNil(t, f.signIn(t, "ops", "a-long-enough-one"))
}

func TestANewUserIsRefusedWhenItsDetailsAreWrong(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")
	cases := map[string]struct {
		form   url.Values
		status int
		error  string
	}{
		"a name in use, in any case": {userForm("username", "VIEWER"), http.StatusConflict, "already exists"},
		"no name":                    {userForm("role", "viewer"), http.StatusBadRequest, "username is required"},
		"a slash in the name":        {userForm("username", "a/b"), http.StatusBadRequest, "may use letters"},
		"a long name":                {userForm("username", strings.Repeat("a", 65)), http.StatusBadRequest, "64 characters"},
		"a role that tokens have":    {userForm("username", "x1", "role", "observer"), http.StatusBadRequest, "admin or viewer"},
		"a short password":           {userForm("username", "x1", "password", "short"), http.StatusBadRequest, "at least 12"},
		"a long password":            {userForm("username", "x1", "password", strings.Repeat("a", 73)), http.StatusBadRequest, "72 bytes"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := f.as(admin, postForm("/api/users/create", tc.form))
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.error)
		})
	}
	require.Len(t, f.storedUsers(t), 2)
}

func TestTheRoleOfAUserChangesAtOnceAndTheLastAdminStays(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")
	viewer := f.signIn(t, "viewer", "test-password")

	require.Equal(t, http.StatusForbidden, f.as(viewer, postForm("/api/users/role", userForm("username", "viewer", "role", "admin"))).Code)

	rec := f.as(admin, postForm("/api/users/role", userForm("username", "viewer", "role", "admin")))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, http.StatusOK, f.as(viewer, httptest.NewRequest(http.MethodGet, "/api/users", nil)).Code,
		"the person who was a viewer is an admin on their next request, without signing in again")

	rec = f.as(admin, postForm("/api/users/role", userForm("username", "viewer", "role", "viewer")))
	require.Equal(t, http.StatusOK, rec.Code, "with another admin, one may be made a viewer")
	rec = f.as(admin, postForm("/api/users/role", userForm("username", "admin", "role", "viewer")))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "last admin")
	require.Equal(t, dashboardRoleAdmin, f.storedUsers(t)[0].Role)

	require.Equal(t, http.StatusBadRequest, f.as(admin, postForm("/api/users/role", userForm("username", "viewer", "role", "owner"))).Code)
	require.Equal(t, http.StatusBadRequest, f.as(admin, postForm("/api/users/role", userForm("username", "viewer"))).Code)
	require.Equal(t, http.StatusNotFound, f.as(admin, postForm("/api/users/role", userForm("username", "ghost", "role", "admin"))).Code)
}

func TestResettingAPasswordSignsTheUserOut(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")
	viewer := f.signIn(t, "viewer", "test-password")
	require.Equal(t, http.StatusOK, f.as(viewer, httptest.NewRequest(http.MethodGet, "/api/session", nil)).Code)

	rec := f.as(admin, postForm("/api/users/password", userForm("username", "Viewer")))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	fresh, _ := decodeBody(t, rec)["password"].(string)
	require.NotEmpty(t, fresh)
	require.Contains(t, f.as(viewer, httptest.NewRequest(http.MethodGet, "/api/session", nil)).Body.String(), `"authenticated":false`,
		"the session from before the reset stopped counting")
	require.Nil(t, f.signIn(t, "viewer", "test-password"), "the old password no longer works")
	require.NotNil(t, f.signIn(t, "viewer", fresh))
	require.Equal(t, http.StatusOK, f.as(admin, httptest.NewRequest(http.MethodGet, "/api/users", nil)).Code, "another user's session is not touched")

	require.Equal(t, http.StatusBadRequest, f.as(admin, postForm("/api/users/password", userForm("username", "viewer", "password", "short"))).Code)
	require.Equal(t, http.StatusNotFound, f.as(admin, postForm("/api/users/password", userForm("username", "ghost"))).Code)
}

func TestRemovingAUserSignsThemOutAndKeepsTheLastAdmin(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")
	viewer := f.signIn(t, "viewer", "test-password")

	rec := f.as(admin, postForm("/api/users/remove", userForm("username", "viewer")))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, f.as(viewer, httptest.NewRequest(http.MethodGet, "/api/session", nil)).Body.String(), `"authenticated":false`)
	require.Nil(t, f.signIn(t, "viewer", "test-password"))
	require.Len(t, f.storedUsers(t), 1)

	rec = f.as(admin, postForm("/api/users/remove", userForm("username", "admin")))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "last admin")
	require.Equal(t, http.StatusNotFound, f.as(admin, postForm("/api/users/remove", userForm("username", "viewer"))).Code)

	// A user added again under the same name does not get back the sessions of the one who was removed.
	again := f.as(admin, postForm("/api/users/create", userForm("username", "viewer", "password", "another-long-password")))
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	require.Contains(t, f.as(viewer, httptest.NewRequest(http.MethodGet, "/api/session", nil)).Body.String(), `"authenticated":false`)
}

func TestSigningEveryoneOutEndsSessionsAndLeavesTokens(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")
	admin := f.signIn(t, "admin", "test-password")
	viewer := f.signIn(t, "viewer", "test-password")

	rec := f.as(admin, postForm("/api/users/sign-out-all", nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var cleared bool
	for _, cookie := range rec.Result().Cookies() {
		cleared = cleared || (cookie.Name == testSessionCookie && cookie.MaxAge < 0)
	}
	require.True(t, cleared, "the asker's cookie is cleared")
	for _, cookie := range []*http.Cookie{admin, viewer} {
		require.Contains(t, f.as(cookie, httptest.NewRequest(http.MethodGet, "/api/session", nil)).Body.String(), `"authenticated":false`)
	}
	require.NotNil(t, f.signIn(t, "admin", "test-password"), "signing in again works")

	req := jsonRequest(token, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, f.do(req).Code, "an API token is not a session")
}

func TestASessionKeyFromTheEnvironmentCannotBeRotatedHere(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")
	t.Setenv("GEASS_DASHBOARD_SESSION_KEY", "0123456789abcdef0123456789abcdef")

	rec := f.as(admin, postForm("/api/users/sign-out-all", nil))

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "GEASS_DASHBOARD_SESSION_KEY")
}

func TestUsersAreManagedThroughTheV1APIWithAnAdminToken(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec := f.do(jsonRequest(token, http.MethodPost, "/api/v1/users", map[string]any{"username": "ci-bot", "role": "viewer"}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	password, _ := decodeBody(t, rec)["password"].(string)
	require.NotEmpty(t, password)

	rec = f.do(jsonRequest(token, http.MethodGet, "/api/v1/users", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"ci-bot"`)

	rec = f.do(jsonRequest(token, http.MethodPost, "/api/v1/users/ci-bot/password", map[string]any{"password": "a-password-of-my-own"}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, f.signIn(t, "ci-bot", "a-password-of-my-own"))
	rec = f.do(jsonRequest(token, http.MethodDelete, "/api/v1/users/ci-bot", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, f.signIn(t, "ci-bot", "a-password-of-my-own"))

	rec = f.do(jsonRequest(token, http.MethodDelete, "/api/v1/users/admin", nil))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	_, code := decodeError(t, rec)
	require.Equal(t, "conflict", code)
}

// A token cannot mint tokens (requireTokenAdmin), so it must not be able to make a password login that can: it manages viewers.
func TestATokenCannotMakeAnAdminOrTakeOverOne(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")
	admin := f.signIn(t, "admin", "test-password")
	require.NotNil(t, admin)

	for name, req := range map[string]*http.Request{
		"make an admin":                  jsonRequest(token, http.MethodPost, "/api/v1/users", map[string]any{"username": "rogue", "role": "admin", "password": "a-password-of-my-own"}),
		"make a viewer an admin":         jsonRequest(token, http.MethodPost, "/api/v1/users/viewer/role", map[string]any{"role": "admin"}),
		"reset the password of an admin": jsonRequest(token, http.MethodPost, "/api/v1/users/admin/password", map[string]any{"password": "a-password-of-my-own"}),
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(req)
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "viewers only")
		})
	}
	require.Len(t, f.storedUsers(t), 2, "nothing was made")
	require.Equal(t, dashboardRoleViewer, f.storedUsers(t)[1].Role)
	require.NotNil(t, f.signIn(t, "admin", "test-password"), "the admin's password is as it was")

	// A signed-in admin may do all three.
	require.Equal(t, http.StatusOK, f.as(admin, postForm("/api/users/create", userForm("username", "second", "role", "admin", "password", "a-password-of-my-own"))).Code)
	require.Equal(t, http.StatusOK, f.as(admin, postForm("/api/users/password", userForm("username", "second", "password", "another-password-of-mine"))).Code)
}

// An entry that Geass ignores when it reads the Secret is not the dashboard's to delete.
func TestAnIgnoredEntryOfTheUsersSecretSurvivesAChange(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")
	secret := &corev1.Secret{}
	key := client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}
	require.NoError(t, f.srv.Client.Get(context.Background(), key, secret))
	users := make([]map[string]string, 0, 3)
	require.NoError(t, json.Unmarshal(secret.Data["users"], &users))
	users = append(users, map[string]string{"username": "typo", "password": "x-long-enough-password", "role": "owner"})
	secret.Data["users"], _ = json.Marshal(users)
	require.NoError(t, f.srv.Client.Update(context.Background(), secret))

	require.Equal(t, http.StatusOK, f.as(admin, postForm("/api/users/create", userForm("username", "kept", "password", "kept-long-password"))).Code)

	names := make([]string, 0, 4)
	for _, user := range f.storedUsers(t) {
		names = append(names, user.Username)
	}
	require.ElementsMatch(t, []string{"admin", "viewer", "kept", "typo"}, names)
	require.NotContains(t, f.as(admin, httptest.NewRequest(http.MethodGet, "/api/users", nil)).Body.String(), "typo", "Geass still ignores it")
}

func TestAPasswordIsNeverAcceptedInTheAddress(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec := f.do(jsonRequest(token, http.MethodPost, "/api/v1/users?username=leak&password=a-password-in-a-url", nil))

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "never in the address")
	require.Len(t, f.storedUsers(t), 2)
}

func TestAProjectTokenCannotManageUsers(t *testing.T) {
	f := newTokenFixture(t)
	project := f.create(t, url.Values{"name": {"ci"}, "scope": {"project"}, "project": {testProjectName}, "environment": {"production"}})

	for _, req := range []*http.Request{
		jsonRequest(project, http.MethodGet, "/api/v1/users", nil),
		jsonRequest(project, http.MethodPost, "/api/v1/users", map[string]any{"username": "x1"}),
		jsonRequest(project, http.MethodPost, "/api/v1/users/sign-out-all", nil),
	} {
		require.Equal(t, http.StatusForbidden, f.do(req).Code, req.URL.Path)
	}
	require.Len(t, f.storedUsers(t), 2)
}

func TestUsersFromTheEnvironmentAreKeptWhenTheFirstOtherUserIsMade(t *testing.T) {
	t.Setenv("GEASS_DASHBOARD_USERNAME", "root")
	t.Setenv("GEASS_DASHBOARD_PASSWORD", "environment-password")
	srv := &Server{Client: newFakeClient()}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	f := tokenFixture{srv: srv, handler: mux}
	hash, err := bcrypt.GenerateFromPassword([]byte("second-long-password"), bcrypt.MinCost)
	require.NoError(t, err)

	// Nobody has signed in, so the Secret does not exist yet and the only user is the one the environment names.
	err = srv.mutateDashboardUsers(httptest.NewRequest(http.MethodPost, "/", nil), func(users []dashboardUser, _ map[string]int64) ([]dashboardUser, error) {
		return append(users, dashboardUser{Username: "second", Password: string(hash), Role: dashboardRoleAdmin}), nil
	})

	require.NoError(t, err)
	require.NotNil(t, f.signIn(t, "root", "environment-password"), "the user the environment names did not vanish")
	require.NotNil(t, f.signIn(t, "second", "second-long-password"))
	for _, user := range f.storedUsers(t) {
		require.True(t, isBcryptHash(user.Password), "%s is stored as a hash", user.Username)
	}
}

func TestTheUsersSecretKeepsItsShape(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.signIn(t, "admin", "test-password")
	require.Equal(t, http.StatusOK, f.as(admin, postForm("/api/users/create", userForm("username", "kept", "password", "kept-long-password"))).Code)

	secret := &corev1.Secret{}
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret))

	var raw []map[string]string
	require.NoError(t, json.Unmarshal(secret.Data["users"], &raw))
	for _, entry := range raw {
		require.ElementsMatch(t, []string{"username", "password", "role"}, slices.Collect(maps.Keys(entry)))
	}
	require.NotEmpty(t, secret.Data["session-key"], "the session key was not lost")
}

func TestGeneratedPasswordsDiffer(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		password, err := generateDashboardPassword()
		require.NoError(t, err)
		require.Len(t, password, 23)
		require.False(t, seen[password])
		seen[password] = true
	}
}
