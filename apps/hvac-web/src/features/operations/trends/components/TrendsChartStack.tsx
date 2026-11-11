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
import type { TrendGaugeLane, TrendGaugeRow } from '../trends-model';

export interface TrendOverlaySpan {
  readonly id: string;
  readonly fromMs: number;
  readonly toMs: number;
  readonly severity: 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING';
  readonly label: string;
}

export interface TrendOverlayMark {
  readonly id: string;
  readonly atMs: number;
  readonly label: string;
}

const SEVERITY_FILL: Readonly<Record<TrendOverlaySpan['severity'], string>> = {
  CRITICAL: 'var(--destructive)',
  MAJOR: 'var(--status-warning)',
  MINOR: 'var(--status-warning)',
  WARNING: 'var(--status-warning)',
};

function formatAxisTick(atMs: number, granularity: TrendGranularity, timezone: string): string {
  const iso = new Date(atMs).toISOString();
  if (granularity === 'DAY' || granularity === 'HOUR') {
    return formatCompactTime(iso, timezone);
  }
  return formatTime(iso, timezone).slice(-5);
}

export function TrendsChartStack({
  lanes,
  fromMs,
  toMs,
  granularity,
  timezone,
  showCompare,
  compareLabel,
  alarmSpans = [],
  commandMarks = [],
  onHoverAt,
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
  readonly onHoverAt?: (atMs: number | null) => void;
}) {
  return (
    <div className="flex flex-col divide-y divide-divider">
      {lanes.map((lane) => {
        const config: ChartConfig = Object.fromEntries(
          lane.series.map((s) => [s.ref, { label: s.label, color: s.colorVar }]),
        );

        return (
          <section
            key={lane.id}
            role="region"
            aria-label={lane.title}
            className="flex flex-col gap-2 px-5 py-3.5"
          >
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
              <span className="font-semibold text-foreground">{lane.title}</span>
              <div className="ml-auto flex flex-wrap items-center gap-x-3.5 gap-y-1 text-muted-foreground">
                {lane.series.map((s) => (
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
                  </span>
                ))}
                {showCompare ? (
                  <span className="inline-flex items-center gap-1.5">
                    <span
                      className="h-0.5 w-3 border-t border-dashed border-muted-foreground"
                      aria-hidden="true"
                    />
                    <span>{compareLabel}</span>
                  </span>
                ) : null}
              </div>
            </div>

            <ChartContainer config={config} className="aspect-auto h-[210px] w-full">
              <ComposedChart
                data={lane.rows as TrendGaugeRow[]}
                syncId="hvac-trends-sync"
                syncMethod="value"
                margin={{ top: 8, right: 12, left: 2, bottom: 0 }}
                onMouseMove={(state) => {
                  const activeLabel = state?.activeLabel;
                  if (typeof activeLabel === 'number' && Number.isFinite(activeLabel)) {
                    onHoverAt?.(activeLabel);
                  }
                }}
                onMouseLeave={() => onHoverAt?.(null)}
              >
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="at"
                  type="number"
                  scale="time"
                  domain={[fromMs, toMs]}
                  tickFormatter={(val: number) => formatAxisTick(val, granularity, timezone)}
                  tickLine={false}
                  axisLine={false}
                  minTickGap={36}
                />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={46}
                  domain={['auto', 'auto']}
                  tickFormatter={(val: number) => formatDecimal(val, lane.unit === 'm³/h' ? 0 : 1)}
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

                <ChartTooltip
                  cursor={{ strokeDasharray: '3 3' }}
                  content={({ active, payload }) => {
                    const row = payload?.[0]?.payload as TrendGaugeRow | undefined;
                    if (!active || !row) return null;
                    const startText = formatCompactTime(new Date(row.at).toISOString(), timezone);
                    const endClock = formatTime(new Date(row.endMs).toISOString(), timezone).slice(-5);

                    return (
                      <div className="grid min-w-52 gap-1.5 rounded-lg border bg-background px-2.5 py-2 text-xs shadow-xl">
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
                  }}
                />

                {lane.series.map((s) =>
                  !s.isSetpoint ? (
                    <Area
                      key={`${s.ref}-band`}
                      dataKey={(row: TrendGaugeRow) => row.values[s.ref]?.band ?? null}
                      stroke="none"
                      fill={s.colorVar}
                      fillOpacity={0.12}
                      connectNulls={false}
                      isAnimationActive={false}
                    />
                  ) : null,
                )}

                {showCompare
                  ? lane.series.map((s) => (
                      <Line
                        key={`${s.ref}-compare`}
                        type={s.isSetpoint ? 'stepAfter' : 'monotone'}
                        dataKey={(row: TrendGaugeRow) => row.values[s.ref]?.compareValue ?? null}
                        stroke={s.colorVar}
                        strokeOpacity={0.45}
                        strokeWidth={1.25}
                        strokeDasharray="3 3"
                        dot={false}
                        connectNulls={false}
                        isAnimationActive={false}
                      />
                    ))
                  : null}

                {lane.series.map((s) => (
                  <Line
                    key={s.ref}
                    type={s.isSetpoint ? 'stepAfter' : 'monotone'}
                    dataKey={(row: TrendGaugeRow) => row.values[s.ref]?.value ?? null}
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
          </section>
        );
      })}
    </div>
  );
}
