---
date: 2026-11-23
category: 改进
---

- **收敛云边告警表达式求值内核至 `libs/alarmexpr`**：抽取共享词法分析、递归下降 AST 解析、复杂度与嵌套深度上限校验、静态类型检查与求值引擎至无外部依赖共享包 `libs/alarmexpr`，替换云端 `modules/alarm/pkg/alarmservice/expression.go` 与边缘重复实现并删除 `libs/edgecontrol/edge_expression.go`；通过 `EvalModeThreeValued` 与 `EvalModeTwoValued` 显式区分 Kleene 三值吸收与边缘前置质量/新鲜度确定性二值短路，补充属性/模糊/云边对拍测试并接入 PR 域门禁与静态漏洞扫描。
