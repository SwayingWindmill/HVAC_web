import { useId, useMemo, useState } from 'react';
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { Badge } from '@/components/ui/badge';
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
import {
  formatHourSlotLabel,
  formatSlotDateTime,
  type GovernanceStatusPresentation,
} from '../integrations-model';
import type { HourlyHistoryRow } from '../query';

const BADGE_VARIANTS: Readonly<
  Record<GovernanceStatusPresentation['tone'], 'destructive' | 'warning' | 'secondary' | 'success'>
> = {
  danger: 'destructive',
  warning: 'warning',
  neutral: 'secondary',
  success: 'success',
};

export function GovernanceBadge({
  status,
  className,
}: {
  readonly status: GovernanceStatusPresentation;
  readonly className?: string;
}) {
  return (
    <Badge variant={BADGE_VARIANTS[status.tone]} className={className}>
      <span className="text-[10px] leading-none" aria-hidden="true">
        {status.shape}
      </span>
      <span>{status.label}</span>
    </Badge>
  );
}

const HISTORY_CHART_CONFIG = {
  value: {
    label: '读数',
    color: 'var(--chart-1)',
  },
  placeholder: {
    label: '缺读数时段',
    color: 'var(--muted-foreground)',
  },
} satisfies ChartConfig;

export function DrawerHourlyHistorySection({
  title,
  unit,
  isCounter,
  rows,
  isLoading,
  isError,
  windowFromMs,
  windowToMs,
  timezone,
}: {
  readonly title: string;
  readonly unit: string | null;
  readonly isCounter?: boolean;
  readonly rows: readonly HourlyHistoryRow[] | undefined;
  readonly isLoading: boolean;
  readonly isError: boolean;
  readonly windowFromMs: number;
  readonly windowToMs: number;
  readonly timezone: string;
}) {
  const [mode, setMode] = useState<'chart' | 'table'>('chart');
  const patternId = useId().replace(/:/g, '');

  const slots = useMemo(() => {
    const byStart = new Map<number, HourlyHistoryRow>();
    for (const row of rows ?? []) {
      byStart.set(Date.parse(row.periodStart), row);
    }
    const maxVal = (rows ?? []).reduce((acc, row) => Math.max(acc, Math.abs(row.value)), 0);
    const placeholderHeight = maxVal > 0 ? Number((maxVal * 0.28).toFixed(2)) : 1;

    const result: {
      readonly startMs: number;
      readonly axisLabel: string;
      readonly fullLabel: string;
      readonly value: number | null;
      readonly placeholder: number | null;
      readonly minimum: number | null;
      readonly maximum: number | null;
      readonly resetCount: number;
    }[] = [];
    for (let cursor = windowFromMs; cursor < windowToMs; cursor += 3_600_000) {
      const hit = byStart.get(cursor);
      result.push({
        startMs: cursor,
        axisLabel: formatHourSlotLabel(cursor, timezone),
        fullLabel: formatSlotDateTime(cursor, timezone),
        value: hit ? Number(hit.value.toFixed(2)) : null,
        placeholder: hit ? null : placeholderHeight,
        minimum: hit?.minimum ?? null,
        maximum: hit?.maximum ?? null,
        resetCount: hit?.resetCount ?? 0,
      });
    }
    return result;
  }, [rows, timezone, windowFromMs, windowToMs]);

  const hasAnyValue = slots.some((slot) => slot.value !== null);
  const metricLabel = isCounter ? '小时增量' : '小时均值';

  return (
    <section className="flex flex-col gap-2.5 border-b border-divider px-6 py-4">
      <div className="flex items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold">{title}</h3>
          <p className="text-xs text-muted-foreground">
            近 24 小时逐时{isCounter ? '增量' : '均值'}
            {unit ? `（${unit}）` : ''}
          </p>
        </div>
        <ToggleGroup
          type="single"
          size="sm"
          variant="outline"
          value={mode}
          onValueChange={(next) => {
            if (next === 'chart' || next === 'table') setMode(next);
          }}
          aria-label={`${title}视图切换`}
        >
          <ToggleGroupItem value="chart" className="text-xs">
            图表
          </ToggleGroupItem>
          <ToggleGroupItem value="table" className="text-xs">
            表格
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      {isLoading ? (
        <Skeleton className="h-44 w-full rounded-lg" />
      ) : isError ? (
        <p className="rounded-lg border border-divider bg-muted/40 px-3 py-4 text-xs text-muted-foreground">
          历史读数暂时无法读取，可前往历史趋势进一步排查。
        </p>
      ) : !hasAnyValue ? (
        <p className="rounded-lg border border-divider bg-muted/40 px-3 py-4 text-xs text-muted-foreground">
          近 24 小时暂无有效历史样本（—）。
        </p>
      ) : mode === 'chart' ? (
        <ChartContainer config={HISTORY_CHART_CONFIG} className="h-44 w-full">
          <BarChart data={slots} margin={{ top: 6, right: 6, left: -12, bottom: 0 }}>
            <defs>
              <pattern
                id={`history-hatch-${patternId}`}
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
              dataKey="axisLabel"
              tickLine={false}
              axisLine={false}
              interval={3}
              className="font-mono text-[11px]"
            />
            <YAxis
              tickLine={false}
              axisLine={false}
              width={44}
              className="font-mono text-[11px]"
            />
            <ChartTooltip
              content={(tooltipProps) => {
                const slot = tooltipProps.payload?.[0]?.payload as (typeof slots)[number] | undefined;
                if (tooltipProps.active && slot && slot.value === null) {
                  return <ChartEmptySlotTooltip label={slot.fullLabel} />;
                }
                return (
                  <ChartTooltipContent
                    active={tooltipProps.active}
                    payload={tooltipProps.payload?.filter((entry) => entry.dataKey === 'value')}
                    label={slot?.fullLabel ?? tooltipProps.label}
                    indicator="dot"
                    formatter={(val) => (
                      <div className="flex w-full items-center gap-2">
                        <span
                          className="size-2 shrink-0 rounded-[2px]"
                          style={{ backgroundColor: 'var(--color-value)' }}
                        />
                        <span className="flex-1 text-muted-foreground">{metricLabel}</span>
                        <span className="font-mono font-medium tabular-nums">
                          {formatDecimal(Number(val), 2)}
                          {unit ? ` ${unit}` : ''}
                        </span>
                      </div>
                    )}
                  />
                );
              }}
            />
            <Bar
              dataKey="value"
              stackId="history"
              fill="var(--color-value)"
              radius={[3, 3, 0, 0]}
              isAnimationActive={false}
            />
            <Bar
              dataKey="placeholder"
              stackId="history"
              fill={`url(#history-hatch-${patternId})`}
              stroke="var(--border)"
              strokeWidth={1}
              radius={[3, 3, 0, 0]}
              isAnimationActive={false}
            />
          </BarChart>
        </ChartContainer>
      ) : (
        <div className="max-h-56 overflow-y-auto rounded-lg border border-divider">
          <Table aria-label={`${title}表格`}>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-3">时段</TableHead>
                <TableHead className="text-right">
                  {isCounter ? '小时增量' : '均值'}
                  {unit ? ` (${unit})` : ''}
                </TableHead>
                <TableHead className="pr-3 text-right">
                  {isCounter ? '翻转/清零' : '最小–最大'}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {slots.map((slot) => (
                <TableRow key={slot.startMs}>
                  <TableCell className="pl-3 font-mono text-xs">{slot.fullLabel}</TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {slot.value !== null ? formatDecimal(slot.value, 2) : '—'}
                  </TableCell>
                  <TableCell className="pr-3 text-right font-mono text-xs text-muted-foreground tabular-nums">
                    {isCounter
                      ? slot.resetCount > 0
                        ? `${slot.resetCount} 次`
                        : '—'
                      : slot.minimum !== null && slot.maximum !== null
                        ? `${formatDecimal(slot.minimum, 1)}–${formatDecimal(slot.maximum, 1)}`
                        : '—'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  );
}
