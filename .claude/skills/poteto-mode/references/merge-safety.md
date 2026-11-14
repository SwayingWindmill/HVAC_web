# Merge and restack safety

Use this reference with [`shipping.md`](../playbooks/shipping.md) and before topology changes in [`autopilot-stack.md`](../playbooks/autopilot-stack.md). These checks govern revision and destination safety using `gh` and `git`; they do not grant merge authority by themselves.

## Inspect and cancel pending merge requests

Before rebasing, retargeting, or force-pushing an affected PR, inspect its live landing revision (repository, PR number, head OID, base branch, base OID, auto-merge request, and merge queue state) via `gh`:

```sh
gh pr view "$pr" --json number,state,headRefName,headRefOid,baseRefName,autoMergeRequest,mergeStateStatus,mergeCommit
git ls-remote --exit-code origin "refs/heads/$base_branch"
```

If `autoMergeRequest` is set or the branch is queued, cancel pending auto-merge / queue admission before rewriting:

```sh
gh pr merge "$pr" --disable-auto
```

If a repository merge queue is in use, dequeue through `gh api graphql` (`dequeuePullRequest`) and re-read `gh pr view "$pr" --json state,autoMergeRequest,mergeStateStatus,headRefOid,baseRefName` to verify both mechanisms are absent on the open PR before rewriting commits or base branches.

Establish the affected dependency chain and coordinate with each branch owner first. Inspect and cancel each affected PR, including descendants whose context will change; leave unrelated PRs alone.

## Preserve concurrent writes and child changes

Capture the remote branch SHA before rewriting:

```sh
captured_remote_head=$(git ls-remote --exit-code origin "refs/heads/$child" | awk 'NR==1 {print $1}')
```

Validate that exactly one ref matched. An implicit lease or stale read is insufficient.

After confirming the parent squash landed in the intended destination, fetch trunk and identify the recorded `old_parent_tip` commit on which the child was built. Verify it is an ancestor of the child and inspect `$old_parent_tip..$child` to confirm it contains only child work (never infer the boundary from the parent's new squash commit):

```sh
git merge-base --is-ancestor "$old_parent_tip" "$child"
git rebase --onto "$trunk_tip" "$old_parent_tip" "$child"
git push --force-with-lease="refs/heads/$child:$captured_remote_head" \
  origin "HEAD:refs/heads/$child"
```

Run the rebase in the child's clean worktree. If `--force-with-lease` refuses the push, fetch and reconcile concurrent writes; do not overwrite blindly. Retarget `--base` only after cancellation is verified, and keep the recorded boundary for remaining children.

## What GitHub guards

GitHub's immediate merge head condition is `--match-head-commit <verified-head>` on `gh pr merge` (`expectedHeadOid` in GraphQL, `sha` in REST). None of these supplies an expected base-branch condition atomically, so inspect `baseRefName` and `mergeStateStatus` right before merging and coordinate retargeting carefully.

After merging, verify `MERGED` state, the approved final head, the intended base branch, and a non-null `mergeCommit`, then fetch destination and confirm ancestry:

```sh
gh pr view "$pr" --json state,headRefOid,baseRefName,mergeCommit
git fetch origin "$trunk"
git merge-base --is-ancestor "$merge_commit" "origin/$trunk"
```
