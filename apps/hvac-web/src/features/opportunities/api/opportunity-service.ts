import type { EcoOpportunity, OpportunitiesSummary } from "./opportunity-types";

export const MOCK_OPPORTUNITIES: readonly EcoOpportunity[] = [
  {
    id: "eco-01",
    code: "ECO-CHW-01",
    title: "冷冻水供水温度自适应动态重置 (7.0°C → 8.5°C)",
    targetObject: "1# 变频高能效离心冷水机组 CH-01",
    targetSystem: "暖通空调 / 冷冻站系统",
    location: "地下二层 制冷机房",
    subsystem: "CHILLER",
    complexity: "ZERO_LOW_COST",
    annualSavingsKWh: 68500,
    annualSavingsCostCNY: 58200,
    investmentCostCNY: 0,
    paybackMonths: 0,
    confidence: 94,
    priority: "critical",
    owner: "张工 (节能主管)",
    status: "CLOSED",
    detectedAt: "2026-03-29 09:30 · 智能诊断引擎",
    summary:
      "根据建筑末端最大阀位开度与回水温度，将主机出水温度由固定 7.0°C 提升至 7.5°C~8.5°C，每提升 1°C 主机 COP 可提高 2.5%~3.0%。",
    mechanismAnalysis:
      "在部分负荷工况下，末端盘管所需温差降低。提高供水温度减小了冷机压缩比和冷凝蒸发压差，使压缩机功耗显著下降，消除传统设计留存的“过冷冷量裕量”。",
    baselineCondition:
      "主机常年设定为固定 7.0°C 供水，末端风机盘管阀门开度中位数仅 42%，存在典型“大流量小温差”能源浪费。",
    proposedCondition:
      "引入末端阀位开度最大前馈控制环路，将出水温度设定值自适应提升至 8.0°C~8.5°C，保持最不利末端阀位开度在 85%~90%。",
    calculationMethod:
      "基于当前主机 COP 曲线模型：供水温度提高 1.5°C 预计全站综合节电率 4.2%，年化节省电量 68,500 kWh。",
    riskNotice:
      "必须联动监控核心区域空气相对湿度，当相对湿度大于 65% 时自动回退至除湿优先级供水温度。",
    evidenceMetrics: [
      {
        label: "设定出水温度",
        baseline: "7.0",
        actual: "8.5",
        unit: "°C",
        delta: "+1.5",
        isFavorable: true,
      },
      {
        label: "实测冷机 COP",
        baseline: "4.85",
        actual: "5.42",
        unit: "",
        delta: "+11.7%",
        isFavorable: true,
      },
      {
        label: "末端阀门平均开度",
        baseline: "42",
        actual: "88",
        unit: "%",
        delta: "+46%",
        isFavorable: true,
      },
      {
        label: "供回水实测温差",
        baseline: "3.2",
        actual: "4.8",
        unit: "°C",
        delta: "+1.6",
        isFavorable: true,
      },
    ],
    auditHistory: [
      {
        time: "2026-03-29 09:30",
        action: "智能识别",
        actor: "智能诊断",
        note: "分析 72 小时连续末端开度发现过冷裕量",
      },
      {
        time: "2026-03-29 14:20",
        action: "专家核查",
        actor: "李工 (暖通工程师)",
        note: "确认现场管路与阀门具备动态调优条件",
      },
      {
        time: "2026-03-30 10:15",
        action: "批准实施",
        actor: "张工 (节能主管)",
        note: "下发控制策略寻优参数，进入群控排程",
      },
      {
        time: "2026-10-01",
        action: "复核关闭",
        actor: "陈工 (M&V 工程师)",
        note: "完成6月至9月报告期核算，关联项目归档。",
      },
    ],
  },
  {
    id: "eco-02",
    code: "ECO-PUMP-02",
    title: "冷冻水二次泵最不利环路变频压差自适应优化",
    targetObject: "1# 一级冷冻水循环水泵组 CHP-01~03",
    targetSystem: "暖通空调 / 水输配水力系统",
    location: "地下二层 水泵区",
    subsystem: "PUMP",
    complexity: "ZERO_LOW_COST",
    annualSavingsKWh: 42000,
    annualSavingsCostCNY: 34500,
    investmentCostCNY: 5000,
    paybackMonths: 1.7,
    confidence: 91,
    priority: "high",
    owner: "李工 (值班电工)",
    status: "VERIFYING",
    detectedAt: "2026-04-29 11:20 · 水力平衡监测",
    summary:
      "消除传统机房固定压差控制的安全裕量冗余，改用最不利末端动态压差复核与自适应设定。",
    mechanismAnalysis:
      "传统压差传感器位于机房分集水器，设定值必须按最恶劣设计工况设定，导致部分负荷下管网富余压头过高。将压差控制点动态迁移至管网最不利末端，可大幅降低输配能耗。",
    baselineCondition:
      "分集水器固定压差 0.25 MPa，变频水泵日均运行频率 46.5 Hz，阀门节流压降占比超 40%。",
    proposedCondition:
      "以西塔顶层 12F 空调箱末端实测压差为闭环控制目标，将机房压差设定下调为 0.16~0.20 MPa 动态浮动。",
    calculationMethod:
      "根据水力流体亲和定律（轴功率与转速立方成正比），水泵运行频率从 46.5Hz 降至 38Hz 可节电 35% 以上。",
    riskNotice:
      "需确保楼层末端无线压差传感器通信延时小于 10 秒，发生通信超时自动切入安全默认压差。",
    evidenceMetrics: [
      {
        label: "系统控制压差",
        baseline: "0.25",
        actual: "0.18",
        unit: "MPa",
        delta: "-0.07",
        isFavorable: true,
      },
      {
        label: "水泵平均频率",
        baseline: "46.5",
        actual: "38.2",
        unit: "Hz",
        delta: "-8.3",
        isFavorable: true,
      },
      {
        label: "输配能效比 WTF",
        baseline: "26.8",
        actual: "32.4",
        unit: "",
        delta: "+20.9%",
        isFavorable: true,
      },
      {
        label: "末端阀门节流损耗",
        baseline: "12.4",
        actual: "4.2",
        unit: "kW",
        delta: "-66.1%",
        isFavorable: true,
      },
    ],
    auditHistory: [
      {
        time: "2026-04-29 11:20",
        action: "智能识别",
        actor: "能效诊断引擎",
        note: "检测到输送水力扬程富余严重",
      },
      {
        time: "2026-04-29 16:45",
        action: "方案评审中",
        actor: "王工 (自控主管)",
        note: "组织现场工程师复核末端压力变送器信号",
      },
    ],
  },
  {
    id: "eco-03",
    code: "ECO-TOWER-03",
    title: "过渡季冷却水板式换热器水侧自然冷却 (Free Cooling)",
    targetObject: "1# 低噪声开式冷却塔组 CT-01~04",
    targetSystem: "暖通空调 / 冷却水散热系统",
    location: "主楼机房顶层 冷却塔平台",
    subsystem: "TOWER",
    complexity: "CAPITAL",
    annualSavingsKWh: 85000,
    annualSavingsCostCNY: 72000,
    investmentCostCNY: 98000,
    paybackMonths: 16.3,
    confidence: 88,
    priority: "high",
    owner: "赵工 (工程总监)",
    status: "CLOSED",
    detectedAt: "2025-08-28 15:00 · 气象预测模型",
    summary:
      "在室外湿球温度低于 10°C 的过渡季及初冬，利用冷却塔出水与板式换热器直接供应冷冻水，停开主力冷机。",
    mechanismAnalysis:
      "商场内区与数据机房常年有恒定制冷需求。当室外空气湿球温度低于 10°C 时，冷却水可冷却至 12°C 以下，足以直接通过板换为冷冻水系统降温，实现“免费制冷”。",
    baselineCondition:
      "过渡季仍开机单台离心机以 28%~35% 低负荷维持运行，运行 COP 仅 3.2，能耗高且容易引发喘振。",
    proposedCondition:
      "增设板式换热器旁通管路与电动切换阀，当湿球温度 ≤ 10°C 且维持 30 分钟以上时自动切换自然冷却。",
    calculationMethod:
      "气象数据统计显示上海地区每年符合自然冷却时长约 950 小时，折合减少主机运行电耗 85 MWh。",
    riskNotice:
      "必须严格控制冷却塔水质与加药杀菌，加装防冻联锁电加热与电动排空阀防止极端低温冻裂。",
    evidenceMetrics: [
      {
        label: "主机运行小时数",
        baseline: "950",
        actual: "0",
        unit: "h/年",
        delta: "-950",
        isFavorable: true,
      },
      {
        label: "系统综合耗电",
        baseline: "112.5",
        actual: "27.5",
        unit: "MWh",
        delta: "-75.5%",
        isFavorable: true,
      },
      {
        label: "预估年化减碳",
        baseline: "88.5",
        actual: "21.6",
        unit: "tCO₂",
        delta: "-66.9",
        isFavorable: true,
      },
    ],
    auditHistory: [
      {
        time: "2025-08-28 15:00",
        action: "智能识别",
        actor: "规划推演模块",
        note: "结合历史气象数据与机房负荷基线生成可行性建议",
      },
    ],
  },
  {
    id: "eco-04",
    code: "ECO-TERM-04",
    title: "非营业时段商铺及办公末端待机能耗自动关断",
    targetObject: "东塔 8F/12F 空气处理机组 AHU-03/04",
    targetSystem: "暖通空调 / 末端空气处理系统",
    location: "东塔/西塔 标准层空调机房",
    subsystem: "TERMINAL",
    complexity: "ZERO_LOW_COST",
    annualSavingsKWh: 29500,
    annualSavingsCostCNY: 23600,
    investmentCostCNY: 0,
    paybackMonths: 0,
    confidence: 96,
    priority: "critical",
    owner: "李工 (值班电工)",
    status: "VERIFYING",
    detectedAt: "2026-05-29 02:00 · 异常待机扫描",
    summary:
      "根据建筑作息时间表与人员在室监测联动，在 22:00~07:00 自动闭锁闲置区域末端水阀与风机。",
    mechanismAnalysis:
      "非营业时段部分租户漏关空调或温控器设定过低，造成冷水在末端无功循环，既空耗风机电量又稀释回水温差。",
    baselineCondition:
      "夜间闲置时段仍有 35% 空调箱风机与电动水阀持续开度，管网夜间冷负荷空损达 180 kW。",
    proposedCondition:
      "通过 BMS 自动下发日程排程闭环，22:00 强制关闭水阀；支持租户通过企业微信扫码一键申请加班延时。",
    calculationMethod:
      "消除夜间 8 小时待机漏冷与风机电耗，折合每日节电 82 kWh，年化节约 2.36 万元。",
    riskNotice:
      "加班延时申请需具备 15 分钟提前响应能力，确保加班租户舒适度不受影响。",
    evidenceMetrics: [
      {
        label: "夜间空跑负荷",
        baseline: "180",
        actual: "18",
        unit: "kW",
        delta: "-90.0%",
        isFavorable: true,
      },
      {
        label: "夜间水阀开启率",
        baseline: "35",
        actual: "2",
        unit: "%",
        delta: "-33%",
        isFavorable: true,
      },
      {
        label: "末端风机日均耗电",
        baseline: "240",
        actual: "158",
        unit: "kWh",
        delta: "-34.2%",
        isFavorable: true,
      },
    ],
    auditHistory: [
      {
        time: "2026-05-29 02:00",
        action: "智能识别",
        actor: "作息能效比对引擎",
        note: "夜间扫描发现大面积末端无效开度",
      },
      {
        time: "2026-05-29 08:30",
        action: "复核通过",
        actor: "运维值班组",
        note: "核实各商铺夜间均无加班备案",
      },
      {
        time: "2026-05-30 09:00",
        action: "排程下发",
        actor: "张工 (节能主管)",
        note: "已在控制中心配置自动日程策略",
      },
    ],
  },
  {
    id: "eco-05",
    code: "ECO-CHW-05",
    title: "冷水机组多机群控加减机策略与最佳负载率协同优化",
    targetObject: "1# & 2# 离心冷水机组 CH-01/CH-02",
    targetSystem: "暖通空调 / 冷冻站系统",
    location: "地下二层 制冷机房",
    subsystem: "CHILLER",
    complexity: "MEDIUM",
    annualSavingsKWh: 38000,
    annualSavingsCostCNY: 31200,
    investmentCostCNY: 15000,
    paybackMonths: 5.8,
    confidence: 92,
    priority: "high",
    owner: "张工 (节能主管)",
    status: "IN_PROGRESS",
    detectedAt: "2026-09-29 14:00 · 群控日志审计",
    summary:
      "优化主机加载与减机负荷阈值，避免 2 台离心机长期双双运行在 40% 低效区，保持单机负载在 70%~85% 高效区间。",
    mechanismAnalysis:
      "离心冷机在 40% 负荷时 COP 仅为 4.30，而在 75% 负荷时 COP 达到 5.50。通过合理加减机滞后回差控制，优先满载单台冷机再启第二台，能效提升显著。",
    baselineCondition:
      "原群控算法在负荷达到 50% 即触发双机并联，导致两台主机长期在低负载低效区运行。",
    proposedCondition:
      "将主机加机阈值提升至单机负载率 82%，减机阈值设定为总负荷低于单机 70% 容量，引入 30 分钟防抖延时。",
    calculationMethod:
      "根据实际负荷分布频次，减少双机低载运行时间 640 小时，提升全天平均 COP 0.45。",
    riskNotice:
      "加减机必须严格保证供回水温度波动小于 ±0.8°C，防止瞬间冷量断档引起末端室温波动。",
    evidenceMetrics: [
      {
        label: "平均主机负载率",
        baseline: "44.5",
        actual: "76.2",
        unit: "%",
        delta: "+31.7%",
        isFavorable: true,
      },
      {
        label: "机组日均 COP",
        baseline: "4.62",
        actual: "5.42",
        unit: "",
        delta: "+17.3%",
        isFavorable: true,
      },
      {
        label: "低效并联时长",
        baseline: "4.5",
        actual: "0.8",
        unit: "h/天",
        delta: "-3.7",
        isFavorable: true,
      },
    ],
    auditHistory: [
      {
        time: "2026-09-29 14:00",
        action: "智能识别",
        actor: "能效诊断引擎",
        note: "检测到频繁双机轻载工况",
      },
      {
        time: "2026-09-30 11:00",
        action: "实施测试",
        actor: "控制中心",
        note: "正在进行寻优算法线上灰度验证",
      },
    ],
  },
  {
    id: "eco-06",
    code: "ECO-SYS-06",
    title: "电价峰谷分时储冷移峰填谷自适应响应",
    targetObject: "水蓄冷槽与主冷水系统",
    targetSystem: "暖通空调 / 储能与负荷柔性",
    location: "地下二层 蓄能站",
    subsystem: "SYSTEM",
    complexity: "MEDIUM",
    annualSavingsKWh: 15000,
    annualSavingsCostCNY: 48000,
    investmentCostCNY: 20000,
    paybackMonths: 5.0,
    confidence: 90,
    priority: "high",
    owner: "赵工 (工程总监)",
    status: "APPROVED",
    detectedAt: "2026-09-28 10:00 · 电价费率优化",
    summary:
      "利用谷段夜间低谷电价（¥0.32/kWh）提前满蓄冷量，在尖峰时段（14:00~17:00，¥1.42/kWh）全开释冷削减主机需量。",
    mechanismAnalysis:
      "虽然储冷存在 5% 蓄放冷损失，但尖峰与低谷电价差高达 4.4 倍，经济效益极为显著，且能大幅降低当月申报的最大需量基本电费。",
    baselineCondition:
      "目前按固定工频充释冷，未能与第二日实时气象温度和用电负荷预测深度协同，常常出现未放完或提前耗尽。",
    proposedCondition:
      "基于 24 小时气象预测与建筑热惰性负荷预测模型，自适应动态计算最佳充冷量和放冷速率。",
    calculationMethod:
      "每日在尖峰段转移负荷 450 kWh，日均节省电费差价 ¥495，年化直接电费净节约 ¥4.8 万元。",
    riskNotice:
      "蓄冷槽放冷末期出水温度上升不能超过 9.0°C，防止末端除湿能力不足。",
    evidenceMetrics: [
      {
        label: "尖峰段移峰电量",
        baseline: "120",
        actual: "450",
        unit: "kWh/天",
        delta: "+330",
        isFavorable: true,
      },
      {
        label: "度电综合成本",
        baseline: "0.98",
        actual: "0.66",
        unit: "元/kWh",
        delta: "-32.7%",
        isFavorable: true,
      },
      {
        label: "申报最大需量",
        baseline: "620",
        actual: "480",
        unit: "kW",
        delta: "-140",
        isFavorable: true,
      },
    ],
    auditHistory: [
      {
        time: "2026-09-28 10:00",
        action: "智能识别",
        actor: "电价响应算法",
        note: "识别到上海夏季尖峰电价移峰套利空间",
      },
    ],
  },
  {
    id: "eco-07",
    code: "ECO-AIR-07",
    title: "过渡季全新风自由利用与二氧化碳浓度协同新风控制",
    targetObject: "商场中庭全空气系统 AHU-01/02",
    targetSystem: "暖通空调 / 通风与空气质量",
    location: "主楼 4F 机房",
    subsystem: "TERMINAL",
    complexity: "ZERO_LOW_COST",
    annualSavingsKWh: 22000,
    annualSavingsCostCNY: 18100,
    investmentCostCNY: 0,
    paybackMonths: 0,
    confidence: 89,
    priority: "medium",
    owner: "李工 (值班电工)",
    status: "APPROVED",
    detectedAt: "2026-09-27 16:30 · 室内环境监测",
    summary:
      "室外焓值低于室内时大比例引入全新风制冷；日常根据室内 CO₂ 实测浓度动态调节新风阀，避免过量新风增加制冷负荷。",
    mechanismAnalysis:
      "夏季高温时新风负荷占空调总负荷 30% 以上。通过室内 CO₂ 浓度（保持在 800ppm 以下）动态按需引入新风，可减少约 25% 新风处理冷负荷。",
    baselineCondition:
      "新风阀常年固定 25% 开度，过渡季无法利用室外凉爽空气，酷暑时过量引入高湿热空气。",
    proposedCondition:
      "增设室外温湿度焓值与室内 CO₂ 双参数前馈闭环控制，过渡季新风阀最大开至 100%，酷暑段按需闭环下调至 15%~20%。",
    calculationMethod:
      "过渡季全开新风减少冷机启动 180 小时，夏季酷暑减少过量新风冷量 22 MWh。",
    riskNotice:
      "必须配备 PM2.5 过滤器压差传感器，室外重污染天气自动关闭大新风模式。",
    evidenceMetrics: [
      {
        label: "过渡季新风比",
        baseline: "25",
        actual: "90",
        unit: "%",
        delta: "+65%",
        isFavorable: true,
      },
      {
        label: "室内 CO₂ 浓度",
        baseline: "650",
        actual: "720",
        unit: "ppm",
        delta: "达标",
        isFavorable: true,
      },
      {
        label: "新风处理冷量",
        baseline: "45.2",
        actual: "32.1",
        unit: "kW",
        delta: "-29.0%",
        isFavorable: true,
      },
    ],
    auditHistory: [
      {
        time: "2026-09-27 16:30",
        action: "智能识别",
        actor: "IAQ 环控算法",
        note: "检测到室外焓值优于室内焓值",
      },
      {
        time: "2026-09-29 15:00",
        action: "批准实施",
        actor: "张工 (节能主管)",
        note: "已在 AHU 自动控制面板启用 CO₂ 联动",
      },
    ],
  },
  {
    id: "eco-08",
    code: "ECO-VFD-08",
    title: "冷却塔风机无级变频逼近度最优水温控制",
    targetObject: "2# 冷却塔风机组 CT-FAN-01~04",
    targetSystem: "暖通空调 / 冷却水散热系统",
    location: "主楼机房顶层 冷却塔平台",
    subsystem: "TOWER",
    complexity: "MEDIUM",
    annualSavingsKWh: 32500,
    annualSavingsCostCNY: 26800,
    investmentCostCNY: 8000,
    paybackMonths: 3.6,
    confidence: 93,
    priority: "high",
    owner: "李工 (值班电工)",
    status: "IDENTIFIED",
    detectedAt: "2026-09-29 17:00 · 冷却水温差分析",
    summary:
      "根据室外空气湿球温度与冷却水出水逼近度（Approach），寻找冷却塔风机电耗与冷机冷凝功耗的“系统综合总能耗极小值点”。",
    mechanismAnalysis:
      "虽然提高冷却塔风机转速会增加风机电耗，但出水水温降低 1°C 可使离心冷机功耗降低约 3%。两者的平衡点通常在逼近度 2.8°C~3.2°C 之间。",
    baselineCondition:
      "冷却塔风机简单根据固定水温 32°C 启停，温降效果差且风机频繁全速运行，冷机冷凝温度偏高。",
    proposedCondition:
      "采用逼近度最优搜索自适应控制算法，实时计算边际收益率，将冷却水回水温度控制在室外湿球温度 + 2.8°C。",
    calculationMethod:
      "主机冷凝功耗降低 6.2%，扣除风机增加的微量能耗后，全系统综合年化节电 32,500 kWh。",
    riskNotice:
      "冷却水进水温度最低限制为 19°C，防止主机发生油分离不良与低冷凝压力保护。",
    evidenceMetrics: [
      {
        label: "冷却水出水温度",
        baseline: "32.0",
        actual: "28.5",
        unit: "°C",
        delta: "-3.5",
        isFavorable: true,
      },
      {
        label: "综合边际节电率",
        baseline: "0",
        actual: "4.8",
        unit: "%",
        delta: "+4.8%",
        isFavorable: true,
      },
      {
        label: "冷水主机冷凝功耗",
        baseline: "228",
        actual: "205",
        unit: "kW",
        delta: "-10.1%",
        isFavorable: true,
      },
    ],
    auditHistory: [
      {
        time: "2026-09-29 17:00",
        action: "智能识别",
        actor: "能效诊断引擎",
        note: "发现冷却水温具有下调 3.5°C 的可优化空间",
      },
    ],
  },
];

export function getOpportunitiesSummary(
  data: readonly EcoOpportunity[] = MOCK_OPPORTUNITIES,
): OpportunitiesSummary {
  const totalCount = data.length;
  const totalAnnualSavingsKWh = data.reduce(
    (sum, item) => sum + item.annualSavingsKWh,
    0,
  );
  const totalAnnualSavingsCostCNY = data.reduce(
    (sum, item) => sum + item.annualSavingsCostCNY,
    0,
  );
  const zeroCostCount = data.filter(
    (item) => item.complexity === "ZERO_LOW_COST",
  ).length;
  const highPriorityCount = data.filter(
    (item) => item.priority === "critical" || item.priority === "high",
  ).length;
  const approvedCount = data.filter(
    (item) => item.status === "APPROVED",
  ).length;
  const inProgressCount = data.filter(
    (item) => item.status === "IN_PROGRESS",
  ).length;

  const capitalOrMediumItems = data.filter(
    (item) => item.investmentCostCNY > 0,
  );
  const avgPayback = capitalOrMediumItems.length
    ? capitalOrMediumItems.reduce((sum, item) => sum + item.paybackMonths, 0) /
      capitalOrMediumItems.length
    : 3.6;

  return {
    totalCount,
    totalAnnualSavingsKWh,
    totalAnnualSavingsCostCNY,
    averagePaybackMonths: parseFloat(avgPayback.toFixed(1)),
    zeroCostRatio: parseFloat(((zeroCostCount / totalCount) * 100).toFixed(1)),
    highPriorityCount,
    approvedCount,
    inProgressCount,
  };
}
