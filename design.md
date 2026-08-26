# Geass Design Guide

> Cursor-minimal clarity for infrastructure you control.

This document defines the visual and interaction system for the Geass brand, marketing website, documentation, installer, and cloud control plane.

## 1. Design direction

Geass should feel calm, precise, capable, and technically credible. It reduces the apparent complexity of K3s without hiding important infrastructure state.

The visual direction combines:

- near-monochrome, low-noise surfaces;
- spacious composition and disciplined typography;
- familiar developer-tool interaction patterns;
- compact operational data where density is useful;
- restrained colour reserved for meaning and action.

### Design principles

1. **Quiet by default.** The interface stays visually neutral until something needs attention.
2. **State before decoration.** Health, change, risk, ownership, and consequence are clearer than branding.
3. **Progressive disclosure.** Start with the operational summary; reveal manifests, events, and raw details on demand.
4. **Dense, not cramped.** Infrastructure tables may carry substantial information, but alignment and spacing must preserve scanability.
5. **One obvious action.** A view should have no more than one visually dominant action.
6. **Trust through predictability.** Destructive, irreversible, and externally visible actions always state their effect.
7. **Keyboard capable.** Common navigation and operations must work without a pointer.

### Personality

Geass is:

- composed, not sterile;
- technical, not intimidating;
- premium, not ornamental;
- direct, not abrupt;
- sovereign, not militaristic;
- powerful, not loud.

## 2. Colour system

Dark mode is the primary Geass experience. Light mode is supported for accessibility, documentation, and user preference.

### Dark palette

| Token | Value | Use |
| --- | --- | --- |
| `--bg-canvas` | `#0A0A0B` | Application and page background |
| `--bg-subtle` | `#0E0E10` | Sidebar and subtle sections |
| `--bg-surface` | `#121214` | Cards, popovers, code blocks |
| `--bg-elevated` | `#18181B` | Menus, dialogs, floating surfaces |
| `--bg-hover` | `#1D1D21` | Neutral hover state |
| `--bg-active` | `#242429` | Selected or pressed state |
| `--border-subtle` | `#202024` | Section separation |
| `--border-default` | `#2A2A30` | Controls and cards |
| `--border-strong` | `#3A3A42` | Focused structural emphasis |
| `--text-primary` | `#F2F2F3` | Main copy and values |
| `--text-secondary` | `#A3A3AB` | Supporting text |
| `--text-muted` | `#6F6F78` | Metadata and placeholders |
| `--text-disabled` | `#4D4D54` | Disabled content |

### Light palette

| Token | Value | Use |
| --- | --- | --- |
| `--bg-canvas` | `#FFFFFF` | Application and page background |
| `--bg-subtle` | `#FAFAFA` | Sidebar and subtle sections |
| `--bg-surface` | `#F6F6F7` | Cards and code blocks |
| `--bg-elevated` | `#FFFFFF` | Menus and dialogs |
| `--bg-hover` | `#F0F0F2` | Neutral hover state |
| `--bg-active` | `#E8E8EB` | Selected or pressed state |
| `--border-subtle` | `#ECECEF` | Section separation |
| `--border-default` | `#DCDCE1` | Controls and cards |
| `--border-strong` | `#C4C4CB` | Focused structural emphasis |
| `--text-primary` | `#171719` | Main copy and values |
| `--text-secondary` | `#5F5F67` | Supporting text |
| `--text-muted` | `#85858E` | Metadata and placeholders |
| `--text-disabled` | `#B2B2B8` | Disabled content |

### Brand accent

Geass uses a cool blue-violet accent. It should occupy less than 10% of most screens.

| Token | Dark | Light | Use |
| --- | --- | --- | --- |
| `--accent` | `#8B8CF8` | `#5B5BD6` | Primary action, focus, selected state |
| `--accent-hover` | `#9D9EFA` | `#4F4FC4` | Hover |
| `--accent-subtle` | `#8B8CF81A` | `#5B5BD614` | Tinted backgrounds |
| `--accent-border` | `#8B8CF84D` | `#5B5BD640` | Selected borders |

Do not use a large purple gradient as a default background. A faint radial accent may appear once in a marketing hero or onboarding screen, never behind operational data.

### Semantic colours

| State | Foreground | Subtle background | Meaning |
| --- | --- | --- | --- |
| Healthy / success | `#59C77A` | `#59C77A14` | Running, connected, complete |
| Warning | `#D9A441` | `#D9A44114` | Degraded, approaching a limit |
| Critical / danger | `#E66A6A` | `#E66A6A14` | Failed, unavailable, destructive |
| Information | `#67A6E8` | `#67A6E814` | Neutral system information |
| Pending | `#A3A3AB` | `#A3A3AB12` | Queued, paused, unknown |

Never communicate state by colour alone. Pair colour with a label, icon, shape, or position. Reserve red for actual failure, danger, or destructive action.

## 3. Typography

### Font families

- **Interface and marketing:** Inter Variable.
- **Code and technical data:** Geist Mono or IBM Plex Mono.
- **Fallback:** `Inter, ui-sans-serif, system-ui, sans-serif` and `"Geist Mono", "IBM Plex Mono", ui-monospace, monospace`.

Use monospace for commands, YAML, logs, IP addresses, ports, hashes, versions, resource quantities, and stable identifiers. Do not use monospace for paragraphs, navigation, buttons, or ordinary labels.

### Type scale

| Role | Size / line height | Weight | Tracking |
| --- | --- | --- | --- |
| Marketing display | `56 / 60px` | 550 | `-0.035em` |
| Page display | `40 / 44px` | 550 | `-0.03em` |
| H1 | `32 / 38px` | 550 | `-0.025em` |
| H2 | `24 / 30px` | 550 | `-0.02em` |
| H3 | `18 / 24px` | 550 | `-0.01em` |
| Large body | `16 / 26px` | 400 | `-0.005em` |
| Body | `14 / 22px` | 400 | `0` |
| Small | `13 / 19px` | 400 | `0` |
| Caption | `12 / 17px` | 450 | `0.01em` |
| Overline | `11 / 16px` | 600 | `0.06em` |
| Code | `13 / 20px` | 400 | `0` |

Use sentence case. Avoid all caps except tiny overlines and fixed technical abbreviations. Use tabular numerals in metrics and tables.

## 4. Spacing and layout

### Base spacing scale

Use a 4px base grid:

`0, 4, 8, 12, 16, 20, 24, 32, 40, 48, 64, 80, 96, 128`

- 4–8px: icon gaps, compact metadata, inline controls.
- 12–16px: control padding and table rhythm.
- 20–24px: cards and grouped content.
- 32–48px: major application sections.
- 64–128px: marketing sections.

### Application shell

- Sidebar: 232px expanded, 56px collapsed.
- Top bar: 48px.
- Context sub-navigation: 40px.
- Main content maximum: 1440px, centered only on wide displays.
- Standard page padding: 24px desktop, 16px tablet, 12px mobile.
- Operational pages may use full available width.

### Marketing grid

- Maximum content width: 1200px.
- 12 columns with 24px gutters.
- Page gutters: 24px mobile, 40px tablet, 64px desktop.
- Hero text width: 720px maximum.
- Paragraph width: 680px maximum.

### Radius, border, and shadow

- Small controls: 6px radius.
- Buttons and inputs: 7px radius.
- Cards and panels: 8px radius.
- Dialogs: 10px radius.
- Pills and status chips: full radius.
- Default border: 1px.
- Shadows are reserved for floating layers, never ordinary cards.

```css
--shadow-popover: 0 12px 36px rgb(0 0 0 / 35%), 0 0 0 1px rgb(255 255 255 / 5%);
--shadow-dialog: 0 24px 80px rgb(0 0 0 / 50%), 0 0 0 1px rgb(255 255 255 / 7%);
```

## 5. Core components

### Buttons

Heights: 28px compact, 32px default, 40px large.

- **Primary:** accent fill, high-contrast text. One per visual region.
- **Secondary:** surface fill with default border.
- **Ghost:** transparent; use in toolbars and navigation.
- **Danger:** neutral by default; becomes red on confirmation or when danger is the meaning.
- **Icon button:** always has an accessible name and tooltip.

Loading buttons preserve width and replace the leading icon with a spinner. Disabled controls must remain legible and must explain why when the reason is not obvious.

### Inputs

- Default height: 32px; large form fields: 40px.
- Labels appear above fields; placeholders are examples, not labels.
- Focus uses a 1px accent border plus a subtle 2px accent ring.
- Validation appears after interaction or submission, not while users are still typing.
- Secrets are masked by default with explicit reveal and copy actions.

### Cards and panels

Cards group related information; they are not the default wrapper for every section.

- Prefer a heading, whitespace, and divider for simple page structure.
- Use cards for independent resources, summaries, choices, or movable modules.
- Avoid nested cards. Use inset rows or subsections inside a card.
- Card headers place title and description left, actions right.

### Tables

Tables are the primary pattern for services, nodes, deployments, databases, volumes, domains, and events.

- Header: 32px; row: 40px default or 32px compact.
- First column identifies the resource and may be sticky.
- Align numbers right; text left; status consistently.
- Use row hover, not zebra striping.
- Put infrequent actions in a trailing overflow menu.
- Support filtering, search, sorting, keyboard navigation, and column selection on large datasets.
- On narrow screens, reduce columns and open the selected resource in a detail view; do not turn every table into unrelated cards.

### Status badges

Badges combine a 6px dot or small icon with a concise label: `Healthy`, `Degraded`, `Failed`, `Pending`, `Paused`, or `Unknown`.

Do not invent different status words for different resource types when the operational meaning is the same.

### Tabs

Use tabs for sibling views of the same resource: `Overview`, `Deployments`, `Networking`, `Storage`, `Events`, `Settings`.

- Use an underline or subtle selected surface, never large coloured pills.
- Keep seven or fewer visible tabs; move rare sections to an overflow menu.
- Tabs must preserve resource context.

### Dialogs, drawers, and pages

- Use a dialog for a short, focused decision.
- Use a right drawer for inspecting supporting details without losing context.
- Use a full page for creation flows, complex configuration, and anything with multiple sections.
- Destructive confirmation names the exact resource and consequence.

### Toasts and banners

- Toast: confirms a completed background action; disappears but remains in activity history.
- Inline alert: explains a local issue within a form or resource.
- Page banner: reserved for system-wide or page-wide conditions.
- Do not use a success toast merely for navigation.

### Code, commands, and logs

- Code surfaces use `--bg-canvas` or a slightly darker inset surface.
- Commands include a copy action and clearly separate prompt from output.
- Logs are monospace, virtualized, searchable, filterable, and pausable.
- Severity may colour timestamps or indicators; never colour entire log lines by default.
- Preserve raw text and allow wrapping to be toggled.

### Charts and metrics

- Prefer direct values and compact sparklines over decorative charts.
- Use line charts for time, bars for comparisons, and gauges only for true bounded capacity.
- Never use 3D charts, gradients under every line, or multiple near-identical accent colours.
- Label units and time ranges. Tooltips expose exact values.

## 6. Dashboard patterns

### Information hierarchy

Every resource overview follows this order:

1. Identity and current status.
2. Primary actions.
3. Health, capacity, and recent change.
4. Child resources or workloads.
5. Events and operational history.
6. Configuration and raw manifests.

### Global shell

The control plane uses a Railway-inspired layout with Geass visual tokens: a fixed 232px left sidebar, full-width main content, and no decorative gradients behind operational data.

Primary navigation (sidebar):

- **Projects** — workspace list and project cards (default landing)
- **Settings** — platform configuration, HA readiness, cloud connections

Place the Geass wordmark at the top of the sidebar. Put theme preference at the bottom. Account and organisation switching will appear above navigation when authentication ships.

The projects search field accepts `Cmd/Ctrl + K` to focus. A full command palette will extend this later.

### UI implementation

The dashboard is server-rendered HTML with HTMX. Do not use DaisyUI, Tailwind CDN, or third-party component libraries.

**CSS** lives in `internal/dashboard/static/` and is embedded into the operator binary:

- `tokens.css` — design tokens (keep in sync with this document)
- `base.css` — reset, typography, layout utilities
- `shell.css` — sidebar and application shell
- `components.css` — buttons, cards, forms, tables, tabs, badges, alerts
- `pages.css` — page-specific styles (e.g. projects grid)

**Components** are Go helpers in `internal/dashboard/components.go` (`Button`, `Card`, `Field`, `Input`, `Select`, `Tabs`, `Breadcrumbs`, `Badge`, `Alert`, `Table`, `PageHeader`). Handlers compose pages from these helpers rather than inline DaisyUI class strings.

When adding a new page, use the component helpers first. Migrate legacy inline markup opportunistically.

### Projects list

The projects page is the default landing view (`/` and `/projects`). It follows Railway's card-grid pattern adapted to Geass:

**Header**

- Page title: `Projects`
- Search field with `⌘K` hint (filters cards client-side)
- Primary action: `+ New` (accent button, one per region)

**Toolbar**

- Project count (`1 Project`, `3 Projects`)
- Sort: Recent activity (default) or Name

**Project cards**

Each card is a single link to `/projects/{name}`:

- **Preview area** — dot-grid canvas with resource-type icons for apps, databases, caches, and object stores. Icons reflect health (green border = ready, amber = unknown, red = not ready). Empty projects show "No services deployed".
- **Footer** — display name, status dot, and summary line: `{environment} · {healthy}/{total} services online`.

Cards sort by recent activity by default. Do not show cluster name or raw CRD status on the card surface; those belong in the project workspace.

**Empty state**

Centered dashed panel with one sentence of context and a single `Create your first project` action.

### Fleet overview

Show:

- fleet health and active incidents;
- clusters, nodes, and capacity;
- recent deployments and failures;
- services requiring attention;
- cost or provider inventory when available.

Do not fill the landing dashboard with equal-weight statistic cards. Lead with exceptions and active work; keep totals compact.

### Cluster overview

Header:

- cluster name and environment;
- health badge;
- region/provider and K3s version;
- last update;
- primary action or overflow menu.

Body:

- nodes and workloads table;
- CPU, memory, disk, and pod capacity;
- recent events and deployments;
- networking and storage summaries.

### Service overview

Lead with deployment state and endpoint. Then show replicas, source/image, version, resource use, recent deployment, logs, domains, variables, and storage.

The default service action should follow context: `Deploy`, `Redeploy`, `Resume`, or `View failure`—not a permanent generic action.

### Creation and deployment flows

Use a reviewable sequence:

1. Choose source.
2. Configure build or image.
3. Set environment and resources.
4. Configure networking and storage.
5. Review.
6. Deploy with visible streaming progress.

Keep advanced Kubernetes configuration available but collapsed. Show generated configuration before applying it.

### Empty states

An empty state explains:

- what belongs here;
- why it is useful;
- the single next action;
- an optional CLI equivalent.

Use minimal line illustration only when it improves comprehension. Avoid mascots and oversized generic cloud art.

### Responsive behaviour

- At 1024px, collapse the sidebar and reduce secondary metadata.
- Below 768px, use a compact header, drawers, and priority columns.
- Charts stack vertically; tables keep identity, status, and primary value.
- Complex manifests and logs remain usable with horizontal scrolling.
- Never hide critical status or destructive consequences on small screens.

## 7. Marketing website direction

### Overall composition

The website is editorial and product-led: large precise typography, abundant dark negative space, real interface imagery, short evidence-backed copy, and almost no decorative illustration.

### Homepage structure

1. **Navigation:** Geass wordmark, Product, Docs, Pricing or Editions, GitHub, Sign in, Get started.
2. **Hero:** one clear promise, one supporting paragraph, primary install/get-started action, secondary GitHub/docs action.
3. **Product proof:** a large real control-plane screenshot or interactive product frame.
4. **Core workflow:** provision, deploy, connect, observe, scale.
5. **Capabilities:** applications, databases, storage, networking, clusters.
6. **Why Geass:** ownership, cost control, portability, lightweight K3s foundation.
7. **Technical proof:** architecture, supported systems, deployment modes, security posture.
8. **Final action:** concise installation or get-started block.

### Hero direction

- Background: near-black canvas.
- Heading: 56–72px on large displays, maximum 12–14 words.
- Copy: 18px, maximum 2–3 lines at desktop width.
- One primary and one secondary action.
- Product UI begins within the first viewport or immediately below it.
- Optional background effect: extremely faint blue-violet radial glow or fine grid, under 8% opacity.

### Product imagery

Use actual or carefully maintained interface captures. Keep them straight-on and sharp. Avoid floating perspective screens, device frames, fake terminal noise, glowing server racks, and generic AI/cloud imagery.

### Documentation

- Three-column structure where space allows: navigation, article, local outline.
- Reading width: 720px.
- Persistent code copy actions.
- Clear version and edition labels.
- Examples begin with the shortest successful path, followed by explanation and advanced options.

## 8. Iconography and imagery

### Icon system

Use Lucide as the initial interface icon set, with custom icons only for Geass-specific resources that Lucide cannot represent clearly.

- 16px default, 14px compact, 20px prominent.
- 1.5px stroke at 16–20px.
- Rounded line caps and joins.
- Icons inherit text colour.
- Filled icons are limited to selected states and severe status.
- Pair unfamiliar icons with labels.

Do not mix icon families or use Kubernetes vendor logos as generic resource icons.

### Custom resource icons

Custom icons should share:

- a 24px grid;
- 1.5px strokes;
- simple geometric construction;
- no internal gradients;
- recognisable silhouettes at 16px;
- neutral default colour.

Possible custom concepts: cluster, service, managed database, volume, ingress, environment, and node pool.

### Illustration

Illustration is rare. When needed, use precise monochrome topology drawings: nodes, connections, layers, or deployment flow. Use the accent colour for a single active path.

## 9. Motion and interaction

- Micro-interactions: 120–160ms.
- Menus and popovers: 140ms.
- Drawers and dialogs: 180–220ms.
- Use ease-out for entering and ease-in for leaving.
- Avoid springy motion in operational views.
- Respect `prefers-reduced-motion`.
- Never animate live metrics so aggressively that values are difficult to read.

Deployment progress should feel continuous and honest: show queued, pulling/building, scheduling, starting, checking, and healthy or failed states. Never display fake progress percentages.

## 10. Accessibility

- Target WCAG 2.2 AA.
- Body text and controls require at least 4.5:1 contrast; large text at least 3:1.
- Focus indicators must remain visible on every surface.
- Minimum pointer target: 32×32px in dense desktop UI, preferably 40×40px for touch.
- Every icon-only action needs an accessible name.
- Dialogs trap focus and return it to the trigger.
- Tables expose headers and sorting state semantically.
- Live deployment and health changes use polite announcements; critical failures may be assertive.
- Do not rely on hover for necessary information.

## 11. Brand rules

### Brand idea

Geass turns infrastructure you own into a coherent cloud. The brand should express control, coordination, and clarity—not domination, fantasy spectacle, or Kubernetes complexity.

### Name and casing

- Product name: **Geass**.
- Sentence example: “Deploy with Geass.”
- CLI and technical identifiers: `geass`.
- Environment variables: `GEASS_*`.
- Avoid `GEASS`, `geAss`, or pluralising the product name in marketing copy.

### Voice

Use concise, concrete language:

- say what happened;
- identify the affected resource;
- explain consequence and recovery;
- prefer verbs over abstract nouns;
- avoid hype such as “revolutionary,” “effortless,” or “infinite scale.”

Good: “The deployment failed because the readiness check timed out.”

Weak: “Oops! Something went wrong with your amazing deployment.”

### Logo direction

The future Geass mark should be geometric, compact, and legible at 16px. Suitable concepts include a controlled node, a connected `G`, a cluster boundary, or layered planes. It should work in one colour before accent versions are created.

Required configurations:

- wordmark;
- horizontal lockup;
- symbol;
- light and dark monochrome;
- app icon and favicon.

Clear space should equal at least the symbol’s internal stroke or one quarter of its height. Never add glow, bevel, drop shadow, outline, or gradient to the primary logo.

### Brand usage

- Use the accent for signature moments, not entire pages.
- Let interface screenshots demonstrate the product.
- Prefer neutral photography-free communication.
- Do not imitate Cursor’s logo, proprietary assets, exact layouts, or copy. Cursor Minimal is a design influence, while Geass maintains an independent identity.

## 12. Content and interface copy

- Buttons begin with verbs: `Create cluster`, `Deploy service`, `Add domain`.
- Use `Delete`, not vague alternatives such as `Remove`, when data will be deleted.
- Dates display in the user’s locale; technical event timelines may include UTC.
- Show relative time with exact time on hover or focus.
- Resource names preserve user casing.
- Errors follow: **what happened → why → what to do next**.
- Confirmation copy names the resource and whether recovery is possible.

## 13. Design tokens starter

```css
:root {
  --font-sans: Inter, ui-sans-serif, system-ui, sans-serif;
  --font-mono: "Geist Mono", "IBM Plex Mono", ui-monospace, monospace;

  --bg-canvas: #0a0a0b;
  --bg-subtle: #0e0e10;
  --bg-surface: #121214;
  --bg-elevated: #18181b;
  --bg-hover: #1d1d21;
  --bg-active: #242429;

  --border-subtle: #202024;
  --border-default: #2a2a30;
  --border-strong: #3a3a42;

  --text-primary: #f2f2f3;
  --text-secondary: #a3a3ab;
  --text-muted: #6f6f78;

  --accent: #8b8cf8;
  --accent-hover: #9d9efa;
  --success: #59c77a;
  --warning: #d9a441;
  --danger: #e66a6a;
  --info: #67a6e8;

  --radius-control: 7px;
  --radius-panel: 8px;
  --radius-dialog: 10px;

  --space-1: 4px;
  --space-2: 8px;
  --space-3: 12px;
  --space-4: 16px;
  --space-5: 20px;
  --space-6: 24px;
  --space-8: 32px;
  --space-10: 40px;
  --space-12: 48px;
  --space-16: 64px;
}
```

## 14. Review checklist

Before shipping a Geass screen, verify:

- Is the current resource and environment obvious?
- Is the most important state visible without opening another view?
- Is there only one dominant action?
- Does every semantic colour also have a label or icon?
- Are advanced details available without overwhelming the default view?
- Are tables aligned and scannable?
- Are destructive consequences explicit?
- Does keyboard focus remain visible?
- Does the screen still work at narrow widths?
- Is accent colour being used for meaning rather than decoration?
- Could any card be replaced by spacing and a divider?
- Does the result feel like Geass rather than a copy of another developer tool?
