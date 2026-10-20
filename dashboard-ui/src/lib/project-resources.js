import { list } from "@/lib/api";

export function projectResources(data, projectName, environment) {
  const match = (item) => item.spec?.project === projectName && (!environment || item.spec?.environment === environment);
  return {
    apps: list(data, "apps").filter(match),
    databases: list(data, "databases").filter(match),
    logical: list(data, "logicalDatabases").filter(match),
    stores: list(data, "objectStores").filter(match),
  };
}

/** The connections a project may use: its own, and those of the organization it belongs to. */
export function availableConnections(data, provider, project) {
  const projectName = String(project || "").trim();
  const owner = list(data, "projects").find((item) => item.metadata?.name === projectName);
  const org = String(owner?.spec?.org || "").trim();
  return list(data, "cloudConnections").filter((item) => {
    if (item.spec?.provider !== provider || !item.status?.available) {
      return false;
    }
    const scoped = String(item.spec?.project || "").trim();
    if (scoped !== "") return scoped === projectName;
    return org !== "" && String(item.spec?.org || "").trim() === org;
  });
}
