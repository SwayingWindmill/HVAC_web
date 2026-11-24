import type { RowData, Table } from "@tanstack/react-table";

import { Check, Settings2 } from "lucide-react";
import { cn } from "cn";
import * as React from "react";

import type { DataTableFeatures } from "./data-table-features";

import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

interface DataTableViewOptionsProps<
  TData extends RowData,
> extends React.ComponentProps<typeof PopoverContent> {
  table: Table<DataTableFeatures, TData>;
  disabled?: boolean;
}

export function DataTableViewOptions<TData extends RowData>({
  table,
  disabled,
  className,
  ...props
}: DataTableViewOptionsProps<TData>) {
  const columns = React.useMemo(
    () =>
      table
        .getAllColumns()
        .filter(
          (column) =>
            typeof column.accessorFn !== "undefined" && column.getCanHide(),
        ),
    [table],
  );

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          aria-label="配置显示列"
          role="combobox"
          variant="outline"
          className="ml-auto hidden h-8 font-normal lg:flex"
          disabled={disabled}
        >
          <Settings2 className="text-muted-foreground" />
          列
        </Button>
      </PopoverTrigger>
      <PopoverContent className={cn("w-44 p-0", className)} {...props}>
        <Command>
          <CommandInput placeholder="搜索列..." />
          <CommandList>
            <CommandEmpty>没有可配置的列</CommandEmpty>
            <CommandGroup>
              {columns.map((column) => {
                const visible = column.getIsVisible();
                return (
                  <CommandItem
                    key={column.id}
                    data-checked={visible}
                    onSelect={() => column.toggleVisibility(!visible)}
                  >
                    <span className="flex-1 truncate">
                      {column.columnDef.meta?.label ?? column.id}
                    </span>
                    <Check className={cn("ml-auto size-3.5 text-primary", visible ? "opacity-100" : "opacity-0")} aria-hidden="true" />
                  </CommandItem>
                );
              })}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
