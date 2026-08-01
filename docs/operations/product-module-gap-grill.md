# Product Module Gap Grill

Status: grilled; ready for ticket decomposition

Date: 2026-07-30

Historical status note (2026-08-01): This document records the planning decisions frozen on 2026-07-30. Module delivery has advanced since then; use `docs/operations/real-product-roadmap.md` for current status. The sequencing, authority boundaries, and acceptance decisions below remain the decision record used for ticket decomposition.

## Purpose

Freeze the remaining non-Agent product delivery roadmap, then define the first module deeply enough to create implementation-ready engineering tickets.

## Explicit exclusions

This discussion does not modify or decide:

- Operations Agent architecture, implementation, migration or deletion work;
- S2 Ticket #70 capacity, failure-injection and release-evidence work;
- S2 Ticket #71 canary, 100% cutover and Legacy current-state retirement.

Those tracks are handled separately and are not dependencies for the first product module discussed here.

## Current factual baseline

- Real Mode shell, authenticated Principal, effective capabilities and authorized Site routing are implemented.
- Registry, current telemetry, realtime recovery, command contracts and the first Site-scoped Energy Analytics slice exist.
- In Real Mode, Site Assets, Commands and BigScreen are still empty business surfaces.
- Alarms and Work Orders are marked not integrated; Optimization is hidden.
- Demo pages exist for Dashboard, Assets, Commands, Energy, Cost, FDD, Alarms, Optimization, BigScreen, AI and System, but Demo completion is not treated as Real product completion.
- Real product surfaces must consume authoritative Platform Gateway contracts and must not fall back to Demo, Mock, browser-owned Organization context or direct internal service calls.

## Confirmed decision 1 — discussion outcome

The desired outcome is both:

1. a complete ordered roadmap for the remaining product modules outside the excluded tracks; and
2. a fully grilled first module with scope, authority boundary, user outcomes, contracts, states, risks, acceptance criteria and verification sufficient to open implementation work.

## Confirmed decision 2 — roadmap priority principle

The roadmap is ordered by technical dependency, not by visual completeness or fastest isolated release.

Priority goes first to modules that establish reusable, authoritative Site, Device, telemetry, cache, realtime and navigation boundaries for later product surfaces. A module is not promoted merely because a Demo page already exists or because its UI can be shipped quickly without its real authority boundary.

## Frozen product roadmap

```text
1. Real Assets operations workspace
2. Real Dashboard
3. Complete Energy workspaces
4. Real Commands
5. Alarm and Work Order domains
6. FDD
7. Optimization
8. Cost and carbon
9. Real BigScreen
```

### Roadmap rationale

- **Real Assets first** establishes shared Device identity, Site-scoped inventory, current-state semantics, cache isolation, deep links and bounded realtime behavior.
- **Real Dashboard second** proves those shared capabilities can support a cross-device operational overview without inventing Alarm, FDD or Optimization facts.
- **Complete Energy workspaces third** extends an existing authoritative analytics boundary and supplies later Cost, Optimization and BigScreen dependencies.
- **Real Commands fourth** exposes only the low-risk S3 capabilities already supported by the server boundary.
- **Alarm and Work Order domains fifth** establish the first complete human operational handling lifecycle.
- **FDD precedes Optimization** because recommendations require stronger diagnostic and verification foundations.
- **Cost and carbon precede Real BigScreen** so savings, cost and carbon claims have authoritative sources rather than Demo narrative data.

## Confirmed decision 3 — first module boundary

The first implementation module is **Real Assets as an asset operations workspace**, not a Registry-only inventory page and not a cross-domain incident center.

It includes:

- authorized Site-scoped asset and Device inventory from Registry;
- Presence, Freshness, latest key telemetry and data-quality state;
- bounded realtime updates and recovery using the existing telemetry platform boundary;
- a read-only Device detail surface;
- explicit empty, unavailable, stale, unknown and forbidden states;
- cache and subscription isolation across Site and Session transitions.

It excludes:

- command execution;
- Alarm lifecycle;
- Work Orders;
- FDD findings;
- Optimization actions;
- Registry writes;
- bulk export;
- arbitrary raw telemetry browsing.

Later domains may deep-link to an Asset or Device, but they do not become part of the first module's ownership boundary.

# Real Assets implementation baseline

## Product outcome

The primary user task is to identify offline, stale, quality-degraded or incompletely observed Devices quickly, understand their current operating evidence and open a stable Device detail link.

Browsing the Registry hierarchy remains available, but it is supporting context rather than the page's primary purpose.

## Information architecture

The default desktop structure is:

```text
Site operating summary
├── compact attention counts
├── asset hierarchy tree
└── Device operating table
    └── URL-backed Device detail drawer
```

The Site summary must remain compact. It may show counts such as total Devices, attention Devices, offline Devices and stale Devices, but it must not become a second dashboard or repeat the table content.

The initial supported hierarchy is:

```text
Site
→ Building or Area
→ Equipment
→ Device
```

The first version does not implement an arbitrary-depth generic graph editor. Missing optional Building or Area levels may be collapsed without inventing objects.

## Default list behavior

The default list shows **Devices requiring attention**, with an explicit control to switch to all Devices.

The default attention set is derived only from authoritative current-state evidence:

- Presence is offline;
- telemetry Freshness is stale;
- current telemetry Quality is suspect or invalid;
- one or more configured critical telemetry points have no usable current observation.

Business thresholds such as temperature, pressure, COP or process deviation do not define attention in this module. Those belong to future Alarm or FDD domains.

## Operating status model

The list exposes one deterministic operational projection while the detail view preserves the underlying dimensions separately.

The projection precedence is:

```text
UNKNOWN
    authoritative identity or current-state dimensions cannot be established

OFFLINE
    authoritative Presence is offline

ATTENTION
    Presence is online, but Freshness is stale,
    Quality is suspect or invalid,
    or a configured critical point has no usable observation

NORMAL
    Presence is online and all applicable current-state checks pass
```

A service-level failure must not be converted into Device `UNKNOWN` rows when the Device collection itself cannot be established. The page instead renders a route or data-source unavailable state.

The Device detail surface displays Presence, Freshness and Quality independently, including their timestamps or revisions where supplied by the public contract.

## Critical telemetry configuration

The preferred long-term owner for Device-type critical telemetry metadata is Registry or another versioned platform metadata boundary.

Until that contract exists, the first version may use a repository-owned, versioned Device-type configuration that declares:

- applicable Device type;
- telemetry key;
- display label;
- unit;
- preferred ordering;
- whether the point is critical for completeness;
- optional formatting metadata.

This configuration is presentation and completeness policy. It is not a source of observed values, safety thresholds, Alarm rules, FDD rules or physical control semantics.

Unknown Device types remain visible and must not be rejected. They show identity, Presence and available generic current-state metadata, with an explicit indication that no curated critical-point profile is configured.

## Data acquisition and realtime strategy

### Device list

- Registry supplies the authorized Site-scoped inventory and hierarchy.
- Public Snapshot or Batch telemetry supplies list initialization.
- The list does not subscribe to every telemetry key for every Device.
- Only low-cardinality Presence and Freshness state may update continuously where the existing public realtime boundary supports it.
- Business telemetry values remain stable at their latest authoritative snapshot until the Device detail is opened or a defined recovery refresh occurs.

### Device detail

Opening a Device detail establishes exact Device-and-key realtime subscriptions for its configured critical points.

The detail flow is:

1. validate the Device against the current authorized Site inventory;
2. load current Snapshot data;
3. load the selected short historical range;
4. establish bounded realtime subscriptions;
5. reconcile with a fresh Snapshot after reconnect or recovery;
6. cancel requests and subscriptions when the Device, Site, Session or policy generation changes.

The first version supports short history presets:

- 1 hour;
- 6 hours;
- 24 hours.

It does not provide an arbitrary date picker or general historical analytics builder.

## URL and interaction model

The list route remains:

```text
/sites/:siteId/assets
```

A selected Device uses:

```text
/sites/:siteId/assets/:deviceId
```

The visual interaction is a right-side Device drawer, but the Device identity is represented in the URL so refresh, browser history, sharing and future cross-module deep links preserve context.

A URL Device must be validated against the current authenticated Principal, Acting Organization, authorized Site and Registry result. An invalid or unauthorized Device must use a non-enumerating not-visible state.

## Device detail contents

The first version includes:

- Registry identity and classification;
- hierarchy context;
- Presence, Freshness and Quality;
- current configured critical telemetry values;
- 1h, 6h and 24h short trends;
- source timestamps, watermarks or revisions exposed by the public contracts;
- copyable stable identifiers and links;
- manual refresh.

It excludes:

- all raw telemetry keys;
- command creation;
- Alarm, Work Order, FDD and Optimization summaries before those domains exist;
- Device metadata editing;
- CSV or historical export.

## Missing-data semantics

Missing or unusable values are never represented as zero.

The UI distinguishes at least:

- no critical-point profile configured;
- critical point configured but never observed;
- observation exists but is stale;
- observation Quality is suspect;
- observation Quality is invalid;
- telemetry service unavailable;
- Device unavailable or not visible under current authorization.

A valid measured zero remains zero.

## Empty and failure states

The module distinguishes:

- authorized Site has no Registry Devices;
- Registry Devices exist but no current telemetry has been observed;
- some Devices have partial current telemetry;
- all Devices are normal and the default attention filter is empty;
- Device is not visible or does not exist;
- required Capability is missing;
- Registry is unavailable;
- telemetry current-state service is unavailable;
- realtime transport is degraded while Snapshot data remains available;
- selected history is unavailable while current state remains available.

These states must not collapse into a generic `暂无数据` message.

## Authorization boundary

The route requires the server-authored effective capabilities needed for:

- Site access;
- Device read;
- current telemetry read;
- historical telemetry read when a trend is requested.

Exact capability names must reuse existing generated public contracts and server policy rather than creating browser-only aliases.

The browser uses capabilities for navigation and safe state presentation. Platform Gateway, IAM and the owning services remain responsible for exact Organization, Site, Device and telemetry-key authorization.

The browser never supplies a trusted Organization identity independently of the authenticated Principal context.

## Allowed user actions

The first version is read-only. It allows:

- switch between attention and all Devices;
- filter and search within the authorized Device collection;
- expand hierarchy context;
- open and close Device details;
- select short trend ranges;
- refresh;
- copy stable IDs and deep links.

It does not edit Registry records, export data or create Commands.

## Cache and lifecycle isolation

Query keys and protected resources include all applicable dimensions:

- authenticated Session generation;
- Acting Organization;
- Site ID;
- Device ID;
- requested telemetry keys;
- current-state or history operation;
- history range and aggregation;
- relevant dataset or business revision where exposed.

Site, Session, Principal or effective-policy transitions must:

- abort stale requests;
- dispose realtime subscriptions;
- purge protected query entries and temporary Device state;
- close or invalidate a Device drawer whose Device is not visible in the new scope;
- prevent late responses from repopulating the new scope.

## Accessibility and responsive behavior

- The hierarchy, filters, table and drawer have explicit accessible labels.
- Attention state is not conveyed by color alone.
- Drawer opening moves focus to its heading; closing restores focus to the originating Device row.
- URL navigation and browser history preserve focus predictably.
- Keyboard users can search, filter, open a Device and change history range.
- Mobile uses a full-width detail surface rather than an unreadably narrow drawer.
- Reduced-motion preferences are respected.

## Acceptance scale

The first module is certified for an authorized Site fixture containing at least 200 Devices.

This is a product and browser-behavior acceptance scale, not the formal S2 production capacity certification excluded above.

At that scale, verification must demonstrate:

- bounded initial requests;
- no one-subscription-per-key behavior across the whole list;
- responsive filtering and selection;
- stable rendering without continuous full-table telemetry re-renders;
- exact detail subscription cleanup;
- correct Site switching and protected-state purge.

## Required acceptance criteria

The implementation is complete only when:

1. `/sites/:siteId/assets` renders authorized Registry Devices without Demo or Mock fallback.
2. The default view shows attention Devices and can switch to all Devices.
3. Attention classification follows the frozen deterministic state model.
4. Presence, Freshness, Quality and critical-point completeness remain distinguishable.
5. Valid zero measurements remain zero and missing observations remain missing.
6. `/sites/:siteId/assets/:deviceId` opens a URL-backed detail drawer.
7. The detail renders current critical points and 1h, 6h and 24h trends.
8. Realtime subscriptions are limited to the selected Device and configured detail keys.
9. Reconnect performs an authoritative Snapshot reconciliation.
10. Site, Session and policy transitions abort, unsubscribe and purge protected state.
11. Unauthorized or unknown Devices use a non-enumerating not-visible state.
12. Registry-empty, telemetry-empty, partial, stale, unavailable and degraded states are separately observable.
13. The module passes keyboard, focus, screen-reader and responsive checks.
14. A 200-Device browser fixture passes the bounded-request and interaction checks.
15. Existing Real Shell, Registry, telemetry, ownership, security and browser gates remain green.

## Verification expectations

The engineering ticket must include focused automated evidence for:

- Registry Site and Device scope validation;
- capability and non-enumeration behavior;
- attention projection precedence;
- zero versus missing semantics;
- repository critical-point profile behavior, including unknown Device types;
- cache-key isolation;
- request cancellation and late-response suppression;
- Site and Session protected-state purge;
- exact realtime subscribe, reconnect reconciliation and unsubscribe behavior;
- URL-backed drawer navigation and focus restoration;
- independent Registry, current telemetry, realtime and history failure states;
- the 200-Device browser scenario;
- production Real build and bundle boundaries.

# Frozen decisions for later modules

## Real Dashboard

Real Dashboard follows Real Assets.

Its first version aggregates only authoritative facts already owned by Registry, Presence, current Telemetry and Energy Analytics. It does not simulate Alarm, Work Order, FDD or Optimization facts in the browser.

## Complete Energy workspaces

Complete Energy workspaces follow Real Dashboard and precede Real Commands.

They expand the existing Site Energy slice toward year, month, week and day workspaces, URL-preserved analysis context and continuous drill-down. Cost and carbon remain separate until their authoritative domain exists.

## Real Commands

Real Commands supports low-risk Command creation, confirmation, execution-state display and reported-state verification only where the existing S3 server contract and capability mapping permit it.

It excludes high-risk start or stop operations, bulk Device control, scheduling, automation and any capability explicitly deferred by S3.

## Alarm and Work Order domains

Alarm and Work Order are separate domains with distinct lifecycle and ownership models. They may initially be deployed within one modular service, but an Alarm is not a Work Order and neither record is reconstructed from frontend telemetry state.

## FDD and Optimization

FDD is delivered before Optimization.

FDD owns governed diagnostic runs and findings. Optimization consumes authoritative operational and diagnostic evidence to produce reviewable opportunities and later verification, rather than inventing diagnostic truth itself.

## Cost, carbon and Real BigScreen

Cost and carbon are delivered before Real BigScreen.

Real BigScreen may only present savings, cost and carbon values backed by authoritative contracts. Demo narrative data must not be reused as Real business evidence.

## Discussion result

No unresolved product decision remains for opening the Real Assets implementation map and ticket breakdown. Contract inspection may still identify engineering prerequisites, but those findings must be handled as explicit dependency tickets rather than reopening the frozen product boundary without evidence.

## Published implementation map

- [Wayfinder Map: Real Assets 资产运行工作台交付](https://github.com/SwayingWindmill/HVAC_web/issues/128)
- [Real Assets 01: 核对公共契约与关键点位配置边界](https://github.com/SwayingWindmill/HVAC_web/issues/129) — initial frontier
- [Real Assets 02: 交付 Site 设备运行列表](https://github.com/SwayingWindmill/HVAC_web/issues/130) — blocked by contract readiness
- [Real Assets 03: 交付 URL 化设备详情当前状态](https://github.com/SwayingWindmill/HVAC_web/issues/131) — blocked by the Site list
- [Real Assets 04: 增加设备关键点位短趋势](https://github.com/SwayingWindmill/HVAC_web/issues/132) — blocked by Device details
- [Real Assets 05: 增加精确实时订阅与恢复](https://github.com/SwayingWindmill/HVAC_web/issues/133) — blocked by Device details and may run in parallel with short history
- [Real Assets 06: 完成 200 设备浏览器认证与发布门禁](https://github.com/SwayingWindmill/HVAC_web/issues/134) — blocked by short history and realtime recovery

GitHub native sub-issue and blocked-by relationships are the canonical execution graph. The first open, unassigned and unblocked child is the implementation frontier.
