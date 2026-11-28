# apps/hvac-miniprogram/AGENTS.md

值班人员微信小程序（`@hvac/miniprogram`，原生小程序 + TypeScript），与网页共用平台网关、账号与权限。开发细节见 `apps/hvac-miniprogram/README.md` 与 `docs/specs/miniprogram-wechat.md`。

## 快速验证命令（WSL 根目录执行）

```bash
npm run miniprogram:check                                                                                    # 微信/测试双类型检查 + 同步校验 + 全套单测
node --experimental-strip-types --import ./apps/hvac-miniprogram/test/resolve-ts.ts --test apps/hvac-miniprogram/test/<名称>.test.ts
npm -w @hvac/miniprogram run sync                                                                            # 从 apps/hvac-web 同步共享域模块
```

## 架构不变量与运行时红线

- **`miniprogram/shared/` 严禁手改**：该目录下 5 个文件由 `scripts/sync-shared.ts` 从 `apps/hvac-web` 复制（`plantTopology.ts`、`centralPlantTelemetry.ts`、`thread-model.ts`、`tool-labels.ts`、`answer-markdown.ts`）；改动规则必须修改网页源头后跑 `sync`，对话类型 `lib/agent-types.ts` 须与网页契约保持一致。
- **微信审核隔离红线**：AI 助手两个页面当前因审核资质暂藏——未写入 `app.json` 且被 `project.config.json` 的 `packOptions.ignore` 排除在上传包外，未获明确批准严禁暴露进主包、页面列表或 `tabBar`。
- **真机/微信宿主限制**：
  - 无 `EventSource` 与 `TextDecoder`，流式问答走 `wx.request({ enableChunked: true })` 配合 `lib/utf8.ts` 与 `lib/agent-stream.ts`；
  - 折线图用 SVG 拼图（`lib/line-image.ts`），禁用会在开发者工具/真机浮层遮挡滚动的 `canvas`；
  - 不依赖手机本地时区或 `toLocaleString`，日期与数字统一基于平台站点起点和自定义格式化（`lib/today.ts`、`lib/format.ts`）；
  - 不引入需「构建 npm」的第三方 Markdown 库，Markdown 结论走 `rich-text` + 可横向滚动表格节点（`lib/answer-nodes.ts`）；
  - 请求统一经过 `lib/gateway.ts`（`Authorization` 头会话、401 回登录页）；`AppSecret` 与会话密钥严禁进入小程序；微信开发者工具导入目录为 `apps/hvac-miniprogram`（非 `miniprogram/`）。
