# CodeGraph

CodeGraph provides a local code knowledge graph for structural search, call paths and change-impact analysis.

## Setup

```bash
npm ci
npm run codegraph:init
```

Restart the MCP-capable agent after installation. The project-level MCP configuration launches the pinned local package and disables anonymous telemetry.

## Commands

```bash
npm run codegraph:status
npm run codegraph:sync
npx --no-install codegraph explore "how does command submission reach PostgreSQL?"
npx --no-install codegraph affected path/to/changed-file.ts
```

The index is stored under `.codegraph/` and must not be committed. `codegraph.json` excludes worktrees, scratch data, generated output, prototypes, references and the frozen Legacy backend so results describe the active platform.
