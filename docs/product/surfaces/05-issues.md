# 05 告警与诊断 Workspace — Current Implementation Brief

**Status: IMPLEMENTED / BROWSER-REVIEWED**
**Primary route:** `/sites/$siteId/issues`
**Workspace view state:** `?view=alarms|diagnostics`

## 1. Responsibility

This workspace consolidates Surface 09 and Surface 10 at the navigation and context level only.

It does **not** merge their domain models:

- Alarm = authoritative abnormal condition + operator handling state.
- Diagnosis = evidence-led finding / hypothesis / verification workflow.
- ACK ≠ cleared.
- Finding ≠ root cause.
- Work created/completed ≠ alarm cleared or root cause confirmed.

The shared workspace owns the handoff between the two tasks while preserving each Surface's original task composition.

## 2. Design inheritance from the 36-Surface system

The old Surface 09 and 10 implementations are design sources, not just capability inventories.

### Alarm view inherits Surface 09

- active-first operator triage;
- compact alarm-load facts;
- Active / History / Shelved / Performance local views;
- standalone ledger, not Card-wrapped Table;
- desktop fixed detail region;
- narrow Sheet detail;
- ACK / Assign actions stay explicit and separate from physical condition;
- no synthetic risk / health score.

### Diagnosis view inherits Surface 10

- finding queue + selected investigation;
- Verified Facts before interpretation;
- published Finding remains distinct from Hypothesis;
- evidence gaps and data-quality blockers remain visible;
- Next Verification is a first-class section;
- explicit handoff to Device / Alarm / Work;
- no “AI root cause” shortcut.

## 3. Shared workspace composition

```text
Workspace Tabs
├─ 告警
│  ├─ operational facts
│  ├─ Alarm local views
│  ├─ standalone ledger
│  └─ selected Alarm detail
└─ 诊断
   ├─ finding queue
   └─ evidence-led investigation detail
```

Shared state is limited to context that is genuinely common:

- site;
- object / device identity;
- source trail;
- selected alarm / finding identity where applicable.

The two views do not share a status enum or lifecycle.

## 4. Route contract

Canonical workspace route:

```text
/sites/:siteId/issues
```

Workspace state:

```text
view=alarms|diagnostics
```

Alarm-local state uses a separate key:

```text
alarmView=active|history|suppressed|performance
```

This prevents the workspace `view` key from colliding with Surface 09's internal view state.

Examples:

```text
/sites/:siteId/issues?view=alarms&alarmView=active
/sites/:siteId/issues?view=diagnostics&alarm=<alarmId>
```

## 5. Visual rules

- No repeated page title inside the workspace body; Shell owns “告警与诊断”.
- No generic KPI-card wall.
- Alarm summary uses the shared `FactStrip`.
- Table-like content remains standalone `DataTableBlock`.
- A table must not be wrapped by an extra decorative Card.
- Section Card usage is allowed only when it matches the original Surface hierarchy and carries a durable investigation unit.
- Exception colors are reserved for abnormal/action-needed facts.
- Narrow layouts must reduce visible ledger columns or use the existing Sheet pattern rather than forcing page-level horizontal scrolling.

## 6. Current route migration

Removed old primary routes:

- `/sites/:siteId/alarms`
- `/sites/:siteId/diagnostics`

Current canonical route:

- `/sites/:siteId/issues`

Cross-workspace links have been changed to enter the correct peer view instead of navigating to the old separate routes.

## 7. Acceptance

The workspace is acceptable only when:

1. Sidebar still contains exactly 10 workspaces.
2. “告警与诊断” owns the active navigation state for both views.
3. Alarm and Diagnosis are restorable by URL.
4. Surface 09 semantics remain intact.
5. Surface 10 evidence hierarchy remains intact.
6. No old `/alarms` or `/diagnostics` primary route remains in the generated route tree.
7. No Ant Design DOM is rendered.
8. Desktop and narrow layouts have no page-level horizontal overflow.
9. Existing diagnostics browser review accepts the new Alarm handoff route.
10. Build, typecheck, lint, design checks and changed-scope design audit pass.
