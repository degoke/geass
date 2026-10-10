/** Matches pkg/platform.NormalizeProjectName for delete confirmation. */
export function normalizeProjectName(name) {
  const trimmed = String(name || "").trim().toLowerCase();
  if (!trimmed) return "";
  const parts = trimmed.split(/[\s_-]+/).filter(Boolean);
  return parts.join("-");
}
