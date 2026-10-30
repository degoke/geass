/** How long after a cluster is made that "not ready" still means "starting": the add-ons of a new install. */
const STARTING_WINDOW_MS = 60 * 60 * 1000;

/**
 * What the dashboard says while the cluster is not ready, or null when it is. The cluster is ready when the operator has
 * its add-ons (certificates, metrics, storage) running; until then a deploy can fail. Only the platform's sessions get
 * the clusters in the bootstrap, so an organization is never told.
 */
export function clusterStartingNotice(data, now = Date.now()) {
  const clusters = data?.clusters?.items || [];
  if (!clusters.length || clusters.some((cluster) => cluster?.status?.phase === "Ready")) return null;
  const created = Math.min(...clusters.map((cluster) => Date.parse(cluster?.metadata?.creationTimestamp || "") || now));
  if (now - created < STARTING_WINDOW_MS) {
    return {
      lead: "Geass is starting",
      message: "Certificates, metrics, and storage are installing. Deploy after this message goes away.",
    };
  }
  return {
    lead: "Geass is not ready",
    message: "Some of its parts are not running. A deploy can fail until they are.",
  };
}
