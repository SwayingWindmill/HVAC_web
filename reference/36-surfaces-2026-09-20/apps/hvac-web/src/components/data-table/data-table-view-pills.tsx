import type { LucideIcon } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface DataTableViewPillOption<T extends string = string> {
  readonly key?: T;
  readonly id?: T;
  readonly label: string;
  readonly count?: number | string;
  readonly icon?: LucideIcon;
}

interface DataTableViewPillsProps<T extends string = string> {
  readonly options: readonly DataTableViewPillOption<T>[];
  readonly value: T;
  readonly onValueChange: (value: T) => void;
  readonly className?: string;
  readonly ariaLabel?: string;
}

/**
 * Segmented view filter pills with optional count badges.
 * Directly matches the shadcn/ui blocks data table header pattern.
 */
export function DataTableViewPills<T extends string = string>({
  options,
  value,
  onValueChange,
  className,
  ariaLabel = '数据视图与状态过滤',
}: DataTableViewPillsProps<T>) {
  return (
    <nav
      aria-label={ariaLabel}
      className={cn('inline-flex flex-wrap items-center gap-1.5 p-1 rounded-lg bg-muted/40 border border-border/60', className)}
    >
      {options.map((option) => {
        const optionKey = (option.key ?? option.id ?? '') as T;
        const active = optionKey === value;
        const Icon = option.icon;

        return (
          <button
            key={optionKey}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onValueChange(optionKey)}
            className={cn(
              'inline-flex items-center gap-1.5 px-3 py-1 text-xs font-medium rounded-md transition-all select-none',
              active
                ? 'bg-background text-foreground shadow-xs border border-border/80 font-semibold'
                : 'text-muted-foreground hover:text-foreground hover:bg-background/60 border border-transparent',
            )}
          >
            {Icon ? <Icon className="size-3.5 shrink-0" aria-hidden="true" /> : null}
            <span>{option.label}</span>
            {option.count !== undefined ? (
              <span
                className={cn(
                  'inline-flex items-center justify-center min-w-4 px-1.5 py-0.2 rounded-full text-[10px] font-semibold tabular-nums',
                  active
                    ? 'bg-primary/10 text-primary dark:bg-primary/20'
                    : 'bg-muted text-muted-foreground',
                )}
              >
                {option.count}
              </span>
            ) : null}
          </button>
        );
      })}
    </nav>
  );
}
