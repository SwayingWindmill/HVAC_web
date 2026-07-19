// 成本与绩效页（#15）的本地配置基线。
// 电价 / 碳排因子 / 投资额等参数默认写死，切真实后端后由 src/api 提供。

/** 默认电价 ¥/kWh（商业峰平谷均价占位） */
export const PRICE = 0.85;

/** 电网平均排放因子 kgCO2/kWh（全国电网均值占位） */
export const CARBON_FACTOR = 0.581;

/** 节能改造总投资 ¥（用于 ROI / 回收期计算） */
export const INVESTMENT = 650_000;

/** 年度节能收益目标 ¥（用于 KPI 达标率 vs 目标） */
export const ANNUAL_TARGET_CNY = 160_000;

/** 年度节能收益基线（按月 kWh），叠加 /optimize 实时已批准建议 */
export const ANNUAL_SAVING_KWH = [
  9800, 10200, 11200, 10800, 12100, 11900, 13400, 12900, 14200, 13800, 15100, 14800,
];

export const MONTH_LABELS = [
  '1月', '2月', '3月', '4月', '5月', '6月',
  '7月', '8月', '9月', '10月', '11月', '12月',
];

/** 年度基线 kWh 合计 */
export const ANNUAL_KWH = ANNUAL_SAVING_KWH.reduce((a, b) => a + b, 0);

/** 年度基线收益 ¥（电价折算） */
export const ANNUAL_CNY = Math.round(ANNUAL_KWH * PRICE);
