import { useId, useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { DataTableBlock } from '@/blocks/data-table';
import { Panel, PanelHeader } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { InfoTip } from '@/components/info-tip';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
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
import { useDataTable } from '@/hooks/use-data-table';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  describeIntegrationsError,
  type HourlyReconciliationBucket,
  type MeterReconciliationModel,
  type MeterReconciliationRow,
  type ReconciliationHourStatus,
} from '../integrations-model';
import { GovernanceBadge } from './GovernanceShared';

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

function HourlyQualityStrip({
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

export interface MeterReconciliationTabProps {
  readonly model: MeterReconciliationModel | null;
  readonly isAssetLoading: boolean;
  readonly isAssetError: boolean;
  readonly assetError: unknown;
  readonly isEnergyLoading: boolean;
  readonly isEnergyError: boolean;
  readonly energyError: unknown;
  readonly canReadSnapshots: boolean;
  readonly isSnapshotsError: boolean;
  readonly onRetry: () => void;
  readonly onInspectMeter: (deviceCode: string) => void;
}

export function MeterReconciliationTab({
  model,
  isAssetLoading,
  isAssetError,
  assetError,
  isEnergyLoading,
  isEnergyError,
  energyError,
  canReadSnapshots,
  isSnapshotsError,
  onRetry,
  onInspectMeter,
}: MeterReconciliationTabProps) {
  const [chartViewMode, setChartViewMode] = useState<'chart' | 'table'>('chart');
  const hatchPatternId = useId().replace(/:/g, '');

  const rows = useMemo(() => [...(model?.rows ?? [])], [model?.rows]);

  const columns = useMemo<ColumnDef<DataTableFeatures, MeterReconciliationRow>[]>(
    () => [
      {
        id: 'meter',
        header: () => '表计与位置',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div>
              <button
                type="button"
                className="block text-left font-semibold text-foreground outline-none hover:underline focus-visible:underline"
                onClick={() => onInspectMeter(item.deviceCode)}
              >
                {item.displayName}
              </button>
              <div className="mt-0.5 text-xs text-muted-foreground">
                <span>{item.meterRoleLabel}</span>
                <span> · </span>
                <span className="font-mono">{item.deviceCode}</span>
                {item.spaceName ? <span> · {item.spaceName}</span> : null}
              </div>
            </div>
          );
        },
      },
      {
        id: 'hourlyStrip',
        header: () => '24 小时状态条',
        cell: ({ row }) => {
          const item = row.original;
          if (!item.supportsHourlyEnergy) {
            return <span className="text-xs text-muted-foreground">仅实时瞬时量监测</span>;
          }
          if (!item.hasHourlySeries || item.hourlyStrip.length === 0) {
            return (
              <span className="text-xs text-muted-foreground">
                {isEnergyError ? '逐时统计暂不可读' : '暂无逐时电量汇总（—）'}
              </span>
            );
          }
          return (
            <HourlyQualityStrip
              buckets={item.hourlyStrip}
              missingCompletedHours={item.missingCompletedHours}
            />
          );
        },
      },
      {
        id: 'energy24h',
        header: () => '近 24 小时计量',
        cell: ({ row }) => {
          const item = row.original;
          if (!item.hasHourlySeries || item.validEnergyKwh === null) {
            return <span className="text-muted-foreground">—</span>;
          }
          const unitLabel = item.meterRole === 'THERMAL_METER' ? 'kWh 冷量' : 'kWh';
          return (
            <div className="tabular-nums">
              <div className="font-mono text-foreground">
                {formatDecimal(item.validEnergyKwh, 2)} {unitLabel}
              </div>
              {item.suspectEnergyKwh > 0.001 ? (
                <div className="mt-0.5 font-mono text-xs text-warning-text">
                  ▲ 可疑 +{formatDecimal(item.suspectEnergyKwh, 2)} {unitLabel}
                </div>
              ) : (
                <div className="mt-0.5 text-xs text-muted-foreground">无跳变或可疑差值</div>
              )}
            </div>
          );
        },
      },
      {
        id: 'reading',
        header: () => '当前累计读数',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="tabular-nums">
              <div className="font-mono text-foreground">
                {item.primaryReadingValue}
                {item.primaryReadingValue !== '—' && item.primaryReadingUnit
                  ? ` ${item.primaryReadingUnit}`
                  : ''}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">{item.counterPolicyLabel}</div>
            </div>
          );
        },
      },
      {
        id: 'actions',
        header: () => '状态与操作',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="inline-flex items-center justify-end gap-2">
              <GovernanceBadge status={item.status} />
              <Button
                variant="outline"
                size="sm"
                onClick={() => onInspectMeter(item.deviceCode)}
              >
                核查详情
              </Button>
            </div>
          );
        },
      },
    ],
    [isEnergyError, onInspectMeter],
  );

  const table = useDataTable({
    key: 'integrations.meters',
    data: rows,
    columns,
    pageSize: 10,
    getRowId: (row) => row.deviceId,
  });

  if (isAssetError) {
    return (
      <StateCard
        tone="danger"
        title={<h2 className="text-base font-semibold">无法读取站点计量表计配置</h2>}
        description={describeIntegrationsError(assetError)}
      >
        <Button variant="outline" size="sm" onClick={onRetry}>
          重试
        </Button>
      </StateCard>
    );
  }

  const summary = model?.summary;
  const hourlyBuckets = model?.hourlyBuckets ?? [];
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
    <div className="flex flex-col gap-4">
      {!canReadSnapshots ? (
        <Alert>
          <AlertDescription>
            当前账号没有实时读数查看权限，当前累计读数暂不可见，以下展示表计登记与逐时电量核对。
          </AlertDescription>
        </Alert>
      ) : isSnapshotsError ? (
        <Alert>
          <AlertDescription>
            实时读数快照暂时无法读取，当前累计读数显示为 —，稍后可重试刷新。
          </AlertDescription>
        </Alert>
      ) : null}

      <Panel aria-label="电表核对概览">
        {isAssetLoading || !summary ? (
          <div className="grid grid-cols-1 gap-4 p-5 sm:grid-cols-3">
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
          </div>
        ) : (
          <div className="grid grid-cols-1 divide-y divide-divider sm:grid-cols-3 sm:divide-x sm:divide-y-0">
            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">已接入表计</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.totalMeters}
                </span>
                <span className="text-xs text-muted-foreground">台</span>
              </div>
              <span className="text-xs text-muted-foreground">
                含总电表、总冷量表、流量计与设备电表
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">有效计量时段</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.completedHours > 0
                    ? `${summary.validHours} / ${summary.completedHours}`
                    : '—'}
                </span>
                {summary.completedHours > 0 ? (
                  <span className="text-xs text-muted-foreground">小时</span>
                ) : null}
              </div>
              <span className="text-xs text-muted-foreground">
                {summary.completedHours > 0
                  ? `可疑 ${summary.suspectHours} 小时 · 缺数据 ${summary.missingHours} 小时`
                  : '等待近 24 小时逐时电量统计'}
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">
                待现场确认
                <InfoTip>
                  同时对比有效口径与含可疑样本口径；可疑跳变电量仅提示现场核查，平台保留原始累计采集记录，不在网页端手工覆盖历史读数
                </InfoTip>
              </span>
              <div className="flex items-baseline gap-1.5">
                <span
                  className={cn(
                    'font-mono text-2xl font-semibold tabular-nums',
                    summary.attentionMeters > 0 ? 'text-warning-text' : 'text-foreground',
                  )}
                >
                  {summary.attentionMeters}
                </span>
                <span className="text-xs text-muted-foreground">台</span>
              </div>
              <span className="text-xs text-muted-foreground">
                {summary.attentionMeters > 0
                  ? '存在可疑差值、缺段或通信延迟'
                  : '各计量表计近 24 小时读数连续'}
              </span>
            </div>
          </div>
        )}
      </Panel>

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
                      const bucket = tooltipProps.payload?.[0]?.payload as
                        | (typeof chartData)[number]
                        | undefined;
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

      <DataTableBlock
        title="计量表计"
        description="核对总电表、总冷量表、流量计与设备电表的通信状态、逐时质量条与累计示数；点击核查详情打开侧边抽屉"
      >
        {isAssetLoading ? (
          <Skeleton className="h-48 w-full rounded-xl" />
        ) : (
          <DataTable
            table={table}
            tableAriaLabel="计量表计"
            empty={
              <div className="py-10 text-center text-sm text-muted-foreground">
                当前站点还没有登记计量表计或累计电表点位
              </div>
            }
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'meter'
                  ? 'pl-5'
                  : header.id === 'energy24h' || header.id === 'reading'
                    ? 'text-right'
                    : header.id === 'actions'
                      ? 'pr-5 text-right'
                      : undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'meter'
                  ? 'pl-5 align-top'
                  : cell.column.id === 'energy24h' || cell.column.id === 'reading'
                    ? 'text-right align-top whitespace-nowrap'
                    : cell.column.id === 'actions'
                      ? 'pr-5 text-right align-top whitespace-nowrap'
                      : 'align-top',
            })}
            footer={
              rows.length > 0 ? <DataTablePagination table={table} totalRows={rows.length} /> : null
            }
          />
        )}
      </DataTableBlock>
    </div>
  );
}
