export interface RunningExpectationInput {
  device?: string;
  point?: string;
  equals?: string;
}

export interface CompiledDeviceRef {
  deviceId: string;
  key: string;
  equals?: string;
}

export interface OnboardingDeviceLike {
  id: string;
  code: string;
  sampleSeconds?: number;
  running?: unknown;
  points: OnboardingPointLike[];
}

export interface OnboardingPointLike {
  code: string;
  pointType?: string;
  status?: string;
  critical?: boolean;
  from?: unknown;
  qualityProfile?: string;
  running?: unknown;
  flatlineStabilizeSeconds?: unknown;
  device?: { id: string; code: string; running?: unknown };
}

export interface OnboardingQualityProfileLike {
  staleAfterSeconds?: number;
  range?: { min: number; max: number };
  maxStep?: number;
  maxStepWindowSeconds?: number;
  sentinels?: number[];
  flatlineSeconds?: number;
  flatlineRunningSeconds?: number;
  flatlineStabilizeSeconds?: unknown;
  running?: RunningExpectationInput[];
  inputs?: RunningExpectationInput[];
}

export interface OnboardingProfileLike {
  qualityProfiles?: Record<string, OnboardingQualityProfileLike | undefined>;
  nameplateCatalog?: Record<string, { feederAuxiliaryPowerKw?: unknown; unconfirmed?: unknown[] } | undefined>;
  assets?: Array<{
    code?: string;
    nameplate?: unknown;
  }>;
  physicsChecks?: Array<{
    code?: string;
    optional?: boolean;
    stabilizeSeconds?: unknown;
    running?: unknown;
    subject?: { device?: string; point?: string };
  }>;
}

export interface OnboardingPlateLike {
  ratedPowerKw?: unknown;
  feederAuxiliaryPowerKw?: unknown;
  unconfirmed?: unknown[];
}

export interface AlarmRulesQualityLike {
  dataQuality?: {
    suspect?: { durationSeconds?: unknown } | null;
  } | null;
  deviceUntrusted?: unknown;
}

const VALID_CONDITION_REASONS = new Set([
  'OUT_OF_RANGE',
  'SPIKE',
  'SENTINEL',
  'FLATLINE',
  'DERIVED_INPUT',
  'LOW_CONFIDENCE',
  'SOURCE_UNTRUSTED',
  'PHYSICS_WET_BULB_DRY',
  'PHYSICS_WET_BULB_CONDENSER',
  'PHYSICS_POWER',
  'PHYSICS_COP',
  'PHYSICS_CHW_DIRECTION',
  'PHYSICS_CW_DIRECTION',
  'PHYSICS_HEAT_BALANCE',
  'PROBE_OFFSET',
]);

export function enrichPowerPointMetadata(
  compiledMetadata: Record<string, unknown>,
  point: { code: string; asset?: string; device?: { asset?: string } },
  platesByAssetId: ReadonlyMap<string, OnboardingPlateLike>,
): Record<string, unknown> {
  if (point.code !== 'power_total') return compiledMetadata;
  const assetId = point.asset ?? point.device?.asset;
  if (!assetId) return compiledMetadata;
  const plate = platesByAssetId.get(assetId);
  if (!plate) return compiledMetadata;
  const unconfirmed = new Set(Array.isArray(plate.unconfirmed) ? plate.unconfirmed : []);
  const next = { ...compiledMetadata };
  if (
    next.ratedPowerKw == null
    && !unconfirmed.has('ratedPowerKw')
    && typeof plate.ratedPowerKw === 'number'
    && Number.isFinite(plate.ratedPowerKw)
    && plate.ratedPowerKw > 0
  ) {
    next.ratedPowerKw = plate.ratedPowerKw;
  }
  if (
    next.feederAuxiliaryPowerKw == null
    && !unconfirmed.has('feederAuxiliaryPowerKw')
    && typeof plate.feederAuxiliaryPowerKw === 'number'
    && Number.isFinite(plate.feederAuxiliaryPowerKw)
    && plate.feederAuxiliaryPowerKw > 0
  ) {
    next.feederAuxiliaryPowerKw = plate.feederAuxiliaryPowerKw;
  }
  return next;
}

function toDeviceRef(
  item: RunningExpectationInput,
  devicesByCode: ReadonlyMap<string, { id: string }>,
  where: string,
): CompiledDeviceRef {
  const device = item.device ? devicesByCode.get(item.device) : undefined;
  if (!device || !item.point) {
    throw new Error(`${where} names unknown device ${item.device}`);
  }
  return item.equals != null
    ? { deviceId: device.id, key: item.point, equals: item.equals }
    : { deviceId: device.id, key: item.point };
}

export function applyPointRunningAndStabilize(
  checks: Record<string, unknown>,
  point: OnboardingPointLike,
  named: OnboardingQualityProfileLike | null | undefined,
  devicesByCode: ReadonlyMap<string, { id: string }>,
): void {
  const flatlineStabilizeSeconds = point.flatlineStabilizeSeconds ?? named?.flatlineStabilizeSeconds;
  if (flatlineStabilizeSeconds != null) {
    checks.flatlineStabilizeSeconds = flatlineStabilizeSeconds;
  }
  const pointRunning = Array.isArray(point.running) && point.running.length > 0 ? point.running : null;
  const deviceRunning = Array.isArray(point.device?.running) && point.device.running.length > 0 ? point.device.running : null;
  const profileRunning = Array.isArray(named?.running) && named.running.length > 0 ? named.running : null;
  const running = pointRunning ?? deviceRunning ?? profileRunning;
  if (!running) {
    return;
  }
  const where = pointRunning
    ? `point ${point.device?.code ?? '?'}/${point.code}`
    : deviceRunning
      ? `device ${point.device?.code ?? '?'}`
      : `quality profile ${point.qualityProfile}`;
  checks.running = running.map((item) => toDeviceRef(item as RunningExpectationInput, devicesByCode, where));
}

export function assertQualityChecksSize(point: OnboardingPointLike, encoded: string | null): void {
  if (encoded && Buffer.byteLength(encoded) > 4096) {
    throw new Error(`point ${point.device?.code ?? '?'}/${point.code} quality checks exceed 4096 bytes`);
  }
}

export function validateRunningList(
  running: unknown,
  where: string,
  activePoints: ReadonlyMap<string, ReadonlySet<string>>,
  problems: string[],
): boolean {
  if (running == null) return true;
  if (!Array.isArray(running)) {
    problems.push(`${where}: running must be an array`);
    return false;
  }
  let ok = true;
  for (const raw of running) {
    const item = raw && typeof raw === 'object' && !Array.isArray(raw) ? (raw as Record<string, unknown>) : null;
    const validEntry = item !== null
      && typeof item.device === 'string' && item.device !== ''
      && typeof item.point === 'string' && item.point !== ''
      && typeof item.equals === 'string' && item.equals !== '';
    if (!validEntry) {
      problems.push(`${where}: running entry must name device, point and non-empty equals`);
      ok = false;
      continue;
    }
    const dev = item.device as string;
    const pt = item.point as string;
    const points = activePoints.get(dev);
    if (!points) {
      problems.push(`${where}: running names unknown device ${dev}`);
      ok = false;
    } else if (!points.has(pt)) {
      problems.push(`${where}: running ${dev} has no active point ${pt}`);
    }
  }
  return ok;
}

export function validateQualityProfileExtensions(
  name: string,
  quality: OnboardingQualityProfileLike,
  problems: string[],
): void {
  if (quality.flatlineStabilizeSeconds != null && (!Number.isInteger(quality.flatlineStabilizeSeconds) || (quality.flatlineStabilizeSeconds as number) < 0)) {
    problems.push(`quality profile ${name}: flatlineStabilizeSeconds must be a non-negative integer`);
  }
}

export function validateSiteQualityAdditions(
  profile: OnboardingProfileLike,
  devices: readonly OnboardingDeviceLike[],
  activePoints: ReadonlyMap<string, ReadonlySet<string>>,
  devicesByCode: ReadonlyMap<string, OnboardingDeviceLike>,
  pointQualityFn: (point: OnboardingPointLike, profile: OnboardingProfileLike, devicesByCode: ReadonlyMap<string, OnboardingDeviceLike>) => unknown,
  problems: string[],
): void {
  for (const device of devices) {
    const deviceRunningOk = validateRunningList(device.running, device.code, activePoints, problems);
    for (const point of device.points) {
      if (point.flatlineStabilizeSeconds != null && (!Number.isInteger(point.flatlineStabilizeSeconds) || (point.flatlineStabilizeSeconds as number) < 0)) {
        problems.push(`${device.code}/${point.code}: flatlineStabilizeSeconds must be a non-negative integer`);
      }
      const pointRunningOk = validateRunningList(point.running, `${device.code}/${point.code}`, activePoints, problems);
      if (point.pointType !== 'COMMAND' && deviceRunningOk && pointRunningOk && (!point.qualityProfile || profile.qualityProfiles?.[point.qualityProfile])) {
        try {
          pointQualityFn({ ...point, device }, profile, devicesByCode);
        } catch (error) {
          problems.push(error instanceof Error ? error.message : String(error));
        }
      }
    }
  }
  for (const [name, plate] of Object.entries(profile.nameplateCatalog ?? {})) {
    const kw = plate?.feederAuxiliaryPowerKw;
    if (kw != null && (typeof kw !== 'number' || !Number.isFinite(kw) || kw < 0)) {
      problems.push(`nameplate catalog ${name}: feederAuxiliaryPowerKw must be a non-negative number`);
    }
  }
  for (const asset of profile.assets ?? []) {
    if (asset.nameplate != null && typeof asset.nameplate === 'object' && !Array.isArray(asset.nameplate)) {
      const kw = (asset.nameplate as Record<string, unknown>).feederAuxiliaryPowerKw;
      if (kw != null && (typeof kw !== 'number' || !Number.isFinite(kw) || kw < 0)) {
        problems.push(`asset ${asset.code}: feederAuxiliaryPowerKw must be a non-negative number`);
      }
    }
  }
  for (const check of profile.physicsChecks ?? []) {
    if (check.stabilizeSeconds != null && (!Number.isInteger(check.stabilizeSeconds) || (check.stabilizeSeconds as number) < 0)) {
      problems.push(`physics check ${check.code ?? '?'}: stabilizeSeconds must be a non-negative integer`);
    } else if (!check.optional && typeof check.stabilizeSeconds === 'number' && check.stabilizeSeconds > 0) {
      const subjectDevice = check.subject?.device ? devicesByCode.get(check.subject.device) : undefined;
      const subjectPoint = subjectDevice?.points.find((p) => p.code === check.subject?.point);
      const subjectProfile = subjectPoint?.qualityProfile ? profile.qualityProfiles?.[subjectPoint.qualityProfile] : undefined;
      const hasRunning = (Array.isArray(check.running) && check.running.length > 0)
        || (Array.isArray(subjectPoint?.running) && subjectPoint.running.length > 0)
        || (Array.isArray(subjectDevice?.running) && subjectDevice.running.length > 0)
        || (Array.isArray(subjectProfile?.running) && subjectProfile.running.length > 0);
      if (!hasRunning) {
        problems.push(`physics check ${check.code ?? '?'}: stabilizeSeconds requires running criteria on the check, subject point/device, or quality profile`);
      }
    }
  }
}

export function validateAlarmQualityAdditions(
  alarmRules: AlarmRulesQualityLike,
  alarmSeverities: ReadonlySet<string>,
  problems: string[],
): void {
  if (alarmRules.dataQuality?.suspect != null) {
    const durationSeconds = alarmRules.dataQuality.suspect.durationSeconds;
    if (durationSeconds != null && (!Number.isInteger(durationSeconds) || (durationSeconds as number) < 0)) {
      problems.push('alarm rules dataQuality.suspect.durationSeconds must be a non-negative integer');
    }
  }
  if (alarmRules.deviceUntrusted != null) {
    if (!alarmRules.deviceUntrusted || typeof alarmRules.deviceUntrusted !== 'object' || Array.isArray(alarmRules.deviceUntrusted)) {
      problems.push('alarm rules deviceUntrusted must be an object');
      return;
    }
    const du = alarmRules.deviceUntrusted as Record<string, unknown>;
    if (du.enabled != null && typeof du.enabled !== 'boolean') {
      problems.push('alarm rules deviceUntrusted.enabled must be true or false');
    }
    if (du.enabled !== false) {
      if (typeof du.title !== 'string' || !du.title.trim()) problems.push('alarm rules deviceUntrusted.title is required');
      if (typeof du.summary !== 'string' || !du.summary.trim()) problems.push('alarm rules deviceUntrusted.summary is required');
      if (typeof du.severity !== 'string' || !alarmSeverities.has(du.severity)) {
        problems.push(`alarm rules deviceUntrusted.severity ${String(du.severity)} must be INFO, WARNING, MINOR, MAJOR or CRITICAL`);
      }
      if (du.durationSeconds != null && (!Number.isInteger(du.durationSeconds) || (du.durationSeconds as number) < 0)) {
        problems.push('alarm rules deviceUntrusted.durationSeconds must be a non-negative integer');
      }
      if (du.minPoints != null && (!Number.isInteger(du.minPoints) || (du.minPoints as number) < 2)) {
        problems.push('alarm rules deviceUntrusted.minPoints must be an integer >= 2');
      }
      if (du.reasons != null) {
        if (
          !Array.isArray(du.reasons)
          || du.reasons.length === 0
          || du.reasons.length > 8
          || du.reasons.some((r) => typeof r !== 'string' || !VALID_CONDITION_REASONS.has(r))
        ) {
          problems.push('alarm rules deviceUntrusted.reasons must be 1 to 8 valid quality reason codes');
        }
      }
    }
  }
}
