# Modern Web Guidance 调研（2026-11-11）

## 看了什么

写网页代码前需要一套现代浏览器标准做法的参照，免得模型用过时的 JS 去模拟原生已经支持的表单、输入法、深浅色和性能能力。看了：

- `modern-web-guidance` 1.0.0（仓库 `GoogleChrome/modern-web-guidance`，提交 `4dd968349e54ca45a05dd5c818fb035c9054b022`），含 `skills/modern-web-guidance/SKILL.md`、`legacy-scan` 规则集，以及 `ime-safe-enter-submit`、`dark-mode`、`dark-mode-toggle`、`browser-ui-theming`、`forms`、`color` 指南；
- 在本仓库 `apps/hvac-web/src` 上实跑了一次 `DISABLE_TELEMETRY=1 npx -y modern-web-guidance@1.0.0 legacy-scan apps/hvac-web/src --json`，核对存量网页代码。

## 决定

**只作为开发期的按需检索技能与静态规则来源，不进前端运行依赖，且服从 `DESIGN.md` 与已安装的 shadcn/ui 组件。**

- **采纳的**：
  - **实验特性门控（`.mwgrc`）**：显式设 `allowOriginTrials: false`、`allowVolatileFeatures: false`，只采纳现代桌面浏览器广泛支持（Baseline Widely Available）或可无感渐进增强的标准写法；命令行调用时带 `DISABLE_TELEMETRY=1`。
  - **中文输入法回车防误触（`ime-safe-enter-submit`）**：单行或多行输入框自己在 `onKeyDown` 里拦截 `Enter` 提交时，同时调用 `event.preventDefault()` 并检查 `event.nativeEvent.isComposing || event.keyCode === 229`，防止值班人员按回车确认中文候选词时（含 Safari `compositionend` 先于 `keydown` 触发的顺序问题）误触发提交。已修复 `features/control/components/ControlWorkspace.tsx` 的目标值输入框，并收进 `scripts/check-design-system.mjs` 门禁。
  - **深浅色与浏览器原生控件同步（`dark-mode`、`dark-mode-toggle`）**：`apps/hvac-web/index.html` 声明 `<meta name="color-scheme" content="light dark" />`；`src/global.css` 在 `:root` 设 `color-scheme: light dark;`、`:root[data-theme="light"]` 设 `color-scheme: light;`、`.dark` 设 `color-scheme: dark;`、打印样式强制 `color-scheme: light;`；`app/ThemeGate.tsx` 在「系统 / 浅色 / 深色」三档切换时同步更新 `<meta name="color-scheme">`，让原生滚动条兜底、自动填充底色和系统弹窗跟着页面深浅色走。同样收进 `scripts/check-design-system.mjs` 门禁。
- **改造的**：
  - 不在 CI 里联网执行 `npx modern-web-guidance`，而是把对本仓库真正起作用的确定性规则直接写进 `scripts/check-design-system.mjs`（随 `npm run design:check` 运行）。
- **拒绝的**：
  - **原生 `<dialog>`、Customizable `<select>`、Popover API 重写组件**：`DESIGN.md` 规定组件来自已安装的 shadcn/ui（`radix-nova`），只做组合和令牌定制，不拆改底座。
  - **`webmcp`、`built-in-ai`、`chrome-extensions`、`pwa`**：平台 AI 助手统一走平台网关签发只读授权和 `operations-agent-service`，不在浏览器标签页暴露客户端工具或调用端侧小模型；桌面冷站工作台不做浏览器扩展和 PWA。
  - **微信小程序（`apps/hvac-miniprogram`）**：小程序受微信渲染层样式限制，不套用浏览器 Baseline 规则。
