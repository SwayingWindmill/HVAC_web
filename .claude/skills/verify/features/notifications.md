# Notifications

The `/notifications` route lists current active alarms (`正在发生`) above recently cleared alarms (`最近恢复`), synchronizes the unread count badge on the header bell (`通知，1 条告警没人确认`), filters unacknowledged items (`?unack=true`), and acknowledges alarms in place.

## Sub-features

- `notifications-feed-and-bell` orders active alarms ahead of cleared ones, auto-opens `告警详情`, and syncs the header bell badge count with unacknowledged alarm count.
- `notifications-filter-and-ack` filters `只看未确认` (`unack=true`), acknowledges an alarm (`POST /api/v1/alarms/{alarmId}/ack`), and clears the unread counter on the header bell button (`通知`).

## How to get to it (user POV)

- Click the header `通知` bell icon, or navigate to `/notifications?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/notifications.spec.ts`.

- **Verify feed ordering, unacknowledged filter, and bell sync.** Open `/notifications?site=${SITE_ID}`, assert `正在发生`, `最近恢复`, and bell label `通知，1 条告警没人确认`; toggle `只看未确认` (`unack=true`); open an active alarm, submit `确认告警` with note `已到现场查看`, and assert header button updates to `通知` (exact) and `gateway.writes` includes `/ack`.
- **Proof.** Capture pass output and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Unlike `/operations/alarms`, `/notifications` always highlights active alarms first even when acknowledged; assert `unack=true` filter separately from cleared/active toggles.
