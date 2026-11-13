# Operations work center

The `/operations/work-center` route hosts the active and closed work-order board (`工单看板`), detailed order drawer (`[role="dialog"]`), claim/start/complete lifecycle controls, and origin equipment links.

## Sub-features

- `work-center-tabs` splits unfinished work orders from `已结束` (`已完成` list) without cross-contaminating counts.
- `work-center-lifecycle` enforces owner rules: `开始处理` stays disabled until claimed (`由我负责` → `:assign` with `assigneeId`), transitions through `处理中`, and requires a completion note (`处理结果` → `:complete` `OPERATOR_NOTE`) before `确认完成`.
- `work-center-origin-links` navigates from equipment-origin orders (`CT-01`) directly into `/operations/systems-devices?inspect=...`.

## How to get to it (user POV)

- Select `运维` → `工单` in the sidebar, or open `/operations/work-center?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/work-center.spec.ts`.

- **Claim, start, and complete an unassigned order.** Open `/operations/work-center?site=${SITE_ID}&inspect=01a0f6dd-3b7f-7c9a-a37a-e8a2f1e71979`, verify `开始处理` is disabled, click `由我负责`, start work, fill `处理结果`, click `确认完成`, and confirm `:assign` and `:complete` (`OPERATOR_NOTE`) requests.
- **Verify tabs and origin link.** On `工单看板`, confirm open list count `2`, switch to `已结束` (`1` row), and verify equipment origin link navigates to `/operations/systems-devices`.
- **Proof.** Capture Playwright stdout and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Completing an order leaves `确认完成` disabled until `处理结果` is non-empty.
