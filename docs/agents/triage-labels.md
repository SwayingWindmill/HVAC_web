# 分诊标签

技能用五种分诊角色来描述一张问题单处在什么状态。下表把这些角色对应到本仓库问题单里实际用的标签。

| mattpocock/skills 里的角色 | 本仓库的标签 | 含义 |
| --- | --- | --- |
| `needs-triage` | `needs-triage` | 需要维护者看一下、决定怎么处理 |
| `needs-info` | `needs-info` | 等提出人补充信息 |
| `ready-for-agent` | `ready-for-agent` | 已经写清楚，agent 可以无人值守地做 |
| `ready-for-human` | `ready-for-human` | 需要人来做 |
| `wontfix` | `wontfix` | 不做 |

技能提到某个角色时（例如「打上可以交给 agent 的标签」），用表里对应的标签。

本仓库的标签和角色名一样；以后改用别的标签时，改右边那一列。
