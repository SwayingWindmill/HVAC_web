import { getRouteApi, useNavigate, useSearch } from '@tanstack/react-router';
import { RefreshCw, Workflow } from 'lucide-react';
import { useMemo } from 'react';
import { Bar, BarChart, CartesianGrid, Layer, Rectangle, Sankey, XAxis, YAxis, type SankeyNodeProps } from 'recharts';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { PeriodTabs } from '@/components/ui/period-tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { bucketLabel } from '@/features/energy-analysis/consumption/model';
import { formatDecimal } from '@/lib/operator-format';
import { type Breakdown, type EquipmentCategory } from '../breakdown-model';
import { useEnergyBreakdown } from '../query';

const siteRoute = getRouteApi('/_app/_site');

type BreakdownPeriod = 'today' | '7d' | 'month';
const PERIODS: readonly { value: BreakdownPeriod; label: string }[] = [
  { value: 'today', label: '今日' },
  { value: '7d', label: '近 7 天' },
  { value: 'month', label: '本月' },
];

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

const chartConfig = Object.fromEntries(
  (Object.keys(CATEGORY_LABEL) as EquipmentCategory[]).map((category) => [category, { label: CATEGORY_LABEL[category], color: CATEGORY_COLOR[category] }]),
) satisfies ChartConfig;

const kWh = (value: number) => (value >= 10_000 ? `${formatDecimal(value / 1000, 2)} MWh` : `${formatDecimal(value, 0)} kWh`);
const percent = (share: number) => `${formatDecimal(share * 100, 1)}%`;

interface FlowNode {
  readonly name: string;
  readonly color: string;
  readonly energyKWh: number;
}

/** Total → subsystem → machine, with what the machines leave unexplained as its own branch. */
function flowData(breakdown: Breakdown) {
  const nodes: FlowNode[] = [];
  const links: { source: number; target: number; value: number }[] = [];
  const add = (node: FlowNode) => nodes.push(node) - 1;
  const allocated = breakdown.items.reduce((sum, item) => sum + item.energyKWh, 0);
  const total = add({ name: '空调总用电', color: 'var(--foreground)', energyKWh: Math.max(breakdown.meteredKWh ?? 0, allocated) });
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
  if (breakdown.unallocatedKWh && breakdown.unallocatedKWh > 0.5) {
    links.push({ source: total, target: add({ name: '未分项', color: CATEGORY_COLOR.UNALLOCATED, energyKWh: breakdown.unallocatedKWh }), value: breakdown.unallocatedKWh });
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
    <ChartContainer config={chartConfig} className="aspect-auto h-[340px] w-full">
      <Sankey
        data={data}
        nodeWidth={10}
        nodePadding={28}
        linkCurvature={0.55}
        iterations={0}
        margin={{ top: 12, right: 120, bottom: 12, left: 8 }}
        node={(props: SankeyNodeProps) => <FlowNodeShape {...props} />}
        link={{ stroke: 'var(--chart-1)', strokeOpacity: 0.16 }}
      />
    </ChartContainer>
  );
}

export function BreakdownWorkspace() {
  const { site } = siteRoute.useRouteContext();
  const search = useSearch({ from: '/_app/_site/energy-analysis/breakdown' });
  const navigate = useNavigate({ from: '/energy-analysis/breakdown' });
  const period: BreakdownPeriod = search.period ?? 'today';
  const { machines, registry, query } = useEnergyBreakdown(period);
  const breakdown = query.data;
  const granularity = period === 'today' ? 'hour' : 'day';
  const rows = useMemo(
    () => (breakdown?.rows ?? []).map((row) => ({
      ...Object.fromEntries((Object.keys(CATEGORY_LABEL) as EquipmentCategory[]).map((key) => [key, Math.round(row[key] * 10) / 10])),
      label: granularity === 'hour' ? bucketLabel(row.key, 'hour', site.timezone) : `${Number(row.key.slice(5, 7))}/${Number(row.key.slice(8, 10))}`,
    })),
    [breakdown, granularity, site.timezone],
  );
  const allocated = (breakdown?.items ?? []).reduce((sum, item) => sum + item.energyKWh, 0);
  const category = (key: EquipmentCategory | 'PUMPS') => {
    const categories = key === 'PUMPS' ? ['CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP'] : [key];
    const items = (breakdown?.items ?? []).filter((item) => categories.includes(item.category));
    return {
      energy: items.reduce((sum, item) => sum + item.energyKWh, 0),
      share: items.reduce((sum, item) => sum + item.share, 0),
      averageKW: items.reduce((sum, item) => sum + item.averageKW, 0),
      peakKW: items.reduce((peak, item) => Math.max(peak, item.peakKW), 0),
      count: items.length,
    };
  };

  return (
    <Main>
      <PageHeader
        title="分项能耗"
        description={`${site.displayName} · 冷站各设备的用电与占比，按设备功率逐时累计`}
        meta={<PageMeta icon={<Workflow />}>按设备功率估算</PageMeta>}
        actions={(
          <>
            <PeriodTabs aria-label="统计期间" options={[...PERIODS]} value={period} onValueChange={(value) => void navigate({ search: (previous) => ({ ...previous, period: value as BreakdownPeriod }), replace: true })} />
            <Button variant="outline" size="icon-sm" aria-label="刷新分项能耗" onClick={() => void query.refetch()}>
              <RefreshCw />
            </Button>
          </>
        )}
      />

      {registry.isPending || (machines !== null && machines.length > 0 && query.isPending) ? (
        <Skeleton className="h-[560px] rounded-xl" />
      ) : machines !== null && machines.length === 0 ? (
        <Card>
          <Empty className="py-16">
            <EmptyHeader>
              <EmptyMedia variant="icon"><Workflow /></EmptyMedia>
              <EmptyTitle>没有上报功率的冷站设备</EmptyTitle>
              <EmptyDescription>冷机、水泵与冷却塔接入功率读数后，这里按设备拆分用电。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        </Card>
      ) : query.isError || !breakdown ? (
        <Card><CardContent className="py-16 text-center text-sm text-muted-foreground">分项能耗暂不可用</CardContent></Card>
      ) : (
        <>
          <MetricGrid ariaLabel="分项概况">
            <MetricCard
              label="空调总用电"
              value={<MetricValue value={breakdown.meteredKWh === null ? null : formatDecimal(breakdown.meteredKWh, 0)} unit="kWh" />}
              lead={`设备合计 ${kWh(allocated)}`}
              detail={breakdown.meteredKWh === null ? '电表本期没有读数'
                : allocated > breakdown.meteredKWh ? `设备估算比电表多 ${kWh(allocated - breakdown.meteredKWh)}，电表有缺数时段`
                : breakdown.unallocatedKWh ? `未分项 ${kWh(breakdown.unallocatedKWh)}` : '设备覆盖全部用电'}
            />
            {([['CHILLER', '冷水机组'], ['PUMPS', '水泵'], ['COOLING_TOWER', '冷却塔']] as const).map(([key, label]) => {
              const facts = category(key);
              return (
                <MetricCard
                  key={key}
                  label={label}
                  value={<MetricValue value={facts.count ? formatDecimal(facts.energy, 0) : null} unit="kWh" />}
                  badge={facts.count ? <Badge variant="secondary" className="tabular-nums">{percent(facts.share)}</Badge> : undefined}
                  lead={facts.count ? `平均 ${formatDecimal(facts.averageKW, 1)} kW` : '没有上报功率的设备'}
                  detail={facts.count ? `最高 ${formatDecimal(facts.peakKW, 1)} kW` : '—'}
                />
              );
            })}
          </MetricGrid>

          <div className="grid gap-6 @5xl/main:grid-cols-[minmax(0,7fr)_minmax(0,5fr)]">
            <Card role="region" aria-label="能流">
              <CardHeader>
                <CardTitle>能流</CardTitle>
                <CardDescription>空调总用电流向各系统与设备；设备功率未覆盖的部分记为未分项</CardDescription>
              </CardHeader>
              <CardContent>
                <EnergyFlow breakdown={breakdown} />
              </CardContent>
            </Card>

            <Card role="region" aria-label="设备用电排行">
              <CardHeader>
                <CardTitle>设备用电排行</CardTitle>
                <CardDescription>本期用电与占比，以及平均、最高功率</CardDescription>
              </CardHeader>
              <CardContent>
                <ul className="flex flex-col gap-4">
                  {breakdown.items.map((item) => (
                    <li key={item.deviceId} className="flex flex-col gap-1.5">
                      <div className="flex items-baseline justify-between gap-4">
                        <span className="flex min-w-0 items-center gap-2 text-sm">
                          <span className="size-2 shrink-0 rounded-[2px]" style={{ background: CATEGORY_COLOR[item.category] }} aria-hidden="true" />
                          <span className="truncate font-medium">{item.name}</span>
                          <span className="text-xs text-muted-foreground">{CATEGORY_LABEL[item.category]}</span>
                        </span>
                        <span className="shrink-0 text-sm tabular-nums">
                          {kWh(item.energyKWh)}
                          <span className="ml-2 inline-block w-12 text-right text-xs text-muted-foreground">{percent(item.share)}</span>
                        </span>
                      </div>
                      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                        <div className="h-full rounded-full" style={{ width: `${Math.min(100, item.share * 100)}%`, background: CATEGORY_COLOR[item.category] }} />
                      </div>
                      <p className="text-xs text-muted-foreground tabular-nums">平均 {formatDecimal(item.averageKW, 1)} kW · 最高 {formatDecimal(item.peakKW, 1)} kW</p>
                    </li>
                  ))}
                </ul>
              </CardContent>
            </Card>
          </div>

          <Card role="region" aria-label="分项用电趋势">
            <CardHeader>
              <CardTitle>{granularity === 'hour' ? '逐时分项用电' : '逐日分项用电'}</CardTitle>
              <CardDescription>{granularity === 'hour' ? '今日每小时' : '每天'}各类设备的用电（kWh），进行中的时段只计已运行的时间</CardDescription>
            </CardHeader>
            <CardContent>
              <ChartContainer config={chartConfig} className="aspect-auto h-[300px] w-full">
                <BarChart data={rows} margin={{ left: 4, right: 8, top: 8 }}>
                  <CartesianGrid vertical={false} />
                  <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={16} />
                  <YAxis tickLine={false} axisLine={false} width={48} />
                  <ChartTooltip cursor={{ fill: 'var(--muted)', opacity: 0.6 }} content={<ChartTooltipContent indicator="dot" />} />
                  {(Object.keys(CATEGORY_LABEL) as EquipmentCategory[]).map((key, index, keys) => (
                    <Bar key={key} dataKey={key} stackId="energy" fill={`var(--color-${key})`} radius={index === keys.length - 1 ? [3, 3, 0, 0] : 0} maxBarSize={36} isAnimationActive={false} />
                  ))}
                  <ChartLegend content={<ChartLegendContent />} />
                </BarChart>
              </ChartContainer>
            </CardContent>
          </Card>
        </>
      )}
    </Main>
  );
}
