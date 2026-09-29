# HVAC Web Content Design

This file defines product-language rules for the operator dashboard. It is a product UI contract, not marketing copy.

## 1. Write the object, not the UI container

Prefer the noun the operator is actually looking for:

- 设备
- 工单
- 告警
- 控制策略
- 节能措施
- 优化方案
- 测点
- 执行记录
- 报告

Do not add UI-type words just because the content is rendered as a table.

Prefer:

- 设备
- 工单
- 历史告警
- 月度节能量
- 功能验证

Avoid by default:

- 设备台账
- 工单台账
- 告警列表
- 功能验证队列与调试台账
- 已生成报告列表

Words such as “台账 / 明细 / 列表 / 工作台” are allowed only when they describe a real business concept that users already recognize, not a component type.

## 2. Secondary copy is optional, not structural

A title does not automatically need a muted subtitle.

Keep secondary copy only when it adds information that is not already obvious from the title, controls, columns, or surrounding context. Valid reasons include:

- time range
- data scope
- calculation or compliance basis
- current system state
- an important constraint or exception
- a freshness / as-of timestamp

Delete copy that only explains what the visible UI already does.

Bad:

> 设备  
> 按设备分类、运行工况与关键属性检索在册设备

Good:

> 设备

Bad:

> 告警规则  
> 管理各系统监测规则的触发条件、延时防抖与通知配置

Good:

> 告警规则

Useful:

> 月度节能量  
> 按 IPMVP Option C 核算

## 3. Do not repeat the same noun through the hierarchy

For authenticated product surfaces, the AppShell owns the page title. Non-detail surfaces must not render a second page-level `h1` inside the business workspace. Keep only context, scope, time, state and actions below the shell title. Detail surfaces may use the business object name as their `h1` because the shell renders their surface label as breadcrumb text rather than a heading.

Avoid:

> 设备  
> 设备概览  
> 设备台账  
> 设备列表

If the page title already names the object, the primary table often needs no additional heading.

A page may therefore use:

```text
设备

[KPI]

[Search] [Filter] [Sort] [Columns]
[Table]
```

instead of adding another “设备” heading above the table.

## 4. Use short dashboard labels

KPI labels should identify the metric, not read like a report heading.

Prefer:

- 当前功率
- 系统 COP
- 在线率
- 待处理
- 最大需量
- 可调负荷
- 节电目标
- 已节省电费

Avoid:

- 实时运行功率
- 综合运行能效 (COP)
- 年度节电目标达成率
- 累计核验节费贡献
- 行动计划推进态势
- 实施受阻风险预警

Keep qualifiers in the value, unit, badge, time range or comparison line.

## 5. Use plain Chinese before administrative or AI-like compounds

Prefer concrete operator language.

Prefer:

- 设备状态
- 设备能效
- 可调负荷
- 电费构成
- 分布式能源设备
- 执行前检查
- 备件与耗材
- 整改措施

Avoid unless the domain genuinely requires it:

- 态势
- 纳管
- 治理
- 核销
- 闭环
- 全链路
- 赋能
- 群控矩阵
- 遥测工况
- 离群排查
- 资源与响应台账

Professional abbreviations such as COP, EUI, SEU, M&V, CAPA and IPMVP may be retained where operators use them.

## 6. Empty, loading and error text should say what happened

Loading:

- 正在加载设备
- 正在读取执行记录

Empty:

- 暂无工单
- 当前没有活动告警

Error / unavailable:

- 设备状态暂不可用
- 无法读取控制条件

Do not explain internal architecture unless the user needs it to decide what to do next.

## 7. Actions should be verbs

Prefer:

- 新建设备
- 导出
- 刷新
- 查看详情
- 批准
- 驳回
- 重试

Avoid button labels that repeat the destination noun without an action.

## 8. Tone hierarchy

Use only the copy needed at each level:

1. Page title — where am I?
2. Section title — what question does this section answer?
3. Metric label / table columns — what is this value?
4. Metadata — time, scope, method, state, exception

Muted text is metadata. It is not a required visual layer.

## 9. Review questions

Before adding a subtitle or description, ask:

1. Does the title already say this?
2. Do the visible controls or columns already say this?
3. Does the text change a user's interpretation or decision?
4. Is this a real business term, or just the name of our UI component?
5. Would an operator naturally say this phrase?

If the first two answers are yes and the third is no, delete the copy.
