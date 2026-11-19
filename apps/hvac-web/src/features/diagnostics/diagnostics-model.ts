import type { SiteAssetModel } from '../../api/generated/platformGateway.gen';

// Fault detection and diagnosis: rules the FDD owner evaluates on authoritative telemetry
// history, and the findings it records when a rule's threshold is crossed.

export interface Finding {
  readonly id: string;
  readonly assetId: string;
  readonly findingType: string;
  readonly evaluationFrom: string;
  readonly evaluationTo: string;
  readonly observedValue: number;
  readonly thresholdValue: number;
  readonly valueUnit: string;
  readonly confidence: number;
  readonly createdAt: string;
}

/** What a rule is to an operator; its threshold and window are FDD's, read as a RuleRevision. */
export interface DiagnosticRule {
  readonly findingType: string;
  readonly title: string;
  /** What the rule watches, in the operator's words. */
  readonly watches: string;
  readonly designValue: number;
  readonly unit: string;
  readonly meaning: string;
  readonly causes: readonly string[];
  readonly actions: readonly string[];
}

// The chilled-water loop is designed for a 5 K rise across the coils. Well below it the pumps
// move more water than the load needs, which costs pump energy and starves the chiller of ΔT.
export const LOW_DELTA_T: DiagnosticRule = {
  findingType: 'CHILLED_WATER_LOW_DELTA_T',
  title: '冷冻水低温差',
  watches: '冷量表的冷冻水供回水温差',
  designValue: 5,
  unit: 'K',
  meaning: '供回水温差低于设定阈值，说明冷冻水流量多于负荷所需，水泵电耗偏高，冷机也难以在高效区运行。',
  causes: ['冷冻水泵定频或频率偏高', '旁通阀常开或开度过大', '末端水阀失控、全开'],
  actions: ['降低冷冻水泵频率，使温差回到 5 K 附近', '检查旁通阀与末端水阀的开度', '确认冷量表供回水温度传感器读数'],
};

export const METER_IMBALANCE_RULE: DiagnosticRule = {
  findingType: 'METER_IMBALANCE',
  title: '主计量与设备计量差额偏大',
  watches: '冷站主计量与各设备计量之和的偏差率',
  designValue: 5,
  unit: '%',
  meaning: '冷站主计量与各设备计量合计之间的差额超出允许偏差，可能存在未单独计量的支路用电、电表互感器倍率配置偏差或设备电表通信中断。',
  causes: [
    '冷站内有风机、辅助水泵或控制柜等未接设备电表的用电回路',
    '冷站主计量或设备电表的电流电压互感器倍率设置与现场不一致',
    '部分设备电表掉线、读数冻结或回传延迟导致统计区间缺少读数',
  ],
  actions: [
    '前往能耗分析页面核对冷站主计量与各设备计量合计的差额走势',
    '核实冷站主计量和各设备电表的互感器倍率及在线状态',
    '排查是否新增了未接电表的用电设备或辅助支路',
  ],
};

export const RULES: readonly DiagnosticRule[] = [LOW_DELTA_T, METER_IMBALANCE_RULE];

/**
 * When the plant counts as operating for the rule (spec docs/specs/fdd-rule-definition.md): FDD
 * judges a window only when operating minutes reach minimumOperatingShare of it, and finds it low
 * when low minutes reach minimumLowShare of those. A gate FDD cannot read at the Site is skipped.
 */
export interface OperatingGates {
  readonly minimumOperatingShare: number;
  readonly minimumLowShare: number;
  readonly temperatureHoldMinutes: number;
  readonly gateHoldMinutes: number;
  readonly chillerRunning?: { readonly minimumRatedPowerFraction: number };
  readonly loadRatio?: { readonly minimum: number };
  readonly chilledWaterPumpRunning?: Record<string, never>;
}

/** The revision of a rule FDD evaluates now: below minimumDeltaTC over the last windowMinutes while operating. */
export interface RuleRevision {
  readonly revisionId: string;
  readonly revision: number;
  readonly minimumDeltaTC: number;
  readonly windowMinutes: number;
  readonly operatingGates: OperatingGates;
  readonly effectiveFrom: string;
}

/** The operating condition in the operator's words, e.g. "冷机开机且负荷率不低于 15%". */
export function operatingCondition(gates: OperatingGates): string {
  const parts = [
    gates.chillerRunning ? '冷机开机' : null,
    gates.loadRatio ? `负荷率不低于 ${Math.round(gates.loadRatio.minimum * 100)}%` : null,
    gates.chilledWaterPumpRunning ? '冷冻水泵运行' : null,
  ].filter((part) => part !== null);
  return parts.length > 0 ? parts.join('、') : '有供回水温度';
}

export const ruleFor = (findingType: string): DiagnosticRule | undefined => RULES.find((rule) => rule.findingType === findingType);

/** The Device a rule reads and the Asset it diagnoses: the loop's BTU meter and what it meters. */
export function lowDeltaTTarget(model: Pick<SiteAssetModel, 'devices' | 'relationships' | 'assets'>): { deviceId: string; assetId: string; assetName: string } | null {
  for (const device of model.devices) {
    if (device.deviceType !== 'BTU_METER' || device.status !== 'ACTIVE') continue;
    const metered = model.relationships.find((relationship) =>
      relationship.status === 'ACTIVE' && relationship.role === 'METER'
      && relationship.fromType === 'DEVICE' && relationship.fromId === device.id && relationship.toType === 'ASSET');
    const asset = metered ? model.assets.find((candidate) => candidate.id === metered.toId) : undefined;
    if (asset) return { deviceId: device.id, assetId: asset.id, assetName: asset.displayName };
  }
  return null;
}

/** The active Device that meters an Asset, e.g. the BTU meter whose readings a finding was drawn from. */
export function meterOf(model: Pick<SiteAssetModel, 'devices' | 'relationships'>, assetId: string): string | undefined {
  return model.relationships.find((relationship) =>
    relationship.status === 'ACTIVE' && relationship.role === 'METER'
    && relationship.fromType === 'DEVICE' && relationship.toType === 'ASSET' && relationship.toId === assetId
    && model.devices.some((device) => device.id === relationship.fromId && device.status === 'ACTIVE'))?.fromId;
}

/** How far a finding fell past its threshold, as a share of the threshold. */
export function severity(finding: Finding): 'high' | 'medium' | 'low' {
  const shortfall = Math.abs(finding.thresholdValue - finding.observedValue) / finding.thresholdValue;
  if (shortfall >= 0.3) return 'high';
  if (shortfall >= 0.1) return 'medium';
  return 'low';
}
