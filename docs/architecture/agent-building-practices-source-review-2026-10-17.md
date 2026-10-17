# Building agents: source review (2026-10-17)

What first-party guidance says about building, evaluating and improving tool-using agents, and
what it means for the Operations Agent (AI 助手). Read in full on 2026-10-17:

- [A] Anthropic, *Building effective agents* (2024-12-19) — <https://www.anthropic.com/engineering/building-effective-agents>
- [T] Anthropic, *Writing effective tools for agents — with agents* (2025-09-11) — <https://www.anthropic.com/engineering/writing-tools-for-agents>
- [C] Anthropic, *Effective context engineering for AI agents* (2025-09-29) — <https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents>
- [E] Anthropic, *Demystifying evals for AI agents* (2026-01-09) — <https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents>

OpenAI's *A practical guide to building agents* sits behind a bot check and was not read.

## What the sources say

### Shape of the system

- Start with the simplest thing that works; add agentic complexity only when it demonstrably
  improves outcomes, measured by evaluation [A]. Agents are "LLMs autonomously using tools in a
  loop" [C]; their implementation is usually simple, so the toolset and its documentation decide
  the result [A].
- Three principles: keep the design simple, show the agent's planning steps, and craft the
  agent-computer interface (tools) as carefully as a human interface [A].
- Frameworks hide prompts and responses; understand the code underneath [A].

### Evaluation comes first

- Without evals a team "flies blind": it cannot tell regressions from noise and fixes one failure
  by creating another [E]. Tool improvements are measured against an evaluation [T].
- Start early with 20–50 tasks drawn from real failures and from the checks already done by hand;
  early changes have large effects, so small sets suffice [E].
- A good task is one two domain experts would grade the same; give each a reference solution;
  test both directions (should do / should not do) to avoid one-sided optimisation [E].
- Graders: code-based where possible (string/number checks, tool calls, turns, tokens), model-based
  rubrics where needed, each dimension judged in isolation, with an "Unknown" way out, calibrated
  against human experts; grade the outcome, not the exact path, and allow partial credit [E].
- Run several trials per task. pass@k (one success in k) fits tools; pass^k (all k succeed) fits
  agents users expect to be reliable every time [E].
- The eval harness must behave like production and isolate trials [E]. Track runtime, number of
  tool calls, tokens and tool errors alongside accuracy [T][E].
- Read the transcripts: they show whether a failure is the agent's or the grader's [T][E].
- Capability evals start at a low pass rate and give a hill to climb; regression evals stay near
  100 %; capability tasks graduate into the regression suite [E].
- Evals let a team judge a new model in days instead of weeks [E]. They complement production
  monitoring, user feedback and periodic transcript review (the "Swiss cheese" model) [E].

### Tools

- A few thoughtful tools for high-impact workflows beat wrappers around every endpoint; tools may
  consolidate several calls; overlapping tools confuse agents — "if a human engineer can't
  definitively say which tool should be used … an AI agent can't be expected to do better" [T][C].
- Return high-signal, natural-language context; avoid low-level identifiers; resolving UUIDs to
  meaningful names reduces hallucination [T].
- Bound responses (filtering, ranges, truncation with guidance); make error responses specific and
  actionable rather than opaque codes [T].
- Write tool descriptions like onboarding a new hire: make implicit terminology explicit, name
  parameters unambiguously; small description changes produce large gains [T].
- Poka-yoke: change arguments so mistakes are hard (the absolute-path example) [A].

### Context

- Context is a finite attention budget; find "the smallest possible set of high-signal tokens" [C].
- System prompts at the right altitude: neither brittle if-else procedures nor vague guidance;
  organise into sections; start minimal and add instructions from observed failures [C].
- Prefer a few diverse canonical examples to a laundry list of edge-case rules [C].
- Long conversations: compaction, clearing old tool results, structured notes; sub-agents only for
  parallel exploration [C].

## What this means for the Operations Agent

| Guidance | Today (2026-10-17) | Decision |
| --- | --- | --- |
| Evaluation-driven development [E][T] | No eval set; quality checked by asking a few questions by hand | **ADOPT** first: a task suite from real Songyaxuan questions and observed failures, code graders first, several trials per task, latency/tokens/tool calls recorded, transcripts kept for reading |
| Graders calibrated by people [E] | — | **ADAPT**: the user (operations lead) spot-checks model-graded dimensions; start with code graders only |
| High-signal tool results, no internal identifiers [T] | Tools return `valid_count`, `PEAK`, `electricity`; answers then speak of 「有效计量区间」 | **ADOPT**: tool results in operators' words; counts and quality enums stay out unless they change a conclusion |
| Few, distinct tools [T][C] | Four overlapping energy tools (series, compare, by-equipment, semantic query) | **ADOPT**, measured: consolidate only when the eval shows the model choosing wrongly or chaining needlessly |
| Actionable errors [T] | Tool's own refusals are generic English messages | **ADOPT**: say what was wrong and what is allowed |
| Right-altitude system prompt with examples [C] | Prompt prescribes a fixed tool sequence; no examples | **ADOPT**: sections (role, data rules, answer shape), a few canonical answers, procedure left to the model |
| Show planning steps [A] | Reads shown collapsed in the panel | Keep |
| Simple single agent [A][C] | One Pi loop, read-only Tools | Keep; no sub-agents |
| Layered guardrails | Site fixed by the server, Gateway authorizes each read, no write Tools; execution-claim guard matches English only | Keep; extend the guard to Chinese |
| Monitoring, feedback, transcript review [E] | None | **ADOPT** after the suite exists |
| Compaction [C] | Not needed yet (short sessions) | **DEFER** until sessions grow |

The LangGraph-era benchmark design (a scenario contract and runner whose code no longer existed)
was deleted; the evaluation suite in `services/operations-agent-service/evals/` replaces it.
