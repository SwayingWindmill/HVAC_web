# Shadcn Application Redesign — 2026-09-13

**Status: SELECTED / SHADCN APPLICATION SYSTEM**

## Decision

The frontend is being redesigned again. The previous Control Desk visual direction is superseded as a visual authority.

The new visual and interaction baseline is:

1. **shadcn/ui current component language** as the component-design authority.
2. **satnaing/shadcn-admin** as the primary application-layout and admin-product reference.
3. `PRODUCT.md` remains the authority for business truth, safety, data semantics, and workflows.
4. TanStack Router / Query / Table, Recharts, ECharts, X6 and G6 remain capability tools; they do not define the visual style.
5. `sadmann7/tablecn` is the default Data Table / ledger composition reference on top of the project's TanStack Table v9 implementation.
6. ReUI, Kibo UI and Dice UI form the approved advanced shadcn-compatible application-component layer. ReUI contributes workspace/composition patterns, Kibo UI contributes functionally rich components such as Gantt/Editor/Dropzone, and Dice UI contributes advanced interactions such as Sortable/Kanban/Editable/Tour. ReUI/Dice use Radix paths in the current application; Kibo components require per-component primitive/headless dependency review.
7. `docs/design-system/hvac-application-ui-specification.md` owns application-level page composition, Surface archetypes and advanced-component selection boundaries.
8. `docs/design-system/shadcn-component-contract.md` remains the component-selection and semantic composition authority for migrated surfaces.

This is a direct redesign. We do not preserve old layouts, CSS geometry, Ant Design page anatomy, ProComponents composition, the first Control Desk composition, or compatibility wrappers merely to keep old screens looking familiar.

## What we are adopting from shadcn-admin

We adopt the patterns, not its sample business domain:

- a neutral, compact application shell;
- a light sidebar in light mode and theme-native sidebar in dark mode;
- product/site switcher in the sidebar header;
- grouped navigation with quiet labels and compact active states;
- a single compact sticky global header;
- global command search;
- restrained theme, notification and user controls;
- content that uses standard shadcn Card, Tabs, Table, Badge, Button, Command and form grammar;
- responsive behavior that changes composition instead of shrinking a desktop dashboard;
- content-width constraints for ordinary pages and fluid width only where the task truly benefits from it;
- dense but readable information hierarchy rather than large decorative operational chrome.

## What we are adopting from current shadcn/ui

- code-owned primitives under `components/ui`;
- semantic CSS variables and Tailwind utilities;
- current Card grammar including `CardHeader`, `CardTitle`, `CardDescription`, `CardAction`, `CardContent`, `CardFooter`;
- current Field grammar for label / control / description / error composition;
- Input Group for search icons, result counts, units and inline input actions;
- compact `size="sm"` components when density matters;
- line-style Tabs for page/workspace mode switching;
- Command for global or large-set search and keyboard navigation;
- Table primitives + TanStack Table v9 `tableFeatures` / `useTable` for ledgers;
- Dialog for short modal tasks, AlertDialog for consequential confirmation, Sheet only for genuine transient auxiliary content;
- consistent focus, keyboard, disabled, invalid and selected states;
- Lucide icons only for product UI icons.

The local registry configuration remains the project source of truth for the exact shadcn variant. It currently selects `radix-nova`, so this production application remains on the Radix component base: Radix composition uses `asChild`, Base UI `render` APIs are not mixed in, and touched primitive internals use the unified `radix-ui` package. Business code depends on `components/ui`, not directly on primitive-library internals.

## Visual character

The target is not “industrial dark UI”. It is a high-quality modern operations application built with the same restraint as current shadcn products.

Light mode uses neutral zinc-like surfaces:

- app background: near-white neutral;
- cards: white;
- sidebar: white;
- borders: subtle neutral;
- primary text/actions: near-black;
- semantic color is reserved for actual operational meaning.

Dark mode uses the corresponding neutral dark surfaces. Dark mode is a theme, not the product identity.

Operational semantic colors remain available for success, warning, destructive, information and unknown states, but they are accents on facts—not large page backgrounds.

## Layout grammar

### App shell

```text
ApplicationShell
├── Sidebar
│   ├── Product + Site switcher
│   ├── Primary navigation
│   ├── Grouped navigation
│   └── Collapse
├── Header
│   ├── Sidebar trigger
│   ├── Realtime/system status when relevant globally
│   ├── Command search
│   ├── Theme
│   ├── Notifications
│   └── Account
└── Main
    └── Route-owned Surface
        ├── Breadcrumb
        ├── Page Header / H1 / local actions
        └── Surface content
```

Targets:

- expanded sidebar: approximately 240 px;
- collapsed sidebar: approximately 48 px;
- global header: approximately 56 px;
- ordinary desktop page padding: 24 px;
- ordinary content max-width: typically 1400–1600 px depending on the page;
- engineering canvases may use a wider fluid content area.

The Site selector lives in the Sidebar product/site switcher. The Header does not repeat it.

### Breadcrumb + Page Header

Most pages start with a compact, route-owned opening:

```text
Breadcrumb

Page title / H1                      local actions
Optional description / scope / freshness
```

Breadcrumb expresses hierarchy; the Page Header owns the single route-level H1. The global Header does not need to repeat that title, and the first Card/section must use its own task-specific title rather than echoing the route title.

Do not create a universal boxed PageHeader component. Use ordinary semantic markup and shadcn controls.

### Sections

Cards are allowed and expected where they express a coherent section. They are not mandatory wrappers for every piece of content.

Use:

- Card for coherent summaries, work queues, evidence panels and small analyses;
- border-only containers for dense tables and grouped facts;
- Tabs for alternate views of the same task;
- Data Table / ledger for scan-heavy collections;
- responsive grids for summary cards;
- real charts only when quantitative comparison is the task.

Avoid:

- decorative card walls;
- nested cards without clear hierarchy;
- uppercase English “console labels” as a recurring visual motif;
- large empty operational canvases when a table or chart communicates better;
- bespoke page-level CSS when standard Tailwind + `components/ui` can express the design.

## Surface redesign rules

### Dashboard

The dashboard becomes a modern shadcn overview page, not a custom Control Desk composition.

Baseline:

```text
Breadcrumb + Page Header + actions
4 concise metric cards
Run posture / device evidence        Priority handling
Optimization opportunity             Data confidence        Continue workflows
```

The metric cards summarize authoritative facts. They are not fake trends or opaque health scores.

### HVAC / Realtime

HVAC remains a real engineering workspace, but the surrounding UI uses shadcn grammar.

Baseline:

```text
Breadcrumb + Page Header + local actions
Compact summary cards
Line Tabs: topology / anomaly / energy evidence
Large workspace Card
  X6 engineering canvas OR measured energy evidence
  optional shadcn-styled context inspector
Supporting evidence Cards
```

X6 remains because the fixed engineering topology is a real product requirement, not because an old screen used it.

### Device Center

**Data Table First** is the default rule. Default is the project tablecn grammar + TanStack Table v9-native ledger rendered with shadcn primitives, not a card wall.

```text
Breadcrumb + Page Header
Filter/search toolbar
Data table / ledger
Row actions and selection
Full detail route or contextual inspector where useful
```

### Alarm Center

Default is a triage ledger with compact summary and filters. Investigation content uses standard shadcn Card/Badge/Tabs/Sheet grammar based on task duration.

### Work Orders

Default is a work-order ledger with explicit owner, SLA, state, source and next action. Detail should be designed as a durable work surface, not inherited Ant Drawer anatomy.

### Energy Analytics

Use Breadcrumb + compact Page Header, period controls, capability-appropriate primary analysis, comparison/evidence cards and a drill-down table. Ordinary application charts use shadcn Chart + Recharts; engineering-grade dense time-series/multi-axis/dataZoom/brush work uses Apache ECharts. Do not recreate Ant Design dashboard cards.

### Control / Strategy / FDD

Use decision-oriented sections: current fact → candidate/diagnosis → evidence → safety/impact → action → verification. Use shadcn controls and domain-specific visualizations only where they improve the decision.

## Migration policy

Migration is direct replacement:

1. redesign the Surface against this document;
2. replace old JSX/CSS rather than wrapping it;
3. delete or detach old CSS and components once no caller needs them;
4. do not create visual compatibility adapters;
5. preserve business contracts, route state, security, authorization and truthful data semantics;
6. verify with real browser rendering in WSL/Windows Chrome before calling a migrated Surface visually complete.

Current sequence:

1. AppShell
2. Dashboard
3. HVAC / Realtime
4. Device Center
5. Alarm Center
6. Work Orders
7. Energy Analytics
8. Control / Strategy / FDD
9. System / Registry
10. delete remaining old shared UI/CSS and remove Ant Design / ProComponents / Ant Design Charts after the final caller is gone

## Superseded authority

The superseded 2026-09-12 Control Desk decision is removed from the active tree. Use Git history only when explicit historical archaeology is needed.

The following are no longer visual authority:

- Control Desk v1 palette and permanent dark navigation rail;
- its 216 px / 60 px sidebar geometry;
- its 52 px global bar target;
- its Dashboard “Operational Situation + Action Queue + Evidence Band” mandatory composition;
- its requirement that HVAC must always be described as Canvas + Context Inspector + Evidence Dock;
- any bespoke Control Desk CSS vocabulary;
- old Ant Design / ProComponents layouts;
- old reference screenshots.

Business truths encoded in `PRODUCT.md` remain in force.

## External source review

Primary sources reviewed for this decision:

- https://github.com/satnaing/shadcn-admin
- https://ui.shadcn.com/
- https://ui.shadcn.com/blocks
- https://ui.shadcn.com/docs/components/sidebar
- https://ui.shadcn.com/docs/components/card
- https://ui.shadcn.com/docs/components/tabs
- https://ui.shadcn.com/docs/components/command
- https://github.com/sadmann7/tablecn
- https://github.com/keenthemes/reui
- https://reui.io/components
- https://github.com/shadcnblocks/kibo
- https://www.kibo-ui.com/
- https://github.com/sadmann7/diceui
- https://diceui.com/docs/introduction
- `docs/architecture/shadcn-tablecn-reui-source-review-2026-09-20.md`
- `docs/architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md`

The purpose of source review is to learn current composition, density, interaction and component grammar. We do not vendor the example application as a product template.
