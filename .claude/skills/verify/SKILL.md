---
name: verify
description: "Run HVAC Web in WSL, drive real operator workflows at 1440x900 via Playwright against either the recorded gateway (port 5190/5194) or live hvac-local stack (https://localhost:8443), and capture before/after screenshot and test evidence in .scratch/verify/. Use whenever verifying UI changes, bug fixes, or user-visible behavior."
---

# Verify HVAC Web

Exercise the real HVAC Web application in WSL using Playwright and Chromium, capture reproducible 1440×900 visual and behavioral proof in `.scratch/verify/<slug>/`, and clean up temporary processes without deleting evidence. Keep recorded-gateway results and live-stack results clearly separated.

Read [`features/README.md`](features/README.md) before driving a journey.

## Launch

Pick the tier that matches the change:

### Tier 1 — Recorded-gateway browser harness (fast, no Docker required)

1. Check if port `5190` is already in use by another worktree or server in WSL:
   ```bash
   npm run verify:doctor 5190
   ```
2. Prefer running `apps/hvac-web` with an isolated build and port (`5194` or another free port) under `CI=1` so parallel worktrees never cross-wire `5190`:
   ```bash
   CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/<spec>.spec.ts
   ```
   When running multiple specs iteratively against the same build, pre-build once into `apps/hvac-web/dist-e2e` and start `vite preview` on a dedicated port in the background, capturing its PID (`echo $!`) for teardown.

### Tier 2 — Live local platform `hvac-local` (`https://localhost:8443`)

1. Inspect container readiness first:
   ```bash
   npm run local:ps
   ```
2. Start the stack only if needed (and Docker Desktop is running):
   ```bash
   npm run local:up
   ```
   A live stack is ready when `https://localhost:8443/healthz` returns HTTP `200` and `/api/v1/principal` accepts the signed-in session. Never print or inspect files under `deploy/platform/phase1/runtime/`.

## Doctor

Run this single read-only check before debugging a flaky or blank browser run (it bridges Windows into WSL automatically and checks Linux Node, Chromium, and `/proc/net/tcp{,6}` ports without shell pipelines):

```bash
npm run verify:doctor
```

Confirm:
- Node is `v22.22.0` or newer (WSL Linux binary, never Windows `node`).
- `resolveLinuxBrowserExecutable()` returns an executable path without throwing.
- Any listener on `5190` / `5194` belongs to this run rather than another worktree.

## Drive

- **Recorded gateway:** Put deterministic end-to-end checks in `apps/hvac-web/e2e/<spec>.spec.ts` using fixtures under `apps/hvac-web/e2e/fixtures/` and owner-faithful mutations in `apps/hvac-web/e2e/gateway.ts`. Run via:
  ```bash
  CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/<spec>.spec.ts
  ```
- **Live `hvac-local` (`https://localhost:8443`):** Reuse `localAdministratorBrowser('https://localhost:8443')` from `scripts/lib/local-administrator-browser.mjs`, which opens headless Chromium at `1440×900`, submits the real login form with `local-admin.credentials`, verifies `/api/v1/principal`, and returns `{ browser, context, page, principal }`.
- **Selectors:** Always target accessible roles (`getByRole`, `getByLabel`, `getByTestId`, `getByText` with `GLOSSARY.md` Chinese labels), never brittle CSS class names or pixel coordinates.

## Evidence

Store all artifacts under `.scratch/verify/<slug>/` (gitignored, preserved across cleanup):

1. Capture **Before** and **After** proof:
   - UI changes: 1440×900 PNG screenshot (`fullPage: true` or key panel) plus Playwright / DOM assertion output showing user-visible text, numbers, and absence of console/page errors.
   - Bug fixes: failing test output or pre-fix screenshot + passing test output or post-fix screenshot.
   - Side effects: when mutating state (gateway writes, created work orders, user roles), also record the intercepted request payload or reload the second read view to prove persistence.
2. In PR descriptions and reports, label Tier 1 (`recorded gateway`) and Tier 2 (`live hvac-local 8443`) separately.

## Cleanup

1. Always close `await browser.close()` in a `try ... finally` block in live browser scripts.
2. Kill ONLY the exact PID(s) started by your verification session (`kill "$SERVER_PID"`); never run `pkill node` and never tear down a shared `hvac-local` stack you did not launch.
3. Verify `.scratch/verify/<slug>/` still contains the proof files after cleanup.

## Helpers

- [`scripts/lib/local-administrator-browser.mjs`](../../../scripts/lib/local-administrator-browser.mjs): shared helper for authenticated 1440×900 Chromium sessions against `https://localhost:8443`.
  Invocation pattern inside a temporary `.mjs`/`.ts` runner under `.scratch/verify/<slug>/`:
  ```ts
  import { localAdministratorBrowser } from '/mnt/e/Code/HVAC_web/scripts/lib/local-administrator-browser.mjs';
  const session = await localAdministratorBrowser('https://localhost:8443');
  try {
    await session.page.goto('https://localhost:8443/system?tab=users', { waitUntil: 'networkidle' });
    await session.page.screenshot({ path: '.scratch/verify/<slug>/after.png', fullPage: true });
  } finally {
    await session.browser.close();
  }
  ```
