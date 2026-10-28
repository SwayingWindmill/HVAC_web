# Go 代码检查：参照 samber/cc-skills-golang 的取舍

参照：`github.com/samber/cc-skills-golang`，commit `246b6c4`（2026-10-23），读的是 `skills/golang-lint`、`skills/golang-security`、`skills/golang-continuous-integration` 三份 `SKILL.md`。它们是写给 AI 编程助手看的 Go 写法规则，本身不检查代码。

落到本仓库的是三项门禁（#707）：

| 检查 | 命令 | 在哪个门禁跑 |
| --- | --- | --- |
| 静态检查 | `npm run go:lint`（`scripts/run-go-lint.mjs`，golangci-lint v2.14.0，规则在 `.golangci.yml`） | 单元门禁的 `go` 一项：PR 改了 `.go`、`go.mod`/`go.sum`、`go.work` 或这些检查本身时，检查全部 55 个模块 |
| 依赖和标准库漏洞 | `npm run security:go-vuln`（govulncheck v1.9.0） | 同上 |
| 数据竞争 | 单元门禁里的 Go 测试都带 `-race`（`.github/workflows/pr-gates.yml` 的 `GOFLAGS`） | 单元门禁，按改动的领域 |

## 采纳

- **每个仓库一份 `.golangci.yml`，作为开了哪些检查的唯一依据。** 放在根目录，`go.work` 里的 55 个模块都用它。
- **CI 上跑 golangci-lint、govulncheck 和 `go test -race`。** 规则里把这三项列为 Go 项目 CI 的基本项。
- **`//nolint` 必须写明检查名和理由。** 目前代码里一个都没有；以后要加，照这个写。

## 改造

- **只开 golangci-lint 默认的 5 项**（errcheck、govet、ineffassign、staticcheck、unused），没有用它推荐的 33–48 项。`AGENTS.md` 要求一条规则一个最小的检查；先开这 5 项，每一项都在现有代码里找到过真问题：没人调用的函数 21 处，赋值后没用到 5 处，Go 1.26 起废弃的写法 4 处。
- **不检查 `Close`、`Flush`、`os.Remove`、打印和事务 `Rollback` 的返回值**（golangci-lint 的 `std-error-handling` 预设，加上 pgx 和 `database/sql` 的 `Rollback`）。2026-10-26 逐个看过被排除的 186 处：`Rollback` 99 处，要么是提交后的 `defer`（一定返回「事务已关闭」），要么在已经返回原错误的出错分支；`response.Body.Close` 68 处；`file.Close` 5 处，都是只读打开的文件，或写失败后已经返回写错误的分支（成功分支检查了 `Close`）；其余是指标输出和命令行打印。没有一处丢了该处理的错误。
- **工具按 `go.work` 里写的 Go 版本来编译。** golangci-lint 和 govulncheck 都读不了比编译它们的 Go 更新的代码；两个脚本都用 `GOTOOLCHAIN=go<go.work 的版本>` 安装，升级 Go 时工具会跟着重新编译。
- **`repo:check` 的 `gofmt` 也用这一版，不走 PATH。** 系统里的 `gofmt` 可能是更旧的 Go。Go 1.27 改了一次返回多个值、每个值都是复合字面量时的缩进（`modules/connectivity/pkg/adapter/broker_test.go` 的 `writeTestPKI`）：旧版 `gofmt -l` 认为已经格式化，CI 上的 Go 1.27.3 会报未格式化。检查时设 `GOTOOLCHAIN=go<go.work 的版本>`，再跑 `go env GOROOT`，用那个目录里的 `bin/gofmt`。
- **只在改了 Go 的 PR 上跑，每次扫全部模块。** 改的是哪个模块不重要：一个依赖升级会影响所有模块，`.golangci.yml` 一改也要全部重查。只改网页或文档的 PR 不跑。
- **漏洞扫描逐个模块扫。** govulncheck 不支持在没有根 `go.mod` 的工作区里一次扫完，所以脚本按 `go.work` 列出的模块挨个运行。

## 不采纳

- **`issues.new-from-rev`（只查新改的代码）**：现有问题这次全部改掉了，不需要。
- **`samber/lo`、`samber/mo`、`samber/do`、uber-fx、wire、testify 等库的规则**：本仓库不用这些库，`AGENTS.md` 要求先用已有依赖。
- **把整套规则装进项目，每次改 Go 代码都加载多份**：会占很多上下文，其中一些写法（多写文档注释、多加防御性检查）和 `AGENTS.md` 冲突。
