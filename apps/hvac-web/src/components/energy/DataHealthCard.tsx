import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Progress } from '@/components/ui/progress';
import { cn } from '@/lib/utils';

export interface DataHealthCardProps {
  readonly completenessRate?: number; // e.g. 99.4
  readonly onlineMeters?: number; // e.g. 36
  readonly totalMeters?: number; // e.g. 37
  readonly avgLatencyMs?: number; // e.g. 1200
  readonly packetLossRate?: number; // e.g. 0.0
  readonly className?: string;
}

export function DataHealthCard({
  completenessRate = 99.4,
  onlineMeters = 36,
  totalMeters = 37,
  avgLatencyMs = 1200,
  packetLossRate = 0.0,
  className,
}: DataHealthCardProps) {
  const isHealthy = completenessRate >= 98;

  return (
    <Card className={cn('flex flex-col rounded-xl border border-border/80 bg-card shadow-xs', className)}>
      <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
        <div>
          <CardTitle className="text-base font-semibold tracking-tight text-foreground">
            数据接入质量
          </CardTitle>
          <p className="mt-0.5 text-xs text-muted-foreground">
            网关时序与计量表计状态
          </p>
        </div>
        <Badge
          variant="outline"
          className={cn(
            'text-xs font-medium border-transparent',
            isHealthy
              ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400'
              : 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-400',
          )}
        >
          {isHealthy ? '通信良好' : '轻度延迟'}
        </Badge>
      </CardHeader>

      <CardContent className="flex flex-1 flex-col justify-between p-5 pt-0 space-y-4">
        {/* Main Metric Strip */}
        <div className="flex items-baseline justify-between border-b border-border/40 pb-3">
          <div className="space-y-0.5">
            <span className="text-2xl font-bold tracking-tight tabular-nums text-foreground">
              {completenessRate.toFixed(1)}%
            </span>
            <div className="text-xs text-muted-foreground">
              时序上报完整率
            </div>
          </div>
          <div className="text-right space-y-0.5">
            <span className="text-sm font-semibold tabular-nums text-foreground">
              {onlineMeters} / {totalMeters}
            </span>
            <div className="text-xs text-muted-foreground">
              表计在线数量
            </div>
          </div>
        </div>

        <div className="space-y-1.5">
          <div className="flex items-center justify-between text-xs font-medium text-foreground">
            <span className="text-muted-foreground">完整率进度</span>
            <span>{completenessRate}%</span>
          </div>
          <Progress value={completenessRate} className="h-1.5" />
        </div>

        <div className="grid grid-cols-3 gap-2 text-xs">
          <div className="flex flex-col rounded-lg border border-border/50 bg-muted/30 p-2.5">
            <span className="text-muted-foreground text-[11px]">时序延迟</span>
            <span className="mt-1 font-semibold tabular-nums text-foreground">
              {(avgLatencyMs / 1000).toFixed(1)}s
            </span>
          </div>

          <div className="flex flex-col rounded-lg border border-border/50 bg-muted/30 p-2.5">
            <span className="text-muted-foreground text-[11px]">丢包率</span>
            <span className="mt-1 font-semibold tabular-nums text-foreground">
              {packetLossRate.toFixed(1)}%
            </span>
          </div>

          <div className="flex flex-col rounded-lg border border-border/50 bg-muted/30 p-2.5">
            <span className="text-muted-foreground text-[11px]">协议状态</span>
            <span className="mt-1 font-semibold text-foreground truncate">
              正常 (MQTT)
            </span>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
