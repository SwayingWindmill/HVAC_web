import type {
  DeviceObservationPublication,
  DeviceObservationSnapshot,
  PresenceSnapshot,
  TelemetryKey,
  TelemetryKeyState,
} from '@/api/generated/s2Telemetry.gen';

const uuidV7Pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const clientSubscriptionPattern = /^[A-Za-z0-9._:-]{1,128}$/;
const telemetryKeyPattern = /^[A-Za-z][A-Za-z0-9._:-]{0,127}$/;
const opaqueSubscriptionPattern = /^[A-Za-z0-9_-]{16,256}$/;
const evaluationAvailability = new Set(['AVAILABLE', 'UNAVAILABLE']);
const availabilityReasons = new Set([
  'SOURCE_UNAVAILABLE',
  'OBSERVATION_COVERAGE_GAP',
  'POLICY_UNAVAILABLE',
  'OWNER_DEPENDENCY_UNAVAILABLE',
]);
const presenceApplicability = new Set(['APPLICABLE', 'NOT_APPLICABLE']);
const presenceStates = new Set(['ONLINE', 'OFFLINE', 'UNKNOWN']);
const readinessStates = new Set(['CURRENT', 'DEGRADED', 'INCOMPLETE', 'NOT_APPLICABLE']);
const displayStates = new Set(['ONLINE', 'OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE']);
const freshnessStates = new Set(['FRESH', 'STALE']);
const qualityStates = new Set(['GOOD', 'SUSPECT']);
const qualityReasons = new Set([
  'SOURCE_UNTRUSTED',
  'TYPE_MISMATCH',
  'UNIT_MISMATCH',
  'OUT_OF_RANGE',
  'CLOCK_AHEAD',
  'CLOCK_BEHIND',
  'SOURCE_LAG_EXCEEDED',
  'DUPLICATE',
  'OUT_OF_ORDER',
  'REPLAYED',
]);
const missingReasons = new Set(['NEVER_OBSERVED', 'ONLY_REJECTED_CANDIDATES', 'POLICY_NOT_CONFIGURED']);

export class TelemetryContractError extends Error {
  readonly category: 'shape' | 'scope' | 'schema';

  constructor(category: 'shape' | 'scope' | 'schema', message: string) {
    super(message);
    this.name = 'TelemetryContractError';
    this.category = category;
  }
}

function fail(category: 'shape' | 'scope' | 'schema', message: string): never {
  throw new TelemetryContractError(category, message);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function record(value: unknown, label: string): Record<string, unknown> {
  if (!isRecord(value)) fail('shape', `${label} must be an object`);
  return value;
}

function exactKeys(value: Record<string, unknown>, keys: ReadonlyArray<string>, label: string): void {
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    fail('schema', `${label} fields do not match schema version 1`);
  }
}

function string(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.length === 0) fail('shape', `${label} must be a non-empty string`);
  return value;
}

function nullableString(value: unknown, label: string): string | null {
  if (value === null) return null;
  return string(value, label);
}

function integer(value: unknown, label: string, minimum = 0): number {
  if (!Number.isSafeInteger(value) || Number(value) < minimum) fail('shape', `${label} must be a safe integer >= ${minimum}`);
  return Number(value);
}

function instant(value: unknown, label: string): string {
  const result = string(value, label);
  if (!/^\d{4}-\d{2}-\d{2}T/.test(result) || !Number.isFinite(Date.parse(result))) {
    fail('shape', `${label} must be an RFC3339 instant`);
  }
  return result;
}

function enumeration(value: unknown, allowed: ReadonlySet<string>, label: string): string {
  const result = string(value, label);
  if (!allowed.has(result)) fail('schema', `${label} is not supported`);
  return result;
}

function stringArray(value: unknown, allowed: ReadonlySet<string>, label: string): string[] {
  if (!Array.isArray(value)) fail('shape', `${label} must be an array`);
  const result = value.map((item, index) => enumeration(item, allowed, `${label}[${index}]`));
  if (new Set(result).size !== result.length) fail('shape', `${label} contains duplicates`);
  return result;
}

function telemetryValue(value: unknown, label: string): unknown {
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) fail('shape', `${label} number must be finite`);
    return value;
  }
  if (typeof value === 'string' || typeof value === 'boolean') return value;
  if (Array.isArray(value)) {
    for (const [index, item] of value.entries()) {
      if (!['number', 'string', 'boolean'].includes(typeof item) || (typeof item === 'number' && !Number.isFinite(item))) {
        fail('shape', `${label}[${index}] has an unsupported value type`);
      }
    }
    return value;
  }
  if (isRecord(value)) return value;
  return fail('shape', `${label} has an unsupported value type`);
}

function parsePresence(value: unknown, label: string): PresenceSnapshot {
  const input = record(value, label);
  exactKeys(input, ['applicability', 'currentState', 'lastSeenAt', 'policyRevision', 'lastKnown'], label);
  const applicability = enumeration(input.applicability, presenceApplicability, `${label}.applicability`);
  const currentState = input.currentState === null ? null : enumeration(input.currentState, presenceStates, `${label}.currentState`);
  const lastSeenAt = input.lastSeenAt === null ? null : instant(input.lastSeenAt, `${label}.lastSeenAt`);
  const policyRevision = input.policyRevision === null ? null : integer(input.policyRevision, `${label}.policyRevision`, 0);
  let lastKnown = null;
  if (input.lastKnown !== null) {
    const known = record(input.lastKnown, `${label}.lastKnown`);
    exactKeys(known, ['state', 'lastSeenAt', 'evaluatedAt', 'policyRevision'], `${label}.lastKnown`);
    lastKnown = {
      state: enumeration(known.state, presenceStates, `${label}.lastKnown.state`) as PresenceSnapshot['lastKnown'] extends infer T
        ? T extends { state: infer S } ? S : never
        : never,
      lastSeenAt: known.lastSeenAt === null ? null : instant(known.lastSeenAt, `${label}.lastKnown.lastSeenAt`),
      evaluatedAt: instant(known.evaluatedAt, `${label}.lastKnown.evaluatedAt`),
      policyRevision: integer(known.policyRevision, `${label}.lastKnown.policyRevision`, 0),
    };
  }
  if (applicability === 'NOT_APPLICABLE' && currentState !== null) fail('shape', `${label}.currentState must be null when not applicable`);
  return {
    applicability: applicability as PresenceSnapshot['applicability'],
    currentState: currentState as PresenceSnapshot['currentState'],
    lastSeenAt,
    policyRevision,
    lastKnown,
  };
}

function parseTelemetryKeyState(value: unknown, label: string): TelemetryKeyState {
  const input = record(value, label);
  const state = string(input.state, `${label}.state`);
  if (state === 'PRESENT') {
    exactKeys(input, ['key', 'state', 'value', 'valueType', 'unit', 'sampledAt', 'receivedAt', 'freshness', 'quality', 'qualityReasons', 'policyRevision'], label);
    const key = string(input.key, `${label}.key`);
    if (!telemetryKeyPattern.test(key)) fail('shape', `${label}.key is invalid`);
    const valueType = enumeration(input.valueType, new Set(['NUMBER', 'STRING', 'BOOLEAN', 'JSON']), `${label}.valueType`);
    const parsedValue = telemetryValue(input.value, `${label}.value`);
    if ((valueType === 'NUMBER' && typeof parsedValue !== 'number')
      || (valueType === 'STRING' && typeof parsedValue !== 'string')
      || (valueType === 'BOOLEAN' && typeof parsedValue !== 'boolean')
      || (valueType === 'JSON' && (!isRecord(parsedValue) && !Array.isArray(parsedValue)))) {
      fail('shape', `${label}.value does not match valueType`);
    }
    return {
      key,
      state: 'PRESENT',
      value: parsedValue as TelemetryKeyState extends infer T
        ? T extends { state: 'PRESENT'; value: infer V } ? V : never
        : never,
      valueType: valueType as 'NUMBER' | 'STRING' | 'BOOLEAN' | 'JSON',
      unit: nullableString(input.unit, `${label}.unit`),
      sampledAt: instant(input.sampledAt, `${label}.sampledAt`),
      receivedAt: instant(input.receivedAt, `${label}.receivedAt`),
      freshness: enumeration(input.freshness, freshnessStates, `${label}.freshness`) as 'FRESH' | 'STALE',
      quality: enumeration(input.quality, qualityStates, `${label}.quality`) as 'GOOD' | 'SUSPECT',
      qualityReasons: stringArray(input.qualityReasons, qualityReasons, `${label}.qualityReasons`) as TelemetryKeyState extends infer T
        ? T extends { state: 'PRESENT'; qualityReasons: infer Q } ? Q : never
        : never,
      policyRevision: integer(input.policyRevision, `${label}.policyRevision`, 0),
    };
  }
  if (state === 'MISSING') {
    exactKeys(input, ['key', 'state', 'freshness', 'missingReason', 'policyRevision'], label);
    const key = string(input.key, `${label}.key`);
    if (!telemetryKeyPattern.test(key)) fail('shape', `${label}.key is invalid`);
    if (input.freshness !== 'MISSING') fail('schema', `${label}.freshness is not supported`);
    return {
      key,
      state: 'MISSING',
      freshness: 'MISSING',
      missingReason: enumeration(input.missingReason, missingReasons, `${label}.missingReason`) as 'NEVER_OBSERVED' | 'ONLY_REJECTED_CANDIDATES' | 'POLICY_NOT_CONFIGURED',
      policyRevision: input.policyRevision === null ? null : integer(input.policyRevision, `${label}.policyRevision`, 0),
    };
  }
  return fail('schema', `${label}.state is not supported`);
}

function parseKeyStates(value: unknown, label: string): TelemetryKeyState[] {
  if (!Array.isArray(value)) fail('shape', `${label} must be an array`);
  const result = value.map((item, index) => parseTelemetryKeyState(item, `${label}[${index}]`));
  const keys = result.map((item) => item.key);
  if (new Set(keys).size !== keys.length) fail('shape', `${label} contains duplicate keys`);
  return result;
}

export function normalizeTarget(target: { clientSubscriptionId: string; deviceId: string; keys: ReadonlyArray<string> }): {
  clientSubscriptionId: string;
  deviceId: string;
  keys: TelemetryKey[];
} {
  if (!clientSubscriptionPattern.test(target.clientSubscriptionId)) fail('shape', 'clientSubscriptionId is invalid');
  if (!uuidV7Pattern.test(target.deviceId)) fail('shape', 'deviceId must be a lowercase UUIDv7');
  if (target.keys.length > 64) fail('shape', 'keys exceeds 64');
  const keys = target.keys.map((key) => {
    if (!telemetryKeyPattern.test(key)) fail('shape', `telemetry key is invalid: ${key}`);
    return key;
  });
  if (new Set(keys).size !== keys.length) fail('shape', 'keys contains duplicates');
  return { clientSubscriptionId: target.clientSubscriptionId, deviceId: target.deviceId, keys };
}

export function parseSnapshot(value: unknown, expectedDeviceId: string, expectedKeys: ReadonlyArray<string>): DeviceObservationSnapshot {
  const input = record(value, 'snapshot');
  exactKeys(input, [
    'schemaVersion', 'deviceId', 'owningOrganizationId', 'siteId', 'businessRevision', 'evaluatedAt',
    'evaluationAvailability', 'availabilityReasons', 'presence', 'telemetryReadiness', 'displayState', 'values',
  ], 'snapshot');
  if (input.schemaVersion !== 1) fail('schema', 'snapshot schemaVersion is not supported');
  const deviceId = string(input.deviceId, 'snapshot.deviceId');
  if (deviceId !== expectedDeviceId) fail('scope', 'snapshot Device does not match the subscription scope');
  const owningOrganizationId = string(input.owningOrganizationId, 'snapshot.owningOrganizationId');
  const siteId = string(input.siteId, 'snapshot.siteId');
  if (!uuidV7Pattern.test(owningOrganizationId) || !uuidV7Pattern.test(siteId)) fail('shape', 'snapshot owner/site identifiers are invalid');
  const values = parseKeyStates(input.values, 'snapshot.values');
  if (values.length !== expectedKeys.length || values.some((item, index) => item.key !== expectedKeys[index])) {
    fail('scope', 'snapshot keys do not exactly match the subscription scope');
  }
  const displayState = input.displayState === null ? null : enumeration(input.displayState, displayStates, 'snapshot.displayState');
  return {
    schemaVersion: 1,
    deviceId,
    owningOrganizationId,
    siteId,
    businessRevision: integer(input.businessRevision, 'snapshot.businessRevision', 1),
    evaluatedAt: instant(input.evaluatedAt, 'snapshot.evaluatedAt'),
    evaluationAvailability: enumeration(input.evaluationAvailability, evaluationAvailability, 'snapshot.evaluationAvailability') as 'AVAILABLE' | 'UNAVAILABLE',
    availabilityReasons: stringArray(input.availabilityReasons, availabilityReasons, 'snapshot.availabilityReasons') as DeviceObservationSnapshot['availabilityReasons'],
    presence: parsePresence(input.presence, 'snapshot.presence'),
    telemetryReadiness: enumeration(input.telemetryReadiness, readinessStates, 'snapshot.telemetryReadiness') as DeviceObservationSnapshot['telemetryReadiness'],
    displayState: displayState as DeviceObservationSnapshot['displayState'],
    values,
  };
}

export function parsePublication(
  value: unknown,
  expectedSubscriptionId: string,
  expectedDeviceId: string,
  selectedKeys: ReadonlySet<string>,
): DeviceObservationPublication {
  const input = record(value, 'publication');
  exactKeys(input, [
    'schemaVersion', 'kind', 'eventId', 'subscriptionId', 'deviceId', 'previousRevision', 'revision',
    'evaluatedAt', 'publishedAt', 'evaluationAvailability', 'availabilityReasons', 'presence',
    'telemetryReadiness', 'displayState', 'telemetryChanges',
  ], 'publication');
  if (input.schemaVersion !== 1 || input.kind !== 'DEVICE_OBSERVATION_DELTA') fail('schema', 'publication schema/kind is not supported');
  const subscriptionId = string(input.subscriptionId, 'publication.subscriptionId');
  const deviceId = string(input.deviceId, 'publication.deviceId');
  if (!opaqueSubscriptionPattern.test(subscriptionId)) fail('shape', 'publication subscriptionId is invalid');
  if (subscriptionId !== expectedSubscriptionId || deviceId !== expectedDeviceId) {
    fail('scope', 'publication does not match the subscription scope');
  }
  const eventId = string(input.eventId, 'publication.eventId');
  if (!uuidV7Pattern.test(eventId)) fail('shape', 'publication eventId is invalid');
  const changes = parseKeyStates(input.telemetryChanges, 'publication.telemetryChanges');
  if (changes.some((change) => !selectedKeys.has(change.key))) fail('scope', 'publication contains an unselected key');
  const displayState = input.displayState === null ? null : enumeration(input.displayState, displayStates, 'publication.displayState');
  return {
    schemaVersion: 1,
    kind: 'DEVICE_OBSERVATION_DELTA',
    eventId,
    subscriptionId,
    deviceId,
    previousRevision: integer(input.previousRevision, 'publication.previousRevision', 0),
    revision: integer(input.revision, 'publication.revision', 1),
    evaluatedAt: instant(input.evaluatedAt, 'publication.evaluatedAt'),
    publishedAt: instant(input.publishedAt, 'publication.publishedAt'),
    evaluationAvailability: enumeration(input.evaluationAvailability, evaluationAvailability, 'publication.evaluationAvailability') as 'AVAILABLE' | 'UNAVAILABLE',
    availabilityReasons: stringArray(input.availabilityReasons, availabilityReasons, 'publication.availabilityReasons') as DeviceObservationPublication['availabilityReasons'],
    presence: parsePresence(input.presence, 'publication.presence'),
    telemetryReadiness: enumeration(input.telemetryReadiness, readinessStates, 'publication.telemetryReadiness') as DeviceObservationPublication['telemetryReadiness'],
    displayState: displayState as DeviceObservationPublication['displayState'],
    telemetryChanges: changes,
  };
}

export function cloneSnapshot(snapshot: DeviceObservationSnapshot): DeviceObservationSnapshot {
  return JSON.parse(JSON.stringify(snapshot)) as DeviceObservationSnapshot;
}

export function scopeKey(deviceId: string, keys: ReadonlyArray<string>): string {
  return `${deviceId}\u0000${keys.join('\u0000')}`;
}
