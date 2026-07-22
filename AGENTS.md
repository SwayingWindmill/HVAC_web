# AGENTS

## Agent skills

### Issue tracker

Issues are tracked in this repo's GitHub Issues (via the `gh` CLI). External PRs are not a triage surface — only issues enter the queue. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` at the repo root plus `docs/adr/` for architectural decisions. See `docs/agents/domain.md`.

### Ticket implementation workflow

Every implementation Ticket must start by loading and following the workspace Matt Pocock `implement` skill at `.agents/skills/implement/SKILL.md`. Use its TDD guidance at pre-agreed seams, review the completed Ticket with the workspace `code-review` skill, and commit the Ticket to the current branch.

To avoid repeatedly paying the full verification cost, apply this repository-specific test cadence:

- During a Ticket, run formatting, typechecking or compilation regularly and run only the smallest directly relevant test file, package or smoke test needed for the current change.
- At the end of a Ticket, do not run the repository-wide test matrix, Docker integration topology, full security-negative suite, observability suite, license scan or vulnerability scan. Record that Map-level gates remain pending.
- At the end of the Map, run the complete verification matrix once, including full tests, integration tests, builds, lint/typechecking, security-negative checks, observability checks, license checks and vulnerability scans required by the affected areas.
- A failing targeted check must still be fixed immediately. The reduced Ticket cadence must never be used to ignore a known failure or commit code that does not compile.

For this repository, the `implement` skill instruction to run the full test suite "once at the end" means once at the end of the Map, not once at the end of every Ticket.

### Reuse-first implementation

Before adding infrastructure, security, testing, observability, data or platform primitives, search GitHub for maintained implementations that satisfy the requirement. Prefer a license-compatible upstream project over custom framework code, pin the selected version or commit, and record the candidates and selection rationale in the ticket documentation. Write project-specific code only for HVAC domain behavior, integration seams or requirements not covered safely by the selected upstream project.

在开始构建项目或实现功能前，先到 GitHub 搜索是否有成熟、可复用的开源项目或组件。

选择时不要只看 Star 数，还要重点评估：

- 与当前项目技术栈和需求的匹配度
- 项目是否持续维护、文档是否完善
- License 和安全风险
- 集成成本及后续维护成本
- 是否会引入过多依赖或不必要的复杂架构

优先复用轻量、稳定、容易替换的方案。

如果开源方案与项目不够吻合，或引入的复杂度明显高于自行实现，则只参考其设计思路，不要强行接入。

编码前简要说明搜索到的候选方案、优缺点及最终选择理由。
