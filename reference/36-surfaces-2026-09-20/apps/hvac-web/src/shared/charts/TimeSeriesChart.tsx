import type { CSSProperties, ReactNode } from 'react';
import {
  Area,
  Bar,
  BarChart,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceLine,
  XAxis,
  YAxis,
} from 'recharts';
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';

export type OperationalChartTone = 'primary' | 'secondary' | 'success' | 'warning' | 'error' | 'info';

export interface OperationalChartPoint<TMeta = unknown> {
  readonly x: string | number;
  readonly value: number | null;
  readonly meta?: TMeta;
}

export interface OperationalChartSeries<TMeta = unknown> {
  readonly key: string;
  readonly label: string;
  readonly points: readonly OperationalChartPoint<TMeta>[];
  readonly tone?: OperationalChartTone;
  readonly dashed?: boolean;
  readonly showPoints?: boolean;
  readonly interpolation?: 'monotone' | 'linear' | 'stepAfter';
  readonly area?: boolean;
  readonly fillOpacity?: number;
}

export interface OperationalChartAnnotation {
  readonly value: number;
  readonly label: string;
  readonly tone?: OperationalChartTone;
}

interface BaseChartProps {
  readonly className?: string;
  readonly style?: CSSProperties;
  readonly ariaLabel: string;
}

export interface TimeSeriesChartProps<TMeta = unknown> extends BaseChartProps {
  readonly series: readonly OperationalChartSeries<TMeta>[];
  readonly xType?: 'time' | 'category';
  readonly xLabelFormatter?: (value: string | number) => string;
  readonly unit?: string;
  readonly yDomain?: readonly [number, number];
  readonly annotations?: readonly OperationalChartAnnotation[];
  readonly tooltipValueFormatter?: (point: OperationalChartPoint<TMeta>, series: OperationalChartSeries<TMeta>) => ReactNode;
  readonly selectedX?: string | number;
  readonly onSelectX?: (value: string | number) => void;
  readonly compact?: boolean;
}

export interface DualAxisTimeSeriesChartProps extends BaseChartProps {
  readonly primarySeries: readonly OperationalChartSeries[];
  readonly secondarySeries: readonly OperationalChartSeries[];
  readonly primaryUnit?: string;
  readonly secondaryUnit?: string;
  readonly secondaryDomain?: readonly [number, number];
  readonly xType?: 'time' | 'category';
  readonly xLabelFormatter?: (value: string | number) => string;
  readonly compact?: boolean;
}

export interface CategoryColumnDatum<TMeta = unknown> {
  readonly category: string;
  readonly value: number;
  readonly meta?: TMeta;
}

export interface CategoryColumnChartProps<TMeta = unknown> extends BaseChartProps {
  readonly data: readonly CategoryColumnDatum<TMeta>[];
  readonly unit?: string;
  readonly onColumnClick?: (datum: CategoryColumnDatum<TMeta>) => void;
}

export interface EnergyTrendDatum {
  readonly category: string;
  readonly current: number | null;
  readonly previous: number | null;
  readonly cumulative?: number | null;
  readonly index: number;
}

export interface EnergyTrendChartProps extends BaseChartProps {
  readonly data: readonly EnergyTrendDatum[];
  readonly currentLabel: string;
  readonly previousLabel: string;
  readonly currentUnit: string;
  readonly cumulativeLabel?: string;
  readonly cumulativeUnit?: string;
  readonly onBucketClick?: (datum: EnergyTrendDatum) => void;
}

const TONE_COLOR: Record<OperationalChartTone, string> = {
  primary: 'var(--chart-1)',
  secondary: 'var(--muted-foreground)',
  success: 'var(--success)',
  warning: 'var(--warning)',
  error: 'var(--destructive)',
  info: 'var(--chart-2)',
};

function toneColor(tone: OperationalChartTone | undefined): string {
  return TONE_COLOR[tone ?? 'primary'];
}

function chartHeight(style: CSSProperties | undefined, fallback: number): number {
  return typeof style?.height === 'number' ? style.height : fallback;
}

function buildConfig(series: readonly OperationalChartSeries[]): ChartConfig {
  return Object.fromEntries(series.map((item) => [item.key, {
    label: item.label,
    color: toneColor(item.tone),
  }])) satisfies ChartConfig;
}

function mergeSeriesRows<TMeta>(series: readonly OperationalChartSeries<TMeta>[]) {
  const rows = new Map<string, Record<string, unknown>>();
  series.forEach((item) => {
    item.points.forEach((point) => {
      const id = String(point.x);
      const row = rows.get(id) ?? { x: point.x };
      row[item.key] = point.value;
      row[`${item.key}__meta`] = point.meta;
      rows.set(id, row);
    });
  });
  return [...rows.values()];
}

export function TimeSeriesChart<TMeta = unknown>({
  series,
  xLabelFormatter,
  unit,
  yDomain,
  annotations = [],
  tooltipValueFormatter,
  selectedX,
  onSelectX,
  compact = false,
  className,
  style,
  ariaLabel,
}: TimeSeriesChartProps<TMeta>) {
  const config = buildConfig(series);
  const rows = mergeSeriesRows(series);
  const seriesByKey = new Map(series.map((item) => [item.key, item] as const));
  const height = chartHeight(style, compact ? 180 : 240);

  return (
    <ChartContainer
      config={config}
      className={className}
      style={{ ...style, height, aspectRatio: 'auto' }}
      role="img"
      aria-label={ariaLabel}
      initialDimension={{ width: 640, height }}
    >
      <ComposedChart
        accessibilityLayer
        data={rows}
        margin={{ top: 12, right: 16, bottom: 4, left: 0 }}
        onClick={(state) => {
          const activeLabel = state?.activeLabel;
          if (onSelectX && (typeof activeLabel === 'string' || typeof activeLabel === 'number')) onSelectX(activeLabel);
        }}
      >
        <defs>
          {series.map((item) => (
            <linearGradient key={`grad-${item.key}`} id={`fill-${item.key}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor={`var(--color-${item.key})`} stopOpacity={0.35} />
              <stop offset="95%" stopColor={`var(--color-${item.key})`} stopOpacity={0.02} />
            </linearGradient>
          ))}
        </defs>
        <CartesianGrid vertical={false} strokeDasharray="3 3" stroke="var(--border)" opacity={0.6} />
        <XAxis
          dataKey="x"
          tickLine={false}
          axisLine={false}
          minTickGap={compact ? 28 : 20}
          tickFormatter={(value) => xLabelFormatter ? xLabelFormatter(value) : String(value)}
        />
        <YAxis
          domain={yDomain ? [...yDomain] : ['auto', 'auto']}
          tickLine={false}
          axisLine={false}
          width={compact ? 34 : 44}
          unit={unit ? ` ${unit}` : undefined}
        />
        <ChartTooltip
          cursor={{ stroke: 'var(--border)', strokeWidth: 1 }}
          content={(
            <ChartTooltipContent
              indicator="dot"
              labelFormatter={(value) => xLabelFormatter ? xLabelFormatter(value as string | number) : String(value ?? '')}
              formatter={(value, _name, item) => {
                const key = String(item.dataKey ?? '');
                const sourceSeries = seriesByKey.get(key);
                const raw = item.payload as Record<string, unknown> | undefined;
                const point: OperationalChartPoint<TMeta> = {
                  x: (raw?.x as string | number | undefined) ?? '',
                  value: typeof value === 'number' ? value : null,
                  meta: raw?.[`${key}__meta`] as TMeta | undefined,
                };
                const formatted = sourceSeries && tooltipValueFormatter
                  ? tooltipValueFormatter(point, sourceSeries)
                  : point.value == null ? '—' : `${point.value}${unit ? ` ${unit}` : ''}`;
                return (
                  <div className="flex min-w-32 items-center justify-between gap-4">
                    <span className="text-muted-foreground">{sourceSeries?.label ?? key}</span>
                    <span className="font-medium tabular-nums">{formatted}</span>
                  </div>
                );
              }}
            />
          )}
        />
        {series.length > 1 ? <ChartLegend content={<ChartLegendContent />} /> : null}
        {selectedX !== undefined ? (
          <ReferenceLine x={selectedX} stroke="var(--foreground)" strokeOpacity={0.35} strokeDasharray="3 3" />
        ) : null}
        {annotations.map((annotation) => (
          <ReferenceLine
            key={`${annotation.label}-${annotation.value}`}
            y={annotation.value}
            stroke={toneColor(annotation.tone ?? 'warning')}
            strokeDasharray="4 4"
            label={{ value: annotation.label, position: 'insideTopRight', fill: 'var(--muted-foreground)', fontSize: 11 }}
          />
        ))}
        {series.map((item) => (
          item.area ? (
            <Area
              key={item.key}
              type={item.interpolation ?? 'monotone'}
              dataKey={item.key}
              name={item.label}
              stroke={`var(--color-${item.key})`}
              strokeWidth={2}
              strokeDasharray={item.dashed ? '5 4' : undefined}
              fillOpacity={item.fillOpacity ?? 1}
              fill={`url(#fill-${item.key})`}
              dot={item.showPoints ? { r: 2.5 } : false}
              activeDot={{ r: 4 }}
              connectNulls={false}
              isAnimationActive={false}
            />
          ) : (
            <Line
              key={item.key}
              type={item.interpolation ?? 'monotone'}
              dataKey={item.key}
              name={item.label}
              stroke={`var(--color-${item.key})`}
              strokeWidth={2}
              strokeDasharray={item.dashed ? '5 4' : undefined}
              dot={item.showPoints ? { r: 2.5 } : false}
              activeDot={{ r: 4 }}
              connectNulls={false}
              isAnimationActive={false}
            />
          )
        ))}
      </ComposedChart>
    </ChartContainer>
  );
}

export function DualAxisTimeSeriesChart({
  primarySeries,
  secondarySeries,
  primaryUnit,
  secondaryUnit,
  secondaryDomain,
  xLabelFormatter,
  compact = false,
  className,
  style,
  ariaLabel,
}: DualAxisTimeSeriesChartProps) {
  const allSeries = [...primarySeries, ...secondarySeries];
  const secondaryKeys = new Set(secondarySeries.map((item) => item.key));
  const config = buildConfig(allSeries);
  const rows = mergeSeriesRows(allSeries);
  const height = chartHeight(style, compact ? 220 : 280);

  return (
    <ChartContainer
      config={config}
      className={className}
      style={{ ...style, height, aspectRatio: 'auto' }}
      role="img"
      aria-label={ariaLabel}
      initialDimension={{ width: 720, height }}
    >
      <ComposedChart accessibilityLayer data={rows} margin={{ top: 12, right: 8, bottom: 4, left: 0 }}>
        <CartesianGrid vertical={false} strokeDasharray="3 3" />
        <XAxis
          dataKey="x"
          tickLine={false}
          axisLine={false}
          minTickGap={compact ? 28 : 20}
          tickFormatter={(value) => xLabelFormatter ? xLabelFormatter(value) : String(value)}
        />
        <YAxis yAxisId="primary" tickLine={false} axisLine={false} width={42} unit={primaryUnit ? ` ${primaryUnit}` : undefined} />
        <YAxis
          yAxisId="secondary"
          orientation="right"
          domain={secondaryDomain ? [...secondaryDomain] : ['auto', 'auto']}
          tickLine={false}
          axisLine={false}
          width={42}
          unit={secondaryUnit ? ` ${secondaryUnit}` : undefined}
        />
        <ChartTooltip
          cursor={false}
          content={(
            <ChartTooltipContent
              labelFormatter={(value) => xLabelFormatter ? xLabelFormatter(value as string | number) : String(value ?? '')}
              formatter={(value, _name, item) => {
                const key = String(item.dataKey ?? '');
                const series = allSeries.find((candidate) => candidate.key === key);
                const unit = secondaryKeys.has(key) ? secondaryUnit : primaryUnit;
                return (
                  <div className="flex min-w-32 items-center justify-between gap-4">
                    <span className="text-muted-foreground">{series?.label ?? key}</span>
                    <span className="font-medium tabular-nums">{value == null ? '—' : `${value}${unit ? ` ${unit}` : ''}`}</span>
                  </div>
                );
              }}
            />
          )}
        />
        <ChartLegend content={<ChartLegendContent />} />
        {primarySeries.map((item) => (
          <Line
            key={item.key}
            yAxisId="primary"
            type="monotone"
            dataKey={item.key}
            name={item.label}
            stroke={`var(--color-${item.key})`}
            strokeWidth={2}
            strokeDasharray={item.dashed ? '5 4' : undefined}
            dot={item.showPoints ? { r: 2.5 } : false}
            activeDot={{ r: 4 }}
            isAnimationActive={false}
          />
        ))}
        {secondarySeries.map((item) => (
          <Line
            key={item.key}
            yAxisId="secondary"
            type="monotone"
            dataKey={item.key}
            name={item.label}
            stroke={`var(--color-${item.key})`}
            strokeWidth={2}
            strokeDasharray={item.dashed ? '5 4' : undefined}
            dot={item.showPoints ? { r: 2.5 } : false}
            activeDot={{ r: 4 }}
            isAnimationActive={false}
          />
        ))}
      </ComposedChart>
    </ChartContainer>
  );
}

export function CategoryColumnChart<TMeta = unknown>({
  data,
  unit,
  onColumnClick,
  className,
  style,
  ariaLabel,
}: CategoryColumnChartProps<TMeta>) {
  const config = { value: { label: unit ?? '数值', color: 'var(--chart-1)' } } satisfies ChartConfig;
  const height = chartHeight(style, 240);
  return (
    <ChartContainer
      config={config}
      className={className}
      style={{ ...style, height, aspectRatio: 'auto' }}
      role="img"
      aria-label={ariaLabel}
      initialDimension={{ width: 640, height }}
    >
      <BarChart accessibilityLayer data={[...data]} margin={{ top: 12, right: 8, bottom: 4, left: 0 }}>
        <CartesianGrid vertical={false} strokeDasharray="3 3" />
        <XAxis dataKey="category" tickLine={false} axisLine={false} />
        <YAxis tickLine={false} axisLine={false} unit={unit ? ` ${unit}` : undefined} />
        <ChartTooltip cursor={false} content={<ChartTooltipContent />} />
        <Bar
          dataKey="value"
          fill="var(--color-value)"
          radius={[4, 4, 0, 0]}
          maxBarSize={28}
          isAnimationActive={false}
          onClick={(_payload, index) => {
            const datum = data[index];
            if (datum) onColumnClick?.(datum);
          }}
        />
      </BarChart>
    </ChartContainer>
  );
}

export function EnergyTrendChart({
  data,
  currentLabel,
  previousLabel,
  currentUnit,
  cumulativeLabel = '累计',
  cumulativeUnit = currentUnit,
  onBucketClick,
  className,
  style,
  ariaLabel,
}: EnergyTrendChartProps) {
  const hasCumulative = data.some((item) => item.cumulative != null);
  const config = {
    current: { label: currentLabel, color: 'var(--chart-1)' },
    previous: { label: previousLabel, color: 'var(--muted-foreground)' },
    cumulative: { label: cumulativeLabel, color: 'var(--success)' },
  } satisfies ChartConfig;
  const height = chartHeight(style, 320);

  return (
    <ChartContainer
      config={config}
      className={className}
      style={{ ...style, height, aspectRatio: 'auto' }}
      role="img"
      aria-label={ariaLabel}
      initialDimension={{ width: 760, height }}
    >
      <ComposedChart accessibilityLayer data={[...data]} margin={{ top: 12, right: 8, bottom: 4, left: 0 }}>
        <CartesianGrid vertical={false} strokeDasharray="3 3" />
        <XAxis dataKey="category" tickLine={false} axisLine={false} minTickGap={20} />
        <YAxis yAxisId="energy" tickLine={false} axisLine={false} unit={` ${currentUnit}`} />
        {hasCumulative ? <YAxis yAxisId="cumulative" orientation="right" tickLine={false} axisLine={false} unit={` ${cumulativeUnit}`} /> : null}
        <ChartTooltip
          cursor={false}
          content={(
            <ChartTooltipContent
              formatter={(value, _name, item) => {
                const key = String(item.dataKey ?? '');
                const label = config[key as keyof typeof config]?.label ?? key;
                const unit = key === 'cumulative' ? cumulativeUnit : currentUnit;
                return (
                  <div className="flex min-w-32 items-center justify-between gap-4">
                    <span className="text-muted-foreground">{label}</span>
                    <span className="font-medium tabular-nums">{value == null ? '未返回' : `${value} ${unit}`}</span>
                  </div>
                );
              }}
            />
          )}
        />
        <ChartLegend content={<ChartLegendContent />} />
        <Bar
          yAxisId="energy"
          dataKey="current"
          name={currentLabel}
          fill="var(--color-current)"
          radius={[4, 4, 0, 0]}
          maxBarSize={32}
          isAnimationActive={false}
          onClick={(_payload, index) => {
            const datum = data[index];
            if (datum) onBucketClick?.(datum);
          }}
        />
        <Line
          yAxisId="energy"
          type="monotone"
          dataKey="previous"
          name={previousLabel}
          stroke="var(--color-previous)"
          strokeWidth={2}
          strokeDasharray="5 4"
          dot={false}
          isAnimationActive={false}
        />
        {hasCumulative ? (
          <Line
            yAxisId="cumulative"
            type="monotone"
            dataKey="cumulative"
            name={cumulativeLabel}
            stroke="var(--color-cumulative)"
            strokeWidth={2}
            dot={false}
            isAnimationActive={false}
          />
        ) : null}
      </ComposedChart>
    </ChartContainer>
  );
}
