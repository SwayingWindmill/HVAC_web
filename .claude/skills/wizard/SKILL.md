---
name: wizard
description: "Generate an interactive bash wizard that walks a human through steps only they can perform. Use when provisioning infrastructure, setting up credentials or CI secrets, walking an unfamiliar third-party dashboard, or running a one-off migration or cutover. Don't invoke this for steps the agent can perform itself via CLI, MCP, or Playwright."
---

# Wizard

A **wizard** is a bash script that walks a human, step by step, through a manual procedure that is tedious to do by hand and tedious to re-explain to an AI every time. It opens each URL, says what to click and copy, captures the values, writes them where they belong (`.env`, GitHub Actions secrets/variables via WSL `gh`), confirms at every stage, and shows how many stages are left.

The UX library is fixed in [`template.sh`](template.sh): stage-by-stage progress, confirmation gates, cross-platform URL opening (including WSL `wslview` and Windows `explorer.exe`), hidden secret entry, idempotent `0600` `.env` upserts, `gh secret`/`gh variable` writes, and a closing summary. Scope the procedure and author only the stages below the `STAGES` marker; never hand-edit the library above it.

## HVAC_web guardrails

- **Default to `.scratch/`**: Write ephemeral one-off wizards to `.scratch/<slug>-wizard.sh` so nothing temporary is tracked. Commit to `scripts/` only when the user explicitly asks for a repeatable repo setup script.
- **Secret boundaries (`AGENTS.md` / `pre-tool-guard.ts`)**: Never read, print, copy, or overwrite `deploy/platform/phase1/runtime/**`, `deploy/platform/phase1/environments/*.runtime.env`, or `deploy/platform/phase1/observability/{wecom-webhook-url,smtp.env,smtp-password}`. If a wizard populates protected files, output a script that prompts the human directly on their terminal and never ask them to paste secret values back into chat.
- **Irreversible gates**: Confirm before any production cutover, database reset, or external message trigger (`confirm` helper + explicit user approval).

## Process

### 1. Scope the procedure

Read the repo first instead of asking cold:

- For setup: check `.env.example`, `docker-compose*`, `deploy/platform/phase1/README.md`, and `.github/workflows/*` (every `secrets.*` / `vars.*` reference CI expects). Do not read protected runtime secret files.
- For a migration or cutover: identify the current state, target state, and irreversible actions between them.

Show the user the ordered list of stages and captured values, and confirm before writing.

**Done when:** every stage is named in order, and for each captured value you know (a) where the human gets it, (b) where it writes (`.env`, GitHub secret/variable, or action-only), and (c) whether input must be hidden (`ask_secret`).

### 2. Map each stage's journey

Write the exact path a human follows: URL to open, UI navigation path (e.g. `Dashboard -> Developers -> API keys -> Reveal`), and target variable. Never invent dashboard paths you did not check; ask or verify docs if unsure.

**Done when:** every stage traces to concrete steps a stranger could follow.

### 3. Author the wizard

Copy `template.sh` to the `.scratch/` target path (using LF line endings). Replace the example stage inside `run_wizard` with one `stage` per step in dependency order, using `stage`, `say`/`step`, `open_url`, `ask`/`ask_secret`, `write_env`, `set_secret`/`set_var`, `pause`/`confirm`, and setting `TOTAL_STAGES`.

Hold the template's bar: open the URL before asking for its value, use `ask_secret` for secrets, `write_env` for persisted local values, `set_secret` only for values CI actually reads, and `confirm` before irreversible steps. Keep each stage to one focused task so instructions never scroll off screen.

### 4. Verify and hand off

Run static checks in WSL:

```bash
wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && bash -n <script> && chmod +x <script>"
```

Run `shellcheck <script>` if available. Do not run the wizard interactively yourself because it opens browsers and blocks on terminal input. Trace statically that every value from Step 1 is captured and every `set_secret` name matches CI references, then tell the human the exact command to run it (`wsl -e bash -lc "bash .scratch/<slug>-wizard.sh"`).
