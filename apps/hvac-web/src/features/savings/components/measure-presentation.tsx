import { cn } from '@/lib/utils';
import { STATUS_LABEL, type Measure } from '../savings-model';

const TONE = {
  planned: { label: STATUS_LABEL.PLANNED, className: 'bg-secondary text-secondary-foreground', dot: 'bg-subtle-foreground' },
  implemented: { label: '统计中', className: 'bg-success-soft text-success-text', dot: 'bg-success' },
  untrusted: { label: '对比基线不可靠', className: 'bg-warning-soft text-warning-text', dot: 'bg-warning' },
  ended: { label: STATUS_LABEL.ENDED, className: 'bg-secondary text-secondary-foreground', dot: 'bg-muted-foreground' },
} as const;

/** An implemented measure is counted only on a reliable baseline, so the badge says which it is. */
export function MeasureStatusBadge({ measure, className }: { readonly measure: Pick<Measure, 'status' | 'baseline'>; readonly className?: string }) {
  const tone = measure.status === 'PLANNED' ? TONE.planned
    : measure.status === 'ENDED' ? TONE.ended
      : measure.baseline?.trusted ? TONE.implemented : TONE.untrusted;
  return (
    <span className={cn('inline-flex h-5.5 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap', tone.className, className)}>
      <span className={cn('size-1.5 rounded-full', tone.dot)} aria-hidden="true" />
      {tone.label}
    </span>
  );
}
