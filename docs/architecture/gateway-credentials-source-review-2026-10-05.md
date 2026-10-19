# 网关证书：参照项目的源码对比（#407）

2026-10-05。范围：接入码 + 证书申请（CSR）、90 天证书、用现有证书续期（双向 TLS）、吊销网关、审计。依据：[#407](https://github.com/SwayingWindmill/HVAC_web/issues/407)、`GLOSSARY.md`、ADR 0015 和 ADR 0016。这是源码对比，不是实现或运行验证；上游的测试只读了，没有跑。

## 参照的版本

用 `git rev-parse HEAD` 核对过的只读浅克隆：

| 项目 | 官方标签 | 提交 |
| --- | --- | --- |
| ThingsBoard | `v4.4` | `6d46786579c8b29caf5102f95ddb133674bed68b` |
| OpenEMS | `2026.9.0` | `14f0dedf5fe279845281bafaf0e48ea0ab51333a` |
| MyEMS | `v6.9.0` | `b360f5bb4c2be4fd15854057963531b2e8d1bc0a` |

没有复制任何上游代码。ThingsBoard 根目录的 [LICENSE](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/LICENSE) 是 BUSL-1.1，各文件的许可头不一致，只当设计参考；OpenEMS 的 [README](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/README.md#license) 分 EPL-2.0 和 AGPL-3.0 两部分；MyEMS 的 [LICENSE](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/LICENSE) 是 MIT。这些都不代表可以不逐个文件审查就复制。

## ThingsBoard：开通设备和校验证书

读了 [DeviceProvisionServiceImpl.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/service/device/DeviceProvisionServiceImpl.java)（`provisionDevice`、`processProvision`、`provisionDeviceViaX509Chain`、`notify`、`logAction`）、[MqttTransportHandler.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/common/transport/mqtt/src/main/java/org/thingsboard/server/transport/mqtt/MqttTransportHandler.java)（`processConnect`、`processX509CertConnect`），测试 [DeviceProvisionServiceTest.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/test/java/org/thingsboard/server/service/device/provision/DeviceProvisionServiceTest.java)、[MqttProvisionJsonDeviceTest.java](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/test/java/org/thingsboard/server/transport/mqtt/mqttv3/provision/MqttProvisionJsonDeviceTest.java)，以及 2026-10-05 的官方文档（[MQTT 开通](https://thingsboard.io/docs/reference/mqtt-api/provisioning/)、[开通方式](https://thingsboard.io/docs/paas/user-guide/provisioning/)）作为补充。

实际做法：预先登记模式检查设备配置的共享密钥和设备归属，再检查并写一个服务端属性 `provisionState`。X509 证书链模式从证书主体名（CN）推出设备名，在这个配置的租户里查设备，可以更新它登记的证书。MQTT 证书认证检查证书有效期（除非明确配置跳过），对实际的证书算摘要，再校验这张证书。开通成功或失败都写审计并发到规则引擎。

- **采用**：身份来自认证过的证书；做任何业务前先把证书对应到租户；要检查证书有效期和具体是哪一张证书，不只看 CN；开通失败要明确报出来，证书的每次变化都留审计。
- **改造后采用**：只有已经登记的网关能开通。把按设备配置共享的开通密钥换成每个网关一个、会过期、只能用一次的接入码；CSR 由设备接入服务的 CA 签。在 PostgreSQL 里保证「接入码只能用一次」和「签发 + 审计」在同一个事务里完成。ThingsBoard「先读属性再写属性」的做法证明不了并发请求下只用一次。
- **不采用**：自动创建设备、用正则从 CN 推业务名称、整批设备共享开通密钥、引入它内嵌的传输和队列架构，这些都和设备登记的归属、ADR 0015 和 #407 冲突。ThingsBoard 是登记设备自己带来的证书，不是 CA 用接入码和 CSR 签 90 天证书的参照；它的 X509 更新会替换旧证书，而 #407 要求新旧证书在旧证书到期前并存。

## OpenEMS：连接时把凭据对应到边缘设备

读了 [WebsocketServer.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.backend.edge.application/src/io/openems/backend/edge/server/WebsocketServer.java)（握手时取 API 密钥、认证、查不到对应就拒绝、把查到的边缘设备编号挂在连接上）、[Cache.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.backend.edge.application/src/io/openems/backend/edge/application/Cache.java)、[ControllerApiBackendImpl.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/ControllerApiBackendImpl.java)（API 密钥放在 HTTP 头里），以及 [metadata.adoc](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/doc/modules/ROOT/pages/backend/metadata.adoc)、[architecture.adoc](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/doc/modules/ROOT/pages/backend/architecture.adoc)、[控制器说明](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.controller.api.backend/readme.adoc)。附近的测试（[MetadataUtilsTest.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.backend.common/test/io/openems/backend/common/metadata/MetadataUtilsTest.java)、[CredentialsTest.java](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.backend.metadata.odoo/test/io/openems/backend/metadata/odoo/postgres/CredentialsTest.java)）都不涉及证书开通、续期或立即吊销。

- **采用**：由负责方认证，把传输层的凭据对应到一个不会变的网关身份。
- **改造后采用**：用设备登记的只读视图和 X509 证书，不用 API 密钥加缓存。
- **不采用**：再加一份身份缓存或副本、用 API 密钥传输。OpenEMS 这部分不是接入码 / CSR 开通或 90 天续期的参照；只在连接时对应一次，也证明不了凭据变了以后已经建立的连接会被马上隔离。

## MyEMS：固定的网关令牌和共享数据库

读了 [core/gateway.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/core/gateway.py)（增删改网关时生成 UUID 令牌，保存后在网关接口里原样返回）、[modbus 采集的 main.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-modbus-tcp/main.py)（用网关编号和令牌写在 SQL 条件里选数据源，启动时加载配置）、[gateway.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-modbus-tcp/gateway.py)（心跳更新共享数据库里的时间）、[说明](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-modbus-tcp/README.md) 和 [test_mqtt.py](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/test_mqtt.py)（手动发 MQTT 的脚本，用配好的用户名和密码，不是凭据生命周期的测试）。读过的网关和采集代码里没有开通、续期或吊销的测试。

- **采用**：以明确登记的网关作为配置和采集的范围。
- **不采用**：长期可读的网关令牌、靠共享数据库认证采集程序、手动安装凭据。读过的 MyEMS 代码没有实现 #407 的证书生命周期（只针对读过的这些代码，不是说它所有的集成或商业版本都没有）。

## 我们自己的做法和安全上的限制

1. **接入码 + CSR**：接入码明文只在第一次返回给运维人员时出现，不保存、不写审计；只存它的加密摘要、24 小时的有效期和是否已用。随机数用 Go 的 `crypto/rand`，解析和签名用标准库 `crypto/x509`，不引入新的 CA 框架。校验 CSR 的签名，证明网关确实持有私钥；签出的证书主体和扩展由服务端决定：CN 是登记的网关编号，只能用于客户端认证，永远不是 CA，也不接受调用方自带的 SAN 或主体扩展。
2. **原子性**：同一个网关的开通、续期和吊销排队执行。签发时证书状态和审计一起提交；两个请求抢同一个接入码，只能成功一个。CSR 校验或签名失败时，接入码不能变成「已用」却没有证书。吊销时也作废还没用的接入码，吊销前生成的码不能在吊销后恢复访问。
3. **续期**：校验双向 TLS 的证书链、当前有效期、网关 CN，以及出示的这张证书的指纹确实对应一条有效的已存证书。只信任 nginx 在受保护的后端边界上认证过的证书信息，不接受可以伪造的公开证书请求头。新证书和旧证书并存，直到旧证书原定的到期时间（#407 的要求）。如果只检查「这个网关有一张有效证书」，一张被吊销的证书就能借另一张证书的状态续期。
4. **吊销整个网关**：当时 `modules/connectivity/pkg/connectivity/uplink.go` 和 `command_route.go` 只检查「这个网关有没有任何一张有效、没过期的证书」，所以只吊销最新的证书时，续期前那张旧证书还有效，吊销等于没生效。在采用的「Mosquitto + 不维护吊销列表」方案下：一次吊销这个网关所有有效的证书，作废还没用的接入码，续期时拒绝这些证书的指纹；上行和下发指令每次都重新读最新状态，不缓存「已接受」。测试要覆盖新旧证书并存、吊销和续期同时发生的情况。
5. **吊销是永久的**：吊销后不能再生成接入码，也不能续期。网关被攻破，就登记一个新的网关编号，重新绑定数据来源。这层意思写进 ADR 0015 和运维页面；#407 没有要求恢复一个被吊销的身份。每个网关一行开通状态，在生成接入码、开通、续期、吊销时都锁住这一行就够了，不需要单独的吊销子系统。这也解决了下面说的「旧证书被重新启用」的问题。
6. **传输层的限制**：设备接入服务从 Mosquitto 收到的是主题和消息内容，拿不到发消息的客户端用的是哪张证书。所以这个设计做到的是「隔离整个网关」，不是「吊销某一张 MQTT 证书」。如果给一个被吊销的网关再签新证书，「有任何一张有效证书」的检查又会接受持有旧证书（MQTT 服务器仍认为有效）的人发来的消息，他通过已有的订阅还能收到指令；永久吊销堵住了这条路。正常续期、新旧并存时，已经建立的旧 MQTT 连接在旧证书到期时会不会断开，读过的程序逻辑证明不了。所以不声称能按单张证书让 MQTT 过期、吊销或断开；要做到这一点，需要另外审查过的传输层身份和认证设计，ADR 0015 不允许悄悄加 MQTT 服务器插件。
7. **审计**：记下网关、谁、做了什么、时间、结果和证书的变化；不记接入码明文、私钥、请求的授权头或完整的 CSR。尽量用现有的审计存储和发件约定，不另建审计管道或门禁检查。

## 结论

### 审计放在哪

看了 `modules/audit/internal/audit/store.go`、`consumer.go`，以及 `libs/sessionevent`、`libs/operationsauditevent` 的约定。中央审计只接收登录会话和运维业务事件，没有通用的「凭据事件」。所以借用它们「和业务一起在事务里写」的原则：`connectivity.gateway_credential_audit` 是设备接入服务自己的、只能追加的记录，和成功的生成接入码、签发、续期、吊销一起提交。谁、哪个网关、什么时间标识一次操作；有这一行就代表成功。运行时不能改或删这些行；不存接入码或密钥，也不另建投递管道。这些记录在设备接入服务的数据库里能查，**不显示在中央审计页面上**。没有为 #407 发明一个中央发布约定。

### 网页入口

当时在浏览器里看，`/system?tab=registry` 用不了，因为平台的一个路由登记的是受保护的站点草稿。#407 用 `/settings/integrations`：真实的设备登记网关清单、按权限选站点、登记和证书对话框。假的连接器清单和统计删掉了。站点选择是路由的查询参数，登记表单用 RHF/Zod，接口用生成的客户端，证书数据归 Query 管。第一次返回的接入码是 #407 明确允许的例外；私钥和内部编号不出现在运维页面上。

按现有的 PostgreSQL + Go X509 + Mosquitto/nginx 边界做 #407，HTTP 续期时校验具体是哪一张证书。三个参照项目支持「身份来自传输层凭据、由负责方对应、明确记录生命周期」；一次性接入码、本地 CA 的有效期、新旧证书并存和隔离整个网关是我们自己的改造。MQTT 恢复上的限制和验收结果记在一起。这次对比不需要新依赖、不复制上游代码，也没有跑任何平台。
