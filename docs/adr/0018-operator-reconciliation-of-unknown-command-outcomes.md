# ADR 0018 — A person settles an unknown command outcome, and that releases the Device

Status: accepted (amends ADR 0006)

Date: 2026-10-10

A command ends `OUTCOME_UNKNOWN` when the platform cannot prove what happened: the request may have reached the Device, or the reported state never confirmed it. ADR 0006 forbids retrying it blindly, and dispatch holds every later command for that Device behind it and freezes the Device's control group. Nothing could release the hold, so one stuck actuator or lost acknowledgement left the Device uncontrollable from the platform, STOP included (#444).

A person with control authority over the Device now settles the command with **Outcome Reconciliation**: `POST /api/v1/commands/{commandId}/reconcile` with `APPLIED` or `NOT_APPLIED`. The caller needs the same `COMMAND_SUBMIT` grant for the command's Device and Capability as submitting it, at the Capability's current revision, so a revision released while the outcome was unknown cannot lock the Device. One person settles it, without step-up, whatever the command's risk. The Intent moves from `OUTCOME_UNKNOWN` to `SUCCEEDED` or `FAILED`, recorded as a PRINCIPAL transition (`OPERATOR_RECONCILED_APPLIED` / `OPERATOR_RECONCILED_NOT_APPLIED`) with an audit intent, and the Attempt keeps `OUTCOME_UNKNOWN` as evidence. The Device's control group is then unfrozen. Dispatch runs one command per Device at a time, so a Device holds at most one unknown outcome. Commands queued behind it have usually expired by then, because their submission grants last 30 seconds; the next command submitted after reconciliation dispatches. We chose this because the person at the plant can see what the drive did, while the platform cannot, and because the hold exists to stop the platform acting on a guess, not to stop people acting.

## Considered Options

- **No hold at all, as ThingsBoard does** ([`RpcStatus.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/common/data/src/main/java/org/thingsboard/server/common/data/rpc/RpcStatus.java) at v4.4: TIMEOUT and EXPIRED are terminal and the next RPC is sent). Rejected: sending the next setpoint while the previous one's effect is unknown is what ADR 0006 prevents.
- **Resolve automatically from a later matching reported state.** Rejected: a later match does not show that this command caused it; local control or another actor could have.
- **Let STOP bypass the hold.** Deferred: it needs per-Capability dispatch ordering. With reconciliation the hold is one action away; this ADR does not add a bypass.
- **A dedicated approval-style permission with a second person.** Rejected for now: settling an outcome changes no setpoint, and the control authority already granted for the Device is the right scope.

OpenEMS and MyEMS have no unknown outcome to settle; see the [source review](../architecture/command-outcome-reconciliation-source-review-2026-10-10.md).

## Consequences

- The reconciliation is the reconciler's statement, not proof; the transition and audit name who made it.
- There is no UI yet, as for submit and approve; the action is the public API route.
