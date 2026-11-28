import { useId, useMemo, useState } from 'react';
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { Panel, PanelHeader } from '@/blocks/panel';
import {
  ChartContainer,
  ChartEmptySlotTooltip,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { describeIntegrationsError } from '../governance-shared-model';
import type {
  HourlyReconciliationBucket,
  MeterReconciliationSummary,
  ReconciliationHourStatus,
} from '../meter-reconciliation-model';

const RECONCILIATION_CHART_CONFIG = {
  validKwh: {
    label: '有效电量',
    color: 'var(--chart-1)',
  },
  suspectKwh: {
    label: '可疑差值电量',
    color: 'var(--warning)',
  },
} satisfies ChartConfig;

const HOUR_STATUS_LABEL: Readonly<Record<ReconciliationHourStatus, string>> = {
  VALID: '有效',
  SUSPECT: '含可疑样本',
  MISSING: '缺数据',
  IN_PROGRESS: '当前小时统计中',
};

const STRIP_CELL_CLASSES: Readonly<Record<ReconciliationHourStatus, string>> = {
  VALID: 'bg-success/80',
  SUSPECT: 'bg-warning',
  MISSING: 'bg-destructive/60',
  IN_PROGRESS: 'border border-dashed border-muted-foreground/60 bg-transparent',
};

export function HourlyQualityStrip({
  buckets,
  missingCompletedHours,
}: {
  readonly buckets: readonly HourlyReconciliationBucket[];
  readonly missingCompletedHours: number;
}) {
  const validCount = buckets.filter((b) => b.status === 'VALID').length;
  const suspectCount = buckets.filter((b) => b.status === 'SUSPECT').length;

  return (
    <div className="flex flex-col gap-1">
      <div
        className="flex items-center gap-0.5"
        role="img"
        aria-label={`近 24 小时质量：有效 ${validCount} 小时，可疑 ${suspectCount} 小时，缺数据 ${missingCompletedHours} 小时`}
      >
        {buckets.map((bucket) => (
          <span
            key={bucket.index}
            title={`${bucket.hourLabel} · ${HOUR_STATUS_LABEL[bucket.status]}${
              bucket.validKwh !== null
                ? ` · 有效 ${formatDecimal(bucket.validKwh, 2)} kWh`
                : ''
            }${
              bucket.suspectKwh > 0
                ? ` · 可疑 +${formatDecimal(bucket.suspectKwh, 2)} kWh`
                : ''
            }`}
            className={cn(
              'h-3.5 w-1.5 rounded-[2px] transition-opacity hover:opacity-80',
              STRIP_CELL_CLASSES[bucket.status],
            )}
          />
        ))}
      </div>
      <div className="text-xs text-muted-foreground tabular-nums">
        <span>有效 {validCount}h</span>
        {suspectCount > 0 ? (
          <span className="text-warning-text"> · ▲ 可疑 {suspectCount}h</span>
        ) : null}
        {missingCompletedHours > 0 ? (
          <span className="text-destructive-text"> · ◆ 缺 {missingCompletedHours}h</span>
        ) : null}
      </div>
    </div>
  );
}

export function MeterHourlyReconciliationPanel({
  summary,
  hourlyBuckets,
  isEnergyLoading,
  isEnergyError,
  energyError,
}: {
  readonly summary: MeterReconciliationSummary | undefined;
  readonly hourlyBuckets: readonly HourlyReconciliationBucket[];
  readonly isEnergyLoading: boolean;
  readonly isEnergyError: boolean;
  readonly energyError: unknown;
}) {
  const [chartViewMode, setChartViewMode] = useState<'chart' | 'table'>('chart');
  const hatchPatternId = useId().replace(/:/g, '');

  const hasChartData = hourlyBuckets.some(
    (b) => b.validKwh !== null || b.suspectKwh > 0,
  );
  const chartData = useMemo(() => {
    const maxTotal = hourlyBuckets.reduce(
      (acc, b) => Math.max(acc, (b.validKwh ?? 0) + b.suspectKwh),
      0,
    );
    const placeholderHeight = maxTotal > 0 ? Number((maxTotal * 0.28).toFixed(2)) : 10;
    return hourlyBuckets.map((b) => {
      const isHatched = b.status === 'MISSING' || (b.status === 'IN_PROGRESS' && b.validKwh === null);
      return {
        ...b,
        chartValidKwh: isHatched ? null : (b.validKwh ?? 0),
        chartSuspectKwh: isHatched ? null : b.suspectKwh,
        placeholderKwh: isHatched ? placeholderHeight : null,
      };
    });
  }, [hourlyBuckets]);

  return (
    <Panel aria-label="近 24 小时逐时电量核对">
      <PanelHeader
        title="近 24 小时逐时电量核对"
        caption={
          summary?.totalValidElectricityKwh !== null && summary?.totalValidElectricityKwh !== undefined
            ? `有效合计 ${formatDecimal(summary.totalValidElectricityKwh, 2)} kWh${
                summary.totalSuspectElectricityKwh > 0
                  ? ` · ▲ 可疑差值 +${formatDecimal(summary.totalSuspectElectricityKwh, 2)} kWh`
                  : ' · 无可疑差值'
              }`
            : '对比冷站有效电量与含可疑样本电量，识别回退、跳变与缺段'
        }
        action={
          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            value={chartViewMode}
            onValueChange={(next) => {
              if (next === 'chart' || next === 'table') setChartViewMode(next);
            }}
            aria-label="逐时电量核对视图切换"
          >
            <ToggleGroupItem value="chart" className="text-xs">
              图表
            </ToggleGroupItem>
            <ToggleGroupItem value="table" className="text-xs">
              表格
            </ToggleGroupItem>
          </ToggleGroup>
        }
      />

      <div className="px-6 py-4">
        {isEnergyLoading ? (
          <Skeleton className="h-52 w-full rounded-lg" />
        ) : isEnergyError ? (
          <p className="rounded-lg border border-divider bg-muted/40 px-4 py-5 text-sm text-muted-foreground">
            {describeIntegrationsError(
              energyError,
              '逐时电量统计暂时无法读取，下方仍可核查各表计实时读数。',
            )}
          </p>
        ) : !hasChartData ? (
          <p className="rounded-lg border border-divider bg-muted/40 px-4 py-5 text-sm text-muted-foreground">
            近 24 小时暂无冷站电表逐时电量统计（—）。
          </p>
        ) : chartViewMode === 'chart' ? (
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
              <span className="inline-flex items-center gap-1.5">
                <span className="size-2.5 rounded-[2px] bg-[var(--chart-1)]" aria-hidden="true" />
                <span>○ 有效电量 (kWh)</span>
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="size-2.5 rounded-[2px] bg-warning" aria-hidden="true" />
                <span>▲ 可疑差值电量 (kWh)</span>
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span
                  className="size-2.5 rounded-[2px] border border-dashed border-muted-foreground/70 bg-muted"
                  aria-hidden="true"
                />
                <span>◆/■ 缺数据或当前小时统计中</span>
              </span>
            </div>
            <ChartContainer config={RECONCILIATION_CHART_CONFIG} className="h-52 w-full">
              <BarChart
                data={chartData}
                margin={{ top: 6, right: 8, left: -8, bottom: 0 }}
              >
                <defs>
                  <pattern
                    id={`reconcile-hatch-${hatchPatternId}`}
                    patternUnits="userSpaceOnUse"
                    width={6}
                    height={6}
                    patternTransform="rotate(45)"
                  >
                    <rect width={6} height={6} fill="var(--muted-foreground)" fillOpacity={0.08} />
                    <rect width={1.5} height={6} fill="var(--muted-foreground)" fillOpacity={0.35} />
                  </pattern>
                </defs>
                <CartesianGrid vertical={false} strokeDasharray="3 3" />
                <XAxis
                  dataKey="hourLabel"
                  tickLine={false}
                  axisLine={false}
                  interval={2}
                  className="font-mono text-xs"
                />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={48}
                  className="font-mono text-xs"
                />
                <ChartTooltip
                  content={(tooltipProps) => {
                    const bucket = chartData.find((item) =>
                      Object.is(item, tooltipProps.payload?.[0]?.payload),
                    );
                    if (tooltipProps.active && bucket) {
                      if (bucket.status === 'MISSING') {
                        return <ChartEmptySlotTooltip label={`${bucket.hourLabel} · ◆ 缺数据`} />;
                      }
                      if (bucket.status === 'IN_PROGRESS' && bucket.validKwh === null) {
                        return (
                          <div className="grid min-w-36 gap-1 rounded-lg border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md">
                            <div className="font-medium tabular-nums">{bucket.hourLabel}</div>
                            <div className="text-muted-foreground">■ 当前小时统计中</div>
                          </div>
                        );
                      }
                    }
                    return (
                      <ChartTooltipContent
                        active={tooltipProps.active}
                        payload={tooltipProps.payload?.filter(
                          (entry) =>
                            entry.dataKey === 'chartValidKwh' ||
                            entry.dataKey === 'chartSuspectKwh',
                        )}
                        label={
                          bucket
                            ? `${bucket.hourLabel} · ${HOUR_STATUS_LABEL[bucket.status]}`
                            : tooltipProps.label
                        }
                        indicator="dot"
                        formatter={(value, name) => {
                          const isValidSeries = name === 'chartValidKwh';
                          return (
                            <div className="flex w-full items-center gap-2">
                              <span
                                className="size-2 shrink-0 rounded-[2px]"
                                style={{
                                  backgroundColor: isValidSeries
                                    ? 'var(--color-validKwh)'
                                    : 'var(--color-suspectKwh)',
                                }}
                              />
                              <span className="flex-1 text-muted-foreground">
                                {isValidSeries ? '有效电量' : '可疑差值电量'}
                              </span>
                              <span className="font-mono font-medium tabular-nums">
                                {isValidSeries
                                  ? `${formatDecimal(Number(value), 2)} kWh`
                                  : `+${formatDecimal(Number(value), 2)} kWh`}
                              </span>
                            </div>
                          );
                        }}
                      />
                    );
                  }}
                />
                <Bar
                  dataKey="chartValidKwh"
                  stackId="reconcile"
                  fill="var(--color-validKwh)"
                  radius={[0, 0, 0, 0]}
                  isAnimationActive={false}
                />
                <Bar
                  dataKey="chartSuspectKwh"
                  stackId="reconcile"
                  fill="var(--color-suspectKwh)"
                  radius={[3, 3, 0, 0]}
                  isAnimationActive={false}
                />
                <Bar
                  dataKey="placeholderKwh"
                  stackId="reconcile"
                  fill={`url(#reconcile-hatch-${hatchPatternId})`}
                  stroke="var(--border)"
                  strokeWidth={1}
                  radius={[3, 3, 0, 0]}
                  isAnimationActive={false}
                />
              </BarChart>
            </ChartContainer>
          </div>
        ) : (
          <div className="max-h-64 overflow-y-auto rounded-lg border border-divider">
            <Table aria-label="近 24 小时逐时电量核对表格">
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">时段</TableHead>
                  <TableHead className="text-right">有效电量 (kWh)</TableHead>
                  <TableHead className="text-right">可疑差值电量 (kWh)</TableHead>
                  <TableHead className="pr-4">核对状态</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {hourlyBuckets.map((bucket) => (
                  <TableRow key={bucket.index}>
                    <TableCell className="pl-4 font-mono text-xs">{bucket.hourLabel}</TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums">
                      {bucket.validKwh !== null ? formatDecimal(bucket.validKwh, 2) : '—'}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums">
                      {bucket.status === 'MISSING' ||
                      (bucket.status === 'IN_PROGRESS' && bucket.validKwh === null)
                        ? '—'
                        : bucket.suspectKwh > 0
                          ? `+${formatDecimal(bucket.suspectKwh, 2)}`
                          : '0.00'}
                    </TableCell>
                    <TableCell className="pr-4 text-xs">
                      {bucket.status === 'SUSPECT' ? (
                        <span className="text-warning-text">▲ 含可疑样本</span>
                      ) : bucket.status === 'MISSING' ? (
                        <span className="text-destructive-text">◆ 缺数据</span>
                      ) : bucket.status === 'IN_PROGRESS' ? (
                        <span className="text-muted-foreground">■ 当前小时统计中</span>
                      ) : (
                        <span className="text-success-text">○ 有效</span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </Panel>
  );
}
