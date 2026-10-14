# 登录页

Issue #540。2026-10-14 与用户确认。登录页用 shadcn 官方登录组件做，和应用其他页面共用一套设计系统。

## 问题

登录页是身份服务在 Go 代码里手写的一段 HTML（`services/identity-service/internal/identity/server.go` 的 `loginPageTemplate`），颜色、字体、圆角都是写死的旧值，深色模式、品牌标志、设计令牌都用不上。前端换设计系统时碰不到它。

## 决定

| 决策 | 结论 |
| --- | --- |
| 谁负责登录流程 | 身份服务。它创建登录请求、校验账号口令和一次性验证码、签发授权码，这部分不变 |
| 谁负责登录界面 | Web 应用的 `/sign-in` 页面，用 shadcn `login-03` block（卡片居中、浅灰背景），去掉第三方登录、注册和忘记密码 |
| 口令走向 | 表单直接 POST 到身份服务的 `/identity/login`（同源），Web 应用和网关不经手口令 |
| 参考 | Ory Hydra 的 login challenge + 自定义登录界面、Zitadel 新版登录：身份服务只管流程，界面独立 |

## 流程

1. 网关把浏览器送到 `/identity/authorize`。身份服务创建登录请求，设置只有它能读的 `__Host-hvac_idp_login` cookie，然后 303 跳到 `/sign-in?login_challenge=<编号>`。
2. `/sign-in` 显示用户名和密码表单，表单 POST 到 `/identity/login`，带上隐藏的 `challenge`。编号必须和 cookie 一致（防止跨站伪造登录）。
3. 身份服务的处理结果：
   - 成功：跳回网关回调，和现在一样。
   - 用户名或密码错：跳到 `/sign-in?login_challenge=<编号>&error=invalid_credentials`。
   - 需要一次性验证码：跳到 `/sign-in?login_challenge=<编号>&step=totp`，页面显示验证码表单，POST 到 `/identity/mfa/totp`。验证码错：`&step=totp&error=totp_invalid`。
   - 需要先配置两步验证：`&error=mfa_enrollment_required`。
   - 请求过期或无效：`/sign-in?error=expired`，页面提示过期，并提供「重新登录」按钮（重新发起登录）。
   - 身份服务出错：`&error=unavailable`。
4. 用户名不放进地址。页面提交前把用户名存进本标签页的 `sessionStorage`，出错回来时填回去。
5. 地址里没有 `login_challenge` 也没有 `error` 时，`/sign-in` 保持现有行为（已退出、登录已失效）。

## 界面

2026-10-14 第二版（用户要求更美观，参考 Vercel、Supabase、Stripe 和一张扁平插画）：

- 布局：shadcn `login-02` 左右翻转。左侧品牌区占满剩余宽度，右侧 560px 白底登录栏。
- 品牌区：泉来禾标志（新版：一颗水滴被 S 形曲线分成靛蓝和青色两半，一半是冷水、一半像叶子，代表冷站节能；没有底色，`public/quanlaihe-mark.svg`，全站共用）加「泉来禾智慧能源」；中间是节能主题扁平插画（`app/SignInIllustration.tsx`：楼顶冷却塔、戴安全帽的运维人员、能耗下降的看板），风扇转动、冷气上升、柱状图起伏、云和徽标漂浮、植物摇摆，全部 `motion-safe`，系统开了「减少动态效果」时静止；底部一句口号「让冷站省下每一度电」。插画颜色取设计系统的靛蓝、青、琥珀，深色主题有一套对应颜色。
- 标题「欢迎回来」，说明「使用平台账号登录」。
- 字段：用户名、密码（加高的输入框，带占位提示），「记住用户名」，按钮「登录」。字段的 id 保持 `username`、`password`，按钮文字保持「登录」，本地脚本用它们登录。
- 「记住用户名」：勾选后用户名存在这台电脑（`localStorage`），下次打开自动填入；不勾选就删掉。没有注册和忘记密码。
- 验证码步骤：标题「安全确认」，字段「一次性验证码」（6 位数字），按钮「确认」。
- 错误用 shadcn `Alert` 显示在表单上方，文案用日常用语。

## 交付

一个 PR：身份服务改为跳转（删除 HTML 模板和渲染代码），Web 应用 `/sign-in` 页面，浏览器测试，CHANGELOG 和 HANDOFF。验收在本地真实环境（https://localhost:8443）用 1440 宽度截图，合并后部署到服务器。
