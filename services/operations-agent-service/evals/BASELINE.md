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
