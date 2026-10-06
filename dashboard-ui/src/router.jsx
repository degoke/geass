import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import { DashboardScreen } from "@/pages/DashboardScreen";

const projectSearch = (search) => {
  const value = (key) => (typeof search[key] === "string" && search[key] ? search[key] : undefined);
  return { environment: value("environment"), view: value("view") };
};

const rootRoute = createRootRoute({ component: () => <Outlet /> });
const route = (path, mode, validateSearch) => createRoute({
  getParentRoute: () => rootRoute,
  path,
  validateSearch,
  component: () => <DashboardScreen mode={mode} />,
});

export const routeTree = rootRoute.addChildren([
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => <DashboardScreen mode="projects" /> }),
  route("/projects", "projects"),
  route("/templates", "templates"),
  route("/projects/$projectName/monitor", "project-monitor", projectSearch),
  route("/projects/$projectName/settings", "project-settings", projectSearch),
  route("/projects/$projectName/apps/$name", "resource:apps", projectSearch),
  route("/projects/$projectName/databases/$name", "resource:databases", projectSearch),
  route("/projects/$projectName/logical-databases/$name", "resource:logical-databases", projectSearch),
  route("/projects/$projectName/object-stores/$name", "resource:object-stores", projectSearch),
  route("/projects/$projectName", "workspace", projectSearch),
  route("/settings", "settings"),
  route("/settings/domain", "settings:domain"),
  route("/settings/connectors", "settings:connectors"),
  route("/settings/github", "settings:connectors"),
  route("/ha-readiness", "settings:cluster"),
  route("/cluster", "settings:cluster"),
  route("/cloud-connections", "settings:cloud"),
  route("/cloud-connections/new", "settings:cloud"),
  route("/object-storage", "settings:object"),
]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });
