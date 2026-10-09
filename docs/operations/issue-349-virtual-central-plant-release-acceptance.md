# Issue 349 — Virtual Central Plant release acceptance

Date: 2026-10-09. Stack: the local WSL `hvac-local` deployment built from the #349 branch. Parent specification: #331.

## Criteria

| Criterion | Result | Evidence |
| --- | --- | --- |
| Live fault-to-maintenance acceptance is green with production APIs/frontend and durable recovery history | Met | #346 (closed): CHWP stuck-high → Alarm ACTIVE → FDD `CHILLED_WATER_LOW_DELTA_T` → ALARM-origin Work Order from the frontend → recovery with history retained ([record](issue-346-wsl-live-acceptance.md)); Site-local energy on the overview from projected facts (2026-10-09) |
| Deployed ATV630 real-TCP acceptance is green with the immutable released Device Template and production Bridge/DeviceAdapter | Met | `node scripts/run-atv630-protocol-acceptance.mjs`, report `out/atv630-protocol-acceptance/report.json`, below |
| Historical Replay/intelligence acceptance is green without current-state mutation or downstream direct writes | Met | `node scripts/run-historical-replay-intelligence-acceptance.mjs` re-run on current main: status passed; eventTime `5\|5\|1\|1`, energyFacts `10,15,15,20`, metricFacts `760,790,800,820`, forecastPoints `96\|96`, currentTruth `2\|2\|2`, optimizationStore `SEALED\|true\|PUBLISHED` |
| Production deployment does not start Virtual Central Plant, Historical Replay or Virtual ATV630 and does not depend on their data | Met | Rendered service lists: default and `--integration --intelligence` contain no `eg8200`/`virtual`/`atv630`/`replay` service; those exist only in `deploy/acceptance/phase1-simulator.compose.yaml` behind `--simulator-acceptance` / `--atv630-protocol-acceptance` |
| No old-field, old-path, protocol-profile, register, fixture or simulator fallback | Met | The ATV630 Edge's `POST /acceptance/commands` back door, which submitted intents around Command Governance, was deleted; acceptance commands go through `POST /api/v1/commands` |
| Real Mode missing/unavailable data stays unavailable; production frontend contracts are the only business surfaces | Met | Overview at 1440 px shows 实际用电 from projected facts while savings, savings rate and cost stay 未接入 |
| Focused verification evidence per owner seam without speculative repository-wide gates | Met | Two release runners above (not PR gates); the Edge/Bridge/Adapter unit tests under `libs/edgecontrol` and `tools/eg8200-simulator` now run in the existing command gate profile |
| Real-hardware ATV630 Vendor Template Certification recorded as later hardware acceptance | Deferred (recorded) | This run proves protocol-level conformance against the Virtual ATV630 over real Modbus/TCP only; certification against a real ATV630 drive remains `DEFERRED_HARDWARE` ([simulator acceptance](phase1-simulator-acceptance.md#hardware-limitation)) |

## ATV630 deployed protocol path

The ATV630 Edge is its own Gateway (ADR 0015) on a dedicated acceptance Site (`atv630-protocol-acceptance`): one GATEWAY device and one drive device behind it with Device Source Key `CHWP-01`. The Edge enrolls its own Gateway Credential with an Enrollment Code issued through the platform API. The runner seeds the Site, issues the code, starts `virtual-atv630` and `atv630-edge`, and drives the drive through Command Governance as the local administrator, with a second local account (`local-approver`) approving because Command Governance rejects self-approval.

Run 2026-10-09 07:10–07:14 UTC, all steps passed:

| Step | Result |
| --- | --- |
| Edge ready | Gateway Credential enrolled; Modbus and MQTT ready; released TemplateRevision `01a11f50-9594-78f5-85f0-1e8db21d2c71` |
| Public telemetry fresh | `run_state` RUNNING, `frequency` 50 Hz via `GET /api/v1/devices/{id}/observation-snapshot` |
| SET_FREQUENCY 48 Hz | LOW risk, no approval, SUCCEEDED; independent Modbus readback 48.47 Hz |
| SET_FREQUENCY 45 Hz | MEDIUM risk, single approver, SUCCEEDED; Modbus readback 45.45 Hz |
| STOP | MEDIUM risk, approved, SUCCEEDED; telemetry STOPPED |
| Stuck-high while stopped | Modbus readback rose to 28.7 Hz; telemetry frequency 30.2 Hz |
| START | MEDIUM risk, approved, SUCCEEDED; telemetry RUNNING |
| Fault 16 then RESET_FAULT | telemetry FAULT / `16`; RESET_FAULT LOW risk SUCCEEDED; fault cleared |
| Modbus outage | stopping `virtual-atv630` made the Edge report Modbus down; restarting it restored Modbus without fallback |

Frequency setpoints are lease-bound: an expired cloud intent returns the drive to local control (ADR 0012), so the runner samples the Modbus readback while each command is live.

## Defects found and fixed during acceptance

- **Command approval never worked after a real sign-in.** Sign-in stores no roles on the BFF Session, and approval required a role from the Session. Approval now reads the approver's current roles from IAM.
- **An unapproved command blocked its Device forever.** Dispatch holds later commands for a Device behind an earlier unfinished one, and AWAITING_APPROVAL never expired, so one unapproved command blocked even STOP. Commands now have a five-minute approval window; later approval is rejected and dispatch expires them.
- **The ATV630 acceptance profile could not run on current main.** The Edge did not enroll a Gateway Credential, shared Site A's MQTT config, was missing the application network and enrollment CA, wrote its template revision into a read-only runtime directory, and published diagnostics ports on internal networks that Docker never exposes. All are fixed in the profile and the Edge.

## Open

- Command result verification in connectivity times out (`context deadline exceeded`, 12 s per pass) and retries with backoff, so reported-state verification took about 75 s per command in this run.
- The projector's candidate query memory growth is tracked in #441.
