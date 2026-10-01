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
→ Detail Sheet / Contextual Tool
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

The product now standardizes **selected-object detail as a right-side Sheet** across workspaces. This is a product consistency decision: the primary ledger/canvas keeps its full layout width, while durable or complex work continues on a dedicated Route. Desktop Sheets are non-modal/no-overlay; narrow Sheets are modal.

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
/issues?alarmView=active
/issues?selected=:alarmId

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

## 7. List / Ledger → Detail Sheet contract

For selected-object details in scan-heavy workspaces such as Devices, Issues, Work Orders, Opportunities, Executions, Reports and Settings children:

```text
Ledger / list / object canvas
→ select object
→ right-side Detail Sheet
→ optional “打开完整详情”
→ durable detail Route
```

The primary workspace must keep its original width. Selection must **not** create a permanent second column, Resizable split pane, or Card-based Inspector.

Desktop behavior:
- official shadcn Sheet primitive;
- right side;
- non-modal;
- no page overlay;
- fixed sensible width, normally about 480–640 px;
- selecting another row updates the same Sheet without closing it;
- Sheet overlays the right edge instead of reflowing the Ledger/canvas;
- selected identity may be URL-restorable; Sheet geometry is never business state.

Sheet content rules:
- `SheetTitle` + concise object context;
- facts required for a quick decision;
- current status / evidence / owner / next action where relevant;
- only a small number of contextual actions;
- no full-page DataTable inside Sheet;
- no large tab hierarchy, long forms, full engineering point inventory, large evidence corpus, or multi-step workflow;
- show `打开完整详情` only when a durable detail Route exists.

Do not build desktop Split Inspector / ResizablePanel just because a row has detail. `Resizable` remains available for genuine analytical canvases whose two panes are both primary task surfaces, not for ordinary object details.

## 8. Narrow Detail Sheet contract

Narrow layouts use the same Sheet content, but switch to normal modal behavior:

```text
Ledger / list
→ row select
→ modal shadcn Sheet + overlay
```

The information hierarchy and actions must remain the same as desktop; only modality and width change.

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
| 运行 | system canvas / object list + contextual facts | Detail Sheet | shadcn Sheet for selected objects; ECharts/X6 only when engineering relation requires it |
| 设备 | tablecn ledger | Detail Sheet → Device Detail Route | DataTableBlock + tablecn + shadcn Sheet; Sheet 不压缩 Ledger |
| 告警与诊断 | Alarm-led issue ledger + integrated Diagnosis | Detail Sheet | FactStrip + DataTableBlock + Sheet; diagnosis/evidence lives inside selected issue context |
| 工单与验证 | work ledger / verification queue | Detail Sheet → Work Order Detail Route | DataTableBlock; ReUI Timeline; Kibo file/dropzone candidate for attachments |
| 能源与绩效 | shared Context Bar + analytical views | object detail uses Sheet; analytical evidence may stay inline | Tabs + shadcn controls; ECharts; tablecn for contributor/bill ledgers |
| 改进 | opportunity/project/action/M&V/review views | Detail Sheet → durable project Route where needed | DataTableBlock; Timeline; Kibo Gantt only if real project scheduling requires it |
| 自动化 | strategies / executions | Detail Sheet → Strategy Detail Route | DataTableBlock; ReUI Timeline; Kibo Gantt/calendar only for real schedules |
| 报告 | definitions + generated reports + scheduling | Detail Sheet for report metadata | tablecn; Kibo Editor/Calendar only if selected after source review |
| 设置 | local side nav + focused settings surfaces | Detail Sheet for selected records | shadcn local nav/Tabs/forms; ReUI Tree candidate for semantic hierarchy |

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
