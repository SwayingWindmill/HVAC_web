import type * as React from 'react';
import { Check, PlusCircle } from 'lucide-react';

import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import { Separator } from '@/components/ui/separator';

interface DataTableFacetedFilterProps {
  readonly title: string;
  readonly options: readonly {
    readonly label: string;
    readonly value: string;
    readonly icon?: React.ComponentType<{ className?: string }>;
  }[];
  readonly selectedValues: ReadonlySet<string>;
  readonly onSelect: (values: string[]) => void;
  readonly selectionMode?: 'single' | 'multiple';
}

export function DataTableFacetedFilter({
  title,
  options,
  selectedValues,
  onSelect,
  selectionMode = 'multiple',
}: DataTableFacetedFilterProps) {
  const handleSelect = (value: string) => {
    if (selectionMode === 'single') {
      onSelect(selectedValues.has(value) ? [] : [value]);
      return;
    }

    const next = new Set(selectedValues);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    onSelect(Array.from(next));
  };

  const handleClear = () => onSelect([]);

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="h-8 border-dashed text-xs">
          <PlusCircle className="mr-1.5 h-3.5 w-3.5" />
          {title}
          {selectedValues.size > 0 ? (
            <>
              <Separator orientation="vertical" className="mx-2 h-4" />
              <Badge
                variant="secondary"
                className="rounded-sm px-1 text-[10px] font-normal lg:hidden"
              >
                {selectedValues.size}
              </Badge>
              <div className="hidden space-x-1 lg:flex">
                {selectedValues.size > 2 ? (
                  <Badge
                    variant="secondary"
                    className="rounded-sm px-1.5 text-[11px] font-normal"
                  >
                    已选 {selectedValues.size} 项
                  </Badge>
                ) : (
                  options
                    .filter((option) => selectedValues.has(option.value))
                    .map((option) => (
                      <Badge
                        variant="secondary"
                        key={option.value}
                        className="rounded-sm px-1.5 text-[11px] font-normal"
                      >
                        {option.label}
                      </Badge>
                    ))
                )}
              </div>
            </>
          ) : null}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-[200px] p-0" align="start">
        <Command>
          <CommandInput placeholder={title} />
          <CommandList>
            <CommandEmpty>未找到结果</CommandEmpty>
            <CommandGroup>
              {options.map((option) => {
                const isSelected = selectedValues.has(option.value);
                return (
                  <CommandItem
                    key={option.value}
                    onSelect={() => handleSelect(option.value)}
                  >
                    <div
                      className={cn(
                        'mr-2 flex h-4 w-4 items-center justify-center rounded-sm border border-primary',
                        isSelected
                          ? 'bg-primary text-primary-foreground'
                          : 'opacity-50 [&_svg]:invisible',
                      )}
                    >
                      <Check className="h-3.5 w-3.5" />
                    </div>
                    {option.icon ? (
                      <option.icon className="mr-2 h-3.5 w-3.5 text-muted-foreground" />
                    ) : null}
                    <span>{option.label}</span>
                  </CommandItem>
                );
              })}
            </CommandGroup>
            {selectedValues.size > 0 ? (
              <>
                <CommandSeparator />
                <CommandGroup>
                  <CommandItem
                    onSelect={handleClear}
                    className="cursor-pointer justify-center text-center text-xs font-medium"
                  >
                    清除筛选
                  </CommandItem>
                </CommandGroup>
              </>
            ) : null}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
