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

export const RULES: readonly DiagnosticRule[] = [LOW_DELTA_T];

/** The revision of a rule FDD evaluates now: below minimumDeltaTC over the last windowMinutes. */
export interface RuleRevision {
  readonly revisionId: string;
  readonly revision: number;
  readonly minimumDeltaTC: number;
  readonly windowMinutes: number;
  readonly effectiveFrom: string;
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

/** How far a finding fell past its threshold, as a share of the threshold. */
export function severity(finding: Finding): 'high' | 'medium' | 'low' {
  const shortfall = Math.abs(finding.thresholdValue - finding.observedValue) / finding.thresholdValue;
  if (shortfall >= 0.3) return 'high';
  if (shortfall >= 0.1) return 'medium';
  return 'low';
}
