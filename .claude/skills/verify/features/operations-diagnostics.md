# Operations diagnostics

The `/operations/diagnostics` route lists Fault Detection & Diagnostics findings (`诊断发现列表`), renders measurement vs threshold details and recommended actions (`诊断发现详情`), and lets operators trigger manual rule evaluations (`立即诊断`).

## Sub-features

- `diagnostics-findings-and-detail` lists `冷冻水低温差` (`BTU-METER-01`, `3.4 / 3.5 K`, `轻微偏离`) and opens `诊断发现详情` (`?inspect=`) with action links (`去调整水泵`).
- `diagnostics-on-demand-rule` displays the active FDD rule revision (`第 1 版`, `3.5 K`, `15 分钟`), gate tooltips (`70%` runtime share), and triggers `POST /api/v1/sites/{siteId}/fdd/evaluate/low-delta-t` on `立即诊断`.
- `diagnostics-not-operating-guard` withholds judgement (`未运行`, no finding inserted) when plant runtime share is below `70%`, and reports unreadable rules cleanly.

## How to get to it (user POV)

- Click `运维` → `故障诊断` in the sidebar, click a finding from Overview's `需要处理` queue, or open `/operations/diagnostics?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/diagnostics.spec.ts`.

- **Inspect finding measurement and causes.** Open `/operations/diagnostics?site=${SITE_ID}&range=all`, click `冷冻水低温差`, verify `?inspect=`, `aria-current="true"`, `测量值3.38 K`, `阈值3.5 K`, and `去调整水泵`.
- **Run evaluation under running and insufficient-runtime gates.** On `/operations/diagnostics?site=${SITE_ID}`, click `立即诊断` to verify `仍在持续` (`24 小时内不重复记录`), then set `gateway.fddOperatingMinutes = 9` and verify `未运行` (`不足 70%，不判断温差`).
- **Proof.** Capture test stdout and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- `evaluate/low-delta-t` deduplicates repeated findings for the same asset and `ruleRevisionId` within 24 hours; reset `gateway.findings` or `fddRule` per test when testing fresh insertion.
