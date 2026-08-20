# Repository tooling governance

Issue: #288

## Purpose

GitHub language statistics must describe product/runtime source rather than repository-local test, audit, benchmark, migration, and deployment tooling. This policy separates those concerns without relabeling or hiding production implementation.

## Source boundaries

- Product/runtime source lives under `apps/`, `libs/`, `services/`, `infra/`, and the governed contracts/deployment trees. New JavaScript or JSX source is not allowed there; use TypeScript/TSX for web/Node code and the existing owner language for backend/runtime code.
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

The initial ratchet after migrating the S3/S4 PostgreSQL runners and repository-governance checks is 256 JavaScript files / 2,890,378 bytes, including 227 files / 2,532,640 bytes under `scripts/`. This is a ceiling, not a target.

## Linguist policy

`.gitattributes` marks these ancillary trees as non-detectable for GitHub language statistics:

- `scripts/**`
- `benchmarks/**`
- `pocs/**`
- `services/operations-agent-service/test/**`
- `.agents/**`

This changes repository language presentation only. It does not weaken testing, ownership, code review, or the JavaScript ratchet.

## Refactoring direction

Do not bulk-rename `.mjs` to `.ts`. Migrate at real duplication seams. The first seam is PostgreSQL/Docker Compose test orchestration: S3 and S4 now share `scripts/lib/postgres-compose-harness.ts` for process execution, Compose detection, temporary port allocation, and `psql` handling. Subsequent migrations should reuse and deepen that seam instead of creating another runner framework.
