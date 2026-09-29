# Repository Toolchain — Linux / WSL Only

## Decision

The repository toolchain is Linux-authoritative.

On Windows developer machines, project commands run inside WSL 2 against the active checkout stored on the WSL ext4 filesystem. This includes Node/npm, Go, Docker, browser automation, route generation, code generation, type checking, tests, Vite development, preview, and production builds.

Windows Node/npm, Git Bash as a project shell, Windows Go/Docker fallbacks, `.cmd`/`.exe` tool fallbacks, Windows-only CI runners, and Windows browser fallbacks are unsupported. Windows is only the host for WSL/CodexPro and desktop applications that open the WSL filesystem.

## Why

Vite/Rollup and several transitive dependencies use platform-specific optional native packages. Sharing one `node_modules` directory between Windows Node and Linux/WSL Node can leave the checkout with the wrong native optional package set and produce failures that appear unrelated to the actual source code, including:

- Rollup native optional-package resolution failures;
- unstable `rendering chunks` exits;
- inconsistent Node heap/OOM behaviour between otherwise identical commands;
- `.bin` launchers or native artifacts belonging to the other operating system.

The verified working baseline on 2026-09-17 is Linux Node `v24.16.0` under WSL with `@rollup/rollup-linux-x64-gnu`; the same current application completed the Vite production build successfully in this environment.

## Enforced workflow

The package scripts use `scripts/check-web-js-runtime.mjs` as a runtime guard. The guard requires a Linux Node runtime and verifies that Rollup can load its Linux native dependency. Installation is also guarded so Windows npm cannot rewrite the dependency tree.

The active checkout is:

```text
/home/haozhang/code/HVAC_web
```

A Windows desktop application may open it through the WSL filesystem bridge, but project commands execute from Linux. The old `/mnt/e/Code/HVAC_web` checkout is not an active development root and must not be used for npm/build/test work.

Typical Windows-host workflow:

```bash
wsl
cd ~/code/HVAC_web
npm ci
npm run lint
npm run build
npm run dev
```

CI uses Ubuntu runners for every gate, including browser regression. Browser automation uses Linux Chromium/Playwright rather than Windows Chrome or Edge interop.

## Recovery from a mixed dependency tree

If the runtime guard reports Windows native packages or Rollup cannot load its Linux dependency, do not repair individual optional packages. Remove the dependency tree and reinstall it from the ext4 checkout with Linux npm and the lockfile. Do not restore Windows execution branches or move the active dependency tree back to a mounted Windows filesystem.
