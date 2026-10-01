import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { formatEnergy, formatPercent } from '@/lib/units/formatter';
import { cn } from '@/lib/utils';

export interface BreakdownItem {
  readonly id: string;
  readonly label: string;
  readonly kwh: number;
  readonly color: string; // Tailwind class or hex
}

export interface EnergyBreakdownProps {
  readonly title?: string;
  readonly items: readonly BreakdownItem[];
  readonly totalKwh?: number;
  readonly className?: string;
}

export function EnergyBreakdown({
  title = '分项能耗构成',
  items,
  totalKwh: providedTotal,
  className,
}: EnergyBreakdownProps) {
  const calculatedTotal = items.reduce((acc, it) => acc + it.kwh, 0);
  const total = providedTotal ?? calculatedTotal;

  return (
    <Card className={cn('overflow-hidden border-border/80 shadow-xs', className)}>
      <CardHeader className="flex flex-row items-center justify-between pb-3 p-5">
        <CardTitle className="text-sm font-semibold tracking-tight">{title}</CardTitle>
        <span className="text-xs text-muted-foreground">
          总计: <span className="font-semibold text-foreground">{formatEnergy(total).formatted}</span>
        </span>
      </CardHeader>

      <CardContent className="p-5 pt-0 space-y-4">
        {/* Multi-segment distribution bar */}
        <div className="flex h-3 w-full overflow-hidden rounded-full bg-muted/60">
          {items.map((it) => {
            const pct = total > 0 ? (it.kwh / total) * 100 : 0;
            if (pct <= 0) return null;
            return (
              <div
                key={it.id}
                style={{ width: `${pct}%`, backgroundColor: it.color }}
                className="transition-all hover:opacity-90"
                title={`${it.label}: ${formatEnergy(it.kwh).formatted} (${pct.toFixed(1)}%)`}
              />
            );
          })}
        </div>

        {/* Legend list */}
        <div className="grid grid-cols-2 gap-2.5 pt-1 text-xs sm:grid-cols-4">
          {items.map((it) => {
            const ratio = total > 0 ? it.kwh / total : 0;
            return (
              <div key={it.id} className="flex flex-col space-y-0.5">
                <div className="flex items-center gap-1.5">
                  <span
                    className="size-2 rounded-full shrink-0"
                    style={{ backgroundColor: it.color }}
                  />
                  <span className="text-muted-foreground truncate">{it.label}</span>
                </div>
                <div className="flex items-baseline gap-1 pl-3.5">
                  <span className="font-semibold text-foreground">
                    {formatEnergy(it.kwh).formatted}
                  </span>
                  <span className="text-[10px] text-muted-foreground">
                    ({formatPercent(ratio)})
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      </CardContent>
    </Card>
  );
}
