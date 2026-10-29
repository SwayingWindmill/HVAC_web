# 从备份恢复

夜间备份是 `hvac-backup.timer`（每天 03:30，上海时间），脚本是 `deploy/platform/phase1/backup/run-scheduled-backups.sh`。PostgreSQL 用 `pg_basebackup`（tar、gzip、带一份 WAL），ClickHouse 用 `BACKUP DATABASE telemetry_history, DATABASE analytics` 写到磁盘 `phase1_backups`。每种默认留最新 7 份。

备份和数据库现在在同一块盘上。它能对付误删和库文件损坏，不能对付整块盘一起坏掉。整机换盘要等备份拷到别的机器之后才做得到，那一步还没做。

恢复会覆盖现有数据。先停写入，再动手。

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
