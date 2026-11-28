import { Link } from '@tanstack/react-router';
import {
  ArrowRight,
  ChevronRight,
  ClipboardList,
  Send,
  ShieldCheck,
  SlidersHorizontal,
} from 'lucide-react';
import type { ReactNode } from 'react';
import type { AutoControlConfirmationMode } from '@/api/generated/platformGateway.gen';
import type { WorkOrder } from '@/api/work-orders';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import type { Command } from '@/features/control/command-api';
import { APPROVAL_LABEL, STATUS_PRESENTATION } from '@/features/control/command-presentation';
import { CommandMark } from '@/features/control/components/CommandMark';
import { type ControlSetting, type ControlTarget } from '@/features/control/control-model';
import { MODE_PRESENTATION, type SiteControlMode } from '@/features/control/control-state';
import { StatusShape } from '@/features/work-orders/components/StatusShape';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  changesSupply,
  formatAuxiliaryCapabilityLabel,
  formatAuxiliaryLoopLabel,
  formatAuxiliaryRationale,
  formatAuxiliaryTargetValue,
  formatCelsius as celsius,
  TRIGGER_LABEL,
  type OptimizationAuxiliaryTarget,
  type OptimizationRecommendation,
  type ReadinessSummary,
  type RecommendationRisk,
} from '../optimization-model';
import { ReadinessDisclosure } from './OptimizationReadinessDisclosure';

const RISK: Record<RecommendationRisk, { label: string; className: string }> = {
  LOW: { label: '低风险', className: 'bg-success-soft text-success-text' },
  MEDIUM: { label: '中风险', className: 'bg-warning-soft text-warning-text' },
  HIGH: { label: '高风险', className: 'bg-destructive-soft text-destructive-text' },
};

export interface AuxiliaryControlBinding {
  readonly aux: OptimizationAuxiliaryTarget;
  readonly matched: { readonly target: ControlTarget; readonly setting: ControlSetting } | null;
  readonly dispatchedValue: number | null;
  readonly sent: Command | undefined;
}

interface Marker {
  readonly value: number;
  readonly label: string;
  readonly emphasis?: boolean;
}

/** A temperature scale with its allowed band shaded and the current and proposed values marked. */
function RangeTrack({ label, axis, band, bandLabel, markers }: {
  readonly label: string;
  readonly axis: readonly [number, number];
  readonly band: readonly [number, number];
  readonly bandLabel: string;
  readonly markers: readonly Marker[];
}) {
  const at = (value: number) => `${((Math.min(axis[1], Math.max(axis[0], value)) - axis[0]) / (axis[1] - axis[0])) * 100}%`;
  const summary = `${label}：${bandLabel} ${celsius(band[0])}–${celsius(band[1])}，${markers.map((marker) => `${marker.label} ${celsius(marker.value)}`).join('，')}`;
  return (
    <figure className="relative h-[60px] min-w-0 flex-1" aria-label={summary}>
      <div className="absolute top-[22px] h-2 w-full rounded-full bg-muted" aria-hidden="true" />
      <div className="absolute top-[22px] h-2 rounded-full bg-primary/20" style={{ left: at(band[0]), width: `calc(${at(band[1])} - ${at(band[0])})` }} aria-hidden="true" />
      {/* The proposal is labelled above the track and the present value below, so close values never collide. */}
      {markers.map((marker) => (
        <div
          key={marker.label}
          className={cn('absolute flex -translate-x-1/2 items-center', marker.emphasis ? 'top-0 flex-col' : 'top-[18px] flex-col-reverse')}
          style={{ left: at(marker.value) }}
          aria-hidden="true"
        >
          <span className={cn('text-[11px] leading-4 whitespace-nowrap', marker.emphasis ? 'font-semibold text-primary' : 'text-muted-foreground')}>
            {marker.label} {formatDecimal(marker.value, 1)}
          </span>
          <span className={cn('size-3.5 rounded-full border-2 border-card shadow-sm', marker.emphasis ? 'mt-0.5 bg-primary' : 'mb-0.5 bg-muted-foreground')} />
        </div>
      ))}
      <div className="absolute inset-x-0 top-[44px] flex justify-between text-[11px] text-subtle-foreground tabular-nums" aria-hidden="true">
        <span>{axis[0]}</span>
        <span>{axis[1]} °C</span>
      </div>
    </figure>
  );
}

function ConstraintRow({ title, detail, children }: { readonly title: string; readonly detail: ReactNode; readonly children: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 border-b border-divider px-5 py-3.5 last:border-b-0 sm:flex-row sm:items-center sm:gap-8">
      <span className="flex shrink-0 flex-col gap-0.5 sm:w-72">
        <span className="text-sm font-semibold">{title}</span>
        <span className="text-xs text-muted-foreground">{detail}</span>
      </span>
      {children}
    </div>
  );
}

function Effect({ label, value, unit, info, children }: { readonly label: string; readonly value: string; readonly unit?: string; readonly info?: string; readonly children?: ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">{label}{info ? <InfoTip>{info}</InfoTip> : null}</span>
      <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
        {value}{unit ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
      </span>
      {children ? <span className="text-xs text-muted-foreground">{children}</span> : null}
    </div>
  );
}

export function OptimizationProposalPanel({
  recommendation,
  readiness,
  timeZone,
  siteId,
  controlMode,
  setpoint,
  setting,
  confirmationMode,
  registryState,
  workOrdersState,
  sent,
  auxiliaryControls,
  activeWorkOrder,
  canCreateWorkOrder,
  onDispatch,
  onDispatchAuxiliary,
  onCreateWorkOrder,
  onRetryWorkOrders,
}: {
  readonly recommendation: OptimizationRecommendation;
  readonly readiness: ReadinessSummary;
  readonly timeZone: string;
  readonly siteId: string;
  readonly controlMode: SiteControlMode;
  readonly setpoint: number | null;
  readonly setting: ControlSetting | null;
  readonly confirmationMode: AutoControlConfirmationMode;
  readonly registryState: 'pending' | 'error' | 'ready';
  readonly workOrdersState: 'pending' | 'error' | 'ready';
  readonly sent: Command | undefined;
  readonly auxiliaryControls: readonly AuxiliaryControlBinding[];
  readonly activeWorkOrder: WorkOrder | null;
  readonly canCreateWorkOrder: boolean;
  readonly onDispatch: () => void;
  readonly onDispatchAuxiliary: (binding: AuxiliaryControlBinding) => void;
  readonly onCreateWorkOrder: () => void;
  readonly onRetryWorkOrders: () => void;
}) {
  const { baseline, candidate, comfortRange, supplyRange } = recommendation;
  const change = changesSupply(recommendation);
  const risk = RISK[recommendation.risk];
  const share = baseline.dailyEnergyKWh > 0 ? recommendation.saving.energyKWhPerDay / baseline.dailyEnergyKWh : null;
  const autoMode = controlMode === 'REMOTE_AUTO';
  const controlBlockedHint = !autoMode ? `${MODE_PRESENTATION[controlMode].label}中，平台不下发指令` : undefined;
  const executionNote = controlBlockedHint ?? (confirmationMode === 'AUTO_WITH_LEASE'
    ? '自动控制租约生效中，满足实时安全复核后将自动下发并观察'
    : '建议不会自动执行');
  const stepNote = setting && setpoint !== null && Math.abs(setpoint - candidate.supplyTempC) >= 0.05
    ? `冷机只能按 ${formatDecimal(setting.step, 1)} °C 调整，实际下发 ${celsius(setpoint)}；${executionNote}`
    : `下发为 ${celsius(setpoint ?? candidate.supplyTempC)}；${executionNote}`;

  return (
    <>
      <Panel aria-label="当前建议" className="grid grid-cols-1 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-3 border-b border-divider px-6 py-5 lg:border-r lg:border-b-0">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <h2 className="text-title font-semibold">当前建议</h2>
            <span className={cn('rounded-md px-1.5 leading-5 font-medium', risk.className)}>{risk.label}</span>
            <span className="text-muted-foreground">{TRIGGER_LABEL[recommendation.trigger]}于 <span className="font-mono">{formatTime(recommendation.createdAt, timeZone)}</span></span>
          </div>
          <span className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">冷冻水供水温度</span>
            {change ? (
              <span className="flex items-center gap-3 text-[34px] leading-10 font-semibold tracking-[-0.03em] tabular-nums">
                <span className="text-muted-foreground">{formatDecimal(baseline.supplyTempC, 1)}</span>
                <ArrowRight className="size-6 text-subtle-foreground" aria-label="→" />
                <span className="text-primary">{formatDecimal(candidate.supplyTempC, 1)}</span>
                <span className="text-sm font-medium tracking-normal text-subtle-foreground">°C</span>
              </span>
            ) : (
              <span className="text-[34px] leading-10 font-semibold tracking-[-0.03em] tabular-nums">
                保持 {formatDecimal(baseline.supplyTempC, 1)}<span className="ml-1 text-sm font-medium tracking-normal text-subtle-foreground">°C</span>
              </span>
            )}
          </span>
          <p className="text-sm text-secondary-foreground">
            {change
              ? `供水温度${candidate.supplyTempC > baseline.supplyTempC ? '升高' : '降低'}后，预计室内温度 ${celsius(candidate.projectedZoneTempC)}，仍在 ${formatDecimal(comfortRange.minC, 0)}–${formatDecimal(comfortRange.maxC, 0)} °C 舒适范围内，距边界还有 ${celsius(recommendation.comfortMarginC)}。`
              : '在安全与舒适约束内，当前供水温度已是最省电的选择。'}
          </p>
          {change ? (
            registryState === 'pending' ? (
              <Skeleton className="h-9 w-40 rounded-md" />
            ) : registryState === 'error' ? (
              <p className="text-xs text-muted-foreground">站点资产列表暂时读不到，无法核验可执行通道。</p>
            ) : setting !== null ? (
              sent ? (
                <Link
                  to="/operations/control"
                  search={{ site: siteId, inspect: sent.commandId }}
                  className="flex items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3 outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft"
                >
                  <CommandMark status={sent.status} />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="text-sm font-semibold">已提交为控制指令 · {STATUS_PRESENTATION[sent.status].label}</span>
                    <span className="truncate text-xs text-muted-foreground">
                      {setting.label} → {setpoint === null ? '—' : celsius(setpoint)} · <span className="font-mono">{formatTime(sent.createdAt, timeZone).slice(-5)}</span>
                      {' · '}{APPROVAL_LABEL[sent.approvalPolicy]}{sent.requiredApprovalCount > 0 ? ` ${sent.approvalCount} / ${sent.requiredApprovalCount}` : ''}
                    </span>
                  </span>
                  <span className="flex items-center text-xs font-medium text-primary">查看指令<ChevronRight className="size-3.5" aria-hidden="true" /></span>
                </Link>
              ) : (
                <div className="flex flex-wrap items-center gap-3">
                  <Button disabled={setpoint === null || !autoMode} title={controlBlockedHint} onClick={onDispatch}>
                    <Send data-icon="inline-start" />
                    按建议下发
                  </Button>
                  <span className="text-xs text-muted-foreground">{stepNote}</span>
                </div>
              )
            ) : workOrdersState === 'pending' ? (
              <Skeleton className="h-9 w-40 rounded-md" />
            ) : workOrdersState === 'error' ? (
              <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span>关联工单状态暂时读不到，为避免重复建单请先重试。</span>
                <Button variant="outline" size="sm" onClick={onRetryWorkOrders}>重试</Button>
              </div>
            ) : activeWorkOrder ? (
              <Link
                to="/operations/work-center"
                search={{ site: siteId, inspect: activeWorkOrder.workOrderId }}
                className="flex items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3 outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft"
              >
                <StatusShape status={activeWorkOrder.status} className="size-3" />
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="text-sm font-semibold">已转为人工调温工单 · {STATUS_LABELS[activeWorkOrder.status]}</span>
                  <span className="truncate text-xs text-muted-foreground">
                    {activeWorkOrder.title} · <span className="font-mono">{formatTime(activeWorkOrder.createdAt, timeZone).slice(-5)}</span>
                  </span>
                </span>
                <span className="flex items-center text-xs font-medium text-primary">查看工单<ChevronRight className="size-3.5" aria-hidden="true" /></span>
              </Link>
            ) : canCreateWorkOrder ? (
              <div className="flex flex-wrap items-center gap-3">
                <Button onClick={onCreateWorkOrder}>
                  <ClipboardList data-icon="inline-start" />
                  创建人工调温工单
                </Button>
                <span className="text-xs text-muted-foreground">当前站点未登记可写冷冻水供水温度设定点，转为人工调温工单现场执行</span>
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">
                当前站点未登记可写冷冻水供水温度设定点；当前账号无创建工单权限，需由运维人员现场调温。
              </p>
            )
          ) : null}
        </div>
        <section role="region" aria-label="预计效果" className="flex flex-col gap-4 bg-muted/20 px-6 py-5">
          <span className="text-xs font-medium text-muted-foreground">预计效果</span>
          <Effect
            label="预计日节电"
            info="波动范围按 90% 置信区间估算"
            value={formatDecimal(recommendation.saving.energyKWhPerDay, 0)}
            unit="kWh"
          >
            预计波动 ±{formatDecimal(recommendation.uncertainty.energyP90KWh, 0)} kWh{share !== null ? ` · 约占最近一天冷站用电 ${formatDecimal(share * 100, 1)}%` : ''}
          </Effect>
          <Effect label="预计日节省" info="按当天峰谷均价折算，不含需量电费" value={`¥${formatDecimal(recommendation.saving.costPerDay, 0)}`}>
            按最近一天估算电费 ¥{formatDecimal(baseline.dailyCost, 0)} 折算
          </Effect>
        </section>
      </Panel>

      <Panel aria-label="约束">
        <PanelHeader
          title={<span className="flex items-center gap-1.5"><ShieldCheck className="size-4 text-primary" aria-hidden="true" />安全与舒适约束<InfoTip>舒适余量已扣除室内温度预测的不确定性</InfoTip></span>}
          caption={`单次调整 ≤ ${celsius(supplyRange.maxStepC)}`}
        />
        <ConstraintRow title="冷冻水供水温度" detail={`安全范围 ${formatDecimal(supplyRange.minC, 1)}–${celsius(supplyRange.maxC)} · ${change ? `调整 ${formatDecimal(Math.abs(candidate.supplyTempC - baseline.supplyTempC), 1)} °C` : '保持当前温度'}`}>
          <RangeTrack
            label="冷冻水供水温度"
            axis={[Math.floor(supplyRange.minC) - 1, Math.ceil(supplyRange.maxC) + 1]}
            band={[supplyRange.minC, supplyRange.maxC]}
            bandLabel="安全范围"
            markers={change
              ? [{ value: baseline.supplyTempC, label: '当前' }, { value: candidate.supplyTempC, label: '调整为', emphasis: true }]
              : [{ value: baseline.supplyTempC, label: '当前', emphasis: true }]}
          />
        </ConstraintRow>
        <ConstraintRow
          title="室内温度"
          detail={`舒适范围 ${formatDecimal(comfortRange.minC, 0)}–${formatDecimal(comfortRange.maxC, 0)} °C · 预计 ±${formatDecimal(recommendation.uncertainty.zoneTempP90C, 1)} °C · 余量 ${celsius(recommendation.comfortMarginC)}`}
        >
          <RangeTrack
            label="室内温度"
            axis={[Math.floor(comfortRange.minC) - 2, Math.ceil(comfortRange.maxC) + 2]}
            band={[comfortRange.minC, comfortRange.maxC]}
            bandLabel="舒适范围"
            markers={change
              ? [{ value: baseline.zoneTempC, label: '当前' }, { value: candidate.projectedZoneTempC, label: '预计', emphasis: true }]
              : [{ value: baseline.zoneTempC, label: '当前', emphasis: true }]}
          />
        </ConstraintRow>
      </Panel>

      {auxiliaryControls.length > 0 ? (
        <Panel aria-label="协同变频与风机建议">
          <PanelHeader
            title={<span className="flex items-center gap-1.5"><SlidersHorizontal className="size-4 text-primary" aria-hidden="true" />协同变频与风机建议</span>}
            badge={
              <StatusBadge
                tone={confirmationMode === 'AUTO_WITH_LEASE' ? 'success' : 'neutral'}
                label={confirmationMode === 'AUTO_WITH_LEASE' ? '自动闭环已授权' : '人工确认下发'}
              />
            }
          />
          <div className="grid grid-cols-1 divide-y divide-divider md:grid-cols-3 md:divide-x md:divide-y-0">
            {auxiliaryControls.map((binding) => {
              const { aux, matched, dispatchedValue, sent: auxSent } = binding;
              const loopLabel = formatAuxiliaryLoopLabel(aux.loopKey);
              const rationaleText = formatAuxiliaryRationale(aux.rationale);
              const steppedCaption = matched && dispatchedValue !== null
                ? Math.abs(dispatchedValue - aux.targetValue) >= 0.05
                  ? `按步长取整为 ${formatAuxiliaryTargetValue(dispatchedValue, matched.setting.unit ?? aux.unit)}`
                  : `下发至 ${matched.target.assetName}`
                : null;
              return (
                <div key={`${aux.loopKey}:${aux.capability}`} className="flex flex-col gap-2 px-5 py-4">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-sm font-semibold text-foreground">{loopLabel}</span>
                    <span className="rounded-md bg-muted px-2 py-0.5 text-[11px] font-medium text-secondary-foreground">
                      {formatAuxiliaryCapabilityLabel(aux.capability)}
                    </span>
                  </div>
                  <span className="text-2xl leading-[30px] font-semibold tracking-[-0.03em] text-primary tabular-nums">
                    {formatAuxiliaryTargetValue(aux.targetValue, aux.unit)}
                  </span>
                  {rationaleText ? (
                    <p className="text-xs leading-relaxed text-muted-foreground">{rationaleText}</p>
                  ) : null}
                  <div className="mt-auto pt-2">
                    {registryState === 'pending' ? (
                      <Skeleton className="h-8 w-28 rounded-md" />
                    ) : registryState === 'error' ? (
                      <span className="text-xs text-muted-foreground">站点资产列表暂时读不到，无法核验控制点</span>
                    ) : matched && dispatchedValue !== null ? (
                      auxSent ? (
                        <Link
                          to="/operations/control"
                          search={{ site: siteId, inspect: auxSent.commandId }}
                          className="flex items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-xs outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft"
                        >
                          <CommandMark status={auxSent.status} />
                          <span className="flex min-w-0 flex-1 flex-col">
                            <span className="font-semibold text-foreground">已提交 · {STATUS_PRESENTATION[auxSent.status].label}</span>
                            <span className="truncate text-muted-foreground">
                              {matched.target.assetName} → {formatAuxiliaryTargetValue(dispatchedValue, matched.setting.unit ?? aux.unit)}
                            </span>
                          </span>
                          <span className="flex shrink-0 items-center font-medium text-primary">
                            查看指令<ChevronRight className="size-3.5" aria-hidden="true" />
                          </span>
                        </Link>
                      ) : (
                        <div className="flex flex-wrap items-center gap-2.5">
                          <Button
                            size="sm"
                            variant="outline"
                            aria-label={`按建议下发${loopLabel}`}
                            disabled={!autoMode}
                            title={controlBlockedHint}
                            onClick={() => onDispatchAuxiliary(binding)}
                          >
                            <Send data-icon="inline-start" />
                            按建议下发
                          </Button>
                          {controlBlockedHint ? (
                            <span className="text-xs text-muted-foreground">{controlBlockedHint}</span>
                          ) : steppedCaption ? (
                            <span className="text-xs text-muted-foreground">{steppedCaption}</span>
                          ) : null}
                        </div>
                      )
                    ) : (
                      <span className="text-xs text-muted-foreground">未登记可写点位</span>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </Panel>
      ) : null}

      <ReadinessDisclosure readiness={readiness} recommendation={recommendation} />
    </>
  );
}
