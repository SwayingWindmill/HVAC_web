import { ChevronDown, Link2, Lock, LockOpen, Play, RotateCcw, ShieldAlert, SlidersHorizontal, Square } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { StatusBadge, statusDotClass } from '@/components/status-badge';
import { deviceStatus, LowConfidenceBadge, ReadingValue } from '@/features/operations/realtime/device-presentation';
import type { PlantDevice } from '@/features/operations/realtime/plant-model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { WITHDRAWABLE, type Command } from '../command-api';
import { ACTION_VERB, CODE_AT_SUBMIT, STATUS_PRESENTATION } from '../command-presentation';
import {
  blockingInterlocks,
  deviceInlineReadings,
  unitActivePower,
  type ControlAction,
  type ControlTarget,
  type Interlock,
  type PlantUnit,
} from '../control-model';
import {
  DEVICE_PRIORITY_TIER_LABEL,
  formatLockRemaining,
  LOCK_PRESENTATION,
  MAINTENANCE_BLOCKED,
  MAINTENANCE_PERMITTED,
  MODE_PRESENTATION,
  type ActiveMaintenanceWindow,
  type AssetControlLockState,
  type DevicePriorityLockState,
  type SiteControlMode,
} from '../control-state';
import type { PendingCommand } from './CommandConfirmation';

export interface PlantContext {
  readonly mode: SiteControlMode;
  readonly maintenance: ActiveMaintenanceWindow | null;
  readonly devices: ReadonlyMap<string, PlantDevice>;
  /** Each registered device's run state by device code, which is how interlocks name devices. */
  readonly runStateByDeviceCode: ReadonlyMap<string, string>;
  /** Interlocked device codes in the operator's words: the asset each controls. */
  readonly nameByDeviceCode: ReadonlyMap<string, string>;
  readonly locks: ReadonlyMap<string, AssetControlLockState>;
  readonly priorityLocks: ReadonlyMap<string, DevicePriorityLockState>;
  readonly nowMs: number;
  readonly commands: readonly Command[];
  readonly canLock: boolean;
  readonly onCommand: (pending: PendingCommand) => void;
  readonly onLock: (target: ControlTarget) => void;
  readonly onPriorityLock: (target: ControlTarget) => void;
  /** Aborts a unit's running one-click sequence and stops the unit in order. */
  readonly onAbort: (unit: PlantUnit) => void;
  readonly onSettings: (target: ControlTarget) => void;
  readonly onInspect: (commandId: string) => void;
}

const BUTTON_LABEL = { START: '启动', STOP: '停止', START_UNIT_SEQUENCE: '一键启动', STOP_UNIT_SEQUENCE: '一键停机', RESET_FAULT: '复位' } as const;
const COLUMNS = [
  'grid-cols-1',
  'grid-cols-1',
  'grid-cols-1 @xl/plant-grid:grid-cols-2',
  'grid-cols-1 @xl/plant-grid:grid-cols-2 @4xl/plant-grid:grid-cols-3',
  'grid-cols-1 @xl/plant-grid:grid-cols-2 @4xl/plant-grid:grid-cols-3 @6xl/plant-grid:grid-cols-4',
] as const;

const ICON = { START: Play, STOP: Square, START_UNIT_SEQUENCE: Play, STOP_UNIT_SEQUENCE: Square, RESET_FAULT: RotateCcw } as const;

/** The newest command on any of these points that a person or the plant still has to finish. */
const openOn = (commands: readonly Command[], pointIds: readonly string[]) =>
  commands.find((command) => pointIds.includes(command.pointId) && (WITHDRAWABLE.has(command.status) || command.status === 'DISPATCHING' || command.status === 'OUTCOME_UNKNOWN'));

/** 先开 1 号冷冻泵、1 号冷却塔 / 先停 A 机组: what has to change before the plant lets this through. */
function interlockHint(blocked: readonly Interlock[], names: ReadonlyMap<string, string>): string | null {
  if (blocked.length === 0) return null;
  const list = (expected: Interlock['expected']) => blocked.filter((item) => item.expected === expected).map((item) => names.get(item.deviceCode) ?? '联锁设备');
  const start = list('RUNNING');
  const stop = list('STOPPED');
  return [start.length ? `先开 ${start.join('、')}` : '', stop.length ? `先停 ${stop.join('、')}` : ''].filter(Boolean).join('，');
}

/** The one action that fits the device now: stop a running one, start a stopped one, reset a fault. */
function primaryAction(target: ControlTarget, device: PlantDevice | undefined): ControlAction | undefined {
  const find = (capability: ControlAction['capability']) => target.actions.find((action) => action.capability === capability);
  if (!device || device.connection === 'OFFLINE') return undefined;
  if (device.runState === 'FAULT') return find('RESET_FAULT') ?? find('STOP');
  if (device.runState === 'RUNNING') return find('STOP');
  if (device.runState === 'STOPPED') return find('START');
  return undefined;
}

function confirmation(target: ControlTarget, action: ControlAction, device: PlantDevice | undefined, title: string, steps?: readonly string[]): PendingCommand {
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
      { label: '现在', value: device ? deviceStatus(device).label : '暂无实时数据' },
      ...(risky ? [{ label: '风险', value: '高风险，按审批规则批准后下发' }] : []),
    ],
    requiresCode: risky,
    acknowledgement: risky ? (stopping ? '我已和现场确认可以停' : '我已和现场确认可以开') : undefined,
    submitLabel: `提交${ACTION_VERB[action.capability]}`,
  };
}

function DeviceRow({ target, label, context }: { readonly target: ControlTarget; readonly label: string; readonly context: PlantContext }) {
  const device = context.devices.get(target.deviceId);
  const status = device ? deviceStatus(device) : null;
  const lock = context.locks.get(target.assetId);
  const priorityLock = context.priorityLocks.get(target.deviceId);
  const hasAnyLock = Boolean(lock) || Boolean(priorityLock);
  const isCriticalLock = lock?.lockMode === 'MAINTENANCE_LOCK' || priorityLock?.lockedPriority === 'SAFETY_INTERLOCK';
  const safetyLocked = priorityLock?.lockedPriority === 'SAFETY_INTERLOCK';
  const isWarningLock = !isCriticalLock && lock?.lockMode === 'MANUAL_LOCK';
  const action = primaryAction(target, device);
  const inlineReadings = deviceInlineReadings(target, device);
  const open = openOn(context.commands, target.actions.filter((item) => item.capability !== 'START_UNIT_SEQUENCE' && item.capability !== 'STOP_UNIT_SEQUENCE').map((item) => item.pointId).concat(target.settings.map((setting) => setting.pointId)));
  const blocked = action ? blockingInterlocks(action, context.runStateByDeviceCode) : [];
  const hint = interlockHint(blocked, context.nameByDeviceCode);
  const auto = context.mode === 'REMOTE_AUTO';
  const why = !auto ? `${MODE_PRESENTATION[context.mode].label}中，平台不下发指令`
    : lock ? '已锁定，先解锁'
      : safetyLocked ? '安全联锁锁定中，不能人工操作'
        : !device ? '读不到运行状态，不能远程启停'
          : device.connection === 'OFFLINE' ? '设备离线，不能远程启停'
            : context.maintenance && action && !MAINTENANCE_PERMITTED.has(action.capability) ? MAINTENANCE_BLOCKED
              : open ? '已有指令还没结束'
                : hint ? `水路联锁：${blocked.map((item) => item.reason).filter(Boolean).join('；') || hint}`
                  : !action ? '读不到运行状态，不能远程启停'
                    : '';
  const Icon = action ? ICON[action.capability] : Square;
  const hasActiveLockBadge = Boolean(lock && lock.lockMode !== 'UNLOCKED') || Boolean(priorityLock);
  const hasBadges = hasActiveLockBadge || Boolean(open) || Boolean(auto && !lock && !open && hint);

  return (
    <div
      role="group"
      aria-label={target.assetName}
      className={cn(
        'flex flex-col gap-2 border-b border-divider py-3 last:border-b-0',
        lock && '-mx-3.5 bg-[repeating-linear-gradient(135deg,transparent_0_6px,var(--muted)_6px_12px)] px-3.5',
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-2">
          <span
            className={cn(
              'size-2 shrink-0 rounded-full',
              status ? statusDotClass(status.tone) : 'border border-dashed border-muted-foreground',
            )}
            aria-hidden="true"
          />
          <span className="truncate text-sm font-medium">{label}</span>
          <span
            className={cn(
              'shrink-0 text-xs',
              status?.tone === 'alarm' ? 'font-medium text-destructive-text' : 'text-muted-foreground',
            )}
          >
            {status?.label ?? '暂无实时数据'}
          </span>
        </span>
        {action ? (
          <Button
            variant="outline"
            size="sm"
            className={cn(
              'shrink-0',
              action.capability === 'STOP' && 'hover:border-destructive/40 hover:text-destructive-text',
            )}
            disabled={why !== ''}
            title={why || undefined}
            onClick={() => context.onCommand(confirmation(target, action, device, `${ACTION_VERB[action.capability]} ${target.assetName}？`))}
          >
            <Icon data-icon="inline-start" />
            {BUTTON_LABEL[action.capability]}
          </Button>
        ) : null}
      </div>

      {inlineReadings.length > 0 ? (
        <dl
          aria-label={`${target.assetName} 实时读数`}
          className="flex flex-wrap items-baseline gap-x-3.5 gap-y-1 pl-4 text-xs"
        >
          {inlineReadings.map(({ key, shortLabel, reading }) => (
            <div key={key} className="inline-flex items-baseline gap-1 whitespace-nowrap">
              <dt className="text-muted-foreground">{shortLabel}</dt>
              <dd className="font-medium text-foreground">
                <ReadingValue reading={reading} />
              </dd>
            </div>
          ))}
        </dl>
      ) : null}

      {hasBadges ? (
        <div className="flex min-w-0 flex-wrap items-center gap-1.5 pl-4 text-xs">
          {lock && lock.lockMode !== 'UNLOCKED' ? (
            <StatusBadge
              tone={lock.lockMode === 'MAINTENANCE_LOCK' ? 'destructive' : 'warning'}
              label={LOCK_PRESENTATION[lock.lockMode].label}
            />
          ) : null}
          {priorityLock ? (
            <span title={`${priorityLock.lockedByName ?? '系统规则'}：${priorityLock.reason}`}>
              <StatusBadge
                tone={priorityLock.lockedPriority === 'SAFETY_INTERLOCK' ? 'destructive' : 'info'}
                label={`${DEVICE_PRIORITY_TIER_LABEL[priorityLock.lockedPriority]} · ${formatLockRemaining(priorityLock.lockedUntil, context.nowMs)}`}
              />
            </span>
          ) : null}
          {open ? (
            <button
              type="button"
              className="rounded-sm outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring-soft"
              onClick={() => context.onInspect(open.commandId)}
            >
              <StatusBadge tone="warning" label={STATUS_PRESENTATION[open.status].label} />
            </button>
          ) : null}
          {auto && !lock && !open && hint ? (
            <span className="inline-flex items-center gap-1 text-warning-text">
              <Link2 className="size-3 shrink-0" aria-hidden="true" />
              {hint}
            </span>
          ) : null}
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-1.5 pl-4">
        {target.settings.length > 0 ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            aria-label={`调整 ${target.assetName} 的设定值`}
            title={!auto || lock || safetyLocked || !device || device.connection === 'OFFLINE' ? why : context.maintenance ? MAINTENANCE_BLOCKED : undefined}
            disabled={!auto || Boolean(lock) || safetyLocked || !device || device.connection === 'OFFLINE' || context.maintenance !== null}
            onClick={() => context.onSettings(target)}
          >
            <SlidersHorizontal data-icon="inline-start" />
            设定值
          </Button>
        ) : null}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant={hasAnyLock ? 'outline' : 'ghost'}
              size="sm"
              className={cn(
                !hasAnyLock && 'border-border/60 text-secondary-foreground hover:border-border hover:bg-muted',
                isCriticalLock && 'border-destructive/40 bg-destructive-soft/55 text-destructive-text hover:bg-destructive-soft hover:text-destructive-text',
                isWarningLock && 'border-warning/50 bg-warning-soft/65 text-warning-text hover:bg-warning-soft hover:text-warning-text',
                hasAnyLock && !isCriticalLock && !isWarningLock && 'border-primary/40 bg-primary-soft text-primary hover:bg-primary-soft',
              )}
              aria-label={hasAnyLock ? `管理 ${target.assetName} 已锁定` : `管理 ${target.assetName} 锁定`}
              title={context.canLock ? undefined : '你没有锁定设备的权限'}
              disabled={!context.canLock}
            >
              {hasAnyLock ? <Lock data-icon="inline-start" /> : <LockOpen data-icon="inline-start" />}
              {hasAnyLock ? '已锁定' : '锁定'}
              <ChevronDown data-icon="inline-end" className="opacity-65" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-64">
            <DropdownMenuLabel>安全保护与锁定</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="h-auto items-start py-2"
              onSelect={() => context.onLock(target)}
            >
              {lock ? <Lock className="mt-0.5 text-foreground" /> : <LockOpen className="mt-0.5 text-muted-foreground" />}
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <div className="flex items-center gap-1.5">
                  <span className="font-medium text-foreground">
                    {lock ? '解除或修改设备锁定' : '设备锁定'}
                  </span>
                  {lock && lock.lockMode !== 'UNLOCKED' ? (
                    <span
                      className={cn(
                        'ml-auto shrink-0 rounded-sm px-1.5 py-0.5 text-[11px] leading-none font-medium',
                        lock.lockMode === 'MAINTENANCE_LOCK'
                          ? 'bg-destructive-soft text-destructive-text'
                          : 'bg-warning-soft text-warning-text',
                      )}
                    >
                      {LOCK_PRESENTATION[lock.lockMode].label}
                    </span>
                  ) : null}
                </div>
                <span className="text-xs text-muted-foreground">
                  {lock ? '恢复平台下发或切换锁机类型' : '人工或检修锁机，暂停远程指令'}
                </span>
              </div>
            </DropdownMenuItem>
            <DropdownMenuItem
              className="h-auto items-start py-2"
              onSelect={() => context.onPriorityLock(target)}
            >
              <ShieldAlert className={cn('mt-0.5', priorityLock ? 'text-primary' : 'text-muted-foreground')} />
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <div className="flex items-center gap-1.5">
                  <span className="font-medium text-foreground">
                    {priorityLock ? '管理优先级仲裁锁' : '优先级仲裁锁'}
                  </span>
                  {priorityLock ? (
                    <span
                      className={cn(
                        'ml-auto shrink-0 rounded-sm px-1.5 py-0.5 text-[11px] leading-none font-medium',
                        priorityLock.lockedPriority === 'SAFETY_INTERLOCK'
                          ? 'bg-destructive-soft text-destructive-text'
                          : 'bg-primary-soft text-primary',
                      )}
                    >
                      {DEVICE_PRIORITY_TIER_LABEL[priorityLock.lockedPriority]}
                    </span>
                  ) : null}
                </div>
                <span className="text-xs text-muted-foreground">
                  {priorityLock
                    ? formatLockRemaining(priorityLock.lockedUntil, context.nowMs)
                    : '限定控制来源优先级与锁定时长'}
                </span>
              </div>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
}

function UnitColumn({ unit, context }: { readonly unit: PlantUnit; readonly context: PlantContext }) {
  const { chiller, members } = unit;
  const device = context.devices.get(chiller.deviceId);
  const status = device ? deviceStatus(device) : null;
  const power = unitActivePower(unit.powerMeterDeviceId ? context.devices.get(unit.powerMeterDeviceId) : undefined);
  const sequence = (capability: 'START_UNIT_SEQUENCE' | 'STOP_UNIT_SEQUENCE') => chiller.actions.find((action) => action.capability === capability);
  const startSeq = sequence('START_UNIT_SEQUENCE');
  const stopSeq = sequence('STOP_UNIT_SEQUENCE');
  const everyone = [chiller, ...members];
  const locked = everyone.some((target) => context.locks.has(target.assetId));
  const safetyLocked = everyone.some((target) => context.priorityLocks.get(target.deviceId)?.lockedPriority === 'SAFETY_INTERLOCK');
  const openSeq = openOn(context.commands, [startSeq?.pointId, stopSeq?.pointId].filter((id): id is string => Boolean(id)));
  const openMember = openOn(
    context.commands,
    everyone.flatMap((target) => [
      ...target.actions.map((item) => item.pointId),
      ...target.settings.map((setting) => setting.pointId),
    ]),
  );
  const why = context.mode !== 'REMOTE_AUTO' ? `${MODE_PRESENTATION[context.mode].label}中，平台不下发指令`
    : locked ? '这组有设备锁定，不能整组启停'
      : safetyLocked ? '这组有设备处于安全联锁锁定，不能整组启停'
        : !device ? '读不到主机状态，不能整组启停'
          : device.connection === 'OFFLINE' ? '主机离线，不能整组启停'
            : openSeq ? '这组已有一键启停指令还没结束'
              : openMember ? '这组已有设备指令还没结束'
                : '';
  const running = device?.runState === 'RUNNING';
  const stopped = device?.runState === 'STOPPED';
  const startOrder = [...members.map((member) => member.assetName), '主机'];
  const stopOrder = ['主机', ...[...members].reverse().map((member) => member.assetName)];
  return (
    <section aria-label={chiller.assetName} className="flex min-w-0 flex-col gap-2.5 border-r border-b border-divider px-3.5 pt-3.5 pb-2.5">
      <header className="flex min-w-0 items-center gap-2">
        <h3 className="truncate text-title font-semibold">{chiller.assetName}</h3>
        {status ? <StatusBadge tone={status.tone} label={status.label} /> : <span className="text-xs text-muted-foreground">暂无实时数据</span>}
        {power && power.numeric !== null ? (
          <span className="ml-auto inline-flex shrink-0 items-baseline text-xs font-medium tabular-nums">
            <span className="mr-1 font-normal text-muted-foreground">电表</span>
            {formatDecimal(power.numeric, 1)} kW
            {power.lowConfidence ? <LowConfidenceBadge /> : null}
          </span>
        ) : (
          <span className="ml-auto shrink-0 text-xs text-muted-foreground">电表 暂无读数</span>
        )}
      </header>
      {startSeq || stopSeq ? (
        <div className="grid grid-cols-2 gap-1.5">
          <Button
            variant="outline"
            size="sm"
            disabled={!startSeq || why !== '' || running || context.maintenance !== null}
            title={why || (running ? '主机已在运行' : context.maintenance ? MAINTENANCE_BLOCKED : undefined)}
            onClick={() => startSeq && context.onCommand(confirmation(chiller, startSeq, device, `一键启动 ${chiller.assetName}？`, startOrder))}
          >
            <Play data-icon="inline-start" />一键启动
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="hover:border-destructive/40 hover:text-destructive-text"
            disabled={!stopSeq || why !== '' || stopped}
            title={why || (stopped ? '主机已停' : undefined)}
            onClick={() => stopSeq && context.onCommand(confirmation(chiller, stopSeq, device, `一键停机 ${chiller.assetName}？`, stopOrder))}
          >
            <Square data-icon="inline-start" />一键停机
          </Button>
        </div>
      ) : null}
      {openSeq ? (
        <div className="flex items-center justify-between gap-2">
          <button type="button" className="rounded-sm text-xs outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring-soft" onClick={() => context.onInspect(openSeq.commandId)}>
            <StatusBadge tone="warning" label={`一键启停 · ${STATUS_PRESENTATION[openSeq.status].label}`} />
          </button>
          <Button
            variant="ghost"
            size="xs"
            className="text-destructive-text hover:text-destructive-text"
            disabled={!context.canLock}
            title={context.canLock ? '撤回这组没下发的指令，按顺序停机' : '你没有停机的权限'}
            onClick={() => context.onAbort(unit)}
          >
            中止…
          </Button>
        </div>
      ) : null}
      <div className="flex flex-col border-t border-divider">
        <DeviceRow target={chiller} label="主机" context={context} />
        {members.map((member) => <DeviceRow key={member.assetId} target={member} label={member.assetName} context={context} />)}
      </div>
    </section>
  );
}

/** Each chiller with its pumps and tower as one column, then any asset no chiller names. */
export function PlantUnitGrid({ units, others, context }: {
  readonly units: readonly PlantUnit[];
  readonly others: readonly ControlTarget[];
  readonly context: PlantContext;
}) {
  const columns = units.length + (others.length > 0 ? 1 : 0);
  return (
    <div className="@container/plant-grid overflow-hidden">
      <div className={cn('-mr-px -mb-px grid', COLUMNS[Math.min(columns, 4)])}>
        {units.map((unit) => <UnitColumn key={unit.chiller.assetId} unit={unit} context={context} />)}
        {others.length > 0 ? (
          <section aria-label="其他设备" className="flex min-w-0 flex-col gap-2.5 border-r border-b border-divider px-3.5 pt-3.5 pb-2.5">
            <header className="flex h-5 items-center"><h3 className="text-title font-semibold">{units.length > 0 ? '其他设备' : '设备'}</h3></header>
            <div className="flex flex-col border-t border-divider">
              {others.map((target) => <DeviceRow key={target.assetId} target={target} label={target.assetName} context={context} />)}
            </div>
          </section>
        ) : null}
      </div>
    </div>
  );
}
