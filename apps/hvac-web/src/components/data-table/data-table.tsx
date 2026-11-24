import type * as React from 'react';
import {
  FlexRender,
  type Cell,
  type Header,
  type Row,
  type RowData,
  type Table as TanstackTable,
} from '@tanstack/react-table';

import { Inbox } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import type { DataTableFeatures } from './data-table-features';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';

type HeaderProps = Omit<React.ComponentProps<typeof TableHead>, 'children'>;
type CellProps = Omit<React.ComponentProps<typeof TableCell>, 'children'>;
type RowProps = Omit<React.ComponentProps<typeof TableRow>, 'children'>;

interface DataTableProps<TData extends RowData>
  extends Omit<React.ComponentProps<'div'>, 'children'> {
  readonly table: TanstackTable<DataTableFeatures, TData>;
  readonly children?: React.ReactNode;
  readonly footer?: React.ReactNode;
  readonly empty?: React.ReactNode;
  readonly filteredEmpty?: React.ReactNode;
  readonly onResetFilters?: () => void;
  readonly tableClassName?: string;
  readonly tableAriaLabel?: string;
  readonly getHeaderRowProps?: (
    headerGroup: ReturnType<TanstackTable<DataTableFeatures, TData>['getHeaderGroups']>[number],
  ) => RowProps;
  readonly getHeaderCellProps?: (
    header: Header<DataTableFeatures, TData, unknown>,
  ) => HeaderProps;
  readonly getRowProps?: (row: Row<DataTableFeatures, TData>) => RowProps;
  readonly getCellProps?: (
    cell: Cell<DataTableFeatures, TData, unknown>,
  ) => CellProps;
}

export function DataTable<TData extends RowData>({
  table,
  children,
  footer,
  empty,
  filteredEmpty,
  onResetFilters,
  className,
  tableClassName,
  tableAriaLabel,
  getHeaderRowProps,
  getHeaderCellProps,
  getRowProps,
  getCellProps,
  ...props
}: DataTableProps<TData>) {
  const rows = table.getRowModel().rows;
  const hasFilters = table.store.state.columnFilters.length > 0;
  const resetFilters = onResetFilters ?? (hasFilters ? () => table.resetColumnFilters() : undefined);

  return (
    <div
      className={cn('flex w-full min-w-0 flex-col gap-3', className)}
      {...props}
    >
      {children}
      <div className="w-full min-w-0 overflow-hidden rounded-xl bg-card shadow-xs ring-1 ring-foreground/10">
        <Table className={cn('text-sm', tableClassName)} aria-label={tableAriaLabel}>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => {
              const headerRowProps = getHeaderRowProps?.(headerGroup);
              return (
                <TableRow
                  key={headerGroup.id}
                  {...headerRowProps}
                  className={cn('hover:bg-transparent', headerRowProps?.className)}
                >
                  {headerGroup.headers.map((header) => {
                    const headerCellProps = getHeaderCellProps?.(header);
                    return (
                      <TableHead
                        key={header.id}
                        colSpan={header.colSpan}
                        {...headerCellProps}
                        className={cn(headerCellProps?.className)}
                      >
                        {header.isPlaceholder ? null : <FlexRender header={header} />}
                      </TableHead>
                    );
                  })}
                </TableRow>
              );
            })}
          </TableHeader>
          <TableBody>
            {rows.length ? (
              rows.map((row) => {
                const rowProps = getRowProps?.(row);
                return (
                  <TableRow
                    key={row.id}
                    data-state={row.getIsSelected() ? 'selected' : undefined}
                    {...rowProps}
                    className={cn(
                      'data-[state=selected]:bg-muted/50',
                      rowProps?.className,
                    )}
                  >
                    {row.getVisibleCells().map((cell) => {
                      const cellProps = getCellProps?.(cell);
                      return (
                        <TableCell
                          key={cell.id}
                          {...cellProps}
                          className={cn(cellProps?.className)}
                        >
                          <FlexRender cell={cell} />
                        </TableCell>
                      );
                    })}
                  </TableRow>
                );
              })
            ) : (
              <TableRow>
                <TableCell colSpan={table.getVisibleLeafColumns().length} className="p-0">
                  {hasFilters ? (
                    filteredEmpty ?? (
                      <TableEmpty action={resetFilters ? <Button type="button" variant="outline" size="sm" onClick={resetFilters}>清除筛选</Button> : undefined}>
                        没有符合筛选条件的记录
                      </TableEmpty>
                    )
                  ) : typeof empty === 'string' || empty === undefined ? (
                    <TableEmpty>{empty ?? '暂无数据'}</TableEmpty>
                  ) : empty}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      {footer ?? null}
    </div>
  );
}

/** An empty table says so plainly, with the same mark everywhere. */
export function TableEmpty({ children, action }: { readonly children: React.ReactNode; readonly action?: React.ReactNode }) {
  return (
    <Empty className="py-12">
      <EmptyHeader>
        <EmptyMedia variant="icon"><Inbox /></EmptyMedia>
        <EmptyTitle className="font-normal text-muted-foreground">{children}</EmptyTitle>
      </EmptyHeader>
      {action ? <div className="mt-2">{action}</div> : null}
    </Empty>
  );
}
