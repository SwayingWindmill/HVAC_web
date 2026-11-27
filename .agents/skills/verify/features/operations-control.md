# Operations control

The `/operations/control` route (`运行控制`) is a three-layer precision instrumentation console: a full-bleed top control cockpit (`控制方式`, proportional unit power load ruler, chilled/condenser header temperatures, live status, history drawer trigger, and site shutdown), a conditional inline blocking/execution channel for active commands, and a split workspace pairing the interactive hydraulic plant canvas (`冷链水路拓扑` default + `精密横轨表` toggle) on the left with a 400px five-gate equipment faceplate on the right. Start/stop and unit sequences are high-risk commands behind an `alertdialog` asking for the one-time code and a site check; setpoints tune inline on the faceplate with a slider and IME-safe numeric input; switching the control mode or locking an asset withdraws commands not yet dispatched.

## Sub-features

- `control-mode-bar` (`ControlCockpitStrip`) reads `GET /api/v1/sites/{siteId}/control-state` and `GET /api/v1/sites/{siteId}/optimization/auto-control-lease`; the `控制方式` region shows `远程自动控制` / `人工接管` / `安全退出`, who set it (`updatedByName` from IAM, falling back only to `我` for self or `系统` without `其他人员`), reason, lock/pending counts, active auto-control lease badge (`自动控制租约生效中` with expiry and `管理自动租约 ›` link to `/energy-analysis/optimization`), total power + unit load ruler + chilled/condenser headers, `历史 (N)` drawer trigger, `刷新`, and `停机…`. Leaving remote auto lists withdrawn commands; `安全退出` is disabled while any command is `下发中` or `待现场确认`.
- `control-active-channel` (`ActiveCommandChannel`) appears under the cockpit bar strictly when non-settled commands exist, offering direct device focus, `批准` / `撤回` / `已生效` / `没生效` / `经过 ›`.
- `control-unit-canvas` (`PlantTopologyCanvas` + `PlantSwissTable`) groups each `CHILLER` unit with the assets named by its compiled `START` `sourceMetadata.interlockChecks` (or combines standalone hydraulic members on single-unit VFD plants), renders condenser/chilled water headers, displays bound `HVAC_POWER_METER` active power (`kW` with `tabular-nums`, or `暂无读数` when unbound or stale), unit sequence actions (`一键启动` / `一键停机` / `中止…`), and interactive device nodes (`group` per asset). Blocked interlocks surface hints (`先开 …` / `先停 …`).
- `control-five-gate-faceplate` (`DeviceFaceplate`) renders for the selected device: live readings (`暂无独立传感器读数` when digital-only), the 5 ordered safety gates (`1. 控制权边界`, `2. 设备锁定保护`, `3. 计划检修窗口`, `4. 通信与指令态`, `5. 水路联锁前提` with one-click `选中 … ›` jump buttons to blocking upstream assets), inline active command card, inline slider + IME-safe setpoint input, primary action (`启动` / `停止` / `复位`), LOTO lock (`挂牌上锁` / `修改/解牌`), priority lock (`设置隔离` / `管理/解除`), and device command shortcuts.
- `control-high-risk-confirm` asks `动态口令` (`inputMode="numeric"`, `autoComplete="one-time-code"`, 6 digits) and `我已和现场确认可以停/开` before `提交停止` / `提交停机`; step-up errors (`CODE_INVALID`, `CODE_REPLAYED`, `MFA_LOCKED`, `MFA_NOT_CONFIGURED`) render in `role="alert"` (`MFA_NOT_CONFIGURED` includes `去开通两步验证 ›`). A `STEP_UP_REQUIRED` approval refusal opens `批准前要再验证一次身份`.
- `control-maintenance-window` displays `检修窗口「…」… 至 …，只能停机和复位` full-bleed inside `控制方式`; gate 3 disables start and setpoints while keeping stop/reset enabled.
- `control-log-and-timeline` opens the slide-out `指令记录` drawer (`全部` / `待审批` / `执行中` / `待现场确认` filters) and `?inspect=` detail timeline sheet.
- `control-shell-chip` shows `▲ 人工接管` / `◆ 安全退出` in the top bar on every Site page; the sidebar `运行控制` badge counts commands waiting on a person.
- `control-theme-appearance` synchronizes `meta[name="color-scheme"]` and document `color-scheme` across system, light, and dark modes.

## How to get to it (user POV)

- Click `运行控制` in the sidebar, the top-bar mode chip, or navigate to `/operations/control?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/control.spec.ts`. The recorded gateway answers start/stop as `HIGH` risk, requires `verificationCode`, refuses high-risk approval until `gateway.stepUpFresh = true`, and keeps `gateway.controlState`.

- **Setpoint with IME guard.** Keep `CHILLER-01` selected on load, fill `7.5` in `冷冻水出水设定温度目标值` on the right faceplate, verify composition `Enter` (`isComposing` / `keyCode === 229`) submits nothing, press `Enter`, confirm `确认下发`, and verify in the `历史` drawer.
- **High-risk stop and step-up.** Select `CHWP-01`, click `停止` in the faceplate, type the code, tick the site check, `提交停止`; approve from `阻塞与执行通道` to verify `STEP_UP_REQUIRED` until `stepUpFresh`, then inspect the executed item in `历史` drawer and `?inspect=` timeline.
- **Mode switch and lock.** `人工接管` with a reason withdraws pending commands, shows `updatedByName` (without `其他人员`), and displays the top-bar chip; `安全退出` is blocked by `OUTCOME_UNKNOWN`; selecting `CWP-01` and locking via `挂牌上锁` → `检修锁定` withdraws its command.
- **Unit power, gate 5 jump, priority locks, view toggle, and TOTP step-up failures.** Verify `CHILLER-01` power transitions (`暂无读数` → `186.2 kW` → `STALE`), compiled `interlockChecks` enable the gate 5 `选中 CHWP-01 ›` jump action, active `AUTO_WITH_LEASE` shows `自动控制租约生效中`, priority lock create/release works from the faceplate, `冷链水路拓扑` ↔ `精密横轨表` toggles cleanly, and all four `ProblemDetails.reason` failures render distinct guidance.
- **Proof.** Save stdout and `1440×900` light/dark plus `1280×800` assistant drawer screenshots under `.scratch/verify/<slug>/`.

## Gotchas

- Never submit commands on live Tier-2 `hvac-local` unless safe simulator targets are confirmed; use Tier 1 for writes.
- The recorded asset model represents a single chiller with standalone pump/tower targets (`isSingleLoopCombined` renders them as a connected loop on load); the four-unit multi-chiller layout with chiller-level interlock hints needs the Songyaxuan registry (Tier 2).
