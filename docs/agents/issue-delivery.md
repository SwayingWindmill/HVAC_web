# Issue delivery workflow

用户于 2026-10-05 要求 #407 及后续 issue 遵守同一交付规范。

1. 读取 issue、父规格、评论、依赖状态、GLOSSARY 和相关 ADR；固定实施起点，已有无关工作分开提交。已有 agent-ready ticket 直接 implement，不重复 triage。
2. 涉及参考架构的决策，先核对固定官方版本的源码、测试与文档，按 AGENTS.md 记录 ADOPT/ADAPT/REJECT。产品和前端权威文档优先于历史实现。
3. 在规格已约定的公开接口上逐个行为 red → green；每个切片打通真实产品路径。测试只保护当前产品契约、安全边界、数据不变量和真实回归；使用已有 domain task matrix，不增加重复 gate。
4. 通过 WSL 在主 checkout 构建、测试和运行。涉及 Web，完成真实数据与批准桌面视口的浏览器验收；单独记录 fixture 测试和真实运行证据。
5. 对固定起点的 diff 做 Standards 和 Spec 两轴 code-review，修复发现，再交付关联 issue 的 PR，逐条列出验收证据与限制。验收未完成时不关闭 issue。

这份流程不自动授权发送消息、重置数据或合并 PR；具体授权沿用当前会话及 AGENTS.md。
