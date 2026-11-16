import type {
  ArmAutoControlLeaseRequest,
  AutoControlLease,
  AutoControlLeaseStatus,
  ExecutionMonitorStatus,
} from '../../api/generated/platformGateway.gen.ts';

export const AUTO_CONTROL_CAPABILITY_LABELS: Readonly<Record<string, string>> = {
  SET_CHILLED_WATER_TEMPERATURE_SETPOINT: '调整冷冻水供水温度设定',
  SET_TEMPERATURE_SETPOINT: '调整温度设定',
  SET_FREQUENCY: '调整运行频率',
  SET_FAN_SPEED: '调整风机转速',
  SET_LOAD_LIMIT: '调整负荷上限',
  SET_OPENING: '调整阀门开度',
  START: '启动设备',
  STOP: '停止设备',
  START_UNIT_SEQUENCE: '一键启动机组',
  STOP_UNIT_SEQUENCE: '一键停机',
  RESET_FAULT: '复位故障',
};

export const SELECTABLE_LEASE_CAPABILITIES = [
  'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
  'SET_TEMPERATURE_SETPOINT',
  'SET_FREQUENCY',
  'SET_FAN_SPEED',
  'SET_LOAD_LIMIT',
  'SET_OPENING',
] as const;

export function formatAutoControlCapability(capability: string): string {
  return AUTO_CONTROL_CAPABILITY_LABELS[capability] ?? '受控设备调节';
}

export const LEASE_STATUS_PRESENTATION: Readonly<Record<
  AutoControlLeaseStatus,
  { readonly label: string; readonly tone: 'success' | 'destructive' | 'neutral' }
>> = {
  ACTIVE: { label: '租约生效中', tone: 'success' },
  TRIPPED: { label: '已触发保护退出', tone: 'destructive' },
  REVOKED: { label: '已人工撤回', tone: 'neutral' },
  EXPIRED: { label: '已到期', tone: 'neutral' },
};

export const MONITOR_STATUS_PRESENTATION: Readonly<Record<
  ExecutionMonitorStatus,
  { readonly label: string; readonly tone: 'in-progress' | 'success' | 'warning' | 'destructive' }
>> = {
  OBSERVING: { label: '观察中', tone: 'in-progress' },
  VERIFIED: { label: '已核验通过', tone: 'success' },
  ROLLED_BACK: { label: '已自动回退', tone: 'warning' },
  TRIPPED: { label: '已触发保护退出', tone: 'destructive' },
};

function formatUnitsInMetrics(raw: string): string {
  return raw
    .replace(/(-?\d+(?:\.\d+)?)C\b/g, '$1 °C')
    .replace(/(-?\d+(?:\.\d+)?)kW\b/g, '$1 kW')
    .replace(/\*/g, ' × ')
    .replace(/\s+/g, ' ')
    .trim();
}

/**
 * Translates backend auto-control trip codes and monitor reasons into operator Chinese
 * without leaking raw snake_case identifiers.
 */
export function formatAutoControlTripReason(reason: string | null | undefined): string {
  const trimmed = reason?.trim() ?? '';
  if (!trimmed) return '暂无';
  if (trimmed === 'telemetry_unhealthy_over_5m') {
    return '关键遥测连续异常超过 5 分钟，已触发保护退出';
  }
  if (trimmed === 'superseded by new operator lease') {
    return '已被操作员重新开通的新租约替换';
  }
  const supplyMatch = /^supply_temperature_exceeded_max\s*\(([^)]+)\)$/.exec(trimmed);
  if (supplyMatch) {
    return `冷冻水供水温度超出上限（${formatUnitsInMetrics(supplyMatch[1])}）`;
  }
  if (trimmed.startsWith('supply_temperature_exceeded_max')) {
    return '冷冻水供水温度超出租约上限';
  }
  const zoneMatch = /^zone_temperature_exceeded_max\s+\S+\s*\(([^)]+)\)$/.exec(trimmed);
  if (zoneMatch) {
    return `室内温度超出上限（${formatUnitsInMetrics(zoneMatch[1])}）`;
  }
  if (trimmed.startsWith('zone_temperature_exceeded_max')) {
    return '室内温度超出租约上限';
  }
  const powerMatch = /^chiller_power_exceeded_90pct\s+\S+\s*\(([^)]+)\)$/.exec(trimmed);
  if (powerMatch) {
    return `冷机运行功率超过额定 90%（${formatUnitsInMetrics(powerMatch[1])}）`;
  }
  if (trimmed.startsWith('chiller_power_exceeded_90pct')) {
    return '冷机运行功率超过额定 90%';
  }
  if (/\p{Script=Han}/u.test(trimmed)) {
    return trimmed;
  }
  return '触发安全边界保护退出';
}

export interface ArmLeaseFormValues {
  readonly expiresInHours: string;
  readonly supplyTempMinC: string;
  readonly supplyTempMaxC: string;
  readonly maxSetpointStepC: string;
  readonly zoneTempMaxC: string;
  readonly minRunningChillers: string;
  readonly maxCommandsPerHour: string;
  readonly allowedCapabilities: readonly string[];
  readonly reason: string;
  readonly verificationCode: string;
}

export type ArmLeaseNumericField =
  | 'expiresInHours'
  | 'supplyTempMinC'
  | 'supplyTempMaxC'
  | 'maxSetpointStepC'
  | 'zoneTempMaxC'
  | 'minRunningChillers'
  | 'maxCommandsPerHour';

export type ArmLeaseFieldName =
  | ArmLeaseNumericField
  | 'allowedCapabilities'
  | 'reason'
  | 'verificationCode';

export type ArmLeaseFieldErrors = Partial<Record<ArmLeaseFieldName, string>>;

export function initialArmLeaseValues(seed?: AutoControlLease | null): ArmLeaseFormValues {
  return {
    expiresInHours: '8',
    supplyTempMinC: seed ? String(seed.supplyTempMinC) : '6.0',
    supplyTempMaxC: seed ? String(seed.supplyTempMaxC) : '12.0',
    maxSetpointStepC: seed ? String(seed.maxSetpointStepC) : '1.0',
    zoneTempMaxC: seed ? String(seed.zoneTempMaxC) : '26.0',
    minRunningChillers: seed ? String(seed.minRunningChillers) : '1',
    maxCommandsPerHour: seed ? String(seed.maxCommandsPerHour) : '2',
    allowedCapabilities:
      seed && seed.allowedCapabilities.length > 0
        ? [...seed.allowedCapabilities]
        : ['SET_CHILLED_WATER_TEMPERATURE_SETPOINT'],
    reason: '',
    verificationCode: '',
  };
}

function parseFinite(raw: string): number | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : null;
}

function parseInteger(raw: string): number | null {
  const parsed = parseFinite(raw);
  return parsed !== null && Number.isInteger(parsed) ? parsed : null;
}

export function validateArmLeaseForm(
  values: ArmLeaseFormValues,
  options: { readonly includeSubmitOnly?: boolean } = {},
): ArmLeaseFieldErrors {
  const errors: ArmLeaseFieldErrors = {};
  const expires = parseInteger(values.expiresInHours);
  if (expires === null || expires < 1 || expires > 24) {
    errors.expiresInHours = '有效期需为 1 至 24 的整数小时';
  }

  const supplyMin = parseFinite(values.supplyTempMinC);
  if (supplyMin === null || supplyMin < 4 || supplyMin > 20) {
    errors.supplyTempMinC = '供水温度下限需在 4.0 至 20.0 °C 之间';
  }

  const supplyMax = parseFinite(values.supplyTempMaxC);
  if (
    supplyMax === null
    || supplyMax < 4
    || supplyMax > 25
    || (supplyMin !== null && supplyMax < supplyMin)
  ) {
    errors.supplyTempMaxC = '供水温度上限需不低于下限且不超过 25.0 °C';
  }

  const step = parseFinite(values.maxSetpointStepC);
  if (step === null || step <= 0 || step > 2) {
    errors.maxSetpointStepC = '单次最大步长需大于 0 且不超过 2.0 °C';
  }

  const zoneMax = parseFinite(values.zoneTempMaxC);
  if (zoneMax === null || zoneMax < 18 || zoneMax > 32) {
    errors.zoneTempMaxC = '室内温度上限需在 18.0 至 32.0 °C 之间';
  }

  const minChillers = parseInteger(values.minRunningChillers);
  if (minChillers === null || minChillers < 0) {
    errors.minRunningChillers = '最少运行冷机数需为不小于 0 的整数';
  }

  const maxPerHour = parseInteger(values.maxCommandsPerHour);
  if (maxPerHour === null || maxPerHour < 1 || maxPerHour > 6) {
    errors.maxCommandsPerHour = '每小时指令上限需为 1 至 6 的整数';
  }

  if (values.allowedCapabilities.length === 0) {
    errors.allowedCapabilities = '请至少选择一项允许自动控制的能力';
  }

  if (options.includeSubmitOnly) {
    const trimmedReason = values.reason.trim();
    if (!trimmedReason || trimmedReason.length > 1000) {
      errors.reason = '请填写授权原因（1 至 1000 字）';
    }
    if (!/^\d{6}$/.test(values.verificationCode)) {
      errors.verificationCode = '请输入 6 位数字动态口令';
    }
  }

  return errors;
}

export function buildArmLeasePayload(
  values: ArmLeaseFormValues,
  expectedRevision?: number,
):
  | { readonly ok: true; readonly payload: ArmAutoControlLeaseRequest }
  | { readonly ok: false; readonly errors: ArmLeaseFieldErrors } {
  const errors = validateArmLeaseForm(values, { includeSubmitOnly: true });
  if (Object.keys(errors).length > 0) {
    return { ok: false, errors };
  }
  return {
    ok: true,
    payload: {
      expiresInHours: Number(values.expiresInHours.trim()),
      allowedCapabilities: [...values.allowedCapabilities],
      maxSetpointStepC: Number(values.maxSetpointStepC.trim()),
      supplyTempMinC: Number(values.supplyTempMinC.trim()),
      supplyTempMaxC: Number(values.supplyTempMaxC.trim()),
      zoneTempMaxC: Number(values.zoneTempMaxC.trim()),
      minRunningChillers: Number(values.minRunningChillers.trim()),
      maxCommandsPerHour: Number(values.maxCommandsPerHour.trim()),
      reason: values.reason.trim(),
      verificationCode: values.verificationCode,
      ...(expectedRevision !== undefined ? { expectedRevision } : {}),
    },
  };
}
