# HVAC Web verification map

This directory indexes user-facing verification journeys for HVAC Web. Read the baseline rules here before running or extending any feature recipe.

## Baseline preconditions

- Work from WSL at `/mnt/e/Code/HVAC_web` with Node `>= 22.22.0` (never Windows `node.exe`).
- Tier 1 (recorded gateway): run Playwright with an isolated port (`CI=1 PLAYWRIGHT_PORT=5194`) so it never collides with another worktree's dev server on `5190`.
- Tier 2 (`hvac-local`): confirm `https://localhost:8443/healthz` returns HTTP `200` and authenticate via `localAdministratorBrowser('https://localhost:8443')` from `scripts/lib/local-administrator-browser.mjs`.
- Never drive or stop an `hvac-local` stack or port `5190` server you did not start.

## Driving conventions

- Target `1440×900` desktop viewport for UI review and regression checks.
- Select elements strictly by accessible roles, labels, headings, or `data-testid` using `GLOSSARY.md` Chinese copy.
- Save artifacts under `.scratch/verify/<slug>/` and inspect screenshots/DOM text before asserting a fix works.

## Proof and skip reporting

- Separate Tier 1 (`recorded gateway`) evidence from Tier 2 (`live hvac-local 8443`) evidence in PRs and summaries.
- Retain `.scratch/verify/<slug>/` proof artifacts after cleaning up temporary servers or Playwright browsers.
- If a precondition is unmet (for example `hvac-local` is stopped because Docker Desktop is not running), report the exact unmet precondition instead of substituting a different check.

## Features

- [Overview and data-gap banner](./overview-and-data-gaps.md) covers `/overview` energy tabs, live plant KPIs, queue navigation, and top data-gap banners (`data-gap-banner`).
- [Integrations workspace](./integrations-workspace.md) covers `/settings/integrations` tabs (`现场网关`, `电表数据核对`, `传感器校准`), chart/table toggles, drawers, and work-order creation.
- [System users and roles](./system-users-and-roles.md) covers `/system?tab=users` user administration, role summaries, password change, and capability group rendering without unnamed keys.
