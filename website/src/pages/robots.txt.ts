import type { APIRoute } from "astro";
import { absoluteUrl, withBase } from "../lib/site";

// Everything is public and meant to be found, so the file only has to point crawlers at the sitemap. It is built
// from `site` so a staging build advertises its own sitemap, not the production one.
export const GET: APIRoute = ({ site }) => {
  const lines = ["User-agent: *", "Allow: /"];
  if (site) lines.push("", `Sitemap: ${absoluteUrl(withBase("sitemap-index.xml"), site)}`);
  return new Response(`${lines.join("\n")}\n`, { headers: { "Content-Type": "text/plain; charset=utf-8" } });
};
