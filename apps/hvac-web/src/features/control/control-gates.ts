import type {
  ActiveMaintenanceWindow,
  AssetControlLockState,
  DevicePriorityLockState,
  SiteControlMode,
} from '../../api/generated/platformGateway.gen.ts';
import { formatCompactTime, formatDecimal } from '../../lib/operator-format.ts';
import type { PlantDevice, PlantReading } from '../operations/realtime/plant-model.ts';
import type { Command } from './command-api.ts';
import { ACTION_VERB, CODE_AT_SUBMIT, STATUS_PRESENTATION } from './command-presentation.ts';
import {
  controlActorLabel,
  LOCK_PRESENTATION,
  MAINTENANCE_BLOCKED,
  MAINTENANCE_PERMITTED,
  MODE_PRESENTATION,
  WITHDRAWABLE,
} from './control-constants.ts';
import {
  blockingInterlocks,
  type ControlAction,
  type ControlSetting,
  type ControlTarget,
  type Interlock,
  type PlantUnit,
} from './control-model.ts';
import { DEVICE_PRIORITY_TIER_LABEL, formatLockRemaining } from './control-priority-lock.ts';
import type { PendingCommand } from './components/CommandConfirmation.tsx';

export type GateStatus = 'pass' | 'warn' | 'fail';

export interface ControlGateCheck {
  readonly index: 1 | 2 | 3 | 4 | 5;
  readonly name: string;
  readonly status: GateStatus;
  readonly detail: string;
  readonly jumpTargetAssetId?: string;
  readonly jumpTargetName?: string;
}

export interface DeviceGateEvaluation {
  readonly primaryAction: ControlAction | undefined;
  readonly openCommand: Command | undefined;
  readonly blockedInterlocks: readonly Interlock[];
  readonly interlockHintText: string | null;
  readonly gates: readonly [
    ControlGateCheck,
    ControlGateCheck,
    ControlGateCheck,
    ControlGateCheck,
    ControlGateCheck,
  ];
  readonly passedCount: number;
  readonly canAct: boolean;
  readonly canSet: boolean;
  readonly blockingGateIndex: number;
  readonly actionBlockReason: string;
  readonly settingBlockReason: string;
}

export type GateDeviceInput = Pick<PlantDevice, 'deviceId' | 'connection' | 'runState' | 'latestSampleAt'>;

export interface EvaluateDeviceGatesInput {
  readonly target: ControlTarget;
  readonly device: GateDeviceInput | undefined;
  readonly mode: SiteControlMode;
  readonly maintenance: ActiveMaintenanceWindow | null;
  readonly lock: AssetControlLockState | undefined;
  readonly priorityLock: DevicePriorityLockState | undefined;
  readonly commands: readonly Command[];
  readonly targets: readonly ControlTarget[];
  readonly runStateByDeviceCode: ReadonlyMap<string, string>;
  readonly nameByDeviceCode: ReadonlyMap<string, string>;
  readonly deviceIdByCode: ReadonlyMap<string, string>;
  readonly myId: string;
  readonly timeZone: string;
  readonly nowMs: number;
}

export const formatRange = (setting: ControlSetting): string => {
  const digits = setting.step < 1 ? 1 : 0;
  return `${formatDecimal(setting.minimum, 1)} – ${formatDecimal(setting.maximum, 1)}${setting.unit ? ` ${setting.unit}` : ''} · 步进 ${formatDecimal(setting.step, digits)}`;
};

/** The newest active command on any of these points that a person or the plant still has to finish. */
export function findOpenCommand(commands: readonly Command[], pointIds: readonly string[]): Command | undefined {
  return commands.find(
    (command) =>
      pointIds.includes(command.pointId)
      && (WITHDRAWABLE.has(command.status) || command.status === 'DISPATCHING' || command.status === 'OUTCOME_UNKNOWN'),
  );
}

/** 先开 1 号冷冻泵、1 号冷却塔 / 先停 A 机组: what has to change before the plant lets this through. */
export function interlockHint(blocked: readonly Interlock[], names: ReadonlyMap<string, string>): string | null {
  if (blocked.length === 0) return null;
  const list = (expected: Interlock['expected']) =>
    blocked.filter((item) => item.expected === expected).map((item) => names.get(item.deviceCode) ?? '联锁设备');
  const start = list('RUNNING');
  const stop = list('STOPPED');
  return [start.length ? `先开 ${start.join('、')}` : '', stop.length ? `先停 ${stop.join('、')}` : '']
    .filter(Boolean)
    .join('，');
}

/** The one action that fits the device now: stop a running one, start a stopped one, reset a fault. */
export function primaryAction(
  target: Pick<ControlTarget, 'actions'>,
  device: Pick<PlantDevice, 'connection' | 'runState'> | undefined,
): ControlAction | undefined {
  const find = (capability: ControlAction['capability']) =>
    target.actions.find((action) => action.capability === capability);
  if (!device || device.connection === 'OFFLINE') return undefined;
  if (device.runState === 'FAULT') return find('RESET_FAULT') ?? find('STOP');
  if (device.runState === 'RUNNING') return find('STOP');
  if (device.runState === 'STOPPED') return find('START');
  return undefined;
}

export function buildActionConfirmation(
  target: ControlTarget,
  action: ControlAction,
  currentReading: PlantReading | null,
  statusLabel: string,
  title: string,
  steps?: readonly string[],
): PendingCommand {
  const stopping = action.capability === 'STOP' || action.capability === 'STOP_UNIT_SEQUENCE';
  const risky = CODE_AT_SUBMIT.has(action.capability);
  return {
    target,
    title,
    label: action.label,
    pointId: action.pointId,
    parameters: {},
    steps,
    facts: [
      { label: '现在', value: currentReading?.state === 'PRESENT' ? `${statusLabel}（${currentReading.display}${currentReading.unit ? ` ${currentReading.unit}` : ''}）` : statusLabel },
      ...(risky ? [{ label: '风险', value: '高风险，按审批规则批准后下发' }] : []),
    ],
    requiresCode: risky,
    acknowledgement: risky ? (stopping ? '我已和现场确认可以停' : '我已和现场确认可以开') : undefined,
    submitLabel: `提交${ACTION_VERB[action.capability]}`,
  };
}

export function evaluateDeviceGates(input: EvaluateDeviceGatesInput): DeviceGateEvaluation {
  const {
    target,
    device,
    mode,
    maintenance,
    lock,
    priorityLock,
    commands,
    targets,
    runStateByDeviceCode,
    nameByDeviceCode,
    deviceIdByCode,
    myId,
    timeZone,
    nowMs,
  } = input;

  const action = primaryAction(target, device);
  const nonSeqPointIds = target.actions
    .filter((item) => item.capability !== 'START_UNIT_SEQUENCE' && item.capability !== 'STOP_UNIT_SEQUENCE')
    .map((item) => item.pointId)
    .concat(target.settings.map((setting) => setting.pointId));
  const openCommand = findOpenCommand(commands, nonSeqPointIds);
  const blockedInterlocks = action ? blockingInterlocks(action, runStateByDeviceCode) : [];
  const interlockHintText = interlockHint(blockedInterlocks, nameByDeviceCode);

  const g1Pass = mode === 'REMOTE_AUTO';
  const gate1: ControlGateCheck = {
    index: 1,
    name: '控制权边界',
    status: g1Pass ? 'pass' : 'fail',
    detail: g1Pass ? '远程自动控制生效中' : `${MODE_PRESENTATION[mode].label}中，平台不下发指令`,
  };

  const activeAssetLockMode = lock && lock.lockMode !== 'UNLOCKED' ? lock.lockMode : null;
  const activeAssetLock = activeAssetLockMode ? lock : undefined;
  const safetyLocked = priorityLock?.lockedPriority === 'SAFETY_INTERLOCK';
  const lockDetail = activeAssetLock && activeAssetLockMode
    ? `${LOCK_PRESENTATION[activeAssetLockMode].label} · ${controlActorLabel(activeAssetLock, myId)}${activeAssetLock.updatedAt ? ` ${formatCompactTime(activeAssetLock.updatedAt, timeZone, nowMs)}` : ''}${activeAssetLock.reason ? ` · ${activeAssetLock.reason}` : ''}`
    : safetyLocked
      ? '安全联锁锁定中，不能人工操作'
      : priorityLock
        ? `${DEVICE_PRIORITY_TIER_LABEL[priorityLock.lockedPriority]}锁（${formatLockRemaining(priorityLock.lockedUntil, nowMs)}）`
        : '未挂牌锁定';
  const gate2: ControlGateCheck = {
    index: 2,
    name: '设备锁定保护',
    status: activeAssetLock || safetyLocked ? 'fail' : 'pass',
    detail: lockDetail,
  };

  const maintenanceBlocksAction = Boolean(maintenance && action && !MAINTENANCE_PERMITTED.has(action.capability));
  const gate3: ControlGateCheck = !maintenance
    ? { index: 3, name: '计划检修窗口', status: 'pass', detail: '当前无检修窗口限制' }
    : maintenanceBlocksAction
      ? { index: 3, name: '计划检修窗口', status: 'fail', detail: `「${maintenance.title}」禁开机与调温` }
      : { index: 3, name: '计划检修窗口', status: 'warn', detail: `「${maintenance.title}」仅放行停机与复位` };

  const sampleClock = device?.latestSampleAt ? formatCompactTime(device.latestSampleAt, timeZone, nowMs) : null;
  const gate4: ControlGateCheck = !device
    ? { index: 4, name: '通信与指令态', status: 'fail', detail: '读不到运行状态，不能远程启停' }
    : device.connection === 'OFFLINE'
      ? { index: 4, name: '通信与指令态', status: 'fail', detail: '设备离线，不能远程启停' }
      : openCommand
        ? { index: 4, name: '通信与指令态', status: 'warn', detail: `已有「${STATUS_PRESENTATION[openCommand.status].label}」指令` }
        : { index: 4, name: '通信与指令态', status: 'pass', detail: sampleClock ? `在线 · ${sampleClock}` : '在线新鲜' };

  const firstBlocked = blockedInterlocks[0];
  const jumpDeviceId = firstBlocked ? deviceIdByCode.get(firstBlocked.deviceCode) : undefined;
  const jumpTarget = jumpDeviceId ? targets.find((candidate) => candidate.deviceId === jumpDeviceId) : undefined;
  const gate5: ControlGateCheck = blockedInterlocks.length > 0
    ? {
        index: 5,
        name: '水路联锁前提',
        status: 'fail',
        detail: interlockHintText ?? '前置水路条件未满足',
        jumpTargetAssetId: jumpTarget?.assetId,
        jumpTargetName: jumpTarget?.assetName,
      }
    : { index: 5, name: '水路联锁前提', status: 'pass', detail: '水路联锁条件已满足' };

  const gates: DeviceGateEvaluation['gates'] = [gate1, gate2, gate3, gate4, gate5];
  const passedCount = gates.filter((gate) => gate.status === 'pass').length;

  const actionBlockers = [
    gate1.status === 'fail' ? { idx: 0, text: gate1.detail } : null,
    gate2.status === 'fail' ? { idx: 1, text: activeAssetLock ? '已锁定，先解锁' : gate2.detail } : null,
    maintenanceBlocksAction ? { idx: 2, text: MAINTENANCE_BLOCKED } : null,
    !device || device.connection === 'OFFLINE' || openCommand
      ? { idx: 3, text: openCommand ? '已有指令还没结束' : gate4.detail }
      : null,
    blockedInterlocks.length > 0
      ? {
          idx: 4,
          text: `水路联锁：${blockedInterlocks.map((item) => item.reason).filter(Boolean).join('；') || interlockHintText || '前置条件未满足'}`,
        }
      : null,
    !action ? { idx: 3, text: '读不到运行状态，不能远程启停' } : null,
  ].filter((entry): entry is { idx: number; text: string } => entry !== null);

  const canAct = Boolean(action) && actionBlockers.length === 0;
  const blockingGateIndex = actionBlockers[0]?.idx ?? -1;
  const actionBlockReason = actionBlockers[0]?.text ?? '';

  const settingBlockReason = !g1Pass
    ? gate1.detail
    : activeAssetLock
      ? '已锁定，先解锁'
      : safetyLocked
        ? '安全联锁锁定中，不能人工操作'
        : !device || device.connection === 'OFFLINE'
          ? gate4.detail
          : maintenance
            ? MAINTENANCE_BLOCKED
            : '';

  return {
    primaryAction: action,
    openCommand,
    blockedInterlocks,
    interlockHintText,
    gates,
    passedCount,
    canAct,
    canSet: settingBlockReason === '',
    blockingGateIndex,
    actionBlockReason,
    settingBlockReason,
  };
}

const HYDRAULIC_FLOW_ORDER: readonly string[] = [
  'COOLING_TOWER',
  'COOLING_WATER_PUMP',
  'CHILLER',
  'CHILLED_WATER_PUMP',
];

function hydraulicFlowRank(assetType: string): number {
  const rank = HYDRAULIC_FLOW_ORDER.indexOf(assetType);
  return rank === -1 ? HYDRAULIC_FLOW_ORDER.length : rank;
}

const SINGLE_LOOP_MEMBER_TYPES = new Set([
  'COOLING_TOWER',
  'COOLING_WATER_PUMP',
  'CHILLED_WATER_PUMP',
]);

/** Single-unit plants without compiled interlockChecks still have CT/CWP/CHWP that belong to the sole loop. */
export function isSingleLoopCombined(units: readonly PlantUnit[], others: readonly ControlTarget[]): boolean {
  return (
    units.length === 1
    && units[0].members.length === 0
    && others.length > 0
    && others.every((target) => SINGLE_LOOP_MEMBER_TYPES.has(target.assetType))
  );
}

export interface UnitSequenceEvaluation {
  readonly startSeq: ControlAction | undefined;
  readonly stopSeq: ControlAction | undefined;
  readonly openSeq: Command | undefined;
  readonly why: string;
  readonly running: boolean;
  readonly stopped: boolean;
  readonly startOrder: readonly string[];
  readonly stopOrder: readonly string[];
}

export function evaluateUnitSequence(input: {
  readonly unit: PlantUnit;
  readonly device: Pick<PlantDevice, 'connection' | 'runState'> | undefined;
  readonly mode: SiteControlMode;
  readonly locks: ReadonlyMap<string, AssetControlLockState>;
  readonly priorityLocks: ReadonlyMap<string, DevicePriorityLockState>;
  readonly commands: readonly Command[];
}): UnitSequenceEvaluation {
  const { unit, device, mode, locks, priorityLocks, commands } = input;
  const everyone = [unit.chiller, ...unit.members];
  const locked = everyone.some((target) => locks.has(target.assetId));
  const safetyLocked = everyone.some(
    (target) => priorityLocks.get(target.deviceId)?.lockedPriority === 'SAFETY_INTERLOCK',
  );
  const startSeq = unit.chiller.actions.find((action) => action.capability === 'START_UNIT_SEQUENCE');
  const stopSeq = unit.chiller.actions.find((action) => action.capability === 'STOP_UNIT_SEQUENCE');
  const openSeq = findOpenCommand(
    commands,
    [startSeq?.pointId, stopSeq?.pointId].filter((id): id is string => Boolean(id)),
  );
  const openMember = findOpenCommand(
    commands,
    everyone.flatMap((target) => [
      ...target.actions.map((item) => item.pointId),
      ...target.settings.map((setting) => setting.pointId),
    ]),
  );
  const why = mode !== 'REMOTE_AUTO'
    ? `${MODE_PRESENTATION[mode].label}中，平台不下发指令`
    : locked
      ? '这组有设备锁定，不能整组启停'
      : safetyLocked
        ? '这组有设备处于安全联锁锁定，不能整组启停'
        : !device
          ? '读不到主机状态，不能整组启停'
          : device.connection === 'OFFLINE'
            ? '主机离线，不能整组启停'
            : openSeq
              ? '这组已有一键启停指令还没结束'
              : openMember
                ? '这组已有设备指令还没结束'
                : '';

  return {
    startSeq,
    stopSeq,
    openSeq,
    why,
    running: device?.runState === 'RUNNING',
    stopped: device?.runState === 'STOPPED',
    startOrder: [...unit.members.map((member) => member.assetName), '主机'],
    stopOrder: ['主机', ...[...unit.members].reverse().map((member) => member.assetName)],
  };
}

/** Arranges one chiller unit in physical water-flow order from tower -> cooling pump -> chiller -> chilled pump. */
export function orderHydraulicChain(
  unit: PlantUnit,
  supplementalOthers: readonly ControlTarget[] = [],
): readonly ControlTarget[] {
  return [unit.chiller, ...unit.members, ...supplementalOthers].sort(
    (left, right) =>
      hydraulicFlowRank(left.assetType) - hydraulicFlowRank(right.assetType)
      || left.assetName.localeCompare(right.assetName, 'zh-CN'),
  );
}
