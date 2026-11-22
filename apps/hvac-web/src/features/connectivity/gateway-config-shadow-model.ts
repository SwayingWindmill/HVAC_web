import type {
  ConcentratorUnitShadow,
  EdgeRuleBundle,
  GatewayAutonomyPolicy,
  GatewayBACnetDriverSpec,
  GatewayBACnetObjectMapping,
  GatewayConfigShadow,
  GatewayConfigShadowUpdateRequest,
  GatewayDesiredConfig,
  GatewayModbusDriverConfig,
  GatewayModbusRegisterMapping,
  GatewayOPCUADriverSpec,
  GatewayOPCUANodeMapping,
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
export const SOUTHBOUND_DRIVER_TYPES = ['MODBUS_TCP', 'MODBUS_RTU', 'BACNET_IP', 'OPC_UA'] as const;
export const BACNET_OBJECT_TYPES = [
  'ANALOG_INPUT',
  'ANALOG_OUTPUT',
  'ANALOG_VALUE',
  'BINARY_INPUT',
  'BINARY_OUTPUT',
  'BINARY_VALUE',
  'DEVICE',
  'MULTI_STATE_INPUT',
  'MULTI_STATE_OUTPUT',
  'MULTI_STATE_VALUE',
] as const satisfies readonly GatewayBACnetObjectMapping['objectType'][];
export type BACnetObjectType = (typeof BACNET_OBJECT_TYPES)[number];
export const DRIVER_CHANNEL_DATA_TYPES = ['DOUBLE', 'BOOLEAN', 'STRING', 'INTEGER'] as const;
export type DriverChannelDataType = (typeof DRIVER_CHANNEL_DATA_TYPES)[number];
export const SAFETY_BOUNDARY_OPTIONS = ['SOFT_REALTIME', 'HARD_REALTIME_SAFETY'] as const;
export type DriverSafetyBoundary = '' | (typeof SAFETY_BOUNDARY_OPTIONS)[number];
export const OPCUA_SECURITY_POLICIES = ['None', 'Basic256Sha256'] as const;
export type OPCUASecurityPolicy = (typeof OPCUA_SECURITY_POLICIES)[number];
export const OPCUA_SECURITY_MODES = ['None', 'Sign', 'SignAndEncrypt'] as const;
export type OPCUASecurityMode = (typeof OPCUA_SECURITY_MODES)[number];
export const OPCUA_AUTH_MODES = ['ANONYMOUS', 'CERTIFICATE', 'USERNAME'] as const;
export type OPCUAAuthMode = (typeof OPCUA_AUTH_MODES)[number];
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

export const SOUTHBOUND_DRIVER_TYPE_LABELS: Readonly<
  Record<GatewayModbusDriverConfig['driverType'], string>
> = {
  MODBUS_TCP: 'Modbus TCP',
  MODBUS_RTU: 'Modbus RTU',
  BACNET_IP: 'BACnet/IP',
  OPC_UA: 'OPC-UA',
};

export const MODBUS_DRIVER_TYPE_LABELS = SOUTHBOUND_DRIVER_TYPE_LABELS;

export const BACNET_OBJECT_TYPE_LABELS: Readonly<Record<BACnetObjectType, string>> = {
  ANALOG_INPUT: '模拟输入 (AI)',
  ANALOG_OUTPUT: '模拟输出 (AO)',
  ANALOG_VALUE: '模拟值 (AV)',
  BINARY_INPUT: '开关输入 (BI)',
  BINARY_OUTPUT: '开关输出 (BO)',
  BINARY_VALUE: '开关值 (BV)',
  DEVICE: '设备对象 (DEV)',
  MULTI_STATE_INPUT: '多态输入 (MI)',
  MULTI_STATE_OUTPUT: '多态输出 (MO)',
  MULTI_STATE_VALUE: '多态值 (MV)',
};

export function isBACnetInputOnlyObjectType(objectType: BACnetObjectType): boolean {
  return (
    objectType === 'ANALOG_INPUT' ||
    objectType === 'BINARY_INPUT' ||
    objectType === 'MULTI_STATE_INPUT' ||
    objectType === 'DEVICE'
  );
}

export const DRIVER_CHANNEL_DATA_TYPE_LABELS: Readonly<Record<DriverChannelDataType, string>> = {
  DOUBLE: '浮点数 (DOUBLE)',
  BOOLEAN: '布尔 (BOOLEAN)',
  STRING: '字符串 (STRING)',
  INTEGER: '整数 (INTEGER)',
};

export const SAFETY_BOUNDARY_LABELS: Readonly<
  Record<(typeof SAFETY_BOUNDARY_OPTIONS)[number], string>
> = {
  SOFT_REALTIME: '软实时可控点位',
  HARD_REALTIME_SAFETY: '硬实时安全保护点（只读）',
};

export const OPCUA_SECURITY_POLICY_LABELS: Readonly<Record<OPCUASecurityPolicy, string>> = {
  None: '无加密 (None)',
  Basic256Sha256: 'Basic256Sha256',
};

export const OPCUA_SECURITY_MODE_LABELS: Readonly<Record<OPCUASecurityMode, string>> = {
  None: '无签名/加密 (None)',
  Sign: '仅签名 (Sign)',
  SignAndEncrypt: '签名并加密 (SignAndEncrypt)',
};

export const OPCUA_AUTH_MODE_LABELS: Readonly<Record<OPCUAAuthMode, string>> = {
  ANONYMOUS: '匿名认证 (ANONYMOUS)',
  CERTIFICATE: '证书认证 (CERTIFICATE)',
  USERNAME: '用户名与凭据引用 (USERNAME)',
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

export interface GatewayBACnetObjectDraft {
  readonly draftId: string;
  readonly pointCode: string;
  readonly semanticChannel: string;
  readonly safetyBoundary: DriverSafetyBoundary;
  readonly objectType: BACnetObjectType;
  readonly objectInstance: string;
  readonly propertyId: string;
  readonly dataType: '' | DriverChannelDataType;
  readonly writable: boolean;
  readonly covSubscribe: boolean;
  readonly scale: string;
  readonly offset: string;
  readonly pollPriority: GatewayBACnetObjectMapping['pollPriority'];
  readonly unit: string;
  readonly enumMapText: string;
}

export interface GatewayBACnetSpecDraft {
  readonly deviceInstance: string;
  readonly networkNumber: string;
  readonly covLifetimeSeconds: string;
  readonly writePriority: string;
  readonly objects: readonly GatewayBACnetObjectDraft[];
}

export interface GatewayOPCUANodeDraft {
  readonly draftId: string;
  readonly pointCode: string;
  readonly semanticChannel: string;
  readonly safetyBoundary: DriverSafetyBoundary;
  readonly nodeId: string;
  readonly namespaceIndex: string;
  readonly dataType: '' | DriverChannelDataType;
  readonly writable: boolean;
  readonly subscribe: boolean;
  readonly scale: string;
  readonly offset: string;
  readonly pollPriority: GatewayOPCUANodeMapping['pollPriority'];
  readonly unit: string;
}

export interface GatewayOPCUASpecDraft {
  readonly securityPolicy: OPCUASecurityPolicy;
  readonly securityMode: OPCUASecurityMode;
  readonly authMode: OPCUAAuthMode;
  readonly username: string;
  readonly credentialRef: string;
  readonly subscriptionIntervalMs: string;
  readonly nodes: readonly GatewayOPCUANodeDraft[];
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
  readonly bacnet: GatewayBACnetSpecDraft;
  readonly opcua: GatewayOPCUASpecDraft;
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

export function createEmptyBACnetObjectDraft(): GatewayBACnetObjectDraft {
  return {
    draftId: nextDraftId('bacobj'),
    pointCode: '',
    semanticChannel: '',
    safetyBoundary: '',
    objectType: 'ANALOG_INPUT',
    objectInstance: '1',
    propertyId: '85',
    dataType: '',
    writable: false,
    covSubscribe: false,
    scale: '1',
    offset: '0',
    pollPriority: 'MEDIUM',
    unit: '',
    enumMapText: '',
  };
}

export function createEmptyBACnetSpecDraft(): GatewayBACnetSpecDraft {
  return {
    deviceInstance: '1001',
    networkNumber: '',
    covLifetimeSeconds: '',
    writePriority: '8',
    objects: [createEmptyBACnetObjectDraft()],
  };
}

export function createEmptyOPCUANodeDraft(): GatewayOPCUANodeDraft {
  return {
    draftId: nextDraftId('uanode'),
    pointCode: '',
    semanticChannel: '',
    safetyBoundary: '',
    nodeId: '',
    namespaceIndex: '',
    dataType: '',
    writable: false,
    subscribe: false,
    scale: '1',
    offset: '0',
    pollPriority: 'MEDIUM',
    unit: '',
  };
}

export function createEmptyOPCUASpecDraft(): GatewayOPCUASpecDraft {
  return {
    securityPolicy: 'None',
    securityMode: 'None',
    authMode: 'ANONYMOUS',
    username: '',
    credentialRef: '',
    subscriptionIntervalMs: '1000',
    nodes: [createEmptyOPCUANodeDraft()],
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
    bacnet: createEmptyBACnetSpecDraft(),
    opcua: createEmptyOPCUASpecDraft(),
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

export interface DriverPointCountSummary {
  readonly driverCount: number;
  readonly modbusRegisterCount: number;
  readonly bacnetObjectCount: number;
  readonly opcuaNodeCount: number;
  readonly totalPointCount: number;
}

export function summarizeDriverPointCounts(
  driverConfigs: readonly GatewayModbusDriverConfig[] | undefined,
): DriverPointCountSummary {
  if (!driverConfigs || driverConfigs.length === 0) {
    return {
      driverCount: 0,
      modbusRegisterCount: 0,
      bacnetObjectCount: 0,
      opcuaNodeCount: 0,
      totalPointCount: 0,
    };
  }
  let modbusRegisterCount = 0;
  let bacnetObjectCount = 0;
  let opcuaNodeCount = 0;
  for (const driver of driverConfigs) {
    modbusRegisterCount += driver.registers?.length ?? 0;
    bacnetObjectCount += driver.bacnet?.objects.length ?? 0;
    opcuaNodeCount += driver.opcua?.nodes.length ?? 0;
  }
  return {
    driverCount: driverConfigs.length,
    modbusRegisterCount,
    bacnetObjectCount,
    opcuaNodeCount,
    totalPointCount: modbusRegisterCount + bacnetObjectCount + opcuaNodeCount,
  };
}

export function countDriverRegisters(
  driverConfigs: readonly GatewayModbusDriverConfig[] | undefined,
): number {
  return summarizeDriverPointCounts(driverConfigs).totalPointCount;
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

function isValidHostPortEndpoint(endpoint: string): boolean {
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

function isValidOpcuaEndpoint(endpoint: string): boolean {
  if (!endpoint || endpoint.length > 256 || /\s/.test(endpoint)) {
    return false;
  }
  let parsed: URL;
  try {
    parsed = new URL(endpoint);
  } catch {
    return false;
  }
  if (parsed.protocol.toLowerCase() !== 'opc.tcp:') {
    return false;
  }
  return isValidHostPortEndpoint(parsed.host);
}

function splitIdentifierTokens(raw: string): string[] {
  return raw
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1_$2')
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((token) => token.length > 0);
}

function hasAdjacentTokenPair(tokens: readonly string[], first: string, second: string): boolean {
  for (let i = 0; i + 1 < tokens.length; i += 1) {
    if (tokens[i] === first && tokens[i + 1] === second) return true;
  }
  return false;
}

function hasAnyToken(tokens: readonly string[], candidates: readonly string[]): boolean {
  return candidates.some((candidate) => tokens.includes(candidate));
}

export function isHardRealtimeProtectionPoint(
  pointCode: string,
  semanticChannel: string,
  safetyBoundary: DriverSafetyBoundary | string | undefined,
): boolean {
  if (safetyBoundary === 'HARD_REALTIME_SAFETY') return true;
  const tokens = [
    ...splitIdentifierTokens(pointCode),
    ...splitIdentifierTokens(semanticChannel),
  ];
  if (tokens.length === 0) return false;
  if (
    tokens.includes('estop') ||
    hasAdjacentTokenPair(tokens, 'e', 'stop') ||
    (tokens.includes('plc') && hasAnyToken(tokens, ['emergency', 'estop', 'shutdown']))
  ) {
    return true;
  }
  if (
    tokens.includes('fire') &&
    hasAnyToken(tokens, [
      'interlock', 'trip', 'alarm', 'bypass', 'override', 'mask', 'cutout', 'relay',
      'shutoff', 'shutdown', 'linkage', 'damper', 'safety', 'protect', 'protection',
      'panel', 'signal', 'link', 'stop',
    ])
  ) {
    return true;
  }
  if (
    hasAdjacentTokenPair(tokens, 'safety', 'trip') ||
    hasAdjacentTokenPair(tokens, 'hard', 'trip') ||
    hasAdjacentTokenPair(tokens, 'trip', 'bypass') ||
    hasAdjacentTokenPair(tokens, 'trip', 'override') ||
    hasAdjacentTokenPair(tokens, 'trip', 'mask')
  ) {
    return true;
  }
  const hasPressureAnchor =
    hasAdjacentTokenPair(tokens, 'high', 'pressure') ||
    tokens.includes('hp') ||
    hasAdjacentTokenPair(tokens, 'discharge', 'pressure') ||
    hasAdjacentTokenPair(tokens, 'relief', 'valve') ||
    tokens.includes('breaker');
  if (
    hasPressureAnchor &&
    hasAnyToken(tokens, ['trip', 'cutout', 'interlock', 'switch', 'bypass', 'override', 'mask', 'protect', 'protection'])
  ) {
    return true;
  }
  const hasFreezeAnchor =
    tokens.includes('freezestat') ||
    ((hasAnyToken(tokens, ['freeze', 'antifreeze', 'frost']) ||
      hasAdjacentTokenPair(tokens, 'anti', 'freeze') ||
      (hasAdjacentTokenPair(tokens, 'low', 'temp') && tokens.includes('cutout'))) &&
      hasAnyToken(tokens, ['protect', 'protection', 'stat', 'trip', 'interlock', 'cutout', 'bypass', 'override', 'mask', 'switch', 'alarm', 'hard']));
  if (hasFreezeAnchor) return true;
  const hardwarePairs: ReadonlyArray<readonly [string, string]> = [
    ['hard', 'realtime'],
    ['plc', 'safety'],
    ['plc', 'protect'],
    ['plc', 'protection'],
    ['plc', 'interlock'],
    ['safety', 'plc'],
    ['safety', 'relay'],
    ['safety', 'circuit'],
    ['safety', 'bypass'],
    ['safety', 'override'],
    ['safety', 'mask'],
    ['hard', 'interlock'],
    ['hard', 'safety'],
    ['hardware', 'interlock'],
    ['hardware', 'protection'],
    ['protection', 'bypass'],
    ['protection', 'override'],
    ['protection', 'mask'],
  ];
  return hardwarePairs.some(([first, second]) => hasAdjacentTokenPair(tokens, first, second));
}

const OPCUA_GUID_PATTERN = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

export function parseOPCUANodeId(
  raw: string,
): { readonly ok: true; readonly namespaceIndex: number; readonly canonical: string } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 512 || /[\u0000-\u001f\u007f]/.test(trimmed)) {
    return { ok: false, error: 'OPC-UA 节点 ID 不能为空、不超过 512 个字符且不能含控制字符' };
  }
  let namespaceIndex = 0;
  let rest = trimmed;
  if (rest.startsWith('ns=')) {
    const semi = rest.indexOf(';');
    if (semi <= 3 || semi === rest.length - 1) {
      return { ok: false, error: 'OPC-UA 节点 ID 命名空间前缀须为 ns=<整数>;' };
    }
    const nsRaw = rest.slice(3, semi);
    if (!/^\d+$/.test(nsRaw)) {
      return { ok: false, error: `无效的命名空间索引 "${nsRaw}"` };
    }
    namespaceIndex = Number(nsRaw);
    if (!Number.isInteger(namespaceIndex) || namespaceIndex < 0 || namespaceIndex > 65535) {
      return { ok: false, error: 'OPC-UA 命名空间索引须为 0 至 65535 的整数' };
    }
    rest = rest.slice(semi + 1);
  }
  if (rest.length < 3 || rest[1] !== '=') {
    return { ok: false, error: `无效的 OPC-UA 节点 ID "${raw}"（示例：ns=2;s=Chiller.SupplyTemp）` };
  }
  const kind = rest[0]!;
  const body = rest.slice(2);
  switch (kind) {
    case 'i': {
      if (!/^(?:0|[1-9]\d*)$/.test(body)) {
        return { ok: false, error: `数字型 OPC-UA 节点 ID "${raw}" 须为无前导零的非负整数` };
      }
      const num = Number(body);
      if (!Number.isInteger(num) || num > 4_294_967_295) {
        return { ok: false, error: `数字型 OPC-UA 节点 ID "${raw}" 超出 32 位无符号整数范围` };
      }
      return { ok: true, namespaceIndex, canonical: `ns=${namespaceIndex};i=${num}` };
    }
    case 's': {
      if (!body || [...body].length > 256) {
        return { ok: false, error: '字符串型 OPC-UA 节点 ID 内容须为 1 至 256 个字符' };
      }
      return { ok: true, namespaceIndex, canonical: `ns=${namespaceIndex};s=${body}` };
    }
    case 'g': {
      if (!OPCUA_GUID_PATTERN.test(body)) {
        return { ok: false, error: `GUID 型 OPC-UA 节点 ID "${raw}" 须为标准 UUID 格式` };
      }
      return { ok: true, namespaceIndex, canonical: `ns=${namespaceIndex};g=${body.toLowerCase()}` };
    }
    case 'b': {
      try {
        const binary = atob(body);
        if (binary.length === 0 || binary.length > 256) {
          return { ok: false, error: '字节流 OPC-UA 节点 ID 解码后须为 1 至 256 字节' };
        }
        return { ok: true, namespaceIndex, canonical: `ns=${namespaceIndex};b=${btoa(binary)}` };
      } catch {
        return { ok: false, error: `字节流 OPC-UA 节点 ID "${raw}" 须为有效 Base64 编码` };
      }
    }
    default:
      return { ok: false, error: `不支持的 OPC-UA 节点标识前缀 "${kind}="（支持 i=/s=/g=/b=）` };
  }
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
  const driverConfigs = (desired?.driverConfigs ?? []).map((driver): GatewayModbusDriverDraft => {
    const registers =
      driver.registers && driver.registers.length > 0
        ? driver.registers.map((reg) => ({
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
          }))
        : [createEmptyModbusRegisterDraft()];
    const bacnet: GatewayBACnetSpecDraft = driver.bacnet
      ? {
          deviceInstance: String(driver.bacnet.deviceInstance),
          networkNumber:
            driver.bacnet.networkNumber !== undefined && driver.bacnet.networkNumber > 0
              ? String(driver.bacnet.networkNumber)
              : '',
          covLifetimeSeconds:
            driver.bacnet.covLifetimeSeconds !== undefined && driver.bacnet.covLifetimeSeconds > 0
              ? String(driver.bacnet.covLifetimeSeconds)
              : '',
          writePriority:
            driver.bacnet.writePriority !== undefined ? String(driver.bacnet.writePriority) : '8',
          objects:
            driver.bacnet.objects.length > 0
              ? driver.bacnet.objects.map((obj) => ({
                  draftId: nextDraftId('bacobj'),
                  pointCode: obj.pointCode,
                  semanticChannel: obj.semanticChannel ?? '',
                  safetyBoundary: obj.safetyBoundary ?? '',
                  objectType: obj.objectType,
                  objectInstance: String(obj.objectInstance),
                  propertyId: obj.propertyId !== undefined ? String(obj.propertyId) : '85',
                  dataType: obj.dataType ?? '',
                  writable: Boolean(obj.writable),
                  covSubscribe: Boolean(obj.covSubscribe),
                  scale: obj.scale !== undefined && obj.scale !== 0 ? String(obj.scale) : '1',
                  offset: obj.offset !== undefined ? String(obj.offset) : '0',
                  pollPriority: obj.pollPriority,
                  unit: obj.unit ?? '',
                  enumMapText: formatModbusEnumMap(obj.enumMap),
                }))
              : [createEmptyBACnetObjectDraft()],
        }
      : createEmptyBACnetSpecDraft();
    const opcua: GatewayOPCUASpecDraft = driver.opcua
      ? {
          securityPolicy: driver.opcua.securityPolicy,
          securityMode: driver.opcua.securityMode,
          authMode: driver.opcua.authMode ?? 'ANONYMOUS',
          username: driver.opcua.username ?? '',
          credentialRef: driver.opcua.credentialRef ?? '',
          subscriptionIntervalMs:
            driver.opcua.subscriptionIntervalMs !== undefined
              ? String(driver.opcua.subscriptionIntervalMs)
              : '1000',
          nodes:
            driver.opcua.nodes.length > 0
              ? driver.opcua.nodes.map((node) => ({
                  draftId: nextDraftId('uanode'),
                  pointCode: node.pointCode,
                  semanticChannel: node.semanticChannel ?? '',
                  safetyBoundary: node.safetyBoundary ?? '',
                  nodeId: node.nodeId,
                  namespaceIndex:
                    node.namespaceIndex !== undefined ? String(node.namespaceIndex) : '',
                  dataType: node.dataType ?? '',
                  writable: Boolean(node.writable),
                  subscribe: Boolean(node.subscribe),
                  scale: node.scale !== undefined && node.scale !== 0 ? String(node.scale) : '1',
                  offset: node.offset !== undefined ? String(node.offset) : '0',
                  pollPriority: node.pollPriority,
                  unit: node.unit ?? '',
                }))
              : [createEmptyOPCUANodeDraft()],
        }
      : createEmptyOPCUASpecDraft();

    return {
      draftId: nextDraftId('drv'),
      deviceKey: driver.deviceKey,
      driverType: driver.driverType,
      endpoint: driver.endpoint,
      unitId: driver.unitId !== undefined ? String(driver.unitId) : '1',
      capabilityProfile: driver.capabilityProfile ?? '',
      timeoutMs: String(driver.timeoutMs),
      retries: String(driver.retries),
      registers,
      bacnet,
      opcua,
    };
  });
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

function validateCommonPointMeta(
  rowPrefix: string,
  driverPrefix: string,
  rawPointCode: string,
  rawSemantic: string,
  rawSafetyBoundary: DriverSafetyBoundary,
  rawUnit: string,
  writable: boolean,
  seenPointCodes: Set<string>,
  seenSemantics: Set<string>,
):
  | {
      readonly ok: true;
      readonly pointCode: string;
      readonly semanticChannel: string;
      readonly safetyBoundary: '' | 'SOFT_REALTIME' | 'HARD_REALTIME_SAFETY';
      readonly unit: string;
    }
  | { readonly ok: false; readonly error: string } {
  const pointCode = rawPointCode.trim();
  const semanticChannel = rawSemantic.trim();
  const unit = rawUnit.trim();

  if (!POINT_CODE_PATTERN.test(pointCode)) {
    return {
      ok: false,
      error: `${rowPrefix}的点位编码 "${pointCode || '(空)'}" 须以小写字母开头，仅含小写字母、数字和下划线`,
    };
  }
  if (seenPointCodes.has(pointCode)) {
    return { ok: false, error: `${driverPrefix}中点位编码 "${pointCode}" 重复` };
  }
  seenPointCodes.add(pointCode);

  if (semanticChannel.length > 64 || semanticChannel.includes('/')) {
    return { ok: false, error: `${rowPrefix}的语义通道不能超过 64 个字符且不能包含 "/"` };
  }
  if (semanticChannel) {
    if (seenSemantics.has(semanticChannel)) {
      return { ok: false, error: `${driverPrefix}中语义通道 "${semanticChannel}" 重复` };
    }
    seenSemantics.add(semanticChannel);
  }

  let safetyBoundary: '' | 'SOFT_REALTIME' | 'HARD_REALTIME_SAFETY' = '';
  if (rawSafetyBoundary) {
    if (!isOneOf(SAFETY_BOUNDARY_OPTIONS, rawSafetyBoundary)) {
      return { ok: false, error: `${rowPrefix}的安全边界类型无效` };
    }
    safetyBoundary = rawSafetyBoundary;
  }

  if (writable && isHardRealtimeProtectionPoint(pointCode, semanticChannel, safetyBoundary)) {
    return {
      ok: false,
      error: `${rowPrefix}（${pointCode}）属于 EDGE-003 硬实时安全保护点位，禁止配置为可写`,
    };
  }

  if (unit.length > 32) {
    return { ok: false, error: `${rowPrefix}的单位不能超过 32 个字符` };
  }

  return { ok: true, pointCode, semanticChannel, safetyBoundary, unit };
}

function validateModbusDriverEntry(
  prefix: string,
  deviceKey: string,
  driverType: 'MODBUS_TCP' | 'MODBUS_RTU',
  endpoint: string,
  capabilityProfile: ModbusCapabilityProfile | undefined,
  timeoutMs: number,
  retries: number,
  driver: GatewayModbusDriverDraft,
):
  | { readonly ok: true; readonly config: GatewayModbusDriverConfig }
  | { readonly ok: false; readonly error: string } {
  if (driverType === 'MODBUS_TCP' && !isValidHostPortEndpoint(endpoint)) {
    return { ok: false, error: `${prefix}的采集地址须为 host:port 格式且端口在 1 至 65535 之间` };
  }

  const unitIdRes = parseIntegerInRange(driver.unitId, 1, 247, `${prefix}的从站地址 (UID)`);
  if (!unitIdRes.ok) return unitIdRes;

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

  return {
    ok: true,
    config: {
      deviceKey,
      driverType,
      endpoint,
      unitId: unitIdRes.value,
      ...(capabilityProfile ? { capabilityProfile } : {}),
      timeoutMs,
      retries,
      registers,
    },
  };
}

function defaultBACnetDataType(objectType: BACnetObjectType): DriverChannelDataType {
  if (objectType === 'BINARY_INPUT' || objectType === 'BINARY_OUTPUT' || objectType === 'BINARY_VALUE') {
    return 'BOOLEAN';
  }
  if (objectType === 'MULTI_STATE_INPUT' || objectType === 'MULTI_STATE_OUTPUT' || objectType === 'MULTI_STATE_VALUE') {
    return 'INTEGER';
  }
  return 'DOUBLE';
}

function validateBACnetDriverEntry(
  prefix: string,
  deviceKey: string,
  endpoint: string,
  capabilityProfile: ModbusCapabilityProfile | undefined,
  timeoutMs: number,
  retries: number,
  spec: GatewayBACnetSpecDraft,
):
  | { readonly ok: true; readonly config: GatewayModbusDriverConfig }
  | { readonly ok: false; readonly error: string } {
  if (!isValidHostPortEndpoint(endpoint)) {
    return { ok: false, error: `${prefix}的 BACnet/IP 地址须为 host:port 格式且端口在 1 至 65535 之间` };
  }

  const deviceInstanceRes = parseIntegerInRange(
    spec.deviceInstance,
    0,
    4_194_302,
    `${prefix}的 BACnet 设备实例号`,
  );
  if (!deviceInstanceRes.ok) return deviceInstanceRes;

  let networkNumber: number | undefined;
  if (spec.networkNumber.trim() !== '') {
    const netRes = parseIntegerInRange(spec.networkNumber, 0, 65534, `${prefix}的 BACnet 网络号`);
    if (!netRes.ok) return netRes;
    if (netRes.value > 0) networkNumber = netRes.value;
  }

  let covLifetimeSeconds: number | undefined;
  if (spec.covLifetimeSeconds.trim() !== '') {
    const covRes = parseIntegerInRange(
      spec.covLifetimeSeconds,
      0,
      86400,
      `${prefix}的 COV 订阅时长`,
    );
    if (!covRes.ok) return covRes;
    if (covRes.value > 0) covLifetimeSeconds = covRes.value;
  }

  let writePriority = 8;
  if (spec.writePriority.trim() !== '') {
    const prioRes = parseIntegerInRange(spec.writePriority, 1, 16, `${prefix}的写优先级`);
    if (!prioRes.ok) return prioRes;
    writePriority = prioRes.value;
  }

  if (spec.objects.length === 0 || spec.objects.length > 256) {
    return { ok: false, error: `${prefix}至少需要配置 1 个 BACnet 对象，最多 256 个` };
  }

  const seenPointCodes = new Set<string>();
  const seenSemantics = new Set<string>();
  const seenObjects = new Set<string>();
  const objects: GatewayBACnetObjectMapping[] = [];

  for (let oIndex = 0; oIndex < spec.objects.length; oIndex += 1) {
    const obj = spec.objects[oIndex]!;
    const rowPrefix = `${prefix}第 ${oIndex + 1} 个 BACnet 对象`;
    const meta = validateCommonPointMeta(
      rowPrefix,
      prefix,
      obj.pointCode,
      obj.semanticChannel,
      obj.safetyBoundary,
      obj.unit,
      obj.writable,
      seenPointCodes,
      seenSemantics,
    );
    if (!meta.ok) return meta;

    if (!isOneOf(BACNET_OBJECT_TYPES, obj.objectType)) {
      return { ok: false, error: `${rowPrefix}的对象类型无效` };
    }
    const instRes = parseIntegerInRange(obj.objectInstance, 0, 4_194_302, `${rowPrefix}的对象实例号`);
    if (!instRes.ok) return instRes;

    let propertyId = 85;
    if (obj.propertyId.trim() !== '') {
      const propRes = parseIntegerInRange(obj.propertyId, 0, 4_194_303, `${rowPrefix}的属性 ID`);
      if (!propRes.ok) return propRes;
      propertyId = propRes.value;
    }

    if (obj.writable) {
      if (isBACnetInputOnlyObjectType(obj.objectType)) {
        return {
          ok: false,
          error: `${rowPrefix}（${BACNET_OBJECT_TYPE_LABELS[obj.objectType]}）为只读对象，不能勾选可写`,
        };
      }
      if (propertyId !== 85) {
        return {
          ok: false,
          error: `${rowPrefix}配置可写时，属性 ID 须为 85（Present_Value）`,
        };
      }
    }

    const objKey = `${obj.objectType}:${instRes.value}:${propertyId}`;
    if (seenObjects.has(objKey)) {
      return {
        ok: false,
        error: `${prefix}中存在重复的 BACnet 对象映射（${obj.objectType}:${instRes.value} 属性 ${propertyId}）`,
      };
    }
    seenObjects.add(objKey);

    if (!isOneOf(MODBUS_POLL_PRIORITIES, obj.pollPriority)) {
      return { ok: false, error: `${rowPrefix}的轮询优先级无效` };
    }

    let enumMap: Record<string, string> | undefined;
    if (obj.enumMapText.trim() !== '') {
      const enumRes = parseModbusEnumMap(obj.enumMapText);
      if (!enumRes.ok) {
        return { ok: false, error: `${rowPrefix}：${enumRes.error}` };
      }
      enumMap = enumRes.enumMap;
    }

    let dataType: DriverChannelDataType = enumMap ? 'STRING' : defaultBACnetDataType(obj.objectType);
    if (obj.dataType) {
      if (!isOneOf(DRIVER_CHANNEL_DATA_TYPES, obj.dataType)) {
        return { ok: false, error: `${rowPrefix}的数据类型无效` };
      }
      dataType = obj.dataType;
    }

    const scaleRes = parseFiniteNumber(obj.scale, `${rowPrefix}的倍率`);
    if (!scaleRes.ok) return scaleRes;
    if (scaleRes.value === 0) {
      return { ok: false, error: `${rowPrefix}的倍率不能为 0` };
    }

    const offsetRes = parseFiniteNumber(obj.offset, `${rowPrefix}的偏置`);
    if (!offsetRes.ok) return offsetRes;

    objects.push({
      pointCode: meta.pointCode,
      ...(meta.semanticChannel ? { semanticChannel: meta.semanticChannel } : {}),
      ...(meta.safetyBoundary ? { safetyBoundary: meta.safetyBoundary } : {}),
      objectType: obj.objectType,
      objectInstance: instRes.value,
      propertyId,
      dataType,
      ...(obj.writable ? { writable: true } : {}),
      ...(obj.covSubscribe ? { covSubscribe: true } : {}),
      scale: normalizeZero(scaleRes.value),
      offset: normalizeZero(offsetRes.value),
      pollPriority: obj.pollPriority,
      ...(meta.unit ? { unit: meta.unit } : {}),
      ...(enumMap ? { enumMap } : {}),
    });
  }

  const bacnetSpec: GatewayBACnetDriverSpec = {
    deviceInstance: deviceInstanceRes.value,
    ...(networkNumber !== undefined ? { networkNumber } : {}),
    ...(covLifetimeSeconds !== undefined ? { covLifetimeSeconds } : {}),
    writePriority,
    objects,
  };

  return {
    ok: true,
    config: {
      deviceKey,
      driverType: 'BACNET_IP',
      endpoint,
      ...(capabilityProfile ? { capabilityProfile } : {}),
      timeoutMs,
      retries,
      bacnet: bacnetSpec,
    },
  };
}

function validateOPCUADriverEntry(
  prefix: string,
  deviceKey: string,
  endpoint: string,
  capabilityProfile: ModbusCapabilityProfile | undefined,
  timeoutMs: number,
  retries: number,
  spec: GatewayOPCUASpecDraft,
):
  | { readonly ok: true; readonly config: GatewayModbusDriverConfig }
  | { readonly ok: false; readonly error: string } {
  if (!isValidOpcuaEndpoint(endpoint)) {
    return {
      ok: false,
      error: `${prefix}的 OPC-UA 地址须以 opc.tcp:// 开头并包含 host:port（端口 1 至 65535）`,
    };
  }
  if (!isOneOf(OPCUA_SECURITY_POLICIES, spec.securityPolicy)) {
    return { ok: false, error: `${prefix}的 OPC-UA 安全策略无效` };
  }
  if (!isOneOf(OPCUA_SECURITY_MODES, spec.securityMode)) {
    return { ok: false, error: `${prefix}的 OPC-UA 安全模式无效` };
  }
  if (spec.securityPolicy === 'None' && spec.securityMode !== 'None') {
    return { ok: false, error: `${prefix}安全策略为 None 时，安全模式必须为 None` };
  }
  if (spec.securityPolicy === 'Basic256Sha256' && spec.securityMode === 'None') {
    return { ok: false, error: `${prefix}安全策略为 Basic256Sha256 时，安全模式须为 Sign 或 SignAndEncrypt` };
  }

  if (!isOneOf(OPCUA_AUTH_MODES, spec.authMode)) {
    return { ok: false, error: `${prefix}的 OPC-UA 认证方式无效` };
  }
  const username = spec.username.trim();
  const credentialRef = spec.credentialRef.trim();
  if (spec.authMode === 'ANONYMOUS' && username) {
    return { ok: false, error: `${prefix}选择匿名认证时不能填写用户名` };
  }
  if (spec.authMode === 'USERNAME' && (!username || !credentialRef || username.length > 128 || credentialRef.length > 128)) {
    return {
      ok: false,
      error: `${prefix}选择用户名认证时，须同时填写用户名和凭据引用（均不超过 128 个字符）`,
    };
  }

  let subscriptionIntervalMs = 1000;
  if (spec.subscriptionIntervalMs.trim() !== '') {
    const subRes = parseIntegerInRange(
      spec.subscriptionIntervalMs,
      50,
      60000,
      `${prefix}的订阅间隔`,
    );
    if (!subRes.ok) return subRes;
    subscriptionIntervalMs = subRes.value;
  }

  if (spec.nodes.length === 0 || spec.nodes.length > 256) {
    return { ok: false, error: `${prefix}至少需要配置 1 个 OPC-UA 节点，最多 256 个` };
  }

  const seenPointCodes = new Set<string>();
  const seenSemantics = new Set<string>();
  const seenNodeIds = new Set<string>();
  const nodes: GatewayOPCUANodeMapping[] = [];

  for (let nIndex = 0; nIndex < spec.nodes.length; nIndex += 1) {
    const node = spec.nodes[nIndex]!;
    const rowPrefix = `${prefix}第 ${nIndex + 1} 个 OPC-UA 节点`;
    const meta = validateCommonPointMeta(
      rowPrefix,
      prefix,
      node.pointCode,
      node.semanticChannel,
      node.safetyBoundary,
      node.unit,
      node.writable,
      seenPointCodes,
      seenSemantics,
    );
    if (!meta.ok) return meta;

    const nodeIdRes = parseOPCUANodeId(node.nodeId);
    if (!nodeIdRes.ok) {
      return { ok: false, error: `${rowPrefix}：${nodeIdRes.error}` };
    }
    if (node.namespaceIndex.trim() !== '') {
      const nsRes = parseIntegerInRange(
        node.namespaceIndex,
        0,
        65535,
        `${rowPrefix}的命名空间索引`,
      );
      if (!nsRes.ok) return nsRes;
      if (nsRes.value !== nodeIdRes.namespaceIndex) {
        return {
          ok: false,
          error: `${rowPrefix}的命名空间索引 (${nsRes.value}) 与节点 ID (${nodeIdRes.namespaceIndex}) 不一致`,
        };
      }
    }
    if (seenNodeIds.has(nodeIdRes.canonical)) {
      return { ok: false, error: `${prefix}中 OPC-UA 节点 ID "${nodeIdRes.canonical}" 重复` };
    }
    seenNodeIds.add(nodeIdRes.canonical);

    if (!isOneOf(MODBUS_POLL_PRIORITIES, node.pollPriority)) {
      return { ok: false, error: `${rowPrefix}的轮询优先级无效` };
    }

    let dataType: DriverChannelDataType = 'DOUBLE';
    if (node.dataType) {
      if (!isOneOf(DRIVER_CHANNEL_DATA_TYPES, node.dataType)) {
        return { ok: false, error: `${rowPrefix}的数据类型无效` };
      }
      dataType = node.dataType;
    }

    const scaleRes = parseFiniteNumber(node.scale, `${rowPrefix}的倍率`);
    if (!scaleRes.ok) return scaleRes;
    if (scaleRes.value === 0) {
      return { ok: false, error: `${rowPrefix}的倍率不能为 0` };
    }

    const offsetRes = parseFiniteNumber(node.offset, `${rowPrefix}的偏置`);
    if (!offsetRes.ok) return offsetRes;

    nodes.push({
      pointCode: meta.pointCode,
      ...(meta.semanticChannel ? { semanticChannel: meta.semanticChannel } : {}),
      ...(meta.safetyBoundary ? { safetyBoundary: meta.safetyBoundary } : {}),
      nodeId: nodeIdRes.canonical,
      namespaceIndex: nodeIdRes.namespaceIndex,
      dataType,
      ...(node.writable ? { writable: true } : {}),
      ...(node.subscribe ? { subscribe: true } : {}),
      scale: normalizeZero(scaleRes.value),
      offset: normalizeZero(offsetRes.value),
      pollPriority: node.pollPriority,
      ...(meta.unit ? { unit: meta.unit } : {}),
    });
  }

  const opcuaSpec: GatewayOPCUADriverSpec = {
    securityPolicy: spec.securityPolicy,
    securityMode: spec.securityMode,
    authMode: spec.authMode,
    ...(username ? { username } : {}),
    ...(credentialRef ? { credentialRef } : {}),
    subscriptionIntervalMs,
    nodes,
  };

  return {
    ok: true,
    config: {
      deviceKey,
      driverType: 'OPC_UA',
      endpoint,
      ...(capabilityProfile ? { capabilityProfile } : {}),
      timeoutMs,
      retries,
      opcua: opcuaSpec,
    },
  };
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

    if (!isOneOf(SOUTHBOUND_DRIVER_TYPES, driver.driverType)) {
      return { ok: false, error: `${prefix}的协议类型无效` };
    }
    if (!endpoint || endpoint.length > 256) {
      return { ok: false, error: `${prefix}的采集地址不能为空且不超过 256 个字符` };
    }

    let capabilityProfile: ModbusCapabilityProfile | undefined;
    if (rawProfile) {
      if (!isOneOf(MODBUS_CAPABILITY_PROFILES, rawProfile)) {
        return { ok: false, error: `${prefix}的能力档位无效` };
      }
      capabilityProfile = rawProfile;
    }

    const timeoutRes = parseIntegerInRange(driver.timeoutMs, 100, 30000, `${prefix}的超时时间`);
    if (!timeoutRes.ok) return timeoutRes;

    const retriesRes = parseIntegerInRange(driver.retries, 0, 10, `${prefix}的重试次数`);
    if (!retriesRes.ok) return retriesRes;

    let validated:
      | { readonly ok: true; readonly config: GatewayModbusDriverConfig }
      | { readonly ok: false; readonly error: string };
    switch (driver.driverType) {
      case 'MODBUS_TCP':
      case 'MODBUS_RTU':
        validated = validateModbusDriverEntry(
          prefix,
          deviceKey,
          driver.driverType,
          endpoint,
          capabilityProfile,
          timeoutRes.value,
          retriesRes.value,
          driver,
        );
        break;
      case 'BACNET_IP':
        validated = validateBACnetDriverEntry(
          prefix,
          deviceKey,
          endpoint,
          capabilityProfile,
          timeoutRes.value,
          retriesRes.value,
          driver.bacnet,
        );
        break;
      case 'OPC_UA':
        validated = validateOPCUADriverEntry(
          prefix,
          deviceKey,
          endpoint,
          capabilityProfile,
          timeoutRes.value,
          retriesRes.value,
          driver.opcua,
        );
        break;
    }
    if (!validated.ok) return validated;
    driverConfigs.push(validated.config);
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
      ...(driver.registers ? { registers: [...driver.registers].sort(compareRegisterMappings) } : {}),
      ...(driver.bacnet
        ? {
            bacnet: {
              ...driver.bacnet,
              objects: [...driver.bacnet.objects].sort((left, right) =>
                compareAscii(left.pointCode, right.pointCode),
              ),
            },
          }
        : {}),
      ...(driver.opcua
        ? {
            opcua: {
              ...driver.opcua,
              nodes: [...driver.opcua.nodes].sort((left, right) =>
                compareAscii(left.pointCode, right.pointCode),
              ),
            },
          }
        : {}),
    }))
    .sort((left, right) => compareAscii(left.deviceKey, right.deviceKey));
}

function areBACnetSpecsEqual(
  left: GatewayBACnetDriverSpec | undefined,
  right: GatewayBACnetDriverSpec | undefined,
): boolean {
  if (!left && !right) return true;
  if (!left || !right) return false;
  if (
    left.deviceInstance !== right.deviceInstance ||
    (left.networkNumber ?? 0) !== (right.networkNumber ?? 0) ||
    (left.covLifetimeSeconds ?? 0) !== (right.covLifetimeSeconds ?? 0) ||
    (left.writePriority ?? 8) !== (right.writePriority ?? 8) ||
    left.objects.length !== right.objects.length
  ) {
    return false;
  }
  for (let i = 0; i < left.objects.length; i += 1) {
    const oa = left.objects[i]!;
    const ob = right.objects[i]!;
    const scaleA = oa.scale === undefined || oa.scale === 0 ? 1 : oa.scale;
    const scaleB = ob.scale === undefined || ob.scale === 0 ? 1 : ob.scale;
    if (
      oa.pointCode !== ob.pointCode ||
      (oa.semanticChannel ?? '') !== (ob.semanticChannel ?? '') ||
      (oa.safetyBoundary ?? '') !== (ob.safetyBoundary ?? '') ||
      oa.objectType !== ob.objectType ||
      oa.objectInstance !== ob.objectInstance ||
      (oa.propertyId ?? 85) !== (ob.propertyId ?? 85) ||
      (oa.dataType ?? defaultBACnetDataType(oa.objectType)) !==
        (ob.dataType ?? defaultBACnetDataType(ob.objectType)) ||
      Boolean(oa.writable) !== Boolean(ob.writable) ||
      Boolean(oa.covSubscribe) !== Boolean(ob.covSubscribe) ||
      scaleA !== scaleB ||
      normalizeZero(oa.offset ?? 0) !== normalizeZero(ob.offset ?? 0) ||
      oa.pollPriority !== ob.pollPriority ||
      (oa.unit ?? '') !== (ob.unit ?? '') ||
      !areStringRecordsEqual(oa.enumMap, ob.enumMap)
    ) {
      return false;
    }
  }
  return true;
}

function areOPCUASpecsEqual(
  left: GatewayOPCUADriverSpec | undefined,
  right: GatewayOPCUADriverSpec | undefined,
): boolean {
  if (!left && !right) return true;
  if (!left || !right) return false;
  if (
    left.securityPolicy !== right.securityPolicy ||
    left.securityMode !== right.securityMode ||
    (left.authMode ?? 'ANONYMOUS') !== (right.authMode ?? 'ANONYMOUS') ||
    (left.username ?? '') !== (right.username ?? '') ||
    (left.credentialRef ?? '') !== (right.credentialRef ?? '') ||
    (left.subscriptionIntervalMs ?? 1000) !== (right.subscriptionIntervalMs ?? 1000) ||
    left.nodes.length !== right.nodes.length
  ) {
    return false;
  }
  for (let i = 0; i < left.nodes.length; i += 1) {
    const na = left.nodes[i]!;
    const nb = right.nodes[i]!;
    const scaleA = na.scale === undefined || na.scale === 0 ? 1 : na.scale;
    const scaleB = nb.scale === undefined || nb.scale === 0 ? 1 : nb.scale;
    if (
      na.pointCode !== nb.pointCode ||
      (na.semanticChannel ?? '') !== (nb.semanticChannel ?? '') ||
      (na.safetyBoundary ?? '') !== (nb.safetyBoundary ?? '') ||
      na.nodeId !== nb.nodeId ||
      (na.namespaceIndex ?? 0) !== (nb.namespaceIndex ?? 0) ||
      (na.dataType ?? 'DOUBLE') !== (nb.dataType ?? 'DOUBLE') ||
      Boolean(na.writable) !== Boolean(nb.writable) ||
      Boolean(na.subscribe) !== Boolean(nb.subscribe) ||
      scaleA !== scaleB ||
      normalizeZero(na.offset ?? 0) !== normalizeZero(nb.offset ?? 0) ||
      na.pollPriority !== nb.pollPriority ||
      (na.unit ?? '') !== (nb.unit ?? '')
    ) {
      return false;
    }
  }
  return true;
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
    const regsA = a.registers ?? [];
    const regsB = b.registers ?? [];
    if (
      a.deviceKey !== b.deviceKey ||
      a.driverType !== b.driverType ||
      a.endpoint !== b.endpoint ||
      (a.unitId ?? 0) !== (b.unitId ?? 0) ||
      (a.capabilityProfile ?? '') !== (b.capabilityProfile ?? '') ||
      a.timeoutMs !== b.timeoutMs ||
      a.retries !== b.retries ||
      regsA.length !== regsB.length
    ) {
      return false;
    }
    for (let j = 0; j < regsA.length; j += 1) {
      const ra = regsA[j]!;
      const rb = regsB[j]!;
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
    if (!areBACnetSpecsEqual(a.bacnet, b.bacnet) || !areOPCUASpecsEqual(a.opcua, b.opcua)) {
      return false;
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

export interface EdgeRuleBundleSummary {
  readonly configured: boolean;
  readonly ruleCount: number;
  readonly bundleRevision: number;
  readonly shortDigest: string;
  readonly compiledAt: string;
}

/** Summarizes a desired or reported edge rule bundle for gateway shadow cards and compilation sheets. */
export function summarizeEdgeRuleBundle(bundle: EdgeRuleBundle | null | undefined): EdgeRuleBundleSummary {
  if (!bundle) {
    return {
      configured: false,
      ruleCount: 0,
      bundleRevision: 0,
      shortDigest: '',
      compiledAt: '',
    };
  }
  return {
    configured: true,
    ruleCount: bundle.rules.length,
    bundleRevision: bundle.bundleRevision,
    shortDigest: bundle.edgeBundleDigest.slice(0, 12),
    compiledAt: bundle.compiledAt ?? '',
  };
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
