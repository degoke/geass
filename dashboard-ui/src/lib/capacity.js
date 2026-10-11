export function workloadKind(type, kind) {
  if (type === "service") return "service";
  if (kind === "logical") return "logical";
  if (kind === "planetscale" || kind === "awsdb" || kind === "s3") return "external";
  if (kind === "minio") return "bucket";
  return kind;
}

export function defaultSizeForKind(type, kind) {
  if (kind === "sqlite" || type === "service") return { cpu: "100m", memory: "128Mi" };
  if (["postgres", "mysql", "redis", "minio"].includes(kind) || type === "database") return { cpu: "250m", memory: "512Mi" };
  return { cpu: "100m", memory: "128Mi" };
}

export function cpuMillis(value) {
  const text = String(value || "").trim();
  if (!text) return 0;
  if (text.endsWith("m")) return parseInt(text, 10) || 0;
  const cores = parseFloat(text);
  return Number.isFinite(cores) ? Math.round(cores * 1000) : 0;
}

export function memoryBytes(value) {
  const text = String(value || "").trim();
  if (text.endsWith("Ti")) return (parseInt(text, 10) || 0) * 1024 ** 4;
  if (text.endsWith("Gi")) return (parseInt(text, 10) || 0) * 1024 ** 3;
  if (text.endsWith("Mi")) return (parseInt(text, 10) || 0) * 1024 ** 2;
  if (text.endsWith("Ki")) return (parseInt(text, 10) || 0) * 1024;
  return parseInt(text, 10) || 0;
}

/** Redis runs without persistence, so it has no volume to size. Mirrors platform.DatabaseHasVolume. */
export function databaseHasVolume(engine) {
  return String(engine || "").toLowerCase() !== "redis";
}

/**
 * Default volume size for an engine, as served by the backend in
 * `platform.defaultStorage` so there is only one place these numbers live.
 */
export function defaultStorageForEngine(engine, defaults) {
  return defaults?.[String(engine || "postgres").toLowerCase()] || "";
}

const STORAGE_PRESETS = ["1Gi", "5Gi", "10Gi", "20Gi", "50Gi", "100Gi", "200Gi", "500Gi"];

/**
 * Disk sizes offered for a database. `current` is always included, so a value set
 * outside the UI is not silently swapped for a preset, and nothing below `floor`
 * is offered because a deployed volume cannot shrink.
 */
export function storageSizeOptions(current, floor) {
  const floorBytes = memoryBytes(floor);
  const values = STORAGE_PRESETS.includes(current) || !current ? STORAGE_PRESETS : [...STORAGE_PRESETS, current];
  return values
    .filter((value) => value === current || memoryBytes(value) >= floorBytes)
    .sort((a, b) => memoryBytes(a) - memoryBytes(b))
    .map((value) => ({ value, label: value.replace(/(\d+)Gi$/, "$1 GB").replace(/(\d+)Ti$/, "$1 TB") }));
}

export function sizeLabel(options, value, fallback) {
  return options.find((option) => option.value === value)?.label || fallback || value;
}

export function resourceEstimate(data, type, kind, ha) {
  const estimates = data?.platform?.capacity?.estimates || {};
  const key = ha && (kind === "postgres" || kind === "mysql" || kind === "redis") ? `${kind}-ha` : workloadKind(type, kind);
  return estimates[key] || estimates[workloadKind(type, kind)];
}

export function liveEstimate(data, type, kind, form) {
  const catalog = resourceEstimate(data, type, kind, form.highAvailability);
  if (!catalog || (!catalog.cpuMillis && !catalog.memoryBytes)) return catalog;
  const copies = type === "service"
    ? (form.autoscaling ? Math.max(Number(form.maxReplicas) || 3, Number(form.replicas) || 1) : Number(form.replicas) || 1)
    : (catalog.replicas || 1);
  const cpu = form.cpu || catalog.perCpu;
  const memory = form.memory || catalog.perMemory;
  return {
    ...catalog,
    cpu,
    memory,
    replicas: copies,
    cpuMillis: cpuMillis(cpu) * copies,
    memoryBytes: memoryBytes(memory) * copies,
    perCpuMillis: cpuMillis(cpu),
    perMemoryBytes: memoryBytes(memory),
  };
}

export function capacityFits(capacity, estimate) {
  if (!estimate) return true;
  if (!estimate.cpuMillis && !estimate.memoryBytes) return true;
  if (!capacity?.known) return false;
  if (estimate.perCpuMillis > capacity.largestNodeCpuMillis || estimate.perMemoryBytes > capacity.largestNodeMemoryBytes) return false;
  return estimate.cpuMillis <= capacity.cpuAvailableMillis && estimate.memoryBytes <= capacity.memoryAvailableBytes;
}
