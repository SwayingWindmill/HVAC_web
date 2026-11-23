# System governance and audit log

The `/system` route hosts platform-wide tabs outside Site scope: `系统概览` (`overview`), `站点与组织` (`site`), `设备与点位登记` (`registry`), `操作记录` (`audit`), and `AI 助手` (`assistant`). (See [`system-users-and-roles.md`](./system-users-and-roles.md) for `用户与角色`.)

## Sub-features

- `system-overview-and-scope` exits any active `?site=` context (`[data-testid="real-shell-site"]` becomes `平台范围`) when opened from the user menu and summarizes platform status, sites, active gateways, and personal capabilities.
- `system-site-directory` (`/system?tab=site`) summarizes `站点概况` and lists authorized sites (`授权站点`), climate zones, floor areas, non-gateway device counts, and deep links to `设备与点位登记`.
- `system-registry-draft-guard` (`/system?tab=registry`) hosts persistent template catalog/revision preview, structured `DEVICE` template builder (standard points, quality rules, capability profiles), persistent `站点已应用模板` history, interactive recursive `空间层级` filter, multi-dimensional/cross-site asset & device search (`status`, `templateKey`, `tag`), asset/device key-value `tags` CRUD, and dirty `登记导入 JSON` navigation confirmation (`alertdialog` `离开会丢弃未保存内容`).
- `system-edge-model-hot-reload` (`/system?tab=governance&section=edge-models`) registers physical-bound-validated `EDGE_CONTROL` / `EDGE_FDD` hyperparam bundles, inspects structured diffs, enforces approval before shadow/production config-shadow deployment, and executes one-click rollback.
- `system-audit-and-assistant` (`/system?tab=audit` and `/system?tab=assistant`) gate on `audit.read`, translate audit actor/action/resource/outcome into Chinese business facts with zero raw UUIDs or enums, and list per-day AI assistant step failures in plain copy.

## How to get to it (user POV)

- Open the user menu in the top-right header, click `系统管理`, or navigate directly to `/system?tab=<overview|site|registry|audit|assistant>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/system.spec.ts` (note `/system?tab=audit` and `/system?tab=assistant` require `audit.read`, and `/system?tab=registry` import requires `registry.import`).

- **Verify site tab and draft guard.** Assert `站点概况`, `授权站点`, zero horizontal overflow at `1440×900`, transition into `设备与点位登记`, and `有未保存修改` prompt dismissal flow.
- **Verify audit log permission and translation.** Confirm `/system?tab=audit` disables `搜索操作记录` and skips `POST /api/v1/audit/search` without `audit.read`, and with `audit.read` renders translated rows (`撤销会话`, `未识别的操作`), filters via `搜索操作记录`, and never leaks UUIDs or enum codes.
- **Verify AI 助手 usage tab.** Confirm `每天的回答` and `查询调用明细` show plain failure reasons when `audit.read` is granted and refuse cleanly when absent.
- **Verify template catalog, structured activation, tags CRUD, and multi-dimensional search.** In `/system?tab=registry`, confirm `模板版本` renders persistent catalog entries + revision summary, releases a structured `DEVICE` template with standard points/capability profiles, assigns a revision, and persists across page reload in `站点已应用模板`. Under `资源与绑定`, add key-value tags (`vendor:carrier`) on a device, filter by tag (`按标签筛选`), filter by recursive `空间层级`, and toggle `全部授权站点` (`所属站点` column) at `1440×900` light and dark themes with zero overflow.
- **Verify edge model hot-reload governance (`e2e/edge-models.spec.ts`).** Open `/system?tab=governance&section=edge-models`, confirm 1440×900 light/dark overflow=0 and zero raw UUID/enum leaks, verify physical-bound validation rejection (`2.0°C`), register valid candidate `1.2.0`, inspect structured hyperparam diff, approve, deploy, and trigger one-click rollback restoring `1.0.0`.
- **Proof.** Capture pass output and `1440×900` PNGs under `.scratch/verify/<slug>/`.

## Gotchas

- Typing into `登记导入 JSON` blocks leaving `/system?tab=registry` with an `alertdialog`; dismiss with `继续编辑` or `丢弃并离开`.
- Non-admin recorded principals lack `audit.read`; `system.spec.ts` explicitly injects `audit.read` via `gateway.principalOverride` or `/api/v1/principal` routing when testing unlocked tabs.
