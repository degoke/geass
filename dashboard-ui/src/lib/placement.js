// Regions and node tags, as the dashboard shows them. Both come from the bootstrap capacity data, so
// everything here is derived from what the cluster reports and nothing is configured in the browser.

export function platformRegions(data) {
  return data?.platform?.capacity?.regions || [];
}

export function platformTags(data) {
  return data?.platform?.capacity?.tags || [];
}

export function defaultRegion(data) {
  return data?.platform?.defaultRegion || "";
}

/** Regions only matter once there is a choice to make: with fewer than two, the UI stays as it was. */
export function hasRegionChoice(data) {
  return platformRegions(data).length >= 2;
}

/** The region a project runs in, falling back to the cluster default. Empty when neither is set. */
export function projectRegion(project, data) {
  return project?.spec?.region || defaultRegion(data);
}

/** Tags a service or database can ask for within a region: tags carried by at least one node there. */
export function tagsInRegion(data, region) {
  const nodes = data?.platform?.capacity?.nodes || [];
  const found = new Map();
  nodes.forEach((node) => {
    if (region && node.region !== region) return;
    (node.tags || []).forEach((tag) => {
      const entry = found.get(tag.name) || { name: tag.name, nodes: 0, reserved: false };
      entry.nodes += 1;
      entry.reserved = entry.reserved || Boolean(tag.reserved);
      found.set(tag.name, entry);
    });
  });
  return [...found.values()].sort((a, b) => a.name.localeCompare(b.name));
}

/** What a node's tags read as in a table: "storage (reserved), compute". */
export function describeTags(tags) {
  return (tags || []).map((tag) => (tag.reserved ? `${tag.name} (reserved)` : tag.name)).join(", ");
}

/** Splits a typed list ("storage, gpu") into clean tag names. */
export function parseTagList(text) {
  const names = [];
  String(text || "")
    .split(",")
    .map((part) => part.trim().toLowerCase())
    .filter(Boolean)
    .forEach((name) => {
      if (!names.includes(name)) names.push(name);
    });
  return names;
}

/**
 * The region a shared database server runs in: where the operator pinned it, or where it was asked to
 * run before it has been placed. Empty when it may run anywhere.
 */
export function sharedServerRegion(server) {
  try {
    const pinned = JSON.parse(server?.metadata?.annotations?.["geass.dev/node-placement"] || "{}");
    if (pinned.region) return pinned.region;
  } catch {
    // An unreadable pin means no region.
  }
  return server?.spec?.serverRegion || "";
}

/** Whether the cluster keeps logs after pods are gone (the logging add-on), and for how many days. */
export function loggingEnabled(data) {
  return (data?.clusters?.items || []).some((cluster) => cluster.spec?.addons?.logging?.enabled === true);
}

export function logRetentionDays(data) {
  const cluster = (data?.clusters?.items || []).find((item) => item.spec?.addons?.logging?.enabled === true);
  return cluster?.spec?.addons?.logging?.retentionDays || 7;
}
