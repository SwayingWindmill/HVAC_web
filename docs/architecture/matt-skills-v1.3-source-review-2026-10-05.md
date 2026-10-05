# Matt Pocock skills v1.3 source review, 2026-10-05

**Subsequent installation:** the user later authorized the full upgrade. All 37 official skills are now installed at the pinned v1.3.1 commit; see [installation and verification](matt-skills-v1.3.1-installation-2026-10-05.md). The following comparison records the original pre-upgrade baseline.

## Scope and pinned sources

This is a read-only source review of the Matt-owned skill folders tracked at HVAC_web commit `c28a38e1` before this review's edits. It compares text after normalizing CRLF to LF. It does not treat unrelated frontend/vendor skills as Matt-owned merely because they share the directory. Missing skill installation and upgrades are recommendations, not actions performed by this report.

The fetched main baseline `8a542fab` has identical tracked `.agents/skills` contents to `c28a38e1` (checked with `git diff`), so the initial skill counts apply to either. The accompanying [full baseline diff](./matt-skills-v1.3.1-baseline.diff) includes all metadata and absent Matt skill source files in the local directory layout. It is a review artifact, not an applied upgrade patch. The parent task separately performs only the requested glossary rename and current consumer updates; that must not be described as installation of the entire v1.3.1 bundle.

Official release pins:

| Release | Commit | Published, Asia/Taipei |
| --- | --- | --- |
| [v1.3.0](https://github.com/mattpocock/skills/releases/tag/v1.3.0) | `984a2c023c9fb42bb6ea40c70a652284a109dc05` | 2026-10-04 20:47:22 |
| [v1.3.1](https://github.com/mattpocock/skills/releases/tag/v1.3.1) | `24fe0ef7737efae15c87225755e9f6f5965e4888` | 2026-10-04 20:48:18 |
| [v1.2.3](https://github.com/mattpocock/skills/releases/tag/v1.2.3) | `6acc160e4e0cd062dbbbd7a1b26ae92855edf07e` | comparison baseline only |

Recommend v1.3.1 as the v1.3 reference. Its only skill-source difference from v1.3.0 is one paragraph in ask-matt: diagnosing-bugs no longer promises an automatic architecture post-mortem handoff. Instead the human can run retro after a fix, or improve-codebase-architecture when the finding is a missing seam. The other patch files are diagnosing-bugs documentation, changelog, and package/plugin versions. [Exact official comparison](https://github.com/mattpocock/skills/compare/v1.3.0...v1.3.1).

The checkout was obtained from the official repository at the pinned tag. Read every compared skill text/reference/script file, the three new graduated skills, the invocation contract, setup documentation/templates, README/plugin listing, CHANGELOG, LICENSE, package scripts, release workflow and version-sync script. The upstream repository has no automated behavioral skill test suite at this tag: tdd/tests.md is instruction/reference material, not executable tests. The package exposes check-plugin-version; the release workflow versions/tags the bundle. This limits validation to source and metadata review, not a claim that prompts are behaviorally tested.

## Findings

Local skills are already substantially newer than the v1.2.3 tag. Most existing Matt skill source files are byte-equivalent after newline normalization to v1.3.1. Therefore a blanket replacement from a presumed v1.2.3 baseline would overstate the update and discard a reviewable narrow scope.

v1.3 graduates implement-spec, pr, and retro into the supported Engineering/plugin collection, removes resolving-merge-conflicts with no replacement, and renames the domain-document convention to GLOSSARY.md/GLOSSARY-MAP.md and the owned format reference to GLOSSARY-FORMAT.md. It also changes invocation language and removes the diagnosing-bugs automatic post-mortem handoff. Most invocation changes are already in this local checkout. [Release source](https://github.com/mattpocock/skills/releases/tag/v1.3.0).

The tracked repository omits upstream agents/openai.yaml metadata because `.gitignore` excludes `.agents/skills/**/agents/`. The actual installed working tree **does contain 31 ignored metadata files**: all 30 retained Matt skills match v1.3.1 exactly after newline normalization; the 31st belongs to resolving-merge-conflicts, which upstream removed. This is a fresh-clone reproducibility distinction, not a missing local installation or a new v1.3 feature. User-invoked skills have both disable-model-invocation: true and policy.allow_implicit_invocation: false upstream; model-invoked skills omit the policy restriction. Metadata adds Codex picker names/descriptions and keeps invocation restrictions synchronized across harnesses. This review makes no metadata-based claim about session failures. [Official invocation contract](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/.agents/invocation.md).

Upstream's literal “Call the Skill tool” convention assumes the harness exposes that tool. This Codex session uses skill-file discovery and reads instead. Adapt invocation mechanics to the documented tool available in the harness while preserving the model/user invocation boundary; do not create a fake tool or silently invoke a user-only orchestration skill.

The upstream existing metadata check was run through Linux Node and passed: `plugin.json version is 1.3.1 (already in sync)`. No dependencies or permanent CI gates were introduced for this documentation review.

## Setup review and required local configuration changes

The local setup skill and domain template differ only by the old CONTEXT names. Its quoted YAML description and the tracker/triage templates are already equivalent to v1.3.1. The generated docs/agents/domain.md still reads CONTEXT.md/CONTEXT-MAP.md, so renaming only the root file leaves consumers pointing at a nonexistent path. Update all current consumers and the format file together; do not add an old-name fallback.

The generated issue-tracker.md already specifies GitHub, PRs-as-triage disabled, map/child relationships, native dependency checks and frontier querying. Preserve this current tracker choice and label vocabulary. No tracker switch or monorepo restructuring is needed. Rename root CONTEXT.md to GLOSSARY.md and update docs/agents/domain.md, the existing skill paths and any current steering/check references.

Upstream setup is prompt-driven, normally confirms before writing and prefers CLAUDE.md when both instruction files exist. Its docs explicitly identify this as a Codex harness gap and accept a canonical AGENTS.md pointer arrangement or manual transfer. Check that Codex's actual instruction authority reaches docs/agents/*.md; do not blindly rerun setup and silently route all configuration to a Claude-only file. [Skill](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/setup-matt-pocock-skills/SKILL.md), [official setup docs](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/docs/engineering/setup-matt-pocock-skills.md).

The docs acknowledge that old seed templates can go stale after upgrades, while the skill's closing message says re-running is only necessary for tracker changes or restarting. Resolve this by inspecting and editing the existing generated Markdown directly here; the concrete mismatch is the glossary name. Setup records label mappings; it does not create remote labels. Do not perform remote writes simply to re-check setup.

## ADOPT / ADAPT / REJECT decisions

| Concern | Decision | Evidence and project reason |
| --- | --- | --- |
| GLOSSARY names and format reference | ADOPT | Upstream explicitly stops reading CONTEXT names. This repo already has one domain glossary, so rename rather than split it or introduce compatibility. |
| v1.3.1 diagnosis routing | ADOPT recommendation | ask-matt promises behavior removed from diagnosing-bugs; corrected explicit human retro/architecture route matches the actual source. |
| diagnosing-bugs phase 6 | ADOPT, already present locally | Local Cleanup already matches v1.3.1, including removal of the obsolete architecture handoff. Only its glossary pointer needs renaming. The ask-matt router still describes the old handoff. |
| Codex metadata | Already ADOPTED on disk; tracking policy is a separate decision | All 30 retained installed metadata files match pinned upstream. Git ignores them, so a fresh clone lacks them. Preserve the harness restrictions and consider tracking only the required selected skill metadata when addressing reproducibility. |
| implement-spec | ADAPT recommendation for a later explicitly invoked whole-spec run | Task-graph/frontier/worktree integration is useful for already-defined independent tickets. Respect branch prefix, managed worktrees, WSL authority, focused gates and meaningful tests. Do not use it to infer permission to resolve arbitrary remote tickets or reset unrelated worktrees. |
| pr | ADAPT recommendation | Keep concrete evidence and merge impact. Scale visual/body detail to change complexity and existing PR guidance; skip irrelevant mobile acceptance and needless template boilerplate. |
| retro | ADAPT recommendation | Prioritize actual navigation/tool-economy friction and unwired existing checks. HVAC no-gate-inflation rule wins over default new permanent hooks/jobs; attach concrete checks to the existing domain matrix. Requires writing-for-agents dependency. |
| resolving-merge-conflicts | REJECT as retained upstream skill recommendation | Official release removes it, no replacement. The agent can resolve concrete merge conflicts without this obsolete folder. No deletion performed by this read-only report. |
| unrelated frontend skills | Out of comparison scope | Project-selected frontend direction owns those skills; Matt's release does not justify replacing them. |

The upstream MIT license permits copied/adapted source provided the copyright and permission notice are included in copies or substantial portions. Any future source import should preserve that notice and the pinned source provenance. pr additionally carries CREDITS.md crediting Dex Horthy's show-me, which must accompany its adoption. [License](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/LICENSE), [pr credits](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/pr/CREDITS.md).

## Exact inventory and file-level comparison

The table counts existing local tracked files only, excluding unrelated local skills. New means a file present upstream but absent from the tracked baseline; ignored metadata already exists on disk. Removed includes the domain format's rename (not lost content). The v1.2.3 column demonstrates that this is not an untouched v1.2.3 installation; it does not label every difference as a local customization.

| Skill | Local files | Equal to v1.3.1 | Changed | Removed/renamed | New upstream files | Equal to v1.2.3 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| ask-matt | 2 | 1 | 1 | 0 | 1 | 0/2 |
| claude-handoff | 1 | 1 | 0 | 0 | 1 | 0/1 |
| code-review | 1 | 1 | 0 | 0 | 1 | 0/1 |
| codebase-design | 3 | 2 | 1 | 0 | 1 | 0/3 |
| diagnosing-bugs | 2 | 1 | 1 | 0 | 1 | 0/2 |
| domain-modeling | 3 | 1 | 1 | 1 | 2 | 0/3 |
| git-guardrails-claude-code | 2 | 2 | 0 | 0 | 1 | 1/2 |
| grill-me | 1 | 1 | 0 | 0 | 1 | 0/1 |
| grill-with-docs | 1 | 1 | 0 | 0 | 1 | 0/1 |
| grilling | 1 | 1 | 0 | 0 | 1 | 0/1 |
| handoff | 1 | 1 | 0 | 0 | 1 | 0/1 |
| implement | 1 | 1 | 0 | 0 | 1 | 1/1 |
| improve-codebase-architecture | 2 | 1 | 1 | 0 | 1 | 0/2 |
| loop-me | 1 | 1 | 0 | 0 | 1 | 0/1 |
| migrate-to-shoehorn | 1 | 1 | 0 | 0 | 1 | 1/1 |
| research | 1 | 1 | 0 | 0 | 1 | 0/1 |
| resolving-merge-conflicts | 1 | 0 | 0 | 1 | 0 | 0/1 |
| scaffold-exercises | 1 | 1 | 0 | 0 | 1 | 1/1 |
| setup-matt-pocock-skills | 6 | 4 | 2 | 0 | 1 | 1/6 |
| setup-pre-commit | 1 | 1 | 0 | 0 | 1 | 0/1 |
| setup-ts-deep-modules | 2 | 2 | 0 | 0 | 1 | 0/2 |
| tdd | 3 | 2 | 1 | 0 | 1 | 2/3 |
| teach | 5 | 5 | 0 | 0 | 1 | 0/5 |
| to-spec | 1 | 1 | 0 | 0 | 1 | 0/1 |
| to-tickets | 1 | 1 | 0 | 0 | 1 | 0/1 |
| triage | 3 | 2 | 1 | 0 | 1 | 0/3 |
| wayfinder | 1 | 1 | 0 | 0 | 1 | 0/1 |
| wizard | 2 | 2 | 0 | 0 | 1 | 0/2 |
| writing-beats | 1 | 1 | 0 | 0 | 1 | 0/1 |
| writing-fragments | 1 | 1 | 0 | 0 | 1 | 0/1 |
| writing-shape | 1 | 1 | 0 | 0 | 1 | 0/1 |

Compared 31 existing Matt folders and 54 tracked local files: 43 equal, 9 changed at the same path, 2 removed/renamed, and 31 upstream additions within retained folders.

### Upstream skills absent locally

- **implement-spec** (engineering): [SKILL.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/implement-spec/SKILL.md), [agents/openai.yaml](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/implement-spec/agents/openai.yaml).
- **pr** (engineering): [CREDITS.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/pr/CREDITS.md), [SKILL.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/pr/SKILL.md), [agents/openai.yaml](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/pr/agents/openai.yaml).
- **prototype** (engineering): [LOGIC.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/prototype/LOGIC.md), [SKILL.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/prototype/SKILL.md), [UI.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/prototype/UI.md), [agents/openai.yaml](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/prototype/agents/openai.yaml).
- **retro** (engineering): [SKILL.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/retro/SKILL.md), [agents/openai.yaml](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/retro/agents/openai.yaml).
- **to-questionnaire** (productivity): [SKILL.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/productivity/to-questionnaire/SKILL.md), [agents/openai.yaml](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/productivity/to-questionnaire/agents/openai.yaml).
- **wait-what** (productivity): [SKILL.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/productivity/wait-what/SKILL.md), [agents/openai.yaml](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/productivity/wait-what/agents/openai.yaml).
- **writing-for-agents** (productivity): [SKILL-MECHANICS.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/productivity/writing-for-agents/SKILL-MECHANICS.md), [SKILL.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/productivity/writing-for-agents/SKILL.md), [agents/openai.yaml](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/productivity/writing-for-agents/agents/openai.yaml).

implement-spec, pr and retro are newly graduated in v1.3. prototype, to-questionnaire, wait-what and writing-for-agents already existed before this release. Their absence is not evidence they were newly added in v1.3.

### Unrelated local folders excluded

frontend-design, migrate-radix-to-base, reui, shadcn, web-design-guidelines.

### Per-file status

**ask-matt**

- same: PHASE-BOUNDARIES.md.
- changed: SKILL.md.
- removed: none.
- added: agents/openai.yaml.

**claude-handoff**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**code-review**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**codebase-design**

- same: DEEPENING.md, SKILL.md.
- changed: DESIGN-IT-TWICE.md.
- removed: none.
- added: agents/openai.yaml.

**diagnosing-bugs**

- same: scripts/hitl-loop.template.sh.
- changed: SKILL.md.
- removed: none.
- added: agents/openai.yaml.

**domain-modeling**

- same: ADR-FORMAT.md.
- changed: SKILL.md.
- removed: CONTEXT-FORMAT.md.
- added: GLOSSARY-FORMAT.md, agents/openai.yaml.

**git-guardrails-claude-code**

- same: SKILL.md, scripts/block-dangerous-git.sh.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**grill-me**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**grill-with-docs**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**grilling**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**handoff**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**implement**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**improve-codebase-architecture**

- same: HTML-REPORT.md.
- changed: SKILL.md.
- removed: none.
- added: agents/openai.yaml.

**loop-me**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**migrate-to-shoehorn**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**research**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**resolving-merge-conflicts**

- same: none.
- changed: none.
- removed: SKILL.md.
- added: none.

**scaffold-exercises**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**setup-matt-pocock-skills**

- same: issue-tracker-github.md, issue-tracker-gitlab.md, issue-tracker-local.md, triage-labels.md.
- changed: SKILL.md, domain.md.
- removed: none.
- added: agents/openai.yaml.

**setup-pre-commit**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**setup-ts-deep-modules**

- same: SKILL.md, dependency-cruiser.config.cjs.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**tdd**

- same: mocking.md, tests.md.
- changed: SKILL.md.
- removed: none.
- added: agents/openai.yaml.

**teach**

- same: GLOSSARY-FORMAT.md, LEARNING-RECORD-FORMAT.md, MISSION-FORMAT.md, RESOURCES-FORMAT.md, SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**to-spec**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**to-tickets**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**triage**

- same: AGENT-BRIEF.md, OUT-OF-SCOPE.md.
- changed: SKILL.md.
- removed: none.
- added: agents/openai.yaml.

**wayfinder**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**wizard**

- same: SKILL.md, template.sh.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**writing-beats**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**writing-fragments**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.

**writing-shape**

- same: SKILL.md.
- changed: none.
- removed: none.
- added: agents/openai.yaml.


### Exact Codex metadata absent from Git tracking (already installed on disk)

| Installed ignored metadata | Count | v1.3.1 comparison |
| --- | ---: | --- |
| Retained Matt skills | 30 | All 30 equal after newline normalization |
| resolving-merge-conflicts | 1 | Removed upstream |
| Total ignored local metadata | 31 | Present on disk, none tracked |


**ask-matt/agents/openai.yaml**

```yaml
interface:
  display_name: "Ask Matt"
  short_description: "Find the right skill or workflow"
policy:
  allow_implicit_invocation: false
```

**claude-handoff/agents/openai.yaml**

```yaml
interface:
  display_name: "Claude Handoff"
  short_description: "Hand off to a background agent"
policy:
  allow_implicit_invocation: false
```

**code-review/agents/openai.yaml**

```yaml
interface:
  display_name: "Code Review"
  short_description: "Review a diff on standards and spec"
```

**codebase-design/agents/openai.yaml**

```yaml
interface:
  display_name: "Codebase Design"
  short_description: "Vocabulary for deep-module design"
```

**diagnosing-bugs/agents/openai.yaml**

```yaml
interface:
  display_name: "Diagnosing Bugs"
  short_description: "Diagnose hard bugs and regressions"
```

**domain-modeling/agents/openai.yaml**

```yaml
interface:
  display_name: "Domain Modeling"
  short_description: "Build and sharpen a domain model"
```

**git-guardrails-claude-code/agents/openai.yaml**

```yaml
interface:
  display_name: "Git Guardrails for Claude Code"
  short_description: "Block dangerous git commands"
```

**grill-me/agents/openai.yaml**

```yaml
interface:
  display_name: "Grill Me"
  short_description: "Sharpen a plan through interview"
policy:
  allow_implicit_invocation: false
```

**grill-with-docs/agents/openai.yaml**

```yaml
interface:
  display_name: "Grill with Docs"
  short_description: "Grill a design and write its docs"
policy:
  allow_implicit_invocation: false
```

**grilling/agents/openai.yaml**

```yaml
interface:
  display_name: "Grilling"
  short_description: "Stress-test thinking a round of questions at a time"
```

**handoff/agents/openai.yaml**

```yaml
interface:
  display_name: "Handoff"
  short_description: "Compact a conversation into a handoff"
policy:
  allow_implicit_invocation: false
```

**implement/agents/openai.yaml**

```yaml
interface:
  display_name: "Implement"
  short_description: "Build work from a spec or tickets"
policy:
  allow_implicit_invocation: false
```

**improve-codebase-architecture/agents/openai.yaml**

```yaml
interface:
  display_name: "Improve Codebase Architecture"
  short_description: "Find and grill architecture improvements"
policy:
  allow_implicit_invocation: false
```

**loop-me/agents/openai.yaml**

```yaml
interface:
  display_name: "Loop Me"
  short_description: "Spec the workflows you want to build"
policy:
  allow_implicit_invocation: false
```

**migrate-to-shoehorn/agents/openai.yaml**

```yaml
interface:
  display_name: "Migrate to Shoehorn"
  short_description: "Replace test assertions with shoehorn"
```

**research/agents/openai.yaml**

```yaml
interface:
  display_name: "Research"
  short_description: "Research from high-trust sources"
```

**scaffold-exercises/agents/openai.yaml**

```yaml
interface:
  display_name: "Scaffold Exercises"
  short_description: "Scaffold lint-ready course exercises"
```

**setup-matt-pocock-skills/agents/openai.yaml**

```yaml
interface:
  display_name: "Setup Matt Pocock Skills"
  short_description: "Configure a repo for the skills"
policy:
  allow_implicit_invocation: false
```

**setup-pre-commit/agents/openai.yaml**

```yaml
interface:
  display_name: "Setup Pre-Commit"
  short_description: "Add pre-commit quality checks"
```

**setup-ts-deep-modules/agents/openai.yaml**

```yaml
interface:
  display_name: "Setup TS Deep Modules"
  short_description: "Enforce deep TypeScript modules"
policy:
  allow_implicit_invocation: false
```

**tdd/agents/openai.yaml**

```yaml
interface:
  display_name: "TDD"
  short_description: "Test-driven red-green-refactor"
```

**teach/agents/openai.yaml**

```yaml
interface:
  display_name: "Teach"
  short_description: "Learn a concept in a guided workspace"
policy:
  allow_implicit_invocation: false
```

**to-spec/agents/openai.yaml**

```yaml
interface:
  display_name: "To Spec"
  short_description: "Turn a conversation into a spec"
policy:
  allow_implicit_invocation: false
```

**to-tickets/agents/openai.yaml**

```yaml
interface:
  display_name: "To Tickets"
  short_description: "Split a plan into tracer-bullet tickets"
policy:
  allow_implicit_invocation: false
```

**triage/agents/openai.yaml**

```yaml
interface:
  display_name: "Triage"
  short_description: "Move issues through triage roles"
policy:
  allow_implicit_invocation: false
```

**wayfinder/agents/openai.yaml**

```yaml
interface:
  display_name: "Wayfinder"
  short_description: "Map a large effort as decision tickets"
policy:
  allow_implicit_invocation: false
```

**wizard/agents/openai.yaml**

```yaml
interface:
  display_name: "Wizard"
  short_description: "Generate an interactive setup wizard"
```

**writing-beats/agents/openai.yaml**

```yaml
interface:
  display_name: "Writing Beats"
  short_description: "Assemble raw material into beats"
policy:
  allow_implicit_invocation: false
```

**writing-fragments/agents/openai.yaml**

```yaml
interface:
  display_name: "Writing Fragments"
  short_description: "Mine raw writing fragments"
policy:
  allow_implicit_invocation: false
```

**writing-shape/agents/openai.yaml**

```yaml
interface:
  display_name: "Writing Shape"
  short_description: "Shape raw material into an article"
policy:
  allow_implicit_invocation: false
```


### Unified diffs, pre-task local baseline → pinned v1.3.1

These exact zero-context source diffs are review artifacts, not an applied patch or a direct-apply recipe. Domain-format rename appears as old-file deletion and new-file addition. Metadata is recorded above to avoid repeating creation hunks.

### .agents/skills/ask-matt/SKILL.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/ask-matt/SKILL.md).

```diff
--- .agents/skills/ask-matt/SKILL.md
+++ skills/engineering/ask-matt/SKILL.md
@@ -17 +17 @@
-1. **`/grill-with-docs`** sharpens the idea by interview. Start here whenever you are **working in a working directory**: it's stateful, retaining what it learns in `CONTEXT.md` and ADRs. (No working directory? Use `/grill-me` instead, covered under Standalone. Both run the same `/grilling` primitive; `grill-with-docs` is the one that leaves a paper trail, which makes it the better of the two whenever a repo is there to leave it in.)
+1. **`/grill-with-docs`** sharpens the idea by interview. Start here whenever you are **working in a working directory**: it's stateful, retaining what it learns in `GLOSSARY.md` and ADRs. (No working directory? Use `/grill-me` instead, covered under Standalone. Both run the same `/grilling` primitive; `grill-with-docs` is the one that leaves a paper trail, which makes it the better of the two whenever a repo is there to leave it in.)
@@ -23 +23,3 @@
-   - **Yes** → **`/to-spec`** (turn the thread into a spec), then **`/to-tickets`** to split it into tracer-bullet tickets, each declaring its **blocking edges**. On a local tracker that's one file per ticket under `.scratch/<feature>/issues/`, worked blockers-first by hand; on a real tracker the edges become native blocking links, so any ticket whose blockers are done can be grabbed: kick off **`/implement`** per ticket, **`/clear`ing context between each one**. Each ticket is self-contained, so the last one's context is disposable.
+   - **Yes** → **`/to-spec`** (turn the thread into a spec), then **`/to-tickets`** to split it into tracer-bullet tickets, each declaring its **blocking edges**. Then work the tickets one of two ways:
+     - **`/implement`** per ticket, **`/clear`ing context between each one**. On a local tracker that's one file per ticket under `.scratch/<feature>/issues/`, worked blockers-first by hand; on a real tracker the edges become native blocking links, so any ticket whose blockers are done can be grabbed. Each ticket is self-contained, so the last one's context is disposable.
+     - **`/implement-spec`** for the whole spec in one run. It reads the tickets as a **task graph**, runs implementer subagents across the ready **frontier** in parallel, and lands everything on one **integration branch**. Reach for it when you'd rather orchestrate the build than drive each ticket yourself.
@@ -26 +28,5 @@
-   Either way, **`/implement`** builds each issue by driving **`/tdd`** internally (one red-green slice at a time), then closes out by running **`/code-review`**, a two-axis review (Standards + Spec) of the diff, before committing. Reach for **`/tdd`** on its own when you just want to build a concrete behaviour test-first without a full spec, and **`/code-review`** on its own whenever you want to review a branch or PR against a fixed point.
+   Either way, the code gets built by driving **`/tdd`** (one red-green slice at a time) and closes out with **`/code-review`**, a two-axis review (Standards + Spec) of the diff. `/implement` runs both per ticket; `/implement-spec`'s implementers each drive `/tdd`, and it runs one `/code-review` over the integration branch. Reach for **`/tdd`** on its own when you just want to build a concrete behaviour test-first without a full spec, and **`/code-review`** on its own whenever you want to review a branch or PR against a fixed point.
+
+   When the work goes up as a pull request, **`/pr`** shapes the body: the smallest visual that shows the change, before/after evidence that it works, and a one-way or two-way door call. It's model-invoked, so the agent reaches for it whenever it writes a PR.
+
+4. **`/retro`** closes the loop. After a build, and especially one that went sideways, it looks back over the session and suggests changes to the agent's **environment**, not the code: navigation pointers, automated checks, the coding standards `/code-review` enforces, steering files, tooling. Mechanical mistakes become deterministic checks; judgement calls become coding standards. The next build then starts from a better environment.
@@ -30 +36 @@
-Keep steps 1–3 in **one unbroken context window** (don't compact or clear until after `/to-tickets`) so the grilling, spec, and tickets all build on the same thinking. Each `/implement` then starts fresh, working from the ticket.
+Keep steps 1–3 in **one unbroken context window** (don't compact or clear until after `/to-tickets`) so the grilling, spec, and tickets all build on the same thinking. Each `/implement` then starts fresh, working from the ticket. Run `/retro` in the session it's looking back on, before you clear; after clearing, point it at that session's log instead.
@@ -42 +48 @@
-- **Something's broken** → **`/diagnosing-bugs`**. For the hard ones: the bug that resists a first glance, the intermittent flake, the regression that crept in between two known-good states. It refuses to theorise until it has a **tight feedback loop** (one command that already goes red on *this* bug), then fixes with a regression test. Its post-mortem hands off to **`/improve-codebase-architecture`** when the real finding is that there's no good seam to lock the bug down.
+- **Something's broken** → **`/diagnosing-bugs`**. For the hard ones: the bug that resists a first glance, the intermittent flake, the regression that crept in between two known-good states. It refuses to theorise until it has a **tight feedback loop** (one command that already goes red on *this* bug), then fixes with a regression test. Once the fix is in, run **`/retro`** in the same session to ask what would have prevented the bug; where the real finding is that there's no good seam to lock it down, that's a job for **`/improve-codebase-architecture`**.
@@ -58 +64 @@
-- **`/domain-modeling`**: sharpen the project's *domain* language: challenge a fuzzy term, resolve an overloaded word ("account" doing three jobs), record a hard-to-reverse decision as an ADR. It's the active discipline `/grill-with-docs` drives to keep `CONTEXT.md` a clean glossary.
+- **`/domain-modeling`**: sharpen the project's *domain* language: challenge a fuzzy term, resolve an overloaded word ("account" doing three jobs), record a hard-to-reverse decision as an ADR. It's the active discipline `/grill-with-docs` drives to keep `GLOSSARY.md` a clean glossary.
@@ -77 +83 @@
-- **`/grill-me`**: the same relentless interview as `/grill-with-docs`, but **stateless**: it saves nothing locally and builds no `CONTEXT.md`. Reach for it when you are **not working in a working directory** (sharpening a plan, a design, a piece of writing, anything with no repo under it). If you are in a working directory, use `/grill-with-docs` instead: it runs the same interview and leaves a paper trail, so it is strictly the better one.
+- **`/grill-me`**: the same relentless interview as `/grill-with-docs`, but **stateless**: it saves nothing locally and builds no `GLOSSARY.md`. Reach for it when you are **not working in a working directory** (sharpening a plan, a design, a piece of writing, anything with no repo under it). If you are in a working directory, use `/grill-with-docs` instead: it runs the same interview and leaves a paper trail, so it is strictly the better one.
@@ -79 +84,0 @@
-- **`/resolving-merge-conflicts`** works an in-progress merge or rebase conflict hunk by hunk, resolving by **intent** traced to each side's primary source rather than by picking lines, then finishes the operation. It never runs `--abort`. Standalone and off every flow: reach for it when you are already mid-conflict.
@@ -84 +89 @@
-- **`/wait-what`** is the corrective for a message that didn't land. Use it mid-conversation, inside any other skill, and the agent re-pitches what it just said with the context you were missing, in plain English, using the `CONTEXT.md` vocabulary. It works after the fact; `/grill-with-docs` is the upfront cure, because a shared language agreed early is what stops the jargon arriving at all.
+- **`/wait-what`** is the corrective for a message that didn't land. Use it mid-conversation, inside any other skill, and the agent re-pitches what it just said with the context you were missing, in plain English, using the `GLOSSARY.md` vocabulary. It works after the fact; `/grill-with-docs` is the upfront cure, because a shared language agreed early is what stops the jargon arriving at all.
```

### .agents/skills/codebase-design/DESIGN-IT-TWICE.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/codebase-design/DESIGN-IT-TWICE.md).

```diff
--- .agents/skills/codebase-design/DESIGN-IT-TWICE.md
+++ skills/engineering/codebase-design/DESIGN-IT-TWICE.md
@@ -30 +30 @@
-Include both [SKILL.md](SKILL.md) vocabulary and CONTEXT.md vocabulary in the brief so each sub-agent names things consistently with the architecture language and the project's domain language.
+Include both [SKILL.md](SKILL.md) vocabulary and GLOSSARY.md vocabulary in the brief so each sub-agent names things consistently with the architecture language and the project's domain language.
```

### .agents/skills/diagnosing-bugs/SKILL.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/diagnosing-bugs/SKILL.md).

```diff
--- .agents/skills/diagnosing-bugs/SKILL.md
+++ skills/engineering/diagnosing-bugs/SKILL.md
@@ -10 +10 @@
-When exploring the codebase, read `CONTEXT.md` (if it exists) to get a clear mental model of the relevant modules, and check ADRs in the area you're touching.
+When exploring the codebase, read `GLOSSARY.md` (if it exists) to get a clear mental model of the relevant modules, and check ADRs in the area you're touching.
```

### .agents/skills/domain-modeling/CONTEXT-FORMAT.md

Source: old path removed and renamed to [pinned upstream GLOSSARY-FORMAT.md](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/domain-modeling/GLOSSARY-FORMAT.md).

```diff
--- .agents/skills/domain-modeling/CONTEXT-FORMAT.md
+++ skills/engineering/domain-modeling/CONTEXT-FORMAT.md
@@ -1,60 +0,0 @@
-# CONTEXT.md Format
-
-## Structure
-
-```md
-# {Context Name}
-
-{One or two sentence description of what this context is and why it exists.}
-
-## Language
-
-**Order**:
-{A one or two sentence description of the term}
-_Avoid_: Purchase, transaction
-
-**Invoice**:
-A request for payment sent to a customer after delivery.
-_Avoid_: Bill, payment request
-
-**Customer**:
-A person or organization that places orders.
-_Avoid_: Client, buyer, account
-```
-
-## Rules
-
-- **Be opinionated.** When multiple words exist for the same concept, pick the best one and list the others under `_Avoid_`.
-- **Keep definitions tight.** One or two sentences max. Define what it IS, not what it does.
-- **Only include terms specific to this project's context.** General programming concepts (timeouts, error types, utility patterns) don't belong even if the project uses them extensively. Before adding a term, ask: is this a concept unique to this context, or a general programming concept? Only the former belongs.
-- **Group terms under subheadings** when natural clusters emerge. If all terms belong to a single cohesive area, a flat list is fine.
-
-## Single vs multi-context repos
-
-**Single context (most repos):** One `CONTEXT.md` at the repo root.
-
-**Multiple contexts:** A `CONTEXT-MAP.md` at the repo root lists the contexts, where they live, and how they relate to each other:
-
-```md
-# Context Map
-
-## Contexts
-
-- [Ordering](./src/ordering/CONTEXT.md): receives and tracks customer orders
-- [Billing](./src/billing/CONTEXT.md): generates invoices and processes payments
-- [Fulfillment](./src/fulfillment/CONTEXT.md): manages warehouse picking and shipping
-
-## Relationships
-
-- **Ordering → Fulfillment**: Ordering emits `OrderPlaced` events; Fulfillment consumes them to start picking
-- **Fulfillment → Billing**: Fulfillment emits `ShipmentDispatched` events; Billing consumes them to generate invoices
-- **Ordering ↔ Billing**: Shared types for `CustomerId` and `Money`
-```
-
-The skill infers which structure applies:
-
-- If `CONTEXT-MAP.md` exists, read it to find contexts
-- If only a root `CONTEXT.md` exists, single context
-- If neither exists, create a root `CONTEXT.md` lazily when the first term is resolved
-
-When multiple contexts exist, infer which one the current topic relates to. If unclear, ask.
```

### .agents/skills/domain-modeling/GLOSSARY-FORMAT.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/domain-modeling/GLOSSARY-FORMAT.md).

```diff
--- .agents/skills/domain-modeling/GLOSSARY-FORMAT.md
+++ skills/engineering/domain-modeling/GLOSSARY-FORMAT.md
@@ -0,0 +1,60 @@
+# GLOSSARY.md Format
+
+## Structure
+
+```md
+# {Context Name}
+
+{One or two sentence description of what this context is and why it exists.}
+
+## Language
+
+**Order**:
+{A one or two sentence description of the term}
+_Avoid_: Purchase, transaction
+
+**Invoice**:
+A request for payment sent to a customer after delivery.
+_Avoid_: Bill, payment request
+
+**Customer**:
+A person or organization that places orders.
+_Avoid_: Client, buyer, account
+```
+
+## Rules
+
+- **Be opinionated.** When multiple words exist for the same concept, pick the best one and list the others under `_Avoid_`.
+- **Keep definitions tight.** One or two sentences max. Define what it IS, not what it does.
+- **Only include terms specific to this project's context.** General programming concepts (timeouts, error types, utility patterns) don't belong even if the project uses them extensively. Before adding a term, ask: is this a concept unique to this context, or a general programming concept? Only the former belongs.
+- **Group terms under subheadings** when natural clusters emerge. If all terms belong to a single cohesive area, a flat list is fine.
+
+## Single vs multi-context repos
+
+**Single context (most repos):** One `GLOSSARY.md` at the repo root.
+
+**Multiple contexts:** A `GLOSSARY-MAP.md` at the repo root lists the contexts, where they live, and how they relate to each other:
+
+```md
+# Glossary Map
+
+## Contexts
+
+- [Ordering](./src/ordering/GLOSSARY.md): receives and tracks customer orders
+- [Billing](./src/billing/GLOSSARY.md): generates invoices and processes payments
+- [Fulfillment](./src/fulfillment/GLOSSARY.md): manages warehouse picking and shipping
+
+## Relationships
+
+- **Ordering → Fulfillment**: Ordering emits `OrderPlaced` events; Fulfillment consumes them to start picking
+- **Fulfillment → Billing**: Fulfillment emits `ShipmentDispatched` events; Billing consumes them to generate invoices
+- **Ordering ↔ Billing**: Shared types for `CustomerId` and `Money`
+```
+
+The skill infers which structure applies:
+
+- If `GLOSSARY-MAP.md` exists, read it to find contexts
+- If only a root `GLOSSARY.md` exists, single context
+- If neither exists, create a root `GLOSSARY.md` lazily when the first term is resolved
+
+When multiple contexts exist, infer which one the current topic relates to. If unclear, ask.
```

### .agents/skills/domain-modeling/SKILL.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/domain-modeling/SKILL.md).

```diff
--- .agents/skills/domain-modeling/SKILL.md
+++ skills/engineering/domain-modeling/SKILL.md
@@ -3 +3 @@
-description: Build and sharpen a project's domain model. Use when discussing codebase terminology, writing or editing a CONTEXT.md, or recording or editing an ADR.
+description: Build and sharpen a project's domain model. Use when discussing codebase terminology, writing or editing a GLOSSARY.md, or recording or editing an ADR.
@@ -8 +8 @@
-Actively build and sharpen the project's domain model as you design. This is the *active* discipline: challenging terms, inventing edge-case scenarios, and writing the glossary and decisions down the moment they crystallise. (Merely *reading* `CONTEXT.md` for vocabulary is not this skill: that's a one-line habit any skill can do. This skill is for when you're changing the model, not just consuming it.)
+Actively build and sharpen the project's domain model as you design. This is the *active* discipline: challenging terms, inventing edge-case scenarios, and writing the glossary and decisions down the moment they crystallise. (Merely *reading* `GLOSSARY.md` for vocabulary is not this skill: that's a one-line habit any skill can do. This skill is for when you're changing the model, not just consuming it.)
@@ -16 +16 @@
-├── CONTEXT.md
+├── GLOSSARY.md
@@ -24 +24 @@
-If a `CONTEXT-MAP.md` exists at the root, the repo has multiple contexts. The map points to where each one lives:
+If a `GLOSSARY-MAP.md` exists at the root, the repo has multiple contexts. The map points to where each one lives:
@@ -28 +28 @@
-├── CONTEXT-MAP.md
+├── GLOSSARY-MAP.md
@@ -33 +33 @@
-│   │   ├── CONTEXT.md
+│   │   ├── GLOSSARY.md
@@ -36 +36 @@
-│       ├── CONTEXT.md
+│       ├── GLOSSARY.md
@@ -40 +40 @@
-Create files lazily: only when you have something to write. If no `CONTEXT.md` exists, create one when the first term is resolved. If no `docs/adr/` exists, create it when the first ADR is needed.
+Create files lazily: only when you have something to write. If no `GLOSSARY.md` exists, create one when the first term is resolved. If no `docs/adr/` exists, create it when the first ADR is needed.
@@ -46 +46 @@
-When the user uses a term that conflicts with the existing language in `CONTEXT.md`, call it out immediately. "Your glossary defines 'cancellation' as X, but you seem to mean Y. Which is it?"
+When the user uses a term that conflicts with the existing language in `GLOSSARY.md`, call it out immediately. "Your glossary defines 'cancellation' as X, but you seem to mean Y. Which is it?"
@@ -60 +60 @@
-### Update CONTEXT.md inline
+### Update GLOSSARY.md inline
@@ -62 +62 @@
-When a term is resolved, update `CONTEXT.md` right there. Don't batch these up: capture them as they happen. Use the format in [CONTEXT-FORMAT.md](./CONTEXT-FORMAT.md).
+When a term is resolved, update `GLOSSARY.md` right there. Don't batch these up: capture them as they happen. Use the format in [GLOSSARY-FORMAT.md](./GLOSSARY-FORMAT.md).
@@ -64 +64 @@
-`CONTEXT.md` should be totally devoid of implementation details. Do not treat `CONTEXT.md` as a spec, a scratch pad, or a repository for implementation decisions. It is a glossary and nothing else.
+`GLOSSARY.md` should be totally devoid of implementation details. Do not treat `GLOSSARY.md` as a spec, a scratch pad, or a repository for implementation decisions. It is a glossary and nothing else.
```

### .agents/skills/improve-codebase-architecture/SKILL.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/improve-codebase-architecture/SKILL.md).

```diff
--- .agents/skills/improve-codebase-architecture/SKILL.md
+++ skills/engineering/improve-codebase-architecture/SKILL.md
@@ -14 +14 @@
-- The domain language in `CONTEXT.md` gives names to good seams; ADRs in `docs/adr/` record decisions this command should not re-litigate.
+- The domain language in `GLOSSARY.md` gives names to good seams; ADRs in `docs/adr/` record decisions this command should not re-litigate.
@@ -25 +25 @@
-Read the project's domain glossary (`CONTEXT.md`) and any ADRs in the area you're touching first.
+Read the project's domain glossary (`GLOSSARY.md`) and any ADRs in the area you're touching first.
@@ -54 +54 @@
-**Use CONTEXT.md vocabulary for the domain, and the `/codebase-design` vocabulary for the architecture.** If `CONTEXT.md` defines "Order," talk about "the Order intake module," not "the FooBarHandler," and not "the Order service."
+**Use GLOSSARY.md vocabulary for the domain, and the `/codebase-design` vocabulary for the architecture.** If `GLOSSARY.md` defines "Order," talk about "the Order intake module," not "the FooBarHandler," and not "the Order service."
@@ -68,2 +68,2 @@
-- **Naming a deepened module after a concept not in `CONTEXT.md`?** Add the term to `CONTEXT.md`. Create the file lazily if it doesn't exist.
-- **Sharpening a fuzzy term during the conversation?** Update `CONTEXT.md` right there.
+- **Naming a deepened module after a concept not in `GLOSSARY.md`?** Add the term to `GLOSSARY.md`. Create the file lazily if it doesn't exist.
+- **Sharpening a fuzzy term during the conversation?** Update `GLOSSARY.md` right there.
```

### .agents/skills/resolving-merge-conflicts/SKILL.md

Source: [v1.3.0 release removal](https://github.com/mattpocock/skills/releases/tag/v1.3.0).

```diff
--- .agents/skills/resolving-merge-conflicts/SKILL.md
+++ (removed upstream)
@@ -1,14 +0,0 @@
----
-name: resolving-merge-conflicts
-description: "Use when you need to resolve an in-progress git merge/rebase conflict."
----
-
-1. **See the current state** of the merge/rebase. Check git history, and the conflicting files.
-
-2. **Find the primary sources** for each conflict. Understand deeply why each change was made, and what the original intent was. Read the commit messages, check the PRs, check original issues/tickets.
-
-3. **Resolve each hunk.** Preserve both intents where possible. Where incompatible, pick the one matching the merge's stated goal and note the trade-off. Do **not** invent new behaviour. Always resolve; never `--abort`.
-
-4. Discover the project's **automated checks** and run them, typically typecheck, then tests, then format. Fix anything the merge broke.
-
-5. **Finish the merge/rebase.** Stage everything and commit. If rebasing, continue the rebase process until all commits are rebased.
```

### .agents/skills/setup-matt-pocock-skills/SKILL.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/setup-matt-pocock-skills/SKILL.md).

```diff
--- .agents/skills/setup-matt-pocock-skills/SKILL.md
+++ skills/engineering/setup-matt-pocock-skills/SKILL.md
@@ -13 +13 @@
-- **Domain docs**: where `CONTEXT.md` and ADRs live, and the consumer rules for reading them
+- **Domain docs**: where `GLOSSARY.md` and ADRs live, and the consumer rules for reading them
@@ -25 +25 @@
-- `CONTEXT.md` and `CONTEXT-MAP.md` at the repo root
+- `GLOSSARY.md` and `GLOSSARY-MAP.md` at the repo root
@@ -59 +59 @@
-**Section C: Domain docs.** Default to **single-context** (one `CONTEXT.md` + `docs/adr/` at the repo root). This fits almost every repo; write it without asking.
+**Section C: Domain docs.** Default to **single-context** (one `GLOSSARY.md` + `docs/adr/` at the repo root). This fits almost every repo; write it without asking.
@@ -61 +61 @@
-Offer **multi-context** (a root `CONTEXT-MAP.md` pointing to per-context `CONTEXT.md` files) only when exploration found monorepo signals. Then confirm which layout they want.
+Offer **multi-context** (a root `GLOSSARY-MAP.md` pointing to per-context `GLOSSARY.md` files) only when exploration found monorepo signals. Then confirm which layout they want.
```

### .agents/skills/setup-matt-pocock-skills/domain.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/setup-matt-pocock-skills/domain.md).

```diff
--- .agents/skills/setup-matt-pocock-skills/domain.md
+++ skills/engineering/setup-matt-pocock-skills/domain.md
@@ -7,2 +7,2 @@
-- **`CONTEXT.md`** at the repo root, or
-- **`CONTEXT-MAP.md`** at the repo root if it exists: it points at one `CONTEXT.md` per context. Read each one relevant to the topic.
+- **`GLOSSARY.md`** at the repo root, or
+- **`GLOSSARY-MAP.md`** at the repo root if it exists: it points at one `GLOSSARY.md` per context. Read each one relevant to the topic.
@@ -19 +19 @@
-├── CONTEXT.md
+├── GLOSSARY.md
@@ -26 +26 @@
-Multi-context repo (presence of `CONTEXT-MAP.md` at the root):
+Multi-context repo (presence of `GLOSSARY-MAP.md` at the root):
@@ -30 +30 @@
-├── CONTEXT-MAP.md
+├── GLOSSARY-MAP.md
@@ -34 +34 @@
-    │   ├── CONTEXT.md
+    │   ├── GLOSSARY.md
@@ -37 +37 @@
-        ├── CONTEXT.md
+        ├── GLOSSARY.md
@@ -43 +43 @@
-When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.
+When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `GLOSSARY.md`. Don't drift to synonyms the glossary explicitly avoids.
```

### .agents/skills/tdd/SKILL.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/tdd/SKILL.md).

```diff
--- .agents/skills/tdd/SKILL.md
+++ skills/engineering/tdd/SKILL.md
@@ -10 +10 @@
-When exploring the codebase, read `CONTEXT.md` (if it exists) so test names and interface vocabulary match the project's domain language, and respect ADRs in the area you're touching.
+When exploring the codebase, read `GLOSSARY.md` (if it exists) so test names and interface vocabulary match the project's domain language, and respect ADRs in the area you're touching.
```

### .agents/skills/triage/SKILL.md

Source: [pinned upstream](https://github.com/mattpocock/skills/blob/24fe0ef7737efae15c87225755e9f6f5965e4888/skills/engineering/triage/SKILL.md).

```diff
--- .agents/skills/triage/SKILL.md
+++ skills/engineering/triage/SKILL.md
@@ -76 +76 @@
-4. **Grill (if needed).** If the request needs fleshing out, call the Skill tool twice, for "grilling" and "domain-modeling", and grill it into shape a round of questions at a time, sharpening domain terms and updating `CONTEXT.md`/ADRs inline as decisions land.
+4. **Grill (if needed).** If the request needs fleshing out, call the Skill tool twice, for "grilling" and "domain-modeling", and grill it into shape a round of questions at a time, sharpening domain terms and updating `GLOSSARY.md`/ADRs inline as decisions land.
```

## Exact v1.3.0 → v1.3.1 skill-source patch

```diff
diff --git a/skills/engineering/ask-matt/SKILL.md b/skills/engineering/ask-matt/SKILL.md
index ce33ac6..8d38b2b 100644
--- a/skills/engineering/ask-matt/SKILL.md
+++ b/skills/engineering/ask-matt/SKILL.md
@@ -48 +48 @@ A starting situation that generates work, then merges onto the main flow.
-- **Something's broken** → **`/diagnosing-bugs`**. For the hard ones: the bug that resists a first glance, the intermittent flake, the regression that crept in between two known-good states. It refuses to theorise until it has a **tight feedback loop** (one command that already goes red on *this* bug), then fixes with a regression test. Its post-mortem hands off to **`/improve-codebase-architecture`** when the real finding is that there's no good seam to lock the bug down.
+- **Something's broken** → **`/diagnosing-bugs`**. For the hard ones: the bug that resists a first glance, the intermittent flake, the regression that crept in between two known-good states. It refuses to theorise until it has a **tight feedback loop** (one command that already goes red on *this* bug), then fixes with a regression test. Once the fix is in, run **`/retro`** in the same session to ask what would have prevented the bug; where the real finding is that there's no good seam to lock it down, that's a job for **`/improve-codebase-architecture`**.
```

## License notice for reproduced upstream source

```text
MIT License

Copyright (c) 2026 Matt Pocock

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
