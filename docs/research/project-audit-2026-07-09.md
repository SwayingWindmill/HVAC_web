# HVAC_web 项目审核报告（2026-07-09）

审核范围：代码质量 + 仓库卫生 + 构建 + 依赖。方式为静态核查 + 构建验证 + Explore 代码审计。

## 🔴 最高优先级

### 1. 整个项目从未提交（最大风险）
- 现状：`git log` 仅 1 次提交 `ac1bf48 chore: init ...`，`git ls-files` 仅 5 个文件被追踪。
- 影响：**10 个页面 + 配置 + 文档全部在工作区未提交**。机器故障 / 误操作会丢失全部工作。
- 建议：尽快提交（建议按模块分批，或先一个 baseline 提交把骨架入库）。

## ✅ 本回合已修复（仓库卫生 + 构建根因）

| 问题 | 处理 |
|------|------|
| 3 个误生成的 CJK 垃圾文件（`关联票：#18（调研）·` / `核实方式：静态代码核查` / `核实日期：2026-07-09`）| 删除。来源：某次 `gh issue comment --body` 把评论正文误当文件路径生成 |
| 11 张 bigscreen 验证截图 + sys_*.png | 删除（均为验证用一次性产物）|
| `tsc -b` 误吐构建产物到仓库根（`vite.config.js` / `vite.config.d.ts` / `*.tsbuildinfo`）| 根因修复：`tsconfig.node.json` 加 `outDir: ./node_modules/.tmp/node-build`；根 `tsconfig.json` 加 `tsBuildInfoFile` 重定向。原 `noEmit:true` 会触发 TS6310（composite 项目禁止禁 emit），已改用 outDir 重定向 |
| `.gitignore` 缺失多类临时产物 | 补全：`.chrome_tmp/`、`.ctmp*`、`sys_*.png`、`bigscreen_*.png`、`prototype/`、`*.tsbuildinfo`、`vite.config.*.timestamp-*.mjs` |
| 构建验证 | `npm run build` 通过，0 error（仅 chunk 体积警告，非阻塞）|

> 注：`.chrome_tmp/`、`.ctmp*` 等 Chrome 临时目录仍留在磁盘（safe-delete 机制拦截了 `rm -rf`，因其回收站操作在本环境不可用）。它们已被 gitignore，不会进版本库，可随时手动删除。

## ✅ 本回合已修复（代码层清理，2026-07-09 晚）

| # | 问题 | 处理 | 验证 |
|---|------|------|------|
| 1 | BigScreen 缺 ErrorBoundary → 无 WebGL 白屏 | `src/components/ErrorBoundary.tsx` 新增通用 render-phase 边界；`BigScreen` 用其包裹 `<System3D>`，fallback「当前环境不支持 3D 渲染（驾驶舱其余数据正常显示）」 | build 0 error |
| 2 | `src/mock/data.ts` ~15 个死导出 | 重写为仅保留被引用的导出（`mockAlarms/mockTree/mockSuggestions/mockFdd/mockWorkOrders` + 类型），删 15+ 死导出 | build 0 error |
| 3 | `System/index.tsx` `walk` 返回 `any[]` 喂 Tree | 改 `: DataNode[]`（从 `antd/es/tree` 导入类型），递归节点也用 `DataNode` | build 0 error |
| 4 | `Placeholder` 死分支 | `App.tsx` 删三元死分支；删除 `src/pages/Placeholder.tsx` | build 0 error |
| 5 | `useTelemetryLive` 恒订阅 | `BigScreen` 改 `useTelemetryLive(realData ? MOCK_DEVICES : [], ...)`，关开关即不订阅 | build 0 error |
| 6 | BigScreen `key={i}` 索引 key | KPI→`key={k.label}`、DIAGNOSTICS→`key={a.title}`、SUGGESTIONS→`key={t}` | grep 确认无残留 |
| 7 | 死依赖 `ai` / `@ai-sdk/react` | `npm uninstall ai @ai-sdk/react`（package.json 已无引用，node_modules 已清）| `src/` 零 `from 'ai'` 引用 |

> 构建结果：`npm run build` 通过，0 error（仅 chunk 体积警告，非阻塞）。

## 🟠 仍未处置（需后续决策）

### MED
- **BigScreen「实时数据」开关仅覆盖局部**：仅设备 `power/cop/load` 接 `useTelemetryLive`，KPI 卡 / 健康仪表 / 诊断 / 告警进度等仍是硬编码参考图数字（COP 6.28、告警 34 条等）。开开关有误导。**需统一收口到一个 bigscreen mock/api 模块**（较大重构，本清理轮未做，留待后续）。

### LOW
- **`Range` 类型重复定义**：`mock/data.ts` 与 `api/types.ts` 各一份。建议归并到 `api/types.ts` 单一来源（本清理轮未做）。



## 🟠 代码层问题处置状态

本部分原始清单已分流到上方两个段落：**「✅ 本回合已修复（代码层清理）」**（7 项已解决）+ **「🟠 仍未处置（需后续决策）」**（2 项留待后续）。下面「🟡 依赖问题」中的死依赖也已在本轮卸载。

## 🟢 已确认健康项
- 路由/页面一致性 OK：10 模块全部接真实页，无一指向 Placeholder。
- 状态管理 OK：zustand 无直接 mutate，无 persist 符合演示预期。
- TS 严格度 OK：`strict` + `noUnusedLocals` + `noUnusedParameters` 开启。
- 构建仅有 chunk 体积警告（System3D ~899KB、主包 2.4MB），可后续做 `manualChunks` 拆分，非阻塞。

## 建议处置顺序
1. **提交代码**（消除最大风险）。
2. 移除死依赖 `ai` / `@ai-sdk/react`。
3. 修 BigScreen ErrorBoundary（防白屏）。
4. 清理死导出 + Placeholder 死分支 + System `any[]`。
5. （可选）BigScreen 实时数据开关收口、chunk 拆分。
