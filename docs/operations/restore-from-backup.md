# 从备份恢复

夜间备份是 `hvac-backup.timer`（每天 03:30，上海时间），脚本是 `deploy/platform/phase1/backup/run-scheduled-backups.sh`。PostgreSQL 用 `pg_basebackup`（tar、gzip、带一份 WAL），ClickHouse 用 `BACKUP DATABASE telemetry_history, DATABASE analytics` 写到磁盘 `phase1_backups`。每种默认按 `YYYYMMDDTHHMMSSZ` 目录名保留最新 `BACKUP_KEEP` 份（默认 7 份），只清理符合时间戳格式的目录，不会误删其他文件。

如果只跑本地目录且没有配置异地同步，备份和数据库就在同一台机器上：能对付误删和库文件损坏，不能对付整台服务器或整块盘一起坏掉。要做到整机换盘后还能恢复，需要在环境文件里配置 `BACKUP_OFFSITE_SYNC_CMD`（或在主机挂独立的异地存储）。

恢复会覆盖现有数据。先停写入，再动手。

## 备份清理、异地同步与就绪检查

1. **PostgreSQL WAL 归档清理**：删掉旧的 PostgreSQL 基础备份后，`run-scheduled-backups.sh` 会找留下来最老的那份基础备份，从它的 `backup_manifest` 或 `base.tar.gz` 里的 `backup_label`（找不到时再回退匹配 `POSTGRES_WAL_ARCHIVE_DIR` 里最老的 `.backup` 文件）拿到起始 WAL 编号，然后在 `postgres` 容器内执行 `pg_archivecleanup /var/lib/postgresql/wal-archive <起始WAL编号或.backup文件名>`，清掉比它更早的 WAL 段。例如 `BACKUP_KEEP=7` 时，第 8 天删掉第 1 天的基础备份，第 1 天之前的 WAL 段会一并清理，既防止归档目录无限膨胀，又保证留下的 7 份基础备份都能继续做时间点恢复。
2. **异地同步与加密上云（`BACKUP_OFFSITE_SYNC_CMD` + `sync-offsite-restic.sh`）**：`run-scheduled-backups.sh` 支持可选环境变量 `BACKUP_OFFSITE_SYNC_CMD`。默认留空不执行；搭配腾讯云 COS 使用时，将 `/etc/hvac/backup-offsite.env`（强制 `chmod 600`，含 `RESTIC_REPOSITORY=s3:cos.ap-guangzhou.myqcloud.com/<bucket>/<prefix>`、`RESTIC_PASSWORD` 与 `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`）配好后，把 `BACKUP_KEEP=1` 和 `BACKUP_OFFSITE_SYNC_CMD=/opt/hvac-platform/deploy/platform/phase1/backup/sync-offsite-restic.sh` 写入 `/etc/hvac/backup.env`。脚本会自动附带 COS 所需的 `-o s3.bucket-lookup=dns`，执行加密增量上传、按 7 天每日 + 4 周每周裁剪旧快照，并抽样 `1%` 做数据完整性校验；只有同步返回 0 才会更新 `observability/offsite-backup.success`（设置 `BACKUP_OFFSITE_REQUIRED=1` 或存在该文件时，`host-textfile.sh` 会输出 `store="offsite"` 指标交由 Prometheus 监控）。
3. **隔离恢复演练脚本（`run-offsite-restore-drill.sh`）**：运行 `deploy/platform/phase1/backup/run-offsite-restore-drill.sh --report-out=/tmp/drill-report.json --drill-record-out=/tmp/drill-record.json` 会从 restic 仓库还原指定快照（默认最新）到临时目录、核对 `SHA256SUMS`，并在与线上完全隔离的临时 Docker 容器中恢复 PostgreSQL 与 ClickHouse 并统计关键表行数；完成后自动销毁临时容器和临时目录。
4. **恢复就绪检查（`verify-recovery-readiness.sh`）**：运行 `deploy/platform/phase1/backup/verify-recovery-readiness.sh` 时，先检查 `POSTGRES_WAL_ARCHIVE_DIR` 最新归档的 WAL 距今不超过 `POSTGRES_RPO_TARGET_SECONDS`（默认 `300` 秒，即 5 分钟），并核对最新 PostgreSQL 基础备份里的 `SHA256SUMS`。当环境变量 `CLICKHOUSE_BACKUP_DIR` 非空时，还会检查该目录下最新的 `YYYYMMDDTHHMMSSZ` 备份目录存在、里面至少包含一个备份文件、且距今不超过 `CLICKHOUSE_RPO_TARGET_SECONDS`（默认 `90000` 秒，即 25 小时，对应每天一次备份加 1 小时缓冲）。

## 恢复前

1. 记下现在的时间、还剩多少磁盘、准备用哪一份备份的目录名（`YYYYMMDDTHHMMSSZ`）。
2. 在那份 PostgreSQL 备份目录里核对校验和：`sha256sum --check SHA256SUMS`。对不上就换一份，不要继续。
3. 停掉会写库的容器：`energy-api`、`telemetry-worker`、`metric-worker`、`identity-service`、`scheduler`、`maintenance`，以及开着的预测、优化、故障诊断、节能验证、报告、AI 助手。PostgreSQL 和 ClickHouse 先留着，确认备份可读之后再停。

## PostgreSQL

基础备份目录里是 `pg_basebackup --format=tar --gzip` 的产物（`base.tar.gz`，以及一份 WAL 包）。把现有数据目录改名留着，不要直接删：

```bash
# 目录以这台机器的 POSTGRES_DATA_DIR 为准。先停 postgres 容器。
sudo mv "$POSTGRES_DATA_DIR" "${POSTGRES_DATA_DIR}.broken-$(date -u +%Y%m%dT%H%M%SZ)"
sudo mkdir -p "$POSTGRES_DATA_DIR"
sudo tar -xzf "$BACKUP/base.tar.gz" -C "$POSTGRES_DATA_DIR"
sudo mkdir -p "$POSTGRES_DATA_DIR/pg_wal"
sudo tar -xzf "$BACKUP/pg_wal.tar.gz" -C "$POSTGRES_DATA_DIR/pg_wal"
docker run --rm --entrypoint id postgres:16.4-bookworm postgres
sudo chown -R <上面打印的 uid:gid> "$POSTGRES_DATA_DIR"
```

`id` 打出来的是镜像里的 postgres 用户（形如 `uid=999(postgres) gid=999(postgres)`）。解出来的文件所有者不对时，库会因为权限起不来。

只要备份里那一份 WAL、不再回放归档时：不要放 `recovery.signal`，直接启动。要回到归档里更晚的一个时间点时，在数据目录放 `recovery.signal`，并在 `postgresql.auto.conf` 写 `restore_command`，从 `POSTGRES_WAL_ARCHIVE_DIR` 把对应的段拷回来。回放的终点用 `recovery_target_time`。这是 PostgreSQL 自己的时间点恢复，不是另写一套。

启动后：

```bash
docker exec <postgres 容器> pg_isready -U postgres
docker exec <postgres 容器> psql -U postgres -d hvac_s0 -c 'SELECT 1'
```

业务容器在这之后再启动。没完成的控制指令按 `deploy/platform/phase1/recovery/README.md` 核对，不要盲目重发。

## ClickHouse

备份名就是当时的时间戳目录，在 `CLICKHOUSE_BACKUP_DIR` 下。恢复语句和备份语句成对：

```sql
RESTORE DATABASE telemetry_history, DATABASE analytics FROM Disk('phase1_backups', '<备份目录名>')
```

库里已经有这些表时，ClickHouse 会拒绝盖上去。要在维护窗口里先停写入，确认没有人还在查这批历史，再清空或换上这次要恢复的库。不要在还有 `telemetry-worker` 写入时做。

恢复后在容器里 `SELECT count() FROM telemetry_history.observations`（或当时还在用的表）看数量不是 0，再启动 `telemetry-worker`。它会从自己的进度接着写，不要为了「补齐」去改进度表，除非已经按 `docs/HANDOFF.md` 里写过的做法清空进度并准备重算。
