# Energy reports

The `/reports` route compiles monthly and annual printable A4 energy, TOU cost, operations, and verified-savings reports from either archived `report-service` snapshots (`已存档`) or live owner queries (`未存档，按当前记录现算`).

## Sub-features

- `reports-live-month` sums daily priced electricity, cooling, TOU periods, unpriced warnings (`有 100 kWh 没有电价，电度电费未计`), operations, and savings when no snapshot is archived.
- `reports-archived-snapshot` reads closed-month snapshots (`/api/v1/sites/{siteId}/reports/monthly/{month}`) cleanly without issuing redundant `/api/v1/analytics/energy-series` queries.
- `reports-annual-summary` switches to `view=year&year=2026` (`逐月明细`) displaying mixed archived (`已存档`) and live rows across 12 months.

## How to get to it (user POV)

- Click `报告` in the sidebar, or open `/reports?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/reports.spec.ts`.

- **Verify live month, archived month, and annual table.** Check live month totals and 4 `day`-granularity `energy-series` calls, load `gateway.reportSnapshots = [report-snapshot-2026-09.json]` on `month=2026-09` asserting zero `energy-series` calls and `覆盖 16 / 30 天`, and switch to `view=year&year=2026` verifying `逐月明细` September row.
- **Proof.** Capture pass output and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Closed months lacking a snapshot stay live-computed; verify both `已存档` and `未存档，按当前记录现算` copy on archived views because some comparative fields (e.g. previous-month delta) fall back honestly when prior snapshots do not exist.
