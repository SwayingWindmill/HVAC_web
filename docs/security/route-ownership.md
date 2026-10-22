# 接口归属和数据归属

平台只有一个公开入口：平台网关（energy-api）。每个公开接口由哪个服务负责、每类数据由哪个服务写，都写在有版本的登记表里，不靠代理代码或服务内存里的约定。

登记表：

- `contracts/ownership/route-ownership.v1.json`：接口归属；
- `contracts/ownership/data-ownership.v1.json`：数据归属；
- `contracts/ownership/ownership.v1.lock.json`：兼容锁。

`npm run ownership:check` 拒绝：归属不明确、版本号倒退、无效的分流设置，以及没有授权的跨服务数据库写入。

## 接口归属

每个公开接口写明：HTTP 方法和 `/api/v1` 下的路径模板；唯一的负责服务；单调递增的接口版本号；分流方式（现在都是 `{"mode": "all"}`，全部请求走同一个负责服务）；兼容方式（现在都是 `native`）；允许影响路由的范围维度（例如租户、用户）。

网关加载一份校验过的、不可变的快照。每个请求只读一份快照，在执行处理之前确定唯一一个负责服务。没有归属、路径模板重叠、归属冲突或分流设置无效，都直接拒绝。

生效的登记表版本在响应头 `X-Route-Policy-Revision` 里。

## 修改和回滚

只接受更高的登记表版本；改某个接口的负责服务、兼容方式或分流，还要提高这个接口的版本号。

新快照生效前，网关先在 PostgreSQL 里追加一条 `ROUTE_POLICY_CHANGED` 记录；记录写不进去，旧快照继续生效。快照是原子替换的：进行中的请求按原来的决定处理，之后的请求用新版本。

回滚也是发布一个更高的版本、选回原来的负责服务。写一个更旧的版本会被拒绝，不改变当前的路由。

## 路由决定的记录

每个生效的决定在执行处理前写进 `gateway.route_audit_records`，只包含：路径模板、选中的负责服务和版本号；兼容方式；发起的人和当前租户；执行的网关服务身份；策略、请求关联和追踪编号；时间。

这张表有只追加的数据库触发器，网关的运行角色只有 `SELECT` 和 `INSERT`。会话 cookie、CSRF、令牌、授权码和授权从不存进去。

旧系统的路由适配器和按比例分流都已经不在网关里；历史上的迁移定义会被现在的解析器拒绝。

## 数据归属

每个数据库 schema、事件类别和派生数据，都只登记一个写入的服务。例如：

- `gateway`：平台网关；`audit_ledger`：操作记录服务；`iam`：IAM；`core_registry`：登记服务；
- `telemetry_runtime`：遥测；`telemetry_history`：历史写入程序；`analytics`：用电量计算程序；
- `command_runtime`、`alarm_runtime`、`work_order_runtime`、`notification_runtime`、`rule_runtime`、`outbound_delivery`：各自的服务；
- 会话安全事件和路由决定事件：平台网关；设备快照、指令、告警、工单的生命周期事件：各自的服务。

投递和查询的身份只拿到很窄的、不能写的权限。PostgreSQL 集成测试证明网关写不了操作记录的表、投递程序写不了会话状态。

## 测试

```bash
npm run ownership:check
npm run contracts:check
npm run test:registry-routing
npm run test:durable-postgres
npm run build:energy-api
npm run lint
npm run build
```
