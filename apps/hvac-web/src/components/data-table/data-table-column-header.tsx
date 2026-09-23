import type { HTMLAttributes } from 'react';
import type { RowData, Table } from '@tanstack/react-table';
import { ArrowDown, ArrowUp, ChevronsUpDown, EyeOff } from 'lucide-react';

import type { DataTableFeatures } from './data-table-features';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';

type DataTableColumn<TData extends RowData> = NonNullable<
  ReturnType<Table<DataTableFeatures, TData>['getColumn']>
>;

interface DataTableColumnHeaderProps<TData extends RowData> extends HTMLAttributes<HTMLDivElement> {
  readonly column: DataTableColumn<TData>;
  readonly title: string;
}

export function DataTableColumnHeader<TData extends RowData>({
  column,
  title,
  className,
}: DataTableColumnHeaderProps<TData>) {
  if (!column.getCanSort()) {
    return <div className={cn('text-xs font-medium text-muted-foreground', className)}>{title}</div>;
  }

  const isSorted = column.getIsSorted();

  return (
    <div className={cn('flex items-center space-x-2', className)}>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            className="-ml-3 h-8 text-xs font-medium data-[state=open]:bg-accent"
          >
            <span>{title}</span>
            {isSorted === 'desc' ? (
              <ArrowDown className="ml-2 h-3.5 w-3.5" />
            ) : isSorted === 'asc' ? (
              <ArrowUp className="ml-2 h-3.5 w-3.5" />
            ) : (
              <ChevronsUpDown className="ml-2 h-3.5 w-3.5" />
            )}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          <DropdownMenuGroup>
            <DropdownMenuItem onClick={() => column.toggleSorting(false)}>
              <ArrowUp className="mr-2 h-3.5 w-3.5 text-muted-foreground/70" />
              升序
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => column.toggleSorting(true)}>
              <ArrowDown className="mr-2 h-3.5 w-3.5 text-muted-foreground/70" />
              降序
            </DropdownMenuItem>
          </DropdownMenuGroup>
          {column.getCanHide() ? (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem onClick={() => column.toggleVisibility(false)}>
                  <EyeOff className="mr-2 h-3.5 w-3.5 text-muted-foreground/70" />
                  隐藏此列
                </DropdownMenuItem>
              </DropdownMenuGroup>
            </>
          ) : null}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
