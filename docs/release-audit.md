# Release audit

The project provides four browser-audit commands.

```bash
npm run audit:ui
npm run audit:bigscreen
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
- deterministic first entry from `/energy` to a fully rendered `/energy/month` workspace, energy MTD boundaries, future-period handling, category/device drilldown, current-view export, year-to-month navigation, and week-to-day navigation;
- energy visual-density limits for the first analytical chart: 650px on desktop, 820px on tablet, and 1000px on mobile;
- productized official CopilotKit `CopilotPopup`: persistent 58px theme-color circular launcher with white assistant icon and attention badge, visible close state while open with zero popup overlap, 520×660 desktop geometry, 20px floating-window radius, 76px-high and 18px-radius composer, 40px send action, unavailable add-action suppression, visible border and restrained shadow, focus-state geometry, exactly three header actions, conversation-title-only header, one route-aware context line, no welcome hero/scope card/suggestion list, three recent sessions, simplified title-and-time history rows, in-panel history search, historical message restoration, local self-managed Agent streaming, Generative UI evidence panels with no custom business text below 11px, mobile full-screen geometry, opaque light/dark surfaces, and zero horizontal overflow;
- official `/ai` `CopilotChat` workspace: shared Agent/thread/context with the Popup, fixed full-bleed AppShell workspace with no PageScaffold Hero, no content padding, no outer radius or border, divider-only thread/conversation/evidence regions, center conversation at least 1.5× either side rail, bottom-anchored Composer in both empty and started states, independently scrollable empty-state guidance above the Composer, shared 760px content axis for guidance/suggestions/Composer, vertically stacked content-width suggestion actions with 40px height, at least 10px radius, visible surface, single-line text and right arrow, centered 78px-high and 18px-radius Composer, 42px send action, unavailable add-action suppression, focus-state geometry, compact pre-investigation evidence rail that expands after the first message, search and historical investigation restore, localStorage persistence across reload, Generative UI rendering, no custom business text below 11px, zero page-level vertical scrolling, vertical scrolling only inside the center content viewport, and zero horizontal overflow;
- known third-party development warnings are counted separately and narrowly matched; all other console, network, HTTP, and runtime problems remain release-blocking.

The current matrix contains 42 access checks, 84 theme/viewport checks, and 24 interaction checks: 150 checks in total.

### `npm run audit:bigscreen`

Runs the dedicated presentation-layout audit against the BigScreen route. When no server is available at `http://127.0.0.1:5173`, the command starts a temporary Vite development server and shuts it down after the audit.

Coverage:

- 1920×1080, 1440×900, 1366×768, and 1024×768 presentation viewports;
- every visible ECharts canvas remains inside its explicit chart frame and does not intersect another chart;
- minimum chart heights remain readable, with low-height desktop layouts dropping secondary panels instead of compressing charts;
- the Three.js canvas fills the central system viewport;
- the device status rail remains collision-free and fixed-position device overlays do not return;
- page-level overflow, operational footer content, the loaded 禾苗 brand mark/title, controlled-zoom guidance, and the declared 10.5–18.5 camera-distance range.

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
5. BigScreen layout audit;
6. HVAC operations-loop audit;
7. Impeccable design audit.

The command stops the temporary preview server whether the audit passes or fails.

For local iteration after a successful build, the runner also supports:

```bash
node scripts/audit-all.mjs --skip-build
```

## Environment variables

- `HVAC_AUDIT_BASE_URL`: use an existing server instead of starting the default local audit server.
- `HVAC_AUDIT_DEV_PORT`: standalone `audit:ui`, `audit:bigscreen`, and `audit:ops-loop` server port; default `5173`.
- `HVAC_AUDIT_PORT`: production preview port used by `audit:all`; default `4173`.
- `HVAC_UI_AUDIT_DEBUG_PORT`: Edge DevTools port for the UI audit; default `9342`.
- `HVAC_BIGSCREEN_AUDIT_DEBUG_PORT`: Edge DevTools port for the BigScreen layout audit; default `9335`.

Microsoft Edge must be installed in one of the standard Windows installation locations.
