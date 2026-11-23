// Mistouch-proof emergency remote control domain rules, pre-flight guards, TOTP step-up retry,
// deterministic idempotency intent tracking, and bounded async command polling for the mini program.

import type { RawSiteAssetModel, RawTelemetryPointRecord } from './asset-model-cache';
import { GatewayError, type Gateway } from './gateway';
import type { PlantDevice, PlantReading } from './plant';
import { formatTelemetryDisplayValue, formatTelemetryUnit } from '../shared/centralPlantTelemetry';

export type ActionCapability =
  | 'START'
  | 'STOP'
  | 'START_UNIT_SEQUENCE'
  | 'STOP_UNIT_SEQUENCE'
  | 'RESET_FAULT';

export type SettingCapability =
  | 'SET_TEMPERATURE_SETPOINT'
  | 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT'
  | 'SET_FREQUENCY'
  | 'SET_FAN_SPEED'
  | 'SET_LOAD_LIMIT'
  | 'SET_OPENING';

export type SiteControlMode = 'REMOTE_AUTO' | 'MANUAL_OVERRIDE' | 'SAFE_SHUTDOWN_EXIT';
export type AssetControlLockMode = 'UNLOCKED' | 'MANUAL_LOCK' | 'MAINTENANCE_LOCK';

export type CommandStatus =
  | 'SUBMITTED'
  | 'VALIDATING'
  | 'AWAITING_APPROVAL'
  | 'APPROVED'
  | 'QUEUED'
  | 'DISPATCHING'
  | 'SUCCEEDED'
  | 'FAILED'
  | 'REJECTED'
  | 'CANCELLED'
  | 'EXPIRED'
  | 'OUTCOME_UNKNOWN';

export interface PrincipalCapabilitiesResponse {
  readonly principal?: { readonly roles?: readonly string[] };
  readonly authorization?: { readonly capabilities?: readonly string[] };
}

export interface ActiveMaintenanceWindow {
  readonly id: string;
  readonly title: string;
  readonly startDate: string;
  readonly endDate: string;
  readonly reason: string;
}

export interface AssetControlLockState {
  readonly siteId: string;
  readonly assetId: string;
  readonly lockMode: AssetControlLockMode;
  readonly reason: string;
  readonly updatedBy?: string;
  readonly updatedByName?: string;
  readonly updatedAt: string;
  readonly revision: number;
}

export interface SiteControlState {
  readonly tenantId: string;
  readonly siteId: string;
  readonly controlMode: SiteControlMode;
  readonly reason: string;
  readonly updatedBy?: string;
  readonly updatedByName?: string;
  readonly updatedAt: string;
  readonly revision: number;
  readonly assetLocks: readonly AssetControlLockState[];
  readonly activeMaintenanceWindow?: ActiveMaintenanceWindow | null;
}

export interface CommandRecord {
  readonly commandId: string;
  readonly siteId: string;
  readonly deviceId: string;
  readonly pointId: string;
  readonly capability: string;
  readonly status: CommandStatus;
  readonly risk?: string;
  readonly approvalPolicy?: string;
  readonly parameters: Readonly<Record<string, number>>;
  readonly outcomeReason?: string | null;
  readonly lastErrorMessage?: string | null;
  readonly createdAt: string;
  readonly updatedAt: string;
}

export interface InterlockRule {
  readonly deviceId: string;
  readonly deviceCode: string;
  readonly expected: 'RUNNING' | 'STOPPED';
  readonly reason: string;
}

export interface DeviceControlAction {
  readonly assetId: string;
  readonly pointId: string;
  readonly capability: ActionCapability;
  readonly label: string;
  readonly highRisk: boolean;
  readonly requiresCode: boolean;
  readonly interlocks: readonly InterlockRule[];
}

export interface DeviceControlSetting {
  readonly assetId: string;
  readonly pointId: string;
  readonly capability: SettingCapability;
  readonly label: string;
  readonly parameterKey: string;
  readonly unit: string | null;
  readonly minimum: number;
  readonly maximum: number;
  readonly step: number;
  readonly feedbackSourceKey: string | null;
}

export interface DeviceControlTarget {
  readonly assetId: string;
  readonly assetName: string;
  readonly assetType: string;
  readonly deviceId: string;
  readonly actions: readonly DeviceControlAction[];
  readonly settings: readonly DeviceControlSetting[];
}

export interface ActionButtonView {
  readonly pointId: string;
  readonly assetId: string;
  readonly assetName: string;
  readonly capability: ActionCapability;
  readonly label: string;
  readonly danger: boolean;
  readonly disabled: boolean;
  readonly disabledReason: string;
  readonly highRisk: boolean;
  readonly requiresCode: boolean;
}

export interface SettingRowView {
  readonly pointId: string;
  readonly assetId: string;
  readonly assetName: string;
  readonly capability: SettingCapability;
  readonly label: string;
  readonly parameterKey: string;
  readonly unit: string | null;
  readonly minimum: number;
  readonly maximum: number;
  readonly step: number;
  readonly currentText: string;
  readonly currentNumeric: number | null;
  readonly draftValue: number;
  readonly draftText: string;
  readonly disabled: boolean;
  readonly disabledReason: string;
}

export interface PendingControlIntent {
  readonly idempotencyKey: string;
  readonly assetId: string;
  readonly assetName: string;
  readonly deviceName: string;
  readonly pointId: string;
  readonly capability: ActionCapability | SettingCapability;
  readonly title: string;
  readonly parameterLabel: string;
  readonly beforeValue: string;
  readonly afterValue: string;
  readonly parameters: Readonly<Record<string, number>>;
  readonly highRisk: boolean;
  readonly requiresTotp: boolean;
  readonly acknowledgementLabel: string;
}

export interface CommandSubmitFailure {
  readonly kind: 'step-up' | 'conflict' | 'forbidden' | 'network' | 'rejected';
  readonly message: string;
  readonly showMfaSetupHint: boolean;
  readonly canRetrySameKey: boolean;
  readonly shouldRefreshState: boolean;
}

export interface PresentedCommandStatus {
  readonly commandId: string;
  readonly status: CommandStatus;
  readonly label: string;
  readonly tone: 'good' | 'bad' | 'warning' | 'neutral';
  readonly active: boolean;
  readonly detail: string;
}

export interface GuardSummary {
  readonly blocked: boolean;
  readonly tone: 'good' | 'warning' | 'bad';
  readonly badge: string;
  readonly headline: string;
  readonly detail: string;
}

export type ControlGatewayClient = Pick<Gateway, 'get' | 'post'>;

const ACTION_ORDER: readonly ActionCapability[] = [
  'START',
  'STOP',
  'START_UNIT_SEQUENCE',
  'STOP_UNIT_SEQUENCE',
  'RESET_FAULT',
];

const SETTING_CAPABILITIES: readonly SettingCapability[] = [
  'SET_TEMPERATURE_SETPOINT',
  'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
  'SET_FREQUENCY',
  'SET_FAN_SPEED',
  'SET_LOAD_LIMIT',
  'SET_OPENING',
];

const HIGH_RISK_ACTIONS: ReadonlySet<ActionCapability> = new Set([
  'START',
  'STOP',
  'START_UNIT_SEQUENCE',
  'STOP_UNIT_SEQUENCE',
]);

const MAINTENANCE_ALLOWED_ACTIONS: ReadonlySet<ActionCapability> = new Set([
  'STOP',
  'STOP_UNIT_SEQUENCE',
  'RESET_FAULT',
]);

const ACTIVE_COMMAND_STATUSES: ReadonlySet<CommandStatus> = new Set([
  'SUBMITTED',
  'VALIDATING',
  'AWAITING_APPROVAL',
  'APPROVED',
  'QUEUED',
  'DISPATCHING',
]);

const CONTROL_CAPABILITIES: ReadonlySet<string> = new Set([
  'schedule.request',
  'schedule.manage',
]);

const ACTION_BUTTON_LABELS: Readonly<Record<ActionCapability, string>> = {
  START: '启动',
  STOP: '停止',
  START_UNIT_SEQUENCE: '一键启动',
  STOP_UNIT_SEQUENCE: '一键停机',
  RESET_FAULT: '复位故障',
};

const ACTION_AFTER_LABELS: Readonly<Record<ActionCapability, string>> = {
  START: '启动运行',
  STOP: '停止运行',
  START_UNIT_SEQUENCE: '按顺序启动整组',
  STOP_UNIT_SEQUENCE: '按顺序停止整组',
  RESET_FAULT: '清除故障状态',
};

const RUN_STATE_LABELS: Readonly<Record<string, string>> = {
  RUNNING: '运行中',
  STOPPED: '已停止',
  FAULT: '故障停机',
  UNKNOWN: '状态未明',
};

export const STATUS_DISPLAY: Readonly<Record<CommandStatus, { readonly label: string; readonly tone: 'good' | 'bad' | 'warning' | 'neutral' }>> = {
  SUBMITTED: { label: '已提交', tone: 'neutral' },
  VALIDATING: { label: '校验中', tone: 'neutral' },
  AWAITING_APPROVAL: { label: '待审批', tone: 'warning' },
  APPROVED: { label: '已批准', tone: 'neutral' },
  QUEUED: { label: '排队下发', tone: 'neutral' },
  DISPATCHING: { label: '正在下发', tone: 'warning' },
  SUCCEEDED: { label: '已执行', tone: 'good' },
  FAILED: { label: '执行失败', tone: 'bad' },
  REJECTED: { label: '已拒绝', tone: 'bad' },
  CANCELLED: { label: '已撤回', tone: 'neutral' },
  EXPIRED: { label: '已过期', tone: 'neutral' },
  OUTCOME_UNKNOWN: { label: '需现场确认', tone: 'warning' },
};

const CONTROL_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  COMMAND_CONTROL_LOCKED: '站点已切到人工接管或安全退出，或这台设备已上锁，平台现在不下发指令。',
  DEVICE_PRIORITY_LOCKED: '这台设备正由现场检修或更高优先级指令锁定，当前不能下发。',
  COMMAND_PRIORITY_INVALID: '当前指令优先级与控制来源不匹配，请刷新状态后再试。',
  SAFE_SHUTDOWN_PRECONDITION_FAILED: '还有指令正在下发，先等待结束再继续操作。',
  COMMAND_INTERLOCK_BLOCKED: '被水路联锁拦下，对应的主机、水泵或冷却塔状态不符合安全要求。',
  COMMAND_INTERLOCK_UNVERIFIABLE: '暂时读不到联锁设备的运行状态，为保护设备没有下发指令。',
  COMMAND_IDEMPOTENCY_CONFLICT: '这条调节请求已经发过了，请下拉刷新确认结果，避免重复操作。',
  REGISTRY_REVISION_CONFLICT: '刚才有人调整过控制状态，请刷新后再确认一次。',
};

const isActionCapability = (value: string): value is ActionCapability =>
  (ACTION_ORDER as readonly string[]).includes(value);

const isSettingCapability = (value: string): value is SettingCapability =>
  (SETTING_CAPABILITIES as readonly string[]).includes(value);

const metadataRecord = (point: RawTelemetryPointRecord): Readonly<Record<string, unknown>> =>
  point.sourceMetadata && typeof point.sourceMetadata === 'object' ? point.sourceMetadata : {};

const metadataNumber = (meta: Readonly<Record<string, unknown>>, key: string): number | null => {
  const value = meta[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
};

const decimalPlaces = (step: number): number => {
  const text = String(step);
  const dot = text.indexOf('.');
  return dot === -1 ? 0 : Math.min(4, text.length - dot - 1);
};

export const CODE_PATTERN = /^\d{6}$/;

/** Checks IAM capabilities from /api/v1/principal; read-only viewers and missing payloads fail closed. */
export function canSubmitCommands(principal: PrincipalCapabilitiesResponse | null | undefined): boolean {
  const capabilities = principal?.authorization?.capabilities;
  if (!Array.isArray(capabilities)) return false;
  return capabilities.some((capability) => (
    CONTROL_CAPABILITIES.has(capability) || capability.startsWith('command.submit.')
  ));
}

/** True when a command has not yet reached a settled terminal outcome. */
export function isCommandActive(status: CommandStatus): boolean {
  return ACTIVE_COMMAND_STATUSES.has(status);
}

/**
 * Reconciles a previously active command banner during a non-polling refresh (for example after
 * onHide or polling timeout) so settled commands never remain stuck with `active: true`.
 */
export function reconcileActiveCommandStatus(
  previousStatus: PresentedCommandStatus | null,
  openStatus: PresentedCommandStatus | null,
  refreshedCommands: readonly CommandRecord[],
): PresentedCommandStatus | null {
  if (openStatus !== null) return openStatus;
  if (previousStatus === null || !previousStatus.active) return previousStatus;
  const matched = refreshedCommands.find((item) => item.commandId === previousStatus.commandId);
  if (matched) return presentCommandStatus(matched);
  return {
    ...previousStatus,
    label: '状态已更新',
    tone: 'neutral',
    active: false,
    detail: '该指令已离开当前进行队列，请核对设备实时读数或下拉刷新。',
  };
}

export function newIdempotencyKey(prefix = 'mpcmd'): string {
  const stamp = Date.now().toString(36);
  const randA = Math.floor(Math.random() * 0xffffffff).toString(36).padStart(7, '0');
  const randB = Math.floor(Math.random() * 0xffffffff).toString(36).padStart(7, '0');
  return `${prefix}-${stamp}-${randA}${randB}`;
}

export function clampAndRoundSetting(raw: number, setting: Pick<DeviceControlSetting, 'minimum' | 'maximum' | 'step'>): number {
  const clamped = Math.min(setting.maximum, Math.max(setting.minimum, raw));
  const step = setting.step > 0 ? setting.step : 1;
  const snapped = setting.minimum + Math.round((clamped - setting.minimum) / step) * step;
  const bounded = Math.min(setting.maximum, Math.max(setting.minimum, snapped));
  const scale = 10 ** Math.max(decimalPlaces(step), decimalPlaces(setting.minimum));
  return Math.round(bounded * scale) / scale;
}

export function formatSettingValue(value: number, unit: string | null): string {
  return `${formatTelemetryDisplayValue(value)}${unit ? ` ${unit}` : ''}`;
}

export function normalizeTotpCode(raw: string): string {
  return raw.replace(/\D+/g, '').slice(0, 6);
}

function isRelationshipValidAt(rel: RawSiteAssetModel['relationships'][number], at: Date): boolean {
  if (rel.status !== 'ACTIVE') return false;
  const start = new Date(rel.validFrom);
  if (Number.isNaN(start.getTime()) || start > at) return false;
  if (rel.validTo === undefined || rel.validTo === null) return true;
  const end = new Date(rel.validTo);
  return !Number.isNaN(end.getTime()) && end > at;
}

function isRelationshipActive(rel: RawSiteAssetModel['relationships'][number], at: Date): boolean {
  return rel.role === 'CONTROLS' && rel.fromType === 'POINT' && rel.toType === 'ASSET' && isRelationshipValidAt(rel, at);
}

function parseInterlocks(
  meta: Readonly<Record<string, unknown>>,
  deviceCodeById: ReadonlyMap<string, string>,
): InterlockRule[] {
  const raw = meta.interlockChecks;
  if (!Array.isArray(raw)) return [];
  const rules: InterlockRule[] = [];
  for (const entry of raw) {
    if (typeof entry !== 'object' || entry === null) continue;
    const candidate = entry as Record<string, unknown>;
    if (typeof candidate.deviceId !== 'string') continue;
    if (candidate.key !== undefined && candidate.key !== 'run_state') continue;
    if (candidate.expected !== 'RUNNING' && candidate.expected !== 'STOPPED') continue;
    rules.push({
      deviceId: candidate.deviceId,
      deviceCode: deviceCodeById.get(candidate.deviceId) ?? candidate.deviceId,
      expected: candidate.expected,
      reason: typeof candidate.reason === 'string' ? candidate.reason : '',
    });
  }
  return rules;
}

/**
 * Extracts controllable assets and points owned by `deviceId` from the Site asset model.
 * Excludes site-wide EMERGENCY_DO_RELEASE points so single-device UI never exposes plant-wide drop triggers.
 */
export function parseDeviceControlTargets(
  model: RawSiteAssetModel,
  deviceId: string,
  at: Date = new Date(),
): DeviceControlTarget[] {
  const assetsById = new Map(model.assets.map((asset) => [asset.id, asset]));
  const deviceCodeById = new Map(model.devices.map((device) => [device.id, device.code ?? device.displayName]));
  const boundAssetIdsForDevice = (id: string): Set<string> => new Set(
    model.relationships
      .filter((rel) => rel.fromType === 'DEVICE' && rel.fromId === id && rel.toType === 'ASSET' && isRelationshipValidAt(rel, at))
      .map((rel) => rel.toId),
  );
  const directAssets = boundAssetIdsForDevice(deviceId);
  const singleAssetId = directAssets.size === 1 ? [...directAssets][0] : null;
  const targetDeviceIds = new Set<string>([deviceId]);
  if (singleAssetId) {
    for (const candidate of model.devices) {
      if (candidate.id !== deviceId && boundAssetIdsForDevice(candidate.id).has(singleAssetId)) {
        targetDeviceIds.add(candidate.id);
      }
    }
  }
  const activePointsById = new Map(
    model.telemetryPoints
      .filter((point) => targetDeviceIds.has(point.reportingDeviceId) && point.status === 'ACTIVE')
      .map((point) => [point.id, point]),
  );

  const grouped = new Map<string, { controllerDeviceId: string; actions: DeviceControlAction[]; settings: DeviceControlSetting[] }>();
  for (const rel of model.relationships) {
    if (!isRelationshipActive(rel, at)) continue;
    const point = activePointsById.get(rel.fromId);
    if (!point || point.pointType !== 'COMMAND' || point.writable !== true) continue;
    if (singleAssetId && point.reportingDeviceId !== deviceId && rel.toId !== singleAssetId) continue;
    const asset = assetsById.get(rel.toId);
    if (!asset || (asset.status !== undefined && asset.status !== 'ACTIVE')) continue;

    const meta = metadataRecord(point);
    if (meta.actuationMode === 'EMERGENCY_DO_RELEASE') continue;
    const capability = typeof meta.capability === 'string' ? meta.capability : '';
    const bucket = grouped.get(asset.id) ?? { controllerDeviceId: point.reportingDeviceId, actions: [], settings: [] };
    grouped.set(asset.id, bucket);

    if (isActionCapability(capability)) {
      const highRisk = HIGH_RISK_ACTIONS.has(capability);
      bucket.actions.push({
        assetId: asset.id,
        pointId: point.id,
        capability,
        label: point.displayName,
        highRisk,
        requiresCode: highRisk,
        interlocks: parseInterlocks(meta, deviceCodeById),
      });
      continue;
    }

    if (!isSettingCapability(capability)) continue;
    const minimum = metadataNumber(meta, 'minimum');
    const maximum = metadataNumber(meta, 'maximum');
    const parameterKey = meta.parameterKey;
    if (minimum === null || maximum === null || minimum > maximum || typeof parameterKey !== 'string' || parameterKey.length === 0) {
      continue;
    }
    const rawStep = metadataNumber(meta, 'step');
    const step = rawStep !== null && rawStep > 0 ? rawStep : 1;
    const feedbackCode = typeof meta.feedbackPointKey === 'string' ? meta.feedbackPointKey : null;
    const feedback = feedbackCode === null
      ? undefined
      : model.telemetryPoints.find((candidate) => (
        (candidate.reportingDeviceId === point.reportingDeviceId || targetDeviceIds.has(candidate.reportingDeviceId))
        && candidate.pointType !== 'COMMAND'
        && candidate.pointCode === feedbackCode
      ));
    bucket.settings.push({
      assetId: asset.id,
      pointId: point.id,
      capability,
      label: point.displayName,
      parameterKey,
      unit: formatTelemetryUnit(point.unit),
      minimum,
      maximum,
      step,
      feedbackSourceKey: feedback?.sourceKey ?? null,
    });
  }

  return [...grouped.entries()]
    .filter(([, bucket]) => bucket.actions.length > 0 || bucket.settings.length > 0)
    .map(([assetId, bucket]) => {
      const asset = assetsById.get(assetId)!;
      return {
        assetId,
        assetName: asset.displayName,
        assetType: asset.assetType,
        deviceId: bucket.controllerDeviceId,
        actions: [...bucket.actions].sort(
          (left, right) => ACTION_ORDER.indexOf(left.capability) - ACTION_ORDER.indexOf(right.capability),
        ),
        settings: bucket.settings,
      };
    });
}

function activeOpenCommandForTarget(
  target: DeviceControlTarget,
  commands: readonly CommandRecord[],
): CommandRecord | null {
  const pointIds = new Set([
    ...target.actions.map((item) => item.pointId),
    ...target.settings.map((item) => item.pointId),
  ]);
  for (const command of commands) {
    if (!pointIds.has(command.pointId)) continue;
    if (isCommandActive(command.status) || command.status === 'OUTCOME_UNKNOWN') return command;
  }
  return null;
}

function evaluateInterlockReason(
  action: DeviceControlAction,
  runStateByDeviceId: ReadonlyMap<string, string>,
  nameByDeviceId: ReadonlyMap<string, string>,
): string {
  for (const rule of action.interlocks) {
    const state = runStateByDeviceId.get(rule.deviceId);
    if (state === rule.expected) continue;
    if (rule.reason) return `联锁限制：${rule.reason}`;
    const peer = nameByDeviceId.get(rule.deviceId) ?? '联锁设备';
    return rule.expected === 'RUNNING' ? `联锁限制：需先启动${peer}` : `联锁限制：需先停止${peer}`;
  }
  return '';
}

function isActionRelevantForRunState(capability: ActionCapability, device: PlantDevice): string {
  if (device.connection === 'OFFLINE') return '设备当前离线，先排查连接与现场供电再试';
  switch (capability) {
    case 'START':
    case 'START_UNIT_SEQUENCE':
      if (device.runState === 'RUNNING') return '设备已在运行中';
      if (device.runState === 'FAULT') return '设备处于故障状态，请先排除故障并复位';
      if (device.runState !== 'STOPPED') return '暂未确认停机状态，不能远程启动';
      return '';
    case 'STOP':
    case 'STOP_UNIT_SEQUENCE':
      if (device.runState === 'STOPPED') return '设备已停止运行';
      if (device.runState !== 'RUNNING' && device.runState !== 'FAULT') return '暂未确认运行状态，请核对现场';
      return '';
    case 'RESET_FAULT':
      return device.runState === 'FAULT' ? '' : '当前无故障状态需要复位';
  }
}

export function evaluateControlGuards(options: {
  readonly target: DeviceControlTarget;
  readonly device: PlantDevice;
  readonly controlState: SiteControlState | null;
  readonly commands: readonly CommandRecord[];
  readonly runStateByDeviceId?: ReadonlyMap<string, string>;
  readonly nameByDeviceId?: ReadonlyMap<string, string>;
  readonly draftValues?: Readonly<Record<string, number>>;
}): {
  readonly guard: GuardSummary;
  readonly actions: readonly ActionButtonView[];
  readonly settings: readonly SettingRowView[];
  readonly openCommand: PresentedCommandStatus | null;
} {
  const {
    target,
    device,
    controlState,
    commands,
    runStateByDeviceId = new Map<string, string>(),
    nameByDeviceId = new Map<string, string>(),
    draftValues = {},
  } = options;

  const open = activeOpenCommandForTarget(target, commands);
  const openCommand = open ? presentCommandStatus(open) : null;

  let guard: GuardSummary = {
    blocked: false,
    tone: 'good',
    badge: '远控就绪',
    headline: '站点处于远程自动控制',
    detail: '所有操作均需二次确认后下发。',
  };

  if (controlState === null) {
    guard = {
      blocked: true,
      tone: 'warning',
      badge: '状态未读到',
      headline: '暂时读不到站点控制与锁定状态',
      detail: '为防误操作已锁定操作入口，请下拉刷新后再试。',
    };
  } else {
    const lock = controlState.assetLocks.find((item) => item.assetId === target.assetId && item.lockMode !== 'UNLOCKED');
    if (controlState.controlMode === 'MANUAL_OVERRIDE') {
      guard = {
        blocked: true,
        tone: 'warning',
        badge: '人工接管中',
        headline: '站点已切至现场人工接管',
        detail: controlState.reason ? `原因：${controlState.reason}` : '平台当前暂停下发控制指令。',
      };
    } else if (controlState.controlMode === 'SAFE_SHUTDOWN_EXIT') {
      guard = {
        blocked: true,
        tone: 'bad',
        badge: '已安全退出',
        headline: '站点已退出远程控制',
        detail: controlState.reason ? `原因：${controlState.reason}` : '全部操作已交回现场控制柜。',
      };
    } else if (lock) {
      const actor = lock.updatedByName ? `，挂牌人：${lock.updatedByName}` : '';
      const modeName = lock.lockMode === 'MAINTENANCE_LOCK' ? '检修锁定' : '手动锁定';
      guard = {
        blocked: true,
        tone: lock.lockMode === 'MAINTENANCE_LOCK' ? 'bad' : 'warning',
        badge: modeName,
        headline: `设备处于现场${modeName}`,
        detail: `${lock.reason || '现场挂牌保护中'}${actor}，禁止远程调节。`,
      };
    } else if (controlState.activeMaintenanceWindow) {
      guard = {
        blocked: false,
        tone: 'warning',
        badge: '检修窗口',
        headline: `当前处于检修窗口：${controlState.activeMaintenanceWindow.title}`,
        detail: '检修期间仅允许停机和故障复位，禁止启动或修改参数。',
      };
    } else if (device.connection === 'OFFLINE') {
      guard = {
        blocked: true,
        tone: 'bad',
        badge: '设备离线',
        headline: '当前连不上设备通信模块',
        detail: '离线期间无法核对现场读数，请先检查控制柜与供电。',
      };
    }
  }

  const actions: ActionButtonView[] = target.actions.map((action) => {
    let disabledReason = '';
    if (guard.blocked) {
      disabledReason = guard.headline;
    } else if (controlState?.activeMaintenanceWindow && !MAINTENANCE_ALLOWED_ACTIONS.has(action.capability)) {
      disabledReason = '检修窗口内只允许停机或复位';
    } else if (open) {
      disabledReason = '上一条指令尚未结束，请先等待完成';
    } else {
      disabledReason = isActionRelevantForRunState(action.capability, device)
        || evaluateInterlockReason(action, runStateByDeviceId, nameByDeviceId);
    }

    return {
      pointId: action.pointId,
      assetId: action.assetId,
      assetName: target.assetName,
      capability: action.capability,
      label: ACTION_BUTTON_LABELS[action.capability],
      danger: action.capability === 'STOP' || action.capability === 'STOP_UNIT_SEQUENCE',
      disabled: disabledReason.length > 0,
      disabledReason,
      highRisk: action.highRisk,
      requiresCode: action.requiresCode,
    };
  });

  const settings: SettingRowView[] = target.settings.map((setting) => {
    const reading: PlantReading | null = setting.feedbackSourceKey ? device.reading(setting.feedbackSourceKey) : null;
    const currentNumeric = reading !== null && reading.numeric !== null ? reading.numeric : null;
    const currentText = currentNumeric !== null ? formatSettingValue(currentNumeric, setting.unit) : '没读到';
    const rawDraft = draftValues[setting.pointId] ?? currentNumeric ?? setting.minimum;
    const draftValue = clampAndRoundSetting(rawDraft, setting);

    let disabledReason = '';
    if (guard.blocked) {
      disabledReason = guard.headline;
    } else if (controlState?.activeMaintenanceWindow) {
      disabledReason = '检修窗口内禁止修改参数设定';
    } else if (open) {
      disabledReason = '上一条指令尚未结束，请先等待完成';
    }

    return {
      pointId: setting.pointId,
      assetId: setting.assetId,
      assetName: target.assetName,
      capability: setting.capability,
      label: setting.label,
      parameterKey: setting.parameterKey,
      unit: setting.unit,
      minimum: setting.minimum,
      maximum: setting.maximum,
      step: setting.step,
      currentText,
      currentNumeric,
      draftValue,
      draftText: formatTelemetryDisplayValue(draftValue),
      disabled: disabledReason.length > 0,
      disabledReason,
    };
  });

  return { guard, actions, settings, openCommand };
}

export function buildActionConfirmationIntent(options: {
  readonly action: DeviceControlAction;
  readonly assetName: string;
  readonly device: Pick<PlantDevice, 'name' | 'runState' | 'connection'>;
}): PendingControlIntent {
  const { action, assetName, device } = options;
  const stopping = action.capability === 'STOP' || action.capability === 'STOP_UNIT_SEQUENCE';
  const beforeLabel = device.connection === 'OFFLINE' ? '离线' : (RUN_STATE_LABELS[device.runState] ?? '暂无读数');
  return {
    idempotencyKey: newIdempotencyKey(),
    assetId: action.assetId,
    assetName,
    deviceName: device.name,
    pointId: action.pointId,
    capability: action.capability,
    title: `${ACTION_BUTTON_LABELS[action.capability]}「${assetName}」`,
    parameterLabel: action.label || ACTION_BUTTON_LABELS[action.capability],
    beforeValue: beforeLabel,
    afterValue: ACTION_AFTER_LABELS[action.capability],
    parameters: {},
    highRisk: action.highRisk,
    requiresTotp: action.requiresCode,
    acknowledgementLabel: stopping
      ? '我已确认现场人员安全，且停机不影响当前运行负荷'
      : '我已核对现场水路和阀门位置，确认可以下发该操作',
  };
}

export function buildSettingConfirmationIntent(options: {
  readonly setting: DeviceControlSetting;
  readonly assetName: string;
  readonly deviceName: string;
  readonly currentNumeric: number | null;
  readonly targetValue: number;
}): PendingControlIntent {
  const { setting, assetName, deviceName, currentNumeric, targetValue } = options;
  const normalizedTarget = clampAndRoundSetting(targetValue, setting);
  const beforeValue = currentNumeric !== null ? formatSettingValue(currentNumeric, setting.unit) : '没读到';
  const afterValue = formatSettingValue(normalizedTarget, setting.unit);
  return {
    idempotencyKey: newIdempotencyKey(),
    assetId: setting.assetId,
    assetName,
    deviceName,
    pointId: setting.pointId,
    capability: setting.capability,
    title: `调整「${assetName}」参数`,
    parameterLabel: setting.label,
    beforeValue,
    afterValue,
    parameters: { [setting.parameterKey]: normalizedTarget },
    highRisk: false,
    requiresTotp: false,
    acknowledgementLabel: '我已核对目标参数，确认按此设定下发',
  };
}

export function stepUpMessage(reason: string | undefined, hadCodeInput: boolean): { readonly message: string; readonly showMfaSetupHint: boolean } {
  switch (reason) {
    case 'CODE_INVALID':
      return { message: '动态口令不对，请核对认证器里最新的 6 位数字后再试。', showMfaSetupHint: false };
    case 'CODE_REPLAYED':
      return { message: '这个动态口令已经用过了，请等认证器刷新下一组 6 位数字后再试。', showMfaSetupHint: false };
    case 'MFA_NOT_CONFIGURED':
      return { message: '当前账号还没有开通两步验证，请先到网页安全设置完成绑定后再操作。', showMfaSetupHint: true };
    case 'MFA_LOCKED':
      return { message: '动态口令输错次数过多，两步验证已暂时锁定，请稍后再试。', showMfaSetupHint: false };
    default:
      return {
        message: hadCodeInput
          ? '动态口令未能通过核验，请核对认证器后重新输入。'
          : '该控制操作风险较高，请输入认证器里的 6 位动态口令。',
        showMfaSetupHint: false,
      };
  }
}

export function mapCommandSubmitError(error: unknown, hadCodeInput: boolean): CommandSubmitFailure {
  if (!(error instanceof GatewayError)) {
    return {
      kind: 'rejected',
      message: '指令暂时没有发出，请刷新设备状态后再试。',
      showMfaSetupHint: false,
      canRetrySameKey: false,
      shouldRefreshState: false,
    };
  }

  if (error.status === 0 || error.code === 'NETWORK_UNAVAILABLE') {
    return {
      kind: 'network',
      message: '机房信号弱，刚刚没有收到平台答复。请点重试，系统会沿用同一次请求编号，不会重复下发。',
      showMfaSetupHint: false,
      canRetrySameKey: true,
      shouldRefreshState: false,
    };
  }

  if (error.status === 428 || error.code === 'STEP_UP_REQUIRED') {
    const stepUp = stepUpMessage(error.reason, hadCodeInput);
    return {
      kind: 'step-up',
      message: stepUp.message,
      showMfaSetupHint: stepUp.showMfaSetupHint,
      canRetrySameKey: true,
      shouldRefreshState: false,
    };
  }

  if (error.status === 403 || error.code === 'FORBIDDEN' || error.code === 'COMMAND_CAPABILITY_DENIED') {
    return {
      kind: 'forbidden',
      message: '你当前的账号权限不能执行这项控制操作，请联系管理员。',
      showMfaSetupHint: false,
      canRetrySameKey: false,
      shouldRefreshState: true,
    };
  }

  if (error.status === 409) {
    return {
      kind: 'conflict',
      message: CONTROL_ERROR_MESSAGES[error.code] ?? '设备控制状态刚刚发生了变化，已刷新，请确认后再试。',
      showMfaSetupHint: false,
      canRetrySameKey: false,
      shouldRefreshState: true,
    };
  }

  if (error.status >= 500) {
    return {
      kind: 'network',
      message: '控制服务暂时忙，请稍后重试。系统会沿用同一次请求编号，避免重复下发。',
      showMfaSetupHint: false,
      canRetrySameKey: true,
      shouldRefreshState: false,
    };
  }

  return {
    kind: 'rejected',
    message: CONTROL_ERROR_MESSAGES[error.code] ?? '这条指令暂时没有通过平台校验，请核对设备状态后再试。',
    showMfaSetupHint: false,
    canRetrySameKey: false,
    shouldRefreshState: true,
  };
}

export function presentCommandStatus(command: CommandRecord): PresentedCommandStatus {
  const presentation = STATUS_DISPLAY[command.status] ?? { label: '处理中', tone: 'neutral' as const };
  let detail = '';
  switch (command.status) {
    case 'SUBMITTED':
    case 'VALIDATING':
      detail = '平台正在校验权限与联锁条件，请稍候…';
      break;
    case 'AWAITING_APPROVAL':
      detail = '这条指令需要管理员审批通过后才会下发。';
      break;
    case 'APPROVED':
    case 'QUEUED':
      detail = '指令已通过校验，正在排队发往现场网关。';
      break;
    case 'DISPATCHING':
      detail = '指令已发往现场控制柜，正在等待设备反馈结果。';
      break;
    case 'SUCCEEDED':
      detail = '现场已确认执行成功，读数将在几秒内同步更新。';
      break;
    case 'FAILED':
      detail = command.lastErrorMessage || '现场未能完成执行，请先检查设备现场状态。';
      break;
    case 'REJECTED':
      detail = command.outcomeReason === 'DEVICE_PRIORITY_LOCKED'
        ? '设备正由更高优先级指令锁定，本次指令已拦截。'
        : '平台或控制柜未批准执行，请核对设备条件。';
      break;
    case 'CANCELLED':
      detail = command.outcomeReason === 'PRIORITY_PREEMPTED'
        ? '已被更高优先级的锁定或人工接管撤回。'
        : '这条指令已撤回，不会发往现场。';
      break;
    case 'EXPIRED':
      detail = '超过有效等待时间，指令已自动作废。';
      break;
    case 'OUTCOME_UNKNOWN':
      detail = '未收到现场设备答复，请务必前往现场确认设备实际状态。';
      break;
  }
  return {
    commandId: command.commandId,
    status: command.status,
    label: presentation.label,
    tone: presentation.tone,
    active: isCommandActive(command.status),
    detail,
  };
}

export async function fetchSiteControlState(client: ControlGatewayClient, siteId: string): Promise<SiteControlState> {
  return client.get<SiteControlState>(`/api/v1/sites/${encodeURIComponent(siteId)}/control-state`);
}

export async function fetchSiteCommands(client: ControlGatewayClient, siteId: string, limit = 50): Promise<readonly CommandRecord[]> {
  const response = await client.get<{ readonly data: readonly CommandRecord[] }>(
    `/api/v1/commands?siteId=${encodeURIComponent(siteId)}&limit=${limit}`,
  );
  return Array.isArray(response.data) ? response.data : [];
}

export async function fetchCommandById(client: ControlGatewayClient, commandId: string): Promise<CommandRecord> {
  return client.get<CommandRecord>(`/api/v1/commands/${encodeURIComponent(commandId)}`);
}

export type AutoControlExecutionMode = 'CLOSED_LOOP' | 'SHADOW';

export interface AutoControlLeaseRecord {
  readonly id: string;
  readonly status: 'ACTIVE' | 'TRIPPED' | 'REVOKED' | 'EXPIRED';
  readonly expiresAt: string;
  readonly executionMode?: AutoControlExecutionMode;
  readonly supplyTempMinC: number;
  readonly supplyTempMaxC: number;
  readonly pumpFreqMinHz?: number;
  readonly pumpFreqMaxHz?: number;
  readonly fanSpeedMinPct?: number;
  readonly fanSpeedMaxPct?: number;
  readonly manualBackoffUntil?: string | null;
  readonly manualBackoffReason?: string;
}

export interface AutoControlLeaseState {
  readonly tenantId: string;
  readonly siteId: string;
  readonly confirmationMode: 'MANUAL_CONFIRM' | 'AUTO_WITH_LEASE';
  readonly activeLease: AutoControlLeaseRecord | null;
  readonly latestLease?: AutoControlLeaseRecord | null;
}

export interface AutoControlLeaseBanner {
  readonly tone: 'good' | 'warning' | 'neutral';
  readonly badge: string;
  readonly headline: string;
  readonly detail: string;
}

function formatLeaseClockHHmm(iso: string, timezone = 'Asia/Shanghai'): string {
  const ms = Date.parse(iso);
  if (!Number.isFinite(ms)) return '--:--';
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone,
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(new Date(ms));
    const hour = parts.find((part) => part.type === 'hour')?.value;
    const minute = parts.find((part) => part.type === 'minute')?.value;
    if (hour && minute) return `${hour}:${minute}`;
  } catch {
    // Fall back to fixed UTC+8 clock on older runtimes without full Intl TZ data.
  }
  const local = new Date(ms + 8 * 3_600_000);
  return `${String(local.getUTCHours()).padStart(2, '0')}:${String(local.getUTCMinutes()).padStart(2, '0')}`;
}

export async function fetchSiteAutoControlLease(client: ControlGatewayClient, siteId: string): Promise<AutoControlLeaseState | null> {
  try {
    return await client.get<AutoControlLeaseState>(`/api/v1/sites/${encodeURIComponent(siteId)}/optimization/auto-control-lease`);
  } catch {
    return null;
  }
}

export function presentAutoControlLease(
  state: AutoControlLeaseState | null,
  now: Date = new Date(),
  timezone = 'Asia/Shanghai',
): AutoControlLeaseBanner | null {
  const lease = state?.activeLease ?? null;
  if (lease === null || lease.status !== 'ACTIVE') return null;
  const nowMs = now.getTime();
  const expiresMs = Date.parse(lease.expiresAt);
  if (!Number.isFinite(expiresMs) || expiresMs <= nowMs) return null;

  const backoffMs = lease.manualBackoffUntil ? Date.parse(lease.manualBackoffUntil) : Number.NaN;
  if (Number.isFinite(backoffMs) && backoffMs > nowMs && lease.manualBackoffUntil) {
    return {
      tone: 'warning',
      badge: '人工退避保护中',
      headline: 'AI 自动调节已暂停',
      detail: `退避至 ${formatLeaseClockHHmm(lease.manualBackoffUntil, timezone)}，期间不会覆盖现场手动调节`,
    };
  }

  const expiryClock = formatLeaseClockHHmm(lease.expiresAt, timezone);
  const supplyBounds = `供水 ${lease.supplyTempMinC.toFixed(1)}–${lease.supplyTempMaxC.toFixed(1)} °C`;
  if (lease.executionMode === 'SHADOW') {
    return {
      tone: 'neutral',
      badge: '影子跟跑中',
      headline: '策略仅模拟评估，不下发控制',
      detail: `租约有效期至 ${expiryClock} · ${supplyBounds}`,
    };
  }

  const bounds: string[] = [supplyBounds];
  if (lease.pumpFreqMinHz != null && lease.pumpFreqMaxHz != null) {
    bounds.push(`水泵 ${lease.pumpFreqMinHz.toFixed(1)}–${lease.pumpFreqMaxHz.toFixed(1)} Hz`);
  }
  if (lease.fanSpeedMinPct != null && lease.fanSpeedMaxPct != null) {
    bounds.push(`风机 ${lease.fanSpeedMinPct.toFixed(0)}–${lease.fanSpeedMaxPct.toFixed(0)}%`);
  }
  return {
    tone: 'good',
    badge: 'AI 自动托管中',
    headline: '站点处于自动闭环控制',
    detail: `有效期至 ${expiryClock} · ${bounds.join(' · ')}`,
  };
}

export async function dispatchControlCommand(
  client: ControlGatewayClient,
  intent: PendingControlIntent,
  verificationCode?: string,
): Promise<CommandRecord> {
  const body: {
    readonly assetId: string;
    readonly commandPointId: string;
    readonly parameters: Readonly<Record<string, number>>;
    readonly verificationCode?: string;
  } = {
    assetId: intent.assetId,
    commandPointId: intent.pointId,
    parameters: intent.parameters,
    ...(verificationCode ? { verificationCode } : {}),
  };
  return client.post<CommandRecord>('/api/v1/commands', body, {
    'Idempotency-Key': intent.idempotencyKey,
  });
}

/**
 * Serializes submissions per controller instance so rapid double-taps under dim cellar lighting
 * or weak cellular signal cannot dispatch concurrent or duplicate commands.
 */
export function createCommandSubmitMutex() {
  let inFlight = false;
  return {
    isBusy: () => inFlight,
    run: async <T>(work: () => Promise<T>): Promise<{ readonly acquired: false } | { readonly acquired: true; readonly value: T }> => {
      if (inFlight) return { acquired: false };
      inFlight = true;
      try {
        const value = await work();
        return { acquired: true, value };
      } finally {
        inFlight = false;
      }
    },
  };
}

export interface CommandPollingOptions {
  readonly client: ControlGatewayClient;
  readonly commandId: string;
  readonly intervalMs?: number;
  readonly maxAttempts?: number;
  readonly maxTransientFailures?: number;
  readonly onUpdate: (command: CommandRecord, presented: PresentedCommandStatus) => void;
  readonly onTimeout: (last: CommandRecord | null) => void;
  readonly onError: (message: string) => void;
}

/**
 * Gently polls GET /api/v1/commands/{commandId} at 1.5s intervals until settled or cancelled,
 * using bounded retries and clean timer cancellation on page hide/unload.
 */
export function startCommandPolling(options: CommandPollingOptions): { readonly cancel: () => void } {
  const {
    client,
    commandId,
    intervalMs = 1500,
    maxAttempts = 20,
    maxTransientFailures = 3,
    onUpdate,
    onTimeout,
    onError,
  } = options;

  let cancelled = false;
  let attempts = 0;
  let transientFailures = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let lastCommand: CommandRecord | null = null;

  const scheduleNext = () => {
    if (cancelled) return;
    timer = setTimeout(() => {
      void step();
    }, intervalMs);
  };

  const step = async () => {
    if (cancelled) return;
    attempts += 1;
    try {
      const latest = await fetchCommandById(client, commandId);
      if (cancelled) return;
      transientFailures = 0;
      lastCommand = latest;
      const presented = presentCommandStatus(latest);
      onUpdate(latest, presented);
      if (!presented.active) return;
      if (attempts >= maxAttempts) {
        onTimeout(lastCommand);
        return;
      }
      scheduleNext();
    } catch (error) {
      if (cancelled) return;
      if (error instanceof GatewayError && (error.status === 0 || error.status >= 500 || error.code === 'NETWORK_UNAVAILABLE')) {
        transientFailures += 1;
        if (transientFailures <= maxTransientFailures && attempts < maxAttempts) {
          scheduleNext();
          return;
        }
        onError('信号弱，暂时拿不到最新执行进度，可下拉刷新确认状态。');
        return;
      }
      onError('指令进度查询中断，请下拉刷新确认设备状态。');
    }
  };

  scheduleNext();

  return {
    cancel: () => {
      cancelled = true;
      if (timer !== null) {
        clearTimeout(timer);
        timer = null;
      }
    },
  };
}
