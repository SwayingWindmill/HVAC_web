# HVAC Web Component / Block Provenance Audit — 2026-09-22

**Status:** ACTIVE AUDIT  
**Scope:** `apps/hvac-web`  
**Goal:** distinguish legitimate project composition from duplicated generic UI, and verify which reusable components are sourced through shadcn-compatible registries rather than reimplemented locally.

## Audit rule

The project is copy-and-own. A local file does not retain cryptographic proof that it was originally installed by CLI. Therefore provenance is assessed using:

1. current `shadcn info` recognition;
2. current registry resolution through `shadcn search/view/add --dry-run --diff`;
3. local source-review records;
4. current usage graph.

A file is not classified as “handwritten” merely because it is locally owned.

## 1. Official shadcn primitive layer

`npx shadcn@latest info --json` recognizes these local `components/ui` items as shadcn components:

- alert
- avatar
- badge
- breadcrumb
- button
- calendar
- card
- chart
- checkbox
- command
- dialog
- dropdown-menu
- empty
- field
- input-group
- input
- item
- kbd
- popover
- progress
- select
- separator
- sheet
- sidebar
- skeleton
- slider
- spinner
- table
- tabs
- textarea
- tooltip

These are **shadcn lineage**, not a parallel custom primitive system.

Current registry drift is not uniform:

- `calendar`: current CLI diff is identical.
- `chart`, `input`, `skeleton`, `textarea`, `spinner`: minimal drift.
- `button`, `badge`, `card`, `checkbox`, `progress`, `slider`, `table`, `tabs`, `tooltip`: moderate drift.
- `field`, `item`, `command`, `select`, `dialog`, `dropdown-menu`, `input-group`, `breadcrumb`, `avatar`, `empty`: substantial drift from the current registry and require component-by-component review before any future upgrade. `sidebar` and its CLI dependency set were subsequently adopted from the current registry during the P2 follow-up below.

Do **not** overwrite the whole primitive directory simply to match latest upstream. Review meaningful behavior/API differences first.

## 2. tablecn / Data Table layer

The operational ledger layer is **registry-derived and project-adapted**, not a fresh local reimplementation.

Current CLI evidence:

`@tablecn/data-table-filter-list` resolves and includes:

- `components/data-table/data-table-filter-list.tsx`
- `components/data-table/data-table-range-filter.tsx`
- `components/data-table/data-table-advanced-toolbar.tsx`
- `components/data-table/data-table-view-options.tsx`
- `components/ui/faceted.tsx`
- `components/ui/sortable.tsx`
- supporting hooks/libs

`@tablecn/data-table-sort-list` resolves and includes:

- `components/data-table/data-table-sort-list.tsx`
- `components/ui/sortable.tsx`
- supporting hooks/libs

Dry-run diffs show the local implementation retains tablecn structure while adapting:

- Chinese product copy;
- project TanStack Table v9 types/API;
- project pagination reset semantics;
- local aliases;
- local accessibility/product behavior.

Classification: **KEEP — registry lineage + intentional ADAPT.**

The current architecture decision remains:

```text
shadcn Table primitives
+ tablecn interaction/composition grammar
+ project TanStack Table v9 behavior
```

### DataTableBlock

`src/blocks/data-table/data-table-block.tsx` is project-owned and is not an upstream shadcn/tablecn block.

It is a small outer composition that standardizes the page-level table boundary and prevents Card → Table nesting.

Classification: **KEEP — justified project block.**

Do not replace it merely because it is not a CLI item. Reassess only if an upstream block provides the same product responsibility without adding another table system.

## 3. Project application composition — justified

Current source matrix:

| Project composition | Approved building blocks |
| --- | --- |
| `AppHeader` | shadcn Avatar, Breadcrumb, Button, Kbd, Separator, SidebarTrigger, DropdownMenu + project CommandSearch |
| `AppSidebar` | shadcn Sidebar + DropdownMenu |
| `CommandSearch` | shadcn CommandDialog / CommandInput / CommandList / CommandGroup / CommandItem |
| `Main` | semantic layout + Tailwind only; no duplicate primitive |
| `PageIntro` | semantic header layout + Tailwind only; no duplicate primitive |
| `DataTableBlock` | project block around the tablecn-derived/TanStack v9 DataTable layer and shadcn Table primitive |
| `FactStrip` | small project block for peer-level operational facts; no independent primitive framework |


These are project-owned compositions built on shadcn primitives. They are not expected to be direct CLI items:

- `components/layout/AppSidebar.tsx`
- `components/layout/AppHeader.tsx`
- `components/layout/CommandSearch.tsx`
- `components/layout/Main.tsx`
- `components/layout/PageIntro.tsx`
- `components/layout/app-navigation.ts`
- `components/layout/use-shell-notifications.ts`

Evidence:

- AppSidebar composes shadcn Sidebar + DropdownMenu.
- AppHeader composes shadcn Breadcrumb + Button + Kbd + SidebarTrigger + DropdownMenu + Avatar.
- CommandSearch composes shadcn CommandDialog/Command primitives.

Classification: **KEEP — application/domain composition, not primitive duplication.**

## 4. Self-written generic layer requiring consolidation

### 4.1 Status badge convergence — COMPLETE 2026-09-22

The duplicate `components/ui/status-pill-badge.tsx` primitive has been removed.

Current structure:

- official shadcn `Badge` remains the only badge primitive;
- `components/status-badge.tsx` is a thin product-semantic mapping that renders `Badge variant="outline"`;
- success / warning / destructive / in-progress / info / neutral are product status tones, not a second primitive API;
- `StatusBadge` is imported directly by features and is no longer re-exported from the DataTable package.

Classification: **KEEP — product semantic composition on shadcn Badge.**

Do not add Kibo Pill merely to replace a simple capability already covered by Badge.

### 4.2 PageScaffold — migrate and remove

`components/PageScaffold.tsx` explicitly declares itself a migration scaffold.

It still powers active surfaces:

- Notifications
- ProductPages: FDD / Optimize / Forecast / Cost / Settlement / AI
- SystemManagement

It manually recreates:

- page heading;
- breadcrumb;
- tabs;
- action header;
- page content scaffold.

These responsibilities now belong to the AppShell + `Main` + `PageIntro` + shadcn Breadcrumb/Tabs grammar.

Classification: **REMOVE AFTER MIGRATION.**

### 4.3 OperationsUI / OperationsUI.css — migrate and remove

`OperationsUI.css` still contains a large Ant-era compatibility visual layer:

- `.ant-card*`
- `.ant-alert*`
- `.ant-table*`
- `.ant-tag`
- `.ant-segmented`
- `.ant-tabs*`
- `.ant-input*`
- `.ant-select*`
- `.ant-btn`
- `.ant-drawer*`
- `.ant-modal*`
- `--ant-color-*` fallbacks

Because `PageScaffold` imports this stylesheet, active routes still bundle this obsolete compatibility CSS even though feature JSX has moved to shadcn components.

Current live consumers of `OperationsUI.tsx` are mainly ProductPages and SystemManagement.

Several exports have no current consumer at all:

- `useOperationsDetailFocus`
- `OperationsDetailHeader`
- `OperationsSummaryStrip`
- `OperationsDetailSection`
- `OperationsTimeline`
- `OperationsActionFooter`
- `OperationsChartCard`

`OperationsTimeline` is especially unnecessary as both `@reui/timeline` and `@diceui/timeline` are available through the shadcn CLI registry ecosystem if a real timeline capability is required.

Classification: **REMOVE / DECOMPOSE.**

Used pieces such as `OperationsMetrics`, `OperationsPanelHeading`, `OperationsSectionIntro`, and `OperationsInsightBand` should either:

- collapse into existing project compositions;
- use shadcn Card/Alert/Item directly;
- or, when genuinely complex, be replaced by one reviewed registry source.

Do not retain `OperationsUI` as a parallel generic component framework.

## 5. Project-owned fact strip — CONSOLIDATED 2026-09-22

The previous overlapping compositions have been removed:

- `SurfaceFactStrip`
- `IndependentStateStrip`
- `OperationsMetrics`

The single current project block is:

- `blocks/fact-strip/FactStrip`

It owns only responsive peer-fact layout and semantic tone presentation. Features own labels, values, units, icons, detail text and business truth.

Classification: **KEEP — one small project block, no separate visual system.**

Do not force this thin composition into an upstream registry unless a reviewed upstream block provides the same responsibility with lower project complexity.

## 6. Registry availability confirmed

The current shadcn CLI resolves:

- `@tablecn/*`
- `@reui/*`
- `@diceui/sortable`
- `@diceui/timeline`
- `@kibo-ui/*`
- exact `@shadcnblocks/<item>` addresses, with `@shadcnblocks/hero1 --dry-run` verified successfully

Dice/Kibo do not need to be treated as unavailable merely because they are not currently written into `components.json`; the current registry directory can resolve them by namespace. Shadcnblocks is now explicitly configured in `components.json`; its exact-item install path works, while its registry does not currently expose the generic CLI search endpoint.

Before installing any advanced item, follow the project source-review/pin policy.

## 7. Remediation order

### P0 — remove parallel generic UI — COMPLETE 2026-09-22

1. Active `PageScaffold` consumers migrated to current `Main` + shadcn composition.
2. `PageScaffold.tsx` removed.
3. `OperationsUI.css` removed, including its Ant compatibility selectors.
4. `OperationsUI.tsx` removed; active metric usage moved to the project `FactStrip` block and active Tabs moved to shadcn `Tabs`.

### P1 — primitive convergence — COMPLETE 2026-09-22

5. Duplicate `StatusPillBadge` primitive removed; product status semantics now render through shadcn `Badge` via `StatusBadge`.
6. `SurfaceFactStrip`, `IndependentStateStrip`, and `OperationsMetrics` consolidated into the single project `FactStrip` block.

### P2 — upstream drift review — COMPLETE 2026-09-22

Each high-drift primitive was reviewed against the current `radix-nova` registry with `npx shadcn@latest add <component> --diff ...`. The result is deliberately mixed rather than a blanket overwrite:

| Primitive | Decision | Applied convergence / preserved contract |
| --- | --- | --- |
| `label` | **ADOPT** | Added directly through the current shadcn CLI and now recognized by `shadcn info`. |
| `field` | **ADAPT** | `FieldLabel` now composes official `Label`; field orientation structure and multi-error aggregation follow current upstream semantics. Project compact label/help/error density remains. |
| `sidebar` | **ADOPT** | Reinstalled with `npx shadcn@latest add sidebar --overwrite`. The current official 16rem Sidebar, cookie-writing Provider, fixed desktop container + gap, rail/action/skeleton APIs and current responsive behavior are now authoritative. `npx shadcn@latest add sidebar --diff src/components/ui/sidebar.tsx` reports **No changes**. The Vite Shell reads `sidebar_state` and passes it to `defaultOpen`; `SidebarInset` receives only the call-site `min-w-0` flex integration constraint. |
| `command` | **KEEP / ADAPT AT CALL SITE** | Current CommandSearch already uses `CommandGroup → CommandItem`, Dialog title/description and focus semantics. Project palette placement, max width and dense operator-search grammar remain instead of adopting the newer generic palette styling. |
| `item` | **ADAPT** | Adds current data-variant/data-size metadata, semantic `ItemDescription <p>`, list semantics on `ItemGroup`, and gap-based content layout. Existing HVAC density remains. |
| `select` | **KEEP VISUAL / ADAPT COMPOSITION** | Project compact trigger density and `popper` default remain. All current Select call sites now use `SelectContent → SelectGroup → SelectItem`, including DataTable pagination and Feature forms/filters. |
| `dialog` | **ADAPT** | Default close action now composes shadcn `Button`; required titles were verified across active callers. Existing modal widths, overlay and product geometry remain. |
| `sheet` | **ADOPT + CALL-SITE COMPOSITION** | The Sidebar install restored the current official shadcn `Sheet`, including its current Portal, close Button, overlay and default `sm:max-w-sm` geometry. The obsolete project-only `SheetBody` API was removed from all callers. Operator inspectors that need more working width now declare that width explicitly at the feature call site (for example 540px), without modifying the primitive. |
| `dropdown-menu` | **ADAPT** | Adds `DropdownMenuGroup` and a semantic destructive item variant. AppHeader, AppSidebar and DataTable column menus now group items; logout uses the destructive variant instead of a caller-owned color override. |
| `input-group` | **ADAPT** | `InputGroupInput` and `InputGroupTextarea` now compose official shadcn `Input` / `Textarea`. The upstream non-semantic addon click-to-focus behavior was rejected because the project accessibility detector correctly flags it; project compact height remains. |
| `resizable` | **ADOPT** | Added through `npx shadcn@latest add resizable`; the official Radix/Nova wrapper over `react-resizable-panels` v4 is now the desktop split-inspector layout primitive. It owns only panel sizing/keyboard resizing; feature selection and business detail remain outside the primitive. |

The review remains selective for most primitives, but the Sidebar follow-up explicitly adopts the current upstream shell architecture rather than preserving the previous 15rem implementation. Product-specific inspector widths and flex constraints live at call sites instead of forking the official Sidebar/Sheet primitives.

Validation after convergence:

- `typecheck:web`: pass;
- `lint`: pass;
- production `build`: pass;
- `design:check`: pass;
- changed-scope design detector: 0 findings / 0 advisory;
- Assets browser review: pass with official 256px Sidebar, desktop/detail/narrow surfaces and no page-level overflow;
- Comfort browser review: pass with official 256px Sidebar, 540px feature-owned inspector width and 768px no-overflow validation;
- Control Center browser review: pass with official 256px Sidebar;
- Energy Analysis browser review: pass with official 256px Sidebar;
- Trend Analysis browser review: pass with official shell integration;
- current Overview browser review: pass, including account DropdownMenu and focused Command Palette behavior.

Two older browser scripts are not current visual acceptance authorities: the legacy Alarm audit still waits for the removed `real-alarms-workbench` selector and obsolete English fixture copy, while the RMS shell audit still expects the retired `data-feature-id="system"` navigation model. Do not reintroduce old DOM contracts to satisfy those stale assertions; update or retire those audits separately against the current Surface Catalog.

### P3 — provenance hygiene

9. For each future ReUI/Kibo/Dice/tablecn adoption, record:
   - CLI item address;
   - upstream release/commit;
   - files reviewed;
   - dependency impact;
   - ADOPT/ADAPT/REJECT decision.

## Conclusion

The project does **not** have a general problem of “everything being handwritten.”

The main issue is narrower:

- official shadcn primitives exist but some have version drift;
- the DataTable stack is correctly tablecn-derived and adapted;
- the Shell composition is legitimately project-owned;
- the legacy `PageScaffold + OperationsUI.css/tsx` layer has been removed;
- the duplicate status-badge primitive has been removed;
- project fact/metric strips have converged on `FactStrip`;
- the high-drift shadcn primitives have been reviewed and selectively converged without discarding deliberate product contracts;
- `Label` is now direct shadcn CLI provenance, and Select/Command/Dropdown composition rules are enforced at current call sites.

The next component-governance work is P3 provenance hygiene for future registry adoption and periodic drift review when upstream behavior provides a concrete accessibility, API or maintenance benefit.
