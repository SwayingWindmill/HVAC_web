import { useQuery } from '@tanstack/react-query';
import { getRouteApi, useNavigate, useSearch } from '@tanstack/react-router';
import { Printer } from 'lucide-react';
import { useMemo } from 'react';
import { FactList, FactRow } from '@/blocks/fact-list';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { PeriodTabs } from '@/components/ui/period-tabs';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { siteFindingsQuery } from '@/features/diagnostics/query';
import { relativeChange, wallTime } from '@/features/energy-analysis/consumption/model';
import { measuresQuery, summaryQuery } from '@/features/savings/query';
import { monthToDate, STATUS_LABEL } from '@/features/savings/savings-model';
import { demandCharges, touEnergyCost } from '@/features/tariff/tariff-model';
import { formatDecimal } from '@/lib/operator-format';
import { monthlySnapshotQuery, useMonthDemand, useMonthEnergy, useMonthOperations, useSiteTariff } from '../query';
import {
  alarmSummary, findingSummary, recentMonths, reportWindow, snapshotDays, snapshotPeak, workOrderSummary,
  type ReportWindow, type StoredReport,
} from '../report-model';
import { CostTable, EnergyTrend, energyText, Figure, monthLabel, NOT_ARCHIVED, OperationsGrid, ReportSheet, SavingsGrid, Section } from './ReportParts';
import { YearlyReport } from './YearlyReport';

const siteRoute = getRouteApi('/_app/_site');

type ReportView = 'month' | 'year';
const VIEWS = [{ value: 'month', label: '月度' }, { value: 'year', label: '年度' }];

export function ReportWorkspace() {
  const { site } = siteRoute.useRouteContext();
  const search = useSearch({ from: '/_app/_site/reports' });
  const navigate = useNavigate({ from: '/reports' });
  const now = useMemo(() => new Date(), []);
  const view: ReportView = search.view ?? 'month';
  const months = useMemo(() => recentMonths(now, site.timezone), [now, site.timezone]);
  const month = search.month && months.includes(search.month) ? search.month : months[0];
  const currentYear = wallTime(now, site.timezone).year;
  const years = [currentYear, currentYear - 1, currentYear - 2];
  const year = search.year && years.includes(search.year) ? search.year : currentYear;
  const span = useMemo(() => reportWindow(month, now, site.timezone), [month, now, site.timezone]);

  return (
    <Main>
      <PageHeader
        className="print:hidden"
        title="能源报告"
        description={`${site.displayName} · 按月、按年汇总用能、能效、电费与运维；已结束的月份按存档显示`}
        meta={view === 'year' ? <PageMeta>{year} 年</PageMeta> : span.complete ? <PageMeta dot="live">已结月</PageMeta> : <PageMeta dot="warning">本月进行中，统计到当前</PageMeta>}
        actions={(
          <div className="flex items-center gap-2 print:hidden">
            <PeriodTabs aria-label="报告周期" options={VIEWS} value={view} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, view: value as ReportView }), replace: true })} />
            {view === 'month' ? (
              <Select value={month} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, month: value }), replace: true })}>
                <SelectTrigger className="h-8 w-36" aria-label="报告月份"><SelectValue /></SelectTrigger>
                <SelectContent>{months.map((option) => <SelectItem key={option} value={option}>{monthLabel(option)}</SelectItem>)}</SelectContent>
              </Select>
            ) : (
              <Select value={String(year)} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, year: Number(value) }), replace: true })}>
                <SelectTrigger className="h-8 w-28" aria-label="报告年份"><SelectValue /></SelectTrigger>
                <SelectContent>{years.map((option) => <SelectItem key={option} value={String(option)}>{option} 年</SelectItem>)}</SelectContent>
              </Select>
            )}
            <Button size="sm" onClick={() => globalThis.print()}>
              <Printer data-icon="inline-start" />
              导出 PDF
            </Button>
          </div>
        )}
      />
      {view === 'year' ? <YearlyReport key={year} year={year} now={now} /> : <MonthlyReport key={month} month={month} span={span} />}
    </Main>
  );
}

const sheetFrame = 'mx-auto w-full max-w-[1040px]';

/** A closed month reads from its snapshot when it has one; otherwise it is built from the records. */
function MonthlyReport({ month, span }: { readonly month: string; readonly span: ReportWindow }) {
  const { site } = siteRoute.useRouteContext();
  const snapshot = useQuery({ ...monthlySnapshotQuery(site.id, month), enabled: span.complete });
  if (span.complete && snapshot.isPending) return <Skeleton className={`${sheetFrame} h-[900px] rounded-xl`} />;
  if (snapshot.data) return <ArchivedMonthlyReport stored={snapshot.data} span={span} />;
  return <LiveMonthlyReport month={month} span={span} archiveUnavailable={snapshot.isError} />;
}

const periodRange = (span: ReportWindow, timeZone: string) => {
  const range = new Intl.DateTimeFormat('zh-CN', { timeZone, month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false });
  return { range, text: `统计期间 ${range.format(span.from)} 至 ${range.format(span.to)}${span.complete ? '' : '（本月进行中）'}` };
};

function ArchivedMonthlyReport({ stored, span }: { readonly stored: StoredReport; readonly span: ReportWindow }) {
  const { site } = siteRoute.useRouteContext();
  const report = stored.report;
  const previousMonth = useMemo(() => {
    const [year, number] = report.month.split('-').map(Number);
    const date = new Date(Date.UTC(year, number - 2, 1));
    return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`;
  }, [report.month]);
  const previous = useQuery(monthlySnapshotQuery(site.id, previousMonth));
  const { meter, demand } = useMonthDemand(report.month, span);
  const { alarms, workOrders } = useMonthOperations(report.month, span);
  const tariff = useSiteTariff();
  const charges = useMemo(
    () => (tariff.data && demand.data ? demandCharges(demand.data, tariff.data, site.timezone, span.from) : null),
    [tariff.data, demand.data, site.timezone, span.from],
  );
  const peakDemand = useMemo(() => {
    let peak: { at: string; demandKW: number } | null = null;
    for (const hour of demand.data ?? []) if (hour.complete && (!peak || hour.demandKW > peak.demandKW)) peak = { at: hour.start, demandKW: hour.demandKW };
    return peak;
  }, [demand.data]);

  const { range, text } = periodRange(span, site.timezone);
  const generated = new Intl.DateTimeFormat('zh-CN', { timeZone: site.timezone, month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(stored.generatedAt));
  const before = previous.data?.report ?? null;
  const beforeNote = (value: number | null, digits = 0) => (previous.isPending ? '…' : before === null ? '上月未存档' : `上月 ${digits ? (value === null ? '—' : formatDecimal(value, digits)) : energyText(value)}`);
  const peak = snapshotPeak(report);
  const periods = report.tariffPeriods.map((period) => ({ code: period.code, energyKWh: period.electricityKWh, cost: period.costCNY, rate: period.electricityKWh > 0 ? period.costCNY / period.electricityKWh : 0 }));
  const demandCost = charges?.reduce((sum, charge) => sum + (charge.charge ?? 0), 0) ?? 0;

  return (
    <ReportSheet
      label={`${monthLabel(report.month)}能源报告`}
      kicker={`${site.displayName} · 冷站能源报告`}
      title={monthLabel(report.month)}
      period={text}
      status={`已存档 · 生成于 ${generated}${stored.version > 1 ? ` · 第 ${stored.version} 版` : ''} · 覆盖 ${report.daysWithData} / ${report.daysInMonth} 天`}
      footnote={<>说明：本报告的用能、能效、电度电费、节能与诊断发现取自月结后存档的快照，之后记录再变化也不会改变；需量、告警与工单{NOT_ARCHIVED}。电费按当时生效的分时电价逐时计算，正式电费以结算为准。{alarms.data && !alarms.data.complete ? ' 告警超过读取上限，统计可能不全。' : ''}</>}
    >
      <Section index={1} title="概要" description="与上月全月对比">
        <div className="grid grid-cols-4 gap-8">
          <Figure label="空调用电" value={formatDecimal(report.electricityKWh, 0)} unit="kWh" delta={before ? relativeChange(report.electricityKWh, before.electricityKWh) : null} invert note={beforeNote(before?.electricityKWh ?? null)} />
          <Figure label="供冷量" value={formatDecimal(report.coolingKWh, 0)} unit="kWh" delta={before ? relativeChange(report.coolingKWh, before.coolingKWh) : null} note={beforeNote(before?.coolingKWh ?? null)} />
          <Figure label="综合能效" value={report.cop === null ? null : formatDecimal(report.cop, 2)} unit="COP" delta={before ? relativeChange(report.cop, before.cop) : null} note={beforeNote(before?.cop ?? null, 2)} />
          <Figure label="电度电费" value={report.energyCostCNY === null ? null : formatDecimal(report.energyCostCNY, 0)} unit="元" note={report.energyCostCNY === null ? '月内有时段没有电价，未计' : '按分时电价逐时计算，不含需量电费'} />
        </div>
      </Section>

      <Section index={2} title="用能与能效" description={`每日空调用电与综合能效；${report.daysWithData} 天有计量数据`}>
        <EnergyTrend rows={snapshotDays(report)} />
        <div className="grid grid-cols-3 gap-8">
          <FactList>
            <FactRow label="日均用电">{report.daysWithData ? energyText(report.electricityKWh / report.daysWithData) : '—'}</FactRow>
            <FactRow label="用电最多的一天" detail={peak ? energyText(peak.electricityKWh) : undefined}>{peak?.label ?? '—'}</FactRow>
          </FactList>
          <FactList>
            <FactRow label="最大需量" detail={peakDemand ? `${range.format(new Date(peakDemand.at))} · ${NOT_ARCHIVED}` : NOT_ARCHIVED}>{peakDemand ? `${formatDecimal(peakDemand.demandKW, 1)} kW` : meter ? '—' : '没有总电表'}</FactRow>
            <FactRow label="计量覆盖">{`${report.daysWithData} / ${report.daysInMonth} 天`}</FactRow>
          </FactList>
          <FactList>
            <FactRow label="上月空调用电">{before ? energyText(before.electricityKWh) : '上月未存档'}</FactRow>
            <FactRow label="上月综合能效">{before ? (before.cop === null ? '—' : formatDecimal(before.cop, 2)) : '上月未存档'}</FactRow>
          </FactList>
        </div>
      </Section>

      <Section index={3} title="电费构成" description="分时电度电费取自存档；需量电费按当前记录与电价现算，正式电费以结算为准">
        {report.energyCostCNY !== null ? (
          <CostTable periods={periods} charges={charges} chargesNote={NOT_ARCHIVED} energyKWh={report.electricityKWh} />
        ) : (
          <p className="text-sm text-muted-foreground">这个月有时段没有电价，存档未计电费{demandCost ? `；需量电费现算 ${formatDecimal(demandCost, 0)} 元` : ''}。</p>
        )}
      </Section>

      <Section index={4} title="节能成效" description="对照实施前基线、按实际供冷量调整后核算的节电量与节省电费（IPMVP 选项 C）">
        <SavingsGrid totals={report.savings} daysNote={`本月 ${report.daysInMonth} 天`} />
      </Section>

      <Section index={5} title="运行与维护" description="本月发生的告警、工单与诊断发现">
        <OperationsGrid
          alarms={alarms.data ? alarmSummary(alarms.data.alarms, span.from, span.to) : null}
          orders={workOrders.data ? workOrderSummary(workOrders.data, span.from, span.to) : null}
          findings={report.findings}
          liveNote={NOT_ARCHIVED}
        />
      </Section>
    </ReportSheet>
  );
}

/** The month's verified savings, and which measures were in verification during it. */
function LiveSavings({ siteId, month, span, timeZone }: { readonly siteId: string; readonly month: string; readonly span: ReportWindow; readonly timeZone: string }) {
  // Verified days are Site-local dates: the whole month once it is over, through today while it runs.
  const to = span.complete ? `${month}-${String(span.days).padStart(2, '0')}` : monthToDate(span.to, timeZone).to;
  const summary = useQuery(summaryQuery(siteId, `${month}-01`, to));
  const measures = useQuery(measuresQuery(siteId));
  if (summary.isPending || measures.isPending) return <Skeleton className="h-20" />;
  if (summary.isError || measures.isError) return <p className="text-sm text-muted-foreground">节能核算暂不可用</p>;
  const active = measures.data.filter((measure) => measure.implementedAt !== null
    && Date.parse(measure.implementedAt) < span.to.getTime() && (measure.endedAt === null || Date.parse(measure.endedAt) > span.from.getTime()));
  return (
    <>
      <SavingsGrid totals={summary.data.totals} daysNote={span.complete ? `本月 ${span.days} 天` : '每天 02:00 核算前一天'} />
      <p className="text-sm text-muted-foreground">
        {active.length === 0
          ? '本月没有实施中的节能措施。'
          : active.map((measure) => `「${measure.name}」${measure.baseline?.trusted ? STATUS_LABEL[measure.status] : '基线不可信，未核算'}`).join('；')}
      </p>
    </>
  );
}

function LiveMonthlyReport({ month, span, archiveUnavailable }: { readonly month: string; readonly span: ReportWindow; readonly archiveUnavailable: boolean }) {
  const { site } = siteRoute.useRouteContext();
  const timeZone = site.timezone;
  const energy = useMonthEnergy(month, span);
  const { meter, demand } = useMonthDemand(month, span);
  const { alarms, workOrders } = useMonthOperations(month, span);
  const findings = useQuery(siteFindingsQuery(site.id));
  const tariff = useSiteTariff();

  const cost = useMemo(
    () => (tariff.data && energy.data ? touEnergyCost(energy.data.hourly, tariff.data, timeZone) : null),
    [tariff.data, energy.data, timeZone],
  );
  const charges = useMemo(
    () => (tariff.data && demand.data ? demandCharges(demand.data, tariff.data, timeZone, span.from) : null),
    [tariff.data, demand.data, timeZone, span.from],
  );
  const peakDemand = useMemo(() => {
    let peak: { at: string; demandKW: number } | null = null;
    for (const hour of demand.data ?? []) if (hour.complete && (!peak || hour.demandKW > peak.demandKW)) peak = { at: hour.start, demandKW: hour.demandKW };
    return peak;
  }, [demand.data]);

  if (energy.isPending) return <Skeleton className={`${sheetFrame} h-[900px] rounded-xl`} />;
  if (energy.isError || !energy.data) {
    return <div className={`${sheetFrame} rounded-xl border bg-card py-16 text-center text-sm text-muted-foreground`}>报告所需的能耗数据暂不可用</div>;
  }

  const { current, previous } = energy.data;
  const demandCost = charges?.reduce((sum, charge) => sum + (charge.charge ?? 0), 0) ?? 0;
  const totalCost = cost ? cost.total + demandCost : null;
  const days = current.rows.filter((row) => row.electricityKWh !== null);
  // Every day of the month on the axis, so days without metering show as gaps.
  const byLabel = new Map(current.rows.map((row) => [row.label.split(' ')[0], row]));
  const monthNumber = Number(month.slice(5, 7));
  const rows = Array.from({ length: span.days }, (_, index) => {
    const label = `${monthNumber}/${index + 1}`;
    const row = byLabel.get(label);
    return { label, electricityKWh: row?.electricityKWh == null ? null : Math.round(row.electricityKWh), cop: row?.cop == null ? null : Number(row.cop.toFixed(2)) };
  });
  const { range, text } = periodRange(span, timeZone);
  const periods = cost && tariff.data
    ? cost.byPeriod.map((usage) => ({ code: usage.code, energyKWh: usage.energyKWh, cost: usage.cost, rate: tariff.data!.periods.find((period) => period.periodCode === usage.code)?.energyRatePerKWh ?? 0 }))
    : [];

  return (
    <ReportSheet
      label={`${monthLabel(month)}能源报告`}
      kicker={`${site.displayName} · 冷站能源报告`}
      title={monthLabel(month)}
      period={text}
      status={span.complete ? (archiveUnavailable ? '实时 · 报告存档暂不可用，按当前记录现算' : '实时 · 这个月还没有存档，按当前记录现算') : '实时 · 本月结束后的 1 日 03:00 存档'}
      footnote={<>说明：空调用电、供冷量取自空调总电表与冷量表的有效计量区间；电费按站点当前生效的分时电价估算，需量按 15 分钟平均功率计，正式电费以结算为准。{alarms.data && !alarms.data.complete ? ' 告警超过读取上限，统计可能不全。' : ''}</>}
    >
      <Section index={1} title="概要" description={span.complete ? '与上月全月对比' : '与上月同期对比'}>
        <div className="grid grid-cols-4 gap-8">
          <Figure label="空调用电" value={current.electricityKWh === null ? null : formatDecimal(current.electricityKWh, 0)} unit="kWh" delta={relativeChange(current.electricityKWh, previous.electricityKWh)} invert note={`上月 ${energyText(previous.electricityKWh)}`} />
          <Figure label="供冷量" value={current.coolingKWh === null ? null : formatDecimal(current.coolingKWh, 0)} unit="kWh" delta={relativeChange(current.coolingKWh, previous.coolingKWh)} note={`上月 ${energyText(previous.coolingKWh)}`} />
          <Figure label="综合能效" value={current.cop === null ? null : formatDecimal(current.cop, 2)} unit="COP" delta={relativeChange(current.cop, previous.cop)} note={previous.cop === null ? '上月没有数据' : `上月 ${formatDecimal(previous.cop, 2)}`} />
          <Figure label="估算电费" value={totalCost === null ? null : formatDecimal(totalCost, 0)} unit="元" note={tariff.data === null ? '站点还没有配置电价' : '按分时电价估算，以结算为准'} />
        </div>
      </Section>

      <Section index={2} title="用能与能效" description={`每日空调用电与综合能效；${days.length} 天有计量数据`}>
        <EnergyTrend rows={rows} />
        <div className="grid grid-cols-3 gap-8">
          <FactList>
            <FactRow label="日均用电">{days.length ? energyText((current.electricityKWh ?? 0) / days.length) : '—'}</FactRow>
            <FactRow label="用电最多的一天" detail={current.peak ? energyText(current.peak.electricityKWh) : undefined}>{current.peak?.label ?? '—'}</FactRow>
          </FactList>
          <FactList>
            <FactRow label="最大需量" detail={peakDemand ? range.format(new Date(peakDemand.at)) : undefined}>{peakDemand ? `${formatDecimal(peakDemand.demandKW, 1)} kW` : meter ? '—' : '没有总电表'}</FactRow>
            <FactRow label="计量质量">{current.excludedIntervals > 0 ? `${current.excludedIntervals} 个区间未计入` : '全部有效'}</FactRow>
          </FactList>
          <FactList>
            <FactRow label="上月空调用电">{energyText(previous.electricityKWh)}</FactRow>
            <FactRow label="上月综合能效">{previous.cop === null ? '—' : formatDecimal(previous.cop, 2)}</FactRow>
          </FactList>
        </div>
      </Section>

      <Section index={3} title="电费构成" description={tariff.data ? `${tariff.data.displayName} · 按分时电价估算，正式电费以结算为准` : '站点还没有配置电价'}>
        {cost && tariff.data ? (
          <CostTable periods={periods} charges={charges} energyKWh={cost.energyKWh} />
        ) : (
          <p className="text-sm text-muted-foreground">为站点的电网进线发布电价后，报告按峰平谷单价与需量电价估算电费。</p>
        )}
      </Section>

      <Section index={4} title="节能成效" description="对照实施前基线、按实际供冷量调整后核算的节电量与节省电费（IPMVP 选项 C）">
        <LiveSavings siteId={site.id} month={month} span={span} timeZone={timeZone} />
      </Section>

      <Section index={5} title="运行与维护" description="本月发生的告警、工单与诊断发现">
        <OperationsGrid
          alarms={alarms.data ? alarmSummary(alarms.data.alarms, span.from, span.to) : null}
          orders={workOrders.data ? workOrderSummary(workOrders.data, span.from, span.to) : null}
          findings={findings.data ? findingSummary(findings.data, span.from, span.to) : null}
          findingsUnavailable={findings.isError}
        />
      </Section>
    </ReportSheet>
  );
}
