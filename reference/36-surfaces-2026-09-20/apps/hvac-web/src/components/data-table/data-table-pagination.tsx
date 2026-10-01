import type { RowData, Table } from '@tanstack/react-table';
import {
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
} from 'lucide-react';

import type { DataTableFeatures } from './data-table-features';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

interface DataTablePaginationProps<TData extends RowData> {
  readonly table: Table<DataTableFeatures, TData>;
  readonly totalRows?: number;
  readonly pageSizeOptions?: readonly number[];
}

export function DataTablePagination<TData extends RowData>({
  table,
  totalRows,
  pageSizeOptions = [10, 20, 30, 50],
}: DataTablePaginationProps<TData>) {
  const selectedCount = table.getFilteredSelectedRowModel().rows.length;
  const total = totalRows ?? table.getFilteredRowModel().rows.length;
  const pageIndex = table.store.state.pagination.pageIndex;
  const pageSize = table.store.state.pagination.pageSize;
  const pageCount = Math.max(1, table.getPageCount());
  const canPreviousPage = table.getCanPreviousPage();
  const canNextPage = table.getCanNextPage();

  return (
    <div className="flex w-full flex-col gap-3 px-2 py-1 text-xs sm:flex-row sm:items-center sm:justify-between">
      <div className="min-h-8 flex items-center text-muted-foreground">
        {selectedCount > 0 ? (
          <>
            已选 <span className="mx-1 font-medium text-foreground tabular-nums">{selectedCount}</span>
            / {total} 行
          </>
        ) : (
          <>{total} 行</>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-4 sm:justify-end">
        <div className="flex items-center gap-2">
          <span className="whitespace-nowrap font-medium text-foreground">每页行数</span>
          <Select
            value={String(pageSize)}
            onValueChange={(value) => table.setPageSize(Number(value))}
          >
            <SelectTrigger className="h-8 w-[70px] bg-background text-xs tabular-nums">
              <SelectValue placeholder={pageSize} />
            </SelectTrigger>
            <SelectContent side="top">
              {pageSizeOptions.map((size) => (
                <SelectItem key={size} value={String(size)} className="text-xs tabular-nums">
                  {size}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="min-w-[88px] text-center font-medium text-foreground tabular-nums">
          第 {pageIndex + 1} / {pageCount} 页
        </div>

        <div className="flex items-center gap-1">
          <Button
            variant="outline"
            size="icon"
            className="hidden size-8 lg:inline-flex"
            onClick={() => table.setPageIndex(0)}
            disabled={!canPreviousPage}
            aria-label="第一页"
          >
            <ChevronsLeft className="size-4" />
          </Button>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            onClick={() => table.previousPage()}
            disabled={!canPreviousPage}
            aria-label="上一页"
          >
            <ChevronLeft className="size-4" />
          </Button>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            onClick={() => table.nextPage()}
            disabled={!canNextPage}
            aria-label="下一页"
          >
            <ChevronRight className="size-4" />
          </Button>
          <Button
            variant="outline"
            size="icon"
            className="hidden size-8 lg:inline-flex"
            onClick={() => table.setPageIndex(pageCount - 1)}
            disabled={!canNextPage}
            aria-label="最后一页"
          >
            <ChevronsRight className="size-4" />
          </Button>
        </div>
      </div>
    </div>
  );
}
