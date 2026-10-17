# Semantic catalog generator: source review (2026-10-17, #573)

The Operations Agent's semantic query Tool may only name the members of the Cube views, and the
query service must check the same list. The list therefore has to be derived from the Cube model
(`semantic/cube/model/**/*.yml`), not kept by hand.

## Options

- **Read Cube's `/v1/meta` at run time.** Rejected: the Agent builds its Tool schema at start-up
  without a Cube token, the query service would depend on Cube being up to validate a request, and
  a model change would reach the Agent unreviewed.
- **Hand-written member list in Go and TypeScript.** Rejected: two copies that drift from the model.
- **Generate both from the YAML model at build time.** Adopted: `scripts/generate-semantic-catalog.mjs`
  writes `libs/analyticsmodel/semantic_catalog.json` (embedded in Go) and
  `semantic-catalog.gen.ts`; `npm run semantic:catalog:check` fails when they differ from the model.

## Dependency

- **ADOPT `js-yaml` 4.3.2** (pinned as a root dev dependency; it was already in the lockfile as a
  transitive dependency). The Cube model is plain YAML without Cube's Jinja/Python templating, so a
  YAML 1.2 parser is enough; `yaml.load` with the default schema reads it, and the generator
  resolves `extends` and view `includes` itself.
- **REJECT** loading Cube's schema compiler: it pulls the whole Cube backend into a build script for
  what is a member listing.
