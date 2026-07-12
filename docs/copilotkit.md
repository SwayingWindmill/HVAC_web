# CopilotKit integration

> This document describes the current frontend integration baseline. The target Agent product, runtime, context, tool, approval, audit, and rollout architecture is defined in `docs/ai-agent-product-technical-design.md`.

## Current UI baseline

The AI surfaces use CopilotKit's official v2 UI rather than an application-owned chat renderer:

```tsx
import { CopilotKit, CopilotChat, CopilotPopup } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
```

`CopilotPopup` owns the global floating launcher, popup window, open/close state, focus, Escape handling, message list, composer, streaming state, and mobile full-screen behavior. The `/ai` route embeds the official `CopilotChat` as the main operations workspace. Both surfaces use the same `default` Agent, active thread, context bridge, frontend tools, suggestions, and Generative UI components.

HVAC product identity is applied through official slot/props rather than an external window shell or a second message renderer: the official toggle button receives a branded content icon, the official modal header supplies the functional close button to an HVAC header layout, and the welcome screen, labels, suggestions, disclaimer, context toolbar, and CSS tokens are application-owned.

All CopilotKit components and hooks must import from the `@copilotkit/react-core/v2` entry point. Mixing `/v2` and `/v2/headless` can create separate React context module instances under Vite.

### HVAC product slots

The current Popup customizes only official extension points:

- `toggleButton`: official `CopilotChatToggleButton` with a low-weight 120×40 HVAC Raised Surface, presence indicator, short assistant label, and attention count;
- `header`: official `CopilotModalHeader` with its injected functional close button, current scope, in-panel history, and new-session action; full-workspace navigation remains in the history footer rather than competing in the compact header;
- `welcomeScreen`: route-aware scope, three page actions, two recent sessions, and composer; no decorative AI hero, duplicated global metrics, nested cards, or equal-weight metric strip;
- Popup history: an application-owned overlay inside the official 520×680, 16px-radius Popup supports search and thread restoration without opening a second Modal or Drawer; the launcher is hidden while the Popup is open;
- `/ai` workspace: a full-bleed AppShell workspace with no page Hero or outer Card; it keeps the shared operations colors and typography while thread navigator, primary official CopilotChat pane, and evidence inspector are separated only by 1px dividers;
- `labels`: Chinese title, dynamic input placeholder, toolbar labels, and safety copy;
- `input.disclaimer`: explicit read-only and human-approval boundary;
- Generative UI cards: single evidence panels with typography, dividers, and semantic state color; no card-within-card or decorative shadow stack;
- CSS tokens/classes: solid HVAC light/dark surfaces, restrained motion, and a strict no-horizontal-scroll contract.

The Popup must not be wrapped in an application Drawer or Modal, and the application must not duplicate its open/close state.

### CopilotKit UI boundary

The product keeps the official `CopilotChat` for the main message surface because it is designed to be embedded and sized inside any application layout. Product styling should prefer the component slot system for message, composer, and nested controls, while the HVAC application owns the surrounding thread navigation and evidence inspector. Fully Headless UI is reserved for a future non-chat interaction model or a layout that the official slots cannot express; it must not be introduced merely to restyle the current workspace.

The current local Zustand thread repository mirrors the product interactions needed for the frontend phase. A later authenticated backend may adopt CopilotKit `useThreads` for persistent, resumable conversations, rename/archive/delete operations, and realtime cross-device synchronization when the Enterprise Intelligence Platform is part of the deployment architecture.

### Thread history and workspace scrolling

`src/ai/history.ts` provides the frontend thread contract for the current demo phase. Zustand persist stores thread metadata and serializable Agent messages under `hvac-ai-thread-history-v1`. Popup and `/ai` can search and restore the same records. The workspace supports new session, rename, pin, archive, delete, filters, and pagination. A transient empty CopilotKit message array must never erase a stored non-empty thread; only an explicit new-session action may create an empty active thread.

The `/ai` route is a fixed, full-bleed workspace. It deliberately omits the standard page Hero, 20px content padding, and outer Card because the left application navigation already identifies the module and the active thread header supplies the working context. It retains the shared operations colors, 8px controls, and 12–14px business copy. The browser document, AppShell Content, AI hub, thread navigator, and evidence inspector must not scroll vertically. The only vertical scrolling surface is the CopilotKit message viewport in the center column; the composer remains visible. At tablet/mobile widths, thread and evidence rails move into Ant Design Drawers while the page remains fixed.

This local persistence layer is a frontend implementation boundary, not the final source of truth. Remote Runtime mode must eventually replace it with authenticated server-side thread, task, report, and audit persistence while preserving the same UI contract.

## Agent execution modes

The UI always uses the same `default` Agent identity.

### Local self-managed mode

When `VITE_COPILOTKIT_RUNTIME_URL` is empty, `AiProvider` registers `HvacMockAgent` through `selfManagedAgents`.

The local Agent:

- implements the official AG-UI event stream;
- reads the existing HVAC mock telemetry snapshot;
- streams responses into the official `CopilotPopup` and `/ai` `CopilotChat`;
- remains strictly read-only;
- exposes no device-control, work-order mutation, or optimization-dispatch capability.

### Remote Runtime mode

When `VITE_COPILOTKIT_RUNTIME_URL` is configured, the provider connects the same official UI to the trusted backend Runtime:

```bash
VITE_COPILOTKIT_RUNTIME_URL=/api/v1/copilotkit
```

The backend Runtime is responsible for model credentials, authentication, RBAC, tool authorization, audit logging, persistence, and human approval for write operations. Model credentials must never be exposed through Vite environment variables.

## Registered application context

`CopilotContextBridge` exposes:

- current route, page title, page description, and route-specific welcome copy;
- selected building, role, object/period label, and human-readable scope;
- work-order, FDD, optimization summary counts, and aggregate attention count;
- route-specific static suggestions and input placeholder;
- permitted application routes;
- a permission-aware `navigate_to_page` frontend tool;
- an `open_ai_workspace` frontend tool.

The bridge also registers three read-only Generative UI components through `useComponent`:

- `render_asset_status_card` → `AssetStatusCard`;
- `render_energy_anomaly_card` → `EnergyAnomalyCard`;
- `render_fdd_evidence_card` → `FddEvidenceCard`.

Their Zod schemas are shared by the local Agent and future Runtime tools. The cards only display evidence, metrics, and business deep links. No device-control tool is exposed.

## Bundle impact

The complete v2 prebuilt UI includes Markdown rendering, KaTeX, Mermaid, and syntax-highlighting resources. In the current production build, the main `vendor-copilotkit` chunk is approximately 2.51 MB minified and 622 KB gzip, in addition to lazily emitted diagram, language, theme, and font assets. This is an intentional tradeoff of adopting the complete official UI. A later performance phase may lazy-mount the global assistant, but must not remove official Popup capabilities without a product decision.

## Release constraints

- The official Popup must not be wrapped in a custom Drawer or Modal.
- The `/ai` route must use the official `CopilotChat` message/composer pipeline; it must not introduce a parallel application-owned chat renderer.
- Popup and workspace Chat must resolve to the same `default` Agent and active thread.
- No visible Popup or workspace Chat descendant may create a horizontal scrollbar.
- Desktop Popup uses the official floating-window geometry; mobile Popup uses the official full-screen geometry. Embedded `CopilotChat` remains inside the responsive `/ai` page layout.
- CopilotKit 1.62.3 currently emits a React development-only ref warning from its internal `DropdownMenuTrigger`. The browser audit reports this separately as a known upstream warning; all other console, network, HTTP, and runtime problems remain release-blocking.
