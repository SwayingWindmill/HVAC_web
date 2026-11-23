import { useEffect, useRef, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Controller, useForm } from 'react-hook-form';
import { toast } from 'sonner';
import { z } from 'zod';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { siteDayKey } from '@/features/energy-analysis/consumption/model';
import { isOneOf } from '@/lib/typed';
import {
  isValidDateKey,
  SITE_CALENDAR_DAY_TYPES,
  SITE_CALENDAR_DAY_TYPE_DESCRIPTION,
} from '../accounting-config-model';
import {
  describeAccountingConfigError,
  upsertSiteAccountingHoliday,
  type SiteCalendarHoliday,
} from '../query';
import { invalidateAfterHolidayMutation } from './accounting-config-helpers';
import { describedBy, preventImeSubmit } from './billing-form-helpers';

const holidayFormSchema = z.object({
  date: z
    .string()
    .trim()
    .refine(isValidDateKey, { message: '请输入有效的日历日期（YYYY-MM-DD）' }),
  dayType: z.enum(['HOLIDAY', 'SPECIAL_DAY', 'WEEKDAY', 'WEEKEND']),
  label: z.string().trim().max(120, { message: '说明不能超过 120 字' }),
});

type HolidayFormValues = z.infer<typeof holidayFormSchema>;

function buildHolidayInitialValues(
  selectedYear: number,
  editTarget: SiteCalendarHoliday | null,
  now: Date,
  timezone: string,
): HolidayFormValues {
  if (editTarget) {
    return {
      date: editTarget.holidayDate,
      dayType: editTarget.dayType,
      label: editTarget.label,
    };
  }
  const todayKey = siteDayKey(now, timezone);
  const defaultDate = todayKey.startsWith(`${selectedYear}-`)
    ? todayKey
    : `${selectedYear}-01-01`;
  return {
    date: defaultDate,
    dayType: 'HOLIDAY',
    label: '',
  };
}

export interface SiteHolidayEditorDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly now: Date;
  readonly selectedYear: number;
  readonly editTarget: SiteCalendarHoliday | null;
  readonly onNotice: (message: string) => void;
}

export function SiteHolidayEditorDialog({
  open,
  onOpenChange,
  siteId,
  timezone,
  csrfToken,
  now,
  selectedYear,
  editTarget,
  onNotice,
}: SiteHolidayEditorDialogProps) {
  const queryClient = useQueryClient();
  const [submitError, setSubmitError] = useState<string | null>(null);
  const nowRef = useRef(now);
  nowRef.current = now;

  const form = useForm<HolidayFormValues>({
    resolver: zodResolver(holidayFormSchema),
    mode: 'onBlur',
    reValidateMode: 'onChange',
    defaultValues: buildHolidayInitialValues(selectedYear, editTarget, now, timezone),
  });

  useEffect(() => {
    if (open) {
      form.reset(
        buildHolidayInitialValues(selectedYear, editTarget, nowRef.current, timezone),
      );
      setSubmitError(null);
    }
  }, [open, editTarget, selectedYear, timezone, form]);

  const upsertHolidayMutation = useMutation({
    mutationFn: (values: HolidayFormValues) =>
      upsertSiteAccountingHoliday(siteId, csrfToken, {
        date: values.date.trim(),
        dayType: values.dayType,
        label: values.label.trim(),
      }),
    onSuccess: async (saved) => {
      await invalidateAfterHolidayMutation(queryClient, siteId);
      const message = saved.recalcJobId
        ? `已保存 ${saved.holidayDate} 日历覆盖，后台重算任务已加入队列`
        : `已保存 ${saved.holidayDate} 日历覆盖`;
      onNotice(message);
      toast.success(message);
      onOpenChange(false);
    },
    onError: (error) => {
      setSubmitError(
        describeAccountingConfigError(error, '保存站点节假日配置失败，请稍后重试'),
      );
    },
  });

  const dateError = form.formState.errors.date;
  const dayTypeError = form.formState.errors.dayType;
  const labelError = form.formState.errors.label;

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (upsertHolidayMutation.isPending) return;
        onOpenChange(nextOpen);
      }}
    >
      <DialogContent
        showCloseButton={!upsertHolidayMutation.isPending}
        onEscapeKeyDown={(event) => {
          if (upsertHolidayMutation.isPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (upsertHolidayMutation.isPending) event.preventDefault();
        }}
        className="sm:max-w-md"
      >
        <DialogHeader>
          <DialogTitle>配置站点节假日</DialogTitle>
          <DialogDescription>
            指定日期覆盖默认星期类型后，系统将按对应分时电价重新计算该日及关联时段电费。
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          noValidate
          onSubmit={form.handleSubmit((values) => {
            setSubmitError(null);
            upsertHolidayMutation.mutate(values);
          })}
        >
          {submitError ? (
            <Alert variant="destructive">
              <AlertTitle>无法保存节假日配置</AlertTitle>
              <AlertDescription>{submitError}</AlertDescription>
            </Alert>
          ) : null}

          <Field data-invalid={Boolean(dateError) || undefined}>
            <FieldLabel htmlFor="holiday-date-input">日期</FieldLabel>
            <Input
              id="holiday-date-input"
              type="text"
              inputMode="numeric"
              placeholder="YYYY-MM-DD"
              readOnly={Boolean(editTarget)}
              disabled={upsertHolidayMutation.isPending}
              aria-invalid={dateError ? 'true' : 'false'}
              aria-describedby={describedBy(
                'holiday-date-desc',
                dateError ? 'holiday-date-error' : undefined,
              )}
              className={
                editTarget ? 'bg-muted text-muted-foreground tabular-nums' : 'tabular-nums'
              }
              onKeyDown={preventImeSubmit}
              {...form.register('date')}
            />
            <FieldDescription id="holiday-date-desc">
              按站点本地日期填写，格式如 2026-10-01。
            </FieldDescription>
            <FieldError id="holiday-date-error" errors={[dateError]} />
          </Field>

          <Field data-invalid={Boolean(dayTypeError) || undefined}>
            <FieldLabel htmlFor="holiday-day-type-select">日期类型</FieldLabel>
            <Controller
              control={form.control}
              name="dayType"
              render={({ field }) => (
                <Select
                  value={field.value}
                  disabled={upsertHolidayMutation.isPending}
                  onValueChange={(value) => {
                    if (isOneOf(SITE_CALENDAR_DAY_TYPES, value)) {
                      field.onChange(value);
                      field.onBlur();
                    }
                  }}
                >
                  <SelectTrigger
                    id="holiday-day-type-select"
                    aria-label="日期类型"
                    aria-invalid={dayTypeError ? 'true' : 'false'}
                    aria-describedby={dayTypeError ? 'holiday-day-type-error' : undefined}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {SITE_CALENDAR_DAY_TYPES.map((type) => (
                      <SelectItem key={type} value={type}>
                        {SITE_CALENDAR_DAY_TYPE_DESCRIPTION[type]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            />
            <FieldError id="holiday-day-type-error" errors={[dayTypeError]} />
          </Field>

          <Field data-invalid={Boolean(labelError) || undefined}>
            <FieldLabel htmlFor="holiday-label-input">说明</FieldLabel>
            <Input
              id="holiday-label-input"
              type="text"
              placeholder="例如：国庆节放假、春节调休补班"
              disabled={upsertHolidayMutation.isPending}
              aria-invalid={labelError ? 'true' : 'false'}
              aria-describedby={labelError ? 'holiday-label-error' : undefined}
              onKeyDown={preventImeSubmit}
              {...form.register('label')}
            />
            <FieldError id="holiday-label-error" errors={[labelError]} />
          </Field>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={upsertHolidayMutation.isPending}
              onClick={() => onOpenChange(false)}
            >
              取消
            </Button>
            <Button type="submit" disabled={upsertHolidayMutation.isPending}>
              {upsertHolidayMutation.isPending ? '正在保存…' : '保存节假日'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
