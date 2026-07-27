# HVAC Web Real Mode Shell Implementation Plan

## Status

Approved ticket breakdown derived from the accepted Q31–Q60 decisions and the Real Mode Shell specification. The eight tickets were published to GitHub on 2026-07-28 with the `ready-for-agent` label and native blocked-by relationships. RMS-01 has an implementation branch with separate Real and Demo build graphs, fail-closed Real startup validation, dependency and emitted-bundle audits, and a passing `npm run rms:ticket-01` gate; review and merge remain pending. RMS-02 has not started.

## Objective

Establish a trustworthy Real Mode application shell around the existing S1 Registry, S2 Telemetry and S3 Command capabilities without advancing S4–S7 or enabling production Command traffic.

The shell is complete when an operator enters through an opaque BFF Session, receives server-authored effective capabilities, selects or validates an authorized Site from the URL, and reaches only authorized Real routes. Demo modules are excluded from the Real artifact, and Site or Session transitions cannot leave stale protected state behind.

## Primary module seam

The implementation introduces one deep `ShellRuntime` module seam. React callers must not coordinate Principal, Session, effective capabilities, Registry Site discovery, realtime cleanup and cache purging independently.

The interface is expected to provide behavior equivalent to:

```text
bootstrap(currentUrl) -> ShellSnapshot
subscribe(listener) -> unsubscribe
prepareSiteNavigation(targetSiteId) -> navigation decision
logout() -> completed or retryable failure
purge(reason) -> completed
```

This is a decision-rich interface sketch, not a required TypeScript signature. The implementation may expose React adapters, but the core state and transition semantics remain behind one interface.

## Primary test seam

The highest test seam is a browser audit running a Real production build against a controlled same-origin Gateway fixture. The fixture varies Session, Principal, effective capability, Site collection, Problem Details and policy revision responses. Assertions observe routes, rendered states, redirects, network requests, storage and emitted bundle evidence.

Existing S0/S1/S2 browser-audit infrastructure is reused. Pure tests are limited to deterministic policy functions such as safe return-path normalization, Shell state reduction and route eligibility.

## Proposed tickets

### RMS-01 — Separate Real and Demo build graphs

**Blocked by:** None — can start immediately.

**What it delivers:** Two explicit build and deployment artifacts. Demo retains its fixture experience and visible Demo marker. Real starts from a separate entry graph and cannot import or dynamically load Mock business data, Demo stores, local role simulation or Mock AI.

**Acceptance criteria:**

- Real and Demo have explicit build commands and build identities.
- Real startup validates required same-origin Gateway and realtime configuration and fails closed on invalid configuration.
- Real has no runtime Demo switch.
- A static dependency audit rejects Real imports reachable from fixture, Demo store, Mock AI and Demo-only page modules.
- An emitted-chunk audit rejects known Mock business symbols and fixture payloads.
- Demo remains buildable and visibly marked as non-authoritative.
- Existing S1–S3 generated clients remain shared without copying projects.

### RMS-02 — Publish server-authored effective capabilities

**Blocked by:** None — can start immediately in parallel with RMS-01.

**What it delivers:** The authenticated Principal response carries an IAM-authored effective capability set and policy revision through IAM, Platform Gateway, OpenAPI and the generated browser client. Real UI can display the resolved authorization context without interpreting roles.

**Acceptance criteria:**

- IAM owns the effective capability decision and returns only capabilities valid for the current Principal and acting Organization context.
- Platform Gateway transports, but does not invent, the capability set.
- The public contract defines a bounded capability vocabulary or a versioned capability identifier contract with runtime validation.
- Generated Go and TypeScript clients are regenerated with no manual DTO copies.
- Principal responses remain strict and reject duplicate, malformed or unsupported capabilities.
- Roles remain present for display/audit but are not documented as authorization input for the SPA.
- A minimal Real diagnostic surface displays Principal, policy revision and capability count/list under authenticated conditions.
- Contract and backend tests cover allow, empty capability, policy revision and malformed-response cases.

### RMS-03 — Implement authenticated Shell bootstrap and logout

**Blocked by:** RMS-01 and RMS-02.

**What it delivers:** A Real operator is held in `BOOTSTRAPPING` until the opaque BFF Session and Principal snapshot are trustworthy. A 401 enters the approved OIDC login flow with a safe return path. Logout revokes the server Session before clearing protected UI state.

**Acceptance criteria:**

- Business routes and realtime subscriptions are not mounted during Principal bootstrap.
- Authentication-required and invalid-Session problems enter `LOGIN_REQUIRED` and create only normalized same-origin relative `returnTo` values.
- No SPA username/password form or automatic test account exists in Real.
- CSRF is held only in the in-memory Principal snapshot.
- No bearer token, refresh token, Principal payload or CSRF value is written to local/session storage, URL or logs.
- Logout calls the server operation with CSRF and only reports completion after a 204 or explicit already-invalid Session result.
- Retryable logout failure remains visible and does not falsely claim server revocation.
- Session expiration or revocation purges protected memory and stops route mounting.
- Browser tests cover first login, return path, failed bootstrap, logout success and logout failure.

### RMS-04 — Drive routes and menus from feature status and effective capability

**Blocked by:** RMS-03.

**What it delivers:** Real navigation is derived from Build Feature Manifest × Server Availability × Effective Capability. Local `demo/ops/rd` role rules no longer participate in Real route visibility or route guards. Accepted but backend-missing modules render a stable Not Integrated surface.

**Acceptance criteria:**

- Real has no local role switcher and no authorization decisions based on the Demo role map.
- Implemented and authorized features display normally.
- Implemented but unauthorized features are absent from navigation; direct URLs show Access Denied.
- Accepted but backend-missing features show Not Integrated when configured visible and never load their Demo implementation.
- Deployment-disabled features are hidden.
- Unknown routes remain 404.
- Access Denied does not reveal whether a protected business resource exists.
- `FORBIDDEN`, `NOT_INTEGRATED`, `UNAVAILABLE`, `DEGRADED` and empty business data are distinct rendered semantics.
- Browser tests exercise capability combinations and direct URL navigation.

### RMS-05 — Introduce authorized Site-scoped routing and chooser

**Blocked by:** RMS-04.

**What it delivers:** Site-level Real routes use explicit Registry UUIDv7 paths. The Shell validates the requested Site under the current acting Organization, automatically enters a sole authorized Site, presents a chooser for multiple Sites and shows a dedicated state for zero Sites.

**Acceptance criteria:**

- Site-level routes use `/sites/{siteId}/...`; platform-level status routes remain outside Site scope.
- The URL Site is validated through authorized Registry data before a Site page becomes `READY`.
- A single authorized Site is selected only when no explicit Site exists.
- Multiple authorized Sites show a chooser and do not silently select the first item.
- Zero authorized Sites show `NO_AUTHORIZED_SITE` with account, retry, help and logout actions.
- Invalid or invisible Site produces one safe state and never redirects to another Site automatically.
- First phase Site discovery is limited to the Principal `actingOrganizationId`; no browser header changes Organization context.
- Real no longer treats `b1`, `b2` or any local building alias as business identity.
- Site context is provided from validated route data, not an implicit global store getter.
- Existing Real Assets, Commands and shell-level BigScreen routes can resolve under the new Site path without changing their domain authority.

### RMS-06 — Purge scoped state on Site, Session and policy transitions

**Blocked by:** RMS-05.

**What it delivers:** Changing Site or losing the Session cannot mix old and new scope. `ShellRuntime` owns cancellation, realtime closure, Query cache removal, selected-resource clearing and policy-change handling.

**Acceptance criteria:**

- Site navigation first prepares the transition, warns about registered unsaved drafts, and then purges the old scope before the new Site becomes ready.
- Outstanding Site requests are aborted.
- S2 realtime sessions are closed and local live state is purged.
- Site-scoped Query cache, selected Device/Equipment and business temporary state are cleared.
- Non-sensitive visual preferences such as theme and sidebar layout remain.
- Session expiration, logout and material authorization-policy revision changes invoke the same protected-state purge semantics.
- A late response from the old Site cannot repopulate the new Site cache.
- Browser tests prove that old Site labels, values and subscriptions disappear before new Site data is rendered.
- Tests cover interrupted Site switching and non-retryable purge failure handling.

### RMS-07 — Deliver trusted Real shell chrome and global states

**Blocked by:** RMS-04, RMS-05 and RMS-06.

**What it delivers:** The complete desktop, tablet and mobile Real chrome shows authenticated identity, current Site, trustworthy Shell state and subscription-scoped realtime status. Mock global affordances are absent. Real BigScreen is Site-scoped.

**Acceptance criteria:**

- Header displays current Principal and Site from Shell snapshots.
- Header contains no Demo switch, local role switch, Mock Alarm bell or fixture Alarm count.
- Real App Shell does not mount Mock AI or Demo-only Copilot state.
- Realtime status distinguishes idle/not subscribed, connecting, live, reconnecting, resync required and unavailable; it does not claim global platform health.
- Real BigScreen uses `/sites/{siteId}/bigscreen` and cannot load without a validated Site.
- Shell state pages expose safe Problem detail, trace ID and retryability without secrets.
- Mobile Site chooser, Access Denied, Session Expired, No Authorized Site and Not Integrated states remain usable.
- Accessibility names, focus restoration and keyboard navigation pass browser audit.

### RMS-08 — Certify Real Mode Shell and publish release evidence

**Blocked by:** RMS-01 through RMS-07.

**What it delivers:** One repeatable gate proves the Real shell’s build isolation, authentication, capability, Site-scope, cleanup and failure behavior. The gate produces machine-readable evidence and does not claim S3 production certification or enable production traffic.

**Acceptance criteria:**

- One package command runs contract checks, generated-client diff checks, relevant Go tests, Real dependency audit, Real production build, TypeScript checking and browser audits.
- Browser evidence covers unauthenticated login, one/many/zero Sites, invalid Site, capability denial, not-integrated module, Site switching, session expiration, logout and mobile flows.
- Network evidence records zero browser-supplied auth, Organization or Site authority headers.
- Storage evidence records zero persisted token, CSRF, Principal, Registry, Telemetry or Command payloads.
- Bundle evidence records zero forbidden Demo/Mock modules and symbols.
- Failure evidence includes trace IDs and confirms no fixture fallback.
- Rollback documentation states that Real deployment rollback may select a previous Real artifact only; it cannot switch production users to Demo.
- The certification report explicitly keeps S3 production traffic percentage and formal certification claims unchanged.

## Blocking graph

```text
RMS-01 ─┐
        ├─> RMS-03 ─> RMS-04 ─> RMS-05 ─> RMS-06 ─┐
RMS-02 ─┘                    └──────────────────────┼─> RMS-07 ─> RMS-08
                                                   └─────────────^
```

More explicitly:

- RMS-01 and RMS-02 form the initial parallel frontier.
- RMS-03 requires both build isolation and the effective capability contract.
- RMS-04 requires authenticated bootstrap.
- RMS-05 requires the feature/capability route model.
- RMS-06 requires validated Site-scoped routing.
- RMS-07 requires route policy, Site context and purge semantics.
- RMS-08 requires every implementation ticket.

## Verification strategy

Each ticket receives a dedicated check command and CI workflow following the existing S1–S3 pattern. Ticket checks should compose prior checks rather than duplicate validation logic. RMS-08 provides the complete certification command.

Expected evidence categories:

- generated contract identity and diff-free regeneration;
- Real/Demo dependency-graph reports;
- Gateway fixture request log;
- browser state-transition report;
- browser storage inventory;
- realtime open/close/resync event log;
- Site-scope cache-purge assertions;
- final certification envelope with `formalProductionClaim: false` and unchanged Command traffic controls.

## Rollback

- Before RMS-08, each ticket must keep the last accepted Real artifact buildable.
- Real rollback changes only the selected Real artifact. It never enables Demo fallback or imports Demo chunks.
- Contract expansion is additive until all Real callers migrate; contract removal occurs only after the generated client and browser audits prove no old caller remains.
- Site route migration may temporarily preserve explicit redirect-only compatibility routes, but no compatibility route may restore implicit local Site state.
- Session or capability failures always fail closed during rollback.

## Out of scope

- Implementing Dashboard, Energy or Cost business read models.
- Building Alarm, Work Order, FDD, Optimize or AI backends.
- Enabling cross-Organization context switching.
- Changing S3 production routing or certification status.
- Publishing S4–S7 tickets.

## Published GitHub tickets

- RMS-01: #86 — Separate Real and Demo build graphs.
- RMS-02: #87 — Publish server-authored effective capabilities.
- RMS-03: #88 — Implement authenticated Shell bootstrap and logout.
- RMS-04: #89 — Drive routes and menus from feature status and effective capability.
- RMS-05: #90 — Introduce authorized Site-scoped routing and chooser.
- RMS-06: #91 — Purge scoped state on Site, Session and policy transitions.
- RMS-07: #92 — Deliver trusted Real shell chrome and global states.
- RMS-08: #93 — Certify Real Mode Shell and publish release evidence.

The current implementation frontier is #86 and #87. All later tickets remain blocked by their native GitHub dependency edges.
