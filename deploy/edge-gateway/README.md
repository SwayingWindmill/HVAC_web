# Linux 边缘守护进程（`cmd/edge-gateway`）构建与 EG8200 部署说明

`cmd/edge-gateway` 是面向 EG8200（ARMv7 Cortex-A7，485 MiB 内存）及通用 Linux 边缘网关的独立守护进程，用于逐步替代占用约 `157 MiB` 内存的 Node-RED，直接复用平台共享库 `libs/edgecontrol` 并具备 A/B 双槽位 OTA 自愈回滚能力。

## 1. 目录规划

现场网关默认部署目录（均位于可写 `/opt`）：

- `/opt/hvac-edge-gateway/config.json`：运行配置（网关 ID、双向 TLS 路径、串口 `/dev/ttyAS3`、现场冷站声明式采集与顺控配置 `fieldPlant` / `fieldPlantFile`、`OTA` 受信任 Ed25519 公钥、反向隧道开关）
- `/opt/hvac-edge-gateway/field-plant.json`：可选独立现场冷站声明式配置文件（由 `config.json` 中的 `fieldPlantFile` 引用；如松雅轩站点直接复用 [songyaxuan.edge-plant.json](../../deploy/sites/songyaxuan.edge-plant.json)），新站点接入只需编写 JSON，无需修改 `cmd/edge-gateway` Go 代码
- `/opt/hvac-edge-gateway/state/`：配置影子快照与分级断点续传磁盘队列（`mqtt-spool.json`、`field-plant-state.json`、`do-executor-state.json`）
- `/opt/hvac-edge-gateway/ota/slots/{slot-a,slot-b}`：A/B 双槽位可执行文件，`ota/current` 符号链接指向当前生效版本，`ota/previous` 指向回退版本

## 2. 声明式现场冷站接入（`fieldPlant` / `fieldPlantFile`）

新站点部署只需在 `config.json` 指定以下二选一：

```json
{
  "fieldPlantFile": "/opt/hvac-edge-gateway/field-plant.json"
}
```

（相对路径按 `config.json` 所在目录解析，也可直接内联为 `"fieldPlant": { ... }`，二者互斥。）

声明式文件完整定义四类站点差异，零 Go 代码改动即可复制到新现场：

- `meters` + `flowMeter` + `btuCalculator`：电表/合并馈线阈值与迟滞、冷量虚拟公式、电量基线与 `synthesizedTotal` 汇总电表；
- `concentrator` + `concentratorRpc`：无线测温集中器激活通道、点位转发 `copyRoutes`（支持通过平台配置影子 `pointMappings` 在线热更新，清空后自动回落到本地基线路由）以及受保护不可修改的 RS-485 从站 UID 集合；
- `doControl`：DO 模块寄存器映射、`winnerControllerId`、任意长度辅机顺控链与失压自检回滚/停机功耗校验。

参考模板见 [songyaxuan.edge-plant.json](../../deploy/sites/songyaxuan.edge-plant.json)。

## 2. 交叉编译（ARMv7）

在开发机或 WSL 仓库根目录执行：

```bash
bash deploy/edge-gateway/build-armv7.sh 1.0.0
```

产物位于 `out/edge-gateway-linux-armv7`（及 `.sha256` 校验文件），为不依赖 CGO 的静态 32 位 ARMv7 ELF 二进制（约 9.5 MiB）。

## 3. EG8200 首次安装与从 Node-RED 安全切换

通过 SSH `root` 登录网关后，`/etc` 与 `/etc/systemd/system` 在宿主机命名空间可直接写入（此前看到的 `/etc` 只读仅限 `node-red.service` 的 `ReadOnlyDirectories=/etc/` 沙箱）。

1. 安装首版二进制到 `slot-a` 并安装 `/etc/systemd/system/hvac-edge-gateway.service`：
   ```bash
   sudo bash deploy/edge-gateway/install.sh /root/edge-gateway-linux-armv7
   ```
2. **先保持 Node-RED 串口页签独占或切换窗口**，在 `edge-gateway` 配置先关闭重复串口采集与冲突端口，启动服务并验证：
   - 反向 SSH 隧道已由 `edge-gateway` 稳定建连并可从跳板机登录；
   - `curl -fsS http://127.0.0.1:<healthListenPort>/healthz` 返回 `ok`。
3. 确认独立隧道可断网重连后，在短维护窗口把 `/dev/ttyAS3` 控制权交由 `edge-gateway`，再彻底停用并屏蔽 Node-RED（防止 `/home/pi/edge8000/app/usr_service_auto_startup.sh` 下次启动时将其拉起）：
   ```bash
   systemctl stop node-red.service
   systemctl mask node-red.service
   ```
   切勿删除 `usr_service_auto_startup.sh` 里的 `eth0` 网络重置与厂商 `iotrouter.service` 启动段。
