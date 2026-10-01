# Alarm workspace — ThingsBoard reference review (2026-10-01)

Reference: ThingsBoard **v4.4**, commit `6d46786579c8b29caf5102f95ddb133674bed68b`.

Reviewed:

- `common/data/.../alarm/AlarmStatus.java`, `AlarmSearchStatus.java`, `AlarmSeverity.java`
- `ui-ngx/src/app/modules/home/components/alarm/alarm-table-config.ts`
- `ui-ngx/.../alarm/alarm-filter-config.component.html`, `alarm-details-dialog.component.html`

## Decisions

- **ADOPT** condition and acknowledgement as independent dimensions, shown together
  (活动/已恢复 · 已确认/未确认), and the search views Active / Unacknowledged / Cleared / All
  with Active as the default.
- **ADOPT** the ledger columns (severity, alarm, originator, status, start, duration,
  occurrence count, assignee) and a details view with start time, duration, history and
  acknowledge/assign actions.
- **ADOPT** repeated occurrences updating counters only. ThingsBoard keeps one alarm row
  and updates it; our alarm owner appended a timeline entry per matching evaluation (one
  incident reached 1,235 entries, ~420 KB). Only severity changes are now recorded.
- **ADAPT** clearing: ThingsBoard lets operators clear alarms manually. HVAC condition
  alarms clear only when observations return to normal, so there is no manual clear.
- **ADAPT** assignment: only "assign to me" until IAM exposes a user directory.
- **ADD** work orders from an alarm (origin source reference) and the alarm's linked work
  orders, which ThingsBoard leaves to rule chains.
- **REJECT for now** alarm comments and bulk acknowledge; the owner has no comment API and
  single-site alarm volumes do not need bulk actions yet.

Lists refresh every 15 s; the alarm owner has no stream (ThingsBoard pushes alarm updates
over its WebSocket subscription).
