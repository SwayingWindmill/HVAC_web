# 微信小程序

冷站值班人员在手机上用的小程序（#664）：看节能、今天用能、冷站和告警，问 AI 助手。和网页共用平台网关、账号和权限。设计稿在画布「值班小程序界面」，规范在设计系统「泉来禾 HVAC 设计系统」的「小程序」一节。

## 现在做到哪

- 账号密码登录（账号开了验证码的再输验证码），登录后的会话放在请求的 `Authorization` 头里。
- 总览：本月节省和在统计的节能措施、还能再省、今天用能和昨天同时段比、正在发生的告警条数。
- 总览上的「冷站现在」：在运行还是停机、几台在转、功率、供水和室内温度，点进运行页。
- 运行：「实时运行」是冷站状态、功率、制冷量、能效、今天每小时能效和水系统读数；「设备」按运行、停机、需关注筛选，点进设备看全部读数和今天每小时用电。每 30 秒刷新一次（和网页没有实时推送时一样），下拉刷新还会重新读设备清单。
- 我的：姓名、当前站点、退出登录；切换站点（从总览的站点名或「我的」进入，记住上次选的）。

还没做：微信一键登录、助手、通知、告警微信提醒。

## 打开和预览

1. 用微信开发者工具「导入项目」，目录选 `apps/hvac-miniprogram`（不是 `miniprogram/` 子目录），AppID 已在 `project.config.json`。
2. 开发者工具自己编译 TypeScript（`useCompilerPlugins`），不需要另外构建。
3. 小程序只连 `https://hvac.swayingwindmill.online`（`miniprogram/lib/platform.ts`）。真机预览前，管理员要在微信公众平台「开发管理 → 开发设置 → 服务器域名」把它加进 request 合法域名。

## 代码约定

照微信官方的开发和性能指引：

- 页面和组件都用 TypeScript；`lazyCodeLoading: requiredComponents` 按需注入组件。
- 底部导航是官方的自定义 tabBar（`custom-tab-bar/`），每个标签页在 `onShow` 里标出自己。
- 窗口用 `navigationStyle: custom`，大标题由 `components/page-header` 对齐微信的胶囊按钮。
- 只通过 `lib/gateway.ts` 访问平台：带上会话、401 时回到登录页、失败给出平台的问题代码。
- `setData` 只传页面要显示的内容；总览只在显示时每分钟刷新，切到后台就停。
- 不靠手机的时区：今天从哪一刻开始、本月是哪几天，都从平台按站点时区切好的「天」里推出来（`lib/today.ts`、`lib/savings.ts`）。数字格式也自己写（`lib/format.ts`），不用 `toLocaleString`。
- 冷站的分类、运行状态和读数规则和网页「实时运行」一致（`lib/plant.ts` 对照 `apps/hvac-web/src/features/operations/realtime/plant-model.ts`）。点位的中文名、单位和小数位从网页的 `src/domain/centralPlantTelemetry.ts` 复制到 `miniprogram/shared/`（微信只编译 `miniprogram/` 里的文件）：改了网页那份后跑 `npm -w @hvac/miniprogram run sync`，测试会检查两份是否一致。
- 设备的时间按站点的钟点显示，也是从站点「今天」的起点推出来的（`lib/run.ts` 的 `siteClock`）。
- AppSecret、会话密钥都不进小程序。

## 测试

```bash
npm run miniprogram:check
```

先做两份类型检查（小程序代码只用微信的类型、测试只用 Node 的类型），再跑 `test/` 里的测试：今天和昨天同时段的计算、本月节省、请求封装和登录结果、数字格式、总览显示什么、运行页显示什么、读设备当前值时怎么分批；还有一条拿网页浏览器测试录下的设备数据，让小程序和网页各算一遍冷站，逐台设备、逐个读数比对。页面在开发者工具和真机上看。
