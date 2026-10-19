# 第一阶段的备份

按《智慧能源系统总体部署架构设计 V1》（`SE-ARCH-DEPLOY-001 V1.0`）和《SE-OPS-009 V1.0》的恢复目标，在不引入数据库高可用集群的前提下做的备份。

## PostgreSQL

`PHASE1_POSTGRES_MODE=local` 时，Compose 里的 PostgreSQL 支持把 WAL 持续归档到 `POSTGRES_WAL_ARCHIVE_DIR`，但只有 `POSTGRES_ARCHIVE_MODE=on` 才真的开。开发和测试默认关；预发和正式环境只有在归档目录可写、有容量管理、并且和 PostgreSQL 数据卷分开时才开。做一次基础备份：

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile local-postgres --profile backup run --rm postgres-backup
```

PostgreSQL 在外部时，备份由那边负责。只有外部数据库允许 `pg_basebackup` 时，才能用同一个 `postgres-backup` 容器对着它备份；否则托管服务的备份和时间点恢复应该在托管服务那一层做，不在应用主机上模拟。

本机 PostgreSQL：正式环境的 `POSTGRES_WAL_ARCHIVE_DIR` 和 `POSTGRES_BASE_BACKUP_DIR` 都要放在和数据卷分开的存储上。外部 PostgreSQL：对应的备份位置要和应用主机分开。和出故障的盘在同一块盘上的本地目录，不算灾难恢复的备份。

基础备份用带流式 WAL 的 `pg_basebackup`，并写 `SHA256SUMS`。持续归档加上一份合适的基础备份，才能做时间点恢复。在带时间记录的恢复演练实际测过之前，仓库不声明任何 RPO。

## ClickHouse

`PHASE1_CLICKHOUSE_MODE=local` 时，ClickHouse 在 `CLICKHOUSE_BACKUP_DIR` 有一个单机的原生备份盘：

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile local-clickhouse --profile backup run --rm clickhouse-backup
```

正式环境要把 `CLICKHOUSE_BACKUP_DIR` 放在受保护的备份存储上，不放在 ClickHouse 数据卷上。ClickHouse 在外部时，备份和恢复由那边或托管服务负责，应用主机不模拟。

## 定时

第一阶段在一个受控的调度器里登记备份（例如 Linux 主机上一组受管理的 systemd timer），不要给每个服务各加一个 cron。

单台服务器上就是 `hvac-backup.service` + `hvac-backup.timer`（每天 03:30，上海时间），执行 `run-scheduled-backups.sh`：一份 PostgreSQL 基础备份和一份 ClickHouse 备份（`telemetry_history` 和 `analytics`），然后每种只留最新的 `BACKUP_KEEP` 份（默认 7）。两个备份都在各自数据库的容器里跑：postgres 镜像的 `pg_hba.conf` 只允许从容器里做复制连接，ClickHouse 的默认用户也只能从那里登录。安装：

```bash
sudo install -m 644 deploy/platform/phase1/backup/hvac-backup.{service,timer} /etc/systemd/system/
sudo install -m 600 /dev/stdin /etc/hvac/backup.env <<'ENV'
PHASE1_RUNTIME_DIR=/opt/hvac-platform/deploy/platform/phase1/runtime/server
PHASE1_ENV_FILE=/opt/hvac-platform/deploy/platform/phase1/environments/server.runtime.env
PHASE1_DEPLOYMENT_TIER=single-lite
HVAC_LOCAL_PROJECT=hvac-platform
BACKUP_KEEP=7
ENV
sudo systemctl daemon-reload && sudo systemctl enable --now hvac-backup.timer
```

在配置好拷到服务器以外之前，备份都和数据在同一块盘上：能防误操作和数据损坏，防不了整台服务器丢失。

基本的节奏：

- PostgreSQL 基础备份：每天。
- PostgreSQL WAL 归档：持续，归档超时由部署配置限定。
- ClickHouse 备份：每天在主要的写入时段之后，或者按站点的数据量定。
- 恢复测试：定期、必须做。现有的「删除 + 恢复」测试可以单独跑来证明软件没问题，但它不是自动的发布检查，也不能代替在预发或类正式环境里用真实备份做的恢复。

## 恢复目标和检查

《SE-OPS-009》定下：PostgreSQL 最多丢 5 分钟数据、2 小时内恢复；控制指令和指令记录最多丢 5 分钟、1 小时内恢复。整台服务器更换 4 小时内恢复，前提是有备用或冷备的硬件、服务器以外的备份、有版本的配置和写好的恢复步骤。

现在 PostgreSQL 的 `archive_timeout=60s`。所以要声明达到这些目标，正式环境的 WAL 和基础备份目录必须放在出故障的服务器和数据盘以外的受保护存储上；同一台服务器上的备份不算整机的灾难恢复。

外部备份挂好之后，每天在主机上跑一次准备情况检查：

```bash
POSTGRES_WAL_ARCHIVE_DIR=/mnt/smart-energy-backup/postgres/wal \
POSTGRES_BASE_BACKUP_DIR=/mnt/smart-energy-backup/postgres/base \
./deploy/platform/phase1/backup/verify-recovery-readiness.sh
```

它检查最新归档的 WAL 不超过 5 分钟，并校验最新一份基础备份的校验和清单。这只证明备份准备好了。

配好备份、检查通过，都不等于证明达到了 RPO/RTO。达标要做 `../recovery/README.md` 里规定的带时间记录的演练，记下确认故障的时间、PostgreSQL 能恢复到的最新时间、各组件和业务恢复的时间，以及最后业务验证完成的时间。
