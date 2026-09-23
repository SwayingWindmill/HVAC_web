# 36 用户、权限与审计 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-15**  
> **Surface Catalog：** `36 用户、权限与审计`  
> **Route intent：** `/settings/access`、`/settings/access/principals/:principalId`、`/settings/access/roles/:roleId`、`/settings/access/audit/:auditEventId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`RBAC`、`ABAC`、`MFA`、`SSO`、`OIDC`、`SAML`、`SCIM`、`PAM`、`Zero Trust` 等标准术语只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目旧用户管理、旧权限树、旧 admin 页面或旧 Ant/ProComponents 页面。现有实现只能在实施阶段作为真实 Identity / Principal / Role / Permission / Scope / Session / Approval / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **治理谁可以以什么身份、在什么资源范围内、执行什么业务动作，并对高风险授权与业务操作形成可追溯、可审计、可复核的证据链。**

36 是 **Identity Projection + Authorization Governance + Audit Evidence Workspace**，不是：

- 简单“管理员 / 普通用户”两级 RBAC；
- IdP / LDAP / Active Directory 本身；
- Password Vault；
- PAM 产品替代；
- SIEM；
- 通用 HR 系统；
- 一个角色打勾矩阵就代表所有安全边界；
- 把业务授权和现场安全联锁混成一个 `canControl=true`；
- 只记录“某人点过按钮”的操作日志。

用户离开本页前应该能回答：

1. 当前 Principal 是谁，来自哪个 identity source；
2. Principal 当前有哪些 Role / Entitlement；
3. Role 包含哪些 Permission；
4. Permission 适用于哪些 Site / Resource / Object Scope；
5. Read / Edit / Approve / Publish / Control / Audit 是否独立；
6. 是否存在职责分离冲突；
7. 是否存在临时委派、到期权限或紧急授权；
8. 当前 Session / Authentication Context 是否满足高风险操作要求；
9. 某个操作是否只是“有权请求”，还是已经满足业务/安全执行条件；
10. 谁在什么时候授予、撤销、批准或使用了高风险权限；
11. 一个 Audit Event 对应哪个业务对象、Revision、Execution / Decision / Result；
12. Audit Event 是否只是记录系统动作，还是有独立业务证据证明现场结果；
13. 历史权限是否可复现，而不是用今天的 Role 重解释过去；
14. 当前有哪些 dormant / excessive / expiring / conflicting access 需要处理。

---

# 2. 主要用户

## Primary

- **Access Administrator：**维护 Principal、Role、Permission、Scope、Delegation 与 Access Review；
- **Security Administrator：**维护 authentication / MFA policy reference、privileged access policy、sensitive actions；
- **Site Administrator：**维护 Site-level assignment，但不能越权授予系统级特权；
- **Approver / Governance Owner：**审批高风险角色、职责分离例外和临时授权；
- **Auditor：**复核权限历史、授权依据和高风险业务操作证据。

## Secondary

- Control Engineer：消费 control permission / sensitive action authority，但 25 仍决定现场 Control Authority；
- Integration Administrator：消费 integration management permission；
- Management：查看 access review / privileged access exceptions；
- Identity Platform Owner：提供 IdP / SSO / SCIM / directory facts；
- Incident / Security Operations：消费 audit stream，但 SIEM 不是本 Surface。

---

# 3. 外部最佳实践依据

## 3.1 NIST SP 800-53 Rev.5 — Access Control、Least Privilege、Separation of Duties、Audit

NIST SP 800-53 Rev.5 将 Access Control、Identification and Authentication、Audit and Accountability 等作为独立控制族，并明确包含 least privilege、privileged functions、separation of duties、account management 与 audit requirements。

来源：

- https://csrc.nist.gov/pubs/sp/800/53/r5/upd1/final
- https://csrc.nist.gov/glossary/term/least_privilege

本页采用：

```text
Role ≠ Permission
Permission ≠ Scope
Privilege ≠ Business Approval
Privileged Function Use → Audit
```

并以 least privilege 为默认授权原则。

## 3.2 NIST RBAC — Role、Permission、Hierarchy、Constraints、Separation of Duty

NIST RBAC 模型把 users、roles、permissions、operations、objects 和 role assignment / permission assignment 分开，并包含 hierarchical RBAC、static separation of duty 和 dynamic separation of duty。

来源：

- https://www.nist.gov/publications/nist-model-role-based-access-control-towards-unified-standard
- https://csrc.nist.gov/projects/role-based-access-control

本页采用：

```text
User Assignment
≠ Role Definition
≠ Permission Definition
≠ Resource Scope
```

并明确职责分离不是 UI 提醒，而是授权约束。

## 3.3 NIST SP 800-207 — Zero Trust 不给予隐式信任

NIST Zero Trust Architecture 明确：不能仅因为网络位置、资产所有权或过去认证就赋予隐式信任；authentication 与 authorization 是离散过程，访问决策应围绕 subject / asset / resource 持续执行。

来源：

- https://csrc.nist.gov/pubs/sp/800/207/final

本页采用：

```text
Authenticated Session
≠ Authorized Action

Inside Corporate Network
≠ Trusted Automatically
```

高风险动作需要重新评估 Principal、Session Context、Permission、Scope 和业务条件。

## 3.4 NIST SP 800-82 Rev.3 — OT 授权必须考虑安全、可靠性和物理影响

NIST SP 800-82 Rev.3 把 Building Automation Systems 纳入 OT，并强调 OT 的 security controls 必须兼顾 performance、reliability 与 safety。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final

本页采用：

```text
Control Permission
≠ Control Authority
≠ Safety Permission
```

36 只负责“谁被授权提出/执行某类业务动作”；现场 Interlock、Precondition、Authority、Readback、Verification 仍由 25–28 管理。

## 3.5 ISA/IEC 62443 — IACS Security Roles、Lifecycle 与 Asset Owner 责任

ISA/IEC 62443 是工业自动化与控制系统的共识安全标准体系，并明确 Asset Owner、Integration Service Provider、Maintenance Service Provider、Product Supplier 等角色与生命周期责任。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-iec-62443-series-of-standards
- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa99
- https://www.isa.org/intech-home/2021/june-2021/features/automation-systems-cybersecurity-from-standards-to

本页采用：OT privileged access、service-provider access、maintenance access 必须有明确责任、范围、期限和审计，不使用共享“工程师账号”作为默认设计。

---

# 4. 产品语言契约

主界面中文优先：

```text
用户、权限与审计
身份
主体
角色
权限
资源范围
站点范围
职责分离
授权
委派
临时权限
紧急权限
会话
认证上下文
多因素认证
访问审查
高风险权限
敏感操作
审计事件
业务对象
变更前
变更后
原因
证据
```

可保留标准术语：

```text
RBAC
ABAC
MFA
SSO
OIDC
SAML
SCIM
PAM
Zero Trust
```

默认业务界面不直接暴露 IAM provider-specific JSON / token claim dump。

---

# 5. Mandatory Semantic Separation

```text
Identity ≠ Principal
Human User ≠ Service Principal
Group ≠ Role
Role ≠ Permission
Role Membership ≠ Permission Grant
Permission ≠ Resource Scope
Site Scope ≠ Action Permission
Resource Scope ≠ Data Filter automatically

Authentication ≠ Authorization
Authentication Strength ≠ Permission
MFA Completed ≠ Sensitive Action Authorized
Session Active ≠ Privileged Session

Read ≠ Edit
Edit ≠ Approve
Approve ≠ Publish
Publish ≠ Execute
Execute ≠ Verify

Control Permission ≠ Control Authority
Control Permission ≠ Interlock Bypass
Control Permission ≠ Safe to Control

Role Hierarchy ≠ Organizational Hierarchy
Role Inheritance ≠ User Management Hierarchy

Static Separation of Duties ≠ Dynamic Separation of Duties
Approval Required ≠ Separation of Duties Satisfied automatically

Delegation ≠ Permanent Role Assignment
Temporary Access ≠ Permanent Entitlement
Emergency Access ≠ Normal Privilege

Access Granted ≠ Access Used
Access Used ≠ Business Success

Audit Event ≠ Business Execution
Audit Record ≠ Physical Outcome
Audit Record ≠ Root Cause
Audit Logged ≠ Audit Reviewed

Current Access ≠ Historical Access
Role Changed ≠ Historical Audit Reinterpreted
Permission Revoked ≠ Historical Evidence Deleted

Session Terminated ≠ Credential Revoked
Credential Disabled ≠ Identity Deleted
Principal Disabled ≠ Historical Audit Deleted
```

---

# 6. Core Domain Objects

36 至少治理：

```text
Identity Reference
Principal
Human Principal
Service Principal
External / Support Principal
Group Reference
Role
Role Revision
Permission
Permission Set
Resource Scope
Site Scope
Role Assignment
Direct Entitlement if explicitly permitted
Delegation
Temporary Access Grant
Emergency Access Grant
Separation-of-Duties Constraint
Access Review Campaign
Access Review Decision
Session Reference
Authentication Context Reference
Privileged Session Reference
Audit Event
Audit Evidence Link
Access Change Request
Approval Record
```

Password / private key / token secret 由 Identity / Credential owner 管理，不由 36 回显。

---

# 7. Identity Contract

Identity 表示身份源中的“是谁”。

至少引用：

```text
Identity ID
Identity Source
Identity Type
Display Name
External Subject ID
Lifecycle State
Last Sync / Verification
Owner
```

Identity Source 可能是：

```text
Enterprise IdP
Directory
SCIM source
Local break-glass identity provider if explicitly governed
Service identity owner
```

36 不成为密码数据库。

---

# 8. Identity ≠ Principal

一个 Identity 可以投影为一个或多个应用 Principal / context；Principal 才是授权系统中被评估权限的主体。

例如：

```text
Identity
alice@example.com

Principal A
Human User / Operations

Principal B
Temporary Vendor Delegate
```

如果产品不允许多 Principal，也仍必须保持概念分离，避免把 IdP record 与 authorization record 混成一个对象。

---

# 9. Principal Contract

Principal 至少记录：

```text
Principal ID
Identity Reference
Principal Type
Organization Context
Lifecycle State
Allowed Authentication Contexts
Current Assignments
Effective Period
Owner
Created At
Disabled At
```

Principal Type 至少区分：

```text
Human
Service
Integration
External Support
Automation / Workload
Emergency / Break-glass if explicitly supported
```

---

# 10. Human Principal ≠ Service Principal

Service Principal 不应继承 Human User 的交互语义。

例如 Service Principal：

- 无 inbox；
- 无 interactive MFA flow；
- 需要 workload credential / certificate；
- 必须有明确 owner；
- 权限范围更窄；
- credential rotation 由其 owner 管理。

禁止把 service account 当成“一个没有邮箱的人类用户”。

---

# 11. Shared Account Boundary

生产系统默认不使用共享个人账号作为 privileged operation identity。

如果 legacy OT / external system 只能提供 shared account：

```text
Shared external credential
≠ Shared application principal
```

应用仍必须保留真实 initiating Principal，并在 28 Execution / 34 Integration 中关联 external credential reference。

---

# 12. Role Contract

Role 是业务责任对应的一组 permission / constraints。

至少记录：

```text
Role ID
Display Name
Purpose
Permissions
Allowed Scope Types
Role Hierarchy
Constraints
Risk Level
Owner
Revision
Effective Period
Lifecycle State
```

例如：

```text
Site Operator
Energy Analyst
M&V Reviewer
Control Engineer
Strategy Approver
Integration Administrator
Access Auditor
```

而不是：

```text
Role1
Role2
Admin2
```

---

# 13. Role ≠ Permission

Role 表示业务职责聚合。

Permission 表示具体可执行操作，例如：

```text
energy.read
mv.review
strategy.edit
strategy.approve
control.request
execution.read
integration.manage
access.review
```

Role 可以变更 permission set，但 Permission 本身应有稳定 identity / semantics。

---

# 14. Group ≠ Role

Directory Group 可以用于 assignment automation，但不能直接等同于业务 Role。

例如：

```text
IdP Group
SG-Facility-Operators
↓ mapping
Application Role
Site Operator
```

Mapping 必须显式、有 owner、可审计。

禁止：

```text
any directory group name
→ automatically becomes role
```

---

# 15. Role Revision Contract

影响授权语义的实质变化必须形成 Revision：

```text
Permission added / removed
Scope type changed
Inheritance changed
SoD constraint changed
Risk classification changed
Privileged requirement changed
```

历史 authorization / audit 必须能解析当时实际 Role Revision。

---

# 16. Role Hierarchy Contract

Role hierarchy 只表达授权继承关系。

例如：

```text
Energy Reviewer
inherits
Energy Reader
```

但：

```text
Manager
≠ automatically inherits every subordinate permission
```

组织层级与权限层级必须分开。

---

# 17. Permission Contract

Permission 至少记录：

```text
Permission ID
Action
Resource Type
Risk Level
Sensitive Action flag
Required Authentication Context
Allowed Scope Types
Owner Domain
```

例如：

```text
alarm.read
alarm.ack
work.edit
work.close
mv.verify
strategy.publish
control.request
control.override
integration.publish
access.grant
```

---

# 18. Permission Granularity Contract

不能只使用：

```text
read
write
admin
```

作为全产品唯一 permission vocabulary。

至少按业务动作区分：

```text
Read
Create Draft
Edit
Submit
Review
Approve
Publish
Enable
Acknowledge
Assign
Close
Verify
Execute / Request Control
Override
Rollback
Administer Access
Read Audit
```

---

# 19. Read ≠ Edit ≠ Approve ≠ Execute

例如 Strategy：

```text
strategy.read
strategy.edit
strategy.review
strategy.approve
strategy.publish
strategy.deploy
```

应该可以分配给不同角色。

不能：

```text
canEditStrategy = true
→ canApproveStrategy = true
```

---

# 20. Resource Scope Contract

Permission 必须结合 Resource Scope 使用。

Scope 至少可以表达：

```text
Organization
Portfolio
Site
Building
System
Asset / Device
Domain Object
Specific Record / Project
```

最终 authorization 是：

```text
Principal
+ Permission
+ Resource Scope
+ Constraints
```

而不是仅检查 `permission name`。

---

# 21. Site Scope Contract

Site Scope 是本产品最重要的 scope 类型之一。

可以表达：

```text
All Sites in Organization
Selected Portfolio
Specific Sites
Explicit exclusion if owner supports
```

必须使用 35 Canonical Site Identity。

禁止按 Site display name 做 scope filter。

---

# 22. Site Scope ≠ Action Permission

用户可能：

```text
Site Scope
SG-01, SG-02

Permission
energy.read
```

这不代表拥有：

```text
control.request
strategy.publish
access.manage
```

Scope 与 Action Permission 必须同时满足。

---

# 23. Resource Scope ≠ UI Filter

UI Filter 只是查看条件。

授权必须由 backend authorization owner enforce。

禁止：

```text
frontend hides SG-03 rows
→ therefore SG-03 is protected
```

所有 resource read / mutation / command 都必须服务端授权。

## Security Boundary

前端只负责可发现性、禁用态和解释性 UX；真正的 Authorization Decision、Resource Scope enforcement、SoD enforcement 和 Sensitive Action policy 必须由受控 backend owner 执行。

```text
Frontend Visibility
≠ Security Boundary
```

任何 API、mutation、approval、publish、control request、audit export 都必须再次进行服务端授权，不能信任客户端传入的 role、scope、permission 或 `isAdmin` 标记。

---

# 24. Attribute / Context Constraint Contract

除了 RBAC，可以存在 owner-defined context constraints，例如：

```text
Site Scope
Environment = Production
Authentication Strength
Session Age
Device trust if identity platform provides
Time-bound grant
Change window
Business approval reference
```

但这不是让前端自由实现一个 ABAC engine。

Policy Decision 必须由受控 authorization owner 执行。

---

# 25. Authentication Contract

36 消费 Identity Platform 的 authentication fact，例如：

```text
Authentication Method
MFA State
Authentication Time
Assurance / Context
Session ID
IdP
```

36 不自行验证 password / token cryptography。

---

# 26. Authentication ≠ Authorization

```text
SSO success
≠ allowed to read every site

MFA success
≠ allowed to control equipment
```

Authentication 只证明当前 session 中 subject identity / assurance context。

Authorization 仍需独立决策。

---

# 27. Session Contract

Session 至少可以引用：

```text
Session ID
Principal
Started At
Last Authentication At
Authentication Method / Context
Expires At
Revocation State
Device / Client context if owner-provided
```

Session lifecycle 由 Identity / Auth owner 持有。

36 不做 token viewer。

---

# 28. Session Active ≠ Privileged Session

一个普通已登录 session 不自动满足 sensitive action。

高风险动作可能要求：

```text
Recent MFA
Step-up Authentication
Privileged Session
Approved change window
Explicit reason
```

具体要求由 permission / action owner 提供。

---

# 29. Step-up Authentication Contract

对于敏感操作，例如：

```text
control.override
strategy.publish
integration.enableControl
access.grantPrivileged
breakGlass.activate
```

系统可以要求 recent MFA / re-authentication。

但：

```text
Step-up Completed
≠ Action Authorized automatically
```

它只满足 authentication-context 条件。

---

# 30. Control Permission Boundary

36 可以授权：

```text
control.read
control.request
control.override.request
control.emergency.request if policy exists
```

但 25 仍必须检查：

```text
Current Control Authority
Precondition
Interlock
Object Capability
Current State
Approval if required
```

所以：

```text
Control Permission
≠ Safe to Control
```

---

# 31. Interlock Bypass Boundary

普通 Access Admin / System Admin / Control Engineer Role 都不能隐含：

```text
Interlock Bypass
```

如果某部署真的存在 bypass authority，必须作为独立敏感 Permission / Safety Governance，并由 25 的 Interlock owner enforce。

36 不能通过超级管理员绕过现场安全逻辑。

---

# 32. Sensitive Action Contract

Sensitive Action 至少可以包括：

```text
High-risk control
Persistent override
Strategy publish / deploy
Enable control-capable integration
Privilege grant / revoke
Role definition change
SoD exception
Emergency access activation
Audit export if sensitive
Site retirement / identity merge
```

每个 Sensitive Action 必须声明：

```text
Permission
Scope
Authentication requirement
Approval requirement if any
Reason requirement
Audit requirement
```

---

# 33. Separation of Duties Contract

必须支持至少：

```text
Static Separation of Duties
Dynamic Separation of Duties
```

这与 NIST RBAC constrained model 一致。

---

# 34. Static Separation of Duties

Static SoD 限制同一个 Principal 同时拥有互斥角色 / entitlement。

例如：

```text
Access Administrator
and
Access Audit Approver
```

可以被定义为不允许长期同时持有。

或者：

```text
Strategy Author
and
Strategy Final Approver
```

在某些组织 policy 下互斥。

具体约束由 organization owner 定义，前端不硬编码通用组织政策。

---

# 35. Dynamic Separation of Duties

Dynamic SoD 允许用户拥有多个 Role，但在同一事务 / session / business object 上不能同时发挥冲突职责。

例如：

```text
Principal may hold:
Strategy Editor
Strategy Approver

But:
Cannot approve own Strategy Revision
```

这种限制必须由 backend owner enforce，并留下证据。

---

# 36. Maker / Checker Contract

对于高风险变更可以使用：

```text
Maker
Reviewer
Approver
Executor
```

但这些角色不必每个场景都四人分离。

业务 owner 定义哪些动作需要双人或多阶段治理。

禁止：

```text
same principal created
→ therefore cannot ever approve
```

这种通用前端推断；应由明确 SoD policy 决定。

---

# 37. Approval ≠ Permission

即使某个 Principal 有：

```text
strategy.publish
```

某次具体 Strategy Revision 仍可能要求：

```text
Approved Change
```

所以：

```text
Permission
≠ Business Approval
```

反过来，一个业务对象 Approved，也不意味着任何用户都能执行 publish。

---

# 38. Role Assignment Contract

每个 Assignment 至少记录：

```text
Assignment ID
Principal
Role
Resource / Site Scope
Effective From
Effective To if temporary
Reason
Requested By
Approved By if required
Source
State
```

Source 可以是：

```text
Manual
SCIM / Group Mapping
HR lifecycle
Delegation
Emergency Grant
```

---

# 39. Direct Entitlement Boundary

默认优先通过 Role 管理 permission。

如果组织允许 direct permission grant，必须：

- 明确标记为 Direct Entitlement；
- 有 reason / owner / effective period；
- 进入 Access Review；
- 不隐藏在 Role calculation 中。

避免长期“例外权限”不可见。

---

# 40. Temporary Access Contract

临时权限至少记录：

```text
Principal
Role / Permission
Scope
Start
Expiry
Reason
Sponsor / Approver
Ticket / Change reference if any
Revocation state
```

到期必须由 authorization owner真正失效，而不是 UI 只隐藏 Badge。

---

# 41. Temporary Access ≠ Delegation

Temporary Access 表示临时给某 Principal 某项授权。

Delegation 表示现有责任人将特定责任 / authority 在明确范围内委托给另一个 Principal。

两者审计和责任语义不同。

---

# 42. Delegation Contract

Delegation 至少记录：

```text
Delegator
Delegate
Delegated Role / Permission
Resource Scope
Start / Expiry
Reason
Allowed Re-delegation = No by default
Approval if required
```

Delegation 不自动复制 delegator 全部权限。

---

# 43. Emergency / Break-glass Access Contract

如果部署支持 Break-glass，必须显式治理：

```text
Emergency Grant ID
Principal
Emergency Role / Permission
Scope
Activated At
Reason
Incident / Emergency reference
Expiry / Maximum duration
Step-up auth
Notification / Review requirement
Post-use review
```

Break-glass 不是“超级管理员永久账号”。

---

# 44. Emergency Access ≠ Safety Bypass

Emergency access 可以扩大 application authorization，但：

```text
Break-glass
≠ Interlock Bypass
≠ PLC Safety Override
```

现场安全保护仍由独立 Safety / Control owner。

---

# 45. Service Provider Access Contract

第三方维护 / 集成服务商必须使用可识别 Principal / organization context。

至少支持：

```text
Vendor Organization
Named Principal
Sponsor
Site Scope
Purpose
Start / Expiry
Allowed Actions
Privileged Access condition
Audit
```

避免共享 vendor account 常驻全站点权限。

---

# 46. Access Review Contract

Access Review 是周期性或事件驱动的权限复核。

Review 可以按：

```text
Principal
Role
Sensitive Permission
Site
External User
Service Principal
Temporary / Direct Grant
```

进行。

---

# 47. Access Review Decision Contract

每项 Review 至少可以形成：

```text
Keep
Remove
Reduce Scope
Change Role
Expire
Needs Investigation
Exception Approved
```

并记录：

```text
Reviewer
Decision At
Reason
Evidence
Effective action
```

`Review Complete` 不等于权限已经变更，必须看后续 enforcement result。

---

# 48. Dormant / Excessive Access Contract

系统可以显示 candidate indicators：

```text
Unused privileged role
Expired business need
Scope larger than assignment
Former vendor access
Orphaned service principal
Direct entitlement outside role model
```

但：

```text
Unused for 90 days
≠ Automatically Revoke
```

除非 organization policy owner 明确定义。

---

# 49. Joiner / Mover / Leaver Boundary

Identity lifecycle 可能由 HR / IdP / SCIM 提供。

36 可以消费：

```text
Joined
Changed Organization
Changed Role
Disabled / Terminated
```

并产生 access lifecycle actions。

但 36 不成为 HR master system。

---

# 50. Principal Disabled Contract

Principal Disabled 表示新的 authorization 应被拒绝。

但：

```text
Principal Disabled
≠ Historical Audit Deleted
≠ Historical Ownership Reassigned
```

历史记录仍必须显示原 Principal identity reference。

---

# 51. Role Removal Contract

撤销 Role assignment 后：

```text
Future authorization denied
```

不代表：

```text
Past approval invalid automatically
Past execution erased
Past audit hidden
```

如果撤销暴露历史风险，需要单独 investigation / review。

---

# 52. Authorization Decision Contract

敏感动作的 authorization decision 至少应能解释：

```text
Principal
Action
Resource
Scope
Roles / Entitlements considered
Constraints
Session / Auth context
Decision
Decision time
Policy / Revision
Reason / deny reason
Correlation ID
```

前端不自行拼权限规则做最终 authoritative decision。

---

# 53. Authorization State Contract

可表达：

```text
Allowed
Denied
Requires Step-up
Requires Approval
Outside Scope
Conflict / SoD Blocked
Principal Disabled
Policy Unavailable
Unknown
```

`Policy Unavailable` 不能当作 Allowed。

---

# 54. Policy Unavailable Contract

对敏感 mutation / control：

```text
Authorization Owner Unavailable
→ Action unavailable
```

不是：

```text
Policy service unavailable
→ use cached admin=true
```

只读页面是否支持受控缓存由独立 product/security policy 决定，但不得把 stale authorization silently 升级成可执行权限。

---

# 55. Default Deny Contract

未定义 / 未解析 / 不在 scope 的敏感动作保持拒绝或不可用。

这不是“防御性 fallback”，而是授权系统的明确 security contract：

```text
No authoritative grant
→ No authorization
```

同时 UI 必须区分：

```text
Denied
Not Authorized
Owner Unavailable
Policy Unknown
```

避免把系统故障伪装成权限拒绝。

---

# 56. Permission Discovery Contract

导航和 action discoverability 可以基于：

```text
Product capability
Site capability
Principal discovery permission
```

但：

```text
Hidden in UI
≠ Protected
```

Backend authorization 始终必要。

---

# 57. Audit Event Contract

Audit Event 至少记录：

```text
Audit Event ID
Occurred At
Recorded At
Principal
Session / Auth Context reference
Action
Resource Type / ID
Site / Scope
Result
Before / After references
Reason
Request / Correlation ID
Source System / Service
Policy / Revision if relevant
Related Business Object / Revision
Related Execution / Decision / Approval
```

---

# 58. Audit Event ≠ Business Execution

例如：

```text
Audit Event
Principal requested CHWS setpoint 7.2°C
```

只说明应用记录了这个业务动作。

真正执行结果仍在 28：

```text
Requested
Attempted
ACK
Readback
Verified
```

因此：

```text
Audit Logged
≠ Control Succeeded
```

---

# 59. Audit Record ≠ Physical Outcome

即使 audit 记录：

```text
strategy.publish succeeded
```

也只说明 publish action 在相应 domain 成功。

它不证明：

```text
Deployed
Enabled
Active
Energy Saved
```

物理 /业务结果由对应 owner 提供。

---

# 60. Audit Event Time Contract

必须区分：

```text
Occurred At
Recorded At
Source Event Time if external
```

跨系统 audit 不能只靠 ingest timestamp 排序推断因果。

---

# 61. Correlation ≠ Causation

Audit 可以关联：

```text
Request ID
Correlation ID
Execution ID
Decision ID
Change ID
```

但：

```text
same correlation
≠ proves physical causation
```

尤其 OT 结果仍需 execution/readback/evidence。

---

# 62. Before / After Contract

对于配置 /权限变更，Audit 应尽量关联结构化 Before / After：

```text
Before Assignment
After Assignment

Before Role Revision
After Role Revision
```

不是只写：

```text
User updated access
```

这样的无效文本日志。

---

# 63. Reason Contract

高风险变更应要求 operator-provided Reason / change reference。

Reason 与系统自动生成 Message 分开。

```text
Reason
≠ Audit Summary
```

系统不能用“User clicked Save”冒充变更理由。

---

# 64. Audit Immutability Contract

应用层 Audit Record 不允许普通管理员原地编辑 / 删除。

如果需要 correction / annotation：

```text
Original Event preserved
+ Annotation / Correction Event
```

不能覆写原历史。

---

# 65. Audit Retention Boundary

Retention / legal hold / archival policy 由 security/compliance owner 定义。

36 显示 policy / status，但不硬编码：

```text
all audit retained 7 years
```

这种通用规则。

---

# 66. Audit Integrity / Export Contract

如果支持导出：

```text
Export Request
Requested By
Scope
Time Range
Reason
Generated Artifact
Integrity metadata if owner provides
Audit of export
```

大规模 /敏感审计导出可以要求更高权限。

---

# 67. Audit Search Contract

默认支持按：

```text
Principal
Action
Site
Resource
Result
Time Range
Sensitive Action
Execution / Decision ID
Change ID
```

查找。

不能要求用户通过 free-text grep 查所有审计。

---

# 68. Audit Detail Contract

Audit Detail 应使用 human-readable facts：

```text
谁
何时
在哪里 / 哪个 Site
对什么对象
做了什么
结果如何
为什么
从什么变成什么
关联哪个业务流程
```

Raw JSON / token claims 放在 Advanced Evidence。

---

# 69. Access Audit ≠ Security Incident

异常权限 /审计行为可以产生：

```text
Access Review Issue
Security Finding candidate
```

但：

```text
Denied attempts
≠ Confirmed attack
```

Security Incident lifecycle 不在本 Surface 内自行确认。

---

# 70. Privileged Function Audit Contract

对 privileged operations 应专门可筛选，例如：

```text
Grant privileged role
Change role definition
Publish strategy
Enable control integration
Persistent override request
Emergency access activation
Audit export
Site retirement
```

这与 NIST least-privilege / privileged-function auditing 方向一致。

---

# 71. Access Change Workflow Contract

高风险 access change 可以采用：

```text
Request
↓
Review
↓
Approval
↓
Enforcement
↓
Verification
↓
Audit
```

低风险常规 role assignment 是否需要审批由组织 policy 决定。

---

# 72. Approval Bound to Change Contract

审批必须绑定明确：

```text
Principal
Role / Permission
Scope
Effective Period
Requested Revision
```

如果请求发生实质变化：

```text
Site Scope changed
Role changed
Expiry changed
Sensitive permission added
```

旧 approval 不能自动继承。

---

# 73. Access Enforcement Contract

Approval 后还需要 authorization owner 真正应用变更。

所以：

```text
Approved
≠ Enforced
```

状态可以是：

```text
Approved
Applying
Active
Failed
Revoked
Expired
Unknown
```

---

# 74. Access Revocation Contract

Revocation 至少需要：

```text
Requested At
Effective At
Enforcement Result
Affected Sessions policy
Reason
Reviewer if required
```

是否立即 terminate session 由 authentication/security owner policy 决定。

不能自动假设：

```text
Role revoked
→ every existing session terminated
```

---

# 75. Session Revocation ≠ Credential Revocation

必须区分：

```text
Session termination
Token/session revocation
Credential disable
Principal disable
Role removal
```

它们影响范围不同。

---

# 76. Audit of Authorization Decision

高风险 action 应能追溯：

```text
Authorization Decision ID
Policy Revision
Principal
Permission
Scope
Result
Constraints
Authentication Context
```

这样才能解释：

> 为什么当时这个人被允许执行这项操作？

而不是只看到当前 Role。

---

# 77. Historical Access Contract

历史调查必须能回答：

```text
At 2026-08-15 14:32
What roles did Principal P-17 hold?
What Site Scope?
Which role revision?
Which permission policy?
Which temporary grants?
```

不能用今天的 assignment 重解释过去。

---

# 78. Current Access ≠ Historical Access

例如：

```text
2026-08
Site Operator on SG-01

2026-09
Energy Analyst on SG-01
```

8 月的 Audit / Execution 仍必须显示当时 Operator 权限上下文。

---

# 79. Identity Rename Contract

姓名 /邮箱 / display name 变化不改写历史 Principal ID。

Audit 可以展示当前 display name + historical label snapshot，但 primary identity 必须稳定。

---

# 80. External Principal Offboarding Contract

外部 support / vendor access 到期后：

```text
Assignment expires
Principal may become inactive
Sessions handled by auth policy
Historical audit retained
```

不能物理删除导致历史责任链断裂。

---

# 81. Service Principal Ownership Contract

每个 Service Principal 必须有：

```text
Business Owner
Technical Owner
Purpose
Allowed Scope
Credential Reference
Rotation reference
Last Review
Lifecycle State
```

Orphaned service principal 是 access governance issue。

---

# 82. Integration Boundary

34 负责：

```text
Connector credential reference
External system authorization scope
Connector capability
```

36 负责：

```text
Which principal can administer integration
Which principal can enable sensitive capabilities
```

34 的 external credential 不自动成为 36 human permission。

---

# 83. Rules / Notification Boundary

33 可以选择 Recipient Group reference，但：

```text
Recipient Group
≠ Role
≠ Permission Group
```

通知接收者不因为收到 P1 告警就自动拥有 Alarm ACK / Control 权限。

---

# 84. Site Configuration Boundary

35 提供 Canonical Site / Organization / Portfolio identity 和 Site capability。

36 使用这些对象做 scope。

但：

```text
Site capability enabled
≠ Principal authorized
```

两者同时满足才决定 Surface / Action discoverability。

---

# 85. Control / Execution Boundary

25–28 负责：

```text
Control Authority
Precondition
Interlock
Command lifecycle
Readback
Verification
```

36 负责：

```text
Principal may request / approve / view / override
```

因此：

```text
Authorization Allowed
≠ Command Executed
```

---

# 86. Management Review Boundary

30 可以形成：

```text
Decision: require privileged-access review
Decision: reduce vendor access
```

但不能直接修改 Role / Assignment。

需要进入 36 的 Access Change workflow。

---

# 87. Data Privacy Boundary

36 应遵循最小必要展示原则：

- 不默认显示无关个人资料；
- 不暴露 credential secret；
- 不在 audit list 大面积展示敏感 payload；
- 个人/隐私字段的可见性由 privacy / security owner 管理。

本页不是个人档案管理系统。

---

# 88. Permission Contract for 36

典型管理权限：

```text
access.read
access.principal.read
access.role.read
access.role.edit
access.assignment.create
access.assignment.revoke
access.sensitiveGrant.request
access.sensitiveGrant.approve
access.delegation.manage
access.breakGlass.activate
access.breakGlass.review
access.review.run
access.audit.read
access.audit.export
access.policy.read
```

`access.admin` 不能成为隐藏所有细粒度权限的永久万能 permission。

---

# 89. Audit Viewer Permission Boundary

Audit 读取本身可能包含敏感信息。

至少可以区分：

```text
Read Standard Audit
Read Privileged Audit
Read Security-sensitive Evidence
Export Audit
```

拥有业务页面 read permission 不自动拥有完整 audit export 权限。

---

# 90. AI Assistance Boundary

AI 可以：

- 总结 Role / Permission 差异；
- 发现 candidate excessive / dormant access；
- 草拟 Access Review summary；
- 帮助解释 SoD conflict；
- 总结 privileged audit history；
- 生成 Role change impact 草稿。

AI 不能：

- 自动授予权限；
- 自动批准 privileged grant；
- 自动激活 break-glass；
- 自动扩大 Site Scope；
- 自动解除 SoD constraint；
- 自动把 denied request 改成 allowed；
- 自动删除 / 修改 Audit Event；
- 根据使用频率自动撤销关键 OT 权限。

AI suggestion 必须保持 Candidate / Draft。

---

# 91. No Defensive Programming / No Compatibility Design

明确禁止：

```text
access API error
→ []

identity missing
→ anonymous user with default role

principal missing
→ identity ID as principal ID

group membership
→ permission automatically

role missing
→ default viewer

role = admin
→ all permissions

site scope missing
→ all sites

permission exists
→ all resources

frontend hidden
→ backend authorization assumed

SSO success
→ authorized

MFA success
→ sensitive action allowed

session active
→ privileged session

control permission
→ control authority

control permission
→ interlock bypass

control permission
→ safe to control

approval exists
→ user authorized

business object approved
→ action can be executed by requester

edit permission
→ approve permission

approve permission
→ execute permission

manager role
→ inherit all subordinate roles

unused 90 days
→ auto revoke

temporary access expiry missing
→ permanent

delegation
→ copy all delegator permissions

break-glass
→ permanent admin

break-glass
→ safety bypass

policy service unavailable
→ cached allow

unknown authorization
→ allowed

role revoked
→ historical audit deleted

principal disabled
→ historical owner removed

session terminated
→ credential revoked

credential disabled
→ identity deleted

audit event exists
→ business execution succeeded

audit log says control requested
→ control succeeded

audit correlation
→ causation proven

same timestamp
→ same operation

role revision changed
→ reinterpret historical audit

new role assignment
→ rewrite old authorization context

multiple authorization APIs
→ first success wins

legacy admin flag fallback
legacy ACL compatibility adapter
frontend-only permission enforcement
```

正式原则：

> **Identity、Principal、Role、Permission、Scope、Authentication、Authorization、Approval、Control Authority 和 Audit Evidence 是不同事实。Read/Edit/Approve/Execute 必须分离；Sensitive Action 必须满足明确授权与上下文；后台是安全边界，前端只做可用性投影；历史授权与审计不可被当前配置静默改写；Unknown 保持 Unknown。**

---

# 92. Information Architecture

```text
Context Header
↓
[主体] [角色] [权限与范围] [访问审查] [临时/紧急访问] [审计]
↓
Principal Ledger
                         → Principal Inspector
↓
Principal Detail
  Identity / Lifecycle
  Role Assignments
  Site / Resource Scope
  Sensitive Permissions
  Temporary / Delegated Access
  Session / Authentication Context refs
  Access Review
  Historical Access
  Audit
↓
Role Workspace
↓
Permission / Scope Matrix
↓
SoD / Constraint Review
↓
Privileged Access / Break-glass
↓
Audit Ledger / Detail
```

默认是 Ledger + Durable Detail，不做权限卡片墙。

---

# 93. Route / URL State Contract

```text
/settings/access
/settings/access/principals/:principalId
/settings/access/roles/:roleId
/settings/access/audit/:auditEventId
```

Search Params 可包括：

```text
view
principalType
identitySource
role
permission
siteId
risk
status
accessType
expires
reviewState
sodState
auditAction
auditResult
q
```

Stable identity 进入 Path；筛选进入 Search Params。

---

# 94. Capability Gating

36 可见条件：

```text
Access Governance capability exists
AND principal may discover it
```

具体 action 仍需：

```text
principal permission
AND resource scope
AND SoD constraints
AND required auth context
AND workflow state
```

Audit Read capability 可以独立于 Access Admin capability。

---

# 95. Principal Ledger Contract

默认列：

```text
主体
类型
身份源
主要角色
站点范围
高风险权限
临时权限
最近访问审查
状态
Owner
```

异常优先：

```text
Privileged Access Expiring
External Access Expired
SoD Conflict
Orphaned Service Principal
Direct Entitlement
Review Overdue
Disabled Principal With Active Grant
```

---

# 96. Principal Inspector Contract

快速 Inspector 显示：

```text
Principal Identity
Type
Lifecycle
Authentication Source
Active Roles
Site Scope
Sensitive Permissions
Temporary / Delegated Grants
Last Access Review
Current Conflicts
Recent Privileged Audit
```

复杂修改进入 Durable Detail。

---

# 97. Role Ledger Contract

默认列：

```text
角色
用途
风险等级
Permissions
Allowed Scope
成员数
SoD Constraints
当前版本
Owner
```

Role member count 只是治理辅助，不代表权限安全。

---

# 98. Role Detail Contract

至少显示：

```text
Purpose
Permissions
Resource Scope Types
Inherited Roles
Constraints
Sensitive Actions
Authentication requirements
Current Revision
Version History
Assignments
Referenced By
Audit
```

不要只显示 permission checkbox wall。

---

# 99. Permission / Scope Matrix Contract

矩阵可以帮助 reviewer 看：

```text
Role × Permission × Scope Type
```

但它是 review UI，不是 authorization engine。

必须能下钻到实际 Principal Assignment 和 effective period。

---

# 100. SoD Workspace Contract

至少显示：

```text
Constraint
Type: Static / Dynamic
Roles / Permissions involved
Scope
Affected Principals
Current Violations
Exceptions
Owner
Effective Period
```

不能只显示一个 `Conflict = true`。

---

# 101. Access Review Workspace Contract

至少支持：

```text
Review Scope
Reviewer
Due Date
Assignments to Review
Sensitive Grants
Dormant / Direct Grants
Decision
Reason
Enforcement Status
```

Review completion 与 actual access enforcement 分开。

---

# 102. Temporary / Emergency Access Workspace Contract

至少区分：

```text
Temporary Role Grant
Delegation
Vendor / External Access
Emergency / Break-glass
```

默认按 expiry、risk、scope、sponsor 排序，而不是简单用户列表。

---

# 103. Audit Ledger Contract

默认列：

```text
时间
主体
动作
对象
站点 / Scope
结果
风险级别
关联业务对象
Correlation / Execution
```

可快速筛选 privileged actions。

---

# 104. Audit Detail Layout

推荐：

```text
Event Summary
Principal / Session Context
Authorization Context
Action / Resource
Before / After
Reason
Result
Related Approval / Change
Related Execution / Business Result
Evidence / Raw Detail
```

这样用户能区分“授权和动作记录”与“业务最终结果”。

---

# 105. Empty / Partial / Unknown / Error States

## Empty

新 Organization 无 Principal assignment 是合法 Empty。

## Partial

Identity directory 可读，但 audit backend 暂不可用。

## Unknown

例如 external IdP 未提供 authentication assurance detail。

## Error

Authorization governance service 请求失败明确显示 Request Failed。

禁止：

```text
service error → no access
unknown scope → all sites
unknown auth context → MFA passed
```

---

# 106. Accessibility / Responsive Contract

- Role / Permission / Scope 不能只靠颜色；
- SoD conflict 有文本解释；
- 高风险权限有明确名称和 scope；
- Audit Before / After 可由 screen reader 理解；
- Permission Matrix 提供 table semantics / list alternative；
- 768px 下仍能完成 Principal lookup、查看 Role / Scope / temporary access / audit；
- 高风险 grant/revoke dialog 不因窄屏省略 scope、expiry、reason、approver。

---

# 107. Wireframe Intent

```text
┌────────────────────────────────────────────────────────────────────────────┐
│ 用户、权限与审计                                       生产环境            │
├────────────────────────────────────────────────────────────────────────────┤
│ [主体] [角色] [权限与范围] [访问审查] [临时/紧急访问] [审计]             │
├────────────────────────────────────────────────────────────────────────────┤
│ 主体                  类型      角色              站点范围       风险       │
│ 王工                  Human     Site Operator     SG-01          普通       │
│ 李工                  Human     Control Engineer  SG-01          高风险     │
│ Vendor-A / Chen       External  Maintainer        SG-01/Plant    3天后到期  │
│ svc-energy-analytics  Service   Energy Reader     SG Portfolio   待复核     │
├───────────────────────────────────────────────┬────────────────────────────┤
│ 李工 · Principal P-108                       │ 快速检查                   │
│ Identity: enterprise SSO                      │ MFA: current               │
│ Roles: Control Engineer                       │ Sensitive: control.request │
│ Site Scope: SG-01                             │ override.request: No       │
│ Temporary Grants: none                        │ Last Review: 2026-08-01    │
│ SoD: no active conflict                       │ [查看审计]                 │
├────────────────────────────────────────────────────────────────────────────┤
│ 高风险权限待处理                                                             │
│ Vendor-A / Chen · Temporary Maintainer · expires 2026-09-18                 │
│ Scope: SG-01 / Central Plant · Read + Maintenance Diagnostics              │
│ Control: No · Access review required                                        │
├────────────────────────────────────────────────────────────────────────────┤
│ 最近高风险审计                                                               │
│ 09:02 李工  Requested control CHWS SP 7.2°C → Execution EX-2047            │
│ 09:05 张工  Approved Strategy STR-18 v7                                     │
│ 09:07 Admin Changed Role Control Engineer rev4 → rev5                       │
└────────────────────────────────────────────────────────────────────────────┘
```

---

# 108. Browser Acceptance Criteria

## Identity / Principal

- Identity / Principal 分开；
- Human / Service / External principal 分开；
- Principal disable 不删除历史；
- shared external credential 不冒充 shared application identity。

## Role / Permission / Scope

- Role / Permission 分开；
- Permission / Site Scope 分开；
- Group / Role 分开；
- Role hierarchy / org hierarchy 分开；
- Read/Edit/Approve/Publish/Execute 可独立授权；
- backend enforce resource scope。

## Authentication / Authorization

- SSO / MFA 不冒充 authorization；
- sensitive actions 可要求 step-up；
- policy unavailable 不 fallback allow；
- frontend hidden 不作为安全边界。

## Control / OT

- control permission 不冒充 Control Authority；
- permission 不绕过 Interlock；
- break-glass 不绕过 safety；
- high-risk control audit 能 deep-link 28 Execution。

## Separation of Duties

- Static / Dynamic SoD 可区分；
- own-object approval constraints 由 policy owner enforce；
- exceptions 有 reason / owner / expiry / audit。

## Temporary / Emergency

- temporary grant 有 start / expiry；
- delegation 不复制全部权限；
- vendor access 有 sponsor / scope / expiry；
- break-glass 有 post-use review。

## Audit

- Audit Event / Business Execution 分开；
- Before / After / Reason / Principal / Scope 可追溯；
- Audit correlation 不冒充 causation；
- historical audit 保留当时 authorization context；
- privileged actions 可独立筛选。

## Historical Integrity

- Role / assignment revision 不改写旧 audit；
- revoke / disable 不删除历史；
- current display name 不替代 stable Principal ID；
- access history 可按 effective period 复原。

## Implementation Integrity

- 无 `admin=true` 万能 fallback；
- 无 frontend-only authorization；
- 无 all-sites fallback；
- 无 legacy ACL compatibility adapter；
- 无 control permission → safe control shortcut；
- review scenario 无 runtime / network error。

---

# 109. Explicit Non-goals

36 不是：

- Identity Provider；
- Password Manager；
- Secret Vault；
- PAM replacement；
- SIEM；
- HR directory；
- Network access control platform；
- Safety interlock authority；
- Generic policy-code IDE；
- Browser token debugger；
- Universal user profile page。

---

# 110. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Identity / Principal 分离；
- Human / Service / External Principal 分离；
- Role / Permission / Scope 分离；
- Group / Role 分离；
- Site Scope / Action Permission 分离；
- Read / Edit / Approve / Publish / Execute 分离；
- Authentication / Authorization 分离；
- Session / Privileged Session 分离；
- Step-up / Permission / Business Approval 分离；
- Control Permission / Control Authority / Interlock 分离；
- Static / Dynamic SoD 明确；
- Temporary Access / Delegation / Emergency Access 分离；
- Access Review / Enforcement 分离；
- Current / Historical Access 分离；
- Audit Event / Business Execution / Physical Outcome 分离；
- Before / After / Reason / Correlation / Evidence 明确；
- privileged action audit 明确；
- backend security boundary 明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
