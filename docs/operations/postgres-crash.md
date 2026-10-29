# PostgreSQL 崩溃之后

容器在重启，日志里是 `PANIC` 或 `No space left on device` 时，按下面做。目标是让库重新接受连接，不是把表改回某个旧样子。

## 1. 先看是不是磁盘

```bash
df -h /
docker ps -a --filter name=postgres --format '{{.Names}} {{.Status}}'
docker logs --tail 80 "$(docker ps -aq --filter name=postgres | head -n 1)"
```

日志里有 `No space left on device`：先做 `docs/operations/disk-full.md`。盘还满着的时候反复 `docker start` 只会再 PANIC 一次。

没有空间问题，再看是不是起不来（配置、权限、数据目录损坏）。

## 2. 确认数据目录还在

数据在 `POSTGRES_DATA_DIR`（正式机器上是运行目录里的 `data/postgres`，以环境文件为准）。目录还在、只是进程崩了：腾出空间或修完权限后让 Compose 把它拉起来。

```bash
sudo -n /opt/hvac-platform-tools/deploy.sh
```

只想拉数据库时，在平台目录里用原来的 Compose 命令 `up -d postgres`，不要换一套没在这台机器上用过的参数。

## 3. 起来之后核对

```bash
docker exec "$(docker ps -q --filter name=postgres | head -n 1)" pg_isready -U postgres
```

`accepting connections` 之后再看依赖它的进程（`energy-api`、`telemetry-worker`、`identity-service`）是否还在重启。它们的 `/metrics` 抓不到时会有告警，见 `docs/operations/alerts.md` 的「PostgreSQL 进程不可用」和「平台进程没有指标」。

## 4. 数据目录损坏或起不来

不要在这个数据目录上试修到没有备份。按 `docs/operations/restore-from-backup.md` 用最新一份基础备份。同一块盘上的备份在「盘还读得了、只是库文件坏了」时有用；整块盘坏了，这份备份也一起没了。

恢复完成之前，不要为了消掉重启把 PostgreSQL 的 `restart: unless-stopped` 改掉，也不要删 `pg_wal`。
