import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { formatEnergy, formatCurrency } from '@/lib/units/formatter';
import { Lightbulb, ArrowRight, ShieldCheck, Clock } from 'lucide-react';
import { EntityLink, type EntityType } from '@/components/common/EntityLink';
import { cn } from '@/lib/utils';

export interface OpportunityCardProps {
  readonly id: string;
  readonly title: string;
  readonly category: string;
  readonly targetEntity: {
    readonly id: string;
    readonly name: string;
    readonly type?: EntityType;
  };
  readonly annualKwh: number;
  readonly annualCost: number;
  readonly confidence: 'high' | 'medium' | 'low';
  readonly priority: 'critical' | 'high' | 'medium' | 'low';
  readonly paybackMonths?: number;
  readonly onAction?: (id: string) => void;
  readonly onDetails?: (id: string) => void;
  readonly className?: string;
}

const confidenceMap = {
  high: { label: '高置信度', class: 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400' },
  medium: { label: '中置信度', class: 'border-sky-300 bg-sky-50 text-sky-700 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-400' },
  low: { label: '待校验', class: 'border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400' },
};

const priorityMap = {
  critical: { label: '紧急', class: 'border-rose-300 bg-rose-50 text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-400' },
  high: { label: '高优', class: 'border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400' },
  medium: { label: '中优', class: 'border-sky-300 bg-sky-50 text-sky-700 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-400' },
  low: { label: '低优', class: 'border-muted bg-muted/40 text-muted-foreground' },
};

export function OpportunityCard({
  id,
  title,
  category,
  targetEntity,
  annualKwh,
  annualCost,
  confidence,
  priority,
  paybackMonths,
  onAction,
  onDetails,
  className,
}: OpportunityCardProps) {
  const fmtEnergy = formatEnergy(annualKwh);
  const fmtCost = formatCurrency(annualCost);
  const confCfg = confidenceMap[confidence] ?? confidenceMap.medium;
  const prioCfg = priorityMap[priority] ?? priorityMap.medium;

  return (
    <Card className={cn('overflow-hidden border-border/80 shadow-xs hover:border-border transition-all', className)}>
      <CardContent className="p-4 space-y-3">
        <div className="flex items-start justify-between gap-2">
          <div className="space-y-1">
            <div className="flex items-center gap-1.5 flex-wrap">
              <span className="text-[11px] font-medium text-muted-foreground bg-muted/60 px-1.5 py-0.5 rounded">
                {category}
              </span>
              <Badge variant="outline" className={cn('text-[10px] h-4 px-1.5 py-0', confCfg.class)}>
                <ShieldCheck className="size-2.5 mr-1" />
                {confCfg.label}
              </Badge>
              <Badge variant="outline" className={cn('text-[10px] h-4 px-1.5 py-0', prioCfg.class)}>
                {prioCfg.label}
              </Badge>
            </div>
            <h4
              onClick={() => onDetails?.(id)}
              className="text-sm font-semibold text-foreground hover:text-primary cursor-pointer transition-colors"
            >
              {title}
            </h4>
          </div>

          <div className="text-right shrink-0">
            <div className="text-base font-bold text-emerald-600 dark:text-emerald-400">
              {fmtCost.formatted}
              <span className="text-xs font-normal text-muted-foreground">/年</span>
            </div>
            <div className="text-xs text-muted-foreground">
              {fmtEnergy.formatted}/年
            </div>
          </div>
        </div>

        <div className="flex items-center justify-between pt-2 border-t border-border/50 text-xs">
          <div className="flex items-center gap-3">
            <EntityLink
              id={targetEntity.id}
              name={targetEntity.name}
              type={targetEntity.type ?? 'equipment'}
            />
            {paybackMonths !== undefined && (
              <span className="inline-flex items-center gap-1 text-muted-foreground text-[11px]">
                <Clock className="size-3" />
                静态回收期: {paybackMonths}个月
              </span>
            )}
          </div>

          <div className="flex items-center gap-2">
            {onAction && (
              <Button
                variant="outline"
                size="sm"
                className="h-7 text-xs px-2.5"
                onClick={() => onAction(id)}
              >
                <Lightbulb className="size-3 mr-1 text-amber-500" />
                采纳
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              className="h-7 text-xs px-2 text-muted-foreground hover:text-foreground"
              onClick={() => onDetails?.(id)}
            >
              详情
              <ArrowRight className="size-3 ml-1" />
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
