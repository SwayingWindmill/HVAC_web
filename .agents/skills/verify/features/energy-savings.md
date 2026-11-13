# Energy savings

The `/energy-analysis/savings` route registers energy conservation measures on Site time (`登记措施`), fits M&V baselines upon implementation (`确认实施并拟合基线`), checks baseline goodness-of-fit gates (`CV(RMSE)`, completeness), and tracks verified avoided kWh/cost.

## Sub-features

- `savings-register-and-implement` converts Site-local `datetime-local` inputs (`2026-10-12T15:17` Asia/Shanghai) to UTC (`2026-10-12T07:17:00.000Z`), registers a measure, and fits its 14-day hourly baseline.
- `savings-baseline-quality-gate` detects thin baselines (`对比基线不可靠，暂不统计`, `15 / 336（4%）`, `未拟合`), withholds unverified savings, and exposes `重新拟合基线`.
- `savings-multi-measure-allocation` distinguishes plant-meter vs device-level verified measures in `节能成效`.

## How to get to it (user POV)

- Click `节能` → `节能项目` in the sidebar, or open `/energy-analysis/savings?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/savings.spec.ts`.

- **Register, implement, and inspect baseline fit.** On `/energy-analysis/savings?site=${SITE_ID}`, click `登记措施`, submit `冷冻水供水温度由 7 °C 提高到 8 °C`, click `确认实施` → `确认实施并拟合基线`, assert `对比基线的检验` table (`15 / 336（4%）`, `未拟合`), press `Escape`, and verify `节能措施列表` shows `对比基线不可靠`.
- **Proof.** Capture pass output and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- `plannedAt` and `implementedAt` inputs use Site-local wall-clock time (`Asia/Shanghai`) even when the browser timezone differs; assert exact UTC serialization (`...T07:17:00.000Z` for `15:17`).
