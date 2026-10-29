# 磁盘写满

这台机器的系统盘大约 59GB。2026-10-20 写满之后，PostgreSQL 打出 `PANIC: could not write to file: No space left on device` 并反复重启，ClickHouse 拒绝写入，写历史读数的程序停了约 15 个小时。

先腾出能让数据库起来的空间，再查是谁在涨。不要手工删 `pg_wal` 里的文件，也不要在几乎没有空闲时跑 `VACUUM FULL`（它需要和留下的数据差不多大的空位）。

## 先看还剩多少

```bash
df -h /
docker system df
```

`docker system df` 里 Build Cache 和 Images 很大时，优先清构建缓存和不再使用的旧镜像，不动正在跑的 `0.1.0-dev`。

部署成功之后，流水线只留最近两套 `before-*` 回滚镜像，并清掉悬空镜像和超过 24 小时的构建缓存。磁盘已经满、部署跑不下去时，可以在服务器上先手动做同样的事：

```bash
docker images --format '{{.Repository}}:{{.Tag}}' | grep ':before-'
docker image prune -f
docker builder prune -f --filter until=24h
```

第三套以及更老的 `before-*` 可以删。只留最近一套时，这次部署如果失败就没有退路，所以能留两套就留两套。

## 再看是哪一类在涨

按这个顺序看，不要一上来删数据库文件：

1. PostgreSQL 数据目录和 `pg_wal`。`pg_wal` 比表大很多，多半是归档或检查点堵住了。开着 `POSTGRES_ARCHIVE_MODE=on` 时，看归档目录是不是也满了。归档失败要在归档那边修，不能删 `pg_wal`。
2. 同一块盘上的夜间备份（`runtime/server/backups`）。备份和数据在同一块盘时，备份本身会把盘写满。删最老的几份之前，确认还留着最新一份，并且 `SHA256SUMS` 还在。
3. ClickHouse 的 `system.query_log`。它有 7 天 TTL，不该靠删数据目录来腾。
4. Prometheus。块数据上限是 2GB、时间上限 15 天。如果 Prometheus 的卷已经超过这个数，先确认进程用的是带 `--storage.tsdb.retention.size=2GB` 的启动参数，再重启它让它自己删旧块。
5. 告警判断流水。2026-10-20 的满盘主要是 `alarm_runtime.alarm_evaluation_event` 把整站快照反复写进去。现在只在判断结果变了时写，超过 30 天会删。不要为了腾空间把告警、处理记录或当前判断状态删掉。

`docs/HANDOFF.md` 里「磁盘」一节写着当时占空间的表。和那一节对得上的，按那里的处理；对不上的，先 `du -xhd1` 找出新的目录再动手。

## 腾出来之后

1. `docker ps -a --filter label=com.docker.compose.project=hvac-platform` 看 PostgreSQL、ClickHouse、`telemetry-worker` 是不是还在重启。
2. PostgreSQL 能接受连接之后，再考虑 `VACUUM`。`VACUUM FULL` 仍然要先有足够空闲。
3. 看 Prometheus 里磁盘是否还在 85% 以上，以及「预计 24 小时写满」是否还亮着。规则和步骤在 `docs/operations/alerts.md` 的「主机磁盘和 PostgreSQL 的 WAL」。
