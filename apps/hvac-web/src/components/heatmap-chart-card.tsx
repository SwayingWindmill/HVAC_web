import * as React from "react"

import { cn } from "@/lib/utils"
import { type NumberFormat } from "@/lib/format"
import { Card, CardContent } from "@/components/ui/card"
import { DeltaBadge } from "@/components/ui/delta-badge"
import {
  HeatmapChart,
  type HeatmapCell,
  type HeatmapChartProps,
} from "@/components/ui/heatmap-chart"
import { MetricValue } from "@/components/ui/metric-value"
import { PeriodTabs, type PeriodOption } from "@/components/ui/period-tabs"

export interface HeatmapChartCardProps
  extends Omit<React.ComponentProps<typeof Card>, "children">,
    Pick<
      HeatmapChartProps,
      "rows" | "columns" | "color" | "max" | "scale" | "unit" | "valueFormatter" | "columnLabelEvery"
    > {
  title: string
  /** Headline value. Defaults to the sum of every cell. Strings render as-is. */
  value?: number | string
  /** Fractional change, e.g. 0.052 for +5.2%. */
  delta?: number
  /** Context under the headline, e.g. "vs previous 7 days". */
  deltaLabel?: string
  /** Treat a decrease as good (churn, latency, errors). */
  invertDelta?: boolean
  format?: NumberFormat
  currency?: string
  /** Period options for the switcher. Omit to hide it. */
  periods?: PeriodOption[]
  period?: string
  defaultPeriod?: string
  onPeriodChange?: (period: string) => void
  chartClassName?: string
}

/**
 * A card with a headline total, a delta, an optional period switcher, and a matrix heatmap.
 * Hovering a cell swaps the headline to that cell's value and names the cell under it.
 */
function HeatmapChartCard({
  title,
  value,
  delta,
  deltaLabel,
  invertDelta,
  format,
  currency,
  unit,
  valueFormatter,
  rows,
  columns,
  columnLabelEvery,
  color,
  max,
  scale,
  periods,
  period,
  defaultPeriod,
  onPeriodChange,
  chartClassName,
  className,
  ...props
}: HeatmapChartCardProps) {
  const [active, setActive] = React.useState<HeatmapCell | null>(null)

  // Null when no cell has a reading, so an empty grid does not read as zero.
  const total = React.useMemo(() => {
    const known = rows.flatMap((row) => row.values.filter((v): v is number => v != null))
    return known.length ? known.reduce((sum, v) => sum + v, 0) : null
  }, [rows])

  const hoveredRow = active ? rows[active.row] : undefined
  const hovered =
    active && hoveredRow && hoveredRow.values[active.column] != null
      ? {
          value: hoveredRow.values[active.column] ?? 0,
          label: `${hoveredRow.label} · ${columns[active.column] ?? ""}`,
        }
      : null

  return (
    <Card data-slot="heatmap-chart-card" className={cn("gap-0 py-0", className)} {...props}>
      <div className="flex h-12 items-center gap-3 border-b border-divider px-4">
        <h2 className="text-sm font-semibold whitespace-nowrap">{title}</h2>
        <MetricValue
          value={hovered ? hovered.value : (value ?? total ?? "—")}
          format={format}
          currency={currency}
          suffix={unit && (hovered || value !== undefined || total !== null) ? <span className="text-subtle-foreground ml-1 text-xs font-medium">{unit}</span> : undefined}
          className="text-sm font-semibold tabular-nums"
        />
        {delta !== undefined && !hovered ? <DeltaBadge delta={delta} invert={invertDelta} /> : null}
        {/* The caption names the hovered cell; it keeps its place so the chart does not shift. */}
        <span data-slot="heatmap-chart-card-caption" className="text-muted-foreground min-w-0 truncate text-xs">
          {hovered ? hovered.label : deltaLabel}
        </span>
        {periods?.length ? (
          <div className="ml-auto">
            <PeriodTabs
              options={periods}
              value={period}
              defaultValue={defaultPeriod ?? periods[0]?.value}
              onValueChange={onPeriodChange}
            />
          </div>
        ) : null}
      </div>
      <CardContent className="px-4 pt-3 pb-4">
        <HeatmapChart
          rows={rows}
          columns={columns}
          columnLabelEvery={columnLabelEvery}
          color={color}
          max={max}
          scale={scale}
          unit={unit}
          valueFormatter={valueFormatter}
          activeCell={active}
          onActiveCellChange={setActive}
          className={chartClassName}
        />
      </CardContent>
    </Card>
  )
}

export { HeatmapChartCard }
