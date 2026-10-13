# Geass website

The public site: a landing page at `/` and the documentation at `/docs/`. It is a static [Astro](https://astro.build)
site, and the docs use [Starlight](https://starlight.astro.build) (sidebar, search, table of contents, dark and light
themes). It is separate from the product: nothing here ships in the operator image or the installer.

## Develop

```bash
cd website
pnpm install
pnpm dev        # http://localhost:4321
pnpm build      # static output in dist/ (also builds the search index)
pnpm preview    # serve dist/
pnpm links      # check every internal link and heading anchor in dist/ (run after pnpm build)
```

## Deploy

Publish `dist/` to any static host (GitHub Pages, Cloudflare Pages, Netlify, an object store). Set the public
address at build time so canonical links, the sitemap and the Open Graph image are absolute:

```bash
SITE_URL=https://example.com pnpm build
# only if the site lives under a sub-path:
SITE_URL=https://example.com SITE_BASE=/geass/ pnpm build
```

Without `SITE_URL` the site still builds, but link scrapers will ignore the relative `og:image`.

## Layout

```
astro.config.mjs               Starlight config and the docs sidebar (add new pages here)
src/pages/index.astro          Landing page, assembled from src/components/landing/*
src/layouts/Landing.astro      Head, fonts, meta and the shared page frame for the landing page
src/styles/landing.css         Landing page styles ("The Contract" tokens)
src/styles/docs.css            Starlight theme overrides (same tokens)
src/scripts/landing.js         Copy button and the tabbed demo
src/content/docs/docs/**       The documentation (Markdown)
src/assets/                    Logos, fonts, the dashboard screenshot, the Open Graph card
public/favicon.svg             Favicon
scripts/check-links.mjs        Internal link checker
```

Brand art comes from `geass-brand/` (regenerate it with `geass-brand/tools/build_brand.py`, then copy the files
you need into `src/assets/`). `src/assets/og.png` is `geass-brand/social/geass-social-card.png`.
`src/assets/dashboard.webp` is a capture of the real dashboard (1440x900 at 2x), not a mockup: recapture it when
the dashboard's look changes.

## Writing the docs

The docs describe what Geass does **today**. They are for someone who runs their own servers: a developer or
operator who wants to know what a feature is for, how to use it, what it does under the hood, and what to do
when it misbehaves.

### Accuracy comes first

- Every statement about behavior, defaults, limits, field names, flags, paths, environment variables and UI
  labels must be checked against the code in this repository (controllers in `internal/controller`, the API in
  `internal/dashboard`, the types in `api/v1alpha1`, the installer in `pkg`, the dashboard in `dashboard-ui/src`).
  If the code does not confirm it, leave it out.
- Say plainly when something is experimental, partial or not built, using a `:::caution` aside.
- Never document features that do not exist: check the code first. The automation surface is the versioned `/api/v1`
  API with API tokens, the `geass` command line (`internal/cli`) and the MCP server (`geass mcp`,
  `internal/mcpserver`). Sample commands and tool names, on the landing page and in the docs, must match what
  `geass --help` and `internal/mcpserver/tools.go` really provide.
- Use the exact label the dashboard shows, in bold: **Settings**, **Deploy changes**. Read the label in
  `dashboard-ui/src` instead of guessing.
- Do not use marketing language. No "simply", "just", "easy", "powerful", "seamless".

### Voice

Second person, present tense, active voice, short sentences. Say what a thing is for before how it works. Prefer a
concrete example to an abstract description. Spell out a number or limit when there is one.

### Page shape

Every page is a Markdown file with a `title` and a one-sentence `description` (under 160 characters, it becomes the
meta description). The page title is the H1, so start body headings at `##`, in sentence case.

1. An opening paragraph (2 to 4 sentences): what this is for and when you would use it.
2. **Before you start**, if there are prerequisites (a connection, a role, a node).
3. The common tasks, each as a `##` section with numbered steps in the dashboard. When the same thing can be done
   with a manifest or the API, show that too, with a runnable example.
4. Options, fields or limits as a table (name, meaning, default).
5. **What happens under the hood**, for operators who need to debug: the Kubernetes objects, namespaces and
   resource names Geass creates, and the `kubectl` commands to inspect them. Keep it short and exact.
6. **Troubleshooting**: specific symptoms, each with its cause and fix.
7. **Related**: links to the neighbouring pages.

Not every page needs every section, but a reader should never be left without the next step.

### Formatting

- Code fences always carry a language (`bash`, `yaml`, `json`, `text`) and, for files, a title:
  ` ```yaml title="app.yaml" `.
- Asides: `:::note`, `:::tip`, `:::caution`, `:::danger` (closed with `:::`).
- Link other pages with an absolute path that ends in a slash, for example `[Databases](/docs/guides/databases/)`,
  and only to pages that exist. Anchors follow the heading text in lowercase with hyphens.
- Use documentation-only values in examples: `example.com`, `203.0.113.10`, `acme/storefront`.
- Do not add images in Markdown unless a screenshot exists in `src/assets/docs/`.

### Adding a page

Create the Markdown file under `src/content/docs/docs/<section>/`, then add its slug to the `sidebar` in
`astro.config.mjs`. `pnpm build` fails on a sidebar entry without a page, and `pnpm links` fails on a broken link.
