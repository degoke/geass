import { defineRouteMiddleware } from "@astrojs/starlight/route-data";
import og from "./assets/og.png";
import { absoluteUrl, docsUrl, jsonLd, siteName, socialTags, withBase } from "./lib/site";

/**
 * Starlight already writes the title, description, canonical URL, `og:url` and the sitemap link once the build has
 * a `site`. This adds what it leaves out on every docs page: the link-preview image, the Twitter fields, a robots
 * hint, and structured data (breadcrumbs plus a TechArticle).
 */
export const onRequest = defineRouteMiddleware((context) => {
  const { entry, head } = context.locals.starlightRoute;
  const site = context.site;
  if (!site) return;

  const url = absoluteUrl(context.url.pathname, site);
  const home = absoluteUrl(withBase("/"), site);
  const docsHome = absoluteUrl(docsUrl, site);
  const image = absoluteUrl(og.src, site);
  const { title, description } = entry.data;
  const isDocsHome = url === docsHome;

  // The 404 page is not a page to find: keep it out of the index and do not let it claim an address.
  if (entry.id === "404") {
    for (let i = head.length - 1; i >= 0; i--) {
      const { tag, attrs } = head[i]!;
      if ((tag === "link" && attrs?.rel === "canonical") || (tag === "meta" && attrs?.property === "og:url")) head.splice(i, 1);
    }
    head.push({ tag: "meta", attrs: { name: "robots", content: "noindex" } });
    return;
  }

  // "Geass documentation | Geass docs" repeats itself, so the docs home gets a title that says what the docs are for.
  // The link preview has to say the same, so the `og:title` Starlight wrote and the Twitter title follow it.
  let socialTitle = title;
  if (isDocsHome) {
    socialTitle = "Geass docs: run your own cloud on your own servers";
    const titleTag = head.find((tag) => tag.tag === "title");
    if (titleTag) titleTag.content = socialTitle;
    const ogTitle = head.find((tag) => tag.tag === "meta" && tag.attrs?.property === "og:title");
    if (ogTitle?.attrs) ogTitle.attrs.content = socialTitle;
  }

  const meta = (attrs: Record<string, string>) => ({ tag: "meta" as const, attrs });
  head.push(
    meta({ name: "robots", content: "index, follow, max-image-preview:large" }),
    ...socialTags({ title: socialTitle, description, image }).map(meta),
  );

  // Breadcrumbs name only real pages (the sidebar's group labels have no URL of their own), so the trail is
  // Geass > Docs > this page.
  const trail = [
    { name: siteName, url: home },
    { name: "Docs", url: docsHome },
    ...(isDocsHome ? [] : [{ name: title, url }]),
  ];
  const structuredData = {
    "@context": "https://schema.org",
    "@graph": [
      {
        "@type": "BreadcrumbList",
        itemListElement: trail.map((step, index) => ({
          "@type": "ListItem",
          position: index + 1,
          name: step.name,
          item: step.url,
        })),
      },
      {
        "@type": isDocsHome ? "WebPage" : "TechArticle",
        headline: title,
        ...(description ? { description } : {}),
        url,
        inLanguage: "en",
        image,
        isPartOf: { "@type": "WebSite", name: siteName, url: home },
      },
    ],
  };
  head.push({ tag: "script", attrs: { type: "application/ld+json" }, content: jsonLd(structuredData) });
});
