import type {
  ArmAutoControlLeaseRequest,
  AutoControlLease,
  AutoControlLeaseStatus,
  ExecutionMonitorStatus,
  OptimizationExecutionMonitor,
} from '../../api/generated/platformGateway.gen.ts';

export type AutoControlExecutionMode = 'CLOSED_LOOP' | 'SHADOW';

export const AUTO_CONTROL_EXECUTION_MODES = ['CLOSED_LOOP', 'SHADOW'] as const;

export type AutoControlLeaseWithExecutionMode = AutoControlLease & {
  readonly executionMode?: AutoControlExecutionMode;
};

export type ArmAutoControlLeaseRequestWithExecutionMode = ArmAutoControlLeaseRequest & {
  readonly executionMode?: AutoControlExecutionMode;
};

export const EXECUTION_MODE_PRESENTATION: Readonly<Record<
  AutoControlExecutionMode,
  {
    readonly optionTitle: string;
    readonly optionDescription: string;
    readonly tileLabel: string;
    readonly monitorBadge: string;
  }
>> = {
  CLOSED_LOOP: {
    optionTitle: '闭环自动控制',
    optionDescription: '按护栏校验通过后直接向网关下发控制设定点，并持续 30 分钟巡检与异常回退。',
    tileLabel: '闭环下发',
    monitorBadge: '闭环',
  },
  SHADOW: {
    optionTitle: '影子跟跑（只记录不下发）',
    optionDescription: '完整执行实时遥测与物理护栏计算并留痕对比，在最后一跳拦截，绝不下发设备指令。',
    tileLabel: '影子跟跑',
    monitorBadge: '影子',
  },
};

export function resolveAutoControlExecutionMode(
  source: { readonly executionMode?: AutoControlExecutionMode } | null | undefined,
): AutoControlExecutionMode {
  return source?.executionMode === 'SHADOW' ? 'SHADOW' : 'CLOSED_LOOP';
}

export function formatAutoControlExecutionMode(mode: AutoControlExecutionMode): string {
  return EXECUTION_MODE_PRESENTATION[mode].tileLabel;
}

export function resolveMonitorExecutionMode(
  monitor: Pick<OptimizationExecutionMonitor, 'status'> & { readonly executionMode?: AutoControlExecutionMode },
): AutoControlExecutionMode {
  if (monitor.executionMode === 'SHADOW' || monitor.status === 'SHADOW_RECORDED') {
    return 'SHADOW';
  }
  return 'CLOSED_LOOP';
}

export type MonitorTargetUnit = 'C' | 'Hz' | '%';

const MONITOR_LOOP_LABELS: Readonly<Record<string, string>> = {
  PLANT: '冷冻水供水',
  CHW_PUMP: '冷冻泵变频',
  CW_PUMP: '冷却泵变频',
  CT_FAN: '冷却塔风机',
};

export function resolveMonitorUnit(
  monitor: Pick<OptimizationExecutionMonitor, 'targetCapability' | 'targetUnit'>,
): MonitorTargetUnit {
  if (monitor.targetUnit === 'Hz' || monitor.targetUnit === '%' || monitor.targetUnit === 'C') {
    return monitor.targetUnit;
  }
  if (monitor.targetCapability === 'SET_FREQUENCY') return 'Hz';
  if (monitor.targetCapability === 'SET_FAN_SPEED') return '%';
  return 'C';
}

export function formatMonitorLoopLabel(
  loopKey: string | null | undefined,
  targetCapability?: string | null,
): string {
  const normalized = loopKey?.trim().toUpperCase() ?? '';
  if (normalized && MONITOR_LOOP_LABELS[normalized]) {
    return MONITOR_LOOP_LABELS[normalized];
  }
  if (targetCapability === 'SET_FREQUENCY') return '水泵变频';
  if (targetCapability === 'SET_FAN_SPEED') return '冷却塔风机';
  return '冷冻水供水';
}

export function formatMonitorNumericValue(
  value: number | undefined,
  unit: MonitorTargetUnit,
  digits = 1,
): string {
  if (value === undefined || !Number.isFinite(value)) return '暂无';
  const formatted = value.toFixed(digits);
  if (unit === '%') return `${formatted}%`;
  if (unit === 'Hz') return `${formatted} Hz`;
  return `${formatted} °C`;
}

export function formatMonitorExecutionMode(mode: AutoControlExecutionMode): string {
  return EXECUTION_MODE_PRESENTATION[mode].monitorBadge;
}

export interface MonitorDecisionTracePresentation {
  readonly label: string;
  readonly title: string;
}

export function formatMonitorDecisionTrace(
  monitor: { readonly decisionId?: string | null; readonly traceId?: string },
): MonitorDecisionTracePresentation | null {
  const decisionId = monitor.decisionId?.trim() ?? '';
  const traceId = monitor.traceId?.trim() ?? '';
  if (!decisionId && !traceId) return null;
  const parts: string[] = [];
  if (decisionId) parts.push(`决策编号：${decisionId}`);
  if (traceId) parts.push(`链路追踪：${traceId}`);
  return {
    label: '已记录决策轨迹',
    title: parts.join(' · '),
  };
}

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
  PENDING_SUBMIT: { label: '待下发', tone: 'in-progress' },
  OBSERVING: { label: '观察中', tone: 'in-progress' },
  VERIFIED: { label: '已核验通过', tone: 'success' },
  ROLLED_BACK: { label: '已自动回退', tone: 'warning' },
  TRIPPED: { label: '已触发保护退出', tone: 'destructive' },
  TRIPPED_ROLLED_BACK: { label: '已自动回退', tone: 'warning' },
  ROLLBACK_FAILED: { label: '回退失败退出', tone: 'destructive' },
  SHADOW_RECORDED: { label: '影子跟跑记录', tone: 'in-progress' },
  SUBMIT_FAILED: { label: '下发失败', tone: 'destructive' },
  MANUAL_OVERRIDE: { label: '人工接管退出', tone: 'warning' },
};

function formatUnitsInMetrics(raw: string): string {
  return raw
    .replace(/(-?\d+(?:\.\d+)?)C\b/g, '$1 °C')
    .replace(/(-?\d+(?:\.\d+)?)Hz\b/g, '$1 Hz')
    .replace(/(-?\d+(?:\.\d+)?)kW\b/g, '$1 kW')
    .replace(/\*/g, ' × ')
    .replace(/\s+/g, ' ')
    .trim();
}

const EXACT_TRIP_REASONS: Readonly<Record<string, string>> = {
  telemetry_unhealthy_over_5m: '关键遥测连续异常超过 5 分钟，已触发保护退出',
  'superseded by new operator lease': '已被操作员重新开通的新租约替换',
  supply_temperature_below_min: '供水温度低于防冻下限，已触发保护退回',
  stale_or_bad_telemetry: '实时传感器数据过期或质量异常，已暂停自动调节',
  device_fault_or_local_manual: '设备存在故障报警或处于现场就地手动，自动调节已锁定',
  cabinet_local_manual: '设备存在故障报警或处于现场就地手动，自动调节已锁定',
  manual_override_backoff: '检测到人工干预设定值，AI 自动调节已进入退避保护窗口',
  cabinet_setpoint_manual_override: '检测到人工干预设定值，AI 自动调节已进入退避保护窗口',
  within_deadband: '目标差值在调节死区内，跳过下发以保护设备动作寿命',
  setpoint_tracking_deviation: '设定值超时未收敛，已自动回退并退出租约',
};

const GUARDRAIL_VERDICT_LABELS: Readonly<Record<string, string>> = {
  allowed: '护栏校验通过',
  pass: '护栏校验通过',
  passed: '护栏校验通过',
  ok: '护栏校验通过',
  clamped: '已按安全边界限幅',
  clamped_to_bounds: '已按安全边界限幅',
  clamped_to_step: '已按单次最大步长限幅',
  step_clamped: '已按单次最大步长限幅',
  bound_clamped: '已按安全边界限幅',
  deadband_skip: '处于调节死区内，已跳过下发',
  within_deadband: '处于调节死区内，已跳过下发',
  skipped_deadband: '处于调节死区内，已跳过下发',
  vetoed: '已由安全护栏拦截',
  manual_override_backoff: '处于人工干预退避窗口',
  cabinet_setpoint_manual_override: '处于人工干预退避窗口',
  supply_temperature_below_min: '低于防冻下限保护阈值',
  stale_or_bad_telemetry: '传感器数据过期或质量异常',
  device_fault_or_local_manual: '设备故障或就地手动锁定',
  cabinet_local_manual: '设备故障或就地手动锁定',
  blocked: '已由安全护栏拦截',
  rejected: '已由安全护栏拦截',
  denied: '已由安全护栏拦截',
};

/**
 * Translates backend auto-control trip codes and monitor reasons into operator Chinese
 * without leaking raw snake_case identifiers.
 */
export function formatAutoControlTripReason(reason: string | null | undefined): string {
  const trimmed = reason?.trim() ?? '';
  if (!trimmed) return '暂无';
  const normalized = trimmed.toLowerCase();
  const exact = EXACT_TRIP_REASONS[normalized];
  if (exact) return exact;

  const parenMatch = /^([a-z0-9_]+)\s*\(([^)]+)\)$/.exec(normalized);
  if (parenMatch) {
    const mapped = EXACT_TRIP_REASONS[parenMatch[1]];
    if (mapped) {
      const rawParenMatch = /^[a-zA-Z0-9_]+\s*\(([^)]+)\)$/.exec(trimmed);
      const detail = rawParenMatch ? formatUnitsInMetrics(rawParenMatch[1]) : formatUnitsInMetrics(parenMatch[2]);
      return `${mapped}（${detail}）`;
    }
  }

  for (const [code, message] of Object.entries(EXACT_TRIP_REASONS)) {
    if (normalized.startsWith(`${code} `) || normalized.startsWith(`${code}:`)) {
      return message;
    }
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

/**
 * Translates backend guardrail verdicts into plain Chinese without leaking internal enums.
 */
export function formatGuardrailVerdict(verdict: string | null | undefined): string | null {
  const trimmed = verdict?.trim() ?? '';
  if (!trimmed) return null;
  const mapped = GUARDRAIL_VERDICT_LABELS[trimmed.toLowerCase()];
  if (mapped) return mapped;
  if (/\p{Script=Han}/u.test(trimmed)) {
    return trimmed;
  }
  return '已触发安全护栏校核';
}

export interface ActiveLeaseBackoffNotice {
  readonly until: string | null;
  readonly reasonText: string | null;
}

/**
 * Determines whether an active lease is currently paused under manual override backoff.
 * A valid `manualBackoffUntil` in the past supersedes a stale reason string.
 */
export function resolveActiveLeaseBackoff(
  lease: { readonly manualBackoffUntil?: string | null; readonly manualBackoffReason?: string } | null | undefined,
  nowMs: number = Date.now(),
): ActiveLeaseBackoffNotice | null {
  if (!lease) return null;
  const rawUntil = lease.manualBackoffUntil?.trim() ?? '';
  const untilMs = rawUntil ? Date.parse(rawUntil) : Number.NaN;
  const hasValidUntil = Number.isFinite(untilMs);
  if (hasValidUntil && untilMs <= nowMs) {
    return null;
  }
  const hasFutureUntil = hasValidUntil && untilMs > nowMs;
  const rawReason = lease.manualBackoffReason?.trim() ?? '';
  if (!hasFutureUntil && !rawReason) {
    return null;
  }
  return {
    until: hasFutureUntil ? rawUntil : null,
    reasonText: rawReason ? formatAutoControlTripReason(rawReason) : null,
  };
}

export interface ArmLeaseFormValues {
  readonly executionMode: AutoControlExecutionMode;
  readonly expiresInHours: string;
  readonly supplyTempMinC: string;
  readonly supplyTempMaxC: string;
  readonly maxSetpointStepC: string;
  readonly deadbandC: string;
  readonly zoneTempMaxC: string;
  readonly minRunningChillers: string;
  readonly maxCommandsPerHour: string;
  readonly pumpFreqMinHz: string;
  readonly pumpFreqMaxHz: string;
  readonly maxFreqStepHz: string;
  readonly freqDeadbandHz: string;
  readonly fanSpeedMinPct: string;
  readonly fanSpeedMaxPct: string;
  readonly maxFanStepPct: string;
  readonly fanDeadbandPct: string;
  readonly allowedCapabilities: readonly string[];
  readonly reason: string;
  readonly verificationCode: string;
}

export type ArmLeaseNumericField =
  | 'expiresInHours'
  | 'supplyTempMinC'
  | 'supplyTempMaxC'
  | 'maxSetpointStepC'
  | 'deadbandC'
  | 'zoneTempMaxC'
  | 'minRunningChillers'
  | 'maxCommandsPerHour'
  | 'pumpFreqMinHz'
  | 'pumpFreqMaxHz'
  | 'maxFreqStepHz'
  | 'freqDeadbandHz'
  | 'fanSpeedMinPct'
  | 'fanSpeedMaxPct'
  | 'maxFanStepPct'
  | 'fanDeadbandPct';

export type ArmLeaseFieldName =
  | ArmLeaseNumericField
  | 'executionMode'
  | 'allowedCapabilities'
  | 'reason'
  | 'verificationCode';

export type ArmLeaseFieldErrors = Partial<Record<ArmLeaseFieldName, string>>;

export function initialArmLeaseValues(seed?: AutoControlLeaseWithExecutionMode | null): ArmLeaseFormValues {
  return {
    executionMode: resolveAutoControlExecutionMode(seed),
    expiresInHours: '8',
    supplyTempMinC: seed ? String(seed.supplyTempMinC) : '6.0',
    supplyTempMaxC: seed ? String(seed.supplyTempMaxC) : '12.0',
    maxSetpointStepC: seed ? String(seed.maxSetpointStepC) : '1.0',
    deadbandC: seed?.deadbandC !== undefined ? String(seed.deadbandC) : '0.2',
    zoneTempMaxC: seed ? String(seed.zoneTempMaxC) : '26.0',
    minRunningChillers: seed ? String(seed.minRunningChillers) : '1',
    maxCommandsPerHour: seed ? String(seed.maxCommandsPerHour) : '2',
    pumpFreqMinHz: seed?.pumpFreqMinHz !== undefined ? String(seed.pumpFreqMinHz) : '28.0',
    pumpFreqMaxHz: seed?.pumpFreqMaxHz !== undefined ? String(seed.pumpFreqMaxHz) : '50.0',
    maxFreqStepHz: seed?.maxFreqStepHz !== undefined ? String(seed.maxFreqStepHz) : '2.5',
    freqDeadbandHz: seed?.freqDeadbandHz !== undefined ? String(seed.freqDeadbandHz) : '0.5',
    fanSpeedMinPct: seed?.fanSpeedMinPct !== undefined ? String(seed.fanSpeedMinPct) : '25.0',
    fanSpeedMaxPct: seed?.fanSpeedMaxPct !== undefined ? String(seed.fanSpeedMaxPct) : '100.0',
    maxFanStepPct: seed?.maxFanStepPct !== undefined ? String(seed.maxFanStepPct) : '10.0',
    fanDeadbandPct: seed?.fanDeadbandPct !== undefined ? String(seed.fanDeadbandPct) : '2.0',
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
  if (supplyMin === null || supplyMin < 5 || supplyMin > 20) {
    errors.supplyTempMinC = '供水温度下限需在 5.0 至 20.0 °C 之间';
  }

  const supplyMax = parseFinite(values.supplyTempMaxC);
  if (
    supplyMax === null
    || supplyMax < 5
    || supplyMax > 25
    || (supplyMin !== null && supplyMax < supplyMin)
  ) {
    errors.supplyTempMaxC = '供水温度上限需不低于下限且不超过 25.0 °C';
  }

  const step = parseFinite(values.maxSetpointStepC);
  if (step === null || step <= 0 || step > 2) {
    errors.maxSetpointStepC = '单次最大步长需大于 0 且不超过 2.0 °C';
  }

  const deadband = parseFinite(values.deadbandC);
  if (deadband === null || deadband < 0.05 || deadband > 2) {
    errors.deadbandC = '调节死区需在 0.05 至 2.0 °C 之间';
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

  if (values.allowedCapabilities.includes('SET_FREQUENCY')) {
    const freqMin = parseFinite(values.pumpFreqMinHz);
    if (freqMin === null || freqMin < 20 || freqMin > 50) {
      errors.pumpFreqMinHz = '水泵最低频率需在 20.0 至 50.0 Hz 之间';
    }
    const freqMax = parseFinite(values.pumpFreqMaxHz);
    if (
      freqMax === null
      || freqMax < 25
      || freqMax > 60
      || (freqMin !== null && freqMax < freqMin)
    ) {
      errors.pumpFreqMaxHz = '水泵最高频率需不低于下限且在 25.0 至 60.0 Hz 之间';
    }
    const freqStep = parseFinite(values.maxFreqStepHz);
    if (freqStep === null || freqStep <= 0 || freqStep > 10) {
      errors.maxFreqStepHz = '水泵单次最大调频步长需大于 0 且不超过 10.0 Hz';
    }
    const freqDb = parseFinite(values.freqDeadbandHz);
    if (freqDb === null || freqDb < 0.1 || freqDb > 5) {
      errors.freqDeadbandHz = '水泵调频死区需在 0.1 至 5.0 Hz 之间';
    }
  }

  if (values.allowedCapabilities.includes('SET_FAN_SPEED')) {
    const fanMin = parseFinite(values.fanSpeedMinPct);
    if (fanMin === null || fanMin < 15 || fanMin > 100) {
      errors.fanSpeedMinPct = '风机最低转速需在 15.0% 至 100.0% 之间';
    }
    const fanMax = parseFinite(values.fanSpeedMaxPct);
    if (
      fanMax === null
      || fanMax < 20
      || fanMax > 100
      || (fanMin !== null && fanMax < fanMin)
    ) {
      errors.fanSpeedMaxPct = '风机最高转速需不低于下限且在 20.0% 至 100.0% 之间';
    }
    const fanStep = parseFinite(values.maxFanStepPct);
    if (fanStep === null || fanStep <= 0 || fanStep > 25) {
      errors.maxFanStepPct = '风机单次最大调速步长需大于 0 且不超过 25.0%';
    }
    const fanDb = parseFinite(values.fanDeadbandPct);
    if (fanDb === null || fanDb < 0.5 || fanDb > 15) {
      errors.fanDeadbandPct = '风机调速死区需在 0.5% 至 15.0% 之间';
    }
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
  | { readonly ok: true; readonly payload: ArmAutoControlLeaseRequestWithExecutionMode }
  | { readonly ok: false; readonly errors: ArmLeaseFieldErrors } {
  const errors = validateArmLeaseForm(values, { includeSubmitOnly: true });
  if (Object.keys(errors).length > 0) {
    return { ok: false, errors };
  }
  const hasFrequency = values.allowedCapabilities.includes('SET_FREQUENCY');
  const hasFanSpeed = values.allowedCapabilities.includes('SET_FAN_SPEED');
  return {
    ok: true,
    payload: {
      executionMode: values.executionMode,
      expiresInHours: Number(values.expiresInHours.trim()),
      allowedCapabilities: [...values.allowedCapabilities],
      maxSetpointStepC: Number(values.maxSetpointStepC.trim()),
      deadbandC: Number(values.deadbandC.trim()),
      supplyTempMinC: Number(values.supplyTempMinC.trim()),
      supplyTempMaxC: Number(values.supplyTempMaxC.trim()),
      zoneTempMaxC: Number(values.zoneTempMaxC.trim()),
      minRunningChillers: Number(values.minRunningChillers.trim()),
      maxCommandsPerHour: Number(values.maxCommandsPerHour.trim()),
      ...(hasFrequency ? {
        pumpFreqMinHz: Number(values.pumpFreqMinHz.trim()),
        pumpFreqMaxHz: Number(values.pumpFreqMaxHz.trim()),
        maxFreqStepHz: Number(values.maxFreqStepHz.trim()),
        freqDeadbandHz: Number(values.freqDeadbandHz.trim()),
      } : {}),
      ...(hasFanSpeed ? {
        fanSpeedMinPct: Number(values.fanSpeedMinPct.trim()),
        fanSpeedMaxPct: Number(values.fanSpeedMaxPct.trim()),
        maxFanStepPct: Number(values.maxFanStepPct.trim()),
        fanDeadbandPct: Number(values.fanDeadbandPct.trim()),
      } : {}),
      reason: values.reason.trim(),
      verificationCode: values.verificationCode,
      ...(expectedRevision !== undefined ? { expectedRevision } : {}),
    },
  };
}
