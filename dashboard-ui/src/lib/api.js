export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

function apiErrorMessage(path, status, payload) {
  if (status === 404 && path.startsWith("/api/")) {
    const body = typeof payload === "string" ? payload : "";
    if (!payload?.error && (body.includes("page not found") || body.includes("Not Found"))) {
      return (
        "Geass API not found. The Vite dev server proxies /api to the Go dashboard — start it with " +
        "make dashboard-backend (API on :8085) or run make run for both UI and API."
      );
    }
    if (payload?.error === "not found" && path.includes("/cloudflare")) {
      return (
        "This dashboard API does not include Cloudflare connector routes. Restart the Geass manager " +
        "from this repo (make dashboard-backend or make run), or rebuild and rollout the controller image " +
        "if you are using a cluster install."
      );
    }
  }
  if (payload?.error) return payload.error;
  if (typeof payload === "string" && payload.trim()) return payload;
  return `Request failed (${status})`;
}

export async function api(path, options = {}) {
  const response = await fetch(path, { credentials: "same-origin", ...options });
  const type = response.headers.get("content-type") || "";
  const payload = type.includes("json") ? await response.json() : await response.text();
  if (!response.ok) {
    throw new ApiError(apiErrorMessage(path, response.status, payload), response.status);
  }
  return payload;
}

export async function action(path, values = {}, options = {}) {
  const body = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value === undefined || value === null) return;
    if (Array.isArray(value)) value.forEach((item) => body.append(key, item));
    else body.set(key, value);
  });
  return api(`/api${path.startsWith("/api/") ? path.slice(4) : path}`, {
    method: "POST",
    body,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8",
    },
    ...options,
  });
}

export const list = (data, key) => data?.[key]?.items || [];
export const resourceName = (item) => item?.metadata?.name || "Unnamed";
export const condition = (item) => item?.status?.conditions?.find((entry) => entry.type === "Ready")?.status || item?.status?.phase || "Pending";
export const isReady = (item) => condition(item) === "True" || condition(item) === "Ready";
