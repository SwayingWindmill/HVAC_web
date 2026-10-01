# Repository scripts

`scripts/` is the repository orchestration surface, not a product/runtime package. Existing script names are intentionally stable because CI, package scripts, release evidence, and deployment certification reference them directly.

Use these placement rules for new work:

- `scripts/lib/`: reusable script-only helpers with no standalone workflow entry.
- `scripts/fixtures/`: bounded test and browser-audit fixtures.
- `check-*.{mjs,ts}`: static or contract checks that fail closed.
- `test-*.{mjs,ts}`: deterministic Node test entrypoints.
- `run-*.{mjs,ts}`: bounded integration, browser, migration, certification, or operational runners.
- `generate-*`: generated-contract or generated-config producers.
- `build-*`, `assemble-*`, `render-*`, `verify-*`: release/evidence production and verification.

Do not add ad-hoc screenshots, browser profiles, downloaded toolchains, temporary PowerShell scripts, or one-off logs here or at repository root. Put disposable local work under an ignored scratch location and run `npm run repo:clean:local` after browser/UI investigations.

The directory is still intentionally flat for existing ticket/release scripts. Do not bulk-move established entries merely for aesthetics: those paths are referenced by package scripts, CI, deployment assets, and historical certification evidence. Reorganize a family only when its callers are migrated in the same change.
