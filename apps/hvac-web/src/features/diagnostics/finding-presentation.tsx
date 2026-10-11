import { Badge } from '@/components/ui/badge';
import { severity, type Finding } from './diagnostics-model';

export const SEVERITY = {
  high: { label: '严重偏离', className: 'border-destructive/30 bg-destructive/10 text-destructive', priority: 'HIGH' },
  medium: { label: '明显偏离', className: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300', priority: 'MEDIUM' },
  low: { label: '轻微偏离', className: 'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300', priority: 'LOW' },
} as const;

export function FindingSeverityBadge({ finding }: { readonly finding: Finding }) {
  const level = SEVERITY[severity(finding)];
  return <Badge variant="outline" className={level.className}>{level.label}</Badge>;
}
