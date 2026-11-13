# System governance and audit log

The `/system` route hosts platform-wide tabs outside Site scope: `系统概览` (`overview`), `站点与组织` (`site`), `资产与点位` (`registry`), `操作记录` (`audit`), and `AI 助手` (`assistant`). (See [`system-users-and-roles.md`](./system-users-and-roles.md) for `用户与角色`.)

## Sub-features

- `system-overview-and-scope` exits any active `?site=` context (`[data-testid="real-shell-site"]` becomes `平台范围`) when opened from the user menu and summarizes platform status, sites, active gateways, and personal capabilities.
- `system-site-directory` (`/system?tab=site`) summarizes `站点概况` and lists authorized sites (`授权站点`), climate zones, floor areas, non-gateway device counts, and deep links to `资产与点位`.
- `system-registry-draft-guard` (`/system?tab=registry`) tracks dirty `登记导入 JSON` edits with `有未保存修改` and confirms before tab switches or sidebar navigation (`alertdialog` `离开会丢弃未保存内容`).
- `system-audit-and-assistant` (`/system?tab=audit` and `/system?tab=assistant`) gate on `audit.read`, translate audit actor/action/resource/outcome into Chinese business facts with zero raw UUIDs or enums, and list per-day AI assistant step failures in plain copy.

## How to get to it (user POV)

- Open the user menu in the top-right header, click `系统管理`, or navigate directly to `/system?tab=<overview|site|registry|audit|assistant>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/system.spec.ts` (note `/system?tab=audit` and `/system?tab=assistant` require `audit.read`, and `/system?tab=registry` import requires `registry.import`).

- **Verify site tab and draft guard.** Assert `站点概况`, `授权站点`, zero horizontal overflow at `1440×900`, transition into `资产与点位`, and `有未保存修改` prompt dismissal flow.
- **Verify audit log permission and translation.** Confirm `/system?tab=audit` disables `搜索操作记录` and skips `POST /api/v1/audit/search` without `audit.read`, and with `audit.read` renders translated rows (`撤销会话`, `未识别的操作`), filters via `搜索操作记录`, and never leaks UUIDs or enum codes.
- **Verify AI 助手 usage tab.** Confirm `每天的回答` and `各个步骤` show plain failure reasons when `audit.read` is granted and refuse cleanly when absent.
- **Proof.** Capture pass output and `1440×900` PNGs under `.scratch/verify/<slug>/`.

## Gotchas

- Typing into `登记导入 JSON` blocks leaving `/system?tab=registry` with an `alertdialog`; dismiss with `继续编辑` or `丢弃并离开`.
- Non-admin recorded principals lack `audit.read`; `system.spec.ts` explicitly injects `audit.read` via `gateway.principalOverride` or `/api/v1/principal` routing when testing unlocked tabs.
