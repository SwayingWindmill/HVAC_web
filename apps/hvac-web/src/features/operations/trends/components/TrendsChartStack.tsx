import { memo, useCallback, useMemo, useSyncExternalStore } from 'react';
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceArea,
  ReferenceLine,
  XAxis,
  YAxis,
} from 'recharts';
import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart';
import { formatCompactTime, formatDecimal, formatTime } from '@/lib/operator-format';
import type { TrendGranularity } from '../timeline';
import type { TrendsHoverStore } from '../trends-hover-store';
import type { TrendGaugeLane, TrendGaugeRow } from '../trends-model';

const NOOP_SUBSCRIBE = () => () => {};
const NULL_HOVER_SNAPSHOT = () => null;
const CHART_MARGIN = { top: 8, right: 12, left: 0, bottom: 0 } as const;
const Y_DOMAIN: ['auto', 'auto'] = ['auto', 'auto'];
const TOOLTIP_CURSOR = { strokeDasharray: '3 3' } as const;

type FlatLaneRow = Record<string, number | readonly [number, number] | null>;

function useTrendsHoverAt(hoverStore?: TrendsHoverStore): number | null {
  return useSyncExternalStore(
    hoverStore ? hoverStore.subscribe : NOOP_SUBSCRIBE,
    hoverStore ? hoverStore.getSnapshot : NULL_HOVER_SNAPSHOT,
    NULL_HOVER_SNAPSHOT,
  );
}

function flattenLaneRows(
  rows: readonly TrendGaugeRow[],
  series: TrendGaugeLane['series'],
): { readonly flatRows: FlatLaneRow[]; readonly rowByAt: ReadonlyMap<number, TrendGaugeRow> } {
  const rowByAt = new Map<number, TrendGaugeRow>();
  const flatRows: FlatLaneRow[] = new Array(rows.length);
  for (let r = 0; r < rows.length; r += 1) {
    const row = rows[r];
    rowByAt.set(row.at, row);
    const flat: FlatLaneRow = { at: row.at };
    for (let s = 0; s < series.length; s += 1) {
      const cell = row.values[series[s].ref];
      flat[`v_${s}`] = cell?.value ?? null;
      flat[`b_${s}`] = cell?.band ?? null;
      flat[`c_${s}`] = cell?.compareValue ?? null;
    }
    flatRows[r] = flat;
  }
  return { flatRows, rowByAt };
}

export interface TrendOverlaySpan {
  readonly id: string;
  readonly fromMs: number;
  readonly toMs: number;
  readonly severity: 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';
  readonly label: string;
}

export interface TrendOverlayMark {
  readonly id: string;
  readonly atMs: number;
  readonly label: string;
}

const SEVERITY_FILL: Readonly<Record<TrendOverlaySpan['severity'], string>> = {
  CRITICAL: 'var(--severity-critical)',
  MAJOR: 'var(--severity-major)',
  MINOR: 'var(--severity-minor)',
  WARNING: 'var(--severity-warning)',
  INFO: 'var(--severity-info)',
};

function formatAxisTick(atMs: number, granularity: TrendGranularity, timezone: string): string {
  const iso = new Date(atMs).toISOString();
  if (granularity === 'DAY' || granularity === 'HOUR') {
    return formatCompactTime(iso, timezone);
  }
  return formatTime(iso, timezone).slice(-5);
}

const LaneHeaderReadout = memo(function LaneHeaderReadout({
  lane,
  showCompare,
  compareLabel,
  hoverStore,
}: {
  readonly lane: TrendGaugeLane;
  readonly showCompare: boolean;
  readonly compareLabel: string;
  readonly hoverStore?: TrendsHoverStore;
}) {
  const hoverAt = useTrendsHoverAt(hoverStore);
  const hoveredRow =
    hoverAt !== null
      ? lane.rows.find((row) => hoverAt >= row.at && hoverAt < row.endMs)
      : undefined;

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
      <span className="font-semibold text-foreground">{lane.title}</span>
      <div className="ml-auto flex flex-wrap items-center gap-x-3.5 gap-y-1 text-muted-foreground">
        {lane.series.map((s) => {
          const hoveredCell = hoveredRow?.values[s.ref];
          return (
            <span key={s.ref} className="inline-flex items-center gap-1.5">
              <span
                className={
                  s.isSetpoint
                    ? 'h-0.5 w-3 border-t-2 border-dashed'
                    : 'h-2 w-2.5 rounded-[2px]'
                }
                style={
                  s.isSetpoint
                    ? { borderColor: s.colorVar }
                    : { backgroundColor: s.colorVar }
                }
                aria-hidden="true"
              />
              <span className="text-foreground/90">{s.label}</span>
              {s.isSetpoint ? (
                <span className="text-[11px] text-muted-foreground">(设定)</span>
              ) : null}
              {hoveredRow ? (
                <span className="font-mono font-medium text-foreground tabular-nums">
                  {hoveredCell?.value !== null && hoveredCell?.value !== undefined
                    ? `${formatDecimal(hoveredCell.value, s.precision)}${s.unit ? ` ${s.unit}` : ''}`
                    : '—'}
                </span>
              ) : null}
            </span>
          );
        })}
        {showCompare ? (
          <span className="inline-flex items-center gap-1.5">
            <span
              className="h-0.5 w-3 border-t border-dashed"
              style={{ borderColor: 'var(--chart-5)' }}
              aria-hidden="true"
            />
            <span>{compareLabel}</span>
          </span>
        ) : null}
      </div>
    </div>
  );
});

const LaneCrosshairOverlay = memo(function LaneCrosshairOverlay({
  fromMs,
  toMs,
  hoverStore,
}: {
  readonly fromMs: number;
  readonly toMs: number;
  readonly hoverStore?: TrendsHoverStore;
}) {
  const hoverAt = useTrendsHoverAt(hoverStore);
  if (hoverAt === null || hoverAt < fromMs || hoverAt > toMs) return null;
  const percent = ((hoverAt - fromMs) / Math.max(1, toMs - fromMs)) * 100;
  return (
    <div aria-hidden="true" className="pointer-events-none absolute top-[8px] right-[12px] bottom-[30px] left-[48px]">
      <div
        className="absolute inset-y-0 w-px border-l border-dashed border-[var(--chart-now)] opacity-70"
        style={{ left: `${percent}%` }}
      />
    </div>
  );
});

const LaneRechartsPlot = memo(function LaneRechartsPlot({
  lane,
  fromMs,
  toMs,
  granularity,
  timezone,
  showCompare,
  compareLabel,
  alarmSpans,
  commandMarks,
  hoverStore,
}: {
  readonly lane: TrendGaugeLane;
  readonly fromMs: number;
  readonly toMs: number;
  readonly granularity: TrendGranularity;
  readonly timezone: string;
  readonly showCompare: boolean;
  readonly compareLabel: string;
  readonly alarmSpans: readonly TrendOverlaySpan[];
  readonly commandMarks: readonly TrendOverlayMark[];
  readonly hoverStore?: TrendsHoverStore;
}) {
  const config: ChartConfig = useMemo(
    () => Object.fromEntries(lane.series.map((s) => [s.ref, { label: s.label, color: s.colorVar }])),
    [lane.series],
  );
  const { flatRows, rowByAt } = useMemo(
    () => flattenLaneRows(lane.rows, lane.series),
    [lane.rows, lane.series],
  );
  const xDomain = useMemo<[number, number]>(() => [fromMs, toMs], [fromMs, toMs]);
  const formatTickX = useCallback((val: number) => formatAxisTick(val, granularity, timezone), [granularity, timezone]);
  const formatTickY = useCallback((val: number) => formatDecimal(val, lane.unit === 'm³/h' ? 0 : 1), [lane.unit]);
  const handleMouseMove = useCallback(
    (state: { readonly activeLabel?: number | string | null }) => {
      const activeLabel = state?.activeLabel;
      if (typeof activeLabel === 'number' && Number.isFinite(activeLabel)) {
        hoverStore?.setHoverAt(activeLabel);
      }
    },
    [hoverStore],
  );
  const handleMouseLeave = useCallback(() => hoverStore?.setHoverAt(null), [hoverStore]);

  const renderTooltipContent = useCallback(
    ({ active, payload }: { readonly active?: boolean; readonly payload?: ReadonlyArray<{ readonly payload?: unknown }> }) => {
      const raw = payload?.[0]?.payload;
      const atVal = raw && typeof raw === 'object' && 'at' in raw && typeof raw.at === 'number' ? raw.at : null;
      const row = atVal !== null ? rowByAt.get(atVal) : undefined;
      if (!active || !row) return null;
      const startText = formatCompactTime(new Date(row.at).toISOString(), timezone);
      const endClock = formatTime(new Date(row.endMs).toISOString(), timezone).slice(-5);

      return (
        <div className="grid min-w-52 gap-1.5 rounded-lg border bg-popover px-2.5 py-2 text-xs text-popover-foreground shadow-md">
          <p className="font-medium tabular-nums">
            {startText}–{endClock}
          </p>
          {lane.series.map((s) => {
            const cell = row.values[s.ref];
            const unitSuffix = s.unit ? ` ${s.unit}` : '';
            return (
              <div key={s.ref} className="flex flex-col gap-0.5">
                <div className="flex items-center justify-between gap-4">
                  <span className="flex items-center gap-1.5 text-muted-foreground">
                    <span
                      className="size-2 rounded-[2px]"
                      style={{ backgroundColor: s.colorVar }}
                      aria-hidden="true"
                    />
                    {s.label}
                  </span>
                  <span className="font-medium tabular-nums">
                    {cell?.value !== null && cell?.value !== undefined
                      ? `${formatDecimal(cell.value, s.precision)}${unitSuffix}`
                      : '该时段无读数'}
                  </span>
                </div>
                {cell?.band && cell.band[0] !== cell.band[1] ? (
                  <p className="pl-3.5 text-[11px] text-muted-foreground tabular-nums">
                    范围 {formatDecimal(cell.band[0], s.precision)}–
                    {formatDecimal(cell.band[1], s.precision)}
                    {unitSuffix}
                  </p>
                ) : null}
                {showCompare && cell?.compareValue !== null && cell?.compareValue !== undefined ? (
                  <p className="pl-3.5 text-[11px] text-muted-foreground tabular-nums">
                    {compareLabel} {formatDecimal(cell.compareValue, s.precision)}
                    {unitSuffix}
                  </p>
                ) : null}
              </div>
            );
          })}
        </div>
      );
    },
    [compareLabel, lane.series, rowByAt, showCompare, timezone],
  );

  return (
    <div className="relative">
      <ChartContainer config={config} className="aspect-auto h-[210px] w-full">
        <ComposedChart
          data={flatRows}
          syncId="hvac-trends-sync"
          syncMethod="value"
          margin={CHART_MARGIN}
          onMouseMove={handleMouseMove}
          onMouseLeave={handleMouseLeave}
        >
          <CartesianGrid vertical={false} />
          <XAxis
            dataKey="at"
            type="number"
            scale="time"
            domain={xDomain}
            tickFormatter={formatTickX}
            tickLine={false}
            axisLine={false}
            minTickGap={36}
          />
          <YAxis
            tickLine={false}
            axisLine={false}
            width={48}
            domain={Y_DOMAIN}
            tickFormatter={formatTickY}
          />

          {alarmSpans.map((span) => (
            <ReferenceArea
              key={span.id}
              x1={Math.max(fromMs, span.fromMs)}
              x2={Math.min(toMs, span.toMs)}
              fill={SEVERITY_FILL[span.severity]}
              fillOpacity={0.11}
              strokeOpacity={0}
              ifOverflow="hidden"
            />
          ))}

          {commandMarks.map((mark) => (
            <ReferenceLine
              key={mark.id}
              x={mark.atMs}
              stroke="var(--primary)"
              strokeDasharray="3 3"
              strokeOpacity={0.75}
              ifOverflow="hidden"
            />
          ))}

          <ChartTooltip cursor={TOOLTIP_CURSOR} content={renderTooltipContent} />

          {lane.series.map((s, index) =>
            !s.isSetpoint ? (
              <Area
                key={`${s.ref}-band`}
                dataKey={`b_${index}`}
                stroke="none"
                fill={s.colorVar}
                fillOpacity={0.12}
                connectNulls={false}
                isAnimationActive={false}
              />
            ) : null,
          )}

          {showCompare
            ? lane.series.map((s, index) => (
                <Line
                  key={`${s.ref}-compare`}
                  type={s.isSetpoint ? 'stepAfter' : 'monotone'}
                  dataKey={`c_${index}`}
                  stroke={lane.series.length === 1 ? 'var(--chart-5)' : s.colorVar}
                  strokeOpacity={lane.series.length === 1 ? 0.9 : 0.45}
                  strokeWidth={1.25}
                  strokeDasharray="3 3"
                  dot={false}
                  connectNulls={false}
                  isAnimationActive={false}
                />
              ))
            : null}

          {lane.series.map((s, index) => (
            <Line
              key={s.ref}
              type={s.isSetpoint ? 'stepAfter' : 'monotone'}
              dataKey={`v_${index}`}
              name={s.label}
              stroke={s.colorVar}
              strokeWidth={s.isSetpoint ? 1.5 : 2}
              strokeDasharray={s.isSetpoint ? '6 3' : undefined}
              dot={false}
              connectNulls={false}
              isAnimationActive={false}
            />
          ))}
        </ComposedChart>
      </ChartContainer>
      <LaneCrosshairOverlay fromMs={fromMs} toMs={toMs} hoverStore={hoverStore} />
    </div>
  );
});

export const TrendsChartStack = memo(function TrendsChartStack({
  lanes,
  fromMs,
  toMs,
  granularity,
  timezone,
  showCompare,
  compareLabel,
  alarmSpans = [],
  commandMarks = [],
  hoverStore,
}: {
  readonly lanes: readonly TrendGaugeLane[];
  readonly fromMs: number;
  readonly toMs: number;
  readonly granularity: TrendGranularity;
  readonly timezone: string;
  readonly showCompare: boolean;
  readonly compareLabel: string;
  readonly alarmSpans?: readonly TrendOverlaySpan[];
  readonly commandMarks?: readonly TrendOverlayMark[];
  readonly hoverStore?: TrendsHoverStore;
}) {
  return (
    <div className="flex flex-col divide-y divide-divider">
      {lanes.map((lane) => (
        <section
          key={lane.id}
          role="region"
          aria-label={lane.title}
          className="flex flex-col gap-2 px-5 py-3.5"
        >
          <LaneHeaderReadout
            lane={lane}
            showCompare={showCompare}
            compareLabel={compareLabel}
            hoverStore={hoverStore}
          />
          <LaneRechartsPlot
            lane={lane}
            fromMs={fromMs}
            toMs={toMs}
            granularity={granularity}
            timezone={timezone}
            showCompare={showCompare}
            compareLabel={compareLabel}
            alarmSpans={alarmSpans}
            commandMarks={commandMarks}
            hoverStore={hoverStore}
          />
        </section>
      ))}
    </div>
  );
});
