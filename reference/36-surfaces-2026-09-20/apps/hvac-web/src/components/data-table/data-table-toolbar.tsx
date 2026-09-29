import type * as React from 'react';
import type { RowData, Table } from '@tanstack/react-table';

import type { DataTableFeatures } from './data-table-features';
import { DataTableViewOptions } from './data-table-view-options';
import { cn } from '@/lib/utils';

interface DataTableToolbarProps<TData extends RowData> extends React.ComponentProps<'div'> {
  readonly table: Table<DataTableFeatures, TData>;
  readonly actions?: React.ReactNode;
  readonly showViewOptions?: boolean;
  readonly viewOptionsLabel?: string;
}

export function DataTableToolbar<TData extends RowData>({
  table,
  children,
  actions,
  showViewOptions = true,
  viewOptionsLabel = '列',
  className,
  ...props
}: DataTableToolbarProps<TData>) {
  return (
    <div
      className={cn(
        'flex w-full flex-col gap-2 sm:flex-row sm:items-center sm:justify-between',
        className,
      )}
      {...props}
    >
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
        {children}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {actions}
        {showViewOptions ? (
          <DataTableViewOptions table={table} label={viewOptionsLabel} />
        ) : null}
      </div>
    </div>
  );
}
