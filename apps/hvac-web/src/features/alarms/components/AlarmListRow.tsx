import type { ReactNode } from 'react';
import { SeverityMark } from '@/components/severity-mark';
import { cn } from '@/lib/utils';
import type { Alarm } from '../alarm-api';
import { SEVERITY_LABELS, SEVERITY_LEVEL } from '../alarm-presentation';

/** The grey bar over a group of alarm rows: its name and how many rows it holds. */
export function AlarmGroupHeader({ label, count }: { readonly label: string; readonly count: number }) {
  return (
    <div className="flex h-[34px] items-center bg-muted px-4 text-xs font-medium text-muted-foreground">
      {label}
      <span className="ml-auto font-mono text-subtle-foreground">{count}</span>
    </div>
  );
}

/** One alarm in a list beside its detail pane (告警 and 通知): severity, title, when, and one line of facts. */
export function AlarmListRow({ alarm, selected, onSelect, when, whenTitle, facts, muted = false, id }: {
  readonly alarm: Alarm;
  readonly selected: boolean;
  readonly onSelect: () => void;
  readonly when: ReactNode;
  readonly whenTitle?: string;
  readonly facts: ReactNode;
  /** Drawn quieter, as an alarm that already cleared. */
  readonly muted?: boolean;
  readonly id?: string;
}) {
  return (
    <li id={id}>
      <button
        type="button"
        aria-current={selected ? 'true' : undefined}
        onClick={onSelect}
        className={cn(
          'relative grid w-full grid-cols-[12px_minmax(0,1fr)_auto] gap-x-2.5 gap-y-[3px] border-b border-divider px-4 pt-[11px] pb-3 text-left outline-none hover:bg-accent focus-visible:bg-accent',
          selected && 'bg-primary-soft hover:bg-primary-soft before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary',
        )}
      >
        <SeverityMark level={SEVERITY_LEVEL[alarm.currentSeverity]} label={SEVERITY_LABELS[alarm.currentSeverity]} className="row-span-2 mt-[5px]" />
        <span className={cn('truncate text-sm', muted ? 'font-medium text-secondary-foreground' : 'font-semibold')}>{alarm.title}</span>
        <span className="text-xs whitespace-nowrap text-subtle-foreground" title={whenTitle}>{when}</span>
        <span className="col-span-2 col-start-2 truncate text-xs text-muted-foreground">{facts}</span>
      </button>
    </li>
  );
}
