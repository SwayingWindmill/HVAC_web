---
date: 2026-11-21
category: 新增
---
- 补齐规则引擎与场景告警收尾闭环能力（覆盖 `modules/command`、`modules/alarm`、`modules/registry`、`services/notification-service`、`cmd/energy-api`、`apps/hvac-web` 与迁移 `control-alarm-dead-letter-inspect.sql`、`alarm-rule-action-outbox.sql`）：
  - **设备级控制优先级锁与控制告警死信重投**：新增设备优先级锁定与解除接口，控制中心主机卡片展示实时锁定分级与到期倒计时，并支持在右滑抽屉中查看控制告警投递死信及按条幂等重投。
  - **规则前置依赖门控、级联重算与联动动作死信**：告警规则支持前置规则依赖门控与下游级联重算；新增规则联动动作发件箱（自动创建工单、按控制优先级路径下发保护指令、经 SSRF 与密钥目录校验的外部回调）以及规则死信查询与幂等重投面板。
  - **站点通知路由配置热重载与分组摘要**：支持从文件系统热加载站点分级通知路由及脱敏摘要查看，告警工作台展示通知路由状态、分组升级路径、中文路由名称与中文化仿真联动计划。
