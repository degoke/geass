import { createContext, useContext, useMemo } from "react";

/**
 * Parts of the dashboard a host can leave out, by name. Each is something a self-hosted Geass is set up through, which a
 * host that runs Geass for people (rungeass) does for them, or something that only works with the dashboard's own session
 * and not with an API token.
 */
export const HIDEABLE = Object.freeze([
  "upgrade", // the Version section of Usage: which Geass and K3s run, and the upgrade of the workers
  "servers", // adding, editing and removing the cluster's servers
  "signin", // Sign-in settings (local users and passwords, the identity provider), Organizations, the command-line approval page
  "domain", // the dashboard's domain and DNS, and the banner that asks for it
  "tokens", // API tokens: a token cannot manage tokens
  "alerts", // alert settings: the server answers only a signed-in admin
  "addons", // the cluster's add-ons (sleep when idle, replicated storage, metrics, logs): the cluster is the host's to run
  "registry", // an organization's container registry: the server answers only a signed-in admin
  "account", // the account menu (theme, sign out) and the switch between organizations and workspaces
  "brand", // the Geass logo in the top bar
]);

/** What a host that runs Geass for people hides: all of the above except the logo. */
export const SELF_HOSTED_ONLY = Object.freeze(HIDEABLE.filter((name) => name !== "brand"));

const standalone = Object.freeze({
  embedded: false,
  hidden: new Set(),
  logos: undefined,
  onUnauthorized: undefined,
});

const OptionsContext = createContext(standalone);

export function DashboardOptionsProvider({ hide, logos, onUnauthorized, children }) {
  const value = useMemo(
    () => ({ embedded: true, hidden: new Set(hide || []), logos, onUnauthorized }),
    // The list is read by content, so a host that passes a new array on every render does not remount anything.
    [Array.isArray(hide) ? hide.join(",") : "", logos, onUnauthorized],
  );
  return <OptionsContext.Provider value={value}>{children}</OptionsContext.Provider>;
}

export function useDashboardOptions() {
  return useContext(OptionsContext);
}

/** Whether a part of the dashboard is left out by the host. Always false when the dashboard is not mounted in another app. */
export function useHidden(name) {
  return useContext(OptionsContext).hidden.has(name);
}

// Which setting of `hide` takes away each page, by the mode the router gives it, and each link of the settings menu, by its id.
const PAGE_HIDDEN_BY = {
  "settings:domain": "domain",
  "settings:tokens": "tokens",
  "settings:auth": "signin",
  "settings:users": "signin",
  "settings:organizations": "signin",
  "settings:alerts": "alerts",
  "settings:addons": "addons",
  "settings:registry": "registry",
  device: "signin",
};
const SETTINGS_LINK_HIDDEN_BY = {
  domain: "domain",
  tokens: "tokens",
  auth: "signin",
  users: "signin",
  organizations: "signin",
  alerts: "alerts",
  addons: "addons",
  registry: "registry",
};

/** Whether the host took a page away (the page then says it is not available, whoever follows a link to it). */
export function pageHidden(mode, hidden) {
  const name = PAGE_HIDDEN_BY[mode];
  return Boolean(name && hidden.has(name));
}

/** The links of the settings menu the host left in. */
export function visibleSettingsLinks(links, hidden) {
  return links.filter((link) => !hidden.has(SETTINGS_LINK_HIDDEN_BY[link.id]));
}
