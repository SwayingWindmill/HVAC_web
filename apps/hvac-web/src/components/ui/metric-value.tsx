"use client"

import * as React from "react"

import { cn } from "@/lib/utils"
import { formatNumber, type NumberFormat } from "@/lib/format"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

/** Values at or above this are abbreviated by default. */
export const DEFAULT_COMPACT_FROM = 100_000

export interface MetricValueProps extends Omit<React.ComponentProps<"span">, "children"> {
  /** Strings render as-is. */
  value: number | string
  format?: NumberFormat
  currency?: string
  maximumFractionDigits?: number
  /** Values at or above this are abbreviated (e.g. $158K) with the full value in a tooltip. Set to Infinity to always show the full value. */
  compactFrom?: number
  /** Text after the number, e.g. a unit. */
  suffix?: React.ReactNode
  /** Set false when rendered inside an outer interactive button/tab to avoid nested focus stops. */
  focusableTooltip?: boolean
}

/** A formatted number. Large values are abbreviated and reveal the full value on hover. */
function MetricValue({
  value,
  format,
  currency,
  maximumFractionDigits,
  compactFrom = DEFAULT_COMPACT_FROM,
  suffix,
  focusableTooltip = true,
  className,
  ...props
}: MetricValueProps) {
  const classes = cn("tabular-nums", className)

  if (typeof value !== "number") {
    return (
      <span data-slot="metric-value" className={classes} {...props}>
        {value}
        {suffix}
      </span>
    )
  }

  const full = formatNumber(value, { format, currency, maximumFractionDigits })
  const abbreviate = format !== "percent" && Math.abs(value) >= compactFrom
  if (!abbreviate) {
    return (
      <span data-slot="metric-value" className={classes} {...props}>
        {full}
        {suffix}
      </span>
    )
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          data-slot="metric-value"
          data-compact=""
          tabIndex={focusableTooltip ? 0 : undefined}
          className={cn(classes, focusableTooltip && "cursor-default rounded-xs outline-none focus-visible:ring-3 focus-visible:ring-ring-soft")}
          {...props}
        >
          {formatNumber(value, { format, currency, compact: true })}
          {suffix}
        </span>
      </TooltipTrigger>
      <TooltipContent className="tabular-nums">
        {full}
        {suffix}
      </TooltipContent>
    </Tooltip>
  )
}

export { MetricValue }
