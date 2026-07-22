# Legacy Registry migration service

`legacy-registry-migrator` is the S1 offline migration executor for normalized Legacy Registry snapshots. It writes only through the restricted `s1_migration_operator` role and never connects directly to the Legacy application database.

## Input contract

Input is newline-delimited JSON. Records are normalized before execution and sorted in dependency order: Organization, Site, Equipment, Device.

```json
{"kind":"ORGANIZATION","sourceSystem":"legacy-hvac-backend","sourceTable":"organization","sourceKey":"legacy-org-42","sourceWatermark":"2026-07-22T00:00:00Z","sourceRowHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transformationVersion":"s1-v1","batchId":"registry-2026-07-22","code":"north-campus","displayName":"North Campus","status":"ACTIVE"}
```

Site records require `organizationRef`. Equipment and Device records require both `organizationRef` and `siteRef`. Equipment migrated from a Legacy `asset` table additionally requires:

```json
{"relationEvidence":{"verifiedEquipmentRelation":true}}
```

The input parser rejects unknown fields, oversized records, non-SHA-256 hashes, NUL bytes, excessive metadata nesting and metadata keys that could carry credentials.

## Apply a snapshot

The database connection must use the dedicated `s1_legacy_migration_service` login. The process activates `s1_migration_operator` inside each transaction and verifies the active role before writing.

```bash
legacy-registry-migrator apply \
  --dsn "$S1_LEGACY_MIGRATION_DSN" \
  --input normalized-registry.jsonl
```

Records are locked by `(sourceSystem, sourceTable, sourceKey)`. An identical replay records `SKIPPED` provenance. A changed source hash for verified truth is quarantined instead of overwriting the existing business row.

## Resolve quarantine

Resolution requires an open quarantine UUIDv7 and exactly one corrected normalized record with the same source identity.

```bash
legacy-registry-migrator resolve \
  --dsn "$S1_LEGACY_MIGRATION_DSN" \
  --quarantine-id 01900000-0000-7000-8000-000000000001 \
  --action apply \
  --input corrected-record.jsonl
```

Use `--action retire` to create a retired target and close the source mapping. Both actions are idempotent and append immutable provenance.

## Guarantees

- Public target IDs are owner-generated UUIDv7 values; Legacy and ThingsBoard IDs stay internal.
- Every record uses a bounded serializable transaction and transaction-scoped advisory lock.
- Mapping states remain `DISCOVERED`, `MAPPED`, `VERIFIED`, `QUARANTINED`, or `RETIRED`.
- Quarantined mappings have no target resource ID and are invisible to ordinary Core runtime reads.
- The migration login cannot read IAM data or Core business tables before activating the operator role.
- Runtime Registry reads never invoke this executable and never synchronize or double-write business data.
