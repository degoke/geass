package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func requestFrom(peer string, forwarded ...string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	req.RemoteAddr = peer
	for _, line := range forwarded {
		req.Header.Add("X-Forwarded-For", line)
	}
	return req
}

func TestTheClientBehindAProxyIsReadFromTheRightOfForwardedFor(t *testing.T) {
	for name, tc := range map[string]struct {
		peer      string
		forwarded []string
		want      string
	}{
		"a public peer is itself, whatever it says":    {"203.0.113.5:4000", []string{"198.51.100.9"}, "203.0.113.5"},
		"a private peer with no header is itself":      {"10.42.0.7:4000", nil, "10.42.0.7"},
		"a private peer says who it is for":            {"10.42.0.7:4000", []string{"203.0.113.9"}, "203.0.113.9"},
		"the right-most public address is believed":    {"10.42.0.7:4000", []string{"198.51.100.1, 203.0.113.9"}, "203.0.113.9"},
		"what a client wrote first is not believed":    {"10.42.0.7:4000", []string{"6.6.6.6, 203.0.113.9, 10.42.0.9"}, "203.0.113.9"},
		"proxies in the chain are skipped":             {"10.42.0.7:4000", []string{"203.0.113.9, 10.42.0.3, 100.64.1.1, 169.254.1.1"}, "203.0.113.9"},
		"several header lines are read as one":         {"10.42.0.7:4000", []string{"6.6.6.6", "203.0.113.9"}, "203.0.113.9"},
		"a port is dropped":                            {"10.42.0.7:4000", []string{"203.0.113.9:51000"}, "203.0.113.9"},
		"an IPv6 client":                               {"10.42.0.7:4000", []string{"2001:db8::7"}, "2001:db8::7"},
		"an IPv4-mapped client":                        {"10.42.0.7:4000", []string{"::ffff:203.0.113.9"}, "203.0.113.9"},
		"nothing but private addresses":                {"10.42.0.7:4000", []string{"10.0.0.1, 192.168.1.1"}, "10.42.0.7"},
		"an entry that cannot be read spoils the rest": {"10.42.0.7:4000", []string{"203.0.113.9, not-an-address"}, "10.42.0.7"},
		"an empty entry spoils the rest":               {"10.42.0.7:4000", []string{"203.0.113.9,"}, "10.42.0.7"},
		"through this machine":                         {"127.0.0.1:4000", []string{"203.0.113.9"}, "203.0.113.9"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, clientIP(requestFrom(tc.peer, tc.forwarded...)))
		})
	}
}

func TestBehindAProxyEachClientHasItsOwnAllowanceAndLock(t *testing.T) {
	require.Equal(t, "203.0.113.9", loginSourceKey(requestFrom("10.42.0.7:4000", "203.0.113.9")))
	require.Equal(t, "", loginSourceKey(requestFrom("10.42.0.7:4000")), "a private peer with nothing said cannot be told apart")
	require.Equal(t, "2001:db8:0:1::/64", loginSourceKey(requestFrom("10.42.0.7:4000", "2001:db8:0:1::7")))
}

func loginFrom(srv *Server, peer, password string, forwarded ...string) *httptest.ResponseRecorder {
	return loginAs(srv, "admin", peer, password, forwarded...)
}

func loginAs(srv *Server, username, peer, password string, forwarded ...string) *httptest.ResponseRecorder {
	form := "username=" + username + "&password=" + password
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.RemoteAddr = peer
	for _, line := range forwarded {
		req.Header.Add("X-Forwarded-For", line)
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	mux.ServeHTTP(rec, req)
	return rec
}

func adminServer() *Server {
	return &Server{Client: newFakeClient(dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin}))}
}

func TestAClientThatKeepsGuessingIsShutOutButNotTheOwnerOfTheAccount(t *testing.T) {
	srv := adminServer()
	for range loginFailureLimit {
		require.Equal(t, http.StatusBadRequest, loginFrom(srv, "203.0.113.10:1000", "guess").Code)
	}

	require.Equal(t, http.StatusBadRequest, loginFrom(srv, "203.0.113.10:1000", authFixtureAdminCred).Code, "the guesser is shut out, even with the right password")
	owner := loginFrom(srv, "203.0.113.20:1000", authFixtureAdminCred)
	require.Equal(t, http.StatusOK, owner.Code, "an attacker who keeps failing at one address locked the account for the person who owns it")
}

func TestAnAttackerBehindTheSameProxyAsTheOwnerDoesNotLockTheOwnerOut(t *testing.T) {
	srv := adminServer()
	for range loginFailureLimit {
		require.Equal(t, http.StatusBadRequest, loginFrom(srv, "10.42.0.7:1000", "guess", "203.0.113.10").Code)
	}

	require.Equal(t, http.StatusBadRequest, loginFrom(srv, "10.42.0.7:1000", authFixtureAdminCred, "203.0.113.10").Code)
	require.Equal(t, http.StatusOK, loginFrom(srv, "10.42.0.7:1000", authFixtureAdminCred, "203.0.113.20").Code, "through the proxy, the owner was locked out by someone else's failures")
}

func TestManyClientsGuessingTogetherStillLockTheAccount(t *testing.T) {
	srv := adminServer()
	for i := range loginUserFailureLimit {
		require.Equal(t, http.StatusBadRequest, loginFrom(srv, fmt.Sprintf("203.0.113.%d:1000", i+1), "guess").Code)
	}

	require.Equal(t, http.StatusBadRequest, loginFrom(srv, "198.51.100.77:1000", authFixtureAdminCred).Code, "25 failures from 25 clients did not lock the account")
}

func TestClientsThatCannotBeToldApartShareTheLockOnTheAccount(t *testing.T) {
	srv := adminServer()
	for range loginFailureLimit {
		require.Equal(t, http.StatusBadRequest, loginFrom(srv, "10.42.0.7:1000", "guess").Code)
	}

	require.Equal(t, http.StatusBadRequest, loginFrom(srv, "10.42.0.7:1000", authFixtureAdminCred).Code, "with nothing to tell clients apart, the account is locked for all")
}

func TestAClientThatSucceedsStartsItsAllowanceOver(t *testing.T) {
	srv := adminServer()
	for range loginFailureLimit - 1 {
		require.Equal(t, http.StatusBadRequest, loginFrom(srv, "203.0.113.10:1000", "guess").Code)
	}
	require.Equal(t, http.StatusOK, loginFrom(srv, "203.0.113.10:1000", authFixtureAdminCred).Code)
	for range loginFailureLimit - 1 {
		require.Equal(t, http.StatusBadRequest, loginFrom(srv, "203.0.113.10:1000", "guess").Code)
	}

	require.Equal(t, http.StatusOK, loginFrom(srv, "203.0.113.10:1000", authFixtureAdminCred).Code, "failures from before a success still counted")
}

func TestOneClientBehindAProxyIsShutOutByTheSourceLimitAndAnotherIsNot(t *testing.T) {
	srv := adminServer()
	// Guesses at names that are not users: only the client is counted, not an account, so nothing else shuts it out first.
	for i := range loginSourceFailureLimit {
		loginAs(srv, fmt.Sprintf("ghost-%d", i), "10.42.0.7:1000", "guess", "203.0.113.10")
	}
	require.True(t, srv.loginSourceBlocked("203.0.113.10"), "a client that failed too often behind a proxy was not blocked")
	require.False(t, srv.loginSourceBlocked("203.0.113.11"))
}

func TestTheDeviceSignInLimitIsPerClientBehindAProxy(t *testing.T) {
	srv := adminServer()
	start := func(forwarded string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device", nil)
		req.RemoteAddr = "10.42.0.7:1000"
		req.Header.Set("X-Forwarded-For", forwarded)
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		srv.handleDeviceStart(rec, req)
		return rec.Code
	}
	for range deviceStartsPerMinute {
		require.Equal(t, http.StatusOK, start("203.0.113.10"))
	}
	require.Equal(t, http.StatusTooManyRequests, start("203.0.113.10"))
	require.Equal(t, http.StatusOK, start("203.0.113.11"), "one client behind the proxy used up everyone's allowance")
}

// platformReads counts the reads of the platform configuration.
type platformReads struct {
	client.Client
	reads atomic.Int32
}

func (c *platformReads) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*geassv1alpha1.GeassPlatformConfig); ok {
		c.reads.Add(1)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestASignInDoesTheSameReadsWhateverTheNameIs(t *testing.T) {
	f := newTokenFixture(t)
	reads := &platformReads{Client: f.srv.Client}
	f.srv.Client = reads
	attempt := func(username string) int32 {
		reads.reads.Store(0)
		rec := f.do(postForm("/api/login", url.Values{"username": {username}, "password": {"not-the-password"}}))
		require.Equal(t, http.StatusBadRequest, rec.Code, username)
		return reads.reads.Load()
	}

	ghost, viewer, admin := attempt("nobody-by-this-name"), attempt("viewer"), attempt("admin")

	require.Positive(t, ghost, "the mode was not read for a name that does not exist")
	require.Equal(t, ghost, viewer, "the work for an existing viewer differs from the work for a name that does not exist")
	require.Equal(t, ghost, admin, "the work for an admin differs from the work for a name that does not exist")
}

func TestTheLockFunctionsUseTheSourceTheyAreGiven(t *testing.T) {
	srv := adminServer()
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	req.RemoteAddr = "198.51.100.200:1" // not the source under test: the functions must not look at the request for it
	user := &dashboardUser{Username: "admin", Role: dashboardRoleAdmin}
	for range loginFailureLimit {
		require.NoError(t, srv.recordLoginFailure(req, user, "203.0.113.9"))
	}

	locked := func(source string) bool {
		got, err := srv.loginLocked(req, "admin", source)
		require.NoError(t, err)
		return got
	}
	require.True(t, locked("203.0.113.9"), "the client that failed is not locked")
	require.False(t, locked("203.0.113.10"), "another client is locked by the failures of the first")
	require.False(t, locked(""), "a client that cannot be told apart is locked by failures of one that could")

	require.NoError(t, srv.clearLoginFailures(req, "admin", "203.0.113.9"))
	require.False(t, locked("203.0.113.9"), "a success did not clear the client's failures")
}
