import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useMemo } from 'react';
import { Skeleton } from '@/components/ui/skeleton';
import { formatDecimal } from '@/lib/operator-format';
import { useYearLive, yearSnapshotsQuery } from '../query';
import { wallTime } from '../../energy-analysis/consumption/model.ts';
import { yearMonths, yearRows, yearTotals, type YearRow } from '../report-model';
import { EnergyTrend, energyText, Figure, FigureGrid, ReportSheet, Section } from './ReportParts';

const siteRoute = getRouteApi('/_app/_site');

const SOURCE_LABEL: Record<YearRow['source'], string> = { snapshot: '已存档', live: '实时', none: '无数据' };

/** A year month by month: closed months from their snapshots, the rest from the live series. */
export function YearlyReport({ year, now }: { readonly year: number; readonly now: Date }) {
  const { site } = siteRoute.useRouteContext();
  const months = useMemo(() => yearMonths(year, now, site.timezone), [year, now, site.timezone]);
  const snapshots = useQuery(yearSnapshotsQuery(site.id, year));
  const live = useYearLive(year, now);

  if (snapshots.isPending || live.isPending) return <Skeleton className="mx-auto h-[900px] w-full max-w-[1040px] rounded-xl" />;
  if (snapshots.isError || live.isError) {
    return <div className="mx-auto w-full max-w-[1040px] rounded-xl border bg-card py-16 text-center text-sm text-muted-foreground">年度报告所需的数据暂时读不到</div>;
  }

  const rows = yearRows(months, snapshots.data, live.data);
  const totals = yearTotals(rows);
  const archived = snapshots.data.filter((stored) => months.includes(stored.month));
  const savings = archived.reduce((sum, stored) => ({
    days: sum.days + stored.report.savings.days,
    avoidedKWh: sum.avoidedKWh + stored.report.savings.avoidedKWh,
    avoidedCost: sum.avoidedCost + stored.report.savings.avoidedCost,
  }), { days: 0, avoidedKWh: 0, avoidedCost: 0 });
  const findings = archived.reduce((sum, stored) => sum + stored.report.findings.count, 0);
  const liveMonths = rows.filter((row) => row.source === 'live').length;
  const monthsWithData = rows.filter((row) => row.source !== 'none').length;
  const monthly = (value: number | null) => (value === null || monthsWithData === 0 ? '—' : `月均 ${energyText(value / monthsWithData)}`);
  const lastMonth = months.length;
  const today = wallTime(now, site.timezone);
  const running = lastMonth > 0 && months[lastMonth - 1] === `${today.year}-${String(today.month).padStart(2, '0')}`;

  return (
    <ReportSheet
      label={`${year} 年能源报告`}
      kicker={`${site.displayName} · 冷站年度能源报告`}
      title={`${year} 年能源报告`}
      period={lastMonth === 0 ? '本年尚未开始' : `统计期间 1 月至 ${lastMonth} 月${running ? '（本月进行中）' : ''}`}
      archived={liveMonths === 0 && archived.length > 0}
      status={`${archived.length} 个月已存档 · ${liveMonths} 个月按当前记录现算`}
      footnote="说明：已存档的月份取自月结后保存的快照，其余月份按当前记录现算；电度电费按每个计量区间所在时段的分时电价计算，不含需量电费，正式电费以结算为准。节能与诊断只合计已存档月份。"
    >
      <Section index={1} title="全年概要" description={`${monthsWithData} 个月有计量数据`}>
        <FigureGrid>
          <Figure label="空调用电" value={totals.electricityKWh === null ? null : formatDecimal(totals.electricityKWh, 0)} unit="kWh" note={monthly(totals.electricityKWh)} />
          <Figure label="供冷量" value={totals.coolingKWh === null ? null : formatDecimal(totals.coolingKWh, 0)} unit="kWh" note={monthly(totals.coolingKWh)} />
          <Figure label="综合能效" value={totals.cop === null ? null : formatDecimal(totals.cop, 2)} unit="COP" note="全年供冷量 ÷ 全年空调用电" />
          <Figure label="电度电费" value={totals.energyCost === null ? null : `¥${formatDecimal(totals.energyCost, 0)}`} note={totals.energyCost === null ? '有月份没有电价，全年未计' : '按分时电价逐时计算，不含需量电费'} />
        </FigureGrid>
      </Section>

      <Section index={2} title="逐月用能与能效" description="用电最多的月份已标出">
        <EnergyTrend unitLabel="空调用电 · kWh（每月）" rows={rows.map((row) => ({ label: `${Number(row.month.slice(5, 7))}月`, electricityKWh: row.electricityKWh === null ? null : Math.round(row.electricityKWh), cop: row.cop === null ? null : Number(row.cop.toFixed(2)) }))} />
        <table className="w-full text-sm" aria-label="逐月明细">
          <thead>
            <tr className="text-xs text-muted-foreground">
              <th className="h-9 rounded-l-md bg-muted px-3 text-left font-medium">月份</th>
              <th className="h-9 bg-muted px-3 text-left font-medium">来源</th>
              <th className="h-9 bg-muted px-3 text-right font-medium">空调用电</th>
              <th className="h-9 bg-muted px-3 text-right font-medium">供冷量</th>
              <th className="h-9 bg-muted px-3 text-right font-medium">综合能效</th>
              <th className="h-9 bg-muted px-3 text-right font-medium">电度电费</th>
              <th className="h-9 rounded-r-md bg-muted px-3 text-right font-medium">计量覆盖</th>
            </tr>
          </thead>
          <tbody className="tabular-nums">
            {rows.map((row) => (
              <tr key={row.month} className="border-b border-divider last:border-b-0">
                <td className="px-3 py-2.5 font-medium">{Number(row.month.slice(5, 7))} 月</td>
                <td className="px-3 py-2.5 text-muted-foreground">{SOURCE_LABEL[row.source]}{row.version && row.version > 1 ? ` · 第 ${row.version} 版` : ''}</td>
                <td className="px-3 py-2.5 text-right">{energyText(row.electricityKWh)}</td>
                <td className="px-3 py-2.5 text-right">{energyText(row.coolingKWh)}</td>
                <td className="px-3 py-2.5 text-right">{row.cop === null ? '—' : formatDecimal(row.cop, 2)}</td>
                <td className="px-3 py-2.5 text-right">{row.energyCost === null ? '—' : `${formatDecimal(row.energyCost, 0)} 元`}</td>
                <td className="px-3 py-2.5 text-right text-muted-foreground">{row.daysWithData === null ? '—' : `${row.daysWithData} / ${row.daysInMonth} 天`}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Section>

      <Section index={3} title="节能与诊断" description="只合计已存档的月份">
        <FigureGrid>
          <Figure label="节电量" value={savings.days > 0 ? formatDecimal(savings.avoidedKWh, 0) : null} unit="kWh" note={`${archived.length} 个存档月份合计`} />
          <Figure label="节省电费" value={savings.days > 0 ? `¥${formatDecimal(savings.avoidedCost, 0)}` : null} note="按逐时分时电度电价，不含需量电费" />
          <Figure label="统计天数" value={String(savings.days)} unit="天" note="每天 02:00 统计前一天" />
          <Figure label="诊断发现" value={String(findings)} unit="条" note="诊断窗口结束于存档月份内" />
        </FigureGrid>
      </Section>
    </ReportSheet>
  );
}
