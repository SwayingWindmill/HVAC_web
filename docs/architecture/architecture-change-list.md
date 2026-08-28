# HVAC_web 架构修改清单

状态：PROPOSED / 需要进入后续 ADR 与实现任务  
依据：ThingsBoard CE v4.3.1.1、OpenEMS 2026.7.0、MyEMS v6.7.0 的固定源码审查

## 1. 先改方向，再加功能

| 优先级 | 当前倾向 | 需要修改为 | 依据 |
| --- | --- | --- | --- |
| P0 | 以现有项目的 Telemetry/Metric 划分作为默认骨架 | 以 Platform Foundation、Data Plane、Energy Domain、Automation/Governance、Presentation 五类逻辑 module 重新评审 | 三项目的职责边界不同，MyEMS 证明能源内容不能隐藏在 telemetry |
| P0 | “有数据库/worker 就算有能源能力” | 先定义 Raw、Current、Normalized、Aggregate、Report 五类事实和 ownership | MyEMS cleaning/normalization/aggregation/database |
| P0 | 三参考项目主要用于解释现有架构 | 对每个机制写 ADOPT/ADAPT/REJECT，允许参考源码推翻本地设计 | source-first 规则 |
| P1 | 继续优先扩展实时监控页面 | Operations 与 Energy Management 并列，补齐趋势、对比、质量、报表和计量上下文 | MyEMS web/admin、ThingsBoard Dashboard |
| P1 | 将 generic Metric 作为能源处理总入口 | 新增 Energy Processing：cleaning、normalization、aggregation，并保留 Metric 作为计算机制 | MyEMS processing chain |
| P1 | adapter、telemetry、能源计算职责容易混合 | adapter 只做协议/映射；telemetry 管采集事实；Energy Processing 管标准化和汇总 | ThingsBoard Integration/Converter、MyEMS Modbus/processing |
| P1 | 以缓存或当前表代替 current/history 契约 | 明确 current authority、历史事实、quality、watermark、revision | ThingsBoard telemetry、MyEMS historical/latest、本项目数据基线 |
| P1 | UI 直接组合底层事实 | 增加 Query/Reporting read model，UI 不访问内部表和 topic | ThingsBoard Dashboard datasource、MyEMS API/reports |
| P2 | 把通用规则链想象成控制器 | Rule/Alarm 处理事件；控制 intent 经过未来 Edge safety/arbiter | ThingsBoard Rule Engine、OpenEMS Cycle/Controller |
| P2 | 复制 MyEMS 多数据库 | 只复制逻辑数据集分层，物理拆分由压力证明 | MyEMS database design |
| P3 | 先建设生产 Edge 网关 | 当前冻结 manifest/intent/evidence seam，Backend/UI 先闭环 | 用户已明确本阶段不考虑 Edge |

## 2. 明确需要删除或改名的方向

进入实现阶段后，以下路径不能通过兼容层长期保留：

- 把能源归一化逻辑隐藏在 generic telemetry handler；
- 把报表在 UI 中临时计算；
- 把 MQTT topic 当成 UI 的业务接口；
- 把 Redis current 当成不可替代的业务事实；
- 把 accepted command 当成 applied command；
- 把 simulator 代码路径当成生产 Edge Host；
- 把“当前已有模块”当成不需要重新验证的理由。

## 3. 本轮不做的事情

- 不引入 ThingsBoard runtime；
- 不复制 OpenEMS Java/OSGi；
- 不复制 MyEMS 的全部数据库数量和表结构；
- 不接入真实现场协议；
- 不把 Kubernetes、Kafka/Redpanda 或多节点部署变成 Phase 1 前置；
- 不为了对标而创建没有真实业务契约的空模块。

## 4. 后续实现顺序

~~~text
1. 固定 domain vocabulary / ownership
2. 固定 Raw → Current → Normalized → Aggregate → Report 数据契约
3. 完成 simulator/MQTT → Backend → UI 最小纵向切片
4. 加入 quality、reprocessing、comparison、report snapshot
5. 加入最小 alarm/work order/command governance
6. 再实现 manifest/intent/evidence 到 simulator 的 Edge seam
7. 最后评估真实 Edge Host 和协议 acquisition
~~~

