# HVAC Web 前后端真实化讨论清单

## 状态

Accepted on 2026-07-28.

本文件是在不推进 S4–S7 的前提下，对 HVAC Web 真实产品范围、数据所有权和后端接入方式的正式决策基线。它授权后续按本文顺序制定实现计划，但不授权批量替换 Mock、创建无主数据接口、启用生产控制或绕过各领域自己的发布门禁。

## 已确认决定

### D1 — Real Mode 零 Mock fallback

`VITE_API_MODE=real` 时，每个业务事实必须来自权威平台 API，或来自有明确定义的权威派生 Read Model。接口缺失、不可用、无权限或覆盖不完整时，页面显示明确的不可用、未接入、无权限或降级状态；不得回退到 Mock、Legacy、浏览器临时状态或编造值。

### D2 — 现有页面是候选范围，不是后端建设合同

逐页决定保留、缩减、合并、仅 Demo 或删除。不能因为 UI 中已经存在按钮、状态机或精确数字，就默认必须建设对应后端领域。

### D3 — Q3–Q30 采用推荐答案

用户已确认本文 Q3–Q30 全部采用各题推荐答案。后续实现、拆票、契约和验收必须以“建议的默认答案集合”为准；任何偏离均需先更新本文，涉及难以逆转的所有权、数据权威或安全边界时还需新增 ADR。

### D4 — Real Mode 与 Demo Mode 独立部署

用户已确认 Q31=A。Real Mode 与 Demo Mode 由独立构建产物和独立部署入口提供，模式在构建时固定，运行时不可切换。Real Mode 构建不得包含或动态加载 Mock 业务数据、Mock 角色切换、Mock AI 或 Demo-only 业务状态。

### D5 — Real Mode Shell 采用 Q32–Q60 推荐答案

用户已确认 Q32–Q60 全部采用推荐答案 A。认证、会话、Principal Bootstrap、有效 Capability、Site-scoped 路由、跨 Scope 清理、全局组件隔离、缓存边界和完成门禁以 `docs/operations/hvac-web-real-mode-shell-decisions.md` 为正式实施基线。

## 已接受答案

```text
Q3=A
Q4=A
Q5=B
Q6=B
Q7=A
Q8=B
Q9=A
Q10=A
Q11=A
Q12=A
Q13=A
Q14=A
Q15=A
Q16=A
Q17=A
Q18=A
Q19=A
Q20=A
Q21=A
Q22=A
Q23=A
Q24=A
Q25=A
Q26=A
Q27=A
Q28=B
Q29=B
Q30=A
Q31=A
```

## 由此形成的正式范围

### 第一阶段真实化页面

- Real Mode Shell、真实 Principal、真实授权 Site 导航与严格空态；
- Assets；
- Dashboard 的权威区块；
- Energy 年/月/周/日暖通电能分析；
- Cost；
- Commands 的 Registry、Capability 与状态体验完善，生产路由继续关闭；
- BigScreen 复用相同 Read Model；
- System 的平台状态与 Registry 只读能力。

### 暂不建设为真实后端领域

- FDD；
- Alarm；
- Work Order；
- Optimize；
- AI Investigation；
- System 用户、角色、规则和完整审计查询管理。

这些功能在 Real Mode 中显示“尚未接入”或按部署配置隐藏；Demo Mode 可继续使用确定性 Fixture，但必须持续标记为非权威演示数据。

### 禁止事项

- Real Mode 回退到 Mock、Legacy、浏览器临时业务状态或编造数值；
- 前端使用 `b1`、`b2`、`MOCK_DEVICES` 或 `DEVICE_META` 作为真实业务身份；
- 浏览器并发拉取全部设备后承担正式园区级聚合；
- 前端固定电价、费率、碳因子或模型值并呈现为权威结算结果；
- BigScreen 拥有独立静态业务数据源；
- Zustand 状态变更被当作工单、审批、建议、用户或规则的持久化成功；
- 任何页面直接调用 ThingsBoard 或绕过 Platform Gateway、领域授权、幂等与审计；
- Real Mode 允许本地切换角色并将其视为真实权限。

## 需要一次性确认的问题

回答格式建议使用 `Q3=A, Q4=B ...`。对页面范围问题，也可直接写页面名和决定。

---

## 一、真实产品最小范围

### Q3 — 第一阶段真实化目标是什么？

- **A. 最小可用运营闭环**：Dashboard、Assets、Commands、Energy、Cost、System 状态、BigScreen。
- **B. 运维闭环优先**：在 A 基础上增加 Alarms、Work Orders、FDD。
- **C. 全产品真实化**：包括 Optimize、AI、完整 System 管理。

**推荐：A。** 先把已有 S1–S3 和遥测可派生页面做实，避免一次创建多个新领域。

### Q4 — Demo Mode 是否继续保留？

- **A. 保留为独立入口和独立构建配置，顶部持续标记“演示数据”。**
- **B. 删除 Demo Mode，只保留真实模式。**
- **C. 真实模式中允许用户切换局部演示数据。**

**推荐：A。** 禁止 C，因为会重新引入真假混合。

### Q5 — 尚未真实化的菜单如何处理？

- **A. 从 Real Mode 菜单隐藏。**
- **B. 保留菜单，进入后显示“尚未接入”，并解释所需后端能力。**
- **C. 保留现有演示内容并加标签。**

**推荐：B。** 产品路线可见，但不伪装为已完成；面向外部客户部署时可配置为 A。

---

## 二、页面逐项范围

### Q6 — Dashboard 的正式范围

当前 Dashboard 同时使用真实遥测、静态设备元数据、Mock FDD/工单/优化和硬编码舒适度。

- **A. 只展示当前已权威化的资产、Presence、实时遥测和 Command 摘要。**
- **B. 保留完整布局，为告警、FDD、优化等缺失区块显示“未接入”。**
- **C. 为完整 Dashboard 一次性建设页面级 BFF 聚合及所有下游领域。**

**推荐：B。** 已有真实区块立即接入；缺失区块保持明确空态。后续重复高成本汇总再建设事件驱动 Read Model。

### Q7 — Assets 页面是否作为设备导航权威入口？

- **A. 是。所有设备选择、详情跳转和其他页面的 Device ID 都从 Registry 获取。**
- **B. 各页面继续维护自己的设备常量和别名。**

**推荐：A。** 删除 `MOCK_DEVICES`、`DEVICE_META` 对 Real Mode 业务身份的影响；公共 ID 统一使用 Registry UUIDv7。

### Q8 — Commands 页面下一步做到什么程度？

- **A. 保持当前本地 ThingsBoard 三设备能力，不扩大正式权限。**
- **B. 接入 Registry 设备选择、Capability 目录和实时 Command 状态，仍保持生产路由关闭。**
- **C. 直接启用生产控制。**

**推荐：B。** 完成前端真实化，但不改变 S3 正式认证和路由门禁。

### Q9 — Energy 页面是否保留年/月/周/日四层工作台？

- **A. 保留，建设统一 Energy Read Model 和聚合 API。**
- **B. 缩减为实时功率与单点历史曲线，不做正式能耗分析。**
- **C. 继续由前端生成年度/月度数据。**

**推荐：A。** 产品文档已明确四层钻取；C 与 Real Mode 冲突。

### Q10 — Energy 一期数据范围

- **A. 只做暖通电能；水、燃气、冷量显示“待接入”。**
- **B. 没有计量数据时，根据额定功率或模型估算并当作实际值。**
- **C. 同时建设全部能源类型。**

**推荐：A。** 与 `PRODUCT.md` 已有原则一致。

### Q11 — Cost 页的费用口径由谁拥有？

- **A. 后端 Cost/Energy Read Model 拥有电价版本、计费时段、币种和计算结果。**
- **B. 前端用固定峰平谷单价乘能耗。**
- **C. ThingsBoard 直接返回费用。**

**推荐：A。** 前端固定 `0.78` 或峰平谷价格无法处理生效日期、Site 时区、合同版本、税费和重算。

### Q12 — BigScreen 的定位

- **A. 只消费 Dashboard/Energy/Alarm 等相同 Read Model，不拥有独立数据集。**
- **B. 保留一套专门的大屏静态数字。**
- **C. 由浏览器根据多页面接口自行重新聚合。**

**推荐：A。** BigScreen 是展示视图，不是数据 Owner。

### Q13 — Alarms 与 Work Orders 是否拆成两个领域？

- **A. 拆分。Alarm 是设备/规则事件，Work Order 是人工处置流程，两者可关联但状态机独立。**
- **B. 继续把“报警工单”视为同一个对象。**
- **C. 暂时只做 Work Order，不做 Alarm。**

**推荐：A。** 当前 UI 把两者混在一个 Mock 类型里，会导致自动恢复告警与人工关单语义冲突。若第一阶段不建设，可先标记未接入。

### Q14 — FDD 是否现在进入真实化范围？

- **A. 暂不建设后端，Real Mode 显示未接入。**
- **B. 建设确定性规则型 FDD：Finding、Evidence、Rule Revision、状态和工单关联。**
- **C. 直接由 AI 对实时数据生成 FDD。**

**推荐：A 或 B；禁止 C。** 若业务近期必须使用 FDD，优先 B，规则和证据必须可重放、可审计。

### Q15 — Optimize 页的真实语义

- **A. 暂时仅 Demo；Real Mode 未接入。**
- **B. 建设人工创建/规则生成的 Optimization Proposal，审批后转成独立 S3 Command。**
- **C. “批准”后前端直接下发设备。**

**推荐：A。** 若后续建设则采用 B；禁止 C。当前 Zustand 状态机不能成为生产审批权威。

### Q16 — AI 页面当前策略

- **A. Real Mode 暂时隐藏或显示未接入；Demo Mode 保留 Mock 助手。**
- **B. 接入现有 EnergyAgent，但仅允许读取权威 Registry/Telemetry 工具。**
- **C. 让前端把当前页面数据拼进 Prompt，并允许执行动作。**

**推荐：A。** 你已明确暂不推进 S5/S6；因此不能把 Mock 聊天包装成真实 AI。

### Q17 — System 页面拆分策略

当前页面混合了真实平台状态、真实 Registry 只读、Mock 用户管理、Mock 告警规则、Mock 审计列表和静态集成状态。

- **A. 拆分成真实“平台状态”和尚未接入的“管理功能”。**
- **B. 为现有所有 Tab 一次性补齐用户、角色、规则、审计、集成管理后端。**
- **C. 保留 Mock CRUD。**

**推荐：A。** Registry、Principal、Session、Route、运行状态先真实化；用户、规则、审计查询分别立项。

---

## 三、数据和 API 边界

### Q18 — 页面聚合放在哪里？

- **A. 简单、低频派生在前端；跨设备、高成本、需要一致口径的汇总放 Gateway BFF 或事件驱动 Read Model。**
- **B. 所有指标都在前端计算。**
- **C. Gateway 临时抓多个服务并无界聚合。**

**推荐：A。** BFF 必须有调用预算、明确必需/可降级区块、`asOf`、watermark 和覆盖率。

### Q19 — 派生指标必须携带哪些解释信息？

- **A. 时间区间、Site 时区、输入覆盖率、`asOf`、watermark、单位、算法/口径版本和是否部分数据。**
- **B. 只返回一个数字。**

**推荐：A。** 适用于能耗、COP、舒适度、在线率、节能率、费用和碳排。

### Q20 — 历史遥测接口是否继续作为页面通用聚合接口？

- **A. 只用于设备/单 Point 历史边界；园区、类别、日/月聚合建设正式聚合 API。**
- **B. 浏览器并发读取所有设备再求和。**

**推荐：A。** 当前 `useBuildingTimeseries` 是兼容层，不适合作为正式大规模聚合。

### Q21 — Real Mode 是否允许前端维护业务状态？

- **A. 仅维护筛选、抽屉、临时表单等 UI 状态；工单、审批、建议、规则、用户状态必须由后端权威资源返回。**
- **B. Zustand 修改后即视为成功，稍后再同步。**

**推荐：A。** 需要 Optimistic UI 时也必须有服务器 revision、幂等键和冲突处理。

### Q22 — 写操作的统一规则

- **A. 所有写操作使用正式 API、CSRF、资源级授权、幂等、revision/ETag、Audit Intent 和明确结果。**
- **B. 某些低风险按钮可直接调用 ThingsBoard。**
- **C. Gateway 保存任意页面 JSON 作为状态。**

**推荐：A。** 禁止 B、C。

### Q23 — 实时更新范围

- **A. 当前状态用 Snapshot + Delta；Command、Alarm、Approval、Work Order、Audit 使用不可合并的 ORDERED_EVENTS。**
- **B. 全部页面每几秒轮询。**
- **C. 前端自行根据遥测推导告警和流程状态。**

**推荐：A。** 对暂未建设的领域可先使用 REST 查询，但不能由浏览器创造权威事件。

### Q24 — 错误、权限和部分数据呈现

- **A. 明确区分无权限、未接入、无数据、数据过期、部分覆盖、服务不可用和映射异常。**
- **B. 都显示“暂无数据”。**

**推荐：A。** 这是可信运营界面的核心。

---

## 四、身份、导航和配置

### Q25 — 全局 Site/Building 选择器的权威来源

- **A. 使用 Registry 授权 Site；URL 保存当前 Site，选择只影响导航，不作为授权事实。**
- **B. 继续使用 `b1`、`b2` 和本地 Zustand。**

**推荐：A。** 移除 `buildingId: 'b1'` 对 Real Mode 的业务含义。

### Q26 — 前端角色切换器如何处理？

- **A. 仅 Demo Mode 可切换角色；Real Mode 从当前 Principal/Capability 读取，不允许本地伪装角色。**
- **B. Real Mode 也允许切换 `demo/ops/rd`。**

**推荐：A。** 前端角色仅用于呈现，不是授权来源。

### Q27 — 设备别名与类型元数据来源

- **A. Registry Equipment/Device 类型、code、displayName 和 ExternalBinding 映射。**
- **B. 页面继续维护 `DEVICE_META` 常量。**

**推荐：A。** 允许前端保留纯视觉映射，例如类型图标，但不能保留设备清单和运行事实。

---

## 五、实施和验收顺序

### Q28 — 实施顺序

- **A. 横向一次性补所有后端，再改全部页面。**
- **B. 按页面纵向切片逐个完成真实闭环。**

**推荐：B。** 建议顺序：

1. Real Mode Shell：真实 Site、Principal、严格空态、移除全局真假混合。
2. Assets：Registry + Presence + 当前遥测 + 历史曲线。
3. Dashboard：只基于已接入数据的真实摘要。
4. Energy：正式聚合 Read Model，完成日/月/周/年钻取。
5. Cost：电价版本和费用聚合。
6. Commands：Registry/Capability 接入和状态体验完善，不启用生产路由。
7. BigScreen：复用相同 Read Model。
8. System：真实平台状态；其余管理能力按独立领域决定。
9. Alarms、Work Orders、FDD、Optimize、AI 只有在对应领域被确认后再进入。

### Q29 — 每个页面何时算“打通”？

- **A. 页面能显示一次真实请求即完成。**
- **B. 契约、身份、授权、空态、错误、部分数据、实时/刷新、审计、测试和回滚均通过。**

**推荐：B。** 至少需要浏览器端到端测试证明 Real Mode 没有导入或渲染 fixture 业务事实。

### Q30 — 是否建立自动化 Real Mode 数据来源门禁？

- **A. 建立：真实页面不得导入 `src/mock`、不得使用 `MOCK_*` 业务数据、不得在 API 失败时返回 fixture。**
- **B. 依赖代码评审人工发现。**

**推荐：A。** 同时允许 Demo-only bundle 明确导入 Mock。

---

## 建议的默认答案集合

如果希望直接采用当前建议，可确认：

```text
Q3=A
Q4=A
Q5=B
Q6=B
Q7=A
Q8=B
Q9=A
Q10=A
Q11=A
Q12=A
Q13=A
Q14=A
Q15=A
Q16=A
Q17=A
Q18=A
Q19=A
Q20=A
Q21=A
Q22=A
Q23=A
Q24=A
Q25=A
Q26=A
Q27=A
Q28=B
Q29=B
Q30=A
```

该默认集合的结果是：先做一个严格、可信、范围受控的真实运营产品；保留演示体验，但不把 FDD、工单、优化、AI 或系统管理的 Mock 状态伪装成已完成后端能力。
