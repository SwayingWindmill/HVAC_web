# IAM 角色模型与用户管理参考比对（2026-10-14）

服务于 `docs/specs/iam-roles-and-users.md`。目标机器是 2 核、3.7 GB 内存的单台服务器，上面同时跑着 ThingsBoard；引入任何组件都不能明显增加内存和运维负担。

## 参考版本

| 来源 | 固定版本 | 读过的内容 |
| --- | --- | --- |
| Kubernetes RBAC | `kubernetes/website` `release-1.31`，`content/en/docs/reference/access-authn-authz/rbac.md` | Role/ClusterRole、RoleBinding/ClusterRoleBinding、默认面向用户的角色 |
| Grafana RBAC | `grafana/grafana` `v11.2.2`（服务器上正在运行的版本），`docs/sources/administration/roles-and-permissions/` | 基础角色、固定角色、权限 = 动作 + 范围 |
| Keycloak | `keycloak/keycloak` `26.0.0`，`docs/documentation/server_admin/topics/users/` | 管理员设定密码、临时密码与必需动作 |
| NIST SP 800-63B-4 | 2025 年 8 月终版，§3.1.1.2 Password Verifiers | 长度、组合规则、黑名单、定期更换 |

## 采纳、改造与拒绝

- **ADOPT**：Kubernetes 的「角色 + 指派（带范围）」。原文写明权限是纯加法：“Permissions are purely additive (there are no "deny" rules).” RoleBinding 可以引用 ClusterRole，把它限定到一个 namespace。对应关系：内置角色 ≈ ClusterRole，整租户指派 ≈ ClusterRoleBinding，站点指派 ≈ 限定到 namespace 的 RoleBinding。
- **ADOPT**：Grafana 的少量内置基础角色（Viewer/Editor/Admin），内容由产品定义，覆盖绝大多数用户。
- **ADAPT**：Grafana 和 Kubernetes 都有自定义角色和细粒度范围（如 `teams:id:1`）。第一版只开放站点这一级范围，不做自定义角色，也不做设备或点位级授权；需要时再加。
- **ADAPT**：保留已有的显式拒绝表。Kubernetes 和 Grafana 都没有拒绝规则，但这张表本身已按「权限 + 可选站点」统一，删除它要改动下游授权凭证的契约，收益不大；界面上不暴露。
- **ADOPT**：Keycloak 的「管理员在后台直接设定密码」。用户确认不要临时密码，所以不采用 Keycloak 的 *Temporary* 开关和 *Update Password* 必需动作。
- **ADAPT**：NIST 要求只用密码登录时至少 15 位（§3.1.1.2：“SHALL require passwords that are used as a single-factor authentication mechanism to be a minimum of 15 characters”）。用户决定至少 8 位（NIST 对多因素场景的下限）。其余照 NIST：不要求字符组合，不强制定期更换，拒绝常见弱密码，最长至少 64 位（现有上限 512）。
- **REJECT**：Keycloak 作为身份服务。它跑在 JVM 上，在这台机器上要占用数百 MB 内存，还会和现有的 OIDC 身份服务、IAM 的租户与站点模型重复。
- **REJECT**：Zitadel、Casdoor、Logto 等完整身份平台。每个都要再加一个常驻服务，并自带一套管理控制台和组织/项目/授权模型，与本仓库的 IAM 重叠，还要做数据迁移。
- **REJECT**：OpenFGA、SpiceDB 这类 Zanzibar 式授权服务。我们的关系只有租户 → 站点两层，为此多跑一个服务和一套建模语言不划算。
- **REJECT**：Casbin。它是进程内库，没有负载问题；但它的「带域的 RBAC」模型和我们要写的判定（约 100 行）等价，引入后还要维护一份 Casbin 模型文件和数据库适配器，等于多了一种策略语言，而行级安全、修订号和审计这些仍要自己做。
