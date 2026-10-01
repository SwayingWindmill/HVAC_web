# Device Center Surface Contract

**Status: PRODUCT / INTERACTION CONTRACT; SHADCN APPLICATION SYSTEM SELECTED**

## Job

Device Center 是设备扫描、比较、定位和进入详情的主要工作区。

用户需要快速完成：

- 按 site / scope / keyword / state 找到设备；
- 比较设备 identity、location、runtime、connectivity、freshness、quality、alarms 和关键当前值；
- 进入 durable device detail；
- 必要时连续检查多个设备而不丢失列表上下文。

## Truth

- registered、online/offline/unknown、runtime、freshness、quality、alarm、diagnosis 必须保持独立。
- Offline 不自动等于 Fault。
- Missing / unavailable 不显示为 zero。
- 用户主视图不显示内部 UUID、trace、revision 或 raw technical enum。
- 当前值必须保留真实 unit、precision、freshness / timestamp 语义。

## Shadcn Application composition

默认采用 **Data Table First**：

```text
Page intro
Search / filters / column controls
Device Data Table
Pagination / server result state
Optional selected-object context
```

### Page intro

包含页面标题、当前 site/scope、结果/更新时间摘要和少量本地动作。

### Toolbar

使用 shadcn Input / Select / Button / Dropdown/Popover grammar。筛选必须映射到真实 URL Search Params。

### Data Table

使用 TanStack Table + `components/ui/table`。默认列优先：

- identity / type；
- location；
- runtime；
- connectivity；
- freshness / quality；
- selected current values；
- active alarm context；
- updated time；
- row actions。

表格承担 scan / compare 任务。Card wall 不再作为默认入口。

### Detail

- `/devices/$deviceId` 是 durable detail；
- 高频连续比较使用 Quick Preview，而不是缩小版 Device Detail；
- desktop Quick Preview 为 non-modal overlay，打开后 Data Table 尺寸不发生 reflow；
- narrow / mobile 使用 modal Sheet 表达同一份 Quick Preview；
- Preview 只保留 identity、独立状态、少量关键值、当前事项、对象上下文和“打开完整详情”，不放 point tabs / relationship tabs / long evidence。

## Scale

保持 server pagination 直到真实规模/交互证明需要 virtualization 或 infinite loading。不要为了“高级感”主动引入复杂列表基础设施。

## Visual authority

当前视觉 authority：

- `DESIGN.md`
- `docs/design-system/shadcn-redesign-2026-09-13.md`
- current shadcn/ui Data Table / Table patterns
- satnaing/shadcn-admin application patterns

旧 Device Center 卡片设计、Control Desk Ledger composition and historical screenshots are no longer the default visual source of truth.
