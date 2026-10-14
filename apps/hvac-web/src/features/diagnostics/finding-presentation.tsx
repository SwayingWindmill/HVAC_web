import { SeverityMark, type SeverityLevel } from '@/components/severity-mark';
import { cn } from '@/lib/utils';
import { severity, type Finding } from './diagnostics-model';

export const SEVERITY = {
  high: { label: '严重偏离', level: 'critical', className: 'bg-destructive-soft text-destructive-text', priority: 'HIGH' },
  medium: { label: '明显偏离', level: 'major', className: 'bg-warning-soft text-warning-text', priority: 'MEDIUM' },
  low: { label: '轻微偏离', level: 'minor', className: 'bg-secondary text-secondary-foreground', priority: 'LOW' },
} as const satisfies Record<string, { label: string; level: SeverityLevel; className: string; priority: string }>;

/** How far a finding fell, as its shape and words on a soft tint. */
export function FindingSeverityBadge({ finding, className }: { readonly finding: Finding; readonly className?: string }) {
  const level = SEVERITY[severity(finding)];
  return (
    <span className={cn('inline-flex h-5.5 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap', level.className, className)}>
      <SeverityMark level={level.level} label="" />
      {level.label}
    </span>
  );
}
