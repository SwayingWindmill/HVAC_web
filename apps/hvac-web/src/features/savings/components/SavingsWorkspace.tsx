import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { Leaf, Plus } from 'lucide-react';
import { useMemo, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ComposedChart } from '@/components/ui/composed-chart';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { measureQuery, measuresQuery, savingsKeys, SavingsRequestError, summaryQuery } from '../query';
import { boundaryLabel, costBasis, dayRows, monthToDate, savingsRate, siteSavingsRate, type Measure, type MeasureStatus } from '../savings-model';
import { usePlantMachines } from '../use-plant-machines';
import { MeasureSheet } from './MeasureSheet';
import { MeasureStatusBadge } from './measure-presentation';
import { RegisterMeasureDialog } from './RegisterMeasureDialog';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/energy-analysis/savings');

const STATUS_ORDER = { IMPLEMENTED: 0, PLANNED: 1, ENDED: 2 } as const;
type Filter = 'all' | MeasureStatus;
const FILTERS: readonly { readonly value: Filter; readonly label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'IMPLEMENTED', label: '统计中' },
  { value: 'PLANNED', label: '计划中' },
  { value: 'ENDED', label: '已结束' },
];

const shortDate = (value: string, timeZone: string) => formatTime(value, timeZone).slice(0, 5);

function Figure({ label, info, value, unit, children }: { readonly label: string; readonly info?: string; readonly value: string | null; readonly unit?: string; readonly children?: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-5 py-4">
      <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">{label}{info ? <InfoTip>{info}</InfoTip> : null}</span>
      <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
        {value ?? '—'}
        {value !== null && unit ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
      </span>
      {children ? <span className="truncate text-xs text-muted-foreground">{children}</span> : null}
    </div>
  );
}

/** The days a measure saved, added up, so its trend reads at a glance. */
function CumulativeSavings({ measureId }: { readonly measureId: string }) {
  const { site } = siteRoute.useRouteContext();
  const detail = useQuery(measureQuery(site.id, measureId));
  const rows = useMemo(() => {
    let total = 0;
    return dayRows(detail.data?.days ?? []).map((row) => { total += row.avoided; return { label: row.label, total: Math.round(total) }; });
  }, [detail.data]);
  if (detail.isPending) return <Skeleton className="h-[180px]" />;
  if (rows.length < 2) return <p className="flex h-[180px] items-center justify-center text-sm text-muted-foreground">统计满两天后显示累计走势</p>;
  return (
    <ComposedChart
      data={rows}
      xKey="label"
      series={[{ key: 'total', label: '累计节电 (kWh)', type: 'area', color: 'var(--chart-2)' }]}
      showGrid
      showYAxis
      yDomain="zero"
      yFormatter={(value) => formatDecimal(value, 0)}
      animate={false}
      className="h-[180px]"
    />
  );
}

function MeasureRow({ measure, timeZone, names, onInspect }: {
  readonly measure: Measure;
  readonly timeZone: string;
  readonly names: ReadonlyMap<string, string> | null;
  readonly onInspect: () => void;
}) {
  const rate = savingsRate(measure.totals);
  const verified = measure.totals.days > 0;
  const when = measure.implementedAt
    ? `${shortDate(measure.implementedAt, timeZone)}${measure.endedAt ? ` – ${shortDate(measure.endedAt, timeZone)}` : ''}`
    : `计划 ${shortDate(measure.plannedAt, timeZone)}`;
  const dash = <span className="text-muted-foreground">—</span>;
  return (
    <TableRow className="cursor-pointer" onClick={onInspect}>
      <TableCell className="pl-5">
        <button type="button" className="block text-left font-semibold outline-none hover:underline focus-visible:underline" onClick={(event) => { event.stopPropagation(); onInspect(); }}>
          {measure.name}
        </button>
        <span className="block max-w-[640px] truncate text-xs text-muted-foreground" title={measure.description || undefined}>
          {boundaryLabel(measure.boundaryDeviceId, names)} · <span className="font-mono">{when}</span>{measure.description ? ` · ${measure.description}` : ''}
        </span>
      </TableCell>
      <TableCell>
        <span className="flex flex-col items-start gap-0.5">
          <MeasureStatusBadge measure={measure} />
          {verified ? <span className="text-xs text-muted-foreground">已统计 {measure.totals.days} 天</span> : null}
        </span>
      </TableCell>
      <TableCell className="text-right tabular-nums">
        {verified ? <><span className="font-semibold">{formatDecimal(measure.totals.avoidedKWh, 0)}</span><span className="ml-1 text-xs text-subtle-foreground">kWh</span></> : dash}
      </TableCell>
      <TableCell className="text-right tabular-nums">{rate === null ? dash : `${formatDecimal(rate * 100, 1)}%`}</TableCell>
      <TableCell className="pr-5 text-right tabular-nums">{verified ? `¥${formatDecimal(measure.totals.avoidedCost, 0)}` : dash}</TableCell>
    </TableRow>
  );
}

export function SavingsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/energy-analysis/savings' });
  const queryClient = useQueryClient();
  const [registering, setRegistering] = useState(false);
  const [filter, setFilter] = useState<Filter>('all');
  const month = useMemo(() => monthToDate(new Date(), site.timezone), [site.timezone]);
  const measures = useQuery(measuresQuery(site.id));
  const summary = useQuery(summaryQuery(site.id, month.from, month.to));
  const { machines, names } = usePlantMachines();
  const canManage = principal.authorization.capabilities.includes('savings.manage');
  const inspect = (measureId: string | undefined) => void navigate({ search: (previous) => ({ ...previous, inspect: measureId }), replace: true });

  const list = [...(measures.data ?? [])].sort((left, right) =>
    STATUS_ORDER[left.status] - STATUS_ORDER[right.status] || right.plannedAt.localeCompare(left.plannedAt));
  const shown = filter === 'all' ? list : list.filter((measure) => measure.status === filter);
  const reporting = list.filter((measure) => measure.status === 'IMPLEMENTED');
  const untrusted = reporting.filter((measure) => !measure.baseline?.trusted).length;
  const counted = list.filter((measure) => measure.totals.days > 0);
  const lifetime = list.reduce((sum, measure) => ({ kwh: sum.kwh + measure.totals.avoidedKWh, cost: sum.cost + measure.totals.avoidedCost }), { kwh: 0, cost: 0 });
  const since = counted.map((measure) => measure.implementedAt!).sort()[0];
  const monthTotals = summary.data?.totals;
  const monthRate = monthTotals ? siteSavingsRate(monthTotals) : null;
  // The measure the chart follows: the one being counted with the most days behind it.
  const followed = [...reporting].filter((measure) => measure.baseline?.trusted).sort((left, right) => right.totals.days - left.totals.days)[0]
    ?? [...counted].sort((left, right) => (right.endedAt ?? '').localeCompare(left.endedAt ?? ''))[0];

  const registerButton = (
    <Button size="sm" onClick={() => setRegistering(true)}>
      <Plus data-icon="inline-start" />
      登记措施
    </Button>
  );

  return (
    <Main>
      <PageHeader title="节能项目" description={site.displayName} actions={canManage ? registerButton : null} />

      {summary.isSuccess && measures.isSuccess && list.length > 0 ? (
        <Panel aria-label="节能成效">
          <div className="grid grid-cols-4 divide-x divide-divider border-b border-divider">
            <Figure label="本月节电量" info="没做措施时的预计用电 − 实际用电" value={monthTotals!.days > 0 ? formatDecimal(monthTotals!.avoidedKWh, 0) : null} unit="kWh">
              {monthTotals!.days > 0 ? `已统计 ${monthTotals!.days} 天` : '本月还没有统计结果'}
            </Figure>
            <Figure label="本月节电率" info="节电量 ÷（冷站同期用电 + 节电量）" value={monthRate === null ? null : formatDecimal(monthRate * 100, 1)} unit="%">
              {monthTotals!.days > 0 ? `冷站同期用电 ${formatDecimal(monthTotals!.plantKWh, 0)} kWh` : '有统计结果后显示'}
            </Figure>
            <Figure label="本月节省电费" info={`节电量逐时乘以当时的电度电价；${costBasis(monthTotals!)}`} value={monthTotals!.days > 0 ? `¥${formatDecimal(monthTotals!.avoidedCost, 0)}` : null} />
            <Figure label="累计" value={counted.length ? formatDecimal(lifetime.kwh, 0) : null} unit="kWh">
              {counted.length ? `节省 ¥${formatDecimal(lifetime.cost, 0)}${since ? ` · 自 ${shortDate(since, site.timezone)} 起` : ''}` : '还没有统计结果'}
            </Figure>
          </div>
          <div className="flex flex-col gap-2 px-5 pt-3.5 pb-3">
            <div className="flex min-h-7 items-center gap-2.5 text-sm">
              <span className="font-semibold">累计节电</span>
              <span className="truncate text-xs text-muted-foreground">
                {followed ? `${followed.status === 'IMPLEMENTED' ? '统计中' : '已结束'} · ${followed.name} · kWh` : 'kWh'}
              </span>
              <span className={cn('ml-auto text-xs', untrusted > 0 ? 'text-warning-text' : 'text-muted-foreground')}>
                {reporting.length === 0 ? '没有正在统计的措施'
                  : `正在统计 ${reporting.length === 1 ? reporting[0].name : `${reporting.length} 项措施`} · ${untrusted === 0 ? '对比基线均可靠' : `${untrusted} 项对比基线不可靠，暂不统计`}`}
              </span>
            </div>
            {followed ? <CumulativeSavings measureId={followed.id} /> : <p className="flex h-[120px] items-center justify-center text-sm text-muted-foreground">措施实施并统计后，这里显示累计节电</p>}
          </div>
        </Panel>
      ) : null}

      <Panel aria-label="节能措施">
        <PanelHeader
          title={<span className="flex items-center gap-1.5">节能措施<InfoTip>同一块电表同一时间只统计一项措施；用冷站总电表统计的措施，不能和单台设备的措施同时统计</InfoTip></span>}
          action={list.length > 0 ? (
            <ToggleGroup type="single" value={filter} onValueChange={(value) => { if (value) setFilter(value as Filter); }} aria-label="措施状态" className="h-12 gap-5">
              {FILTERS.map((option) => (
                <ToggleGroupItem
                  key={option.value}
                  value={option.value}
                  className="h-full flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
                >
                  {option.label}
                  <span className="font-mono text-[11px] text-subtle-foreground">{option.value === 'all' ? list.length : list.filter((measure) => measure.status === option.value).length}</span>
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          ) : null}
        />
        {measures.isPending ? <div className="p-4"><Skeleton className="h-40" /></div> : measures.isError ? (
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">{measures.error instanceof SavingsRequestError ? measures.error.message : '节能统计服务暂时不可用'}</p>
            <Button variant="outline" size="sm" onClick={() => void measures.refetch()}>重试</Button>
          </div>
        ) : list.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
            <span className="flex size-9 items-center justify-center rounded-xl bg-success-soft text-success-text"><Leaf className="size-[18px]" aria-hidden="true" /></span>
            <p className="text-sm font-semibold">还没有节能措施</p>
            <p className="max-w-md text-xs text-muted-foreground">登记一项措施，例如提高冷冻水供水温度。确认实施后，系统用实施前 14 天的数据算出对比基线，每天统计节电量与电费。</p>
            {canManage ? <div className="mt-2">{registerButton}</div> : null}
          </div>
        ) : (
          <Table aria-label="节能措施列表">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">措施</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">累计节电</TableHead>
                <TableHead className="text-right">节电率</TableHead>
                <TableHead className="pr-5 text-right">节省电费</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.length === 0 ? (
                <TableRow className="hover:bg-transparent"><TableCell colSpan={5} className="py-8 text-center text-muted-foreground">没有这类措施</TableCell></TableRow>
              ) : shown.map((measure) => (
                <MeasureRow key={measure.id} measure={measure} timeZone={site.timezone} names={names} onInspect={() => inspect(measure.id)} />
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

      <RegisterMeasureDialog
        open={registering}
        machines={machines}
        onOpenChange={setRegistering}
        onRegistered={(measure) => {
          toast.success('措施已登记');
          void queryClient.invalidateQueries({ queryKey: savingsKeys.all(site.id) });
          inspect(measure.id);
        }}
      />
      <MeasureSheet measureId={search.inspect} names={names} canManage={canManage} onClose={() => inspect(undefined)} />
    </Main>
  );
}
