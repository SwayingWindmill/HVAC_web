---
name: poteto-help
description: Guides users through pstack setup, /poteto-mode, and picking the right installed skill or playbook in HVAC_web. Type /poteto-help with a question.
disable-model-invocation: true
---

# Poteto help

Answer the user's question about `pstack` in `HVAC_web`, hand them a prompt they can send, and link the local file under `.agents/skills/` that the answer came from. For a pure help question, do not start the task itself; let the user send the prompt.

A message asking for real work (e.g. "use poteto-mode to fix this bug") is not a help question: read [`poteto-mode`](../poteto-mode/SKILL.md) and execute the matching playbook.

This file maps questions to the local skills and playbooks in `.agents/skills/`. Read the file you route to before quoting it, and trust it when it disagrees with this map.

## Find out what they need

Infer the need from the message. If unclear, ask one multiple-choice question with `ask_question` using these options, then answer only the section they pick:

- Get set up
- Start a task with `poteto-mode`
- Pick a skill for a situation
- Fix a run that went wrong
- Make `pstack` my own

Check repository/user state and mention it only when relevant:

- Absence of `~/.gemini/config/pstack-models.md` (or `.claude/pstack-models.md` / `~/.claude/pstack-models.md` in Claude Code) means `/setup-pstack` has not run, so Antigravity defaults every subagent to `Model: "inherit"`.
- `HVAC_web` already provides project verification skills: [`verify`](../verify/SKILL.md) (1440px UI Playwright / live stack verification) and [`command-safety`](../command-safety/SKILL.md) (`npm run command:formal` model check). Remind users of [`maintain-verification-skill`](../maintain-verification-skill/SKILL.md) / [`maintain-formal-verification-skill`](../maintain-formal-verification-skill/SKILL.md) when verified code or UI features drift.

## Get set up

All `pstack` skills in `HVAC_web` are already checked into `.agents/skills/` (mirrored byte-for-byte to `.claude/skills/` by `npm run repo:check`):

1. By default, no model override sheet is required — Antigravity runs subagents with `Model: "inherit"`. Run [`/setup-pstack`](../setup-pstack/SKILL.md) only if you want per-role model overrides (such as `flash` or `pro` in Antigravity, or Claude slugs in Claude Code).
2. `HVAC_web` does not install an upstream `SessionStart` routing hook; mention `poteto-mode` (or `/poteto-mode` / a specific skill name) when starting non-trivial tasks.
3. Frame your prompt with a goal and a pass/fail verification check, per [`references/prompting.md`](references/prompting.md).

## Start a task with `poteto-mode`

[`poteto-mode`](../poteto-mode/SKILL.md) matches your task to one of 23 playbooks, copies its steps into your checklist (marking unused steps `skip: <reason>`), and orchestrates sibling skills as needed. State the goal and done check rather than hand-coding a skill sequence. Read [`references/prompting.md`](references/prompting.md) before helping word a prompt.

Note that `HVAC_web`'s subagent tool mapping lives in [`agy-tools.md`](../poteto-mode/references/agy-tools.md): use `invoke_subagent` (`TypeName: "self"` for writable/shell work or `"research"` for read-only reading) instead of Cursor's `poteto-agent`.

## Pick a skill (48 skills installed)

The default entry point for non-trivial work is [`poteto-mode`](../poteto-mode/SKILL.md). Name a specific skill directly when you want focused execution:

| The user wants to | Skill |
|---|---|
| Run end-to-end tasks following pstack playbooks | [`poteto-mode`](../poteto-mode/SKILL.md) |
| Check understanding of goal, problem, and smallest change first | [`in-your-own-words`](../in-your-own-words/SKILL.md) |
| Make a prompt deeper (raw sources, methods, outside checks) without running it | [`sharpen`](../sharpen/SKILL.md) |
| Understand how code works or which layer/package owns something | [`how`](../how/SKILL.md) |
| Research why code or a threshold exists (git, docs, ADRs, specs, PRs) | [`why`](../why/SKILL.md) |
| Learn a change or subsystem plainly (`how` + `why` + diagrams) | [`teach`](../teach/SKILL.md) |
| Reconstruct recent context from transcripts, git, and PRs/issues | [`recall`](../recall/SKILL.md) |
| Summarize authored commits in a time window (status update) | [`what-did-i-get-done`](../what-did-i-get-done/SKILL.md) |
| Catch up on this chat in plain words + visuals + waiting calls | [`ketchup`](../ketchup/SKILL.md) |
| Catch up like `/ketchup` in a subtitled slide video (requires `ffmpeg`) | [`ketchup-video`](../ketchup-video/SKILL.md) |
| Hear the last reply restated in plain human language | [`bro`](../bro/SKILL.md) |
| Find what a change could break outside its diff and prove safety | [`blast-radius`](../blast-radius/SKILL.md) |
| Settle types, signatures, and module structure before coding | [`architect`](../architect/SKILL.md) |
| Spawn N parallel attempts and graft the strongest parts | [`arena`](../arena/SKILL.md) |
| Fan out N parallel workers over slices or a race | [`swarm`](../swarm/SKILL.md) |
| Run adversarial multi-angle review proven by live code runs | [`interrogate`](../interrogate/SKILL.md) |
| Fix a bug test-first when a cheap local test exists | [`tdd`](../tdd/SKILL.md) |
| Apply TypeScript rules (`no any`, strict narrowing) to `.ts`/`.tsx` | [`typescript-best-practices`](../typescript-best-practices/SKILL.md) |
| Remove AI-generated code clutter and over-engineering | [`deslop`](../deslop/SKILL.md) |
| Audit/remove ungrounded comments via an independent reviewer | [`no-comments`](../no-comments/SKILL.md) |
| Cut AI tells and fluff from prose and docs | [`unslop`](../unslop/SKILL.md) |
| Write/review technical docs, PRs, commits, or specs (Diataxis + clarity) | [`technical-writing`](../technical-writing/SKILL.md) |
| Prepare clean commits and a fast-to-review PR description | [`make-pr-easy-to-review`](../make-pr-easy-to-review/SKILL.md) |
| Shepherd an open PR to merge-ready green CI | [`babysit`](../babysit/SKILL.md) |
| Diagnose and fix failing GitHub Actions checks | [`fix-ci`](../fix-ci/SKILL.md) |
| Resolve merge conflicts non-interactively and validate | [`fix-merge-conflicts`](../fix-merge-conflicts/SKILL.md) |
| Fetch and triage PR review comments | [`get-pr-comments`](../get-pr-comments/SKILL.md) |
| Deep security, breakage, devex regression, and leak audit on a branch | [`thermo-nuclear-review`](../thermo-nuclear-review/SKILL.md) |
| Harsh maintainability, file-size, and abstraction audit | [`thermo-nuclear-code-quality-review`](../thermo-nuclear-code-quality-review/SKILL.md) |
| Run both thermo reviews in parallel and synthesize | [`thermos`](../thermos/SKILL.md) |
| Run a large or ambiguous multi-part migration with auditable log | [`figure-it-out`](../figure-it-out/SKILL.md) |
| Record/audit an append-only TSV decision trail | [`show-me-your-work`](../show-me-your-work/SKILL.md) |
| Model-check a TLA+ state spec and render Mermaid bug-path charts | [`statechart`](../statechart/SKILL.md) |
| Vet performance benchmarks (limiters, tuning, bounds, repeatability) | [`benchmark-checklist`](../benchmark-checklist/SKILL.md) |
| Encode repeated mistakes structurally (schema -> types -> lint -> test -> docs) | [`correct`](../correct/SKILL.md) |
| Generate a repo-local app verification skill | [`create-verification-skill`](../create-verification-skill/SKILL.md) |
| Audit and sync the repo's verification skill and feature map | [`maintain-verification-skill`](../maintain-verification-skill/SKILL.md) |
| Generate a repo-local formal verification skill | [`create-formal-verification-skill`](../create-formal-verification-skill/SKILL.md) |
| Sync formal verification models after verified code changes | [`maintain-formal-verification-skill`](../maintain-formal-verification-skill/SKILL.md) |
| Configure per-role pstack model overrides | [`setup-pstack`](../setup-pstack/SKILL.md) |
| Capture user habits into a personal `-mode` skill | [`automate-me`](../automate-me/SKILL.md) |
| Turn session transcripts into approved skill improvements | [`reflect`](../reflect/SKILL.md) |
| Navigate installed pstack skills and playbooks | [`poteto-help`](SKILL.md) |
| Drive HVAC Web 1440px UI Playwright & live checks (project) | [`verify`](../verify/SKILL.md) |
| Verify command intent, fence, MQTT commit, and recovery invariants (project) | [`command-safety`](../command-safety/SKILL.md) |
| Shape HVAC Web visual design, hierarchy, and composition (project) | [`frontend-design`](../frontend-design/SKILL.md) |
| Add/style shadcn/ui components consistently (project) | [`shadcn`](../shadcn/SKILL.md) |
| Audit web accessibility, keyboard, focus, and UX quality (project) | [`web-design-guidelines`](../web-design-guidelines/SKILL.md) |

### Not installed separately in `HVAC_web`

- **`make-bot-ui`**: Cursor-cloud webhook automation UI builder (calls non-existent hosted Cursor webhook endpoints and secret-request UI). Excluded intentionally as non-portable.
- **`principle-*` directories**: `poteto-mode` already embeds the non-negotiable engineering principles cleanly without creating 23 extra skill directories.

## Close calls

- `/how` explains what code does; `/why` explains decisions/history; `/teach` runs both and teaches plainly.
- `/arena` races parallel complete solutions and grafts the best parts; `/swarm` splits slices or independent workers into one report.
- `/interrogate` challenges a diff/design with adversarial runs; `/blast-radius` traces breakage outside the diff and proves safety.
- `/thermos` launches both `/thermo-nuclear-review` (security/breakage) and `/thermo-nuclear-code-quality-review` (maintainability) in parallel.
- `/recall` mines across past chats and shared records; `/ketchup` summarizes only what changed in this chat since your last message.
- `/statechart` model-checks ad-hoc TLA+ specs for state flows; `HVAC_web`'s command/MQTT state machine uses [`command-safety`](../command-safety/SKILL.md) (`npm run command:formal`).

## Playbooks

Playbooks live in `../poteto-mode/playbooks/` (23 total). Describe the task with `poteto-mode` to match one automatically, or name it explicitly ("run the `prototype` playbook", "run the `babysit` playbook", "land the stack with `shipping`", "pause safely", "pick up this branch with `session-pickup`").

Note on principles inside `HVAC_web`: refer to [`poteto-mode`](../poteto-mode/SKILL.md) "Non-negotiables" and "Principles" and `AGENTS.md` (e.g., "prove it works with real output", "no compatibility layers — delete old paths", "small verified increments").

## Fix a run that went wrong

| Symptom | Fix |
|---|---|
| `poteto-mode` drifted mid-chat | Re-state `poteto-mode` or name the exact playbook step checklist you want followed. |
| Model override change had no effect | Start a new chat and check `~/.gemini/config/pstack-models.md` (or `.claude/pstack-models.md`). |
| Parallel workers stepped on each other's files | Require `Workspace: "share"` in `invoke_subagent` (or separate git worktrees). |
| Scheduled/autonomous loop ran without finishing | Require a concrete pass/fail check predicate and decision log (`.scratch/` TSV), never just a time budget. |
| Reply declared done from `build`/`typecheck` alone | Demand live repro, `verify` screenshots, `command:formal`, or `npm run check` output per `prove-it-works`. |

For more steering lines and reusable prompts, see [`references/prompting.md`](references/prompting.md) and [`references/recipes.md`](references/recipes.md).

## Make pstack my own

- [`/automate-me`](../automate-me/SKILL.md) mines your workspace history into a personal `-mode` skill.
- [`/reflect`](../reflect/SKILL.md) turns session lessons into concrete skill edits (mirrored `.agents/skills/` <-> `.claude/skills/` and validated with `npm run repo:check`).
- Authoring new skills: use `poteto-mode` -> [`authoring-a-skill.md`](../poteto-mode/playbooks/authoring-a-skill.md) + built-in `agy-customizations`. Test skill edits with [`eval.md`](../poteto-mode/playbooks/eval.md).

## Reply

Lead with the answer. Give at most one example prompt in a code block, adapted from [`references/recipes.md`](references/recipes.md) when one fits, plus the file link(s). Keep it short unless the user asks for the full skill catalog.
