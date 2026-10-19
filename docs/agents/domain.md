# 业务文档

工程类技能在看代码之前，怎样使用本仓库的业务文档。

## 看代码前先读

- 根目录的 **`GLOSSARY.md`**（业务名词）。如果根目录有 **`GLOSSARY-MAP.md`**，它会指向每个领域各自的 `GLOSSARY.md`，读和当前主题相关的那几份。
- **`docs/adr/`**：读和要动的地方相关的架构决定。多领域的仓库里，还要看 `src/<领域>/docs/adr/` 里只属于那个领域的决定。

这些文件不存在时，**直接继续，不用提**：不要指出它们缺了，也不要建议先建。等名词或决定真的定下来时，`/domain-modeling` 技能（经 `/grill-with-docs` 和 `/improve-codebase-architecture` 进入）会再建它们。

## 文件结构

单一领域的仓库（大多数是这样，本仓库也是）：

```
/
├── GLOSSARY.md
├── docs/adr/
│   ├── 0001-event-sourced-orders.md
│   └── 0002-postgres-for-write-model.md
└── src/
```

多领域的仓库（根目录有 `GLOSSARY-MAP.md`）：

```
/
├── GLOSSARY-MAP.md
├── docs/adr/                          ← 整个系统的决定
└── src/
    ├── ordering/
    │   ├── GLOSSARY.md
    │   └── docs/adr/                  ← 只属于这个领域的决定
    └── billing/
        ├── GLOSSARY.md
        └── docs/adr/
```

## 用术语表里的说法

产出里提到业务概念时（问题单标题、重构提议、猜测的原因、测试名），用 `GLOSSARY.md` 里定义的那个词，不要换成术语表明确列为「不要说」的同义词。

需要的概念术语表里还没有，这本身就是个信号：要么是在发明项目不用的说法（重新考虑），要么是真的缺了（记下来交给 `/domain-modeling`）。

## 和 ADR 冲突要说出来

产出和已有的 ADR 冲突时，明确指出来，不要悄悄推翻：

> _和 ADR-0007（订单用事件溯源）冲突，但值得重新讨论，因为……_
