import { useEffect, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2 } from 'lucide-react';
import { Controller, useFieldArray, useForm } from 'react-hook-form';
import { toast } from 'sonner';
import { z } from 'zod';
import type {
  OperatingExceptionKind,
  OperatingExceptionSlot,
} from '@/api/generated/platformGateway.gen';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
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
import { createOperatingException, scheduleErrorMessage, scheduleKeys } from '../query';
import {
  createClientUuidV7,
  EXCEPTION_KIND_LABELS,
  findOverlappingMinuteSlot,
  formatMinuteOfDay,
  validateCoolingSlotRowInput,
} from '../schedule-model';

const replacementSlotSchema = z.object({
  startTime: z.string().min(1, '请填写开始时间'),
  endTime: z.string().min(1, '请填写结束时间'),
  targetSupplyTempInput: z.string(),
});

const exceptionRequestSchema = z
  .object({
    title: z
      .string()
      .trim()
      .min(1, '请填写 1–100 字以内的例外名称')
      .max(100, '例外名称不能超过 100 字'),
    kind: z.enum(['HOLIDAY_SHUTDOWN', 'OVERTIME_COOLING', 'MAINTENANCE_WINDOW']),
    startDate: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, '请选择有效的开始日期'),
    endDate: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, '请选择有效的结束日期'),
    slotRows: z.array(replacementSlotSchema),
    reason: z
      .string()
      .trim()
      .min(1, '请填写申请原因，以便审批与审计留痕')
      .max(500, '申请原因不能超过 500 字'),
    autoApprove: z.boolean(),
  })
  .refine((data) => data.startDate <= data.endDate, {
    path: ['endDate'],
    message: '结束日期不能早于开始日期',
  });

type ExceptionRequestFormValues = z.infer<typeof exceptionRequestSchema>;

export interface ExceptionRequestInitial {
  readonly kind?: OperatingExceptionKind;
  readonly startDate?: string;
  readonly endDate?: string;
}

export interface ExceptionRequestDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly defaultDate: string;
  readonly initial?: ExceptionRequestInitial | null;
  readonly canManageSchedule: boolean;
}

function buildDefaultValues(
  defaultDate: string,
  initial: ExceptionRequestInitial | null | undefined,
  canManageSchedule: boolean,
): ExceptionRequestFormValues {
  const startDate = initial?.startDate ?? defaultDate;
  return {
    title: '',
    kind: initial?.kind ?? 'HOLIDAY_SHUTDOWN',
    startDate,
    endDate: initial?.endDate ?? startDate,
    slotRows: [{ startTime: '09:00', endTime: '18:00', targetSupplyTempInput: '7.5' }],
    reason: '',
    autoApprove: canManageSchedule,
  };
}

export function ExceptionRequestDialog({
  open,
  onOpenChange,
  siteId,
  csrfToken,
  defaultDate,
  initial,
  canManageSchedule,
}: ExceptionRequestDialogProps) {
  const queryClient = useQueryClient();
  const [submitError, setSubmitError] = useState<string | null>(null);

  const form = useForm<ExceptionRequestFormValues>({
    resolver: zodResolver(exceptionRequestSchema),
    mode: 'onBlur',
    defaultValues: buildDefaultValues(defaultDate, initial, canManageSchedule),
  });

  const slotsField = useFieldArray({
    control: form.control,
    name: 'slotRows',
  });

  useEffect(() => {
    if (!open) return;
    form.reset(buildDefaultValues(defaultDate, initial, canManageSchedule));
    setSubmitError(null);
  }, [open, initial, defaultDate, canManageSchedule, form]);

  const selectedKind = form.watch('kind');
  const autoApprove = form.watch('autoApprove');

  const mutation = useMutation({
    mutationFn: (payload: {
      readonly values: ExceptionRequestFormValues;
      readonly replacementSlots: OperatingExceptionSlot[];
    }) =>
      createOperatingException(
        siteId,
        {
          title: payload.values.title,
          kind: payload.values.kind,
          startDate: payload.values.startDate,
          endDate: payload.values.endDate,
          replacementSlots: payload.replacementSlots,
          ...(canManageSchedule ? { autoApprove: payload.values.autoApprove } : {}),
          meta: {
            expectedRevision: 0,
            idempotencyKey: `exc-create-${createClientUuidV7()}`,
            reason: payload.values.reason,
          },
        },
        csrfToken,
      ),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: scheduleKeys.exceptionsRoot(siteId) });
      toast.success(
        created.status === 'APPROVED' ? '已登记并立即生效运行例外' : '已提交运行例外申请，等待审批',
      );
      onOpenChange(false);
    },
    onError: (error) => {
      setSubmitError(scheduleErrorMessage(error, '提交运行例外申请失败，请稍后重试', 'exception'));
    },
  });

  const onSubmit = (values: ExceptionRequestFormValues) => {
    setSubmitError(null);
    const compiledSlots: OperatingExceptionSlot[] = [];

    if (values.kind === 'OVERTIME_COOLING') {
      if (values.slotRows.length === 0) {
        setSubmitError('加班供冷例外至少需要填写一段当日供冷时段');
        return;
      }
      for (let i = 0; i < values.slotRows.length; i += 1) {
        const row = values.slotRows[i];
        const checked = validateCoolingSlotRowInput({
          rowLabel: `第 ${i + 1} 段加班时段`,
          startTime: row.startTime,
          endTime: row.endTime,
          crossesMidnight: false,
          allowCrossMidnight: false,
          targetSupplyTempInput: row.targetSupplyTempInput,
        });
        if (!checked.ok) {
          setSubmitError(checked.message);
          return;
        }
        compiledSlots.push({
          startMinute: checked.startMinute,
          endMinute: checked.endMinute,
          ...(checked.targetSupplyTempC !== undefined
            ? { targetSupplyTempC: checked.targetSupplyTempC }
            : {}),
        });
      }

      const overlap = findOverlappingMinuteSlot(compiledSlots);
      if (overlap) {
        setSubmitError(
          `当日供冷时段存在重叠（${formatMinuteOfDay(overlap[0].startMinute)}–${formatMinuteOfDay(overlap[0].endMinute)} 与 ${formatMinuteOfDay(overlap[1].startMinute)}–${formatMinuteOfDay(overlap[1].endMinute)}），请调整后再提交`,
        );
        return;
      }
    }

    mutation.mutate({ values, replacementSlots: compiledSlots });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!mutation.isPending) onOpenChange(next);
      }}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>要申请哪几天的运行例外？</DialogTitle>
          <DialogDescription>
            已生效的运行例外将按站点本地日历日全天（00:00–24:00）覆盖常规周运行计划，首尾日期均包含在内。
          </DialogDescription>
        </DialogHeader>

        <form className="space-y-4" onSubmit={form.handleSubmit(onSubmit)}>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field data-invalid={Boolean(form.formState.errors.title)}>
              <FieldLabel htmlFor="exc-title">例外名称</FieldLabel>
              <Input
                id="exc-title"
                maxLength={100}
                placeholder="例如：国庆假期冷站停机 / 周六展厅加班供冷"
                {...form.register('title')}
              />
              {form.formState.errors.title ? (
                <FieldError errors={[form.formState.errors.title]} />
              ) : null}
            </Field>

            <Field>
              <FieldLabel>例外类型</FieldLabel>
              <Controller
                control={form.control}
                name="kind"
                render={({ field }) => (
                  <Select value={field.value} onValueChange={field.onChange}>
                    <SelectTrigger aria-label="例外类型">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {(Object.keys(EXCEPTION_KIND_LABELS) as OperatingExceptionKind[]).map(
                        (itemKind) => (
                          <SelectItem key={itemKind} value={itemKind}>
                            {EXCEPTION_KIND_LABELS[itemKind]}
                          </SelectItem>
                        ),
                      )}
                    </SelectContent>
                  </Select>
                )}
              />
            </Field>
          </div>

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field data-invalid={Boolean(form.formState.errors.startDate)}>
              <FieldLabel htmlFor="exc-start-date">开始日期（含当日）</FieldLabel>
              <Controller
                control={form.control}
                name="startDate"
                render={({ field }) => (
                  <Input
                    id="exc-start-date"
                    type="date"
                    value={field.value}
                    onBlur={field.onBlur}
                    onChange={(event) => {
                      const next = event.target.value;
                      field.onChange(next);
                      const currentEnd = form.getValues('endDate');
                      if (currentEnd < next) {
                        form.setValue('endDate', next, { shouldValidate: true });
                      }
                    }}
                    className="tabular-nums"
                  />
                )}
              />
              {form.formState.errors.startDate ? (
                <FieldError errors={[form.formState.errors.startDate]} />
              ) : null}
            </Field>

            <Field data-invalid={Boolean(form.formState.errors.endDate)}>
              <FieldLabel htmlFor="exc-end-date">结束日期（含当日）</FieldLabel>
              <Input
                id="exc-end-date"
                type="date"
                className="tabular-nums"
                {...form.register('endDate')}
              />
              {form.formState.errors.endDate ? (
                <FieldError errors={[form.formState.errors.endDate]} />
              ) : null}
            </Field>
          </div>

          {selectedKind === 'OVERTIME_COOLING' ? (
            <div className="space-y-2 rounded-md border border-border p-3">
              <div className="flex items-center justify-between">
                <div>
                  <span className="text-xs font-medium text-foreground">加班供冷时段（每日重复）</span>
                  <p className="text-xs text-muted-foreground">
                    替代所选日期原有的常规周计划；参考供水温度仅作值班交接参考，不自动下发指令。
                  </p>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    slotsField.append({
                      startTime: '18:00',
                      endTime: '22:00',
                      targetSupplyTempInput: '7.5',
                    })
                  }
                >
                  <Plus data-icon="inline-start" />
                  添加时段
                </Button>
              </div>

              <div className="space-y-2">
                {slotsField.fields.map((field, idx) => (
                  <div
                    key={field.id}
                    className="grid grid-cols-[1fr_1fr_1fr_36px] items-center gap-2"
                  >
                    <Input
                      aria-label={`加班时段 ${idx + 1} 开始时间`}
                      placeholder="09:00"
                      className="tabular-nums"
                      {...form.register(`slotRows.${idx}.startTime`)}
                    />
                    <Input
                      aria-label={`加班时段 ${idx + 1} 结束时间`}
                      placeholder="18:00"
                      className="tabular-nums"
                      {...form.register(`slotRows.${idx}.endTime`)}
                    />
                    <Input
                      aria-label={`加班时段 ${idx + 1} 参考供水温度`}
                      type="number"
                      min={4}
                      max={20}
                      step={0.1}
                      placeholder="参考 °C"
                      className="tabular-nums"
                      {...form.register(`slotRows.${idx}.targetSupplyTempInput`)}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      disabled={slotsField.fields.length <= 1}
                      aria-label={`删除加班时段 ${idx + 1}`}
                      onClick={() => slotsField.remove(idx)}
                    >
                      <Trash2 className="size-4 text-muted-foreground" />
                    </Button>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="rounded-md border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
              所选日期将按全天（00:00–24:00）停机执行，自动屏蔽当日全部常规供冷时段（含前一日跨午夜延续至当日凌晨的时段）。
            </div>
          )}

          <Field data-invalid={Boolean(form.formState.errors.reason)}>
            <FieldLabel htmlFor="exc-reason">申请原因</FieldLabel>
            <Textarea
              id="exc-reason"
              rows={2}
              maxLength={500}
              placeholder="例如：租户周末展厅活动申请延长供冷 / 冷冻水主管阀门年度检修停机"
              {...form.register('reason')}
            />
            {form.formState.errors.reason ? (
              <FieldError errors={[form.formState.errors.reason]} />
            ) : (
              <FieldDescription>审批人员与值班人员均可在运行例外记录中查看</FieldDescription>
            )}
          </Field>

          {canManageSchedule ? (
            <Controller
              control={form.control}
              name="autoApprove"
              render={({ field }) => (
                <label className="flex cursor-pointer items-start gap-2.5 rounded-md border border-border px-3 py-2.5">
                  <Checkbox
                    checked={field.value}
                    onCheckedChange={(checked) => field.onChange(checked === true)}
                    aria-label="提交并立即生效"
                    className="mt-0.5"
                  />
                  <div className="space-y-0.5">
                    <span className="text-sm font-medium text-foreground">提交并立即生效</span>
                    <p className="text-xs text-muted-foreground">
                      取消勾选后将作为「待审批」申请提交，由其他运维主管或管理员审批生效。
                    </p>
                  </div>
                </label>
              )}
            />
          ) : null}

          {submitError ? (
            <p role="alert" className="text-xs text-destructive">
              {submitError}
            </p>
          ) : null}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={mutation.isPending}
              onClick={() => onOpenChange(false)}
            >
              取消
            </Button>
            <Button type="submit" disabled={mutation.isPending}>
              {mutation.isPending
                ? '正在提交…'
                : canManageSchedule && autoApprove
                  ? '提交并生效'
                  : '提交申请'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
