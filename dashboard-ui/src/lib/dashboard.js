import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

function bootstrapShouldPoll(error) {
  const status = error?.status;
  return status !== 401 && status !== 404;
}

export function useBootstrap() {
  return useQuery({
    queryKey: ["dashboard", "bootstrap"],
    queryFn: () => api("/api/bootstrap"),
    staleTime: 2000,
    retry: (count, error) => bootstrapShouldPoll(error) && count < 2,
    refetchInterval: (query) => (bootstrapShouldPoll(query.state.error) ? 60000 : false),
  });
}

export function dashboardSession(data) {
  return data?.platform?.session || { username: "", role: "viewer", canMutate: false };
}

export function canMutate(data) {
  return Boolean(dashboardSession(data).canMutate);
}

/**
 * The version a database runs. Every database has a version field, and it starts as a Postgres version whatever the engine,
 * so a Redis database says 16. Only a Postgres database's own version is shown; for the other engines it is the version
 * Geass runs, which the server reports.
 */
export function databaseVersion(data, item) {
  const engine = item?.spec?.engine;
  if (engine === "Postgres") return item.spec?.version || "";
  return data?.platform?.engineVersions?.[engine] || "";
}

/** Where the signed-in person may work, once the platform has organizations on: the platform's own place (for a platform
 * administrator) and each organization and workspace they were given. Empty when organizations are off. */
export function sessionScopes(data) {
  return dashboardSession(data).scopes || [];
}

/** Whether the session acts for the platform as a whole. While organizations are off everyone does; once they are on,
 * only a platform administrator who has not gone into an organization. */
export function isPlatformSession(data) {
  const session = dashboardSession(data);
  const scopes = session.scopes || [];
  if (!scopes.length) return true;
  return Boolean(session.platformAdmin) && !session.org;
}

/** What a place is called: "Platform", "Acme", or "Acme / Payments team". */
export function scopeLabel(scope) {
  if (!scope || !scope.org) return "Platform";
  const org = scope.orgName || scope.org;
  return scope.workspace ? `${org} / ${scope.workspaceName || scope.workspace}` : org;
}

/** The place the session works in now, among the places it may work in. */
export function currentScope(data) {
  const session = dashboardSession(data);
  return sessionScopes(data).find((scope) => (scope.org || "") === (session.org || "") && (scope.workspace || "") === (session.workspace || ""));
}
