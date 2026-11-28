import { useEffect, useMemo, useRef, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Controller, useForm, useWatch } from 'react-hook-form';
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
import { Field, FieldError, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { siteInputValue } from '@/features/savings/savings-model';
import { isOneOf } from '@/lib/typed';
import {
  parseSiteMomentInput,
  previewCloudMultiplier,
  RATIO_APPLICATION_MODES,
  RATIO_APPLICATION_MODE_LABEL,
  RATIO_READING_UNITS,
  RATIO_READING_UNIT_LABEL,
  validateRatioForm,
} from '../accounting-config-model';
import { createSiteMeterRatioVersion, describeAccountingConfigError } from '../query';
import {
  formatMultiplier,
  invalidateAfterRatioMutation,
  type TelemetryPointOption,
} from './accounting-config-helpers';
import { preventImeSubmit } from './billing-form-helpers';

function isPositiveNumericString(raw: string): boolean {
  const trimmed = raw.trim();
  if (!trimmed) return false;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) && parsed > 0;
}

function createRatioFormSchema(timezone: string) {
  return z
    .object({
      pointId: z.string().trim().min(1, { message: '请选择或填写点位 ID' }),
      ctRatio: z
        .string()
        .trim()
        .refine(isPositiveNumericString, { message: 'CT 变比须为大于 0 的数字' }),
      ptRatio: z
        .string()
        .trim()
        .refine(isPositiveNumericString, { message: 'PT 变比须为大于 0 的数字' }),
      meterMultiplier: z.string().trim().refine(
        (v) => v === '' || isPositiveNumericString(v),
        { message: '附加乘数须为大于 0 的数字' },
      ),
      ratioApplicationMode: z.enum(['PLATFORM_APPLIED', 'DEVICE_APPLIED', 'EDGE_APPLIED']),
      readingUnit: z.enum(['Wh', 'kWh', 'MWh', 'GJ', 'MJ', 'RTh']),
      effectiveFrom: z
        .string()
        .trim()
        .refine((v) => parseSiteMomentInput(v, timezone) !== null, {
          message: '请输入有效的生效开始时间',
        }),
      effectiveTo: z.string().trim(),
      reason: z.string().trim().max(500, { message: '变更说明不能超过 500 字' }),
    })
    .superRefine((values, ctx) => {
      if (!values.effectiveTo) return;
      const effectiveToDate = parseSiteMomentInput(values.effectiveTo, timezone);
      if (!effectiveToDate) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['effectiveTo'],
          message: '请输入有效的生效结束时间',
        });
        return;
      }
      const effectiveFromDate = parseSiteMomentInput(values.effectiveFrom, timezone);
      if (effectiveFromDate && effectiveToDate.getTime() <= effectiveFromDate.getTime()) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['effectiveTo'],
          message: '生效结束时间必须晚于生效开始时间',
        });
      }
    });
}

type CreateRatioFormValues = z.infer<ReturnType<typeof createRatioFormSchema>>;

function buildRatioInitialValues(
  defaultPointId: string,
  now: Date,
  timezone: string,
): CreateRatioFormValues {
  return {
    pointId: defaultPointId,
    ctRatio: '1',
    ptRatio: '1',
    meterMultiplier: '1',
    ratioApplicationMode: 'PLATFORM_APPLIED',
    readingUnit: 'kWh',
    effectiveFrom: siteInputValue(now, timezone),
    effectiveTo: '',
    reason: '',
  };
}

export interface CreateMeterRatioDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly now: Date;
  readonly pointOptions: readonly TelemetryPointOption[];
  readonly onNotice: (message: string) => void;
}

export function CreateMeterRatioDialog({
  open,
  onOpenChange,
  siteId,
  timezone,
  csrfToken,
  now,
  pointOptions,
  onNotice,
}: CreateMeterRatioDialogProps) {
  const queryClient = useQueryClient();
  const [manualPointMode, setManualPointMode] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const nowRef = useRef(now);
  nowRef.current = now;
  const pointOptionsRef = useRef(pointOptions);
  pointOptionsRef.current = pointOptions;

  const schema = useMemo(() => createRatioFormSchema(timezone), [timezone]);

  const form = useForm<CreateRatioFormValues>({
    resolver: zodResolver(schema),
    mode: 'onBlur',
    reValidateMode: 'onChange',
    defaultValues: buildRatioInitialValues(pointOptions[0]?.id ?? '', now, timezone),
  });

  useEffect(() => {
    if (open) {
      const currentOptions = pointOptionsRef.current;
      setManualPointMode(currentOptions.length === 0);
      form.reset(
        buildRatioInitialValues(currentOptions[0]?.id ?? '', nowRef.current, timezone),
      );
      setSubmitError(null);
    }
  }, [open, timezone, form]);

  // Sync default point selection when asset model finishes loading after dialog opens.
  useEffect(() => {
    if (open && !manualPointMode && !form.getValues('pointId') && pointOptions.length > 0) {
      form.setValue('pointId', pointOptions[0]?.id ?? '');
    }
  }, [open, manualPointMode, pointOptions, form]);

  const watchedCtRatio = useWatch({ control: form.control, name: 'ctRatio' });
  const watchedPtRatio = useWatch({ control: form.control, name: 'ptRatio' });
  const watchedMultiplier = useWatch({ control: form.control, name: 'meterMultiplier' });
  const watchedMode = useWatch({ control: form.control, name: 'ratioApplicationMode' });

  const liveCloudMultiplier = useMemo(
    () =>
      previewCloudMultiplier({
        ctRatio: Number(watchedCtRatio),
        ptRatio: Number(watchedPtRatio),
        meterMultiplier: watchedMultiplier.trim() === '' ? 1 : Number(watchedMultiplier),
        ratioApplicationMode: watchedMode,
      }),
    [watchedCtRatio, watchedPtRatio, watchedMultiplier, watchedMode],
  );

  const createRatioMutation = useMutation({
    mutationFn: (payload: Parameters<typeof createSiteMeterRatioVersion>[2]) =>
      createSiteMeterRatioVersion(siteId, csrfToken, payload),
    onSuccess: async (created) => {
      await invalidateAfterRatioMutation(queryClient, siteId);
      const message = created.recalcJobId
        ? '已创建电表倍率版本，后台重算任务已加入队列'
        : '已创建电表倍率版本';
      onNotice(message);
      notifySuccess(message);
      onOpenChange(false);
    },
    onError: (error) => {
      setSubmitError(
        describeAccountingConfigError(error, '创建电表倍率版本失败，请稍后重试'),
      );
    },
  });

  const errors = form.formState.errors;

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (createRatioMutation.isPending) return;
        onOpenChange(nextOpen);
      }}
    >
      <DialogContent
        showCloseButton={!createRatioMutation.isPending}
        onEscapeKeyDown={(event) => {
          if (createRatioMutation.isPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (createRatioMutation.isPending) event.preventDefault();
        }}
        className="sm:max-w-xl"
      >
        <DialogHeader>
          <DialogTitle>新增电表倍率版本</DialogTitle>
          <DialogDescription>
            为指定计量测点配置互感器变比、单位与生效时间段。同一测点的已发布生效时间段不能重叠。
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          noValidate
          onSubmit={form.handleSubmit((values) => {
            setSubmitError(null);
            const validated = validateRatioForm(values, timezone);
            if (!validated.ok) {
              form.setError(validated.field, { message: validated.message });
              return;
            }
            createRatioMutation.mutate(validated.value);
          })}
        >
          {submitError ? (
            <Alert variant="destructive">
              <AlertTitle>无法创建倍率版本</AlertTitle>
              <AlertDescription>{submitError}</AlertDescription>
            </Alert>
          ) : null}

          <Field data-invalid={Boolean(errors.pointId) || undefined}>
            <div className="flex items-center justify-between gap-2">
              <FieldLabel htmlFor="ratio-point-id">
                {!manualPointMode && pointOptions.length > 0 ? '计量点位' : '点位编号'}
              </FieldLabel>
              {pointOptions.length > 0 ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  disabled={createRatioMutation.isPending}
                  onClick={() => setManualPointMode((prev) => !prev)}
                >
                  {manualPointMode ? '从站点点位选择' : '手动填写编号'}
                </Button>
              ) : null}
            </div>

            {!manualPointMode && pointOptions.length > 0 ? (
              <Controller
                control={form.control}
                name="pointId"
                render={({ field }) => (
                  <Select
                    value={field.value}
                    disabled={createRatioMutation.isPending}
                    onValueChange={(value) => {
                      field.onChange(value);
                      field.onBlur();
                    }}
                  >
                    <SelectTrigger
                      id="ratio-point-id"
                      aria-label="点位 ID"
                      aria-invalid={errors.pointId ? 'true' : 'false'}
                      aria-describedby={errors.pointId ? 'ratio-point-id-error' : undefined}
                      className="w-full"
                    >
                      <SelectValue placeholder="请选择电表或冷量点位" />
                    </SelectTrigger>
                    <SelectContent>
                      {pointOptions.map((option) => (
                        <SelectItem key={option.id} value={option.id}>
                          {option.label}（{option.sublabel}）
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
              />
            ) : (
              <Input
                id="ratio-point-id"
                type="text"
                placeholder="请输入点位编号"
                autoCorrect="off"
                autoCapitalize="none"
                spellCheck={false}
                disabled={createRatioMutation.isPending}
                aria-invalid={errors.pointId ? 'true' : 'false'}
                aria-describedby={errors.pointId ? 'ratio-point-id-error' : undefined}
                className="font-mono text-xs"
                onKeyDown={preventImeSubmit}
                {...form.register('pointId')}
              />
            )}
            <FieldError id="ratio-point-id-error" errors={[errors.pointId]} />
          </Field>

          <div className="grid gap-3 sm:grid-cols-3">
            <Field data-invalid={Boolean(errors.ctRatio) || undefined}>
              <FieldLabel htmlFor="ratio-ct-input">CT 变比</FieldLabel>
              <Input
                id="ratio-ct-input"
                type="text"
                inputMode="decimal"
                disabled={createRatioMutation.isPending}
                aria-invalid={errors.ctRatio ? 'true' : 'false'}
                aria-describedby={errors.ctRatio ? 'ratio-ct-error' : undefined}
                className="tabular-nums"
                onKeyDown={preventImeSubmit}
                {...form.register('ctRatio')}
              />
              <FieldError id="ratio-ct-error" errors={[errors.ctRatio]} />
            </Field>

            <Field data-invalid={Boolean(errors.ptRatio) || undefined}>
              <FieldLabel htmlFor="ratio-pt-input">PT 变比</FieldLabel>
              <Input
                id="ratio-pt-input"
                type="text"
                inputMode="decimal"
                disabled={createRatioMutation.isPending}
                aria-invalid={errors.ptRatio ? 'true' : 'false'}
                aria-describedby={errors.ptRatio ? 'ratio-pt-error' : undefined}
                className="tabular-nums"
                onKeyDown={preventImeSubmit}
                {...form.register('ptRatio')}
              />
              <FieldError id="ratio-pt-error" errors={[errors.ptRatio]} />
            </Field>

            <Field data-invalid={Boolean(errors.meterMultiplier) || undefined}>
              <FieldLabel htmlFor="ratio-mult-input">附加乘数</FieldLabel>
              <Input
                id="ratio-mult-input"
                type="text"
                inputMode="decimal"
                disabled={createRatioMutation.isPending}
                aria-invalid={errors.meterMultiplier ? 'true' : 'false'}
                aria-describedby={errors.meterMultiplier ? 'ratio-mult-error' : undefined}
                className="tabular-nums"
                onKeyDown={preventImeSubmit}
                {...form.register('meterMultiplier')}
              />
              <FieldError id="ratio-mult-error" errors={[errors.meterMultiplier]} />
            </Field>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <Field data-invalid={Boolean(errors.ratioApplicationMode) || undefined}>
              <FieldLabel htmlFor="ratio-app-mode-select">应用层级</FieldLabel>
              <Controller
                control={form.control}
                name="ratioApplicationMode"
                render={({ field }) => (
                  <Select
                    value={field.value}
                    disabled={createRatioMutation.isPending}
                    onValueChange={(value) => {
                      if (isOneOf(RATIO_APPLICATION_MODES, value)) {
                        field.onChange(value);
                        field.onBlur();
                      }
                    }}
                  >
                    <SelectTrigger
                      id="ratio-app-mode-select"
                      aria-label="应用层级"
                      aria-invalid={errors.ratioApplicationMode ? 'true' : 'false'}
                      aria-describedby={
                        errors.ratioApplicationMode ? 'ratio-app-mode-error' : undefined
                      }
                      className="w-full"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {RATIO_APPLICATION_MODES.map((mode) => (
                        <SelectItem key={mode} value={mode}>
                          {RATIO_APPLICATION_MODE_LABEL[mode]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
              />
              <FieldError id="ratio-app-mode-error" errors={[errors.ratioApplicationMode]} />
            </Field>

            <Field data-invalid={Boolean(errors.readingUnit) || undefined}>
              <FieldLabel htmlFor="ratio-unit-select">计量单位</FieldLabel>
              <Controller
                control={form.control}
                name="readingUnit"
                render={({ field }) => (
                  <Select
                    value={field.value}
                    disabled={createRatioMutation.isPending}
                    onValueChange={(value) => {
                      if (isOneOf(RATIO_READING_UNITS, value)) {
                        field.onChange(value);
                        field.onBlur();
                      }
                    }}
                  >
                    <SelectTrigger
                      id="ratio-unit-select"
                      aria-label="计量单位"
                      aria-invalid={errors.readingUnit ? 'true' : 'false'}
                      aria-describedby={errors.readingUnit ? 'ratio-unit-error' : undefined}
                      className="w-full"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {RATIO_READING_UNITS.map((unit) => (
                        <SelectItem key={unit} value={unit}>
                          {RATIO_READING_UNIT_LABEL[unit]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
              />
              <FieldError id="ratio-unit-error" errors={[errors.readingUnit]} />
            </Field>
          </div>

          <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-muted/40 px-3.5 py-2.5">
            <div>
              <div className="text-xs font-medium text-foreground">云端生效乘数</div>
              <div className="text-xs text-muted-foreground">
                {watchedMode === 'PLATFORM_APPLIED'
                  ? '按 CT 变比 × PT 变比 × 附加乘数在云端折算'
                  : '表端或边缘已完成变比计算，云端按 ×1 折算单位'}
              </div>
            </div>
            <div
              data-testid="cloud-multiplier-preview"
              className="font-mono text-base font-semibold text-foreground tabular-nums"
            >
              {liveCloudMultiplier !== null ? `×${formatMultiplier(liveCloudMultiplier)}` : '—'}
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <Field data-invalid={Boolean(errors.effectiveFrom) || undefined}>
              <FieldLabel htmlFor="ratio-effective-from">生效开始时间</FieldLabel>
              <Input
                id="ratio-effective-from"
                type="datetime-local"
                disabled={createRatioMutation.isPending}
                aria-invalid={errors.effectiveFrom ? 'true' : 'false'}
                aria-describedby={errors.effectiveFrom ? 'ratio-effective-from-error' : undefined}
                className="tabular-nums"
                {...form.register('effectiveFrom')}
              />
              <FieldError id="ratio-effective-from-error" errors={[errors.effectiveFrom]} />
            </Field>

            <Field data-invalid={Boolean(errors.effectiveTo) || undefined}>
              <FieldLabel htmlFor="ratio-effective-to">生效结束时间（选填）</FieldLabel>
              <Input
                id="ratio-effective-to"
                type="datetime-local"
                disabled={createRatioMutation.isPending}
                aria-invalid={errors.effectiveTo ? 'true' : 'false'}
                aria-describedby={errors.effectiveTo ? 'ratio-effective-to-error' : undefined}
                className="tabular-nums"
                {...form.register('effectiveTo')}
              />
              <FieldError id="ratio-effective-to-error" errors={[errors.effectiveTo]} />
            </Field>
          </div>

          <Field data-invalid={Boolean(errors.reason) || undefined}>
            <FieldLabel htmlFor="ratio-reason-input">变更说明（选填）</FieldLabel>
            <Input
              id="ratio-reason-input"
              type="text"
              placeholder="例如：更换进线电流互感器，变比由 100 调整为 200"
              disabled={createRatioMutation.isPending}
              aria-invalid={errors.reason ? 'true' : 'false'}
              aria-describedby={errors.reason ? 'ratio-reason-error' : undefined}
              onKeyDown={preventImeSubmit}
              {...form.register('reason')}
            />
            <FieldError id="ratio-reason-error" errors={[errors.reason]} />
          </Field>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={createRatioMutation.isPending}
              onClick={() => onOpenChange(false)}
            >
              取消
            </Button>
            <Button type="submit" disabled={createRatioMutation.isPending}>
              {createRatioMutation.isPending ? <Spinner data-icon="inline-start" /> : null}
              {createRatioMutation.isPending ? '正在创建…' : '创建倍率版本'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
