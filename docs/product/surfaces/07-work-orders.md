# 07 工单中心 — Surface Brief

**Status: DESIGN REVIEW**  
**Route intent:** `/sites/$siteId/work-orders`

**Review evidence:** `scripts/fixtures/remaining-pages-review` contains the new Product IA v2 ledger-first design review surface. It is browser-rendered for layout and interaction review, but is not yet the production Work Order surface or production-data integration.

## User job
Turn detected problems into owned work, track SLA and execution, and verify that completed work actually resolved the intended issue.

## Questions
- Which work is urgent, overdue, blocked or unowned?
- What source problem created the work?
- Who owns it and what is the next required action?
- What is due when?
- What field/action evidence exists?
- Is the result verified, not merely marked complete?

## Information hierarchy
```text
Compact work facts strip
Search + status / priority / owner / source / SLA filters
Work-order ledger
  work / source / priority / state / owner / SLA / next action / due
Context Inspector
  problem statement
  source evidence
  responsibility and SLA
  tasks / progress
  evidence / updates
  verification result
Durable detail route for sustained work
```

## Interaction model
- Ledger-first for daily operations.
- Creating/assigning work is a focused task; complex work configuration gets its own route if needed.
- “Completed” and “verified resolved” are distinct facts.
- Source Alarm/Diagnosis/Opportunity remains linked and understandable in business language.
- Do not close physical Alarm state as a side effect of Work Order completion.

## Component mapping
TanStack Table v9, Input Group, Select/Combobox filters, Badge, Progress only for real task completion data, Dialog for small assignments/edits, durable route for full detail.

## URL state
Filters, owner, source type, SLA state, sort and page are shareable. Full work identity belongs in route.

## Browser acceptance
- Urgent/overdue/unowned work is easier to find than normal work.
- “Next action” is more prominent than internal workflow metadata.
- Source and verification evidence are understandable without exposing technical IDs.
- Page density supports repeated scanning during a shift.
