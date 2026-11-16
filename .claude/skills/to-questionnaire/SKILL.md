---
name: to-questionnaire
description: Turn a decision you can't fully answer into a structured Markdown questionnaire for someone else to fill in async or in a meeting.
disable-model-invocation: true
---

# To Questionnaire

Turn something the user cannot answer alone (knowledge held by a field HVAC engineer, site operator, client, or colleague) into a **questionnaire**: a Markdown document they hand to one person to fill in async or complete together in a meeting.

**Grill the send, not the subject.** Interview the user only about the _send_, which they can always answer: who it goes to and what they need back. Aim every question at the gap between what the recipient knows and what the user needs.

## Process

1. **Who is it going to?** Ask (via `ask_question` or one concise exchange) the recipient's role, domain expertise, language preference, and relationship to the user. Done when you know who will read it and what context they hold.
2. **What do you need back?** Ask in one exchange what specific decisions, site constraints, or facts the user must walk away able to act on. Done when you have a concrete list of required outputs.
3. **Write the questionnaire.** Draft non-compound questions ordered most-important-first using the template below, applying `GLOSSARY.md` terms where shared with the recipient and [`../unslop/SKILL.md`](../unslop/SKILL.md) plain prose. Save to `.scratch/to-questionnaire-<slug>.md` by default (unless the user specifies a `docs/` path) and report the clickable path. Done when every item from Step 2 is covered.

## Document structure

```markdown
# <Questionnaire title>

**Purpose:** why this questionnaire exists and the decision riding on it.

**From:** <the user>, **To:** <the recipient>, **How your answers will be used:** <where they go>

## Context

One short paragraph orienting a recipient who was not in the session. Enough to answer well, not a full spec.

## How to answer

Deadline and rough effort. Partial answers and "I don't know" are useful: flag anything you are unsure of rather than skipping it.

## <Theme heading>

One `##` section per theme when there are more than a handful of questions. Each question covers one idea, never compound, with an answer stub beneath and a one-line _Why this matters_ hint only where the question could be misread.

### What cooling load range is expected at peak summer occupancy?

_Why this matters: it determines whether the plant runs one or two chillers during staging tests._

>

## Anything else?

Anything we did not ask that we should know before deciding?
```
