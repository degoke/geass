// The library build: what another app imports as "@geass/dashboard-ui" (see package.json "exports"). The standalone
// dashboard that the operator embeds is built by vite.config.js and is not touched by this one.
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";
import pkg from "./package.json" with { type: "json" };

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const srcDir = path.resolve(__dirname, "src") + path.sep;

/** The element the dashboard renders into; every rule of styles.css is placed under it. */
export const SCOPE = ".geass-dashboard";

// What the host app provides or installs: the build leaves these as imports, so that there is one React, one Mantine and
// one router on the page. A stylesheet of one of them (xterm's, say) is the exception: it is bundled into style.css.
const packages = [...Object.keys(pkg.dependencies || {}), ...Object.keys(pkg.peerDependencies || {})];
const external = (id) => !id.endsWith(".css") && packages.some((name) => id === name || id.startsWith(`${name}/`));

/**
 * Places the dashboard's own stylesheets under SCOPE, so that loading them into another app restyles only the dashboard.
 * styles.css was written for a page of its own: its tokens are on :root, and it styles html, body and Mantine's classes.
 * - :root and body become the scope itself, and :root[data-mantine-color-scheme="dark"] becomes the scope under that
 *   attribute, which the host's Mantine sets on <html>;
 * - html and #root are dropped, since the host owns them;
 * - every other selector is placed below the scope. Menus and dialogs are rendered inside it (see GeassDashboard).
 * Stylesheets of packages are left as they are: their classes are their own.
 */
function scopeUnder(scope) {
  const scopeSelector = (selector) => {
    const s = selector.trim();
    if (s === "html" || s === "#root") return [];
    if (s === "body" || s === ":root") return [scope];
    const onRoot = /^(?:html)?:root(\[[^\]]+\])(.*)$/.exec(s);
    if (onRoot) return [`${onRoot[1]} ${scope}${onRoot[2]}`];
    if (s.startsWith(":root")) return [`${scope}${s.slice(":root".length)}`];
    return [`${scope} ${s}`];
  };
  const own = (file) => file.startsWith(srcDir) && file.endsWith(".css") && !file.endsWith(`${path.sep}embedded.css`);
  return {
    postcssPlugin: "geass-scope-under",
    Once(root, { result }) {
      const file = (result.opts.from || "").split("?")[0];
      if (!own(file)) return;
      root.walkRules((rule) => {
        if (rule.parent?.type === "atrule" && /keyframes$/i.test(rule.parent.name)) return;
        const selectors = rule.selectors.flatMap(scopeSelector);
        if (selectors.length) rule.selectors = [...new Set(selectors)];
        else rule.remove();
      });
    },
  };
}

export default defineConfig({
  plugins: [react()],
  publicDir: false,
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
  css: { postcss: { plugins: [scopeUnder(SCOPE)] } },
  build: {
    outDir: "dist-lib",
    emptyOutDir: true,
    sourcemap: true,
    cssCodeSplit: false,
    // A library is minified by the app that uses it, with the rest of its code.
    minify: false,
    cssMinify: false,
    lib: { entry: path.resolve(__dirname, "src/index.js"), formats: ["es"], fileName: "index", cssFileName: "style" },
    rollupOptions: { external },
  },
});
