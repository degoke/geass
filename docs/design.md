# Geass Design Guide

> Absolute command over servers you already own.

This document defines the visual and interaction system for the Geass brand, marketing website, documentation, installer, and cloud control plane. The system is called **The Contract**: a black grid, square blocks, and a single green accent. Tokens live in `geass-brand/tokens/`, brand art in `geass-brand/`, the dashboard theme in `dashboard-ui/src/theme.js`, and the landing page and documentation in `website/` (an Astro site; the docs use Starlight).

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
| `--bg-canvas` | `#0A0A0A` | Application and page background |
| `--bg-subtle` | `#0F0F0F` | Sidebar and subtle sections |
| `--bg-surface` | `#151515` | Cards, popovers, code blocks |
| `--bg-elevated` | `#1A1A1A` | Menus, dialogs, floating surfaces |
| `--bg-hover` | `#222222` | Neutral hover state |
| `--bg-active` | `#2A2A2A` | Selected or pressed state |
| `--border-subtle` | `#262626` | Section separation |
| `--border-default` | `#333333` | Controls and cards |
| `--border-strong` | `#5A5A5A` | Focused structural emphasis |
| `--text-primary` | `#F2F2F2` | Main copy and values |
| `--text-secondary` | `#A1A1A1` | Supporting text |
| `--text-muted` | `#8F8F8F` | Metadata and placeholders |
| `--text-disabled` | `#5E5E5E` | Disabled content |

Terminals and other deep insets use pure `#000000`.

### Light palette

| Token | Value | Use |
| --- | --- | --- |
| `--bg-canvas` | `#FFFFFF` | Application and page background |
| `--bg-subtle` | `#F4F4F2` | Sidebar and subtle sections |
| `--bg-surface` | `#FFFFFF` | Cards and code blocks |
| `--bg-elevated` | `#FFFFFF` | Menus and dialogs |
| `--bg-hover` | `#F4F4F2` | Neutral hover state |
| `--bg-active` | `#ECEBE7` | Selected or pressed state |
| `--border-subtle` | `#E3E2DE` | Section separation |
| `--border-default` | `#D5D4CF` | Controls and cards |
| `--border-strong` | `#0A0A0A` | Focused structural emphasis |
| `--text-primary` | `#0A0A0A` | Main copy and values |
| `--text-secondary` | `#45454B` | Supporting text |
| `--text-muted` | `#6B6B70` | Metadata and placeholders |
| `--text-disabled` | `#B2B2B8` | Disabled content |

### Brand accent

Geass uses C.C. green, a light mint. It should occupy less than 10% of most screens. It is a light colour, so text on a green fill is always `#0A0A0A`, never white.

| Token | Dark | Light | Use |
| --- | --- | --- | --- |
| `--accent` | `#7CE0A0` | `#16935A` | Primary action, focus, selected state |
| `--accent-hover` | `#94E8B2` | `#0F7A49` | Hover |
| `--accent-subtle` | `#7CE0A01A` | `#16935A14` | Tinted backgrounds |
| `--accent-border` | `#7CE0A04D` | `#16935A40` | Selected borders |
| `--on-accent` | `#0A0A0A` | `#FFFFFF` | Text on a solid accent fill |

Green carries two meanings, kept apart by shape: a **solid green block is an action**; a **small green dot or text is status**. Never use green as decoration, and never use a gradient or glow behind operational data.

### Semantic colours

| State | Foreground | Subtle background | Meaning |
| --- | --- | --- | --- |
| Healthy / success | `#7CE0A0` | `#7CE0A014` | Running, connected, complete |
| Warning | `#E8A84A` | `#E8A84A14` | Degraded, approaching a limit |
| Critical / danger | `#FF8A8A` | `#FF8A8A14` | Failed, unavailable, destructive |
| Information | `#67A6E8` | `#67A6E814` | Neutral system information |
| Pending | `#A1A1A1` | `#A1A1A112` | Queued, paused, unknown |

A destructive *button* fills with solid `#C93C3C` and white text. Critical status text uses `#FF8A8A`.

Never communicate state by colour alone. Pair colour with a label, icon, shape, or position: healthy is a round dot, critical is a square. Reserve red for actual failure, danger, or destructive action.

## 3. Typography

### Font families

- **Interface and marketing:** Space Grotesk Variable.
- **Code and technical data:** JetBrains Mono Variable.
- **Wordmark only:** Geist 700, outlined in the logo files. Never set live text in it.
- **Fallback:** `"Space Grotesk", ui-sans-serif, system-ui, sans-serif` and `"JetBrains Mono", ui-monospace, monospace`.

Use monospace for commands, YAML, logs, IP addresses, ports, hashes, versions, resource quantities, and stable identifiers. Do not use monospace for paragraphs, navigation, buttons, or ordinary labels.

### Type scale

| Role | Size / line height | Weight | Tracking |
| --- | --- | --- | --- |
| Marketing display | `56 / 60px` | 600 | `-0.035em` |
| Page display | `40 / 44px` | 600 | `-0.03em` |
| H1 | `32 / 38px` | 600 | `-0.025em` |
| H2 | `24 / 30px` | 600 | `-0.02em` |
| H3 | `18 / 24px` | 600 | `-0.01em` |
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

- Account rail: 200px expanded, 56px collapsed.
- Top bar: 54px.
- Project tabs: 56px.
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

- Everything is square: controls, buttons, inputs, cards, dialogs, status chips, and avatars use `0` radius.
- The only round elements are status dots and the pulsing live indicator.
- Default border: 1px. Structural separators use `--border-subtle`, controls use `--border-default`.
- Shadows are reserved for floating layers, never ordinary cards.

```css
--shadow-popover: 0 12px 36px rgb(0 0 0 / 35%), 0 0 0 1px rgb(255 255 255 / 5%);
--shadow-dialog: 0 24px 80px rgb(0 0 0 / 50%), 0 0 0 1px rgb(255 255 255 / 7%);
```

## 5. Core components

### Buttons

Heights: 28px compact, 32px default, 40px large.

- **Primary:** solid green fill with `#0A0A0A` text. One per visual region.
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

The shell has four parts, and every authenticated screen uses them:

1. **Top bar** (full width, 54px). Left to right: the `geass▮` wordmark; inside a project, the project name (a switcher) and **environment tabs** (`production`, `pr-482 PR #482`, `+ Environment`); at the far right, `N pending` and the green **Deploy changes** button (admins only, only inside a project), and the account menu. Cells are divided by 1px rules, never by gaps.
2. **Account rail** (200px, collapsible). Always the account level: Projects, Templates, Usage, Settings. The active item has a 2px green inset bar. The footer shows **cluster health**: one bar per node (green ready, red under pressure, amber unschedulable) and the worst problem in words (`node-3 memory high`), then Docs, Support and Collapse.
3. **Project tabs** (inside a project only): Resources, Metrics, Logs, Settings, with the underline in green. On Resources the right side holds `From template` (ghost) and `+ Add resource` (white), which keeps green reserved for Deploy.
4. **Docked drawer.** Selecting a resource opens a panel to the right of the canvas instead of an overlay, so the canvas stays visible. The canvas pans to keep the selected card in view.

Pending changes live in the top bar, not in a floating banner: `N pending` opens the list of changes and `Deploy changes` applies them. The dashboard never shows a control that does nothing: there is no notification bell and no global search until those exist.

### Canvas cards

Each resource is a 236px card: kind icon and name on the left, a mono kind label on the right (`app`, `function`, `postgres 16`, `object store`); a mono subtitle (repository@branch, image, or `40 GiB · continuous backup`); and a status row with the state on the left and, on the right, the endpoint or `N changes` waiting to deploy. States are shapes as well as colours: a round dot is healthy, a ring is in progress, a square is failed. A failed card gets a red tint and border. The selected card has a green border, and the connection lines to and from it turn green. Lines leave and enter cards at right angles.

### UI implementation

The dashboard is a React single-page app in `dashboard-ui/`, built with Vite, Mantine and TanStack Router/Query, and embedded into the operator binary (`internal/dashboard/frontend/dist`). Rebuild it with `pnpm build` after any UI change.

- `src/theme.js` and the top of `src/styles.css` hold the tokens, kept in sync with `geass-brand/tokens/`.
- `src/layout/AppLayout.jsx` is the shell (top bar, rail, project tabs). Resource pages render inside it.
- `src/components/DeployControls.jsx` and `src/components/ClusterHealth.jsx` are the top-bar and rail-footer widgets.
- `src/components/ProjectCanvas.jsx` draws the canvas and its cards.

Prefer the Mantine primitives and the existing `geass-ui.jsx` helpers before adding new components. Do not add another component library.

### Projects list

The projects page is the default landing view (`/` and `/projects`). It is a card grid:

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

- Background: near-black canvas with a fine grid at about 4.5% opacity.
- Heading: 56–72px on large displays, maximum 12–14 words.
- Copy: 18px, maximum 2–3 lines at desktop width.
- One primary and one secondary action.
- Product UI begins within the first viewport or immediately below it.
- The only background effect is the fine grid. No glow, gradient wash, or illustration behind the headline.

### Product imagery

Use actual or carefully maintained interface captures. Keep them straight-on and sharp. Avoid floating perspective screens, device frames, fake terminal noise, glowing server racks, and generic AI/cloud imagery.

### Documentation

- Three-column structure where space allows: navigation, article, local outline.
- Reading width: 720px.
- Persistent code copy actions.
- Clear version and edition labels.
- Examples begin with the shortest successful path, followed by explanation and advanced options.
- Built with Starlight in `website/` (theme in `website/src/styles/docs.css`); square corners, green only for links and actions, dark and light themes that follow the visitor's system setting (the landing page is always dark). Page structure and voice rules are in `website/README.md`.

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

Geass gives you absolute command over servers you already own. The idea comes from the power of command: state the order once, and the platform carries it out and keeps it that way. The brand should express control, calm, and certainty, not domination, fantasy spectacle, or Kubernetes complexity.

Tagline: **Absolute command over servers you already own.** Install copy may use the contract metaphor (“Sign the contract in one line”), but interface copy stays plain.

### Name and casing

- Product name: **Geass** in prose. The logo and wordmark are lowercase `geass`.
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

### Logo

The mark is the **command cursor**: the lowercase wordmark `geass` followed by a solid block, as if the terminal is waiting for your order.

- Wordmark: Geist 700, lowercase, tracking −5.5%, kerned, converted to outlines.
- Cursor: 0.30em wide, exactly one x-height tall, sitting on the baseline, about 0.075em after the final `s`.
- Colour: letters `#F2F2F2` with a `#7CE0A0` cursor on dark; letters `#0A0A0A` with a `#16935A` cursor on light; one-colour versions use a single ink for both.
- App icon and avatar: `g▮` on black. Below 24px, drop the letters: the green cursor alone is the icon (the favicon).
- The cursor may blink (1s steps) only while something is deploying. It is steady everywhere else.

Required configurations: horizontal lockup, stacked lockup with tagline, symbol, light and dark one-colour versions, app icon, and favicon. All are generated by `geass-brand/tools/build_brand.py`.

Clear space equals the cursor's width on every side. Never add glow, bevel, drop shadow, outline, or gradient, and never set the wordmark in a different typeface or add letter-spacing.

### Brand usage

- Use green for actions and status, not for decoration or entire pages.
- Let interface screenshots demonstrate the product.
- Prefer neutral photography-free communication.
- Do not imitate another product's logo, proprietary assets, exact layouts, or copy. Geass takes influence from developer-tool sites but keeps an independent identity.

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
  --font-sans: "Space Grotesk Variable", "Space Grotesk", ui-sans-serif, system-ui, sans-serif;
  --font-mono: "JetBrains Mono Variable", "JetBrains Mono", ui-monospace, monospace;

  --bg-canvas: #0a0a0a;
  --bg-subtle: #0f0f0f;
  --bg-surface: #151515;
  --bg-elevated: #1a1a1a;
  --bg-hover: #222222;
  --bg-active: #2a2a2a;

  --border-subtle: #262626;
  --border-default: #333333;
  --border-strong: #5a5a5a;

  --text-primary: #f2f2f2;
  --text-secondary: #a1a1a1;
  --text-muted: #8f8f8f;

  --accent: #7ce0a0;
  --accent-hover: #94e8b2;
  --on-accent: #0a0a0a;
  --success: #7ce0a0;
  --warning: #e8a84a;
  --danger: #ff8a8a;
  --danger-solid: #c93c3c;
  --info: #67a6e8;

  --radius: 0;

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

The authoritative copy is `geass-brand/tokens/geass-tokens.css`.

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
