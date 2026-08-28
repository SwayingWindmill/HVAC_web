# Historical implementation evidence

`docs/evidence/` contains retained planning and release evidence that is still referenced by repository acceptance or CI workflows but is no longer current architecture authority.

Rules:

- Current architecture authority remains under `docs/architecture/` and machine-readable contracts under `contracts/`.
- Historical evidence is preserved when an acceptance matrix, workflow, audit record, or release bundle still needs a stable repository path.
- New temporary agent output, scratch notes, logs, generated source copies, credentials, and local checkout content must not be added here.
- Historical content may describe superseded names or topology. It is evidence of what was accepted at that time, not permission to reintroduce obsolete architecture.

The first retained sets were migrated from `.scratch/go-data-ai-platform*` during RC-01 repository convergence so active S0 release evidence and S1 IAM-provider CI no longer depend on a scratch directory.
