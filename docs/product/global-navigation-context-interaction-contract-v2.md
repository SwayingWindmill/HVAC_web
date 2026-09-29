# Global Navigation + Workspace Interaction Contract v2

> **Status: SELECTED / PRODUCT INTERACTION AUTHORITY**
>
> **Date:** 2026-09-22
>
> **Product blueprint:** `docs/product/smart-energy-system-page-architecture-v3-research-backed.md`
>
> **Code model:** `apps/hvac-web/src/app/workspace-catalog.ts`
>
> **Supersedes:** `docs/product/global-navigation-context-interaction-contract-v1.md`

## 1. Authority and intent

The product capability catalog is no longer equivalent to navigation.

The selected hierarchy is:

```text
Capability
→ Workspace
→ Workspace View
→ Context Inspector / Contextual Tool
→ Durable Detail Route
```

The Sidebar exposes **Primary Workspaces only**.

The current target Primary Workspace set is:

```text
总览
运行
设备
告警与诊断
工单与验证
能源与绩效
改进
自动化
报告
设置
```

This is a product-task hierarchy, not a backend-service or route-file hierarchy.

## 2. Research basis

The interaction model is based on recurring patterns found in mature building operations / energy products and mature application design systems.

### Building operations

**Johnson Controls Metasys UI**

Equipment Dashboard is a cohesive object dashboard with Trend, Equipment Activity, Equipment Data, Relationships, Graphics and Schedule. Metasys also keeps a separate Custom Trend Viewer for cross-object historical analysis.

- https://docs.johnsoncontrols.com/bas/r/Metasys/en-US/Metasys-UI-Help/16.0/Widgets-dashboards-and-apps/Equipment-dashboard
- https://docs.johnsoncontrols.com/bas/r/Metasys/en-US/Metasys-System-Product-Bulletin/16.0/User-interface/Metasys-UI/Dashboards-and-widgets

**Clockworks Analytics**

Diagnostics, Tasks and Analysis Builder are separate long-lived tasks. Analysis Builder is an advanced trend/investigation tool rather than the product's top-level business domain.

- https://clockworksanalytics.atlassian.net/wiki/spaces/ClockworksAnalyticsUM/overview

The product therefore adopts:

- contextual trend inside equipment / operation / investigation context;
- advanced Trend Studio as a secondary durable route;
- diagnosis-to-work handoff without collapsing diagnostic truth and work lifecycle into one object.

### Application interaction patterns

**Microsoft List/Details**

Wide layouts should keep list and detail side-by-side; selecting a new list item updates the detail pane. Narrow layouts may stack/drill down.

- https://learn.microsoft.com/en-us/windows/apps/develop/ui/controls/list-details

**Atlassian Panel**

A contextual panel is appropriate for selected-item details and supplementary tasks without leaving the current page. Dense/heavy data and complex multi-step flows belong on a full page.

- https://atlassian.design/components/panel/usage

The product therefore does **not** define `Table row → modal Sheet` as the universal desktop interaction.

## 3. shadcn application foundation

The implementation model remains:

```text
shadcn/ui
  primitive authority

tablecn + project TanStack Table v9
  operational ledger grammar

ReUI / Kibo UI / Dice UI
  advanced interaction sources

Shadcnblocks
  application-shell / page-block composition reference

project blocks
  small stable HVAC application compositions

features
  business truth and workflow
```

### Current sources

Official shadcn:
- Sidebar: https://ui.shadcn.com/docs/components/base/sidebar
- Data Table: https://ui.shadcn.com/docs/components/base/data-table
- Resizable: https://ui.shadcn.com/docs/components/base/resizable
- Sheet / Dialog / Tabs: current shadcn registry for the selected `radix-nova` project.

tablecn:
- default scan-heavy operational ledger grammar;
- faceted filters;
- sorting;
- visibility;
- pagination;
- URL-shareable query state where appropriate.

Dice UI:
- Data Table / Selection Toolbar / Action Bar / Sortable only when the specific interaction is better than the existing project implementation.
- https://diceui.com/docs/components/radix/data-table

ReUI:
- Timeline for execution / work history;
- Tree for semantic model / hierarchy when justified;
- advanced Filters only for real nested boolean query building;
- Data Grid only for spreadsheet / virtualized / tree-grid tasks, **not** normal ledgers.
- https://reui.io/docs/components/base/timeline
- https://reui.io/docs/components/base/tree
- https://reui.io/docs/components/base/filters
- https://reui.io/docs/components/base/data-grid

Kibo UI:
- Gantt / scheduling;
- rich Editor;
- file / dropzone workflows;
- complex calendar-like application components.
- https://www.kibo-ui.com/docs

Shadcnblocks:
- Application Shell reference, especially the standard Sidebar + header + breadcrumb composition;
- Settings sections;
- composed dashboard / table sections when they match the product hierarchy.
- https://www.shadcnblocks.com/blocks/application-shell
- https://www.shadcnblocks.com/block/application-shell1

Shadcnblocks remains a composition reference, never the HVAC information-architecture authority.

## 4. Sidebar contract

Sidebar contains the 10 Primary Workspaces only.

It must not directly list:

- Advanced Trend Studio;
- Device Detail;
- Work Order Detail;
- Optimization Project Detail;
- M&V Project Detail;
- Strategy Detail;
- individual Settings children;
- contextual Control;
- every Energy capability.

The official shadcn Sidebar remains the primitive and shell baseline.

Workspace visibility is projected from:

```text
product capability
AND deployment/site capability
AND principal discoverability
```

Action enablement remains a separate decision.

## 5. Workspace View navigation

Workspace Views are peers inside one durable task context.

Preferred implementation:

- 2–6 peer views: shadcn `Tabs`;
- large Settings section: local side navigation, not a single overflowing TabsList;
- capability views not present in the deployment: do not render the trigger;
- view selection belongs in TanStack Router search params when it is shareable / history-meaningful.

Examples:

```text
/issues?view=alarms
/issues?view=diagnostics

/work?view=orders
/work?view=verification

/performance?view=energy
/performance?view=efficiency
/performance?view=billing
```

Do not create separate page headers for every tab.

## 6. Operational Ledger contract

Operational Ledgers use:

```text
DataTableBlock
+ project TanStack Table v9
+ tablecn interaction grammar
```

The feature owns:

- columns;
- row semantics;
- filters;
- sorting meaning;
- selected object;
- server queries;
- business actions.

The reusable layer owns:

- toolbar grammar;
- table boundary;
- pagination composition;
- column visibility composition;
- selection/action presentation.

Do not replace the normal ledger path with ReUI Data Grid or Kibo Table.

## 7. Desktop List → Detail contract

For scan-heavy workspaces such as Devices, Alarms, Diagnostics, Work Orders, Opportunities and Executions:

```text
Desktop
┌──────────────────────────┬──────────────────────┐
│ Ledger / list            │ Context Inspector    │
│ selected row             │ selected object      │
│ next row                 │ evidence / actions   │
│ ...                      │ contextual links     │
└──────────────────────────┴──────────────────────┘
```

Selection updates the inspector without navigation.

Recommended implementation depends on the workspace responsibility:

- use a split inspector with the official shadcn `ResizablePanelGroup` only when the detail truly needs persistent side-by-side space and the primary content remains usable after compression;
- Device Ledger is the explicit exception: keep the ledger at full width and render a fixed-width non-modal Quick Preview over the right side on wide screens;
- the Device Quick Preview uses the existing shadcn Sheet/Dialog primitive in non-modal mode without an overlay; narrow screens use the same content as a modal Sheet;
- no Card wrapping around the entire table;
- preview / splitter geometry is application layout, not business state.

Do not add a splitter merely because a row has details. The selected object may be URL-restorable, but panel pixels are not business state.

Inspector width should have:
- sensible default;
- minimum width that preserves readable labels/actions;
- maximum width that does not turn the ledger into a narrow strip.

Do not persist panel width unless a real product requirement exists.

## 8. Narrow List → Detail contract

When side-by-side detail is no longer usable:

```text
Ledger
→ row select
→ official shadcn Sheet
```

Sheet is therefore the **responsive representation of the Context Inspector**, not the default desktop architecture.

Sheet must contain:
- SheetTitle;
- short object context;
- selected-item details;
- at most a small number of contextual actions;
- “打开完整详情” only when a durable detail actually exists.

## 9. Dialog contract

Use Dialog for a short task that needs focused completion before returning to the workspace.

Examples:
- ACK;
- Assign;
- short create/edit form;
- choose a reason;
- confirm a non-destructive scoped operation.

Use AlertDialog / explicit confirmation pattern for high-risk or irreversible action.

Dialog is not an object-detail viewer.

## 10. Durable Detail Route contract

Create/keep a durable detail route only when the task needs one or more of:

- long-running investigation;
- multiple sections/tabs;
- large evidence set;
- checklist/timeline/attachments;
- simulation/version diff/approval;
- deep link / bookmark / notification destination;
- audit-reference URL;
- work continued across sessions.

Selected durable routes:

- Device Detail;
- Work Order Detail;
- Optimization Project Detail;
- M&V Project Detail;
- Strategy Detail;
- Advanced Trend Studio;
- complex Report Definition / Integration Mapping editors where needed.

## 11. Contextual Control contract

Surface 25 is no longer planned as a default Primary Workspace.

Immediate command / setpoint / override actions live where the controlled object is already understood:

- System Operations;
- Device Inspector / Device Detail.

Control UI must keep visible:
- current value/state;
- authority source;
- preconditions;
- interlocks / guardrails;
- intended command;
- confirmation;
- ACK;
- readback.

Long-lived automation policy belongs to the Automation workspace.

## 12. Workspace-specific composition plan

| Workspace | Main composition | Inspector | shadcn / approved source plan |
| --- | --- | --- | --- |
| 总览 | attention sections + ranked lists + restrained charts | only contextual drill-in where useful | Sidebar shell; shadcn Chart/Table; Shadcnblocks dashboard sections only as composition reference |
| 运行 | system canvas / object list + contextual facts | split inspector | shadcn Tabs + Resizable; ECharts/X6 only when engineering relation requires it |
| 设备 | tablecn ledger | non-modal Quick Preview; modal Sheet narrow | DataTableBlock + tablecn + shadcn Sheet primitive; Preview 不压缩 Ledger |
| 告警与诊断 | peer views over shared site/time/object context | split inspector | Tabs + DataTableBlock; ReUI Timeline candidate for evidence/history |
| 工单与验证 | work ledger / verification queue | split inspector | DataTableBlock; ReUI Timeline; Kibo file/dropzone candidate for attachments |
| 能源与绩效 | shared Context Bar + analytical views | mostly inline analytical drill-down | Tabs + shadcn controls; ECharts; tablecn for contributor/bill ledgers |
| 改进 | opportunity/project/action/M&V/review views | split inspector for list views | DataTableBlock; Timeline; Kibo Gantt only if real project scheduling requires it |
| 自动化 | strategies / executions | split inspector | DataTableBlock; ReUI Timeline; Kibo Gantt/calendar only for real schedules |
| 报告 | definitions + generated reports + scheduling | inspector for report metadata | tablecn; Kibo Editor/Calendar only if selected after source review |
| 设置 | local side nav + focused settings surfaces | per-child inspector where helpful | shadcn local nav/Tabs/forms; ReUI Tree candidate for semantic hierarchy |

## 13. Surface placement

The authoritative machine-readable mapping is:

`apps/hvac-web/src/app/workspace-catalog.ts`

The 36 existing Surface specifications remain business/domain evidence during migration, but their current route or Sidebar status is no longer automatically authoritative.

Surface placement kinds:

- `workspace-core`;
- `workspace-view`;
- `capability-view`;
- `contextual-capability`;
- `secondary-route`;
- `durable-detail`;
- `settings-child`.

## 14. Migration rule

Do not create compatibility navigation.

Migration sequence:

1. build the target Workspace;
2. move the old Surface capability into the selected View / Inspector / Route;
3. verify browser behavior and URL state;
4. switch the Workspace navigation projection;
5. delete the obsolete route/navigation implementation;
6. do not keep aliases or duplicate pages “for safety”.

The current runtime may temporarily still use `SURFACE_CATALOG` while a target Workspace is not implemented. That is migration state, not target architecture.

## 15. Acceptance criteria

A Workspace is not complete until:

- its Primary Job can be stated in one sentence;
- the user does not need to reselect Site/Object/Time unnecessarily between views;
- desktop Ledger inspection does not force repetitive route hopping;
- narrow layouts preserve the task with Sheet or stacked detail;
- durable routes exist only for durable tasks;
- capability-gated views disappear when unsupported;
- no duplicate page title is introduced;
- table/list hierarchy does not regress into Card → Card → Table;
- component choice follows the project reuse ladder;
- real browser review validates desktop and narrow layouts.
