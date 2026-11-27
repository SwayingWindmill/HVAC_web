import { Link } from '@tanstack/react-router';
import { History, Power, RefreshCw, TriangleAlert } from 'lucide-react';
import type { SiteAutoControlLeaseState } from '@/api/generated/platformGateway.gen';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { formatClock, LiveIndicator, ReadingValue } from '@/features/operations/realtime/device-presentation';
import type { HydraulicSchematicModel } from '@/features/operations/realtime/hydraulic-schematic-model';
import type { PlantDevice, PlantView } from '@/features/operations/realtime/plant-model';
import type { PlantLiveMode } from '@/features/operations/realtime/use-realtime-plant';
import { formatCompactTime, formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { unitActivePower, type PlantUnit } from '../control-model';
import {
  controlActorLabel,
  MODE_ORDER,
  MODE_PRESENTATION,
  type SiteControlMode,
  type SiteControlState,
} from '../control-state';
import { ModeGlyph } from './ControlBoundary';

const BAR_TONE: Readonly<Record<SiteControlMode, string>> = {
  REMOTE_AUTO: '',
  MANUAL_OVERRIDE: 'bg-warning-soft',
  SAFE_SHUTDOWN_EXIT: 'bg-destructive-soft',
};

const UNIT_RULER_COLORS = ['bg-primary', 'bg-chart-2', 'bg-warning', 'bg-chart-3'] as const;
const UNIT_SWATCH_COLORS = ['bg-primary', 'bg-chart-2', 'bg-warning', 'bg-chart-3'] as const;

interface UnitPowerShare {
  readonly id: string;
  readonly name: string;
  readonly kw: number;
  readonly barClass: string;
  readonly swatchClass: string;
}

function readUnitKw(
  unit: PlantUnit,
  devices: ReadonlyMap<string, PlantDevice>,
): number | null {
  const meterPower = unitActivePower(
    unit.powerMeterDeviceId ? devices.get(unit.powerMeterDeviceId) : undefined,
  );
  if (meterPower && meterPower.numeric !== null) return Math.max(0, meterPower.numeric);
  const chillerDevice = devices.get(unit.chiller.deviceId);
  const chillerPower = chillerDevice?.reading('chiller.power');
  if (
    chillerPower
    && chillerPower.state === 'PRESENT'
    && (chillerPower.current || chillerPower.lowConfidence)
    && chillerPower.numeric !== null
  ) {
    return Math.max(0, chillerPower.numeric);
  }
  return null;
}

/**
 * Layer 1 precision cockpit strip across the top of `/operations/control`:
 * authority boundary + mode switch on the left, proportional unit load ruler & dual headers in
 * the middle, and live sync timestamp + history drawer + shutdown action on the right.
 */
export function ControlCockpitStrip({
  state,
  units,
  devices,
  plant,
  hydraulic,
  liveMode,
  commandCount,
  lockCount,
  pendingCount,
  myId,
  timeZone,
  canManage,
  autoControlLease,
  onPickMode,
  onShutdown,
  onOpenHistory,
  onRefresh,
}: {
  readonly state: SiteControlState;
  readonly units: readonly PlantUnit[];
  readonly devices: ReadonlyMap<string, PlantDevice>;
  readonly plant: PlantView;
  readonly hydraulic: HydraulicSchematicModel;
  readonly liveMode: PlantLiveMode;
  readonly commandCount: number;
  readonly lockCount: number;
  readonly pendingCount: number;
  readonly myId: string;
  readonly timeZone: string;
  readonly canManage: boolean;
  readonly autoControlLease?: SiteAutoControlLeaseState;
  readonly onPickMode: (mode: SiteControlMode) => void;
  readonly onShutdown: () => void;
  readonly onOpenHistory: () => void;
  readonly onRefresh: () => void;
}) {
  const presentation = MODE_PRESENTATION[state.controlMode];
  const runningUnits = units.filter((u) => devices.get(u.chiller.deviceId)?.runState === 'RUNNING').length;
  const unitShares: UnitPowerShare[] = [];
  for (const [index, unit] of units.entries()) {
    const kw = readUnitKw(unit, devices);
    if (kw === null) continue;
    const toneIndex = index % UNIT_RULER_COLORS.length;
    unitShares.push({
      id: unit.chiller.assetId,
      name: unit.chiller.assetName.replace(' 机组', ''),
      kw,
      barClass: UNIT_RULER_COLORS[toneIndex]!,
      swatchClass: UNIT_SWATCH_COLORS[toneIndex]!,
    });
  }

  const sharesSum = unitShares.reduce((sum, entry) => sum + entry.kw, 0);
  const totalKw =
    plant.totalPower
    && plant.totalPower.state === 'PRESENT'
    && (plant.totalPower.current || plant.totalPower.lowConfidence)
    && plant.totalPower.numeric !== null
      ? Math.max(0, plant.totalPower.numeric)
      : null;
  const scaleMax = Math.max(totalKw ?? 0, sharesSum);
  const remainderKw = totalKw !== null && unitShares.length > 0 && totalKw - sharesSum > 0.5 ? totalKw - sharesSum : 0;

  return (
    <section aria-label="控制方式" className="border-b border-border bg-card">
      <div className="grid grid-cols-1 divide-y divide-border @5xl/main:grid-cols-[minmax(282px,308px)_minmax(0,1fr)_minmax(240px,260px)] @5xl/main:divide-x @5xl/main:divide-y-0">
        <div className={cn('flex min-w-0 flex-col justify-between gap-2 px-4 py-2.5', BAR_TONE[state.controlMode])}>
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="inline-flex items-center gap-1.5 text-sm font-bold tracking-tight whitespace-nowrap">
              <ModeGlyph mode={state.controlMode} className="text-xs" />
              {presentation.label}
            </span>
            {state.updatedAt ? (
              <span className="truncate text-[11.5px] text-muted-foreground" title={`设定 ${controlActorLabel(state, myId)}`}>
                设定 <span className="font-medium text-foreground">{controlActorLabel(state, myId)}</span>{' '}
                <span className="font-mono">{formatCompactTime(state.updatedAt, timeZone)}</span>
              </span>
            ) : null}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <div
              role="group"
              aria-label="切换控制方式"
              className={cn(
                'inline-flex gap-0.5 rounded-lg border border-border p-0.5',
                state.controlMode === 'REMOTE_AUTO' ? 'bg-muted' : 'bg-card',
              )}
            >
              {MODE_ORDER.map((mode) => {
                const current = mode === state.controlMode;
                return (
                  <button
                    key={mode}
                    type="button"
                    aria-pressed={current}
                    disabled={!current && !canManage}
                    title={!current && !canManage ? '你没有切换控制方式的权限' : undefined}
                    onClick={() => { if (!current) onPickMode(mode); }}
                    className={cn(
                      'inline-flex h-6 items-center gap-1 rounded-md border border-transparent px-2 text-xs font-medium text-secondary-foreground transition-colors duration-100 outline-none hover:text-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring-soft disabled:cursor-not-allowed disabled:opacity-50',
                      current && 'border-border bg-card text-foreground shadow-xs',
                      current && state.controlMode !== 'REMOTE_AUTO' && 'bg-secondary',
                    )}
                  >
                    <ModeGlyph mode={mode} />
                    {MODE_PRESENTATION[mode].short}
                  </button>
                );
              })}
            </div>
          </div>

          {state.reason || (autoControlLease?.confirmationMode === 'AUTO_WITH_LEASE' && autoControlLease.activeLease) ? (
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs">
              {state.reason ? (
                <span className="truncate text-muted-foreground" title={state.reason}>
                  原因：{state.reason}
                </span>
              ) : null}
              {autoControlLease?.confirmationMode === 'AUTO_WITH_LEASE' && autoControlLease.activeLease ? (
                <span className="inline-flex flex-wrap items-center gap-1.5">
                  <StatusBadge tone="info" label="自动控制租约生效中" />
                  <span className="font-mono text-[11px] text-secondary-foreground">
                    至 {formatCompactTime(autoControlLease.activeLease.expiresAt, timeZone)}
                  </span>
                  <Link
                    to="/energy-analysis/optimization"
                    search={{ site: autoControlLease.siteId }}
                    className="font-medium text-primary hover:underline"
                  >
                    管理自动租约 ›
                  </Link>
                </span>
              ) : null}
            </div>
          ) : null}
        </div>

        <div className="flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-2 px-3.5 py-2.5 @5xl/main:flex-nowrap">
          <div className="flex shrink-0 flex-col gap-0.5">
            <div className="flex items-baseline gap-1.5">
              <span className="text-[11px] text-muted-foreground">冷站总功率</span>
              <ReadingValue reading={plant.totalPower} className="text-xl leading-6 font-bold tracking-tight" />
            </div>
            <div className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-[11px] text-muted-foreground tabular-nums">
              <span>
                {units.length > 0
                  ? `在运 ${runningUnits}/${units.length} 组`
                  : `在线 ${plant.onlineCount}/${plant.devices.length} 台`}
              </span>
              <span>锁定设备 {lockCount} 台</span>
              <span>待处理指令 {pendingCount} 条</span>
            </div>
          </div>

          <div className="flex min-w-[155px] flex-1 flex-col gap-1 overflow-hidden">
            <div
              aria-label="冷站负荷分布标尺"
              className="flex h-2.5 w-full overflow-hidden rounded-sm border border-border bg-muted"
            >
              {scaleMax > 0 && unitShares.length > 0 ? (
                <>
                  {unitShares.map((share) => (
                    <div
                      key={share.id}
                      className={cn('h-full border-r border-card/80 last:border-r-0', share.barClass)}
                      style={{ width: `${Math.max(1.5, (share.kw / scaleMax) * 100).toFixed(2)}%` }}
                      title={`${share.name}：${formatDecimal(share.kw, 1)} kW`}
                    />
                  ))}
                  {remainderKw > 0 ? (
                    <div
                      className="h-full bg-chart-inactive"
                      style={{ width: `${((remainderKw / scaleMax) * 100).toFixed(2)}%` }}
                      title={`其余设备：${formatDecimal(remainderKw, 1)} kW`}
                    />
                  ) : null}
                </>
              ) : scaleMax > 0 && totalKw !== null ? (
                <div className="h-full bg-primary" style={{ width: '100%' }} title={`全站有功功率：${formatDecimal(totalKw, 1)} kW`} />
              ) : null}
            </div>
            <div className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-[11px] text-secondary-foreground">
              {unitShares.length > 0 ? (
                <>
                  {unitShares.map((share) => (
                    <span key={share.id} className="inline-flex items-center gap-1 whitespace-nowrap">
                      <span className={cn('size-2 rounded-[2px]', share.swatchClass)} aria-hidden="true" />
                      <span>{share.name}</span>
                      <span className="font-mono font-semibold text-foreground">{formatDecimal(share.kw, 1)}</span>
                    </span>
                  ))}
                  {remainderKw > 0 ? (
                    <span className="inline-flex items-center gap-1 whitespace-nowrap text-muted-foreground">
                      <span className="size-2 rounded-[2px] bg-chart-inactive" aria-hidden="true" />
                      <span>其余</span>
                      <span className="font-mono">{formatDecimal(remainderKw, 1)}</span>
                    </span>
                  ) : null}
                </>
              ) : (
                <span className="text-muted-foreground">暂无分机组电表读数</span>
              )}
            </div>
          </div>

          <div className="flex shrink-0 flex-col gap-1 border-l border-border pl-3 text-xs">
            <div className="flex items-center gap-1.5 whitespace-nowrap">
              <span className="size-1.5 shrink-0 rounded-full bg-primary" aria-hidden="true" />
              <span className="text-muted-foreground">冷冻总管</span>
              <span className="font-semibold">
                <ReadingValue reading={hydraulic.chilled.supplyTemp} /> / <ReadingValue reading={hydraulic.chilled.returnTemp} />
              </span>
              {hydraulic.chilled.deltaT?.state === 'PRESENT' ? (
                <span className="text-[11px] text-muted-foreground">
                  ΔT <ReadingValue reading={hydraulic.chilled.deltaT} />
                </span>
              ) : null}
            </div>
            <div className="flex items-center gap-1.5 whitespace-nowrap">
              <span className="size-1.5 shrink-0 rounded-full bg-chart-2" aria-hidden="true" />
              <span className="text-muted-foreground">冷却总管</span>
              <span className="font-semibold">
                <ReadingValue reading={hydraulic.condenser.leavingTowerTemp} /> / <ReadingValue reading={hydraulic.condenser.enteringTowerTemp} />
              </span>
            </div>
          </div>
        </div>

        <div className="flex min-w-0 flex-col justify-between gap-2 bg-muted/45 px-3.5 py-2.5">
          <div className="flex items-center justify-between gap-2 whitespace-nowrap">
            <LiveIndicator mode={liveMode} />
            <div className="flex shrink-0 items-center gap-1">
              <Button type="button" variant="outline" size="xs" onClick={onOpenHistory}>
                <History data-icon="inline-start" />
                历史 ({commandCount})
              </Button>
              <Button type="button" variant="outline" size="icon-xs" aria-label="刷新" title="刷新" onClick={onRefresh}>
                <RefreshCw />
              </Button>
            </div>
          </div>
          <div className="flex items-center justify-between gap-2">
            {plant.latestSampleAt ? (
              <span className="text-[11px] whitespace-nowrap text-muted-foreground">
                数据 <span className="font-mono">{formatClock(plant.latestSampleAt, timeZone)}</span>
              </span>
            ) : <span />}
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="w-fit border-destructive/35 bg-destructive-soft/45 text-destructive-text hover:border-destructive/60 hover:bg-destructive-soft hover:text-destructive-text"
              disabled={!canManage}
              title={canManage ? '安全停机或紧急全断站点' : '你没有停机的权限'}
              onClick={onShutdown}
            >
              <Power data-icon="inline-start" />
              停机…
            </Button>
          </div>
        </div>
      </div>

      {state.activeMaintenanceWindow ? (
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-warning/35 bg-warning-soft px-4 py-1.5 text-xs text-warning-text">
          <span className="inline-flex items-center gap-1.5 font-medium">
            <TriangleAlert className="size-3.5 shrink-0" aria-hidden="true" />
            检修窗口「{state.activeMaintenanceWindow.title}」{state.activeMaintenanceWindow.startDate} 至{' '}
            {state.activeMaintenanceWindow.endDate}，只能停机和复位
          </span>
          <span className="font-mono text-[11px]">第 3 道安全闸门拦截生效中</span>
        </div>
      ) : null}
    </section>
  );
}
