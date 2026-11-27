import { Check, Lock, LockOpen, Minus, Play, Plus, RotateCcw, Send, ShieldAlert, Square, TriangleAlert, X } from 'lucide-react';
import { useState } from 'react';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Slider } from '@/components/ui/slider';
import { deviceStatus, ReadingValue } from '@/features/operations/realtime/device-presentation';
import type { PlantDevice } from '@/features/operations/realtime/plant-model';
import { formatCompactTime, formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type { Command } from '../command-api';
import { ACTION_VERB, commandSummary, STATUS_PRESENTATION } from '../command-presentation';
import { buildActionConfirmation, evaluateDeviceGates, formatRange, type ControlGateCheck } from '../control-gates';
import { deviceInlineReadings, type ControlAction, type ControlSetting, type ControlTarget } from '../control-model';
import {
  controlActorLabel,
  DEVICE_PRIORITY_TIER_LABEL,
  formatLockRemaining,
  LOCK_PRESENTATION,
  type ActiveMaintenanceWindow,
  type AssetControlLockState,
  type DevicePriorityLockState,
  type SiteControlMode,
} from '../control-state';
import type { PendingCommand } from './CommandConfirmation';
import type { CommandActions } from './CommandLog';

const ACTION_BUTTON_LABEL: Readonly<Record<ControlAction['capability'], string>> = {
  START: '启动',
  STOP: '停止',
  START_UNIT_SEQUENCE: '一键启动',
  STOP_UNIT_SEQUENCE: '一键停机',
  RESET_FAULT: '复位',
};

const ASSET_ROLE_LABEL: Readonly<Record<string, string>> = {
  CHILLER: '冷机主机',
  CHILLED_WATER_PUMP: '冷冻水泵',
  COOLING_WATER_PUMP: '冷却水泵',
  COOLING_TOWER: '冷却塔',
  ISOLATION_VALVE: '电动隔断阀',
  MOTORIZED_VALVE: '电动调节阀',
};

function GateStatusMarker({ status }: { readonly status: ControlGateCheck['status'] }) {
  if (status === 'pass') {
    return (
      <span className="inline-flex size-4 shrink-0 items-center justify-center rounded-full bg-success-soft text-success-text" aria-hidden="true">
        <Check className="size-2.5" />
      </span>
    );
  }
  if (status === 'warn') {
    return (
      <span className="inline-flex size-4 shrink-0 items-center justify-center rounded-full bg-warning text-primary-foreground" aria-hidden="true">
        <TriangleAlert className="size-2.5" />
      </span>
    );
  }
  return (
    <span className="inline-flex size-4 shrink-0 items-center justify-center rounded-full bg-destructive text-destructive-foreground" aria-hidden="true">
      <X className="size-2.5" />
    </span>
  );
}

function InlineSetpointEditor({
  target,
  setting,
  device,
  canSet,
  blockReason,
  timeZone,
  onCommand,
}: {
  readonly target: ControlTarget;
  readonly setting: ControlSetting;
  readonly device: PlantDevice | undefined;
  readonly canSet: boolean;
  readonly blockReason: string;
  readonly timeZone: string;
  readonly onCommand: (pending: PendingCommand) => void;
}) {
  const reading = setting.feedbackSourceKey && device ? device.reading(setting.feedbackSourceKey) : null;
  const [draft, setDraft] = useState('');
  const digits = setting.step < 1 ? 1 : 0;
  const parsed = draft === '' ? null : Number(draft);
  const steps = parsed === null ? 0 : (parsed - setting.minimum) / setting.step;
  const valid = parsed !== null
    && Number.isFinite(parsed)
    && parsed >= setting.minimum
    && parsed <= setting.maximum
    && Math.abs(steps - Math.round(steps)) < 1e-6;
  const currentNumeric = reading?.state === 'PRESENT' ? reading.numeric : null;
  const sliderValue = Math.min(setting.maximum, Math.max(setting.minimum, valid && parsed !== null ? parsed : (currentNumeric ?? setting.minimum)));

  const nudge = (direction: 1 | -1) => {
    const base = valid && parsed !== null ? parsed : (currentNumeric ?? setting.minimum);
    const rawSteps = (base - setting.minimum) / setting.step;
    const isOnGrid = Math.abs(rawSteps - Math.round(rawSteps)) < 1e-6;
    const nextStepIndex = isOnGrid
      ? Math.round(rawSteps) + direction
      : direction > 0
        ? Math.ceil(rawSteps)
        : Math.floor(rawSteps);
    const next = Math.min(setting.maximum, Math.max(setting.minimum, Number((setting.minimum + nextStepIndex * setting.step).toFixed(6))));
    setDraft(String(Number(next.toFixed(6))));
  };

  const submit = () => {
    if (!canSet || !valid || parsed === null) return;
    onCommand({
      target,
      title: `调整 ${target.assetName} 的${setting.label}？`,
      label: setting.label,
      pointId: setting.pointId,
      parameters: { [setting.parameterKey]: parsed },
      change: { from: reading, to: parsed, unit: setting.unit },
      facts: [
        { label: '可设范围', value: formatRange(setting) },
        { label: '读数时间', value: reading?.sampledAt ? formatCompactTime(reading.sampledAt, timeZone) : '—' },
      ],
    });
  };

  const delta = valid && parsed !== null && currentNumeric !== null ? parsed - currentNumeric : null;

  return (
    <div className="flex flex-col gap-2 rounded-md border border-border bg-muted/45 p-2.5">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="font-semibold text-foreground">{setting.label}</span>
        <span className="text-[11px] text-muted-foreground">可设 {formatRange(setting)}</span>
      </div>

      <Slider
        min={setting.minimum}
        max={setting.maximum}
        step={setting.step}
        value={[sliderValue]}
        disabled={!canSet}
        aria-label={`${setting.label}滑轨`}
        onValueChange={(values) => {
          const next = values[0];
          if (next !== undefined) setDraft(String(Number(next.toFixed(6))));
        }}
      />

      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-1">
          <Button
            type="button"
            variant="outline"
            size="icon-xs"
            aria-label="减小"
            disabled={!canSet}
            onClick={() => nudge(-1)}
          >
            <Minus />
          </Button>
          <Input
            inputMode="decimal"
            aria-label={`${setting.label}目标值`}
            aria-invalid={draft !== '' && !valid}
            disabled={!canSet}
            placeholder={currentNumeric !== null ? formatDecimal(currentNumeric, digits) : '目标值'}
            value={draft}
            onChange={(event) => setDraft(event.target.value.trim())}
            onKeyDown={(event) => {
              if (event.key !== 'Enter') return;
              event.preventDefault();
              if (event.nativeEvent.isComposing || event.keyCode === 229) return;
              submit();
            }}
            className="h-7 w-20 bg-card px-2 text-center font-mono text-xs tabular-nums"
          />
          {setting.unit ? <span className="text-xs text-muted-foreground">{setting.unit}</span> : null}
          <Button
            type="button"
            variant="outline"
            size="icon-xs"
            aria-label="增大"
            disabled={!canSet}
            onClick={() => nudge(1)}
          >
            <Plus />
          </Button>
          {delta !== null && Math.abs(delta) > 1e-6 ? (
            <span className="rounded-sm bg-primary-soft px-1.5 font-mono text-[11px] font-medium text-primary">
              {delta > 0 ? '+' : '−'}{formatDecimal(Math.abs(delta), digits)}
            </span>
          ) : null}
        </div>

        <Button
          type="button"
          size="xs"
          variant={canSet && valid ? 'default' : 'outline'}
          disabled={!canSet || !valid}
          title={!canSet ? blockReason : !valid ? '请输入有效设定值' : undefined}
          onClick={submit}
        >
          <Send data-icon="inline-start" />
          下发设定
        </Button>
      </div>

      {draft !== '' && !valid ? (
        <span className="text-[11px] text-destructive-text">超出可设范围或不是步进的整数倍</span>
      ) : null}
    </div>
  );
}

/**
 * Right-pane 400px precision equipment faceplate: live readings -> 5-gate safety checklist ->
 * open command actions / inline setpoint tuning / start-stop-reset -> LOTO & priority lock ->
 * recent device commands.
 */
export function DeviceFaceplate({
  target,
  unitName,
  device,
  mode,
  maintenance,
  locks,
  priorityLocks,
  commands,
  allTargets,
  runStateByDeviceCode,
  nameByDeviceCode,
  deviceIdByCode,
  labels,
  actions,
  myId,
  timeZone,
  nowMs,
  canManage,
  onSelectAsset,
  onCommand,
  onLock,
  onPriorityLock,
  onInspect,
  onOpenHistory,
}: {
  readonly target: ControlTarget;
  readonly unitName: string;
  readonly device: PlantDevice | undefined;
  readonly mode: SiteControlMode;
  readonly maintenance: ActiveMaintenanceWindow | null;
  readonly locks: ReadonlyMap<string, AssetControlLockState>;
  readonly priorityLocks: ReadonlyMap<string, DevicePriorityLockState>;
  readonly commands: readonly Command[];
  readonly allTargets: readonly ControlTarget[];
  readonly runStateByDeviceCode: ReadonlyMap<string, string>;
  readonly nameByDeviceCode: ReadonlyMap<string, string>;
  readonly deviceIdByCode: ReadonlyMap<string, string>;
  readonly labels: ReadonlyMap<string, { readonly assetName: string; readonly label: string; readonly unit: string | null }>;
  readonly actions: CommandActions;
  readonly myId: string;
  readonly timeZone: string;
  readonly nowMs: number;
  readonly canManage: boolean;
  readonly onSelectAsset: (assetId: string) => void;
  readonly onCommand: (pending: PendingCommand) => void;
  readonly onLock: (target: ControlTarget) => void;
  readonly onPriorityLock: (target: ControlTarget) => void;
  readonly onInspect: (commandId: string) => void;
  readonly onOpenHistory: () => void;
}) {
  const status = device ? deviceStatus(device) : null;
  const lock = locks.get(target.assetId);
  const priorityLock = priorityLocks.get(target.deviceId);
  const evalRes = evaluateDeviceGates({
    target,
    device,
    mode,
    maintenance,
    lock,
    priorityLock,
    commands,
    targets: allTargets,
    runStateByDeviceCode,
    nameByDeviceCode,
    deviceIdByCode,
    myId,
    timeZone,
    nowMs,
  });
  const inlineReadings = deviceInlineReadings(target, device, 4);
  const ownedPointIds = new Set([
    ...target.actions.map((item) => item.pointId),
    ...target.settings.map((setting) => setting.pointId),
  ]);
  const deviceCommands = commands
    .filter((command) => command.deviceId === target.deviceId || ownedPointIds.has(command.pointId))
    .slice(0, 2);
  const action = evalRes.primaryAction;
  const firstReading = inlineReadings[0]?.reading ?? null;

  return (
    <aside aria-label="设备精密操控台" className="flex min-w-0 flex-col divide-y divide-border bg-card">
      <section className="flex flex-col gap-2.5 px-4 py-3">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="truncate text-base leading-6 font-bold tracking-tight">{target.assetName}</h2>
            <p className="text-[11.5px] text-muted-foreground">
              {unitName} · {ASSET_ROLE_LABEL[target.assetType] ?? '受控设备'}
            </p>
          </div>
          {status ? (
            <StatusBadge tone={status.tone} label={status.label} />
          ) : (
            <StatusBadge tone="neutral" label="暂无实时数据" />
          )}
        </div>

        <div className="rounded-md border border-border bg-muted/45 px-3 py-2">
          {inlineReadings.length > 0 ? (
            <dl aria-label={`${target.assetName} 实时读数`} className="flex flex-wrap gap-x-4 gap-y-1.5">
              {inlineReadings.map(({ key, shortLabel, reading }) => (
                <div key={key} className="min-w-[68px]">
                  <dt className="text-[10.5px] text-muted-foreground">{shortLabel}</dt>
                  <dd className="font-mono text-xs font-semibold text-foreground">
                    <ReadingValue reading={reading} />
                  </dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="text-xs text-muted-foreground">仅启停开关量控制，暂无独立传感器读数</p>
          )}
        </div>
      </section>

      <section className="flex flex-col gap-2 px-4 py-3">
        <div className="flex items-center justify-between text-xs">
          <span className="font-semibold text-foreground">五道控制安全闸门</span>
          <span className={cn('font-mono text-[11px] font-semibold', evalRes.passedCount === 5 ? 'text-success-text' : 'text-destructive-text')}>
            {evalRes.passedCount}/5 通过
          </span>
        </div>

        <div className="divide-y divide-border overflow-hidden rounded-md border border-border">
          {evalRes.gates.map((gate) => (
            <div
              key={gate.index}
              className={cn(
                'grid grid-cols-[16px_88px_minmax(0,1fr)_auto] items-center gap-2 px-2.5 py-1.5 text-[11.5px]',
                gate.status === 'fail' && 'bg-destructive-soft/55',
                gate.status === 'warn' && 'bg-warning-soft/55',
              )}
            >
              <GateStatusMarker status={gate.status} />
              <span className="font-medium text-secondary-foreground">{gate.index}. {gate.name}</span>
              <span className="truncate text-foreground" title={gate.detail}>{gate.detail}</span>
              {gate.jumpTargetAssetId && gate.jumpTargetName ? (
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  className="h-5 px-1.5 text-[10.5px]"
                  onClick={() => onSelectAsset(gate.jumpTargetAssetId!)}
                >
                  选中 {gate.jumpTargetName} ›
                </Button>
              ) : <span />}
            </div>
          ))}
        </div>
      </section>

      <section className="flex flex-col gap-2.5 px-4 py-3">
        {evalRes.openCommand ? (
          <div className={cn(
            'flex flex-col gap-2 rounded-md border p-2.5 text-xs',
            evalRes.openCommand.status === 'DISPATCHING'
              ? 'border-primary-line bg-primary-soft'
              : 'border-warning/40 bg-warning-soft/55',
          )}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="font-semibold text-foreground">
                {commandSummary(evalRes.openCommand, labels).title}
              </span>
              <Button type="button" variant="outline" size="xs" onClick={() => onInspect(evalRes.openCommand!.commandId)}>
                经过 ›
              </Button>
            </div>
            <div className="flex items-center gap-1.5">
              {evalRes.openCommand.status === 'AWAITING_APPROVAL' ? (
                <>
                  <Button
                    type="button"
                    size="xs"
                    disabled={actions.busy || !actions.canApprove}
                    onClick={() => actions.onApprove(evalRes.openCommand!)}
                  >
                    批准
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={actions.busy}
                    onClick={() => actions.onCancel(evalRes.openCommand!)}
                  >
                    撤回
                  </Button>
                </>
              ) : evalRes.openCommand.status === 'OUTCOME_UNKNOWN' ? (
                <>
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={actions.busy}
                    onClick={() => actions.onReconcile(evalRes.openCommand!.commandId, 'APPLIED')}
                  >
                    已生效
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={actions.busy}
                    onClick={() => actions.onReconcile(evalRes.openCommand!.commandId, 'NOT_APPLIED')}
                  >
                    没生效
                  </Button>
                </>
              ) : (
                <span className="text-primary">正在执行中，等待设备反馈</span>
              )}
            </div>
          </div>
        ) : null}

        {target.settings.map((setting) => (
          <InlineSetpointEditor
            key={setting.pointId}
            target={target}
            setting={setting}
            device={device}
            canSet={evalRes.canSet}
            blockReason={evalRes.settingBlockReason}
            timeZone={timeZone}
            onCommand={onCommand}
          />
        ))}

        {action ? (
          <div className="flex items-center justify-between gap-2 pt-0.5">
            <span className={cn('text-xs font-medium', evalRes.canAct ? 'text-success-text' : 'text-destructive-text')}>
              {evalRes.canAct
                ? `五道闸门全部绿灯，允许${ACTION_VERB[action.capability]}`
                : `被第 ${evalRes.blockingGateIndex + 1} 道闸门拦截`}
            </span>
            <Button
              type="button"
              size="sm"
              variant={action.capability === 'STOP' ? 'outline' : 'default'}
              className={cn(action.capability === 'STOP' && 'border-destructive/40 text-destructive-text hover:bg-destructive-soft')}
              disabled={!evalRes.canAct}
              title={evalRes.actionBlockReason || undefined}
              onClick={() => {
                onCommand(
                  buildActionConfirmation(
                    target,
                    action,
                    firstReading,
                    status?.label ?? '暂无实时数据',
                    `${ACTION_VERB[action.capability]} ${target.assetName}？`,
                  ),
                );
              }}
            >
              {action.capability === 'START' ? <Play data-icon="inline-start" /> : action.capability === 'RESET_FAULT' ? <RotateCcw data-icon="inline-start" /> : <Square data-icon="inline-start" />}
              {ACTION_BUTTON_LABEL[action.capability]}
            </Button>
          </div>
        ) : null}
      </section>

      <section className="flex flex-col gap-2 px-4 py-3">
        <span className="text-xs font-semibold text-foreground">挂牌与优先级隔离</span>

        <div
          className={cn(
            'flex items-center justify-between gap-2 rounded-md border px-2.5 py-2',
            lock && lock.lockMode !== 'UNLOCKED'
              ? 'border-destructive/35 bg-destructive-soft/45'
              : 'border-border bg-card',
          )}
        >
          <div className="min-w-0">
            <div className="flex items-center gap-1.5 text-xs font-semibold">
              {lock && lock.lockMode !== 'UNLOCKED' ? <Lock className="size-3.5 text-destructive-text" aria-hidden="true" /> : <LockOpen className="size-3.5 text-muted-foreground" aria-hidden="true" />}
              <span>
                {lock && lock.lockMode !== 'UNLOCKED'
                  ? `${LOCK_PRESENTATION[lock.lockMode].label}（${controlActorLabel(lock, myId)}）`
                  : '设备锁定 · 未锁定'}
              </span>
            </div>
            <p className="truncate text-[11px] text-muted-foreground">
              {lock && lock.lockMode !== 'UNLOCKED'
                ? lock.reason || '现场锁定保护中'
                : '现场检修或手动操作时锁定以拦截远程指令'}
            </p>
          </div>
          <Button
            type="button"
            variant="outline"
            size="xs"
            className={cn(lock && lock.lockMode !== 'UNLOCKED' && 'border-destructive/40 text-destructive-text hover:bg-destructive-soft')}
            aria-label={lock && lock.lockMode !== 'UNLOCKED' ? `管理 ${target.assetName} 已锁定` : `管理 ${target.assetName} 锁定`}
            disabled={!canManage}
            title={canManage ? undefined : '你没有锁定设备的权限'}
            onClick={() => onLock(target)}
          >
            {lock && lock.lockMode !== 'UNLOCKED' ? '修改/解牌' : '挂牌上锁'}
          </Button>
        </div>

        <div
          className={cn(
            'flex items-center justify-between gap-2 rounded-md border px-2.5 py-2',
            priorityLock ? 'border-primary-line bg-primary-soft/60' : 'border-border bg-card',
          )}
        >
          <div className="min-w-0">
            <div className="flex items-center gap-1.5 text-xs font-semibold">
              <ShieldAlert className={cn('size-3.5', priorityLock ? 'text-primary' : 'text-muted-foreground')} aria-hidden="true" />
              <span>
                {priorityLock
                  ? `${DEVICE_PRIORITY_TIER_LABEL[priorityLock.lockedPriority]}锁 · ${formatLockRemaining(priorityLock.lockedUntil, nowMs)}`
                  : '优先级仲裁锁 · 未启用'}
              </span>
            </div>
            <p className="truncate text-[11px] text-muted-foreground">
              {priorityLock ? priorityLock.reason : '拦截低优先级 AI 优化与定时日程'}
            </p>
          </div>
          <Button
            type="button"
            variant={priorityLock ? 'default' : 'outline'}
            size="xs"
            disabled={!canManage}
            title={canManage ? undefined : '你没有设置优先级锁定的权限'}
            onClick={() => onPriorityLock(target)}
          >
            {priorityLock ? '管理/解除' : '设置隔离'}
          </Button>
        </div>
      </section>

      <section className="flex flex-col gap-1.5 px-4 py-3">
        <div className="flex items-center justify-between text-xs">
          <span className="font-semibold text-foreground">该设备近期记录</span>
          <Button type="button" variant="ghost" size="xs" className="text-primary" onClick={onOpenHistory}>
            全站历史 ({commands.length}) ›
          </Button>
        </div>
        {deviceCommands.length > 0 ? (
          <div className="flex flex-col gap-1">
            {deviceCommands.map((command) => {
              const summary = commandSummary(command, labels);
              return (
                <button
                  key={command.commandId}
                  type="button"
                  onClick={() => onInspect(command.commandId)}
                  className="flex h-7 items-center justify-between gap-2 rounded-md border border-border bg-muted/45 px-2.5 text-left text-xs hover:border-input-hover"
                >
                  <span className="truncate font-medium text-foreground">{summary.title}</span>
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                    {formatCompactTime(command.createdAt, timeZone, nowMs)} · {STATUS_PRESENTATION[command.status].label}
                  </span>
                </button>
              );
            })}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">暂无该设备最近控制记录</p>
        )}
      </section>
    </aside>
  );
}
