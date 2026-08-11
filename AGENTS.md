- Do not preserve backward compatibility. Remove obsolete paths instead of
  adding compatibility layers, fallbacks, or migrations.
- Choose the simplest implementation that fully meets the current
  requirements. Avoid speculative abstractions, configuration, and
  indirection.
- Grow the system in layers. Start from the smallest version that works end
  to end, and add each new capability on top of a product that already
  works. Never trade a working product for unfinished complexity.
- Keep components modular and concerns clearly separated.
- Prefer established, well-maintained libraries when they reduce overall
  complexity or improve reliability. Do not reimplement common
  functionality without a clear reason.
- Lean on the dependencies already in the project before writing your own
  implementation or adding packages. Do not assume a library lacks a
  capability without checking its documentation and types.
- Make architectural decisions for the long term. Do not accept a stopgap
  that only works for now and is meant to be replaced later.
- Execute all project commands from WSL. This includes dependency installation,
  npm/node scripts, builds, tests, lint/typecheck, Go commands, Docker/Compose,
  and local-stack helpers. When the agent itself is running on Windows, invoke
  commands through WSL (for this workspace, `E:\Code\HVAC_web` maps to
  `/mnt/e/Code/HVAC_web`) instead of running project commands directly in
  PowerShell or cmd.exe.
- WSL commands must use the Linux toolchain installed inside WSL. Do not fall
  back to Windows Node/npm/Go/Docker executables exposed through `/mnt/*` PATH
  entries. For Node work, verify `node` and `npm` resolve inside the WSL Linux
  filesystem (for example under `~/.nvm`) before installing or building; if a
  shared `node_modules` was produced by Windows tooling, recreate it from WSL
  before continuing.
