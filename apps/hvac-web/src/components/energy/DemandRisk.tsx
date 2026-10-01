import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Progress } from '@/components/ui/progress';
import { Badge } from '@/components/ui/badge';
import { formatPower, formatPercent } from '@/lib/units/formatter';
import { AlertTriangle, ShieldCheck, Zap } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface DemandRiskProps {
  readonly currentDemand: number; // in kW
  readonly quotaLimit: number; // in kW (申报需量 / 变压器容量限制)
  readonly predictedPeak?: number; // in kW
  readonly predictedTime?: string;
  readonly className?: string;
}

export function DemandRisk({
  currentDemand,
  quotaLimit,
  predictedPeak,
  predictedTime = '14:30 ~ 15:30',
  className,
}: DemandRiskProps) {
  const ratio = quotaLimit > 0 ? currentDemand / quotaLimit : 0;
  const percentage = Math.min(100, Math.round(ratio * 100));

  const isCritical = ratio >= 0.95;
  const isWarning = ratio >= 0.85 && ratio < 0.95;

  const fmtCurrent = formatPower(currentDemand);
  const fmtQuota = formatPower(quotaLimit);
  const fmtPredicted = predictedPeak ? formatPower(predictedPeak) : undefined;

  return (
    <Card className={cn('overflow-hidden border-border/80 bg-card shadow-xs', className)}>
      <CardHeader className="flex flex-row items-center justify-between p-4 pb-2">
        <div className="space-y-0.5">
          <CardTitle className="text-sm font-semibold tracking-tight">
            最大需量与越限预警
          </CardTitle>
          <div className="text-xs text-muted-foreground">
            月度核定需量限额: {fmtQuota.formatted}
          </div>
        </div>

        {isCritical ? (
          <Badge variant="outline" className="border-rose-300 bg-rose-50 text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-400 gap-1 text-xs">
            <AlertTriangle className="size-3" />
            越限风险
          </Badge>
        ) : isWarning ? (
          <Badge variant="outline" className="border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400 gap-1 text-xs">
            <AlertTriangle className="size-3" />
            接近限额
          </Badge>
        ) : (
          <Badge variant="outline" className="border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400 gap-1 text-xs">
            <ShieldCheck className="size-3" />
            需量安全
          </Badge>
        )}
      </CardHeader>

      <CardContent className="p-4 pt-1 space-y-3">
        <div className="flex items-baseline justify-between">
          <div className="flex items-baseline gap-1.5">
            <span className="text-2xl font-bold tracking-tight text-foreground">
              {fmtCurrent.value}
            </span>
            <span className="text-xs text-muted-foreground">{fmtCurrent.unit}</span>
          </div>
          <div className="text-xs font-mono font-medium">
            已占用 {formatPercent(ratio)}
          </div>
        </div>

        <Progress
          value={percentage}
          className={cn(
            'h-2.5',
            isCritical
              ? '[&>div]:bg-rose-500'
              : isWarning
                ? '[&>div]:bg-amber-500'
                : '[&>div]:bg-emerald-500'
          )}
        />

        <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
          <div className="flex items-center gap-1">
            <Zap className="size-3 text-amber-500" />
            <span>剩余可用空间:</span>
            <span className="font-semibold text-foreground">
              {formatPower(Math.max(0, quotaLimit - currentDemand)).formatted}
            </span>
          </div>

          {fmtPredicted && (
            <div>
              <span>预测日峰值: </span>
              <span className="font-medium text-foreground">{fmtPredicted.formatted}</span>
              <span className="text-[11px] text-muted-foreground ml-1">({predictedTime})</span>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
