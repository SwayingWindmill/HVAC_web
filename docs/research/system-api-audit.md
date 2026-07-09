# 系统管理（/system）后端 API 核实摘要

> 关联票：#18（调研）· 地图 #13 · 下游 #19（/system 页面实装）
> 核实方式：静态代码核查 `E:\Code\hvac-backend`（NestJS 10 + TS 模块化单体），未改动任何文件
> 核实日期：2026-07-09

## 背景事实（已确认，非本次新发现）

- 多租户边界 = `X-Site-Id` 头（= ThingsBoard Customer ID）。`GET /auth/sites` + `PUT /auth/me/active-site` 已存在。
- 鉴权链：`JwtAuthGuard → RolesGuard → ScopesGuard`。角色枚举 `ADMIN/MAINTENANCE/READONLY`。
- 可选 Logto(OIDC) IdP（`AUTH_MODE=legacy|dual|logto`）。**关键细节**：`ScopesGuard` 仅当 `authProvider==='logto'` 时校验 scope；legacy JWT 用户直接放行 → legacy 模式下权限完全由 role 决定，scope 仅对 Logto 用户生效。
- 资产层级完全由 **ThingsBoard 资产树**承载，后端无本地 building/zone/unit 建模。

## 子域 1 — IAM 用户 / 角色管理

唯一相关控制器 `IamController`（`@Controller('auth')`），全部面向「当前用户自身」或「站点绑定」，**无管理员维度的用户管理端点**。

| 能力 | 端点 | 状态 | 说明 |
|------|------|------|------|
| 列举用户 | — | ❌ 不存在 | `iam.service` 无 `findAllUsers`/`listUsers` |
| 管理员创建用户（带角色） | — | ❌ 不存在 | 唯一创建入口 `POST /auth/register` 为**自注册**，DTO 仅 `username/password/phone/email`，缺 role/scope，落库 role 默认 `READONLY` |
| 改用户角色 / scope | — | ❌ 不存在 | 全仓零命中 `updateRole/setRole/assignRole/changeScope` |
| 自定义角色 / 列 scope | — | ❌ 不存在 | `UserRole` 为固定 enum；`AuthScope` 是 9 个硬编码常量（`src/iam/constants/auth-scopes.constants.ts`），无 list-scopes 端点、无 scope 列 |

**裁决：missing entirely（需后端从零搭建用户管理 controller/service）**

> 备注：9 个 scope 常量已硬编码可知（`asset:read/device:read/device:write/telemetry:read/command:send/schedule:read/schedule:write/site:manage` + 1 个），v1 可把 scope 目录作为**静态常量**呈现，无需后端。

## 子域 2 — 站点 / 建筑 / 区域 / 单元管理

| 能力 | 端点 | 状态 | 说明 |
|------|------|------|------|
| 读资产树 | `GET /assets/tree` | ✅ 已实现 | `@Scopes(AssetRead)`，经 `X-Site-Id` 隔离。已被 /assets 页消费 |
| 设备列表/详情/状态 | `GET /devices` 系列 | ✅ 已实现 | 读写均透传 ThingsBoard |
| 创建/更新/删除 建筑/区域/单元 | — | ❌ 不存在 | 无 building/zone/unit 实体或 controller；资产层级全在 TB |

**裁决：写操作 missing entirely；只读树 usable directly**

## 子域 3 — 操作审计日志

| 能力 | 端点 | 状态 | 说明 |
|------|------|------|------|
| 审计实体 | — | ✅ 已实现 | `audit_logs` 表（`src/common/entities/audit-log.entity.ts`）：`id/traceId/eventType(14种)/userId/targetEntity/targetId/action/result(SUCCESS\|FAILURE)/details(jsonb)/ipAddress/userAgent/createdAt` |
| 写审计 | （服务内调用） | ✅ 已实现 | `AuditLoggerService.log()` 被 command/site-binding 等服务注入自动落库；无独立 CREATE 端点 |
| 列举/查询审计 | — | ❌ 不存在 | `AuditLoggerService` 仅 `log()`+`save()`，无 `find/findAndCount`；全仓无 audit controller、无 `GET /audit`、无分页/时间范围/操作人筛选端点 |

**裁决：needs backend work（实体与写入就绪，缺 LIST/查询端点 + controller）**

## 总览裁决

| 子域 | 裁决 | 后端工作量 |
|------|------|-----------|
| ① IAM 用户/角色 | missing entirely | 高（新建整条 controller/service） |
| ② 建筑/区域/单元写 | missing entirely（只读树可用） | 高（对接 TB 资产 API 或本地建模） |
| ③ 审计日志 | needs backend work | 中（补 LIST/分页查询端点） |

## v1 /system 页面 mock vs 真接口边界

基于上述裁决，#19 v1 页面按下表划分边界（**优先 mock-first 离线可演示，待后端补齐再切真接口**）：

### ① 用户与角色管理区块 → 全 mock
- 用户列表、角色分配、scope 勾选 UI 全部 mock。
- **scope 目录**用静态常量（9 个硬编码 scope，已知）呈现，不从后端拉。
- 真实落地依赖：后端新建用户管理端点（不在 v1 范围）。

### ② 站点 / 资产结构区块 → 只读真接口 + 写操作 mock
- **资产树展示**：直接消费已存在的 `GET /assets/tree`（真接口，与 /assets 页同源）。
- **新增/编辑/删除 建筑·区域·单元**：mock 表单 + 乐观 UI，后端无写端点；按钮可保留但提交走 mock（标注「后端待补」或演示模式生效）。
- 站点切换：沿用已存在的 `GET /auth/sites` + `PUT /auth/me/active-site`（真接口）。

### ③ 操作审计日志区块 → 列表 mock（写入真已实现）
- 审计**列表** mock（后端无读取端点），但**实体 shape 严格对齐真实 `audit_logs`**（14 种 eventType + result + details jsonb + ip/userAgent + createdAt），保证切真接口时零改造成本。
- 真实落地依赖：后端补 `GET /audit`（分页/筛选）。

### 跨切面
- 所有 mock 数据形状对齐真实实体/DTO，集中放 `src/mock/system.ts`，与既有 `src/mock/data.ts` 同范式。
- 沿用 `#7` 范式的人「人在回路」红线：/system 的「保存/下发」类动作在 v1 仅 mock，不直连真实写链路。
- 只读红线 Tag 同 /ai 页，避免任何误以为已接通真实写操作。

## 下一步
- 解锁 #19（/system 实装）：依据本摘要的边界，v1 用 mock 覆盖①全量、②写操作、③列表；真接口仅取 /assets/tree 与 /auth/sites。
- 若后端先行补齐①/②/③，再回头把对应区块从 mock 切 `src/api`。
