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

export function availableConnections(data, provider, project) {
  const projectName = String(project || "").trim();
  return list(data, "cloudConnections").filter((item) => {
    if (item.spec?.provider !== provider || !item.status?.available) {
      return false;
    }
    const scoped = String(item.spec?.project || "").trim();
    return scoped !== "" && scoped === projectName;
  });
}
