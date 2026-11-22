---
name: eg8200-linux-access
description: "Establish, operate, and troubleshoot SSH Linux access to EG8200 edge gateways over the platform fail-closed reverse tunnel (127.0.0.1:18822 -> EG8200:22), bootstrap operator ED25519 keys via the Node-RED 1880 tunnel when gateway SSH credentials are unavailable, verify connectivity.gateway_tunnel_audit rows, and prepare armv7l edge deployments. Use when connecting to an EG8200 via SSH, provisioning a gateway, debugging tunnel or systemd loopback units, or planning edge runtime deployments."
---

# EG8200 网关 Linux SSH 接入与无口令引导

通过平台已有双向 TLS 反向隧道按需打开受控 SSH 回环通道（宿主机 `127.0.0.1:18822` 转发至网关 `127.0.0.1:22`），并在只有 Node-RED `1880` 通道、没有现场 SSH 口令时安全注入操作员公钥与升级白名单。

## 1. 架构与代码定位

EG8200 侧 Node-RED（`tab-platform-tunnel`）主动向平台 `:443/gateway/v1/tunnel` 发起 `Upgrade: hvac-gateway-tunnel/v1` 双向 TLS 长连接 -> 平台 `nginx` 校验网关证书并通过 `X-Gateway-Client-Cert` 转发给 `connectivity:8448` -> 宿主机 `systemd` 回环套接字按需桥接至网关本机端口：

- `127.0.0.1:18800` -> 网关 `127.0.0.1:1880`（Node-RED 管理端，常开）
- `127.0.0.1:18822` -> 网关 `127.0.0.1:22`（SSH，默认关闭，需双端同时放行后才可用）

| 组件 | 仓库路径与关键符号 | 作用 |
| --- | --- | --- |
| 服务端隧道白名单与回环监听器 | `modules/connectivity/pkg/tunnel/tunnel.go`（`AllowedTargetPort = 1880`、`SSHTargetPort = 22`、`IsSupportedTargetPort`、`WithAllowedTargetPorts`、`StartLoopbackListenerForPort`） | 仅允许 `{1880, 22}`；`OpenStream` 按允许集合 fail-closed 拦截其他端口 |
| 运行时开关与回环地址校验 | `cmd/connectivity/credential_runtime.go`（`CONNECTIVITY_TUNNEL_SSH_LOOPBACK_ADDR`、`ValidateLoopbackAddr`） | 环境变量默认留空关闭；启用时拒绝 `0.0.0.0` 与 `:0` |
| Compose 与主机 systemd 模板 | `deploy/platform/phase1/compose.yaml`、`deploy/platform/phase1/systemd/hvac-gateway-ssh-loopback.{socket,@.service}` | 透传环境变量并限定仅宿主机 `127.0.0.1:18822` 拉起 `-tunnel-stdio 127.0.0.1:18822` |
| 网关侧隧道客户端 | `edge/node-red/gateway-nodered-tunnel.ts`（`SSH_TARGET_PORT = 22`、`allowedTargetPorts: [1880, 22]`、`buildGatewayTunnelFlow`） | 网关本机端口映射表，拒绝白名单外端口并避免发往本机无效建连 |
| SSH 建流审计表 | `infra/connectivity/postgres/init/gateway-tunnel-audit.sql`（`connectivity.gateway_tunnel_audit`） | 记录 `SESSION_OPENED`、`SESSION_CLOSED`、`STREAM_OPENED`、`STREAM_REJECTED`（含 `target_port` 与流量字节数） |
| 1880 隧道与证书续签说明 | `docs/operations/gateway-nodered-tunnel.md` | 节点流同步脚本与 14 天双向 TLS 证书自动续签规范 |

## 2. 已验证的 EG8200 硬件与系统基线（松雅轩现场）

规划边缘自研进程或排障时直接按下列实测事实决策，不再反复探测：

- **系统与 init**：`Ubuntu 20.04.5 LTS`（Focal），内核 `Linux IOTRouter 5.4.61 #19 SMP PREEMPT armv7l`，`PID 1` 为 `systemd 245`。
- **CPU 与 Go 交叉编译目标**：双核 ARMv7 Cortex-A7（`vfpv3` / `vfpv4` / `neon` / `idiva` / `idivt`），编译边缘二进制固定使用 `GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0`。
- **内存预算**：总计 `485 MiB`（`512 MB` DDR），`node-red` 单进程约占 `157 MiB` RSS，空余约 `173 MiB`。
- **存储与权限实况（重要纠偏）**：宿主机根分区 `/dev/mmcblk0p5`（`3.4 GiB` `ext4 rw`，`/etc/fstab` 为 `/dev/mmcblk0p5 / ext4 remount,rw 0 0`）可用约 `1.2 GiB`，**宿主机 SSH `root` 下 `/`、`/etc`、`/etc/systemd/system`、`/root`、`/opt` 与 `/home/pi/edge8000` 全部可读写（`lsblk` 确认 `RO=0`）**。此前通过 Node-RED 沙箱看到的 `/dev/root /etc ext4 ro,relatime` **仅存在于 `node-red.service` 的私有挂载命名空间内**（因其 `/lib/systemd/system/node-red.service` 配置了 `ReadOnlyDirectories=/etc/`）。
- **在跑服务、自启链与账号**：
  - `sshd.service`（`/usr/sbin/sshd -D`，`PermitRootLogin yes`，默认启用 `PubkeyAuthentication yes`）、`iotrouter.service`（`zh_node.app`）、`iotrouter-web.service`；登录账号为 `root` 与 `pi`，`/root/.ssh` 为 `0700 root:root`。
  - `node-red.service` 在 systemd 中本身为 `disabled`，真正开机拉起它的是 `/etc/rc.local` 调起的 `/home/pi/edge8000/app/usr_service_auto_startup.sh`（重置 `eth0`/`networking` 并拉起 `iotrouter.service`、`influxdb.service`，随后执行 `systemctl restart node-red.service`，并用后台 3 秒巡检循环守护伴生进程 `ZHCSharpMid`）。因此停用 Node-RED 时需在独立进程接管反向隧道与串口后再执行 `systemctl stop node-red.service && systemctl mask node-red.service`（或清理 `usr_service_auto_startup.sh` 对应拉起段落），防止重启复活。
- **现场串口总线**：`/dev/ttyAS3`（`9600 8N1`）由电表（`UID 2,3,4,5`）、水表（`UID 10`）、无线采集器（`UID 20`）和 ET1010（`UID 31`）共用。

## 3. 已开通站点的日常连接、按需开关与核验

1. **通过平台跳板机非交互 SSH 登录或执行命令**（从 Windows PowerShell 或 WSL 均可执行，网关和服务器公网均不暴露 `22` 端口通道）：
   ```bash
   ssh -n -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=5 -o ServerAliveCountMax=2 -J <运维账号>@<平台服务器> -p 18822 root@127.0.0.1 'uname -a'
   ```
   如需进入交互式终端，去掉 `-n` 和远端命令参数即可。
2. **在平台服务器按需关闭或重新开启 SSH 通道**（不影响 `1880` Node-RED 隧道）：
   ```bash
   sudo systemctl stop hvac-gateway-ssh-loopback.socket
   sudo systemctl start hvac-gateway-ssh-loopback.socket
   ss -ltn | grep 18822   # 必须严格只见 127.0.0.1:18822
   ```
3. **在 `hvac_s1` 库核验 22 端口审计留痕**：
   ```sql
   SELECT event_type, target_port, stream_id, bytes_tx, bytes_rx, reason, occurred_at
   FROM connectivity.gateway_tunnel_audit
   WHERE target_port = 22
   ORDER BY occurred_at DESC
   LIMIT 20;
   ```

## 4. 从零开通流程（仅有 1880 Node-RED 隧道、无 EG8200 SSH 凭据）

前提：修改现网网关或生产主机前必须取得当前会话明确授权；严禁读取、打印或写入任何文件中的口令、私钥、Bearer Token、`server.runtime.env` 全文或 `/opt/hvac-platform/runtime/secrets/songyaxuan-nodered-admin` 内容。

1. **第 1 步（只读探测 `sshd` 与挂载状态）**：
   - 先通过宿主机 `127.0.0.1:18800` 调用 `GET /diagnostics` 与 `GET /settings` 确认网关在线。
   - 若新网关 OS / `sshd` 状态未知，仅在非串口、非隧道页签（如 `平台·证书` `9f20000000000000`）临时挂载带随机 `?k=<nonce>` 的 HTTP 函数节点，用 `Node-RED-Deployment-Type: nodes` 提交并在 `try ... finally` 中确保还原原流程及校验 SHA-256。核实 `sshd` 运行中、`PermitRootLogin yes`、`PubkeyAuthentication` 未禁用且 `/root/.ssh` 所在分区可写。
2. **第 2 步（幂等写入操作员 ED25519 公钥）**：
   - 仅从跳板机 `~/.ssh/authorized_keys` 读取经 `ssh-ed25519 ` 前缀校验的公钥行（禁止碰私钥）。
   - 用同样的 `Deployment-Type: nodes` 作用域节点与 `try ... finally` 还原模式，将缺失公钥去重合并进 `/root/.ssh/authorized_keys`：保证 `/root/.ssh` 为 `0700`、目标文件为 `0600`、属主 `root:root`，采用同目录临时文件写盘后原子 `rename`。
   - 还原后再次 `GET /flows`，核对整份流的 SHA-256 与变更前完全一致且全部页签仍处于启用状态。
3. **第 3 步（升级网关隧道放行端口 `22`）**：
   - 用 `tsc` 将 `edge/node-red/gateway-nodered-tunnel.ts` 编译为 CommonJS，并在本地 Node 通过 `createRequire` 自检：验证 `1880` 与 `22` 放行、PoP 校验通过、未授权端口（如 `80`）在拨号前即被 `RangeError` 拒绝。
   - 只更新 `tab-platform-tunnel` 页签中的 `fn-platform-tunnel-client` 节点正文（`allowedTargetPorts: [1880, 22]`），通过 `Node-RED-Deployment-Type: flows` 提交。
   - **预期断连**：提交时旧隧道因 `finalize` 执行 `c.stop()` 而立即关闭，本次 HTTP 请求返回 `RemoteDisconnected` 属正常现象；等待 `2` 到 `5` 秒重连后，调 `GET /flows` 核对节点指纹与全部页签启用状态，并确认 `http://127.0.0.1:18800/auth/login` 返回 HTTP `200`。
4. **第 4 步（服务端启用与端到端验收）**：
   - 在平台服务器 `/opt/hvac-platform/deploy/platform/phase1/environments/server.runtime.env`（`0600`）原子写入 `CONNECTIVITY_TUNNEL_SSH_LOOPBACK_ADDR=127.0.0.1:18822`，发布或重启 `connectivity`，复制并启用 `hvac-gateway-ssh-loopback.{socket,@.service}`。
   - 必须同时验证四项才算完成：
     1. `ss -ltn | grep 18822` 仅显示 `127.0.0.1:18822`。
     2. 实际跳板登录执行 `uname -a; id; df -h /` 返回预期设备与权限。
     3. Postgres `hvac_s1` 的 `connectivity.gateway_tunnel_audit` 出现 `STREAM_OPENED`（`target_port = 22`）。
     4. `/healthz`、`127.0.0.1:18800` 及 Prometheus `hvac_mqtt_online_gateways`、`hvac_mqtt_active_tunnels` 指标正常。

## 5. 高频踩坑与硬性红线

1. **Node-RED 函数沙箱没有全局 `process`**：不要直接引用 `process.pid` 或 `process.cwd()`，改用读取 `/proc/self/status`、`/proc/self/cmdline`、`os` 与 `fs.statfsSync`；所有临时 HTTP 函数节点外层必须包 `try / catch` 返回 HTTP `500` JSON，防止 `http response` 不触发导致请求死等超时。
2. **严禁改动或整套重部署 Modbus / ET1010 页签**：不要触碰 `ET1010·机组启停` 及 `/dev/ttyAS3` 串口节点；临时探测一律局限在非串口、非隧道页签并用 `Node-RED-Deployment-Type: nodes` + `finally` 原样还原。
3. **升级 `tab-platform-tunnel` 会切断当前响应连接**：看到 `http.client.RemoteDisconnected` 后不要盲目重试写入，先等 `2` 到 `5` 秒并用 `GET /flows` 核对是否已经生效。
4. **PowerShell、WSL 与 SSH 引号防卡死规范**：禁止在 PowerShell 单行命令里嵌套多层引号调 WSL SSH，尤其禁止在传给原生 `ssh.exe` 的单引号远程命令里混用双引号 `"`（Windows `CommandLineToArgvW` 会剥离裸 `"` 导致 `grep`/`cat` 等待 stdin 卡死）。非交互探测统一加 `-n -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=5 -o ServerAliveCountMax=2`；复杂多步脚本写成无 BOM 的 LF `.sh` 或使用 PowerShell `@'...'@` 转 Base64 执行。
5. **区分 Node-RED 挂载命名空间与宿主机 `root` 权限**：Node-RED 内部因 `ReadOnlyDirectories=/etc/` 无法写 `/etc`，但 SSH `root` 登录宿主机对 `/etc` 与 `/etc/systemd/system/` 完全可写，可直接安装标准 `systemd` 服务；同时切勿在未接管反向 SSH 隧道前停掉 `node-red.service`，也不要误改 `/home/pi/edge8000/app/usr_service_auto_startup.sh` 中的 `eth0`/`networking` 与 `iotrouter.service` 启动段。
6. **代码与单测易错点**：
   - Go 侧 `ValidateLoopbackAddr` 拒绝 `:0` 端口，编写回环监听器拒接单测时需传入明确端口（如 `127.0.0.1:18822`）。
   - `connectivity.gateway_tunnel_audit` 只定义了 4 个事件类型，不存在 `STREAM_CLOSED`。
   - TypeScript `TunnelStreamHandle` 使用 `await stream.readUntilEnd()` 结束读取，不要使用不存在的 `onEnd` 回调。
   - Python `subprocess.check_output(..., text=True)` 的 `input` 参数必须传 `str`，不可传 `bytes`。
