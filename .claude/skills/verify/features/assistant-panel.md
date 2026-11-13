# Operations AI assistant panel

The top-right `AI 助手` toggle and inline `解释` actions open a 420px supplementary drawer (`complementary` `AI 助手`) backed by `operations-agent-service` SSE sessions.

## Sub-features

- `assistant-inline-explain` pre-fills contextual questions when clicking `解释` on hourly consumption, alarm details, or device inspectors.
- `assistant-finding-card` renders `[data-testid="assistant-finding"]` with plain-language conclusions (stated once in the answer), data-limitation notes, native `figure` charts, and source links back to `/energy-analysis/consumption`.
- `assistant-follow-up-turns` displays expandable read steps (`查询了 2 项数据`), sends follow-ups via `:run` with `expectedRevision`, keeps follow-up chips only on the latest answer, and shows `[data-testid="assistant-error"]` when disconnected.

## How to get to it (user POV)

- Click `AI 助手` in the global header from any Site route, or click `解释` next to an hour row, alarm, or device card.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/assistant.spec.ts` with clock pinned to `energy-series.json` `electricity.metadata.dataWatermark`.

- **Ask via inline 解释 and inspect finding card.** On `/energy-analysis/consumption?site=${SITE_ID}`, click `解释 11:00`, verify `textbox` `向助手提问` pre-fill, click `发送`, assert `[data-testid="assistant-finding"]` (`有数据依据`, `多用电+18kWh`, `figure` chart), expand `查询了 2 项数据`, and confirm follow-up triggers `POST ...:run`.
- **Verify unavailable state.** Set `gateway.assistant.unavailable = true`, open `AI 助手`, and assert `[data-testid="assistant-error"]` reads `这个环境还没有接入 AI 助手。`.
- **Proof.** Capture Playwright stdout and `1440×900` drawer screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- `assistant.spec.ts` pins `page.clock.setFixedTime` to `dataWatermark` because `解释 <hour>` questions derive exact calendar dates from the Site clock.
