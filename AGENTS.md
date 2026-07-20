# AGENTS

## Agent skills

### Issue tracker

Issues are tracked in this repo's GitHub Issues (via the `gh` CLI). External PRs are not a triage surface — only issues enter the queue. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` at the repo root plus `docs/adr/` for architectural decisions. See `docs/agents/domain.md`.

### Reuse-first implementation

Before adding infrastructure, security, testing, observability, data or platform primitives, search GitHub for maintained implementations that satisfy the requirement. Prefer a license-compatible upstream project over custom framework code, pin the selected version or commit, and record the candidates and selection rationale in the ticket documentation. Write project-specific code only for HVAC domain behavior, integration seams or requirements not covered safely by the selected upstream project.
