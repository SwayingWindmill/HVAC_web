# 05 告警中心 — Surface Brief

**Status: IMPLEMENTED / BROWSER REVIEWED**  
**Route intent:** `/sites/$siteId/alarms`

**Implementation evidence:** `npm run alarm:browser` validates the shadcn/TanStack triage ledger, same-page context Inspector, ACK/Assign Dialog workflows, authorized Rules surface, Overview/Device/Work Order handoffs, active-versus-cleared physical condition semantics, and absence of legacy Ant DOM. `npm run alarm:lifecycle:browser` validates the authoritative handling lifecycle without inventing manual Close/Reopen stages.

## User job
Triage authoritative Alarm facts, establish operator awareness/responsibility, investigate evidence and hand work into Diagnosis or Work Orders without falsifying recovery semantics.

## Questions
- Which active alarms deserve attention first?
- What is severity, age, source context and handling state?
- Has the alarm been acknowledged, assigned or suppressed?
- What evidence and related objects support investigation?
- Has the physical condition cleared automatically?

## Information hierarchy
```text
Compact alarm facts strip
Peer views: active / cleared / correlation / history / rules when authorized
Search + severity / handling / suppression / source filters
Alarm ledger
Context Inspector
  authoritative facts
  evidence
  diagnosis status
  acknowledgement / assignment / suppression
  related device / work order
  lifecycle timeline
```

## Interaction model
- Ledger-first; row selection opens same-page Inspector.
- Acknowledge and Assign are focused Dialog tasks.
- Cleared is a physical rule result; manual Close/Reopen is not invented.
- Correlation is not causality.
- Detailed diagnosis belongs to Diagnosis Center.

## Component mapping
TanStack Table v9, Input Group, Select, Badge, Checkbox only for supported batch capability, Dialog for short mutations, AlertDialog only for genuinely dangerous/irreversible actions.

## Truth rules
ACK = awareness, Assign = responsibility, Suppress = handling/notification behavior, CLEARED = physical recovery. These are independent facts.

## Browser acceptance
- Active triage dominates the page, not summary metrics.
- Handling state and physical condition are visually distinguishable.
- No raw Alarm/device/principal IDs in normal UI.
- A user can move from Alarm → Diagnosis / Device / Work Order while retaining source context.
