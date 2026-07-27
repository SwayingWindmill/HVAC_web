# HVAC Web Real Mode Shell 决策清单

## 状态

Accepted on 2026-07-28.

本文件是第一阶段 Real Mode Shell 的正式实施决策基线，不推进 S4–S7，也不授权业务页面批量实现。

- Q31–Q60 已全部确认采用推荐答案 A。
- 所有实施、拆票、契约和验收必须以本文为准。
- 任何偏离均需先更新本文；涉及认证、授权、Site Scope、数据权威或构建隔离的变更还需新增或更新 ADR。
- 上位约束为 `docs/adr/0007-hvac-web-real-mode-data-authority.md` 和 `docs/operations/hvac-web-real-integration-decisions.md`。

## 已接受答案

```text
Q31=A
Q32=A
Q33=A
Q34=A
Q35=A
Q36=A
Q37=A
Q38=A
Q39=A
Q40=A
Q41=A
Q42=A
Q43=A
Q44=A
Q45=A
Q46=A
Q47=A
Q48=A
Q49=A
Q50=A
Q51=A
Q52=A
Q53=A
Q54=A
Q55=A
Q56=A
Q57=A
Q58=A
Q59=A
Q60=A
```

当前代码事实：

- `API_MODE` 已是构建时配置，但 Header 仍提供运行时 Demo Switch；
- Header 在 Real Mode 仍允许切换本地 `demo/ops/rd` 角色；
- Header 告警铃铛直接读取 `mockAlarms`；
- `AppShell` 无条件挂载 Mock 全局 AI；
- `auth.ts` 仍从 `localStorage` 读取 Bearer Token，与 BFF Session Cookie 架构冲突；
- Site 仍同时使用 Registry UUIDv7 和本地 `b1/b2`；
- 菜单权限仍由本地 Role Map 决定，而不是服务端有效授权。

回答格式可使用：

```text
Q32=A
Q33=A
...
Q60=A
```

---

## 一、构建与部署边界

### Q31 — Real Mode 与 Demo Mode 如何部署？

- **A. 独立构建产物和独立部署入口，模式在构建时固定，运行时不可切换。**
- **B. 同一部署使用 `/real` 和 `/demo` 路径。**
- **C. 同一页面使用开关切换。**

**已确认：A。** Real Build 不得包含或动态加载 Mock 业务数据、Mock AI、Mock 角色和 Demo-only 状态机。

### Q32 — 两种构建如何共享代码？

- **A. 同一代码库、两个显式入口和 Feature Manifest；Real 入口在模块依赖图上不能引用 Demo 模块。**
- **B. 一个入口，根据运行时变量隐藏 Demo 组件。**
- **C. 复制两套前端项目。**

**推荐：A。** 共享纯 UI、类型和格式化组件，但 `src/mock`、Demo Store、Mock AI 和 Demo 页面只能从 Demo 入口可达。CI 对 Real Bundle 做依赖和产物扫描。

### Q33 — 模式如何在界面中表达？

- **A. Demo 环境持续显示明显“演示数据”标识；Real 环境删除 Demo Switch，非生产 Real 环境只显示环境标识。**
- **B. Real 环境也保留 Demo Switch，默认关闭。**
- **C. 两种环境外观完全相同。**

**推荐：A。** Demo 必须可见，Real 不应暗示存在运行时切换能力。

### Q34 — Real Build 配置异常时如何处理？

- **A. 启动时校验构建模式、Gateway 同源路径、Realtime 协议和 Build Identity；异常则阻断 Shell。**
- **B. 使用默认地址继续运行。**
- **C. 自动切到 Demo。**

**推荐：A。** Fail Closed，并显示不包含 Secret 的配置错误和 Build Commit。

---

## 二、认证与会话

### Q35 — Real Mode 使用哪种浏览器认证？

- **A. 只使用 Gateway/BFF 的不透明 `HttpOnly` Session Cookie。**
- **B. 从 `localStorage` 读取 Bearer Token。**
- **C. Cookie 与 Bearer Token 同时支持，由页面自行选择。**

**推荐：A。** 删除 `auth.ts` 中的 `hvac_token` 读取；Token 不进入浏览器 JavaScript、URL 或 WebSocket 查询参数。

### Q36 — 未登录用户如何进入系统？

- **A. Shell 调用 `/api/v1/principal` 收到 401 后，跳转 `/api/v1/auth/login?returnTo=<同源路径>`。**
- **B. 前端显示用户名密码表单。**
- **C. 自动使用测试账号。**

**推荐：A。** `returnTo` 只能是同源相对路径，禁止开放重定向。

### Q37 — Shell 启动时是否允许先渲染业务页面？

- **A. Principal、Session 和基础授权未解析前，只显示 Shell Bootstrap 状态，不挂载业务路由和实时订阅。**
- **B. 先显示页面，再异步隐藏无权限内容。**

**推荐：A。** 防止越权闪烁、错误请求和 Mock/默认上下文短暂出现。

### Q38 — Session 过期或被撤销后如何处理？

- **A. 立即停止实时连接、取消请求、清空内存缓存和敏感草稿，显示“会话已失效”，再进入登录。**
- **B. 保留页面最后数据并继续操作。**
- **C. 自动切换为 Demo。**

**推荐：A。** Last Known Value 只能在有正式语义的业务页面显示，不能作为失效会话下的可操作状态。

### Q39 — Logout 的完成语义是什么？

- **A. 使用当前 Principal 的 CSRF 调用 `POST /api/v1/auth/logout`；204 或明确已失效后清空本地状态并跳转登录。**
- **B. 只删除浏览器缓存。**
- **C. 直接关闭标签页。**

**推荐：A。** 对可重试服务失败不能宣称服务端 Session 已撤销；应显示失败并允许重试。

### Q40 — CSRF Token 如何保存？

- **A. 仅保存在当前内存 Principal Snapshot 中；不写入 Local Storage、Session Storage、URL 或日志。**
- **B. 写入 Local Storage，避免重复读取 Principal。**

**推荐：A。** Principal/Session 变化时自动替换，退出或失效时立即清除。

### Q41 — Login `returnTo` 如何防止开放重定向？

- **A. 前端只生成站内相对路径，Gateway 再执行允许列表和规范化校验。**
- **B. 接受任意完整 URL。**

**推荐：A。** 丢弃协议、主机、双斜杠和控制字符形式的目标。

---

## 三、Principal、授权与菜单

### Q42 — Real Mode 的权限来源是什么？

- **A. 服务端返回的有效 Capability/Policy Snapshot；Role 只用于展示和审计上下文。**
- **B. 继续使用前端 `ROLE_RULES`。**
- **C. 只根据菜单配置判断。**

**推荐：A。** 当前 Principal Contract 只有字符串 Roles，需补充可生成客户端的有效 Capability 或专用授权目录契约。

### Q43 — Real Mode 是否保留本地角色切换器？

- **A. 删除；只展示当前 Principal、角色和 Policy Revision。**
- **B. 保留，用于快速预览不同权限。**

**推荐：A。** 本地角色切换只属于 Demo Build。

### Q44 — 菜单项如何由授权和功能状态共同决定？

- **A. 使用 Build Feature Manifest × Server Availability × Effective Capability 三者交集。**
- **B. 只看本地 Role Map。**
- **C. 所有菜单始终可点击。**

**推荐：A。** 规则如下：

- 已实现且有权限：正常显示；
- 已实现但无权限：菜单隐藏，直接 URL 返回 Access Denied；
- 产品已确认但尚未接入：按 Q5 保留为禁用/“尚未接入”入口；
- 当前部署未启用：按部署配置隐藏；
- 未知路由：404。

### Q45 — 用户直接访问无权限 URL 时怎么处理？

- **A. 显示统一 Access Denied，不泄露资源是否存在；提供返回当前 Site Dashboard 的动作。**
- **B. 跳到 404。**
- **C. 自动提升为只读。**

**推荐：A。** 单资源 API 仍遵守资源不可见语义，前端不能根据错误差异推断对象存在。

### Q46 — “尚未接入”页面采用什么语义？

- **A. 稳定的 Not Integrated 页面，说明该模块没有权威后端、不会显示演示数据，并列出所需能力。**
- **B. 显示空表格。**
- **C. 加载 Demo 页面。**

**推荐：A。** 与“无权限”“服务不可用”“当前无数据”严格区分。

---

## 四、Site 导航与 Scope

### Q47 — Site 如何进入 URL？

- **A. 所有 Site 级页面使用 `/sites/{siteId}/...` 路径；平台级页面保持独立路径。**
- **B. 使用 `?siteId=` 查询参数。**
- **C. 只保存在 Zustand。**

**推荐：A。** 示例：

```text
/sites/{siteId}/dashboard
/sites/{siteId}/assets
/sites/{siteId}/energy/month
/sites/{siteId}/commands
/system
```

### Q48 — 登录后的初始 Site 如何选择？

- **A. 有有效 URL 就使用 URL；只有一个授权 Site 时自动重定向；多个 Site 时显示 Site Chooser。**
- **B. 总是自动选择列表第一项。**
- **C. 使用上次 Local Storage 中的 Site 并跳过校验。**

**推荐：A。** 可将上次 Site 作为非权威提示，但必须重新授权，且不能覆盖显式 URL。

### Q49 — URL 中 Site 不存在或不可见时如何处理？

- **A. 显示“Site 不可见或不存在”的统一状态，不泄露原因，并允许选择其他授权 Site。**
- **B. 自动换成第一个 Site。**
- **C. 显示 Demo Building。**

**推荐：A。** 禁止静默改变用户正在查看的 Scope。

### Q50 — 当前账号没有授权 Site 时如何处理？

- **A. 显示专用 No Authorized Site 页面，只提供账号信息、刷新、帮助信息和退出。**
- **B. 显示空 Dashboard。**
- **C. 自动创建 Site。**

**推荐：A。** 不挂载 Site 级页面和实时连接。

### Q51 — 第一阶段是否支持跨 Organization 切换 Site？

- **A. 暂不支持。Site Selector 只列出当前 `actingOrganizationId` 下的授权 Site；等显式 Context Switch API 后再开放。**
- **B. 直接列出所有 Organization 的 Site。**
- **C. 前端修改 Organization Header。**

**推荐：A。** 当前 Telemetry 客户端要求 Device owning Organization 与 Principal acting Organization 一致，但现有公共 Contract 没有安全的 Acting Organization 切换端点。

### Q52 — 切换 Site 时必须清理哪些状态？

- **A. 取消旧 Site 请求，关闭实时 Session，清除 Site-scoped Query Cache、选中 Device、页面草稿和业务临时状态。**
- **B. 只替换 Header 文本。**
- **C. 保留旧数据直到新数据返回。**

**推荐：A。** Theme、语言和 Sidebar 折叠等非业务偏好可保留；未保存表单先提示用户。

### Q53 — Site Context 在代码中如何表示？

- **A. 由路由参数和已验证 Registry Site 组成只读 `SiteContext`；删除 Real Mode 的 `buildingId` 业务语义。**
- **B. 继续以全局 Zustand `buildingId` 为准。**

**推荐：A。** API Hook 必须显式接收 Site/Organization Scope，不能从隐式 Header 或全局变量猜测。

---

## 五、全局组件与 Shell 状态

### Q54 — Real Build 是否挂载全局 AI 助手？

- **A. 不挂载；在正式 AI Investigation 能力确认前仅 Demo Build 包含。**
- **B. 保留 Mock AI，并标记演示。**
- **C. 保留入口但允许读取页面 DOM。**

**推荐：A。** 与暂不推进 S5/S6 的决定一致。

### Q55 — Header 告警铃铛如何处理？

- **A. Real Build 暂时移除；Alarm 领域接入后再使用真实 Ordered Events 和查询 API。**
- **B. 保留 Mock 数量。**
- **C. 根据遥测阈值在浏览器临时生成告警。**

**推荐：A。** 不显示 `0`，因为 `0` 会被理解为“已查询且没有告警”。

### Q56 — Header 实时状态如何表达？

- **A. 只显示当前页面/当前 Site 的真实订阅状态：未订阅、连接中、Live、重连、需 Resync、不可用。**
- **B. 只要 Real Mode 就显示“S2 实时”。**
- **C. 显示全局绿色圆点。**

**推荐：A。** Shell 不得把设备级 Session 描述为全平台连接健康。

### Q57 — BigScreen 从哪里进入？

- **A. Real Build 使用当前 Site 的 `/sites/{siteId}/bigscreen`，并复用同一 Read Model；Demo Build 使用独立 Demo 数据。**
- **B. 保持全局 `/bigscreen` 并自行选择静态 Site。**

**推荐：A。** 无 Site 时不能进入 Real BigScreen。

### Q58 — Shell 的统一状态模型是什么？

- **A. 明确定义 `BOOTSTRAPPING`、`LOGIN_REQUIRED`、`NO_AUTHORIZED_SITE`、`FORBIDDEN`、`NOT_INTEGRATED`、`UNAVAILABLE`、`DEGRADED`、`READY`。**
- **B. 所有异常统一显示“暂无数据”。**

**推荐：A。** 每个状态定义允许的导航、重试、注销和诊断信息；服务错误显示安全 Detail、Trace ID 和可重试性。

---

## 六、缓存、观测与验收

### Q59 — Real Mode 可以持久化哪些浏览器状态？

- **A. 只持久化非敏感 UI 偏好；Principal、CSRF、Capability、Registry 数据、Telemetry、Command 和业务状态只在内存或服务端。**
- **B. 所有 React Query Cache 都持久化到 Local Storage。**
- **C. 保存最后业务响应用于离线操作。**

**推荐：A。** Site 由 URL 表达；如保存“最近 Site”，只能作为重新授权后的导航建议。

### Q60 — Real Mode Shell 的完成门禁是什么？

- **A. 自动化验证完整身份、授权、Site Scope、清理、错误和 Bundle 边界。**
- **B. 手工打开首页正常即可。**

**推荐：A。** 最低门禁：

1. Real Bundle 依赖图和产物中不存在 `src/mock`、Mock AI、Demo Store 和 Demo 角色切换代码；
2. 浏览器不读取或写入 Auth Token、CSRF、业务响应到 Local Storage；
3. 401 登录跳转和同源 `returnTo` 正确；
4. Principal Bootstrap 期间不挂载业务路由；
5. Capability 决定菜单和 Route Guard，前端 Role Map 不参与真实授权；
6. 单 Site 自动进入，多 Site 进入 Chooser，无 Site 进入专用状态；
7. 无效/不可见 Site 不静默改 Scope；
8. Site 切换关闭旧实时 Session 并清除旧 Scope Cache；
9. Session 失效、Logout、Policy Revision 变化会 Purge；
10. Real Header 无 Demo Switch、Mock Role Switch、Mock Bell 和 Mock AI；
11. 未接入、无权限、无数据、部分覆盖、服务不可用有不同页面状态；
12. 所有关键错误可显示 Trace ID，日志不包含 Token、CSRF 或敏感业务值；
13. Desktop、Tablet、Mobile 的 Site Chooser、Access Denied 和 Session Expired 流程通过浏览器测试；
14. 构建、类型检查、Lint、Contract Generation Diff 和 E2E 全部通过。

---

## 推荐默认答案集合

```text
Q31=A
Q32=A
Q33=A
Q34=A
Q35=A
Q36=A
Q37=A
Q38=A
Q39=A
Q40=A
Q41=A
Q42=A
Q43=A
Q44=A
Q45=A
Q46=A
Q47=A
Q48=A
Q49=A
Q50=A
Q51=A
Q52=A
Q53=A
Q54=A
Q55=A
Q56=A
Q57=A
Q58=A
Q59=A
Q60=A
```

采用该集合后，Real Mode Shell 将成为一个基于 BFF Session、服务端有效授权、显式 Site Scope 和严格数据权威的应用外壳；Demo 功能在构建和部署层面完全隔离。