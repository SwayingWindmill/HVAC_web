### Eval

**You own the experiment design. Plan, blind, run, synthesize.**

**Non-negotiables for blinding:**

- None of the words `eval`, `test`, `judge`, `experiment`, `rubric`, `score`, `compare`, `benchmark`, `candidate`, or `arena` in any directory, file, or prompt the candidate sees.
- The candidate prompt looks like an organic user request: state the goal, not the meta.
- No chain-eliciting cues. Do not ask the candidate to list which skills, principles, or files it read. Ask for design notes generally and grade chain-following from actual code/files read, not self-report.
- Sanitize directory and slug names (e.g. under `.scratch/`). Use realistic project-shaped names.
- Never tell a candidate other candidates exist.
- The judge can know it is judging, but sees outputs by sanitized label only, never by model name.
- When comparing variants, one judge scores all sets in a single pass on one scale, blind to which variant each came from.

**Steps:**

1. **Frame.** State what variant is under test and what behavior counts as success. Write the rubric (3-6 concrete criteria) for the judge only; hold it back from candidates.
2. **Set up sanitized environments.** Prepare per-candidate isolated directories (`Workspace: "share"` or isolated scratch copies under `.scratch/`) containing the variant under test and realistic context. Note: `Workspace: "share"` copies tracked repo state and does **not** inherit untracked `.scratch/` files from the parent workspace — stage untracked variant inputs via temporary branch commits, inside each candidate directory, or pass explicit parent workspace paths; run Node/repo commands in WSL login shell (`wsl -e bash -lc ...`).
3. **Author one organic prompt.** What a real user would type, with zero leakage of what is being measured.
4. **Spawn N parallel candidates.** Launch subagents via `invoke_subagent` (`TypeName: "self"`, `Workspace: "share"` if editing files) using the same organic prompt. Follow [`agy-tools.md`](../references/agy-tools.md) for model choices (`Model: "inherit"` unless configured otherwise or user explicitly compares models).
5. **Spawn one blinded judge.** Launch an independent read-only subagent per [`arena`](../../arena/SKILL.md) Phase C (`TypeName: "research"` or read-only `"self"`). Judge sees outputs by sanitized label and the rubric only.
6. **Verify behavior from transcripts, not self-report.** Inspect each candidate subagent's `transcript.jsonl` (at `<appDataDir>/brain/<conversation-id>/.system_generated/logs/transcript.jsonl` using the `conversationId` returned by `invoke_subagent`, reading matching lines in `transcript_full.jsonl` when `truncated_fields` is present) to see which skills/files it actually read and which commands it ran.
7. **Read every candidate output yourself end-to-end.** Compare to the judge's verdict; disagreement exposes judge bias or rubric ambiguity.

**Reply:** variant under test, rubric, per-candidate notes, judge's verdict, your synthesis, and recommendation on promoting the variant.
