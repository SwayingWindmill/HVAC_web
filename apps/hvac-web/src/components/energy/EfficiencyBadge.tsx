import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { Gauge } from 'lucide-react';

export type MetricKind = 'COP' | 'WTF' | 'EUI' | 'kW/RT';
export type EfficiencyGrade = 'level-1' | 'level-2' | 'level-3' | 'substandard';

export interface EfficiencyBadgeProps {
  readonly kind: MetricKind;
  readonly value: number | string;
  readonly grade?: EfficiencyGrade;
  readonly className?: string;
  readonly showGradeLabel?: boolean;
}

const gradeMap: Record<
  EfficiencyGrade,
  { label: string; tone: string }
> = {
  'level-1': {
    label: '一级能效 (优)',
    tone: 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400',
  },
  'level-2': {
    label: '二级能效 (良)',
    tone: 'border-sky-300 bg-sky-50 text-sky-700 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-400',
  },
  'level-3': {
    label: '三级能效 (中)',
    tone: 'border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400',
  },
  substandard: {
    label: '能效偏低 (劣)',
    tone: 'border-rose-300 bg-rose-50 text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-400',
  },
};

function resolveCopGrade(cop: number | string): EfficiencyGrade {
  const num = typeof cop === 'number' ? cop : parseFloat(cop);
  if (isNaN(num)) return 'level-2';
  if (num >= 5.0) return 'level-1';
  if (num >= 4.2) return 'level-2';
  if (num >= 3.5) return 'level-3';
  return 'substandard';
}

export function EfficiencyBadge({
  kind,
  value,
  grade,
  className,
  showGradeLabel = true,
}: EfficiencyBadgeProps) {
  const resolvedGrade = grade ?? (kind === 'COP' ? resolveCopGrade(value) : 'level-2');
  const cfg = gradeMap[resolvedGrade] ?? gradeMap['level-2'];
  const displayVal = typeof value === 'number' ? value.toFixed(2) : value;

  return (
    <Badge
      variant="outline"
      className={cn('gap-1.5 px-2 py-0.5 text-xs font-medium select-none', cfg.tone, className)}
    >
      <Gauge className="size-3.5 shrink-0" />
      <span className="font-bold">
        {kind} {displayVal}
      </span>
      {showGradeLabel && (
        <span className="opacity-90 font-normal">· {cfg.label}</span>
      )}
    </Badge>
  );
}
