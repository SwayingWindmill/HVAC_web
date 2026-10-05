# 冷站平台评审之后：下一步建议

日期：2026-10-05（Asia/Taipei）。本文件是当前证据支持的建议，不是新的实现授权或产品架构权威。

## 当前证据

- 用户提供的桌面 HTML《冷站平台架构评审》基于 2026-10-02 的 `c0374408` 和当时的运行环境，属于历史评审材料。里面的删除、重构和排序建议不能直接当成本次执行指令。
- 本次开始时工作分支为 `chore/check-governance`，HEAD `c28a38e1`。只存在用户已有的未跟踪 `.tmp_refs/`。远端 main 为 `8a542fab3a512455c9af48e72e127404cff3f60d`；两者的文件差异只有已合并 #430 的 `CLAUDE.md` 导航说明。本次更名工作从该 main 建立 `codex/matt-skills-v1-3-review`。
- [#431](https://github.com/SwayingWindmill/HVAC_web/pull/431) 的有效检查接入和过时检查删除、[#430](https://github.com/SwayingWindmill/HVAC_web/pull/430) 的 agent 导航说明均已合并。截图里的四项必需检查和复盘整理不是新的待办。
- Connectivity 的注册表读口、解析后的遥测身份、多网关上行和命令路由已经落地；[#405](https://github.com/SwayingWindmill/HVAC_web/issues/405) 已关闭，[#420](https://github.com/SwayingWindmill/HVAC_web/pull/420) 已合并。`modules/connectivity/README.md`、`pkg/connectivity/uplink.go`、`command_route.go` 和 `uplink_integration_test.go` 对应当前实现。不要重新设计或重新执行整份旧接入层候选。
- [#407](https://github.com/SwayingWindmill/HVAC_web/issues/407) 仍然 open，原先的 blocker #405 已关闭。当前代码有有效凭据查询与吊销状态隔离，但还没有该票要求的接入码、领证、续期及运维操作完整路径。
- `cmd/energy-api/internal/gateway/dashboard.go:111` 起仍将当前功率、费用、节能量和 COP 标记为未接入；`modules/energy` 已有电力累计读数到区间事实的 projector，因此能源工作应先比较已有处理链，而非另建第二套起点。
- `cmd/energy-api/embedded_energy.go:159` 仍启动内嵌 owner 的 TLS listener。进程内调用的候选尚未实施，但不构成 #407 已知前置条件。

没有重启本地环境或运行新的现场验收；PR 合并和代码存在不等于真实设备验收完成。旧评审列出的每个运行时缺陷是否彻底消失，应在对应链路验收时确认，不能只凭标题判定。

## 建议顺序

1. **完成 #407，收口 #397。** 以当前票据为执行合同：Web 登记网关和生成一次性接入码 → 模拟器用 CSR 领证 → MQTT 上报 → 下发命令 → 续期 → 吊销。验证 24 小时单次接入码、90 天证书、续期后旧证书在原有效期内可用、吊销后上行隔离及命令阻断、审计记录。模拟器改用同一路径，移除种子脚本直接签证书的路径。第二站点应无需手工改配置或重启已有进程。
2. **完成这一条链的真实环境验收，再规划能源首片。** 只运行相关领域门禁和需要证明的集成/浏览器行为；Web 部分按当前产品权威和 1440px 桌面真实数据检查。浏览器桩通过不替代模拟器、broker 和 Connectivity 的真实闭环。
3. **能源先做可解释的站点区间事实与 COP。** 先复核当前电力 projector、Meter/Binding、冷量数据的单位与时间窗，再确定一个冷站、一个时间窗的电量/冷量/COP/覆盖率合同。COP 必须按同窗总冷量除以总电量，缺数不得冒充 0。费用、电价、碳排、虚拟表、基线节能量按明确的下一片推进，不能一轮全部铺开。正式形成实现决策前，按 AGENTS.md 阅读固定版本 ThingsBoard/OpenEMS/MyEMS 的适用源码、测试和官方文档，并更新源审查记录；现有旧记录仅是索引和背景。
4. **再推进真机 edge-runtime。** 先形成模拟与真机共用的宿主/驱动接口和一个实际控制行为，保留当前命令审批、租约、安全约束、回读验证。控制器时间尺度、断网缓冲和硬件安全责任需要明确后再增加寻优能力。
5. **owner 进程内调用及删除工作按具体摩擦穿插。** 对真实进程边界、授权和 owner 数据所有权先作源码比较；从一条调用链开始验证。不要把整个平台授权重写或批量删表作为当前接入交付的前置工程。仅凭“没有部署”或“没有搜到引用”不足以删除某张表；也不要以新 gate 或兼容层代替行为证明。

[#346](https://github.com/SwayingWindmill/HVAC_web/issues/346)、[#349](https://github.com/SwayingWindmill/HVAC_web/issues/349) 仍在验收队列；应按其实际合同安排对应现场验证，不在本次技能更名中代为关闭。智能层 [#381](https://github.com/SwayingWindmill/HVAC_web/issues/381)、[#382](https://github.com/SwayingWindmill/HVAC_web/issues/382) 同样仍 open；当前证据不支持让智能工具扩展抢占接入闭环和能源事实的优先级。

## Matt 技能如何配合

发布和逐文件差异见 [v1.3 源审查](matt-skills-v1.3-source-review-2026-10-05.md)，实际使用证据见 [25 次会话审计](matt-skills-session-audit-2026-10-05.md)。推荐目标为官方 v1.3.1，不是不存在的 `v1.3` tag。

包含历史 Matt 技能，25 个可访问用户根会话中有 18 个成功加载技能，共 110 次加载、17 种技能；其中 1 次为 v1.3 已移除的 resolving-merge-conflicts。仅看 v1.3 保留名单则是 17 个会话、109 次、16 种。高频为 diagnosing-bugs 22、implement 15、code-review 15、wayfinder 11、research 10。加载次数不是交付次数。大部分技能由 agent 针对你的业务要求选择，只有三个会话存在明确技能命令；其中 `/retro` 有请求而没有成功加载证据。因此升级重点应是现有诊断/实施/评审链的可靠路由，以及解决已观察到的长会话导航摩擦。

- #407 已有规范和验收合同，适合先 `/implement` 实现这张明确票据；等更新 `/implement-spec` 后，用它沿 #397 检查剩余子票和依赖，避免重写规范或重做已合并任务。
- 能源模块责任和词汇仍需裁决时，用 `/grill-with-docs` + `/domain-modeling`，只将稳定词汇写入 `GLOSSARY.md`，将真正需要长期解释的权衡写入 ADR，再 `/to-spec`、`/to-tickets`。已清楚的票据不重新采访。
- `/wayfinder` 留给跨会话且路径仍不明的重大决策；不要把 #407 这样清楚的 feature 重新变成探索流程。
- `/diagnosing-bugs` 用于有实际红灯、可重复的故障；修复后是否做 `/retro` 或 `/improve-codebase-architecture` 由人按证据决定。v1.3.1 的路由正是对此作了修正。
- `/retro` 在一个有代表性的交付批次后提取一次稳定导航知识。#430 已经补过环境配置、证书、模拟器状态和门禁入口，后续只补新发现的重复摩擦，不持续增长常驻说明。
- `/pr` 用于撰写 PR 正文：用最小的结构说明变更、验证证据和合并影响。它本身不执行 push、创建 PR 或合并；简单变更仍按仓库规则控制说明长度。

首轮完成术语表命名与消费者修正；用户随后授权完整升级，现已安装官方 v1.3.1 的全部 37 个技能，见 [安装验证记录](matt-skills-v1.3.1-installation-2026-10-05.md)。GitHub tracker、标签和当前单术语表布局保留。没有执行 #407，没有发起新的 GitHub issue 或 PR。

## 本次验证

- WSL/Linux 执行现有 `node scripts/check-s2-telemetry-public-contract.mjs` 通过。
- `GLOSSARY.md` 与 main 基线的原术语表逐字节一致，领域定义没有变动。
- 原术语表和原格式文件已移除；所有原已跟踪消费者均不再含旧文件路径，新格式内容与固定 v1.3.1 上游一致。
- `git diff --check` 通过；仅更新本次文件，保留用户原有 `.tmp_refs/`。
- 没有为命名变更增加永久 gate 或新测试，没有运行不相关的产品 build 或现场验收。
