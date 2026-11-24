---
date: 2026-11-24
category: 变更
---
- 平台网页公网主入口切换至 `https://hvac.qlhtech.com`（通过 Cloudflare Tunnel 与 DNS-01 自动续期证书提供访问），并在过渡期内保留原域名 `https://hvac.swayingwindmill.online` 的微信小程序 `/api/` 与现场网关 `/gateway/v1/` 接口兼容（浏览器访问自动 301 跳转至新域名）。
