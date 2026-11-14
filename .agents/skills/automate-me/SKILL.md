---
name: automate-me
description: "Use for \"automate me\", \"create/update/refresh my -mode skill\", \"turn/capture my preferences or working style into a skill\", or wanting agents to follow how the user works. Drafts or revises a personal -mode skill via agy-customizations + unslop, optionally pulling fresh evidence from recent transcripts."
disable-model-invocation: true
---

# Automate me

A guided flow for turning the user's working conventions into a skill agents will follow. The output is one `-mode` skill tailored to them (e.g. `hao-mode`).

This skill orchestrates three parts: an inline mining pass (step 1), Antigravity's built-in `agy-customizations` skill (authoring), and [`unslop`](../unslop/SKILL.md) (prose discipline). It sequences them and does not replace them.

## Flow

### 0. Check for an existing skill

Look for `.agents/skills/**/*-mode/SKILL.md` (and `.claude/skills/` mirror) or a personal skill under Antigravity's discovered global roots (`~/.gemini/config/skills/*-mode/SKILL.md`, plus `~/.agents/skills/*-mode/SKILL.md` or `~/.gemini/skills/*-mode/SKILL.md` when registered in `~/.gemini/config/skills.json`) matching the user's handle. If one exists, confirm intent with `ask_question` (unless they already said "update my skill" or similar):

- Update the existing skill (default for repeat runs)
- Start fresh (rare, ask why before doing it)

Update mode changes the rest of the flow:
- For repo-shared skills, Step 1 mines only history since the skill was last edited (`git log -1 --format=%cI <path>`). For personal skills outside the git repo, compare against file modification time (`stat`) instead of `git log`.
- Step 2 asks what's changed or missing, not what to capture from zero.
- Step 4 edits the existing file in place. Preserve sections the user hasn't contradicted. Revise ones with new evidence. Add new sections only for genuinely new rules.

### 1. Mine their history

Locate this workspace's transcripts before fanning out:
- **Antigravity**: `<appDataDir>/brain/<conversation-id>/.system_generated/logs/transcript.jsonl` (`transcript_full.jsonl` when `truncated_fields` is present). Match workspace URIs so you never inspect unrelated projects' chats.
- **Claude Code**: `~/.claude/projects/<slug>/<uuid>.jsonl`.

Survey recent agent conversations within that workspace scope for recurring patterns. Spawn parallel read-only subagents (`invoke_subagent`, `TypeName: "research"`, following [`agy-tools.md`](../poteto-mode/references/agy-tools.md)) across slices of history (e.g. last 2-4 weeks, split into slices). Each slice miner reads transcripts from the workspace-scoped path you provide, looks for the signals below, and returns a short structured list of patterns with evidence pointers:

- Response preferences (length, tone, language, format, "dumb it down" corrections)
- Delegation habits (subagents, models, specialized workflows, parallelism)
- Verification posture (what "done" means, unit tests vs live repro, reviewers)
- Code and prose discipline (style, principles cited, lint/format tools)
- Process conventions (branches, commits, PRs, auto-merge, CI gates)
- Meta preferences (fixing skills mid-task, proposing new ones)

Cross-check across slices before elevating a signal. Patterns seen in 2+ slices are high-confidence. Lone signals are weak and usually get dropped.

### 2. Ask the user directly

Mining misses intent that hasn't come up yet. Use `ask_question` (structured multi-choice) rather than asking the user to type from scratch.

Shape: one or two questions with 4-6 options each, `is_multi_select: true` for category questions. Start broad ("Which areas matter most?"), then follow up on selected areas with specific options. After the structured rounds, one free-form prompt catches anything the options missed.

Don't dump 20 questions.

### 3. Cluster findings

Group the combined signals into sections. Common ones (use only what applies):

- **Response style**: length, tone, format.
- **Autonomy**: how much to do without asking, MCP tool use.
- **Understand first**: which skills to reach for when scoping or investigating a change.
- **Subagents**: default, parallelism, specialized workflows.
- **Prose / code discipline**: principles, lint tools, style guides.
- **Review and verify**: repro posture, verification skills, live-testing tools.
- **Process**: branches, commits, PRs, review/merge tooling.
- **Skills**: skill-authoring habits, fix-the-skill-first, proposing new skills.

[`poteto-mode`](../poteto-mode/SKILL.md) shows the shape. Read it for granularity. Don't copy its content; the user's rules are not the same as poteto-mode's.

### 4. Draft the skill

Read the built-in `agy-customizations` skill to author cleanly under `HVAC_web`'s rules:

- Path: for a repo-shared skill, `.agents/skills/<handle>-mode/SKILL.md` mirrored 1:1 to `.claude/skills/<handle>-mode/SKILL.md` (with LF endings and zero JS files); for a personal skill across projects on Antigravity, `~/.gemini/config/skills/<handle>-mode/SKILL.md` (or `~/.agents/skills/` / `~/.gemini/skills/` if registered in `~/.gemini/config/skills.json`).
- Handle: the user's first name or chosen identifier.
- Frontmatter `description`: trigger on their name + `/<handle>-mode` + "work in their style", not on generic keywords like "write code" or "review PR".
- Frontmatter formatting: valid YAML scalar (`description: "..."` or `description: >-`).
- Frontmatter `disable-model-invocation: true` by default. Opt out only if the user explicitly wants their mode to apply on every turn.

### 5. Iterate on prose

Apply [`unslop`](../unslop/SKILL.md) to every line.

Show the draft to the user and take feedback. Expect multiple iterations. Cut ruthlessly. A mode skill is not a manual.

### 6. Land it

If writing into the repo (`.agents/skills/` + `.claude/skills/`), mirror both directories verbatim, run `npm run repo:check` in WSL, and open a PR following [`opening-a-pr.md`](../poteto-mode/playbooks/opening-a-pr.md). Don't push directly to `main`.

## Guardrails

- **Don't overfit to one conversation.** A preference stated once and contradicted another time is noise. Require multiple instances before codifying it.
- **Don't be clever.** Restating other skills' contents, inventing metaphors, or writing "poetic" prose for an agent reader is cost without benefit. Keep it operational.
- **Reference, don't inline.** Other skills the user relies on should appear as path references, not pasted excerpts.
- **Keep sections minimal.** Only add a section if the user has a specific, non-default rule there.
- **Name conventions generic.** Use "the user" or "the human" in imperatives, not the author's first name.
- **Don't force symmetry.** If a user has no process rules worth writing down, skip the Process section entirely.

## Evaluation

A `-mode` skill is subjective output. Vibe-check with the user: does it read like them? Did it miss anything? Then ship.

## When not to use

- User wants a task-specific skill (not working conventions): read `agy-customizations` + [`authoring-a-skill.md`](../poteto-mode/playbooks/authoring-a-skill.md) directly, no mining required.
- User wants to capture one narrow workflow (e.g. "how I write commit messages"). That's a regular skill, not a mode skill.
