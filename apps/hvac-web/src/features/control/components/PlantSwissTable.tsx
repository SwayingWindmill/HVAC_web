import { Play, Square } from 'lucide-react';
import { StatusBadge, statusDotClass } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { deviceStatus, LowConfidenceBadge } from '@/features/operations/realtime/device-presentation';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { STATUS_PRESENTATION } from '../command-presentation';
import {
  buildActionConfirmation,
  evaluateDeviceGates,
  evaluateUnitSequence,
  isSingleLoopCombined,
  orderHydraulicChain,
} from '../control-gates';
import { deviceInlineReadings, unitActivePower, type ControlTarget } from '../control-model';
import {
  DEVICE_PRIORITY_TIER_LABEL,
  LOCK_PRESENTATION,
  MAINTENANCE_BLOCKED,
} from '../control-state';
import { unitHasAbnormality, type CanvasSharedProps } from './PlantTopologyCanvas';

function SwissDeviceTile({
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
  const reading = deviceInlineReadings(target, device, 1)[0];

  const subText = lock && lock.lockMode !== 'UNLOCKED'
    ? LOCK_PRESENTATION[lock.lockMode].label
    : evalRes.openCommand
      ? STATUS_PRESENTATION[evalRes.openCommand.status].label
      : priorityLock
        ? `${DEVICE_PRIORITY_TIER_LABEL[priorityLock.lockedPriority]}锁`
        : evalRes.interlockHintText
          ? evalRes.interlockHintText
          : reading
            ? `${reading.shortLabel} ${reading.reading.display}${reading.reading.unit ?? ''}`
            : (status?.label ?? '暂无数据');

  return (
    <div role="group" aria-label={target.assetName} className="min-w-0">
      <button
        type="button"
        aria-pressed={selected}
        onClick={() => shared.onSelectAsset(target.assetId)}
        className={cn(
          'flex w-full flex-col gap-1 rounded-md border px-2 py-1.5 text-left transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring-soft',
          lock
            ? 'border-destructive/50 bg-destructive-soft/45'
            : evalRes.openCommand || evalRes.blockedInterlocks.length > 0
              ? 'border-warning/55 bg-warning-soft/40'
              : device?.runState === 'STOPPED'
                ? 'border-dashed border-border bg-muted/45'
                : 'border-border bg-card hover:border-input-hover hover:bg-muted/35',
          selected && 'border-[1.5px] border-primary bg-primary-soft ring-2 ring-primary/15',
        )}
      >
        <div className="flex min-w-0 items-center justify-between gap-1">
          <span className="truncate text-xs font-semibold whitespace-nowrap text-foreground">
            {target.assetName}
          </span>
          <span
            className={cn(
              'size-1.5 shrink-0 rounded-full',
              status ? statusDotClass(status.tone) : 'border border-dashed border-muted-foreground',
            )}
            aria-hidden="true"
          />
        </div>
        <span className="truncate text-[11px] text-muted-foreground">{subText}</span>
      </button>
    </div>
  );
}

/**
 * Alternative left-pane tabular track view (`精密横轨表`) lining chiller groups up horizontally
 * for quick multi-unit comparison while driving the exact same right-hand faceplate.
 */
export function PlantSwissTable({ shared }: { readonly shared: CanvasSharedProps }) {
  const combinedSingle = isSingleLoopCombined(shared.units, shared.others);
  const visibleUnits = shared.units.filter((unit) => {
    if (shared.filterMode === 'all') return true;
    return unitHasAbnormality(unit, combinedSingle ? shared.others : [], shared);
  });
  const standaloneOthers = combinedSingle
    ? []
    : shared.others.filter((target) => {
        if (shared.filterMode === 'all') return true;
        const device = shared.devices.get(target.deviceId);
        return Boolean(
          shared.locks.has(target.assetId)
          || shared.priorityLocks.has(target.deviceId)
          || device?.connection === 'OFFLINE'
          || device?.runState === 'FAULT',
        );
      });

  return (
    <div className="flex min-w-0 flex-1 flex-col divide-y divide-border">
      <div className="hidden grid-cols-[112px_88px_minmax(0,1fr)_128px] gap-2 bg-muted/55 px-4 py-2 text-[11px] font-medium text-muted-foreground @xl/canvas:grid @3xl/canvas:grid-cols-[118px_86px_minmax(0,1fr)_134px] @3xl/canvas:gap-2">
        <span>机组与状态</span>
        <span>机组电表</span>
        <span>水路设备（点击在右侧细控）</span>
        <span className="text-right">机组级调度</span>
      </div>

      {visibleUnits.length === 0 && standaloneOthers.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-1.5 px-6 py-12 text-center">
          <p className="text-sm font-semibold">当前没有异常或锁定中的机组</p>
          <p className="text-xs text-muted-foreground">切换到「全部机组」查看所有受控设备</p>
        </div>
      ) : null}

      {visibleUnits.map((unit) => {
        const chainTargets = orderHydraulicChain(unit, combinedSingle ? shared.others : []);
        const device = shared.devices.get(unit.chiller.deviceId);
        const status = device ? deviceStatus(device) : null;
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

        return (
          <section
            key={unit.chiller.assetId}
            aria-label={unit.chiller.assetName}
            className="grid grid-cols-1 items-center gap-3 px-4 py-3 @xl/canvas:grid-cols-[112px_88px_minmax(0,1fr)_128px] @xl/canvas:gap-2 @3xl/canvas:grid-cols-[118px_86px_minmax(0,1fr)_134px] @3xl/canvas:gap-2"
          >
            <div className="flex flex-col gap-1">
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  onClick={() => shared.onSelectAsset(unit.chiller.assetId)}
                  className="truncate text-left text-sm font-bold text-foreground hover:text-primary"
                >
                  {unit.chiller.assetName}
                </button>
                {status ? <StatusBadge tone={status.tone} label={status.label} /> : null}
              </div>
              {openSeq ? (
                <button
                  type="button"
                  className="w-fit rounded-sm outline-none hover:underline"
                  onClick={() => shared.onInspect(openSeq.commandId)}
                >
                  <StatusBadge tone="warning" label={`一键启停 · ${STATUS_PRESENTATION[openSeq.status].label}`} />
                </button>
              ) : null}
            </div>

            <div className="text-xs">
              {power && power.numeric !== null ? (
                <span className="inline-flex items-baseline gap-1 font-semibold tabular-nums">
                  {formatDecimal(power.numeric, 1)} kW
                  {power.lowConfidence ? <LowConfidenceBadge /> : null}
                </span>
              ) : (
                <span className="text-muted-foreground">暂无读数</span>
              )}
            </div>

            <div className="grid grid-cols-2 gap-1.5 @3xl/canvas:grid-cols-4">
              {chainTargets.map((target) => (
                <SwissDeviceTile key={target.assetId} target={target} shared={shared} />
              ))}
            </div>

            <div className="flex justify-end gap-1.5">
              {openSeq ? (
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  className="border-destructive/35 text-destructive-text hover:bg-destructive-soft"
                  disabled={!shared.canManage}
                  onClick={() => shared.onAbort(unit)}
                >
                  中止…
                </Button>
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
                          status?.label ?? '暂无实时数据',
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
                          status?.label ?? '暂无实时数据',
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
          </section>
        );
      })}

      {standaloneOthers.length > 0 ? (
        <section aria-label="其他设备" className="flex flex-col gap-2 px-4 py-3">
          <h3 className="text-xs font-semibold text-muted-foreground">其他设备</h3>
          <div className="grid grid-cols-2 gap-2 @xl/canvas:grid-cols-4">
            {standaloneOthers.map((target) => (
              <SwissDeviceTile key={target.assetId} target={target} shared={shared} />
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}
