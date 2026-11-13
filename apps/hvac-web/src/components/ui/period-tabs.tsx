import * as React from "react"

import { cn } from "@/lib/utils"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"

export interface PeriodOption<T extends string = string> {
  value: T
  label: string
}

export const defaultPeriods: PeriodOption[] = [
  { value: "week", label: "Week" },
  { value: "month", label: "Month" },
  { value: "year", label: "Year" },
]

export interface PeriodTabsProps<T extends string = string>
  extends Omit<React.ComponentProps<typeof Tabs>, "children" | "value" | "defaultValue" | "onValueChange"> {
  options?: readonly PeriodOption<T>[]
  size?: "sm" | "default"
  /** Slide a single pill between tabs instead of swapping backgrounds. */
  animated?: boolean
  value?: T
  defaultValue?: T
  onValueChange?: (value: T) => void
}

interface IndicatorRect {
  left: number
  top: number
  width: number
  height: number
}

/** Small segmented control for switching a chart's time range. */
function PeriodTabs<T extends string>(
  props: PeriodTabsProps<T> & { options: readonly PeriodOption<T>[] }
): React.JSX.Element
function PeriodTabs(
  props: PeriodTabsProps<string> & { options?: undefined }
): React.JSX.Element
function PeriodTabs({
  options,
  size = "sm",
  animated = false,
  className,
  value,
  defaultValue,
  onValueChange,
  ...props
}: PeriodTabsProps<string>) {
  const renderedOptions: readonly PeriodOption[] = options ?? defaultPeriods
  const listRef = React.useRef<HTMLDivElement>(null)
  const [internalValue, setInternalValue] = React.useState<string | undefined>(
    defaultValue ?? renderedOptions[0]?.value
  )
  const active = value ?? internalValue
  const [rect, setRect] = React.useState<IndicatorRect | null>(null)

  const measure = React.useCallback(() => {
    const list = listRef.current
    if (!list) return
    const trigger = list.querySelector<HTMLElement>('[data-state="active"]')
    if (!trigger) return
    setRect({
      left: trigger.offsetLeft,
      top: trigger.offsetTop,
      width: trigger.offsetWidth,
      height: trigger.offsetHeight,
    })
  }, [])

  React.useLayoutEffect(() => {
    if (!animated) return
    measure()
  }, [animated, active, options, size, measure])

  React.useEffect(() => {
    if (!animated || !listRef.current) return
    const observer = new ResizeObserver(measure)
    observer.observe(listRef.current)
    return () => observer.disconnect()
  }, [animated, measure])

  return (
    <Tabs
      data-slot="period-tabs"
      className={cn("w-fit", className)}
      value={value}
      defaultValue={defaultValue}
      onValueChange={(next) => {
        setInternalValue(next)
        const matched = renderedOptions.find((option) => option.value === next)?.value
        if (matched !== undefined) onValueChange?.(matched)
      }}
      {...props}
    >
      <TabsList
        ref={listRef}
        className={cn(
          "relative",
          size === "sm" && "group-data-[orientation=horizontal]/tabs:h-8 p-1"
        )}
      >
        {animated && rect ? (
          <span
            aria-hidden
            data-slot="period-tabs-indicator"
            className="pointer-events-none absolute rounded-md bg-background shadow-sm transition-[left,top,width,height] duration-300 ease-[cubic-bezier(0.25,1,0.5,1)] motion-reduce:transition-none dark:border dark:border-input dark:bg-input/30"
            style={rect}
          />
        ) : null}
        {renderedOptions.map((option) => (
          <TabsTrigger
            key={option.value}
            value={option.value}
            className={cn(
              size === "sm" && "h-6 px-2.5 text-xs",
              animated &&
                "relative z-10 data-[state=active]:bg-transparent group-data-[variant=default]/tabs-list:data-[state=active]:shadow-none dark:data-[state=active]:border-transparent dark:data-[state=active]:bg-transparent"
            )}
          >
            {option.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  )
}

export { PeriodTabs }
