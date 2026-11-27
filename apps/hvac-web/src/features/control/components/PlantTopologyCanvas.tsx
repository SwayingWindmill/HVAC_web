import { Fragment } from 'react';
import { Fan, Link2, Play, SlidersHorizontal, Snowflake, Square, Waves } from 'lucide-react';
import { StatusBadge, statusDotClass } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { deviceStatus, LowConfidenceBadge, ReadingValue } from '@/features/operations/realtime/device-presentation';
import type { HydraulicSchematicModel } from '@/features/operations/realtime/hydraulic-schematic-model';
import type { PlantDevice } from '@/features/operations/realtime/plant-model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type { Command } from '../command-api';
import { STATUS_PRESENTATION } from '../command-presentation';
import {
  buildActionConfirmation,
  evaluateDeviceGates,
  evaluateUnitSequence,
  formatRange,
  isSingleLoopCombined,
  orderHydraulicChain,
} from '../control-gates';
import { deviceInlineReadings, unitActivePower, type ControlTarget, type PlantUnit } from '../control-model';
import {
  DEVICE_PRIORITY_TIER_LABEL,
  formatLockRemaining,
  LOCK_PRESENTATION,
  MAINTENANCE_BLOCKED,
  type ActiveMaintenanceWindow,
  type AssetControlLockState,
  type DevicePriorityLockState,
  type SiteControlMode,
} from '../control-state';
import type { PendingCommand } from './CommandConfirmation';

export interface CanvasSharedProps {
  readonly units: readonly PlantUnit[];
  readonly others: readonly ControlTarget[];
  readonly targets: readonly ControlTarget[];
  readonly devices: ReadonlyMap<string, PlantDevice>;
  readonly hydraulic: HydraulicSchematicModel;
  readonly mode: SiteControlMode;
  readonly maintenance: ActiveMaintenanceWindow | null;
  readonly locks: ReadonlyMap<string, AssetControlLockState>;
  readonly priorityLocks: ReadonlyMap<string, DevicePriorityLockState>;
  readonly commands: readonly Command[];
  readonly runStateByDeviceCode: ReadonlyMap<string, string>;
  readonly nameByDeviceCode: ReadonlyMap<string, string>;
  readonly deviceIdByCode: ReadonlyMap<string, string>;
  readonly myId: string;
  readonly timeZone: string;
  readonly nowMs: number;
  readonly canManage: boolean;
  readonly selectedAssetId: string | null;
  readonly filterMode: 'all' | 'abnormal';
  readonly onSelectAsset: (assetId: string) => void;
  readonly onCommand: (pending: PendingCommand) => void;
  readonly onAbort: (unit: PlantUnit) => void;
  readonly onInspect: (commandId: string) => void;
}

const NODE_ICON_MAP: Readonly<Record<string, typeof Snowflake>> = {
  CHILLER: Snowflake,
  COOLING_TOWER: Fan,
  COOLING_WATER_PUMP: Waves,
  CHILLED_WATER_PUMP: Waves,
};

type GateSharedContext = Pick<
  CanvasSharedProps,
  | 'devices'
  | 'mode'
  | 'maintenance'
  | 'locks'
  | 'priorityLocks'
  | 'commands'
  | 'targets'
  | 'runStateByDeviceCode'
  | 'nameByDeviceCode'
  | 'deviceIdByCode'
  | 'myId'
  | 'timeZone'
  | 'nowMs'
>;

function targetHasAbnormality(
  target: ControlTarget,
  shared: GateSharedContext,
): boolean {
  const device = shared.devices.get(target.deviceId);
  if (device && (device.connection === 'OFFLINE' || device.runState === 'FAULT')) return true;
  const lock = shared.locks.get(target.assetId);
  const priorityLock = shared.priorityLocks.get(target.deviceId);
  if (lock || priorityLock) return true;
  const evalRes = evaluateDeviceGates({ ...shared, target, device, lock, priorityLock });
  return Boolean(evalRes.openCommand || evalRes.blockedInterlocks.length > 0);
}

export function unitHasAbnormality(
  unit: PlantUnit,
  supplementalTargets: readonly ControlTarget[],
  shared: GateSharedContext,
): boolean {
  const everyone = [unit.chiller, ...unit.members, ...supplementalTargets];
  return everyone.some((target) => targetHasAbnormality(target, shared));
}

function PipeLink({
  left,
  right,
  devices,
  tone,
}: {
  readonly left: ControlTarget;
  readonly right: ControlTarget;
  readonly devices: ReadonlyMap<string, PlantDevice>;
  readonly tone: 'condenser' | 'chilled';
}) {
  const leftDevice = devices.get(left.deviceId);
  const rightDevice = devices.get(right.deviceId);
  const active = leftDevice?.runState === 'RUNNING' && rightDevice?.runState === 'RUNNING';
  const mismatch =
    (leftDevice?.runState === 'STOPPED' && rightDevice?.runState === 'RUNNING')
    || (leftDevice?.runState === 'RUNNING' && rightDevice?.runState === 'STOPPED');
  return (
    <div className="hidden h-6 w-3.5 shrink-0 items-center justify-center @3xl/canvas:flex" aria-hidden="true">
      <div
        className={cn(
          'h-0.5 w-full',
          active
            ? tone === 'condenser'
              ? 'bg-chart-2'
              : 'bg-primary'
            : mismatch
              ? 'border-t-2 border-dashed border-warning'
              : 'border-t-2 border-dashed border-border',
        )}
      />
    </div>
  );
}

function TopologyDeviceNode({
  target,
  shared,
}: {
  readonly target: ControlTarget;
  readonly shared: CanvasSharedProps;
}) {
  const device = shared.devices.get(target.deviceId);
  const status = device ? deviceStatus(device) : null;
  const lock = shared.locks.get(target.assetId);
  const priorityLock = shared.priorityLocks.get(target.deviceId);
  const evalRes = evaluateDeviceGates({ ...shared, target, device, lock, priorityLock });
  const selected = shared.selectedAssetId === target.assetId;
  const maxInline = target.assetType === 'CHILLER' ? 4 : 2;
  const readings = deviceInlineReadings(target, device, maxInline);
  const Icon = NODE_ICON_MAP[target.assetType] ?? SlidersHorizontal;
  const primarySetting = target.settings[0];
  const settingReading = primarySetting?.feedbackSourceKey && device
    ? device.reading(primarySetting.feedbackSourceKey)
    : null;
  const stopped = device?.runState === 'STOPPED';

  return (
    <div role="group" aria-label={target.assetName} className="min-w-0 flex-1">
      <button
        type="button"
        aria-pressed={selected}
        onClick={() => shared.onSelectAsset(target.assetId)}
        className={cn(
          'flex h-full w-full flex-col justify-between gap-1.5 rounded-md border px-2 py-2 text-left transition-[border-color,background-color,box-shadow] duration-100 outline-none focus-visible:ring-3 focus-visible:ring-ring-soft',
          lock
            ? 'border-destructive/50 bg-destructive-soft/45'
            : evalRes.openCommand || evalRes.blockedInterlocks.length > 0
              ? 'border-warning/55 bg-warning-soft/40'
              : stopped
                ? 'border-dashed border-border bg-muted/45 text-muted-foreground'
                : 'border-border bg-card hover:border-input-hover hover:bg-muted/35',
          selected && 'border-[1.5px] border-primary bg-primary-soft ring-2 ring-primary/15',
        )}
      >
        <div className="flex min-w-0 items-center justify-between gap-1.5">
          <span className="flex min-w-0 items-center gap-1.5">
            <span
              className={cn(
                'size-2 shrink-0 rounded-full',
                status ? statusDotClass(status.tone) : 'border border-dashed border-muted-foreground',
              )}
              aria-hidden="true"
            />
            <Icon className="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="truncate text-xs font-semibold text-foreground">{target.assetName}</span>
          </span>
          <span className="shrink-0 text-[11px] text-muted-foreground">{status?.label ?? '暂无数据'}</span>
        </div>

        <div className="min-h-[18px] pl-3.5 text-[11px]">
          {readings.length > 0 ? (
            <dl className="flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
              {readings.map(({ key, shortLabel, reading }) => (
                <div key={key} className="inline-flex items-baseline gap-1 whitespace-nowrap">
                  <dt className="text-muted-foreground">{shortLabel}</dt>
                  <dd className="font-mono font-medium text-foreground">
                    <ReadingValue reading={reading} />
                  </dd>
                </div>
              ))}
            </dl>
          ) : (
            <span className="text-muted-foreground">{status?.label ?? '开关量受控'}</span>
          )}
        </div>

        <div className="flex min-h-[18px] flex-wrap items-center gap-1 pl-3.5 text-[10.5px]">
          {lock && lock.lockMode !== 'UNLOCKED' ? (
            <StatusBadge
              tone={lock.lockMode === 'MAINTENANCE_LOCK' ? 'destructive' : 'warning'}
              label={LOCK_PRESENTATION[lock.lockMode].label}
              className="h-4 px-1.5 text-[10px]"
            />
          ) : evalRes.openCommand ? (
            <StatusBadge
              tone={evalRes.openCommand.status === 'DISPATCHING' ? 'info' : 'warning'}
              label={STATUS_PRESENTATION[evalRes.openCommand.status].label}
              className="h-4 px-1.5 text-[10px]"
            />
          ) : priorityLock ? (
            <StatusBadge
              tone={priorityLock.lockedPriority === 'SAFETY_INTERLOCK' ? 'destructive' : 'info'}
              label={`${DEVICE_PRIORITY_TIER_LABEL[priorityLock.lockedPriority]} · ${formatLockRemaining(priorityLock.lockedUntil, shared.nowMs)}`}
              className="h-4 px-1.5 text-[10px]"
            />
          ) : shared.mode === 'REMOTE_AUTO' && evalRes.interlockHintText ? (
            <span className="inline-flex items-center gap-1 text-warning-text">
              <Link2 className="size-3 shrink-0" aria-hidden="true" />
              {evalRes.interlockHintText}
            </span>
          ) : primarySetting ? (
            <span className="text-primary">
              可调 {settingReading?.state === 'PRESENT' ? `${settingReading.display}${settingReading.unit ?? ''}` : primarySetting.label}
            </span>
          ) : (
            <span className="text-muted-foreground">{device?.runState === 'RUNNING' ? '正常受控' : '停机待命'}</span>
          )}
        </div>
      </button>
    </div>
  );
}

/**
 * Default left-pane P&ID hydraulic loop view: two vertical condenser/chilled header buses flanking
 * horizontal chiller chains (`Cooling Tower -> CWP -> Chiller -> CHWP`), plus combined setpoint
 * overview cards on single-unit VFD sites.
 */
export function PlantTopologyCanvas({ shared }: { readonly shared: CanvasSharedProps }) {
  const combinedSingle = isSingleLoopCombined(shared.units, shared.others);
  const visibleUnits = shared.units.filter((unit) => {
    if (shared.filterMode === 'all') return true;
    return unitHasAbnormality(unit, combinedSingle ? shared.others : [], shared);
  });
  const standaloneOthers = combinedSingle
    ? []
    : shared.others.filter((target) => {
        if (shared.filterMode === 'all') return true;
        return targetHasAbnormality(target, shared);
      });
  const allAdjustableTargets = shared.targets.filter((target) => target.settings.length > 0);

  return (
    <div className="grid min-h-0 flex-1 grid-cols-1 @xl/canvas:grid-cols-[68px_minmax(0,1fr)_68px] @3xl/canvas:grid-cols-[76px_minmax(0,1fr)_76px]">
      <div
        aria-label="冷却水总管"
        className="hidden flex-col items-center justify-between gap-2 border-r border-border bg-muted/45 px-1 py-3.5 @xl/canvas:flex"
      >
        <span className="text-[10.5px] font-bold whitespace-nowrap text-chart-2">冷却水总管</span>
        <div className="flex flex-col items-center text-center">
          <span className="text-[10px] text-muted-foreground">出塔</span>
          <ReadingValue reading={shared.hydraulic.condenser.leavingTowerTemp} className="text-xs font-bold" />
        </div>
        <div
          className={cn(
            'relative flex min-h-12 w-[3px] flex-1 items-center justify-center rounded-full',
            shared.hydraulic.coolingFlowActive ? 'bg-chart-2' : 'bg-border',
          )}
          aria-hidden="true"
        >
          <span
            className={cn(
              'inline-flex size-[18px] items-center justify-center rounded-full border border-border bg-card text-[10px] font-bold',
              shared.hydraulic.coolingFlowActive ? 'text-chart-2' : 'text-muted-foreground',
            )}
          >
            ↓
          </span>
        </div>
        <div className="flex flex-col items-center text-center">
          <span className="text-[10px] text-muted-foreground">回塔</span>
          <ReadingValue reading={shared.hydraulic.condenser.enteringTowerTemp} className="text-xs font-bold" />
        </div>
      </div>

      <div className="flex min-w-0 flex-col divide-y divide-border">
        {visibleUnits.length === 0 && standaloneOthers.length === 0 ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-1.5 px-6 py-12 text-center">
            <p className="text-sm font-semibold">当前没有异常或锁定中的机组</p>
            <p className="text-xs text-muted-foreground">切换到「全部机组」查看所有受控设备</p>
          </div>
        ) : null}

        {visibleUnits.map((unit) => {
          const chainTargets = orderHydraulicChain(unit, combinedSingle ? shared.others : []);
          const device = shared.devices.get(unit.chiller.deviceId);
          const power = unitActivePower(
            unit.powerMeterDeviceId ? shared.devices.get(unit.powerMeterDeviceId) : undefined,
          );
          const {
            startSeq,
            stopSeq,
            openSeq,
            why: seqWhy,
            running,
            stopped,
            startOrder,
            stopOrder,
          } = evaluateUnitSequence({
            unit,
            device,
            mode: shared.mode,
            locks: shared.locks,
            priorityLocks: shared.priorityLocks,
            commands: shared.commands,
          });
          const chillerStatus = device ? deviceStatus(device) : null;

          return (
            <section
              key={unit.chiller.assetId}
              aria-label={unit.chiller.assetName}
              className={cn(
                'flex flex-col gap-2.5 bg-card px-4 py-3',
                !combinedSingle && 'flex-1 justify-center',
                stopped && !openSeq && 'bg-muted/20',
              )}
            >
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
                <button
                  type="button"
                  onClick={() => shared.onSelectAsset(unit.chiller.assetId)}
                  className="inline-flex items-center gap-2 text-sm font-bold tracking-tight hover:text-primary"
                >
                  <span
                    className={cn(
                      'size-2 shrink-0 rounded-full',
                      chillerStatus ? statusDotClass(chillerStatus.tone) : 'border border-dashed border-muted-foreground',
                    )}
                    aria-hidden="true"
                  />
                  <span>{unit.chiller.assetName}</span>
                </button>

                {power && power.numeric !== null ? (
                  <span className="inline-flex items-baseline gap-1 text-xs font-semibold tabular-nums">
                    <span className="font-normal text-muted-foreground">电表</span>
                    {formatDecimal(power.numeric, 1)} kW
                    {power.lowConfidence ? <LowConfidenceBadge /> : null}
                  </span>
                ) : (
                  <span className="text-xs text-muted-foreground">电表 暂无读数</span>
                )}

                <div className="ml-auto flex flex-wrap items-center gap-1.5">
                  {openSeq ? (
                    <>
                      <button
                        type="button"
                        className="rounded-sm outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring-soft"
                        onClick={() => shared.onInspect(openSeq.commandId)}
                      >
                        <StatusBadge tone="warning" label={`一键启停 · ${STATUS_PRESENTATION[openSeq.status].label}`} />
                      </button>
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        className="border-destructive/35 text-destructive-text hover:bg-destructive-soft"
                        disabled={!shared.canManage}
                        title={shared.canManage ? '撤回这组没下发的指令，按顺序停机' : '你没有停机的权限'}
                        onClick={() => shared.onAbort(unit)}
                      >
                        中止…
                      </Button>
                    </>
                  ) : startSeq || stopSeq ? (
                    <>
                      <Button
                        type="button"
                        variant={!startSeq || seqWhy !== '' || running || shared.maintenance !== null ? 'outline' : 'default'}
                        size="xs"
                        disabled={!startSeq || seqWhy !== '' || running || shared.maintenance !== null}
                        title={seqWhy || (running ? '主机已在运行' : shared.maintenance ? MAINTENANCE_BLOCKED : undefined)}
                        onClick={() => {
                          if (!startSeq) return;
                          shared.onCommand(
                            buildActionConfirmation(
                              unit.chiller,
                              startSeq,
                              null,
                              chillerStatus?.label ?? '暂无实时数据',
                              `一键启动 ${unit.chiller.assetName}？`,
                              startOrder,
                            ),
                          );
                        }}
                      >
                        <Play data-icon="inline-start" />
                        一键启动
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        className="hover:border-destructive/40 hover:text-destructive-text"
                        disabled={!stopSeq || seqWhy !== '' || stopped}
                        title={seqWhy || (stopped ? '主机已停' : undefined)}
                        onClick={() => {
                          if (!stopSeq) return;
                          shared.onCommand(
                            buildActionConfirmation(
                              unit.chiller,
                              stopSeq,
                              null,
                              chillerStatus?.label ?? '暂无实时数据',
                              `一键停机 ${unit.chiller.assetName}？`,
                              stopOrder,
                            ),
                          );
                        }}
                      >
                        <Square data-icon="inline-start" />
                        一键停机
                      </Button>
                    </>
                  ) : null}
                </div>
              </div>

              <div className="grid grid-cols-1 gap-2 @xl/canvas:grid-cols-2 @3xl/canvas:flex @3xl/canvas:items-stretch @3xl/canvas:gap-0">
                {chainTargets.map((target, index) => {
                  const prev = chainTargets[index - 1];
                  const pipeTone: 'condenser' | 'chilled' =
                    prev?.assetType === 'CHILLER' || target.assetType === 'CHILLED_WATER_PUMP'
                      ? 'chilled'
                      : 'condenser';
                  return (
                    <Fragment key={target.assetId}>
                      {prev ? (
                        <PipeLink left={prev} right={target} devices={shared.devices} tone={pipeTone} />
                      ) : null}
                      <TopologyDeviceNode target={target} shared={shared} />
                    </Fragment>
                  );
                })}
              </div>
            </section>
          );
        })}

        {standaloneOthers.length > 0 ? (
          <section aria-label={shared.units.length > 0 ? '其他设备' : '设备'} className="flex flex-col gap-2.5 bg-card px-4 py-3">
            <h3 className="text-xs font-semibold text-muted-foreground">
              {shared.units.length > 0 ? '其他设备' : '设备'}
            </h3>
            <div className="grid grid-cols-1 gap-2 @xl/canvas:grid-cols-2 @4xl/canvas:grid-cols-4">
              {standaloneOthers.map((target) => (
                <TopologyDeviceNode key={target.assetId} target={target} shared={shared} />
              ))}
            </div>
          </section>
        ) : null}

        {visibleUnits.length <= 1 && allAdjustableTargets.length > 0 ? (
          <section aria-label="可调设定值总览" className="flex flex-1 flex-col gap-2.5 bg-muted/45 px-4 py-3.5">
            <div className="flex items-center justify-between gap-2 text-xs">
              <span className="font-semibold text-foreground">可调设定值总览（点击节点在右侧微调）</span>
              <span className="text-[11px] text-muted-foreground">{allAdjustableTargets.length} 台设备支持参数调节</span>
            </div>
            <div className="grid grid-cols-1 gap-2 @xl/canvas:grid-cols-2 @4xl/canvas:grid-cols-4">
              {allAdjustableTargets.map((target) => {
                const setting = target.settings[0];
                if (!setting) return null;
                const device = shared.devices.get(target.deviceId);
                const reading = setting.feedbackSourceKey && device ? device.reading(setting.feedbackSourceKey) : null;
                const selected = shared.selectedAssetId === target.assetId;
                return (
                  <button
                    key={target.assetId}
                    type="button"
                    onClick={() => shared.onSelectAsset(target.assetId)}
                    className={cn(
                      'flex flex-col gap-1 rounded-md border px-3 py-2 text-left transition-colors',
                      selected ? 'border-primary bg-primary-soft' : 'border-border bg-card hover:border-input-hover',
                    )}
                  >
                    <span className="text-[11px] text-muted-foreground">{target.assetName}</span>
                    <span className="text-xs font-semibold text-foreground">{setting.label}</span>
                    <span className="text-base font-bold text-primary">
                      <ReadingValue reading={reading} />
                    </span>
                    <span className="text-[10.5px] text-muted-foreground">{formatRange(setting)}</span>
                  </button>
                );
              })}
            </div>
          </section>
        ) : null}
      </div>

      <div
        aria-label="冷冻水总管"
        className="hidden flex-col items-center justify-between gap-2 border-l border-border bg-muted/45 px-1 py-3.5 @xl/canvas:flex"
      >
        <span className="text-[10.5px] font-bold whitespace-nowrap text-primary">冷冻水总管</span>
        <div className="flex flex-col items-center text-center">
          <span className="text-[10px] text-muted-foreground">供水</span>
          <ReadingValue reading={shared.hydraulic.chilled.supplyTemp} className="text-xs font-bold" />
        </div>
        <div
          className={cn(
            'relative flex min-h-12 w-[3px] flex-1 items-center justify-center rounded-full',
            shared.hydraulic.chilledFlowActive ? 'bg-primary' : 'bg-border',
          )}
          aria-hidden="true"
        >
          <span
            className={cn(
              'inline-flex size-[18px] items-center justify-center rounded-full border border-border bg-card text-[10px] font-bold',
              shared.hydraulic.chilledFlowActive ? 'text-primary' : 'text-muted-foreground',
            )}
          >
            ↑
          </span>
        </div>
        <div className="flex flex-col items-center text-center">
          <span className="text-[10px] text-muted-foreground">回水</span>
          <ReadingValue reading={shared.hydraulic.chilled.returnTemp} className="text-xs font-bold" />
        </div>
      </div>
    </div>
  );
}
