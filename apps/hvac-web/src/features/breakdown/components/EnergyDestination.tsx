import { Workflow } from 'lucide-react';
import { useMemo } from 'react';
import { Layer, Rectangle, Sankey, type SankeyNodeProps } from 'recharts';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Button } from '@/components/ui/button';
import { ChartContainer, type ChartConfig } from '@/components/ui/chart';
import { DeltaBadge } from '@/components/ui/delta-badge';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { bucketLabel, relativeChange, type Granularity } from '@/features/energy-analysis/consumption/model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { EQUIPMENT_ORDER, type Breakdown, type EquipmentCategory } from '../breakdown-model';
import type { useEnergyBreakdown } from '../query';

// Where the period's electricity went, for the energy page: the total meter flowing to each
// subsystem and machine, each machine's share against the period before, and each category's
// energy bucket by bucket.

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

type BreakdownResult = ReturnType<typeof useEnergyBreakdown>;

const NOUN: Record<Granularity, string> = { hour: '时', day: '日', month: '月' };

function bucketName(key: string, granularity: Granularity, timeZone: string): string {
  if (granularity === 'hour') return bucketLabel(key, 'hour', timeZone);
  return granularity === 'day' ? `${key.slice(5, 7)}/${key.slice(8, 10)}` : `${Number(key.slice(5, 7))}月`;
}

function MachineCell({ name, note, color }: { readonly name: string; readonly note: string; readonly color: string }) {
  return (
    <span className="flex items-center gap-2.5">
      <span className="size-2 shrink-0 rounded-[2px]" style={{ background: color }} aria-hidden="true" />
      <span className="font-medium">{name}</span>
      <span className="text-xs text-muted-foreground">{note}</span>
    </span>
  );
}

function ShareBar({ share, color }: { readonly share: number; readonly color: string }) {
  return (
    <span className="flex items-center gap-2">
      <span className="h-1.5 w-20 overflow-hidden rounded-full bg-muted" aria-hidden="true">
        <span className="block h-full rounded-full" style={{ width: `${Math.min(100, share * 100)}%`, background: color }} />
      </span>
      <span className="text-xs tabular-nums">{percent(share)}</span>
    </span>
  );
}

/** The 用电去向 panel: the flow from the total meter and each machine's energy beside the period before. */
export function EnergyDestination({ result, previousLabel }: { readonly result: BreakdownResult; readonly previousLabel: string }) {
  const { machines, registry, query, previous } = result;
  const breakdown = query.data;
  if (registry.isPending || (machines !== null && machines.length > 0 && query.isPending)) return <Skeleton className="h-[400px] rounded-xl" />;
  if (machines !== null && machines.length === 0) {
    return (
      <Panel aria-label="用电去向">
        <PanelHeader title="用电去向" />
        <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
          <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><Workflow className="size-[18px]" aria-hidden="true" /></span>
          <p className="text-sm font-semibold">冷站没有登记用电设备</p>
          <p className="text-xs text-muted-foreground">冷机、水泵与冷却塔登记并接入分表后，这里按设备拆分用电</p>
        </div>
      </Panel>
    );
  }
  if (query.isError || !breakdown) {
    return (
      <Panel aria-label="用电去向">
        <PanelHeader title="用电去向" />
        <div className="flex flex-col items-start gap-2 px-5 py-8">
          <p className="text-sm font-semibold">各设备用电暂时读不到</p>
          <Button variant="outline" size="sm" onClick={() => void query.refetch()}>重试</Button>
        </div>
      </Panel>
    );
  }
  const before = new Map((previous.data?.items ?? []).map((item) => [item.deviceId, item.energyKWh]));
  const whole = breakdown.totalKWh ?? breakdown.subMeteredKWh;
  return (
    <Panel aria-label="用电去向">
      <PanelHeader
        title={<span className="flex items-center gap-1.5">用电去向<InfoTip>空调总电表的用电流向各系统与设备；各设备电表没覆盖的部分记为未单独计量</InfoTip></span>}
        caption={breakdown.unallocatedKWh !== null && breakdown.unallocatedKWh < 0
          ? `各设备电表合计比总电表多 ${kWh(-breakdown.unallocatedKWh)}`
          : `空调总用电 ${breakdown.totalKWh === null ? '—' : kWh(breakdown.totalKWh)} · 设备电表合计 ${kWh(breakdown.subMeteredKWh)}`}
      />
      <div className="grid grid-cols-[minmax(0,5fr)_minmax(0,6fr)] divide-x divide-divider">
        <div className="px-4 pt-3" role="region" aria-label="能流">
          <EnergyFlow breakdown={breakdown} />
        </div>
        <Table aria-label="设备用电" className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="pl-5">设备</TableHead>
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
                  <TableCell className="pl-5"><MachineCell name={item.name} note={CATEGORY_LABEL[item.category]} color={CATEGORY_COLOR[item.category]} /></TableCell>
                  <TableCell className="text-right font-medium tabular-nums">{formatDecimal(item.energyKWh, 0)}</TableCell>
                  <TableCell><ShareBar share={item.share} color={CATEGORY_COLOR[item.category]} /></TableCell>
                  <TableCell className="text-right text-muted-foreground tabular-nums">{prior === null ? '—' : formatDecimal(prior, 0)}</TableCell>
                  <TableCell className="pr-5 text-right">{change === null ? dash : <DeltaBadge delta={change} invert variant="text" showIcon={false} />}</TableCell>
                </TableRow>
              );
            })}
            {breakdown.unmetered.map((machine) => (
              <TableRow key={machine.deviceId}>
                <TableCell className="pl-5"><MachineCell name={machine.name} note={`${CATEGORY_LABEL[machine.category]} · 没有电表，用电计入未单独计量`} color="var(--muted)" /></TableCell>
                <TableCell className="text-right">{dash}</TableCell>
                <TableCell>{dash}</TableCell>
                <TableCell className="text-right">{dash}</TableCell>
                <TableCell className="pr-5 text-right">{dash}</TableCell>
              </TableRow>
            ))}
            {breakdown.unallocatedKWh !== null ? (
              <TableRow>
                <TableCell className="pl-5"><MachineCell name={UNALLOCATED} note="总电表减各设备电表" color={CATEGORY_COLOR.UNALLOCATED} /></TableCell>
                <TableCell className="text-right font-medium tabular-nums">{wholeKWh(breakdown.unallocatedKWh)}</TableCell>
                <TableCell>{whole > 0 && breakdown.unallocatedKWh >= 0 ? <ShareBar share={breakdown.unallocatedKWh / whole} color={CATEGORY_COLOR.UNALLOCATED} /> : dash}</TableCell>
                <TableCell className="text-right text-muted-foreground tabular-nums">{previous.data?.unallocatedKWh == null ? '—' : wholeKWh(previous.data.unallocatedKWh)}</TableCell>
                <TableCell className="pr-5 text-right">{dash}</TableCell>
              </TableRow>
            ) : null}
          </TableBody>
        </Table>
      </div>
    </Panel>
  );
}

/** Each category's energy bucket by bucket, on its own scale. */
export function CategoryTrend({ result, granularity, timeZone }: { readonly result: BreakdownResult; readonly granularity: Granularity; readonly timeZone: string }) {
  const breakdown = result.query.data;
  if (!breakdown || breakdown.rows.length === 0) return <p className="px-5 py-10 text-center text-sm text-muted-foreground">本期间还没有分表读数</p>;
  const categories = EQUIPMENT_ORDER.filter((category) => breakdown.items.some((item) => item.category === category));
  return (
    <div role="region" aria-label="分项趋势" className="grid grid-cols-4 gap-3 p-4">
      {categories.map((category) => (
        <CategoryBars key={category} category={category} rows={breakdown.rows} labelOf={(key) => bucketName(key, granularity, timeZone)} noun={NOUN[granularity]} />
      ))}
    </div>
  );
}

/** The machines' rows of the energy export, with the period before alongside. */
export function destinationCsv(result: BreakdownResult, previousLabel: string): (string | number)[][] {
  const breakdown = result.query.data;
  if (!breakdown) return [];
  const before = new Map((result.previous.data?.items ?? []).map((item) => [item.deviceId, item.energyKWh]));
  return [
    [],
    ['设备', '类别', '计量', '用电 kWh', '占比 %', `${previousLabel} kWh`],
    ...breakdown.items.map((item) => [item.name, CATEGORY_LABEL[item.category], '电表实测', item.energyKWh.toFixed(1), (item.share * 100).toFixed(1), before.get(item.deviceId)?.toFixed(1) ?? '']),
    ...breakdown.unmetered.map((machine) => [machine.name, CATEGORY_LABEL[machine.category], '未计量', '', '', '']),
    ...(breakdown.unallocatedKWh === null ? [] : [[UNALLOCATED, '总电表减各设备电表', '差值', breakdown.unallocatedKWh.toFixed(1), '', result.previous.data?.unallocatedKWh?.toFixed(1) ?? '']]),
  ];
}
