import { CheckCircle2, ChevronDown, CircleDashed, TriangleAlert, Undo2 } from 'lucide-react';
import { useState } from 'react';
import { Panel } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  formatCelsius as celsius,
  formatSigned as signed,
  type OptimizationRecommendation,
  type ReadinessItem,
  type ReadinessStatus,
  type ReadinessSummary,
} from '../optimization-model';

const READINESS_STATUS: Record<ReadinessStatus, { readonly badge: string; readonly className: string; readonly icon: typeof CheckCircle2 }> = {
  ready: { badge: '就绪', className: 'bg-success-soft text-success-text', icon: CheckCircle2 },
  manual: { badge: '人工工单', className: 'bg-primary-soft text-primary', icon: CheckCircle2 },
  blocked: { badge: '未就绪', className: 'bg-warning-soft text-warning-text', icon: TriangleAlert },
  unknown: { badge: '待核验', className: 'bg-muted text-muted-foreground', icon: CircleDashed },
};

function ReadinessRow({ item }: { readonly item: ReadinessItem }) {
  const presentation = READINESS_STATUS[item.status];
  const Icon = presentation.icon;
  return (
    <li className="flex items-start gap-2.5 rounded-lg border border-divider bg-muted/20 px-3.5 py-2.5">
      <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium">{item.label}</span>
          <span className={cn('rounded px-1.5 py-0.5 text-[11px] leading-4 font-medium', presentation.className)}>
            {presentation.badge}
          </span>
        </span>
        <span className="text-xs text-muted-foreground">{item.detail}</span>
      </span>
    </li>
  );
}

export function ReadinessDisclosure({
  readiness,
  recommendation,
}: {
  readonly readiness: ReadinessSummary;
  readonly recommendation: OptimizationRecommendation | null;
}) {
  const [expanded, setExpanded] = useState(false);
  const hasBlocked = readiness.items.some((item) => item.status === 'blocked');
  const SummaryIcon = hasBlocked
    ? TriangleAlert
    : readiness.readyCount >= 5
      ? CheckCircle2
      : CircleDashed;

  return (
    <Panel>
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded((prev) => !prev)}
        className="flex w-full items-center justify-between gap-4 px-5 py-3.5 text-left outline-none hover:bg-muted/30 focus-visible:ring-3 focus-visible:ring-ring-soft"
      >
        <span className="flex min-w-0 items-center gap-2.5">
          <SummaryIcon
            className={cn(
              'size-4 shrink-0',
              hasBlocked ? 'text-warning' : readiness.readyCount >= 5 ? 'text-success' : 'text-muted-foreground',
            )}
            aria-hidden="true"
          />
          <span className="truncate text-sm font-semibold">{readiness.headline}</span>
        </span>
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
          <span>{expanded ? '收起核验与依据' : '查看核验与依据'}</span>
          <ChevronDown className={cn('size-4 transition-transform', expanded && 'rotate-180')} aria-hidden="true" />
        </span>
      </button>

      {expanded ? (
        <section role="region" aria-label="前置与安全条件" className="flex flex-col gap-4 border-t border-divider px-5 py-4">
          <ul className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            {readiness.items.map((item) => (
              <ReadinessRow key={item.key} item={item} />
            ))}
          </ul>

          {readiness.responseCoefficient || readiness.tenantStrategy ? (
            <div className="grid grid-cols-1 gap-2.5 border-t border-divider pt-4 sm:grid-cols-2">
              {readiness.responseCoefficient ? (
                <div className="flex items-start gap-2.5 rounded-lg border border-divider bg-muted/20 px-3.5 py-2.5">
                  {readiness.responseCoefficient.status === 'CALIBRATED' ? (
                    <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" aria-hidden="true" />
                  ) : (
                    <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
                  )}
                  <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium">响应系数标定</span>
                      <span className={cn(
                        'rounded px-1.5 py-0.5 text-[11px] leading-4 font-medium',
                        readiness.responseCoefficient.status === 'CALIBRATED'
                          ? 'bg-success-soft text-success-text'
                          : 'bg-warning-soft text-warning-text',
                      )}
                      >
                        {readiness.responseCoefficient.label}
                      </span>
                    </span>
                    <span className="text-xs text-muted-foreground">{readiness.responseCoefficient.detail}</span>
                  </span>
                </div>
              ) : null}
              {readiness.tenantStrategy ? (
                <div className="flex items-start gap-2.5 rounded-lg border border-divider bg-muted/20 px-3.5 py-2.5">
                  {readiness.tenantStrategy.status === 'CONFIRMED' ? (
                    <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" aria-hidden="true" />
                  ) : (
                    <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
                  )}
                  <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium">共享租户策略</span>
                      <span className={cn(
                        'rounded px-1.5 py-0.5 text-[11px] leading-4 font-medium',
                        readiness.tenantStrategy.status === 'CONFIRMED'
                          ? 'bg-success-soft text-success-text'
                          : 'bg-warning-soft text-warning-text',
                      )}
                      >
                        {readiness.tenantStrategy.label}
                      </span>
                    </span>
                    <span className="text-xs text-muted-foreground">{readiness.tenantStrategy.detail}</span>
                  </span>
                </div>
              ) : null}
            </div>
          ) : null}

          {recommendation ? (
            <div className="grid grid-cols-1 items-start gap-6 border-t border-divider pt-4 sm:grid-cols-2">
              <div className="flex flex-col gap-1.5">
                <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  策略响应与基线依据
                  <InfoTip>标「人工设定」的响应系数由人在优化策略里填写，不是从站点运行数据拟合的</InfoTip>
                </span>
                <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-5">
                  {([
                    ['最近一天冷站用电', `${formatDecimal(recommendation.baseline.dailyEnergyKWh, 0)} kWh · ¥${formatDecimal(recommendation.baseline.dailyCost, 0)}`],
                    ['供水每升高 1 °C 日用电', `${signed(recommendation.response.energyKWhPerSupplyC, 0)} kWh · 人工设定`],
                    ['供水每升高 1 °C 室内温度', `${signed(recommendation.response.zoneCPerSupplyC, 1)} °C · 人工设定`],
                  ] as const).map(([label, value]) => (
                    <div key={label} className="col-span-2 grid grid-cols-subgrid border-b border-divider py-1.5 last:border-b-0">
                      <dt className="text-xs leading-5 text-muted-foreground">{label}</dt>
                      <dd className="text-xs leading-5 font-medium tabular-nums">{value}</dd>
                    </div>
                  ))}
                </dl>
              </div>

              <div className="flex flex-col gap-2">
                <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  <Undo2 className="size-3.5 text-primary" aria-hidden="true" />
                  观察与回退规则
                </span>
                <p className="text-xs text-muted-foreground">
                  调整后需同时保持室内温度在 {formatDecimal(recommendation.comfortRange.minC, 0)}–{formatDecimal(recommendation.comfortRange.maxC, 0)} °C 舒适范围内，且冷站负荷不高于调整前（最近一天冷站用电 {formatDecimal(recommendation.baseline.dailyEnergyKWh, 0)} kWh）。
                </p>
                <p className="rounded-md bg-muted/40 px-3 py-2 text-xs text-secondary-foreground">
                  下发后观察 {recommendation.verificationMinutes} 分钟；任一条件不满足，将供水温度恢复为{' '}
                  <b className="font-semibold text-foreground">{celsius(recommendation.restoreSupplyTempC)}</b>
                </p>
              </div>
            </div>
          ) : null}
        </section>
      ) : null}
    </Panel>
  );
}
