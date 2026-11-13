# Energy optimization

The `/energy-analysis/optimization` route publishes chilled-water supply temperature reset recommendations (`当前建议`, `预计效果`), dispatches setpoint commands or fallback manual adjustment work orders when write points are absent, and inspects chiller COP shadow evaluations (`模型与负荷分析`).

## Sub-features

- `optimization-manual-run-and-guard` explains missing readiness inputs (`502` declined run `alert`) when prerequisites are unready, and publishes `当前建议` (`7.1 → 8.1 °C`, `预计日节电 60 kWh`, `预计日节省 ¥51`) when ready.
- `optimization-progressive-disclosure` collapses `前置与安全条件` behind a summary toggle and hides engineering sensitivity details until expanded.
- `optimization-dispatch-vs-manual-fallback` sends a confirmed setpoint command when a write point exists, or creates a manual work order when `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` is absent from `assetModel`.

## How to get to it (user POV)

- Click `节能` → `优化建议` in the sidebar, or open `/energy-analysis/optimization?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/optimization.spec.ts`.

- **Test declined vs ready recommendation runs.** First trigger `生成建议` without `nextRecommendation` to assert `alert` prerequisite message; next set `gateway.nextRecommendation = fixture('optimization-latest.json')`, click `生成建议`, assert `当前建议`, `预计效果`, and collapsed `前置与安全条件：6/6 就绪`, then expand and dispatch/verify shadow evaluation tab.
- **Proof.** Capture pass output and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Sites without a writable `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` telemetry point in `assetModel` (such as 松雅轩 without room sensors) automatically fall back to manual work-order creation instead of direct command submission.
