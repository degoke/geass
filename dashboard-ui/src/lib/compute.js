// Machines: clouds, pools and single servers. Everything is read from the API (the providers, the sizes a
// cloud offers, the connections and pools), so the dashboard carries no copy of what a cloud needs.
//
// Reads are the v1 routes (GET /api/v1/...), which the dashboard's session is allowed to call. Changes go through the
// dashboard's own routes with `action` (POST /api/pools/create and so on), the same as every other form.

import { useCallback, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";

export const COMPUTE_KEY = ["compute"];
const BOOTSTRAP_KEY = ["dashboard", "bootstrap"];

const itemsOf = (data) => data?.items || [];

export function useProviders() {
  return useQuery({
    queryKey: [...COMPUTE_KEY, "providers"],
    queryFn: () => api("/api/v1/compute/providers"),
    staleTime: 5 * 60 * 1000,
    select: itemsOf,
  });
}

/** A connection is still being checked until the operator has asked the cloud about it once. */
const connectionsSettling = (connections) => connections.some((item) => !item.verifiedAt && !item.message);

export function useConnections() {
  return useQuery({
    queryKey: [...COMPUTE_KEY, "connections"],
    queryFn: () => api("/api/v1/connections"),
    refetchInterval: (query) => (connectionsSettling(itemsOf(query.state.data)) ? 3000 : 20000),
    select: itemsOf,
  });
}

export function usePools() {
  return useQuery({
    queryKey: [...COMPUTE_KEY, "pools"],
    queryFn: () => api("/api/v1/pools"),
    refetchInterval: 8000,
    select: itemsOf,
  });
}

export function usePool(name) {
  return useQuery({
    queryKey: [...COMPUTE_KEY, "pool", name],
    queryFn: () => api(`/api/v1/pools/${encodeURIComponent(name)}`),
    refetchInterval: 6000,
    retry: (count, error) => error?.status !== 404 && count < 2,
  });
}

/** The sizes a cloud offers in a region. Admin only: the call is made with the connection's credentials. */
export function useSizes(connection, region, enabled = true) {
  return useQuery({
    queryKey: [...COMPUTE_KEY, "sizes", connection, region || ""],
    queryFn: () => {
      const query = new URLSearchParams({ connection });
      if (region) query.set("region", region);
      return api(`/api/v1/compute/sizes?${query.toString()}`);
    },
    enabled: Boolean(connection) && enabled,
    staleTime: 5 * 60 * 1000,
    retry: false,
    select: itemsOf,
  });
}

/**
 * Reloads everything a change to a pool, a server or a connection can alter, the node table included. `again` looks twice
 * more soon after, for changes whose result the operator writes a moment later (a connection being checked).
 */
export function useRefreshCompute() {
  const queryClient = useQueryClient();
  return useCallback(
    (options) => {
      const run = () => Promise.all([
        queryClient.invalidateQueries({ queryKey: COMPUTE_KEY }),
        queryClient.invalidateQueries({ queryKey: BOOTSTRAP_KEY }),
      ]);
      if (options?.again) [1500, 4000].forEach((delay) => window.setTimeout(run, delay));
      return run();
    },
    [queryClient],
  );
}

// ---- connections ----

/** A connection that can create machines for the cluster: it has the Compute purpose and belongs to no project. */
export function isMachineConnection(connection) {
  return !connection.project && (connection.purposes || []).includes("Compute");
}

export function providerOf(providers, id) {
  return (providers || []).find((provider) => provider.id === id);
}

export function providerLabel(providers, id) {
  return providerOf(providers, id)?.label || id || "";
}

const DAY = 24 * 60 * 60 * 1000;

/** What to warn about on a connection: what the operator reported, and credentials that are about to expire. */
export function connectionWarnings(connection, now = Date.now()) {
  const warnings = [...(connection.warnings || [])];
  if (connection.expiresAt) {
    const left = new Date(connection.expiresAt).getTime() - now;
    if (left <= 0) warnings.push("The credentials have expired. Replace them.");
    else if (left < 14 * DAY) {
      const days = Math.max(1, Math.ceil(left / DAY));
      warnings.push(`The credentials expire in ${days} ${days === 1 ? "day" : "days"}. Replace them before then.`);
    }
  }
  return warnings;
}

/** What depends on a connection, in words: "pools web and batch, 2 machines". Empty when nothing does. */
export function connectionUse(connection) {
  const pools = connection.usedBy || [];
  const parts = [];
  if (pools.length) parts.push(`${pools.length === 1 ? "pool" : "pools"} ${pools.join(", ")}`);
  if (connection.machines) parts.push(`${connection.machines} ${connection.machines === 1 ? "machine" : "machines"}`);
  return parts.join(", ");
}

// ---- pools ----

export const PHASE_TONE = {
  Ready: "success",
  Scaling: "neutral",
  Pending: "neutral",
  Suspended: "neutral",
  Degraded: "warning",
  Paused: "danger",
};

export const PHASE_HELP = {
  Ready: "Has the nodes it wants.",
  Scaling: "Adding or removing nodes now.",
  Pending: "Waiting for its first look at the cluster.",
  Suspended: "Scaling is switched off: no node is added or removed.",
  Degraded: "Something is wrong, but the pool is still trying.",
  Paused: "Stopped after repeated failures.",
};

export const poolPhase = (pool) => pool?.phase || "Pending";

/** The short line under a pool's status in a list. A paused pool's own message repeats what its alert says. */
export function poolNote(pool) {
  const phase = poolPhase(pool);
  if (phase === "Ready") return "";
  if (phase === "Paused") return pool.pausedUntil ? `Tries again ${relativeTime(pool.pausedUntil)}` : "Stopped after repeated failures";
  return pool.message || "";
}

/** Where a pool's machines come from, for a table cell. */
export function poolSource(pool, providers) {
  if (pool.connection) {
    const label = providerLabel(providers, pool.provider);
    return label ? `${label} (${pool.connection})` : pool.connection;
  }
  const count = (pool.inventory || []).length;
  return `My servers (${count} ${count === 1 ? "server" : "servers"})`;
}

export function poolSize(pool) {
  if (!pool.connection) return "";
  const active = pool.activeSize?.name || "";
  return active || pool.size || "";
}

/** Whether new nodes are made with a size or region other than the first the pool asks for. */
export const poolUsesFallback = (pool) => (pool.fallbackStep || 0) > 0;

export function formatPrice(price, currency, unit = "/h") {
  const value = Number(price);
  if (!(value > 0)) return "";
  const text = value < 1 ? value.toFixed(4) : value.toFixed(2);
  return `${text}${currency ? ` ${currency}` : ""}${unit}`;
}

/** What the pool's nodes cost now, as the operator states it ("0.0690 EUR"), per hour. */
export function poolCost(pool) {
  return pool.estimatedHourlyCost ? `${pool.estimatedHourlyCost}/h` : "";
}

/** What the pool costs at its largest, from the price of its size: the most it can ever be asked to spend per hour. */
export function poolCostCap(pool) {
  const price = Number(pool.activeSize?.hourlyPrice);
  if (!(price > 0) || !pool.maxNodes) return "";
  return formatPrice(price * pool.maxNodes, pool.activeSize.currency);
}

export function formatMemoryMiB(mib) {
  const value = Number(mib);
  if (!(value > 0)) return "—";
  if (value < 1024) return `${value} MB`;
  const gb = value / 1024;
  return `${Number.isInteger(gb) ? gb : gb.toFixed(1)} GB`;
}

export function formatSizeSummary(size) {
  if (!size) return "";
  const parts = [];
  if (size.vcpus) parts.push(`${size.vcpus} vCPU`);
  if (size.memoryMiB) parts.push(formatMemoryMiB(size.memoryMiB));
  const price = formatPrice(size.hourlyPrice, size.currency);
  if (price) parts.push(price);
  return parts.join(" · ");
}

export function relativeTime(value, now = Date.now()) {
  if (!value) return "";
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return "";
  const diff = Math.round((time - now) / 1000);
  const seconds = Math.abs(diff);
  if (seconds < 45) return diff > 0 ? "in a few seconds" : "just now";
  let text;
  if (seconds < 3600) text = `${Math.max(1, Math.round(seconds / 60))} min`;
  else if (seconds < 86400) text = `${Math.round(seconds / 3600)} h`;
  else text = `${Math.round(seconds / 86400)} d`;
  return diff > 0 ? `in ${text}` : `${text} ago`;
}

export function absoluteTime(value) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleString();
}

/** The age of something, without "ago": "3 d", "5 h", "12 min". */
export function ageOf(value, now = Date.now()) {
  const text = relativeText(value, now);
  return text || "—";
}

function relativeText(value, now) {
  if (!value) return "";
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return "";
  const seconds = Math.max(0, Math.round((now - time) / 1000));
  if (seconds < 90) return "just now";
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)} h`;
  return `${Math.round(seconds / 86400)} d`;
}

/** A node of a pool that is not ready and has a reason to say: what to show under its name. */
export const nodeIsStuck = (node) => !node.ready && !node.deleting && Boolean(node.message);

// ---- forms ----

/** Splits a typed list ("cx23, cx33") into clean entries. */
export function parseList(text) {
  const seen = [];
  String(text || "")
    .split(/[,\n]/)
    .map((part) => part.trim())
    .filter(Boolean)
    .forEach((part) => {
      if (!seen.includes(part)) seen.push(part);
    });
  return seen;
}

/** A switch is sent as the word true or false: the routes read anything present but not on as off. */
export const flag = (value) => (value ? "true" : "false");

export const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export function nameProblem(name, noun, max) {
  if (!name) return `Give the ${noun} a name.`;
  if (name.length > max) return `A ${noun}'s name is at most ${max} characters.`;
  if (!NAME_PATTERN.test(name)) return `A ${noun}'s name uses lowercase letters, numbers and hyphens, and starts and ends with a letter or number.`;
  return "";
}

/** What all the pools cost now, per hour: "0.1380 EUR/h". Amounts in different currencies are listed, never added. */
export function totalPoolCost(pools) {
  const sums = {};
  pools.forEach((pool) => {
    const match = /^([\d.]+)\s*(\S*)$/.exec(pool.estimatedHourlyCost || "");
    if (match) sums[match[2]] = (sums[match[2]] || 0) + Number(match[1]);
  });
  return Object.entries(sums)
    .map(([currency, value]) => formatPrice(value, currency))
    .join(" + ");
}

/** A value that follows another after it has stopped changing: a region is looked up once it is typed, not per key. */
export function useDebounced(value, delay = 500) {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return settled;
}

/** The settings of a form as the JSON text the routes take: blank values are left out. */
export function settingsPayload(descriptor, values, rows) {
  const out = {};
  (descriptor?.settings || []).forEach((setting) => {
    const value = String(values[setting.key] || "").trim();
    if (value) out[setting.key] = value;
  });
  if (descriptor?.freeSettings) {
    rows.forEach((row) => {
      const key = row.key.trim();
      const value = row.value.trim();
      if (key && value) out[key] = value;
    });
  }
  return JSON.stringify(out);
}

/** Splits what a pool already has into the named settings and the free rows. */
export function settingsFromPool(descriptor, settings) {
  const values = {};
  const named = new Set((descriptor?.settings || []).map((setting) => setting.key));
  const rows = [];
  Object.entries(settings || {}).forEach(([key, value]) => {
    if (named.has(key)) values[key] = value;
    else rows.push({ key, value });
  });
  return { values, rows };
}

export function missingSettings(descriptor, values) {
  return (descriptor?.settings || []).filter((setting) => setting.required && !String(values[setting.key] || "").trim());
}

