# Matt Pocock skills v1.3.1 installation

Installed in this repository on 2026-10-05 (Asia/Taipei), following the user's explicit instruction to complete the upgrade. This supersedes the earlier review-only installation status, not its baseline comparison or session evidence.

## Source and scope

- Official repository: [mattpocock/skills](https://github.com/mattpocock/skills).
- Release: [v1.3.1](https://github.com/mattpocock/skills/releases/tag/v1.3.1).
- Pinned commit: `24fe0ef7737efae15c87225755e9f6f5965e4888`.
- Installation: `.agents/skills/<skill-name>/`, retaining the repository's existing flat layout. All 37 official skill folders across engineering, productivity, misc and in-progress are included, with all 101 source/reference/script/metadata files copied byte-for-byte.
- Reviewed evidence and original per-file comparison: [source review](matt-skills-v1.3-source-review-2026-10-05.md) and [baseline diff](matt-skills-v1.3.1-baseline.diff). The baseline diff remains a historical review artifact, not the final working-tree diff.

## ADOPT / ADAPT / REJECT

| Concern | Decision and installed result |
| --- | --- |
| Official v1.3.1 skill source | ADOPT. All 37 folders match the pinned official source exactly. No local prompt rewrites or compatibility wrappers. |
| Missing skills | ADOPT. Installed implement-spec, pr, retro, prototype, to-questionnaire, wait-what and writing-for-agents with the skill-installer helper at the exact commit. |
| Existing skill updates | ADOPT. Synchronized the remaining 30 folders, including ask-matt's corrected diagnosis routing and GLOSSARY consumers. |
| Removed upstream skill | REJECT. Removed resolving-merge-conflicts and its metadata; its historical source remains in Git history. |
| Repository layout | ADAPT installation layout only. Flatten upstream category folders into the existing `.agents/skills` discovery directory; skill folder contents stay official. |
| Codex metadata | ADOPT. Every skill carries official `agents/openai.yaml`. Removed the blanket metadata ignore; scoped the root scratch `/prototype/` ignore so it does not hide the installed skill. |
| Invocation restrictions | ADOPT. User-only skills retain both `disable-model-invocation: true` and `policy.allow_implicit_invocation: false`; installation does not invoke those workflows. |
| Project authority | Retain AGENTS.md and developer/user instructions as the authority when an invoked upstream workflow conflicts with WSL runtime, meaningful tests, gate selection, desktop-only acceptance or external action authorization. |

Frontend-design, migrate-radix-to-base, reui, shadcn and web-design-guidelines are project-owned/other-source skills and were verified unchanged. The existing GitHub tracker, labels, `docs/agents/` configuration, root GLOSSARY definitions and ADRs were retained. This is a repository-scoped skill installation; no global Codex skill directories or Claude plugin caches were changed.

The upstream MIT copyright, permission and disclaimer are preserved in [MATTPOCOCK-LICENSE.txt](../../.agents/skills/MATTPOCOCK-LICENSE.txt). The pr skill's upstream CREDITS.md is included. No package dependencies or permanent checks were added.

## Verification

- Confirmed the source checkout's HEAD is the pinned commit and its working tree is clean.
- Compared every installed official file byte-for-byte against that checkout: **37 skills, 101 files, zero differences**.
- Verified the paired Claude/Codex invocation flags for every skill and confirmed each Codex metadata file is not Git-ignored.
- Verified the five unrelated skill trees have unchanged SHA-256 hashes and the obsolete skill/format paths are absent.
- Upstream's existing version check passed: `plugin.json version is 1.3.1 (already in sync)`.
- `git diff --check` passed. No product code changed in this installation, so unrelated product builds and runtime tests were not run.

Installed official tree SHA-256: `272b6035fd8a8478cc09ee54d6e4c2d6aec77e2d164c59e94924086979c6c38e`. This hashes sorted repository-relative official file paths followed by NUL, exact file bytes, then NUL; the shared license file is outside the 101-file skill tree digest.

Skills become available to Codex on the next turn's discovery. File verification does not claim every prompt was executed or its workflow behavior tested. Changes remain local on `codex/matt-skills-v1-3-review`; this installation does not publish a PR or push the branch.
