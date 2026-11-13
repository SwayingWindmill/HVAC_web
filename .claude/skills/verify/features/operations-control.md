# Operations control

The `/operations/control` route issues setpoint and start/stop commands to plant equipment behind explicit `alertdialog` confirmations, handles multi-step approval workflows, tracks command execution timelines, and hosts the global appearance theme switch (`系统` / `浅色` / `深色`).

## Sub-features

- `control-setpoint-and-stop` submits low-risk setpoints (`冷冻水出水设定温度目标值`) and medium-risk stop commands (`停止冷冻水泵`) through `确认下发` confirmation dialogs (`POST /api/v1/commands` with `X-CSRF-Token`).
- `control-ime-enter-safety` ignores CJK IME composition `Enter` events (`isComposing: true` or post-composition `keyCode === 229`) before opening `确认下发`.
- `control-approval-and-timeline` lists `指令记录`, filters `待审批`, executes `:approve`, and opens `[role="dialog"]` (`?inspect=`) showing step transitions (`已提交` → `待审批` → `已批准` → `已执行`).
- `control-theme-appearance` synchronizes `meta[name="color-scheme"]` and document `color-scheme` across system, light, and dark modes.

## How to get to it (user POV)

- Click `控制指令` in the sidebar, or navigate to `/operations/control?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/control.spec.ts`.

- **Dispatch setpoint and stop command with IME guard.** Fill `7.5` in `CHILLER-01` `冷冻水出水设定温度目标值`, verify composition `Enter` (`keyCode: 229`) does NOT trigger `alertdialog`, press `Enter` cleanly, confirm `确认下发`, and approve `CHWP-01` `停止冷冻水泵` in `指令记录`.
- **Verify color-scheme sync.** Toggle `外观` radios between `深色` and `浅色` and inspect `meta[name="color-scheme"]`.
- **Proof.** Save stdout and `1440×900` light/dark screenshots under `.scratch/verify/<slug>/`.

## Gotchas

- Never test command submission on live Tier-2 `hvac-local` unless safe simulator targets are confirmed; use Tier 1 (`e2e/control.spec.ts`) for setpoint/stop write verification.
