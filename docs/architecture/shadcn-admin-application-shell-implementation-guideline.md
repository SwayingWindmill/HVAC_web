# shadcn-admin Application Shell Implementation Guideline

> Source basis: [`docs/reference/shadcn-admin与shadcn-ui在智慧能源React-SPA中的应用方案.md`](../reference/shadcn-admin与shadcn-ui在智慧能源React-SPA中的应用方案.md), approved by the user as the current plan authority.
> Scope: apps/hvac-web frontend refactor.

## Decision

The project adopts:

- shadcn/ui as the base UI component source.
- shadcn-admin as Application Shell and interaction pattern reference only.
- Our own Energy Domain Components and feature architecture as the product layer.
- Radix as the shadcn/ui primitive baseline for this application.

## Boundary

```text
shadcn/ui
    ↓
components/ui
    ↓
Domain Components
    ↓
Features
    ↓
Energy Product
```

shadcn-admin contributes patterns:

- App Shell
- Sidebar composition
- Header
- Command Menu
- Theme patterns
- Responsive layout
- DataTable UX

It does not become the product framework.

## App Shell migration rule

Replace the existing ProLayout-based shell directly. Do not create a compatibility wrapper around Ant Design Pro.

The new shell should be built around:

- TanStack Router route structure
- TanStack Query server state
- shadcn/ui primitives
- Tailwind semantic tokens
- Lucide icons

## Component ownership

`components/ui`

- Generic UI primitives only.
- No energy business semantics.

`components/domain`

- DeviceStatus
- MetricValue
- TelemetryQuality
- AlarmSeverity
- CommandStatus
- TimeRangePicker

`features`

- Device
- Energy
- Alarm
- Storage
- PV
- Operations

## Rejected approaches

- Do not fork shadcn-admin as a long-term template.
- Do not preserve Ant Design Pro compatibility layers.
- Do not put business rules into UI primitives.
- Do not keep demo pages, mock stores, or template authentication patterns.

## Current next implementation target

Refactor ShellChrome into a new shadcn-admin-inspired AppShell:

- Sidebar
- Header
- Navigation model
- Theme integration
- Command Menu integration
- Responsive behavior

without retaining ProLayout or antd dependencies.
