package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoutesAreClassifiedByWhoTheyAreFor(t *testing.T) {
	get, post := http.MethodGet, http.MethodPost
	for _, tc := range []struct {
		method, path string
		want         routeScope
	}{
		// The platform's own.
		{post, "/api/settings/domain/save", scopePlatform},
		{get, "/api/settings/auth", scopePlatform},
		{post, "/api/settings/auth/mode", scopePlatform},
		{post, "/api/settings/alerts/channels", scopePlatform},
		{post, "/api/nodes/worker-1/placement", scopePlatform},
		{get, "/api/servers/worker-1/impact", scopePlatform},
		{post, "/api/platform/default-region", scopePlatform},
		{post, "/api/ha-readiness/check", scopePlatform},
		{post, "/api/shared-databases/create", scopePlatform},
		{get, "/api/cluster/metrics", scopePlatform},
		{get, "/api/operator/metrics", scopePlatform},
		{get, "/api/v1/nodes", scopePlatform},
		{post, "/api/templates/create", scopePlatform},
		{post, "/api/templates/web/delete", scopePlatform},
		{post, "/settings/github/manifest/callback", scopePlatform},
		{post, "/api/github/connections/rungeass/save", scopePlatform},
		{post, "/api/github/connections/rungeass/installations/42/token", scopePlatform},
		{get, "/api/v1/github/connections", scopePlatform},
		{get, "/api/v1/github/connections/rungeass/installations", scopePlatform},
		{get, "/api/orgs", scopePlatform},
		{post, "/api/orgs/create", scopePlatform},
		{post, "/api/orgs/org-1/workspaces/create", scopePlatform},
		{post, "/api/settings/auth/tenancy", scopePlatform},
		{post, "/api/session/scope", scopeAny},
		{post, "/api/projects/p1/assign", scopeTenant},
		{get, "/api/object-stores/cluster-s3/logs", scopePlatform},
		{get, "/api/object-stores/cluster-s3/runtime", scopePlatform},
		{post, "/api/object-stores/cluster-s3/console/create", scopePlatform},
		// An organization's.
		{post, "/api/projects/create", scopeTenant},
		{post, "/api/projects/p1/settings/save", scopeTenant},
		{get, "/api/projects/p1/logs", scopeTenant},
		{get, "/api/projects/p1/metrics", scopeTenant},
		{get, "/api/alerts", scopeTenant},
		{post, "/api/alerts/channels/save", scopeTenant},
		{post, "/api/alerts/channels/test", scopeTenant},
		{post, "/api/apps/create", scopeTenant},
		{post, "/api/apps/web/deploy", scopeTenant},
		{get, "/api/apps/web/logs", scopeTenant},
		{get, "/api/apps/web/console/ws", scopeTenant},
		{post, "/api/databases/create", scopeTenant},
		{get, "/api/databases/db/metrics", scopeTenant},
		{post, "/api/logical-databases/create", scopeTenant},
		{post, "/api/object-stores/create", scopeTenant},
		{post, "/api/cloud-connections/create", scopeTenant},
		{get, "/api/tokens", scopeTenant},
		{post, "/api/tokens/create", scopeTenant},
		{post, "/api/token/deploy", scopeTenant},
		{get, "/api/templates", scopeTenant},
		{post, "/api/templates/web/deploy", scopeTenant},
		{get, "/api/v1/projects", scopeTenant},
		{get, "/api/v1/apps/web/status", scopeTenant},
		{get, "/api/v1/databases/db/connection", scopeTenant},
		{post, "/github/callback", scopeTenant},
		{post, "/api/projects/p1/github/install", scopeTenant},
		{get, "/api/projects/p1/github/installations", scopeTenant},
		// Everyone's, answered for their own tenant.
		{get, "/api/bootstrap", scopeAny},
		{get, "/api/events", scopeAny},
		{get, "/api/usage", scopeAny},
		{get, "/api/session", scopeAny},
		{get, "/api/v1/me", scopeAny},
		{get, "/api/v1/version", scopeAny},
		{post, "/api/auth/device/approve", scopeAny},
		{get, "/projects/p1", scopeAny},
		{get, "/assets/app.js", scopeAny},
	} {
		require.Equal(t, tc.want, routeScopeOf(tc.method, tc.path), "%s %s", tc.method, tc.path)
	}
}

func TestARouteNobodyClassifiedIsThePlatformsAlone(t *testing.T) {
	for _, path := range []string{
		"/api/something-new", "/api/", "/api", "/api/v1", "/api/v1/", "/api/v1/something-new", "/api/settings", "/api/templates/web/other",
		"/api/projects-but-not/p1",
	} {
		scope := routeScopeOf(http.MethodPost, path)
		if path == "/api" {
			// Not an API path at all: the page.
			require.Equal(t, scopeAny, scope, path)
			continue
		}
		require.Equal(t, scopePlatform, scope, "%s is out of reach of organizations until someone says otherwise", path)
	}
}

func TestEveryRouteTheV1APIDocumentsIsClassified(t *testing.T) {
	// A v1 operation is turned into a dashboard route before authentication, and the routes that has produced are what
	// is classified; this checks the native ones, which are classified as they are written, and that a documented
	// read of a resource is not left to the platform by accident.
	for _, op := range v1Operations {
		if op.Method != http.MethodGet {
			continue
		}
		path := "/api/v1" + op.Path
		switch path {
		case "/api/v1/nodes", "/api/v1/cluster/metrics", "/api/v1/operator/metrics":
			continue
		case "/api/v1/usage/hours":
			// What every organization reserved, for billing: the platform's (usage_hours_test.go).
			continue
		}
		if op.Tag == tagCompute || op.Tag == tagGitHub {
			// Machines and the clouds they come from belong to the cluster, and so does the GitHub App a provider runs: see
			// the tests below.
			continue
		}
		// Reads that are not native are rewritten to /api/<path>, which is what authentication sees.
		if _, native := (&Server{}).v1Router().Handler(httptest.NewRequest(http.MethodGet, path, nil)); native == "" {
			path = "/api" + op.Path
		}
		scope := routeScopeOf(http.MethodGet, path)
		require.NotEqual(t, scopePlatform, scope, "GET %s is documented for callers of every organization but classified as the platform's", path)
	}
}

// The clouds Geass creates machines at, the pools and the servers they make belong to the cluster, whoever may read them in
// the API's documentation: an organization's people see none of it, and cannot change it.
func TestMachinesAndTheCloudsTheyComeFromAreThePlatformsAlone(t *testing.T) {
	seen := 0
	for _, op := range v1Operations {
		if op.Tag != tagCompute {
			continue
		}
		seen++
		path := "/api/v1" + op.Path
		if _, native := (&Server{}).v1Router().Handler(httptest.NewRequest(op.Method, path, nil)); native == "" {
			path = "/api" + op.Path
		}
		require.Equal(t, scopePlatform, routeScopeOf(op.Method, path), "%s %s", op.Method, path)
	}
	require.NotZero(t, seen, "there are operations for machines")
	for _, path := range []string{
		"/api/connections/create", "/api/connections/hetzner/update", "/api/connections/hetzner/delete", "/api/connections/hetzner/verify",
		"/api/pools/create", "/api/pools/burst/update", "/api/pools/burst/delete", "/api/pools/burst/resume",
		"/api/servers/create", "/api/servers/worker-1/delete", "/api/compute/providers", "/api/compute/sizes",
	} {
		require.Equal(t, scopePlatform, routeScopeOf(http.MethodPost, path), path)
	}
}

// The GitHub App a provider runs for the cluster, its installations and their tokens are the platform's: an organization
// sees only the installations its own projects may connect to, through its projects' routes.
func TestTheGitHubAppOfAProviderIsThePlatformsAlone(t *testing.T) {
	seen := 0
	for _, op := range v1Operations {
		if op.Tag != tagGitHub {
			continue
		}
		seen++
		path := "/api/v1" + op.Path
		if _, native := (&Server{}).v1Router().Handler(httptest.NewRequest(op.Method, path, nil)); native == "" {
			legacy, _, ok := v1LegacyPath(op.Method, strings.Split(strings.Trim(op.Path, "/"), "/"))
			require.True(t, ok, "%s %s maps to a dashboard route", op.Method, op.Path)
			path = legacy
		}
		require.Equal(t, scopePlatform, routeScopeOf(op.Method, path), "%s %s", op.Method, path)
	}
	require.Equal(t, 8, seen)
}
