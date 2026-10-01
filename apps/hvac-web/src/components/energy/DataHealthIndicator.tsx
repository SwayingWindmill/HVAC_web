import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Activity, CheckCircle2, AlertTriangle, Clock } from 'lucide-react';
import { Progress } from '@/components/ui/progress';
import { cn } from '@/lib/utils';

export interface DataHealthIndicatorProps {
  readonly coverageRate: number; // e.g. 0.982 -> 98.2%
  readonly totalMeters?: number;
  readonly normalCount?: number;
  readonly missingCount?: number;
  readonly latencyMs?: number;
  readonly className?: string;
}

export function DataHealthIndicator({
  coverageRate,
  totalMeters = 186,
  normalCount = 182,
  missingCount = 4,
  latencyMs = 3200,
  className,
}: DataHealthIndicatorProps) {
  const percentage = (coverageRate * 100).toFixed(1);
  const isHealthy = coverageRate >= 0.95;
  const isDegraded = coverageRate >= 0.85 && coverageRate < 0.95;

  return (
    <Card className={cn('overflow-hidden border-border/80 bg-card shadow-xs', className)}>
      <CardHeader className="flex flex-row items-center justify-between p-4 pb-2">
        <div className="flex items-center gap-2">
          <Activity className="size-4 text-primary" />
          <CardTitle className="text-sm font-semibold tracking-tight">
            数据健康度与接入完整率
          </CardTitle>
        </div>

        {isHealthy ? (
          <Badge variant="outline" className="border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400 gap-1 text-[11px]">
            <CheckCircle2 className="size-3" />
            数据完好
          </Badge>
        ) : isDegraded ? (
          <Badge variant="outline" className="border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400 gap-1 text-[11px]">
            <AlertTriangle className="size-3" />
            存在缺数
          </Badge>
        ) : (
          <Badge variant="outline" className="border-rose-300 bg-rose-50 text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-400 gap-1 text-[11px]">
            <AlertTriangle className="size-3" />
            严重缺失
          </Badge>
        )}
      </CardHeader>

      <CardContent className="p-4 pt-1 space-y-3">
        <div className="flex items-baseline justify-between">
          <div className="flex items-baseline gap-1.5">
            <span className="text-2xl font-bold tracking-tight text-foreground">
              {percentage}%
            </span>
            <span className="text-xs text-muted-foreground">时序采集完整率</span>
          </div>
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            <Clock className="size-3 text-muted-foreground" />
            <span>平均延迟:</span>
            <span className="font-mono font-medium text-foreground">
              {(latencyMs / 1000).toFixed(1)}s
            </span>
          </div>
        </div>

        <Progress
          value={Number(percentage)}
          className={cn(
            'h-2',
            isHealthy
              ? '[&>div]:bg-emerald-500'
              : isDegraded
                ? '[&>div]:bg-amber-500'
                : '[&>div]:bg-rose-500'
          )}
        />

        <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
          <div>
            监测点位总数: <span className="font-semibold text-foreground">{totalMeters}</span> 个
          </div>
          <div className="flex items-center gap-3">
            <span className="text-emerald-600 dark:text-emerald-400">
              正常在线 {normalCount}
            </span>
            {missingCount > 0 && (
              <span className="text-rose-600 dark:text-rose-400">
                异常/缺失 {missingCount}
              </span>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
