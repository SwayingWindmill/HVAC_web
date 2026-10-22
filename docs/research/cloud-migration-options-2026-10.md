# 换云和「数据与服务分开」调研（2026-10）

- 日期：2026-10-22。
- 对照的代码：`e67e5af1772d878bd741f990c8a581ef6f034b48`（分支 `docs/gap-review-decisions`，和 main 一致）。下面写 `路径:行号` 的都指这个提交。
- 价格查询日期：2026-10-22，全部是官网刊例价或官网计算器在未登录状态下显示的价格，地域选华南（广州）。实际下单价、折扣、代金券以各家下单页为准。
- 阿里云 ECS 和云盘的价格是用户登录后，在 ECS 购买页（华南3 广州、包年包月）上看到的刊例价（第 5 节）；没有下单。
- 没能查到官方来源的：阿里云的 RDS、Redis、ClickHouse 价格；华为云对象存储 OBS 单价和 S3 兼容说明（计算器不随选项更新、价格页 404）；火山引擎托管 ClickHouse 是否存在及价格（价格计算器搜不到）；各家托管 Redis 的价格（腾讯定价中心要交互、华为计算器的价格不随选项变化）；阿里云、华为云、火山引擎托管 MQTT 能否用自己的 CA；小程序「长期订阅消息」现在的开放范围原文（见第 7 节）。

## 1. 结论

**推荐：用方案 B（应用一台云服务器 + 数据一台云服务器、数据盘单独挂载 + 对象存储放备份），不用容器服务（第 9 节）。** 选哪家要看新域名：

- 如果一个月内要迁、继续用 `swayingwindmill.online`：留在腾讯云，理由见下面第 1 条。
- 如果迁移时换成带 quanlaihe 的企业域名（`swayingwindmill.online` 是个人备案，新域名本来就要用企业主体重新备案）：备案的工作量在哪家都一样，「留在腾讯云」最大的理由就没有了。这时阿里云的方案 B 约 624 元/月，比腾讯云同配置（两台 4 核 8 GB）的约 766 元/月便宜约 140 元（第 5 节）。

理由：

1. **一个月内、不换域名的话，能落地的只有「留在腾讯云」。** 换到别家要先在那家做接入备案：腾讯云写的是初审 1–2 个工作日、管局「通常在 20 个工作日内」审核（[腾讯云接入备案](https://cloud.tencent.com/document/product/243/19024)），阿里云也要求审核通过后才能把域名解析过去（[阿里云接入备案 FAQ](https://help.aliyun.com/zh/icp-filing/support/access-for-the-record-the-faq)）。一个月大约 22 个工作日，备案就把时间吃完了，迁移本身只能排在下个月。
2. **方案 B 便宜一半以上，而且不用改代码。** 腾讯云方案 B 约 690 元/月，方案 A 约 1,550 元/月（第 5 节）。最贵的是托管 ClickHouse：腾讯云最小一个节点 4 核 16 GB 就要 701 元/月，华为云最小一个节点 1,710 元/月。
3. **方案 A 有两个没验证过的坑。** 托管 PostgreSQL 不给 superuser（[腾讯云权限说明](https://cloud.tencent.com/document/product/409/61550)），而我们的迁移脚本里有 78 处 `SET ROLE`、15 处 `OWNER TO`（第 3 节），能不能在托管库上跑通要先实测；托管 ClickHouse 要给 12 个只读/写入账号加密码，仓库里的初始化 SQL 用的是无密码账号，不能原样用（`deploy/platform/phase1/README.md:68`）。
4. 第一阶段不要求高可用，方案 B 的「坏了几小时内恢复」靠两件事：数据盘和系统盘分开（磁盘满不会再拖垮应用），每晚备份进 COS（整台服务器丢了也能恢复）。
5. Mosquitto 和 Redis 都留在应用服务器上自建：Redis 不落盘（`deploy/platform/phase1/compose.yaml:964`），丢了能重建；托管 MQTT 要改网关证书的签发流程，只有一个网关不值得。

以后真要用托管数据库，先把 PostgreSQL 换成腾讯云托管版（2 核 4 GB 408 元/月），ClickHouse 继续自建。这一步可以等迁移稳定后单独做。

**微信消息：** 普通的小程序长期订阅，企业主体做暖通运维告警基本拿不到（官方现在只对服务号写明「长期订阅通知仅向政务民生、医疗等公共服务领域开放」，小程序页面已经不写范围，见第 7 节）。值班人员都是自己公司的人，**推荐用企业微信的应用消息**：不限次数、不用每次点「允许」，每个应用对同一个人每分钟最多 30 条。小程序的「设备消息」是一种长期订阅，企业认证后可以申请，但模板的关键词是微信定好的固定选项，只适合做补充。

## 2. 一个月内的迁移步骤和停机时间

前提：留在腾讯云广州、同一个账号。只换服务器、不换接入商和域名的备案主体，不需要重新备案（这一点按腾讯云文档的「新增/接入服务」规则推断，没有找到逐字写明的官方说明，开工前在备案控制台确认）。

| 时间 | 做什么 | 会影响谁 |
| --- | --- | --- |
| 第 1 周 | 在现在这台服务器上配 restic，每晚把备份传到 COS（第 6 节）；量一次 PostgreSQL 和 ClickHouse 各库的实际大小 | 不影响使用 |
| 第 1 周 | 给 Mosquitto 签一张带域名的服务器证书（比如 `mqtt.hvac.swayingwindmill.online`，DNS 的 TTL 调到 60 秒），远程把松雅轩 Node-RED 的连接地址从 IP 改成这个域名 | 网关重连一次，读数先存在网关的发件箱里，连上后补发 |
| 第 2 周 | 买两台新服务器（应用、数据）和一块数据盘，装 Docker；数据服务器只开内网 | 不影响使用 |
| 第 2–3 周 | 在新服务器上用前一晚的 COS 备份完整恢复一遍，跑表结构预检和网页验收；记下恢复用了多久 | 不影响使用。这一步同时就是第一次真的恢复演练 |
| 第 3 周 | 正式切换（见下面） | 网页和小程序停 2–4 小时 |
| 第 4 周 | 观察一周；旧服务器关机保留到月底再退 | 不影响使用 |

正式切换那天：

1. 停旧服务器上写数据的服务，网关的读数这时会存在 Node-RED 的持久发件箱里（`docs/specs/site-gateway-onboarding.md:34`）。
2. 做最后一次 PostgreSQL 基础备份和 ClickHouse 备份，经内网拷到数据服务器，恢复。
3. 改 DNS：`hvac.swayingwindmill.online` 和 MQTT 域名指向新应用服务器。
4. 在新应用服务器上启动全部服务，网关自动重连，发件箱里的读数补发进来。
5. 微信公众平台「开发设置 → IP 白名单」换成新服务器的公网 IP，否则微信登录报 40164（`docs/specs/miniprogram-wechat.md:113`）。
6. 腾讯云安全组放行 443 和 8884（`docs/specs/site-gateway-onboarding.md:25`）。

停机多久主要看拷数据。现在的夜间备份 7 份共 6.5 GB（`docs/HANDOFF.md:54`），一份约 1 GB；同一地域的内网拷几分钟就够，大头是恢复 ClickHouse 和启动、验收，估 2–4 小时。第 2–3 周的演练会给出实测时间。

几个要注意的地方：

- **网关现在按 IP 连。** Mosquitto 的服务器证书只有 `mqtt-broker` 这个名字，Node-RED 用连接地址覆盖校验名，所以网关按 IP `139.199.58.47` 连（`docs/specs/site-gateway-onboarding.md:62`）。不先改成域名，换服务器那天就得同时改现场网关的配置。网关的客户端证书是平台自己的 CA 签的，2027-01-12 到期（`docs/HANDOFF.md:58`），把 CA 拷到新服务器就继续有效，不用重签。
- **Let's Encrypt 证书**：域名解析切过去之后在新服务器上重新签，或者先把旧证书拷过去用到续期。
- 小程序的 request 合法域名不变（`docs/specs/miniprogram-wechat.md:115`），不用改。
- 如果现在这台是轻量应用服务器而不是 CVM，它和新 CVM 之间默认不通内网，拷数据要走公网或先开内网互联（未核实现在是哪一种）。

## 3. 我们实际用了哪些有状态组件

| 组件 | 现在的版本和用法 | 数据量 | 换成托管服务必须满足 |
| --- | --- | --- | --- |
| PostgreSQL | `postgres:16.4`（`compose.yaml:793`），8 个库：`hvac_identity`、`hvac_s0`–`hvac_s5`、`hvac_operations_agent`（`deploy/platform/phase1/postgres/init/001-create-phase1-databases.sql:1-23`） | `source_observations` 5.4 GB，每小时删 7 天前的原始读数后能腾出约 3.5 GB（`docs/HANDOFF.md:53-54`）；整库大小没有记录 | 大版本 16；能建很多 `NOINHERIT NOBYPASSRLS` 的角色（`deploy/platform/phase1/migrations/bootstrap-roles.sql:5-36`）；行级安全（59 个 SQL 文件 `ENABLE ROW LEVEL SECURITY`，67 个 `CREATE POLICY`）；管理账号能 `SET ROLE` 到各模块的迁移角色（78 处）和 `OWNER TO`（15 处）、建 `SECURITY DEFINER` 函数（19 处）。**没有用任何扩展**：在 infra、modules、services、deploy、cmd、libs 里 grep `CREATE EXTENSION` 为 0；`gen_random_uuid()` 是内置函数 |
| ClickHouse | `clickhouse-server:26.3.39.7`（`compose.yaml:896`），两个库 `telemetry_history`、`analytics`；引擎只有 MergeTree、ReplacingMergeTree、AggregatingMergeTree，2 个物化视图（`infra/telemetry/clickhouse/init/`） | 没有记录总量；`system.query_log` 0.8 GB（`docs/HANDOFF.md:54`） | 版本不能比 26.3 太旧（我们没在旧版本上测过）；能 `CREATE USER` 和 `GRANT`，并且这些账号要改成有密码（`infra/telemetry/clickhouse/init/002-analytics-energy-interval.sql:59-63` 是 `no_password`；`deploy/platform/phase1/README.md:68`）；Cube 要能直连 8123 端口（`compose.yaml:918-945`） |
| Redis | `redis:7.4.2`，不落盘（`--appendonly no --save ""`，`compose.yaml:964`）；用 DB 0/1/2 分开实时推送、登录状态、限流（`deploy/platform/phase1/README.md:69`） | 只有内存里的临时数据 | 要支持多个 DB 编号（集群版通常不支持）。**数据丢了能重建，不算真正的有状态组件** |
| MQTT | Mosquitto 2.1.2（`compose.yaml:980`），只开 TLS 1.3、必须有客户端证书、证书里的名字当用户名、按 ACL 限主题（`infra/telemetry/mqtt/mosquitto.conf:1-12`）；主机 nginx 在 8884 四层转发（`docs/specs/site-gateway-onboarding.md:25`） | 持久化队列，很小 | 用我们自己的 CA 校验客户端证书；证书名当身份；按网关限主题；网关证书由平台签发和续期（`/gateway/v1/renew`，`docs/specs/site-gateway-onboarding.md:63`） |
| 备份 | 每晚 03:30 一份 PostgreSQL 基础备份（tar + gzip）和一份 ClickHouse 备份，各留 7 份，放在同一块盘上（`deploy/platform/phase1/backup/README.md:33-47`，`deploy/platform/phase1/backup/postgres-base-backup.sh:8-17`） | 6.5 GB（`docs/HANDOFF.md:54`） | 要拷到服务器以外；WAL 归档现在没开（`compose.yaml:800` 默认 `off`） |
| 对象存储 | 仓库按「外部 S3 兼容存储」设计，要求备份桶开版本和不可改（`deploy/platform/phase1/object-storage.external.v1.json:5-31`），还没有接 | 0 | S3 兼容；最好支持对象锁定 |
| 监控 | Prometheus（保留 15 天，`compose.yaml:1041`）、Loki、Grafana、Alertmanager，各用一个 Docker 卷 | 没有记录 | 都能重建，跟应用放在一起即可，不需要托管 |

另外两点：

- 仓库已经支持把这三种数据库单独放到外面：`PHASE1_POSTGRES_MODE=external`、`PHASE1_CLICKHOUSE_MODE=external`、`PHASE1_REDIS_MODE=external`（`deploy/platform/phase1/README.md:54-71`）。方案 B 的数据服务器对应用来说就是「外部数据库」，用的是同一套开关，不用改代码。
- 资源：服务器现在跑的 `single-lite` 档位，档位建议 8 核 16 GB（`deploy/platform/phase1/deployment-tiers.v1.json:108-118`），实际只有 2 核 3.6 GB。这一档给 PostgreSQL 1.5 GB、ClickHouse 2 GB 内存上限（同文件 `:121-142`）。所以数据服务器至少要 8 GB 内存，应用服务器 4 核 8 GB 比现在宽裕得多。

## 4. 四家逐项对比

### 托管 PostgreSQL

| 云 | 版本 | 我们用不用得上 | 备份和按时间点恢复 | 最小规格价格（广州，包月） |
| --- | --- | --- | --- | --- |
| 腾讯云 | 14–18（[版本列表](https://cloud.tencent.com/document/product/409/67017)） | 不给 superuser，给 `tencentdb_superuser` 角色（[权限说明](https://cloud.tencent.com/document/product/409/61550)），迁移脚本要实测 | 每天全量 + 实时日志备份，保留 7–1830 天（[备份](https://cloud.tencent.com/document/product/409/33945)）；可克隆到日志保留期内任意时间点（[克隆实例](https://cloud.tencent.com/document/product/409/68277)） | 1 核 2 GB 204 元，2 核 4 GB 408 元；硬盘 0.72 元/GB/月（[定价](https://buy.cloud.tencent.com/price/PostgreSQL/overview?region=ap-guangzhou)，[计费示例](https://cloud.tencent.com/document/product/409/4993)） |
| 阿里云 | 列到 18（[插件列表](https://help.aliyun.com/zh/rds/apsaradb-rds-for-postgresql/extensions-supported-by-apsaradb-rds-for-postgresql)） | 未核实管理账号权限 | 未查到官方页 | 未查到（[定价页](https://rdsbuy.console.aliyun.com/pricing)要登录） |
| 华为云 | 14–18（[计算器](https://www.huaweicloud.com/pricing/calculator.html#/rds)） | 未核实管理账号权限 | 送和存储一样大的备份空间（计算器页的说明）；按时间点恢复未查 | 单机 2 核 4 GB + 40 GB SSD 196 元；主备 470 元 |
| 火山引擎 | 11–17，2026-10-15 新增 18（[价格计算器](https://www.volcengine.com/pricing?product=RDS%20for%20PostgreSQL&tab=2)，[发布记录](https://www.volcengine.com/docs/6438/71941)） | 未核实 | 未查到 | 高可用 1 核 2 GB ×2 + 200 GiB 本地 SSD 360 元（未登录估价） |

我们没用扩展，「扩展能不能装」这一项四家都不构成障碍。真正要验的是第 3 节说的 `SET ROLE` 和 `OWNER TO`：PostgreSQL 16 起，非 superuser 建的角色默认不能直接 `SET ROLE` 过去，要先给自己授权。这只能在一台托管实例上跑一遍 `phase1-migrator` 才知道。

### 托管 ClickHouse

| 云 | 有没有 | 版本 | 最小规格价格（广州） |
| --- | --- | --- | --- |
| 腾讯云 | 有，TCHouse-C，写明「内核完全兼容」社区版（[FAQ](https://cloud.tencent.com/document/product/1299/47758)） | 未查到版本号 | 标准型 4 核 16 GB 一个节点 701 元/月；高性能云硬盘 0.35 元/GB/月；备份存到 COS 0.00018 元/GB/小时（[计费](https://cloud.tencent.com/document/product/1299/47757)）。高可用要另买 ZooKeeper 节点 |
| 阿里云 | 有，企业版和社区兼容版（[产品简介](https://help.aliyun.com/zh/clickhouse/product-overview/what-is-apsaradb-for-clickhouse)） | 未查 | 未查到（要登录） |
| 华为云 | 有，在表格存储服务 CloudTable 里 | 未查 | 不开高可用、1 个节点 8 核 32 GB + 500 GB SSD：1,710 元/月（[计算器](https://www.huaweicloud.com/pricing/calculator.html#/cloudtable)） |
| 火山引擎 | 价格计算器里搜不到 ClickHouse | — | 未查到 |

### 托管 Redis

我们的 Redis 不落盘，留在应用服务器上即可，两套方案都没算托管 Redis。各家价格：腾讯云文档只说去定价中心查（[定价中心说明](https://cloud.tencent.com/document/product/239/9894)），华为云计算器有 0.125 GB 起的单机和主备版本（[计算器](https://www.huaweicloud.com/pricing/calculator.html#/redis)）但价格不随选项变化，都没取到可信的数字。

### MQTT

| 云 | 托管 MQTT 能不能用我们自己的 CA | 建议 |
| --- | --- | --- |
| 腾讯云 | 能。消息队列 MQTT 版支持双向认证和「一机一证」：每台设备用自己签的 CA 签发的证书，证书直接当身份（[配置认证](https://cloud.tencent.com/document/product/1778/114813)）。价格只写「以售卖页为准」，基础版已停售（[计费说明](https://cloud.tencent.com/document/product/1778/109698)） | 继续自建 |
| 阿里云、华为云、火山引擎 | 未查到官方说明 | 继续自建 |

继续自建 Mosquitto 的理由：只有一个网关、流量很小；网关证书由平台自己签发和续期，换托管就要把证书登记和主题权限改成那家的做法；Mosquitto 只占 256 MB 内存（`compose.yaml:991-993`）。

### 对象存储（放备份）

| 云 | 标准存储 | 低频存储 | 上传 / 下载流量 | S3 兼容（restic 能直接用） |
| --- | --- | --- | --- | --- |
| 腾讯云 COS | 0.118 元/GB/月 | 0.08 | 外网下载 0.5 元/GB；价格页没有列上传收费项（[定价](https://buy.cloud.tencent.com/price/cos)） | 是（[用 S3 SDK 访问 COS](https://cloud.tencent.com/document/product/436/37421)） |
| 阿里云 OSS | 0.12 | 0.08 | 上传免费；同地域内网下载免费；外网下载 0.25（0–8 点）/ 0.50 元/GB（[定价](https://www.aliyun.com/price/detail/oss)） | 未在官方页核实 |
| 华为云 OBS | 未查到 | 未查到 | 未查到 | 未查到官方页 |
| 火山引擎 TOS | 0.099 元/GiB/月 | 0.08 | 上传和内网流量免费；外网下载 0.5 元/GB（[定价](https://www.volcengine.com/pricing?product=TOS&tab=1)） | 未在官方页核实 |

restic 原生支持 S3 协议，腾讯云 COS 官方写明兼容 S3 API。低频存储有最短存储时间和取回费，备份只留 4 周，按标准存储算更省事。

### 云服务器（放应用）

| 云 | 4 核 8 GB 价格（广州，包月） | 不含 | 来源 |
| --- | --- | --- | --- |
| 腾讯云 | S5.LARGE8：包月 320.16 元（标准月价 368），按量 0.66 元/小时；2 核 8 GB 的 S5.MEDIUM8 包月 240.12 元 | 系统盘、数据盘、带宽 | [CVM 定价](https://buy.cloud.tencent.com/price/cvm/overview)（广州三区） |
| 腾讯云云硬盘 | 通用型 SSD 0.5 元/GiB/月，高性能 0.35 | — | [云硬盘价格](https://cloud.tencent.com/document/product/362/2413) |
| 腾讯云带宽 | 包月 ≤2 Mbps 20 元/Mbps，2–5 Mbps 25 元/Mbps；按流量 0.8 元/GB | — | CVM 定价页「宽带价格」 |
| 华为云 | S6 4 核 8 GB + 40 GB 通用型 SSD + 5 Mbps 全动态 BGP：447.30 元 | 已含盘和带宽 | [计算器](https://www.huaweicloud.com/pricing/calculator.html#/ecs) |
| 阿里云 | 未查到（价格页是旧机型表，计算器要登录） | — | — |
| 火山引擎 | 未查 | — | — |

### 备案

| 情况 | 要不要备案 | 多久 | 期间域名能不能用 |
| --- | --- | --- | --- |
| 留在腾讯云、换服务器 | 不用重新备案（推断，开工前在备案控制台确认） | — | 能 |
| 换到阿里云 / 华为云 / 火山引擎 | 要在新的那家做「接入备案」，备案主体必须和现在一致（[腾讯云接入备案](https://cloud.tencent.com/document/product/243/19024)） | 初审 1–2 个工作日，管局通常 20 个工作日内（腾讯云的说法）；阿里云要求先在它那里买够格的服务器（[FAQ](https://help.aliyun.com/zh/icp-filing/support/access-for-the-record-the-faq)） | 能：多个接入商可以同时存在，审核期间域名继续指向旧服务器，审核通过后再改解析 |
| 主体从个人换成企业 | 接入备案不能换主体（同上）。如果现在的备案是个人主体，换成企业要另走变更或新备案流程 | 未查 | 未查 |

留在腾讯云的另一个好处：服务器在同一家，备案、域名解析、证书都不用动，换服务器这件事和「小程序改企业主体」可以分开做，互不卡。微信生态这边，小程序的服务器域名和 IP 白名单不管换不换云都要按第 2 节改，没有看到「必须用腾讯云」的官方要求。

## 5. 两套方案的月费估算（广州，包月，2026-10-22）

方案 B（推荐）：应用一台、数据一台、备份进 COS。

| 项 | 规格 | 元/月 |
| --- | --- | --- |
| 应用服务器 | S5.LARGE8，4 核 8 GB | 320.16 |
| 数据服务器 | S5.MEDIUM8，2 核 8 GB，只开内网 | 240.12 |
| 系统盘 | 两台各 50 GB 通用型 SSD | 50 |
| 数据盘 | 100 GB 通用型 SSD，挂在数据服务器 | 50 |
| 公网带宽 | 应用服务器按流量，假设每月 30 GB | 约 24 |
| COS 备份 | 约 12 GB 标准存储（第 6 节） | 约 1.5 |
| 合计 | | **约 686** |

数据服务器换成 4 核 8 GB 多 80 元，约 766 元/月。

方案 A：应用一台、数据全用托管。

| 项 | 规格 | 元/月 |
| --- | --- | --- |
| 应用服务器 | S5.LARGE8 + 50 GB 系统盘 + 按流量带宽 | 约 369 |
| 托管 PostgreSQL | 2 核 4 GB + 50 GB | 408 + 36 = 444 |
| 托管 ClickHouse | TCHouse-C 标准型 4 核 16 GB 一个节点 + 100 GB 高性能云硬盘，不含 ZooKeeper | 701 + 35 = 736 |
| Redis、MQTT | 留在应用服务器 | 0 |
| 备份 | 托管服务自带（PostgreSQL 送等量备份空间；ClickHouse 备份按量另算） | 约 0–5 |
| 合计 | | **约 1,550** |

PostgreSQL 换成 1 核 2 GB（204 元）可以降到约 1,330 元/月，但我们这一档给 PostgreSQL 的内存上限是 1.5 GB，1 核 2 GB 偏紧。

其他几家：华为云方案 A 约 447 + 196（单机 PostgreSQL）+ 1,710（ClickHouse）≈ 2,350 元/月；火山引擎因为缺价格算不全。

阿里云方案 B（用户登录后在 [ECS 购买页](https://ecs-buy.aliyun.com/ecs#/custom/prepay/cn-guangzhou) 看到的华南3 广州包月刊例价，2026-10-22，没有下单）：

| 项 | 规格 | 元/月 |
| --- | --- | --- |
| 应用服务器 | 通用算力型 u2a，`ecs.u2a-c1m2.xlarge`，4 核 8 GB | 262.4 |
| 数据服务器 | 同上，只开内网 | 262.4 |
| 系统盘 | 两台各 40 GB ESSD PL0（购买页上加 40 GB 系统盘多 20 元） | 40 |
| 数据盘 | 100 GB ESSD PL0，挂在数据服务器（按系统盘的 0.5 元/GB 推算；购买页默认的 ESSD AutoPL 是 1 元/GB） | 约 50 |
| 公网带宽 | 应用服务器按流量，假设每月 30 GB；开通 CDT 后中国内地每月送 20 GB，超出按帮助文档示例的 0.8 元/GB（[公网带宽计费](https://help.aliyun.com/zh/ecs/public-bandwidth)） | 约 8 |
| OSS 备份 | 约 12 GB 标准存储（第 6 节） | 约 1.5 |
| 合计 | | **约 624** |

- u2a 是 AMD 处理器的「通用算力型」，官方写明适用中小型数据库（[通用算力型实例](https://help.aliyun.com/zh/ecs/user-guide/general-work-force)）。不要选同一页上更便宜的「经济型 e」（4 核 8 GB 216 元，活动价 159.84 元）：它是共享 CPU。
- 同一页的其他规格：计算型 c9i 4 核 8 GB 371.82 元，通用型 g9i 2 核 8 GB 238.6 元、4 核 16 GB 477.2 元。u2a 也有 1:4 的规格（2 核 8 GB），可以当数据服务器，这次没查它的价格。
- 购买页提示：中国内地用于网站的实例必须完成 ICP 备案，备案用的实例要买 3 个月及以上。

## 6. 备份的价格

### 迁移前：现在这台服务器用 restic 传到对象存储

估多大：夜间备份 7 份共 6.5 GB，一晚约 0.93 GB。PostgreSQL 基础备份是 gzip 压过的 tar（`deploy/platform/phase1/backup/postgres-base-backup.sh:13-14`），每天的压缩包整个都不一样，restic 几乎去不了重，也压不动；ClickHouse 的原生备份按数据块存文件，旧数据块每天一样，能去重。保留「7 天每日 + 4 周每周」最多 11 份，按全部不去重算上限约 11 × 0.93 ≈ 10 GB，留点余量按 12 GB 算。把 PostgreSQL 备份改成不压缩的 tar 交给 restic 压，能明显变小，这次没测。

| 云 | 存储（12 GB/月） | 每晚上传约 1 GB | 恢复时下载一晚（约 1 GB）/ 全部（12 GB） | 来源 |
| --- | --- | --- | --- | --- |
| 腾讯云 COS | 1.42 元 | COS 不收上传费（价格页没有这一项）；同地域走内网 | 外网 0.5 / 6 元；同地域内网下载价格页没有列，未核实 | [COS 定价](https://buy.cloud.tencent.com/price/cos) |
| 阿里云 OSS | 1.44 元 | 免费 | 外网 0.25–0.5 / 3–6 元；同地域 ECS 内网免费 | [OSS 定价](https://www.aliyun.com/price/detail/oss) |
| 华为云 OBS | 未查到 | 未查到 | 未查到 | — |
| 火山引擎 TOS | 1.19 元 | 免费 | 外网 0.5 / 6 元；内网免费 | [TOS 定价](https://www.volcengine.com/pricing?product=TOS&tab=1) |

请求费：restic 每晚几百到几千次请求，按 0.01 元/万次算不到 1 分钱。

容易漏的一项是**现在这台服务器的出网流量**。传到同在广州的 COS 走内网不花钱（要用内网地址）；传到别家要走公网，如果服务器是按流量计费（腾讯云 0.8 元/GB），第一次全量约 10 元，之后每晚约 1 GB、一个月约 24 元；按固定带宽计费则不另收钱，但 1 GB 在 5 Mbps 下要传半个小时左右。现在这台是哪种计费方式没查。

### 迁移后：托管数据库自带的备份

| 项 | 另收费吗 | 保留多久 | 来源 |
| --- | --- | --- | --- |
| 腾讯云 PostgreSQL | 送「购买的存储空间 × 100%」的备份空间，超出部分中国大陆 0.0008 元/GB/小时（约 0.58 元/GB/月） | 数据备份可设 7–1830 天；按时间点克隆的范围由日志保留时长决定（克隆文档另一处写「最近 7 天」，以控制台为准） | [备份空间收费](https://cloud.tencent.com/document/product/409/88470)，[备份](https://cloud.tencent.com/document/product/409/33945)，[克隆实例](https://cloud.tencent.com/document/product/409/68277) |
| 腾讯云 TCHouse-C | 备份存在托管 COS，按量 0.00018 元/GB/小时（约 0.13 元/GB/月） | 未查 | [计费](https://cloud.tencent.com/document/product/1299/47757) |
| 华为云 RDS | 送和存储一样大的备份空间，超出按需收费 | 未查 | [计算器](https://www.huaweicloud.com/pricing/calculator.html#/rds) 页面说明 |
| 阿里云、火山引擎 | 未查到 | 未查到 | — |

托管服务的备份只防数据库自己出事。按仓库的要求，备份还要和应用主机分开（`deploy/platform/phase1/backup/README.md:15`），用托管时也建议每周导一份逻辑备份进对象存储，防误删账号或欠费停机。

## 7. 微信消息能不能长期发给值班人员

现在的做法：小程序一次性订阅，用户点一次「允许」才能发一条（`docs/specs/miniprogram-wechat.md:20`）；规格里写了长期订阅只给政务、医疗、交通、教育这类机构（同文件 `:119`），但没附官方链接。

| 办法 | 条件 | 限制 | 适不适合我们 |
| --- | --- | --- | --- |
| 小程序长期订阅 | 官方现在的小程序文档只说有一次性和长期两种（[订阅消息](https://developers.weixin.qq.com/miniprogram/dev/framework/open-ability/subscribe-message.html)，模板类型 2 是一次性、3 是长期，[获取模板标题](https://developers.weixin.qq.com/miniprogram/dev/server/API/mp-message-management/subscribe-message/api_getpubnewtemplatetitles.html)），**没写开放范围**；同一页唯一列出的长期模板属于「金融业-银行」类目。服务号的文档写明「长期订阅通知仅向政务民生、医疗等公共服务领域开放」（[服务号订阅通知](https://developers.weixin.qq.com/doc/offiaccount/Subscription_Messages/intro.html)） | — | 企业主体做暖通运维基本拿不到；小程序的范围原文未查到 |
| 小程序设备消息 | 一种长期订阅；要「经过微信认证的非个人主体小程序」，加「工具—设备管理」类目，开通硬件设备能力，登记设备类型拿 `model_id`（[设备接入](https://developers.weixin.qq.com/miniprogram/dev/framework/device/device-access.html)，[设备消息](https://developers.weixin.qq.com/miniprogram/dev/framework/device/device-message.html)） | 用户按设备序列号订阅；模板关键词「由平台生成，为枚举值，开发者不能够自定义」；接入条件写的是「面向智能硬件生产企业或开发者」（[发送接口](https://developers.weixin.qq.com/miniprogram/dev/server/API/hardware-device/api_sendhardwaredevicemessage.html)） | 企业认证后可以试，把冷站网关登记成设备；内容只能用固定选项，审核能不能过不确定 |
| 服务号模板消息 | 认证服务号，按行业选类目和公共模板（[模板消息](https://developers.weixin.qq.com/doc/service/guide/product/template_message/Template_Message_Interface.html)） | 收件人用服务号的 OpenID，接口文档提示要确认用户「是否已关注公众号」（[发送接口](https://developers.weixin.qq.com/doc/service/api/notify/template/api_sendtemplatemessage.html)）；「不允许在用户没做任何操作或未经用户同意接收的前提下，主动下发」，标题不能是「系统通知」这类泛称（[运营规范](https://developers.weixin.qq.com/doc/service/guide/product/template_message/Template_Message_Operation_Specifications.html)）；每天 10 万次 | 能用，但要另注册服务号、值班人员要关注、要把服务号和小程序的用户对上（OpenID 不同） |
| 企业微信应用消息 | 企业微信里建一个自建应用，按成员账号发（[发送应用消息](https://developer.work.weixin.qq.com/document/path/90236)） | 每个应用对同一成员每分钟 30 条、每小时 1000 条，超出丢弃；每天「账号上限 × 200」人次；应用设成「在微工作台中始终进入主页」时，在微信里只能收 20 字节文本 | **推荐**：值班人员都是本公司员工，不用订阅、不限次数，能发卡片 |
| 企业微信群机器人 | 群里加一个消息推送，拿 webhook 地址（[消息推送配置](https://developer.work.weixin.qq.com/document/path/91770)） | 每个机器人每分钟 20 条；webhook 地址泄露就谁都能发 | 适合「值班群」里广播告警，配置最简单 |

接企业微信要改两处：通知模块加一个企业微信的发送渠道；Compose 里现在只有身份服务能访问微信（`compose.yaml:260`、`:1146`），要给发通知的服务开一个到 `qyapi.weixin.qq.com` 的出站网络。

## 8. 没能核实的地方

- 阿里云的 RDS、ECS、ClickHouse、Redis 价格（官网价格计算器要登录）；阿里云 RDS 管理账号权限、备份保留和收费。
- 华为云 OBS 单价、是否 S3 兼容；华为云 RDS 按时间点恢复的范围；华为云方案 B 的数据服务器价格。
- 火山引擎有没有托管 ClickHouse 和价格；火山引擎 ECS 价格；TOS 和阿里云 OSS 的 S3 兼容说明。
- 腾讯云 PostgreSQL 的 204/408 元规格是否含备节点；TCHouse-C 的内核版本号和单节点是否必须配 ZooKeeper；腾讯云 COS 同地域内网下载是否免费；腾讯云托管 Redis、托管 MQTT 的实际月价。
- 三家以外的云（阿里、华为、火山）托管 MQTT 能否用自己的 CA。
- 留在腾讯云换服务器是否完全不用动备案；现在这台服务器的带宽怎么计费。（已确认：`swayingwindmill.online` 是个人备案；现在的 139 是腾讯云轻量应用服务器，迁移后不再用轻量服务器，只比较 CVM 和 ECS。）
- 阿里云 u2a 2 核 8 GB 的价格、ESSD PL0 数据盘的确切单价、CDT 超出 20 GB 后的阶梯单价。
- 小程序长期订阅消息的开放范围原文；企业微信自建应用是否要先配「企业可信 IP」。
- 托管 PostgreSQL 上能否跑通 `phase1-migrator`（`SET ROLE`、`OWNER TO`），要拿一台实例实测。
- PostgreSQL 和 ClickHouse 现在的实际总大小，restic 去重后的真实占用（上面是按不去重估的上限）。

## 9. 云服务器还是容器服务

现在的 139 是腾讯云轻量应用服务器；迁移后不再用轻量服务器，只在正式的云服务器（腾讯云 CVM、阿里云 ECS）里选。

结论：迁移时用云服务器跑 Docker Compose，不买容器服务（托管 Kubernetes，或按秒计费的 Serverless 容器）。

- **我们的程序一直在跑。** 读数接收、MQTT、实时推送、告警判断和每分钟的定时复查都是常驻进程或长连接，Serverless 容器「没请求不收钱」的好处用不上，按秒算下来通常比包月服务器贵。托管 Kubernetes 的容器最后也跑在包月的节点上，另外还有集群管理和负载均衡的费用。
- **部署要整个重写，第一阶段用不上换来的东西。** `deploy/platform/phase1/compose.yaml` 有 41 个服务，配套的迁移和表结构预检、服务之间的双向 TLS 证书、后台服务身份的签发（`scripts/issue-workload-identities.mjs`）、`deploy.sh`、本地 `local:up` 都按 Compose 写；换成 Kubernetes 要改写成 Helm 或 manifest，MQTT 的 8884 要找支持四层透传的负载均衡，本地和服务器会变成两套。换来的自动扩容、多副本、滚动发布，第一阶段不需要：ADR 0013 第 13 节定的是「单机、可恢复」，不是高可用。
- **排查更难。** 现在一台机器上 `docker compose ps`、看日志、`--volumes-from` 就能临时排查；Kubernetes 上多了节点、Pod 调度、网络插件、存储卷几层，项目没有专门的运维人员。
- **数据和服务分开不需要容器服务。** 方案 B 已经把数据放在另一台机器、另一块盘上，部署里的 `PHASE1_POSTGRES_MODE=external` 等开关（`deploy/platform/phase1/README.md:54-71`）让应用那台不用改代码。

出现下面任何一种情况，再评估托管 Kubernetes：站点多到一台应用服务器扛不住；客户要求升级不停机或坏一台不影响使用；要给不同客户各部署一套；有了专门的运维人员。那时数据已经在单独的机器或托管服务上，应用全是无状态容器，换过去主要是改写部署文件。

## 10. 厂家突然多了，ECS 能不能直接扩、停不停业务

按阿里云帮助文档（2026-10-22 查）：

| 扩什么 | 停不停业务 | 出处 |
| --- | --- | --- |
| CPU 和内存（换更大的规格） | **要重启一次。** 包年包月实例升配后「重启实例后生效」；运行中的实例可以选立即重启，也可以预约一个重启时间（在「事件 > 变配重启事件」里改） | [升降配概述](https://help.aliyun.com/zh/ecs/user-guide/overview-of-instance-configuration-changes)、[包年包月实例升降配](https://help.aliyun.com/zh/ecs/user-guide/change-the-instance-types-of-subscription-instances) |
| 云盘容量 | **不停。** 「在线扩容：扩展容量后无需重启实例」，再在系统里用 `growpart` 扩分区、`resize2fs` 或 `xfs_growfs` 扩文件系统。云盘只能扩不能缩 | [扩容云盘（Linux）](https://help.aliyun.com/zh/ecs/user-guide/resize-linux-cloud-disks) |
| 公网带宽 | **不停。** 包年包月实例改基础公网带宽「立即生效」，也可以在一段时间内临时升带宽 | [升降配概述](https://help.aliyun.com/zh/ecs/user-guide/overview-of-instance-configuration-changes) |

换规格的几个限制（[哪些实例规格支持变配](https://help.aliyun.com/zh/ecs/user-guide/instance-families-that-support-instance-type-changes)）：

- u2a 只能换到同为 AMD 的 g9a、c9a、r9a、g9ae、c9ae、r9ae、u2a，不能换到 Intel 的规格；购买页也提示「创建后将无法直接变更为 Intel 实例规格」。
- 8 代及以后的规格（包括 u2a）要求系统里装了 NVMe 驱动才能变配。
- 目标规格在当前可用区要有库存，没有库存就换规格或跨可用区。
- 参加低价活动（例如 99 计划）的实例变配后，续费可能拿不到原来的优惠价。

对我们的影响：

- 应用机或数据机换大规格，就是挑夜里预约一次重启，停几分钟。网页和小程序这几分钟打不开；数据机重启时，应用连不上数据库，等它起来后自动重连。
- 现场读数在松雅轩网关的耐久发件箱改发平台之后，断开期间先存在网关上、连上后补发，不会丢；在那之前，这几分钟的读数会丢。
- 按松雅轩的量（约 80 个点位，每分钟一次），接 100 个站点也只是每分钟约 8,000 个读数。一台 4 核 8 GB 撑得住相当多站点，不够了再换 8 核 16 GB、16 核 32 GB。最先撑不住的多半是长时间范围的能耗现算（ClickHouse 单次查询内存上限 1.5 GiB），要靠能耗汇总表解决，加机器只能缓解。
- 要做到换规格也不停，需要应用跑两台、前面放负载均衡、一台一台换。这要先改程序：MQTT broker 换成能组集群的、限流计数改成共用 Redis、核实实时推送和读数接收能多份一起跑（第 9 节）。
