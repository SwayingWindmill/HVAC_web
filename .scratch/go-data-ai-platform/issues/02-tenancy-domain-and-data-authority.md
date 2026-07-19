# 确定租户模型、领域边界与数据权威

Type: grilling
Status: resolved
Blocked by: none
Part of: ../map.md

## Question

平台应如何定义 Organization、Project、Site、Building、System、Equipment、Device 及用户绑定关系？需要明确租户隔离边界、业务 ID 与 ThingsBoard ID 映射，以及 Go/PostgreSQL、ThingsBoard、Redis、ClickHouse、Object Storage 和 EnergyAgent 分别拥有哪些权威数据、缓存、派生数据与临时状态。

## Comments

- 租户边界采用 Organization。一个 Site 只有一个 owning organization，不允许多个组织共同拥有。
- 业主、物业运维公司、节能服务公司等其他组织通过 SiteBinding 获得授权访问；绑定记录角色、权限范围、有效期和授权来源。
- 用户可以加入多个 Organization；Project 默认是管理分组，不是独立安全边界。
- 所有业务数据必须包含 organization_id；站点级数据还必须包含 site_id。任何 X-Site-Id 都必须结合已认证主体的授权重新验证。
- Portfolio 用于区域、客户或产品线等长期管理分组；Project 表示有明确生命周期的建设、调试、节能改造、托管运营或能源审计项目。
- Site 由 Organization 直接拥有，不从属于 Project；一个 Site 可关联多个 Project，一个 Project 也可覆盖多个 Site。Project 授权仅作用于其关联范围，结束后保留历史与审计，但访问授权可以到期。
- 资产模型采用五层语义：空间层级（Site/Building/Floor/Zone）、HVAC System 关系图、Equipment 业务资产、Device IoT 端点、TelemetryPoint 标准信号。
- Equipment 是可维护、可控制、可形成工单的稳定业务资产；Device 是可连接、上报或接收 RPC 的 IoT 端点。两者通过带有效期的 DeviceBinding 建立多对多历史关系，设备更换不改变 Equipment 身份。
- HVAC System 与 Equipment 使用可表达共享母管、跨建筑和计量关系的图关系，不强制为单棵树。TelemetryPoint 通过 PointBinding 映射到 ThingsBoard Device 与原始 key；前端和 AI 只使用平台 Equipment ID 与标准点位。
- 所有平台业务对象使用平台生成的不可变 UUIDv7；ThingsBoard UUID 不作为前端 URL、数据库主键或跨服务业务标识。
- ExternalBinding 记录 integration_instance_id、provider、external_entity_type、external_id、valid_from、valid_to 和 binding_status；同一外部身份在同一集成实例内唯一。
- 平台从第一版支持多个 ThingsBoard IntegrationInstance。Site 同一时刻绑定一个主实例，可配置备用或迁移目标；迁移时关闭旧绑定并保留完整历史。无法解析绑定的事件进入隔离队列，不得进入正式遥测流。
- 数据权威划分：ThingsBoard 拥有设备连接、认证、在线状态、原始遥测与原始属性；Go/PostgreSQL 拥有组织、站点、空间、系统、资产、设备业务身份、映射规则、命令、审批、调度、告警业务状态、工单、Investigation、Finding、Recommendation 与授权关系。
- Redis 只保存可重建的最新状态、会话和短期缓存；分析型存储保存标准化遥测、聚合与派生数据，不得反向修改业务主数据；Object Storage 保存报表、Evidence 和归档大对象，PostgreSQL 保存索引与元数据。
- 外部 IAM 拥有用户主身份和密码；Go 平台保存业务用户映射、组织成员关系和授权。EnergyAgent 仅拥有运行中临时 checkpoint，长期 AI 业务状态必须写回 Go/PostgreSQL。
- ThingsBoard RPC 与设备 ACK 是命令状态机输入，Go/PostgreSQL 中的命令状态才是业务权威。所有派生结果必须可追溯到原始事件、ExternalBinding、点位映射版本和计算版本。
- 跨系统同步采用按字段单一权威，不使用通用双向覆盖或“最后写入者获胜”。ThingsBoard → Go 主要同步连接状态、原始遥测、原始属性、生命周期和 RPC 原始响应；Go → ThingsBoard 只同步平台配置、经审批的共享属性、设备命令、必要标签与业务外部 ID。
- 配置协调使用 desired_version / reported_version；不一致进入 drifted。同步状态至少包含 pending、synchronized、drifted、external_missing、conflicted、suspended。
- 同步事件必须携带来源、版本、时间戳和幂等键；删除默认采用停用和解除绑定。未映射、冲突、版本倒退和外部缺失进入隔离队列，可受控重放。跨系统更新通过 Outbox、事件和可重试协调流程完成，禁止数据库层直接双写。
- 授权模型采用 OrganizationMembership + 范围化 RoleBinding + SiteBinding 跨组织授权 + 有限 ABAC。Membership 只表示成员关系，不自动授予所有 Site 权限；显式拒绝优先于允许。
- 默认最细授权到 Site，必要时支持 Building 与 EquipmentGroup；不为几十万设备建立常规逐设备 ACL。读取遥测、管理 Registry、提议/执行/审批命令、运行分析、调用 AI 工具和读取审计必须拆成独立权限。
- Project 权限仅作用于项目关联范围，不能自动获得站点全部控制能力。AI 服务账户只能代表已认证用户在授权 Site 内调用白名单工具；权限审计必须保存主体、角色、Scope、策略版本和授权链。
- 业务对象生命周期采用 active、suspended、decommissioned、archived 等状态，不对已产生遥测、命令、工单、审计或 AI 调查的数据对象做常规物理删除。设备更换关闭 DeviceBinding，TelemetryPoint 停用后仍保留定义与映射版本。
- Site 所有权转移通过 SiteTransfer 完成，包含来源组织、目标组织、生效时间、数据范围、授权范围、状态和审批记录。转移要求双边审批，Site 平台 ID 保持不变，历史数据保留事件发生时的归属与授权快照。
- 所有权转移不得改写历史命令、审批和审计归属；原组织的历史访问权必须由 data_scope 明确。ThingsBoard 绑定迁移是独立、可回滚步骤；迁移验证完成前默认禁止高风险命令。
- 租户隔离采用可升级的混合模型：默认共享服务、PostgreSQL、消息和分析基础设施，以 organization_id / site_id 实施强逻辑隔离；大型、高合规或高噪声租户可升级到独立数据库或 Schema、消息 Namespace、分析资源组乃至独立部署单元，外部契约保持一致。
- PostgreSQL 所有租户表必须包含 organization_id，数据访问层强制注入租户上下文，并以 Row-Level Security 作为第二道防线；跨租户查询只允许受审计的内部管理身份。Redis Key、Object Storage 前缀、分析分区与查询条件均必须包含租户维度。
- 消息事件必须携带不可变 organization_id 与 site_id，不得由名称反推租户；禁止跨租户事务。隔离测试必须覆盖数据库越权、缓存串租户、消息错路由、对象存储越权和 AI 工具跨 Site 访问。

## Answer

平台以 Organization 作为租户和默认数据隔离边界，Site 采用单一所有者、多组织 SiteBinding 授权访问。Portfolio 用于长期分组，Project 用于有生命周期的业务项目，Site 独立存在并可关联多个 Project。资产模型由空间层级、HVAC System 关系图、Equipment、Device 与 TelemetryPoint 构成，平台 UUIDv7 与 ThingsBoard 外部身份通过可追溯 ExternalBinding 解耦，并从第一版支持多个 ThingsBoard 实例。

ThingsBoard 是设备连接、在线状态、原始遥测和原始属性的权威来源；Go/PostgreSQL 是业务主数据、权限、命令、审计和 AI 业务状态的权威来源；Redis 只做可重建缓存，分析存储只保存标准化副本与派生数据，EnergyAgent 只拥有运行中临时状态。跨系统同步实行单字段单一权威和 desired/reported 协调，禁止通用双向覆盖与数据库直接双写。

授权采用 OrganizationMembership、范围化 RoleBinding、SiteBinding 和有限 ABAC；业务对象默认软删除或归档，Site 所有权通过双边审批、带生效时间且保留历史归属的 SiteTransfer 完成。基础设施默认共享但实施全链路强逻辑隔离，并保留大客户升级为独享数据库、消息、分析资源或部署单元的能力。
