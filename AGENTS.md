# AGENTS.md

商用 HVAC 智能节能平台，第一个场景是冷站。React SPA + Go 后端，第一阶段部署在一台 Linux 服务器上（Docker Compose）。这里是在本仓库写代码和文档的规则；和代码冲突时以代码为准。

## 先读什么

- `docs/HANDOFF.md`：现在做到哪、下一步、服务器和本地环境的特殊情况。接手工作先读它。
- `GLOSSARY.md` 和 `docs/adr/`：业务名词和架构决定。动一个领域的代码前，读相关的名词和决定。
- `docs/specs/`：每个功能做什么、不做什么、怎么验收。
- `DESIGN.md`：页面的设计系统。`PRODUCT.md`：用户是谁、要完成什么、哪些安全边界不能碰。
- 改本地环境、查一个服务的配置、用开发服务器连本地平台时，读 `docs/agents/local-environment.md`。

## 仓库结构

- `apps/hvac-web/`：唯一的网页
- `apps/hvac-miniprogram/`：值班人员用的微信小程序（原生小程序 + TypeScript，见它的 README）
- `cmd/`：可执行进程。`energy-api` 是平台网关，`telemetry-worker` 接收读数、实时推送、写历史、把电表读数算成用电量
- `modules/`：各业务领域的代码和数据表，每类业务数据只由一个模块（它的负责方）修改；`services/`：单独部署的服务；`libs/`：共享库
- `contracts/`：接口定义和「哪类数据归哪个模块」的登记；`infra/`：数据库迁移；`deploy/platform/phase1/`：Compose、迁移清单、镜像
- `tools/eg8200-simulator/`：冷站模拟器，本地实时数据的来源

## 命令

所有项目命令都在 WSL 里、在 `/mnt/e/Code/HVAC_web`（Windows 侧是 `E:\Code\HVAC_web`）执行：Node/npm、Go、Docker、浏览器测试、代码生成。代码和 CI 只面向 Linux，Windows 侧只用来编辑文件和 `git push`。

```bash
npm -w @hvac/web run typecheck
npm -w @hvac/web run build
npm run web:shell:test && npm run web:model:test         # 网页的单元测试
npm run web:e2e                                          # 浏览器测试：录制的网关数据，不需要后端
(cd modules/telemetry && go vet ./... && go test ./...)   # go.work 多模块，按模块执行
npm run go:lint && npm run security:go-vuln              # 改了 Go 代码或依赖后：静态检查、漏洞扫描
npm run deployment:phase1:check                          # 改了 Compose 或迁移清单后
```

- 交 PR 前，把 `scripts/domain-task-matrix.mjs` 里和改动相关的命令在本地跑一遍。
- 新增或移动 `routes/` 下的文件后，跑一次 dev 或 build，更新生成的 `routeTree.gen.ts`。
- 浏览器测试的接口数据在 `apps/hvac-web/e2e/fixtures/`（从本地环境录制、已脱敏），写操作由 `e2e/gateway.ts` 按负责那类数据的模块的规则模拟。页面改了接口调用，同步改测试桩。
- 日志里的 `error` 字段一律显示为 `[REDACTED]`。要看真实错误，写一个直接调用那个 Go 函数的测试，或临时把错误记在带 `[DEBUG-xxxx]` 前缀的字段里，查完删掉。
- 从 Windows 调 WSL 时，多行或带嵌套引号的命令先写成脚本文件，再 `wsl -e bash -lc "bash <脚本路径>"`；偶发 `WSAETIMEDOUT` 时重试。
- `deploy/platform/phase1/runtime/**` 里是密钥和凭据：只按路径引用，不打印、不提交、不改权限。

## 写代码

- **删掉旧路径。** 功能改了，旧的代码路径直接删，不留兼容层、回退或迁移代码。
- **从最小的可用版本做起。** 先做一个从头到尾能用的版本，再一层层加；每一步之后产品都还能用。
- **只写现在需要的。** 目录和抽象只描述已经存在的复杂度；将来的需求等它真的来了再加。
- **做长期的决定。** 选一个经得起时间的方案，不做「先凑合、以后再换」。
- **先用已有依赖和成熟的库。** 先看项目里已有的依赖能不能做，查它的文档和类型再下结论；确实要新加时，选维护良好的，固定版本。
- **参照外部实现时读源码。** 设备接入、遥测、告警、资产、报表、权限、能源管理这类功能，动手前看 ThingsBoard、OpenEMS、MyEMS 的实际代码（固定版本的记录在 `docs/architecture/references/`）。借鉴时固定版本和提交，读源码、测试和官方文档，在 `docs/architecture/` 里简短记下采纳、改造或拒绝了什么、为什么。已有代码和参照实现冲突、又说不出更好的理由时，按参照实现改。

## 测试

- 测试守住约定：接口和数据格式、安全和数据规则、权限边界、用户看得到的行为，以及出现过的回归。覆盖率、实现细节和简单的 getter 不是测试的对象。
- 每条要求用最少的、直接测行为的测试证明。
- 约定变了（例如接口少了一个字段），就改或删旧测试，让代码保持干净。

## 防御和报错

- 校验、重试、默认值和兜底只放在真实会失败的地方：信任边界、外部 I/O、并发、数据损坏、安全攸关的规则。类型、schema、数据库约束或上游接口已经排除的情况，信任它们。
- 失败要明确报出来：抛出或返回错误，让调用方和用户看到错误状态。

## 门禁（CI 检查）

- 一条规则用一个最小的检查守住，只跑和改动相关的领域。
- 门禁只运行 `scripts/domain-task-matrix.mjs` 里登记的命令，集成脚本只跑它用 `-run` 点名的 Go 测试。新检查要登记进去才会运行；不会运行的检查会悄悄过时，删掉它。
- 检查只看代码和配置，文档的措辞靠审查。

## 网页和数据库

- **改网页**前读 `apps/hvac-web/README.md`「约定」（技术栈、状态归属、组件、只显示真实数据、1440px 真实数据验收）和 `DESIGN.md`。
- **改表结构**前读 `deploy/platform/phase1/README.md`「数据库和迁移」：新增迁移要登记到三个文件；迁移已经冻结，登记过的迁移不能再改，改表结构只能新增迁移。

## 写文档

- 用中文和具体的大白话：说清楚谁会看到什么、哪个数字会变，用事情本身代替抽象名词（写「把电表读数算成用电量」，而不是「投影」）；讲规则时给一个例子，例如「日期按站点本地日期，首尾都算：10 月 13 日到 16 日就是四整天」。
- 业务概念用 `GLOSSARY.md` 里的词；专业词第一次出现时用一句话解释，或换成日常说法；代码里的名字（函数、字段、命令、路径）照原样写。
- 只写现在成立的事：过时的直接改掉或删掉，历史留在 git 里。状态写「已采纳」「已实现」「还没做」这类现在的状态；阶段代号（S0–S5）和「目标态」「候选」这类标签换成具体的现状。
- 调研和参照比对的记录，只在被现行的 ADR、规格、HANDOFF 或代码引用时保留。
- 新的架构决定只在难以撤回、不看背景会让人意外、又确实在几个方案里做了取舍时写 ADR；格式和名词的维护按 `domain-modeling` 技能。
- 用户看得到的变化记进 `CHANGELOG.md`，当前状态和下一步记进 `docs/HANDOFF.md`，都随 PR 一起更新。

## 交付和 Git

- 每张问题单按 `docs/agents/issue-delivery.md` 交付。
- `main` 只通过 PR 合并。每项改动一个分支，用 Conventional Commits，提交说明写清楚原因。
- 文本文件统一 LF（见 `.gitattributes`）；构建产物和二进制留在仓库外。
- `git push` 用 Windows 侧的 git（WSL 访问 GitHub 有 TLS 问题）；`gh` 用 WSL 里已登录的那份。
