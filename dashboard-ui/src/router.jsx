import { createRootRoute, createRoute, createRouter, Outlet, redirect } from "@tanstack/react-router";
import { DashboardScreen } from "@/pages/DashboardScreen";

const projectSearch = (search) => {
  const value = (key) => (typeof search[key] === "string" && search[key] ? search[key] : undefined);
  return { environment: value("environment"), view: value("view"), deployment: value("deployment"), create: value("create") };
};

const deviceSearch = (search) => ({ code: typeof search.code === "string" && search.code ? search.code : undefined });

/**
 * The dashboard's routes. Each router gets a tree of its own: a route object belongs to the router that first used it, and
 * the dashboard is mounted again (another cluster, another base path) while a host app lives on.
 */
export function createDashboardRouteTree() {
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const route = (path, mode, validateSearch) => createRoute({
    getParentRoute: () => rootRoute,
    path,
    validateSearch,
    component: () => <DashboardScreen mode={mode} />,
  });
  const redirectTo = (path, to) => createRoute({
    getParentRoute: () => rootRoute,
    path,
    beforeLoad: () => {
      throw redirect({ to });
    },
  });

  return rootRoute.addChildren([
    createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => <DashboardScreen mode="projects" /> }),
    route("/projects", "projects"),
    route("/templates", "templates"),
    route("/projects/$projectName/monitor", "project-monitor", projectSearch),
    route("/projects/$projectName/logs", "project-logs", projectSearch),
    route("/projects/$projectName/settings", "project-settings", projectSearch),
    route("/projects/$projectName/apps/$name", "resource:apps", projectSearch),
    route("/projects/$projectName/databases/$name", "resource:databases", projectSearch),
    route("/projects/$projectName/logical-databases/$name", "resource:logical-databases", projectSearch),
    route("/projects/$projectName/object-stores/$name", "resource:object-stores", projectSearch),
    route("/projects/$projectName", "workspace", projectSearch),
    route("/pools", "pools"),
    route("/pools/$poolName", "pool"),
    route("/device", "device", deviceSearch),
    route("/setup", "setup"),
    route("/settings", "settings"),
    route("/settings/domain", "settings:domain"),
    route("/settings/connectors", "settings:connectors"),
    route("/settings/tokens", "settings:tokens"),
    route("/settings/auth", "settings:auth"),
    route("/settings/users", "settings:users"),
    route("/settings/organizations", "settings:organizations"),
    route("/settings/alerts", "settings:alerts"),
    route("/settings/addons", "settings:addons"),
    route("/settings/registry", "settings:registry"),
    route("/settings/github", "settings:connectors"),
    route("/ha-readiness", "settings:cluster"),
    route("/cluster", "settings:cluster"),
    redirectTo("/cloud-connections", "/settings/connectors"),
    redirectTo("/cloud-connections/new", "/settings/connectors"),
    route("/object-storage", "settings:object"),
    route("/shared-databases", "settings:databases"),
  ]);
}

/**
 * A router for the dashboard. basepath is where the dashboard lives in the page's URL space ("/" when it is the whole
 * site, "/acme/clusters/c1" when a host app mounts it under its own routes): every link and every navigation stays below
 * it. history is the host router's, so that both see every change of the address; without it the browser's is used.
 */
export function createDashboardRouter({ basepath = "/", history } = {}) {
  return createRouter({ routeTree: createDashboardRouteTree(), basepath, history, defaultPreload: "intent" });
}
