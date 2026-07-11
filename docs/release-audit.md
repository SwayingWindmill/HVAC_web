# Release audit

The project provides three browser-audit commands.

```bash
npm run audit:ui
npm run audit:ops-loop
npm run audit:all
```

## Commands

### `npm run audit:ui`

Runs the full-site UI audit. When no server is available at `http://127.0.0.1:5173`, the command starts a temporary Vite development server and shuts it down after the audit.

Coverage:

- role-to-route access matrix for `demo`, `ops`, and `rd`;
- all product routes plus 403 and 404 states, including `/energy/year`, `/energy/month`, `/energy/week`, and `/energy/day`;
- light and dark themes;
- 1440×900, 1024×768, and 390×844 viewports;
- page-level horizontal overflow, blank states, visible loading states, and escaped content;
- console errors, uncaught exceptions, failed application resources, and HTTP 4xx/5xx responses;
- deep-link refresh, browser back/forward, Drawer and Modal Escape handling, Popconfirm, mobile Popover, BigScreen Escape, and 404 return navigation;
- energy MTD boundaries, future-period handling, category/device drilldown, current-view export, year-to-month navigation, and week-to-day navigation;
- energy visual-density limits for the first analytical chart: 650px on desktop, 820px on tablet, and 1000px on mobile;
- official CopilotKit `CopilotPopup`: native mount/open/close behavior, route-context updates, local self-managed Agent streaming, desktop/mobile geometry, opaque surfaces, and zero horizontal overflow across the popup, messages, composer, Markdown, code, and tables;
- known third-party development warnings are counted separately and narrowly matched; all other console, network, HTTP, and runtime problems remain release-blocking.

The current matrix contains 42 access checks, 84 theme/viewport checks, and 20 interaction checks: 146 checks in total.

### `npm run audit:ops-loop`

Runs the HVAC business-loop audit in one SPA session so Zustand state remains continuous.

Coverage:

- FDD diagnosis deep links;
- linked asset and optimization navigation;
- optimization submission, approval, and dispatch;
- FDD-to-work-order generation;
- work-order assignment, processing, and closure;
- closed work-order status propagation back to FDD;
- operations and demo role boundaries.

### `npm run audit:all`

Runs the release gate in this order:

1. TypeScript validation;
2. production build;
3. temporary Vite production preview;
4. full-site UI audit;
5. HVAC operations-loop audit;
6. Impeccable design audit.

The command stops the temporary preview server whether the audit passes or fails.

For local iteration after a successful build, the runner also supports:

```bash
node scripts/audit-all.mjs --skip-build
```

## Environment variables

- `HVAC_AUDIT_BASE_URL`: use an existing server instead of starting the default local audit server.
- `HVAC_AUDIT_DEV_PORT`: standalone `audit:ui` and `audit:ops-loop` server port; default `5173`.
- `HVAC_AUDIT_PORT`: production preview port used by `audit:all`; default `4173`.
- `HVAC_UI_AUDIT_DEBUG_PORT`: Edge DevTools port for the UI audit; default `9342`.

Microsoft Edge must be installed in one of the standard Windows installation locations.
