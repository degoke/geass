// Checks the search and sharing tags of every page in the built site (dist/). Run after `pnpm build`.
// It looks for what breaks discovery: a missing or wrong canonical URL, a duplicate title, a link-preview image
// that does not exist, structured data that does not parse, and a sitemap that disagrees with the pages.
// Exits non-zero when anything is wrong.
import { readdirSync, readFileSync, existsSync, statSync } from "node:fs";
import path from "node:path";

const dist = path.resolve(process.argv[2] ?? "dist");
const base = (process.env.SITE_BASE ?? "/").replace(/\/?$/, "/");
if (!existsSync(dist)) {
  console.error(`No ${dist}. Run \`pnpm build\` first.`);
  process.exit(2);
}

function* walk(dir) {
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    if (statSync(full).isDirectory()) yield* walk(full);
    else if (full.endsWith(".html")) yield full;
  }
}

const decode = (text) =>
  text.replace(/&amp;/g, "&").replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, "<").replace(/&gt;/g, ">");

// The value of `<meta {attr}="{name}" content="...">`, whichever order the attributes are in.
function meta(html, attr, name) {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const a = html.match(new RegExp(`<meta[^>]*\\s${attr}="${escaped}"[^>]*\\scontent="([^"]*)"`, "i"));
  const b = html.match(new RegExp(`<meta[^>]*\\scontent="([^"]*)"[^>]*\\s${attr}="${escaped}"`, "i"));
  const value = (a ?? b)?.[1];
  return value === undefined ? undefined : decode(value);
}

const files = [...walk(dist)];
const problems = [];
const titles = new Map();
const descriptions = new Map();
const pageUrls = new Set();

// The site's origin is whatever the landing page declares as its canonical URL.
const landing = readFileSync(path.join(dist, "index.html"), "utf8");
const origin = landing.match(/<link rel="canonical" href="(https?:\/\/[^/"]+)/)?.[1];
if (!origin) problems.push("index.html: no absolute canonical link, so the build has no site address (set SITE_URL)");
else if (!origin.startsWith("https://")) problems.push(`index.html: canonical origin ${origin} is not https`);

const sitePath = (pathname) => (base === "/" ? pathname : pathname.slice(base.length - 1));
const fileForUrl = (url) => {
  const { pathname } = new URL(url);
  const local = path.join(dist, decodeURIComponent(sitePath(pathname)));
  return [path.join(local, "index.html"), local].find((c) => existsSync(c) && statSync(c).isFile());
};

for (const file of files) {
  const html = readFileSync(file, "utf8");
  const rel = path.relative(dist, file);
  const is404 = rel === "404.html";
  const fail = (message) => problems.push(`${rel}: ${message}`);

  if (!/<html[^>]*\slang="[a-z-]+"/i.test(html)) fail("<html> has no lang attribute");

  const title = html.match(/<title>([^<]*)<\/title>/)?.[1];
  if (!title) fail("no <title>");
  else {
    const text = decode(title).trim();
    if (text.length > 65) fail(`title is ${text.length} characters (over 65): ${text}`);
    if (!is404) (titles.get(text) ?? titles.set(text, []).get(text)).push(rel);
  }
  if (is404) {
    if (!/<meta[^>]*name="robots"[^>]*noindex/i.test(html)) fail("the 404 page is not marked noindex");
    if (/<link rel="canonical"/.test(html)) fail("the 404 page declares a canonical link");
    continue;
  }

  const description = meta(html, "name", "description");
  if (!description) fail("no meta description");
  else {
    if (description.length < 50 || description.length > 160) fail(`description is ${description.length} characters (want 50 to 160)`);
    (descriptions.get(description) ?? descriptions.set(description, []).get(description)).push(rel);
  }

  const h1 = (html.match(/<h1[\s>]/g) ?? []).length;
  if (h1 !== 1) fail(`${h1} <h1> elements (want exactly 1)`);

  if (/<meta[^>]*name="robots"[^>]*noindex/i.test(html)) fail("is marked noindex");

  const canonical = html.match(/<link rel="canonical" href="([^"]+)"/)?.[1];
  if (!canonical) fail("no canonical link");
  else {
    pageUrls.add(canonical);
    if (origin && !canonical.startsWith(`${origin}/`)) fail(`canonical ${canonical} is not on ${origin}`);
    if (!fileForUrl(canonical) || path.resolve(fileForUrl(canonical)) !== path.resolve(file)) fail(`canonical ${canonical} does not point at this page`);
    if (meta(html, "property", "og:url") !== canonical) fail("og:url differs from the canonical link");
  }

  if (!meta(html, "property", "og:title")) fail("no og:title");
  if (!meta(html, "property", "og:description")) fail("no og:description");
  if (meta(html, "name", "twitter:card") !== "summary_large_image") fail("twitter:card is not summary_large_image");
  for (const [attr, name] of [["property", "og:image"], ["name", "twitter:image"]]) {
    const image = meta(html, attr, name);
    if (!image) fail(`no ${name}`);
    else if (!/^https?:\/\//.test(image)) fail(`${name} is not an absolute URL: ${image}`);
    else if (!fileForUrl(image)) fail(`${name} points at a file that is not in dist: ${image}`);
  }
  if (!meta(html, "property", "og:image:alt")) fail("no og:image:alt");

  const blocks = [...html.matchAll(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/g)];
  if (blocks.length === 0) fail("no JSON-LD structured data");
  for (const [, body] of blocks) {
    try {
      const data = JSON.parse(body);
      if (data["@context"] !== "https://schema.org") fail("JSON-LD has no schema.org @context");
    } catch (error) {
      fail(`JSON-LD does not parse: ${error.message}`);
    }
  }

  for (const tag of html.matchAll(/<img\b[^>]*>/g)) {
    if (!/\salt="/.test(tag[0])) fail(`image without alt: ${tag[0].slice(0, 90)}`);
    if (!/\swidth="/.test(tag[0]) || !/\sheight="/.test(tag[0])) fail(`image without width and height (layout shift): ${tag[0].slice(0, 90)}`);
  }
}

for (const [text, pages] of titles) if (pages.length > 1) problems.push(`duplicate title "${text}": ${pages.join(", ")}`);
for (const [text, pages] of descriptions) if (pages.length > 1) problems.push(`duplicate description "${text.slice(0, 60)}...": ${pages.join(", ")}`);

// robots.txt must point at the sitemap, and the sitemap must list exactly the indexable pages.
const robots = existsSync(path.join(dist, "robots.txt")) ? readFileSync(path.join(dist, "robots.txt"), "utf8") : "";
if (!robots) problems.push("robots.txt is missing");
else {
  if (/^\s*Disallow:\s*\/\s*$/m.test(robots)) problems.push("robots.txt disallows the whole site");
  if (origin && !robots.includes(`Sitemap: ${origin}${base}sitemap-index.xml`)) problems.push("robots.txt does not point at the sitemap");
}
const sitemapFile = path.join(dist, "sitemap-0.xml");
if (!existsSync(sitemapFile)) problems.push("sitemap-0.xml is missing");
else {
  const listed = new Set([...readFileSync(sitemapFile, "utf8").matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]));
  for (const url of pageUrls) if (!listed.has(url)) problems.push(`sitemap is missing ${url}`);
  for (const url of listed) if (!pageUrls.has(url)) problems.push(`sitemap lists ${url}, which no built page claims as its canonical URL`);
}
if (!existsSync(path.join(dist, "favicon.ico"))) problems.push("favicon.ico is missing");
if (!existsSync(path.join(dist, "apple-touch-icon.png"))) problems.push("apple-touch-icon.png is missing");

if (problems.length) {
  console.error(problems.join("\n"));
  console.error(`\n${problems.length} SEO problem(s) in ${files.length} pages.`);
  process.exit(1);
}
console.log(`OK: ${files.length} pages checked, ${pageUrls.size} in the sitemap, site address ${origin}.`);
