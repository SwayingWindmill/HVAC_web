# ADR 0015：网关的身份就是它的证书，一个设备接入服务管所有网关

状态：已采纳（2026-10-02）

参照：`docs/architecture/references/thingsboard-v4.4.md` §2、`openems-2026.9.0.md` §5、`myems-v6.9.0.md` §2

## 背景

以前每个网关要单独跑一个 `iot-service`：接入数据只能属于一个租户，进程只服务一个集成，MQTT 服务器的权限文件里按网关写死了租户和站点的编号。加第二个站点要改六个文件、重启三个进程，同一个设备的身份散在七个互不同步的地方。消息主题里带租户、站点和网关，接入程序还得拿它们和配置核对。

## 决定

- 网关在线上的身份就是它的网关证书：一张 X.509 证书，主体名（CN）是这个网关在设备登记里的设备编号。租户和站点从设备登记里查，消息里不带。
- 主题只带网关编号：上行 `hvac/v1/{gatewayId}/up/{telemetry|event|status|reply|concentrator|shadow}`，下行 `hvac/v1/{gatewayId}/down/{command|concentrator|shadow}`。
- MQTT 服务器仍用 Mosquitto，用证书认证，权限是固定的模式规则（`infra/telemetry/mqtt/acl`：`pattern write hvac/v1/%u/up/#`、`pattern read hvac/v1/%u/down/#`），每个网关只能读写自己的主题。加网关不用改 MQTT 服务器的任何配置。同时在 `mosquitto.conf` 限定单实例最大连接数（`2048`）、保活上限（`120` 秒）与单连接在途消息上限（`32`），并将 `$SYS/broker/log/N` 开放给 `mqtt-telemetry-adapter` 只读订阅，用于在代理断开客户端连接时及时刷新在线状态；`mqtt-telemetry-adapter` 与 `command-dispatcher` 分离控制回复（`up/reply`）与遥测上行读写权限，互不越界。
- 一个设备接入服务（Connectivity）服务所有租户和网关。它先把网关对应到租户，再做任何按租户的事；控制指令执行器通过租户发现视图与即时通知信号按租户加锁扫描，并拆分为独立的下发与读后核对协程池。
- 设备接入服务持有网关的签发 CA（新生成的 CA 证书同时携带签发证书与签发吊销列表用途）。运维人员给一个已登记的网关生成一个接入码；网关用接入码和证书申请（CSR）换一张 90 天的证书，之后可用现有证书无损轮换：支持通过轮换接口重发接入码并保留既有有效证书并行过渡，也可在 `/gateway/v1/renew` 提交 `revokePeerCertificate: true` 原子签发新证并立即吊销旧证。对于经前置 TLS 代理转发客户端证书的 `/gateway/v1/*` 入口（续期、反向隧道、配置影子拉取与上报），Nginx 强制校验客户端证书 CN 与 `X-Gateway-Cert-Forwarded-By`，客户端与服务端同步支持基于私钥签名的 `X-Gateway-Cert-Pop-*` 持有证明与短期防重放检查。
- 吊销由设备接入服务以单张证书指纹（`gateway_credentials.status = 'ACTIVE'`）为核心事实源执行，并在需要时导出由网关 CA 签名的 X.509 CRL（`/internal/v1/gateways/ca.crl`；配置 `GATEWAY_CRL_OUTPUT_FILE` 且 CA 支持 CRL 签名时在启动与每次吊销后原子落盘）。部署环境下还可选开启进程内 `mqttguard` mTLS 代理（`CONNECTIVITY_MQTT_GUARD_ADDR`）：网关连接 `mqttguard` 时逐条校验数据库有效凭据并由内存 CA 签发短期（60 秒）上游证书转发至内部 Mosquitto，吊销网关或单张证书时会立即关闭对应 `mqttguard` 会话，不依赖 Mosquitto 端口额外重载 CRL。
- 支持两条吊销路径：
  - **单证吊销**（`POST /api/v1/gateways/{gatewayId}/credentials/{credentialId}/revoke`）：只把指定证书标为 `REVOKED` 并记入 `CERTIFICATE_REVOKED` 审计，不封禁 `gateway_enrollments.revoked_at` 也不影响同一网关下其余仍在有效期内的轮换证书；设备接入服务按被吊销证书的 SHA-256 指纹精准踢下线仍使用该旧证书的反向运维隧道（以及启用 `mqttguard` 时的对应会话），并在续期与隧道握手入口立即拒绝该旧证（新证书上的活跃连接保持不断线），同时更新签名的 X.509 CRL 供 TLS 终止点吊销校验（若网关直接连接未配置 CRL 的 MQTT broker 端口且同网关仍持有另一张未过期有效证书，旧证书连接 MQTT 仍由 broker 仅校验 CA 与 `CN={gatewayId}`）。
  - **整台网关吊销**（`POST /api/v1/gateways/{gatewayId}/revoke`）：把该网关所有活跃证书和未使用的接入码一起作废并写入 `gateway_enrollments.revoked_at`，使该网关上行遥测与指令路由立即全部隔离；之后普通接入码接口（`enrollment-code`）与续期接口继续拒绝（`409 GATEWAY_REVOKED`）。只有在管理员显式调用轮换重启用接口（`POST /api/v1/gateways/{gatewayId}/rotate`）时，才会在记入 `IDENTITY_REENABLED` 审计后清零 `revoked_at` 并签发新接入码，所有历史已吊销证书仍保持 `REVOKED`。
- 配置影子与协议演进约定：期望配置更新支持通过内部接口与设备模板绑定出站投递幂等同步，并以 `CHANGE_BATCH`（`hvac/v1/{gatewayId}/down/shadow`）通知边缘侧拉取或内联应用后通过 `CHANGE_ACK`（`hvac/v1/{gatewayId}/up/shadow`）确认结果；上行 JSON v2 协议对 `2.0` 保持未知字段严格拦截，对 `2.1` 及以上小版本保留头字段、数值精度与尾部结束校验并放行前向扩展字段。
- 直接删掉、不做替代的：集成实例和传输配置、接入会话、指令发送的归属租约、按网关的静态绑定文件和环境变量列表、早期独立的边缘版本发布与手动快照设计（现已收敛到配置影子下发与模板激活链路）。

## 考虑过的做法

- **让 MQTT 服务器查数据库做认证**（Mosquitto 动态安全、go-auth 插件、EMQX 回调）：没有采用。证书已经证明了身份，模式规则已经把每个网关限制在自己的主题里，插件只多一个会出错的环节，不多任何保证。
- **在设备接入服务里内嵌一个 Go 写的 MQTT 服务器**：暂不采用。权限检查会变成进程内调用，但要拿成熟服务器的会话和持久化能力去换我们自己写的。
- **主题里带租户和站点**：没有采用。主题会变成第二个、可以伪造的范围来源，还得来回核对；网关换站点时主题也要变。

## 结果

- `contracts/mqtt/*`、模拟器和遥测的数据来源约定一起改；旧的 `energy/v1` 主题直接删掉，不和新主题并存。
- 遥测只信任设备接入服务这一个 MQTT 来源，由它把查好的租户、站点、设备和点位一起交过来（遥测在自己的数据库里，读不到设备登记的视图，见 ADR 0016）。所以按集成的数据来源绑定和遥测里的设备登记副本都没有了。
- 被盗或已单证吊销的网关证书在未于 TLS 接入端启用 CRL 且同网关仍持有效轮换证书时，在到期前仍能连上 MQTT 服务器（因为 MQTT broker 只传出 `CN={gatewayId}`）；接入层通过单证 SHA-256 校验与 CRL 导出收敛，整台网关吊销则会立刻隔离其全部上行消息。
