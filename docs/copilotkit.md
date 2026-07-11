# CopilotKit integration

> This document describes the current frontend integration baseline. The target Agent product, runtime, context, tool, approval, audit, and rollout architecture is defined in `docs/ai-agent-product-technical-design.md`.

## Current UI baseline

The global assistant uses CopilotKit's official v2 UI without a custom window shell:

```tsx
import { CopilotKit, CopilotPopup } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
```

`CopilotPopup` owns the floating launcher, popup window, welcome state, message list, composer, streaming state, and mobile full-screen behavior. The application only provides Chinese labels, page context, frontend tools, and a minimal CSS safety layer that prevents horizontal scrolling in messages, Markdown, code blocks, tables, and the composer.

All CopilotKit components and hooks must import from the `@copilotkit/react-core/v2` entry point. Mixing `/v2` and `/v2/headless` can create separate React context module instances under Vite.

## Agent execution modes

The UI always uses the same `default` Agent identity.

### Local self-managed mode

When `VITE_COPILOTKIT_RUNTIME_URL` is empty, `AiProvider` registers `HvacMockAgent` through `selfManagedAgents`.

The local Agent:

- implements the official AG-UI event stream;
- reads the existing HVAC mock telemetry snapshot;
- streams responses into the official `CopilotPopup`;
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

- current route and page description;
- selected building and role;
- work-order, FDD, and optimization summary counts;
- permitted application routes;
- a permission-aware `navigate_to_page` frontend tool;
- an `open_ai_workspace` frontend tool.

No device-control tool is exposed.

## Bundle impact

The complete v2 prebuilt UI includes Markdown rendering, KaTeX, Mermaid, and syntax-highlighting resources. In the current production build, the main `vendor-copilotkit` chunk is approximately 2.51 MB minified and 622 KB gzip, in addition to lazily emitted diagram, language, theme, and font assets. This is an intentional tradeoff of adopting the complete official UI. A later performance phase may lazy-mount the global assistant, but must not remove official Popup capabilities without a product decision.

## Release constraints

- The official Popup must not be wrapped in a custom Drawer or Modal.
- No visible Popup descendant may create a horizontal scrollbar.
- Desktop uses the official floating-window geometry; mobile uses the official full-screen geometry.
- CopilotKit 1.62.3 currently emits a React development-only ref warning from its internal `DropdownMenuTrigger`. The browser audit reports this separately as a known upstream warning; all other console, network, HTTP, and runtime problems remain release-blocking.
