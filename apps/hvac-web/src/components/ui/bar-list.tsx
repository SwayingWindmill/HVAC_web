import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

export interface BarListItem {
  readonly name: string;
  readonly value: number;
  readonly key?: string;
  readonly icon?: ReactNode;
  readonly meta?: string;
}

interface BarListProps extends React.ComponentProps<'div'> {
  readonly data: readonly BarListItem[];
  readonly valueFormatter?: (value: number) => string;
  readonly onItemClick?: (item: BarListItem) => void;
}

export function BarList({
  data,
  valueFormatter = (value) => new Intl.NumberFormat('zh-CN').format(value),
  onItemClick,
  className,
  ...props
}: BarListProps) {
  const items = [...data].sort((left, right) => right.value - left.value);
  const maximum = Math.max(...items.map((item) => item.value), 0);

  return (
    <div data-slot="bar-list" className={cn('flex flex-col gap-2', className)} {...props}>
      {items.map((item) => {
        const width = maximum > 0 ? (item.value / maximum) * 100 : 0;
        return (
          <button
            key={item.key ?? item.name}
            type="button"
            disabled={!onItemClick}
            onClick={onItemClick ? () => onItemClick(item) : undefined}
            className="group flex w-full items-center gap-3 text-left text-sm disabled:cursor-default"
          >
            <span className="relative flex h-9 min-w-0 flex-1 items-center overflow-hidden rounded-md">
              <span
                aria-hidden="true"
                className="absolute inset-y-0 left-0 rounded-md bg-chart-1/15 transition-[width]"
                style={{ width: `${width}%` }}
              />
              <span className="relative flex min-w-0 items-center gap-2 px-2">
                {item.icon ? <span className="shrink-0 text-muted-foreground [&>svg]:size-4">{item.icon}</span> : null}
                <span className={cn('truncate', onItemClick && 'group-hover:underline')}>{item.name}</span>
              </span>
            </span>
            <span className="shrink-0 text-right">
              <span className="block font-medium tabular-nums">{valueFormatter(item.value)}</span>
              {item.meta ? <span className="block text-xs text-muted-foreground">{item.meta}</span> : null}
            </span>
          </button>
        );
      })}
    </div>
  );
}
