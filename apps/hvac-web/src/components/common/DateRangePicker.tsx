import { useState } from 'react';
import { format, subDays, startOfMonth, endOfMonth, startOfYear } from 'date-fns';
import { zhCN } from 'date-fns/locale';
import { Calendar as CalendarIcon } from 'lucide-react';
import type { DateRange } from 'react-day-picker';

import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Calendar } from '@/components/ui/calendar';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';

export interface DateRangePickerProps {
  readonly value?: DateRange;
  readonly onChange?: (range: DateRange | undefined) => void;
  readonly className?: string;
  readonly placeholder?: string;
}

export function DateRangePicker({
  value,
  onChange,
  className,
  placeholder = '选择日期范围',
}: DateRangePickerProps) {
  const [open, setOpen] = useState(false);

  const presets = [
    {
      label: '今日',
      getValue: () => ({ from: new Date(), to: new Date() }),
    },
    {
      label: '最近7天',
      getValue: () => ({ from: subDays(new Date(), 6), to: new Date() }),
    },
    {
      label: '本月',
      getValue: () => ({ from: startOfMonth(new Date()), to: new Date() }),
    },
    {
      label: '上月',
      getValue: () => {
        const lastMonth = subDays(startOfMonth(new Date()), 1);
        return { from: startOfMonth(lastMonth), to: endOfMonth(lastMonth) };
      },
    },
    {
      label: '今年',
      getValue: () => ({ from: startOfYear(new Date()), to: new Date() }),
    },
  ];

  return (
    <div className={cn('grid gap-2', className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            id="date"
            variant="outline"
            size="sm"
            className={cn(
              'h-9 justify-start text-left font-normal text-xs',
              !value && 'text-muted-foreground'
            )}
          >
            <CalendarIcon className="mr-2 size-3.5" />
            {value?.from ? (
              value.to ? (
                <>
                  {format(value.from, 'yyyy-MM-dd')} ~ {format(value.to, 'yyyy-MM-dd')}
                </>
              ) : (
                format(value.from, 'yyyy-MM-dd')
              )
            ) : (
              <span>{placeholder}</span>
            )}
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-auto p-0" align="end">
          <div className="flex flex-col sm:flex-row">
            <div className="flex sm:flex-col gap-1 p-2 border-b sm:border-b-0 sm:border-r border-border/60">
              {presets.map((preset) => (
                <Button
                  key={preset.label}
                  variant="ghost"
                  size="sm"
                  className="justify-start text-xs h-7 px-2.5"
                  onClick={() => {
                    const range = preset.getValue();
                    onChange?.(range);
                    setOpen(false);
                  }}
                >
                  {preset.label}
                </Button>
              ))}
            </div>
            <Calendar
              mode="range"
              defaultMonth={value?.from}
              selected={value}
              onSelect={onChange}
              numberOfMonths={2}
              locale={zhCN}
            />
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}
