package dashboard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/platform"
)

func loginCookie(t *testing.T, f tokenFixture, host string) *http.Cookie {
	t.Helper()
	req := postForm("/api/login", url.Values{"username": {"admin"}, "password": {"test-password"}})
	req.Host = host
	req.Header.Set("Origin", "http://"+host)
	rec := f.do(req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == platform.DashboardSessionCookie || cookie.Name == "__Host-"+platform.DashboardSessionCookie {
			return cookie
		}
	}
	require.FailNow(t, "no session cookie was set")
	return nil
}

func TestTheSessionCookieIsAHostCookieWhereItIsSecure(t *testing.T) {
	f := newTokenFixture(t)
	cookie := loginCookie(t, f, "geass.example.com")

	require.Equal(t, "__Host-geass_session", cookie.Name)
	require.True(t, cookie.Secure, "a __Host- cookie must be Secure, or the browser drops it")
	require.Equal(t, "/", cookie.Path, "a __Host- cookie must be for the whole site")
	require.Empty(t, cookie.Domain, "a __Host- cookie must not have a Domain, which is what keeps it from being shared with sibling subdomains")
	require.True(t, cookie.HttpOnly)
}

func TestOnThisMachineOverHTTPTheSessionCookieKeepsItsPlainName(t *testing.T) {
	f := newTokenFixture(t)
	cookie := loginCookie(t, f, "127.0.0.1:8085")

	require.Equal(t, "geass_session", cookie.Name)
	require.False(t, cookie.Secure, "a Secure cookie is not kept by every browser over http on this machine")
}

func TestASessionCookiePlantedByASiblingSubdomainIsNotRead(t *testing.T) {
	f := newTokenFixture(t)
	real := loginCookie(t, f, "geass.example.com")
	planted := &http.Cookie{Name: platform.DashboardSessionCookie, Value: real.Value}
	session := func(cookies ...*http.Cookie) map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		req.Host = "geass.example.com"
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		return decodeBody(t, f.do(req))
	}

	require.Equal(t, true, session(&http.Cookie{Name: real.Name, Value: real.Value})["authenticated"], "the cookie the dashboard set should work")
	require.Equal(t, false, session(planted)["authenticated"], "a session cookie under the plain name counted where the dashboard sets the __Host- one")

	// A planted cookie beside the real one does not displace it: the dashboard reads the one only it could have set.
	viewer := withDashboardSession(t, f.srv, httptest.NewRequest(http.MethodGet, "/", nil), "viewer", dashboardRoleViewer)
	viewerValue, err := viewer.Cookie(testSessionCookie)
	require.NoError(t, err)
	plantedViewer := &http.Cookie{Name: platform.DashboardSessionCookie, Value: viewerValue.Value}
	body := session(plantedViewer, &http.Cookie{Name: real.Name, Value: real.Value})
	require.Equal(t, "admin", body["role"], "the planted session was read in place of the real one")
}

func TestTheStateCookieIsAHostCookieWhereItIsSecureAndANeighboursIsNotRead(t *testing.T) {
	f := newSSOFixture(t, "external")
	start, state := f.startSignIn(t, oidcLoginPath)
	require.NotNil(t, state)
	require.Equal(t, "__Host-geass_oidc", state.Name)
	require.True(t, state.Secure)
	require.Equal(t, "/", state.Path)
	require.Empty(t, state.Domain)
	require.True(t, state.HttpOnly)

	back, err := url.Parse(f.atProvider(t, start.Header().Get("Location")))
	require.NoError(t, err)
	planted := &http.Cookie{Name: "geass_oidc", Value: state.Value}
	rec := f.do(browserRequest(http.MethodGet, back.RequestURI(), planted))
	require.Equal(t, "state", failureOf(t, rec), "a state cookie under the plain name was accepted")
	require.Nil(t, cookieNamed(rec, testSessionCookie))
}

func TestOnThisMachineTheStateCookieKeepsItsPlainNameAndPath(t *testing.T) {
	f := newSSOFixture(t, "external")
	req := browserRequest(http.MethodGet, oidcLoginPath)
	req.Host = "127.0.0.1:8085"
	rec := f.do(req)

	state := cookieNamed(rec, oidcStateCookie)
	require.NotNil(t, state, "no state cookie was set")
	require.Equal(t, "/auth/oidc", state.Path)
	require.False(t, state.Secure)
}

func TestSigningOutClearsTheHostCookie(t *testing.T) {
	f := newTokenFixture(t)
	cookie := loginCookie(t, f, "geass.example.com")
	out := postForm("/api/logout", nil)
	out.Host = "geass.example.com"
	out.Header.Set("Origin", "http://geass.example.com")
	out.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})

	rec := f.do(out)

	cleared := cookieNamed(rec, "__Host-"+platform.DashboardSessionCookie)
	require.NotNil(t, cleared)
	require.Less(t, cleared.MaxAge, 0, "the cookie was not cleared")
	require.True(t, cleared.Secure, "a __Host- cookie can only be cleared by a Secure one")
	require.Equal(t, "/", cleared.Path)
}
