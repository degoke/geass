// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

// The public address. Canonical links, the sitemap, robots.txt and the Open Graph image are all built from it, so
// it has to be the real domain in production. Set SITE_URL to build for another address (a staging host, say):
// `SITE_URL=https://staging.example.com pnpm build`. SITE_BASE is only needed when the site is served from a sub-path.
const site = process.env.SITE_URL || "https://rungeass.com";
const base = process.env.SITE_BASE || "/";
const file = (path) => `${base.replace(/\/$/, "")}/${path}`;

export default defineConfig({
  site,
  base,
  trailingSlash: "always",
  compressHTML: false, // keep the whitespace between inline text and links exactly as authored
  integrations: [
    starlight({
      title: "Geass docs",
      description:
        "Documentation for Geass: your own cloud on your own servers. Install it, open the dashboard, and deploy apps, databases, and buckets.",
      logo: {
        light: "./src/assets/geass-logo-light.svg",
        dark: "./src/assets/geass-logo.svg",
        alt: "Geass",
        replacesTitle: true,
      },
      favicon: "/favicon.svg",
      social: [{ icon: "github", label: "GitHub", href: "https://github.com/degoke/geass" }],
      editLink: { baseUrl: "https://github.com/degoke/geass/edit/main/website/" },
      customCss: ["./src/styles/fonts.css", "./src/styles/docs.css"],
      expressiveCode: { styleOverrides: { borderRadius: "0" } },
      routeMiddleware: "./src/route-middleware.ts",
      head: [
        { tag: "meta", attrs: { name: "theme-color", content: "#ffffff" } },
        {
          // Starlight's theme picker changes data-theme, so the browser bar colour follows it, not the system.
          tag: "script",
          content:
            "(function(){var r=document.documentElement;function p(){var m=document.querySelector('meta[name=\"theme-color\"]');if(m)m.setAttribute('content',r.dataset.theme==='light'?'#ffffff':'#0a0a0a')}new MutationObserver(p).observe(r,{attributes:true,attributeFilter:['data-theme']});document.addEventListener('DOMContentLoaded',p)})()",
        },
        { tag: "link", attrs: { rel: "icon", href: file("favicon.ico"), sizes: "48x48" } },
        { tag: "link", attrs: { rel: "apple-touch-icon", href: file("apple-touch-icon.png") } },
      ],
      sidebar: [
        {
          label: "Get started",
          items: [
            "docs/overview/introduction",
            "docs/getting-started/requirements",
            "docs/getting-started/install",
            "docs/getting-started/sign-in",
            "docs/getting-started/first-app",
            "docs/getting-started/connect-domain",
            "docs/overview/concepts",
            "docs/overview/faq",
          ],
        },
        {
          label: "Build and deploy",
          items: [
            "docs/guides/projects-and-environments",
            "docs/guides/apps",
            "docs/guides/functions",
            "docs/guides/environment-variables",
            "docs/guides/github-and-builds",
            "docs/guides/pull-request-environments",
            "docs/guides/templates",
            "docs/guides/domains-and-tls",
            "docs/guides/moving-a-project",
          ],
        },
        {
          label: "Databases and files",
          items: [
            "docs/guides/databases",
            "docs/guides/shared-database-servers",
            "docs/guides/object-storage",
            "docs/guides/backups-and-restore",
            "docs/guides/storage",
          ],
        },
        {
          label: "Watch and scale",
          items: [
            "docs/guides/observability",
            "docs/guides/alerts",
            "docs/guides/usage-and-capacity",
            "docs/guides/scaling-and-placement",
            "docs/guides/scale-to-zero",
          ],
        },
        {
          label: "Connect your accounts",
          items: [
            "docs/guides/connectors",
            "docs/guides/connectors/github",
            "docs/guides/connectors/cloudflare",
            "docs/guides/connectors/aws",
            "docs/guides/connectors/planetscale",
            "docs/guides/connectors/clouds",
          ],
        },
        {
          label: "People and automation",
          items: [
            "docs/guides/users-and-tokens",
            "docs/guides/external-sign-in",
            "docs/guides/organizations-and-workspaces",
            "docs/guides/command-line",
            "docs/guides/mcp-server",
          ],
        },
        {
          label: "Servers",
          items: [
            "docs/getting-started/add-servers",
            "docs/guides/node-pools",
            "docs/operations/sizing",
            "docs/operations/high-availability",
            "docs/operations/upgrading",
            "docs/operations/uninstall",
          ],
        },
        {
          label: "Isolation and limits",
          collapsed: true,
          items: [
            "docs/guides/network-isolation",
            "docs/guides/compliance-tags",
            "docs/guides/outbound-limits",
            "docs/guides/project-limits",
          ],
        },
        {
          label: "Host Geass for others",
          collapsed: true,
          items: [
            "docs/guides/tenant-limits",
            "docs/guides/abuse-detection",
            "docs/guides/mail-relay",
            "docs/operations/managed-mode",
            "docs/operations/hosted-control-plane",
            "docs/operations/dashboard-only",
            "docs/operations/sandboxed-runtime",
            "docs/operations/registry-isolation",
          ],
        },
        {
          label: "Under the hood",
          collapsed: true,
          items: [
            "docs/overview/how-it-works",
            "docs/operations/installer",
            "docs/operations/troubleshooting",
            "docs/operations/disaster-recovery",
            "docs/operations/security",
            "docs/operations/multi-provider",
            "docs/operations/mesh-networking",
            "docs/operations/releases",
            "docs/reference/configuration",
            { label: "Resources", collapsed: true, items: [{ autogenerate: { directory: "docs/reference/resources" } }] },
          ],
        },
        {
          label: "Reference",
          collapsed: true,
          items: ["docs/reference/cli", "docs/reference/rest-api", "docs/reference/mcp-tools", "docs/reference/glossary"],
        },
      ],
    }),
  ],
});
