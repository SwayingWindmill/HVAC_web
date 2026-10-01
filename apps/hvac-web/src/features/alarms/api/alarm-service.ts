import type {
  AlarmHeatmapCell,
  AlarmIssueItem,
  AlarmsFilterParams,
  AlarmsSummary,
  RecurringContributor,
} from "./alarm-types";

const MOCK_ALARM_ISSUES: AlarmIssueItem[] = [
  {
    id: "iss-0891",
    code: "AL-2026-0891",
    title: "1# 离心式冷水机组蒸发温度过低预警",
    findingSummary:
      "实测蒸发温度 1.8℃ (基准 4.5℃)，偏离度 2.7℃，冷水出水温度存在冻管风险且系统能效衰减 14%",
    severity: "CRITICAL",
    state: "OPEN",
    diagnosisState: "ROOT_CAUSE_CONFIRMED",
    rootCauseTitle: "电子膨胀阀步进电机齿轮滑移，实际节流开度不足",
    deviceId: "dev-ch-01",
    deviceLabel: "1# 离心冷水机组",
    subsystem: "冷源系统",
    locationLabel: "地下一层冷站主机区",
    avoidableCostWeekly: 1850,
    avoidableEnergyWeeklyKWh: 2200,
    reliabilityRisk: "CRITICAL",
    comfortImpactHours: 1.5,
    firstOccurredAt: "2026-09-30T04:15:00Z",
    durationFormatted: "4小时18分",
    updatedAt: "2026-09-30T08:20:00Z",
    occurrenceCount: 1,
    assigneeName: undefined,
    acknowledged: false,
    telemetryReadings: [
      {
        pointName: "蒸发温度",
        pointCode: "CH01_TE_EVAP",
        currentValue: 1.8,
        unit: "℃",
        baselineValue: 4.5,
        deviation: -2.7,
        status: "CRITICAL",
      },
      {
        pointName: "蒸发饱和压力",
        pointCode: "CH01_PE_EVAP",
        currentValue: 285.4,
        unit: "kPa",
        baselineValue: 345.0,
        deviation: -59.6,
        status: "WARNING",
      },
      {
        pointName: "冷水供水温度",
        pointCode: "CH01_TW_SUP",
        currentValue: 5.2,
        unit: "℃",
        baselineValue: 7.0,
        deviation: -1.8,
        status: "WARNING",
      },
      {
        pointName: "膨胀阀指令开度",
        pointCode: "CH01_VLV_EXP",
        currentValue: 35.0,
        unit: "%",
        baselineValue: 52.0,
        deviation: -17.0,
        status: "CRITICAL",
      },
    ],
    hypotheses: [
      {
        id: "hyp-01",
        title: "电子膨胀阀执行机构步进电机滑移，实际开度低于反馈信号",
        status: "CONFIRMED",
        confidence: 94,
        rationale:
          "红外测温节流阀阀体温差异常，且排气过热度持续高达 11.2K (正常 4~6K)。",
        supportingEvidenceCount: 5,
        contradictingEvidenceCount: 0,
        verificationMethod: "现场手操阀体校零并测量线圈电阻。",
      },
      {
        id: "hyp-02",
        title: "系统制冷剂 R134a 微量渗漏导致低压侧吸气欠压",
        status: "WEAKENED",
        confidence: 12,
        rationale:
          "停机平衡压力检测在正常阈值范围内 (490 kPa)，无明显充注量不足特征。",
        supportingEvidenceCount: 1,
        contradictingEvidenceCount: 3,
        verificationMethod: "气密性检漏仪探测冷凝器与压缩机法兰。",
      },
      {
        id: "hyp-03",
        title: "蒸发器管束内壁结垢导致换热热阻剧增",
        status: "REJECTED",
        confidence: 4,
        rationale:
          "上月刚完成物理通刷清洗，换热端温差当前为 0.9℃，完全在优秀范围。",
        supportingEvidenceCount: 0,
        contradictingEvidenceCount: 4,
        verificationMethod: "水质化验及污垢热阻核算。",
      },
    ],
    evidences: [
      {
        id: "ev-01",
        sensorName: "蒸发压力传感器 P_evap",
        expectedRange: "330 ~ 360 kPa",
        actualRange: "285.4 kPa",
        isDeviated: true,
        timestamp: "09-30 04:15",
        detail: "低于低限报警阈值 (300 kPa)，触发防冻连锁准备",
      },
      {
        id: "ev-02",
        sensorName: "吸气过热度 Superheat",
        expectedRange: "4.0 ~ 6.5 K",
        actualRange: "11.2 K",
        isDeviated: true,
        timestamp: "09-30 04:20",
        detail: "过热度严重偏高，证实供液量不足",
      },
      {
        id: "ev-03",
        sensorName: "冷水供水温度 T_chw_sup",
        expectedRange: "6.5 ~ 7.5 ℃",
        actualRange: "5.2 ℃",
        isDeviated: true,
        timestamp: "09-30 04:25",
        detail: "出水温度越下限，有结晶冻管潜在威胁",
      },
    ],
    timeline: [
      {
        timestamp: "2026-09-30 04:15:12",
        operation: "触发告警",
        operator: "监控引擎",
        description: "蒸发温度越过下限 2.0℃，系统发布 CRITICAL 级告警。",
        type: "TRIGGER",
      },
      {
        timestamp: "2026-09-30 04:15:18",
        operation: "智能诊断",
        operator: "FDD 诊断内核",
        description:
          "比对时序热力模型，命中规则 [RULE-CH-04: 膨胀阀开度不足与异常节流]，确诊根因为执行器滑移。",
        type: "DIAGNOSE",
      },
      {
        timestamp: "2026-09-30 05:00:00",
        operation: "通知推送",
        operator: "消息中心",
        description: "已向当班工程师张工推送声光与短信告警。",
        type: "TRIGGER",
      },
    ],
  },
  {
    id: "iss-0892",
    code: "AL-2026-0892",
    title: "3# 冷却塔逼近度恶化与风机能效异常",
    findingSummary:
      "实测逼近度 5.8℃ (设计上限 ≤3.5℃)，风机满负荷运行但换热效率衰减 32%，间接导致主机冷凝温度上升 2.1℃",
    severity: "MAJOR",
    state: "INVESTIGATING",
    diagnosisState: "PUBLISHED",
    rootCauseTitle: "填料层局部生物泥垢堵塞及布水喷头部分堵塞",
    deviceId: "dev-ct-03",
    deviceLabel: "3# 闭式冷却塔",
    subsystem: "冷却系统",
    locationLabel: "主楼屋顶冷却塔群组",
    avoidableCostWeekly: 1200,
    avoidableEnergyWeeklyKWh: 1550,
    reliabilityRisk: "MEDIUM",
    comfortImpactHours: 0,
    firstOccurredAt: "2026-09-29T14:30:00Z",
    durationFormatted: "18小时05分",
    updatedAt: "2026-09-30T07:15:00Z",
    occurrenceCount: 3,
    assigneeName: "李强 (值班技师)",
    acknowledged: true,
    acknowledgedAt: "2026-09-29T15:00:00Z",
    telemetryReadings: [
      {
        pointName: "冷却水出水温度",
        pointCode: "CT03_TW_OUT",
        currentValue: 32.8,
        unit: "℃",
        baselineValue: 30.5,
        deviation: 2.3,
        status: "WARNING",
      },
      {
        pointName: "室外湿球温度",
        pointCode: "WEATHER_TW_WET",
        currentValue: 27.0,
        unit: "℃",
        baselineValue: 27.0,
        deviation: 0,
        status: "NORMAL",
      },
      {
        pointName: "冷却塔逼近度",
        pointCode: "CT03_APPROACH",
        currentValue: 5.8,
        unit: "℃",
        baselineValue: 3.5,
        deviation: 2.3,
        status: "CRITICAL",
      },
      {
        pointName: "风机运行频率",
        pointCode: "CT03_FAN_FREQ",
        currentValue: 50.0,
        unit: "Hz",
        baselineValue: 38.0,
        deviation: 12.0,
        status: "WARNING",
      },
    ],
    hypotheses: [
      {
        id: "hyp-11",
        title: "冷却塔布水喷头局部钙化堵塞，水膜分布不均",
        status: "SUPPORTED",
        confidence: 88,
        rationale:
          "红外航拍热成像显示塔芯填料存在纵向干区条带，淋水覆盖率不足 70%。",
        supportingEvidenceCount: 3,
        contradictingEvidenceCount: 0,
        verificationMethod: "现场登顶开检修孔目视检查喷头流态。",
      },
      {
        id: "hyp-12",
        title: "风机叶片积垢导致风量下降",
        status: "WEAKENED",
        confidence: 25,
        rationale:
          "风机全压与进出风速实测仅下降 6%，不足以解释 2.3℃ 逼近度恶化。",
        supportingEvidenceCount: 1,
        contradictingEvidenceCount: 2,
        verificationMethod: "风速仪网格测定喉部平均出风量。",
      },
    ],
    evidences: [
      {
        id: "ev-11",
        sensorName: "逼近度指标 Approach",
        expectedRange: "2.5 ~ 3.5 ℃",
        actualRange: "5.8 ℃",
        isDeviated: true,
        timestamp: "09-29 14:30",
        detail: "超出设计逼近度 2.3℃，能耗恶化显著",
      },
      {
        id: "ev-12",
        sensorName: "风机功率单耗",
        expectedRange: "0.02 ~ 0.03 kW/kW_th",
        actualRange: "0.045 kW/kW_th",
        isDeviated: true,
        timestamp: "09-29 16:00",
        detail: "风机打满 50Hz 仍无法将回水降温至设定值",
      },
    ],
    timeline: [
      {
        timestamp: "2026-09-29 14:30:00",
        operation: "触发告警",
        operator: "监控引擎",
        description: "冷却塔逼近度恶化超过 4.5℃ 持续 30 分钟。",
        type: "TRIGGER",
      },
      {
        timestamp: "2026-09-29 15:00:00",
        operation: "确认告警",
        operator: "李强 (值班技师)",
        description: "确认现场告警信息，进入排查阶段。",
        type: "ACKNOWLEDGE",
      },
    ],
  },
  {
    id: "iss-0893",
    code: "AL-2026-0893",
    title: "2# 冷冻水循环泵运行效率偏低与工作点偏离",
    findingSummary:
      "并联运行下扬程偏离最佳效率区 28%，水泵电机单耗 0.042 kW/m³ (基准 0.028 kW/m³)",
    severity: "MAJOR",
    state: "ACTION_PENDING",
    diagnosisState: "ROOT_CAUSE_CONFIRMED",
    rootCauseTitle: "末端压差平衡阀阻抗不匹配，旁通旁流导致泵工况点严重右移",
    deviceId: "dev-chwp-02",
    deviceLabel: "2# 定速冷冻水泵",
    subsystem: "输配系统",
    locationLabel: "地下一层冷站泵房",
    avoidableCostWeekly: 800,
    avoidableEnergyWeeklyKWh: 950,
    reliabilityRisk: "LOW",
    comfortImpactHours: 0,
    firstOccurredAt: "2026-09-28T09:00:00Z",
    durationFormatted: "47小时15分",
    updatedAt: "2026-09-30T06:45:00Z",
    occurrenceCount: 2,
    assigneeName: "王工 (暖通班长)",
    acknowledged: true,
    acknowledgedAt: "2026-09-28T10:15:00Z",
    telemetryReadings: [
      {
        pointName: "水泵进出口压差",
        pointCode: "CHWP02_DP",
        currentValue: 185.0,
        unit: "kPa",
        baselineValue: 260.0,
        deviation: -75.0,
        status: "WARNING",
      },
      {
        pointName: "瞬时流量",
        pointCode: "CHWP02_FLOW",
        currentValue: 380.0,
        unit: "m³/h",
        baselineValue: 280.0,
        deviation: 100.0,
        status: "CRITICAL",
      },
    ],
    hypotheses: [
      {
        id: "hyp-21",
        title: "集分水器旁通电动阀未关严，冷水短路旁流",
        status: "CONFIRMED",
        confidence: 91,
        rationale: "压差极低而流量暴增，典型管网阻抗急剧偏低短路特征。",
        supportingEvidenceCount: 4,
        contradictingEvidenceCount: 0,
        verificationMethod: "关闭旁通检修蝶阀并复测流量压差。",
      },
    ],
    evidences: [
      {
        id: "ev-21",
        sensorName: "旁通阀位置反馈",
        expectedRange: "0% (关死)",
        actualRange: "18% 开度残留",
        isDeviated: true,
        timestamp: "09-28 09:10",
        detail: "控制器指令为 0，但执行器机构机械卡涩未能落座",
      },
    ],
    timeline: [
      {
        timestamp: "2026-09-28 09:00:00",
        operation: "触发告警",
        operator: "系统自动生成",
        description: "输配管网水力平衡失调告警。",
        type: "TRIGGER",
      },
      {
        timestamp: "2026-09-28 10:15:00",
        operation: "确认并派工准备",
        operator: "王工",
        description: "安排排查旁通阀卡塞问题。",
        type: "ACKNOWLEDGE",
      },
    ],
  },
  {
    id: "iss-0894",
    code: "AL-2026-0894",
    title: "东区办公末端 AHU-04 送风温度震荡与阀门内漏",
    findingSummary:
      "二通调节水阀关死 (0%) 时，表冷器出水温升仍有 1.8℃，导致过度制冷与重热能耗",
    severity: "MINOR",
    state: "OPEN",
    diagnosisState: "EVIDENCE_LIMITED",
    deviceId: "dev-ahu-04",
    deviceLabel: "AHU-04 组合式空调箱",
    subsystem: "末端空调",
    locationLabel: "东塔 12 层空调机房",
    avoidableCostWeekly: 450,
    avoidableEnergyWeeklyKWh: 500,
    reliabilityRisk: "LOW",
    comfortImpactHours: 2.0,
    firstOccurredAt: "2026-09-30T06:10:00Z",
    durationFormatted: "2小时15分",
    updatedAt: "2026-09-30T08:15:00Z",
    occurrenceCount: 1,
    assigneeName: undefined,
    acknowledged: false,
    telemetryReadings: [
      {
        pointName: "送风温度",
        pointCode: "AHU04_T_SUP",
        currentValue: 14.2,
        unit: "℃",
        baselineValue: 17.5,
        deviation: -3.3,
        status: "WARNING",
      },
      {
        pointName: "水阀控制开度",
        pointCode: "AHU04_VLV_CMD",
        currentValue: 0.0,
        unit: "%",
        baselineValue: 0.0,
        deviation: 0,
        status: "NORMAL",
      },
    ],
    hypotheses: [
      {
        id: "hyp-31",
        title: "水阀阀芯磨损或密封面有焊渣卡涩，造成关断不严内漏",
        status: "SUPPORTED",
        confidence: 76,
        rationale: "水阀完全关闭时，回水温度仍显著低于房间设定回水温。",
        supportingEvidenceCount: 2,
        contradictingEvidenceCount: 0,
        verificationMethod: "手摸阀后铜管温度或拆开检修。",
      },
    ],
    evidences: [
      {
        id: "ev-31",
        sensorName: "表冷器出水温升",
        expectedRange: "0.0 ℃ (关断无水流)",
        actualRange: "1.8 ℃ 温升",
        isDeviated: true,
        timestamp: "09-30 06:20",
        detail: "冷水在阀门关闭状态下仍持续泄漏流入",
      },
    ],
    timeline: [
      {
        timestamp: "2026-09-30 06:10:00",
        operation: "触发告警",
        operator: "系统规则",
        description: "末端过冷及阀门零位内漏告警。",
        type: "TRIGGER",
      },
    ],
  },
  {
    id: "iss-0895",
    code: "AL-2026-0895",
    title: "2# 离心冷机冷凝器进出水温差不足 (大流量小温差)",
    findingSummary:
      "冷凝水循环流量超标 22%，但进出水温差仅 3.1℃ (设计 5.0℃)，水泵能耗浪费达 35%",
    severity: "WARNING",
    state: "INVESTIGATING",
    diagnosisState: "PUBLISHED",
    rootCauseTitle: "冷却水泵恒频满载运行，未跟随主机排热负荷动态调节变频",
    deviceId: "dev-ch-02",
    deviceLabel: "2# 离心冷水机组",
    subsystem: "冷源系统",
    locationLabel: "地下一层冷站主机区",
    avoidableCostWeekly: 650,
    avoidableEnergyWeeklyKWh: 800,
    reliabilityRisk: "LOW",
    comfortImpactHours: 0,
    firstOccurredAt: "2026-09-29T10:00:00Z",
    durationFormatted: "22小时20分",
    updatedAt: "2026-09-30T07:30:00Z",
    occurrenceCount: 2,
    assigneeName: "陈工",
    acknowledged: true,
    acknowledgedAt: "2026-09-29T11:20:00Z",
    telemetryReadings: [
      {
        pointName: "冷凝器进出水温差",
        pointCode: "CH02_CDW_DT",
        currentValue: 3.1,
        unit: "℃",
        baselineValue: 5.0,
        deviation: -1.9,
        status: "WARNING",
      },
      {
        pointName: "冷却水流量",
        pointCode: "CH02_CDW_FLOW",
        currentValue: 620.0,
        unit: "m³/h",
        baselineValue: 480.0,
        deviation: 140.0,
        status: "WARNING",
      },
    ],
    hypotheses: [
      {
        id: "hyp-41",
        title: "冷却水变频群控逻辑被现场切为就地手动工频",
        status: "SUPPORTED",
        confidence: 85,
        rationale: "变频柜状态信号显示“就地手动控制”，未响应温差调节指令。",
        supportingEvidenceCount: 3,
        contradictingEvidenceCount: 0,
        verificationMethod: "将控制柜旋钮切回“远程自动”。",
      },
    ],
    evidences: [
      {
        id: "ev-41",
        sensorName: "变频柜工作模式",
        expectedRange: "远程自动 (REMOTE)",
        actualRange: "就地手动 (LOCAL_50HZ)",
        isDeviated: true,
        timestamp: "09-29 10:15",
        detail: "导致循环水泵全天候工频恒速打满",
      },
    ],
    timeline: [
      {
        timestamp: "2026-09-29 10:00:00",
        operation: "触发告警",
        operator: "能效诊断引擎",
        description: "冷凝水小温差能耗浪费提示。",
        type: "TRIGGER",
      },
    ],
  },
  {
    id: "iss-0896",
    code: "AL-2026-0896",
    title: "1# 变频冷却水泵电机轴承温度超限预警",
    findingSummary:
      "驱动端轴承温度 82.5℃ (安全警戒线 75℃)，高频振动加速度超标 40%，存在轴承抱死重大风险",
    severity: "CRITICAL",
    state: "OPEN",
    diagnosisState: "ROOT_CAUSE_CONFIRMED",
    rootCauseTitle: "轴承润滑脂变质硬化且联轴器同轴度偏差超标",
    deviceId: "dev-cwp-01",
    deviceLabel: "1# 变频冷却水泵",
    subsystem: "冷却系统",
    locationLabel: "地下一层冷站泵房",
    avoidableCostWeekly: 2100,
    avoidableEnergyWeeklyKWh: 0,
    reliabilityRisk: "CRITICAL",
    comfortImpactHours: 0,
    firstOccurredAt: "2026-09-30T07:05:00Z",
    durationFormatted: "1小时20分",
    updatedAt: "2026-09-30T08:10:00Z",
    occurrenceCount: 1,
    assigneeName: undefined,
    acknowledged: false,
    telemetryReadings: [
      {
        pointName: "电机轴承温度",
        pointCode: "CWP01_T_BEAR",
        currentValue: 82.5,
        unit: "℃",
        baselineValue: 65.0,
        deviation: 17.5,
        status: "CRITICAL",
      },
      {
        pointName: "振动烈度 RMS",
        pointCode: "CWP01_VIB_RMS",
        currentValue: 4.8,
        unit: "mm/s",
        baselineValue: 2.8,
        deviation: 2.0,
        status: "WARNING",
      },
    ],
    hypotheses: [
      {
        id: "hyp-51",
        title: "润滑脂干涸结焦，高速运转产生严重摩擦热",
        status: "CONFIRMED",
        confidence: 96,
        rationale: "振动频谱分析中出现典型轴承滚珠外圈点蚀特征频率。",
        supportingEvidenceCount: 4,
        contradictingEvidenceCount: 0,
        verificationMethod: "现场停泵盘车检查异响并补注高温锂基润滑脂。",
      },
    ],
    evidences: [
      {
        id: "ev-51",
        sensorName: "温度越限报警",
        expectedRange: "< 75.0 ℃",
        actualRange: "82.5 ℃",
        isDeviated: true,
        timestamp: "09-30 07:05",
        detail: "超出报警上限，需立即倒泵检修",
      },
    ],
    timeline: [
      {
        timestamp: "2026-09-30 07:05:00",
        operation: "触发严重告警",
        operator: "状态感知传感器",
        description: "水泵轴承温升速率超过 1.5℃/min 并突破上限。",
        type: "TRIGGER",
      },
      {
        timestamp: "2026-09-30 07:06:00",
        operation: "联锁联动建议",
        operator: "设备保护专家系统",
        description: "建议调度切入备用 3# 冷却水泵，停机 1# 水泵检查。",
        type: "DIAGNOSE",
      },
    ],
  },
  {
    id: "iss-0897",
    code: "AL-2026-0897",
    title: "西区商场新风机组滤网压差超限提示",
    findingSummary:
      "初中效过滤段前后压差达 225 Pa (清洗预警线 180 Pa)，导致送风阻力上升且风机耗电增加 8%",
    severity: "INFO",
    state: "ACTION_PENDING",
    diagnosisState: "ROOT_CAUSE_CONFIRMED",
    rootCauseTitle: "秋季粉尘积聚达周期性清洗更换标准",
    deviceId: "dev-pau-02",
    deviceLabel: "PAU-02 新风处理机组",
    subsystem: "末端空调",
    locationLabel: "西区地下一层新风机房",
    avoidableCostWeekly: 180,
    avoidableEnergyWeeklyKWh: 200,
    reliabilityRisk: "LOW",
    comfortImpactHours: 0,
    firstOccurredAt: "2026-09-27T10:00:00Z",
    durationFormatted: "70小时25分",
    updatedAt: "2026-09-30T05:00:00Z",
    occurrenceCount: 1,
    assigneeName: "物业保洁/维保组",
    acknowledged: true,
    acknowledgedAt: "2026-09-27T14:00:00Z",
    telemetryReadings: [
      {
        pointName: "过滤器压差",
        pointCode: "PAU02_DP_FILT",
        currentValue: 225.0,
        unit: "Pa",
        baselineValue: 120.0,
        deviation: 105.0,
        status: "WARNING",
      },
    ],
    hypotheses: [
      {
        id: "hyp-61",
        title: "滤网积尘饱和，需例行水洗或更换滤袋",
        status: "CONFIRMED",
        confidence: 99,
        rationale: "已运行 95 天，已达额定维护周期。",
        supportingEvidenceCount: 2,
        contradictingEvidenceCount: 0,
        verificationMethod: "现场目视检查滤袋积尘饱满度。",
      },
    ],
    evidences: [
      {
        id: "ev-61",
        sensorName: "差压变送器 DP",
        expectedRange: "< 180 Pa",
        actualRange: "225 Pa",
        isDeviated: true,
        timestamp: "09-27 10:00",
        detail: "超出阻力上限",
      },
    ],
    timeline: [
      {
        timestamp: "2026-09-27 10:00:00",
        operation: "触发例行维护提示",
        operator: "运维管理规则",
        description: "新风过滤器维护提示。",
        type: "TRIGGER",
      },
    ],
  },
  {
    id: "iss-0898",
    code: "AL-2026-0898",
    title: "水蓄冷系统放冷换热器板换端温差偏大",
    findingSummary:
      "换热端温差达 2.4℃ (设计 ≤1.2℃)，影响谷电蓄能释放速率，致使尖峰电价时段需开启额外冷机",
    severity: "WARNING",
    state: "ACTION_PENDING",
    diagnosisState: "PUBLISHED",
    rootCauseTitle: "二次侧板式换热器水垢沉积增加热阻",
    deviceId: "dev-hex-01",
    deviceLabel: "HEX-01 板式换热器",
    subsystem: "蓄能系统",
    locationLabel: "蓄能机房换热间",
    avoidableCostWeekly: 550,
    avoidableEnergyWeeklyKWh: 620,
    reliabilityRisk: "LOW",
    comfortImpactHours: 0,
    firstOccurredAt: "2026-09-29T16:00:00Z",
    durationFormatted: "16小时25分",
    updatedAt: "2026-09-30T07:50:00Z",
    occurrenceCount: 1,
    assigneeName: "张工",
    acknowledged: true,
    acknowledgedAt: "2026-09-29T16:30:00Z",
    telemetryReadings: [
      {
        pointName: "换热端温差",
        pointCode: "HEX01_DT_APPROACH",
        currentValue: 2.4,
        unit: "℃",
        baselineValue: 1.2,
        deviation: 1.2,
        status: "WARNING",
      },
    ],
    hypotheses: [
      {
        id: "hyp-71",
        title: "板换流道水垢堵塞及泥沙附着",
        status: "SUPPORTED",
        confidence: 82,
        rationale: "反冲洗后压降轻微恢复但温差仍未达标，需化学酸洗。",
        supportingEvidenceCount: 3,
        contradictingEvidenceCount: 0,
        verificationMethod: "测量进出水压差与端温差变化。",
      },
    ],
    evidences: [
      {
        id: "ev-71",
        sensorName: "端温差指标",
        expectedRange: "< 1.2 ℃",
        actualRange: "2.4 ℃",
        isDeviated: true,
        timestamp: "09-29 16:00",
        detail: "超出设计换热允许温差",
      },
    ],
    timeline: [
      {
        timestamp: "2026-09-29 16:00:00",
        operation: "触发效能告警",
        operator: "蓄能优化算法",
        description: "换热效率不达标预警。",
        type: "TRIGGER",
      },
    ],
  },
];

const MOCK_HEATMAP_DATA: AlarmHeatmapCell[] = [
  // 周一
  { dayOfWeek: "周一", hourRange: "00:00-04:00", count: 1, level: 1 },
  { dayOfWeek: "周一", hourRange: "04:00-08:00", count: 3, level: 2 },
  { dayOfWeek: "周一", hourRange: "08:00-12:00", count: 8, level: 4 },
  { dayOfWeek: "周一", hourRange: "12:00-16:00", count: 5, level: 3 },
  { dayOfWeek: "周一", hourRange: "16:00-20:00", count: 4, level: 2 },
  { dayOfWeek: "周一", hourRange: "20:00-24:00", count: 2, level: 1 },
  // 周二
  { dayOfWeek: "周二", hourRange: "00:00-04:00", count: 0, level: 0 },
  { dayOfWeek: "周二", hourRange: "04:00-08:00", count: 2, level: 1 },
  { dayOfWeek: "周二", hourRange: "08:00-12:00", count: 7, level: 3 },
  { dayOfWeek: "周二", hourRange: "12:00-16:00", count: 6, level: 3 },
  { dayOfWeek: "周二", hourRange: "16:00-20:00", count: 3, level: 2 },
  { dayOfWeek: "周二", hourRange: "20:00-24:00", count: 1, level: 1 },
  // 周三
  { dayOfWeek: "周三", hourRange: "00:00-04:00", count: 1, level: 1 },
  { dayOfWeek: "周三", hourRange: "04:00-08:00", count: 4, level: 2 },
  { dayOfWeek: "周三", hourRange: "08:00-12:00", count: 9, level: 4 },
  { dayOfWeek: "周三", hourRange: "12:00-16:00", count: 7, level: 3 },
  { dayOfWeek: "周三", hourRange: "16:00-20:00", count: 2, level: 1 },
  { dayOfWeek: "周三", hourRange: "20:00-24:00", count: 1, level: 1 },
  // 周四
  { dayOfWeek: "周四", hourRange: "00:00-04:00", count: 0, level: 0 },
  { dayOfWeek: "周四", hourRange: "04:00-08:00", count: 2, level: 1 },
  { dayOfWeek: "周四", hourRange: "08:00-12:00", count: 6, level: 3 },
  { dayOfWeek: "周四", hourRange: "12:00-16:00", count: 5, level: 2 },
  { dayOfWeek: "周四", hourRange: "16:00-20:00", count: 3, level: 2 },
  { dayOfWeek: "周四", hourRange: "20:00-24:00", count: 0, level: 0 },
  // 周五
  { dayOfWeek: "周五", hourRange: "00:00-04:00", count: 2, level: 1 },
  { dayOfWeek: "周五", hourRange: "04:00-08:00", count: 3, level: 2 },
  { dayOfWeek: "周五", hourRange: "08:00-12:00", count: 8, level: 4 },
  { dayOfWeek: "周五", hourRange: "12:00-16:00", count: 6, level: 3 },
  { dayOfWeek: "周五", hourRange: "16:00-20:00", count: 4, level: 2 },
  { dayOfWeek: "周五", hourRange: "20:00-24:00", count: 1, level: 1 },
  // 周六
  { dayOfWeek: "周六", hourRange: "00:00-04:00", count: 0, level: 0 },
  { dayOfWeek: "周六", hourRange: "04:00-08:00", count: 1, level: 1 },
  { dayOfWeek: "周六", hourRange: "08:00-12:00", count: 2, level: 1 },
  { dayOfWeek: "周六", hourRange: "12:00-16:00", count: 2, level: 1 },
  { dayOfWeek: "周六", hourRange: "16:00-20:00", count: 1, level: 1 },
  { dayOfWeek: "周六", hourRange: "20:00-24:00", count: 0, level: 0 },
  // 周日
  { dayOfWeek: "周日", hourRange: "00:00-04:00", count: 0, level: 0 },
  { dayOfWeek: "周日", hourRange: "04:00-08:00", count: 1, level: 1 },
  { dayOfWeek: "周日", hourRange: "08:00-12:00", count: 1, level: 1 },
  { dayOfWeek: "周日", hourRange: "12:00-16:00", count: 2, level: 1 },
  { dayOfWeek: "周日", hourRange: "16:00-20:00", count: 1, level: 1 },
  { dayOfWeek: "周日", hourRange: "20:00-24:00", count: 0, level: 0 },
];

const MOCK_RECURRING_CONTRIBUTORS: RecurringContributor[] = [
  {
    deviceName: "1# 离心冷水机组",
    subsystem: "冷源系统",
    alarmCount: 14,
    avoidableCost: 4500,
    percentage: 32,
  },
  {
    deviceName: "3# 闭式冷却塔",
    subsystem: "冷却系统",
    alarmCount: 11,
    avoidableCost: 3200,
    percentage: 25,
  },
  {
    deviceName: "1# 变频冷却水泵",
    subsystem: "冷却系统",
    alarmCount: 8,
    avoidableCost: 2600,
    percentage: 18,
  },
  {
    deviceName: "AHU-04 空调机组",
    subsystem: "末端空调",
    alarmCount: 6,
    avoidableCost: 1100,
    percentage: 14,
  },
  {
    deviceName: "HEX-01 板式换热器",
    subsystem: "蓄能系统",
    alarmCount: 4,
    avoidableCost: 850,
    percentage: 11,
  },
];

export async function getAlarms(
  params?: AlarmsFilterParams,
): Promise<AlarmIssueItem[]> {
  await new Promise((resolve) => setTimeout(resolve, 80));

  let result = [...MOCK_ALARM_ISSUES];

  if (params?.search?.trim()) {
    const q = params.search.trim().toLowerCase();
    result = result.filter(
      (item) =>
        item.title.toLowerCase().includes(q) ||
        item.code.toLowerCase().includes(q) ||
        item.deviceLabel.toLowerCase().includes(q) ||
        item.locationLabel.toLowerCase().includes(q) ||
        item.findingSummary.toLowerCase().includes(q),
    );
  }

  if (params?.severity && params.severity !== "all") {
    result = result.filter((item) => item.severity === params.severity);
  }

  if (params?.state && params.state !== "all") {
    result = result.filter((item) => item.state === params.state);
  }

  if (params?.diagnosis && params.diagnosis !== "all") {
    result = result.filter((item) => item.diagnosisState === params.diagnosis);
  }

  if (params?.subsystem && params.subsystem !== "all") {
    result = result.filter((item) => item.subsystem === params.subsystem);
  }

  return result;
}

export async function getAlarmsSummary(): Promise<AlarmsSummary> {
  await new Promise((resolve) => setTimeout(resolve, 50));
  return {
    activeCount: 8,
    criticalCount: 2,
    majorCount: 3,
    minorCount: 3,
    unacknowledgedCount: 2,
    averageMttaMinutes: 6.5,
    averageMttrMinutes: 42.0,
    confirmedRootCauseCount: 5,
    diagnosisCoverageRate: 85,
    avoidableCostWeeklyTotal: 3850,
    avoidableEnergyWeeklyTotalKWh: 4600,
  };
}

export async function getAlarmHeatmap(): Promise<AlarmHeatmapCell[]> {
  await new Promise((resolve) => setTimeout(resolve, 50));
  return MOCK_HEATMAP_DATA;
}

export async function getRecurringContributors(): Promise<
  RecurringContributor[]
> {
  await new Promise((resolve) => setTimeout(resolve, 50));
  return MOCK_RECURRING_CONTRIBUTORS;
}
