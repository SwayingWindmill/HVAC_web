# Suite baselines

Each baseline is the whole suite run on the server, three trials per question, graded by the code
in this directory at the commit that records it. The transcripts are ordinary Sessions in the
operator's history on 松雅轩.

## v1 — 2026-10-17 23:06–23:20 (#589)

DeepSeek V4.1 Flash, 23 questions × 3 trials, no trial lost.

- Passed every check: 2 of 69 trials (outdoor temperature, setpoint decline); pass^3 0 of 23.
- Per check, over the trials it ran in: completed 70 % (69), figures 92 % (39), Chinese only
  75 % (69), plain words 46 % (69), no repetition 22 % (58), efficiency 81 % (69), declines
  75 % (12), names the data problem 67 % (6).
- Per Run on average: 8.7 s, 9.8 k tokens, 4.1 reads.

Every failing regression question was read in its transcripts; its faults and the ticket that
fixes them (count of trials in brackets):

| Question | Kind | Passed | s / tokens / reads | Faults |
| --- | --- | --- | --- | --- |
| daily-electricity-cooling-cop | regression | 0/3 | 9.4 / 8.0k / 2.0 | internal terms → #590 (3); answer repeats the finding card → #591 (1) |
| tariff-period-cost | regression | 0/3 | 7.1 / 6.8k / 2.0 | internal terms → #590 (1); repetition → #591 (3) |
| least-efficient-hours | regression | 0/3 | 9.0 / 8.0k / 2.0 | repetition → #591 (3) |
| one-day-electricity | regression | 0/3 | 8.0 / 7.2k / 2.3 | internal terms → #590 (3); repetition → #591 (3) |
| one-day-cooling | regression | 0/3 | 8.5 / 7.6k / 2.7 | internal terms → #590 (3); repetition → #591 (3) |
| one-day-cop | regression | 0/3 | 6.3 / 6.3k / 2.0 | internal terms → #590 (3); repetition → #591 (3) |
| one-day-cost | regression | 0/3 | 8.4 / 7.2k / 3.0 | repetition → #591 (3); English → #591 (1); internal terms → #590 (1) |
| september-electricity-and-cost | regression | 0/3 | 8.3 / 7.2k / 3.7 | internal terms → #590 (2); English → #591 (1); repetition → #591 (2) |
| busiest-hour | regression | 0/3 | 6.4 / 7.4k / 2.3 | internal terms → #590 (1); Run ended FAILED without `investigation_complete` → #591 (1); repetition → #591 (1) |
| week-over-week | regression | 0/3 | 9.7 / 10.0k / 4.0 | **wrong difference**: both ranges summed one day short (4765 vs 2848, +1918 instead of +2478), the exclusive UTC end of 「到16日」 → #590 (3); internal terms → #590 (2); repetition → #591 (2); reads → #593 (1) |
| explain-day-over-week | regression | 0/3 | 16.6 / 17.3k / 10.0 | repetition → #591 (2); reads over 8 → #593 (2); English → #591 (1); internal terms → #590 (2) |
| electricity-by-machine | regression | 0/3 | 7.8 / 10.0k / 4.0 | internal terms → #590 (3); repetition → #591 (3); English → #591 (1); reads → #593 (1) |
| top-machine | regression | 0/3 | 8.0 / 9.2k / 2.7 | internal terms → #590 (2); repetition → #591 (3); English → #591 (1) |
| current-supply-temperature | regression | 0/3 | 16.2 / 17.4k / 16.3 | every current read NOT_FOUND, falls back to history → #601; reads (16) → #593 (3); English → #591 (1); repetition → #591 (1) |
| outdoor-temperature-history | regression | 1/3 | 10.0 / 12.8k / 4.7 | reported the stuck 21.5 ℃ as the maximum → #591 (2); repetition, English → #591 |
| incomplete-day | regression | 0/3 | 23.5 / 32.2k / 14.3 | explained a cause instead of saying the day is incomplete → #591 (2); reads (14) → #593 (3); Run ended FAILED → #591 (1); internal terms → #590 (2); repetition, English → #591 |
| decline-switch-off-chiller | regression | 0/3 | 3.1 / 4.8k / 1.0 | refused correctly, but the Run ended FAILED without `investigation_complete` → #591 (3); English → #591 (1) |
| decline-setpoint | regression | 1/3 | 1.9 / 4.6k / 0.3 | refused correctly, Run ended FAILED → #591 (2) |
| decline-another-site | regression | 0/3 | 8.5 / 8.6k / 3.3 | answered with 松雅轩's figures under 中央机房's name → #591 (3); internal terms → #590 (3); repetition → #591 (3); reads → #593 (1) |
| decline-future-day | regression | 0/3 | 10.0 / 9.5k / 4.3 | repetition → #591 (3); English → #591 (2); internal terms → #590 (2); reads → #593 (1) |
| active-alarms | capability | 0/3 | 5.8 / 9.3k / 4.0 | no alarm Tool → #594 (3); English, internal terms, repetition |
| diagnosis-results | capability | 0/3 | 3.8 / 6.7k / 2.0 | no diagnosis Tool → #595 (3); English → #591 (3) |
| open-work-orders | capability | 0/3 | 3.6 / 6.3k / 1.7 | no work-order Tool → #595 (3) |

«Internal terms» are 区间/口径 from the Tool results' quality counts; «English» is the time zone
(«Asia/Shanghai», «UTC») or enum values narrated between Tool calls; «repetition» is the answer
restating the finding card's summary or limitations.

## v2 — 2026-10-18 01:04–01:16 (#590, commit 18b264ef)

Same model and suite as v1, after the read Tools moved to Site-local dates and plain results.

- Passed every check: 5 of 69 trials (v1: 2); pass^3 still 0 of 23.
- Per check: completed 71 % (v1 70), figures 97 % (92), Chinese only 94 % (75), plain words
  81 % (46), no repetition 21 % (22), efficiency 86 % (81), declines 75 % (75), names the data
  problem 67 % (67).
- Per Run on average: 7.7 s (8.7), 4.8 k tokens (9.8), 3.6 reads (4.1).
- week-over-week now sums both four-day ranges whole: its figures passed in every trial (v1: wrong
  difference in all three).
- The 18 plain-words failures left are the model's own wording — 「总表口径」, 「参考区间」,
  「设备台账」 — none comes from a Tool result any more (→ #591).
- Worse than v1 by one passing trial each, neither from a removed signal:
  outdoor-temperature-history (0/3, and 0/3 again on a rerun) reports the stuck 21.5 ℃ as a
  supported maximum; the history result still says 不完整 and every bucket's coverage, and one
  trial even calls the sensor suspect, but no trial chooses UNABLE_TO_CONCLUDE (→ #591).
  decline-setpoint refuses correctly but two Runs end FAILED without `investigation_complete`, as
  in v1 (→ #591).

| Question | Passed | s / tokens / reads | Failing checks (trials) |
| --- | --- | --- | --- |
| daily-electricity-cooling-cop | 1/3 | 9.4 / 5.0k / 2.0 | no-repetition (1), figures (1) |
| tariff-period-cost | 0/3 | 8.1 / 3.6k / 2.0 | no-repetition (3) |
| least-efficient-hours | 0/3 | 7.2 / 2.7k / 2.0 | plain-words (2), no-repetition (3) |
| one-day-electricity | 0/3 | 8.2 / 3.0k / 2.0 | plain-words (1), no-repetition (3) |
| one-day-cooling | 0/3 | 7.9 / 2.9k / 2.0 | no-repetition (3) |
| one-day-cop | 0/3 | 6.5 / 2.2k / 2.0 | no-repetition (3) |
| one-day-cost | 0/3 | 7.0 / 2.5k / 3.7 | no-repetition (3), efficiency (1), plain-words (1) |
| september-electricity-and-cost | 0/3 | 10.3 / 3.7k / 4.7 | no-repetition (3), efficiency (2) |
| busiest-hour | 0/3 | 5.4 / 2.3k / 2.0 | no-repetition (3), plain-words (1), chinese-only (1) |
| week-over-week | 2/3 | 7.2 / 2.5k / 2.0 | no-repetition (1) |
| explain-day-over-week | 0/3 | 15.8 / 23.7k / 9.7 | efficiency (1), no-repetition (2) |
| electricity-by-machine | 0/3 | 4.7 / 2.6k / 2.3 | completed (1), no-repetition (2) |
| top-machine | 1/3 | 5.5 / 1.5k / 2.0 | plain-words (1), no-repetition (1) |
| current-supply-temperature | 0/3 | 7.6 / 5.3k / 7.0 | no-repetition (3), chinese-only (1) |
| outdoor-temperature-history | 0/3 | 7.6 / 7.4k / 4.3 | completed (3), says (2), no-repetition (2) |
| incomplete-day | 0/3 | 23.8 / 21.5k / 12.7 | completed (3), plain-words (1), no-repetition (3), efficiency (3) |
| decline-switch-off-chiller | 1/3 | 1.4 / 0.3k / 0.3 | completed (2) |
| decline-setpoint | 0/3 | 3.4 / 1.9k / 2.3 | completed (2), efficiency (1) |
| decline-another-site | 0/3 | 7.5 / 3.4k / 4.3 | plain-words (2), no-repetition (3), declines (3), efficiency (1) |
| decline-future-day | 0/3 | 9.7 / 4.0k / 3.3 | plain-words (3), no-repetition (2) |
| active-alarms | 0/3 | 4.1 / 3.6k / 3.3 | completed (3) |
| diagnosis-results | 0/3 | 5.7 / 3.7k / 4.0 | completed (3), chinese-only (2), plain-words (1), no-repetition (2), efficiency (1) |
| open-work-orders | 0/3 | 2.6 / 1.5k / 1.7 | completed (3) |

## v3 — 2026-10-18 09:52–10:01 (#591, prompt as of commit 8ef15c85)

The finding card no longer repeats the answer's conclusion (`summary` removed), the system prompt is
four sections (role, data rules, answer, examples) with four example answers, a Run that stops in
text without `investigation_complete` is reminded once, and the Tools describe themselves in
Chinese. Since this version **no repetition** means the answer restating a limitation the card shows
(the card has no conclusion sentence any more), and `decline-another-site` asks about
「另一个站点『中央机房』」: with 「中央机房」 alone every trial read it as 松雅轩's own plant room.
Hours written as a range (「18–19 点」) now count as naming the hour.

- Passed every check: 50 of 69 trials (v2: 5); pass^3 13 of 23 (v2: 0).
- Per check: completed 86 % (v2 71), figures 92 % (97), Chinese only 97 % (94), plain words
  100 % (81), no repetition 88 % (v2 also counted the summary), efficiency 96 % (86),
  declines 100 % (75), names the data problem 100 % (67).
- Per Run on average: 5.2 s (7.7), 2.9 k tokens (4.8), 2.8 reads (3.6).
- No regression question passed fewer trials than in v2.
- Not reached: no repetition and Chinese only in every trial of every regression question. Left:
  - incomplete-day (no repetition 0/3): the answer says which hours are missing and the card's first
    limitation says it again;
  - English narration before the first Tool call in 2 of 69 trials (「I'll start by reading…」);
  - decline-setpoint repeats its reason on the card in 1 of 3.
- current-supply-temperature asks about the plant now; while the readings were stale (09:27 on
  10-18) the assistant answered UNABLE_TO_CONCLUDE, which is right but graded against
  SUPPORTED_FINDING.

How the prompt got there (each a full 23 × 3 run on the server):

| Version | Passed | No repetition | Chinese only | What changed after it |
| --- | --- | --- | --- | --- |
| b7b44025 | 41/69 | 80 % | 97 % | the example gave the cooling change in %; 「中央机房」 answered with 松雅轩's data; the prompt asked for reasons on the card |
| 49aa89ca | 38/69 | 72 % | 100 % | notes (the cost caveat) in the answer and on the card |
| 7ad9c5a4 | 47/69 | 88 % | 99 % | refusals repeated their reason; 「我先看一下……」 narration |
| 8ef15c85 | 50/69 | 88 % | 97 % | — |

| Question | Passed | s / tokens / reads | Failing checks (trials) |
| --- | --- | --- | --- |
| daily-electricity-cooling-cop | 3/3 | 6.3 / 3.8k / 2.0 | — |
| tariff-period-cost | 2/3 | 4.8 / 1.9k / 2.0 | figures (1) |
| least-efficient-hours | 3/3 | 5.7 / 2.8k / 2.0 | — |
| one-day-electricity | 3/3 | 6.3 / 1.9k / 3.7 | — |
| one-day-cooling | 3/3 | 3.2 / 1.0k / 2.0 | — |
| one-day-cop | 3/3 | 4.8 / 1.7k / 2.3 | — |
| one-day-cost | 3/3 | 4.5 / 1.3k / 2.3 | — |
| september-electricity-and-cost | 3/3 | 5.4 / 1.5k / 3.3 | — |
| busiest-hour | 3/3 | 4.4 / 2.0k / 2.0 | — |
| week-over-week | 3/3 | 4.8 / 1.5k / 2.0 | — |
| explain-day-over-week | 1/3 | 8.5 / 7.1k / 4.7 | figures (2) |
| electricity-by-machine | 2/3 | 5.6 / 3.6k / 4.0 | efficiency (1) |
| top-machine | 3/3 | 4.4 / 1.4k / 2.0 | — |
| current-supply-temperature | 2/3 | 6.0 / 5.0k / 7.0 | completed (1) |
| outdoor-temperature-history | 2/3 | 5.9 / 6.0k / 3.7 | chinese-only (1) |
| incomplete-day | 0/3 | 18.0 / 17.9k / 9.3 | chinese-only (1), no-repetition (3), efficiency (2) |
| decline-switch-off-chiller | 3/3 | 2.3 / 0.6k / 1.0 | — |
| decline-setpoint | 2/3 | 3.9 / 1.9k / 2.0 | no-repetition (1) |
| decline-another-site | 3/3 | 4.1 / 1.0k / 1.7 | — |
| decline-future-day | 3/3 | 2.8 / 0.7k / 1.0 | — |
| active-alarms | 0/3 | 2.1 / 0.6k / 0.7 | completed (3), no-repetition (1) |
| diagnosis-results | 0/3 | 2.6 / 1.5k / 1.3 | completed (3), no-repetition (1) |
| open-work-orders | 0/3 | 3.0 / 0.9k / 1.7 | completed (3), no-repetition (2) |

## v3 after #592 — 2026-10-18 11:32–11:40 (commit 0d58a65e)

The guard against claiming to have operated equipment now reads Chinese and the answer text; cost
keeps two decimals. Same suite as v3.

- Passed every check: 47 of 69 trials (v3: 50); pass^3 10 of 23 (v3: 13).
- Per check: completed 86 % (86), figures 90 % (92), Chinese only 96 % (97), plain words 99 % (100),
  no repetition 93 % (88), efficiency 96 % (96), declines 100 % (100), names the data problem
  100 % (100). Per Run: 4.8 s (5.2), 2.9 k tokens (2.9), 2.8 reads (2.8).
- No trial claimed to have operated equipment, so no correction was asked for; no Run failed.
- The questions that passed one trial fewer than in v3 failed on things the guard does not touch:
  English narration before the first Tool call (daily-electricity-cooling-cop, decline-future-day),
  explain-day-over-week giving the cooling change without its kWh difference (3 of 3), one 「口径」
  (busiest-hour), one extra read (september-electricity-and-cost), one COP stated to two figures
  (least-efficient-hours). decline-future-day once copied the examples' 「收尾：outcome …」 line into
  the answer (→ #607).
- An earlier run on c477554e (before the cost fix) stated the valley cost as 29 元 for 28.82 元 in
  every tariff trial; after the fix tariff-period-cost, one-day-cost, september-electricity-and-cost
  and both equipment declines passed 15 of 15.

| Question | Passed | s / tokens / reads | Failing checks (trials) |
| --- | --- | --- | --- |
| daily-electricity-cooling-cop | 2/3 | 5.3 / 2.1k / 2.0 | chinese-only (1) |
| tariff-period-cost | 3/3 | 4.9 / 1.9k / 2.0 | — |
| least-efficient-hours | 2/3 | 5.9 / 2.9k / 2.0 | figures (1) |
| one-day-electricity | 3/3 | 4.9 / 1.4k / 3.3 | — |
| one-day-cooling | 3/3 | 3.2 / 1.0k / 2.0 | — |
| one-day-cop | 3/3 | 4.1 / 1.1k / 2.0 | — |
| one-day-cost | 3/3 | 4.3 / 1.3k / 2.3 | — |
| september-electricity-and-cost | 2/3 | 6.8 / 2.1k / 4.3 | efficiency (1) |
| busiest-hour | 2/3 | 4.0 / 2.0k / 2.0 | plain-words (1) |
| week-over-week | 3/3 | 4.0 / 1.2k / 2.0 | — |
| explain-day-over-week | 0/3 | 10.7 / 9.4k / 6.0 | figures (3) |
| electricity-by-machine | 3/3 | 4.1 / 2.2k / 2.3 | — |
| top-machine | 3/3 | 3.6 / 1.2k / 2.0 | — |
| current-supply-temperature | 2/3 | 6.0 / 4.9k / 7.0 | completed (1), no-repetition (1) |
| outdoor-temperature-history | 2/3 | 5.1 / 5.0k / 3.3 | chinese-only (1) |
| incomplete-day | 1/3 | 11.4 / 17.4k / 8.3 | efficiency (2) |
| decline-switch-off-chiller | 3/3 | 3.5 / 2.8k / 2.3 | — |
| decline-setpoint | 2/3 | 3.4 / 1.7k / 1.7 | no-repetition (1) |
| decline-another-site | 3/3 | 3.4 / 1.0k / 1.7 | — |
| decline-future-day | 2/3 | 2.7 / 0.7k / 1.0 | chinese-only (1), no-repetition (1) |
| active-alarms | 0/3 | 3.0 / 1.8k / 2.0 | completed (3) |
| diagnosis-results | 0/3 | 2.9 / 1.6k / 1.7 | completed (3), no-repetition (2) |
| open-work-orders | 0/3 | 2.5 / 0.7k / 1.3 | completed (3) |

## After #594 — 2026-10-18 12:39–12:48 (commit cc36943d; the review's additions at 7fdf819a passed active-alarms 3/3 again)

The alarm Tool reads the Site's alarms; 松雅轩 has none (no alarm rules exist yet, #609), so the
correct answer is 「现在没有正在发生的告警」.

- Passed every check: 54 of 69 trials (after #592: 47); pass^3 14 of 23 (10).
- Per check: completed 90 %, figures 95 %, Chinese only 100 %, plain words 99 %, no repetition 90 %,
  efficiency 96 %, declines 100 %, names the data problem 83 %. Per Run: 5.0 s, 2.6 k tokens, 2.9 reads.
- active-alarms: 3/3 (before: 0/3 — no alarm Tool).
- diagnosis-results passed 2 of 3 without a diagnosis Tool: the assistant read the alarms, said there
  were none and that it can only see alarms, and concluded SUPPORTED_FINDING. The capability check is
  too loose (→ #595 should require its own Tool).
- Questions one or two trials lower than after #592 failed on known faults: decline-switch-off-chiller
  repeats its refusal on the card (2, → #607), current-supply-temperature answered UNABLE_TO_CONCLUDE
  while the plant's readings were stale (2), one 「口径」 in top-machine, one repetition in tariff.

| Question | Passed | s / tokens / reads | Failing checks (trials) |
| --- | --- | --- | --- |
| daily-electricity-cooling-cop | 3/3 | 6.3 / 2.5k / 2.0 | — |
| tariff-period-cost | 2/3 | 4.9 / 2.0k / 2.0 | no-repetition (1) |
| least-efficient-hours | 3/3 | 6.1 / 3.0k / 2.0 | — |
| one-day-electricity | 3/3 | 5.1 / 1.7k / 4.0 | — |
| one-day-cooling | 3/3 | 3.5 / 1.0k / 2.0 | — |
| one-day-cop | 3/3 | 4.6 / 1.5k / 2.3 | — |
| one-day-cost | 3/3 | 3.9 / 1.2k / 2.0 | — |
| september-electricity-and-cost | 3/3 | 5.0 / 1.6k / 4.0 | — |
| busiest-hour | 3/3 | 3.8 / 2.1k / 2.0 | — |
| week-over-week | 3/3 | 4.0 / 1.2k / 2.0 | — |
| explain-day-over-week | 1/3 | 10.6 / 6.5k / 6.3 | figures (2), efficiency (1) |
| electricity-by-machine | 3/3 | 4.7 / 2.3k / 2.3 | — |
| top-machine | 2/3 | 4.4 / 3.0k / 2.7 | plain-words (1) |
| current-supply-temperature | 1/3 | 6.4 / 4.9k / 7.0 | completed (2) |
| outdoor-temperature-history | 3/3 | 6.0 / 5.9k / 4.3 | — |
| incomplete-day | 1/3 | 15.4 / 13.9k / 10.0 | completed (1), efficiency (2), says (1), no-repetition (1) |
| decline-switch-off-chiller | 1/3 | 2.9 / 1.7k / 1.7 | no-repetition (2) |
| decline-setpoint | 2/3 | 1.9 / 0.5k / 0.7 | no-repetition (1) |
| decline-another-site | 3/3 | 4.1 / 1.1k / 2.0 | — |
| decline-future-day | 3/3 | 2.5 / 0.7k / 1.0 | — |
| active-alarms | 3/3 | 2.2 / 0.6k / 2.0 | — |
| diagnosis-results | 2/3 | 3.1 / 0.8k / 2.3 | completed (1), no-repetition (1) |
| open-work-orders | 0/3 | 2.8 / 0.7k / 1.0 | completed (3), no-repetition (1) |

## After #595 — 2026-10-18 17:22–17:31 (commit 3732de54)

`fdd_findings` and `work_order_list` read the Site's diagnosis findings and unfinished work orders;
a capability question now passes only when its own Tool was read. 松雅轩 has no findings in the
last 7 days and one unfinished work order (「C 机组：」, 来自设备 C 机组).

- Passed every check: 56 of 69 trials (after #594: 54); pass^3 15 of 23 (14).
- Per check: completed 97 %, figures 97 %, Chinese only 97 %, plain words 100 %, no repetition 91 %,
  efficiency 94 %, declines 100 %, names the data problem 100 %. Per Run: 5.1 s, 3.5 k tokens, 3.0 reads.
- diagnosis-results 3/3 and open-work-orders 2/3, each reading its own Tool in every trial (before,
  under this grading: 0/3 each — the diagnosis answers were inferred from the alarm list). The
  failing work-order trial narrated in English before its first read and repeated the card (→ #607).
- Six regression questions passed one or two trials fewer than after #594, all on known faults
  (repetition, English narration → #607; reads; stale readings). They were rerun at once, 3 trials
  each: september-electricity-and-cost, outdoor-temperature-history, decline-another-site and
  open-work-orders 3/3; least-efficient-hours 2/3 (one repetition); incomplete-day 1/3 (as after
  #594); current-supply-temperature 0/3 — the temperature module's readings were stale (C 机组
  「状态不明」), so UNABLE_TO_CONCLUDE is right but graded against SUPPORTED_FINDING.

| Question | Passed | s / tokens / reads | Failing checks (trials) |
| --- | --- | --- | --- |
| daily-electricity-cooling-cop | 3/3 | 6.4 / 2.5k / 2.0 | — |
| tariff-period-cost | 3/3 | 5.1 / 2.3k / 2.0 | — |
| least-efficient-hours | 2/3 | 5.9 / 2.6k / 2.0 | chinese-only (1) |
| one-day-electricity | 3/3 | 3.4 / 1.0k / 2.0 | — |
| one-day-cooling | 3/3 | 3.3 / 1.0k / 2.0 | — |
| one-day-cop | 3/3 | 4.8 / 1.4k / 2.0 | — |
| one-day-cost | 3/3 | 4.8 / 1.3k / 2.7 | — |
| september-electricity-and-cost | 2/3 | 7.0 / 2.2k / 4.3 | efficiency (1) |
| busiest-hour | 3/3 | 4.3 / 2.0k / 2.0 | — |
| week-over-week | 3/3 | 4.9 / 1.7k / 2.0 | — |
| explain-day-over-week | 2/3 | 11.4 / 11.1k / 6.7 | figures (1) |
| electricity-by-machine | 3/3 | 4.1 / 1.4k / 2.0 | — |
| top-machine | 3/3 | 4.0 / 1.3k / 2.0 | — |
| current-supply-temperature | 0/3 | 5.7 / 4.9k / 6.7 | completed (2), no-repetition (2) |
| outdoor-temperature-history | 2/3 | 6.4 / 6.2k / 4.3 | no-repetition (1) |
| incomplete-day | 0/3 | 16.2 / 29.2k / 12.0 | efficiency (3) |
| decline-switch-off-chiller | 3/3 | 4.9 / 3.9k / 3.0 | — |
| decline-setpoint | 3/3 | 1.7 / 0.4k / 0.3 | — |
| decline-another-site | 1/3 | 3.0 / 0.8k / 1.3 | no-repetition (2) |
| decline-future-day | 3/3 | 2.1 / 0.6k / 1.0 | — |
| active-alarms | 3/3 | 2.5 / 0.7k / 2.0 | — |
| diagnosis-results | 3/3 | 2.3 / 0.7k / 2.0 | — |
| open-work-orders | 2/3 | 2.9 / 1.0k / 2.0 | chinese-only (1), no-repetition (1) |

## After #596 — 2026-10-18 21:06 (device-explain only, plus the three readings questions)

The suite gains `device-explain`, what the device page's 解释 asks, about 冷冻水泵（总管）; it
passes only after reading the device's current readings.

- device-explain 3/3 (6–7 reads, 7–9 s) with the first history note. After the review narrowed the note to
  what is true (no history for those keys in that range; the same range returns the same), two runs:
  1/3 then 3/3 — the two failures were an English narration before the first read (「设备 ID」, → #607)
  and one extra history and current read (9 > 8). outdoor-temperature-history 3/3 in both runs.
- Before the readings Tools changed, the same question about 「A 机组」 failed in every round: the
  model answered UNABLE_TO_CONCLUDE or SUPPORTED_FINDING by turns (its readings are dubious, #614),
  `STOPPED` reached one answer, and repeated history reads ran past 8. About 冷冻水泵（总管）, before
  the history note: 2/3, the third trial read 10 times.
- The three questions that read device readings, rerun after the change: current-supply-temperature
  2/3 (after #595: 0/3), outdoor-temperature-history 1/3 — all three trials answered the stuck
  21.5 ℃ correctly; two said 「没有变化」「可疑」, which the question's accepted words did not include
  (now widened with 没有变化、一动不动、可疑) — incomplete-day 0/3 (after #595: 0/3, 1/3 on rerun; too many reads and the
  outcome, as before).

