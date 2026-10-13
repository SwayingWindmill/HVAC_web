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

export interface MetricTabsChartCardProps extends Omit<React.ComponentProps<typeof Card>, "title"> {
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
  /** Panel title bar above the tabs: what the card is, a short caption and one way onward. */
  title?: React.ReactNode
  caption?: React.ReactNode
  action?: React.ReactNode
  /** A line above the chart naming what it plots. */
  chartTitle?: React.ReactNode
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
  title,
  caption,
  action,
  chartTitle,
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
      {title ? (
        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-divider px-4">
          <h2 className="text-sm font-semibold whitespace-nowrap">{title}</h2>
          {caption ? <span className="text-muted-foreground min-w-0 truncate text-xs">{caption}</span> : null}
          {action ? <div className="ml-auto flex items-center gap-2">{action}</div> : null}
        </div>
      ) : null}
      <div
        role="tablist"
        aria-label="指标"
        onKeyDown={onKeyDown}
        className="flex divide-x divide-divider overflow-x-auto border-b border-divider [scrollbar-width:thin]"
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
                "flex min-w-0 flex-[1_0_0] flex-col gap-1 px-[18px] pt-[13px] pb-3 text-left shadow-[inset_0_-2px_0_transparent] outline-none transition-[background-color,box-shadow] duration-100",
                "focus-visible:ring-ring-soft focus-visible:ring-3 focus-visible:ring-inset",
                isSelected
                  ? "shadow-[inset_0_-2px_0_var(--primary)]"
                  : "hover:bg-accent/60"
              )}
            >
              <span className={cn("text-xs font-medium whitespace-nowrap", isSelected ? "text-primary" : "text-muted-foreground")}>{metric.label}</span>
              <span className="flex items-center justify-between gap-3">
                <MetricValue
                  value={value ?? "—"}
                  format={metric.format}
                  currency={metric.currency}
                  maximumFractionDigits={metric.fractionDigits}
                  suffix={metric.unit && value !== null ? <span className="text-subtle-foreground ml-1 text-xs font-medium">{metric.unit}</span> : undefined}
                  className="text-2xl leading-[29px] font-semibold tracking-[-0.03em]"
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
      <CardContent id={`${id}-panel`} role="tabpanel" className="flex flex-col gap-2 px-5 pt-3.5 pb-3">
        {chartTitle ? <div className="flex min-h-7 items-center gap-2.5 text-sm">{chartTitle}</div> : null}
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
