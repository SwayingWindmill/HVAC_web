# 数据刷新与实时性（规格章节 · 对应 wayfinder #8）

> 调研结论，锁定前端「实时遥测 vs 历史回测」的数据获取与共存策略。
> 调研基于后端代码实测（E:\Code\hvac-backend，NestJS 10 + Socket.IO），非假设。

## 1. 后端事实（已实测）

### 1.1 实时传输：Socket.IO 网关 `telemetry.gateway.ts`
- 路径/命名空间：`/ws/telemetry`（连接 `wss://host/ws/telemetry`）。
- 鉴权：握手阶段读 `client.handshake.query.token`，回退到 `Authorization: Bearer` 头；无/无效 token 直接断开。`subscribe`/`unsubscribe` 另受 `WsJwtGuard` 保护。
- 客户端 → 服务端事件：
  - `subscribe` ← `{ cmdId?, deviceId:string, keys:string[] }`
  - `unsubscribe` ← `{ cmdId?, deviceId:string }`
- 服务端 → 客户端事件：
  - `subscribe_ack` / `unsubscribe_ack` ← `{ event, data:{ cmdId, status:'ok', deviceId } }`
  - `telemetry` 推送 ← `{ deviceId, key, value, ts }`（**逐 key 逐连接**发出）
- 订阅粒度：**仅按设备**（无站点级订阅）；`keys:['*']` 可收该设备全部键；多设备 = 发多条 `subscribe`。
- 心跳/重连：无自定义选项，用 Socket.IO 默认（ping 25s / pingTimeout 20s / 自动 upgrade）。Ack 为内联返回对象，无 callback ack。
- 节流/批处理：**无**。`telemetry` 模块里唯一的 `server.emit` 就是 `telemetry` 事件——**服务端不推告警/FDD 事件**（#4 缺口①，需前端派生或等后端补）。

### 1.2 历史/批量：REST 遥测端点（全局前缀 `api/v1`）
- `GET /api/v1/telemetry/devices/:id/latest?keys=` → `{ key: { ts, value } }`（`keys` 必填）
- `GET /api/v1/telemetry/devices/:id/timeseries?keys=&startTs=&endTs=&limit=&agg=` → `{ key: [{ts, value}] }`
  - `agg` ∈ `NONE/AVG/MIN/MAX/SUM/COUNT`，**直传 ThingsBoard**，后端不再二次聚合
- `POST /api/v1/telemetry/latest/batch`（body `{ deviceIds:string[], keys:string[] }`）→ 每设备 latest 快照
- `GET /api/v1/devices/:id/telemetry?keys=&range=1h|6h|24h`（便捷封装）
- 鉴权：`telemetry:read` scope + `X-Site-Id` 头（服务端校验 device∈site）。
- 缓存：仅 `latest` 走 Redis（**无 TTL**）；`timeseries`/`batch` 无缓存。
- 响应包体不一致（#4 缺口③，前端需 tolerant 包装：兼容 `code:0` / `200` / 裸 `data`）。

## 2. 决策（刷新架构）

### 2.1 传输选型：**实时走 WS，历史走 REST，不引入 SSE/轮询**
后端已提供 `/ws/telemetry`，**前端不再自建 SSE 或定时轮询实时数据**——多一套传输=多一份维护与状态不一致。仅在 WS 不可达时提供**降级轮询**（见 2.6），默认关闭。

### 2.2 分层：`TelemetryClient` 单例 + React Query 历史缓存（实现见 #10）
- **实时层**：一个全局单例 Socket.IO 客户端（连接一次），对外暴露 `subscribe(deviceId, keys, onPush)` / `unsubscribe(deviceId, keys)`。内部用**订阅登记表**（按 deviceId+key 引用计数）让多个组件共享同一条 WS，避免重复订阅。
- **历史层**：React Query（`@tanstack/react-query`）封装 REST `timeseries`/`latest/batch`，用 `staleTime` 做客户端缓存（历史视图 30–60s；快照页挂载时拉 `latest/batch` 做初值）。
- 两层都从 `src/api`（#4 契约）取数据，mock 阶段可用同一接口形状切换。

### 2.3 站点上下文
- 每个 REST 调用带 `X-Site-Id`（来自 UI store 当前建筑）。
- WS 只在当前站点内的设备订阅（服务端也会校验 device∈site，前端提前过滤避免无效订阅）。

### 2.4 鉴权
- WS 握手携带 JWT（来自 auth store）。JWT 刷新后需重连以换新 token（接管 `connect` 时若 token 变化则 `disconnect`→重连，或后端支持 token 刷新事件时切）。

### 2.5 重连与数据修复
- 依赖 Socket.IO 自动重连；`connect` 成功后**重发所有活跃订阅**，再拉一次 `latest` REST 回填缺口（WS 断连期间的遥测用 latest 快照补齐，不要求逐点精确）。
- UI 更新做**合并缓冲**：WS 推送进入缓冲，按 `requestAnimationFrame` 批量 flush，避免后端无节流导致的高频重渲染卡顿（驾驶舱多 KPI 同屏尤需）。

### 2.6 降级模式（可选，默认关）
- WS 连续重连失败 N 次后，标记 `realtimeDegraded`，实时卡降级为对 `latest` 的轻量轮询（如 10s 一次，仅刷新数值，不画实时曲线尾部）。恢复 WS 后切回。

### 2.7 告警/FDD 缺口的桥接
- 服务端**不**经 WS 推告警/FDD（#4 缺口① / 后端 #26）。v1 前端**从遥测流客户端派生阈值告警**（如温度超上限、COP 低于阈值），仅作「优先处理」摘要；待后端补告警事件后，改为订阅服务端事件、关闭客户端派生。
- FDD 诊断流的刷新同样先吃遥测派生指标，后端补 FDD 事件后切换（#7 范式已预留）。

## 3. 实时驾驶舱 vs 历史分析页 的共存与切换

| 维度 | 实时驾驶舱（`/dashboard` 等） | 历史分析页（趋势/报表） |
|------|------|------|
| 实时数值(KPI/仪表) | WS 订阅，rAF 合并刷新 | 不订阅，挂载时拉 `latest` 快照 |
| 趋势曲线 | WS 推尾部点 + 历史 `timeseries` 回填 | 纯 `timeseries` + 日期范围选择器 |
| 缓存 | 不缓存实时值（WS 即真相） | React Query `staleTime` 30–60s |
| 多设备 | `latest/batch` 初值 + 逐设备 `subscribe` | `timeseries` 循环/批量拉取 |
| 告警 | WS 派生阈值告警常驻 | 不常驻，进 `/alarms` 看工单 |

- **切换语义**：同一指标在驾驶舱用 WS 保持「活」，在分析页用 REST 做「回望」。两页不冲突——驾驶舱不缓存历史，分析页不占 WS 连接（除非开「实时尾巴」开关）。

## 4. 待办/开放项
- **实现票 #10**：落地 `TelemetryClient`（WS 单例 + 订阅登记表 + 重连修复 + rAF 合并）与 React Query 历史缓存封装，接 `src/api`。
- 后端 #26（告警/FDD 事件）、#27（能耗聚合端点）、#28（统一响应包体）落地后，前端对应切换数据源（见 #4 缺口）。
- 降级轮询阈值（`N` 次失败）、rAF 合并窗口（建议 100–200ms）按真机压测微调。

## 5. 与既有决议的关系
- 依赖的数据模型已由 #4 后端契约澄清（原 #8 阻塞项 T3 数据模型已被 #4 解锁）。
- 设计语言沿用 #6（teal + 语义状态色 + 深浅主题）；实时刷新不改变视觉，只改变数据来源。
- 角色/站点上下文沿用 #5 RBAC 与 #2 全局设备树。
