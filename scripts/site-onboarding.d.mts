export interface OnboardingAsset {
  id: string;
  code: string;
  nameplate?: string;
}

export interface OnboardingPoint {
  id?: string;
  code: string;
  status?: string;
  critical?: boolean | string;
  from?: string[];
  device?: OnboardingDevice;
}

export interface OnboardingDevice {
  id: string;
  code: string;
  sourceKey?: string;
  asset?: string;
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

export interface OnboardingProfile {
  site: { id: string; code: string };
  assets: OnboardingAsset[];
  devices: OnboardingDevice[];
  meterPoints?: OnboardingPoint[];
  energy?: {
    totalMeter?: string | { kind: string };
    checkMeters?: string[];
  };
  physicsChecks?: Array<Record<string, unknown>>;
  nameplateCatalog?: Record<string, unknown>;
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
  rules: Array<{
    raise?: unknown;
    [key: string]: unknown;
  }>;
  [key: string]: unknown;
}

export interface OnboardingState {
  site: string;
  devices: Record<string, string>;
  points: Record<string, string>;
  assets: Record<string, string>;
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
export function pointStatus(point: Pick<OnboardingPoint, 'code' | 'status'>): 'ACTIVE' | 'INACTIVE' | 'RETIRED';
export function expandPoints(device: OnboardingDevice | undefined, meterPoints?: OnboardingPoint[]): OnboardingPoint[];
export function presenceApplicability(points: readonly OnboardingPoint[]): 'APPLICABLE' | 'NOT_APPLICABLE';
export function pointQuality(
  point: OnboardingPoint,
  profile: OnboardingProfile,
  devicesByCode: Map<string, OnboardingDevice>,
): { checks: string | null };
export function updateWhenChanged(
  table: string,
  assignments: ReadonlyArray<readonly [string, string]>,
  options?: { revision?: string | false },
): string;
export function buildOnboardingSql(profile: OnboardingProfile): {
  registry: string;
  telemetry: string;
  energy: string;
  intelligence: string;
};
export function checkSiteFile(profile: OnboardingProfile, alarmRules: OnboardingAlarmRules | null): string[];
export function planOnboarding(profile: OnboardingProfile, state: OnboardingState): PlanItem[];
export function siteStateQuery(profile: OnboardingProfile): string;
