# Geass EUI wireframes

This document turns the Railway reference screenshots into a page-by-page plan for the Geass control plane. Each page records the intent, information hierarchy, primary interactions, and the states the implementation must support.

The reference direction is structural rather than literal: Geass keeps the calm dark shell, persistent navigation, generous canvas, compact operational summaries, and one dominant action, while using the Geass tokens and resource language from [`design.md`](design.md).

## Shared shell

Every authenticated page uses the same shell:

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass mark + organization / project context                         notifications   account │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Overview             │                                                                      │
│ Clusters             │                         Page content                                 │
│ Projects             │                                                                      │
│ Services             │                                                                      │
│ Databases            │                                                                      │
│ Storage              │                                                                      │
│ Networking           │                                                                      │
│ Observability        │                                                                      │
│                      │                                                                      │
│ Docs                 │                                                                      │
│ Preferences          │                                                                      │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

- Dark mode is primary: `#0A0A0B` canvas, `#121214` surfaces, and `#8B8CF8` accent.
- The expanded sidebar is 232px; it collapses to a 56px icon rail at narrower desktop widths.
- Organization and project context remain visible above navigation. Resource pages also expose the active environment.
- The page content has one visually dominant action. Destructive actions stay in an overflow menu until confirmation.

## Page 00 — Overview

**Short description:** Overview is the control-plane landing page. It summarizes project health and platform signals, then routes users to the project workspace for resource-level actions.

### Page anatomy

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Geass / Overview                                             [Open projects]  │
├──────────────────────────────────────────────────────────────────────────────┤
│ Projects                         3 of 4 projects healthy      View all       │
│                                                                              │
│ Platform signals   Running pods · Nodes · CPU usage                        │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Wireframe flows

#### Flow A — Open a project

```text
Overview → Projects summary → Open projects → project list → project workspace
```

#### Flow B — Investigate a platform signal

```text
Overview → platform signal → Observability → inspect current metric state
```

### Geass implementation notes

- Overview and Projects are separate navigation destinations; only the active destination is highlighted.
- Zero projects and unavailable Prometheus data are explicit states, not fabricated health values.
- Resource creation and destructive actions remain scoped to the project workspace.

## Page 01 — Projects

**Reference:** [`Screenshot 2026-08-05 at 21.09.01.png`](images/Screenshot%202026-08-05%20at%2021.09.01.png)

**Short description:** The project landing page is the entry point to Geass workspaces. It answers “what projects do I control?” at a glance, surfaces service health without opening a project, and gives the user one obvious path to create a project.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass / organization ▼                                      ◌ notifications     account ▼   │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ ▦  Projects           │  Projects                                      [⌕ Search projects] │
│ □  Templates          │                                                        [＋ New]      │
│                      │  ┌──────────────────────────────────────────────────────────────┐   │
│ ◫  Usage             │  │ 17 days or $4.22 left  ·  Upgrade to keep services online     │   │
│ ♧  People            │  └──────────────────────────────────────────────────────────────┘   │
│ ⚙  Settings          │                                                                      │
│                      │  ▦ 1 project  ·  Sort by: Recent activity ▼                         │
│ ▤  Docs              │                                                                      │
│ ◉  Central Station   │  ┌──────────────────────────────────────┐                           │
│ ◇  Support           │  │ payments                             │                           │
│                      │  │                                      │                           │
│                      │  │       ◈ ── ◈ ── ◈                    │                           │
│                      │  │       │                                │                           │
│                      │  │       ◈                                │                           │
│                      │  │                                      │                           │
│                      │  │  ● production · 4/4 services healthy │                           │
│                      │  └──────────────────────────────────────┘                           │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Information hierarchy

1. Page title and project search establish scope.
2. `New project` is the only primary action in the header.
3. A compact account or platform banner appears only when action is needed; it should not dominate the page.
4. Project count and sort control provide orientation without becoming a large statistics grid.
5. Each project card shows name, a quiet topology preview, active environment, and one health summary.

### Project card states

- **Healthy:** green status dot plus `Healthy` or `4/4 services healthy`.
- **Degraded:** amber dot plus the affected count, for example `3/4 services healthy`.
- **Failed:** red dot plus a concrete failure summary; never rely on colour alone.
- **Empty:** explain that projects contain isolated environments and managed resources, then show `Create project`.
- **Loading:** preserve the card grid rhythm with neutral skeleton rows; do not show fake health values.

### Wireframe flows

#### Flow A — Open an existing project

```text
Projects index
  → select project card
  → project workspace overview
  → choose environment (dev / staging / production)
  → inspect services, databases, storage, or settings
```

The selected project and environment must remain visible in the shell. Returning to Projects preserves the previous sort and search query where possible.

#### Flow B — Create a project

```text
Projects index
  → New project
  → name + display name
  → choose at least one environment
  → choose or confirm the Geass cluster
  → review project namespaces and consequences
  → Create project
  → project workspace overview
```

The review step names the cluster and environments that will be provisioned. The final action is `Create project`, not a vague `Continue`.

#### Flow C — Find a project

```text
Projects index
  → focus Search projects (Cmd/Ctrl + K may also open global search)
  → filter by project display name or identifier
  → empty result explains the query and offers Clear search
  → select result
```

Search filters the visible cards without changing navigation context. Keyboard focus and arrow-key selection are required for the final implementation.

### Geass implementation notes

- Existing server route: `GET /projects`.
- Existing create route: `GET /projects/new` then `POST /projects/create`.
- Existing project workspace route: `GET /projects/:name`.
- The current implementation has project cards and health data; the revamp should add the persistent sidebar, search/sort controls, topology preview, and explicit empty/loading states described here.
- Use the Geass horizontal lockup in the expanded shell and the symbol asset at collapsed widths. Do not introduce Railway logos, purple gradients, or vendor-specific service icons as Geass branding.

## Page inventory

The 25 reference screenshots in `images/` are documented below, one reference at a time. They are grouped into these product areas:

- Project workspace overview
- Service list and service overview
- Service deployment and deploy-progress flow
- Database and storage resource pages
- Cluster overview and node health
- Settings, cloud connections, and account/preferences

The wireframes intentionally separate summary pages, drawers, modal sub-steps, and destructive states when the reference changes the user’s task or risk. All references were checked against the local screenshot inventory.

## Page 02 — Add resource chooser

**Reference:** [`Screenshot 2026-08-05 at 21.13.17.png`](images/Screenshot%202026-08-05%20at%2021.13.17.png)

**Short description:** From a project workspace, the user opens a centered resource chooser instead of navigating through a long form immediately. The chooser makes the source of the resource explicit, keeps the project and environment context visible behind the modal, and progressively reveals only the configuration required by the selected path.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass / payments / production                              notifications     account ▼       │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Project navigation    │                         dimmed workspace                            │
│                      │                                                                      │
│                      │                    ┌──────────────────────────────┐                  │
│                      │                    │ ✦ Describe a resource or    │                  │
│                      │                    │   paste a repository link   │                  │
│                      │                    ├──────────────────────────────┤                  │
│                      │                    │ ◉ GitHub repository       › │                  │
│                      │                    │ ▤ PostgreSQL database     › │                  │
│                      │                    │ ▱ Template                 › │                  │
│                      │                    │ ◈ Container image          › │                  │
│                      │                    │ ƒ Function                 › │                  │
│                      │                    │ ▱ Object storage           › │                  │
│                      │                    │ > Empty service            › │                  │
│                      │                    └──────────────────────────────┘                  │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Open from the workspace’s single `Add resource` action or the global `Cmd/Ctrl + K` command palette.
- Focus the search/description field on open. `Escape` closes the chooser and returns focus to the trigger.
- Every option has a label and supporting description on hover/focus; icons never carry the meaning alone.
- Selecting an option replaces the chooser with the matching creation flow. The project and environment remain in the header and are carried through as hidden form context.
- The chooser is a dialog, not a new page. Background content is inert and dimmed; the dialog receives focus first.

### Wireframe flows

#### Flow A — Deploy from a repository

```text
Add resource chooser
  → GitHub repository
  → connect/select repository and branch
  → configure build, environment, networking, and storage
  → review generated deployment
  → Deploy service
  → service overview with deployment progress
```

#### Flow B — Deploy an existing image

```text
Add resource chooser
  → Container image
  → image + port + replicas
  → optional environment variables, metrics, and ingress
  → review
  → Deploy service
```

#### Flow C — Provision a managed resource

```text
Add resource chooser
  → PostgreSQL database / cache / object storage
  → resource name + environment + engine/version
  → review connection and ownership details
  → Create resource
  → resource overview
```

#### Flow D — Start from a template

```text
Add resource chooser
  → Template
  → search/filter templates
  → inspect template resources and required variables
  → choose environment
  → review generated resources
  → Create project resources
```

### Geass implementation notes

- Existing app entry point: `GET /apps/new`.
- Existing database entry point: `GET /databases/new`.
- Existing cache and object storage entry points: `GET /caches/new` and `GET /object-stores/new`.
- The chooser should be a reusable modal component with a typed `resource kind`, not a set of unrelated buttons that each lose project context.
- Geass should prefer `PostgreSQL database`, `Cache`, and `Object storage` labels over provider-specific labels. Provider details belong inside the selected flow.
- The primary action changes with context (`Deploy service`, `Create database`, `Create cache`, `Create object store`) and should never be a generic `Continue` at the final step.

## Page 03 — Database provider chooser

**Reference:** [`Screenshot 2026-08-05 at 21.13.34.png`](images/Screenshot%202026-08-05%20at%2021.13.34.png)

**Short description:** This is the second step of the add-resource flow. After choosing Database, the user selects an engine from a focused list. The page deliberately keeps the same centered surface and background context as Page 02, with a back affordance and no unrelated configuration exposed yet.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass / payments / production                              notifications     account ▼       │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Project navigation    │                         dimmed workspace                            │
│                      │                                                                      │
│                      │                    ┌──────────────────────────────┐                  │
│                      │                    │ ←  Choose a database...      │                  │
│                      │                    ├──────────────────────────────┤                  │
│                      │                    │ ◉ PostgreSQL                  │                  │
│                      │                    │ ▰ Redis                       │                  │
│                      │                    │ ◇ MongoDB                     │                  │
│                      │                    │ ⌁ MySQL                       │                  │
│                      │                    └──────────────────────────────┘                  │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- The back control returns to Page 02 and restores the previous focus and query where possible.
- The chooser supports keyboard navigation, type-to-filter, and `Escape` to close the complete flow.
- Provider icons are supplementary. The engine name is always visible as text and announced to assistive technology.
- Selecting an engine advances to the database configuration page with the current project and environment preserved.
- Only engines supported by the current Geass release are enabled. Planned engines can appear disabled with a short reason, but should not look selectable.

### Wireframe flows

#### Flow A — Create PostgreSQL

```text
Add resource chooser
  → Database
  → PostgreSQL
  → name + environment + version
  → storage, resources, and backup settings
  → review connection endpoint and secret handling
  → Create database
  → database overview
```

#### Flow B — Create Redis or another cache engine

```text
Add resource chooser
  → Database
  → Redis (or supported engine)
  → engine-specific configuration
  → review
  → Create resource
```

If Redis is represented as a cache in Geass, the chooser should route it to the Cache flow and use the same progressive provider-selection pattern. The user should never have to infer the difference from an icon.

#### Flow C — Backtrack without losing context

```text
Database provider chooser
  → Back
  → Add resource chooser
  → select another resource kind
```

The project, environment, and any initial description remain intact while the user moves between chooser levels.

### Geass implementation notes

- Existing database creation entry point: `GET /databases/new`.
- Existing cache creation entry point: `GET /caches/new`.
- Current API support centers on PostgreSQL and Redis-backed resources; provider availability should be driven by capability data rather than hard-coded decorative choices.
- The provider step should be a reusable variant of the Page 02 dialog, not a separate visual language or full-page redirect.

## Page 04 — Container image input

**Reference:** [`Screenshot 2026-08-05 at 21.13.44.png`](images/Screenshot%202026-08-05%20at%2021.13.44.png)

**Short description:** The image-source path uses a single focused input for a registry-qualified container image. Guidance is placed directly below the field, with concrete examples that make the accepted syntax obvious before the user submits.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass / payments / production                              notifications     account ▼       │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Project navigation    │                         dimmed workspace                            │
│                      │                                                                      │
│                      │                    ┌──────────────────────────────┐                  │
│                      │                    │ ←  ghcr.io/acme/api:latest  │                  │
│                      │                    ├──────────────────────────────┤                  │
│                      │                    │ ⓘ Enter a Docker image from  │                  │
│                      │                    │   a supported registry       │                  │
│                      │                    ├──────────────────────────────┤                  │
│                      │                    │ Examples                     │                  │
│                      │                    │ • hello-world                │                  │
│                      │                    │ • ghcr.io/user/repo:latest   │                  │
│                      │                    │ • quay.io/user/repo:tag      │                  │
│                      │                    │ • registry.gitlab.com/...    │                  │
│                      │                    └──────────────────────────────┘                  │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Keep the input focused on entry and support paste without requiring a separate “Add” click.
- Validate image syntax after interaction and on submit. An error states what is invalid, why it matters, and how to correct it.
- Support common registries (`ghcr.io`, Docker Hub, Quay, GitLab, and MCR) while preserving the exact image reference the user entered.
- A valid image advances to service configuration: port, replicas, environment variables, health checks, networking, and optional metrics.
- Back returns to the resource chooser without losing the entered image reference.

### Wireframe flows

#### Flow A — Deploy a public image

```text
Add resource chooser
  → Container image
  → enter image reference
  → validate registry and tag
  → configure service
  → review image, exposed port, and environment
  → Deploy service
```

#### Flow B — Deploy a private image

```text
Container image input
  → enter private registry image
  → choose or create registry credentials
  → validate access without exposing the secret
  → configure service
  → review pull permissions and consequence
  → Deploy service
```

#### Flow C — Recover from invalid input

```text
Container image input
  → invalid or incomplete reference
  → inline error with accepted examples
  → correct reference
  → continue
```

### Geass implementation notes

- Existing image deployment entry point: `GET /apps/new`, then `POST /apps/create`.
- The current Geass form already accepts an image, port, ingress host, and metrics flag; this wireframe makes image entry a deliberate progressive step before the larger form.
- Use monospace for the image field and examples, but keep the explanatory copy in the interface font.
- The primary action after configuration is `Deploy service`; the image field itself should not use an ambiguous `Submit` label.

## Page 05 — Project workspace and service drawer

**Reference:** [`Screenshot 2026-08-05 at 21.14.19.png`](images/Screenshot%202026-08-05%20at%2021.14.19.png)

**Short description:** The project workspace is a visual topology surface for deployed resources. Selecting a service opens a right drawer without losing the canvas context. Pending edits are grouped into one explicit change set and applied through a single deployment action.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass / payments / production                              notifications     account ▼       │
├──────────────────────┬─────────────────────────────────┬────────────────────────────────────┤
│ Project navigation    │  [Apply 6 changes] [Details]    │  [×]  ◈  api-service                │
│                      │  [Deploy  ⌘↵]                   │                                    │
│                      │                                  │  Overview  Deployments  Variables │
│                      │        ·  ·  ·  ·  ·             │  Metrics  Logs  Console  Settings │
│                      │                                  │  ───────────────────────────────── │
│                      │          ┌──────────────┐        │  ● Healthy                        │
│                      │          │ ◈ api-service │        │  endpoint / replicas / image      │
│                      │          │  5 settings  │        │                                    │
│                      │          └──────────────┘        │  recent deployment                 │
│                      │                                  │  events and resource usage         │
│                      │  ⊞ zoom  +  −  fit  ↶  ↷         │                                    │
└──────────────────────┴─────────────────────────────────┴────────────────────────────────────┘
```

### Information hierarchy

1. The project and environment remain in the global header.
2. The canvas communicates resource relationships and pending topology changes.
3. The change bar identifies how many changes are pending, exposes review details, and owns the single dominant `Deploy` action.
4. The drawer leads with resource identity and health, then exposes sibling views through tabs.
5. Raw configuration, console access, and destructive settings remain secondary tabs or overflow actions.

### Interaction rules

- The canvas supports pan, zoom, fit-to-content, keyboard movement between resources, and a non-colour relationship indicator.
- A selected resource has a clear border and focus state. Selection opens the drawer; it does not navigate away from the workspace.
- The drawer is a right-side panel on desktop and a full-screen sheet on narrow screens.
- Closing the drawer restores focus to the selected resource card.
- `Apply changes` remains visible while there are pending changes. `Details` opens a review surface listing each resource and consequence.
- Deployment progress is honest and state-based: queued, pulling/building, scheduling, starting, checking, healthy, or failed.

### Wireframe flows

#### Flow A — Inspect a service

```text
Project workspace
  → select service node
  → service drawer / Overview
  → inspect endpoint, health, replicas, image, and latest change
  → open Deployments, Variables, Metrics, Logs, Console, or Settings
```

#### Flow B — Apply pending changes

```text
Workspace with pending edits
  → Apply changes
  → Details / review change set
  → confirm affected resources and consequences
  → Deploy
  → deployment progress
  → healthy service or explicit failure with recovery action
```

#### Flow C — Navigate from a service to a dependency

```text
Service drawer
  → environment variable or attached resource reference
  → dependency resource
  → dependency drawer
  → Back to service
```

Navigation should preserve the canvas viewport and the original drawer context so inspection does not feel like losing place.

### Geass implementation notes

- Existing resource detail routes include `/apps/:name`, `/databases/:name`, `/caches/:name`, and `/object-stores/:name`.
- Existing app detail already exposes deployments, logs, metrics, attachment, edit, scale, and rollback actions; these map naturally to drawer tabs or overflow actions.
- The current server-rendered dashboard uses pages rather than a topology canvas. This wireframe establishes the target experience; the first implementation can provide a responsive workspace frame and drawer before adding live graph layout.
- Use status labels such as `Healthy`, `Degraded`, `Failed`, and `Pending` beside indicators. Do not use a glowing or gradient canvas as a substitute for operational state.

## Page 06 — Project settings

**Reference:** [`Screenshot 2026-08-05 at 21.22.07.png`](images/Screenshot%202026-08-05%20at%2021.22.07.png)

**Short description:** Project settings are a full page with a local settings rail. General metadata is separated from operational areas such as environments, shared variables, integrations, and danger-zone actions. The page keeps edits reviewable and makes copyable identifiers easy to retrieve.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass / payments / production                              notifications     account ▼       │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Project Settings     │  General                                                               │
│                      │                                                                        │
│ ⚙ General            │  Project info                                                          │
│ ◫ Usage              │  Name             [ payments                                      ]   │
│ ▤ Environments       │  Description      [ Optional description                         ]   │
│ ◎ Shared variables   │  Project ID       [ geass-project-id                         ⧉ ]      │
│ ⑂ Webhooks           │  [Update]                                                               │
│ ⚑ Feature flags      │  ────────────────────────────────────────────────────────────────      │
│ ♧ Members            │  Visibility                                                             │
│ ◉ Tokens             │  This project is currently PRIVATE.                                    │
│ ◇ Integrations       │  [Change visibility]                                                   │
│ ⚠ Danger             │  ────────────────────────────────────────────────────────────────      │
│                      │  Generate template from project                                       │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Information hierarchy

1. The local settings rail establishes which project concern is being edited.
2. General settings lead with editable name and description, then the stable ID and copy action.
3. Visibility communicates current access state in text before offering a change.
4. Template/export tooling and other advanced sections follow lower on the page.
5. Destructive actions are isolated in `Danger`, with explicit consequence and recovery language.

### Interaction rules

- The settings rail keeps the project name and active environment context visible.
- Save actions are scoped to a section where possible; show unsaved-change state before leaving.
- Project IDs, connection endpoints, and similar stable identifiers use monospace and have a copy button with a tooltip and accessible label.
- Visibility changes require a confirmation dialog naming the project and new audience.
- Shared variables and tokens are masked by default, with explicit reveal, copy, rotate, and revoke actions.
- On mobile, the settings rail becomes a select or horizontal overflow navigation; it must not push the form below an unusable width.

### Wireframe flows

#### Flow A — Update project metadata

```text
Project settings / General
  → edit name or description
  → Update
  → inline success confirmation with changed fields
```

#### Flow B — Change visibility

```text
Project settings / General
  → Change visibility
  → review who can access the project
  → confirm project name and new visibility
  → update visibility
```

#### Flow C — Configure an environment

```text
Project settings
  → Environments
  → choose dev / staging / production
  → edit environment variables, resources, and deployment defaults
  → Save environment
```

#### Flow D — Dangerous project action

```text
Project settings / Danger
  → select Delete project or irreversible action
  → show exact project name, affected environments, and recovery status
  → type project name to confirm
  → destructive action
```

### Geass implementation notes

- Existing project settings route: `GET /projects/:name/settings` and save route `POST /projects/:name/settings/save`.
- Platform settings, HA readiness, and cloud connections are separate global settings surfaces; do not mix them into the project settings rail.
- The current server page exposes display name, environments, and cluster association. This wireframe adds the fuller settings IA while keeping cluster association platform-controlled.
- Use `Delete` for irreversible actions and reserve the danger colour for the confirmation state, not the entire page.

## Page 07 — Usage and cost

**Reference:** [`Screenshot 2026-08-05 at 21.22.21.png`](images/Screenshot%202026-08-05%20at%2021.22.21.png)

**Short description:** Usage makes cost and capacity legible without turning the page into a wall of charts. Current usage and estimated usage lead the page, followed by resource-specific detail for CPU, memory, and network egress.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass / payments / production                              notifications     account ▼       │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Project Settings     │  Usage                                                                 │
│                      │                                                                        │
│ ⚙ General            │  Current usage                                                         │
│ ▥ Usage              │  ┌────────────────────────────────────────────────────────────────┐  │
│ ◫ Environments       │  │ Memory usage                         $0.00   Current total    │  │
│ ...                  │  │ CPU usage                            $0.00       $0.00        │  │
│                      │  │ Network usage (egress)               $0.00                  │  │
│                      │  └────────────────────────────────────────────────────────────────┘  │
│                      │                                                                        │
│                      │  Estimated usage                                                       │
│                      │  ┌────────────────────────────────────────────────────────────────┐  │
│                      │  │ Memory / CPU / egress                  Estimated total $0.00  │  │
│                      │  └────────────────────────────────────────────────────────────────┘  │
│                      │                                                                        │
│                      │  Details                                                               │
│                      │  ┌────────────────────────────────────────────────────────────────┐  │
│                      │  │ CPU  ─────────────── timeline / exact values                   │  │
│                      │  │ RAM  ─────────────── timeline / exact values                   │  │
│                      │  │ Network egress ───── timeline / exact values                   │  │
│                      │  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Information hierarchy

1. Current cost is the first answer; estimated cost is second.
2. The three drivers (memory, CPU, egress) remain visible as aligned rows with units.
3. Details explain the totals through time-series charts and exact values.
4. A time-range and environment filter belongs near the page heading, not hidden inside each chart.

### Interaction rules

- Every metric exposes units, time range, and exact values on hover and keyboard focus.
- Use direct values and restrained line charts; no 3D, full-screen gradients, or decorative gauges.
- Align currency and quantities with tabular numerals.
- A zero value is valid data and should be labeled `No usage recorded`, not confused with loading.
- Unavailable metering states explain why data is missing and what the user can do next.
- Cost warnings use amber only when a threshold is approaching; red is reserved for an actual incident or blocked action.

### Wireframe flows

#### Flow A — Investigate a cost increase

```text
Usage
  → choose time range and environment
  → compare current and estimated totals
  → inspect CPU / memory / egress detail
  → group or filter by service
  → open affected service overview
```

#### Flow B — Export usage data

```text
Usage
  → choose range and dimensions
  → Export CSV
  → confirm scope and timezone
  → download report
```

#### Flow C — No metering data

```text
Usage
  → metering unavailable or not configured
  → inline explanation: what is missing and why
  → Configure metrics / cloud connection
  → return to Usage
```

### Geass implementation notes

- Existing metrics support is available through the app metrics route and Prometheus client; this page should eventually consume the same source with a project/environment scope.
- Do not present provider billing estimates as authoritative unless the source and time range are shown.
- Keep charts readable on narrow screens by stacking them vertically while preserving metric name, unit, and exact-value access.

## Page 08 — Usage details and cost breakdown

**Reference:** [`Screenshot 2026-08-05 at 21.22.30.png`](images/Screenshot%202026-08-05%20at%2021.22.30.png)

**Short description:** The lower Usage section turns the headline totals into inspectable measurements. CPU, RAM, network egress, and volume get aligned charts, followed by a cost table that exposes the measured quantity, unit rate, and resulting total.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Project Settings / Usage                                                                      │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Usage selected        │  Details                                                               │
│                      │  ┌────────────────────────────────────────────────────────────────┐  │
│ General              │  │ CPU                         0.1 vCPU / 0.0 vCPU               │  │
│ Usage                │  │ ──────────────────────────────────────────────────────────── │  │
│ Environments         │  │ RAM                         100 MB / 50 MB / 0 B               │  │
│ ...                  │  │ ──────────────────────────────────────────────────────────── │  │
│                      │  │ Network egress              100 MB / 50 MB / 0 B               │  │
│                      │  │ ──────────────────────────────────────────────────────────── │  │
│                      │  │ Volume                      100 MB / 50 MB / 0 B               │  │
│                      │  └────────────────────────────────────────────────────────────────┘  │
│                      │                                                                        │
│                      │  Project cost                                   View cost by service   │
│                      │  ┌──────────┬──────────────┬──────────────────┬──────────────────┐  │
│                      │  │ Metric   │ Quantity     │ Unit rate        │ Total            │  │
│                      │  ├──────────┼──────────────┼──────────────────┼──────────────────┤  │
│                      │  │ Memory   │ 0.00 GB-min  │ $0.000231 / GB-min│ $0.0000         │  │
│                      │  │ CPU      │ 0.00 vCPU-min│ $0.000463 / min   │ $0.0000         │  │
│                      │  │ Egress   │ 0.00 GB      │ $0.05 / GB        │ $0.0000         │  │
│                      │  └──────────┴──────────────┴──────────────────┴──────────────────┘  │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Charts share the same time range, timezone, and sampling interval so comparisons are trustworthy.
- Each chart labels its unit and scale; tooltips expose timestamp and exact value.
- The cost table aligns quantities and currency values with tabular numerals and keeps the rate visible beside the total.
- `View cost by service` opens a filtered table or drawer without changing the selected project or time range.
- A rate change or estimate is labeled with its effective date and source.

### Wireframe flows

#### Flow A — Explain project cost

```text
Usage summary
  → scroll to Details
  → compare CPU, RAM, egress, and volume charts
  → inspect Project cost table
  → View cost by service
  → select service
  → service usage and deployment history
```

#### Flow B — Inspect an anomalous metric

```text
Usage details
  → focus chart point or choose metric filter
  → read exact timestamp and value
  → open related service or deployment
  → inspect logs and recent changes
```

#### Flow C — Handle missing rates

```text
Project cost table
  → rate unavailable or stale
  → show affected metric and source timestamp
  → configure billing / provider data
  → refresh estimate
```

### Geass implementation notes

- This is a continuation of Page 07, not a replacement for the summary view; deep detail should remain below the first viewport or behind a disclosure on narrow screens.
- The backend should calculate totals from a consistent interval and preserve raw measurements for auditability.
- Cost-by-service must use the same resource identity and environment filters as the project workspace so users can move from an amount to an owner and consequence.

## Page 09 — Environments

**Reference:** [`Screenshot 2026-08-05 at 21.22.39.png`](images/Screenshot%202026-08-05%20at%2021.22.39.png)

**Short description:** Environments explain the isolation boundary for a project and list each deployable environment as a compact row. The page separates stable environments from optional pull-request environments so temporary previews do not look like permanent production targets.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Project Settings / Environments                                                               │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Environments selected │  Environments                                                         │
│                      │  Each environment is an isolated instance of each service. [Docs ↗]  │
│ General              │                                                                        │
│ Usage                │  ┌────────────────────────────────────────────────────────────────┐  │
│ Environments         │  │ ✓ production                         Updated 9m ago   ⋮       │  │
│ Shared variables     │  └────────────────────────────────────────────────────────────────┘  │
│ ...                  │  [＋ New environment]                                                 │
│                      │                                                                        │
│                      │  PR environments                                                       │
│                      │  Temporary environments created for pull requests and cleaned up      │
│                      │  when the pull request closes.                                        │
│                      │  [Enable PR environments]                                              │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- The environment row exposes name, health, last update, owner, and an overflow menu for edit/archive actions.
- Environment names and lifecycle states are text-first; the check icon is supplemental.
- `New environment` opens a focused creation flow with name, source/clone choice, cluster, and default policy.
- Production deletion or archive requires explicit confirmation and names the affected services and namespaces.
- PR environments are clearly labeled temporary and show retention/cleanup behavior before enablement.
- The active environment is reflected in the global header and is preserved when navigating into resources.

### Wireframe flows

#### Flow A — Create an environment

```text
Environments
  → New environment
  → name + clone from existing environment or start empty
  → choose cluster and default policies
  → review isolated namespaces and inherited resources
  → Create environment
  → environment overview
```

#### Flow B — Switch environment

```text
Environment list
  → select production / staging / dev
  → global context updates
  → workspace resources refresh to selected environment
```

The switch must make the context change obvious and avoid silently carrying actions from one environment into another.

#### Flow C — Enable pull-request environments

```text
Environments
  → Enable PR environments
  → configure source integration, naming, and retention
  → review automatic creation and cleanup consequences
  → Enable
  → preview environment appears when a pull request opens
```

#### Flow D — Archive an environment

```text
Environment overflow menu
  → Archive environment
  → list services, databases, and storage affected
  → confirm environment name
  → archive and show recovery/retention state
```

### Geass implementation notes

- Existing project settings already stores environment names and uses them to scope resources; this page gives that model a first-class management surface.
- Geass should use `dev`, `staging`, and `production` as common defaults without forcing those names on every project.
- PR environments require a clear ownership and cleanup model; they should not be presented as ordinary permanent environments.
- The environment list belongs inside project settings, while the active environment switch belongs in the global/project workspace shell.

## Page 10 — Integrations

**Reference:** [`Screenshot 2026-08-05 at 21.23.25.png`](images/Screenshot%202026-08-05%20at%2021.23.25.png)

**Short description:** Integrations present external systems as deliberate, self-contained cards. Each card explains what the connection enables and exposes one clear setup action; the page does not imply that an unconfigured provider is already active.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Project Settings / Integrations                                                               │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Integrations selected │  Integrations                                                         │
│                      │                                                                        │
│ General              │  ┌────────────────────────────────────────────────────┐                │
│ Usage                │  │  ◇  Cloud provider                                │                │
│ Environments         │  │                                                     │                │
│ Shared variables     │  │  Connect provider credentials to provision and     │                │
│ Webhooks             │  │  manage resources from infrastructure you control. │                │
│ ...                  │  │                                                     │                │
│                      │  │                         [Connect integration]      │                │
│                      │  └────────────────────────────────────────────────────┘                │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- The card clearly shows provider name, connection state, scopes requested, and last verification time once connected.
- `Connect integration` opens a focused, provider-specific authorization/configuration flow.
- Never show third-party logos as Geass branding; use approved provider marks only as integration identifiers.
- Credentials and tokens are masked. The page offers rotate, revoke, and verify actions with explicit consequences.
- An unavailable adapter is labeled as unavailable with the reason and alternative path; it must not look like a broken active connection.

### Wireframe flows

#### Flow A — Connect a cloud provider

```text
Integrations
  → Connect integration
  → choose provider and credential method
  → review requested permissions and target cluster/project
  → authorize or enter credentials
  → verify connection
  → connected integration card
```

#### Flow B — Verify or rotate credentials

```text
Connected integration
  → Verify connection or Rotate credentials
  → confirm affected provisioning actions
  → perform verification/rotation
  → show last verified time and resulting state
```

#### Flow C — Revoke integration

```text
Connected integration
  → Revoke
  → explain services that may lose provisioning or management access
  → confirm provider and project name
  → revoke credentials
  → degraded integration state with recovery action
```

### Geass implementation notes

- Existing global route: `GET /cloud-connections`, with creation at `/cloud-connections/new` and `/cloud-connections/create`.
- The current implementation exposes AWS as planned/unavailable; the Geass wireframe should keep that honest while the adapter is incomplete.
- Project-level integrations may inherit from platform settings, but inheritance and override state must be explicit.
- Keep the integration card neutral; use accent only for the setup action and selected/verified state.

## Page 11 — Danger zone

**Reference:** [`Screenshot 2026-08-05 at 21.23.31.png`](images/Screenshot%202026-08-05%20at%2021.23.31.png)

**Short description:** Irreversible project and service actions live in a deliberately separated Danger page. The page explains consequence before the action, distinguishes removing one service from deleting the whole project, and reserves red for the actual destructive meaning.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Project Settings / Danger                                                                     │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Danger selected       │  Manage services                                                       │
│                      │  ┌────────────────────────────────────────────────────────────────┐  │
│ General              │  │ ⚠ Removing a service permanently deletes its data and          │  │
│ Usage                │  │   deployments in every environment.                          │  │
│ Environments         │  └────────────────────────────────────────────────────────────────┘  │
│ ...                  │  ┌────────────────────────────────────────────────────────────────┐  │
│ Integrations         │  │ ◇ api-service                                      [Remove]    │  │
│ Danger               │  └────────────────────────────────────────────────────────────────┘  │
│                      │                                                                        │
│                      │  Delete project                                                        │
│                      │  ┌────────────────────────────────────────────────────────────────┐  │
│                      │  │ ⚠ This permanently deletes project data across all services.  │  │
│                      │  │   This cannot be undone.                                      │  │
│                      │  └────────────────────────────────────────────────────────────────┘  │
│                      │  [Delete project]                                                     │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Warning copy states what happens, what data is affected, whether recovery exists, and what must be done next.
- Service removal and project deletion use separate confirmation dialogs and never share a generic confirmation component without resource-specific copy.
- Require typing the exact service or project identifier for destructive actions.
- Show a final review of environments, databases, volumes, secrets, deployments, and external resources that may remain or be deleted.
- Disable the destructive button until the confirmation requirement is satisfied; do not rely on colour alone.
- After completion, show the resulting state and recovery/documentation path rather than silently navigating away.

### Wireframe flows

#### Flow A — Remove a service

```text
Danger / Manage services
  → Remove service
  → review service name, environments, data, and deployments
  → type service name
  → Remove service permanently
  → confirmation and project workspace
```

#### Flow B — Delete a project

```text
Danger / Delete project
  → Delete project
  → review all environments, resources, data, and external side effects
  → type project identifier
  → final confirmation
  → Delete project permanently
  → projects index with recovery/support guidance
```

#### Flow C — Cancel safely

```text
Confirmation dialog
  → Cancel or Escape
  → return to Danger page
  → no resource state changed
```

### Geass implementation notes

- The current project settings route is the natural home for this page; service removal should use the existing resource ownership and finalizer behavior.
- Deletion copy must name the exact resource and consequence, following the content rules in `design.md`.
- Prefer recoverable archive/disable actions when technically possible; label permanent deletion distinctly.
- Audit destructive actions with actor, timestamp, resource, environment scope, and result.

## Page 12 — Workspace topology overview

**Reference:** [`Screenshot 2026-08-05 at 21.24.03.png`](images/Screenshot%202026-08-05%20at%2021.24.03.png)

**Short description:** The populated workspace presents applications, databases, caches, and volumes as connected resource cards on a calm topology canvas. Each card leads with identity and operational state; relationships communicate dependencies without requiring the user to open every resource.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Geass / payments / production                                                [＋ Add]        │
├─────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                              │
│        ┌──────────────────┐                 ┌──────────────────┐                            │
│        │ ◇ postgres       │                 │ ◉ api-service    │                            │
│        │ ● Healthy        │                 │ api.geass.dev    │                            │
│        │  ▱ db-volume     │◄─────────────── │ ● Healthy        │                            │
│        └──────────────────┘                 └─────────┬────────┘                            │
│                                                       │                                     │
│        ┌──────────────────┐                 ┌────────▼────────┐                            │
│        │ ◇ Redis          │                 │ ▱ object-store   │                            │
│        │ ● Healthy        │                 │ 1.1 MB           │                            │
│        │  ▱ redis-volume  │                 └──────────────────┘                            │
│        └──────────────────┘                                                                  │
│                                                                                              │
│  ⠿ fit  +  −  fullscreen  ↶  ↷                               [toast: deletion scheduled]   │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Information hierarchy

1. The header identifies project and environment and keeps `Add` available.
2. Resource cards show type, name, endpoint or quantity, and labeled health state.
3. Edges show ownership or dependency direction; they must remain legible when cards move.
4. A toast confirms background actions and explains recovery time without blocking the workspace.

### Interaction rules

- Cards are draggable only when the workspace is in layout mode; normal pointer interaction selects and opens the resource drawer.
- Connection lines have an accessible equivalent in the selected resource’s dependency list.
- Resource cards preserve a stable minimum size and reduce secondary metadata before becoming unreadable on small screens.
- Topology controls support keyboard focus and have labels/tooltips: fit, zoom in, zoom out, fullscreen, undo, and redo.
- Toasts are polite live announcements, persist in activity history, and include a dismiss action. Destructive operations expose `Cancel deletion` when recovery is available.
- Failed or degraded resources retain their position and show a concise reason, not a red-filled card.

### Wireframe flows

#### Flow A — Add a connected resource

```text
Workspace topology
  → Add
  → resource chooser
  → create resource
  → new card appears in pending state
  → apply changes
  → card becomes Healthy or Failed with next action
```

#### Flow B — Follow a dependency

```text
Workspace topology
  → select service or relationship edge
  → resource drawer
  → dependency list
  → select database / cache / volume
  → inspect dependency drawer
```

#### Flow C — Cancel a scheduled deletion

```text
Workspace toast: deletion scheduled
  → Cancel deletion
  → confirm the named project/resource
  → deletion halted within recovery window
  → toast confirms restored state
```

### Geass implementation notes

- This is the primary target for the Geass project workspace and should be consistent with Page 05’s drawer behavior.
- Resource types map to Geass apps, databases, caches, and object stores; volumes should be shown as owned child resources where the platform exposes them.
- Use the Geass symbol or neutral geometric resource icons rather than copying Railway, GitHub, PostgreSQL, or Redis marks as the product identity.
- Toast copy must name the operation and recovery window: `Project deletion scheduled. You can cancel within 48 hours.`

## Page 13 — Service deployments and history

**Reference:** [`Screenshot 2026-08-05 at 21.24.13.png`](images/Screenshot%202026-08-05%20at%2021.24.13.png)

**Short description:** A service drawer’s Deployments tab leads with the endpoint and runtime context, then shows the active deployment and a chronological history. Each entry exposes its state, source/change, actor, timestamp, and relevant next action without hiding skipped or removed history.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service                           ×  │ │
│                                             │                                             │ │
│                                             │ Deployments  Variables  Metrics  Logs      │ │
│                                             │                                             │ │
│                                             │ ◎ api.geass.dev       us-east  2 replicas │ │
│                                             │ ┌─────────────────────────────────────────┐ │ │
│                                             │ │ ACTIVE  chore(ci): fixed format        │ │ │
│                                             │ │         2 hours ago via GitHub   ⋮     │ │ │
│                                             │ │ ✓ Deployment successful            ⌄ │ │ │
│                                             │ └─────────────────────────────────────────┘ │ │
│                                             │ HISTORY                         Hide skipped │ │
│                                             │ ┌─────────────────────────────────────────┐ │ │
│                                             │ │ REMOVED  previous deploy          ⋮     │ │ │
│                                             │ │ SKIPPED  audio updates             ⋮     │ │ │
│                                             │ │ FAILED   CI check suite failed      ⋮    │ │ │
│                                             │ └─────────────────────────────────────────┘ │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- The active deployment is visually distinct but still uses a labeled state and icon, not colour alone.
- Deployment records use a consistent compact row with state, change title, actor/source, timestamp, and overflow actions.
- Expanding a record reveals image/commit, environment, rollout events, health checks, and logs link.
- Failed deployments explain what failed and offer the next safe action (`View logs`, `Redeploy`, or `Rollback`).
- Skipped and removed history is visible by default or controlled by an explicit `Hide skipped` toggle; hiding never deletes history.
- The drawer retains the selected topology node and closes back to it.

### Wireframe flows

#### Flow A — Inspect a successful deployment

```text
Service drawer / Deployments
  → expand active deployment
  → review source, rollout, health checks, and endpoint
  → View logs
  → return to deployment history
```

#### Flow B — Recover from a failed deployment

```text
Deployment history
  → select failed deployment
  → read failure reason
  → View logs or inspect CI result
  → fix configuration or choose a known-good revision
  → Redeploy / Rollback
  → monitor progress
```

#### Flow C — Filter deployment history

```text
Deployment history
  → show/hide skipped and removed records
  → filter by environment, actor, source, or state
  → select a deployment record
```

### Geass implementation notes

- Existing app detail and deployment history routes expose much of this information; the drawer is the target presentation model.
- Keep deployment state vocabulary consistent across apps, databases, caches, and object stores where the lifecycle is comparable.
- Use monospace for image tags, commit hashes, IDs, and timestamps when they are technical identifiers; use interface typography for human-readable change titles.
- Never label a deployment successful before health checks and readiness state agree.

## Page 14 — Service variables and secrets

**Reference:** [`Screenshot 2026-08-05 at 21.24.21.png`](images/Screenshot%202026-08-05%20at%2021.24.21.png)

**Short description:** Service variables are managed inside the service drawer and remain masked by default. The page supports search, shared-variable references, a raw editor for advanced users, and explicit creation without exposing secret values in the normal list.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service                           ×  │ │
│                                             │ Deployments  Variables  Metrics  Logs      │ │
│                                             │                                             │ │
│                                             │ 38 service variables   ⌕ Search           │ │
│                                             │             ↪ Shared variable  {} Raw editor│ │
│                                             │                                   [＋ New] │ │
│                                             │ ┌─────────────────────────────────────────┐ │ │
│                                             │ │ ⛓ Need a database connection? Add variable│ │ │
│                                             │ ├─────────────────────────────────────────┤ │ │
│                                             │ │ {} DATABASE_URL          ********   ⋮   │ │ │
│                                             │ │ {} REDIS_URL             ********   ⋮   │ │ │
│                                             │ │ {} S3_ENDPOINT            ********   ⋮   │ │ │
│                                             │ │ {} API_PORT               ********   ⋮   │ │ │
│                                             │ └─────────────────────────────────────────┘ │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Values are masked by default and never rendered into page source unnecessarily.
- Reveal, copy, rotate, and delete actions are explicit per variable and require accessible labels/tooltips.
- Search matches variable names, not secret values.
- A shared-variable reference clearly identifies its source and inheritance behavior; editing the source warns about downstream services.
- Raw editor supports structured review and validation, but keeps secrets masked until an intentional reveal.
- Variable changes show pending state and are included in the same review/apply flow as other deployment changes.

### Wireframe flows

#### Flow A — Add a service variable

```text
Service drawer / Variables
  → New variable
  → key + value or secret reference
  → choose scope and environments
  → Save variable
  → review pending change
  → Apply changes
```

#### Flow B — Attach a managed resource

```text
Variables
  → Add variable / connect resource
  → choose database, cache, or object store
  → select connection field
  → generated secret-backed reference
  → review and apply
```

#### Flow C — Update a secret safely

```text
Variable overflow menu
  → Rotate or update secret
  → enter new value with confirmation
  → show affected environments/services
  → save masked value
  → redeploy if required
```

#### Flow D — Inspect raw configuration

```text
Variables
  → Raw editor
  → edit structured variable map
  → validate keys, scopes, and references
  → review diff
  → Save and apply
```

### Geass implementation notes

- Existing app configuration supports config and secrets panels and secret-backed attachments; this drawer is the target unified presentation.
- Use `Geist Mono`/IBM Plex Mono for keys and technical references, never for explanatory copy.
- Secret values should be encrypted at rest, excluded from logs and normal HTML, and auditable on reveal/copy/rotate actions.
- A variable referencing a managed resource should prefer a generated connection reference over asking the user to paste credentials.

## Page 15 — Service metrics

**Reference:** [`Screenshot 2026-08-05 at 21.24.30.png`](images/Screenshot%202026-08-05%20at%2021.24.30.png)

**Short description:** Service metrics are shown inside the resource drawer so operational context stays attached to the selected service. The page combines a shared time range, pause/live control, view toggle, and a grid of charts for compute, memory, traffic, and requests.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service                           ×  │ │
│                                             │ Deployments  Variables  Metrics  Logs      │ │
│                                             │                                             │ │
│                                             │ [table ▤ / grid ▦] [Last 15 min ▼] [Ⅱ live]│ │
│                                             │                                             │ │
│                                             │ ┌─────────────────┐ ┌────────────────────┐ │ │
│                                             │ │ CPU  Sum/Replicas│ │ Memory Sum/Replicas│ │ │
│                                             │ │ line chart       │ │ line chart        │ │ │
│                                             │ └─────────────────┘ └────────────────────┘ │ │
│                                             │ ┌─────────────────┐ ┌────────────────────┐ │ │
│                                             │ │ Public traffic  │ │ Requests  7 total │ │ │
│                                             │ │ bar/line chart   │ │ bar chart         │ │ │
│                                             │ └─────────────────┘ └────────────────────┘ │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Every chart labels units, time range, series name, and exact values on hover and keyboard focus.
- `Last 15 min` offers approved ranges and preserves the selected range while switching tabs.
- Pause stops live updates without hiding the last update time; resuming announces that data is live again.
- Sum versus replicas is a visible series choice, not an unexplained colour difference.
- Grid and table views expose the same data; the table is the accessible and exact-value equivalent.
- Charts stack vertically in narrow drawers and remain horizontally scrollable only when labels require it.

### Wireframe flows

#### Flow A — Diagnose resource pressure

```text
Service drawer / Metrics
  → choose time range
  → compare CPU and memory with replica series
  → inspect traffic and request volume
  → correlate timestamp with deployment history
  → open deployment or logs
```

#### Flow B — Freeze a live incident snapshot

```text
Metrics
  → Pause live updates
  → inspect exact values and timestamp
  → switch to table view
  → export or share investigation context
  → Resume live updates
```

#### Flow C — Handle unavailable metrics

```text
Metrics
  → metric unavailable
  → identify missing Prometheus/agent capability
  → Configure observability
  → refresh metrics
```

### Geass implementation notes

- Existing Prometheus support and app metrics route can provide the first implementation of this tab.
- Use restrained line and bar charts with one accent series and semantic warning colours only when thresholds are meaningful.
- Avoid decorative chart backgrounds and gradients; operational values must remain readable in the dark Geass canvas.
- The service identity, environment, and last update time must remain visible when the metrics drawer scrolls.

## Page 16 — Service console

**Reference:** [`Screenshot 2026-08-05 at 21.24.40.png`](images/Screenshot%202026-08-05%20at%2021.24.40.png)

**Short description:** The Console tab provides a controlled terminal and file surface for a selected service. Connection state and safe access controls are visible above the terminal; the file browser remains a separate lower panel so logs and commands do not become visually conflated with file operations.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service  Deployments Variables ... × │ │
│                                             │                                             │ │
│                                             │ container-id        Copy SSH   Fullscreen  │ │
│                                             │ ● Connected                                │ │
│                                             │ ┌─────────────────────────────────────────┐ │ │
│                                             │ │ $ /app#                                  │ │ │
│                                             │ │                                           │ │ │
│                                             │ │                                           │ │ │
│                                             │ └─────────────────────────────────────────┘ │ │
│                                             │ ───────── resize handle ─────────────────── │ │
│                                             │ ▱ /app                  Upload  Refresh     │ │
│                                             │   📁 apps/                  modified        │ │
│                                             │   📁 assets/                modified        │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Connection state is explicit: connecting, connected, disconnected, expired, or unavailable.
- Commands and output use monospace, preserve raw text, and support wrapping or horizontal scrolling.
- `Copy SSH command` never exposes credentials in page copy; show a confirmation and provide rotation guidance for expiring access.
- Fullscreen is optional and reversible. The selected service and environment remain in the accessible title.
- Upload and file actions require permission checks and show target path, size, and overwrite consequence before execution.
- Terminal and file browser are independently scrollable; keyboard focus can move between them without trapping users in the console.
- Destructive commands cannot be inferred from text colour alone; show the normal confirmation mechanism for platform-mediated actions.

### Wireframe flows

#### Flow A — Open a connected console

```text
Service drawer / Console
  → establish connection
  → show connected state and working directory
  → enter command
  → review output
  → close console and return focus to service
```

#### Flow B — Copy access command

```text
Console
  → Copy SSH command
  → authenticate/reveal if required
  → copy short-lived command
  → show copied confirmation and expiry
```

#### Flow C — Upload a file

```text
Console file browser
  → choose target directory
  → Upload
  → select file and review size/overwrite behavior
  → upload
  → refresh directory and announce result
```

#### Flow D — Connection failure

```text
Console
  → connection fails or expires
  → show what happened and why
  → Reconnect or inspect deployment health
  → return to console after connection is healthy
```

### Geass implementation notes

- The current dashboard exposes logs but not an interactive console; this is a future capability requiring explicit Kubernetes/agent permissions.
- Treat console access as privileged and audit actor, service, environment, connection time, and command/session metadata according to the security model.
- Never render sensitive environment values into terminal history, logs, or browser storage.
- Keep the console as a supporting drawer surface; full-page mode is for focused work, not the default service overview.

## Page 17 — Service settings and deployment source

**Reference:** [`Screenshot 2026-08-05 at 21.24.46.png`](images/Screenshot%202026-08-05%20at%2021.24.46.png)

**Short description:** Service Settings centralizes source, networking, build, deploy, and danger configuration behind a searchable local index. The first section establishes repository and branch ownership, then makes automation such as auto-deploy and wait-for-CI explicit.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service             Deployments ... × │ │
│                                             │                                             │ │
│                                             │ [Filter settings...                         ]│ │
│                                             │                                             │ │
│                                             │ Source                                      │ │
│                                             │ Source repo                                  │ │
│                                             │ [ ◉ org/repository              ✎ Disconnect]│ │
│                                             │ Add root directory                           │ │
│                                             │ Branch connected to production               │ │
│                                             │ [ main ▼                         Disconnect ]│ │
│                                             │ ⚡ Auto deploy when pushed to repository     │ │
│                                             │ Wait for CI  [on]                            │ │
│                                             │                                             │ │
│                                             │ Source · Networking · Edge · Scale · Build  │ │
│                                             │ Deploy · Config-as-code · Feature flags     │ │
│                                             │ Danger                                      │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Settings search filters sections and focuses the matching setting; keyboard `/` shortcut is discoverable and accessible.
- Source connection shows repository, branch, root directory, authorization state, and last verification.
- Disconnecting a repository names the consequences for future deploys and preserves existing deployment history.
- Auto-deploy and wait-for-CI are separate controls with plain-language descriptions and a visible current state.
- Changes to production source or automation require review before applying; do not silently deploy on a settings toggle.
- Section navigation preserves scroll position and drawer context.

### Wireframe flows

#### Flow A — Connect a source repository

```text
Service settings / Source
  → Connect repository
  → authorize provider and select repository
  → choose root directory and branch
  → review production deployment consequence
  → Connect source
```

#### Flow B — Change production branch

```text
Source settings
  → branch selector
  → choose branch
  → review auto-deploy impact
  → Save branch
  → wait for next deployment or Deploy now
```

#### Flow C — Configure CI gating

```text
Source settings
  → Wait for CI
  → choose required checks / timeout
  → preview deployment gate behavior
  → Save and apply
```

#### Flow D — Disconnect source

```text
Source settings
  → Disconnect
  → explain future deployment and webhook consequences
  → confirm repository and service name
  → disconnect
  → service remains at last known deployment with explicit state
```

### Geass implementation notes

- Geass currently supports image-based app creation; repository-driven deployment is a planned extension of the existing app lifecycle.
- Replace provider-specific “GitHub” assumptions with a source adapter model while retaining the same source/branch/root-directory information hierarchy.
- Deployment automation settings belong to the service, while cluster/provider credentials belong to platform or project integrations.
- All automation toggles should produce an auditable change and communicate whether an immediate rollout occurs.

## Page 18 — Service networking

**Reference:** [`Screenshot 2026-08-05 at 21.24.52.png`](images/Screenshot%202026-08-05%20at%2021.24.52.png)

**Short description:** Networking separates public access from private service-to-service communication. Public domains show protocol, target port, proxy/provider state, and copy/edit/delete actions; private networking exposes the internal hostname and address family without implying public reachability.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service             Settings ...   × │ │
│                                             │                                             │ │
│                                             │ Networking                                  │ │
│                                             │                                             │ │
│                                             │ Public networking                           │ │
│                                             │ Access over HTTP                            │ │
│                                             │ ┌─────────────────────────────────────────┐ │ │
│                                             │ │ ☁ api.geass.dev      ⧉  ✎  ⌫            │ │ │
│                                             │ │   → Port 8080 · proxy detected          │ │ │
│                                             │ └─────────────────────────────────────────┘ │ │
│                                             │ TCP proxy                                    │ │
│                                             │ ┌─────────────────────────────────────────┐ │ │
│                                             │ │ ▣ proxy.geass.dev:32852 → :8080  ⧉ ⌫    │ │ │
│                                             │ └─────────────────────────────────────────┘ │ │
│                                             │ [Generate domain] [＋ Custom domain]          │ │
│                                             │ ℹ domain limit / plan guidance              │ │
│                                             │                                             │ │
│                                             │ Private networking                           │ │
│                                             │ ✓ api-service.internal   IPv4 · IPv6        │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Public and private networking use separate headings and explanatory copy.
- Every endpoint exposes protocol, hostname, port, TLS/proxy state, and a copy action with an accessible label.
- Domain creation validates ownership, port, certificate, and proxy requirements before applying changes.
- Limits explain the affected capability and recovery option; do not hide a disabled action behind a vague error.
- Private hostnames include environment scope and address family; avoid suggesting they are reachable from the public internet.
- Delete-domain confirmation names the hostname and services that may lose access.

### Wireframe flows

#### Flow A — Generate a public domain

```text
Service settings / Networking
  → Generate domain
  → choose target port and protocol
  → review public exposure and proxy/TLS behavior
  → Generate domain
  → endpoint appears with copy action
```

#### Flow B — Add a custom domain

```text
Networking
  → Custom domain
  → enter hostname
  → verify DNS ownership
  → configure certificate/proxy
  → review public exposure
  → Add domain
```

#### Flow C — Connect privately

```text
Networking
  → Private hostname
  → select environment and service port
  → review internal-only reachability
  → Save private endpoint
  → share generated reference with dependent service
```

#### Flow D — Remove an endpoint

```text
Endpoint overflow
  → Delete endpoint
  → list affected clients and consequence
  → confirm hostname
  → delete endpoint
  → health/deployment state reflects any broken dependency
```

### Geass implementation notes

- Current app configuration includes an ingress host and port; this page gives that capability a first-class, inspectable presentation.
- Geass should use its own domain/provider abstraction and label Cloudflare or another provider as an implementation detail.
- Network endpoint changes should participate in the pending-change review and deployment/apply flow where they affect service rollout.
- Use information blue for limits and documentation, green for verified/ready endpoints, and red only for actual failure or destructive confirmation.

## Page 19 — Service scale and capacity

**Reference:** [`Screenshot 2026-08-05 at 21.25.03.png`](images/Screenshot%202026-08-05%20at%2021.25.03.png)

**Short description:** Scale settings make replica placement and per-replica CPU/memory limits explicit. Capacity constraints are shown beside the controls, with plan or cluster limits explained before a user attempts an unavailable configuration.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service             Settings ...   × │ │
│                                             │ [Filter settings...                       ]│ │
│                                             │                                             │ │
│                                             │ Scale                                       │ │
│                                             │ Regions and replicas                        │ │
│                                             │ [ region: Geass primary ▼ ] [ 1 replica ]  │ │
│                                             │ ℹ Multi-region requires eligible capacity  │ │
│                                             │                                             │ │
│                                             │ Replica limits                              │ │
│                                             │ CPU     2 vCPU        limit: 2 vCPU        │ │
│                                             │ [────────────── slider ────────────────]   │ │
│                                             │ Memory  1 GB          limit: 1 GB          │ │
│                                             │ [────────────── slider ────────────────]   │ │
│                                             │ ↑ Request higher limit / capacity          │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Replica count, regions, CPU, and memory use bounded controls with current value, unit, and maximum visible.
- Region selection explains data locality, availability, latency, and cost consequences.
- Multi-region configuration shows the minimum required capacity and prevents partial/ambiguous saves.
- A plan or cluster limit uses neutral information styling and a clear upgrade/request path; it does not make unavailable controls appear broken.
- Scaling changes show estimated rollout impact and whether downtime or redistribution is possible before apply.
- Values use tabular numerals and technical units; accessible labels announce the current and maximum values.

### Wireframe flows

#### Flow A — Scale replicas

```text
Service settings / Scale
  → change replica count
  → optionally choose regions
  → review capacity, cost, and rollout behavior
  → Save scale settings
  → apply/deploy
  → monitor health and replica readiness
```

#### Flow B — Adjust resource limits

```text
Scale
  → adjust CPU or memory
  → validate against cluster/plan limits
  → review scheduling and cost consequence
  → Save and apply
```

#### Flow C — Request unavailable capacity

```text
Scale
  → requested value exceeds limit
  → show current limit and reason
  → Request capacity / configure provider
  → return with new maximum after verification
```

### Geass implementation notes

- Existing app scaling route supports replica changes; resource requests/limits should be surfaced through the same service settings model when supported.
- Geass should describe limits in terms of cluster capacity and platform policy, not only commercial plan language.
- Scale changes must update status and deployment history so users can correlate the setting with health and cost changes.
- Multi-region is an explicit future capability; do not show it as active unless the cluster/provider model can actually place replicas safely.

## Page 20 — Service build configuration

**Reference:** [`Screenshot 2026-08-05 at 21.25.09.png`](images/Screenshot%202026-08-05%20at%2021.25.09.png)

**Short description:** Build settings define how source becomes a deployable artifact. The page makes the builder, Dockerfile path, and watch paths explicit so users understand both what is built and which repository changes trigger a new deployment.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service             Settings ...   × │ │
│                                             │ [Filter settings...                       ]│ │
│                                             │                                             │ │
│                                             │ Build                                       │ │
│                                             │ Builder                                     │ │
│                                             │ [ Dockerfile ▼ ]  BuildKit docs ↗          │ │
│                                             │                                             │ │
│                                             │ Dockerfile path                             │ │
│                                             │ [ /apps/api/Dockerfile                    ] │ │
│                                             │                                             │ │
│                                             │ Watch paths                                 │ │
│                                             │ Git-style patterns triggering deployments   │ │
│                                             │ [ **                                      ] │ │
│                                             │ [ !/apps/web/**                           ] │ │
│                                             │ [ Add pattern e.g. /src/**              ] │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Builder choice explains supported inputs and output; advanced build details remain collapsed until relevant.
- Dockerfile path is validated against the connected source and shown as a repository-relative path when possible.
- Watch patterns provide examples, syntax validation, preview of matched changes, and a clear fallback when no rules are configured.
- A build setting change previews whether the next source change will trigger a deployment.
- Documentation links open in a new context and do not lose unsaved settings.
- Build failures identify whether the error came from path resolution, dependency install, image build, or registry push.

### Wireframe flows

#### Flow A — Configure Dockerfile build

```text
Service settings / Build
  → choose Dockerfile builder
  → enter or select Dockerfile path
  → validate against source repository
  → configure watch paths
  → preview trigger behavior
  → Save and apply
```

#### Flow B — Configure watch paths

```text
Build
  → add include/exclude pattern
  → preview files that match
  → validate pattern order
  → Save watch rules
```

#### Flow C — Recover from a build failure

```text
Build settings
  → failed path/build validation
  → inspect actionable error
  → correct builder/path/dependency setting
  → validate locally or run build
  → deploy after successful build
```

### Geass implementation notes

- Current Geass app flow accepts an image directly; repository/Dockerfile builds are planned and should not be represented as implemented until the build pipeline exists.
- Use monospace for paths and patterns. Keep builder explanation and recovery copy in the interface font.
- Build settings should feed deployment history with builder, source revision, and artifact metadata for reproducibility.
- Watch paths are advanced configuration and should be available behind progressive disclosure in the initial create flow.

## Page 21 — Service deploy configuration

**Reference:** [`Screenshot 2026-08-05 at 21.25.15.png`](images/Screenshot%202026-08-05%20at%2021.25.15.png)

**Short description:** Deploy settings define how a built artifact starts, replaces an old deployment, and proves readiness. Optional lifecycle features—pre-deploy, teardown, schedules, and health checks—are grouped with clear consequences instead of hidden behind a generic deployment button.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ ◉ api-service             Settings ...   × │ │
│                                             │ [Filter settings...                       ]│ │
│                                             │                                             │ │
│                                             │ Deploy                                      │ │
│                                             │ Custom start command                        │ │
│                                             │ [＋ Start command]                           │ │
│                                             │ ＋ Add pre-deploy step                       │ │
│                                             │                                             │ │
│                                             │ Teardown                                    │ │
│                                             │ [toggle] Enable teardown                    │ │
│                                             │                                             │ │
│                                             │ Cron schedule                               │ │
│                                             │ [＋ Add schedule]                            │ │
│                                             │                                             │ │
│                                             │ Healthcheck path                            │ │
│                                             │ [＋ Healthcheck path]                        │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Start and pre-deploy commands use monospace, shell validation, and an explanation of execution context.
- Teardown states whether old replicas stop before or after new replicas pass readiness; this consequence is visible before enabling it.
- Cron schedules validate timezone, overlap behavior, and whether the service is long-running or job-like.
- Health checks validate path, port, protocol, timeout, and retry behavior, with a preview of when deployment becomes healthy.
- Optional settings remain collapsed until enabled but retain their state and show an unsaved-change indicator.
- A deploy configuration change is reviewed with the resulting rollout sequence before apply.

### Wireframe flows

#### Flow A — Configure service startup

```text
Deploy settings
  → Start command
  → enter command and working directory
  → validate execution context
  → save pending change
```

#### Flow B — Add a health check

```text
Deploy settings
  → Healthcheck path
  → configure endpoint, port, timeout, and retries
  → run/preview check against a deployment
  → save and apply
```

#### Flow C — Schedule a service

```text
Deploy settings
  → Add schedule
  → enter cron expression and timezone
  → preview next runs and overlap behavior
  → save schedule
```

#### Flow D — Review rollout consequences

```text
Deploy settings changed
  → Review changes
  → show start/pre-deploy/teardown/healthcheck sequence
  → identify possible downtime
  → Apply changes
  → deployment progress and readiness result
```

### Geass implementation notes

- Existing app configuration has port, replicas, ingress, and metrics fields; lifecycle and health checks are the next progressive layer.
- Health readiness should use the same status conditions and event history as the controller rather than a separate UI-only state.
- Cron workloads may need a distinct resource kind in Geass; do not force scheduled jobs into a long-running service model without clarifying lifecycle semantics.
- Destructive teardown behavior must be reviewed and audited like other externally visible actions.

## Page 22 — Shared variables

**Reference:** [`Screenshot 2026-08-05 at 21.22.48.png`](images/Screenshot%202026-08-05%20at%2021.22.48.png)

**Short description:** Shared Variables are project-level values that multiple services can reference within an environment. The page groups variables by environment, makes the reference syntax visible, and distinguishes the shared source from each service’s local variable list.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ Project Settings / Shared variables                                                           │
├──────────────────────┬──────────────────────────────────────────────────────────────────────┤
│ Shared variables      │  Shared variables                                                     │
│ selected              │  Variables that can be referenced by multiple services in an env.     │
│                      │                                                                        │
│ General              │  ▾ production                                      0 variables  {}   │
│ Usage                │  ┌────────────────────────────────────────────────────────────────┐  │
│ Environments         │  │ [VARIABLE_NAME] [VALUE or ${REF}]        [Add]               │  │
│ Shared variables     │  │                                                                │  │
│ Webhooks             │  │                     No shared variables yet                  │  │
│ ...                  │  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────┴──────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Environment is an explicit scope and each environment can be expanded/collapsed independently.
- Keys are visible; secret values are masked or represented as references and never exposed in the list by default.
- The value field explains whether it accepts a literal, secret reference, or generated resource reference.
- Adding a shared variable previews the services that reference it and the environments affected.
- Editing or deleting a shared variable warns about downstream services before apply.
- Empty state explains why shared variables are useful and gives one inline add action; it does not require a separate empty-page wizard.

### Wireframe flows

#### Flow A — Add a shared variable

```text
Shared variables / environment
  → enter key and literal/reference value
  → validate key and reference syntax
  → review affected services
  → Add variable
  → apply changes to dependent services
```

#### Flow B — Reference a shared variable from a service

```text
Service drawer / Variables
  → Shared variable
  → choose project environment and key
  → preview resolved reference without exposing secret
  → Save and apply
```

#### Flow C — Update shared value safely

```text
Shared variables
  → edit value
  → list dependent services/environments
  → choose immediate or next-deploy propagation
  → confirm and apply
```

### Geass implementation notes

- This page complements Page 14: shared values are managed at project/environment scope, while service variables consume them.
- Existing Geass project settings stores environment names; shared-variable storage and reference resolution should be added as a first-class API surface.
- Keep variable names and `${REF}` syntax in monospace, with explanatory labels in the interface font.
- Audit reads, reveals, edits, and propagation of shared secrets separately from ordinary configuration changes.

## Page 23 — Runtime policies and config-as-code

**Reference:** [`Screenshot 2026-08-05 at 21.25.20.png`](images/Screenshot%202026-08-05%20at%2021.25.20.png)

**Short description:** Advanced Deploy settings define how a service sleeps, restarts, and can be represented as configuration. These controls are separated from basic startup settings because they change latency, reliability, cost, and recovery behavior.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ Deploy settings                             │ │
│                                             │                                             │ │
│                                             │ Serverless                                  │ │
│                                             │ Scale to zero when idle; wake on traffic    │ │
│                                             │ [toggle] Enable serverless                  │ │
│                                             │                                             │ │
│                                             │ Restart policy                              │ │
│                                             │ [On failure ▼]                              │ │
│                                             │ ℹ retry limit: 10        [Upgrade]          │ │
│                                             │ [ 10 retries                              ] │ │
│                                             │                                             │ │
│                                             │ Config-as-code                              │ │
│                                             │ Geass config file / preview / sync status    │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Serverless explains cold-start latency, queued requests, scale trigger, and cost effect before enablement.
- Restart policy names the exit conditions and distinguishes retry count, backoff, and permanent failure.
- Limits show the current maximum and the reason for the limit; an upgrade/request path is informational and not the only way to recover.
- Config-as-code shows source of truth, drift state, last sync, and a reviewable diff before import/export.
- Advanced policies are progressively disclosed and included in deployment review with their operational consequences.

### Wireframe flows

#### Flow A — Enable scale-to-zero

```text
Runtime policies
  → Enable serverless
  → review cold start, queueing, and health-check behavior
  → choose idle threshold if supported
  → Save and apply
  → observe sleep/wake state
```

#### Flow B — Configure restart behavior

```text
Runtime policies
  → choose restart policy
  → set retry count and backoff
  → preview failure behavior
  → Save and apply
  → inspect deployment events after an exit
```

#### Flow C — Adopt config-as-code

```text
Runtime policies / Config-as-code
  → choose repository/file source
  → compare current platform state with file
  → review import diff
  → apply configuration
  → show synced/drifted state
```

### Geass implementation notes

- Kubernetes restart policy, probes, autoscaling, and declarative manifests are the closest Geass primitives; map the UI to those semantics rather than copying Railway terminology blindly.
- Config-as-code must identify the authoritative source and prevent silent overwrites of live settings.
- Serverless behavior should be offered only when the underlying runtime supports safe scale-to-zero and wake-up behavior.
- Runtime policy changes must be visible in events and deployment history.

## Page 24 — Edge and traffic protection

**Reference:** [`Screenshot 2026-08-05 at 21.24.58.png`](images/Screenshot%202026-08-05%20at%2021.24.58.png)

**Short description:** Edge settings contain infrastructure and incident-response controls that affect traffic outside the service process. Outbound IPv6 and CDN caching are opt-in capabilities; attack mode is a high-consequence action that uses danger styling and explains propagation time.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ Settings                                     │ │
│                                             │                                             │ │
│                                             │ Outbound IPv6                                │ │
│                                             │ Enable outbound connections to IPv6 targets │ │
│                                             │ [toggle] Enable outbound IPv6               │ │
│                                             │                                             │ │
│                                             │ Edge                                         │ │
│                                             │ Under attack mode                            │ │
│                                             │ [Until turned off ▼]             [Activate] │ │
│                                             │ Takes effect globally in ~20 seconds       │ │
│                                             │                                             │ │
│                                             │ CDN caching                                  │ │
│                                             │ Cache static assets/optional HTML at edge   │ │
│                                             │ [toggle] Enable CDN caching                │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Outbound IPv6 states target compatibility and whether the cluster/network supports the feature.
- Attack mode requires an explicit duration/scope, names the domains affected, and explains the visitor verification experience.
- The activate action is red because it is an active incident response, not because the page is generally dangerous.
- Activation confirmation includes start time, propagation estimate, exit condition, and a clear deactivate action.
- CDN caching states cache scope, invalidation behavior, stale-content risk, and whether private responses are excluded.
- Edge changes are audited and surfaced in the activity/event history.

### Wireframe flows

#### Flow A — Enable outbound IPv6

```text
Edge settings
  → Enable outbound IPv6
  → verify cluster/network support
  → review destination and egress consequences
  → Save and apply
```

#### Flow B — Activate attack mode

```text
Edge settings
  → Under attack mode
  → choose duration and domains
  → review visitor challenge and global propagation
  → Activate protection
  → monitor state
  → Deactivate when incident ends
```

#### Flow C — Configure CDN caching

```text
Edge settings
  → Enable CDN caching
  → choose static/HTML scope and cache headers
  → review private-data exclusions and invalidation path
  → Save and apply
  → purge cache when required
```

### Geass implementation notes

- Geass currently has an ingress host and platform cloud connection model but not an edge-control plane; this is a future capability and should be marked accordingly.
- Attack protection must never be a decorative toggle. It requires permission checks, audit events, explicit blast radius, and a reliable off path.
- CDN caching should default to safe private-data behavior and show cache invalidation as a first-class action.

## Page 25 — Config-as-code, feature flags, and service deletion

**Reference:** [`Screenshot 2026-08-05 at 21.25.25.png`](images/Screenshot%202026-08-05%20at%2021.25.25.png)

**Short description:** The final service settings state groups declarative configuration, build optimization, and the service-level danger boundary. It makes file paths and skipped-build behavior explicit, then isolates permanent service deletion with exact consequence copy.

### Page anatomy

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│ topology canvas                                                service drawer               │
│                                             ┌─────────────────────────────────────────────┐ │
│                                             │ Settings                                     │ │
│                                             │                                             │ │
│                                             │ Config-as-code                              │ │
│                                             │ Geass config file                            │ │
│                                             │ Manage build/deploy settings in a file      │ │
│                                             │ [＋ Add file path]                            │ │
│                                             │                                             │ │
│                                             │ Feature flags                                │ │
│                                             │ Skipped builds [toggle]                     │ │
│                                             │ Avoid rebuilding identical source artifacts  │ │
│                                             │                                             │ │
│                                             │ Delete service                               │ │
│                                             │ Permanently deletes deployments for this    │ │
│                                             │ service in this environment.                │ │
│                                             │ [Delete service]                             │ │
│                                             └─────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Config-as-code shows file path, source repository, sync direction, last sync, and drift state.
- Adding a file path validates repository existence and prevents two sources of truth without an explicit resolution step.
- Skipped builds explains cache identity, invalidation conditions, and how to force a rebuild.
- Delete service is visually separated from reversible settings and uses red only for the actual danger control and confirmation.
- Deletion confirmation names service, project, environment, deployments, variables, volumes, and recovery status.
- Service deletion should never imply the project or shared resources are also deleted unless that is explicitly true.

### Wireframe flows

#### Flow A — Link config-as-code

```text
Service settings / Config-as-code
  → Add file path
  → choose repository and config file
  → compare live state with file
  → review ownership and drift behavior
  → Link configuration
```

#### Flow B — Force a rebuild

```text
Feature flags / Skipped builds
  → inspect cache identity
  → Force rebuild
  → review expected build/deploy cost
  → build artifact
```

#### Flow C — Delete a service

```text
Service settings / Delete service
  → Delete service
  → review exact environment and deployment/data consequence
  → type service name
  → Delete service permanently
  → return to topology with explicit result
```

### Geass implementation notes

- This page completes the service settings model established in Pages 17, 19, 20, 21, and 23–24.
- Geass config-as-code should use Kubernetes manifests or a Geass-native declarative format and clearly show which fields are controlled by file versus dashboard.
- Build caching must be reproducible and invalidatable; skipped builds should never obscure a stale artifact.
- Service deletion must follow the same finalizer, audit, and confirmation rules as the project Danger page, with a narrower resource scope.

## Platform surfaces without a reference screenshot

The following pages are part of the current Geass product surface but were not represented by one of the 25 reference screenshots. They follow the same structure and interaction rules as the screenshot-backed pages above.

## Page 26 — Cluster overview and node health

**Short description:** The cluster page is an operational summary for capacity, readiness, add-ons, and node scheduling state. It is read-only unless a separate cluster-management flow is introduced.

### Page anatomy

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Clusters                                      [Refresh]                       │
├──────────────────────────────────────────────────────────────────────────────┤
│ Cluster cards: name · namespace · API status · add-ons                        │
│                                                                              │
│ Capacity overview                 Node health                                │
│ allocatable CPU / memory          Ready · schedulable · pressure              │
│                                                                              │
│ Empty/error state explains whether the control plane has no cluster or failed │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Readiness, add-on health, and capacity use distinct states; an unknown value is never rendered as healthy.
- Node health identifies unschedulable and pressure conditions separately from a node that is simply not ready.
- Refresh preserves the page and announces the updated timestamp.
- Cluster mutation controls are absent unless an authenticated provider adapter supports them.

### Wireframe flows

```text
Clusters → inspect cluster card → inspect node/add-on health → open HA readiness
Clusters → no cluster → explain required GeassCluster resource → open platform settings
```

### Geass implementation notes

- `GET /cluster` is the read-only control-plane entry point.
- Cluster/provider changes belong in platform settings or an explicit provider workflow, not an incidental dashboard card.

## Page 27 — Platform observability

**Short description:** Observability summarizes platform-wide signals and separates verified Prometheus data from Kubernetes-only status and unavailable metrics.

### Page anatomy

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Observability                              [Configure metrics]                │
├──────────────────────────────────────────────────────────────────────────────┤
│ Signal cards: API · controllers · scheduling · storage                         │
│                                                                              │
│ Prometheus window / source / last sample                                      │
│ Charts or explicit “data unavailable” state                                  │
│                                                                              │
│ Recommended action and link to affected project/service                      │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Every metric identifies its source and time window.
- Missing or stale Prometheus data uses an unavailable state with a configuration path; it does not fabricate zeroes.
- Kubernetes object health remains visible when Prometheus is unavailable.
- Chart controls preserve the selected window and scope.

### Wireframe flows

```text
Observability → select window → inspect signal → open affected resource
Observability → metrics unavailable → configure Prometheus → return to same window
```

### Geass implementation notes

- `GET /observability` is the platform-level counterpart to Page 15 service metrics.
- Prometheus configuration is stored in `GeassPlatformConfig`; provider-backed alerting remains a future integration.

## Page 28 — Platform settings and preferences

**Short description:** Platform settings owns cluster defaults, domain configuration, observability endpoints, and user-visible preferences. It is separate from project and service settings.

### Page anatomy

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Platform settings                                                            │
├──────────────────────┬───────────────────────────────────────────────────────┤
│ Settings index        │ Cluster overview                                     │
│ Cluster               │ Default cluster / namespace context                  │
│ Domains               │                                                       │
│ Observability         │ Configuration                                        │
│ Preferences           │ Default domain · Prometheus URL · save state          │
└──────────────────────┴───────────────────────────────────────────────────────┘
```

### Interaction rules

- Saving configuration reports exactly which platform behavior changes.
- Cluster status is read-only in this page unless a cluster-management adapter is present.
- Invalid or unreachable Prometheus URLs are shown as configuration errors, not healthy metrics.
- Preferences must not silently change project or service deployment state.

### Wireframe flows

```text
Platform settings → edit domain/metrics endpoint → validate → save → verify status
Platform settings → inspect cluster → open HA readiness or cloud connections
```

### Geass implementation notes

- `GET /settings` and `POST /settings/save` are the current implementation.
- The `DefaultClusterRef` API field is the authoritative default-cluster selection when multiple clusters exist.

## Page 29 — HA readiness gate

**Short description:** HA readiness is a diagnostic gate for PostgreSQL and other high-availability workflows. It explains each prerequisite and offers a deliberate check action.

### Page anatomy

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ HA readiness                              [Run readiness check]                │
├──────────────────────────────────────────────────────────────────────────────┤
│ Schedulable nodes       Persistent storage                                  │
│ healthy / minimum       StorageClass / missing                              │
│                                                                              │
│ Cluster readiness       Required add-ons                                    │
│ GeassCluster state      monitoring / cert-manager state                      │
│                                                                              │
│ Gate explanation: what is blocked and how to remediate                       │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- A check result names the failing prerequisite and its observed value.
- “Not ready” is distinct from an API error or an unavailable Kubernetes client.
- The page never provisions infrastructure as a side effect of viewing it.
- A readiness check is auditable and safe to repeat.

### Wireframe flows

```text
HA readiness → inspect failed check → remediate cluster/add-on/storage → run check
HA readiness → all checks pass → continue to gated database provisioning
```

### Geass implementation notes

- `GET /ha-readiness` renders the gate; `POST /ha-readiness/check` records a check request.
- The gate is a prerequisite signal, not a replacement for controller reconciliation.

## Page 30 — Documentation and product guidance

**Short description:** Docs is a lightweight product-guidance surface that routes users to the supported project workflow, platform settings, and integration documentation without pretending to be a full external documentation system.

### Page anatomy

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Docs                                                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│ Build with Geass                                                            │
│ project → environment → resource workflow                                   │
│                                                                              │
│ [Open projects] [Platform settings] [Cloud connections]                     │
│                                                                              │
│ Unsupported capability notices link to the integration required to enable it │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Guidance must match currently supported capabilities and label planned integrations.
- Links preserve the user’s product context where possible.
- Documentation does not expose credentials, secret values, or internal control-plane details.

### Wireframe flows

```text
Docs → choose workflow → open project/resource creation flow
Docs → unsupported feature → inspect required integration → return to workflow
```

### Geass implementation notes

- `GET /docs` is intentionally concise until a versioned documentation service is added.

## Page 31 — Managed resource index and detail

**Short description:** Databases, logical databases, caches, and object stores share a resource-index pattern: preserve project/environment context, show readiness and connection metadata, and keep destructive operations explicit.

### Page anatomy

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Databases / Caches / Storage                 [Create resource]                │
├──────────────────────────────────────────────────────────────────────────────┤
│ Project + environment context                                               │
│ Name · environment · engine/provider · readiness                             │
│                                                                              │
│ Detail drawer/page: endpoint · connection Secret reference · settings       │
│                                                                              │
│ Destructive action is isolated and names the exact resource scope            │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Interaction rules

- Resource lists and detail pages use the same health vocabulary as project environments.
- Connection values are represented by Kubernetes Secret references; secret payloads are never rendered.
- Unsupported providers remain visible as unavailable choices with an alternative supported path.
- Project and environment context survives creation, editing, and return navigation.

### Wireframe flows

```text
Resource index → Create → choose project/environment → configure → review → create
Resource index → select resource → inspect health/endpoint → edit settings
Resource detail → delete → exact confirmation → return with explicit result
```

### Geass implementation notes

- Current routes include `/databases`, `/logical-databases`, `/caches`, and `/object-stores` plus their creation/detail flows.
- Provider provisioning must remain honest when the adapter is unavailable; Kubernetes reconciliation remains the source of observed state.
