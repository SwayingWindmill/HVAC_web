import type {
  ConcentratorUnitShadow,
  GatewayAutonomyPolicy,
  GatewayConfigShadow,
  GatewayConfigShadowUpdateRequest,
  GatewayDesiredConfig,
  GatewayModbusDriverConfig,
  GatewayModbusRegisterMapping,
  GatewayPointMapping,
  GatewaySamplingPolicy,
  PointCopyTarget,
} from '../../api/generated/platformGateway.gen.ts';
import { isOneOf } from '../../lib/typed.ts';

const POINT_CODE_PATTERN = /^[a-z][a-z0-9_]{0,127}$/;
const UINT16_DECIMAL_PATTERN = /^(?:0|[1-9]\d{0,4})$/;

let draftIdSequence = 0;

function nextDraftId(prefix: string): string {
  draftIdSequence += 1;
  return `${prefix}-${draftIdSequence}`;
}

function compareAscii(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

function normalizeZero(value: number): number {
  return value === 0 ? 0 : value;
}

export const MODBUS_DRIVER_TYPES = ['MODBUS_TCP', 'MODBUS_RTU'] as const;
export const MODBUS_READ_FUNCTION_CODES = ['3', '4'] as const;
export const MODBUS_RAW_TYPES = [
  'UINT16',
  'INT16',
  'UINT32',
  'INT32',
  'FLOAT32',
  'BIT_BOOL',
  'ENUM16',
] as const;
export const MODBUS_ENDIAN_OPTIONS = ['BIG_ENDIAN', 'LITTLE_ENDIAN'] as const;
export const MODBUS_POLL_PRIORITIES = ['VERY_HIGH', 'HIGH', 'MEDIUM', 'LOW'] as const;

export const MODBUS_CAPABILITY_PROFILES = [
  'VARIABLE_SPEED_PUMP',
  'CHILLER',
  'COOLING_TOWER',
  'ELECTRICITY_METER',
  'THERMAL_ENERGY_METER',
  'WEATHER_STATION',
  'ZONE_TEMPERATURE_SENSOR',
] as const satisfies readonly NonNullable<GatewayModbusDriverConfig['capabilityProfile']>[];

export type ModbusCapabilityProfile = (typeof MODBUS_CAPABILITY_PROFILES)[number];

export const MODBUS_CAPABILITY_PROFILE_LABELS: Readonly<
  Record<ModbusCapabilityProfile, string>
> = {
  VARIABLE_SPEED_PUMP: '变频水泵',
  CHILLER: '冷机',
  COOLING_TOWER: '冷却塔',
  ELECTRICITY_METER: '电表',
  THERMAL_ENERGY_METER: '热能表',
  WEATHER_STATION: '气象站',
  ZONE_TEMPERATURE_SENSOR: '温区传感器',
};

export const MODBUS_DRIVER_TYPE_LABELS: Readonly<Record<GatewayModbusDriverConfig['driverType'], string>> = {
  MODBUS_TCP: 'Modbus TCP',
  MODBUS_RTU: 'Modbus RTU',
};

export const MODBUS_FUNCTION_CODE_LABELS: Readonly<
  Record<GatewayModbusRegisterMapping['functionCode'], string>
> = {
  3: '保持寄存器 FC3',
  4: '输入寄存器 FC4',
};

export const MODBUS_WRITE_FUNCTION_CODE_LABELS: Readonly<Record<'6' | '16', string>> = {
  6: '单写 FC6',
  16: '多写 FC16',
};

export const MODBUS_RAW_TYPE_LABELS: Readonly<Record<GatewayModbusRegisterMapping['rawType'], string>> = {
  UINT16: '16 位无符号整数',
  INT16: '16 位有符号整数',
  UINT32: '32 位无符号整数',
  INT32: '32 位有符号整数',
  FLOAT32: '32 位浮点数',
  BIT_BOOL: '位布尔',
  ENUM16: '16 位枚举',
};

export const MODBUS_BYTE_ORDER_LABELS: Readonly<
  Record<GatewayModbusRegisterMapping['byteOrder'], string>
> = {
  BIG_ENDIAN: '大端字节序',
  LITTLE_ENDIAN: '小端字节序',
};

export const MODBUS_WORD_ORDER_LABELS: Readonly<
  Record<GatewayModbusRegisterMapping['wordOrder'], string>
> = {
  BIG_ENDIAN: '大端字序',
  LITTLE_ENDIAN: '小端字序',
};

export const MODBUS_POLL_PRIORITY_PRESENTATION: Readonly<
  Record<
    GatewayModbusRegisterMapping['pollPriority'],
    { readonly label: string; readonly tone: 'info' | 'warning' | 'neutral' }
  >
> = {
  VERY_HIGH: { label: '极高', tone: 'info' },
  HIGH: { label: '高', tone: 'warning' },
  MEDIUM: { label: '中', tone: 'neutral' },
  LOW: { label: '低', tone: 'neutral' },
};

export type AutonomyPolicySummaryState = 'ENABLED' | 'DISABLED' | 'UNCONFIGURED';

export const AUTONOMY_POLICY_STATE_PRESENTATION: Readonly<
  Record<AutonomyPolicySummaryState, { readonly label: string; readonly tone: 'success' | 'neutral' }>
> = {
  ENABLED: { label: '已启用本地自治', tone: 'success' },
  DISABLED: { label: '仅云端受控', tone: 'neutral' },
  UNCONFIGURED: { label: '未配置', tone: 'neutral' },
};

export const AUTONOMY_POLICY_BOUNDS = {
  offlineGraceSeconds: { min: 10, max: 3600 },
  defaultChilledWaterSupplySetpointC: { min: 4, max: 18 },
  pumpFrequencyHz: { min: 15, max: 50 },
  minRunSeconds: { min: 0, max: 3600 },
  minStopSeconds: { min: 0, max: 3600 },
  snapshotIntervalSeconds: { min: 30, max: 3600 },
} as const;

export interface GatewayPointMappingDraft {
  readonly draftId: string;
  readonly sourceChannel: string;
  readonly primaryDeviceKey: string;
  readonly primaryPointCode: string;
  readonly copyTargetsText: string;
}

export interface GatewaySamplingPolicyDraft {
  readonly draftId: string;
  readonly deviceKey: string;
  readonly pointCode: string;
  readonly sampleIntervalMs: string;
  readonly publishIntervalMs: string;
}

export interface GatewayModbusRegisterDraft {
  readonly draftId: string;
  readonly pointCode: string;
  readonly semanticChannel: string;
  readonly functionCode: '3' | '4';
  readonly writeFunctionCode: '' | '6' | '16';
  readonly address: string;
  readonly registerCount: string;
  readonly rawType: GatewayModbusRegisterMapping['rawType'];
  readonly byteOrder: GatewayModbusRegisterMapping['byteOrder'];
  readonly wordOrder: GatewayModbusRegisterMapping['wordOrder'];
  readonly bitIndex: string;
  readonly scale: string;
  readonly offset: string;
  readonly pollPriority: GatewayModbusRegisterMapping['pollPriority'];
  readonly enumMapText: string;
}

export interface GatewayModbusDriverDraft {
  readonly draftId: string;
  readonly deviceKey: string;
  readonly driverType: GatewayModbusDriverConfig['driverType'];
  readonly endpoint: string;
  readonly unitId: string;
  readonly capabilityProfile: string;
  readonly timeoutMs: string;
  readonly retries: string;
  readonly registers: readonly GatewayModbusRegisterDraft[];
}

export interface GatewayAutonomyPolicyDraft {
  readonly enabled: boolean;
  readonly offlineGraceSeconds: string;
  readonly defaultChilledWaterSupplySetpointC: string;
  readonly defaultPumpFrequencyHz: string;
  readonly minPumpFrequencyHz: string;
  readonly maxPumpFrequencyHz: string;
  readonly minRunSeconds: string;
  readonly minStopSeconds: string;
  readonly snapshotIntervalSeconds: string;
}

export interface GatewayConfigShadowEditorDraft {
  readonly pointMappings: readonly GatewayPointMappingDraft[];
  readonly samplingPolicies: readonly GatewaySamplingPolicyDraft[];
  readonly driverConfigs: readonly GatewayModbusDriverDraft[];
  readonly autonomyPolicy: GatewayAutonomyPolicyDraft | null;
  readonly reason: string;
}

export interface ConcentratorShadowUnitSummary {
  readonly unitId: string;
  readonly hasParams: boolean;
  readonly boundEidCount: number;
}

export function createEmptyPointMappingDraft(): GatewayPointMappingDraft {
  return {
    draftId: nextDraftId('map'),
    sourceChannel: '',
    primaryDeviceKey: '',
    primaryPointCode: '',
    copyTargetsText: '',
  };
}

export function createEmptySamplingPolicyDraft(): GatewaySamplingPolicyDraft {
  return {
    draftId: nextDraftId('samp'),
    deviceKey: '',
    pointCode: '',
    sampleIntervalMs: '5000',
    publishIntervalMs: '5000',
  };
}

export function createEmptyModbusRegisterDraft(): GatewayModbusRegisterDraft {
  return {
    draftId: nextDraftId('reg'),
    pointCode: '',
    semanticChannel: '',
    functionCode: '3',
    writeFunctionCode: '',
    address: '0',
    registerCount: '1',
    rawType: 'UINT16',
    byteOrder: 'BIG_ENDIAN',
    wordOrder: 'BIG_ENDIAN',
    bitIndex: '',
    scale: '1',
    offset: '0',
    pollPriority: 'MEDIUM',
    enumMapText: '',
  };
}

export function createEmptyModbusDriverDraft(): GatewayModbusDriverDraft {
  return {
    draftId: nextDraftId('drv'),
    deviceKey: '',
    driverType: 'MODBUS_TCP',
    endpoint: '',
    unitId: '1',
    capabilityProfile: '',
    timeoutMs: '1000',
    retries: '2',
    registers: [createEmptyModbusRegisterDraft()],
  };
}

export function createDefaultAutonomyPolicyDraft(): GatewayAutonomyPolicyDraft {
  return {
    enabled: true,
    offlineGraceSeconds: '120',
    defaultChilledWaterSupplySetpointC: '7',
    defaultPumpFrequencyHz: '40',
    minPumpFrequencyHz: '25',
    maxPumpFrequencyHz: '50',
    minRunSeconds: '300',
    minStopSeconds: '180',
    snapshotIntervalSeconds: '300',
  };
}

export function defaultRegisterCountForRawType(
  rawType: GatewayModbusRegisterMapping['rawType'],
): '1' | '2' {
  return rawType === 'UINT32' || rawType === 'INT32' || rawType === 'FLOAT32' ? '2' : '1';
}

export function summarizeAutonomyPolicyState(
  policy: GatewayAutonomyPolicy | null | undefined,
): AutonomyPolicySummaryState {
  if (!policy) return 'UNCONFIGURED';
  return policy.enabled ? 'ENABLED' : 'DISABLED';
}

export function countDriverRegisters(
  driverConfigs: readonly GatewayModbusDriverConfig[] | undefined,
): number {
  if (!driverConfigs) return 0;
  return driverConfigs.reduce((sum, driver) => sum + driver.registers.length, 0);
}

export function formatPointCopyTargets(targets?: readonly PointCopyTarget[]): string {
  if (!targets || targets.length === 0) return '';
  return targets.map((item) => `${item.deviceKey}:${item.pointCode}`).join(', ');
}

export function parsePointCopyTargets(
  raw: string,
): { readonly ok: true; readonly targets: PointCopyTarget[] } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: true, targets: [] };
  }
  const tokens = trimmed
    .split(/[\n,]+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
  const targets: PointCopyTarget[] = [];
  for (const token of tokens) {
    const sepIndex = token.indexOf(':');
    if (sepIndex <= 0 || sepIndex === token.length - 1) {
      return {
        ok: false,
        error: `复用目标 "${token}" 格式须为 deviceKey:pointCode`,
      };
    }
    const deviceKey = token.slice(0, sepIndex).trim();
    const pointCode = token.slice(sepIndex + 1).trim();
    if (!deviceKey || deviceKey.length > 128) {
      return {
        ok: false,
        error: `复用目标 "${token}" 的设备标识无效`,
      };
    }
    if (!POINT_CODE_PATTERN.test(pointCode)) {
      return {
        ok: false,
        error: `复用目标 "${token}" 的点位编码须以小写字母开头，仅含小写字母、数字和下划线`,
      };
    }
    targets.push({ deviceKey, pointCode });
  }
  return { ok: true, targets };
}

function isValidUint16DecimalKey(key: string): boolean {
  if (!UINT16_DECIMAL_PATTERN.test(key)) return false;
  const parsed = Number(key);
  return Number.isInteger(parsed) && parsed >= 0 && parsed <= 65535;
}

export function formatModbusEnumMap(enumMap?: Readonly<Record<string, string>>): string {
  if (!enumMap) return '';
  return Object.entries(enumMap)
    .sort(([left], [right]) => {
      const leftNum = Number(left);
      const rightNum = Number(right);
      if (Number.isInteger(leftNum) && Number.isInteger(rightNum) && leftNum !== rightNum) {
        return leftNum - rightNum;
      }
      return compareAscii(left, right);
    })
    .map(([key, label]) => `${key}=${label}`)
    .join(', ');
}

export function parseModbusEnumMap(
  raw: string,
): { readonly ok: true; readonly enumMap: Record<string, string> } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return {
      ok: false,
      error: '16 位枚举寄存器须至少配置一项枚举映射（例如 0=停机, 1=运行）',
    };
  }
  const tokens = trimmed
    .split(/[\n,，]+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
  if (tokens.length === 0) {
    return {
      ok: false,
      error: '16 位枚举寄存器须至少配置一项枚举映射（例如 0=停机, 1=运行）',
    };
  }
  const enumMap: Record<string, string> = {};
  for (const token of tokens) {
    const sepIndex = token.indexOf('=');
    if (sepIndex <= 0) {
      return {
        ok: false,
        error: `枚举映射 "${token}" 格式须为 数值=状态标签`,
      };
    }
    const key = token.slice(0, sepIndex).trim();
    const label = token.slice(sepIndex + 1).trim();
    if (!isValidUint16DecimalKey(key)) {
      return {
        ok: false,
        error: `枚举键 "${key}" 须为 0 至 65535 的十进制无符号整数`,
      };
    }
    if (!label || label.length > 64) {
      return {
        ok: false,
        error: `枚举键 ${key} 的状态标签不能为空且不超过 64 个字符`,
      };
    }
    if (Object.prototype.hasOwnProperty.call(enumMap, key)) {
      return {
        ok: false,
        error: `枚举键 ${key} 重复`,
      };
    }
    enumMap[key] = label;
  }
  return { ok: true, enumMap };
}

function isValidModbusTcpEndpoint(endpoint: string): boolean {
  if (!endpoint || endpoint.length > 256 || /\s/.test(endpoint)) {
    return false;
  }
  let host: string;
  let portStr: string;
  if (endpoint.startsWith('[')) {
    const closeBracket = endpoint.indexOf(']');
    if (closeBracket <= 1 || endpoint[closeBracket + 1] !== ':') {
      return false;
    }
    host = endpoint.slice(1, closeBracket);
    portStr = endpoint.slice(closeBracket + 2);
    if (host.includes('[') || host.includes('/')) {
      return false;
    }
  } else {
    const colonIdx = endpoint.lastIndexOf(':');
    if (colonIdx <= 0) {
      return false;
    }
    host = endpoint.slice(0, colonIdx);
    portStr = endpoint.slice(colonIdx + 1);
    if (host.includes(':') || host.includes('/')) {
      return false;
    }
  }
  if (!/^[1-9]\d{0,4}$/.test(portStr)) {
    return false;
  }
  const port = Number(portStr);
  return port >= 1 && port <= 65535;
}

export function createShadowEditorDraft(
  shadow: GatewayConfigShadow | undefined,
): GatewayConfigShadowEditorDraft {
  const desired = shadow?.desired;
  const pointMappings = (desired?.pointMappings ?? []).map((item) => ({
    draftId: nextDraftId('map'),
    sourceChannel: item.sourceChannel,
    primaryDeviceKey: item.primaryDeviceKey,
    primaryPointCode: item.primaryPointCode,
    copyTargetsText: formatPointCopyTargets(item.copyTargets),
  }));
  const samplingPolicies = (desired?.samplingPolicies ?? []).map((item) => ({
    draftId: nextDraftId('samp'),
    deviceKey: item.deviceKey,
    pointCode: item.pointCode ?? '',
    sampleIntervalMs: String(item.sampleIntervalMs),
    publishIntervalMs: String(item.publishIntervalMs),
  }));
  const driverConfigs = (desired?.driverConfigs ?? []).map((driver) => ({
    draftId: nextDraftId('drv'),
    deviceKey: driver.deviceKey,
    driverType: driver.driverType,
    endpoint: driver.endpoint,
    unitId: String(driver.unitId),
    capabilityProfile: driver.capabilityProfile ?? '',
    timeoutMs: String(driver.timeoutMs),
    retries: String(driver.retries),
    registers: driver.registers.map((reg) => ({
      draftId: nextDraftId('reg'),
      pointCode: reg.pointCode,
      semanticChannel: reg.semanticChannel ?? '',
      functionCode: reg.functionCode === 4 ? ('4' as const) : ('3' as const),
      writeFunctionCode:
        reg.writeFunctionCode === 6
          ? ('6' as const)
          : reg.writeFunctionCode === 16
            ? ('16' as const)
            : ('' as const),
      address: String(reg.address),
      registerCount: String(reg.registerCount),
      rawType: reg.rawType,
      byteOrder: reg.byteOrder,
      wordOrder: reg.wordOrder,
      bitIndex: reg.bitIndex !== undefined ? String(reg.bitIndex) : '',
      scale: String(reg.scale),
      offset: String(reg.offset),
      pollPriority: reg.pollPriority,
      enumMapText: formatModbusEnumMap(reg.enumMap),
    })),
  }));
  const autonomyPolicy = desired?.autonomyPolicy
    ? {
        enabled: desired.autonomyPolicy.enabled,
        offlineGraceSeconds: String(desired.autonomyPolicy.offlineGraceSeconds),
        defaultChilledWaterSupplySetpointC: String(
          desired.autonomyPolicy.defaultChilledWaterSupplySetpointC,
        ),
        defaultPumpFrequencyHz: String(desired.autonomyPolicy.defaultPumpFrequencyHz),
        minPumpFrequencyHz: String(desired.autonomyPolicy.minPumpFrequencyHz),
        maxPumpFrequencyHz: String(desired.autonomyPolicy.maxPumpFrequencyHz),
        minRunSeconds: String(desired.autonomyPolicy.minRunSeconds),
        minStopSeconds: String(desired.autonomyPolicy.minStopSeconds),
        snapshotIntervalSeconds: String(desired.autonomyPolicy.snapshotIntervalSeconds),
      }
    : null;

  return {
    pointMappings,
    samplingPolicies,
    driverConfigs,
    autonomyPolicy,
    reason: '',
  };
}

export function summarizeConcentratorShadowUnits(
  configs: Readonly<Record<string, ConcentratorUnitShadow>> | undefined,
): readonly ConcentratorShadowUnitSummary[] {
  if (!configs) return [];
  return Object.entries(configs)
    .map(([unitId, unit]) => ({
      unitId,
      hasParams: unit.params !== undefined,
      boundEidCount: (unit.eids ?? []).filter((slot) => slot.eid > 0).length,
    }))
    .sort((left, right) => {
      const leftNum = Number(left.unitId);
      const rightNum = Number(right.unitId);
      if (Number.isInteger(leftNum) && Number.isInteger(rightNum)) {
        return leftNum - rightNum;
      }
      return left.unitId.localeCompare(right.unitId);
    });
}

function parseIntegerInRange(
  raw: string,
  min: number,
  max: number,
  label: string,
): { readonly ok: true; readonly value: number } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: false, error: `${label}不能为空` };
  }
  const value = Number(trimmed);
  if (!Number.isInteger(value) || value < min || value > max) {
    return { ok: false, error: `${label}须为 ${min} 至 ${max} 之间的整数` };
  }
  return { ok: true, value };
}

function parseNumberInRange(
  raw: string,
  min: number,
  max: number,
  unit: string,
  label: string,
): { readonly ok: true; readonly value: number } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: false, error: `${label}不能为空` };
  }
  const value = Number(trimmed);
  if (!Number.isFinite(value) || value < min || value > max) {
    return { ok: false, error: `${label}须在 ${min} 至 ${max}${unit}范围内` };
  }
  return { ok: true, value };
}

function parseFiniteNumber(
  raw: string,
  label: string,
): { readonly ok: true; readonly value: number } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: false, error: `${label}不能为空` };
  }
  const value = Number(trimmed);
  if (!Number.isFinite(value)) {
    return { ok: false, error: `${label}须为有效数字` };
  }
  return { ok: true, value };
}

function validateDriverConfigs(
  draftDrivers: readonly GatewayModbusDriverDraft[],
):
  | { readonly ok: true; readonly driverConfigs: GatewayModbusDriverConfig[] }
  | { readonly ok: false; readonly error: string } {
  const seenDeviceKeys = new Set<string>();
  const driverConfigs: GatewayModbusDriverConfig[] = [];
  for (let dIndex = 0; dIndex < draftDrivers.length; dIndex += 1) {
    const driver = draftDrivers[dIndex]!;
    const prefix = `协议驱动第 ${dIndex + 1} 项`;
    const deviceKey = driver.deviceKey.trim();
    const endpoint = driver.endpoint.trim();
    const rawProfile = driver.capabilityProfile.trim();

    if (!deviceKey || deviceKey.length > 128 || deviceKey.includes('/')) {
      return { ok: false, error: `${prefix}的本地设备标识须为 1 至 128 个字符且不能包含 "/"` };
    }
    if (seenDeviceKeys.has(deviceKey)) {
      return { ok: false, error: `协议驱动设备标识 "${deviceKey}" 重复` };
    }
    seenDeviceKeys.add(deviceKey);

    if (!isOneOf(MODBUS_DRIVER_TYPES, driver.driverType)) {
      return { ok: false, error: `${prefix}的协议类型无效` };
    }
    if (!endpoint || endpoint.length > 256) {
      return { ok: false, error: `${prefix}的采集地址不能为空且不超过 256 个字符` };
    }
    if (driver.driverType === 'MODBUS_TCP' && !isValidModbusTcpEndpoint(endpoint)) {
      return { ok: false, error: `${prefix}的采集地址须为 host:port 格式且端口在 1 至 65535 之间` };
    }

    let capabilityProfile: ModbusCapabilityProfile | undefined;
    if (rawProfile) {
      if (!isOneOf(MODBUS_CAPABILITY_PROFILES, rawProfile)) {
        return { ok: false, error: `${prefix}的能力档位无效` };
      }
      capabilityProfile = rawProfile;
    }

    const unitIdRes = parseIntegerInRange(driver.unitId, 1, 247, `${prefix}的从站地址 (UID)`);
    if (!unitIdRes.ok) return unitIdRes;

    const timeoutRes = parseIntegerInRange(driver.timeoutMs, 100, 30000, `${prefix}的超时时间`);
    if (!timeoutRes.ok) return timeoutRes;

    const retriesRes = parseIntegerInRange(driver.retries, 0, 10, `${prefix}的重试次数`);
    if (!retriesRes.ok) return retriesRes;

    if (driver.registers.length === 0 || driver.registers.length > 256) {
      return { ok: false, error: `${prefix}至少需要配置 1 条寄存器映射，最多 256 条` };
    }

    const seenPointCodes = new Set<string>();
    const seenBitKeys = new Set<string>();
    const registers: GatewayModbusRegisterMapping[] = [];
    for (let rIndex = 0; rIndex < driver.registers.length; rIndex += 1) {
      const reg = driver.registers[rIndex]!;
      const regPrefix = `${prefix}第 ${rIndex + 1} 条寄存器`;
      const pointCode = reg.pointCode.trim();
      const semanticChannel = reg.semanticChannel.trim();

      if (!POINT_CODE_PATTERN.test(pointCode)) {
        return {
          ok: false,
          error: `${regPrefix}的点位编码 "${pointCode || '(空)'}" 须以小写字母开头，仅含小写字母、数字和下划线`,
        };
      }
      if (seenPointCodes.has(pointCode)) {
        return { ok: false, error: `${prefix}中点位编码 "${pointCode}" 重复` };
      }
      seenPointCodes.add(pointCode);

      if (semanticChannel.length > 64) {
        return { ok: false, error: `${regPrefix}的语义通道不能超过 64 个字符` };
      }

      const functionCode = reg.functionCode === '4' ? (4 as const) : (3 as const);
      const writeFunctionCode =
        reg.writeFunctionCode === '6'
          ? (6 as const)
          : reg.writeFunctionCode === '16'
            ? (16 as const)
            : undefined;

      if (functionCode === 4 && writeFunctionCode !== undefined) {
        return { ok: false, error: `${regPrefix}使用输入寄存器 (FC4) 时为只读，不能配置写功能码` };
      }

      if (!isOneOf(MODBUS_RAW_TYPES, reg.rawType)) {
        return { ok: false, error: `${regPrefix}的数据类型无效` };
      }

      const addressRes = parseIntegerInRange(reg.address, 0, 65535, `${regPrefix}的寄存器地址`);
      if (!addressRes.ok) return addressRes;

      const countRes = parseIntegerInRange(reg.registerCount, 1, 4, `${regPrefix}的寄存器数量`);
      if (!countRes.ok) return countRes;

      const expectedCount = Number(defaultRegisterCountForRawType(reg.rawType));
      if (countRes.value !== expectedCount) {
        return {
          ok: false,
          error: `${regPrefix}的寄存器数量须为 ${expectedCount}（匹配${MODBUS_RAW_TYPE_LABELS[reg.rawType]}）`,
        };
      }
      if (addressRes.value + countRes.value > 65536) {
        return {
          ok: false,
          error: `${regPrefix}的寄存器地址与数量之和不能超过 65536`,
        };
      }
      if (writeFunctionCode === 6 && countRes.value !== 1) {
        return {
          ok: false,
          error: `${regPrefix}使用单写 (FC6) 时仅支持 1 个寄存器`,
        };
      }

      if (!isOneOf(MODBUS_ENDIAN_OPTIONS, reg.byteOrder)) {
        return { ok: false, error: `${regPrefix}的字节序无效` };
      }
      if (!isOneOf(MODBUS_ENDIAN_OPTIONS, reg.wordOrder)) {
        return { ok: false, error: `${regPrefix}的字序无效` };
      }
      if (!isOneOf(MODBUS_POLL_PRIORITIES, reg.pollPriority)) {
        return { ok: false, error: `${regPrefix}的轮询优先级无效` };
      }

      let bitIndex: number | undefined;
      if (reg.rawType === 'BIT_BOOL') {
        const bitRes = parseIntegerInRange(reg.bitIndex, 0, 15, `${regPrefix}的位布尔索引`);
        if (!bitRes.ok) return bitRes;
        bitIndex = bitRes.value;
        const bitKey = `${functionCode}:${addressRes.value}:${bitIndex}`;
        if (seenBitKeys.has(bitKey)) {
          return {
            ok: false,
            error: `${prefix}中读功能码 FC${functionCode} 地址 ${addressRes.value} 第 ${bitIndex} 位的位布尔映射重复`,
          };
        }
        seenBitKeys.add(bitKey);
      } else if (reg.bitIndex.trim() !== '') {
        return { ok: false, error: `${regPrefix}仅位布尔类型可配置位索引` };
      }

      const scaleRes = parseFiniteNumber(reg.scale, `${regPrefix}的倍率`);
      if (!scaleRes.ok) return scaleRes;
      if (scaleRes.value === 0) {
        return { ok: false, error: `${regPrefix}的倍率不能为 0` };
      }

      const offsetRes = parseFiniteNumber(reg.offset, `${regPrefix}的偏置`);
      if (!offsetRes.ok) return offsetRes;

      let enumMap: Record<string, string> | undefined;
      if (reg.rawType === 'ENUM16') {
        const enumRes = parseModbusEnumMap(reg.enumMapText);
        if (!enumRes.ok) {
          return { ok: false, error: `${regPrefix}：${enumRes.error}` };
        }
        enumMap = enumRes.enumMap;
      } else if (reg.enumMapText.trim() !== '') {
        return { ok: false, error: `${regPrefix}仅 16 位枚举类型可配置枚举映射` };
      }

      registers.push({
        pointCode,
        ...(semanticChannel ? { semanticChannel } : {}),
        functionCode,
        ...(writeFunctionCode !== undefined ? { writeFunctionCode } : {}),
        address: addressRes.value,
        registerCount: countRes.value,
        rawType: reg.rawType,
        byteOrder: reg.byteOrder,
        wordOrder: reg.wordOrder,
        ...(bitIndex !== undefined ? { bitIndex } : {}),
        scale: normalizeZero(scaleRes.value),
        offset: normalizeZero(offsetRes.value),
        pollPriority: reg.pollPriority,
        ...(enumMap ? { enumMap } : {}),
      });
    }

    driverConfigs.push({
      deviceKey,
      driverType: driver.driverType,
      endpoint,
      unitId: unitIdRes.value,
      ...(capabilityProfile ? { capabilityProfile } : {}),
      timeoutMs: timeoutRes.value,
      retries: retriesRes.value,
      registers,
    });
  }
  return { ok: true, driverConfigs };
}

function validateAutonomyPolicy(
  draftPolicy: GatewayAutonomyPolicyDraft,
):
  | { readonly ok: true; readonly autonomyPolicy: GatewayAutonomyPolicy }
  | { readonly ok: false; readonly error: string } {
  const graceRes = parseIntegerInRange(
    draftPolicy.offlineGraceSeconds,
    AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds.min,
    AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds.max,
    '断网宽限时间',
  );
  if (!graceRes.ok) return graceRes;

  const setpointRes = parseNumberInRange(
    draftPolicy.defaultChilledWaterSupplySetpointC,
    AUTONOMY_POLICY_BOUNDS.defaultChilledWaterSupplySetpointC.min,
    AUTONOMY_POLICY_BOUNDS.defaultChilledWaterSupplySetpointC.max,
    ' °C ',
    '冷冻水供水设定值',
  );
  if (!setpointRes.ok) return setpointRes;

  const minFreqRes = parseNumberInRange(
    draftPolicy.minPumpFrequencyHz,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.min,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.max,
    ' Hz ',
    '水泵最小频率',
  );
  if (!minFreqRes.ok) return minFreqRes;

  const defaultFreqRes = parseNumberInRange(
    draftPolicy.defaultPumpFrequencyHz,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.min,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.max,
    ' Hz ',
    '水泵默认频率',
  );
  if (!defaultFreqRes.ok) return defaultFreqRes;

  const maxFreqRes = parseNumberInRange(
    draftPolicy.maxPumpFrequencyHz,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.min,
    AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.max,
    ' Hz ',
    '水泵最大频率',
  );
  if (!maxFreqRes.ok) return maxFreqRes;

  if (
    minFreqRes.value > defaultFreqRes.value ||
    defaultFreqRes.value > maxFreqRes.value
  ) {
    return {
      ok: false,
      error: `水泵频率须满足：最小频率 <= 默认频率 <= 最大频率（均在 ${AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.min} 至 ${AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz.max} Hz 之间）`,
    };
  }

  const minRunRes = parseIntegerInRange(
    draftPolicy.minRunSeconds,
    AUTONOMY_POLICY_BOUNDS.minRunSeconds.min,
    AUTONOMY_POLICY_BOUNDS.minRunSeconds.max,
    '最短运行保护时间',
  );
  if (!minRunRes.ok) return minRunRes;

  const minStopRes = parseIntegerInRange(
    draftPolicy.minStopSeconds,
    AUTONOMY_POLICY_BOUNDS.minStopSeconds.min,
    AUTONOMY_POLICY_BOUNDS.minStopSeconds.max,
    '最短停机保护时间',
  );
  if (!minStopRes.ok) return minStopRes;

  const snapshotRes = parseIntegerInRange(
    draftPolicy.snapshotIntervalSeconds,
    AUTONOMY_POLICY_BOUNDS.snapshotIntervalSeconds.min,
    AUTONOMY_POLICY_BOUNDS.snapshotIntervalSeconds.max,
    '全量快照周期',
  );
  if (!snapshotRes.ok) return snapshotRes;

  return {
    ok: true,
    autonomyPolicy: {
      enabled: draftPolicy.enabled,
      offlineGraceSeconds: graceRes.value,
      defaultChilledWaterSupplySetpointC: setpointRes.value,
      defaultPumpFrequencyHz: defaultFreqRes.value,
      minPumpFrequencyHz: minFreqRes.value,
      maxPumpFrequencyHz: maxFreqRes.value,
      minRunSeconds: minRunRes.value,
      minStopSeconds: minStopRes.value,
      snapshotIntervalSeconds: snapshotRes.value,
    },
  };
}

function areStringRecordsEqual(
  left: Readonly<Record<string, string>> | undefined,
  right: Readonly<Record<string, string>> | undefined,
): boolean {
  const leftKeys = left ? Object.keys(left) : [];
  const rightKeys = right ? Object.keys(right) : [];
  if (leftKeys.length !== rightKeys.length) return false;
  return leftKeys.every((key) => left?.[key] === right?.[key]);
}

function compareRegisterMappings(
  left: GatewayModbusRegisterMapping,
  right: GatewayModbusRegisterMapping,
): number {
  const byPoint = compareAscii(left.pointCode, right.pointCode);
  if (byPoint !== 0) return byPoint;
  if (left.address !== right.address) return left.address - right.address;
  if (left.functionCode !== right.functionCode) return left.functionCode - right.functionCode;
  return compareAscii(left.rawType, right.rawType);
}

function canonicalizeDriverConfigs(
  drivers: readonly GatewayModbusDriverConfig[],
): readonly GatewayModbusDriverConfig[] {
  return [...drivers]
    .map((driver) => ({
      ...driver,
      registers: [...driver.registers].sort(compareRegisterMappings),
    }))
    .sort((left, right) => compareAscii(left.deviceKey, right.deviceKey));
}

function areDriverConfigsEqual(
  left: readonly GatewayModbusDriverConfig[],
  right: readonly GatewayModbusDriverConfig[],
): boolean {
  if (left.length !== right.length) return false;
  const sortedLeft = canonicalizeDriverConfigs(left);
  const sortedRight = canonicalizeDriverConfigs(right);
  for (let i = 0; i < sortedLeft.length; i += 1) {
    const a = sortedLeft[i]!;
    const b = sortedRight[i]!;
    if (
      a.deviceKey !== b.deviceKey ||
      a.driverType !== b.driverType ||
      a.endpoint !== b.endpoint ||
      a.unitId !== b.unitId ||
      (a.capabilityProfile ?? '') !== (b.capabilityProfile ?? '') ||
      a.timeoutMs !== b.timeoutMs ||
      a.retries !== b.retries ||
      a.registers.length !== b.registers.length
    ) {
      return false;
    }
    for (let j = 0; j < a.registers.length; j += 1) {
      const ra = a.registers[j]!;
      const rb = b.registers[j]!;
      if (
        ra.pointCode !== rb.pointCode ||
        (ra.semanticChannel ?? '') !== (rb.semanticChannel ?? '') ||
        ra.functionCode !== rb.functionCode ||
        ra.writeFunctionCode !== rb.writeFunctionCode ||
        ra.address !== rb.address ||
        ra.registerCount !== rb.registerCount ||
        ra.rawType !== rb.rawType ||
        ra.byteOrder !== rb.byteOrder ||
        ra.wordOrder !== rb.wordOrder ||
        ra.bitIndex !== rb.bitIndex ||
        normalizeZero(ra.scale) !== normalizeZero(rb.scale) ||
        normalizeZero(ra.offset) !== normalizeZero(rb.offset) ||
        ra.pollPriority !== rb.pollPriority ||
        !areStringRecordsEqual(ra.enumMap, rb.enumMap)
      ) {
        return false;
      }
    }
  }
  return true;
}

function areAutonomyPoliciesEqual(
  left: GatewayAutonomyPolicy | null,
  right: GatewayAutonomyPolicy | null | undefined,
): boolean {
  const normalizedRight = right ?? null;
  if (left === null && normalizedRight === null) return true;
  if (left === null || normalizedRight === null) return false;
  return (
    left.enabled === normalizedRight.enabled &&
    left.offlineGraceSeconds === normalizedRight.offlineGraceSeconds &&
    left.defaultChilledWaterSupplySetpointC === normalizedRight.defaultChilledWaterSupplySetpointC &&
    left.defaultPumpFrequencyHz === normalizedRight.defaultPumpFrequencyHz &&
    left.minPumpFrequencyHz === normalizedRight.minPumpFrequencyHz &&
    left.maxPumpFrequencyHz === normalizedRight.maxPumpFrequencyHz &&
    left.minRunSeconds === normalizedRight.minRunSeconds &&
    left.minStopSeconds === normalizedRight.minStopSeconds &&
    left.snapshotIntervalSeconds === normalizedRight.snapshotIntervalSeconds
  );
}

export function buildValidatedShadowUpdateRequestFromDesired(
  draft: GatewayConfigShadowEditorDraft,
  expectedDesiredRevision: number,
  currentDesired: GatewayDesiredConfig | undefined,
):
  | { readonly ok: true; readonly request: GatewayConfigShadowUpdateRequest }
  | { readonly ok: false; readonly error: string } {
  const reason = draft.reason.trim();
  if (!reason) {
    return { ok: false, error: '请填写配置影子变更原因' };
  }
  if (reason.length > 1000) {
    return { ok: false, error: '配置影子变更原因不能超过 1000 个字符' };
  }

  const pointMappings: GatewayPointMapping[] = [];
  for (let index = 0; index < draft.pointMappings.length; index += 1) {
    const row = draft.pointMappings[index]!;
    const sourceChannel = row.sourceChannel.trim();
    const primaryDeviceKey = row.primaryDeviceKey.trim();
    const primaryPointCode = row.primaryPointCode.trim();
    if (!sourceChannel || sourceChannel.length > 128) {
      return { ok: false, error: `点位映射第 ${index + 1} 行的源通道标识不能为空` };
    }
    if (!primaryDeviceKey || primaryDeviceKey.length > 128) {
      return { ok: false, error: `点位映射第 ${index + 1} 行的主设备标识不能为空` };
    }
    if (!POINT_CODE_PATTERN.test(primaryPointCode)) {
      return {
        ok: false,
        error: `点位映射第 ${index + 1} 行的点位编码 "${primaryPointCode || '(空)'}" 须以小写字母开头，仅含小写字母、数字和下划线`,
      };
    }
    const copyResult = parsePointCopyTargets(row.copyTargetsText);
    if (!copyResult.ok) {
      return { ok: false, error: `点位映射第 ${index + 1} 行：${copyResult.error}` };
    }
    pointMappings.push(
      copyResult.targets.length > 0
        ? {
            sourceChannel,
            primaryDeviceKey,
            primaryPointCode,
            copyTargets: copyResult.targets,
          }
        : {
            sourceChannel,
            primaryDeviceKey,
            primaryPointCode,
          },
    );
  }

  const samplingPolicies: GatewaySamplingPolicy[] = [];
  for (let index = 0; index < draft.samplingPolicies.length; index += 1) {
    const row = draft.samplingPolicies[index]!;
    const deviceKey = row.deviceKey.trim();
    const pointCode = row.pointCode.trim();
    const sampleIntervalMs = Number(row.sampleIntervalMs);
    const publishIntervalMs = Number(row.publishIntervalMs);
    if (!deviceKey || deviceKey.length > 128) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的设备标识不能为空` };
    }
    if (pointCode.length > 128) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的点位编码不能超过 128 个字符` };
    }
    if (!Number.isInteger(sampleIntervalMs) || sampleIntervalMs < 100 || sampleIntervalMs > 86_400_000) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的采样间隔需为 100 至 86400000 毫秒的整数` };
    }
    if (!Number.isInteger(publishIntervalMs) || publishIntervalMs < 100 || publishIntervalMs > 86_400_000) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的上报间隔需为 100 至 86400000 毫秒的整数` };
    }
    if (publishIntervalMs < sampleIntervalMs) {
      return { ok: false, error: `采集周期第 ${index + 1} 行的上报间隔不能小于采样间隔` };
    }
    samplingPolicies.push(
      pointCode.length > 0
        ? { deviceKey, pointCode, sampleIntervalMs, publishIntervalMs }
        : { deviceKey, sampleIntervalMs, publishIntervalMs },
    );
  }

  const driversResult = validateDriverConfigs(draft.driverConfigs);
  if (!driversResult.ok) return driversResult;

  let validatedAutonomy: GatewayAutonomyPolicy | null = null;
  if (draft.autonomyPolicy !== null) {
    const autonomyResult = validateAutonomyPolicy(draft.autonomyPolicy);
    if (!autonomyResult.ok) return autonomyResult;
    validatedAutonomy = autonomyResult.autonomyPolicy;
  }

  const includePointMappings =
    pointMappings.length > 0 || (currentDesired?.pointMappings.length ?? 0) > 0;
  const includeSamplingPolicies =
    samplingPolicies.length > 0 || (currentDesired?.samplingPolicies.length ?? 0) > 0;
  const driverConfigsChanged = !areDriverConfigsEqual(
    driversResult.driverConfigs,
    currentDesired?.driverConfigs ?? [],
  );
  const autonomyChanged = !areAutonomyPoliciesEqual(validatedAutonomy, currentDesired?.autonomyPolicy);

  // Omit concentratorConfigs and unchanged driverConfigs/autonomyPolicy so partial PUT
  // preserves offline-staged concentrator state and avoids resending unmodified sections.
  const request: GatewayConfigShadowUpdateRequest = {
    expectedDesiredRevision,
    ...(includePointMappings ? { pointMappings } : {}),
    ...(includeSamplingPolicies ? { samplingPolicies } : {}),
    ...(driverConfigsChanged ? { driverConfigs: driversResult.driverConfigs } : {}),
    ...(autonomyChanged ? { autonomyPolicy: validatedAutonomy } : {}),
    reason,
  };

  return { ok: true, request };
}

export function buildValidatedShadowUpdateRequest(
  draft: GatewayConfigShadowEditorDraft,
  shadow: GatewayConfigShadow | undefined,
):
  | { readonly ok: true; readonly request: GatewayConfigShadowUpdateRequest }
  | { readonly ok: false; readonly error: string } {
  return buildValidatedShadowUpdateRequestFromDesired(
    draft,
    shadow?.desired?.desiredRevision ?? 0,
    shadow?.desired,
  );
}
