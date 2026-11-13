# Operations digital twin

The `/operations/digital-twin` route renders an interactive 3D plant scene with HTML HUD overlays for plant power, view switching (`冷站` `view=plant`), and URL-persisted equipment selection (`?inspect=`).

## Sub-features

- `twin-webgl-scene` initializes the WebGL `<canvas>` scene alongside non-blocking corner HUD panels (`冷站功率`, `冷站` heading).
- `twin-view-and-selection` switches to `冷站` (`view=plant`) and updates `?inspect=<DEVICE_ID>` when picking `CHILLER-01` in the context list without spawning scene popups.

## How to get to it (user POV)

- Select `运行` → `数字孪生` in the sidebar, or open `/operations/digital-twin?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/digital-twin.spec.ts`.

- **Verify WebGL canvas and URL state transitions.** Open `/operations/digital-twin?site=${SITE_ID}` (with `reducedMotion: 'reduce'`), wait up to 45s for `canvas` visibility, verify `冷站功率` and `冷站`, click `冷站` (`view=plant`), click `CHILLER-01`, and assert `inspect=` appears in URL alongside heading `CHILLER-01`.
- **Proof.** Capture Playwright stdout and `1440×900` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Software WebGL rendering in headless Linux CI/WSL can take up to 30–45s to mount `<canvas>`; keep `test.slow()` and the explicit 45s visibility timeout when modifying `e2e/digital-twin.spec.ts`.
- Currently only 松雅轩 spatial layout is populated in `twin-building.ts`; check Tier-2 building metadata before testing other sites.
