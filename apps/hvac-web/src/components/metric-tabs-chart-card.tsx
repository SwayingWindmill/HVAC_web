"use client"

import * as React from "react"

import { cn } from "@/lib/utils"
import {
  computeDelta,
  formatNumber,
  type NumberFormat,
} from "@/lib/format"
import { Card, CardContent } from "@/components/ui/card"
import { ComposedChart, type ComposedChartProps } from "@/components/ui/composed-chart"
import { DeltaBadge } from "@/components/ui/delta-badge"
import { MetricValue } from "@/components/ui/metric-value"

export interface MetricTab {
  /** Key of this metric in each data row. */
  key: string
  label: string
  /** Key of the previous-period series in each row, drawn as a dashed line. */
  compareKey?: string
  /** Headline value. Defaults to the aggregate of the metric's series; null reads as unknown. */
  value?: number | string | null
  /** Fractional change. Defaults to the aggregate vs the compare series; null hides it. */
  delta?: number | null
  /** Unit after the headline value, e.g. kWh. */
  unit?: string
  /** How to roll the series up into the headline value. */
  aggregate?: "sum" | "average" | "last"
  format?: NumberFormat
  currency?: string
  fractionDigits?: number
  /** Treat a decrease as good and an increase as bad (spend, cost, churn). */
  invertDelta?: boolean
  /** The change is neither good nor bad. */
  neutralDelta?: boolean
}

export interface MetricTabsChartCardProps extends React.ComponentProps<typeof Card> {
  metrics: MetricTab[]
  data: Record<string, unknown>[]
  xKey: string
  /** Controlled selected metric key. */
  selected?: string
  defaultSelected?: string
  onSelectedChange?: (key: string) => void
  /** Line color for the selected metric. */
  color?: string
  /** Line color for the previous-period series. */
  compareColor?: string
  /** Tooltip label for the previous-period series. */
  compareLabel?: string
  xFormatter?: ComposedChartProps["xFormatter"]
  /** Draw a y-axis with grid lines, for readings an operator compares against a scale. */
  showYAxis?: boolean
  showLegend?: boolean
  /** "zero" starts the axis at 0, for magnitudes such as energy. */
  yDomain?: ComposedChartProps["yDomain"]
  /** Mark every point of the selected series, so an hour between gaps still shows. */
  dots?: boolean
  /** Shown in place of the chart when there are no rows. */
  empty?: React.ReactNode
  chartClassName?: string
}

function aggregate(
  data: Record<string, unknown>[],
  key: string,
  mode: MetricTab["aggregate"] = "sum"
): number | null {
  const values = data
    .map((row) => row[key])
    .filter((value): value is number => typeof value === "number" && Number.isFinite(value))
  if (!values.length) return null
  if (mode === "last") return values[values.length - 1]!
  const total = values.reduce((sum, value) => sum + value, 0)
  return mode === "average" ? total / values.length : total
}

/** A card with a row of metric tabs, each with a value and delta, and a line chart of the selected metric against the previous period. */
function MetricTabsChartCard({
  metrics,
  data,
  xKey,
  selected: selectedProp,
  defaultSelected,
  onSelectedChange,
  color = "var(--chart-2)",
  compareColor = "var(--chart-1)",
  compareLabel = "上一期",
  xFormatter,
  showYAxis = false,
  showLegend = false,
  yDomain = "auto",
  dots = false,
  empty,
  chartClassName,
  className,
  ...props
}: MetricTabsChartCardProps) {
  const id = React.useId()
  const [selectedState, setSelectedState] = React.useState(
    defaultSelected ?? metrics[0]?.key
  )
  const selectedKey = selectedProp ?? selectedState
  const active = metrics.find((metric) => metric.key === selectedKey) ?? metrics[0]

  const select = (key: string) => {
    setSelectedState(key)
    onSelectedChange?.(key)
  }

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return
    event.preventDefault()
    const index = metrics.findIndex((metric) => metric.key === active?.key)
    const step = event.key === "ArrowRight" ? 1 : -1
    const next = metrics[(index + step + metrics.length) % metrics.length]
    if (!next) return
    select(next.key)
    document.getElementById(`${id}-tab-${next.key}`)?.focus()
  }

  const yFormatter = React.useCallback(
    (value: number) =>
      formatNumber(value, {
        format: active?.format,
        currency: active?.currency,
        maximumFractionDigits: active?.fractionDigits,
      }),
    [active?.format, active?.currency, active?.fractionDigits]
  )

  if (!active) return null

  return (
    <Card
      data-slot="metric-tabs-chart-card"
      className={cn("gap-0 overflow-hidden py-0", className)}
      {...props}
    >
      <div
        role="tablist"
        aria-label="指标"
        onKeyDown={onKeyDown}
        className="flex divide-x overflow-x-auto border-b [scrollbar-width:thin] [scrollbar-color:color-mix(in_oklab,var(--foreground)_20%,transparent)_transparent]"
      >
        {metrics.map((metric) => {
          const isSelected = metric.key === active.key
          const current = aggregate(data, metric.key, metric.aggregate)
          const value = metric.value !== undefined ? metric.value : current
          const previous = metric.compareKey ? aggregate(data, metric.compareKey, metric.aggregate) : null
          const delta =
            metric.delta !== undefined
              ? metric.delta
              : current !== null && previous !== null
                ? computeDelta(current, previous)
                : null
          return (
            <button
              key={metric.key}
              id={`${id}-tab-${metric.key}`}
              type="button"
              role="tab"
              aria-selected={isSelected}
              aria-controls={`${id}-panel`}
              tabIndex={isSelected ? 0 : -1}
              data-slot="metric-tab"
              data-state={isSelected ? "active" : "inactive"}
              onClick={() => select(metric.key)}
              className={cn(
                "flex flex-[1_0_auto] flex-col gap-1.5 px-4 py-3 text-left outline-none transition-colors",
                "focus-visible:ring-ring/50 focus-visible:ring-2 focus-visible:ring-inset",
                isSelected
                  ? "bg-muted"
                  : "hover:bg-muted/50"
              )}
            >
              <span className="text-muted-foreground text-sm whitespace-nowrap">{metric.label}</span>
              <span className="flex items-center justify-between gap-3">
                <MetricValue
                  value={value ?? "—"}
                  format={metric.format}
                  currency={metric.currency}
                  maximumFractionDigits={metric.fractionDigits}
                  suffix={metric.unit && value !== null ? <span className="text-muted-foreground ml-1 text-xs font-normal">{metric.unit}</span> : undefined}
                  className="text-lg font-semibold tracking-tight"
                />
                {delta !== null ? (
                  <DeltaBadge
                    delta={delta}
                    invert={metric.invertDelta}
                    neutral={metric.neutralDelta}
                    variant="text"
                    showIcon={false}
                  />
                ) : null}
              </span>
            </button>
          )
        })}
      </div>
      <CardContent id={`${id}-panel`} role="tabpanel" className="pt-4 pb-4">
        {data.length === 0 && empty ? (
          <div className={cn("text-muted-foreground grid h-40 place-items-center text-sm", chartClassName)}>{empty}</div>
        ) : (
        <ComposedChart
          data={data}
          xKey={xKey}
          series={[
            ...(active.compareKey
              ? [
                  {
                    key: active.compareKey,
                    label: compareLabel,
                    type: "line" as const,
                    color: compareColor,
                    dashed: true,
                  },
                ]
              : []),
            { key: active.key, label: active.label, type: "area" as const, color, dots },
          ]}
          showGrid={showYAxis}
          showYAxis={showYAxis}
          showLegend={showLegend}
          yDomain={yDomain}
          xFormatter={xFormatter}
          yFormatter={yFormatter}
          className={cn("h-40", chartClassName)}
        />
        )}
      </CardContent>
    </Card>
  )
}

export { MetricTabsChartCard }
