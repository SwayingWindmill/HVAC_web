import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { formatEnergy, formatCurrency, formatPercent } from '@/lib/units/formatter';
import { ShieldCheck, ArrowDownRight } from 'lucide-react';
import { Progress } from '@/components/ui/progress';
import { cn } from '@/lib/utils';

export interface SavingsCardProps {
  readonly baselineKwh: number;
  readonly actualKwh: number;
  readonly costSaved: number;
  readonly standard?: string; // e.g. "IPMVP Option C"
  readonly modelR2?: number; // e.g. 0.94
  readonly periodLabel?: string;
  readonly className?: string;
}

export function SavingsCard({
  baselineKwh,
  actualKwh,
  costSaved,
  standard = 'IPMVP Option C',
  modelR2 = 0.93,
  periodLabel = '本统计周期',
  className,
}: SavingsCardProps) {
  const energySaved = Math.max(0, baselineKwh - actualKwh);
  const savingsRate = baselineKwh > 0 ? energySaved / baselineKwh : 0;
  const actualRatio = baselineKwh > 0 ? Math.min(100, Math.round((actualKwh / baselineKwh) * 100)) : 100;

  const fmtSaved = formatEnergy(energySaved);
  const fmtCost = formatCurrency(costSaved);
  const fmtBaseline = formatEnergy(baselineKwh);
  const fmtActual = formatEnergy(actualKwh);

  return (
    <Card className={cn('overflow-hidden border-border/80 bg-card shadow-xs', className)}>
      <CardHeader className="flex flex-row items-center justify-between p-4 pb-2">
        <div className="space-y-0.5">
          <div className="flex items-center gap-2">
            <CardTitle className="text-sm font-semibold tracking-tight">
              实测节能验证 (M&V)
            </CardTitle>
            <Badge
              variant="outline"
              className="text-[10px] font-normal gap-1 px-1.5 py-0 border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400"
            >
              <ShieldCheck className="size-3" />
              {standard}
            </Badge>
          </div>
          <p className="text-xs text-muted-foreground">{periodLabel}</p>
        </div>

        <div className="text-right">
          <div className="text-xs text-muted-foreground">基线模型精度</div>
          <div className="text-xs font-mono font-medium text-foreground">
            R² = {modelR2.toFixed(2)}
          </div>
        </div>
      </CardHeader>

      <CardContent className="p-4 pt-1 space-y-3">
        <div className="grid grid-cols-2 gap-4 rounded-lg bg-muted/40 p-3.5">
          <div>
            <div className="text-xs text-muted-foreground">经调整基线能耗</div>
            <div className="text-base font-semibold text-foreground mt-0.5">
              {fmtBaseline.formatted}
            </div>
          </div>
          <div>
            <div className="text-xs text-muted-foreground">实际运行能耗</div>
            <div className="text-base font-semibold text-foreground mt-0.5">
              {fmtActual.formatted}
            </div>
          </div>
        </div>

        <div className="space-y-2">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted-foreground">实际能耗占比</span>
            <span className="font-mono font-medium">{actualRatio}%</span>
          </div>
          <Progress value={actualRatio} className="h-2" />
        </div>

        <div className="flex items-center justify-between pt-2 border-t border-border/40">
          <div className="flex items-center gap-1.5">
            <div className="flex size-7 items-center justify-center rounded-full bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400">
              <ArrowDownRight className="size-4" />
            </div>
            <div>
              <div className="text-[11px] text-muted-foreground">节能率</div>
              <div className="text-sm font-bold text-emerald-600 dark:text-emerald-400">
                {formatPercent(savingsRate)}
              </div>
            </div>
          </div>

          <div className="text-right">
            <div className="text-[11px] text-muted-foreground">节约费用 (Cost Avoidance)</div>
            <div className="text-sm font-bold text-foreground">
              {fmtCost.formatted}
              <span className="text-xs font-normal text-muted-foreground ml-1">
                ({fmtSaved.formatted})
              </span>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
