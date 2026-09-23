# 03 设备工作区 — Workspace Brief

**Status: IMPLEMENTED / BROWSER REVIEWED**  
**Current route:** `/sites/$siteId/devices`  
**IA role:** 10-Workspace 架构中的“设备”主工作区。  
**Design inheritance:** 36 Surface 阶段已验收的 **06「设备中心」是本 Workspace 的视觉与交互母体**；36→10 只合并职责，不重新发明 Device Ledger。后续 9 月 20–21 日完成的全站 tablecn / TanStack DataTable 标准化继续作为 Table implementation grammar。

## 继承顺序

```text
36 Surface 06 已验收业务结构
        +
36 Surface 后期统一 Table grammar
        +
10 Workspace 新职责边界
        ↓
当前 Device Workspace
```

发生冲突时：

- 设备业务信息层级、列语义、详情责任优先继承已验收 Surface 06 / 07；
- 页面级 Table 外观与交互使用当前标准 `DataTableBlock + DataTable + DataTableAdvancedToolbar`；
- 36→10 允许删除重复入口、合并 owner workflow、更新导航归属；
- 不允许仅因为导航收敛，就把已验收的 Device Ledger / Device Detail 重新设计成另一套页面。

## User job

扫描、筛选和比较当前站点的设备群体，快速定位设备并理解互相独立的运行、连接和数据事实，再决定是否进入持续调查。

## Accepted composition

```text
Surface Fact Strip
  设备 / 在线 / 需关注 / 数据问题

tablecn / TanStack Toolbar
  搜索 / 范围 / 排序 / 筛选 / 列

Device Ledger
  设备
  对象与位置
  运行
  连接
  数据
  关键值
  当前事项
  更新

Optional Quick Preview
  仅承担快速查看
  不改变上述 Ledger 的业务结构

Durable Device Detail
```

## Table contract

可见列固定继承 36 Surface 最终验收语义：

`设备 / 对象与位置 / 运行 / 连接 / 数据 / 关键值 / 当前事项 / 更新`

规则：

- `运行` 与 `连接` 不合并为通用“状态”；
- `对象与位置` 不退化为只有 location；
- normal state 使用中性表达；warning / destructive 只给真实 attention；
- `数据` 同时呈现 freshness + quality，不创造 synthetic health；
- 页面级表格继续采用当前 standalone DataTableBlock，不回退为 Card 套 Table；
- Search / Sort / Filter / Columns 使用当前 tablecn grammar，不恢复重复 Select 筛选条。

## Preview contract

后续交互优化允许把 36 Surface 的 contextual Inspector 改成 desktop non-modal Quick Preview / narrow Sheet，但 Preview：

- 不改变 Ledger 列语义；
- 不替代 durable Device Detail；
- 只显示 identity、独立状态、少量关键值、当前事项、对象上下文和详情入口。

## Browser acceptance

- 15 行设备在目标桌面宽度稳定扫描；
- 可见表头必须严格等于 8 列合同；
- FactStrip 在 Ledger 之前；
- DataTable 无外层 Card；
- desktop Preview 不触发 Ledger reflow；
- zero / unknown / unavailable truth 不被错误归并。
