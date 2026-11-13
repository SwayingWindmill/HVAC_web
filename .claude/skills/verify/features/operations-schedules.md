# Operations schedules

The `/operations/schedules` route configures weekly plant cooling slots (including cross-midnight intervals), manages single-day overtime/holiday exceptions, and audits unexpected chiller runs outside approved windows.

## Sub-features

- `schedules-weekly-editor` populates default workday windows or custom weekday/time/cross-midnight (`跨日`) slots with mandatory `变更原因` on empty-state and populated sites.
- `schedules-deviation-inspection` highlights `计划外开机` intervals detected via `QUARTER_HOUR` power buckets, inspects them in `计划外开机核查`, links into `/operations/trends`, and creates deduplicated inspection work orders.
- `schedules-exceptions` approves temporary overtime/holiday overrides that clear false-positive deviations while keeping advisory temperature targets explicitly read-only (`仅作冷站值班交接参考`).

## How to get to it (user POV)

- Open `运行` → `运行计划` in the sidebar, or navigate to `/operations/schedules?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/schedules.spec.ts` (pins clock to `2026-10-14T07:00:00.000Z` and injects off-window chiller power buckets).

- **Configure weekly slots and inspect cross-midnight split.** Click `配置默认周计划`, populate `填充工作日 08:00–21:00`, add a Saturday `22:00` cross-midnight (`跨日`) `02:00` slot, save, and verify `document.documentElement.scrollWidth <= document.documentElement.clientWidth` at `1440×900`.
- **Audit unscheduled running and exceptions.** Open `计划外开机`, verify `在历史趋势查看` link (`granularity=QUARTER_HOUR`), click `创建核查工单`, and exercise exception requests to confirm deviation clearance.
- **Proof.** Capture pass output and `1440×900` schedule grid PNG under `.scratch/verify/<slug>/`.

## Gotchas

- `schedules.spec.ts` installs Playwright clock pinning and custom `gateway.aggregateHandler` buckets (`PINNED_NOW_ISO`); live Tier-2 checks depend on the site's actual configured schedule and timezone.
