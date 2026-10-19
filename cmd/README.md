# 进程入口

`cmd/` 里是第一阶段长期运行的进程，每个目录一个可执行程序，只负责读配置、组装各业务模块、启动服务。业务逻辑不写在这里，写在 `modules/` 里。

- `energy-api`：平台网关，网页的接口、登录会话和权限检查，默认还嵌着告警、工单、登记、权限等业务模块（见 `energy-api/README.md`）。
- `connectivity`：接收站点网关的 MQTT 数据，下发并核对控制指令，管理网关证书。
- `telemetry-worker`：接收设备读数、维护当前状态和实时推送、写历史，把电表读数算成用电量，同步电价窗口。
- `metric-worker`：按计划计算指标。
- `scheduler`：扫描定时任务、分派给对应的服务。
- `maintenance-worker`：证书到期检查、失败任务处理、退租户这类运维作业。

每个入口有自己的 `go.mod`（模块路径 `github.com/quanlaihe/hvac-web/cmd/<名字>`）。哪些进程在第一阶段默认运行，以 `deploy/platform/phase1/runtime-inventory.v1.json` 为准。
