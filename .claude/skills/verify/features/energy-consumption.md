# Energy consumption

The `/energy-analysis/consumption` route compares electricity, cooling output, synthetic COP, and estimated TOU electricity cost over Site-local hours and breaks consumption down by sub-metered machines (`用电去向`).

## Sub-features

- `consumption-period-summary` switches between `空调用电`, `供冷量`, `综合能效` (`metric=cop`), and `估算电费`, displaying data freshness (`[data-testid="energy-freshness"]`) and unpriced energy notices.
- `consumption-tou-and-hourly-table` toggles `分时电费` (`view=tou`, `两部制分时电价`, peak/flat/valley breakdown + uncovered kWh warning) and `分时段明细` (preserves scroll position across tab clicks and launches `AI 助手` via `解释 <hour>`).
- `consumption-machine-destination` breaks electricity down by equipment (`breakdown: 'device'`).

## How to get to it (user POV)

- Select `能源` → `能耗分析` in the sidebar, or open `/energy-analysis/consumption?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/consumption.spec.ts` (pins clock to `energy-series.json` `dataWatermark`).

- **Verify period KPIs, TOU breakdown, and hourly details.** Open `/energy-analysis/consumption?site=${SITE_ID}`, check `918.9kWh`, `3,198.5kWh`, `3.48COP`, `¥745`, scroll to `能耗明细`, click `分时电费` (`view=tou`) confirming scroll stability, and switch to `分时段明细` confirming `10:00解释` row values and four `analytics/energy-series` requests (`cooling` & `electricity`).
- **Proof.** Capture pass output and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Completed-hour figures depend on the browser clock having passed each recorded hour; keep `page.clock.setFixedTime(new Date(recordedAt))` synchronized with fixture `dataWatermark`.
