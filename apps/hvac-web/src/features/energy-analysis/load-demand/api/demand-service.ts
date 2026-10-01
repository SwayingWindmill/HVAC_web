import type {
  DemandAnalyticsData,
  DemandFilterParams,
  DemandSheddingCandidate,
  LoadDurationPoint,
  RollingDemandPoint,
} from './demand-types';

const MOCK_SHEDDING_CANDIDATES: readonly DemandSheddingCandidate[] = [
  {
    id: 'ch-01',
    name: '1# 离心冷水机组 (CH-01)',
    category: '冷水主机',
    systemPath: '冷站系统 > 冷水机组群',
    currentPowerKW: 342.5,
    sheddingPotentialKW: 60.0,
    responseTimeMinutes: 5,
    constraintDescription: '允许冷冻水供水温度上调 1.0°C，持续时间不超过 60 分钟',
    flexibilityStatus: 'READY',
    availableDurationMinutes: 60,
    comfortImpactScore: 'LOW',
  },
  {
    id: 'ch-02',
    name: '2# 螺杆冷水机组 (CH-02)',
    category: '冷水主机',
    systemPath: '冷站系统 > 冷水机组群',
    currentPowerKW: 328.0,
    sheddingPotentialKW: 55.0,
    responseTimeMinutes: 5,
    constraintDescription: '允许出水设定上调 1.0°C，与 1# 主机交替分段削峰',
    flexibilityStatus: 'READY',
    availableDurationMinutes: 60,
    comfortImpactScore: 'LOW',
  },
  {
    id: 'ahu-b1',
    name: '地下一层商业区 AHU 群组 (AHU-B1~04)',
    category: '空调末端',
    systemPath: '末端风系统 > B1商业区',
    currentPowerKW: 45.0,
    sheddingPotentialKW: 20.0,
    responseTimeMinutes: 2,
    constraintDescription: '变频风机转速下调 15%，维持送风温度稳定',
    flexibilityStatus: 'READY',
    availableDurationMinutes: 45,
    comfortImpactScore: 'LOW',
  },
  {
    id: 'ahu-1f',
    name: '首层大堂高大空间 AHU-01/02',
    category: '空调末端',
    systemPath: '末端风系统 > 首层大堂',
    currentPowerKW: 38.0,
    sheddingPotentialKW: 15.0,
    responseTimeMinutes: 2,
    constraintDescription: '大堂高大空间利用建筑热惰性阶段性释能，暂停制冷 30 分钟',
    flexibilityStatus: 'STANDBY',
    availableDurationMinutes: 30,
    comfortImpactScore: 'MEDIUM',
  },
  {
    id: 'chwp-sys',
    name: '二次冷冻水泵变频群组 (SCHWP-01~03)',
    category: '水力输配',
    systemPath: '水力系统 > 二次泵组',
    currentPowerKW: 54.0,
    sheddingPotentialKW: 12.0,
    responseTimeMinutes: 3,
    constraintDescription: '最不利末端压差下限保护 (不得低于 0.12 MPa)',
    flexibilityStatus: 'READY',
    availableDurationMinutes: 90,
    comfortImpactScore: 'LOW',
  },
  {
    id: 'cwp-sys',
    name: '冷却水泵定频变频混编群',
    category: '散热循环',
    systemPath: '散热循环 > 冷却泵组',
    currentPowerKW: 67.8,
    sheddingPotentialKW: 10.0,
    responseTimeMinutes: 3,
    constraintDescription: '受冷却塔逼近度与冷凝压力下限硬约束保护，当前处于约束锁定',
    flexibilityStatus: 'LOCKED',
    availableDurationMinutes: 0,
    comfortImpactScore: 'HIGH',
  },
];

function generateRollingDemand(): readonly RollingDemandPoint[] {
  const points: RollingDemandPoint[] = [];
  const contractLimit = 2800;
  const warningLimit = 2520; // 90%

  for (let i = 0; i < 96; i++) {
    const totalMinutes = i * 15;
    const h = Math.floor(totalMinutes / 60);
    const m = totalMinutes % 60;
    const timeStr = `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`;

    // Diurnal curve peaking around 11:30 - 15:30
    const peakCenter = 13.5;
    const dist = Math.abs(h + m / 60 - peakCenter);
    const baseLoad = 850 + Math.sin(i / 10) * 40;
    const peakAdd = 1550 * Math.exp(-Math.pow(dist / 4.2, 2));
    const noise = Math.sin(i * 1.5) * 35;
    const actual = Math.round(baseLoad + peakAdd + noise);

    const rolling15 = Math.round(actual * 0.98 + (i % 3) * 15);
    const outdoorTemp = Math.round((26 + Math.sin((h - 6) / 4) * 6.5) * 10) / 10;

    let status: 'NORMAL' | 'WARNING' | 'CRITICAL' = 'NORMAL';
    if (rolling15 >= contractLimit) {
      status = 'CRITICAL';
    } else if (rolling15 >= warningLimit) {
      status = 'WARNING';
    }

    points.push({
      time: timeStr,
      actualDemandKW: actual,
      rolling15MinKW: rolling15,
      contractLimitKW: contractLimit,
      warningThresholdKW: warningLimit,
      outdoorTemp,
      status,
    });
  }

  return points;
}

function generateLoadDurationCurve(): readonly LoadDurationPoint[] {
  const points: LoadDurationPoint[] = [];
  const totalHours = 720; // monthly hours

  for (let p = 0; p <= 100; p += 5) {
    const hours = Math.round((p / 100) * totalHours);
    // Sigmoid curve from 2480 kW down to 680 kW
    const loadKW = Math.round(
      680 + (2480 - 680) * Math.pow(1 - p / 100, 1.8)
    );

    let loadType: 'BASE' | 'INTERMEDIATE' | 'PEAK' = 'BASE';
    if (p < 15) {
      loadType = 'PEAK';
    } else if (p < 60) {
      loadType = 'INTERMEDIATE';
    }

    points.push({
      percentile: p,
      hours,
      loadKW,
      loadType,
    });
  }

  return points;
}

const MOCK_ROLLING_DEMAND = generateRollingDemand();
const MOCK_LDC = generateLoadDurationCurve();

export const demandService = {
  getDemandAnalytics: async (
    params?: DemandFilterParams
  ): Promise<DemandAnalyticsData> => {
    await new Promise((resolve) => setTimeout(resolve, 80));

    let filteredCandidates = [...MOCK_SHEDDING_CANDIDATES];
    if (params?.search) {
      const q = params.search.toLowerCase();
      filteredCandidates = filteredCandidates.filter(
        (c) =>
          c.name.toLowerCase().includes(q) ||
          c.category.toLowerCase().includes(q) ||
          c.systemPath.toLowerCase().includes(q)
      );
    }

    const totalAvailableShedding = filteredCandidates
      .filter((c) => c.flexibilityStatus === 'READY')
      .reduce((acc, c) => acc + c.sheddingPotentialKW, 0);

    return {
      summary: {
        currentDemandKW: 2185,
        contractDemandKW: 2800,
        declaredCapacityKVA: 3600,
        peakDemandThisMonthKW: 2480,
        peakDemandTime: '09-18 14:15',
        demandSafetyMarginKW: 320,
        demandSafetyMarginPercent: 11.4,
        totalSheddingAvailableKW: totalAvailableShedding,
        transformerLoadRatePercent: 68.5,
        penaltyRiskLevel: 'NONE',
      },
      rollingDemand: MOCK_ROLLING_DEMAND,
      loadDuration: MOCK_LDC,
      sheddingCandidates: filteredCandidates,
      periodLabel: '2026年9月 (本计费周期)',
    };
  },
};
