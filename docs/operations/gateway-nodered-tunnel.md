# 现场网关 Node-RED 常驻反向隧道与自动续签

现场网关 EG8200 部署在内网 NAT 后面，默认只开放本机 `127.0.0.1:1880`（Node-RED 管理端）。过去依赖网关厂商云入口远程访问时，一段时间无操作会自动断开。本文档说明如何通过平台自有 `:443` 端口常驻维护 Node-RED 访问通道，以及 14 天证书自动续签的工作方式。

## 链路怎么走

1. 网关侧 `edge/node-red/gateway-nodered-tunnel.ts` 运行在 Node-RED `平台·常驻隧道` 页签中，主动通过双向 TLS（使用 `/root/hvac-platform/gateway.crt`、`gateway.key` 和 `ca.crt`）连接平台域名 `:443/gateway/v1/tunnel`，发送 `Upgrade: hvac-gateway-tunnel/v1`。
2. 平台 `nginx`（`deploy/platform/phase1/nginx/nginx.conf`）校验证书有效后把原始证书放进 `X-Gateway-Client-Cert` 头，走内部双向 TLS 转发给 `connectivity:8448`。
3. `connectivity` 进程里的 `CredentialService.VerifyActiveCredential` 检查库里这台网关没有被吊销、证书未过期后，返回 `HTTP/1.1 101 Switching Protocols`，清掉 HTTP 读写超时并转入多路复用二进制帧通道。
4. 平台主机 `systemd` 套接字单元 `hvac-gateway-nodered-loopback.socket` 只绑定宿主机回环地址 `127.0.0.1:18800`。有人访问宿主机 `127.0.0.1:18800` 时，`systemd` 拉起 `hvac-gateway-nodered-loopback@.service`（执行 `docker exec -i hvac-platform-connectivity-1 /service -tunnel-stdio 127.0.0.1:18800`），经过隧道打开网关本机 `127.0.0.1:1880`。
5. 长连接默认每 `25` 秒双向发送 `PING`/`PONG` 心跳，超过 `75` 秒无报文才判定断线并按指数退避重连，避免 HTTP 服务器或 NAT 空闲切断。

## 运维人员怎么连上现场 Node-RED

先在自己电脑用 SSH 把服务器回环端口映射到本地（不把 `18800` 暴露到公网或宿主机 `0.0.0.0`）：

```bash
ssh -N -L 18800:127.0.0.1:18800 <运维账号>@<平台服务器>
```

隧道保持常驻时，本地浏览器打开 `http://127.0.0.1:18800` 即可直接编辑和部署 Node-RED（包括 WebSocket `/comms` 实时状态）。使用完关闭 SSH 端口转发即可。

## 安全边界与断线规则

- **只放行 `1880` 端口**：服务端 `OpenStream` 和网关侧 `GatewayTunnelClient` 都在建流阶段强制校验目标端口必须等于 `1880`。哪怕收到目标端口 `22`（SSH）或 `80`（HTTP）的 `OPEN` 帧，网关也会直接返回 `OPEN_ERR`，不会向本地套接字发起连接。
- **只允许本机回环地址**：`StartLoopbackListener`、`RunStdioBridge` 以及 `CONNECTIVITY_TUNNEL_LOOPBACK_ADDR` 启动校验在遇到 `0.0.0.0:18800`、`:18800` 或局域网/公网 IP 时会立即报错退出，不允许绕过。
- **吊销即刻踢下线**：在平台执行网关凭据吊销（`CredentialService.Revoke`）提交成功后，会立即调用 `Hub.CloseGateway(gatewayId)` 切断现役隧道连接，网关随后发起的任何重连都会被 `VerifyActiveCredential` 拒绝并返回 `401`。
- **保护关键 Node-RED 页签**：通过 `validateProtectedFlowTabs` 发布流配置前检查 `ET1010·机组启停` 和 `平台·常驻隧道` 页签必须保持存在且启用，误删或禁用会直接拦截部署。

## 14 天证书自动续签

`edge/node-red/gateway-cert-autorenew.ts`（Node-RED `平台·证书` 页签）开机自检一次、之后每 `12` 小时检查本地证书剩余有效期：

- 例如证书还剩 `60` 天有效期时，大于阈值 `14` 天，直接跳过续签，不发网络请求。
- 例如证书只剩 `10` 天到期时，自动生成与现有网关 ID（`CN=<gatewayId>`）匹配的 PKCS#10 CSR，用现役证书走双向 TLS 调用 `POST /gateway/v1/renew`。
- 收到新证书后先核对 CA 签发有效且公钥与本地 `gateway.key` 一致，再写入同目录 `.tmp-*` 临时文件并执行原子 `rename` 覆盖 `gateway.crt`，最后调用隧道 `reloadCredentials()` 热切换到新证书。

## 平台主机安装回环服务模板

生产服务器上线时安装模板文件到 `/etc/systemd/system/` 并启用套接字：

```bash
sudo cp deploy/platform/phase1/systemd/hvac-gateway-nodered-loopback.socket /etc/systemd/system/
sudo cp deploy/platform/phase1/systemd/hvac-gateway-nodered-loopback@.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now hvac-gateway-nodered-loopback.socket
```
