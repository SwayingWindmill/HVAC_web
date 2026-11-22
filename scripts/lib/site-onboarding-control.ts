const CAPABILITY_REVISIONS: Record<string, string> = {
  START: 'capability:start:v1',
  STOP: 'capability:stop:v1',
  START_UNIT_SEQUENCE: 'capability:start-unit-sequence:v1',
  STOP_UNIT_SEQUENCE: 'capability:stop-unit-sequence:v1',
  RESET_FAULT: 'capability:reset-fault:v1',
  SET_TEMPERATURE_SETPOINT: 'capability:set-temperature-setpoint:v1',
  SET_CHILLED_WATER_TEMPERATURE_SETPOINT: 'capability:set-chilled-water-temperature-setpoint:v1',
  SET_FREQUENCY: 'capability:set-frequency:v1',
  SET_FAN_SPEED: 'capability:set-fan-speed:v1',
  SET_LOAD_LIMIT: 'capability:set-load-limit:v1',
  SET_OPENING: 'capability:set-opening:v1',
};

const VALID_ACTUATION_MODES = new Set(['LEVEL', 'PULSE', 'UNIT_SEQUENCE', 'EMERGENCY_DO_RELEASE']);
const VALID_INTERLOCK_STATES = new Set(['RUNNING', 'STOPPED', 'OPEN', 'CLOSED', 'TRUE', 'FALSE', 'FLOWING']);
const VALID_HYDRAULIC_INTERLOCK_HINTS = new Set(['VALVE_OPEN', 'FLOW_PROVEN']);
const NUMERIC_CAPABILITY_PARAM_KEYS: Record<string, string> = {
  SET_TEMPERATURE_SETPOINT: 'setpointC',
  SET_CHILLED_WATER_TEMPERATURE_SETPOINT: 'setpointC',
  SET_FREQUENCY: 'frequencyHz',
  SET_FAN_SPEED: 'fanSpeedPct',
  SET_LOAD_LIMIT: 'loadLimitPct',
  SET_OPENING: 'openingPct',
};

export interface ControlInterlockInput {
  device: string;
  point: string;
  expected: string;
  reason?: string;
}

export interface ControlInterlockCheck {
  deviceId: string;
  key: string;
  expected: string;
  reason?: string;
}

export interface ControlPointLike {
  code: string;
  pointType?: string;
  status?: string;
  writable?: boolean;
  asset?: string;
  address?: string;
  protocol?: string;
  sourceMetadata?: Record<string, unknown>;
  device?: {
    id: string;
    code: string;
    sourceKey?: string;
    asset?: string;
  };
}

export interface ControlDeviceLike {
  id: string;
  code: string;
  asset?: string;
  points: ControlPointLike[];
}

export interface ControlAssetLike {
  id: string;
}

export function buildPointSourceMetadata(
  point: ControlPointLike,
  devicesByCode: ReadonlyMap<string, ControlDeviceLike>,
): Record<string, unknown> {
  const rawMetadata = point.sourceMetadata ? { ...point.sourceMetadata } : {};
  const rawInterlocks = rawMetadata.interlocks;
  delete rawMetadata.interlocks;

  const compiled: Record<string, unknown> = {
    protocol: point.protocol ?? 'MODBUS_RTU',
    address: point.address,
    gatewaySourceKey: point.device?.sourceKey,
    ...rawMetadata,
  };

  if (Array.isArray(rawInterlocks)) {
    const checks: ControlInterlockCheck[] = [];
    for (const item of rawInterlocks) {
      if (!item || typeof item !== 'object') continue;
      const spec = item as Partial<ControlInterlockInput>;
      const targetDevice = typeof spec.device === 'string' ? devicesByCode.get(spec.device) : undefined;
      if (!targetDevice || typeof spec.point !== 'string' || typeof spec.expected !== 'string') continue;
      checks.push({
        deviceId: targetDevice.id,
        key: spec.point,
        expected: spec.expected,
        ...(typeof spec.reason === 'string' && spec.reason.trim() !== '' ? { reason: spec.reason.trim() } : {}),
      });
    }
    compiled.interlockChecks = checks;
  }

  return compiled;
}

export function validateControlPoints(
  devices: ReadonlyArray<ControlDeviceLike>,
  assets: ReadonlyArray<ControlAssetLike>,
  activePoints: ReadonlyMap<string, ReadonlySet<string>>,
): string[] {
  const problems: string[] = [];
  const devicesByCode = new Map(devices.map((device) => [device.code, device]));
  const pointByDeviceCode = new Map<string, Map<string, ControlPointLike>>();
  for (const device of devices) {
    pointByDeviceCode.set(device.code, new Map(device.points.map((point) => [point.code, point])));
  }
  const assetIds = new Set(assets.map((asset) => asset.id));

  for (const device of devices) {
    for (const point of device.points) {
      if ((point.status ?? 'ACTIVE') !== 'ACTIVE') continue;
      if (point.pointType !== 'COMMAND') {
        const hint = point.sourceMetadata?.hydraulicInterlock;
        if (hint != null && (typeof hint !== 'string' || !VALID_HYDRAULIC_INTERLOCK_HINTS.has(hint))) {
          problems.push(`point ${device.code}/${point.code}: unsupported hydraulicInterlock ${String(hint)}`);
        }
        continue;
      }
      const where = `${device.code}/${point.code}`;
      if (point.writable === false) {
        problems.push(`command point ${where}: writable cannot be false`);
      }
      const targetAsset = point.asset ?? device.asset;
      if (!targetAsset || !assetIds.has(targetAsset)) {
        problems.push(`command point ${where}: target asset is missing from profile.assets`);
      }
      const metadata = point.sourceMetadata;
      if (!metadata || typeof metadata !== 'object') {
        problems.push(`command point ${where}: sourceMetadata is required`);
        continue;
      }
      if (metadata.safetyBoundary === 'HARD_REALTIME_SAFETY' || metadata.hardRealtimeProtected === true) {
        problems.push(`command point ${where}: hard real-time safety points cannot be registered as writable COMMAND points`);
      }
      const capability = metadata.capability;
      const capabilityRevision = metadata.capabilityRevision;
      if (typeof capability !== 'string' || !(capability in CAPABILITY_REVISIONS)) {
        problems.push(`command point ${where}: unsupported capability ${String(capability)}`);
      } else if (capabilityRevision !== CAPABILITY_REVISIONS[capability]) {
        problems.push(`command point ${where}: capabilityRevision ${String(capabilityRevision)} does not match ${CAPABILITY_REVISIONS[capability]}`);
      }
      if (
        metadata.controlKind === 'NUMBER' ||
        metadata.parameterKey != null ||
        metadata.minimum != null ||
        metadata.maximum != null
      ) {
        const expectedParam = typeof capability === 'string' ? NUMERIC_CAPABILITY_PARAM_KEYS[capability] : undefined;
        if (expectedParam != null && metadata.parameterKey != null && metadata.parameterKey !== expectedParam) {
          problems.push(`command point ${where}: parameterKey ${String(metadata.parameterKey)} must match ${expectedParam}`);
        }
        if (metadata.minimum != null || metadata.maximum != null) {
          if (
            typeof metadata.minimum !== 'number' ||
            !Number.isFinite(metadata.minimum) ||
            typeof metadata.maximum !== 'number' ||
            !Number.isFinite(metadata.maximum) ||
            metadata.minimum >= metadata.maximum
          ) {
            problems.push(`command point ${where}: numeric minimum and maximum must be finite numbers with minimum < maximum`);
          }
        }
        if (metadata.step != null && (typeof metadata.step !== 'number' || !Number.isFinite(metadata.step) || metadata.step <= 0)) {
          problems.push(`command point ${where}: numeric step must be a positive finite number`);
        }
      }
      const feedbackPointKey = metadata.feedbackPointKey;
      const devicePoints = pointByDeviceCode.get(device.code);
      const feedbackPoint = typeof feedbackPointKey === 'string' ? devicePoints?.get(feedbackPointKey) : undefined;
      if (
        typeof feedbackPointKey !== 'string' ||
        !activePoints.get(device.code)?.has(feedbackPointKey) ||
        !feedbackPoint ||
        feedbackPoint.pointType === 'COMMAND'
      ) {
        problems.push(`command point ${where}: feedbackPointKey ${String(feedbackPointKey)} must reference an active observed point on ${device.code}`);
      }

      if (metadata.actuationMode != null) {
        const mode = metadata.actuationMode;
        if (typeof mode !== 'string' || !VALID_ACTUATION_MODES.has(mode)) {
          problems.push(`command point ${where}: unsupported actuationMode ${String(mode)}`);
        } else if (mode === 'LEVEL' || mode === 'PULSE' || mode === 'EMERGENCY_DO_RELEASE') {
          if (typeof metadata.channel !== 'string' || metadata.channel.trim() === '') {
            problems.push(`command point ${where}: actuationMode ${mode} requires channel`);
          }
          if (mode !== 'EMERGENCY_DO_RELEASE' && (typeof metadata.coilAddress !== 'number' || !Number.isInteger(metadata.coilAddress) || metadata.coilAddress < 0)) {
            problems.push(`command point ${where}: actuationMode ${mode} requires non-negative integer coilAddress`);
          }
          if (mode === 'PULSE' && (typeof metadata.pulseDurationMs !== 'number' || !Number.isInteger(metadata.pulseDurationMs) || metadata.pulseDurationMs <= 0)) {
            problems.push(`command point ${where}: actuationMode PULSE requires positive integer pulseDurationMs`);
          }
        }
      }

      if (metadata.interlocks != null) {
        if (!Array.isArray(metadata.interlocks)) {
          problems.push(`command point ${where}: sourceMetadata.interlocks must be an array`);
          continue;
        }
        for (const item of metadata.interlocks) {
          if (!item || typeof item !== 'object') {
            problems.push(`command point ${where}: interlock entry must be an object`);
            continue;
          }
          const check = item as Partial<ControlInterlockInput>;
          if (typeof check.device !== 'string' || !devicesByCode.has(check.device)) {
            problems.push(`command point ${where}: interlock unknown device ${String(check.device)}`);
            continue;
          }
          const peerActive = typeof check.point === 'string' ? activePoints.get(check.device)?.has(check.point) : false;
          const peerPoint = typeof check.point === 'string' ? pointByDeviceCode.get(check.device)?.get(check.point) : undefined;
          if (!peerActive || !peerPoint || peerPoint.pointType === 'COMMAND') {
            problems.push(`command point ${where}: interlock ${check.device}/${String(check.point)} must reference an active observed point`);
          }
          if (typeof check.expected !== 'string' || !VALID_INTERLOCK_STATES.has(check.expected)) {
            problems.push(`command point ${where}: interlock expected state ${String(check.expected)} is unsupported`);
          }
        }
      }
    }
  }

  return problems;
}
