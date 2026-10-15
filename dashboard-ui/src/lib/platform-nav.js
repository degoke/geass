import { list, resourceName } from "@/lib/api";

export const DOCS_URL = "https://rungeass.com/docs/";

export const ACCOUNT_PRIMARY = [
  {
    id: "projects",
    icon: "projects",
    label: "Projects",
    to: "/projects",
    active: (path) => path === "/" || path === "/projects" || path.startsWith("/projects/"),
  },
  {
    id: "templates",
    icon: "templates",
    label: "Templates",
    to: "/templates",
    active: (path) => path === "/templates" || path.startsWith("/templates/"),
  },
];

export const ACCOUNT_USAGE = {
  id: "usage",
  icon: "cluster",
  label: "Usage",
  to: "/cluster",
  active: (path) => path === "/cluster" || path.startsWith("/cluster/") || path === "/ha-readiness",
};

export const SETTINGS_PATHS = [
  "/settings",
  "/settings/domain",
  "/settings/connectors",
  "/settings/github",
  "/settings/tokens",
  "/settings/auth",
  "/settings/alerts",
  "/object-storage",
  "/shared-databases",
  "/cluster",
  "/ha-readiness",
];

export function settingsActive(path) {
  return SETTINGS_PATHS.some((prefix) => path === prefix || path.startsWith(`${prefix}/`));
}

export const SETTINGS_LINKS = [
  { id: "general", label: "General", to: "/settings", active: (path) => path === "/settings" },
  { id: "usage", label: "Usage", to: "/cluster", active: (path) => path === "/cluster" || path === "/ha-readiness" || path.startsWith("/cluster/") },
  { id: "domain", label: "Domain", to: "/settings/domain", active: (path) => path.startsWith("/settings/domain") },
  { id: "connectors", label: "Connectors", to: "/settings/connectors", active: (path) => path.startsWith("/settings/connectors") || path.startsWith("/settings/github") },
  { id: "tokens", label: "Tokens", to: "/settings/tokens", active: (path) => path.startsWith("/settings/tokens") },
  { id: "auth", label: "Sign-in", to: "/settings/auth", active: (path) => path.startsWith("/settings/auth") },
  { id: "alerts", label: "Alerts", to: "/settings/alerts", active: (path) => path.startsWith("/settings/alerts") },
  { id: "object", label: "Object storage", to: "/object-storage", active: (path) => path.startsWith("/object-storage") },
  { id: "databases", label: "Databases", to: "/shared-databases", active: (path) => path.startsWith("/shared-databases") },
];

/** @deprecated use SETTINGS_LINKS */
export const SETTINGS_NAV = SETTINGS_LINKS;

export const FOOTER_LINKS = [
  { id: "docs", label: "Docs", href: DOCS_URL },
  { id: "support", label: "Support", href: "https://github.com/degoke/geass/issues" },
];

/** @deprecated use ACCOUNT_* exports */
export const SIDEBAR = [];

export function breadcrumbItems(pathname, data) {
  const parts = pathname.split("/").filter(Boolean);
  if (!parts.length || (parts[0] === "projects" && !parts[1])) {
    return [{ label: "Projects" }];
  }
  if (parts[0] === "templates") {
    return [{ label: "Templates" }];
  }
  if (parts[0] === "projects" && parts[1]) {
    const project = list(data, "projects").find((item) => resourceName(item) === parts[1]);
    const items = [
      { label: "Projects", to: "/projects" },
      {
        label: project?.spec?.displayName || parts[1],
        to: parts.length > 2 ? `/projects/${parts[1]}` : undefined,
      },
    ];
    if (parts[3]) items.push({ label: parts[3] });
    return items;
  }
  if (parts[0] === "cluster") return [{ label: "Usage" }];
  const settingsLink = SETTINGS_LINKS.find((item) => item.active(pathname));
  if (settingsLink) return [{ label: "Settings" }, { label: settingsLink.label }];
  return [{ label: "Geass" }];
}
