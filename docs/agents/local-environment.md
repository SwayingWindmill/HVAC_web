# 本地环境

改本地环境、查一个服务的配置、用某个服务的身份做探测、用开发服务器连本地平台时读这一份。现在本地库和种子数据不一致的地方记在 `docs/HANDOFF.md`「本地环境的偏差」。

## 本地平台

本地只有一套：Compose 项目 `hvac-local`，地址 https://localhost:8443，用私有 CA，包含设备接入、两个模拟器和智能服务（`single-lite` 档位）。

```bash
npm run local:up      # 从当前代码构建，依次迁移、初始化身份、写种子数据、启动；可以重复执行
npm run local:ps      # 看状态
npm run local:down    # 停止
npm run local:reset   # 删掉容器、数据卷和模拟器状态；之后 local:up 从零重建
```

运行时文件（证书、密钥、凭据、配置）在 `deploy/platform/phase1/runtime/local/`：

- 管理员口令在 `local-admin.credentials`；
- 各服务的证书在 `internal-pki/<身份>/tls.{crt,key}`，CA 是 `internal-pki/ca.crt`；
- `data/eg8200*` 是两个模拟器收到的指令记录和待发消息。

## 一个服务的配置从哪来

按这个顺序找，前面的覆盖后面的：

1. 运行时环境文件；
2. `scripts/phase1-wsl-compose.mjs`（注入数据库连接串等）；
3. `deploy/platform/phase1/compose.yaml` 和 `wsl.override.yaml`；
4. 服务代码里 `envOr` 的默认值。

## 用某个服务的身份做探测

```bash
docker run --rm --network hvac-local_application --volumes-from <服务容器> ...
```

证书路径和那个服务里一样。

## 开发服务器连本地平台

开发服务器可以代理到 8443，要设三个环境变量：

- `PLATFORM_GATEWAY_PROXY_TARGET=https://localhost:8443`
- `NODE_EXTRA_CA_CERTS` 指向 `runtime/local/internal-pki/ca.crt`
- `VITE_TLS_CERT` / `VITE_TLS_KEY` 指向 `runtime/local/tls/public.*`

实时推送只接受 8443 同源的连接，所以开发服务器下只能看到快照。
