# Device-first Real Assets source review

Status: DECIDED / SOURCE-BACKED
Review date: 2026-08-31
Parent spec: #366 `Device-first Real Assets operations workspace`
Implementation ticket: #367 `Freeze the source-backed Device-first Assets contract`

## 1. Scope

This record freezes the source-level evidence for redesigning the Real Mode `资产与设备` workspace. It does not change Registry, Telemetry, Command, Alarm, Work Order, Energy or deployment ownership. It exists to satisfy the repository source-first rule before changing the current Assets runtime implementation.

The redesign is limited to these product boundaries:

- the operational object hierarchy shown in the Assets workspace;
- the Device runtime presentation model;
- Device/Asset list and detail information architecture;
- typed Asset/Device detail routing;
- the test seams that protect those contracts.

The broader three-workspace decision in [`ui-workspace-information-architecture-v1.md`](./ui-workspace-information-architecture-v1.md) remains authoritative. This record supersedes only its earlier internal `资产与设备` decision that treated Asset, Device Endpoint and Point as peer ledger modes.

## 2. Local source reviewed

The existing implementation was re-read before this decision:

- [`apps/hvac-web/src/real/assets/model.ts`](../../apps/hvac-web/src/real/assets/model.ts)
- [`apps/hvac-web/src/real/assets/catalog.ts`](../../apps/hvac-web/src/real/assets/catalog.ts)
- [`apps/hvac-web/src/real/assets/data.ts`](../../apps/hvac-web/src/real/assets/data.ts)
- [`apps/hvac-web/src/real/assets/realtime.ts`](../../apps/hvac-web/src/real/assets/realtime.ts)
- [`apps/hvac-web/src/real/assets/history.ts`](../../apps/hvac-web/src/real/assets/history.ts)
- [`apps/hvac-web/src/real/assets/RealAssetsWorkspace.tsx`](../../apps/hvac-web/src/real/assets/RealAssetsWorkspace.tsx)
- [`apps/hvac-web/src/real/assets/AssetDetailDrawer.tsx`](../../apps/hvac-web/src/real/assets/AssetDetailDrawer.tsx)
- [`apps/hvac-web/src/real/assets/detail.ts`](../../apps/hvac-web/src/real/assets/detail.ts)
- [`apps/hvac-web/src/real/site-routing.ts`](../../apps/hvac-web/src/real/site-routing.ts)
- [`apps/hvac-web/src/real/SiteScopedShell.tsx`](../../apps/hvac-web/src/real/SiteScopedShell.tsx)
- [`CONTEXT.md`](../../CONTEXT.md), especially Device, Physical Sensor, Point, Registry Lifecycle, Presence Applicability, Device Presence, Evaluation Availability, Telemetry Freshness, Telemetry Quality and Telemetry Readiness.

### 2.1 Local conflicts found

The current implementation has no incumbency preference under `AGENTS.md`. The following conflicts are material and must be changed:

1. `projectRealAssetsOperatingState()` collapses owner availability, Presence, presentation-profile configuration, freshness, quality and readiness into `UNKNOWN/OFFLINE/ATTENTION/NORMAL`.
2. `isRealAssetsAttentionState()` treats every non-`NORMAL` state as attention, so `Presence UNKNOWN` alone makes otherwise current telemetry appear operationally abnormal.
3. An unconfigured frontend Device profile produces `POINT_CATALOG_UNCONFIGURED` and prevents the Device from reaching the old `NORMAL` state even when Registry Points and S2 current values are valid.
4. `buildRealAssetsHierarchy()` projects Site → Space → Asset → Device → Sensor → Point, plus `virtual-sensor`, directly into the Operations navigation tree.
5. `RealAssetsWorkspace` exposes Asset, Device and Point as peer ledger modes and automatically switches the ledger mode when the tree selection changes.
6. `openDeviceDetail()` does not open the Device. It resolves the first Asset binding and opens the Asset detail, so an unbound Device has no first-class detail path.
7. `AssetDetailDrawer` combines Asset identity, Device realtime/history, Sensors, Points and controls in one long Asset-centric surface.
8. `detail.ts` and `site-routing.ts` only model an untyped single Assets detail ID as `assetId`, so Device is not a first-class routable detail target.

The existing owner boundaries are not conflicts and are retained: `data.ts` reads the Registry model and S2 current state separately; protected Site scope, route-policy revision, current batching, realtime recovery and history remain independent contracts.

## 3. Mandatory upstream references

### 3.1 ThingsBoard CE

Pinned baseline: `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

Re-read directly from the pinned commit:

- [`ui-ngx/src/app/modules/home/components/entity/entities-table.component.html`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/modules/home/components/entity/entities-table.component.html)
- [`ui-ngx/src/app/modules/home/models/entity/entities-table-config.models.ts`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/modules/home/models/entity/entities-table-config.models.ts)
- [`ui-ngx/src/app/core/api/entity-data-subscription.ts`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/core/api/entity-data-subscription.ts)
- Existing implementation evidence in [`thingsboard-source-review.md`](./thingsboard-source-review.md), especially the separate Asset/Device identity decision and the S18 entity-table configuration review.

Source-level findings:

- The entity table is the stable primary surface. A right-side `mat-drawer` contains an entity details panel while the list remains the main context.
- `EntityTableConfig` owns list columns, fetch behavior and header/group/cell actions separately from the table composition. The page does not need one component to own every table behavior.
- `EntityDataSubscription` owns latest/history/timeseries/reconnect data lifecycle separately from table/widget composition.
- ThingsBoard models Asset and Device as distinct first-class entities rather than requiring every Device interaction to be mediated through an Asset.

Decision:

- `ADOPT`: separate first-class Asset and Device identities in Operations presentation.
- `ADOPT`: stable object list plus contextual details as the primary desktop interaction pattern.
- `ADOPT`: keep data/subscription lifecycle outside list/detail composition.
- `ADAPT`: HVAC uses its typed Tenant → Site → Space → Asset/Device Registry model and owner contracts instead of ThingsBoard generic relations/customer scope.
- `REJECT`: a generic configurable dashboard/entity runtime for this page; the local problem is narrower and already has explicit Registry/S2 contracts.

### 3.2 OpenEMS

Pinned baseline: release `2026.7.0`, commit `2e2792d59fc5ba3b99ce3cf98d15081c0a74895e`.

Re-read directly from the pinned commit:

- [`ui/src/app/app-routing.module.ts`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/ui/src/app/app-routing.module.ts)
- [`ui/src/app/shared/components/navigation/service/navigation.service.ts`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/ui/src/app/shared/components/navigation/service/navigation.service.ts)
- Existing Component/Channel source evidence in [`openems-source-review.md`](./openems-source-review.md), especially Review 003.

Source-level findings:

- `/overview` is separated from `/device/:edgeId`; the Device/Edge becomes an addressable navigation context with its own live/history/settings descendants.
- `NavigationService` derives a NavigationTree from the current Edge/config and uses left-side navigation on desktop and bottom navigation on smartphone. Navigation is contextual rather than a dump of every internal datum.
- OpenEMS Component owns Channels. Channel metadata/value is a capability of a Component; Review 003 confirms Component self-description includes its owned Channel IDs.

Decision:

- `ADOPT`: an addressable Device-level operational object can own deeper runtime/configuration content.
- `ADAPT`: use a lightweight Site/Space/Asset/Device operational tree; Sensor/Point remain Registry/runtime components inside Device/Asset detail rather than global Operations navigation objects.
- `ADAPT`: preserve HVAC Device → Point canonical identity and optional Physical Sensor traceability rather than mapping OpenEMS Edge/Component/Channel names directly into the Web UI.
- `REJECT`: exposing every Point/Channel as a peer primary navigation node.
- `REJECT`: importing OpenEMS Controller/Cycle/Edge configuration navigation into the cloud Operations workspace.

### 3.3 MyEMS

Pinned baseline: commit `be6e6ce8ddeac57afb04bddb9621501fb555cab0`.

Re-read directly from the pinned commit:

- [`myems-web/src/routes.js`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-web/src/routes.js)
- Existing MyEMS source evidence in [`myems-source-review.md`](./myems-source-review.md) and the three-workspace comparison record.

Source-level findings:

- MyEMS keeps Space, Equipment, Meter, Tenant, Store, Shopfloor and Combined Equipment as distinct user-facing resource families.
- Its Web routes replicate large energy/carbon/cost/load/saving/plan/prediction/comparison page families for many object types.

Decision:

- `ADOPT`: distinct domain resources should remain distinguishable instead of forcing Device and Asset into one UI abstraction.
- `ADAPT`: keep Device and Asset as the two Operations objects that the current backend can authoritatively serve; Point remains a child capability/data identity.
- `REJECT`: copying the per-object page matrix. HVAC uses one bounded Assets workspace plus typed details and existing owner APIs.

## 4. Frozen Device runtime presentation semantics

The Assets workspace must use the canonical domain vocabulary in `CONTEXT.md` and keep these dimensions independent:

| Dimension | Source / values | UI contract |
| --- | --- | --- |
| Registry Lifecycle | Registry `ACTIVE / INACTIVE / RETIRED` | Configuration/lifecycle state only. Never means online/offline or telemetry freshness. |
| Presence Applicability | `APPLICABLE / NOT_APPLICABLE` | `NOT_APPLICABLE` is a valid explicit state and is not `UNKNOWN`. |
| Device Presence | `ONLINE / OFFLINE / UNKNOWN` when applicable | Connection knowledge only. `UNKNOWN` does not by itself degrade otherwise current telemetry. |
| Evaluation Availability | `AVAILABLE / UNAVAILABLE` | Platform observation/evaluation availability. `UNAVAILABLE` is not evidence that the Device is offline. |
| Telemetry Readiness | canonical readiness returned by S2 | Required-set completeness/fitness for the view. It remains independent of Presence. |
| Telemetry Freshness | `FRESH / STALE / MISSING` per Point, summarized without fabricating data | Temporal currency. `STALE` is not the same as `OFFLINE`; `MISSING` is not zero. |
| Telemetry Quality | S2 quality values such as `GOOD`, `PARTIAL`, `ESTIMATED`, `MANUAL`, `STALE`, `INVALID` | Evidence quality. A fresh but degraded-quality observation must remain distinguishable from a stale observation. |

There is deliberately no replacement `overallState`. UI summaries and filters may derive bounded selectors from these facts, but the underlying dimensions remain visible and queryable.

The `需关注` filter may include concrete actionable runtime/data conditions such as `OFFLINE`, stale data, incomplete/missing required data or degraded quality. It must not mean “anything that is not NORMAL”, and `Presence UNKNOWN` or `NOT_APPLICABLE` alone must not become an attention condition.

## 5. Frozen Assets information architecture

### 5.1 Primary page

```text
资产与设备

Device-oriented operational summary

┌──────────────────────┬────────────────────────────────────────────┐
│ operational context  │ [设备] [资产]                             │
│ Site                 │ search / connection / data / type filters │
│ └─ Space             │                                            │
│    ├─ Asset           │ primary object list                        │
│    │  └─ Device      │                                            │
│    └─ Device          │                                            │
└──────────────────────┴────────────────────────────────────────────┘
```

Rules:

- The operational navigation tree stops at Device. It contains no Sensor, Point or `virtual-sensor` nodes.
- Tree selection changes context/filter only; it does not silently switch the active object mode.
- Main object modes are exactly `设备` and `资产`; `设备` is the default.
- There is no peer Point ledger in Operations. Authorized Points are fully available inside Device/Asset detail where they have semantic context.
- Device rows are compact and show identity/location, connection, telemetry condition, a bounded representative-value preview and Registry lifecycle.
- Device presentation profiles may improve labels/order/preferred preview/trend selection. They are optional. An unprofiled Device still uses Registry Points + S2 current data for its generic view.
- Asset rows summarize the maintainable Asset and its bound Devices/runtime/control capability without exposing every internal topology count as a peer workspace concept.
- The two current metric strips are replaced by one Device-oriented operational summary. Registry inventory totals remain available in the navigation/detail/Administration contexts that own them.

### 5.2 Typed details

The target detail routes are:

```text
/sites/:siteId/assets/device/:deviceId
/sites/:siteId/assets/asset/:assetId
```

Device detail is a first-class object and does not resolve through a first Asset binding. It must work for bound and unbound Devices.

Device detail owns:

- overview: identity, type/code, location/bindings, independent connection/telemetry/lifecycle facts;
- current data: all authorized Registry Points with current value, unit, quality, freshness and sample time;
- trends: existing Device history owner;
- connection: Presence plus realtime transport continuity/recovery evidence without conflating the two;
- configuration: Registry revision/status and Point acquisition metadata.

Asset detail owns:

- overview and Asset identity/location;
- runtime summary derived from bound Devices without creating another Device telemetry authority;
- bound Devices and internal components;
- governed Asset controls from existing COMMAND/CONTROLS bindings.

A shared detail shell may own open/close/refresh/focus/mobile behavior. Device and Asset do not share one monolithic content component.

After all callers migrate, the old untyped `/sites/:siteId/assets/:id` detail path is removed rather than kept as compatibility behavior.

## 6. Owner and security boundaries retained

The UI refactor must not create new data authority:

- Registry remains owner of Site/Space/Asset/Device/Sensor/Point identity, relationships, lifecycle and revision.
- S2 remains owner of Device Current, Presence/Evaluation/Readiness, Point freshness/quality and observation truth.
- Current batch reads continue to use all authorized Registry telemetry Points; presentation profiles do not narrow authoritative acquisition.
- History and realtime remain separate owner paths and separate failure domains from Current.
- Site protected scope continues to cancel/purge requests, cache, realtime sessions and selections on scope change.
- Route-policy revision changes continue to invalidate the affected protected runtime state.
- Non-visible or unauthorized typed detail IDs retain non-enumerating behavior.
- Real Mode never substitutes history, cached values, zero or mock values for unavailable Current facts.

## 7. Testing seams

The implementation uses TDD through three stable public seams:

1. **Operational projection seam** — authoritative Device Snapshot + Registry Device/Points become independent connection/readiness/freshness/quality/lifecycle presentation facts. Tests protect the changed business semantics, not helper structure.
2. **Typed Assets detail routing seam** — `resolveSiteRouting`/Assets detail parsing and resolution prove exact Device/Asset deep links, browser-addressable identity and non-enumeration.
3. **Existing Real Assets browser certification/audit seam** — end-to-end list/search/navigation/detail/current/history/realtime/focus/mobile behavior. The existing certification is updated; no second permanent gate is introduced.

Tests for obsolete Point ledger structure, Sensor/Point Operations tree nodes, the old single operating state and the old untyped detail route are deleted when those contracts are removed rather than kept through compatibility code.

## 8. Final ADOPT / ADAPT / REJECT summary

### ADOPT

- Asset and Device as separate first-class operational identities.
- Stable primary object list with contextual details.
- Data/subscription lifecycle outside page/list composition.
- Device-level addressability with deeper runtime/configuration content.
- Contextual tree navigation rather than a global dump of low-level telemetry identities.

### ADAPT

- Tree is HVAC Site/Space/Asset/Device, not ThingsBoard generic relations or OpenEMS Edge navigation.
- Point remains the canonical Device-owned data/control identity, but appears inside Device/Asset detail in Operations.
- Physical Sensor is retained only where Registry traceability requires it, not as a global navigation object.
- Existing Registry + S2 + protected-scope architecture remains the authority underneath the new presentation.
- Presentation profiles become optional enhancement over the generic Registry/S2 projection.

### REJECT

- Catch-all `UNKNOWN/OFFLINE/ATTENTION/NORMAL` as the Device runtime truth model.
- `state !== NORMAL` as attention semantics.
- Frontend profile presence as a prerequisite for basic Device usability.
- Sensor/Point/virtual-sensor primary Operations navigation.
- Asset/Device/Point peer ledger modes.
- Device click → first Asset binding → Asset detail.
- A single Asset-centric detail component for all Device/Asset content.
- MyEMS-style duplication of a full page family per resource.
- New compatibility routes, generic dashboard runtimes or duplicate certification gates.
