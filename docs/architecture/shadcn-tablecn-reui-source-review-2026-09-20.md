# shadcn/ui + tablecn + ReUI Source Review — 2026-09-20

> **Status:** SELECTED / ACTIVE SOURCE REVIEW  
> **Scope:** `apps/hvac-web` frontend component and composition architecture  
> **Decision owner:** project design system  
> **Related authorities:** `DESIGN.md`, `docs/design-system/hvac-application-ui-specification.md`, `docs/design-system/shadcn-component-contract.md`

## 1. Why this review exists

The project needs a richer application-component vocabulary than base shadcn/ui blocks alone provide, while preserving one coherent primitive architecture and one coherent data-workspace architecture.

The selected model is:

```text
shadcn/ui
  = primitive + token + accessibility baseline

satnaing/shadcn-admin
  = application-shell and admin-product composition reference

sadmann7/tablecn
  = default data-table / ledger interaction and composition reference

ReUI
  = one approved source in the advanced application-component layer
    alongside Kibo UI and Dice UI; see the dedicated advanced-component source review

TanStack Table v9
  = table state/behavior engine owned by the project

Recharts + shadcn Chart
  = ordinary application charts

Apache ECharts
  = engineering-grade HVAC analytics
```

This is a reuse-first architecture, not a multi-framework compatibility layer. Kibo UI and Dice UI are reviewed separately in `docs/architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md`; all three advanced sources share the same no-duplicate-implementation rule.

## 2. Sources reviewed

### shadcn/ui

Reviewed current official Data Table guidance:

- https://ui.shadcn.com/docs/components/base/data-table

Current guidance uses TanStack Table v9 and explicitly recommends domain-specific tables rather than one universal table API.

### sadmann7/tablecn

Reviewed:

- https://github.com/sadmann7/tablecn
- https://github.com/sadmann7/tablecn/blob/main/registry.json
- https://github.com/sadmann7/tablecn/issues/1144

The registry exposes a substantial Data Table / Data Grid composition including filtering, sorting, pagination, advanced filter controls and virtualization-oriented grid capabilities.

Important compatibility evidence as of 2026-09-20:

- upstream issue #1144, opened 2026-08-14, tracks TanStack Table v9 support and remains open;
- therefore the repository must not assume that copying the current upstream tablecn implementation verbatim is compatible with this project's TanStack Table v9 baseline.

The project therefore adopts tablecn's **UX, composition and interaction grammar** while retaining a project-owned TanStack Table v9-native implementation.

### ReUI

Reviewed:

- https://github.com/keenthemes/reui
- https://reui.io/components
- https://reui.io/components/data-grid
- https://reui.io/docs/components/base/frame
- https://reui.io/docs/components/base/filters
- https://github.com/keenthemes/reui/issues/111

ReUI provides a large shadcn-compatible catalog plus in-house components such as Frame, Filters, Timeline, Kanban, Gantt, Event Calendar, Tree/Cascader and Data Grid. It follows the copy-and-own registry model and publishes both Radix and Base UI flavors.

Compatibility evidence needs component-level verification. In particular, upstream issue #111 recorded that ReUI Data Grid source on `main` at commit `c2f1dce` still targeted TanStack Table v8 at that point, while current public docs now describe a v9 Data Grid. This divergence is exactly why the project must inspect the selected source before adoption rather than relying on catalog prose alone.

### Source pin policy

This review approves **architectural roles and selection boundaries**, not an unpinned source import.

Before copying any new tablecn or ReUI component source into production code, the implementation change must record the exact upstream tag/commit and the concrete files/tests/docs reviewed. If no suitable stable release exists, pin the exact reviewed commit. A catalog page or `main` branch alone is not an implementation pin.

The existing TanStack Table v9-native project DataTable is therefore not replaced merely because tablecn or ReUI exposes a similarly named component.

## 3. ADOPT

### 3.1 shadcn/ui as primitive authority

Adopt shadcn/ui for standard controls and semantic tokens. Business features import project-owned `components/ui`, not primitive-library internals.

### 3.2 tablecn as the default ledger/table grammar

Adopt tablecn patterns for scan-heavy tables:

- table toolbar structure;
- column header/sort affordances;
- faceted filters;
- filter menus/lists;
- column visibility;
- pagination;
- row selection;
- URL-shareable filter/sort state where appropriate;
- dense border-first table presentation.

Project tables remain TanStack Table v9-native and domain-specific.

### 3.3 ReUI as the advanced extension catalog

Adopt ReUI when base shadcn/ui and the project's tablecn layer do not already cover the required capability well.

Preferred ReUI candidates include:

- Frame-style composed work blocks;
- advanced Filters / nested filter builders;
- Timeline;
- Kanban;
- Gantt;
- Event Calendar;
- Tree / Cascader;
- Sortable / Stepper and similar higher-order interaction patterns;
- richer composed examples that can be adapted without importing a second visual system.

Use the **Radix flavor** when a ReUI component has primitive-specific variants, because `apps/hvac-web/components.json` selects `radix-nova`.

## 4. ADAPT

### 4.1 ReUI Data Grid

ReUI Data Grid is **not the default table path**.

Use it only for a genuinely grid-like task that requires capabilities beyond the standard project ledger, such as spreadsheet-style cell navigation/editing, heavy virtualization, tree/grid behavior, or another explicitly justified advanced interaction.

Before adoption:

1. inspect the exact selected ReUI Radix source;
2. confirm TanStack Table v9 compatibility against the project's installed API;
3. copy only the required source;
4. align tokens and interaction semantics with the project;
5. ensure it replaces rather than duplicates an existing implementation for that task.

Do not introduce a generic ReUI Data Grid merely because a page contains rows and columns.

### 4.2 ReUI Filters

For ordinary table filtering, use the tablecn/project DataTable filter grammar.

Use ReUI Filters when the business query genuinely requires nested attributes, operator choice, AND/OR expression building or another advanced filtering workflow.

### 4.3 ReUI visual examples

ReUI examples are composition references, not product templates. HVAC business truth, information hierarchy and Surface Specifications remain authoritative.

## 5. REJECT

Reject:

- a second Button / Select / Dialog / Popover / Tooltip primitive family;
- Base UI ReUI variants inside the current Radix application;
- simultaneous generic `DataTable`, `AdvancedTable`, `SuperTable`, ReUI Data Grid and tablecn wrappers competing for the same ledger task;
- copying upstream table/grid source without verifying the current TanStack Table API;
- using ReUI or tablecn example-domain content as HVAC product design authority;
- compatibility wrappers that switch between component families.

## 6. Selection ladder

Before creating a component or interaction:

```text
1. Existing project components/ui primitive
2. Current shadcn/ui primitive / official block
3. Existing project tablecn/TanStack v9 data-workspace layer, when tabular
4. Existing approved project domain component
5. ReUI Radix component / composition when it fills a real capability gap
6. Small project-owned composition
7. Custom component only when the above cannot satisfy the task
```

For table/grid work specifically:

```text
Simple static facts
→ shadcn Table

Interactive ledger / operational table
→ project tablecn grammar + TanStack Table v9

Spreadsheet/virtualized/advanced grid interaction
→ evaluate ReUI Data Grid only after source/API review
```

## 7. No compatibility architecture

ReUI and tablecn are copy-and-own source/reference inputs. They do not create a runtime abstraction layer between business features and shadcn.

When a source is adopted, it becomes project-owned code aligned to the selected project architecture. Obsolete competing implementations are removed rather than preserved behind adapters.
