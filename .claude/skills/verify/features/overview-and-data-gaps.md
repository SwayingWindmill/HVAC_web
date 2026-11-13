# Overview and data-gap banner

The `/overview` route summarizes today's energy usage, synthetic COP, estimated electricity cost, live plant state, pending alarms/diagnostics/work orders, and active reading-gap alerts at the top of the page.

## Sub-features

- `overview-energy-tabs` switches between `今日空调用电`, `今日综合能效`, and `今日电费（估算）` while syncing the `metric=` URL search param.
- `overview-plant-and-queue` displays live power, cooling load, running equipment counts, and Severity-sorted links into alarms, findings, and work orders.
- `overview-data-gap-banner` stays hidden (`data-gap-banner` count `0`) when no critical reading gap is active, and links directly to `/settings/integrations` meter reconciliation when a sub-meter stops reporting.

## How to get to it (user POV)

- Sign in and click `总览` in the left navigation rail, or navigate directly to `/overview?site=<SITE_ID>`.
- From a reading-gap alert or top banner elsewhere, open `/overview?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Recorded gateway fixtures in `apps/hvac-web/e2e/fixtures/` (`optimization-latest.json`, `site_id`), OR live `hvac-local` at `https://localhost:8443` with a seeded site.

- **Load overview and switch metric tabs.** Open `/overview?site=${SITE_ID}` at `1440×900`. Run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/overview.spec.ts`. Region `今日用能` selects `今日空调用电` by default, clicking `今日综合能效` updates the URL to `metric=cop` and sets `aria-selected="true"`.
- **Inspect live plant and work queue.** In regions `冷站实时` and `需要处理`, verify power, cooling load, active counts, and switching tabs (`告警`, `诊断`, `工单`, `全部`). Clicking an alarm navigates to `/operations/alarms?inspect=...` with region `告警详情`.
- **Verify synthesized sub-meter gap banner.** Simulate a missing sub-meter point in Tier 1 (`e2e/overview.spec.ts` second test) and assert `[data-testid="data-gap-banner"]` shows the missing meter name and timestamp while remaining `0` elements when healthy.
- **Proof.** Save stdout and optional `1440×900` PNG to `.scratch/verify/<slug>/` (`overview-after.png`).

## Gotchas

- `energy-breakdown.json` dates in fixtures may lag wall-clock dates if testing relative windows on Tier 2; keep Tier 1 and Tier 2 outputs distinct.
- Do not assert zero horizontal scroll without calling `await page.setViewportSize({ width: 1440, height: 900 })` first.
