import { list } from "@/lib/api";
import { cpuMillis, memoryBytes, sizeLabel } from "@/lib/capacity";

/** The engines an object store can have: the two servers Geass runs in the cluster, and AWS S3 for a bucket in the customer's own account. */
export const OBJECT_STORE_ENGINES = ["SeaweedFS", "Garage", "S3"];

/** The servers a cluster's object storage can be set up with, in the order they are offered. */
export const CLUSTER_SERVER_ENGINES = ["SeaweedFS", "Garage"];

export function isObjectStoreEngine(engine) {
  return OBJECT_STORE_ENGINES.includes(engine);
}

function livesInCluster(item) {
  return item?.spec?.engine !== "S3" && item?.spec?.placement !== "External";
}

/** The cluster's object storage server: the store that belongs to no project and lives in the cluster. */
export function clusterObjectStoreServer(data) {
  return list(data, "objectStores").find((item) => !item.spec?.project && livesInCluster(item));
}

/** Project buckets that are kept on the cluster's server (not buckets in a cloud account). */
export function clusterProjectBuckets(data) {
  return list(data, "objectStores").filter((item) => item.spec?.project && livesInCluster(item));
}

/** The size a server starts at, as the backend reports it in `platform.objectStorageServers`. */
export function objectStorageServer(data, engine) {
  return (data?.platform?.objectStorageServers || []).find((server) => server.engine === engine);
}

/**
 * A server's starting size in the words the size pickers use ("0.25 CPU, 512 MB"). The pickers' own labels win,
 * so the two never disagree.
 */
export function startingSizeLabel(data, engine) {
  const server = objectStorageServer(data, engine);
  if (!server) return "";
  const cpuMilli = cpuMillis(server.cpu);
  const memoryMiB = memoryBytes(server.memory) / 1024 ** 2;
  const cpu = sizeLabel(data?.platform?.capacity?.cpuSizes || [], server.cpu, `${+(cpuMilli / 1000).toFixed(2)} CPU`);
  const memory = sizeLabel(
    data?.platform?.capacity?.memorySizes || [],
    server.memory,
    memoryMiB >= 1024 ? `${+(memoryMiB / 1024).toFixed(1)} GB` : `${Math.round(memoryMiB)} MB`,
  );
  return `${cpu}, ${memory}`;
}

const STORAGE_UNITS = { "": 1, k: 1e3, K: 1e3, M: 1e6, G: 1e9, T: 1e12, Ki: 1024, Mi: 1024 ** 2, Gi: 1024 ** 3, Ti: 1024 ** 4 };

/** The smallest volume a server can have. The backend enforces the same floor. */
export const MIN_SERVER_STORAGE = "1Gi";

/** A Kubernetes quantity such as "10Gi" in bytes, or NaN when it is not one. */
export function storageBytes(value) {
  const match = /^(\d+(?:\.\d+)?)(Ki|Mi|Gi|Ti|k|K|M|G|T)?$/.exec(String(value ?? "").trim());
  return match ? Number(match[1]) * STORAGE_UNITS[match[2] || ""] : NaN;
}

/** Why a size cannot be used for the server's volume, or "" when it can. An empty size means "use the default". */
export function storageSizeProblem(value) {
  if (!String(value ?? "").trim()) return "";
  const bytes = storageBytes(value);
  if (Number.isNaN(bytes)) return "Storage size must be a quantity such as 10Gi.";
  if (bytes < storageBytes(MIN_SERVER_STORAGE)) return `Storage size must be at least ${MIN_SERVER_STORAGE}.`;
  return "";
}

/** The size the cluster's server starts with, as the backend reports it in `platform.defaultStorage`. */
export function defaultServerStorage(data) {
  return data?.platform?.defaultStorage?.objectstore || "";
}

/** The server's volume size from its spec, or "" before the operator has set one. */
export function serverStorageSize(server) {
  return String(server?.spec?.storageSize || "");
}
