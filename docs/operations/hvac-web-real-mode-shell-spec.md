# HVAC Web Real Mode Shell Spec

## Status

Approved design synthesis based on the accepted Real Mode decisions through Q60. This specification does not enable production Command traffic and does not advance S4–S7.

## Problem Statement

HVAC Web currently mixes authoritative S1–S3 platform data with local roles, local Site aliases, Mock alarms, Mock AI, deterministic fixtures and browser-owned business state. Although individual Registry, Telemetry and Command paths can operate in Real Mode, the application shell does not yet provide a trustworthy authenticated boundary around them.

An operator can currently encounter local role switching, a runtime Demo switch, an alarm count sourced from fixtures, a globally mounted Mock assistant and Site state represented by either Registry UUIDv7 or local aliases. Principal loading, authorization, Site selection, session invalidation and realtime cleanup are distributed across multiple callers. This makes it difficult to prove that a Real deployment contains no fabricated operational facts or stale cross-Site state.

## Solution

Deliver a dedicated Real Mode application shell as a separate build and deployment from Demo Mode. The Real shell will bootstrap an opaque BFF Session, obtain the authenticated Principal and server-authored effective capabilities, resolve one explicitly authorized Site scope, and only then mount Site-scoped business routes.

The shell will expose a single deep `ShellRuntime` interface to the React application. Its implementation will coordinate generated Gateway clients, Principal and Session state, authorized Registry navigation, deployment feature status, route normalization, Site-scoped cache ownership, realtime lifecycle and logout. Callers will consume a small state model instead of independently recreating authentication or Site rules.

The Real build will fail closed. Missing configuration, missing capabilities, invalid Site scope, session loss, service unavailability and not-yet-integrated product modules will remain visibly distinct. No Real failure path will import or render Mock data.

## User Stories

1. As an unauthenticated operator, I want the application to begin the approved OIDC login flow, so that credentials are never collected by the SPA.
2. As an operator returning from login, I want to return to the same safe in-application path, so that authentication does not discard my intended destination.
3. As a security owner, I want login return targets restricted to normalized same-origin paths, so that the product cannot become an open redirector.
4. As an authenticated operator, I want the shell to resolve my Principal before showing business pages, so that unauthorized content never flashes on screen.
5. As an authenticated operator, I want to see my real display name, roles and policy revision, so that I can understand the identity context in use.
6. As an authorization owner, I want menus and route guards driven by server-authored effective capabilities, so that local role mappings cannot grant access.
7. As an operator without a capability, I want a consistent Access Denied state, so that I understand the restriction without learning whether hidden resources exist.
8. As an operator with one authorized Site, I want the shell to enter that Site automatically, so that routine entry is efficient.
9. As an operator with several authorized Sites, I want to choose the Site explicitly, so that the shell never silently changes my operating scope.
10. As an operator with no authorized Site, I want a dedicated state with account, retry, help and logout actions, so that an empty dashboard is not mistaken for a healthy Site.
11. As an operator following a Site-scoped link, I want the URL to preserve the Site UUIDv7, so that refresh, sharing and browser history restore the same explicit scope.
12. As an operator opening an invalid or invisible Site URL, I want a safe Site-not-visible state, so that the application does not silently redirect me to another Site.
13. As an operator switching Site, I want old requests, realtime sessions, selected Devices and Site-scoped caches removed, so that data from two Sites cannot be mixed.
14. As an operator with an unsaved form, I want a warning before a Site change discards it, so that scope safety does not unexpectedly destroy work.
15. As an operator whose Session expires, I want protected data and realtime activity cleared immediately, so that stale data cannot remain actionable.
16. As an operator logging out, I want the server Session revoked before the UI claims success, so that local cleanup is not confused with server-side logout.
17. As a security owner, I want CSRF capability held only in memory, so that it is not persisted in browser storage or logs.
18. As a deployment owner, I want Real and Demo to be separate artifacts, so that Demo fixtures cannot accidentally reach a Real environment.
19. As a deployment owner, I want invalid Real configuration to block startup, so that the application does not guess unsafe defaults.
20. As an operator in Demo, I want a persistent visible Demo marker, so that fixture data is never mistaken for production state.
21. As an operator in Real, I do not want a Demo switch or local role switch, so that there is no suggestion that authority can be changed in the browser.
22. As an operator in Real, I do not want a fixture alarm count, so that absence of a real Alarm service is not represented as zero alarms.
23. As an operator in Real, I do not want a Mock AI assistant mounted, so that generated text is not mistaken for a platform investigation.
24. As an operator, I want “not integrated”, “forbidden”, “unavailable”, “degraded” and “no data” to be different states, so that I can respond appropriately.
25. As a support engineer, I want safe error detail, trace ID and retryability displayed, so that failures can be diagnosed without exposing secrets.
26. As an operator, I want realtime status to describe the current subscription rather than a global green indicator, so that connection state is not overstated.
27. As an operator entering BigScreen in Real, I want it scoped to the current Site and shared read models, so that it cannot display an unrelated static facility.
28. As a product owner, I want not-yet-integrated modules visible as explicit product placeholders when configured, so that roadmap visibility does not fabricate functionality.
29. As a maintainer, I want one shell runtime seam, so that authentication, Site and cleanup behavior is fixed once rather than spread across pages.
30. As a release owner, I want automated evidence that the Real bundle contains no Mock business modules, so that the data-authority decision is enforceable.

## Implementation Decisions

- Real Mode and Demo Mode use distinct entry points, dependency graphs, build commands and deployment artifacts. Pure presentation utilities may be shared; Demo fixtures, Demo stores, local role simulation and Mock AI are unreachable from the Real entry graph.
- A build-time feature manifest declares whether a route is implemented, not integrated or hidden for that deployment. It does not authorize users and does not contain business data.
- Real startup validates build mode, same-origin Gateway configuration, supported realtime protocol and build identity. Invalid configuration produces a blocking Shell state.
- Browser authentication uses only the Gateway/BFF opaque `HttpOnly` Session Cookie. Bearer access tokens and refresh tokens are never available to SPA code.
- The Shell first reads the current Principal. Authentication-required responses produce a same-origin login redirect with a normalized relative return path.
- The Principal contract is extended with a server-authored effective capability set and its policy revision. Roles remain descriptive context and are not interpreted by the Real SPA as permissions.
- Effective capabilities are authored by IAM and transported through Platform Gateway in the generated public contract. The Gateway does not invent a separate browser role policy.
- The Real shell state model is: `BOOTSTRAPPING`, `LOGIN_REQUIRED`, `NO_AUTHORIZED_SITE`, `FORBIDDEN`, `NOT_INTEGRATED`, `UNAVAILABLE`, `DEGRADED`, `READY`.
- Business routes are not mounted until Principal bootstrap and the required Site context are trustworthy.
- Site-scoped routes use `/sites/{siteId}/...`. Platform-level routes, such as system status, remain outside a Site path.
- The explicit URL Site wins. With no explicit Site, a single authorized Site may be selected automatically; multiple authorized Sites require a chooser; zero authorized Sites produce a dedicated state.
- The first phase only lists Sites owned by the current `actingOrganizationId`. Cross-Organization context switching remains unavailable until a dedicated server operation exists.
- The `SiteContext` interface contains the validated Registry Site and owning Organization context derived from the route. It is read-only and cannot be supplied by a browser header or local alias.
- Site transitions cancel old requests, close realtime sessions and purge Site-scoped caches before the new Site becomes `READY`.
- Principal, capability, policy revision, Session and CSRF state remain in memory. Only non-sensitive visual preferences may be persisted.
- Session expiration, revocation, logout completion and material policy revision changes purge protected application state.
- Menu visibility is the intersection of build feature status, current server availability and effective capability. Direct URL access is guarded independently from menu visibility.
- Product modules that are accepted but lack an authoritative backend use a stable Not Integrated surface. They do not mount their Demo implementation in Real Mode.
- The Real header has no runtime Demo switch, local role switch, Mock Alarm bell or Mock AI assistant.
- Realtime status is subscription-scoped and may express idle, connecting, live, reconnecting, resync-required and unavailable states.
- BigScreen is a Site-scoped presentation surface and consumes the same authoritative read models as the normal Site UI.
- `ShellRuntime` is the primary module seam. Its interface covers bootstrap, current snapshot, subscription to shell transitions, Site navigation preparation, logout and purge. The implementation hides generated clients, request cancellation, cache ownership and realtime cleanup.

## Testing Decisions

- The primary test seam is browser-observable Shell behavior driven by a controlled same-origin Gateway fixture. Tests interact through rendered navigation and network behavior rather than internal React components.
- Existing browser audit infrastructure is extended instead of adding a new browser framework. The fixture can vary authentication, Principal, effective capabilities, Site collections, policy revision and Problem Details.
- Contract tests verify that IAM, Gateway, OpenAPI and generated TypeScript clients agree on effective capability semantics.
- Pure unit tests cover path normalization, safe `returnTo`, feature/capability route decisions and shell state reduction.
- Browser tests cover unauthenticated login redirect, bootstrap blocking, logout, session expiration, one/many/zero Sites, invalid Site, capability denial, not-integrated routes, Site switching and mobile navigation.
- Network assertions prove that no browser-supplied authorization, Organization or Site headers are used and that no token or CSRF capability is persisted.
- Build audits inspect the Real dependency graph and emitted chunks for forbidden Demo and Mock modules.
- Tests verify externally visible behavior and owner contracts; they do not assert component implementation details or internal hook names.
- The highest acceptance seam is one Real Shell certification command that runs contract generation checks, backend tests, build isolation checks, type checking, production build and browser audits.

## Out of Scope

- Dashboard metric realisation beyond the shell states and route frame.
- Energy and Cost read models.
- Expansion of S3 Command production traffic or formal certification claims.
- Alarm, Work Order, FDD, Optimization and AI Investigation backends.
- Cross-Organization acting-context switching.
- Offline business operation or persistent business response caches.
- User, role or policy administration UI.
- S4 Schedule, S5 AI Investigation, S6 Recommendation execution and S7 production cohort work.

## Further Notes

- Existing S1 Registry, S2 Telemetry and S3 Command pages remain the first authoritative consumers placed behind this shell.
- The specification deliberately separates product availability from authorization: an unavailable or not-integrated feature is not equivalent to a denied feature.
- A future server-side Shell Bootstrap BFF response may reduce request count, but it must preserve IAM and Registry ownership rather than becoming a new authority for identity or Site data.
