# 业务模块

`modules/` 里每个目录是一个业务领域：它的代码、数据库迁移和测试数据都放在一起，这类业务数据只由它修改。模块不等于一个单独运行的进程：大多数模块默认嵌在 `cmd/` 的某个进程里。

- `registry/`：租户、站点、空间、资产、设备、产品和点位的登记。
- `iam/`：权限（角色指派）、对每次请求的权限判定、签发短时授权，以及从清单同步身份（`identity-reconciler`）。
- `audit/`：操作记录，只追加不修改；带一个可靠接收事件的收件表。
- `connectivity/`：站点网关的接入、证书和 MQTT 数据接收（`cmd/connectivity` 运行它）。
- `command/`：控制指令的审批、下发和结果核对；下发和核对在 `cmd/connectivity` 里跑。
- `telemetry/`：设备读数的接收、当前状态、历史和查询；默认在 `cmd/telemetry-worker` 里跑。
- `energy/`：把电表的累计读数算成每段时间的用电量，以及电价窗口；默认在 `cmd/telemetry-worker` 里跑。
- `metric/`：指标的定义、绑定、计算和结果；在 `cmd/metric-worker` 里跑。
- `alarm/`：告警规则和告警的发生、确认、恢复；`cmd/alarm-rules` 从文件发布站点的告警规则。
- `workorder/`：工单。
- `scheduler/`：定时任务的扫描和领取；在 `cmd/scheduler` 里跑。
- `maintenance/`：运维作业；在 `cmd/maintenance-worker` 里跑，`maintenance-admin` 是手工执行的管理入口。

## 规则

- 业务代码、它的数据库迁移和测试数据放在同一个模块里，一类数据只有一个模块负责。
- 别的模块只用一个模块公开的包，不碰它的 `internal/`。
- `modules/*/cmd/*-owner` 只在需要把某个模块单独拆成进程（验证一个模块出故障时别的不受影响）时使用，不是默认部署的一部分。
- 共用的技术和安全小库放在 `libs/`，不要把 `modules/` 当成工具箱。
