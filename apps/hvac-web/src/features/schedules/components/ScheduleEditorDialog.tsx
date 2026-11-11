import { useEffect, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Copy, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { Controller, useFieldArray, useForm } from 'react-hook-form';
import { toast } from 'sonner';
import { z } from 'zod';
import type { OperatingSchedule, OperatingScheduleSlotInput } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
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
import { isRevisionConflictError, saveOperatingSchedule, scheduleErrorMessage, scheduleKeys } from '../query';
import {
  createClientUuidV7,
  DAY_OF_WEEK_LABELS,
  findOverlappingMinuteSlot,
  formatMinuteOfDay,
  mergeWeeklyScheduleSpans,
  splitScheduleSlotDraft,
  validateCoolingSlotRowInput,
} from '../schedule-model';

const slotRowSchema = z.object({
  dayOfWeek: z.number().int().min(1).max(7),
  startTime: z.string().min(1, '请填写开始时间'),
  endTime: z.string().min(1, '请填写结束时间'),
  crossesMidnight: z.boolean(),
  targetSupplyTempInput: z.string(),
  spanGroupId: z.string().optional(),
});

const scheduleEditorSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, '请填写 1–100 字以内的周运行计划名称')
    .max(100, '计划名称不能超过 100 字'),
  preCoolLeadMinutes: z.coerce
    .number({ invalid_type_error: '预冷提前时间须为 0–120 之间的整数分钟' })
    .int('预冷提前时间须为整数分钟')
    .min(0, '预冷提前时间不能小于 0 分钟')
    .max(120, '预冷提前时间不能超过 120 分钟'),
  coastDownLagMinutes: z.coerce
    .number({ invalid_type_error: '停机滞后时间须为 0–60 之间的整数分钟' })
    .int('停机滞后时间须为整数分钟')
    .min(0, '停机滞后时间不能小于 0 分钟')
    .max(60, '停机滞后时间不能超过 60 分钟'),
  rows: z.array(slotRowSchema),
  reason: z
    .string()
    .trim()
    .min(1, '请填写本次调整原因，以便写入运行审计记录')
    .max(500, '调整原因不能超过 500 字'),
});

type ScheduleEditorFormInput = z.input<typeof scheduleEditorSchema>;
type ScheduleEditorFormValues = z.output<typeof scheduleEditorSchema>;

function toDefaultValues(schedule: OperatingSchedule): ScheduleEditorFormValues {
  const merged = mergeWeeklyScheduleSpans(schedule.slots);
  return {
    name: schedule.name || '标准供冷周计划',
    preCoolLeadMinutes: schedule.preCoolLeadMinutes,
    coastDownLagMinutes: schedule.coastDownLagMinutes,
    rows: merged.map((span) => ({
      dayOfWeek: span.dayOfWeek,
      startTime: formatMinuteOfDay(span.startMinute),
      endTime: formatMinuteOfDay(span.endMinute),
      crossesMidnight: span.crossesMidnight,
      targetSupplyTempInput:
        span.targetSupplyTempC !== undefined ? span.targetSupplyTempC.toFixed(1) : '',
      ...(span.spanGroupId ? { spanGroupId: span.spanGroupId } : {}),
    })),
    reason: '',
  };
}

export interface ScheduleEditorDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly schedule: OperatingSchedule;
  readonly onReloadLatest: () => Promise<OperatingSchedule | undefined>;
}

export function ScheduleEditorDialog({
  open,
  onOpenChange,
  siteId,
  csrfToken,
  schedule,
  onReloadLatest,
}: ScheduleEditorDialogProps) {
  const queryClient = useQueryClient();
  const [slotError, setSlotError] = useState<string | null>(null);
  const [conflictDetected, setConflictDetected] = useState(false);

  const form = useForm<ScheduleEditorFormInput, unknown, ScheduleEditorFormValues>({
    resolver: zodResolver(scheduleEditorSchema),
    mode: 'onBlur',
    defaultValues: toDefaultValues(schedule),
  });

  const rowsField = useFieldArray({
    control: form.control,
    name: 'rows',
  });

  useEffect(() => {
    if (!open) return;
    form.reset(toDefaultValues(schedule));
    setSlotError(null);
    setConflictDetected(false);
  }, [open, schedule, form]);

  const mutation = useMutation({
    mutationFn: (payload: {
      readonly values: ScheduleEditorFormValues;
      readonly slots: OperatingScheduleSlotInput[];
    }) =>
      saveOperatingSchedule(
        siteId,
        {
          name: payload.values.name,
          preCoolLeadMinutes: payload.values.preCoolLeadMinutes,
          coastDownLagMinutes: payload.values.coastDownLagMinutes,
          slots: payload.slots,
          meta: {
            expectedRevision: schedule.revision,
            idempotencyKey: `sched-save-${createClientUuidV7()}`,
            reason: payload.values.reason,
          },
        },
        csrfToken,
      ),
    onSuccess: (saved) => {
      queryClient.setQueryData(scheduleKeys.schedule(siteId), {
        data: saved,
        requestId: null,
        traceparent: null,
        auditMessageId: null,
        routePolicyRevision: null,
        location: null,
      });
      void queryClient.invalidateQueries({ queryKey: scheduleKeys.schedule(siteId) });
      toast.success('已保存周运行计划');
      onOpenChange(false);
    },
    onError: (error) => {
      if (isRevisionConflictError(error)) {
        setConflictDetected(true);
      }
      setSlotError(scheduleErrorMessage(error, '保存周运行计划失败，请稍后重试', 'schedule'));
    },
  });

  const handleFillWorkdayPreset = () => {
    rowsField.replace(
      [1, 2, 3, 4, 5].map((dayOfWeek) => ({
        dayOfWeek,
        startTime: '08:00',
        endTime: '21:00',
        crossesMidnight: false,
        targetSupplyTempInput: '7.0',
      })),
    );
    setSlotError(null);
  };

  const handleCopyToWorkdays = (index: number) => {
    const allRows = form.getValues('rows') ?? [];
    const sourceRow = allRows[index];
    if (!sourceRow) return;
    const keptWeekend = allRows.filter((item) => item.dayOfWeek >= 6);
    const copied = [1, 2, 3, 4, 5].map((dayOfWeek) => ({
      dayOfWeek,
      startTime: sourceRow.startTime,
      endTime: sourceRow.endTime,
      crossesMidnight: sourceRow.crossesMidnight,
      targetSupplyTempInput: sourceRow.targetSupplyTempInput,
    }));
    rowsField.replace([...copied, ...keptWeekend]);
    setSlotError(null);
    toast.info('已将该供冷时段复制到周一至周五');
  };

  const handleReloadConflict = async () => {
    const latest = await onReloadLatest();
    if (latest) {
      form.reset(toDefaultValues(latest));
      setConflictDetected(false);
      setSlotError(null);
      toast.info('已加载最新周运行计划，请核对后重新保存');
    }
  };

  const onSubmit = (values: ScheduleEditorFormValues) => {
    setSlotError(null);
    const compiledSlots: OperatingScheduleSlotInput[] = [];

    for (let idx = 0; idx < values.rows.length; idx += 1) {
      const row = values.rows[idx];
      const dayLabel = DAY_OF_WEEK_LABELS[row.dayOfWeek] ?? `第 ${idx + 1} 行`;
      const checked = validateCoolingSlotRowInput({
        rowLabel: dayLabel,
        startTime: row.startTime,
        endTime: row.endTime,
        crossesMidnight: row.crossesMidnight,
        allowCrossMidnight: true,
        targetSupplyTempInput: row.targetSupplyTempInput,
      });
      if (!checked.ok) {
        setSlotError(checked.message);
        return;
      }

      compiledSlots.push(
        ...splitScheduleSlotDraft({
          dayOfWeek: row.dayOfWeek,
          startMinute: checked.startMinute,
          endMinute: checked.endMinute,
          crossesMidnight: row.crossesMidnight,
          ...(checked.targetSupplyTempC !== undefined
            ? { targetSupplyTempC: checked.targetSupplyTempC }
            : {}),
          ...(row.spanGroupId ? { spanGroupId: row.spanGroupId } : {}),
        }),
      );
    }

    for (let day = 1; day <= 7; day += 1) {
      const daySlots = compiledSlots.filter((slot) => slot.dayOfWeek === day);
      const overlap = findOverlappingMinuteSlot(daySlots);
      if (overlap) {
        setSlotError(
          `${DAY_OF_WEEK_LABELS[day]} 的供冷时段存在重叠（${formatMinuteOfDay(overlap[0].startMinute)}–${formatMinuteOfDay(overlap[0].endMinute)} 与 ${formatMinuteOfDay(overlap[1].startMinute)}–${formatMinuteOfDay(overlap[1].endMinute)}），请调整后再保存`,
        );
        return;
      }
    }

    mutation.mutate({ values, slots: compiledSlots });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!mutation.isPending) onOpenChange(next);
      }}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            {schedule.revision === 0 ? '如何配置站点周运行计划？' : '如何调整站点周运行计划？'}
          </DialogTitle>
          <DialogDescription>
            按站点时区（{schedule.timezone}）设定每周各日预期供冷窗口。目标冷冻水供水温度仅作冷站值班交接参考，平台不会据此自动下发设备指令。
          </DialogDescription>
        </DialogHeader>

        <form className="space-y-4" onSubmit={form.handleSubmit(onSubmit)}>
          {conflictDetected ? (
            <Alert variant="destructive">
              <AlertTitle>检测到版本冲突</AlertTitle>
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>其他成员刚刚更新了本站点的周运行计划。</span>
                <Button type="button" size="sm" variant="outline" onClick={() => void handleReloadConflict()}>
                  <RefreshCw data-icon="inline-start" />
                  刷新最新计划
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Field data-invalid={Boolean(form.formState.errors.name)}>
              <FieldLabel htmlFor="schedule-name">计划名称</FieldLabel>
              <Input
                id="schedule-name"
                maxLength={100}
                placeholder="例如：标准供冷周计划"
                {...form.register('name')}
              />
              {form.formState.errors.name ? (
                <FieldError errors={[form.formState.errors.name]} />
              ) : null}
            </Field>

            <Field data-invalid={Boolean(form.formState.errors.preCoolLeadMinutes)}>
              <FieldLabel htmlFor="schedule-precool">预冷提前窗（分钟）</FieldLabel>
              <Input
                id="schedule-precool"
                type="number"
                min={0}
                max={120}
                step={5}
                className="tabular-nums"
                {...form.register('preCoolLeadMinutes')}
              />
              {form.formState.errors.preCoolLeadMinutes ? (
                <FieldError errors={[form.formState.errors.preCoolLeadMinutes]} />
              ) : (
                <FieldDescription>范围 0–120 分钟</FieldDescription>
              )}
            </Field>

            <Field data-invalid={Boolean(form.formState.errors.coastDownLagMinutes)}>
              <FieldLabel htmlFor="schedule-coastdown">停机滞后窗（分钟）</FieldLabel>
              <Input
                id="schedule-coastdown"
                type="number"
                min={0}
                max={60}
                step={5}
                className="tabular-nums"
                {...form.register('coastDownLagMinutes')}
              />
              {form.formState.errors.coastDownLagMinutes ? (
                <FieldError errors={[form.formState.errors.coastDownLagMinutes]} />
              ) : (
                <FieldDescription>范围 0–60 分钟</FieldDescription>
              )}
            </Field>
          </div>

          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <span className="text-xs font-medium text-foreground">供冷时段配置</span>
                <p className="text-xs text-muted-foreground">
                  未添加供冷时段的星期默认全天停机；勾选「跨日」后时段将延续至次日凌晨并在保存时自动拆分为两段。
                </p>
              </div>
              <div className="flex items-center gap-2">
                {rowsField.fields.length === 0 ? (
                  <Button type="button" variant="outline" size="sm" onClick={handleFillWorkdayPreset}>
                    填充工作日 08:00–21:00
                  </Button>
                ) : null}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    rowsField.append({
                      dayOfWeek: 1,
                      startTime: '08:00',
                      endTime: '21:00',
                      crossesMidnight: false,
                      targetSupplyTempInput: '7.0',
                    })
                  }
                >
                  <Plus data-icon="inline-start" />
                  添加供冷时段
                </Button>
              </div>
            </div>

            {rowsField.fields.length === 0 ? (
              <div className="rounded-md border border-dashed border-border px-4 py-6 text-center text-sm text-muted-foreground">
                当前未配置任何供冷时段（全周预期停机）。点击上方按钮添加供冷时段。
              </div>
            ) : (
              <div className="space-y-2 rounded-md border border-border p-3">
                {rowsField.fields.map((field, index) => {
                  const crossesMidnight = Boolean(form.watch(`rows.${index}.crossesMidnight`));
                  return (
                    <div
                      key={field.id}
                      className="grid grid-cols-1 items-center gap-2 border-b border-divider pb-2 last:border-b-0 last:pb-0 sm:grid-cols-[104px_108px_108px_84px_132px_1fr]"
                    >
                      <Controller
                        control={form.control}
                        name={`rows.${index}.dayOfWeek`}
                        render={({ field: selectField }) => (
                          <Select
                            value={String(selectField.value)}
                            onValueChange={(val) => selectField.onChange(Number(val))}
                          >
                            <SelectTrigger aria-label={`第 ${index + 1} 行星期`}>
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {[1, 2, 3, 4, 5, 6, 7].map((day) => (
                                <SelectItem key={day} value={String(day)}>
                                  {DAY_OF_WEEK_LABELS[day]}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}
                      />

                      <Input
                        aria-label={`第 ${index + 1} 行开始时间`}
                        placeholder="08:00"
                        className="tabular-nums"
                        {...form.register(`rows.${index}.startTime`)}
                      />

                      <Input
                        aria-label={`第 ${index + 1} 行结束时间`}
                        placeholder={crossesMidnight ? '02:00' : '21:00'}
                        className="tabular-nums"
                        {...form.register(`rows.${index}.endTime`)}
                      />

                      <Controller
                        control={form.control}
                        name={`rows.${index}.crossesMidnight`}
                        render={({ field: checkField }) => (
                          <label className="inline-flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground">
                            <Checkbox
                              checked={Boolean(checkField.value)}
                              onCheckedChange={(checked) => checkField.onChange(checked === true)}
                              aria-label={`第 ${index + 1} 行跨日`}
                            />
                            <span>跨日</span>
                          </label>
                        )}
                      />

                      <Input
                        aria-label={`第 ${index + 1} 行参考供水温度`}
                        type="number"
                        min={4}
                        max={20}
                        step={0.1}
                        placeholder="参考 °C"
                        className="tabular-nums"
                        {...form.register(`rows.${index}.targetSupplyTempInput`)}
                      />

                      <div className="flex items-center justify-end gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          title="将该时段复制到周一至周五"
                          onClick={() => handleCopyToWorkdays(index)}
                        >
                          <Copy className="size-3.5" aria-hidden="true" />
                          <span className="sr-only sm:not-sr-only sm:ml-1 sm:text-xs">工作日</span>
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          aria-label={`删除第 ${index + 1} 行时段`}
                          onClick={() => rowsField.remove(index)}
                        >
                          <Trash2 className="size-4 text-muted-foreground hover:text-destructive" />
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>

          <Field data-invalid={Boolean(form.formState.errors.reason)}>
            <FieldLabel htmlFor="schedule-reason">变更原因</FieldLabel>
            <Textarea
              id="schedule-reason"
              rows={2}
              maxLength={500}
              placeholder="例如：按园区夏季作息将工作日供冷时间定为 08:00–21:00"
              {...form.register('reason')}
            />
            {form.formState.errors.reason ? (
              <FieldError errors={[form.formState.errors.reason]} />
            ) : (
              <FieldDescription>保存后将随版本号一起记入审计记录</FieldDescription>
            )}
          </Field>

          {slotError ? (
            <p role="alert" className="text-xs text-destructive">
              {slotError}
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
              {mutation.isPending ? '正在保存…' : '保存周计划'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
