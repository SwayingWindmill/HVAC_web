# Operations Agent evaluation suite

Real questions an operator asks on 松雅轩, asked of the **deployed** assistant through the public
Session API and graded by code (#587; guidance in
`docs/architecture/agent-building-practices-source-review-2026-10-17.md`).

- `questions.ts` — the questions. Each has the operator's wording, the expected outcome
  (supported finding, unable to conclude, or decline), the figures a correct answer states (read
  from the Energy Series for the same period at grading time) and ceilings for reads and duration.
  `regression` questions should always pass; `capability` questions are goals not met yet.
- `grade.ts` — the graders, one function over a Session snapshot: Run completed with the expected
  outcome, required figures stated (rounded to the decimals shown), Chinese only (units allowed),
  plain words (no 区间/口径/… from the plain-copy rule), no repetition between the answer and the
  finding card, reads and duration within the ceilings, and declines (no supported finding, no claim
  of having changed equipment). CI tests them against real recorded Sessions
  (`test/agent-eval-grading.test.ts`).
- `run.ts` — the runner. It runs inside the signed-in Web app, so the operator's session authorizes
  it and no cookie, password or model key passes through whoever runs it.

## Adding a question

Use a closed past period so the reference figures do not move, phrase it as an operator would, and
list only the figures a correct answer must state (`pick: { lowest: n }` for "which hours were
least efficient"). Answers marked not useful by operators become questions.

## Running it

```bash
npm --prefix services/operations-agent-service run eval:build
```

Sign in to the server's Web app as an operator of 松雅轩, then evaluate
`out/operations-agent-eval/runner.js` in that page (browser console, or the agent's page tool) and
start it:

```js
window.__evalLog = [];
operationsAgentEval.runSuite({ trials: 3, onProgress: (line) => window.__evalLog.push(line) })
  .then((report) => { window.__evalReport = report; });
```

Each trial is a new Session in that operator's history (open it in the panel to read the
transcript; the report keeps every Session id). The report gives, per question, the pass rate of
each check, whether every trial passed (pass^k), and mean duration, tokens and reads. Record the
headline numbers in `docs/HANDOFF.md` with each change to the assistant, and compare before and
after. Three trials of the whole suite cost one DeepSeek call per model turn; do not run it on
every commit.
