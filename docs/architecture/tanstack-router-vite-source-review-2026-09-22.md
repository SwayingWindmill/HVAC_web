# TanStack Router Vite integration source review — 2026-09-22

## Scope

Build-time route generation for `apps/hvac-web`.

## Project baseline reviewed

- `@tanstack/react-router` 1.170.35
- `@tanstack/router-plugin` 1.168.37
- Vite 5.4.x
- The Vite config already registers `tanstackRouter({ target: 'react', autoCodeSplitting: true })` before the React plugin.
- The Web build also ran `@tanstack/router-cli ... generate` before Vite, so route generation was duplicated.

## Upstream sources reviewed

- TanStack Router official “Installation with Vite” documentation: the Vite plugin owns file-route generation during dev/build for supported Vite projects.
- TanStack Router official “Installation with Router CLI” documentation: the CLI is intended for projects that are not using a supported bundler.
- Installed `@tanstack/router-core` 1.171.29 CJS source, `dist/cjs/router.cjs` and `dist/cjs/load-client.cjs`.
- Latest published `@tanstack/router-core` 1.171.32 CJS package source was also inspected. The same development-only circular CJS access remains:
  `router.cjs -> load-client.cjs -> router.cjs`, with `router.cjs` reading `replaceRouteChunk` from the partially initialized module.
- The warning was reproduced specifically by `router-cli` route generation with `node --trace-warnings`; invoking the Vite build directly did not emit it.

## Decision

### ADOPT

Use the already-configured `@tanstack/router-plugin/vite` as the single owner of file-route generation during Vite dev/build.

### REJECT

Do not run `@tanstack/router-cli` before the Vite build. It duplicates the supported-bundler integration and is the path that triggers the Node circular-require warning.

### REMOVE

- `@tanstack/router-cli` from root and Web devDependencies.
- Root and Web `routes:generate` scripts.
- The `npm run routes:generate` step from the Web build script.

## Acceptance

The Vite plugin must regenerate `src/routeTree.gen.ts` from file routes during build, and production/review builds, typecheck, routing tests and browser acceptance must remain green.
