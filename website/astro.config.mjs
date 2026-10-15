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
        "Documentation for Geass: your own cloud on your own servers. Install it, deploy apps and databases, connect your other clouds and operate the cluster.",
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
        { tag: "meta", attrs: { name: "theme-color", content: "#0a0a0a" } },
        { tag: "link", attrs: { rel: "icon", href: file("favicon.ico"), sizes: "48x48" } },
        { tag: "link", attrs: { rel: "apple-touch-icon", href: file("apple-touch-icon.png") } },
      ],
      sidebar: [
        {
          label: "Overview",
          items: [
            "docs/overview/introduction",
            "docs/overview/how-it-works",
            "docs/overview/concepts",
            "docs/overview/faq",
          ],
        },
        {
          label: "Getting started",
          items: [
            "docs/getting-started/requirements",
            "docs/getting-started/install",
            "docs/getting-started/sign-in",
            "docs/getting-started/add-servers",
            "docs/getting-started/first-app",
            "docs/getting-started/connect-domain",
          ],
        },
        {
          label: "Cloud connectors",
          items: [
            "docs/guides/connectors",
            "docs/guides/connectors/aws",
            "docs/guides/connectors/planetscale",
            "docs/guides/connectors/cloudflare",
            "docs/guides/connectors/github",
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
          ],
        },
        {
          label: "Data and storage",
          items: [
            "docs/guides/databases",
            "docs/guides/shared-database-servers",
            "docs/guides/object-storage",
            "docs/guides/backups-and-restore",
            "docs/guides/storage",
          ],
        },
        {
          label: "Run and scale",
          items: [
            "docs/guides/scaling-and-placement",
            "docs/guides/scale-to-zero",
            "docs/guides/compliance-tags",
            "docs/guides/network-isolation",
            "docs/guides/project-limits",
            "docs/guides/observability",
            "docs/guides/usage-and-capacity",
          ],
        },
        {
          label: "Integrations and access",
          items: [
            "docs/guides/command-line",
            "docs/guides/mcp-server",
            "docs/guides/users-and-tokens",
            "docs/guides/external-sign-in",
          ],
        },
        {
          label: "Operations",
          items: [
            "docs/operations/upgrading",
            "docs/operations/releases",
            "docs/operations/high-availability",
            "docs/operations/sizing",
            "docs/operations/multi-provider",
            "docs/operations/security",
            "docs/operations/troubleshooting",
            "docs/operations/uninstall",
          ],
        },
        {
          label: "Reference",
          items: [
            "docs/reference/rest-api",
            "docs/reference/cli",
            "docs/reference/mcp-tools",
            "docs/reference/configuration",
            { label: "Resources", items: [{ autogenerate: { directory: "docs/reference/resources" } }] },
            "docs/reference/glossary",
          ],
        },
      ],
    }),
  ],
});
