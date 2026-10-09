# Issue 349 — Virtual Central Plant release acceptance

Date: 2026-10-09. Stack: the local WSL `hvac-local` deployment built from the #349 branch. Parent specification: #331.

## Criteria

| Criterion | Result | Evidence |
| --- | --- | --- |
| Live fault-to-maintenance acceptance is green with production APIs/frontend and durable recovery history | Met | #346 (closed): CHWP stuck-high → Alarm ACTIVE → FDD `CHILLED_WATER_LOW_DELTA_T` → ALARM-origin Work Order from the frontend → recovery with history retained ([record](issue-346-wsl-live-acceptance.md)); Site-local energy on the overview from projected facts (2026-10-09) |
| Deployed ATV630 real-TCP acceptance is green with the immutable released Device Template and production Bridge/DeviceAdapter | Met, with limits below | `node scripts/run-atv630-protocol-acceptance.mjs`, report `out/atv630-protocol-acceptance/report.json`, below |
| Historical Replay/intelligence acceptance is green without current-state mutation or downstream direct writes | Met | `node scripts/run-historical-replay-intelligence-acceptance.mjs` re-run on current main: status passed; eventTime `5\|5\|1\|1`, energyFacts `10,15,15,20`, metricFacts `760,790,800,820`, forecastPoints `96\|96`, currentTruth `2\|2\|2`, optimizationStore `SEALED\|true\|PUBLISHED` |
| Production deployment does not start Virtual Central Plant, Historical Replay or Virtual ATV630 and does not depend on their data | Met | Rendered service lists: default and `--integration --intelligence` contain no `eg8200`/`virtual`/`atv630`/`replay` service; those exist only in `deploy/acceptance/phase1-simulator.compose.yaml` behind `--simulator-acceptance` / `--atv630-protocol-acceptance` |
| No old-field, old-path, protocol-profile, register, fixture or simulator fallback | Met | The ATV630 Edge's `POST /acceptance/commands` back door, which submitted intents around Command Governance, was deleted; acceptance commands go through `POST /api/v1/commands` |
| Real Mode missing/unavailable data stays unavailable; production frontend contracts are the only business surfaces | Met | Overview at 1440 px shows 实际用电 from projected facts while savings, savings rate and cost stay 未接入 |
| Focused verification evidence per owner seam without speculative repository-wide gates | Met | Two release runners above (not PR gates); the Edge/Bridge/Adapter unit tests under `libs/edgecontrol` and `tools/eg8200-simulator` now run in the existing command gate profile |
| Real-hardware ATV630 Vendor Template Certification recorded as later hardware acceptance | Deferred (recorded) | This run proves protocol-level conformance against the Virtual ATV630 over real Modbus/TCP only; certification against a real ATV630 drive remains `DEFERRED_HARDWARE` ([simulator acceptance](phase1-simulator-acceptance.md#hardware-limitation)) |

## ATV630 deployed protocol path

The ATV630 Edge is its own Gateway (ADR 0015) on a dedicated acceptance Site (`atv630-protocol-acceptance`): one GATEWAY device and one drive device behind it with Device Source Key `CHWP-01`. The Edge enrolls its own Gateway Credential with an Enrollment Code issued through the platform API. The runner seeds the Site, issues the code, starts `virtual-atv630` and `atv630-edge`, and drives the drive through Command Governance as the local administrator, with a second local account (`local-approver`, role `command-approver` with `device.read` only, plus COMMAND_APPROVE on the drive) approving because Command Governance rejects self-approval.

Run 2026-10-09 12:34–12:36 UTC after #443, all steps passed (three consecutive runs passed):

| Step | Result |
| --- | --- |
| Edge ready | Gateway Credential enrolled; Modbus and MQTT ready; released TemplateRevision `01a11f50-9594-78f5-85f0-1e8db21d2c71` |
| Public telemetry fresh | `run_state` RUNNING, `frequency` 50 Hz via `GET /api/v1/devices/{id}/observation-snapshot` |
| SET_FREQUENCY 48 Hz | LOW risk, no approval, SUCCEEDED; Virtual ATV630 drive at 48 Hz |
| SET_FREQUENCY 45 Hz | MEDIUM risk, single approver, SUCCEEDED; drive at 45 Hz |
| STOP | MEDIUM risk, approved, SUCCEEDED; telemetry STOPPED |
| START | MEDIUM risk, approved, SUCCEEDED; telemetry RUNNING |
| Stuck-high while running | governed reference 45 Hz; drive and telemetry at 50 Hz |
| Fault 16 then RESET_FAULT | telemetry FAULT / `16`; RESET_FAULT LOW risk SUCCEEDED; fault cleared |
| Modbus outage | stopping `virtual-atv630` made the Edge report Modbus down; restarting it restored Modbus without fallback |

The first run of this record (07:23 UTC) placed stuck-high after STOP and reported the drive "rising" to 27.3 Hz. That was the coast-down from 45 Hz under the old 20 s first-order pump model, not the disturbance: stuck-high only acts while the pump is requested to run (#334). The step now runs after START and checks the drive leaves its governed reference.

Frequency setpoints are lease-bound: an expired cloud intent returns the drive to local control (ADR 0012), so the runner samples the drive while each command is live.

Limits of this evidence:

- "Drive" values are the Virtual ATV630's own state from its diagnostics endpoint, independent of the Edge and the platform. The Edge itself reaches the drive only over Modbus/TCP, so the commanded values arriving there prove the Bridge and DeviceAdapter wrote the registers.
- The Edge runs the code adapter built from `edgecontrol.ATV630ProtocolReleaseCandidate()`, the same mapping `atv630-template-release` released as the immutable revision; the Edge loads and reports that revision ID but does not read the register map from Registry, and the drive Device carries no template assignment.
- Polling and the semantic Process Image are shown through FRESH public telemetry and the commanded run states, not asserted separately.
- Only the network failure (Modbus outage) is scripted. Identity failure was observed while fixing the profile (an Edge without a Gateway Credential kept failing enrollment and never published), but it is not part of the runner.

## Defects found and fixed during acceptance

- **Command approval never worked after a real sign-in.** Sign-in stores no roles on the BFF Session, and approval required a role from the Session. Approval now reads the approver's current roles from IAM.
- **An unapproved command blocked its Device forever.** Dispatch holds later commands for a Device behind an earlier unfinished one, and AWAITING_APPROVAL never expired, so one unapproved command blocked even STOP. Commands now have a five-minute approval window; later approval is rejected and dispatch expires them.
- **The ATV630 acceptance profile could not run on current main.** The Edge did not enroll a Gateway Credential, shared Site A's MQTT config, was missing the application network and enrollment CA, wrote its template revision into a read-only runtime directory, and published diagnostics ports on internal networks that Docker never exposes. All are fixed in the profile and the Edge.

## Open

- Command verification latency (#443) is fixed: a frequency command now verifies 1–3 s after acknowledgement ([source review](../architecture/command-verification-latency-source-review-2026-10-09.md)). A verification still waiting after 12 s is logged as a failed pass; that is recorded there as deferred.
- An `OUTCOME_UNKNOWN` command blocks every later command for its Device with no way to resolve it (#444). This is why the stuck-high step sends no command.
- Once, before the clean runs above, the MEDIUM 45 Hz command ended `OUTCOME_UNKNOWN` although the drive reached 45 Hz. It followed manual unblocking of the acceptance drive in `hvac_s3` and did not recur in eleven later commands and runs; the cause is not established (noted on #444).
- The projector's candidate query memory growth (#441) is fixed: it reads from a History Sequence checkpoint with bounded counter reads ([source review](../architecture/energy-projection-checkpoint-source-review-2026-10-09.md)).
