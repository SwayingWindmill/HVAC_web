# Repository tooling governance

Issue: #288

## Purpose

GitHub language statistics must describe product/runtime source rather than repository-local test, audit, benchmark, migration, and deployment tooling. This policy separates those concerns without relabeling or hiding production implementation.

## Source boundaries

- Product/runtime source lives under `apps/`, `cmd/`, `modules/`, `libs/`, intentionally independent `services/`, `infra/`, and the governed contracts/deployment trees. New JavaScript or JSX source is not allowed there; use TypeScript/TSX for web/Node code and the existing owner language for backend/runtime code.
- Repository tooling lives primarily under `scripts/`. New tooling must be TypeScript. Existing `.js`/`.mjs` files are a legacy surface that may shrink or be migrated, but must not silently grow.
- `benchmarks/`, `pocs/`, and `services/operations-agent-service/test/` are ancillary verification/research surfaces. They remain subject to the JavaScript ratchet while being excluded from GitHub Linguist product-language statistics.
- `.scratch/` is not a runtime/tooling ownership boundary. Tracked executable JavaScript is not allowed there; the Phase 1 governance change removes the remaining tracked scratch capture script.

## Machine contract

`scripts/javascript-tooling-baseline.json` records the reviewed legacy JavaScript surface after the first governance tranche. `npm run repo:javascript:check` validates this policy independently, and the existing `npm run repo:check` includes the same checks. The policy enforces:

1. JavaScript-family extensions (`.js`, `.mjs`, `.cjs`, `.jsx`) may exist only in reviewed legacy tooling roots.
2. The tracked JavaScript path-set digest and file count must match the reviewed baseline. Adding or swapping a JavaScript file therefore requires an explicit baseline change in the same review.
3. Total JavaScript bytes and per-root bytes may not exceed the recorded budgets.
4. Product/runtime JavaScript cannot be legalized by adding another legacy root to normal feature code; new Node/browser implementation uses TypeScript/TSX.
5. The required Linguist exclusions must remain present in `.gitattributes`.

The initial #288 ratchet was 256 JavaScript files / 2,890,378 bytes, including 227 files / 2,532,640 bytes under `scripts/`. After S22 updated existing tooling on `main`, the same path set reached 2,895,222 bytes overall / 2,537,484 bytes under `scripts/`. Issue #291 ratchets the latest `main` down to 253 files / 2,862,954 bytes overall and 224 files / 2,505,216 bytes under `scripts/`. These values are ceilings, not targets.

## Linguist policy

`.gitattributes` marks these ancillary trees as non-detectable for GitHub language statistics:

- `scripts/**`
- `benchmarks/**`
- `pocs/**`
- `services/operations-agent-service/test/**`
- `.agents/**`

This changes repository language presentation only. It does not weaken testing, ownership, code review, or the JavaScript ratchet.

## Verification minimalism

- One current invariant has one authoritative gate. Do not keep parallel stage/ticket/snapshot gates that prove the same thing.
- Domain tests protect domain behavior; architecture gates protect architecture contracts; deployment gates protect deployable topology. Do not duplicate the same assertion across all three layers.
- PR classification should select affected capabilities only. Do not run unrelated domains as a precaution.
- Temporary migration/certification gates are deleted or folded into the stable domain task matrix when the migration/certification purpose ends.
- Test count, script count, and gate count are costs, not quality metrics. Prefer deleting redundant checks over adding another umbrella chain.
- No defensive validation is added merely because a refactor changes paths. Trusted owner contracts, schemas, database constraints, and typed module interfaces remain the source of truth.

## Refactoring direction

Do not bulk-rename `.mjs` to `.ts`. Migrate at real duplication seams. PostgreSQL/Docker Compose test orchestration is the first shared seam: S3, S4, and S5 use `scripts/lib/postgres-compose-harness.ts` for process execution, Compose v2 invocation, temporary port allocation, and `psql` handling. Repository tooling uses a deterministic Compose v2/v5 invocation: `docker-compose` on Windows and `docker compose` on Linux/WSL; it does not probe and switch implementations at runtime. Subsequent migrations should reuse and deepen that seam instead of creating another runner framework.
