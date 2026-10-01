import { Badge } from '@/components/ui/badge';
import { AlertCircle, AlertTriangle, Info, Bell } from 'lucide-react';
import { cn } from '@/lib/utils';

export type AlarmSeverityLevel = 'critical' | 'major' | 'minor' | 'info';

export interface AlarmSeverityProps {
  readonly level: AlarmSeverityLevel;
  readonly count?: number;
  readonly className?: string;
  readonly showLabel?: boolean;
}

const severityConfig: Record<
  AlarmSeverityLevel,
  { label: string; tone: string; icon: typeof AlertCircle }
> = {
  critical: {
    label: '紧急告警',
    tone: 'border-rose-300 bg-rose-50 text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-400',
    icon: AlertCircle,
  },
  major: {
    label: '重要告警',
    tone: 'border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400',
    icon: AlertTriangle,
  },
  minor: {
    label: '次要告警',
    tone: 'border-yellow-300 bg-yellow-50 text-yellow-700 dark:border-yellow-800 dark:bg-yellow-950/40 dark:text-yellow-400',
    icon: Bell,
  },
  info: {
    label: '提示信息',
    tone: 'border-sky-300 bg-sky-50 text-sky-700 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-400',
    icon: Info,
  },
};

export function AlarmSeverity({
  level,
  count,
  className,
  showLabel = true,
}: AlarmSeverityProps) {
  const cfg = severityConfig[level] ?? severityConfig.info;
  const Icon = cfg.icon;

  return (
    <Badge
      variant="outline"
      className={cn(
        'gap-1 px-2 py-0.5 text-xs font-medium select-none',
        cfg.tone,
        className
      )}
    >
      <Icon className="size-3 shrink-0" />
      {showLabel && <span>{cfg.label}</span>}
      {count !== undefined && (
        <span className="font-mono font-bold ml-0.5">({count})</span>
      )}
    </Badge>
  );
}
