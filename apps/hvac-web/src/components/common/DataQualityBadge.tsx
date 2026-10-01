import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { CheckCircle2, AlertTriangle, HelpCircle, Activity } from 'lucide-react';

export type DataQualityTier = 'verified' | 'estimated' | 'missing' | 'degraded';

export interface DataQualityBadgeProps {
  readonly quality: DataQualityTier;
  readonly coverage?: number; // e.g. 0.98 -> 98%
  readonly className?: string;
  readonly showLabel?: boolean;
}

const qualityConfig: Record<
  DataQualityTier,
  { label: string; tone: string; icon: typeof CheckCircle2 }
> = {
  verified: {
    label: '实测数据',
    tone: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400',
    icon: CheckCircle2,
  },
  estimated: {
    label: '估算补全',
    tone: 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400',
    icon: Activity,
  },
  degraded: {
    label: '数据降级',
    tone: 'border-yellow-200 bg-yellow-50 text-yellow-700 dark:border-yellow-800 dark:bg-yellow-950/40 dark:text-yellow-400',
    icon: AlertTriangle,
  },
  missing: {
    label: '存在缺失',
    tone: 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-400',
    icon: HelpCircle,
  },
};

export function DataQualityBadge({
  quality,
  coverage,
  className,
  showLabel = true,
}: DataQualityBadgeProps) {
  const cfg = qualityConfig[quality] ?? qualityConfig.verified;
  const Icon = cfg.icon;

  return (
    <Badge
      variant="outline"
      className={cn(
        'gap-1 px-2 py-0.5 font-normal text-[11px] select-none',
        cfg.tone,
        className
      )}
    >
      <Icon className="size-3 shrink-0" />
      {showLabel && <span>{cfg.label}</span>}
      {coverage !== undefined && (
        <span className="font-mono text-[10px] opacity-80">
          {(coverage * 100).toFixed(0)}%
        </span>
      )}
    </Badge>
  );
}
