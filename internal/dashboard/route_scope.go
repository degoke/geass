package dashboard

import "strings"

// routeScope is who a route is for.
type routeScope int

const (
	// scopePlatform is a route for the platform's own administrators: the cluster, its nodes, add-ons, domain, registry,
	// sign-in and organizations. It is what a route is until someone says otherwise, so a route added without a thought
	// is out of reach of every organization and not open to all of them.
	scopePlatform routeScope = iota
	// scopeTenant is a route that works on projects and what is in them. What it reads and writes is limited to the
	// caller's organization by the dashboard's client, not by the route.
	scopeTenant
	// scopeAny is a route every signed-in caller may use, which answers for the caller's own tenant.
	scopeAny
)

// routeScopeOf says who a route is for. It is given the request as authentication sees it: the v1 API's writes and its
// reads that are not its own have already been turned into the dashboard routes they are.
func routeScopeOf(method, path string) routeScope {
	switch path {
	case "/github/callback":
		return scopeTenant
	case "/settings/github/manifest/callback":
		return scopePlatform
	}
	if !strings.HasPrefix(path, "/api/") {
		// The page that is served and its files.
		return scopeAny
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/"), "/"), "/")
	if parts[0] == "v1" {
		return v1RouteScope(parts[1:])
	}
	switch parts[0] {
	case "session", "bootstrap", "events", "usage", "login", "logout", "auth":
		return scopeAny
	case "object-stores":
		// A bucket is a project's. The logs, the pods and the console of an object store are those of the cluster's own
		// server, which no project owns.
		if len(parts) >= 3 && (parts[2] == "logs" || parts[2] == "runtime" || parts[2] == "console") {
			return scopePlatform
		}
		return scopeTenant
	case "token", "tokens", kindProjects, kindApps, "databases", "logical-databases", "cloud-connections", "planetscale", "alerts", "registry":
		// An organization's own alert channels. The platform's are under settings.
		return scopeTenant
	case "templates":
		// The catalog is read by everyone and deployed into one's own project. Saving a template, or deleting one, changes
		// what every organization is offered.
		switch {
		case len(parts) == 1:
			return scopeTenant
		case len(parts) == 3 && parts[2] == formDeploy:
			return scopeTenant
		}
		return scopePlatform
	}
	return scopePlatform
}

// v1RouteScope is routeScopeOf for the v1 API's own reads, which are the ones that reach authentication under /api/v1.
func v1RouteScope(parts []string) routeScope {
	if len(parts) == 0 {
		return scopePlatform
	}
	switch parts[0] {
	case "version", "me", "openapi.json", "auth":
		return scopeAny
	case kindProjects, kindApps, kindDatabases, kindObjectStores, kindLogicalDatabases:
		return scopeTenant
	case "usage":
		// GET /api/v1/usage reaches authentication as the dashboard's /api/usage, which answers for the caller's tenant.
		// What is under it here, what every organization reserved hour by hour, is the platform's.
		return scopePlatform
	case "mail":
		// What every organization sent through the mail relay.
		return scopePlatform
	}
	return scopePlatform
}
