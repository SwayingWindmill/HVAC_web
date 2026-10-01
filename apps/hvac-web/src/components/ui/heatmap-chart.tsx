import * as React from 'react';

import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

export interface HeatmapRow {
  readonly label: string;
  readonly values: readonly number[];
}

export interface HeatmapDatum {
  readonly row: number;
  readonly column: number;
  readonly rowLabel: string;
  readonly columnLabel: string;
  readonly value: number;
}

interface HeatmapChartProps extends React.ComponentProps<'div'> {
  readonly rows: readonly HeatmapRow[];
  readonly columns: readonly string[];
  readonly max?: number;
  readonly levels?: number;
  readonly columnLabelEvery?: number;
  readonly valueFormatter?: (value: number) => string;
  readonly unit?: string;
  readonly renderTooltip?: (datum: HeatmapDatum) => React.ReactNode;
  readonly onCellClick?: (datum: HeatmapDatum) => void;
}

function fillFor(fraction: number, levels: number): string {
  if (fraction <= 0) return 'var(--muted)';
  const step = Math.max(1, Math.ceil(fraction * (levels - 1))) / (levels - 1);
  const percentage = Math.round(12 + step * 88);
  return `color-mix(in oklab, var(--chart-1) ${percentage}%, var(--muted))`;
}

export function HeatmapChart({
  rows,
  columns,
  max,
  levels = 5,
  columnLabelEvery,
  valueFormatter = (value) => new Intl.NumberFormat('zh-CN').format(value),
  unit,
  renderTooltip,
  onCellClick,
  className,
  ...props
}: HeatmapChartProps) {
  const [activeCell, setActiveCell] = React.useState<{ row: number; column: number } | null>(null);
  const top = max ?? rows.reduce(
    (rowMaximum, row) => Math.max(rowMaximum, ...row.values, 0),
    0,
  );
  const every = columnLabelEvery ?? Math.max(1, Math.ceil(columns.length / 12));

  return (
    <div data-slot="heatmap-chart" className={cn('flex min-w-0 flex-col gap-2 text-xs', className)} {...props}>
      <TooltipProvider delayDuration={100} skipDelayDuration={0}>
        <div
          role="group"
          aria-label="告警发生热力图"
          className="grid min-w-0"
          style={{ gap: 4, gridTemplateColumns: `auto repeat(${columns.length}, minmax(0, 1fr))` }}
          onPointerLeave={() => setActiveCell(null)}
        >
          {rows.map((row, rowIndex) => (
            <React.Fragment key={row.label}>
              <div
                data-active={activeCell?.row === rowIndex}
                className="flex items-center pr-1.5 whitespace-nowrap leading-none text-muted-foreground transition-colors data-[active=true]:text-foreground"
              >
                {row.label}
              </div>
              {columns.map((column, columnIndex) => {
                const value = row.values[columnIndex] ?? 0;
                const datum: HeatmapDatum = {
                  row: rowIndex,
                  column: columnIndex,
                  rowLabel: row.label,
                  columnLabel: column,
                  value,
                };
                const body = renderTooltip?.(datum) ?? (
                  <>
                    <span className="font-medium tabular-nums">
                      {valueFormatter(value)}{unit ? ` ${unit}` : ''}
                    </span>
                    <span className="block text-background/70">{row.label} · {column}</span>
                  </>
                );
                return (
                  <Tooltip key={column}>
                    <TooltipTrigger asChild>
                      <button
                        type="button"
                        aria-label={`${row.label} ${column}：${valueFormatter(value)}${unit ?? ''}`}
                        onPointerEnter={() => setActiveCell({ row: rowIndex, column: columnIndex })}
                        onFocus={() => setActiveCell({ row: rowIndex, column: columnIndex })}
                        onBlur={() => setActiveCell(null)}
                        onClick={onCellClick ? () => onCellClick(datum) : undefined}
                        className="aspect-square w-full rounded-sm outline-none inset-ring inset-ring-foreground/6 transition-[box-shadow] focus-visible:inset-ring-2 focus-visible:inset-ring-ring"
                        style={{
                          backgroundColor: fillFor(top > 0 ? Math.sqrt(Math.min(1, value / top)) : 0, levels),
                        }}
                      />
                    </TooltipTrigger>
                    <TooltipContent sideOffset={4} className="text-left">
                      {body}
                    </TooltipContent>
                  </Tooltip>
                );
              })}
            </React.Fragment>
          ))}
          <div aria-hidden />
          {columns.map((column, columnIndex) => (
            <div
              key={column}
              data-active={activeCell?.column === columnIndex}
              className="min-w-0 pt-0.5 text-center leading-none whitespace-nowrap text-muted-foreground transition-colors data-[active=true]:text-foreground"
            >
              {columnIndex % every === 0 ? column : ''}
            </div>
          ))}
        </div>
      </TooltipProvider>
      <div className="flex items-center justify-end gap-1 text-muted-foreground">
        <span className="mr-0.5">少</span>
        {Array.from({ length: levels }, (_, index) => (
          <span
            key={index}
            className="size-3 rounded-[3px] inset-ring inset-ring-foreground/6"
            style={{ backgroundColor: fillFor(index / Math.max(1, levels - 1), levels) }}
          />
        ))}
        <span className="ml-0.5">多</span>
      </div>
    </div>
  );
}
