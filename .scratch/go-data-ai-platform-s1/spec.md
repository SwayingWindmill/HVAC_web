# S1 — Organization–Site–Device Read Slice

Status: ready-for-review

Source architecture: `../go-data-ai-platform/map.md`

Prerequisite foundation: S0 completion commit `be65275` and Release Evidence run `29763231123`.

## Problem Statement

S0 已建立 Gateway、OIDC/BFF Session、可信 Principal Context、mTLS Workload Identity、委托、统一 OpenAPI/Problem Details、Outbox/Inbox、Audit、Route/Data Ownership、观测、签名供应链和故障门禁，但 HVAC Web 的 Organization、Site 与资产结构仍主要来自本地 Mock 或 Legacy DTO。当前前端可以展示站点与设备，但这些展示值并不证明平台 UUID、IAM 当前授权、Core 数据权威、PostgreSQL RLS、Legacy 映射和 Gateway 路由能够共同工作。

S1 必须交付第一个真实业务读取切片，使用户通过现有 BFF Session，从 Gateway 读取其当前授权范围内的 Organization、Site、Equipment 与 Device，并在 HVAC Web 中关闭目标页面的 Registry Mock。该切片必须证明跨组织 SiteBinding、资源不可见语义、不透明 Cursor、Site 时区、Legacy ID 映射、Migration Quarantine、Shadow Compare 和路由回滚都遵守 S0 已建立的安全与交付不变量。

## Solution

交付一个只读、端到端、可迁移和可回滚的 Registry 切片：

1. 扩展公共 OpenAPI，定义 Organization、Site、Equipment、Device、DeviceBinding、集合分页和 Registry 页面所需的类型化 DTO。
2. 采用 Logto 作为外部身份提供方，复用其 OIDC、凭据、MFA/Passkey、企业身份联邦和外部用户生命周期；在平台 IAM 中实现 `registry.read` 授权投影，由当前 OrganizationMembership、RoleBinding、SiteBinding、显式拒绝和 Policy revision 生成短期、Audience 受限的读取委托。
3. 建立独立的 `platform-core-service`，以 PostgreSQL 独立 Schema 和运行身份拥有 Organization、Site、Equipment、Device、DeviceBinding、ExternalBinding 与 S1 迁移映射。
4. 对所有租户表实施应用层 Scope 过滤和 PostgreSQL RLS 双重隔离；跨组织访问只能通过有效 SiteBinding 获得明确 Site Scope。
5. 从一个内部测试 Organization/Site 建立可重复的 Legacy 快照映射、Migration Provenance、Quarantine 和异步 Shadow Compare，不在读请求中触发 ThingsBoard 同步。
6. 通过 Gateway 暴露 `/api/v1` 列表与详情接口，使用平台 UUIDv7、不透明 Keyset Cursor、稳定 Problem Details 和资源不可见语义。
7. 将 HVAC Web 的目标 Organization/Site/资产结构读取切换到生成的 TypeScript Client；真实模式不得静默回退到 Mock。
8. 通过双 Organization 黑盒测试、RLS、Shadow 差异、故障注入、容量基线、备份 Restore、Route rollback 和 Release Evidence Bundle 关闭 S1。

最高测试接缝固定为 Gateway 公开契约或真实浏览器。实现必须同时验证公开响应、IAM 授权、Core 持久状态、RLS、Audit、Migration 映射、Shadow 结果和前端真实数据来源。

## User Stories

1. As an authenticated user, I want to list Organizations in which I currently have an effective membership, so that the UI does not infer tenant scope from a token claim.
2. As a user with a cross-organization SiteBinding, I want to see only the specifically delegated Site, so that membership in my acting Organization does not expand to the owning Organization.
3. As a user, I want inaccessible and nonexistent resources to be indistinguishable, so that IDs cannot be used to enumerate another tenant.
4. As a user, I want every Site to expose its authoritative IANA timezone, so that later schedules, reports and telemetry ranges use the correct calendar.
5. As a user, I want Organization, Site, Equipment and Device lists to page consistently, so that large registries do not require offset scans or exact counts.
6. As a frontend developer, I want generated Registry types and clients, so that the HVAC Web does not hand-copy protocol DTOs.
7. As a frontend developer, I want real mode to fail visibly when Registry APIs are unavailable, so that production never silently displays Mock business data.
8. As a Core service owner, I want each Registry table and external mapping to have one writer, so that Legacy and Go cannot both become authoritative.
9. As a security engineer, I want forged Organization/Site headers and stale grants rejected, so that navigation preferences cannot become authorization facts.
10. As a database owner, I want tenant Scope enforced by application queries and RLS, so that one missing predicate does not become a cross-tenant incident.
11. As a migration owner, I want every Legacy identifier mapped through explicit provenance, so that old Customer/Asset/Device IDs never become new public IDs.
12. As a migration owner, I want ambiguous Legacy records quarantined rather than guessed, so that an incorrect Asset-to-Equipment conversion cannot enter the Core registry.
13. As a migration owner, I want Legacy and Go reads compared asynchronously, so that migration differences are visible without delaying the user response.
14. As an operator, I want Registry routing to be revisioned and rollbackable, so that a bad Go rollout can return future reads to an approved read-only Legacy path.
15. As an auditor, I want allowed and denied Registry access to record initiating principal, executing service, acting/owning Organization, Site Scope and policy revision without credentials.
16. As an operator, I want IAM, Core PostgreSQL, Legacy and Audit failures to produce explicit safe states, so that no dependency failure widens access or exposes internal details.
17. As a release reviewer, I want capacity, backup/restore, tenant, migration, security and rollback evidence in one Bundle, so that S1 approval is reproducible.
18. As a future S2 owner, I want stable Device and ExternalBinding identities, so that Telemetry can attach to S1 resources without reopening tenant or ID decisions.

## Implementation Decisions

### Slice scope and service boundaries

- S1 introduces one new production-shaped binary: `platform-core-service`. It remains a modular Core service rather than separate Organization, Site, Equipment and Device microservices.
- Logto is the selected external identity provider. It owns authentication ceremonies, password/passkey/MFA factors, external IdP federation, external sessions and the external subject lifecycle.
- IAM remains the only owner of Principal, OrganizationMembership, RoleBinding, SiteBinding and Policy. Core must not persist a second editable authorization policy store.
- Core owns `Organization`, `Site`, `Equipment`, `Device`, `DeviceBinding`, `ExternalBinding`, `LegacyResourceMap`, `MigrationProvenance` and S1 `MigrationQuarantine` records.
- Gateway remains edge/protocol focused. It validates the BFF Session, obtains the current scoped delegation, selects a Route Owner, calls Core or the approved Legacy anti-corruption path, and normalizes the public contract.
- A normal Registry request must synchronously call at most one domain owner after trusted identity resolution. Gateway does not fan out to multiple business services to build an unbounded asset tree.

### Domain model

- `Organization` is the tenant boundary and has immutable platform `id`, mutable tenant-scoped `code`, `displayName`, lifecycle `status`, `revision`, `createdAt` and `updatedAt`.
- `Site` has exactly one `owningOrganizationId`, immutable platform `id`, tenant-scoped `code`, `displayName`, required IANA `timezone`, lifecycle `status`, `revision`, `createdAt` and `updatedAt`.
- `Equipment` is a stable maintainable business asset. S1 includes identity, owning Organization, Site, code, display name, equipment type, status and revision; it does not implement the complete HVAC System graph or control capability model.
- `Device` is an IoT endpoint identity. S1 includes identity, owning Organization, Site, code, display name, device type, status and revision; online state and telemetry remain S2.
- `DeviceBinding` relates Device and Equipment with `validFrom`, optional `validTo`, binding role, status and revision. Device and Equipment are not interchangeable.
- `ExternalBinding` contains `integrationInstanceId`, `provider`, `externalEntityType`, `externalId`, `validFrom`, optional `validTo`, `bindingStatus` and revision. The tuple `(integration_instance_id, external_entity_type, external_id)` is unique for active bindings.
- Every platform business identifier is UUIDv7 generated by the owning service. Legacy database IDs, ThingsBoard UUIDs, demo aliases and internal serial keys are never public primary keys.
- S1 does not introduce Building/Floor/Zone CRUD, HVAC System graph mutation, TelemetryPoint mapping, Equipment control capabilities or asset write APIs.

### Authorization

- Logto OIDC `iss` and `sub` identify the authenticated external subject. The platform stores a stable Principal mapping keyed by provider and subject; email, display name and similar profile fields are synchronized attributes rather than immutable authorization keys.
- Logto Organization membership, organization-role or custom claims may seed onboarding and reconciliation workflows, but they never directly authorize HVAC Organization, Site, Equipment or Device access. Platform IAM remains the business authorization authority.
- Provisioning from Logto into platform IAM is explicit, idempotent, versioned and audited. Claim or Management API changes do not silently grant access until a platform-owned OrganizationMembership/RoleBinding/SiteBinding transition is committed.
- Logto Management API credentials remain server-side, narrowly scoped and isolated from browser sessions, delegation grants and Core service credentials.
- The public action introduced by S1 is `registry.read`; implementation may use narrower internal actions such as `organization.list`, `site.read`, `equipment.read` and `device.read`, but public authorization semantics remain explicit and versioned.
- Membership only proves association with an Organization. Effective access is the intersection of current Membership, RoleBinding, SiteBinding, explicit deny, resource Scope, action and limited ABAC.
- IAM produces a short-lived delegation for `platform-core-service` containing initiating principal, executing audience, acting Organization, allowed Organization/Site Scope, actions, policy revision, expiry and `jti`.
- Core validates Workload Identity and grant audience, expiry, revocation, action and Scope, then intersects the grant with the resource's authoritative owning Organization and Site.
- A cross-organization SiteBinding grants only the bound Site and permitted actions. It does not grant Organization-wide listing of the Site owner or access to sibling Sites.
- Client `X-Organization-*`, `X-Site-*`, role, admin and scope headers are rejected or ignored and produce security evidence. Active Organization/Site values are UI preferences only.
- Invalid, expired, revoked, wrong-audience or policy-stale grants fail closed. The maximum accepted policy staleness and revocation propagation target remain bounded by the S0 identity baseline.
- Logto outage handling follows the BFF Session boundary: existing platform sessions may operate only within the accepted token/session freshness and revocation limits; new login, refresh or identity reconciliation fails explicitly and never widens authorization.

### PostgreSQL ownership and RLS

- IAM and Core use separate Schemas, runtime accounts and migration accounts. Neither service may query or write the other's Schema.
- Core tenant tables contain immutable `organization_id`; Site-scoped rows also contain immutable `site_id`. Cross-service foreign keys are prohibited.
- Runtime queries always include authorized Organization/Site predicates. Each read transaction sets transaction-local authorized Scope used by RLS as a second enforcement layer.
- The Core runtime role cannot `BYPASSRLS`, own tenant tables or alter policies. Migration and break-glass identities remain separate and audited.
- RLS must support owner-Organization access and explicitly authorized cross-organization Site Scope without allowing access to sibling Sites or owner-Organization collections.
- Pagination queries use indexes that begin with tenant/Scope columns and the selected deterministic sort tuple. Exact total counts are not part of the default contract.

### Public HTTP contract

S1 adds the following typed read contracts under `/api/v1`:

- `GET /organizations`
- `GET /organizations/{organizationId}`
- `GET /organizations/{organizationId}/sites`
- `GET /sites/{siteId}`
- `GET /sites/{siteId}/equipment`
- `GET /equipment/{equipmentId}`
- `GET /sites/{siteId}/devices`
- `GET /devices/{deviceId}`

Contract rules:

- Organization-level targets are explicit in the path. Site and resource details resolve owning Organization from Core authority.
- Success responses are direct typed resources or `{items,nextCursor,hasMore}` collection DTOs; no global success envelope is added.
- Collection requests use `limit` and opaque `cursor`. Cursor content binds route, authorized Scope, filters, ordering, query/schema revision and the last sort tuple; it never replaces current authorization.
- Default ordering is deterministic and includes immutable `id` as a tie-breaker. Cursor tampering, route/filter mismatch or unsupported revision returns stable `CURSOR_INVALID` Problem Details.
- Missing and unauthorized resources return the same `404` status and `RESOURCE_NOT_FOUND` code with safe detail. Responses do not reveal whether a resource exists in another tenant.
- Normal Registry DTOs do not expose ThingsBoard IDs or raw Legacy IDs. Controlled migration diagnostics require a separate internal/operator permission and are not consumed by the normal HVAC Web page.
- Site timezones use canonical IANA names. All Instants use RFC 3339 UTC milliseconds.
- Generated Go and TypeScript artifacts continue to use the existing locked `scripts/generate-platform-contracts.mjs` pipeline unless Ticket 01 proves a replacement has lower total cost and equal compatibility guarantees.

### Legacy mapping and migration

- The first S1 cohort is one internal test Organization and Site with deterministic, non-production fixtures plus a repeatable import path for approved Legacy records.
- Legacy Customer/Site/Asset/Device identifiers are represented by `LegacyResourceMap` and `MigrationProvenance`; no mapping is inferred from names alone.
- Legacy Asset is not automatically Equipment. Legacy Device is not automatically both Device and Equipment. Conversion requires explicit transformation version, relation evidence and validation state.
- Mapping states include at least `DISCOVERED`, `MAPPED`, `VERIFIED`, `QUARANTINED` and `RETIRED`. Ambiguous, conflicting, missing-owner or non-unique records enter Quarantine.
- Snapshot backfill is keyset-paged, idempotent and records source watermark, source key, target ID, transformation version, row hash, batch ID, timestamps and result.
- Read requests never invoke `sync=true`, direct ThingsBoard scans or on-demand full Legacy imports.
- Shadow Compare is asynchronous and side-effect free. It canonicalizes Go and Legacy reads and compares authorization result, resource IDs/mappings, ordering, fields and hashes; the user receives one authoritative response.
- Route phases are `LEGACY_PRIMARY_GO_SHADOW`, `GO_CANARY_LEGACY_SHADOW`, `GO_PRIMARY_LEGACY_READ_FALLBACK` and `GO_PRIMARY`. Fallback is explicit, read-only and never occurs after an authorization denial or resource-not-found result.
- Route rollback changes the future read owner revision only. It does not reverse Core data ownership, enable Legacy writes or publish duplicate events.

### HVAC Web integration

- The target surfaces are the System site's Organization/Site/asset-structure read view and the Registry metadata needed by the existing Assets navigation.
- `mockSites` and `mockAssetTree` stop being the source of truth for the target Registry view when `VITE_API_MODE=real`.
- Registry hooks use generated TypeScript types and client calls. Thin adapters may build presentation nodes but cannot duplicate protocol DTOs or infer authorization.
- Real mode never silently falls back to Mock after a network, authorization, mapping or server failure. The page shows typed empty, unavailable or retryable states derived from Problem Details.
- Registry write controls, user/role management, live device status, alarms, telemetry values and command actions remain disabled, unchanged or explicitly deferred; S1 must not preserve fake production writes by mutating local Mock state.
- Device status displayed by S1 is lifecycle/registry status only. Online state and latest telemetry remain S2 and must not be synthesized from Mock data in a real Registry view.

### Observability and audit

- W3C Trace Context propagates browser → Gateway → IAM identity seam → Core → PostgreSQL and into Shadow Compare/Audit evidence.
- Structured logs and Problem Details contain stable IDs, revisions, decision/result codes and trace IDs, but no cookies, grants, tokens, raw external credentials or full sensitive Legacy payloads.
- Metrics cover Registry request rate/error/latency, authorization allow/deny, RLS denial, cursor invalidation, mapping state, Quarantine age, shadow difference, route owner/revision and fallback usage. Principal, resource and Site IDs are not metric labels.
- User-facing Registry collection/detail reads and authorization denials produce Audit events with initiating/executing principals, acting/owning Organization, Site/resource Scope, action, policy revision, route/data owner revision, result and trace correlation.
- Logto login, callback, logout, subject-linking, provisioning/reconciliation and external identity conflicts are correlated to the platform Principal without recording tokens, credentials or full Management API payloads.
- Audit backend failure must not widen access or change a denial into an allow. Ordinary read availability follows the documented degraded mode; security denial evidence is retained through bounded local/Outbox mechanisms where applicable.

### Capacity, availability and recovery

- Gateway/Core Registry APIs inherit the 99.95% online-service target, P95 ≤ 300 ms and P99 ≤ 1 s for representative list/detail reads.
- S1 capacity evidence uses synthetic data up to the architecture envelope of 300 Sites and 600,000 Devices, with indexed keyset pagination and no default exact count.
- A staging load gate exercises the ordinary business API target of 2,000 QPS as a controlled peak and reports latency, error rate, database saturation, RLS overhead and at least 30% planned capacity headroom.
- Core/IAM database unavailability, stale authorization projection, Legacy timeout, Shadow worker failure and Audit outage have explicit safe behavior and must not cause cross-tenant success.
- Backup/restore evidence covers IAM authorization facts required by S1, Core Registry data, External/Legacy mappings, migration provenance and Quarantine. Restored data is verified by tenant counts, mapping uniqueness, representative hashes and black-box authorization queries.
- Deployment uses S0 signed-image, SBOM, provenance, NetworkPolicy, rolling update and rollback conventions. Database changes follow expand-contract and remain compatible through the rollback window.

## Testing Decisions

- Contract tests validate OpenAPI, generated artifacts, Problem Details, UUIDv7 fields, timezones, cursor behavior and current/previous compatible clients.
- Authorization tests use at least two owning Organizations, multiple Sites, an acting Organization, direct memberships, cross-organization SiteBinding, explicit deny, no-access principals and service identities.
- External identity tests use Logto-compatible fixtures for login, logout, JWKS rotation, subject mapping, duplicate email with distinct subjects, disabled external user, stale organization claim and Management API/provisioning failure.
- Every list/detail endpoint has positive, sibling-Site denial, cross-Organization denial, forged-header, stale-grant, wrong-audience and resource-enumeration tests.
- PostgreSQL integration tests prove application predicates and RLS independently block unauthorized rows; runtime roles are tested for inability to bypass RLS or access the other service Schema.
- Pagination tests cover first/next/final pages, stable tie-breakers, limit bounds, cursor tampering, filter/scope mismatch, query revision change and authorization changes between pages.
- Migration tests cover idempotent backfill, duplicate Legacy key, ambiguous Asset conversion, missing owner, hash mismatch, Quarantine, controlled replay and no read-time synchronization.
- Shadow tests prove one authoritative response, bounded asynchronous comparison, safe canonicalization, cross-tenant difference as a hard failure and Legacy failure isolation.
- Frontend browser tests prove the target Registry view uses real generated API calls, displays authoritative Site timezone and IDs, handles 404/403-equivalent invisibility, and does not import target Mock data in real mode.
- Observability tests verify one trace crosses Gateway/IAM/Core/PostgreSQL and associates route/policy/data revisions without high-cardinality metric labels or credentials.
- Failure tests cover IAM, Core PostgreSQL, Legacy, Shadow worker, Audit, certificate and route-registry failures plus service restarts and rolling rollback.
- Capacity tests use the declared synthetic dataset and report P50/P95/P99, throughput, database CPU/IO/locks, connection pool saturation and RLS/query-plan evidence.
- Restore tests rebuild IAM/Core S1 data into an isolated database and rerun the tenant isolation and mapping hash checks.
- Release tests download and verify the S1 Evidence Bundle, image signatures, attestations, SBOM/provenance, security scans, migration state, canary/rollback and four zero invariants.

## Zero Invariants

S1 release evidence must report all of the following as zero:

- successful cross-Organization or unauthorized sibling-Site reads;
- resources whose existence is disclosed through status, cursor, count, timing bucket or error detail;
- forged client headers accepted as authorization facts;
- ambiguous Legacy mappings promoted to verified Core resources;
- Registry read requests that trigger a ThingsBoard full synchronization;
- business double writes between Legacy and Core;
- credentials, grants, cookies or raw integration secrets in repository, images, logs, traces, metrics, events or the Evidence Bundle.

## Out of Scope

- Organization, Site, Equipment, Device, Binding, Membership, Role or Policy create/update/delete APIs and management UI.
- Full Building/Floor/Zone model, HVAC System relationship graph, EquipmentGroup authorization and SiteTransfer workflows.
- TelemetryPoint mapping, ThingsBoard ingestion, online state, latest/history telemetry, Realtime Snapshot/Delta or alarms.
- Device Capability, Command, approval, RPC, ACK, Scheduler, Automation, reports, AI Investigation or Recommendation.
- Replacing IAM with OpenFGA, SpiceDB or Casbin in S1.
- Building passwords, MFA, passkeys, account recovery, enterprise SSO or an external user directory inside the Go `iam-service`.
- Treating Logto Organization, role or custom claims as direct HVAC resource authorization.
- Generic offset pagination, exact total counts, unrestricted search, export or arbitrary cross-Site aggregation.
- Deleting NestJS, restoring public Legacy ports or granting Legacy new write ownership.
- Production cutover for more than the approved internal S1 cohort before the S1 Release Gate passes.

## Approval Gate

This specification is ready for review. Acceptance must be explicit. Until accepted, all S1 implementation tickets remain blocked and no service, database, contract or frontend implementation may begin.
