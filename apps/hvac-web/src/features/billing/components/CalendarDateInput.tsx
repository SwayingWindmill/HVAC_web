import { useState } from 'react';
import { CalendarIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Calendar } from '@/components/ui/calendar';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { formatIsoCalendarDate, parseIsoCalendarDate } from '../utility-bill-form';
import { preventImeSubmit } from './billing-form-helpers';

export interface CalendarDateInputProps {
  readonly id: string;
  readonly value: string;
  readonly disabled?: boolean;
  readonly invalid?: boolean;
  readonly describedBy?: string;
  readonly buttonAriaLabel: string;
  readonly onChange: (nextValue: string) => void;
  readonly onBlur: () => void;
}

export function CalendarDateInput({
  id,
  value,
  disabled,
  invalid,
  describedBy,
  buttonAriaLabel,
  onChange,
  onBlur,
}: CalendarDateInputProps) {
  const [open, setOpen] = useState(false);
  const selectedDate = parseIsoCalendarDate(value);

  return (
    <div className="flex items-center gap-1.5">
      <Input
        id={id}
        type="text"
        inputMode="numeric"
        placeholder="YYYY-MM-DD"
        value={value}
        disabled={disabled}
        aria-invalid={invalid ? 'true' : 'false'}
        aria-describedby={describedBy}
        onChange={(event) => onChange(event.target.value)}
        onBlur={onBlur}
        onKeyDown={preventImeSubmit}
        className="tabular-nums"
      />
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="outline"
            size="icon"
            disabled={disabled}
            aria-label={buttonAriaLabel}
            className="shrink-0"
          >
            <CalendarIcon className="size-4" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-auto p-0" align="end">
          <Calendar
            mode="single"
            selected={selectedDate}
            defaultMonth={selectedDate}
            onSelect={(picked) => {
              if (picked) {
                onChange(formatIsoCalendarDate(picked));
                onBlur();
                setOpen(false);
              }
            }}
          />
        </PopoverContent>
      </Popover>
    </div>
  );
}
