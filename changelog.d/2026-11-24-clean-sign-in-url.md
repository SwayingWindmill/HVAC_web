---
date: 2026-11-24
category: 改进
---
- 登录页地址精简为纯 `/sign-in`：身份服务将 `login_challenge` 与一次性失败提示保存在同源 `HttpOnly` Cookie 中，前端通过 `GET /identity/login-context` 读取当前登录步骤（密码或一次性验证码）与派生的 `csrfToken`；退出登录落到 `/?logged_out=1` 后由前端清理为 `/sign-in`。
