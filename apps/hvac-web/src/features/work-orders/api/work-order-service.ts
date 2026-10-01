import type {
  WorkOrderItem,
  WorkOrdersFilterParams,
  WorkOrdersSummary,
} from "./work-order-types";

const MOCK_WORK_ORDERS: WorkOrderItem[] = [
  {
    id: "wo-101",
    code: "WO-202609-101",
    title: "1# 离心冷机电子膨胀阀执行器检修与调校",
    description:
      "针对蒸发温度过低告警，现场开箱排查步进电机传动齿轮滑移，校正节流开度与反馈电位器信号。",
    priority: "URGENT",
    status: "IN_PROGRESS",
    category: "CORRECTIVE",
    sourceAlarmCode: "AL-2026-0891",
    sourceFinding: "实测蒸发温度 1.8℃ 越下限，过热度持续 11.2K，判定供液不足",
    deviceId: "dev-ch-01",
    deviceName: "1# 离心冷水机组",
    subsystem: "冷源系统",
    location: "地下一层冷站主机区",
    assigneeName: "张工",
    assigneeRole: "制冷高级技师",
    teamName: "冷站运维组",
    scheduledStart: "2026-09-30 06:00",
    dueAt: "2026-09-30 12:00",
    durationHoursEstimated: 4.0,
    durationHoursActual: 2.5,
    tasksTotal: 4,
    tasksCompleted: 2,
    checklist: [
      {
        id: "chk-1",
        title: "断开膨胀阀控制回路电源并锁定挂牌",
        requirement: "核验控制回路断电，确认 24VAC 供电安全",
        completed: true,
        operator: "张工",
        completedAt: "09-30 06:15",
      },
      {
        id: "chk-2",
        title: "拆卸步进电机驱动头，检查齿轮传动啮合状态",
        requirement: "使用内六角扳手拆卸，检查是否有尼龙齿轮打滑或磨损粉末",
        completed: true,
        operator: "张工",
        completedAt: "09-30 07:10",
      },
      {
        id: "chk-3",
        title: "更换执行器滑移卡扣并手操全行程校零",
        requirement: "连接手持测试仪驱动 0%~100% 往复 3 次，确认无卡滞",
        completed: false,
      },
      {
        id: "chk-4",
        title: "复原线路通电试车，记录过热度恢复曲线",
        requirement: "连续运行 30 分钟，蒸发过热度应收敛至 4.5~6.0K",
        completed: false,
      },
    ],
    parts: [
      {
        id: "prt-1",
        code: "PART-EXV-450",
        name: "电子膨胀阀执行机构总成",
        specification: "丹佛斯 ETS-50 适配款 24VDC",
        quantity: 1,
        unit: "套",
        inStock: true,
      },
      {
        id: "prt-2",
        code: "PART-SEAL-R134",
        name: "特氟龙耐氟密封圈",
        specification: "DN40 丁腈橡胶",
        quantity: 2,
        unit: "个",
        inStock: true,
      },
    ],
    timeline: [
      {
        id: "tl-1",
        time: "2026-09-30 04:30",
        action: "告警联动自动建单",
        operator: "运维调度引擎",
        detail: "源自 AL-2026-0891 严重告警，系统自动生成紧急检修工单。",
        type: "CREATE",
      },
      {
        id: "tl-2",
        time: "2026-09-30 05:15",
        action: "指派责任人与班组",
        operator: "值班调度王主任",
        detail: "指派制冷专业主管张工，协同冷站运维二班接单。",
        type: "ASSIGN",
      },
      {
        id: "tl-3",
        time: "2026-09-30 06:00",
        action: "现场签到并开始作业",
        operator: "张工",
        detail: "已领取备件出库，进入地下一层冷站现场展开检修。",
        type: "START",
      },
    ],
    createdAt: "2026-09-30 04:30:00",
    updatedAt: "2026-09-30 07:15:00",
  },
  {
    id: "wo-098",
    code: "WO-202609-098",
    title: "3# 闭式冷却塔填料高压水洗与布水喷头清堵",
    description:
      "针对逼近度恶化异常，利用非满载窗口清洗塔芯填料生物泥垢，拆洗疏通 16 只堵塞喷头。",
    priority: "HIGH",
    status: "OPEN",
    category: "CORRECTIVE",
    sourceAlarmCode: "AL-2026-0892",
    sourceFinding: "逼近度达 5.8℃，风机满负荷运行但换热效率衰减 32%",
    deviceId: "dev-ct-03",
    deviceName: "3# 闭式冷却塔",
    subsystem: "冷却系统",
    location: "主楼屋顶冷却塔群组",
    assigneeName: undefined,
    assigneeRole: "暖通技师",
    teamName: "冷却维护班",
    scheduledStart: "2026-09-30 09:30",
    dueAt: "2026-09-30 16:00",
    durationHoursEstimated: 5.0,
    tasksTotal: 3,
    tasksCompleted: 0,
    checklist: [
      {
        id: "chk-11",
        title: "登顶拆卸 3# 塔侧检修盖板与进性格栅",
        requirement: "系挂双挂钩安全带，检查屋顶防滑措施",
        completed: false,
      },
      {
        id: "chk-12",
        title: "逐组拆卸布水支管喷头并酸泡疏通",
        requirement: "清除水垢及青苔杂质，喷雾角度恢复 120 度伞状",
        completed: false,
      },
      {
        id: "chk-13",
        title: "使用高压水枪自上而下冲洗 PVC 填料层",
        requirement: "冲水压力控制在 0.3~0.5 MPa，防止填料破损变形",
        completed: false,
      },
    ],
    parts: [
      {
        id: "prt-11",
        code: "PART-NOZZLE-ABS",
        name: "螺旋布水喷嘴",
        specification: "ABS 螺旋扩散型 G1/2",
        quantity: 16,
        unit: "只",
        inStock: true,
      },
    ],
    timeline: [
      {
        id: "tl-11",
        time: "2026-09-29 15:30",
        action: "创建工单",
        operator: "系统能效建议",
        detail: "生成冷却塔深度清洗维护工单。",
        type: "CREATE",
      },
    ],
    createdAt: "2026-09-29 15:30:00",
    updatedAt: "2026-09-30 06:00:00",
  },
  {
    id: "wo-095",
    code: "WO-202609-095",
    title: "2# 冷冻水泵旁通控制阀机械卡塞消缺",
    description:
      "冷水集分水器旁通电动调节水阀卡在 18% 开度残留无法关严，拆解执行器连杆并研磨密封面。",
    priority: "HIGH",
    status: "IN_PROGRESS",
    category: "CORRECTIVE",
    sourceAlarmCode: "AL-2026-0893",
    sourceFinding: "水泵扬程严重偏离最佳工况点，水力短路导致轴功率浪费 18%",
    deviceId: "dev-chwp-02",
    deviceName: "2# 定速冷冻水泵",
    subsystem: "输配系统",
    location: "地下一层冷站泵房",
    assigneeName: "王工",
    assigneeRole: "暖通班长",
    teamName: "水系统运维组",
    scheduledStart: "2026-09-30 07:00",
    dueAt: "2026-09-30 11:30",
    durationHoursEstimated: 3.5,
    durationHoursActual: 2.0,
    tasksTotal: 4,
    tasksCompleted: 3,
    checklist: [
      {
        id: "chk-21",
        title: "关闭旁通管路前后隔离检修蝶阀并泄压",
        requirement: "确认压力表指针归零，打开泄水旋塞放空余水",
        completed: true,
        operator: "王工",
        completedAt: "09-30 07:15",
      },
      {
        id: "chk-22",
        title: "分离电动执行机构与阀杆连接销轴",
        requirement: "检查销轴是否发生剪切变形或卡槽磨损",
        completed: true,
        operator: "王工",
        completedAt: "09-30 07:45",
      },
      {
        id: "chk-23",
        title: "清理阀腔内焊渣异物并重新装配研磨",
        requirement: "阀瓣手操平顺，落座密封达到严密标准",
        completed: true,
        operator: "王工",
        completedAt: "09-30 08:30",
      },
      {
        id: "chk-24",
        title: "通水试压并校准 DDC 模拟量 0~10V 反馈",
        requirement: "0V 严格对应 0% 全关，无渗漏水滴",
        completed: false,
      },
    ],
    parts: [],
    timeline: [
      {
        id: "tl-21",
        time: "2026-09-28 10:30",
        action: "创建工单并下达",
        operator: "王工",
        detail: "水力短路工单下达。",
        type: "CREATE",
      },
    ],
    createdAt: "2026-09-28 10:30:00",
    updatedAt: "2026-09-30 08:30:00",
  },
  {
    id: "wo-092",
    code: "WO-202609-092",
    title: "1# 变频冷却水泵轴承润滑补油与同轴度校验",
    description:
      "轴承温度达 82.5℃ 触发急停预警，需立即倒备用泵，停机加注高温锂基润滑脂并激光对中。",
    priority: "URGENT",
    status: "OPEN",
    category: "CORRECTIVE",
    sourceAlarmCode: "AL-2026-0896",
    sourceFinding: "轴承温度达 82.5℃ (安全线 75℃)，高频振动 RMS 4.8 mm/s",
    deviceId: "dev-cwp-01",
    deviceName: "1# 变频冷却水泵",
    subsystem: "冷却系统",
    location: "地下一层冷站泵房",
    assigneeName: undefined,
    teamName: "暖通机电组",
    scheduledStart: "2026-09-30 08:30",
    dueAt: "2026-09-30 11:00",
    durationHoursEstimated: 2.5,
    tasksTotal: 3,
    tasksCompleted: 0,
    checklist: [
      {
        id: "chk-31",
        title: "联动切换至备用 3# 冷却水泵，确保主机冷却不中断",
        requirement: "确认 3# 泵平稳升频至 42Hz，冷却水流量稳定",
        completed: false,
      },
      {
        id: "chk-32",
        title: "停机 1# 泵断电锁定，清洗轴承压盖补注 2# 复合锂基脂",
        requirement: "充脂量约为轴承腔容积的 1/2 至 2/3",
        completed: false,
      },
      {
        id: "chk-33",
        title: "使用激光对中仪复核联轴器径向与角向偏差",
        requirement: "径向跳动 ≤0.05mm，端面倾斜 ≤0.04mm",
        completed: false,
      },
    ],
    parts: [
      {
        id: "prt-31",
        code: "PART-GREASE-HP",
        name: "极压高温复合锂基润滑脂",
        specification: "壳牌加适达 2# 1kg装",
        quantity: 1,
        unit: "桶",
        inStock: true,
      },
    ],
    timeline: [
      {
        id: "tl-31",
        time: "2026-09-30 07:15",
        action: "急修派工",
        operator: "系统自动生成",
        detail: "设备重大故障前兆紧急工单。",
        type: "CREATE",
      },
    ],
    createdAt: "2026-09-30 07:15:00",
    updatedAt: "2026-09-30 07:15:00",
  },
  {
    id: "wo-091",
    code: "WO-202609-091",
    title: "东区办公末端 AHU-04 电动二通调节水阀内漏更换",
    description:
      "二通调节水阀关死依然渗漏导致重热与过度制冷，需整体更换 DN65 阀体与执行器。",
    priority: "MEDIUM",
    status: "BLOCKED",
    category: "CORRECTIVE",
    sourceAlarmCode: "AL-2026-0894",
    blockReason: "等待采购原厂 DN65 电动调节水阀阀芯备件 (预计明天下午到货)",
    deviceId: "dev-ahu-04",
    deviceName: "AHU-04 组合式空调箱",
    subsystem: "末端空调",
    location: "东塔 12 层空调机房",
    assigneeName: "赵工",
    assigneeRole: "末端技师",
    teamName: "末端维护班",
    scheduledStart: "2026-09-30 09:00",
    dueAt: "2026-10-01 17:00",
    durationHoursEstimated: 3.0,
    durationHoursActual: 1.0,
    tasksTotal: 3,
    tasksCompleted: 1,
    checklist: [
      {
        id: "chk-41",
        title: "关闭 AHU 表冷器进出口截止阀，现场核定内漏",
        requirement: "测温仪测量阀体前后管道温差证实存在内漏流量",
        completed: true,
        operator: "赵工",
        completedAt: "09-30 07:00",
      },
      {
        id: "chk-42",
        title: "拆除旧阀体，更换法兰密封垫并安装新阀芯",
        requirement: "注意阀体介质流向箭头必须与水流方向一致",
        completed: false,
      },
      {
        id: "chk-43",
        title: "接线调试并在 BAS 上校验 0%、50%、100% 对应行程",
        requirement: "送风温度控制回路死区设定在 ±0.5℃",
        completed: false,
      },
    ],
    parts: [
      {
        id: "prt-41",
        code: "PART-VLV-DN65",
        name: "电动等百分比二通调节阀芯",
        specification: "西门子 VVF42.65",
        quantity: 1,
        unit: "套",
        inStock: false,
      },
    ],
    timeline: [
      {
        id: "tl-41",
        time: "2026-09-30 06:40",
        action: "创建工单",
        operator: "调度值班",
        detail: "末端水阀内漏工单。",
        type: "CREATE",
      },
      {
        id: "tl-42",
        time: "2026-09-30 07:30",
        action: "置为挂起阻塞",
        operator: "赵工",
        detail: "备件库暂无 DN65 备品，已提报紧急采购，工单挂起。",
        type: "BLOCK",
      },
    ],
    createdAt: "2026-09-30 06:40:00",
    updatedAt: "2026-09-30 07:30:00",
  },
  {
    id: "wo-088",
    code: "WO-202609-088",
    title: "全站冷源水质秋季阻垢缓蚀药剂加注与化验",
    description:
      "执行每月常规水质保养，化验浓缩倍数、pH值及浊度，补充水溶性阻垢缓蚀剂与杀菌灭藻剂。",
    priority: "LOW",
    status: "IN_PROGRESS",
    category: "PREVENTIVE",
    deviceId: "dev-chem-01",
    deviceName: "冷水与冷却水加药装置",
    subsystem: "水质处理",
    location: "地下一层化学水处理间",
    assigneeName: "周工",
    assigneeRole: "水质化验工程师",
    teamName: "水质化验组",
    scheduledStart: "2026-09-29 08:30",
    dueAt: "2026-09-30 17:00",
    durationHoursEstimated: 6.0,
    durationHoursActual: 4.0,
    tasksTotal: 3,
    tasksCompleted: 2,
    checklist: [
      {
        id: "chk-51",
        title: "冷却水与冷冻水系统取样检测 pH、电导率及总硬度",
        requirement: "冷却水浓缩倍数应保持在 4.0~5.0 之间",
        completed: true,
        operator: "周工",
        completedAt: "09-29 10:00",
      },
      {
        id: "chk-52",
        title: "自动计量泵校验加药冲程，补注 50kg 环保型缓蚀阻垢剂",
        requirement: "设定加药泵自动比例投加模式与电导率排污联动",
        completed: true,
        operator: "周工",
        completedAt: "09-29 14:00",
      },
      {
        id: "chk-53",
        title: "清洗电导率探头与 ORP 氧化还原探头并双点标定",
        requirement: "标准缓冲液标定误差在 ±1% 以内",
        completed: false,
      },
    ],
    parts: [],
    timeline: [
      {
        id: "tl-51",
        time: "2026-09-29 08:00",
        action: "例行保养建单",
        operator: "维保计划系统",
        detail: "周期性预防维护任务。",
        type: "CREATE",
      },
    ],
    createdAt: "2026-09-29 08:00:00",
    updatedAt: "2026-09-29 14:30:00",
  },
  {
    id: "wo-085",
    code: "WO-202609-085",
    title: "水蓄冷系统放冷换热器板换化学酸洗循环",
    description:
      "针对端温差偏大缺陷，在非营业停放冷夜间窗口，接驳专用酸洗车执行反冲洗与有机酸循环除垢。",
    priority: "MEDIUM",
    status: "BLOCKED",
    category: "OPTIMIZATION",
    sourceAlarmCode: "AL-2026-0898",
    blockReason: "等待商场非营业夜间停运窗口 (预定今晚 23:00 开始切入酸洗)",
    deviceId: "dev-hex-01",
    deviceName: "HEX-01 板式换热器",
    subsystem: "蓄能系统",
    location: "蓄能机房换热间",
    assigneeName: "陈工",
    assigneeRole: "蓄能工程师",
    teamName: "蓄能专责组",
    scheduledStart: "2026-09-30 23:00",
    dueAt: "2026-10-01 05:00",
    durationHoursEstimated: 6.0,
    tasksTotal: 3,
    tasksCompleted: 0,
    checklist: [
      {
        id: "chk-61",
        title: "旁通蓄冰放冷回路，开启 HEX-01 盲板并连接耐酸软管",
        requirement: "确认密封面防酸密封圈安装到位，接驳耐酸循环泵",
        completed: false,
      },
      {
        id: "chk-62",
        title: "注入 5% 柠檬酸与缓蚀剂清洗液循环 180 分钟",
        requirement: "定时测定清洗液 pH 值与钙离子溶出浓度饱和点",
        completed: false,
      },
      {
        id: "chk-63",
        title: "水洗钝化后恢复管路，复测放冷进出水端温差",
        requirement: "实测端温差恢复至设计标准 ≤1.2℃",
        completed: false,
      },
    ],
    parts: [],
    timeline: [
      {
        id: "tl-61",
        time: "2026-09-29 17:00",
        action: "创建专项调优工单",
        operator: "节能方案库",
        detail: "能效提升专项。",
        type: "CREATE",
      },
      {
        id: "tl-62",
        time: "2026-09-30 08:00",
        action: "挂起等待夜间窗口",
        operator: "陈工",
        detail: "白天水蓄冷正在执行削峰放冷，延后至今晚 23:00 执行。",
        type: "BLOCK",
      },
    ],
    createdAt: "2026-09-29 17:00:00",
    updatedAt: "2026-09-30 08:00:00",
  },
  {
    id: "wo-082",
    code: "WO-202609-082",
    title: "西区商场新风机组初中效滤袋整体更换",
    description:
      "针对滤网压差越限提示，更换 PAU-02 全部 12 组 G4 初效及 F7 中效阻燃无纺布袋式过滤器。",
    priority: "LOW",
    status: "COMPLETED",
    category: "PREVENTIVE",
    sourceAlarmCode: "AL-2026-0897",
    deviceId: "dev-pau-02",
    deviceName: "PAU-02 新风处理机组",
    subsystem: "末端空调",
    location: "西区地下一层新风机房",
    assigneeName: "物业维保组",
    assigneeRole: "维修电工",
    teamName: "综合机电班",
    scheduledStart: "2026-09-29 14:00",
    dueAt: "2026-09-29 18:00",
    durationHoursEstimated: 3.0,
    durationHoursActual: 2.2,
    tasksTotal: 3,
    tasksCompleted: 3,
    checklist: [
      {
        id: "chk-71",
        title: "停机新风机组并打开检修门锁",
        requirement: "机组停风并确认负压风门完全卸压",
        completed: true,
        operator: "综合机电班",
        completedAt: "09-29 14:15",
      },
      {
        id: "chk-72",
        title: "拆卸旧积尘滤袋并装入防尘塑封袋运出机房",
        requirement: "机房内吸尘清洁，防止扬尘飞散进入送风管网",
        completed: true,
        operator: "综合机电班",
        completedAt: "09-29 15:30",
      },
      {
        id: "chk-73",
        title: "安装新 G4+F7 滤袋，启动风机复测前后压差",
        requirement: "新滤网初始阻力应稳定在 75 Pa，提交后等待独立恢复验证",
        completed: true,
        operator: "综合机电班",
        completedAt: "09-29 16:20",
      },
    ],
    parts: [
      {
        id: "prt-71",
        code: "PART-FILT-F7",
        name: "袋式中效空气过滤器",
        specification: "592x592x500mm 6袋 F7",
        quantity: 12,
        unit: "只",
        inStock: true,
      },
    ],
    timeline: [
      {
        id: "tl-71",
        time: "2026-09-29 13:00",
        action: "派发工单",
        operator: "维保派单",
        detail: "新风滤网更换。",
        type: "CREATE",
      },
      {
        id: "tl-72",
        time: "2026-09-29 16:30",
        action: "完工提交验收",
        operator: "综合机电班",
        detail: "全部滤网更换完成，待独立观测验证设备恢复。",
        type: "COMPLETE",
      },
    ],
    createdAt: "2026-09-29 13:00:00",
    updatedAt: "2026-09-29 16:30:00",
  },
];

export async function getWorkOrders(
  params?: WorkOrdersFilterParams,
): Promise<WorkOrderItem[]> {
  await new Promise((resolve) => setTimeout(resolve, 80));

  let result = [...MOCK_WORK_ORDERS];

  if (params?.search?.trim()) {
    const q = params.search.trim().toLowerCase();
    result = result.filter(
      (item) =>
        item.title.toLowerCase().includes(q) ||
        item.code.toLowerCase().includes(q) ||
        item.deviceName.toLowerCase().includes(q) ||
        item.location.toLowerCase().includes(q) ||
        (item.assigneeName && item.assigneeName.toLowerCase().includes(q)) ||
        item.teamName.toLowerCase().includes(q) ||
        item.description.toLowerCase().includes(q),
    );
  }

  if (params?.priority && params.priority !== "all") {
    result = result.filter((item) => item.priority === params.priority);
  }

  if (params?.status && params.status !== "all") {
    result = result.filter((item) => item.status === params.status);
  }

  if (params?.category && params.category !== "all") {
    result = result.filter((item) => item.category === params.category);
  }

  if (params?.team && params.team !== "all") {
    result = result.filter((item) => item.teamName === params.team);
  }

  return result;
}

export async function getWorkOrdersSummary(): Promise<WorkOrdersSummary> {
  await new Promise((resolve) => setTimeout(resolve, 50));
  return {
    openCount: 4,
    inProgressCount: 6,
    blockedCount: 2,
    completedCount: 28,
    urgentCount: 2,
    unassignedCount: 1,
    averageExecutionHours: 2.4,
    weeklyClosedCount: 28,
    slaComplianceRate: 94.2,
  };
}
