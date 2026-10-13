import { cn } from '@/lib/utils';

/**
 * Severity is read by shape as well as colour, so it survives colour blindness and greyscale print:
 * ◆ critical, ▲ major, ● minor, ■ warning, ○ info.
 */
export type SeverityLevel = 'critical' | 'major' | 'minor' | 'warning' | 'info';

const SHAPES: Readonly<Record<SeverityLevel, { readonly d: string; readonly color: string; readonly hollow?: true }>> = {
  critical: { d: 'M5 0 10 5 5 10 0 5z', color: 'var(--severity-critical)' },
  major: { d: 'M5 0.5 9.5 9H0.5z', color: 'var(--severity-major)' },
  minor: { d: 'M5 0.5a4.5 4.5 0 1 1 0 9 4.5 4.5 0 0 1 0-9z', color: 'var(--severity-minor)' },
  warning: { d: 'M1 1h8v8H1z', color: 'var(--severity-warning)' },
  info: { d: 'M5 1.2a3.8 3.8 0 1 1 0 7.6 3.8 3.8 0 0 1 0-7.6z', color: 'var(--severity-info)', hollow: true },
};

export const SEVERITY_LEVEL_LABELS: Readonly<Record<SeverityLevel, string>> = {
  critical: '严重',
  major: '主要',
  minor: '次要',
  warning: '提示',
  info: '信息',
};

export function SeverityMark({ level, label, className }: { readonly level: SeverityLevel; readonly label?: string; readonly className?: string }) {
  const shape = SHAPES[level];
  return (
    <svg viewBox="0 0 10 10" className={cn('size-2.5 shrink-0', className)} role="img" aria-label={label ?? SEVERITY_LEVEL_LABELS[level]}>
      <path
        d={shape.d}
        strokeWidth={1.5}
        style={shape.hollow ? { fill: 'none', stroke: shape.color } : { fill: shape.color }}
      />
    </svg>
  );
}
