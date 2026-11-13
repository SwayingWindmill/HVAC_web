# Operations alarms

The `/operations/alarms` route displays active and cleared site alarms (`告警记录`), an adjacent `告警详情` inspector, acknowledgement with note persistence, work-order escalation (`domain: 'ALARM'`), and keyword/severity filtering.

## Sub-features

- `alarms-list-and-view` defaults to active alarms (`活动 · 未确认`), auto-opens the first item in `告警详情`, and switches to `已恢复` (`view=cleared`) or `全部` (`view=all`).
- `alarms-acknowledge` opens `确认告警`, submits `确认说明`, transitions state to `活动 · 已确认`, and posts `/api/v1/alarms/{alarmId}/ack` with `X-CSRF-Token`.
- `alarms-work-order-origin` opens `创建工单` from `告警详情` (`由告警创建工单`) and links back with `sourceReferences` `{ domain: 'ALARM', relationship: 'ORIGIN' }`.
- `alarms-search-and-filter` syncs `q=` and shows `没有符合条件的告警` plus `清除筛选` when filters match zero rows.

## How to get to it (user POV)

- Open `运维` → `告警` in the sidebar, click an alarm in Overview's `需要处理` queue, or visit `/operations/alarms?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/alarms.spec.ts`.

- **Verify default active view and cleared toggle.** Open `/operations/alarms?site=${SITE_ID}`, assert `告警记录` shows `冷冻水回水温度过低` (`未确认`), click radio `已恢复`, and assert `view=cleared`.
- **Acknowledge, create work order, and filter.** Submit `已到现场查看` via `确认告警`, verify CSRF-tagged ack payload, raise a high-priority work order from `告警详情`, and exercise `搜索告警` + `清除筛选`.
- **Proof.** Capture pass output and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Alarm acknowledgement is stateful inside the gateway stand-in; run tests isolated per test case rather than chaining cleared counts across specs.
