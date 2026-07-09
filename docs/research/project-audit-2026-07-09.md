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

## 🟠 代码层问题（需决策，未改）

### MED
- **BigScreen 缺 ErrorBoundary**：`<Suspense>` 不捕获渲染期错误；无 WebGL 时 `Canvas` 同步抛错 → 整个 `/bigscreen` 白屏。建议包一层 ErrorBoundary（fallback「当前环境不支持 3D」）。
- **BigScreen「实时数据」开关仅覆盖局部**：仅设备 `power/cop/load` 接 `useTelemetryLive`，KPI 卡 / 健康仪表 / 构成 donut / 诊断 / 告警进度等仍硬编码参考图数字（如 COP 6.28、告警 34 条）。开开关有误导。建议统一收口到一个 bigscreen mock/api 模块。
- **`src/mock/data.ts` 一批死导出无引用**：`mockKpi` / `mockEnergy` / `mockSetpoint` / `chartAccent` / `mockZoneEnergy` / `mockComposition` 等约 15 个，是早期版本遗留。维护噪声，建议删除或归并。
- **`src/pages/System/index.tsx:213`** `walk` 返回 `any[]` 喂 AntD `Tree`，绕过 `DataNode` 类型校验。建议改 `: DataNode[]`。

### LOW
- **`Placeholder` 组件不可达分支**：`App.tsx` 三元 `Real ? <Real/> : <Placeholder/>` 中 `Real` 恒真，`:` 分支死代码（注释也已过时）。可删。
- **BigScreen `useTelemetryLive` 在 `realData=false` 也始终订阅**，无谓启动 mock 推送定时器。
- **`key={i}` 索引 key**：BigScreen/System3D 静态数组用索引 key，无重排风险，低。
- **`Range` 类型重复定义**：`mock/data.ts` 与 `api/types.ts` 各一份。

## 🟡 依赖问题
- **死依赖**：`ai@^4.3.19` 与 `@ai-sdk/react@^4.0.19` 在 `package.json` 但 `src/` 完全未用（AI SDK 当时因版本陷阱被弃用，改自包含 mock hook，仅注释提及）。不删不报错，但误导 + 占 node_modules。建议 `npm uninstall ai @ai-sdk/react`。

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
