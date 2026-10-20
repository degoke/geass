/** Prefix a site path with the configured base (SITE_BASE), so links keep working under a sub-path. */
export function withBase(path: string): string {
  const base = import.meta.env.BASE_URL.replace(/\/$/, "");
  return `${base}/${path.replace(/^\//, "")}`;
}

export const docsUrl = withBase("docs/");
export const installDocsUrl = withBase("docs/getting-started/install/");
export const connectorsDocsUrl = withBase("docs/guides/connectors/");
export const addServersDocsUrl = withBase("docs/getting-started/add-servers/");
export const nodePoolsDocsUrl = withBase("docs/guides/node-pools/");

export const siteName = "Geass";
export const githubUrl = "https://github.com/degoke/geass";
export const licenseUrl = "https://www.elastic.co/licensing/elastic-license";

/** The link-preview card (src/assets/og.png). It is the same image on every page. */
export const socialCard = {
  width: 1200,
  height: 630,
  alt: "Geass: absolute command over servers you already own. One cloud to manage them all.",
};

/** What Geass is, in one sentence. The landing page, its structured data and llms.txt all use this. */
export const siteDescription =
  "A self-hosted cloud for your own servers, from VPS to bare metal. Deploy apps and databases, scale across machines and connect other clouds, with no lock-in.";

/**
 * The link-preview image and the Twitter fields, as `<meta>` attributes. The landing layout and every docs page
 * (through the route middleware) both use this, so the card's size, type and alt text are defined once.
 */
export function socialTags({ title, description, image }: { title: string; description?: string; image: string }) {
  const tags: Record<string, string>[] = [
    { property: "og:image", content: image },
    { property: "og:image:type", content: "image/png" },
    { property: "og:image:width", content: String(socialCard.width) },
    { property: "og:image:height", content: String(socialCard.height) },
    { property: "og:image:alt", content: socialCard.alt },
    { name: "twitter:title", content: title },
    { name: "twitter:image", content: image },
    { name: "twitter:image:alt", content: socialCard.alt },
  ];
  if (description) tags.push({ name: "twitter:description", content: description });
  return tags;
}

/**
 * Make a site path absolute. Link scrapers and search engines ignore relative `og:image`, canonical and sitemap
 * URLs, so anything that is shared or indexed goes through this. `site` is `Astro.site` / `context.site`.
 */
export function absoluteUrl(path: string, site: URL | string): string {
  return new URL(path, site).href;
}

/**
 * Serialise structured data for an inline `<script type="application/ld+json">`. The `<` is escaped so a title or
 * description can never contain `</script>` and close the tag early.
 */
export function jsonLd(data: unknown): string {
  return JSON.stringify(data).replace(/</g, "\\u003c");
}
