import type {
  MvProjectRecord,
  VerificationSummary,
} from "./verification-types";

export const MOCK_MV_PROJECTS: readonly MvProjectRecord[] = [
  {
    id: "mv-001",
    code: "MV-2026-CHW-01",
    projectName: "1#站房冷水主机供水温度阶梯自适应重设",
    sourceProjectCode: "PRJ-CHW-01",
    ipmvpOption: "Option B",
    subsystem: "CHILLER",
    baselinePeriod: "2025-06-01 至 2025-09-30 (基线负荷期)",
    reportingPeriod: "2026-06-01 至 2026-09-30 (报告监测期)",
    measurementBoundary: "1#站房离心冷水主机回路电表及冷冻水进出水冷量表",
    r2: 0.96,
    cvRmse: 4.8,
    nmbe: -1.2,
    verifiedSavingsKWh: 68500,
    verifiedSavingsCostCNY: 58200,
    targetSavingsKWh: 65000,
    realizationRate: 105.4,
    status: "CERTIFIED",
    verifier: "陈工 (CMVP 认证验证师)",
    lastAuditDate: "2026-10-01",
    adjustments: [
      {
        id: "adj-1",
        type: "ROUTINE_WEATHER",
        label: "CDD 气象常规归一化调整",
        description:
          "2026 年夏季平均室外气温较 2025 年同期偏高 1.2°C，依据度日数模型上调基准冷负荷。",
        adjustmentDeltaKWh: 4200,
      },
      {
        id: "adj-2",
        type: "OCCUPANCY",
        label: "主力商户入驻率提升非例行调整",
        description:
          "商场三层新开业餐饮区增加散热负荷，通过增加基准耗电量确保节能量测算公正性。",
        adjustmentDeltaKWh: 2100,
      },
    ],
    monthlyRecords: [
      {
        month: "2026-06",
        rawActualKWh: 112000,
        adjustedBaselineKWh: 128500,
        verifiedSavingsKWh: 16500,
        verifiedCostCNY: 14025,
        cdd: 185,
        realizationRate: 103.1,
      },
      {
        month: "2026-07",
        rawActualKWh: 145000,
        adjustedBaselineKWh: 164200,
        verifiedSavingsKWh: 19200,
        verifiedCostCNY: 16320,
        cdd: 242,
        realizationRate: 106.7,
      },
      {
        month: "2026-08",
        rawActualKWh: 142000,
        adjustedBaselineKWh: 160800,
        verifiedSavingsKWh: 18800,
        verifiedCostCNY: 15980,
        cdd: 236,
        realizationRate: 104.4,
      },
      {
        month: "2026-09",
        rawActualKWh: 108000,
        adjustedBaselineKWh: 122000,
        verifiedSavingsKWh: 14000,
        verifiedCostCNY: 11875,
        cdd: 172,
        realizationRate: 107.7,
      },
    ],
  },
  {
    id: "mv-002",
    code: "MV-2026-PUMP-02",
    projectName: "冷水二次泵最不利环路变频压差闭环改造",
    sourceProjectCode: "PRJ-PUMP-02",
    ipmvpOption: "Option A",
    subsystem: "PUMP",
    baselinePeriod: "2025-07-01 至 2025-09-30",
    reportingPeriod: "2026-07-01 至 2026-09-30",
    measurementBoundary: "冷冻水循环水泵变频配电柜进线智能多功能电表",
    r2: 0.94,
    cvRmse: 6.2,
    nmbe: 0.8,
    verifiedSavingsKWh: 42000,
    verifiedSavingsCostCNY: 34500,
    targetSavingsKWh: 42000,
    realizationRate: 100.0,
    status: "VERIFYING",
    verifier: "陈工 (CMVP 认证验证师)",
    lastAuditDate: "2026-09-25",
    adjustments: [
      {
        id: "adj-1",
        type: "ROUTINE_WEATHER",
        label: "循环水管网阻力常规修正",
        description: "根据实际开泵台数时数加权水泵电耗基线。",
        adjustmentDeltaKWh: 1200,
      },
    ],
    monthlyRecords: [
      {
        month: "2026-07",
        rawActualKWh: 38200,
        adjustedBaselineKWh: 52400,
        verifiedSavingsKWh: 14200,
        verifiedCostCNY: 11644,
        cdd: 242,
        realizationRate: 101.4,
      },
      {
        month: "2026-08",
        rawActualKWh: 37900,
        adjustedBaselineKWh: 51800,
        verifiedSavingsKWh: 13900,
        verifiedCostCNY: 11398,
        cdd: 236,
        realizationRate: 99.3,
      },
      {
        month: "2026-09",
        rawActualKWh: 36500,
        adjustedBaselineKWh: 50400,
        verifiedSavingsKWh: 13900,
        verifiedCostCNY: 11458,
        cdd: 172,
        realizationRate: 99.3,
      },
    ],
  },
  {
    id: "mv-003",
    code: "MV-2026-TOWER-03",
    projectName: "板式换热器水侧自然冷却 (Free Cooling) 节能核证",
    sourceProjectCode: "PRJ-TOWER-03",
    ipmvpOption: "Option B",
    subsystem: "TOWER",
    baselinePeriod: "2024-11-01 至 2025-02-28 (历史无板换供冷季)",
    reportingPeriod: "2025-11-01 至 2026-02-28 (板换投运季)",
    measurementBoundary: "冷站高压冷水主机及屋顶冷却塔风机总电度表",
    r2: 0.97,
    cvRmse: 3.9,
    nmbe: -0.5,
    verifiedSavingsKWh: 85000,
    verifiedSavingsCostCNY: 72000,
    targetSavingsKWh: 85000,
    realizationRate: 100.0,
    status: "CERTIFIED",
    verifier: "李工 (暖通工程师)",
    lastAuditDate: "2026-08-30",
    adjustments: [],
    monthlyRecords: [
      {
        month: "2025-11",
        rawActualKWh: 18500,
        adjustedBaselineKWh: 36500,
        verifiedSavingsKWh: 18000,
        verifiedCostCNY: 15300,
        cdd: 0,
        realizationRate: 100.0,
      },
      {
        month: "2025-12",
        rawActualKWh: 12000,
        adjustedBaselineKWh: 37000,
        verifiedSavingsKWh: 25000,
        verifiedCostCNY: 21250,
        cdd: 0,
        realizationRate: 104.2,
      },
      {
        month: "2026-01",
        rawActualKWh: 11500,
        adjustedBaselineKWh: 37500,
        verifiedSavingsKWh: 26000,
        verifiedCostCNY: 22100,
        cdd: 0,
        realizationRate: 100.0,
      },
      {
        month: "2026-02",
        rawActualKWh: 16000,
        adjustedBaselineKWh: 32000,
        verifiedSavingsKWh: 16000,
        verifiedCostCNY: 13350,
        cdd: 0,
        realizationRate: 94.1,
      },
    ],
  },
  {
    id: "mv-004",
    code: "MV-2026-TERM-04",
    projectName: "非营业时段商铺及办公末端待机能耗闭锁",
    sourceProjectCode: "PRJ-TERM-04",
    ipmvpOption: "Option C",
    subsystem: "TERMINAL",
    baselinePeriod: "2026-03-01 至 2026-05-31",
    reportingPeriod: "2026-07-01 至 2026-09-30",
    measurementBoundary: "商业主楼全楼层低压动力照明配电总进线回路",
    r2: 0.91,
    cvRmse: 8.5,
    nmbe: 2.1,
    verifiedSavingsKWh: 21300,
    verifiedSavingsCostCNY: 19500,
    targetSavingsKWh: 23000,
    realizationRate: 92.6,
    status: "ADJUSTMENT_REQUIRED",
    verifier: "张工 (节能主管)",
    lastAuditDate: "2026-09-20",
    adjustments: [
      {
        id: "adj-1",
        type: "OPERATIONAL_HOURS",
        label: "商铺闭店后违规加班用电漂移",
        description:
          "部分重餐饮租户闭店后夜间备料延长 2 小时，需排除非空调系统生活用电。",
        adjustmentDeltaKWh: -1700,
      },
    ],
    monthlyRecords: [
      {
        month: "2026-07",
        rawActualKWh: 68500,
        adjustedBaselineKWh: 75500,
        verifiedSavingsKWh: 7000,
        verifiedCostCNY: 6440,
        cdd: 242,
        realizationRate: 91.3,
      },
      {
        month: "2026-08",
        rawActualKWh: 69200,
        adjustedBaselineKWh: 76400,
        verifiedSavingsKWh: 7200,
        verifiedCostCNY: 6624,
        cdd: 236,
        realizationRate: 93.9,
      },
      {
        month: "2026-09",
        rawActualKWh: 64100,
        adjustedBaselineKWh: 71200,
        verifiedSavingsKWh: 7100,
        verifiedCostCNY: 6436,
        cdd: 172,
        realizationRate: 92.6,
      },
    ],
  },
];

export function getVerificationSummary(
  projects: readonly MvProjectRecord[],
): VerificationSummary {
  const totalVerifiedSavingsKWh = projects.reduce(
    (acc, p) => acc + p.verifiedSavingsKWh,
    0,
  );
  const totalVerifiedCostCNY = projects.reduce(
    (acc, p) => acc + p.verifiedSavingsCostCNY,
    0,
  );
  const totalTargetKWh = projects.reduce(
    (acc, p) => acc + p.targetSavingsKWh,
    0,
  );
  const overallRealizationRate =
    totalTargetKWh > 0 ? (totalVerifiedSavingsKWh / totalTargetKWh) * 100 : 100;
  const averageCvRmse =
    projects.length > 0
      ? projects.reduce((acc, p) => acc + p.cvRmse, 0) / projects.length
      : 0;
  const certifiedCount = projects.filter(
    (p) => p.status === "CERTIFIED",
  ).length;
  const verifyingCount = projects.filter(
    (p) => p.status === "VERIFYING",
  ).length;

  return {
    totalVerifiedSavingsKWh,
    totalVerifiedCostCNY,
    overallRealizationRate: Number(overallRealizationRate.toFixed(1)),
    averageCvRmse: Number(averageCvRmse.toFixed(1)),
    certifiedCount,
    verifyingCount,
  };
}
