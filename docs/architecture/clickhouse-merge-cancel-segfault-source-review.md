# ClickHouse merge cancel segfault source review

日期：2026-10-03。范围：本地/一期 ClickHouse `clickhouse/clickhouse-server:26.3.12.3` 在后台 merge 中先报 `MEMORY_LIMIT_EXCEEDED`，随后在 `MergeTreeDataPartWriterCompact::cancel()` 段错误（`Address: 0xf9`，2 小时内同一指令地址 7 次）。只使用一手来源：ClickHouse GitHub 仓库（源码、PR、issue、`docs/changelogs/`）和 Docker Hub tag API。

## 结论

- 这是已知上游缺陷，已修复。修复 PR [#101292](https://github.com/ClickHouse/ClickHouse/pull/101292)（"Fix UBSan in MergeTreeDataPartWriterCompact::cancel"），26.3 回补 PR [#105786](https://github.com/ClickHouse/ClickHouse/pull/105786)。
- **26.3.12.3 不含修复；第一个包含修复的 26.3 LTS 版本是 `26.3.13.31`。**
- **建议目标：留在 26.3 LTS 线，升级到当前最新补丁 `26.3.39.7`**（tag `v26.3.39.7-lts`，2026-10-02 发布，Docker Hub tag `26.3.39.7` 存在）。它同时包含两项与我们用法相关的后续修复：内存追踪泄漏导致的误报 `MEMORY_LIMIT_EXCEEDED`，以及 MV + 分区去重目标表上的越界读崩溃（见"升级影响"）。
- **决定（2026-10-03）：采纳。** 所有固定 `26.3.12.3` 的位置都改为 `clickhouse/clickhouse-server:26.3.39.7@sha256:3a91276f066905da0edbbd622d3fc2a2df632c87ea7fe32ef4e74fdf8c9567b0`（`deploy/platform/phase1/compose.yaml` 维持只写 tag 的写法）。留在 LTS 线，是因为 changelog 中的不兼容项都不影响我们的用法。merge 内存不足本身已经单独修复：历史数据改为攒批写入，去重窗口降为 10000。#112649 让物化视图目标表按触及的分区登记去重 token；我们的汇总表按月分区，一次插入通常只触及一个分区，窗口占用基本不变。
- 修复只消除"OOM 之后的段错误"。merge 申请内存失败本身仍会发生，只是变成可重试的普通异常，不再拖垮整个 server。内存余量问题要另外处理。

## 证据

### 根因（源码，tag `v26.3.12.3-lts`，peeled commit `d23c7536b980c34b39c850b08ef23c509f06aaaa`，与崩溃日志里的 git hash 一致）

[`src/Storages/MergeTree/MergeTreeDataPartWriterCompact.cpp`](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/src/Storages/MergeTree/MergeTreeDataPartWriterCompact.cpp)：

```cpp
// addStreams() 的回调，第 86-88 行
auto & stream = streams_by_codec[codec_id];          // operator[] 先插入空 shared_ptr
if (!stream)
    stream = std::make_shared<CompressedStream>(plain_hashing, compression_codec);  // 这里抛异常

// cancel()，第 549-555 行
for (const auto & [_, stream] : streams_by_codec)
{
    stream->hashing_buf.cancel();   // stream 为 null 时解引用
    stream->compressed_buf.cancel();
}
```

- `CompressedStream` 的构造包含 `CompressedWriteBuffer`（[`MergeTreeDataPartWriterCompact.h`](https://github.com/ClickHouse/ClickHouse/blob/d23c7536b980c34b39c850b08ef23c509f06aaaa/src/Storages/MergeTree/MergeTreeDataPartWriterCompact.h) 第 85-93 行），它需要分配缓冲区。内存超限时，`CompressedWriteBuffer::CompressedWriteBuffer` 抛出 `MEMORY_LIMIT_EXCEEDED`，和我们日志里的栈顶一致：`addStreams` <- `initStreamsIfNeeded` <- `write`。
- 异常抛出时，`operator[]` 已经在 `std::map streams_by_codec` 里留下了一个值为空的条目。之后后台执行器走错误路径 `TaskRuntimeData::cancel` -> `MergePlainMergeTreeTask::cancel` -> `MergeTask::cancel` -> `MergeTreeDataPartWriterCompact::cancel`，遍历 map 时就解引用了这个空指针。
- 推断（未逐字节核对）：`0xf9` 很小，符合"空指针 + `hashing_buf` 成员偏移 + 被读字段偏移"的形态，不像随机的野指针。
- PR #101292 的描述给出的根因与上面一致："`streams_by_codec[codec_id]` inserts a null `shared_ptr` via `operator[]` before `std::make_shared` runs"（[#101292](https://github.com/ClickHouse/ClickHouse/pull/101292)）。

### 上游 issue 与修复

- Issue [#105777](https://github.com/ClickHouse/ClickHouse/issues/105777)（"Clickhouse crash"，26.3.11）：栈为 `MergeTreeDataPartWriterCompact::cancel()` <- `MergeTask::cancel()` <- `MergeFromLogEntryTask::cancel()` <- `TaskRuntimeData::cancel()`。它走的是 Replicated 的 merge 入口，我们走的是 `MergePlainMergeTreeTask`，但都会进入同一个 `MergeTask::cancel`，根因相同。该 issue 由 #105786 关闭。
- 修复 [#101292](https://github.com/ClickHouse/ClickHouse/pull/101292)（2026-04-29 合入 master，merge commit `880f38dc01c8d788e08f8e296dc362a64bfee673`）：改成先 `find`，在 `std::make_shared` 成功后再 `emplace`，构造抛异常时 map 保持不变。PR 同时新增 failpoint `compact_part_writer_fail_in_add_streams` 和回归测试 `tests/queries/0_stateless/04113_compact_part_writer_cancel_on_exception.sql`。标签包括 `pr-bugfix`、`v26.3-must-backport`、`v26.4-must-backport`。PR 的 Version info 写的是 "Merged into `26.5.1.164`; Backported to `26.4.2.6`, `26.3.13.10`"（这些是合入时的开发版本号，正式 tag 见下文）。
- 26.3 回补 [#105786](https://github.com/ClickHouse/ClickHouse/pull/105786)（标签 `pr-backport`，base `26.3`，2026-05-25 合入，merge commit `3e44216b60a2082399b95359c04ce705a7cb95ec`），对应的 cherry-pick 是 [#103696](https://github.com/ClickHouse/ClickHouse/pull/103696)。26.4 回补是 [#104211](https://github.com/ClickHouse/ClickHouse/pull/104211)。
- 修复后的代码（master 与各修复 tag 相同）：[`MergeTreeDataPartWriterCompact.cpp` @ v26.3.39.7-lts](https://github.com/ClickHouse/ClickHouse/blob/v26.3.39.7-lts/src/Storages/MergeTree/MergeTreeDataPartWriterCompact.cpp) 中有注释 "Exception safety: if `make_shared` throws, the map is not modified, avoiding null entries in `cancel`"。

### 哪些发布包含修复（用 tag 内容核对）

用 GitHub compare API（`3e44216b...<tag>`）加 tag 下源码 grep 核对：

| Tag | 是否含修复 | 依据 |
| --- | --- | --- |
| `v26.3.12.3-lts`（2026-05-22） | 否 | compare 状态 `behind`；源码仍是 `streams_by_codec[codec_id]` |
| `v26.3.13.31-lts`（2026-06-08，commit `27ae4e9f...`） | 是，26.3 中第一个 | compare `ahead`；[v26.3.13.31-lts changelog](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.13.31-lts.md) 列出 "Backported in #105786: Fix undefined behavior in `MergeTreeDataPartWriterCompact::cancel` when a stream allocation fails" |
| `v26.3.39.7-lts`（2026-10-02，commit `4277fab4...`，26.3 最新） | 是 | compare `ahead`；源码含修复 |
| `v26.4.2.10-stable` 及以后 | 是 | 源码含修复（26.4 线第一个） |
| `v26.5.1.882-stable` 及以后 | 是 | 源码含修复 |
| `v26.8.15.10-lts`（下一条 LTS 线） | 是 | 源码含修复 |

tag 列表来自 `git ls-remote --tags https://github.com/ClickHouse/ClickHouse.git`。

Docker Hub（`https://hub.docker.com/v2/namespaces/clickhouse/repositories/clickhouse-server/tags/<tag>`）：

- `26.3.39.7` 存在，2026-10-02 推送，index digest 为 `sha256:3a91276f066905da0edbbd622d3fc2a2df632c87ea7fe32ef4e74fdf8c9567b0`（amd64 `sha256:e89b92b1...`，arm64 `sha256:f88e7608...`）。滚动 tag `26.3` 当前也指向同一个 amd64 镜像。
- `26.3.13.31` 也存在，是可接受的最小修复版本。

## 升级影响

对照 `docs/changelogs/v26.3.13.31-lts.md` 到 `v26.3.39.7-lts.md` 里的 "Backward Incompatible Change" 段落，逐条和我们的用法比对：单节点 Docker Compose，MergeTree/AggregatingMergeTree，`non_replicated_deduplication_window`，物化视图，HTTP `JSONEachRow` 加 `insert_deduplication_token`。

| 版本 | 不兼容项 | 对我们的影响 |
| --- | --- | --- |
| [26.3.13.31](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.13.31-lts.md) | `show_data_lake_catalogs_in_system_tables` 改名为 `show_remote_databases_in_system_tables`，默认隐藏 MySQL/PostgreSQL 数据库（[#104416](https://github.com/ClickHouse/ClickHouse/pull/104416)） | 无：我们不用这类数据库引擎；旧名保留为别名 |
| [26.3.17.110](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.17.110-lts.md) | dynamic disk 默认禁用 `from_env`/`from_zk`/`include`（[#99138](https://github.com/ClickHouse/ClickHouse/pull/99138)） | 无：不使用 dynamic disk |
| [26.3.32.14](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.32.14-lts.md) | 显式 `rocksdb_dir` 的 `EmbeddedRocksDB` 需要 FILE 授权（[#117665](https://github.com/ClickHouse/ClickHouse/pull/117665)） | 无 |
| [26.3.33.24](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.33.24-lts.md) | `File` 源的 `rename_files_after_processing` 需要 `WRITE ON FILE`（[#117895](https://github.com/ClickHouse/ClickHouse/pull/117895)） | 无 |
| [26.3.34.136](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.34.136-lts.md) | 出站 TLS 校验主机名（[#116724](https://github.com/ClickHouse/ClickHouse/pull/116724)）；MySQL 字典不能开 `enable_local_infile`（[#120972](https://github.com/ClickHouse/ClickHouse/pull/120972)） | 预期无：我们是服务端，客户端走入站 HTTP，没有出站 TLS（s3/url/remoteSecure）和 MySQL 字典。升级前确认 ClickHouse 配置里没有出站 HTTPS 源 |

同一区间内与我们用法直接相关的修复（不属于不兼容项，但会改变行为，需要知道）：

- [26.3.34.136](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.34.136-lts.md)，[#110784](https://github.com/ClickHouse/ClickHouse/pull/110784)：父级内存追踪器拒绝分配后，线程、查询、用户三级追踪器没有扣回被拒的量，`MemoryTracking` 会越积越高，"could lead to spurious `MEMORY_LIMIT_EXCEEDED` errors"。这可能就是我们 merge 反复 OOM 的诱因之一（推断，未验证）。
- [26.3.39.7](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.39.7-lts.md)，[#112649](https://github.com/ClickHouse/ClickHouse/pull/112649)：INSERT 经过会改变行数的物化视图（如小时聚合）写入分区去重目标表时，`DeduplicationInfo::filterToPartition` 会越界读，release 构建下会崩溃。修复后，对任何物化视图目标表，每个去重 token 都会登记到它触及的每个分区。这正是我们"MV -> 分区 AggregatingMergeTree + `non_replicated_deduplication_window`"的形态。推断：修复后 MV 目标表每次插入占用的去重窗口条目可能变多，需要复核 `telemetry-history-microbatch-source-review.md` 里 100,000 窗口的余量判断。
- [26.3.22.7](https://github.com/ClickHouse/ClickHouse/blob/master/docs/changelogs/v26.3.22.7-lts.md)，[#115866](https://github.com/ClickHouse/ClickHouse/pull/115866)：去重哈希加入 Object 路径。我们用显式 `insert_deduplication_token`，不使用 Object 列，预期无影响。
- 只涉及 async insert 的去重修复（[#111049](https://github.com/ClickHouse/ClickHouse/pull/111049) 等）与我们无关，我们用的是同步插入。

上述 changelog 里没有要求迁移数据或改表结构的条目（这是对 changelog 的阅读结论，没有额外源码证据）。升级方式：替换镜像 tag 和 digest 后重启容器，再跑一遍现有的 ClickHouse 历史集成测试。

## 未解决的问题

- 修复只消除段错误，merge 申请内存失败本身的根因（内存上限、并发 merge 数、part 大小、#110784 记账泄漏）不在本文范围内。升级后要观察 `system.part_log` / 错误日志里的 merge 失败是否仍持续出现。
- 仓库里镜像同时按 tag 和 digest 固定（`infra/telemetry/compose.yaml` 和若干 `scripts/*.mjs` 使用 `26.3.12.3@sha256:1f7cd090...`；`deploy/platform/phase1/compose.yaml` 只用 tag）。Docker Hub 上 `26.3.12.3` 的 tag 已在 2026-08-05 重新推送，当前 index digest 是 `sha256:22886e23...`，和仓库里固定的不一样。升级时要统一改成 `26.3.39.7@sha256:3a91276f...`，并同步 `scripts/check-s2-telemetry-history.mjs` 里的断言。重新推送是否只改了基础镜像层，本文未核实。
- `0xf9` 与成员偏移的精确对应关系没有反汇编核对；结论依据的是源码路径、栈完全一致，以及上游 PR 的描述。
- master 上 `cancel()` 后来又加了 `if (plain_file)` 判空，用于处理 quorum INSERT 场景（见 [#122992](https://github.com/ClickHouse/ClickHouse/pull/122992) 相关测试 `04405_mergetree_compact_cancel_after_finish`），`v26.3.39.7-lts` 不含这项改动。该路径只出现在 Replicated quorum INSERT，与我们的单节点 MergeTree 无关，没有继续追溯它的原始 PR。
