# Operations control

The `/operations/control` route (`运行控制`) shows the Site's control mode, every controllable chiller unit with its pumps and tower, and the command log. Start/stop and unit sequences are high-risk commands behind an `alertdialog` that asks for the one-time code and a site check; setpoints open from a device row; switching the control mode or locking an asset withdraws commands not yet dispatched. It also hosts the global appearance theme switch (`系统` / `浅色` / `深色`).

## Sub-features

- `control-mode-bar` reads `GET /api/v1/sites/{siteId}/control-state`; the `控制方式` region shows `远程自动控制` / `人工接管` / `安全退出`, who set it (`updatedByName` from IAM, falling back only to `我` for self or `系统` without `其他人员`), the reason, lock and pending counts, and a segmented choice that opens `切换到…？` (`PUT /control-mode` with `reason` and `expectedRevision`). Leaving remote auto lists the commands it withdraws; `安全退出` is disabled while any command is `下发中` or `待现场确认`.
- `control-unit-grid` groups each `CHILLER` asset with the assets named by its compiled `START` `sourceMetadata.interlockChecks` (`deviceId` mapped to `deviceCode`, checking `key === 'run_state'`), displays bound `HVAC_POWER_METER` active power (`kW` with `tabular-nums`, or `暂无读数` when unbound or stale) in the column header (`group` per device row, `一键启动` / `一键停机` per unit); a blocked interlock shows `先开 …` / `先停 …` and disables the button; assets no chiller names sit in `其他设备`.
- `control-high-risk-confirm` asks `动态口令` (`inputMode="numeric"`, `autoComplete="one-time-code"`, 6 digits) and `我已和现场确认可以停/开` before `提交停止` / `提交停机`; the request carries `verificationCode`. Step-up failures return `ProblemDetails.reason` (`CODE_INVALID`, `CODE_REPLAYED`, `MFA_LOCKED`, `MFA_NOT_CONFIGURED`) rendered in a `role="alert"` message (`MFA_NOT_CONFIGURED` includes a `去开通两步验证 ›` link to `/system?tab=users` where non-admin `selfView` notes two-step setup). A `STEP_UP_REQUIRED` refusal on approval opens `批准前要再验证一次身份`, which signs in again with `assurance=high` and returns to `?inspect=`.
- `control-maintenance-window` shows `检修窗口「…」… 至 …，只能停机和复位` in the `控制方式` region when `activeMaintenanceWindow` is set; start and setpoint controls are disabled while stop stays enabled.
- `control-asset-lock` opens `锁定 …？` / `解除 … 的锁定？` from the lock icon (`PUT /api/v1/assets/{assetId}/control-lock`), greys the row and shows `手动锁定` / `检修锁定`.
- `control-setpoint-and-ime` opens `调整 … 的设定值` from the sliders icon; composition `Enter` (`isComposing` or `keyCode === 229`) never submits.
- `control-log-and-timeline` lists `指令记录` with `待审批` / `执行中` / `待现场确认` filters, `批准` / `撤回` / `已生效` / `没生效`, and the `?inspect=` sheet with transitions.
- `control-shell-chip` shows `▲ 人工接管` / `◆ 安全退出` in the top bar on every Site page; the sidebar `运行控制` badge counts commands waiting on a person.
- `control-theme-appearance` synchronizes `meta[name="color-scheme"]` and document `color-scheme` across system, light, and dark modes.

## How to get to it (user POV)

- Click `运行控制` in the sidebar, the top-bar mode chip, or navigate to `/operations/control?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/control.spec.ts`. The recorded gateway answers start/stop as `HIGH` risk, requires `verificationCode`, refuses high-risk approval until `gateway.stepUpFresh = true`, and keeps `gateway.controlState`.

- **Setpoint with IME guard.** Open `调整 CHILLER-01 的设定值`, fill `7.5`, check composition `Enter` opens nothing, press `Enter`, confirm `确认下发`.
- **High-risk stop and step-up.** `CHWP-01` → `停止`, type the code, tick the site check, `提交停止`; `批准` opens the step-up dialog until `stepUpFresh`.
- **Mode switch and lock.** `人工接管` with a reason withdraws the pending command, shows `updatedByName` (without `其他人员`), and displays the top-bar chip; `安全退出` is blocked by a `OUTCOME_UNKNOWN` command; `锁定 CWP-01` → `检修锁定` withdraws its command.
- **Unit power, interlocks, and TOTP step-up failures.** Verify unbound chillers show `暂无读数`, bound active `HVAC_POWER_METER` shows `186.2 kW` (reverting to `暂无读数` when stale), compiled `interlockChecks` render precondition hints, and all four `ProblemDetails.reason` failures (`CODE_INVALID`, `CODE_REPLAYED`, `MFA_LOCKED`, `MFA_NOT_CONFIGURED`) show distinct guidance including the `/system?tab=users` enrollment link.
- **Proof.** Save stdout and `1440×900` light/dark plus `375×812` mobile screenshots of the unit grid, the mode dialog and the confirm dialog under `.scratch/verify/<slug>/`.

## Gotchas

- Never submit commands on live Tier-2 `hvac-local` unless safe simulator targets are confirmed; use Tier 1 for writes.
- The recorded asset model is the simulator plant (one chiller, no interlocks), so its grid shows `CHILLER-01` plus `其他设备`; the four-unit layout with interlock hints needs the Songyaxuan registry (Tier 2).
