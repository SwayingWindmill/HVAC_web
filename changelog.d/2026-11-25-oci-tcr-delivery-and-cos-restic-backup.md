---
date: 2026-11-25
category: 改进
---
- 发布与镜像交付新增基于不可变 `@sha256` 摘要的 OCI 构建发布脚本 `build-oci-images.sh` 与只拉取不构建的服务端切换逻辑（支持 `deploy_mode` 与 `target_environment` 选择），消除生产机原地构建及 `npm ci`。
- 夜间备份新增基于 restic 与腾讯云 COS（`s3.bucket-lookup=dns`）的加密异地同步脚本 `sync-offsite-restic.sh`、全隔离容器恢复演练脚本 `run-offsite-restore-drill.sh` 及 `store="offsite"` 新鲜度监控指标。
