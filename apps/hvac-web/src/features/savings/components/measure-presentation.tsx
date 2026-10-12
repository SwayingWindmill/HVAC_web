import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { STATUS_LABEL, type Measure } from '../savings-model';

const TONE = {
  planned: { label: STATUS_LABEL.PLANNED, className: 'bg-muted text-muted-foreground' },
  reporting: { label: STATUS_LABEL.IMPLEMENTED, className: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' },
  untrusted: { label: '基线不可信', className: 'bg-amber-500/10 text-amber-700 dark:text-amber-300' },
  ended: { label: STATUS_LABEL.ENDED, className: 'bg-muted text-muted-foreground' },
} as const;

/** An implemented measure reports only on a trusted baseline, so the badge says which it is. */
export function MeasureStatusBadge({ measure }: { readonly measure: Pick<Measure, 'status' | 'baseline'> }) {
  const tone = measure.status === 'PLANNED' ? TONE.planned
    : measure.status === 'ENDED' ? TONE.ended
      : measure.baseline?.trusted ? TONE.reporting : TONE.untrusted;
  return <Badge variant="outline" className={cn('h-5.5 rounded-md border-transparent px-1.5', tone.className)}>{tone.label}</Badge>;
}
