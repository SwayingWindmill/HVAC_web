# 04 — Legacy mapping, backfill and quarantine

**What to build:** implement the S1 migration support owned by the Core migration boundary: LegacyResourceMap, MigrationProvenance, MigrationQuarantine, deterministic snapshot backfill and side-effect-free Shadow Compare. Import one approved internal Organization/Site cohort without treating Legacy Asset as Equipment by default or performing ThingsBoard synchronization from a user read.

**Blocked by:** 01 — Contract, domain model and ownership baseline.

**Status:** ready-after-spec-approval

- [ ] Legacy Customer/Site/Asset/Device identifiers map to platform resources through explicit versioned records rather than public IDs.
- [ ] Mapping records contain source system/table/key, target resource type/id, owning Organization, transformation version, batch, source watermark, row hash and timestamps.
- [ ] Mapping states implement `DISCOVERED`, `MAPPED`, `VERIFIED`, `QUARANTINED` and `RETIRED` with controlled transitions.
- [ ] Legacy Asset is never automatically promoted to Equipment; Legacy Device is never automatically treated as both Device and Equipment.
- [ ] Duplicate external keys, missing owners, ambiguous type conversion, hash mismatch and conflicting mappings enter Quarantine.
- [ ] Quarantined records are visible only through a restricted operator surface and never appear in ordinary Registry lists/details.
- [ ] Snapshot backfill is keyset-paged, checkpointed, idempotent and safe to resume after process or database failure.
- [ ] Re-running the same batch produces no duplicate Core resource, ExternalBinding or provenance row.
- [ ] The approved internal cohort can be rebuilt from deterministic fixtures and, when authorized, from controlled Legacy source reads.
- [ ] No Registry HTTP request invokes `sync=true`, direct ThingsBoard APIs, a full device scan or an on-demand migration job.
- [ ] Shadow Compare runs asynchronously with `side_effect_policy=NONE`, one authoritative user response and bounded timeout/resource use.
- [ ] Shadow comparison covers authorization outcome, canonical resource identity, ordering, fields, mapping state and response hash.
- [ ] Cross-tenant or unexplained authorization differences are hard migration failures rather than tolerated numeric differences.
- [ ] Legacy outage, Shadow worker crash and malformed Legacy payload do not delay or alter an authorized Core response.
- [ ] Quarantine age, backfill watermark, mapping counts, shadow differences and source/target hashes are observable and included in release evidence.
- [ ] Migration data has explicit retention, access control, encryption and cleanup ownership.
