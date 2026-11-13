# HVAC Web verification map

This directory indexes user-facing verification journeys for HVAC Web across all 23 operator surfaces and 24 Playwright E2E specs. Read the baseline rules here before running or extending any feature recipe.

## Baseline preconditions

- Work from WSL at `/mnt/e/Code/HVAC_web` with Node `>= 22.22.0` (never Windows `node.exe`).
- Tier 1 (recorded gateway): run Playwright with an isolated port (`CI=1 PLAYWRIGHT_PORT=5194`) so it never collides with another worktree's dev server on `5190`.
- Tier 2 (`hvac-local`): run `npm run verify:live -- --slug=<slug> --route=/overview` (requires `https://localhost:8443/healthz` HTTP `200`, authenticated via `localAdministratorBrowser('https://localhost:8443')`).
- Never drive or stop an `hvac-local` stack or port `5190` server you did not start.

## Driving conventions

- Target `1440×900` desktop viewport for UI review and regression checks.
- Select elements strictly by accessible roles, labels, headings, or `data-testid` using `GLOSSARY.md` Chinese copy.
- Save artifacts under `.scratch/verify/<slug>/` and inspect screenshots/DOM text before asserting a fix works.

## Proof and skip reporting

- Separate Tier 1 (`recorded gateway`) evidence from Tier 2 (`live hvac-local 8443`) evidence in PRs and summaries.
- Retain `.scratch/verify/<slug>/` proof artifacts after cleaning up temporary servers or Playwright browsers.
- If a precondition is unmet (for example `verify:live` exits `2` `PRECONDITION_UNMET` because `hvac-local` is stopped), report the exact unmet precondition instead of substituting a different check.

## Features

### 总览与全站入口

- [Sign-in and MFA](./sign-in-and-mfa.md) covers `/sign-in` password and TOTP form submissions, error alerts, and expired login notices (`e2e/sign-in.spec.ts`).
- [Overview and data-gap banner](./overview-and-data-gaps.md) covers `/overview` energy tabs, live plant KPIs, queue navigation, and top `data-gap-banner` alerts (`e2e/overview.spec.ts`).
- [Operations AI assistant panel](./assistant-panel.md) covers the 420px `AI 助手` drawer, inline `解释` prompts, finding charts, follow-up turns, and disconnected error copy (`e2e/assistant.spec.ts`).
- [Notifications](./notifications.md) covers `/notifications` active-above-cleared alarm feed, `只看未确认` filtering, in-place acknowledgement, and header bell count sync (`e2e/notifications.spec.ts`).

### 运行与运维

- [Operations realtime](./operations-realtime.md) covers `/operations/realtime` live plant KPIs, honest `未订阅` polling indicators, URL metric/window state, and gap-preserving curves (`e2e/realtime.spec.ts`).
- [Operations trends](./operations-trends.md) covers `/operations/trends` physical-unit chart lanes, granularity/comparison sync, `统计表`, and 8-point custom picker (`e2e/trends.spec.ts`).
- [Operations schedules](./operations-schedules.md) covers `/operations/schedules` weekly slot configuration (including cross-midnight intervals), overtime exceptions, and unscheduled-running work orders (`e2e/schedules.spec.ts`).
- [Operations systems and devices](./operations-systems-devices.md) covers `/operations/systems-devices` card wall and table views, observed point counts, inspector drawers, and asset work orders (`e2e/systems-devices.spec.ts`).
- [Operations digital twin](./operations-digital-twin.md) covers `/operations/digital-twin` WebGL plant scene, corner HUD metrics, `view=plant`, and equipment URL inspection (`e2e/digital-twin.spec.ts`).
- [Operations alarms](./operations-alarms.md) covers `/operations/alarms` active/cleared views, acknowledgement persistence, alarm-origin work orders, and keyword filtering (`e2e/alarms.spec.ts`).
- [Operations diagnostics](./operations-diagnostics.md) covers `/operations/diagnostics` FDD finding inspection, manual evaluation runs, and low-runtime gating (`e2e/diagnostics.spec.ts`).
- [Operations work center](./operations-work-center.md) covers `/operations/work-center` open/closed boards, claim-start-complete enforcement, and equipment backlinks (`e2e/work-center.spec.ts`).
- [Operations control](./operations-control.md) covers `/operations/control` setpoint/stop confirmations, CJK IME `Enter` safety, approval timelines, and theme `color-scheme` sync (`e2e/control.spec.ts`).

### 能源分析与报告

- [Energy consumption](./energy-consumption.md) covers `/energy-analysis/consumption` period energy/COP/cost tabs, TOU breakdown, and scroll-stable hourly tables (`e2e/consumption.spec.ts`).
- [Energy load](./energy-load.md) covers `/energy-analysis/load` quarter-hour demand KPIs, 24-hour load forecast curves/tables, daily peaks, and peak-demand billing status (`e2e/load.spec.ts`).
- [Energy billing](./energy-billing.md) covers `/energy-analysis/billing` monthly budgets, utility settlement reconciliation, whole-building allocation, and 1440px layout (`e2e/billing.spec.ts`).
- [Energy optimization](./energy-optimization.md) covers `/energy-analysis/optimization` recommendation runs, readiness progressive disclosure, command dispatch, and manual fallback (`e2e/optimization.spec.ts`).
- [Energy savings](./energy-savings.md) covers `/energy-analysis/savings` measure registration in Site local time, baseline fit gates (`CV(RMSE)`), and verified avoided energy (`e2e/savings.spec.ts`).
- [Energy reports](./energy-reports.md) covers `/reports` live current-month compilation, archived closed-month snapshots, and annual month-by-month tables (`e2e/reports.spec.ts`).
- [Site benchmark](./site-benchmark.md) covers `/benchmark` cross-site plant COP, floor-area intensity, `CDD26` climate adjustment, and denied-site isolation (`e2e/benchmark.spec.ts`).

### 平台与系统管理

- [Integrations workspace](./integrations-workspace.md) covers `/settings/integrations` tabs (`现场网关`, `电表数据核对`, `传感器校准`), enrollment-code disposal on `Escape`, and permanent gateway revocation (`e2e/integrations.spec.ts`, `e2e/gateway-credentials.spec.ts`).
- [System users and roles](./system-users-and-roles.md) covers `/system?tab=users` user administration, role summaries, password change, and capability group rendering without unnamed keys (`e2e/users.spec.ts`, `e2e/system.spec.ts`).
- [System governance and audit log](./system-governance-and-audit.md) covers `/system` platform overview, `站点与组织`, `资产与点位` unsaved draft guard, `操作记录` (`audit.read` + translated facts), and `AI 助手` usage (`e2e/system.spec.ts`).
