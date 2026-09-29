# 06 诊断中心 — Surface Brief

**Status: IMPLEMENTED / BROWSER REVIEWED**  
**Route intent:** `/sites/$siteId/diagnostics`

**Implementation evidence:** `npm run diagnostics:browser:review` validates the diagnostics queue, selected investigation workspace, explicit separation of verified facts / published deterministic or model Finding / unresolved root-cause hypothesis, Alarm and Work Order handoffs, Registry name projection without UUID leakage, 1672×941 desktop composition, 768px responsive behavior, and zero legacy Ant DOM.

## User job
Investigate why a problem may be occurring, separate verified evidence from hypotheses, and define the next verification or work step.

## Questions
- What symptoms are verified?
- Which objects and time windows are involved?
- What hypotheses are currently supported, unsupported or unresolved?
- What evidence would discriminate between hypotheses?
- What is the impact and confidence?
- What action or work order should happen next?

## Information hierarchy
```text
Investigation queue / selected diagnosis
Problem statement + scope + current status
Verified evidence
Hypotheses / candidate causes
  confidence + supporting / contradicting evidence
Impact / related objects
Verification steps
Recommended next action
History of investigation updates
```

## Interaction model
- The page must visually distinguish fact, deterministic result and AI/model hypothesis.
- A correlation graph may be used only when relationships are evidence-backed; a graph does not itself prove causality.
- Users can open source Alarm, Device, Trend or Work Order while preserving investigation context.
- AI may propose hypotheses and verification steps; authoritative state changes remain with domain owners.

## Component mapping
TanStack Table/list for investigation queue, Tabs only for true peer evidence views, Card/section for one coherent hypothesis, Badge for diagnosis status/confidence category, ECharts for evidence trends, G6 only when a dynamic relationship graph materially helps investigation.

## URL state
Selected diagnosis, time window, hypothesis/evidence view and source object may be shareable search/route state.

## Browser acceptance
- Verified facts are never styled like AI conclusions.
- “No conclusion yet” is a valid state.
- Confidence is shown only when produced by an authoritative model/calculation.
- The page always provides a clear next verification/action path rather than a decorative root-cause diagram.
