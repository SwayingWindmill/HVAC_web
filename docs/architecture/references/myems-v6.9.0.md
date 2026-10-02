# MyEMS v6.9.0 源码参考评审

- 版本：MyEMS tag `v6.9.0`，commit `b360f5b`（"Merge branch 'develop'"），本地浅克隆，只读。
- 依据：只看源码、安装 SQL 和仓库内 README；文中路径都相对于 MyEMS 仓库根，`:N` 表示行号。
- 目的：给本平台（Go + TypeScript，PostgreSQL + ClickHouse，冷站优先，第一阶段单机 Compose）的架构评审提供一手对照，重点是能耗核算和报表。
- 说明：标为"推断"的是从代码结构推出来的结论，没有运行验证。

---

## 1. 进程与部署拓扑

**服务清单**（`README.md:130-162`、`database/README.md:23-31`）：
`myems-api`（Python Falcon + gunicorn）、`myems-admin`（AngularJS 管理端，`myems-admin/js/angular/`）、`myems-web`（React 16 用户端，`myems-web/package.json:53`）、`myems-modbus-tcp`、`myems-cleaning`、`myems-normalization`、`myems-aggregation`。仓库里另有 `myems-bacnet`、`myems-opc-ua`、`myems-s7` 采集服务，结构与 modbus-tcp 相同，但没有放进 compose。

**Compose**（`others/docker-compose-on-linux.yml:1-49`）：7 个容器（api、aggregation、cleaning、modbus_tcp、normalization、admin、web），**没有 MySQL 容器**。数据库在外部，各服务通过 `.env` 里的连接串直连。另有一个"一体镜像"（`others/Dockerfile:1-17`、`others/entrypoint.sh`），把 nginx 和 gunicorn 塞进同一个容器。

**通信方式：只靠共享 MySQL，没有消息总线、RPC 或事件。**
- 13 个库按用途拆分（`database/README.md:35-51`）：system（元数据）、historical（原始点值）、energy、billing、carbon、energy_baseline、energy_model、energy_plan、energy_prediction、fdd、user、reporting、production。
- 每个服务同时连多个库：modbus-tcp 读 system 库的点表，写 historical 库（`myems-modbus-tcp/config.py:9-28`）。API 甚至做跨库 JOIN，例如 `myems_historical_db.tbl_analog_value_latest JOIN myems_system_db.tbl_points`（`myems-api/reports/pointrealtime.py:116-121`）。
- 服务之间的顺序靠"水位线"串起来：下游查 `MAX(start_datetime_utc)`，决定从哪一小时继续算（见第 3 节），没有显式依赖或触发。

**调度：每个子任务都是一个死循环加 `time.sleep`。**
- cleaning：3 个 `multiprocessing.Process`（`myems-cleaning/main.py:55-61`）；能耗值清洗每 900 秒跑一轮（`myems-cleaning/clean_energy_value.py:691-693`）。
- normalization：5 个 Process（`myems-normalization/main.py:61-73`），分别是 meter、offline meter、virtual meter、virtual point、data repair。meter 每轮之间睡 60 秒，每轮内部用 `Pool(config.pool_size)` 并行（`myems-normalization/meter.py:122-133`，默认 `POOL_SIZE=5`，`myems-normalization/config.py:73`）。
- aggregation：`main.py` 里有 **42 个** `Process(target=...)`（`myems-aggregation/main.py:104-221`）。对象类型 × {energy, billing, carbon} × {input/output, category/item} 的每种组合各占一个进程，每个进程再开 Pool，每轮睡 300 秒（`myems-aggregation/space_energy_input_category.py:117-128`）。

**扩展方式**：
- 采集端可以按网关横向扩展：`tbl_data_sources.gateway_id` 加上 `GATEWAY_ID/GATEWAY_TOKEN`，决定一个采集实例负责哪些数据源（`myems-modbus-tcp/main.py:91-95`，`myems-modbus-tcp/config.py:43-46`）。每个数据源对应一个 OS 进程（`myems-modbus-tcp/main.py:170-171`）。
- 处理端只能单实例运行，靠调大 Pool 提升吞吐。代码里没有分片和锁；如果多实例同时跑，会按同一条水位线重复插入（表上没有唯一约束，见第 8 节）。
- API 是无状态的 gunicorn 4 workers（`others/docker-compose-on-linux.yml:5`），可选 Redis 报表缓存，TTL 30 分钟（`myems-api/reports/equipmentefficiency.py:220-271`）。

---

## 2. 数据采集

**配置模型**：
- `tbl_data_sources`（`database/install/myems_system_db.sql:248-267`）：字段有 `protocol` 和 `connection`（JSON，例如 `host/port/interval_in_seconds`），还有 `process_id` 和 `last_seen_datetime_utc`。
- `tbl_points`（`myems_system_db.sql:1763-1792`）：
  - `object_type` 取 `ANALOG_VALUE`、`DIGITAL_VALUE`、`ENERGY_VALUE`（采集端只认这三种）。
  - `ratio/offset_constant` 做线性换算，`high_limit/low_limit` 用于清洗，`higher_limit/lower_limit/is_in_alarm` 留给 FDD。
  - `is_trend` 决定是否写历史，`is_virtual` 标记虚拟点。
  - `address` 是协议相关的 JSON。Modbus 用 `slave_id/function_code/offset/number_of_registers/format/byte_swap`（`myems-modbus-tcp/acquisition.py:255-266`）；BACnet 用 `object_type/object_id/property_name/property_array_index`（`myems-bacnet/acquisition.py:193-205`）。

**Modbus 轮询流程**（`myems-modbus-tcp/acquisition.py`）：
1. 先用 telnet 探测 TCP 端口，失败后睡 300 秒（`:130-138`）。
2. 从 system 库读这个数据源的点表（`:163-168`）。**点表只在重连时重新加载**，代码自己也标了 TODO（`:243`，以及 `main.py:69` 写着"This service has to RESTART to reload latest data sources"）。
3. **每个点单独发一次 `master.execute`**（`:275-279`），不合并相邻寄存器的读请求。只要有一次超时，就放弃这一整轮，睡 60 秒后重连（`:290-293`、`:357-376`）。其他异常跳过当前点，继续读下一个（`:294-297`）。
4. 只做两项校验：值是 NaN 就丢弃，超出 DECIMAL 范围也丢弃（`:309-315`、`:337`）；然后 `value*ratio+offset`。
5. **时间戳是整批写库时的 `datetime.now()`，不是每个点的实际读取时刻**（`:414`）。
6. 按 100 条一批，用字符串拼接出 SQL 写入（`:418-433`）；latest 表用"先 DELETE 再 INSERT"更新，两步分别提交，不在同一事务里（`:446-475`）。写库失败只记日志，"Ignore this exception"（`:441-443`），数据直接丢掉，没有本地缓冲或重试。
7. 每轮结束更新 `tbl_data_sources.last_seen_datetime_utc`（`:600-605`）。另有一个网关心跳进程，每 3 分钟更新 `tbl_gateways.last_seen_datetime_utc`（`myems-modbus-tcp/gateway.py:98-101`、`:128-132`）。

**BACnet**：一轮把所有点都读一遍，用 bacpypes 应用对象逐个发请求。DIGITAL 值把 `'active'/'inactive'` 映射成 1/0（`myems-bacnet/acquisition.py:300-305`）。这个服务没有 offset，只乘 ratio（`:283-298`）。

**存储**（`database/install/myems_historical_db.sql`）：
- 按值类型分表：`tbl_analog_value`、`tbl_digital_value`、`tbl_energy_value`、`tbl_text_value`，每张表各配一张 `*_latest`（`:15-147`）。
- 列有 `point_id, utc_date_time, actual_value DECIMAL(21,6), is_bad`。索引是 `(point_id, utc_date_time)` 和 `(utc_date_time)`，**没有唯一约束，也没有分区**。
- 保留期：cleaning 删除 365 天之前的 analog/digital 值（`myems-cleaning/clean_analog_value.py:49-53`，`LIVE_IN_DAYS`，`myems-cleaning/config.py:42`）。energy 值永远不删，只打 `is_bad` 标记（`clean_energy_value.py:4-6`）。

**过期数据（stale）**：没有显式的质量位，也没有 stale 状态。实时接口直接把"latest 时间早于 60 分钟前"的点过滤掉（`myems-api/reports/pointrealtime.py:66-67`、`:118-121`），所以前端看到的是这个点不存在，而不是标成过期。

---

## 3. 数据处理流水线（批处理，按小时）

### 3.1 cleaning（只对 energy 值打标）
- 处理区间：从 `MAX(utc_date_time) WHERE is_bad IS NOT NULL` 往前回退 1 小时，到 `MAX(utc_date_time) WHERE is_bad IS NULL` 为止（`myems-cleaning/clean_energy_value.py:91-117`）。
- 第一类坏值：超出点的 `high_limit/low_limit`（`:261-330`）。
- 第二类坏值是"凹形"：累计值先跌破基准、之后又回升，中间这段标为坏值（`:487-575`）。如果跌下去以后一直没回升（例如换表、表计清零），尾段候选值不会被标记，这时依赖 normalization 的"初值 ≤0.1 就丢弃"来兜底。
- 其余没被标记的值置为 `is_bad=0`（`:665-675`）。

### 3.2 normalization（累计读数 → 小时增量）
- meter 的水位线：`MAX(start_datetime_utc) FROM tbl_meter_hourly`，加一个时段后作为起点。终点是 `now - minutes_to_clean`（默认 30 分钟，给清洗留时间），再对齐到整时段（`myems-normalization/meter.py:186-229`、`config.py:53-58`）。
- 增量算法：只读 `is_bad = 0` 的值（`:269-299`），在时段内对单调上升的部分求和（`:420-447`）。几条重要的规则：
  - 时段内一条数据都没有（离线或全是坏值），**每个小时写 0**（`:398-405`）。这样"缺数"和"零耗"就分不清了。
  - 离线恢复之后，累计的跳变会整体落进恢复的那一小时；超过 `hourly_high_limit` 就**整小时置 0**（`:455-459`）。代码注释自己承认"may cause the loss of energy consumption"。
  - 初始最大值 ≤0.1（新表或者从 0 值恢复）时，该小时也置 0（`:449-450`）。
  - 低于 `hourly_low_limit` 的值置 0（`:452-453`），用来压掉表计精度噪声。
- virtual meter：公式存在 `tbl_virtual_meters.equation`，变量通过 `tbl_variables` 绑定到 meter、virtual_meter 或 offline_meter（`myems_system_db.sql:2738-2774`），用 SymPy `sympify` 求值（`myems-normalization/virtualmeter.py:29`、`:454-466`）。求值只在所有依赖都有数据的公共时间区间里进行（`:405-451`）。
- offline meter：通过 Excel 上传日值，按 24 小时**平均分摊**到每小时，先删后插 `tbl_offline_meter_hourly`（`myems-normalization/offlinemeter.py:252-320`）。
- virtual point：用 SymPy 对其他点的 latest 值求表达式（支持 piecewise），结果写回 historical 库（`myems-normalization/virtualpoint.py:1-20`、`:641-674`）。
- data repair：上传 Excel 后，按点和时间段先删后插原始 `tbl_energy_value`（`myems-normalization/datarepair.py:314-330`）。**推断**：已经算出来的 `tbl_meter_hourly` 不会因此重算，因为水位线只往前走。

### 3.3 aggregation（对象 × 分类 / 分项 × 能耗 / 费用 / 碳排）
- 以 space 为例（`myems-aggregation/space_energy_input_category.py`），输入包括：
  - 直接挂在 space 上的 meter、virtual meter、offline meter，只取 `is_counted=1` 的（`:194-202`）；
  - 下挂的 combined equipment、equipment、shopfloor、store、tenant，读它们已经聚合好的小时表；
  - 子 space（`parent_space_id`，`:447-460`）。
  - 求和之后写入 `tbl_space_input_category_hourly`。
- 水位线是自身表的 `MAX(start_datetime_utc)`（`:515-536`），终点是 now。真正写入的区间是**所有输入的公共区间**（`:911-1000`），也就是只要有一个输入滞后，整个对象就停在那里不动。层级深的父对象要等子对象先算完，每一层还要再等一个 300 秒周期。
- billing：`hourly energy × 当小时单价`（`myems-aggregation/meter_billing.py:250-271`）。单价来自 `tariff.get_energy_category_tariffs()`：
  - 先按 `cost_center` 关联到 tariff，再按 `valid_from/through` 截取有效期，然后按 `tbl_tariffs_timeofuses` 的 `start/end_time_of_day` 展开成逐小时价格（`myems-aggregation/tariff.py:70-160`）。
  - peak_type 有尖、峰、平、谷、深谷五档（`myems_system_db.sql:2303-2319`）。
  - **本地时间换算用的是全局 `UTC_OFFSET`（默认 +08:00）**（`tariff.py:54-56`、`myems-aggregation/config.py:83`），不用 space 自己的 `timezone_id`。
  - 时段规则不区分工作日、节假日；季节电价只能靠多条有效期不同的 tariff 拼出来。阶梯和两部制（需量电费）在这个模型里没有位置。
- carbon：优先用按 cost center 绑定、支持分时的排放因子（`tbl_emission_factors`、`tbl_emission_factors_timeofuses`），没有就回退到 `tbl_energy_categories.kgco2e` 这个静态值（`myems-aggregation/carbon_dioxide_emission_factor.py:4-44`）。

### 3.4 延迟与重算
- 端到端延迟（**推断**）：一小时结束后，要再等 `minutes_to_clean`（30 分钟），normalization 每 60 秒一轮，aggregation 每层 300 秒一轮，所以对象级小时能耗通常要晚 35–60 分钟以上才出来，层级越深越晚。没有任何流式或增量路径。
- 迟到数据或修正数据：**没有自动重算**。所有阶段都是"水位线只进不退"。官方做法是先停掉 normalization 和 aggregation，手工执行 `database/recalculate/batch-delete.sql`，按时间删除各个 hourly、billing、carbon 表，再启动服务让它们从头追算（脚本头部注释说明了这个顺序，`database/recalculate/batch-delete.sql:1-8`）。

---

## 4. 领域与元数据模型（`database/install/myems_system_db.sql`，共 163 张表）

- **计量三分**：
  - `tbl_meters`（实体表，`:1082-1106`）：有 `energy_category_id`、`energy_item_id`、`cost_center_id`、`is_counted`、`hourly_low/high_limit`、`master_meter_id`（主表-子表树，供 `metersubmetersbalance` 报表使用），通过 `tbl_meters_points` 绑定 energy 点。
  - `tbl_virtual_meters`（公式表）。
  - `tbl_offline_meters`（人工录入，`:1469-1491`）。
  - 三类表在 space、equipment、tenant 等关系表里各有一张关联表（例如 `tbl_spaces_meters`、`tbl_spaces_virtual_meters`、`tbl_spaces_offline_meters`）。
- **能源分类 / 分项**：`tbl_energy_categories` 带 `unit_of_measure`、`kgce`（折标煤系数）、`kgco2e`（`:316-330`）；`tbl_energy_items` 隶属于某个 category（`:331-343`），对应国内"分类分项计量"。
- **对象层级**：
  - `tbl_spaces` 是带 `parent_space_id` 的树，有 `area`、`number_of_occupants`、`timezone_id`、`cost_center_id`（`:2016-2050`），根节点 id=1 是硬编码的。
  - 在 space 之下挂 equipment、combined_equipment、tenant（带租约字段，`:2501-2527`）、store、shopfloor，都通过多对多关联表挂接。
- **设备的输入 / 输出**：`tbl_equipments_meters.is_output`（`:782-794`）区分输入能源（电、气）和输出能源（冷量、热量）；equipment 和 combined_equipment 上有 `is_input_counted/is_output_counted/efficiency_indicator`（`:45-64`、`:737-756`）。这是 MyEMS 里最接近冷站 COP 的建模方式：把冷机电表设为 input，冷量表（热量表）设为 output，combined equipment 就是"冷站"。
- **设备参数**：`tbl_equipments_parameters` 支持 `constant`、`point`、`fraction` 三种类型；`fraction` 由分子表 uuid 和分母表 uuid 定义一个比值 KPI（`:809-825`）。
- **成本中心与电价**：`tbl_cost_centers`（带 `external_id` 对接 ERP）通过 `tbl_cost_centers_tariffs` 绑定 tariff（`:223-247`）。电价跟着成本中心走，不跟表走。
- 其他：`tbl_working_calendars` 加非工作日（与 space、tenant 等关联，`:2849-2861`），`tbl_energy_flow_diagrams`（能流图节点和连线），`tbl_distribution_systems/circuits`（配电系统图）。

**对 HVAC 能耗核算有价值的部分**：实体表、虚拟表、离线表三类统一进入小时表；category/item 双维度；`is_counted` 防止重复计入；输入/输出分离并且可以用比值表达效率；电价挂在成本中心上并带有效期；kgce 和 kgco2e 换算放在 category 上。

---

## 5. 报表 / KPI

`myems-api/reports/` 里有 197 个文件，共 103,440 行。命名规则是 {对象} × {报表类型}，对象包括 space、equipment、combinedequipment、meter、virtualmeter、offlinemeter、tenant、store、shopfloor，报表类型如下：
- energycategory / energyitem（分类、分项能耗）、cost、carbon、load、statistics、comparison、saving、plan、prediction、efficiency、output、income、batch、dashboard，以及 tenantbill、metersubmetersbalance、metertracking、metertrend、meterrealtime 等。
- 每个报表都有对应的 Excel、PDF、DOCX 导出器（`myems-api/excelexporters/` 等，Excel 导出共 117 个文件、91,102 行）。

几项关键算法：
- **效率**（`myems-api/reports/equipmentefficiency.py`）：读取该设备 `parameter_type='fraction'` 的分子表和分母表（`:334-347`），把各自的小时能耗按 hourly、daily、weekly、monthly、yearly 汇总。每个时段的效率 = 分子 / 分母，累计效率 = Σ分子 / Σ分母（`:730-743`），**用的是比值之和而不是比值的平均，这一点做对了**。分母为 0 时返回 0，不返回空值（`:732-743`），会把"无数据"显示成效率为 0。另外，`efficiency_indicator` 在 API 里只是原样返回，没有参与计算。
- **节能**（`myems-api/reports/equipmentsaving.py:411-505`）：节能量 = `myems_energy_baseline_db` 里同结构小时表的基线值减去实际值，并换算成 kgce 和 kgco2e。**社区版没有任何代码写入 baseline、plan、prediction、model 这几个库**（只有 `myems-api/config.py` 配了连接，读取方都在 reports 里），基线由企业版或外部导入提供。另外，基线和实际值是按列表下标逐项相减的（`:496-499`），默认两个列表长度一致。
- **负荷**（`myems-api/reports/spaceload.py:407-437`）：从小时能耗算出时段内的平均值和最大值，负荷系数 = 平均值 / 最大值。这里的"负荷"其实是小时平均功率，不是瞬时功率。
- **计划**：plan 报表读 energy_plan_db；`core/energyplanfile.py:167` 只是把上传的文件登记下来，社区版里找不到解析计划文件的代码（**推断**：由企业版处理）。
- 报表都是**请求时现算**：读小时表，在 Python 里按周期聚合（`myems-api/core/utilities.py` 的 `aggregate_hourly_data_by_period`），没有日表和月表这类物化汇总。

---

## 6. 控制、告警、工单

- **控制**：只有"命令"这一种形态。`tbl_commands` 存 MQTT `topic` 和 `payload` 模板，`$s1` 会被替换成 `set_value`（`myems_system_db.sql:162-178`）。`CommandSend.on_put` 要求管理员权限，执行时先把 set_value 写回命令表，然后**每次请求都新建一个 MQTT 客户端，publish 后立即返回 `success`**（`myems-api/core/command.py:462-570`）。没有设备回执，没有执行结果，没有单独的审计表（只有通用的 user log），也没有互锁、限值或回滚。另有 `tbl_points_set_values`（point_id、set_value、is_set）（`myems_system_db.sql:1793-1807`），但社区版的采集服务不写点。
- **告警 / FDD**：`myems_fdd_db.tbl_rules` 定义了 category、fdd_code、priority、channel（WEB、EMAIL、SMS、WECHAT、CALL）、expression（JSON）、message_template（`database/install/myems_fdd_db.sql:35-56`），API 只提供规则的增删改查和"立即运行"标记（`myems-api/core/rule.py:490-520`）。**社区版没有执行规则的服务**。消息发件箱表都在（email、text、wechat、web message）。
- **工单**：`myems-api/core/ticket.py` 用 md5 签名代理外部工作流服务（`config.myems_workflow`，`myems-api/config.py:115`），本身没有工单模型。

---

## 7. 多租户、用户权限、API 与认证

- **"租户"指商业租户，不是 SaaS 租户**。`tbl_tenants` 是商场或写字楼里的承租方，用于分户计量和账单（`tenantbill`）。整个系统只有一棵 space 树，没有组织隔离的概念，所有对象共享同一套库。
- **用户**：`myems_user_db.tbl_users`（`database/install/myems_user_db.sql:61-80`），字段有 `is_admin`、`is_read_only`、`privilege_id`、账号和密码有效期、`failed_login_count`。密码用 `sha512(salt + password)` 单轮哈希（`myems-api/core/user.py:368-370`、`:910`），不是 bcrypt 或 argon2 这类慢哈希。会话 token = `sha512(os.urandom(24))`，存在 `tbl_sessions`，默认 8 小时过期（`user.py:941-947`、`myems-api/config.py:154`）。默认管理员密码 `!MyEMS1` 写在 README 里（`README.md:172-185`）。
- **鉴权**：每个请求都带 `USER-UUID` 和 `TOKEN` 请求头，或者 `API-KEY`。`access_control` 只检查会话是否存在、是否过期，以及用户是否存在，**源码里留着 `# todo: check user privilege`**（`myems-api/core/useractivity.py:85-147`，TODO 在约 `:131`）。`admin_control` 额外要求 `is_admin=1 AND is_read_only=0`（`:10-82`）。
- **数据范围**：`tbl_privileges.data` 是一段 JSON，形如 `{"spaces":[...]}`，只在 `SpaceTreeCollection` 里用来决定前端树的根节点（`myems-api/core/space.py:3667-3720`）。报表接口只做 `access_control` 或 `api_key_control`（例如 `equipmentefficiency.py:71-76`），**不校验请求的对象 id 是否在用户的空间范围内**。**推断**：任何已登录用户只要改 id，就能读到别的空间或租户的报表。
- **API 风格**：Falcon 资源类，每个类配 `on_get/on_post/on_put/on_delete`；`app.py` 有 1598 行、649 条 `add_route`。CORS 设置为 `allow_origins='*', allow_credentials='*'`（`myems-api/app.py:209-210`）。错误响应统一成 `falcon.HTTPError(title='API.XXX', description='API.YYY')`，前端拿这两个字段做多语言。没有 OpenAPI 契约，只有 Postman 和 Apipost 集合文件（`myems-api/MyEMS.postman_collection.json`）。

---

## 8. 源码里能看到的弱点和代价

1. **批处理延迟和水位线语义**：每一层都是睡眠加轮询；"所有输入的公共区间"让最慢的那块表拖住整个父对象；水位线不能回退，迟到数据、修补数据、改公式、改电价都没法自动生效，只能停服务、手工删表、从头重算（`database/recalculate/batch-delete.sql:1-8`）。
2. **缺数和零值混在一起**：离线时段写 0，超限整小时置 0（`myems-normalization/meter.py:398-405`、`:455-459`）。报表里看不出数据缺失，效率接口在分母为 0 时也返回 0。对节能验证（M&V）来说，这是致命问题。
3. **时间语义粗糙**：采集时间戳用的是写库时刻（`myems-modbus-tcp/acquisition.py:414`）；分时电价用全局 UTC 偏移（`myems-aggregation/tariff.py:54-56`）；没有工作日和节假日电价，也没有需量电费。
4. **共享数据库耦合**：13 个库被 7 个以上的进程直接读写，还有跨库 JOIN（`pointrealtime.py:116-121`）。表结构就是服务之间的接口，改一张表要同时改采集、处理、API 三处。
5. **表结构缺少约束**：12 个业务库里**没有一条外键或唯一约束**，`grep FOREIGN KEY|UNIQUE` 只在 user 库的 phone 列命中一次。小时表只有非唯一索引（`database/install/myems_energy_db.sql:7-17`），并发或重复运行会写出重复行。historical 库没有分区，`tbl_energy_value` 永久保留。
6. **SQL 用字符串拼接**：采集端插入（`acquisition.py:423-433`）、`IN (...)` 列表（`tariff.py:104-106`）都直接拼值。来源虽然是内部数据，但写法本身不安全，也用不上预编译语句。
7. **大面积复制粘贴**：报表 103k 行、导出器 91k 行、core 85k 行；tenant 和 store 的分类能耗报表把名字替换掉以后只差 125 行（各约 800 行）。aggregation 的 42 个进程文件结构相同，公共区间那段代码在同一个文件里就复制了 9 次（`space_energy_input_category.py:911-1000+`）。效率报表里有 `for i in range(len(list)): ... del list[i]` 这样的越界和跳项 bug（`equipmentefficiency.py:388-415`）。
8. **安全**：权限只控制前端树根，接口层没有对象级授权；单轮 SHA-512 存密码；`CORS *`；默认口令；gateway token 明文比对（`myems-modbus-tcp/gateway.py:60-66`，源码 TODO 写着"Choose a more secure method"）；MQTT 下发没有回执，也没有安全约束。
9. **运维可观测性差**：日志级别是 ERROR，写 1 MB 的轮转文件（`myems-modbus-tcp/main.py:45-49`）；子进程挂了不会被拉起，源码自己写了 TODO（`main.py:169`）；配置变更要重启进程才生效。
10. **几乎没有测试**：仓库里的 `test*.py` 都是手工脚本，例如 `myems-aggregation/test_tariff.py` 只打印结果，没有断言；CI 只有 CodeQL（`.github/workflows/codeql.yml`）。
11. **社区版和企业版的边界**：baseline、plan、prediction 库，FDD 规则执行，工单，设备控制闭环，在社区版里都只有表或接口外壳。能拿来参考的是数据模型，不是实现。

---

## 结论清单

### 值得吸收（尤其是能耗核算和报表）

1. **计量对象三分法（实体表、虚拟表、离线表）统一进入小时增量表**，下游不区分来源。冷站常见的"总表减分表""冷量 = 流量 × 温差积分""人工抄表"都能套进来。证据：`tbl_meters/tbl_virtual_meters/tbl_offline_meters` 和 `tbl_variables`（`database/install/myems_system_db.sql:1082`、`:2738-2774`）。
2. **能源分类加分项的双维度，并把 `kgce` 和 `kgco2e` 换算系数挂在分类上**，正好对应国内分类分项计量和折标煤、碳排报表。证据：`myems_system_db.sql:316-343`。
3. **设备和设备组的输入、输出计量分离（`is_output`），效率按 Σ输出/Σ输入计算**。冷站 COP/EER 就用"冷量表 / 电表组"，并且必须是比值之和，不能取比值的平均。证据：`myems_system_db.sql:782-794`，`myems-api/reports/equipmentefficiency.py:730-743`。
4. **`is_counted`（以及 `is_input_counted/is_output_counted`）防重复计入，`master_meter_id` 做主表-子表平衡核对**，用于分摊和漏损检查。证据：`myems_system_db.sql:1082-1106`，`myems-api/reports/metersubmetersbalance.py`。
5. **电价挂成本中心，带有效期，再按尖峰平谷深谷展开成逐小时价格，费用 = 小时能耗 × 小时价格**。这种做法简单、可审计，便于以后加季节、节假日、需量电费维度。证据：`myems-aggregation/tariff.py:70-160`，`myems_system_db.sql:2284-2319`。
6. **排放因子支持分时，并按成本中心绑定，缺省时回退到分类上的静态因子**。证据：`myems-aggregation/carbon_dioxide_emission_factor.py:4-44`。
7. **能耗值只打 `is_bad` 标记、不删除，增量只用好值计算；用表级 `hourly_low/high_limit` 压掉噪声和跳变**。思路可以借用，但缺数必须保持为空，不能写 0（见下一节）。证据：`myems-cleaning/clean_energy_value.py:4-6`，`myems-normalization/meter.py:452-459`。
8. **报表矩阵的产品形态**：同一个对象可以看能耗、费用、碳排、负荷、效率、节能、计划、同比环比，每份都带基期和报告期对比以及 kgce、kgco2e 小计。这套形态被中国商业建筑客户长期验证过，可以作为功能清单参考。证据：`myems-api/reports/` 目录结构，`equipmentsaving.py:399-505`。
9. **租户模型带租约号、租期、是否重点租户，并有租户账单报表**，可参考用于商业楼宇按冷量分户计费。证据：`myems_system_db.sql:2501-2527`，`myems-api/reports/tenantbill.py`。
10. **数据源和网关的 `last_seen_datetime_utc` 心跳**，是最简单可用的采集在线状态。证据：`myems-modbus-tcp/acquisition.py:600-605`，`gateway.py:98-101`。

### 不应照搬

1. **用共享 MySQL 加睡眠轮询串联服务**。各服务直接读写对方的表，还跨库 JOIN，表结构就成了隐式接口；应改为 owner 模块加显式契约或事件。证据：`others/docker-compose-on-linux.yml`，`myems-api/reports/pointrealtime.py:116-121`。
2. **只进不退的水位线，又没有重算机制**。迟到数据、修补、改公式、改电价都要人工删表重跑；应改为按时间窗幂等重算（upsert 加脏区间队列）。证据：`myems-normalization/meter.py:186-209`，`database/recalculate/batch-delete.sql:1-8`。
3. **"所有输入的公共区间"阻塞策略**：一块表滞后会冻结整棵树；应允许部分完成，并显式记录完整度。证据：`myems-aggregation/space_energy_input_category.py:911-1000`。
4. **缺数写 0，超限整小时置 0，分母为 0 时效率返回 0**。这会让节能验证和 COP 统计失真；应保留空值、质量码和覆盖率。证据：`myems-normalization/meter.py:398-405`、`:455-459`，`equipmentefficiency.py:732-743`。
5. **用写库时刻作为采集时间戳，逐点单独发 Modbus 请求，一次超时就丢掉整轮，写库失败直接丢数据**。证据：`myems-modbus-tcp/acquisition.py:275-297`、`:414`、`:441-443`。
6. **全局 UTC 偏移的分时电价**，以及只有时段维度的电价模型（没有日类型、需量、阶梯）。证据：`myems-aggregation/tariff.py:54-56`、`:140-155`。
7. **没有唯一约束和外键的表结构，以及永不分区的原始值表**。小时表应该用 `(entity, category, hour)` 做唯一键；原始时序数据放 ClickHouse，或者放 PG 分区表。证据：`database/install/myems_energy_db.sql:7-17`，`myems_historical_db.sql:15-24`。
8. **每个"对象 × 报表"写一份复制粘贴的接口和导出器**（约 20 万行）。应该用统一的度量查询层，加上声明式报表定义。证据：`myems-api/reports/tenantenergycategory.py` 与 `storeenergycategory.py` 只差 125 行。
9. **只控制前端树根的权限模型，接口层没有对象级授权（源码里有 `todo: check user privilege`），单轮 SHA-512 存密码，CORS `*`，默认口令写进 README**。证据：`myems-api/core/useractivity.py:85-147`，`myems-api/core/user.py:370`，`myems-api/app.py:209`，`README.md:172-185`。
10. **fire-and-forget 的 MQTT 控制下发**：没有回执、限值、互锁和审计。冷站控制必须要有命令状态机和安全边界。证据：`myems-api/core/command.py:462-570`。
11. **配置变更要重启进程，子进程崩溃不会被拉起，日志只记 ERROR 级**。证据：`myems-modbus-tcp/main.py:45-49`、`:69`、`:169`，`acquisition.py:243`。
12. **把"有表没实现"的企业版能力（baseline、plan、prediction、FDD 执行）当成可参考的实现**。社区版只能借鉴它们的表结构。证据：社区版里写这些库的代码只有 `myems-api/config.py` 中的连接配置，读取方在 `myems-api/reports/*saving.py`、`*plan.py`。
