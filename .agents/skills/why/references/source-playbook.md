# Source playbooks

The `why` skill spawns one investigator per available evidence category. Always run the local source-control investigator; for other categories, run an investigator only when a matching MCP is actually connected in the current runtime.

| Category | Playbook / Sources | How to adapt |
|---|---|---|
| Source control history | [`code-archaeology.md`](./sources/code-archaeology.md) | `git`, `gh pr`, `gh issue`, `docs/adr/`, `docs/specs/`, `docs/architecture/`, `docs/operations/`, `CHANGELOG.md` |
| External categories (issue tracker, long-form docs, chat, observability, error tracking, analytics warehouse) | Adapt [`code-archaeology.md`](./sources/code-archaeology.md) + category schemas | Inspect the connected MCP's tool schemas first; run broad keyword + ID queries, read full threads/issues, and follow intra-source links without inventing unavailable MCP tools. |

Cross-cutting:

- [`incident-postmortem.md`](./sources/incident-postmortem.md). Add this if the target code looks defensive (null checks, retry, timeout, rate limit, feature flag, egress guard, OOM handler).
