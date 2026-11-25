export interface OnboardingRunningExpect {
  device: string;
  point: string;
  equals?: string;
}

export interface OnboardingNameplate {
  manufacturer?: string;
  model?: string;
  year?: number;
  refrigerant?: string;
  ratedCapacityKw?: number;
  ratedPowerKw?: number;
  ratedCurrentA?: number;
  feederAuxiliaryPowerKw?: number;
  unconfirmed?: string[];
  [key: string]: unknown;
}

export interface OnboardingQualityProfile {
  accuracyK?: number;
  accuracyFraction?: number;
  range?: { min: number; max: number };
  maxStep?: number;
  maxStepWindowSeconds?: number;
  sentinels?: number[];
  staleAfterSeconds?: number;
  flatlineSeconds?: number;
  flatlineRunningSeconds?: number;
  flatlineStabilizeSeconds?: number;
  running?: OnboardingRunningExpect[];
  inputs?: Array<{ device: string; point: string }>;
}

export interface OnboardingAsset {
  id: string;
  code: string;
  nameplate?: string | OnboardingNameplate;
}

export interface OnboardingPoint {
  id?: string;
  code: string;
  pointType?: string;
  valueType?: string;
  unit?: string;
  status?: string;
  writable?: boolean;
  asset?: string;
  semanticRole?: string;
  address?: string;
  protocol?: string;
  sourceMetadata?: Record<string, unknown>;
  critical?: boolean | string;
  from?: string[];
  qualityProfile?: string;
  running?: OnboardingRunningExpect[];
  flatlineStabilizeSeconds?: number;
  device?: OnboardingDevice;
}

export interface OnboardingDevice {
  id: string;
  code: string;
  sourceKey?: string;
  asset?: string;
  type?: string;
  sampleSeconds?: number;
  running?: OnboardingRunningExpect[];
  points: OnboardingPoint[];
  morePoints?: OnboardingPoint[];
}

export interface OnboardingLowDeltaT {
  cron: string;
  enabled: boolean;
  asset: string;
  device: string;
  note: string;
}

export interface OnboardingOptimizationPolicy {
  code: string;
  objective: string;
  dispatchMode: string;
  horizon: string;
  horizonMinutes: number;
  granularity: string;
  constraints: {
    inputMapping: {
      supplyTemperatureKey: string;
      zoneTemperatureKey: string;
    };
    [key: string]: unknown;
  };
  responseModelSource?: string;
  note?: string;
}

export interface OnboardingOptimization {
  cron: string;
  enabled: boolean;
  model: string;
  policy: OnboardingOptimizationPolicy;
}

export interface OnboardingAssetRelationship {
  from: string;
  to: string;
  role: string;
}

export interface OnboardingSiteBuildingRoom {
  id: string;
  name: string;
  kind: 'hall' | 'private' | 'smallPrivate' | 'kitchen' | 'plantRoom' | 'service' | 'corridor' | 'core' | 'shaft';
  x: [number, number];
  z: [number, number];
}

export interface OnboardingSiteBuildingFloor {
  level: number;
  label: string;
  height: number;
  rooms: OnboardingSiteBuildingRoom[];
}

export interface OnboardingSiteBuildingPlan {
  name: string;
  width: number;
  depth: number;
  floors: OnboardingSiteBuildingFloor[];
}

export interface OnboardingProfile {
  site: { id: string; code: string; buildingPlan?: OnboardingSiteBuildingPlan | null };
  assets: OnboardingAsset[];
  assetRelationships?: OnboardingAssetRelationship[];
  devices: OnboardingDevice[];
  meterPoints?: OnboardingPoint[];
  energy?: {
    totalMeter?: string | { kind: string };
    checkMeters?: string[];
  };
  physicsChecks?: Array<Record<string, unknown>>;
  qualityProfiles?: Record<string, OnboardingQualityProfile>;
  nameplateCatalog?: Record<string, OnboardingNameplate>;
  intelligence: {
    diagnosis: { lowDeltaT: OnboardingLowDeltaT };
    optimization?: OnboardingOptimization;
  };
}

export interface OnboardingAlarmRules {
  readingGaps?: {
    title: string;
    summary: string;
    severity: string;
    ageSeconds: number;
  };
  dataQuality?: {
    suspect?: Record<string, unknown>;
    flatline?: Record<string, unknown>;
  };
  deviceUntrusted?: {
    enabled?: boolean;
    title?: string;
    summary?: string;
    severity?: string;
    durationSeconds?: number;
    minPoints?: number;
    reasons?: string[];
  };
  rules: Array<{
    raise?: unknown;
    [key: string]: unknown;
  }>;
  [key: string]: unknown;
}

export interface OnboardingState {
  site: string;
  siteRevision?: number | null;
  siteBuildingPlan?: OnboardingSiteBuildingPlan | null;
  devices: Record<string, string>;
  points: Record<string, string>;
  assets: Record<string, string>;
  pointSemanticRoles?: Record<string, string>;
  assetRelationships?: Array<{ fromAssetId: string; toAssetId: string; role: string }>;
  schedules: Record<string, { jobType?: string; cron: string; enabled: boolean; payload: Record<string, unknown> }>;
  topology: boolean;
  synthesizedTotal: boolean;
  tariff: boolean;
  forecast: boolean;
  policy?: Record<string, unknown> & {
    constraints: {
      inputMapping: {
        supplyTemperatureKey: string;
        zoneTemperatureKey: string;
      };
      [key: string]: unknown;
    };
  } | null;
  modelRevision?: string | null;
  optimizationBinding?: { status: string; enabled: boolean } | null;
  lowDeltaTRule?: { revision: number; minimumDeltaTC: number; windowMinutes: number } | null;
}

export interface PlanItem {
  status: string;
  item: string;
  detail: string;
}

export const SUBJECT_BINDING_LIFECYCLE: ReadonlyArray<readonly [string, string]>;
export const SEMANTIC_ROLES: readonly string[];
export const ASSET_RELATIONSHIP_ROLES: readonly string[];
export function pointStatus(point: Pick<OnboardingPoint, 'code' | 'status'>): 'ACTIVE' | 'INACTIVE' | 'RETIRED';
export function expandPoints(device: OnboardingDevice | undefined, meterPoints?: OnboardingPoint[]): OnboardingPoint[];
export function presenceApplicability(points: readonly OnboardingPoint[]): 'APPLICABLE' | 'NOT_APPLICABLE';
export function pointQuality(
  point: OnboardingPoint,
  profile: OnboardingProfile,
  devicesByCode: Map<string, OnboardingDevice>,
): { staleAfterSeconds: number | null; range: { min: number; max: number } | null; checks: string | null };
export function updateWhenChanged(
  table: string,
  assignments: ReadonlyArray<readonly [string, string]>,
  options?: { revision?: string | false },
): string;
export function buildOnboardingSql(
  profile: OnboardingProfile,
  options?: { expectedSiteRevision?: number | null; principalId?: string },
): {
  registry: string;
  telemetry: string;
  energy: string;
  intelligence: string;
};
export function checkSiteFile(profile: OnboardingProfile, alarmRules: OnboardingAlarmRules | null): string[];
export function planOnboarding(profile: OnboardingProfile, state: OnboardingState): PlanItem[];
export function siteStateQuery(profile: OnboardingProfile): string;
