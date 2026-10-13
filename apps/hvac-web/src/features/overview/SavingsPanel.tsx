import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Panel, PanelHeader, PanelLink } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import { changesSupply, TRIGGER_LABEL, type RecommendationRisk } from '@/features/optimization/optimization-model';
import { latestRecommendationQuery } from '@/features/optimization/query';
import { measuresQuery, summaryQuery } from '@/features/savings/query';
import { monthToDate, siteSavingsRate } from '@/features/savings/savings-model';
import { formatCompactTime, formatDecimal } from '@/lib/operator-format';

const RISK: Readonly<Record<RecommendationRisk, { readonly label: string; readonly variant: 'success' | 'warning' | 'destructive' }>> = {
  LOW: { label: '低风险', variant: 'success' },
  MEDIUM: { label: '中风险', variant: 'warning' },
  HIGH: { label: '高风险', variant: 'destructive' },
};
const MEASURE_LIMIT = 2;

function SectionTitle({ title, count, action }: { readonly title: string; readonly count?: number; readonly action?: React.ReactNode }) {
  return (
    <div className="flex items-center gap-1.5 border-b border-divider px-4 pt-3 pb-2 text-xs text-muted-foreground">
      {title}
      {count !== undefined ? <span className="font-mono text-subtle-foreground">{count}</span> : null}
      {action ? <span className="ml-auto">{action}</span> : null}
    </div>
  );
}

/**
 * What the Site has saved and what it could still save: verified savings come from the measures
 * the M&V owner reports on, the opportunity from the latest optimization recommendation's estimate.
 */
export function SavingsPanel({ siteId, timeZone }: { readonly siteId: string; readonly timeZone: string }) {
  const month = useMemo(() => monthToDate(new Date(), timeZone), [timeZone]);
  const summary = useQuery(summaryQuery(siteId, month.from, month.to));
  const measures = useQuery(measuresQuery(siteId));
  const latest = useQuery(latestRecommendationQuery(siteId, null));
  const totals = summary.data?.totals;
  const verified = totals !== undefined && totals.days > 0;
  const reporting = measures.data?.filter((measure) => measure.status === 'IMPLEMENTED') ?? [];
  const rate = totals ? siteSavingsRate(totals) : null;
  const recommendation = latest.data ?? null;

  return (
    <Panel role="region" aria-label="节能成效" className="shrink-0">
      <PanelHeader title="节能" action={<PanelLink to="/energy-analysis/savings" search={{ site: siteId }}>节能项目</PanelLink>} />

      <div className="flex flex-col gap-1 px-4 pt-3 pb-3.5">
        <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
          本月已统计节省
          <InfoTip>节电量 = 没做措施时的预计用电（按实际供冷量调整）− 实际用电；节电率相对冷站同期用电</InfoTip>
        </span>
        {summary.isPending || measures.isPending ? <Skeleton className="h-8 w-40" /> : summary.isError || measures.isError ? (
          <span className="text-sm text-muted-foreground">节能统计暂不可用</span>
        ) : verified ? (
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <span className="text-2xl font-semibold tracking-[-0.03em] tabular-nums">¥{formatDecimal(totals.avoidedCost, 0)}</span>
            <span className="text-xs text-muted-foreground">节电 <b className="font-semibold text-foreground tabular-nums">{formatDecimal(totals.avoidedKWh, 0)}</b> kWh</span>
            <span className="text-xs text-muted-foreground">节电率 <b className="font-semibold text-foreground tabular-nums">{rate === null ? '—' : `${formatDecimal(rate * 100, 1)}%`}</b></span>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            {reporting.length === 0
              ? '还没有在统计的节能措施'
              : reporting.some((measure) => !measure.baseline?.trusted)
                ? '对比基线不可靠，暂不统计'
                : '实施满 24 小时后的次日开始出结果'}
          </p>
        )}
      </div>

      <SectionTitle title="优化建议 · 预计" count={recommendation ? 1 : 0} />
      {latest.isPending ? <div className="p-4"><Skeleton className="h-10" /></div> : latest.isError ? (
        <p className="px-4 py-3 text-xs text-muted-foreground">优化建议暂不可用</p>
      ) : recommendation ? (
        <Link
          to="/energy-analysis/optimization"
          search={{ site: siteId }}
          className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-1 border-b border-divider px-4 py-3 outline-none hover:bg-accent focus-visible:bg-accent"
        >
          <span className="truncate text-sm font-semibold">
            {changesSupply(recommendation)
              ? `冷冻水供水 ${formatDecimal(recommendation.baseline.supplyTempC, 1)} → ${formatDecimal(recommendation.candidate.supplyTempC, 1)} °C`
              : `保持供水 ${formatDecimal(recommendation.baseline.supplyTempC, 1)} °C`}
          </span>
          <span className="text-sm font-semibold tabular-nums">
            {formatDecimal(recommendation.saving.costPerDay, 0)} 元<span className="font-normal text-subtle-foreground">/天</span>
          </span>
          <span className="col-span-2 flex items-center gap-2 text-xs text-muted-foreground">
            <Badge variant={RISK[recommendation.risk].variant}>{RISK[recommendation.risk].label}</Badge>
            <span className="truncate">
              预计节电 {formatDecimal(recommendation.saving.energyKWhPerDay, 0)} kWh/天 · {formatCompactTime(recommendation.createdAt, timeZone)} {TRIGGER_LABEL[recommendation.trigger]}
            </span>
          </span>
        </Link>
      ) : (
        <Link to="/energy-analysis/optimization" search={{ site: siteId }} className="block px-4 py-3 text-xs text-muted-foreground hover:text-foreground hover:underline">
          还没有建议，去生成
        </Link>
      )}

      <SectionTitle
        title="措施"
        count={measures.data ? reporting.length : undefined}
        action={<PanelLink to="/energy-analysis/savings" search={{ site: siteId }}>登记措施</PanelLink>}
      />
      {measures.isPending ? <div className="p-4"><Skeleton className="h-10" /></div> : reporting.length === 0 ? (
        <p className="px-4 py-3 text-xs text-muted-foreground">没有在统计的措施</p>
      ) : (
        <ul>
          {reporting.slice(0, MEASURE_LIMIT).map((measure) => (
            <li key={measure.id} className="border-b border-divider last:border-b-0">
              <Link
                to="/energy-analysis/savings"
                search={{ site: siteId, inspect: measure.id }}
                className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-1 px-4 py-3 outline-none hover:bg-accent focus-visible:bg-accent"
              >
                <span className="truncate text-sm font-semibold">{measure.name}</span>
                <span className="text-sm font-semibold tabular-nums">{measure.totals.days > 0 ? `¥${formatDecimal(measure.totals.avoidedCost, 0)}` : '—'}</span>
                <span className="col-span-2 flex items-center gap-2 text-xs text-muted-foreground">
                  <Badge>{measure.baseline?.trusted === false ? '基线不可靠' : '统计中'}</Badge>
                  <span className="truncate">已统计 {measure.totals.days} 天 · 节电 {formatDecimal(measure.totals.avoidedKWh, 0)} kWh</span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      {reporting.length > MEASURE_LIMIT ? (
        <div className="flex items-center justify-between border-t border-divider px-4 py-2.5 text-xs text-muted-foreground">
          还有 {reporting.length - MEASURE_LIMIT} 项
          <PanelLink to="/energy-analysis/savings" search={{ site: siteId }}>全部措施</PanelLink>
        </div>
      ) : null}
    </Panel>
  );
}
