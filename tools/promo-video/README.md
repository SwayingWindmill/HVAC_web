# 宣传片「冷站的一天」

60 秒、1920 × 1080、30 帧的产品宣传片，全部由代码生成，用 [HyperFrames](https://github.com/heygen-com/hyperframes)（0.8.140，Apache-2.0）把 HTML 逐帧渲染成视频。片子的故事和分镜在 `film/STORYLINES.md`、`film/SPEC.md`，片子里先于产品出现的小程序功能记在 `film/FEATURE-GAPS.md`。

## 画面从哪来

| 画面 | 来源 |
| --- | --- |
| 冷站三维 | 产品数字孪生的场景代码（`apps/hvac-web/src/features/operations/digital-twin`），`twin/` 只把转动、水流、相机和日光改成按片子时间计算，打包成 `film/assets/twin/twin.js` |
| 产品界面 | 一张卡片，在 `film/index.html` 里按产品设计系统重画产品的单个组件（实时指标、AI 助手、优化建议、确认框、故障诊断、昨日节电），文案和数字与产品一致；不放整页截图 |
| 数据画面 | 负荷曲线、能流图、全年节电日历墙，都在 `film/index.html` 里用 SVG 和演示数据画出来 |
| 小程序画面 | `film/index.html` 里按小程序的设计令牌画的手机界面 |
| 标题、转场、片尾 | `film/index.html` 一条 GSAP 时间轴 |
| 配乐和音效 | `music/compose.ts` 用振荡器和噪声合成（不用采样），写成 `film/assets/audio/score.wav` |

剪辑点、点击、音效的时间只写在 `film/cues.ts`：三维镜头和配乐直接 import 它，页面从 twin 包里拿（`HVACTwin.cues`）。改完重跑 `npm run twin:build` 和 `node music/compose.ts`，三边一起动。

`film/assets/` 下除了手写文件都是生成的，不提交。

## 重新生成

WSL 里（`tools/promo-video`）：

```bash
npm install --no-workspaces
npm run prepare:assets
npm run twin:build
node music/compose.ts
```

只检查三维镜头和光线时，不用渲染整片：

```bash
node scripts/twin-stills.ts /tmp/stills.png 0,10,20,30,40
```

## 渲染

WSL 里的 Chrome 只有软件 WebGL，渲整片要一个多小时；Windows 版 Chrome 能直接用显卡，所以渲染在 Windows 侧做（`win/` 是同一版本 HyperFrames 的 Windows 安装，第一次在 PowerShell 里 `cd win; npm install`）：

```powershell
tools\promo-video\scripts\deliver.ps1 film
```

`deliver.ps1` 按 30 fps 交付质量渲染并混入配乐（约 5 分钟）；快速的页面移动在合成里自带运动模糊。改动中途看效果用草稿：`scripts\hf-win.ps1 render --browser-gpu --quality draft --output renders\draft.mp4`（约 3 分钟）。

检查与快照仍可在 WSL 里跑：`bash scripts/hf.sh lint`、`bash scripts/hf.sh snapshot --at 0,12,27`。
