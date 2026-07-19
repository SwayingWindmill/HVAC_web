# S0 — Platform Contract & Delivery Foundation

Status: ready-for-agent

Source architecture: `../go-data-ai-platform/map.md`

## Problem Statement

当前仓库拥有 HVAC Web、NestJS Legacy 后端、Copilot Runtime 和 EnergyAgent，但尚不存在可承载目标架构的 Go 平台运行骨架。浏览器仍可能依赖旧端口、旧响应包和 Legacy 身份语义；服务间没有统一的 Workload Identity、委托上下文、公共契约生成、事务 Outbox/Inbox、不可变 Audit 入口和端到端 Trace；NestJS 也尚未被放到受控 Route Ownership 后方。

在开发 Organization、Telemetry 或 Command 业务之前，必须先建立一个最小但真实的交付闭环，证明以下基础假设能够共同工作：浏览器只访问 Gateway；OIDC/BFF Session 能形成可信 Principal Context；Gateway 到内部服务使用独立 Workload Identity；业务事务可以原子产生 Outbox 与 Audit Intent；Kafka 至少一次交付不会制造重复业务结果；Legacy 路由可以被显式代理和撤销；整个链路可以被观测、部署、回滚和安全验证。

## Solution

交付一个端到端可运行的 S0 平台基础切片：

1. 建立可独立构建的 Go workspace 和多二进制服务结构。
2. 建立唯一公开 `platform-gateway`，公开 `/api/v1`、统一 Problem Details、Trace Header 和版本信息。
3. 建立可重复测试的 OIDC/BFF Session，并通过 mTLS 调用内部 IAM 服务返回当前 Principal。
4. 在 Session 建立或安全事件发生时，原子写入业务状态、Audit Intent 与 Outbox，经 Kafka API 兼容 Control Backbone 投递，由 Audit Ledger 通过 Inbox 幂等消费。
5. 建立版本化 Route Ownership Registry 和 Data Ownership Registry，使未迁移路由只能通过 Gateway 进入私网 NestJS，并在配置冲突时 Fail Closed。
6. 用 OpenTelemetry 将浏览器请求、Gateway、内部 RPC、PostgreSQL、Outbox、Kafka、Inbox 和 Audit 关联起来。
7. 建立可重复启动的 local/test/staging 交付方式、签名镜像、SBOM、安全扫描、契约兼容检查和回滚门禁。
8. 通过黑盒 API 和浏览器测试提交完整 Release Evidence Bundle，证明 S0 可以作为 S1–S7 的统一基础。

最高测试接缝固定为 Gateway 公开契约。关键闭环必须从浏览器或 HTTP Client 发起，通过正式身份与 Gateway，最终验证持久状态、异步事件和 Audit 结果。

## User Stories

1. As an HVAC Web user, I want every platform request to use one Gateway URL, so that I never need to know whether the implementation is Go or NestJS.
2. As an authenticated user, I want the browser to hold only a secure opaque session cookie, so that platform access tokens are not exposed to JavaScript storage.
3. As an authenticated user, I want to view my current principal and acting Organization through a stable `/api/v1` response, so that the UI can establish trusted context.
4. As a user whose session has been revoked, I want subsequent API access to fail promptly, so that stale browser sessions cannot continue accessing data.
5. As a platform operator, I want invalid issuer, audience, token type, expiry and signature cases rejected consistently, so that all services share one authentication baseline.
6. As a service owner, I want every internal call to identify the executing workload separately from the initiating user, so that authorization and audit record both principals.
7. As a service owner, I want delegation grants to be audience-, action-, scope- and expiry-bound, so that downstream services cannot expand user authority.
8. As a security engineer, I want direct browser calls to internal services to fail, so that private service boundaries are enforceable.
9. As an API consumer, I want generated clients and stable Problem Details, so that clients do not depend on inconsistent response envelopes or error text.
10. As a frontend developer, I want contract generation to fail CI when generated artifacts drift, so that handwritten protocol copies do not emerge.
11. As a backend developer, I want a standard transaction helper for state, Outbox and Audit Intent, so that accepted writes cannot silently omit events or audit.
12. As an event consumer, I want Inbox idempotency and aggregate-version checks, so that at-least-once delivery does not create duplicate effects.
13. As an auditor, I want a successful session event to appear in Audit Ledger with initiating and executing principals, so that the identity chain is traceable.
14. As an auditor, I want audit payloads to exclude tokens, cookies and secrets, so that compliance records do not become a credential store.
15. As a migration owner, I want each route to have a versioned owner, so that Go and Legacy do not both process the same request unpredictably.
16. As a migration owner, I want unknown or conflicting route ownership to fail closed, so that traffic is not randomly routed during configuration errors.
17. As a migration owner, I want Legacy requests to pass only through the anti-corruption boundary, so that old IDs and response shapes do not enter new services.
18. As a data owner, I want every schema, event family and projection to have one declared writer, so that shared write ownership is detected before implementation.
19. As an operator, I want a trace to connect the browser request to internal service calls, Outbox, Kafka, Inbox and Audit, so that asynchronous failures can be diagnosed.
20. As an operator, I want observability backend outages not to block user traffic, so that telemetry is not a hidden synchronous dependency.
21. As a developer, I want one documented local command to start the S0 dependencies and services, so that the delivery loop is reproducible.
22. As a release engineer, I want immutable signed images and SBOMs, so that staging and production run verified artifacts.
23. As a release reviewer, I want contract, security, tenant, event, failure and rollback evidence collected in one bundle, so that approval is reproducible.
24. As a platform owner, I want S0 to remain free of Organization, Telemetry, Command and AI business implementations, so that the foundation does not become a premature monolith.
25. As a NestJS maintainer, I want Legacy to remain operational behind Gateway while frozen for new features, so that migration can proceed without a big-bang cutover.
26. As a future slice owner, I want reusable libraries for identity context, Problem Details, Outbox/Inbox, audit and tracing, so that S1–S7 use the same invariants.
27. As a security tester, I want forged identity headers, invalid mTLS identities and wrong grant audiences rejected through public tests, so that internal trust is not based on network location.
28. As an SRE, I want broker restart, duplicate delivery and service restart tests to converge without lost accepted events, so that the delivery foundation is safe under routine failure.

## Implementation Decisions

- S0 creates the Go workspace and minimum production-shaped binaries required to prove the platform seam: `platform-gateway`, `iam-service`, `audit-ledger-service`, and required Outbox/Audit relay workers. Additional domain binaries are deferred.
- Gateway is the only public ingress. It owns edge authentication, BFF Session, CSRF/Origin enforcement, protocol normalization, trace creation and route selection. It does not own Organization, Device, Command, Telemetry or AI business state.
- Public API base is `/api/v1`. Successful resources are typed; errors use `application/problem+json` with stable `code`, `traceId`, `retryable` and optional field errors. No global success envelope is introduced.
- OpenAPI is the public HTTP contract authority. Internal service contracts use Protobuf/gRPC where appropriate. Generated Go and TypeScript types and runtime validators are checked in and regenerated by locked tooling.
- Initial public behavior is a health/version contract plus an authenticated current-principal contract. The latter traverses Gateway and internal IAM rather than being synthesized solely from client claims.
- Browser authentication uses OIDC Authorization Code Flow with PKCE and a server-side BFF Session. Development and test may use a deterministic local OIDC provider, but validation behavior must match production expectations.
- Browser receives only an opaque `HttpOnly`, `Secure` and suitable `SameSite` cookie. Access Token, Refresh Token and ID Token remain encrypted server-side and never enter localStorage, URL parameters or Agent context.
- Internal calls use mTLS Workload Identity. Executing service identity and initiating user are separate. A short-lived delegation grant carries acting Organization, allowed actions, resource scope, audience, policy revision, expiry and unique token ID.
- Client-supplied principal, role, Organization and Site headers are ignored or rejected. Gateway identity context is accepted only over authenticated internal transport and remains subject to final authorization by the data owner.
- Session creation, revocation and security-relevant changes are durable state transitions. The owning transaction writes Session state, Audit Intent and Outbox atomically.
- Control Backbone uses a Kafka API compatible broker. Application behavior depends only on Kafka API, Protobuf schemas, majority-safe acknowledgement configuration, idempotent production and consumer groups.
- Outbox publication is at-least-once. Consumers use Transactional Inbox, stable message IDs and aggregate versions. Duplicate messages produce no duplicate audit entry or downstream effect.
- Audit Ledger is append-only. S0 proves ingestion and query of a minimal security event; full WORM archival and seven-year production retention remain later infrastructure work. The event already includes actor chain, scope, action, result, policy version, correlation and payload hash without secrets.
- Route Ownership Registry is versioned and used by Gateway. Each route declares method/path, owner, allowed scope dimensions, legacy compatibility mode, rollout policy and revision. Conflicting or missing ownership fails closed.
- Data Ownership Registry declares the unique writer for each introduced database schema, event family, object namespace and projection. CI validates duplicates and forbidden cross-service access declarations.
- Legacy NestJS remains private and frozen. S0 proves one controlled Legacy proxy path using a safe read endpoint or test fixture. Gateway passes restricted internal identity context, applies timeout/circuit breaking, and normalizes only at the anti-corruption boundary.
- Local and test delivery are reproducible without production credentials. Dependencies include PostgreSQL, a Kafka API compatible broker, an OIDC test provider or fixture, and OpenTelemetry Collector.
- Staging follows the production shape sufficiently to validate workload identity, NetworkPolicy, probes, rolling updates and rollback. Specific Kubernetes distribution and cloud services remain environment choices.
- OpenTelemetry uses W3C Trace Context across public HTTP, internal RPC, Outbox, Kafka, Inbox and Audit. Business IDs remain correlation fields; metrics must not use request, session or principal IDs as labels.
- Logs are structured and redact cookies, tokens, grants, secrets and full sensitive payloads. Problem Details and health endpoints do not expose internal addresses, stack traces or credentials.
- CI gates include Go tests, frontend type/build checks, schema validation, generated-code diff, compatibility checks, secret scanning, SAST/dependency scanning, SBOM creation and image signature verification.
- Runtime containers use non-root identities, read-only root filesystems where possible, minimal capabilities, explicit resource limits and separate ServiceAccounts. Database migration identities are distinct from runtime identities.
- S0 contains no production ThingsBoard telemetry, RPC, Command, Schedule, AI Tool or Recommendation behavior. Synthetic identity and delivery events may be used only to prove the foundation.
- Every S0 release produces a Release Evidence Bundle and proves rollback to the previous compatible Gateway/IAM/Audit version. Database changes use expand-contract.

## Testing Decisions

- Primary tests start at Gateway HTTP or a real browser session and validate contracts, durable state, emitted events and Audit results. Handler or repository tests supplement but do not replace black-box gates.
- Contract tests validate OpenAPI, Problem Details, generated clients, Protobuf compatibility and current/previous version interoperability. Generated output drift fails CI.
- Authentication tests cover valid login, invalid issuer/audience/token type, expiry, JWKS rotation, CSRF/Origin, logout and session revocation.
- Service identity tests cover valid mTLS, unknown workload, wrong trust domain, wrong audience, expired delegation, scope expansion and forged identity headers.
- Transaction tests fail between state, Audit Intent and Outbox operations to prove atomic commit or rollback.
- Event tests cover duplicate publication/consumption, consumer restart, broker restart, offset replay and Inbox idempotency.
- Audit tests assert initiating and executing principals, stable action/result codes, payload hash and absence of credentials.
- Route ownership tests cover Go ownership, Legacy ownership, sticky cohort selection, unknown owner, conflicting revision, unavailable Legacy and route rollback.
- Tenant tests use at least two Organizations. Session, route policy, audit query and diagnostic results must not leak existence or content across Organizations.
- Observability tests assert one trace can be followed from Gateway through IAM and asynchronous Audit ingestion. Collector outage must not fail the user request.
- Security tests scan repository, images, logs, traces, metrics, Kafka payloads and persisted records for seeded secrets and tokens.
- Deployment tests cover startup/readiness/liveness, graceful shutdown, rolling update, previous-version rollback and migration compatibility.
- Failure tests cover PostgreSQL unavailability, broker unavailability, Outbox backlog, Audit consumer failure, Legacy timeout, invalid certificates and session store restart.
- Prior art includes existing HVAC Web build/browser audits, NestJS Jest suites and EnergyAgent verification scripts. S0 adds Go contract/integration tests instead of copying NestJS implementation tests.

## Out of Scope

- Organization, Site, Equipment and Device CRUD beyond minimal identity scope references.
- Real ThingsBoard telemetry ingestion, historical storage or realtime device updates.
- Device Command creation, approval, dispatch, RPC or ACK handling.
- Scheduler, Automation, Alarm, report and analytics behavior.
- AI Investigation, Tool Broker, model calls or Recommendation control handoff.
- Final cloud provider, Kubernetes distribution, managed component selection or production cost sizing.
- Complete WORM audit archival and legal retention implementation.
- NestJS deletion or database migration. Legacy remains frozen and privately proxied.
- Migrating HVAC Web business pages from Mock to real Organization or Telemetry data; that begins in S1 and S2.

## Further Notes

- The local tracker is used because repository notes indicate GitHub CLI authentication is unavailable.
- S0 must stay narrow. Business-domain proposals belong in S1 or later slices.
- Completion means future slices can add a resource and event without inventing new identity, error, audit, delivery, observability or deployment conventions.
