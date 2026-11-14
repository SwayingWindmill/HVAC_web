### Multi-phase or multi-PR plan

Resolve the repo driver skill first (`.agents/skills/verify/SKILL.md` for web UI / live stack, `.agents/skills/command-safety/SKILL.md` for command/MQTT state machine, or `scripts/domain-task-matrix.ts` for backend/static domains).

**You own the plan, not the code. The plan is a checklist an owner runs box by box and the operator audits from the evidence.** The plan is the deliverable; do not implement while planning.

1. When the change is one or two files with an obvious approach, skip this playbook. Say so and stop.
2. Settle open questions by prototype before writing the plan. Run [`prototype.md`](prototype.md) for each open design fork, keeping the branch, SHA, and screenshots for Appendix A. Ask the operator via `ask_question` only about product or preference calls no run can settle.
3. Explore in subagents (`invoke_subagent`, see [`agy-tools.md`](../references/agy-tools.md)) returning file pointers, conventions, test commands, and entry points rather than full file dumps (`guard the context window`).
4. Copy the skeleton below into `.scratch/orchestrate/<slug>/docs/plan.md` (unless the user names another path) and fill every placeholder cleanly. Keep every heading and sub-block in exact order, one section per PR (`sequence verifiable units`). Name [`autopilot-stack.md`](autopilot-stack.md) when a PR builds on another PR, [`autopilot-full.md`](autopilot-full.md) when every PR stands alone off `main`, or [`orchestrate.md`](orchestrate.md) for a multi-day program.
5. Write prose under [`technical-writing`](../../technical-writing/SKILL.md) and [`unslop`](../../unslop/SKILL.md): body is how-to, appendices hold explanation/reference, no long dashes, no curly quotes, and no mid-sentence colons (`: `) outside code/URLs.
6. Run `node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/check-plan.ts <plan.md>` in WSL and fix every reported issue before handing back.
7. Hand back the plan path and the script's output, then stop until the user gives explicit go.

````markdown
# <Program> plan

<Under ten lines. What changes, for whom, the rule the program enforces, and the PR ids in order.>

## How to read this

One box is one unit of work. Every box names the evidence that checks it. A nested box is a sub-step of the box above it. Check a box only when its evidence exists, a file, a log line, a screenshot, a test run, or a SHA. The body is a how-to. The appendices explain and record.

The program runs `.agents/skills/poteto-mode/playbooks/<execution playbook>.md`. <Who merges, and which PR ids stop at merge-ready.>

Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

## Program checklist

### Arm the program

- [ ] State the protocol and this plan to the operator, then stop. Start execution only on the operator's explicit go.
- [ ] On the operator's go, write this exact text into `notes.md` in the program's store under `.scratch/orchestrate/<slug>/` and restate it in your checklist. "<The plan path, the PR ids in order, the verification rule, who merges, and the done condition.>"
- [ ] Read these files at program start and on every audit tick.
  - [ ] `.agents/skills/poteto-mode/playbooks/<execution playbook>.md`
  - [ ] `.agents/skills/swarm/SKILL.md`
  - [ ] `<driver skill path>`
  - [ ] `.agents/skills/poteto-mode/playbooks/opening-a-pr.md`
  - [ ] `.agents/skills/<each other leaf skill the program uses>/SKILL.md`
- [ ] Arm the hourly audit tick using Antigravity `schedule` (never shell `sleep`).
- [ ] Use this tick prompt, verbatim. "Re-read the execution playbook under `.agents/skills/poteto-mode/playbooks/` and the objective in `notes.md` in the program's store. Audit the operation against both and fix drift in this tick. Probe every active lane and judge progress by side effects only. Stand down a stuck lane and dispatch its replacement now. Then post a short status message to the operator in chat only when the audit found a tracked change that no earlier status message reported, such as a PR opened, a code-ready head, a round launched or closed, a verdict, a merge, a stuck agent and the action taken, a blocker added or cleared, or a decision only the operator can make. Name every such change and nothing else. If the audit found none, end the turn silently. Either way, log this tick's row in your decision trail under `.scratch/`."
- [ ] On the operator's hold or stand-down, send every owner a zero-writes order at once.

### Spawn owners

- [ ] Spawn one owner per PR with the full lifecycle the execution playbook names.
- [ ] Follow this dependency graph. Start dependent work only after its parent merges, or base it on the parent branch when the execution playbook stacks.
  - [ ] <PR id> and <PR id> are independent and first. Both branch from `main`.
  - [ ] <PR id> after <PR id>.
- [ ] Hold the file boundaries. <PR id or class> touches only `<glob>`.
- [ ] Hold the review gate. <PR ids> change an interaction and wait for the operator's review with screenshots before merge.

### PR mechanics, for every PR

- [ ] Use GitHub CLI (`gh`) and `git` for every PR operation.
- [ ] Open the PR ready via `gh pr create --base <base-branch>` per `opening-a-pr.md`. A stack child targets its parent branch.
- [ ] Run repo checks (`wsl -e bash -lc "npm run domain:run ..."` / `npm run check`) before pushing from Windows `git`. Push with hooks on.
- [ ] Run `deslop` before each commit and `no-comments` before review.
- [ ] Triage every review comment per `.agents/skills/poteto-mode/references/bugbot-triage.md`.
- [ ] Before the code-ready report and babysit, and again after merge prep, record the base and head SHAs.

### Verdict and merge, for every PR

- [ ] A full swarm runs once at the code-ready head (re-run only for new behavior, redesign, or large diff). Run each full swarm per `.agents/skills/swarm/SKILL.md` with unit/gates lane, ten live lanes, perf lane, and audit lanes.
- [ ] Block only on proven failures or wrong docs. A new head gets a fresh verdict unless `git patch-id` confirms unchanged code per `.agents/skills/poteto-mode/playbooks/shipping.md`.
- [ ] Solo PRs follow `opening-a-pr.md` + `AGENTS.md` auto-squash merge on green CI; stacked queues follow `shipping.md`.

### Boot recipe, for every live lane

- [ ] `git fetch origin <head-branch> && git checkout <head SHA>`.
- [ ] <Start backend / recorded gateway or dev server via `wsl -e bash -lc` and wait for ready.>
- [ ] <Drive surface in WSL through `.agents/skills/verify/SKILL.md` or `.agents/skills/command-safety/SKILL.md`.>
- [ ] Save every screenshot under `/mnt/e/Code/HVAC_web/.scratch/swarm-<pr-id>/worker-<n>/<slug>.png` (explicit main-repo path so `Workspace: "share"` results remain visible) and return exact paths.

## <Task as a verb phrase> (<PR id>)

**Depends on.** <PR id, or None.>

**Files.**

- [ ] Edit `<path>`.

**Build.**

- [ ] <One concrete change naming symbol and file.>

**You see.**

- [ ] <One observable result, exact log line, or UI state.>

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] <Test file and case.> Run `<command>`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` model at the PR head.

- [ ] Lane 1. Regression lane against trunk. Run `<scenario>` at trunk and head. Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 2. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 3. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 4. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 5. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 6. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 7. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 8. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 9. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.
- [ ] Lane 10. <Scenario.> Save `<slug>.png`. Pass when `<predicate>`.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. <What is measured on trunk and head.>
- [ ] Probe. <Interleaved command producing the metric.>
- [ ] Baseline. <Trunk measurement first.>
- [ ] Rule. <Head vs trunk threshold or absolute diff budget.>

**Review gate.** <`None. <PR id> is not review-gated.` OR checklist below:>

- [ ] Copy screenshots into `.scratch/orchestrate/<slug>/<pr-id>-review-<slug>.png`.
- [ ] Record a short video or Playwright trace as `.scratch/orchestrate/<slug>/<pr-id>-review.mp4` and review screenshot/video evidence with the operator before merge.

**Merge.**

- [ ] Clean verdict at exact head SHA.
- [ ] Review threads resolved and base/patch-id verified under `shipping.md`.

## Close the program

- [ ] Every box above is checked with recorded evidence.
- [ ] Reply with the report required by the execution playbook.

## Appendix A. Prototype evidence

<Prototype questions settled, branch/SHA, and screenshot paths.>

## Appendix B. Alternatives rejected

<Approaches weighed and why each lost.>

## Appendix C. Risks

<Risks per PR and what owners monitor.>

## Appendix D. Links and reading list

<ADRs, specs, `how`/`interrogate` targets, and `.scratch/` decision log path per `.agents/skills/show-me-your-work/SKILL.md`.>
````

**Reply:** plan path, PR IDs with dependencies and review-gated set, prototype evidence vs open questions, and `check-plan.ts` output.
