# HVAC Web Application UI & Surface Composition Specification

**Version:** 2.0  
**Status:** SELECTED / ACTIVE  
**Date:** 2026-09-20  
**Scope:** `apps/hvac-web`  
**UI stack:** shadcn/ui + tablecn + ReUI + Kibo UI + Dice UI + Tailwind CSS  
**State / data stack:** TanStack Router + Query + Table v9  
**Visualization:** shadcn Chart + Recharts for ordinary charts; Apache ECharts for engineering analytics

---

## 1. Purpose

This specification adapts the generic Dashboard UI guidance to the actual smart-energy / HVAC operations product.

The product is not a generic SaaS admin panel. Its primary surface families are:

- HVAC operations and realtime monitoring;
- energy analytics;
- alarms and diagnosis;
- devices and assets;
- work management;
- control and strategy;
- data quality and governance;
- verification and audit.

The UI must help users move through:

```text
Orient
→ Detect
→ Prioritize
→ Investigate
→ Act
→ Verify
```

A page does not need to contain every stage, but it must make its own role in the operational loop clear.

---

## 2. Authority order

When rules conflict, follow the repository authority chain defined by `DESIGN.md`.

This document owns application-level UI composition patterns. It does not override:

- business truth in `PRODUCT.md`;
- page responsibility in Surface Specifications;
- routing/state ownership;
- safety or authorization;
- domain-specific lifecycle semantics.

Component libraries are implementation inputs, not product-information architecture.

---

## 3. Component architecture

The project uses four complementary sources.

### Layer 1 — shadcn/ui

shadcn/ui is the primitive, token and accessibility baseline.

Use it for standard controls including:

- Button;
- Input / Input Group;
- Select / Combobox;
- Checkbox / Radio / Switch;
- Dialog / AlertDialog / Sheet;
- Dropdown Menu;
- Popover / Tooltip;
- Tabs;
- Card;
- Badge;
- Sidebar;
- Breadcrumb;
- Skeleton;
- Empty;
- Alert;
- Sonner;
- Table primitives;
- Chart composition primitives.

Rule:

> If shadcn/ui already expresses the interaction correctly, do not create another primitive.

### Layer 2 — tablecn

tablecn is the default **table / ledger interaction and composition reference**.

Use the project-owned TanStack Table v9 implementation to express tablecn patterns for:

- toolbar structure;
- sorting;
- faceted filtering;
- filter menus/lists;
- pagination;
- column visibility;
- row selection;
- server-side state;
- dense operational ledgers.

Rule:

> Scan-heavy operational tables use one project DataTable grammar. Do not create competing ProTable / AdvancedTable / SmartTable / SuperTable systems.

Because upstream tablecn v9 support is still evolving, adopt its UX/composition patterns into the project's v9-native table layer instead of assuming source-level drop-in compatibility.

### Layer 3 — Advanced application components

ReUI, Kibo UI and Dice UI participate as the advanced shadcn-compatible application-component layer when base shadcn/ui, official blocks and the specialized table layer do not provide enough capability.

#### ReUI

Preferred candidates:

- Frame;
- advanced Filters;
- Timeline;
- Kanban;
- Gantt;
- Event Calendar;
- Tree / Cascader;
- richer composed application workspaces.

Use the **Radix flavor** for primitive-specific ReUI components because the application baseline is `radix-nova`.

ReUI Data Grid is reserved for genuinely grid-like tasks that need capabilities beyond the standard ledger, such as spreadsheet editing, heavy virtualization or tree/grid interaction. It is not the ordinary table default.

#### Kibo UI

Preferred candidates:

- Gantt / scheduling;
- Calendar;
- rich text Editor;
- Dropzone / file workflows;
- complex content/application blocks;
- other functionally rich shadcn-compatible components that would otherwise require substantial custom implementation.

Kibo UI Table does not replace the project operational ledger. Before adoption, inspect the selected component's actual primitive/headless dependencies and confirm compatibility with the current `radix-nova` project.

#### Dice UI

Preferred candidates:

- Sortable / drag-and-drop;
- Kanban;
- Editable;
- Selection Toolbar / Action Bar;
- Tour;
- Tags Input / Mention / Listbox;
- File Upload / rich media interactions;
- advanced accessibility-heavy controls not present in shadcn/ui.

Dice UI maintains Radix and Base UI source paths; the current project uses the **Radix** path.

#### Shared rule

ReUI, Kibo UI and Dice UI are candidate source libraries, not parallel runtime frameworks. If several expose the same capability, choose one implementation by task semantics, accessibility/keyboard model, dependency weight, Radix compatibility, source quality/tests and state-model fit. Do not retain duplicate generic implementations.

### Layer 4 — project domain components

Create project-owned components only for stable HVAC / energy semantics or repeated composed product patterns.

Examples:

- operational status presentation;
- data freshness / quality presentation;
- alarm severity semantics;
- verification state;
- equipment process-value groups.

Do not put domain rules into generic shadcn primitives.

---

## 4. Component selection ladder

Before building a new UI pattern:

```text
1. Existing project component
2. shadcn/ui primitive
3. shadcn official block
4. tablecn/project DataTable layer when tabular
5. approved project domain component
6. Advanced application component source: ReUI / Kibo UI / Dice UI
7. Shadcnblocks application block / composed pattern
8. small project-owned composition
9. custom component only if the above cannot express the task
```

Do not choose a richer library component merely because it looks more impressive. Choose it because the task needs its interaction model.

---

## 5. Primitive architecture

The project primitive baseline is fixed:

```text
shadcn/ui
style: radix-nova
primitive base: Radix
composition: asChild
```

Rules:

- business code imports `@/components/ui/*`;
- do not import primitive internals directly from feature code;
- do not mix Base UI `render` composition into the current application;
- when ReUI or Dice UI offers Base UI and Radix versions, use Radix;
- before adopting Kibo UI, inspect the selected component's primitive/headless dependencies and keep it compatible with the current Radix project;
- Shadcnblocks is an application-block source only: inspect exact-item CLI diffs and do not accept incidental overwrites of project primitives without separate review;
- do not create Radix/Base compatibility wrappers.

---

## 6. Standard application shell

```text
Application Shell
├── Sidebar
│   ├── Product / site context
│   ├── Primary navigation
│   ├── Grouped secondary navigation
│   └── User/help footer
│
├── App Header
│   ├── Sidebar trigger
│   ├── Global command/search
│   ├── Global realtime/system status when truly global
│   ├── Notifications
│   ├── Theme
│   └── Account
│
└── Main
    └── Route-owned Surface
```

The global header does not need to repeat the page H1.

---

## 7. Standard page opening

The standard page opening is:

```text
Breadcrumb

Page Header
├── Title
├── Optional description / scope / freshness
└── Primary and secondary local actions
```

This structure is approved.

Example:

```text
站点 / 东京中央冷站 / 工单

工单中心                                      [导出] [创建工单]
处理维护、整改与现场执行工作。
数据更新于 09:30
```

Rules:

- one route-owned H1;
- Breadcrumb communicates hierarchy, not a second hero title;
- description exists only when it adds task context;
- local actions align with the Page Header;
- do not wrap the Page Header in a decorative Card;
- do not repeat the same page title again in the first Card or section;
- section titles describe the section's job, not the route name.

Allowed:

```text
Breadcrumb: 站点 / 工单
H1: 工单中心
First section: 待处理工作
```

Rejected:

```text
App Header: 工单中心
H1: 工单中心
First Card: 工单中心
```

---

## 8. Surface archetypes

Do not force every page into one dashboard template.

### 8.1 Overview

```text
Breadcrumb
Page Header
Authoritative summary
Priority / exception handling
Primary evidence
Next workflows
```

Purpose: situational awareness and routing to action.

### 8.2 Ledger / operational list

```text
Breadcrumb
Page Header
Optional compact summary
Search / filters / view controls
Data Table
Pagination / result state
Optional contextual inspector
```

Default for Devices, Alarms and Work Orders.

### 8.3 Analysis

```text
Breadcrumb
Page Header + period/context controls
Primary quantitative visualization
Supporting comparisons / evidence
Drill-down table
```

Default for Energy, Trend, Efficiency, Demand and similar surfaces.

### 8.4 Diagnosis / investigation

```text
Breadcrumb
Page Header
Finding / problem statement
Evidence
Cause / confidence / competing explanation
Recommended next action
Related work / verification
```

### 8.5 Control / strategy

```text
Breadcrumb
Page Header
Current authoritative state
Candidate action / strategy
Constraints / safety / expected impact
Execution action
Readback / verification
Audit evidence
```

### 8.6 Durable detail

```text
Breadcrumb
Page Header
Identity + current state
Primary facts
Evidence / related entities
History
Actions
```

Complex durable details use a Route, not a default Sheet.

### 8.7 Engineering workspace

```text
Breadcrumb
Page Header
Compact controls / context
Primary engineering canvas or dense time-series surface
Contextual evidence / inspector
```

Use X6/G6/ECharts only where the task genuinely requires engineering visualization.

### 8.8 Governance / audit

```text
Breadcrumb
Page Header
Scope / policy context
Decision or state summary
Ledger
Audit evidence / provenance
```

---

## 9. Layout

These are project defaults, not universal laws.

### Desktop

Primary acceptance target.

```text
Page padding: 24px typical
Dense pages: 16–24px
Section gap: 24–32px
Grid gap: 16–24px
Ordinary content max-width: 1400–1600px
Engineering canvas: fluid when justified
```

A 12-column grid may be used when it improves alignment, but the project does not require every page to expose a literal 12-column implementation.

Do not compress wide operational tables merely to preserve a card grid.

### Responsive composition

Responsive behavior changes priority and composition rather than shrinking the desktop UI.

Laptop/tablet adaptations are part of normal layout design.

Mobile-specific product acceptance is not mandatory unless explicitly requested for the Surface.

---

## 10. Spacing

Use a consistent spacing rhythm:

```text
4
8
12
16
20
24
32
40
48
64
```

Avoid arbitrary spacing values without a real geometry reason.

Typical use:

- 4–8: icon/label relationships;
- 12–16: compact controls;
- 16–24: internal section spacing;
- 24–32: section separation;
- 48–64: only for major structural breaks.

---

## 11. Card

Card is a semantic section, not a universal layout wrapper.

Rule:

> One Card = one coherent topic or task boundary.

Use Card for:

- concise summaries;
- evidence panels;
- coherent analyses;
- work queues;
- bounded task modules.

Prefer border-only or plain semantic sections for:

- dense tables;
- fact groups;
- toolbars;
- layout-only grouping.

Avoid nested card walls.

---

## 12. KPI and summary metrics

KPI hierarchy:

```text
Label
Primary value
Comparison / context
Optional trend only when authoritative
```

Value has the strongest visual emphasis.

Do not add a chart simply to make a KPI look like a dashboard.

Do not force heterogeneous facts into equal-weight cards.

Separate:

- primary operating outcomes;
- scope/context facts;
- abnormal state;
- data-quality evidence.

All trends, deltas and percentages must have an authoritative data definition.

---

## 13. Filters

### Ordinary table filters

Use the project tablecn/TanStack v9 filter grammar.

Typical filters:

- Date;
- Status;
- Owner;
- Site;
- Asset;
- Priority;
- Region/category where relevant.

### Advanced filters

Use ReUI advanced Filters only when the business actually needs expressions such as:

```text
status = active
AND
impact > threshold
AND
(assetType = chiller OR assetType = pump)
```

Do not expose a query builder for a simple five-field dashboard.

### URL ownership

Shareable or workflow-important filter/sort/page state belongs in TanStack Router search params.

Refresh, Back/Forward and deep links should preserve meaningful state.

---

## 14. Charts

Charts answer a concrete quantitative question.

### Chart type

- time trend → Line;
- trend with volume emphasis → Area;
- category comparison → Bar;
- ranking → Horizontal Bar;
- composition → Stacked Bar;
- very small complete composition → Donut/Pie;
- exact comparison → Table;
- distribution/correlation → Scatter where justified.

Pie/Donut is not a default.

### Engine boundary

Use **shadcn Chart + Recharts** for ordinary application charts:

- simple Bar;
- simple Line/Area;
- Donut/Pie;
- small categorical comparisons;
- compact dashboard charts.

Use **Apache ECharts** for engineering analytics:

- high-density time series;
- multiple Y axes;
- dataZoom;
- brush;
- linked cursor;
- large datasets;
- dense equipment/process comparison;
- advanced engineering interaction.

Do not create a third chart abstraction.

### Chart truthfulness

Never fabricate:

- trends;
- smoothed history;
- health scores;
- forecast bands;
- complete composition from incomplete data.

---

## 15. Chart color and labeling

Use project semantic/chart tokens.

Business status colors and categorical chart colors are separate concerns.

Do not use red/yellow/green categorically when users may interpret those colors as Alarm/Warning/Healthy.

Color cannot be the only encoding.

Charts must communicate core meaning without hover through:

- title;
- unit;
- axes;
- direct labels or legend;
- necessary context.

Tooltip provides precision, not essential interpretation.

---

## 16. Data Table / Data Grid

### Simple static table

Use shadcn `Table`.

### Operational ledger

Use the project DataTable based on:

```text
tablecn composition grammar
+
TanStack Table v9 behavior
+
shadcn Table primitives
```

Feature owns:

- columns;
- domain filters;
- selection;
- sorting;
- row actions;
- URL state;
- server queries;
- business semantics.

Shared table infrastructure owns only proven repeated behavior.

### Advanced grid

Evaluate ReUI Data Grid only for a genuinely different grid task:

- spreadsheet-like editing;
- cell navigation/selection;
- heavy virtualization;
- tree/grid interaction;
- another justified advanced grid capability.

Do not replace an ordinary ledger with an advanced grid.

Before importing ReUI Data Grid, inspect the exact Radix source and confirm TanStack Table v9 compatibility.

---

## 17. Table behavior

Defaults:

- identity column left;
- numeric values right-aligned;
- text left-aligned;
- status uses semantic text + Badge where helpful;
- dates use one project format;
- row action menu is right-aligned;
- long identity/title text truncates without breaking layout;
- important identity/actions remain visible when a wide grid truly requires pinning.

At normal approved desktop widths, core operational ledgers should avoid unnecessary horizontal scrolling.

Do not solve density by reducing body text to unreadable sizes.

---

## 18. Density

Supported densities:

### Default

For analytics, management and ordinary application work.

### Compact

For logs, ledgers, operations and high-throughput review.

Density changes primarily through:

- row height;
- vertical padding;
- toolbar spacing;
- card spacing.

It should not be achieved by making essential text tiny.

---

## 19. Loading

Use partial/local loading.

```text
Summary → Skeleton
Chart → shape-preserving skeleton
Table → row skeleton
Action → local pending state
```

Do not block an entire Surface behind one central spinner when independent sections can render.

---

## 20. Empty, no-result, permission and error states

Keep these states distinct.

### No data

The source has no records.

### No results

Current filters exclude all records.

### Permission

The user cannot access the data/action.

### Error

The capability failed to load or execute.

Use shadcn Empty / Alert patterns and state the affected business capability.

Do not show fake sample data to avoid an empty page.

---

## 21. Partial failure

One failed secondary API must not automatically destroy an otherwise usable Surface.

Example:

```text
Current load        ✅
Active alarms       ✅
Efficiency trend    Unable to load [Retry]
Work queue          ✅
```

Escalate to a page-level error only when the page's core job cannot be performed.

---

## 22. Actions

Each local decision area should have one clearly dominant primary action when practical.

Use:

```text
Primary
Secondary
Ghost
Dropdown
Destructive
```

Do not style every action as primary.

High-consequence actions require the appropriate confirmation semantics.

---

## 23. Toast

Use Sonner for transient success/progress feedback such as:

- changes saved;
- report exported;
- assignment completed.

Do not use Toast as the only presentation for:

- form validation;
- blocking permission errors;
- page-level failures;
- safety-critical control failures.

Those belong near the affected task.

---

## 24. Design tokens

Business UI uses semantic tokens.

Prefer:

```text
bg-background
bg-card
text-foreground
text-muted-foreground
border-border
bg-primary
text-primary-foreground
bg-destructive
ring-ring
chart-*
```

Do not introduce feature-local color systems that compete with global tokens.

---

## 25. Radius, border and shadow

Radius derives from the project theme scale.

Do not assign arbitrary radius values component-by-component.

For normal dashboard/workspace surfaces:

```text
Border > Shadow
```

Strong elevation is mainly for overlays such as:

- Dialog;
- Popover;
- Dropdown;
- floating UI.

Operational hierarchy comes from structure, spacing and typography.

---

## 26. Typography

Default scale:

```text
Page title      24 / 32 / 600
Section title   18 / 28 / 600
Card title      14 / 20 / 500–600
Body            14 / 20 / 400
Secondary       13 / 20 / 400
Caption         12 / 16 / 400
Metric          28–32 / 36–40 / 600
```

Use tabular numerals for aligned operational values.

Do not use landing-page typography in dense operations tools.

---

## 27. Industrial data truthfulness

This is a project-specific mandatory rule.

Never collapse distinct dimensions:

```text
Physical state
Running / Stopped / Fault

Connectivity
Online / Offline

Freshness
Fresh / Delayed / Stale

Data quality
Good / Suspect / Missing
```

Mandatory invariants include:

```text
Offline ≠ Fault
Missing ≠ Zero
Unknown ≠ Healthy
Completed ≠ Verified
Alarm ≠ Diagnosis
Finding ≠ Work Order
Work Priority ≠ Alarm Severity
```

Do not use green or “normal” merely because a value is unavailable.

Do not infer business facts from missing technical data.

---

## 28. Operational closure and verification

Actions do not end at “save” or “complete” when the business requires confirmation.

The UI should preserve workflows such as:

```text
Alarm
→ Investigation / Diagnosis
→ Corrective Action / Work Order
→ Physical completion
→ Functional verification
→ Closure / monitoring
```

Control and strategy workflows likewise require readback/verification where applicable.

The UI must not imply that execution success equals outcome success.

---

## 29. Accessibility

Target WCAG 2.2 AA.

Core requirements:

- readable contrast;
- visible keyboard focus;
- keyboard-operable controls;
- icon-only buttons have accessible names;
- target sizes follow WCAG 2.2 expectations;
- sticky headers/toolbars do not hide focused elements;
- charts do not depend only on color or mouse hover;
- status meaning remains available to assistive technology.

Library defaults help but do not replace page-level accessibility review.

---

## 30. Responsive strategy

Responsive design changes composition and priority.

Desktop remains the primary product/workspace target unless a Surface explicitly declares otherwise.

Possible adaptations include:

- summary grids collapsing;
- secondary actions wrapping;
- inspectors moving below primary content;
- lower-priority table columns hiding;
- controlled horizontal scrolling for genuinely wide grids.

Do not compress a 12-column engineering ledger into unreadable mobile columns.

---

## 31. Recommended component mapping

| Product need | Default implementation |
| --- | --- |
| App navigation | shadcn Sidebar |
| Breadcrumb | shadcn Breadcrumb |
| Page actions | shadcn Button / Dropdown Menu |
| Standard Page Header | semantic markup + shadcn controls |
| KPI / summary | shadcn Card or semantic section |
| Simple date/status control | shadcn primitives |
| Table filters | tablecn/project DataTable filters |
| Advanced boolean/nested filters | ReUI Filters when justified |
| Ordinary charts | shadcn Chart + Recharts |
| Engineering analytics | Apache ECharts |
| Static/simple table | shadcn Table |
| Operational ledger | tablecn grammar + TanStack Table v9 |
| Spreadsheet/virtualized grid | evaluate ReUI Data Grid |
| Structured composed block | evaluate ReUI Frame / Kibo application blocks |
| Timeline | evaluate ReUI or Dice UI; select one implementation |
| Kanban | evaluate ReUI or Dice UI; select one implementation |
| Gantt / scheduling | evaluate Kibo UI or ReUI; select one implementation |
| Rich text editing | Kibo UI Editor when justified |
| Dropzone / file workflow | Kibo UI Dropzone or Dice UI File Upload by task fit |
| Sortable / drag-and-drop | Dice UI Sortable by default candidate |
| Inline editing | Dice UI Editable by default candidate |
| Selection/action toolbar | Dice UI Selection Toolbar / Action Bar |
| Guided product tour | Dice UI Tour |
| Loading | shadcn Skeleton |
| Empty | shadcn Empty |
| Error | shadcn Alert |
| Toast | shadcn Sonner |
| Temporary auxiliary detail | shadcn Sheet |
| Confirmation | shadcn AlertDialog |
| Small form task | shadcn Dialog |
| Status | shadcn Badge + text |
| Engineering topology | X6 |
| Dynamic relationship graph | G6 |

---

## 32. Default analytics template

For a genuinely analytics-oriented page, this is a useful starting point:

```text
Breadcrumb

Page Header                               Actions
Title
Description / scope / freshness

Period / context filters

Authoritative summary

Primary analysis                  Supporting breakdown

Drill-down Data Table
```

This is a **template for analytics**, not the universal page architecture.

---

## 33. Definition of Done

### Product truth

- Are all visible metrics authoritative?
- Are missing/offline/unknown states represented truthfully?
- Does the page preserve the business lifecycle and verification semantics?

### Information architecture

- Is the Surface clearly one of the approved archetypes or justified otherwise?
- Can the user identify the current scope and primary task quickly?
- Does the Page Header contain useful context rather than repeated prose?
- Are section titles task-specific rather than repeating the route title?

### Components

- Was existing project/shadcn/tablecn/ReUI/Kibo UI/Dice UI capability checked before custom implementation?
- Are ReUI and Dice UI components using the Radix path?
- Was the selected Kibo UI component's primitive/headless dependency model reviewed?
- When multiple advanced libraries provide the same capability, is there one explicit selected implementation rather than parallel variants?
- Is there only one implementation path for ordinary operational tables?
- Is any advanced Data Grid justified by an actual grid interaction requirement?

### Filters/state

- Is meaningful state in the URL when it should be shareable/recoverable?
- Can filters be cleared?
- Is the scope of filters obvious?

### Charts

- Is the chart type appropriate?
- Is the engine choice appropriate?
- Are units and context explicit?
- Is critical meaning available without hover?
- Is any trend/forecast/composition based on real source data?

### Tables

- Are columns prioritized for scanning?
- Are numeric values aligned?
- Is long content controlled?
- Are loading/empty/error states complete?
- Is horizontal scrolling justified rather than accidental?

### Interaction/accessibility

- Is focus visible?
- Do icon-only actions have accessible labels?
- Are Dialog/AlertDialog/Sheet/Route choices appropriate to task duration and consequence?
- Are destructive/control actions explicit?

### Visual quality

- No decorative card wall;
- no duplicated route title in downstream sections;
- no unnecessary pill proliferation;
- no arbitrary color/radius systems;
- no empty fixed-height wasteland;
- no generic SaaS composition that ignores the HVAC task.

### Verification

- relevant TypeScript/tests/build pass;
- the actual rendered desktop Surface is visually reviewed;
- visual approval is not inferred from a green build alone.

---

## 34. Governing principle

The final rule is:

> **Reuse mature interaction systems aggressively, but let HVAC business truth and Surface responsibility decide the page.**

shadcn/ui, tablecn, ReUI, Kibo UI and Dice UI are there to reduce reinvention. They must not turn every operational Surface into the same generic dashboard or create parallel implementations of the same capability.
