---
date: 2026-11-26
category: 改进
---
- 夜间异地备份补充 restic 持久化缓存目录支持并修复隔离恢复演练脚本 `run-offsite-restore-drill.sh` 中的 3 张 PostgreSQL 实际表名（`core_registry.telemetry_points`、`command_runtime.command_intents`、`alarm_runtime.alarm_current`）。
- 平台告警与接收器改进：`wecom-alert-adapter` 与 `render-alertmanager-config.sh` 新增微信公众号 / 官方测试号模板消息推送（`wechat-mp.env` + `wechat-mp-secret`），同时将 `warning`/`ticket` 级告警静默分流以免除离线探头每日轰炸、把长期离线与严重质量劣化拆分为 `S2DataGoodRateLow`（`warning`）与 `S2DataQualityCritical`（`page`），并在主机指标中补齐常驻容器静默停机告警 `Phase1ContainerStopped`（`hvac_container_running`）。
- OCI 发布脚本 `build-oci-images.sh` 与 `apply-on-server.sh` 新增推送/拉取失败指数退避重试、未启用的 `optional` 镜像默认跳过拉取，以及切流后清理废弃 `:deploy-base` 标签（`--prune-deploy-base`）。
