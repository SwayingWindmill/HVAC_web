### Autopilot-stack

Run [`autopilot-full.md`](autopilot-full.md), but do not merge.

1. **Stop at verified instead of merging.** Stop when each PR is verified and green at its current head, then hand the PRs to the operator (or hand off to [`shipping.md`](shipping.md) once authorized). Mention this no-merge rule in every scheduled audit tick prompt.
2. **Stack a PR only when it builds on another PR.** Base the PR on its parent PR's branch instead of `main`. Each child rebases onto its parent PR's branch, and the stack root rebases onto `main`. Before any restack, follow [`merge-safety.md`](../references/merge-safety.md): capture the affected chain and remote heads, cancel any auto-merge or queue membership, verify cancellation via `gh pr view`, rebase carefully, and push with `--force-with-lease=refs/heads/<branch>:<captured-remote-head>`. Record each verdict's head SHA, base SHA, base branch, and `git patch-id`, and re-verify whenever the patch-id or base interactions change.

**Reply:** list each PR with its verdict, head/base SHA, and patch-id status instead of merged items.
