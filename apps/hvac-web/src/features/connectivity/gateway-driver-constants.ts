import type {
  GatewayBACnetObjectMapping,
  GatewayModbusDriverConfig,
  GatewayModbusRegisterMapping,
  GatewayOPCUANodeMapping,
} from '../../api/generated/platformGateway.gen.ts';

export const POINT_CODE_PATTERN = /^[a-z][a-z0-9_]{0,127}$/;
export const UINT16_DECIMAL_PATTERN = /^(?:0|[1-9]\d{0,4})$/;

let draftIdSequence = 0;

export function nextDraftId(prefix: string): string {
  draftIdSequence += 1;
  return `${prefix}-${draftIdSequence}`;
}

export function compareAscii(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

export function normalizeZero(value: number): number {
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
