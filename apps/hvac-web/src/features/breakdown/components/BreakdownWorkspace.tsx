import { getRouteApi, useNavigate, useSearch } from '@tanstack/react-router';
import { Download, RefreshCw, Workflow } from 'lucide-react';
import { useMemo } from 'react';
import { Layer, Rectangle, Sankey, type SankeyNodeProps } from 'recharts';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ChartContainer, type ChartConfig } from '@/components/ui/chart';
import { DeltaBadge } from '@/components/ui/delta-badge';
import { PeriodTabs } from '@/components/ui/period-tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { bucketLabel, periodWindow, relativeChange, wallTime } from '@/features/energy-analysis/consumption/model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { EQUIPMENT_ORDER, type Breakdown, type EquipmentCategory } from '../breakdown-model';
import { useEnergyBreakdown } from '../query';

const siteRoute = getRouteApi('/_app/_site');

type BreakdownPeriod = 'today' | '7d' | 'month';
const PERIODS: readonly { value: BreakdownPeriod; label: string }[] = [
  { value: 'today', label: '今日' },
  { value: '7d', label: '近 7 天' },
  { value: 'month', label: '本月' },
];
const PREVIOUS_LABEL: Record<BreakdownPeriod, string> = { today: '昨日同时段', '7d': '前 7 天', month: '上月同期' };

const CATEGORY_LABEL: Readonly<Record<EquipmentCategory, string>> = {
  CHILLER: '冷水机组',
  CHILLED_WATER_PUMP: '冷冻水泵',
  COOLING_WATER_PUMP: '冷却水泵',
  COOLING_TOWER: '冷却塔',
};

const CATEGORY_COLOR: Readonly<Record<EquipmentCategory | 'UNALLOCATED', string>> = {
  CHILLER: 'var(--chart-1)',
  CHILLED_WATER_PUMP: 'var(--chart-2)',
  COOLING_WATER_PUMP: 'var(--chart-3)',
  COOLING_TOWER: 'var(--chart-4)',
  UNALLOCATED: 'var(--chart-5)',
};

/** The plant's subsystems: the chiller makes cold, the pumps move water, the tower rejects heat. */
const SUBSYSTEMS: readonly { name: string; categories: readonly EquipmentCategory[] }[] = [
  { name: '主机系统', categories: ['CHILLER'] },
  { name: '输配系统', categories: ['CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP'] },
  { name: '散热系统', categories: ['COOLING_TOWER'] },
];

const UNALLOCATED = '未单独计量';

const chartConfig = Object.fromEntries(
  (Object.keys(CATEGORY_LABEL) as EquipmentCategory[]).map((category) => [category, { label: CATEGORY_LABEL[category], color: CATEGORY_COLOR[category] }]),
) satisfies ChartConfig;

const kWh = (value: number) => (value >= 10_000 ? `${formatDecimal(value / 1000, 2)} MWh` : `${formatDecimal(value, 0)} kWh`);
const percent = (share: number) => `${formatDecimal(share * 100, 1)}%`;
const dash = <span className="text-muted-foreground">—</span>;
/** Whole kWh, without a minus sign on a difference that rounds to nothing. */
const wholeKWh = (value: number) => formatDecimal(Math.abs(value) < 0.5 ? 0 : value, 0);

interface FlowNode {
  readonly name: string;
  readonly color: string;
  readonly energyKWh: number;
}

/** Total → subsystem → machine, with what the sub-meters leave unexplained as its own branch. */
function flowData(breakdown: Breakdown) {
  const nodes: FlowNode[] = [];
  const links: { source: number; target: number; value: number }[] = [];
  const add = (node: FlowNode) => nodes.push(node) - 1;
  const total = add({ name: '空调总用电', color: 'var(--foreground)', energyKWh: breakdown.totalKWh ?? breakdown.subMeteredKWh });
  for (const subsystem of SUBSYSTEMS) {
    const machines = breakdown.items.filter((item) => subsystem.categories.includes(item.category) && item.energyKWh > 0);
    if (machines.length === 0) continue;
    const energy = machines.reduce((sum, item) => sum + item.energyKWh, 0);
    const node = add({ name: subsystem.name, color: CATEGORY_COLOR[machines[0].category], energyKWh: energy });
    links.push({ source: total, target: node, value: energy });
    for (const machine of machines) {
      links.push({ source: node, target: add({ name: machine.name, color: CATEGORY_COLOR[machine.category], energyKWh: machine.energyKWh }), value: machine.energyKWh });
    }
  }
  if (breakdown.unallocatedKWh !== null && breakdown.unallocatedKWh > 0.5) {
    links.push({ source: total, target: add({ name: UNALLOCATED, color: CATEGORY_COLOR.UNALLOCATED, energyKWh: breakdown.unallocatedKWh }), value: breakdown.unallocatedKWh });
  }
  return { nodes, links };
}

function FlowNodeShape({ x, y, width, height, payload }: SankeyNodeProps) {
  const node = payload as unknown as FlowNode;
  return (
    <Layer>
      <Rectangle x={x} y={y} width={width} height={height} fill={node.color} fillOpacity={0.9} radius={2} />
      <text x={x + width + 8} y={y + height / 2 - 7} dominantBaseline="middle" className="fill-foreground text-xs font-medium">
        {node.name}
      </text>
      <text x={x + width + 8} y={y + height / 2 + 8} dominantBaseline="middle" className="fill-muted-foreground text-[11px] tabular-nums">
        {kWh(node.energyKWh)}
      </text>
    </Layer>
  );
}

function EnergyFlow({ breakdown }: { readonly breakdown: Breakdown }) {
  const data = useMemo(() => flowData(breakdown), [breakdown]);
  return (
    <ChartContainer config={chartConfig} className="aspect-auto h-[320px] w-full">
      <Sankey
        data={data}
        nodeWidth={10}
        nodePadding={28}
        linkCurvature={0.55}
        iterations={0}
        margin={{ top: 12, right: 120, bottom: 28, left: 8 }}
        node={(props: SankeyNodeProps) => <FlowNodeShape {...props} />}
        link={{ stroke: 'var(--chart-1)', strokeOpacity: 0.16 }}
      />
    </ChartContainer>
  );
}

/** One category's energy per bucket on its own scale, the running bucket drawn lighter. */
function CategoryBars({ category, rows, labelOf, noun }: {
  readonly category: EquipmentCategory;
  readonly rows: Breakdown['rows'];
  readonly labelOf: (key: string) => string;
  readonly noun: string;
}) {
  const values = rows.map((row) => row[category]);
  const top = Math.max(...values, 0);
  const complete = values.slice(0, -1);
  const average = complete.length > 0 ? complete.reduce((sum, value) => sum + value, 0) / complete.length : null;
  const peakIndex = values.indexOf(top);
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-divider px-4 py-3">
      <span className="flex items-center gap-2 text-sm">
        <span className="size-2 shrink-0 rounded-[2px]" style={{ background: CATEGORY_COLOR[category] }} aria-hidden="true" />
        <span className="font-medium">{CATEGORY_LABEL[category]}</span>
        <span className="ml-auto text-xs text-muted-foreground tabular-nums">{average === null ? '—' : `${noun}均 ${formatDecimal(average, 0)} kWh`}</span>
      </span>
      <div
        role="img"
        aria-label={`${CATEGORY_LABEL[category]}每${noun}用电，最高 ${rows.length > 0 ? labelOf(rows[peakIndex].key) : '—'} ${formatDecimal(top, 0)} kWh`}
        className="flex h-16 items-end gap-[3px]"
      >
        {rows.map((row, index) => (
          <span
            key={row.key}
            title={`${labelOf(row.key)} · ${formatDecimal(row[category], 1)} kWh${index === rows.length - 1 ? '（进行中）' : ''}`}
            className={cn('min-w-0 flex-1 rounded-t-[2px]', index === rows.length - 1 && 'opacity-40')}
            style={{ height: `${top > 0 ? Math.max(2, (row[category] / top) * 100) : 2}%`, background: CATEGORY_COLOR[category] }}
          />
        ))}
      </div>
      <span className="flex justify-between text-[11px] text-muted-foreground tabular-nums">
        <span>{rows.length > 0 ? labelOf(rows[0].key) : ''}</span>
        <span>{rows.length > 0 ? `最高 ${labelOf(rows[peakIndex].key)}` : ''}</span>
        <span>{rows.length > 0 ? labelOf(rows[rows.length - 1].key) : ''}</span>
      </span>
    </div>
  );
}

export function BreakdownWorkspace() {
  const { site } = siteRoute.useRouteContext();
  const search = useSearch({ from: '/_app/_site/energy-analysis/breakdown' });
  const navigate = useNavigate({ from: '/energy-analysis/breakdown' });
  const period: BreakdownPeriod = search.period ?? 'today';
  const previousLabel = PREVIOUS_LABEL[period];
  const { machines, registry, query, previous } = useEnergyBreakdown(period);
  const breakdown = query.data;
  const granularity = period === 'today' ? 'hour' : 'day';
  const labelOf = (key: string) => (granularity === 'hour' ? bucketLabel(key, 'hour', site.timezone) : `${key.slice(5, 7)}/${key.slice(8, 10)}`);
  const before = new Map((previous.data?.items ?? []).map((item) => [item.deviceId, item.energyKWh]));
  const range = (() => {
    const { from } = periodWindow(period, new Date(), site.timezone);
    const day = (instant: Date) => { const local = wallTime(instant, site.timezone); return `${String(local.month).padStart(2, '0')}/${String(local.day).padStart(2, '0')}`; };
    return period === 'today' ? `今日 ${day(new Date())}` : `${day(from)} – ${day(new Date())}`;
  })();

  const exportCsv = () => {
    if (!breakdown) return;
    const lines = [
      [site.displayName, PERIODS.find((option) => option.value === period)!.label, '站点当地时间'],
      ['设备', '类别', '计量', '用电 kWh', '占比 %', `${previousLabel} kWh`],
      ...breakdown.items.map((item) => [item.name, CATEGORY_LABEL[item.category], '电表实测', item.energyKWh.toFixed(1), (item.share * 100).toFixed(1), before.get(item.deviceId)?.toFixed(1) ?? '']),
      ...breakdown.unmetered.map((machine) => [machine.name, CATEGORY_LABEL[machine.category], '未计量', '', '', '']),
      ...(breakdown.unallocatedKWh === null ? [] : [[UNALLOCATED, '总电表减各设备电表', '差值', breakdown.unallocatedKWh.toFixed(1), '', previous.data?.unallocatedKWh?.toFixed(1) ?? '']]),
    ];
    const url = URL.createObjectURL(new Blob(['﻿' + lines.map((line) => line.join(',')).join('\n')], { type: 'text/csv;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = `${site.displayName}-分项能耗-${PERIODS.find((option) => option.value === period)!.label}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  };

  const whole = breakdown ? breakdown.totalKWh ?? breakdown.subMeteredKWh : 0;
  const categories = EQUIPMENT_ORDER.filter((category) => breakdown?.items.some((item) => item.category === category));

  return (
    <Main>
      <PageHeader
        title="分项能耗"
        description={site.displayName}
        actions={(
          <>
            <span className="text-xs text-muted-foreground">{range} · 对比{previousLabel}</span>
            <PeriodTabs aria-label="统计期间" options={[...PERIODS]} value={period} onValueChange={(value) => void navigate({ search: (prior) => ({ ...prior, period: value as BreakdownPeriod }), replace: true })} />
            <Button variant="outline" size="icon" aria-label="刷新分项能耗" onClick={() => { void query.refetch(); void previous.refetch(); }}>
              <RefreshCw />
            </Button>
            <Button variant="outline" disabled={!breakdown} onClick={exportCsv}>
              <Download data-icon="inline-start" aria-hidden="true" />
              导出
            </Button>
          </>
        )}
      />

      {registry.isPending || (machines !== null && machines.length > 0 && query.isPending) ? (
        <Skeleton className="h-[560px] rounded-xl" />
      ) : machines !== null && machines.length === 0 ? (
        <Panel aria-label="用电去向">
          <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
            <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><Workflow className="size-[18px]" aria-hidden="true" /></span>
            <p className="text-sm font-semibold">冷站没有登记用电设备</p>
            <p className="text-xs text-muted-foreground">冷机、水泵与冷却塔登记并接入分表后，这里按设备拆分用电</p>
          </div>
        </Panel>
      ) : query.isError || !breakdown ? (
        <Panel aria-label="用电去向">
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">分项能耗暂时读不到</p>
            <Button variant="outline" size="sm" onClick={() => void query.refetch()}>重试</Button>
          </div>
        </Panel>
      ) : (
        <>
          <Panel aria-label="用电去向">
            <PanelHeader
              title={<span className="flex items-center gap-1.5">用电去向<InfoTip>空调总电表的用电流向各系统与设备；各设备电表没覆盖的部分记为未单独计量</InfoTip></span>}
              caption={breakdown.unallocatedKWh !== null && breakdown.unallocatedKWh < 0
                ? `各设备电表合计比总电表多 ${kWh(-breakdown.unallocatedKWh)}`
                : `空调总用电 ${breakdown.totalKWh === null ? '—' : kWh(breakdown.totalKWh)} · 设备电表合计 ${kWh(breakdown.subMeteredKWh)}`}
            />
            <div className="px-5 pt-3" role="region" aria-label="能流">
              <EnergyFlow breakdown={breakdown} />
            </div>
            <Table aria-label="设备用电" className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="pl-5">设备</TableHead>
                  <TableHead>计量</TableHead>
                  <TableHead className="text-right">用电（kWh）</TableHead>
                  <TableHead>占比</TableHead>
                  <TableHead className="text-right">{previousLabel}</TableHead>
                  <TableHead className="pr-5 text-right">变化</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {breakdown.items.map((item) => {
                  const prior = before.get(item.deviceId) ?? null;
                  const change = relativeChange(item.energyKWh, prior);
                  return (
                    <TableRow key={item.deviceId}>
                      <TableCell className="pl-5">
                        <span className="flex items-center gap-2.5">
                          <span className="size-2 shrink-0 rounded-[2px]" style={{ background: CATEGORY_COLOR[item.category] }} aria-hidden="true" />
                          <span className="font-medium">{item.name}</span>
                          <span className="text-xs text-muted-foreground">{CATEGORY_LABEL[item.category]}</span>
                        </span>
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">电表实测</TableCell>
                      <TableCell className="text-right font-medium tabular-nums">{formatDecimal(item.energyKWh, 0)}</TableCell>
                      <TableCell>
                        <span className="flex items-center gap-2">
                          <span className="h-1.5 w-24 overflow-hidden rounded-full bg-muted" aria-hidden="true">
                            <span className="block h-full rounded-full" style={{ width: `${Math.min(100, item.share * 100)}%`, background: CATEGORY_COLOR[item.category] }} />
                          </span>
                          <span className="text-xs tabular-nums">{percent(item.share)}</span>
                        </span>
                      </TableCell>
                      <TableCell className="text-right text-muted-foreground tabular-nums">{prior === null ? '—' : formatDecimal(prior, 0)}</TableCell>
                      <TableCell className="pr-5 text-right">{change === null ? dash : <DeltaBadge delta={change} invert variant="text" showIcon={false} />}</TableCell>
                    </TableRow>
                  );
                })}
                {breakdown.unmetered.map((machine) => (
                  <TableRow key={machine.deviceId}>
                    <TableCell className="pl-5">
                      <span className="flex items-center gap-2.5">
                        <span className="size-2 shrink-0 rounded-[2px] bg-muted" aria-hidden="true" />
                        <span className="font-medium">{machine.name}</span>
                        <span className="text-xs text-muted-foreground">{CATEGORY_LABEL[machine.category]}</span>
                      </span>
                    </TableCell>
                    <TableCell className="text-xs text-warning-text">未计量 · 用电计入未单独计量</TableCell>
                    <TableCell className="text-right">{dash}</TableCell>
                    <TableCell>{dash}</TableCell>
                    <TableCell className="text-right">{dash}</TableCell>
                    <TableCell className="pr-5 text-right">{dash}</TableCell>
                  </TableRow>
                ))}
                {breakdown.unallocatedKWh !== null ? (
                  <TableRow>
                    <TableCell className="pl-5">
                      <span className="flex items-center gap-2.5">
                        <span className="size-2 shrink-0 rounded-[2px]" style={{ background: CATEGORY_COLOR.UNALLOCATED }} aria-hidden="true" />
                        <span className="font-medium">{UNALLOCATED}</span>
                        <span className="text-xs text-muted-foreground">总电表减各设备电表</span>
                      </span>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">差值</TableCell>
                    <TableCell className="text-right font-medium tabular-nums">{wholeKWh(breakdown.unallocatedKWh)}</TableCell>
                    <TableCell>
                      {whole > 0 && breakdown.unallocatedKWh >= 0 ? (
                        <span className="flex items-center gap-2">
                          <span className="h-1.5 w-24 overflow-hidden rounded-full bg-muted" aria-hidden="true">
                            <span className="block h-full rounded-full" style={{ width: `${(breakdown.unallocatedKWh / whole) * 100}%`, background: CATEGORY_COLOR.UNALLOCATED }} />
                          </span>
                          <span className="text-xs tabular-nums">{percent(breakdown.unallocatedKWh / whole)}</span>
                        </span>
                      ) : dash}
                    </TableCell>
                    <TableCell className="text-right text-muted-foreground tabular-nums">{previous.data?.unallocatedKWh == null ? '—' : wholeKWh(previous.data.unallocatedKWh)}</TableCell>
                    <TableCell className="pr-5 text-right">{dash}</TableCell>
                  </TableRow>
                ) : null}
              </TableBody>
            </Table>
          </Panel>

          <Panel aria-label="分项用电趋势">
            <PanelHeader
              title={granularity === 'hour' ? '逐时分项用电' : '每日分项用电'}
              caption="各分项按自身刻度 · 浅色为进行中"
            />
            {breakdown.rows.length === 0 ? (
              <p className="px-5 py-8 text-center text-sm text-muted-foreground">本期间还没有分表读数</p>
            ) : (
              <div className="grid grid-cols-4 gap-3 p-4">
                {categories.map((category) => (
                  <CategoryBars key={category} category={category} rows={breakdown.rows} labelOf={labelOf} noun={granularity === 'hour' ? '时' : '日'} />
                ))}
              </div>
            )}
          </Panel>
        </>
      )}
    </Main>
  );
}
