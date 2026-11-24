"use client"

import * as React from "react"

import { cn } from "@/lib/utils"
import { formatNumber } from "@/lib/format"

export interface HeatmapRow {
  label: string
  /** One value per column, in column order. Null marks a cell with no reading yet (drawn empty, not as 0). */
  values: readonly (number | null)[]
}

/** A cell's position by row and column index. */
export interface HeatmapCell {
  row: number
  column: number
}

export interface HeatmapDatum extends HeatmapCell {
  rowLabel: string
  columnLabel: string
  value: number
}

export interface HeatmapChartProps extends React.ComponentProps<"div"> {
  /** One row per line of the matrix, top to bottom. */
  rows: HeatmapRow[]
  /** Column labels, left to right. Sets the number of columns. */
  columns: string[]
  /** Any CSS color. Cells mix this into `--muted` by value. Defaults to chart-1. */
  color?: string
  /** Value drawn fully saturated. Defaults to the largest value. */
  max?: number
  /** How values map to color. `sqrt` spreads out small values. */
  scale?: "linear" | "sqrt"
  /** Quantize the fill into this many steps instead of a continuous ramp. */
  levels?: number
  /** Gap between cells in px. Cells themselves stretch to fill the container. */
  gap?: number
  valueFormatter?: (value: number) => string
  /** Label for the value in the tooltip, e.g. "sessions". */
  unit?: string
  showLegend?: boolean
  showRowLabels?: boolean
  showColumnLabels?: boolean
  /** Show every nth column label. Defaults to whatever keeps about 12 labels. */
  columnLabelEvery?: number
  /** Replace the tooltip body. Return `null` to hide it for that cell. */
  renderTooltip?: (datum: HeatmapDatum) => React.ReactNode
  onCellClick?: (datum: HeatmapDatum) => void
  /** The hovered cell, to control it from outside. */
  activeCell?: HeatmapCell | null
  /** Called when the hovered cell changes, with null when the pointer leaves. */
  onActiveCellChange?: (cell: HeatmapCell | null) => void
}

/** Where a value sits between the muted track (0) and the accent (1). */
function fractionFor(
  value: number,
  max: number,
  scale: "linear" | "sqrt",
  levels?: number
) {
  if (value <= 0 || max <= 0) return 0
  let t = Math.min(1, value / max)
  if (scale === "sqrt") t = Math.sqrt(t)
  if (levels && levels > 1) {
    t = Math.max(1, Math.ceil(t * (levels - 1))) / (levels - 1)
  }
  return t
}

function fillFor(color: string, fraction: number) {
  if (fraction <= 0) return "var(--muted)"
  // Floor the mix so the smallest non-zero values still read against the track.
  const pct = Math.round(12 + fraction * 88)
  return `color-mix(in oklab, ${color} ${pct}%, var(--muted))`
}

/**
 * A matrix heatmap with a row per line and a column per label, e.g. weekday by hour.
 * Cells stretch to the container width. Hovering a cell rings it, lights up its row
 * and column labels, and shows its value via a single delegated floating tooltip.
 */
const HeatmapChart = React.memo(function HeatmapChart({
  rows,
  columns,
  color = "var(--chart-1)",
  max,
  scale = "linear",
  levels,
  gap = 4,
  valueFormatter = (value) => formatNumber(value),
  unit,
  showLegend = true,
  showRowLabels = true,
  showColumnLabels = true,
  columnLabelEvery,
  renderTooltip,
  onCellClick,
  activeCell,
  onActiveCellChange,
  className,
  ...props
}: HeatmapChartProps) {
  const gridRef = React.useRef<HTMLDivElement | null>(null)
  const [internal, setInternal] = React.useState<HeatmapCell | null>(null)
  const [tooltipPos, setTooltipPos] = React.useState<{
    readonly left: number
    readonly top: number
    readonly align: "center" | "start" | "end"
  } | null>(null)
  const active = activeCell === undefined ? internal : activeCell
  const setActive = React.useCallback(
    (cell: HeatmapCell | null) => {
      setInternal(cell)
      onActiveCellChange?.(cell)
    },
    [onActiveCellChange]
  )

  const top = React.useMemo(
    () =>
      max ??
      rows.reduce(
        (acc, row) => row.values.reduce<number>((m, v) => Math.max(m, v ?? 0), acc),
        0
      ),
    [rows, max]
  )

  const every = columnLabelEvery ?? Math.max(1, Math.ceil(columns.length / 12))
  const steps = levels && levels > 1 ? levels : 5
  const swatches = React.useMemo(
    () => Array.from({ length: steps }, (_, i) => fillFor(color, i / (steps - 1))),
    [color, steps]
  )

  const withUnit = React.useCallback(
    (value: number) => (unit ? `${valueFormatter(value)} ${unit}` : valueFormatter(value)),
    [unit, valueFormatter]
  )

  React.useLayoutEffect(() => {
    const grid = gridRef.current
    if (!active || !grid) {
      setTooltipPos(null)
      return
    }
    const reading = rows[active.row]?.values[active.column]
    if (reading === null || reading === undefined) {
      setTooltipPos(null)
      return
    }
    const cell = grid.querySelector<HTMLElement>(
      `[data-slot="heatmap-chart-cell"][data-row="${active.row}"][data-column="${active.column}"]`
    )
    if (!cell) {
      setTooltipPos(null)
      return
    }
    const centerX = cell.offsetLeft + cell.offsetWidth / 2
    const width = grid.clientWidth
    const align = centerX < 64 ? "start" : width - centerX < 64 ? "end" : "center"
    const left =
      align === "start"
        ? cell.offsetLeft
        : align === "end"
          ? cell.offsetLeft + cell.offsetWidth
          : centerX
    setTooltipPos({ left, top: cell.offsetTop - 4, align })
  }, [active, rows])

  const readCellPosition = (target: EventTarget | null): HeatmapCell | null => {
    if (!(target instanceof HTMLElement)) return null
    const cell = target.closest<HTMLElement>('[data-slot="heatmap-chart-cell"]')
    if (!cell) return null
    const row = Number(cell.dataset.row)
    const column = Number(cell.dataset.column)
    return Number.isInteger(row) && Number.isInteger(column) ? { row, column } : null
  }

  const handlePointerOver = (event: React.PointerEvent<HTMLDivElement>) => {
    const cell = readCellPosition(event.target)
    if (!cell) {
      if (
        active !== null &&
        event.target instanceof HTMLElement &&
        event.target.closest('[data-slot="heatmap-chart-row-label"], [data-slot="heatmap-chart-column-label"]')
      ) {
        setActive(null)
      }
      return
    }
    if (active?.row === cell.row && active.column === cell.column) return
    setActive(cell)
  }

  const handleFocus = (event: React.FocusEvent<HTMLDivElement>) => {
    const cell = readCellPosition(event.target)
    if (cell) setActive(cell)
  }

  const handleBlur = (event: React.FocusEvent<HTMLDivElement>) => {
    const next = event.relatedTarget
    if (
      next instanceof HTMLElement &&
      next.dataset.slot === "heatmap-chart-cell" &&
      gridRef.current?.contains(next)
    ) {
      return
    }
    setActive(null)
  }

  const handleClick = (event: React.MouseEvent<HTMLDivElement>) => {
    if (!onCellClick) return
    const cell = readCellPosition(event.target)
    if (!cell) return
    const row = rows[cell.row]
    const columnLabel = columns[cell.column]
    const value = row?.values[cell.column]
    if (!row || columnLabel === undefined || value === null || value === undefined) return
    onCellClick({
      row: cell.row,
      column: cell.column,
      rowLabel: row.label,
      columnLabel,
      value,
    })
  }

  let tooltipBody: React.ReactNode = null
  if (active) {
    const activeRow = rows[active.row]
    const activeColumn = columns[active.column]
    const activeValue = activeRow?.values[active.column]
    if (activeRow && activeColumn !== undefined && activeValue !== null && activeValue !== undefined) {
      const datum: HeatmapDatum = {
        row: active.row,
        column: active.column,
        rowLabel: activeRow.label,
        columnLabel: activeColumn,
        value: activeValue,
      }
      tooltipBody = renderTooltip ? (
        renderTooltip(datum)
      ) : (
        <>
          <span className="font-medium tabular-nums">{withUnit(activeValue)}</span>
          <span className="text-background/70 block">
            {activeRow.label} · {activeColumn}
          </span>
        </>
      )
    }
  }

  return (
    <div
      data-slot="heatmap-chart"
      className={cn("flex min-w-0 flex-col gap-2 text-xs", className)}
      {...props}
    >
      <div
        ref={gridRef}
        role="group"
        aria-label="热力图"
        className="relative grid min-w-0"
        style={{
          gap,
          gridTemplateColumns: `${showRowLabels ? "auto " : ""}repeat(${columns.length}, minmax(0, 1fr))`,
        }}
        onPointerOver={handlePointerOver}
        onPointerLeave={() => setActive(null)}
        onFocus={handleFocus}
        onBlur={handleBlur}
        onClick={handleClick}
      >
        {rows.map((row, r) => (
          <React.Fragment key={r}>
            {showRowLabels ? (
              <div
                data-slot="heatmap-chart-row-label"
                data-active={active?.row === r}
                className="text-muted-foreground data-[active=true]:text-foreground flex items-center pr-1.5 leading-none whitespace-nowrap transition-colors"
              >
                {row.label}
              </div>
            ) : null}
            {columns.map((column, c) => {
              const reading = row.values[c]
              if (reading === null || reading === undefined) {
                const isEmptyActive = active?.row === r && active?.column === c
                return (
                  <div
                    key={c}
                    data-slot="heatmap-chart-cell"
                    data-row={r}
                    data-column={c}
                    data-empty="true"
                    data-active={isEmptyActive}
                    aria-hidden="true"
                    className="aspect-square w-full rounded-sm ring-1 ring-inset ring-border/60 data-[active=true]:ring-foreground/35"
                  />
                )
              }
              const value = reading
              const isActive = active?.row === r && active?.column === c
              return (
                <button
                  key={c}
                  type="button"
                  aria-label={`${withUnit(value)}, ${row.label} ${column}`}
                  data-slot="heatmap-chart-cell"
                  data-row={r}
                  data-column={c}
                  data-active={isActive}
                  className={cn(
                    "inset-ring-foreground/6 aspect-square w-full rounded-sm outline-none inset-ring transition-[box-shadow] duration-100",
                    "data-[active=true]:inset-ring-foreground/45 data-[active=true]:inset-ring-2",
                    "focus-visible:inset-ring-ring focus-visible:inset-ring-2",
                    onCellClick ? "cursor-pointer" : "cursor-default"
                  )}
                  style={{
                    backgroundColor: fillFor(
                      color,
                      fractionFor(value, top, scale, levels)
                    ),
                  }}
                />
              )
            })}
          </React.Fragment>
        ))}

        {showColumnLabels ? (
          <>
            {showRowLabels ? <div aria-hidden /> : null}
            {columns.map((column, c) => (
              <div
                key={c}
                data-slot="heatmap-chart-column-label"
                data-active={active?.column === c}
                className="text-muted-foreground data-[active=true]:text-foreground min-w-0 pt-0.5 text-center leading-none whitespace-nowrap transition-colors"
              >
                {c % every === 0 ? column : ""}
              </div>
            ))}
          </>
        ) : null}

        {tooltipBody !== null && tooltipPos ? (
          <div
            role="tooltip"
            data-slot="heatmap-chart-tooltip"
            className="bg-foreground text-background pointer-events-none absolute z-50 w-fit rounded-md px-3 py-1.5 text-left text-xs whitespace-nowrap shadow-md"
            style={{
              left: tooltipPos.left,
              top: tooltipPos.top,
              transform:
                tooltipPos.align === "start"
                  ? "translate(0, -100%)"
                  : tooltipPos.align === "end"
                    ? "translate(-100%, -100%)"
                    : "translate(-50%, -100%)",
            }}
          >
            {tooltipBody}
          </div>
        ) : null}
      </div>

      {showLegend ? (
        <div className="text-muted-foreground flex items-center justify-end gap-1">
          <span className="mr-0.5">少</span>
          {swatches.map((fill, index) => (
            <span
              key={index}
              className="inset-ring-foreground/6 size-3 rounded-[3px] inset-ring"
              style={{ backgroundColor: fill }}
            />
          ))}
          <span className="ml-0.5">多</span>
        </div>
      ) : null}
    </div>
  )
})

export { HeatmapChart }
