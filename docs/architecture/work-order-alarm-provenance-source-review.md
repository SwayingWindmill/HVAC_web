# Work Order authoritative Alarm provenance source review

Status: REVIEWED
Issue: #342
Parent specification: #331
Reviewed: 2026-08-29

## Scope

This review covers only the ALARM-origin Work Order provenance and the formal Alarm/FDD/Work Order association seams required by #342. It does not change Alarm evaluation ownership, FDD detection ownership, Work Order lifecycle semantics, simulator behavior, or introduce new FDD/SIMULATOR Work Order source domains.

The accepted local owner boundaries are: Alarm owns Alarm existence and tenant/site scope, Work Order owns Work Order source references and lifecycle, and FDD owns Finding facts plus explicit cross-bounded-context Alarm/Work Order IDs. Cross-owner links are resolved through owner contracts rather than direct database reads.

## ThingsBoard CE

Pinned baseline: ThingsBoard CE v4.3.1.1, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

Relevant official source families reviewed for this seam:

- `application/src/main/java/org/thingsboard/server/controller/AlarmController.java`
- `dao/src/main/resources/sql/schema-entities.sql`
- `dao/src/main/resources/sql/schema-entities-idx.sql`

Decisions:

- **ADOPT** — a supplied Alarm identifier must resolve to a real Alarm before an association is accepted; syntactic identifier validity is not provenance.
- **ADOPT** — Alarm visibility/association is tenant-owned and authorization-aware. An unknown or out-of-scope Alarm is not accepted as a valid source merely because its ID is well formed.
- **ADAPT** — HVAC strengthens the scope boundary to the existing canonical tenant + site tuple. Platform Gateway uses the existing signed `alarm:resolve` owner contract and requires the resolved Alarm Site to equal the Work Order/FDD Site.
- **ADAPT** — ThingsBoard can maintain Alarm relations inside its own persistence model. HVAC keeps Alarm, Work Order and FDD in separate bounded contexts, so cross-owner IDs are explicit references validated through owner APIs instead of adding cross-database foreign keys.
- **REJECT** — UUID-shape-only source validation, browser-authored aliases, direct Work Order/FDD reads of the Alarm database, or a compatibility fallback when Alarm resolution is unavailable.

## OpenEMS

Implementation checkpoint from #331: OpenEMS `develop` commit `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6`.

Relevant official source family reviewed includes the Edge alarm/controller bundles and existing OpenEMS source review material, including `io.openems.edge.controller.io.alarm` usage in Edge application composition.

Decisions:

- **ADOPT** — alarm/fault behavior remains owned by the component that knows the authoritative equipment/runtime state; downstream maintenance behavior should consume that fact rather than invent it.
- **REJECT** — OpenEMS does not expose a directly comparable Work Order provenance owner seam for this ticket. Copying its OSGi/controller composition into the Work Order or FDD bounded context would increase coupling without improving provenance.
- **REJECT** — no OpenEMS-inspired fallback is added when an Alarm or Work Order owner cannot resolve a requested link target.

## MyEMS

Pinned baseline: MyEMS v6.7.0, commit `be6e6ce8ddeac57afb04bddb9621501fb555cab0`.

The reviewed community source separates acquisition/analysis products, but the community v6.7.0 source does not provide a directly reviewable equivalent implementation of the authoritative Work Order provenance flow required here.

Decisions:

- **ADOPT** — analytical/FDD facts and maintenance records remain separate domain products with explicit identifiers rather than collapsing FDD into a Work Order source type.
- **REJECT** — implementation behavior is not inferred from product/enterprise feature descriptions where equivalent community source is unavailable.
- **REJECT** — FDD, simulator state, or repaired/derived data is not introduced as a substitute Work Order origin for an Alarm-driven maintenance flow.

## #342 implementation consequence

The accepted ALARM-origin Work Order chain is:

```text
Existing Alarm UI
    -> Platform Gateway Work Order create authorization
    -> authoritative Site resolution
    -> Alarm owner `alarm:resolve`
    -> exact tenant/site match
    -> Work Order owner persists sourceDomain=ALARM + sourceRef=alarmId
```

The accepted formal FDD association chain is:

```text
FDD link request with required alarmId + workOrderId
    -> Platform Gateway Site authorization + CSRF
    -> Alarm owner resolve for alarmId
    -> Work Order IAM authorization + owner detail read for workOrderId
    -> exact tenant/site match and Work Order ALARM/ORIGIN match to that same alarmId
    -> FDD owner persists explicit alarmId/workOrderId
```

Reverse associations remain owner-owned: Work Order list supports the closed `sourceDomain + sourceRef` filter, while FDD list supports closed `alarmId` / `workOrderId` filters. No Alarm row is mutated to duplicate Work Order state.

Alarm physical recovery has no Work Order lifecycle side effect. Clearing an Alarm or later clearing an FDD condition does not invoke Work Order transition APIs; completion remains an explicit Work Order owner lifecycle mutation with its existing evidence requirements.

`EQUIPMENT` remains a canonical Work Order source domain distinct from `ASSET` in the Go model, database constraint, public OpenAPI contract and frontend schema. No alias or compatibility mapping is introduced.
