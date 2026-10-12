import { createRootRoute, createRoute, createRouter, Outlet, redirect } from "@tanstack/react-router";
import { DashboardScreen } from "@/pages/DashboardScreen";

const projectSearch = (search) => {
  const value = (key) => (typeof search[key] === "string" && search[key] ? search[key] : undefined);
  return { environment: value("environment"), view: value("view"), deployment: value("deployment"), create: value("create") };
};

const rootRoute = createRootRoute({ component: () => <Outlet /> });
const route = (path, mode, validateSearch) => createRoute({
  getParentRoute: () => rootRoute,
  path,
  validateSearch,
  component: () => <DashboardScreen mode={mode} />,
});

const deviceSearch = (search) => ({ code: typeof search.code === "string" && search.code ? search.code : undefined });

export const routeTree = rootRoute.addChildren([
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
  route("/device", "device", deviceSearch),
  route("/settings", "settings"),
  route("/settings/domain", "settings:domain"),
  route("/settings/connectors", "settings:connectors"),
  route("/settings/tokens", "settings:tokens"),
  route("/settings/github", "settings:connectors"),
  route("/ha-readiness", "settings:cluster"),
  route("/cluster", "settings:cluster"),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/cloud-connections",
    beforeLoad: () => {
      throw redirect({ to: "/settings/connectors" });
    },
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/cloud-connections/new",
    beforeLoad: () => {
      throw redirect({ to: "/settings/connectors" });
    },
  }),
  route("/object-storage", "settings:object"),
  route("/shared-databases", "settings:databases"),
]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });
