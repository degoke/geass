// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

// The public address is not decided yet. Set SITE_URL at build time (for example
// `SITE_URL=https://geass.dev pnpm build`) and the canonical links, sitemap and Open Graph image
// become absolute. SITE_BASE is only needed when the site is served from a sub-path.
const site = process.env.SITE_URL || undefined;
const base = process.env.SITE_BASE || "/";

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
      head: [{ tag: "meta", attrs: { name: "theme-color", content: "#0a0a0a" } }],
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
            "docs/guides/observability",
            "docs/guides/usage-and-capacity",
          ],
        },
        {
          label: "Integrations and access",
          items: [
            "docs/guides/command-line",
            "docs/guides/mcp-server",
            "docs/guides/connections",
            "docs/guides/users-and-tokens",
          ],
        },
        {
          label: "Operations",
          items: [
            "docs/operations/upgrading",
            "docs/operations/high-availability",
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
