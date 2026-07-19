# ThingsBoard 真实契约采集运行手册

## 目的

运行 `capture-thingsboard-contracts.mjs`，从目标 ThingsBoard 实例只读采集并脱敏固化以下证据：

- Tenant、Customer、Asset、Device、Device Profile、Asset Profile 和 Relation；
- 代表设备的遥测 Key、观测类型、24 小时采样频率、乱序和重复时间戳；
- Client、Shared、Server Attribute Scope 及类型；
- Device Profile、Rule Chain 与实际 Alarm 类型、级别和生命周期；
- Dashboard、Device Profile 与 Rule Chain 中可静态发现的 RPC method、timeout、one-way/persistent 配置；
- 尚未取得真实 ACK 的明确缺口。

采集器不会调用 RPC、Attribute 写入、实体写入、删除或设备凭据接口。

## 安全运行

在 PowerShell 中运行。启动脚本会隐藏输入密码，并在 `finally` 中清理所有凭据环境变量，不写入仓库或 Shell 历史。

```powershell
Set-Location E:\Code\HVAC_web
powershell -ExecutionPolicy Bypass -File `
  .\.scratch\go-data-ai-platform\research\run-thingsboard-contract-capture.ps1
```

可选覆盖：

```powershell
$env:TB_WINDOW_HOURS = '24'
$env:TB_REPRESENTATIVES_PER_PROFILE = '2'
$env:TB_MAX_ENTITIES = '500'
$env:TB_OUTPUT_DIR = '.scratch/go-data-ai-platform/research/thingsboard-contract-evidence'
```

## 预期输出

默认输出目录：

```text
.scratch/go-data-ai-platform/research/thingsboard-contract-evidence/
├── capture-manifest.json
├── capture-report.md
├── instance-and-entity-inventory.json
├── representative-device-contracts.json
├── telemetry-metadata-evidence.json
├── alarm-contract-evidence.json
└── rpc-contract-evidence.json
```

实体 ID、Customer/Device/Asset 标题不会写入输出；它们被替换为稳定哈希别名。遥测 Key、Attribute Key、Alarm Type、RPC Method 和工程单位属于契约信息，会被保留。访问令牌、密码、设备凭据及其他 Secret 不会持久化。

## 验收

执行成功必须满足：

1. `capture-manifest.json` 中 `readOnly=true`、`credentialsPersisted=false`、`mutationsPerformed=[]`；
2. `capture-report.md` 包含实际覆盖数量；
3. 每个 Device Profile 至少有一台代表设备，或者报告明确指出该 Profile 没有设备；
4. 遥测、Attribute 与 Alarm 的观测结果来自目标实例，而不是仓库 Mock；
5. RPC 静态证据与真实 ACK 证据分开；只读采集不得把 HTTP 超时推断成设备未执行；
6. 若 `ackSamplesObserved=0`，票据 14 保持未解决，直到完成单独审批的无破坏性命令测试。

## 后续无破坏性 RPC 验证

只读采集完成后，应从静态证据中选择设备厂商明确声明安全、可重复且不改变控制状态的方法，例如版本查询、状态读取或 Ping。测试前必须确认：

- 设备和现场负责人；
- 方法名与参数；
- 是否支持 `command_id` 或幂等键；
- 超时、离线和迟到 ACK 的预期；
- 禁止使用启停、设定值、模式切换等控制命令作为首次验证。

该测试必须单独审批和记录，不能由采集器自动执行。
