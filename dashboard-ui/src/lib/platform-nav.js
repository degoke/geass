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

export const ACCOUNT_POOLS = {
  id: "pools",
  icon: "pools",
  label: "Pools",
  to: "/pools",
  active: (path) => path === "/pools" || path.startsWith("/pools/"),
};

export const SETTINGS_PATHS = [
  "/settings",
  "/settings/domain",
  "/settings/connectors",
  "/settings/github",
  "/settings/tokens",
  "/settings/auth",
  "/settings/organizations",
  "/settings/alerts",
  "/settings/registry",
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
  // `tenant` is what an organization's administrators are shown: the rest is the platform's.
  { id: "tokens", label: "Tokens", to: "/settings/tokens", tenant: true, active: (path) => path.startsWith("/settings/tokens") },
  { id: "auth", label: "Sign-in", to: "/settings/auth", active: (path) => path.startsWith("/settings/auth") },
  { id: "organizations", label: "Organizations", to: "/settings/organizations", active: (path) => path.startsWith("/settings/organizations") },
  { id: "alerts", label: "Alerts", to: "/settings/alerts", tenant: true, active: (path) => path.startsWith("/settings/alerts") },
  // `orgOnly` is shown to organizations and not to the platform, whose registry is set under Connectors.
  { id: "registry", label: "Registry", to: "/settings/registry", tenant: true, orgOnly: true, active: (path) => path.startsWith("/settings/registry") },
  { id: "object", label: "Object storage", to: "/object-storage", active: (path) => path.startsWith("/object-storage") },
  { id: "databases", label: "Databases", to: "/shared-databases", active: (path) => path.startsWith("/shared-databases") },
];

/** The settings links a session is shown: all of them for the platform, and only the organization's own for an organization. */
export function settingsLinksFor(platform) {
  return platform ? SETTINGS_LINKS.filter((item) => !item.orgOnly) : SETTINGS_LINKS.filter((item) => item.tenant);
}

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
  if (parts[0] === "pools") {
    return parts[1] ? [{ label: "Pools", to: "/pools" }, { label: parts[1] }] : [{ label: "Pools" }];
  }
  const settingsLink = SETTINGS_LINKS.find((item) => item.active(pathname));
  if (settingsLink) return [{ label: "Settings" }, { label: settingsLink.label }];
  return [{ label: "Geass" }];
}
