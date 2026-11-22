import { StatusBadge, type StatusTone } from '@/components/status-badge';
import { STATUS_LABEL, type Measure } from '../savings-model';

const TONE: Readonly<Record<'planned' | 'implemented' | 'untrusted' | 'ended', { readonly tone: StatusTone; readonly label: string }>> = {
  planned: { tone: 'neutral', label: STATUS_LABEL.PLANNED },
  implemented: { tone: 'success', label: '统计中' },
  untrusted: { tone: 'warning', label: '对比基线不可靠' },
  ended: { tone: 'neutral', label: STATUS_LABEL.ENDED },
};

/** An implemented measure is counted only on a reliable baseline, so the badge says which it is. */
export function MeasureStatusBadge({ measure, className }: { readonly measure: Pick<Measure, 'status' | 'baseline'>; readonly className?: string }) {
  const entry = measure.status === 'PLANNED' ? TONE.planned
    : measure.status === 'ENDED' ? TONE.ended
      : measure.baseline?.trusted ? TONE.implemented : TONE.untrusted;
  return <StatusBadge tone={entry.tone} label={entry.label} className={className} />;
}
