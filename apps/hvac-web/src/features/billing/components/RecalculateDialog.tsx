import { useEffect, useMemo, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useForm, useWatch } from 'react-hook-form';
import { notifySuccess } from '@/lib/toast';
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
import { Textarea } from '@/components/ui/textarea';
import { siteDayKey } from '@/features/energy-analysis/consumption/model';
import { isOneOf } from '@/lib/typed';
import {
  ACCOUNTING_RECALC_GRANULARITY_LABEL,
  defaultRecalcMonthRange,
  validateRecalculateWindow,
} from '../billing-model';
import {
  billingKeys,
  describeAccountingRecalcError,
  submitSiteAccountingRecalc,
  type AccountingRecalcGranularity,
} from '../query';

const RECALC_GRANULARITIES = [
  'ALL',
  'HOUR',
  'DAY',
  'MONTH',
] as const satisfies readonly AccountingRecalcGranularity[];

const DATE_PATTERN = /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;

function buildRecalculateSchema(timezone: string, maxDate: string) {
  return z
    .object({
      fromDate: z
        .string()
        .trim()
        .regex(DATE_PATTERN, { message: '请输入有效的开始日期（YYYY-MM-DD）' }),
      toDate: z
        .string()
        .trim()
        .regex(DATE_PATTERN, { message: '请输入有效的结束日期（YYYY-MM-DD）' }),
      granularity: z.enum(['HOUR', 'DAY', 'MONTH', 'ALL']),
      reason: z
        .string()
        .trim()
        .min(1, { message: '请填写重算原因' })
        .max(500, { message: '重算原因不能超过 500 字' }),
    })
    .superRefine((values, ctx) => {
      const check = validateRecalculateWindow(values.fromDate, values.toDate, timezone, {
        maxDate,
      });
      if (!check.ok) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: [check.field],
          message: check.message,
        });
      }
    });
}

type RecalculateFormValues = {
  fromDate: string;
  toDate: string;
  granularity: AccountingRecalcGranularity;
  reason: string;
};

export interface RecalculateDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly billingMonth: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly now: Date;
}

const DEFAULT_RECALC_REASON = '更新分时电价或修正电表读数后重算电费与能耗';

export function RecalculateDialog({
  open,
  onOpenChange,
  siteId,
  billingMonth,
  timezone,
  csrfToken,
  now,
}: RecalculateDialogProps) {
  const queryClient = useQueryClient();
  const [submitError, setSubmitError] = useState<string | null>(null);

  const todayKey = useMemo(() => siteDayKey(now, timezone), [now, timezone]);
  const initialRange = useMemo(
    () => defaultRecalcMonthRange(billingMonth, now, timezone),
    [billingMonth, now, timezone],
  );
  const formSchema = useMemo(
    () => buildRecalculateSchema(timezone, todayKey),
    [timezone, todayKey],
  );

  const form = useForm<RecalculateFormValues>({
    resolver: zodResolver(formSchema),
    mode: 'onBlur',
    defaultValues: {
      fromDate: initialRange.fromDate,
      toDate: initialRange.toDate,
      granularity: 'ALL',
      reason: DEFAULT_RECALC_REASON,
    },
  });

  useEffect(() => {
    if (open) {
      form.reset({
        fromDate: initialRange.fromDate,
        toDate: initialRange.toDate,
        granularity: 'ALL',
        reason: DEFAULT_RECALC_REASON,
      });
      setSubmitError(null);
    }
  }, [open, initialRange, form]);

  const watchedGranularity = useWatch({ control: form.control, name: 'granularity' });
  const watchedFromDate = useWatch({ control: form.control, name: 'fromDate' });
  const watchedToDate = useWatch({ control: form.control, name: 'toDate' });

  const windowPreview = useMemo(
    () => validateRecalculateWindow(watchedFromDate, watchedToDate, timezone, { maxDate: todayKey }),
    [watchedFromDate, watchedToDate, timezone, todayKey],
  );

  const recalcMutation = useMutation({
    mutationFn: async (values: RecalculateFormValues) =>
      submitSiteAccountingRecalc(siteId, csrfToken, {
        fromDate: values.fromDate.trim(),
        toDate: values.toDate.trim(),
        granularity: values.granularity,
        reason: values.reason.trim(),
      }),
    onSuccess: async (_job, values) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: billingKeys.recalcJobs(siteId) }),
        queryClient.invalidateQueries({ queryKey: billingKeys.monthElectricityRoot(siteId) }),
        queryClient.invalidateQueries({
          queryKey: billingKeys.readingWindowElectricityRoot(siteId),
        }),
        queryClient.invalidateQueries({ queryKey: ['energy-series', siteId] }),
      ]);
      notifySuccess(
        `已提交重算任务（${values.fromDate} 至 ${values.toDate}），完成后将自动刷新电费与能耗数据`,
      );
      onOpenChange(false);
    },
    onError: (error) => {
      setSubmitError(describeAccountingRecalcError(error, '提交重算任务失败，请稍后重试'));
    },
  });

  const handleOpenChange = (nextOpen: boolean) => {
    if (recalcMutation.isPending) return;
    onOpenChange(nextOpen);
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        showCloseButton={!recalcMutation.isPending}
        onEscapeKeyDown={(event) => {
          if (recalcMutation.isPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (recalcMutation.isPending) event.preventDefault();
        }}
        className="sm:max-w-lg"
      >
        <DialogHeader>
          <DialogTitle>要重新计算哪段时间的电费与能耗？</DialogTitle>
          <DialogDescription>
            电价版本、节假日日历调整或补录电表读数后，可按站点当地时间发起后台重算任务。任务完成后，月度估算电费与能耗曲线将自动更新，已确认归档的供电局结算电费单不受影响。
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={form.handleSubmit((values) => {
            setSubmitError(null);
            recalcMutation.mutate(values);
          })}
        >
          {submitError ? (
            <Alert variant="destructive">
              <AlertTitle>无法提交重算任务</AlertTitle>
              <AlertDescription>{submitError}</AlertDescription>
            </Alert>
          ) : null}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field data-invalid={Boolean(form.formState.errors.fromDate) || undefined}>
              <FieldLabel htmlFor="recalc-from-date">开始日期</FieldLabel>
              <Input
                id="recalc-from-date"
                type="text"
                inputMode="numeric"
                placeholder="YYYY-MM-DD"
                disabled={recalcMutation.isPending}
                className="tabular-nums"
                {...form.register('fromDate')}
              />
              <FieldError errors={[form.formState.errors.fromDate]} />
            </Field>

            <Field data-invalid={Boolean(form.formState.errors.toDate) || undefined}>
              <FieldLabel htmlFor="recalc-to-date">结束日期（含当日）</FieldLabel>
              <Input
                id="recalc-to-date"
                type="text"
                inputMode="numeric"
                placeholder="YYYY-MM-DD"
                disabled={recalcMutation.isPending}
                className="tabular-nums"
                {...form.register('toDate')}
              />
              <FieldError errors={[form.formState.errors.toDate]} />
            </Field>
          </div>

          <Field data-invalid={Boolean(form.formState.errors.granularity) || undefined}>
            <FieldLabel htmlFor="recalc-granularity-select">重算统计粒度</FieldLabel>
            <Select
              value={watchedGranularity}
              disabled={recalcMutation.isPending}
              onValueChange={(value) => {
                if (!isOneOf(RECALC_GRANULARITIES, value)) return;
                form.setValue('granularity', value, { shouldValidate: true });
              }}
            >
              <SelectTrigger id="recalc-granularity-select" aria-label="重算统计粒度">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {RECALC_GRANULARITIES.map((option) => (
                  <SelectItem key={option} value={option}>
                    {ACCOUNTING_RECALC_GRANULARITY_LABEL[option]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FieldDescription>
              {windowPreview.ok
                ? `按站点时区（${timezone}）包含首尾共 ${windowPreview.window.dayCount} 天。`
                : '按站点本地日历日包含首尾两日，单次最多重算 366 天。'}
            </FieldDescription>
            <FieldError errors={[form.formState.errors.granularity]} />
          </Field>

          <Field data-invalid={Boolean(form.formState.errors.reason) || undefined}>
            <FieldLabel htmlFor="recalc-reason-input">重算原因</FieldLabel>
            <Textarea
              id="recalc-reason-input"
              rows={2}
              placeholder="例如：更新深谷分时电价后重算 10 月电费"
              disabled={recalcMutation.isPending}
              {...form.register('reason')}
            />
            <FieldError errors={[form.formState.errors.reason]} />
          </Field>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={recalcMutation.isPending}
              onClick={() => handleOpenChange(false)}
            >
              取消
            </Button>
            <Button type="submit" disabled={recalcMutation.isPending}>
              {recalcMutation.isPending ? '正在提交…' : '开始重算'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
