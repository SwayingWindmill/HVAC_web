import {
  resolveAutoControlExecutionMode,
  type ArmAutoControlLeaseRequestWithExecutionMode,
  type AutoControlExecutionMode,
  type AutoControlLeaseWithExecutionMode,
} from './auto-control-status.ts';

export {
  AUTO_CONTROL_CAPABILITY_LABELS,
  AUTO_CONTROL_EXECUTION_MODES,
  EXECUTION_MODE_PRESENTATION,
  LEASE_STATUS_PRESENTATION,
  MONITOR_STATUS_PRESENTATION,
  SELECTABLE_LEASE_CAPABILITIES,
  filterExecutionMonitors,
  formatAutoControlCapability,
  formatAutoControlExecutionMode,
  formatAutoControlTripReason,
  formatGuardrailVerdict,
  formatMonitorDecisionTrace,
  formatMonitorExecutionMode,
  formatMonitorLoopLabel,
  formatMonitorNumericValue,
  resolveActiveLeaseBackoff,
  resolveAutoControlExecutionMode,
  resolveMonitorExecutionMode,
  resolveMonitorUnit,
} from './auto-control-status.ts';
export type {
  ActiveLeaseBackoffNotice,
  ArmAutoControlLeaseRequestWithExecutionMode,
  AutoControlExecutionMode,
  AutoControlLeaseWithExecutionMode,
  ExecutionMonitorFilter,
  MonitorDecisionTracePresentation,
  MonitorTargetUnit,
} from './auto-control-status.ts';

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
