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
