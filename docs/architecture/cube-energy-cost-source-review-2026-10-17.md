# Cube Energy Cost Source Review — 2026-10-17

#575 prices electricity in Cube (`docs/specs/estimated-energy-cost.md`). Checked against the
pinned `clickhouse/clickhouse-server:26.3.39.7` (probed on the Phase 1 server with inline
`values()` tables, read-only) and `cubejs/cube:v1.6.51`
(`@cubejs-backend/schema-compiler/dist/src/adapter/BaseQuery.js`).

| Option | Verdict | Why |
| --- | --- | --- |
| Pick the Tariff period in SQL from the fact's local time (`toHour(period_end, tariff.timezone)` in the JOIN) | REJECT | ClickHouse 26.3 requires a constant timezone argument: `Argument at index 1 for function toHour must be constant` (ILLEGAL_COLUMN). Per-version timezones and daylight saving cannot be resolved per row in SQL. |
| `range_hashed` dictionary keyed by Site over the version's effective range | REJECT | One range per key; the time-of-day range would need a second one, or every minute of every day type expanded per version. |
| Expand each Tariff Version into UTC price windows in Go, join with `ASOF LEFT JOIN … ON site AND period_end >= window_start` | ADOPT | Probed: a fact ending 03:29 takes the window ending 03:30, one ending 03:31 the next, and one past the last window gets no match (`window_end` defaults to 1970, so `period_end < window_end` marks it unpriced). Daylight saving and future holiday calendars stay in `libs/tariffmodel`. |
| Cube `FILTER_PARAMS.<cube>.site_id.filter('site_id')` inside the cube SQL so both sides of the join are cut to the queried Site | ADOPT | Present in 1.6.51 (`BaseQuery.filtersProxy`, `filterProxyFromAllFilters`); when no filter reaches the member it renders `1 = 1`, so results stay correct and only the scan widens. On the server both the view filter and the row policy reach it (generated SQL checked), so both sides read one Site; the facts also take the time range through `period_end`, the windows do not, since a fact needs the window before it. |
| Generations with a sync row written last, `FINAL` over `ReplacingMergeTree` | ADAPT | Same visibility pattern as the energy facts' checkpoint: readers see the newest generation only after all its windows are written; a retried insert is collapsed by `FINAL`. |
