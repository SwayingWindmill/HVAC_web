# ADR 0015：网关的身份就是它的证书，一个设备接入服务管所有网关

状态：已采纳（2026-10-02）

参照：`docs/architecture/references/thingsboard-v4.4.md` §2、`openems-2026.9.0.md` §5、`myems-v6.9.0.md` §2

## 背景

以前每个网关要单独跑一个 `iot-service`：接入数据只能属于一个租户，进程只服务一个集成，MQTT 服务器的权限文件里按网关写死了租户和站点的编号。加第二个站点要改六个文件、重启三个进程，同一个设备的身份散在七个互不同步的地方。消息主题里带租户、站点和网关，接入程序还得拿它们和配置核对。

## 决定

- 网关在线上的身份就是它的网关证书：一张 X.509 证书，主体名（CN）是这个网关在设备登记里的设备编号。租户和站点从设备登记里查，消息里不带。
- 主题只带网关编号：上行 `hvac/v1/{gatewayId}/up/{telemetry|event|reply}`，下行 `hvac/v1/{gatewayId}/down/command`。
- MQTT 服务器仍用 Mosquitto，用证书认证，权限是固定的模式规则（`infra/telemetry/mqtt/acl`：`pattern write hvac/v1/%u/up/#`、`pattern read hvac/v1/%u/down/#`），每个网关只能读写自己的主题。加网关不用改 MQTT 服务器的任何配置。
- 一个设备接入服务（Connectivity）服务所有租户和网关。它先把网关对应到租户，再做任何按租户的事。
- 设备接入服务持有网关的签发 CA（新生成的 CA 证书同时携带签发证书与签发吊销列表用途）。运维人员给一个已登记的网关生成一个接入码；网关用接入码和证书申请（CSR）换一张 90 天的证书，之后可用现有证书无损轮换：支持通过轮换接口重发接入码并保留既有有效证书并行过渡，也可在 `/gateway/v1/renew` 提交 `revokePeerCertificate: true` 原子签发新证并立即吊销旧证。
- 吊销由设备接入服务以单张证书指纹（`gateway_credentials.status = 'ACTIVE'`）为核心事实源执行，并在需要时导出由网关 CA 签名的 X.509 CRL（`/internal/v1/gateways/ca.crl`；配置 `GATEWAY_CRL_OUTPUT_FILE` 且 CA 支持 CRL 签名时在启动与每次吊销后原子落盘）。由于部署环境下 MQTT 与内部服务共享静态受信根文件且存量 CA 可能无 CRL 签名权限，Mosquitto 默认不配置可能静默失效的 `crlfile`，而由接入服务在上行解析（按网关身份）、续期和隧道握手入口（按证书 SHA-256 指纹）直接拦截。
- 支持两条吊销路径：
  - **单证吊销**（`POST /api/v1/gateways/{gatewayId}/credentials/{credentialId}/revoke`）：只把指定证书标为 `REVOKED` 并记入 `CERTIFICATE_REVOKED` 审计，不封禁 `gateway_enrollments.revoked_at` 也不影响同一网关下其余仍在有效期内的轮换证书；设备接入服务按被吊销证书的 SHA-256 指纹精准踢下线仍使用该旧证书的反向运维隧道并在续期与隧道握手入口立即拒绝该旧证（新证书上的活跃隧道保持不断线），同时更新签名的 X.509 CRL 供 TLS 终止点吊销校验（在未配置 CRL 的 MQTT broker 端口上，若同网关仍持有另一张未过期有效证书，旧证书连接 MQTT 仍由 broker 仅校验 CA 与 `CN={gatewayId}`）。
  - **整台网关吊销**（`POST /api/v1/gateways/{gatewayId}/revoke`）：把该网关所有活跃证书和未使用的接入码一起作废并写入 `gateway_enrollments.revoked_at`，使该网关上行遥测与指令路由立即全部隔离；之后普通接入码接口（`enrollment-code`）与续期接口继续拒绝（`409 GATEWAY_REVOKED`）。只有在管理员显式调用轮换重启用接口（`POST /api/v1/gateways/{gatewayId}/rotate`）时，才会在记入 `IDENTITY_REENABLED` 审计后清零 `revoked_at` 并签发新接入码，所有历史已吊销证书仍保持 `REVOKED`。
- 直接删掉、不做替代的：集成实例和传输配置、接入会话、指令发送的归属租约、按网关的静态绑定文件和环境变量列表、边缘设备管理（版本发布、远程升级、快照）。下发边缘配置以后和边缘运行时一起重新设计。

## 考虑过的做法

- **让 MQTT 服务器查数据库做认证**（Mosquitto 动态安全、go-auth 插件、EMQX 回调）：没有采用。证书已经证明了身份，模式规则已经把每个网关限制在自己的主题里，插件只多一个会出错的环节，不多任何保证。
- **在设备接入服务里内嵌一个 Go 写的 MQTT 服务器**：暂不采用。权限检查会变成进程内调用，但要拿成熟服务器的会话和持久化能力去换我们自己写的。
- **主题里带租户和站点**：没有采用。主题会变成第二个、可以伪造的范围来源，还得来回核对；网关换站点时主题也要变。

## 结果

- `contracts/mqtt/*`、模拟器和遥测的数据来源约定一起改；旧的 `energy/v1` 主题直接删掉，不和新主题并存。
- 遥测只信任设备接入服务这一个 MQTT 来源，由它把查好的租户、站点、设备和点位一起交过来（遥测在自己的数据库里，读不到设备登记的视图，见 ADR 0016）。所以按集成的数据来源绑定和遥测里的设备登记副本都没有了。
- 被盗或已单证吊销的网关证书在未于 TLS 接入端启用 CRL 且同网关仍持有效轮换证书时，在到期前仍能连上 MQTT 服务器（因为 MQTT broker 只传出 `CN={gatewayId}`）；接入层通过单证 SHA-256 校验与 CRL 导出收敛，整台网关吊销则会立刻隔离其全部上行消息。
