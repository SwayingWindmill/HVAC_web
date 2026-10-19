# Cube 视图调研（2026-10-17）

## 看了什么

#571 让视图成为 Cube 唯一对外的部分（ADR 0023）。对照的是固定版本的镜像 `cubejs/cube:v1.6.51`（schema-compiler 1.6.51）和当时的官方文档（docs.cube.dev 的数据访问策略和视图说明）。

## 发现

在镜像里读了上游的 `@cubejs-backend/schema-compiler/dist/src/compiler/CubeValidator.js`（视图的写法：`includes`/`excludes`、`accessPolicy`、`folders`、`defaultFilters`）和 `CubeEvaluator.js`（`prepareAccessPolicy`、`prepareViewFilters`），再用 `scripts/run-analytics-cube-tests.mjs` 在镜像上验证实际行为。

- **视图和隐藏的 cube**：视图用 `cubes: [{ join_path, includes }]` 就能露出一个 `public: false` 的 cube 的字段。直接查 cube 的字段会报错「You requested hidden member」（1.6.51 里返回 HTTP 500）。
- **访问策略**：查视图时，字段级的规则只看视图自己的；行级的过滤会把视图和底下 cube 的合在一起。基础 cube 上的行规则会通过 `extends` 继承，所以租户和站点的过滤写在 `energy_facts` 上，租户不用作为视图字段。测试证明两个视图对别的站点或租户都返回空。
- **视图的 `default_filters`**：1.6.51 里有（`CubeValidator` 的 `defaultFilters`、`CubeEvaluator.prepareViewFilters`），由 Rust 查询规划器分发过滤条件。
- **视图里的指标**：视图只能转出 cube 的字段；能效这种算出来的指标要定义在 cube 上（`type: number`，引用两个指标），再由视图包含进来。

## 决定

- **采用**：视图是对外的部分，cube 设成 `public: false`；行规则写在基础 cube 上，字段的开放写在视图上。
- **改造后采用**：计入范围的划分。`site_energy_facts` 和 `equipment_energy_facts` 都继承同一个内部的 `energy_facts` cube，各自在 SQL 里固定 `accounting_scope`。不管 Cube 用哪种查询规划器都成立。
- **不采用**：用 `default_filters` 划分计入范围。范围是数据本身的事实，不是默认值；而且 1.6.51 里这条过滤路径绑在 Rust 查询规划器上，我们的部署没有开它。
- 能效只放在 `site_energy_facts` 上：供冷是按站点量的，分表没有自己的供冷量。

之后加的电费 cube `site_energy_cost_facts` 不继承 `energy_facts`，有自己同样的行规则（见 `semantic/cube/README.md`）。
