/** Prefix a site path with the configured base (SITE_BASE), so links keep working under a sub-path. */
export function withBase(path: string): string {
  const base = import.meta.env.BASE_URL.replace(/\/$/, "");
  return `${base}/${path.replace(/^\//, "")}`;
}

export const docsUrl = withBase("docs/");
export const installDocsUrl = withBase("docs/getting-started/install/");
