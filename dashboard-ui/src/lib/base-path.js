/** "/acme/clusters/c1/" and "acme/clusters/c1" are both "/acme/clusters/c1"; the whole site is "/". */
export function normalizeBasePath(basePath) {
  const path = `/${String(basePath || "").trim()}`.replace(/\/{2,}/g, "/").replace(/\/+$/, "");
  return path || "/";
}
