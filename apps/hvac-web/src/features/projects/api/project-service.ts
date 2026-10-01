import type { EnergySavingProject, ProjectsSummary } from "./project-types";

export const MOCK_PROJECTS: readonly EnergySavingProject[] = [
  {
    id: "prj-001",
    code: "PRJ-CHW-01",
    title: "1#站房冷水主机供水温度阶梯动态重设工程",
    sourceOpportunityCode: "ECO-CHW-01",
    sourceOpportunityTitle: "冷水供水温度自适应动态重设 (7.0°C → 8.5°C)",
    subsystem: "CHILLER",
    category: "ZERO_LOW_COST",
    stage: "COMPLETED",
    progress: 100,
    budgetCNY: 15000,
    spentCostCNY: 14200,
    expectedAnnualSavingsKWh: 68500,
    expectedAnnualSavingsCostCNY: 58200,
    paybackMonths: 3.1,
    owner: "张工 (节能主管)",
    location: "地下二层 1#制冷机房",
    startDate: "2026-04-01",
    targetCompletionDate: "2026-10-01",
    targetDevices: ["CH-01 离心机组", "CH-02 离心机组", "CH-03 螺杆机组"],
    summary:
      "根据各楼层最不利末端最大阀位开度前馈反馈，将主机出水温度设定由定温 7.0°C 自适应提升至 7.5°C~8.5°C，减小主机压缩比并降低过冷能源浪费。",
    milestones: [
      {
        id: "m1",
        title: "现场末端阀位反馈信号与 BAS 控制点通信校核",
        targetDate: "2026-04-10",
        status: "DONE",
        owner: "李工 (暖通工程师)",
        remark: "完成 48 组末端 DDC 通信链路点位校验，无丢包",
      },
      {
        id: "m2",
        title: "PLC 供水温度自适应滑模 PID 控制逻辑编制与仿真",
        targetDate: "2026-04-25",
        status: "DONE",
        owner: "王工 (自控专家)",
        remark: "在虚拟仿真回路完成抗扰动性测试，设定安全死区 ±0.3°C",
      },
      {
        id: "m3",
        title: "现场分阶段下发策略与连续 72 小时平稳度压测",
        targetDate: "2026-05-20",
        status: "DONE",
        owner: "张工 (节能主管)",
        remark: "主机排气压力及回油压差平稳，室内温湿度达标率 99.4%",
      },
      {
        id: "m4",
        title: "进入 IPMVP Option B 节能验证测量期数据归档",
        targetDate: "2026-10-01",
        status: "DONE",
        owner: "陈工 (M&V 工程师)",
        remark: "已完成报告期数据归档与复核，基线相关性 R²=0.96",
      },
    ],
    technicalHandover: {
      setpointChanges: "主机供水温度由固定 7.0°C 变更为 7.5°C~8.5°C 浮动设定。",
      interlockRules:
        "当室内环境相对湿度 > 65% 时，强制锁定出水温度在 7.0°C 优先除湿。",
      rollbackTrigger:
        "任意核心敏感商户区域温度 > 26.5°C 持续 10 分钟自动回退。",
      safetyBoundaries:
        "出水温度单次步进 ≤ 0.5°C/15min，极限上限不得超过 9.0°C。",
    },
    mvPlan: {
      ipmvpOption: "Option B",
      measurementBoundary: "1#站房离心冷水主机回路电表及冷冻水进出水冷量表",
      baselineModel:
        "基于 2025 年同期室外干湿球温度与冷负荷构建的多元线性回归模型",
      reportingPeriod: "2026-06-01 至 2026-09-30 (报告监测期)",
      expectedVerifiedSavings: "约 68,500 kWh/年 (折合 ¥58,200/年)",
    },
  },
  {
    id: "prj-002",
    code: "PRJ-PUMP-02",
    title: "空调二级冷冻水泵最不利环路变频压差闭环改造",
    sourceOpportunityCode: "ECO-PUMP-02",
    sourceOpportunityTitle: "冷冻水二次泵最不利环路变频压差自适应重设",
    subsystem: "PUMP",
    category: "CONTROL_STRATEGY",
    stage: "MV_VERIFYING",
    progress: 95,
    budgetCNY: 28000,
    spentCostCNY: 26500,
    expectedAnnualSavingsKWh: 42000,
    expectedAnnualSavingsCostCNY: 34500,
    paybackMonths: 9.7,
    owner: "李工 (暖通工程师)",
    location: "地下二层 水泵区及主楼管廊",
    startDate: "2026-05-01",
    targetCompletionDate: "2026-10-30",
    targetDevices: [
      "CHP-01 一级冷冻泵",
      "CHP-02 一级冷冻泵",
      "CHP-03 一级冷冻泵",
    ],
    summary:
      "将传统以集分水器定压差控制升级为基于高区、低区末端最不利环路压差无线测点动态反馈，消除沿程阀门节流阻力冗余，降低水泵输配扬程及电耗。",
    milestones: [
      {
        id: "m1",
        title: "主楼最远端两个不利环路压差传感器LoRa无线部署",
        targetDate: "2026-05-15",
        status: "DONE",
        owner: "李工 (暖通工程师)",
      },
      {
        id: "m2",
        title: "变频水泵群控 PLC 控制柜接线与变频器参数重写",
        targetDate: "2026-06-01",
        status: "DONE",
        owner: "王工 (自控专家)",
      },
      {
        id: "m3",
        title: "现场水力平衡与低流量下限防气蚀试车调试",
        targetDate: "2026-06-20",
        status: "DONE",
        owner: "李工 (暖通工程师)",
        remark: "已完成频响测试，保持 32Hz 最低频率约束",
      },
      {
        id: "m4",
        title: "移交运维并进入正式 M&V 节能量跟踪",
        targetDate: "2026-10-30",
        status: "IN_PROGRESS",
        owner: "张工 (节能主管)",
      },
    ],
    technicalHandover: {
      setpointChanges:
        "二次泵供回水设定压差由固定 0.22 MPa 降低为 0.12~0.16 MPa 动态闭环。",
      interlockRules:
        "水泵变频器运行频率下限锁定 32Hz，防止电机温升与循环水气蚀。",
      rollbackTrigger:
        "最不利环路差压 < 0.08 MPa 或管网流量报警即刻切入安全工频。",
      safetyBoundaries: "备用泵自动联锁投切时间 ≤ 15 秒。",
    },
    mvPlan: {
      ipmvpOption: "Option A",
      measurementBoundary: "冷冻水循环水泵变频配电柜进线智能多功能电表",
      baselineModel: "改造前定频/定压差运行工况下的输配耗电输冷比 (WTAR) 基准",
      reportingPeriod: "2026-07-01 至 2026-09-30",
      expectedVerifiedSavings: "约 42,000 kWh/年 (节约 ¥34,500/年)",
    },
  },
  {
    id: "prj-003",
    code: "PRJ-TOWER-03",
    title: "过渡季板式换热器水侧自然冷却接入工程",
    sourceOpportunityCode: "ECO-TOWER-03",
    sourceOpportunityTitle:
      "过渡季冷却水板式换热器水侧自然冷却 (Free Cooling) 投运",
    subsystem: "TOWER",
    category: "EQUIPMENT_RETROFIT",
    stage: "COMPLETED",
    progress: 100,
    budgetCNY: 120000,
    spentCostCNY: 58000,
    expectedAnnualSavingsKWh: 85000,
    expectedAnnualSavingsCostCNY: 72000,
    paybackMonths: 20.0,
    owner: "周工 (项目经理)",
    location: "1#冷冻机房及主楼屋顶冷却塔平台",
    startDate: "2025-09-01",
    targetCompletionDate: "2026-08-30",
    targetDevices: ["HEX-01 板式换热器", "CT-01~04 冷却塔群", "电动旁通三通阀"],
    summary:
      "在现有冷水机组旁新增高效率板式换热器与电动切换阀门组。当室外湿球温度 ≤ 10°C 时，直接通过冷却塔水给室内冷冻水降温，关停高电耗冷水机组。",
    milestones: [
      {
        id: "m1",
        title: "板式换热器采购进场与基础预埋固定",
        targetDate: "2025-09-20",
        status: "DONE",
        owner: "周工 (项目经理)",
      },
      {
        id: "m2",
        title: "DN350 冷却水/冷冻水旁通管路熔接与电动三通阀安装",
        targetDate: "2025-10-15",
        status: "DONE",
        owner: "周工 (项目经理)",
        remark: "已完成管路吊装与压力测试",
      },
      {
        id: "m3",
        title: "水质旁流水处理器与自动反冲洗排污联调",
        targetDate: "2025-10-25",
        status: "DONE",
        owner: "李工 (暖通工程师)",
      },
      {
        id: "m4",
        title: "冬季自然冷却工况全负荷试运行与冷量标定",
        targetDate: "2026-08-30",
        status: "DONE",
        owner: "张工 (节能主管)",
      },
    ],
    technicalHandover: {
      setpointChanges: "过渡季冷水机组休眠，板换出水直接提供 9.0°C 冷冻水。",
      interlockRules:
        "室外湿球温度稳定 < 10.5°C 且维持 30 分钟方允许开启自然冷却切换。",
      rollbackTrigger: "板换出水温度 > 12.0°C 时自动启动 1# 离心机组预冷兜底。",
      safetyBoundaries: "冷却塔水温极限防冻保护设定在 4.5°C。",
    },
    mvPlan: {
      ipmvpOption: "Option B",
      measurementBoundary: "冷站高压冷水主机及屋顶冷却塔风机总电度表",
      baselineModel: "同等室外气温区间历史压缩机制冷电耗基准线",
      reportingPeriod: "2025-11-01 至 2026-02-28 (板换投运季)",
      expectedVerifiedSavings: "约 85,000 kWh (节约 ¥72,000/年)",
    },
  },
  {
    id: "prj-004",
    code: "PRJ-TERM-04",
    title: "商业区多联空调与末端风柜夜间自动待机闭锁",
    sourceOpportunityCode: "ECO-TERM-04",
    sourceOpportunityTitle: "非营业时段商铺及办公末端待机能耗闭锁控制",
    subsystem: "TERMINAL",
    category: "ZERO_LOW_COST",
    stage: "MV_VERIFYING",
    progress: 95,
    budgetCNY: 5000,
    spentCostCNY: 800,
    expectedAnnualSavingsKWh: 29500,
    expectedAnnualSavingsCostCNY: 23600,
    paybackMonths: 2.5,
    owner: "张工 (节能主管)",
    location: "东塔 8F/12F 办公区与主楼商场",
    startDate: "2026-06-01",
    targetCompletionDate: "2026-10-15",
    targetDevices: ["AHU-01~04 空气处理机组", "PAU 新风机组", "VRV 多联机网关"],
    summary:
      "结合租户打卡作息与商铺闭店营业时钟，建立非营运时段空调温度回退与定时待机闭锁机制，杜绝夜间空载制冷。",
    milestones: [
      {
        id: "m1",
        title: "租户作息日历与非营运时段商铺负荷基线盘点",
        targetDate: "2026-06-05",
        status: "DONE",
        owner: "张工 (节能主管)",
      },
      {
        id: "m2",
        title: "楼宇自控时钟日历策略配置与例外加班预约小程序对接",
        targetDate: "2026-06-20",
        status: "DONE",
        owner: "王工 (自控专家)",
      },
      {
        id: "m3",
        title: "现场巡检确认夜间静默待机状态与电表读数校核",
        targetDate: "2026-10-15",
        status: "IN_PROGRESS",
        owner: "李工 (暖通工程师)",
      },
    ],
    technicalHandover: {
      setpointChanges:
        "夜间 22:30 至次日 06:30 室内设定温度上浮至 28.5°C 或关闭风机。",
      interlockRules: "消防排烟与值班新风享有最高优先级抢占。",
      rollbackTrigger: "租户线上提交加班申请，自动开放对应分区 2 小时用冷。",
      safetyBoundaries: "服务器弱电机房末端绝对禁止进入节能休眠。",
    },
    mvPlan: {
      ipmvpOption: "Option C",
      measurementBoundary: "商业主楼全楼层低压动力照明配电总进线回路",
      baselineModel: "改造前连续 3 个月夜间平均基底负荷（Base Load）",
      reportingPeriod: "2026-07-01 至 2026-09-30",
      expectedVerifiedSavings: "约 29,500 kWh/年 (节约 ¥23,600/年)",
    },
  },
  {
    id: "prj-005",
    code: "PRJ-SYS-05",
    title: "电价峰谷时段水蓄冷水池动态充放冷负荷转移系统",
    sourceOpportunityCode: "ECO-SYS-06",
    sourceOpportunityTitle: "电价峰谷分时储冷移峰填谷自适应响应",
    subsystem: "SYSTEM",
    category: "CONTROL_STRATEGY",
    stage: "DESIGN",
    progress: 30,
    budgetCNY: 35000,
    spentCostCNY: 10500,
    expectedAnnualSavingsKWh: 60000,
    expectedAnnualSavingsCostCNY: 54000,
    paybackMonths: 7.8,
    owner: "王工 (自控专家)",
    location: "地下三层 消防蓄冷水池",
    startDate: "2026-09-15",
    targetCompletionDate: "2026-12-05",
    targetDevices: ["蓄冷释冷循环泵", "板换隔绝阀", "水池分层测温电缆"],
    summary:
      "充分利用上海市峰平谷分时电价策略（谷电 0.35 元，尖峰 1.48 元），在深夜 00:00~06:00 谷电期开满冷水机组向蓄冷水池蓄冷，在白天尖峰段释冷关停主机，实现显著用电成本转移。",
    milestones: [
      {
        id: "m1",
        title: "蓄冷水池斜温层分层特性与有效储冷容量现场水温实测",
        targetDate: "2026-09-30",
        status: "DONE",
        owner: "李工 (暖通工程师)",
      },
      {
        id: "m2",
        title: "基于次日天气负荷预测的最优充放冷排程算法编写",
        targetDate: "2026-10-25",
        status: "IN_PROGRESS",
        owner: "王工 (自控专家)",
      },
      {
        id: "m3",
        title: "谷段深夜自动蓄冷联锁逻辑现场闭环试车",
        targetDate: "2026-11-15",
        status: "PENDING",
        owner: "王工 (自控专家)",
      },
      {
        id: "m4",
        title: "正式并网投入经济优化运行与移峰填谷收益统计",
        targetDate: "2026-12-05",
        status: "PENDING",
        owner: "张工 (节能主管)",
      },
    ],
    technicalHandover: {
      setpointChanges:
        "谷电时段主机出水调至 4.5°C 充冷；尖峰时段水池释冷优先。",
      interlockRules: "水池释冷出水温差 < 1.5°C 时自动切换为机组联合供冷。",
      rollbackTrigger: "蓄冷水泵故障或释冷量不足立即恢复常规主机直供。",
      safetyBoundaries: "水池水位必须维持在 2.8m~3.2m 安全红线之间。",
    },
    mvPlan: {
      ipmvpOption: "Option B",
      measurementBoundary: "蓄水池释冷热量表与分时进线计量电表",
      baselineModel: "无蓄冷调节工况下主机尖峰时段运行电费基线",
      reportingPeriod: "2026-12-01 至 2027-03-31",
      expectedVerifiedSavings: "电量节约 60,000 kWh，电费转移节省 ¥54,000/年",
    },
  },
];

export function getProjectsSummary(
  projects: readonly EnergySavingProject[],
): ProjectsSummary {
  const totalCount = projects.length;
  const inProgressCount = projects.filter(
    (p) => p.stage !== "COMPLETED",
  ).length;
  const completedCount = projects.filter((p) => p.stage === "COMPLETED").length;
  const totalBudgetCNY = projects.reduce((acc, p) => acc + p.budgetCNY, 0);
  const totalExpectedAnnualSavingsKWh = projects.reduce(
    (acc, p) => acc + p.expectedAnnualSavingsKWh,
    0,
  );
  const totalExpectedSavingsCostCNY = projects.reduce(
    (acc, p) => acc + p.expectedAnnualSavingsCostCNY,
    0,
  );

  const onTimeMilestoneRate = 92.5;

  return {
    totalCount,
    inProgressCount,
    completedCount,
    totalBudgetCNY,
    totalExpectedAnnualSavingsKWh,
    totalExpectedSavingsCostCNY,
    onTimeMilestoneRate: Number(onTimeMilestoneRate.toFixed(1)),
  };
}
