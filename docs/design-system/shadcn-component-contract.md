# Shadcn Component Contract

**Status: ACTIVE / UI COMPONENT AUTHORITY**  
**Scope:** `apps/hvac-web`  
**Base:** `radix-nova` from `apps/hvac-web/components.json`  
**Primary sources:** current `ui.shadcn.com` component documentation and `satnaing/shadcn-admin`

## 1. Purpose

This document is the component-level design contract for the HVAC web application. It exists to prevent every feature from inventing its own form grammar, table anatomy, modal behavior, spacing, status language, or page chrome.

`PRODUCT.md` remains authoritative for business truth and safety. `DESIGN.md` remains authoritative for product-wide visual direction. This document is authoritative for **which UI component pattern to use and how to compose it**.

The goal is not to imitate screenshots. The goal is to build a coherent application from the same small set of current shadcn primitives and TanStack patterns.

## 2. Source and base-library policy

The local registry configuration is authoritative:

```json
{
  "style": "radix-nova",
  "baseColor": "neutral",
  "cssVariables": true,
  "iconLibrary": "lucide"
}
```

Therefore:

- the existing product remains on the **Radix** shadcn component base;
- do not mix Base UI `render` composition into Radix components;
- Radix composition uses `asChild` where the shadcn component exposes it;
- new or touched Radix primitive internals use the unified `radix-ui` package;
- feature code must not import Radix primitives directly;
- feature code imports only project-owned `@/components/ui/*` components;
- changing shadcn's global default for new projects is not a reason to rewrite this production application to a different primitive library;
- primitive-library migration requires a separate architecture decision, not opportunistic component edits.

Reference:

- https://ui.shadcn.com/docs/components
- https://ui.shadcn.com/docs/changelog/2026-02-radix-ui
- https://ui.shadcn.com/docs/changelog/2026-07-base-ui-default
- https://ui.shadcn.com/docs/components/radix

## 3. Component ownership

### `components/ui`

`components/ui` owns reusable visual primitives and their direct shadcn-style composition.

Allowed responsibilities:

- focus/hover/disabled/invalid states;
- semantic variants and sizes;
- primitive accessibility wiring;
- common spacing inside the primitive;
- `data-slot` anatomy;
- Radix integration;
- generic composition such as `CardHeader`, `FieldLabel`, `DialogFooter`.

Not allowed:

- HVAC business terms;
- site/device/alarm-specific behavior;
- server fetching;
- route parsing;
- authorization rules;
- feature-specific table columns;
- fake application state.

### Feature components

Feature code owns business composition:

- columns and filters;
- business copy;
- domain state;
- query/mutation behavior;
- row actions;
- section arrangement;
- route-owned state passed in as props;
- truthful empty/loading/error states.

Do not create a second generic UI framework under a feature folder.

## 4. Page composition

Ordinary route surfaces use semantic markup plus shadcn components.

Recommended anatomy:

```text
Route Surface
├── compact page intro
│   ├── scope/site
│   ├── h1
│   ├── supporting fact/freshness
│   └── local actions
├── optional concise summary
├── task controls / tabs
└── primary work surface
    ├── ledger / analysis / form / canvas
    └── contextual supporting content
```

Rules:

- default page padding follows the shell: approximately `px-4 py-6`, growing to `px-6` where desktop density benefits;
- ordinary content uses a purposeful max-width; engineering canvases may be fluid;
- do not recreate a universal boxed `PageHeader`;
- do not put each paragraph or metric in a Card;
- do not create page-specific shadow/radius systems;
- Tailwind composition is preferred over large feature CSS files;
- dedicated CSS is reserved for hard visualization/layout cases such as X6 canvas internals, not standard Cards, tables, forms, or dialogs.

## 5. Card

Reference: https://ui.shadcn.com/docs/components/radix/card

Use `Card` for a **coherent semantic section** that benefits from its own title/action/content boundary.

Preferred anatomy:

```tsx
<Card>
  <CardHeader>
    <CardTitle />
    <CardDescription />
    <CardAction />
  </CardHeader>
  <CardContent />
  <CardFooter /> // only when the section really has a footer/action boundary
</Card>
```

Rules:

- use `CardAction` for header-local actions rather than hand-positioned absolute buttons;
- use `size="sm"` for dense secondary facts, summaries, inspectors, and admin panels;
- default Card has border hierarchy, not decorative shadow;
- a collection of data rows is usually a Table, not a wall of Cards;
- nested Cards require a real semantic hierarchy; prefer border-only sub-sections otherwise;
- a metric Card must show an authoritative fact; do not add fake trends or opaque scores to make it look richer.

## 6. Data Table / Data Grid

References:

- https://ui.shadcn.com/docs/components/radix/data-table
- https://github.com/sadmann7/tablecn
- `docs/architecture/shadcn-tablecn-reui-source-review-2026-09-20.md`

The project uses **TanStack Table v9**.

### 6.1 Ordinary operational ledger

The default ledger architecture is:

```text
tablecn interaction/composition grammar
+
project-owned TanStack Table v9 behavior
+
shadcn Table primitives
```

Required behavior pattern:

```ts
const features = tableFeatures({ ...onlyFeaturesActuallyNeeded })
type Features = typeof features
const columns: Array<ColumnDef<Features, Row>> = [...]
const table = useTable({ key, features, data, columns })
```

Rules:

- shadcn `Table` renders semantic markup and visual density;
- TanStack Table v9 owns table state/behavior;
- tablecn is the default reference for toolbar, filters, sorting, pagination, column visibility and selection grammar;
- each domain table defines its own columns, filters, selection, row actions, URL/server state, and business semantics;
- do not create one mega `DataTable` API that attempts to encode every screen;
- do not introduce a competing ProTable / AdvancedTable / SmartTable / SuperTable family;
- extract reusable controls only after the same behavior appears in multiple real tables;
- register only v9 features actually used;
- scan-heavy domains such as Devices, Alarms and Work Orders default to a ledger/table rather than cards;
- row click may open a contextual inspector when maintaining list spatial memory is important;
- durable complex detail uses a Route.

### 6.2 ReUI Data Grid

ReUI Data Grid is a separate, advanced interaction choice rather than the default ledger.

It may be evaluated only when the task genuinely requires capabilities such as:

- spreadsheet-style cell navigation/editing;
- heavy row/column virtualization;
- tree/grid interaction;
- another explicit grid-specific behavior not served by the standard ledger.

Before use, inspect the exact ReUI **Radix** source and verify that its TanStack Table API matches the project's installed v9 baseline. Do not run ordinary operational ledgers through ReUI Data Grid merely for visual richness.

### 6.3 Advanced filtering

Ordinary table filtering follows the tablecn/project DataTable grammar.

Use ReUI Filters only when the product actually needs nested attributes, operator selection, AND/OR expression building, or a comparable advanced query workflow.

## Advanced application component layer

References:

- `docs/architecture/shadcn-tablecn-reui-source-review-2026-09-20.md`
- `docs/architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md`

ReUI, Kibo UI and Dice UI are approved copy-and-own sources above shadcn/ui for advanced application interactions. They do not replace the shadcn primitive authority.

Selection rules:

- use ReUI for strong workspace/composition candidates such as Frame, advanced Filters, Timeline, Tree/Cascader and justified advanced Data Grid;
- use Kibo UI for functionally rich components such as Gantt, Calendar, Editor, Dropzone and complex application blocks;
- use Dice UI for advanced interaction components such as Sortable, Kanban, Editable, Selection Toolbar, Action Bar, Tour, Tags Input and similar accessibility-heavy controls;
- use Radix variants for ReUI/Dice UI in the current `radix-nova` project;
- inspect Kibo UI component dependencies before adoption to ensure primitive/headless compatibility;
- ordinary operational ledgers remain tablecn + project TanStack Table v9;
- when two or more sources provide the same capability, select one implementation by task semantics, keyboard/a11y quality, dependency weight, Radix compatibility, source quality/tests and state ownership;
- never preserve parallel generic implementations behind compatibility wrappers.

## 7. Tabs

Reference: https://ui.shadcn.com/docs/components/radix/tabs

Tabs are for **peer views of the same task and context**.

Use Tabs for:

- active vs recovered alarm views;
- topology vs anomaly vs energy evidence in the same HVAC context;
- alternative analysis modes over the same scoped entity.

Do not use Tabs for:

- primary application navigation;
- unrelated modules;
- hiding an overgrown page architecture;
- replacing durable route state when a URL should be independently addressable.

If tab state matters for deep links, the Route owns the URL/search param and passes the selected tab plus callback to the feature.

## 8. Dialog, Alert Dialog, Sheet and contextual inspector

References:

- https://ui.shadcn.com/docs/components/radix/dialog
- https://ui.shadcn.com/docs/components/radix/alert-dialog
- https://ui.shadcn.com/docs/components/radix/sheet

### Dialog

Use `Dialog` for a short, focused modal task where background interaction should pause:

- acknowledge with an optional note;
- assign with a small form;
- small create/edit action with limited fields.

Dialog content should normally have:

```text
DialogHeader
  DialogTitle
  DialogDescription
Task content / FieldGroup
DialogFooter
  secondary action
  primary action
```

### Alert Dialog

Use `AlertDialog` for consequential confirmation where the user should explicitly understand risk before proceeding:

- destructive delete;
- irreversible reset;
- dangerous control action confirmation.

Do not use ordinary Dialog for a dangerous action just because it is already available.

### Sheet

Use Sheet for transient auxiliary content that benefits from remaining spatially attached to the page but does not deserve a durable route.

Do not make Sheet the universal detail pattern. Every active Sheet must provide a `SheetTitle` (it may be visually hidden when the visible surface already supplies the heading); every active Dialog must provide a `DialogTitle` for the same accessibility reason.

### Contextual inspector

A same-page inspector is preferred over Sheet when the user is scanning a table/canvas and repeatedly opening neighboring records while preserving place memory. It must remain subordinate to the primary work surface.

For wide scan-heavy workspaces, compose the official shadcn `ResizablePanelGroup` / `ResizablePanel` / `ResizableHandle` as the layout boundary:

```text
Ledger / list
+ ResizableHandle
+ Context Inspector
```

Rules:

- the ledger remains the larger primary panel;
- panel size is layout state and is not written to Router search params unless a real product requirement appears;
- selection updates inspector content in place;
- the resizable primitive owns sizing mechanics only; business selection/evidence/actions stay in the feature;
- below the workspace's validated wide breakpoint, keep the ledger intact and represent the same inspector content with official shadcn `Sheet`;
- do not keep desktop Resizable and modal Sheet active at the same breakpoint.

### Durable detail

A long-lived detail with its own history, sections, commands, or shareable identity uses a Route.

## 9. Field and forms

References:

- https://ui.shadcn.com/docs/components/radix/field
- https://ui.shadcn.com/docs/components/radix/input
- https://ui.shadcn.com/docs/components/radix/textarea
- https://ui.shadcn.com/docs/components/radix/select

Use `Field` to compose labels, controls, help text and validation.

Preferred anatomy:

```tsx
<Field data-invalid={hasError || undefined}>
  <FieldLabel htmlFor="...">...</FieldLabel>
  <Input id="..." aria-invalid={hasError || undefined} />
  <FieldDescription>...</FieldDescription>
  <FieldError>...</FieldError>
</Field>
```

Related controls use `FieldGroup`; semantic groups use `FieldSet` + `FieldLegend`.

Rules:

- do not hand-roll dozens of inconsistent `<label><Input /></label>` layouts;
- every control has an accessible label unless its accessible name is supplied by an explicit approved pattern;
- helper text explains business meaning, not obvious UI mechanics;
- validation uses both visual `data-invalid` and accessible `aria-invalid` where relevant;
- `FieldError` stays next to the relevant control;
- forms use React Hook Form + Zod where validation/state complexity warrants it; tiny one-field dialogs do not need abstraction for abstraction's sake.

## 10. Input Group and search

Reference: https://ui.shadcn.com/docs/components/radix/input-group

Use `InputGroup` when an icon, result count, unit, suffix, or inline action is semantically part of the input control.

Examples:

- search icon + query input;
- query input + `12 results` suffix;
- numeric value + unit;
- compact clear/action button inside the input boundary.

Do not repeatedly create search fields with an absolutely positioned icon over a standalone Input.

Use ordinary `Input` when no addon belongs inside the field boundary. Inside `InputGroup`, use `InputGroupInput` / `InputGroupTextarea`; those project components compose the official shadcn `Input` / `Textarea` and own the grouped border/focus reset.

## 11. Select vs Combobox vs Command

Use `Select` when the option set is known, reasonably small, and the user chooses one value.

Use a Combobox/Command composition when:

- options are large;
- text filtering is needed;
- keyboard search is central to the task;
- remote/async lookup is needed.

Do not put hundreds of Device leaf nodes in a Select. Organizational scope Selects contain meaningful scope levels; device discovery belongs in Device Table/search.

Composition rules:

- `SelectItem` belongs inside `SelectGroup`, which belongs inside `SelectContent`;
- `CommandItem` belongs inside `CommandGroup`, which belongs inside `CommandList`;
- preserve the project's compact Select trigger density and `popper` positioning unless a Surface has a documented reason to change them.

## 12. Badge

Reference: https://ui.shadcn.com/docs/components/radix/badge

Badges communicate concise status/category metadata.

Use:

- `destructive` only for a genuinely destructive/critical fact;
- `outline` for neutral status/category;
- `secondary` for low-emphasis metadata;
- semantic utility classes when success/warning/information status is real and project tokens support it.

For repeated product status semantics, use `@/components/status-badge`. `StatusBadge` is a thin semantic mapping that renders the official shadcn `Badge`; it is not a second primitive and must not be re-exported from unrelated component packages such as DataTable.

Do not use Badge for:

- primary numerical metrics;
- long explanations;
- decoration;
- status that is already obvious from adjacent text and gains no scanning value.

## 13. Button and actions

Button hierarchy:

- default: primary local action;
- outline: secondary action;
- ghost: low-emphasis table/toolbar/navigation action;
- destructive: destructive action only;
- link: actual low-emphasis text action where button chrome would be noise.

Operational toolbars prefer `size="sm"` or `xs`; primary workflow confirmations may use default size.

Rules:

- one dominant primary action per local decision area when practical;
- icon-only buttons require `aria-label`; add Tooltip when meaning is not universally obvious;
- do not color every action;
- links rendered with Button use `asChild` under the Radix component base;
- disabled state must correspond to a real unavailable action and should have nearby explanatory copy when the reason is not obvious.

## 14. Dropdown Menu and row actions

Use Dropdown Menu when a row or entity has multiple secondary actions that would otherwise make the table noisy.

Keep the most common safe action visible if it materially improves throughput.

Do not hide the primary task behind a three-dot menu merely to look minimal.

Group related menu items with `DropdownMenuGroup`; separate materially different groups with `DropdownMenuSeparator`. Use the shared destructive item variant for destructive actions instead of caller-owned red text classes.

## 15. Tooltip / Popover

Tooltip explains a compact control or unavailable state; it does not contain critical workflow content.

Popover contains small non-modal supplemental interaction such as compact secondary filters.

If content requires a heading, form, multi-step reasoning or persistent state, use Dialog/Sheet/Route instead.

## 16. Loading, empty and error states

Use the same surface hierarchy as the content being loaded.

- Skeleton: known content shape where preserving geometry matters;
- small spinner: short inline operation;
- empty state: states what is empty and what filter/scope can change it;
- error state: states the affected business capability and offers a meaningful retry if available.

Never fill unavailable business data with examples just to avoid an empty screen.

## 17. Responsive behavior

Responsive design changes composition, not merely scale.

- summary grids collapse from 4 → 2 → 1 as appropriate;
- page actions wrap or move below headings;
- scan-heavy contextual inspectors use a wide-screen shadcn Resizable split only while both ledger and inspector remain readable; below that validated breakpoint the same inspector content moves to Sheet;
- table secondary columns may be hidden based on task priority;
- global Sidebar uses the current official shadcn Sidebar implementation: 16rem desktop width, fixed container + layout gap, icon collapse behavior and Provider cookie contract;
- Vite AppShell reads `sidebar_state` at the integration boundary and passes it to `SidebarProvider.defaultOpen`; do not fork the primitive for persistence;
- `SidebarInset` may receive `min-w-0` at the call site so the official `w-full flex-1` inset can shrink beside the fixed Sidebar without page-level overflow at the `md` boundary;
- feature inspectors use official `Sheet`; widths wider than its default `sm:max-w-sm` are declared explicitly at the feature call site rather than by reintroducing a project `SheetBody` or modifying `sheet.tsx`;
- engineering canvas gets minimum usable dimensions and dedicated responsive handling.

Do not shrink dense desktop dashboards until all content is technically visible but unreadable.

## 18. Iconography

Lucide is the product icon language.

- default UI icon size: 14–16 px in compact controls;
- no mixed icon libraries in migrated surfaces;
- icons support labels, they do not replace unfamiliar business terminology;
- decorative icons are optional and should be sparse.

## 19. CSS and styling policy

Order of preference:

1. existing `components/ui` primitive;
2. Tailwind composition in the feature;
3. small reusable project component;
4. dedicated CSS only for visualization/geometry that Tailwind composition cannot express clearly.

Forbidden migration patterns:

- importing old Ant/Pro CSS into a migrated surface;
- retaining a 1000-line legacy feature stylesheet only to preserve old class geometry;
- compatibility wrappers that mimic Ant component props;
- page-level token systems that compete with global semantic variables;
- arbitrary hard-coded colors for semantic states.

Test-facing class names or `data-testid` may remain temporarily when they do not carry legacy styling. They are not a visual compatibility contract.

## 20. Feature-specific defaults

### Dashboard

Concise authoritative summary Cards + operational sections. Avoid KPI walls.

### Device Center

TanStack v9 Data Table first. Scope/search toolbar. Same-page inspector only for rapid scanning; durable detail Route.

### Alarm Center

Compact triage summary + filters + TanStack v9 ledger. Row opens contextual inspector. ACK/Assign uses Dialog. Consequential future actions use AlertDialog. History and rules remain truthful business work surfaces, not decorative dashboards.

### Work Orders

TanStack v9 ledger first; owner/SLA/status/source/next action are scan priorities. Durable detail Route.

### Energy Analytics

Breadcrumb + Page Header + period/context controls + capability-appropriate primary chart + compact comparison cards + drill-down Table. Ordinary application charts use shadcn Chart + Recharts; engineering-grade dense time-series/multi-axis/dataZoom/brush work uses Apache ECharts.

### HVAC Realtime

Standard page intro + compact summary + Tabs + Card surrounding the real X6 engineering canvas/evidence. Domain canvas is allowed to be special; surrounding controls are not.

## 21. Review checklist

Before a migrated Surface is accepted:

- [ ] no Ant/Pro component import remains in the migrated feature;
- [ ] no old feature CSS is required for standard shadcn layout;
- [ ] feature code does not import Radix primitives directly;
- [ ] component base matches `components.json` (`radix-nova`);
- [ ] no Base UI `render` API is mixed into Radix components;
- [ ] ordinary operational ledgers follow the project tablecn grammar on TanStack Table v9;
- [ ] ReUI Data Grid is used only for a justified advanced grid task and its selected Radix source/API has been reviewed;
- [ ] ReUI and Dice UI primitive-specific components use the Radix path;
- [ ] selected Kibo UI components have had their primitive/headless dependencies reviewed;
- [ ] overlapping ReUI/Kibo/Dice capabilities resolve to one selected project implementation rather than parallel variants;
- [ ] advanced component adoption records an exact upstream tag/commit and reviewed source/tests/docs;
- [ ] forms use Field grammar for labels/help/errors;
- [ ] search/addon inputs use Input Group when appropriate;
- [ ] Dialog/AlertDialog/Sheet/Route choice matches task lifetime and consequence;
- [ ] Card usage represents real semantic sections;
- [ ] semantic colors correspond to real facts;
- [ ] loading/empty/error states do not fabricate data;
- [ ] keyboard/focus/disabled/selected states are preserved;
- [ ] responsive composition is reviewed in a real browser;
- [ ] production TypeScript/build and relevant business-contract tests pass.

## 22. Sources reviewed

Current official references:

- https://ui.shadcn.com/docs/components
- https://ui.shadcn.com/docs/components/radix/data-table
- https://ui.shadcn.com/docs/components/radix/card
- https://ui.shadcn.com/docs/components/radix/tabs
- https://ui.shadcn.com/docs/components/radix/dialog
- https://ui.shadcn.com/docs/components/radix/alert-dialog
- https://ui.shadcn.com/docs/components/radix/sheet
- https://ui.shadcn.com/docs/components/radix/field
- https://ui.shadcn.com/docs/components/radix/input
- https://ui.shadcn.com/docs/components/radix/input-group
- https://ui.shadcn.com/docs/components/radix/textarea
- https://ui.shadcn.com/docs/components/radix/select
- https://ui.shadcn.com/docs/components/radix/badge
- https://ui.shadcn.com/docs/changelog/2026-02-radix-ui
- https://ui.shadcn.com/docs/changelog/2026-07-base-ui-default
- https://github.com/satnaing/shadcn-admin
- https://github.com/sadmann7/tablecn
- https://github.com/keenthemes/reui
- https://reui.io/components
- https://reui.io/components/data-grid
- https://reui.io/docs/components/base/filters
- https://github.com/shadcnblocks/kibo
- https://www.kibo-ui.com/docs
- https://www.kibo-ui.com/components/gantt
- https://www.kibo-ui.com/components/editor
- https://github.com/sadmann7/diceui
- https://diceui.com/docs/introduction
- https://diceui.com/docs/components

The sources define component grammar and proven application patterns. They do not override HVAC business truth or authorize copying example-domain data into this product.
