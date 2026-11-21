---
date: 2026-11-21
category: 修复
---
- 为 `deploy/platform/phase1/nginx/nginx.conf` 中含 `{8}` 等量词的 UUIDv7 网关客户端证书正则加上双引号，修复 Nginx 启动报 `unexpected "{"` 导致生产发布冒烟 502 回滚的问题，并在 `deployment:phase1:check` 增加未加引号 `{...}` 正则的静态拦截。
