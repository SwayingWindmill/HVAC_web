import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { CheckCircle2, SlidersHorizontal } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface BaselineSummaryProps {
  readonly modelType?: string;
  readonly r2: number; // e.g. 0.94
  readonly cvRmse: number; // e.g. 0.082 (8.2%)
  readonly variables?: readonly string[];
  readonly baselinePeriod?: string;
  readonly isCompliant?: boolean;
  readonly className?: string;
}

export function BaselineSummary({
  modelType = '多元线性回归 (MLR)',
  r2,
  cvRmse,
  variables = ['室外干球温度 (CDD/HDD)', '营业时间排班', '客流密度'],
  baselinePeriod = '2025-01-01 ~ 2025-12-31',
  isCompliant = true,
  className,
}: BaselineSummaryProps) {
  const cvRmsePercent = (cvRmse * 100).toFixed(1);

  return (
    <Card className={cn('overflow-hidden border-border/80 shadow-xs', className)}>
      <CardHeader className="flex flex-row items-center justify-between pb-3 p-5">
        <div className="flex items-center gap-2">
          <SlidersHorizontal className="size-4 text-primary" />
          <CardTitle className="text-sm font-semibold tracking-tight">
            基准能耗模型 (Baseline Model)
          </CardTitle>
        </div>
        {isCompliant && (
          <Badge
            variant="outline"
            className="border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400 gap-1 text-[11px]"
          >
            <CheckCircle2 className="size-3" />
            IPMVP Option C 合规
          </Badge>
        )}
      </CardHeader>

      <CardContent className="p-5 pt-0 space-y-3.5 text-xs">
        <div className="grid grid-cols-3 gap-3 rounded-lg bg-muted/40 p-3 text-center">
          <div>
            <div className="text-muted-foreground text-[11px]">判定系数 R²</div>
            <div className="text-base font-bold text-foreground mt-0.5">
              {r2.toFixed(3)}
            </div>
            <span className="text-[10px] text-emerald-600 dark:text-emerald-400">
              {r2 >= 0.75 ? '拟合优良 (≥0.75)' : '拟合不足'}
            </span>
          </div>

          <div>
            <div className="text-muted-foreground text-[11px]">变异系数 CV(RMSE)</div>
            <div className="text-base font-bold text-foreground mt-0.5">
              {cvRmsePercent}%
            </div>
            <span className="text-[10px] text-emerald-600 dark:text-emerald-400">
              {cvRmse <= 0.15 ? '误差达标 (≤15%)' : '超标'}
            </span>
          </div>

          <div>
            <div className="text-muted-foreground text-[11px]">模型算法</div>
            <div className="text-xs font-semibold text-foreground mt-1 truncate">
              {modelType}
            </div>
            <span className="text-[10px] text-muted-foreground">气象与运行关联</span>
          </div>
        </div>

        <div className="space-y-1.5 pt-1">
          <div className="text-muted-foreground">基准训练期: <span className="font-mono text-foreground">{baselinePeriod}</span></div>
          <div className="flex items-center gap-1.5 flex-wrap">
            <span className="text-muted-foreground">自变量特征:</span>
            {variables.map((v) => (
              <span key={v} className="bg-muted px-1.5 py-0.5 rounded text-[11px] text-foreground">
                {v}
              </span>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
