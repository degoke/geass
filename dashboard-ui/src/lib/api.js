export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

export async function api(path, options = {}) {
  const response = await fetch(path, { credentials: "same-origin", ...options });
  const type = response.headers.get("content-type") || "";
  const payload = type.includes("json") ? await response.json() : await response.text();
  if (!response.ok) throw new ApiError(payload?.error || payload || `Request failed (${response.status})`, response.status);
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
    headers: { Accept: "application/json" },
    ...options,
  });
}

export const list = (data, key) => data?.[key]?.items || [];
export const resourceName = (item) => item?.metadata?.name || "Unnamed";
export const condition = (item) => item?.status?.conditions?.find((entry) => entry.type === "Ready")?.status || item?.status?.phase || "Pending";
export const isReady = (item) => condition(item) === "True" || condition(item) === "Ready";
