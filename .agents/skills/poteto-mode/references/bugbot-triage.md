# Bugbot triage

Use this reference when [`../../babysit/SKILL.md`](../../babysit/SKILL.md) handles Bugbot or automated PR-review comments. The goal is not to ignore bots by default. The goal is to stop treating every bot comment as a mandatory code change.

## Decision rubric

Classify each automated review thread before acting:

- `fix`: The comment identifies a plausible correctness, security, privacy, data loss, auth, billing, migration, idempotency, race, or shipped-behavior issue. Fix it in the owning branch, then reply with the commit SHA and resolve the thread.
- `dismiss`: The comment matches a documented low-risk noisy pattern, and the current code/context proves the concern does not need a code change. Reply with a short reason and resolve the thread.
- `ask`: The comment is novel, high-severity, security/privacy/data-related, or ambiguous. Ask the user instead of guessing.

When in doubt, ask. Skipping a noisy code-quality comment is cheap; skipping a real data or security bug is not.

## Learned pattern format

Add future patterns in this shape:

```markdown
### <short pattern name>

- Confidence: candidate | recurring | strong
- Skip when: <conditions that must be true>
- Do not skip when: <risk boundaries>
- Example signal: <phrases or code context that identify the pattern>
- Source: <PR/comment URL or short historical note>
```

Use `candidate` for one or two examples. Use `recurring` after multiple real dismissals. Use `strong` only when the pattern is narrow, repeatedly verified, and low-risk.

## Recurring skip candidates

### Intentional UI or design-system visual changes

- Confidence: candidate
- Skip when: The PR description, screenshots, design review, or nearby code makes the visual change explicit, and the bot comment is only restating that a shared visual default changed.
- Do not skip when: The comment points to accessibility, focus visibility, keyboard navigation, color contrast, or a component API contract that the PR did not intentionally change.
- Example signal: Comments about focus outlines, button sizes, spacing, or shared component visual defaults where the owner replies "intentional" or "intended".

### Upstack or stack-local usage Bugbot cannot see

- Confidence: candidate
- Skip when: Bot flags an export, component, helper, or file as unused, and GitHub PR list/diffs show it is used by a stacked follow-up PR.
- Do not skip when: The current PR is not part of a stack, the symbol is public API, or the supposed upstack use cannot be verified.
- Example signal: "Exported component is never used" with a human reply like "used upstack".

### Temporary duplication during parallel implementation

- Confidence: candidate
- Skip when: The PR intentionally duplicates a small amount of code to keep a new path parallel to an old path that is being deleted, replaced, or proven out.
- Do not skip when: The duplicated code changes security, billing, data access, API behavior, or a long-lived shared abstraction would clearly reduce risk.
- Example signal: "Significant duplication" or "duplicated validation logic" where the owner explains the old path will be deleted or the duplicate logic is intentionally local.

### Existing framework or component invariant covers the warning

- Confidence: candidate
- Skip when: The concern is already guaranteed by a shared component, framework contract, type invariant, or single source of truth visible in the current diff or nearby code.
- Do not skip when: The invariant is assumed but not enforced, depends on timing, or crosses async/state boundaries where values can diverge.
- Example signal: Comments about missing max-height on an inner popover when the shared popover enforces viewport bounds, or nullable values where the local checked value and passed value share the same source.

### Owner-declared follow-up or deferred cleanup

- Confidence: candidate
- Skip when: The PR owner explicitly says the issue is a known follow-up, the behavior is not made worse by the current PR, and the comment is not about a high-risk area.
- Do not skip when: The agent is acting without owner input, the issue is medium/high severity product behavior, or deferring would merge a new regression.
- Example signal: "I'll worry about that later" or "we'll delete this eventually".

### Self-withdrawn or explicit false-positive rule comments

- Confidence: recurring
- Skip when: The comment body or a later bot reply explicitly says the finding is withdrawn, compliant, or a false positive, and the agent can verify the relevant rule locally.
- Do not skip when: The only evidence is someone saying "false positive" on a high-risk issue without explanation.
- Example signal: A file-naming rule comment whose body says the file is already compliant.

## Ask by default

Do not auto-skip these categories, even if a previous PR dismissed something similar:

- Security, privacy, auth, billing, data retention, and permission-boundary findings.
- High-severity findings.
- Migration, schema, idempotency, concurrency, and cross-system behavior findings.
- Comments where the suggested fix is small and clearly reduces risk without changing product intent.

## Candidate learnings

### Manual reimplementations of native browser behavior

- Confidence: candidate
- Skip when: Practically never. When a diff replaces native browser behavior with a manual equivalent (native sticky -> JS clones, native scroll -> forwarded wheel events), logic-bug findings against that code are consistently legitimate.
- Do not skip when: The finding concerns event-forwarding gaps, hit-testing divergence, or observer timing races. Default to fix.

### Contract-test drift claims are cheaply verifiable

- Confidence: candidate
- Skip when: Never skip verification itself; run the pinned contract/governance test on the PR tip before classifying. A red run confirms the claim empirically; a green run provides the disproof for dismissal.
- Do not skip when: n/a.

### Stale security-review finding already fixed later in the same PR

- Confidence: candidate
- Skip when: A review claims a missing authz/validation call, and the current PR tip clearly invokes that exact guard before the side effect with test coverage.
- Do not skip when: The helper is a no-op for the principal, runs after the side effect, or coverage for that principal is missing.

### Widening a deliberately narrow error condition would mask the real error

- Confidence: candidate
- Skip when: The finding asks to broaden a narrow error condition (like `ENOENT` vs command exit failure) into a catch-all where the narrowness encodes a real operational distinction.
- Do not skip when: The narrow condition misses an error in the same category (such as `EACCES`), leaves partial state, or hides failures.
