// Checks every internal link and heading anchor in the built site (dist/). Run after `pnpm build`.
// External links are not fetched. Exits non-zero when anything is broken.
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

const idsByFile = new Map();
const files = [...walk(dist)];
for (const file of files) {
  const html = readFileSync(file, "utf8");
  idsByFile.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
}

function resolveTarget(from, href) {
  let target = href;
  if (target.startsWith(base) && base !== "/") target = "/" + target.slice(base.length);
  const [pathname, hash = ""] = target.split("#");
  const clean = decodeURIComponent(pathname.split("?")[0]);
  let abs;
  if (clean === "") abs = from;
  else if (clean.startsWith("/")) abs = path.join(dist, clean);
  else abs = path.resolve(path.dirname(from), clean);
  const candidates = abs.endsWith(".html") ? [abs] : [path.join(abs, "index.html"), abs + ".html", abs];
  const found = candidates.find((c) => existsSync(c) && statSync(c).isFile());
  return { found, hash: decodeURIComponent(hash) };
}

const problems = [];
let checked = 0;
for (const file of files) {
  const html = readFileSync(file, "utf8");
  const rel = path.relative(dist, file);
  for (const m of html.matchAll(/\s(?:href|src)="([^"]*)"/g)) {
    const href = m[1].replace(/&amp;/g, "&");
    if (!href || /^(https?:|mailto:|tel:|data:|javascript:|\/\/)/.test(href)) continue;
    checked++;
    const { found, hash } = resolveTarget(file, href);
    if (!found) {
      problems.push(`${rel}: missing target ${href}`);
    } else if (hash && found.endsWith(".html") && !idsByFile.get(found)?.has(hash)) {
      problems.push(`${rel}: missing anchor ${href}`);
    }
  }
}

if (problems.length) {
  const unique = [...new Set(problems)];
  console.error(unique.join("\n"));
  console.error(`\n${unique.length} broken link(s) among ${checked} checked in ${files.length} pages.`);
  process.exit(1);
}
console.log(`OK: ${checked} internal links and anchors checked in ${files.length} pages.`);
