import { useEffect, useMemo, useRef, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
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
import { siteInputValue } from '@/features/savings/savings-model';
import { formatTime } from '@/lib/operator-format';
import { parseSiteMomentInput } from '../accounting-config-model';
import {
  describeAccountingConfigError,
  retireSiteMeterRatioVersion,
  type MeterRatioVersion,
} from '../query';
import { formatMultiplier, invalidateAfterRatioMutation } from './accounting-config-helpers';
import { describedBy, preventImeSubmit } from './billing-form-helpers';

function buildRetireRatioSchema(timezone: string, effectiveFromIso: string | undefined) {
  return z
    .object({
      effectiveTo: z
        .string()
        .trim()
        .refine((v) => parseSiteMomentInput(v, timezone) !== null, {
          message: '请输入有效的生效结束时间',
        }),
      reason: z.string().trim().max(500, { message: '终止说明不能超过 500 字' }),
    })
    .superRefine((values, ctx) => {
      if (!effectiveFromIso) return;
      const parsedEnd = parseSiteMomentInput(values.effectiveTo, timezone);
      if (!parsedEnd) return;
      const startMs = Date.parse(effectiveFromIso);
      if (Number.isFinite(startMs) && parsedEnd.getTime() <= startMs) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['effectiveTo'],
          message: '生效结束时间必须晚于该版本的生效开始时间',
        });
      }
    });
}

type RetireRatioFormValues = z.infer<ReturnType<typeof buildRetireRatioSchema>>;

export interface RetireMeterRatioDialogProps {
  readonly retiringRatio: MeterRatioVersion | null;
  readonly onClose: () => void;
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly now: Date;
  readonly onNotice: (message: string) => void;
}

export function RetireMeterRatioDialog({
  retiringRatio,
  onClose,
  siteId,
  timezone,
  csrfToken,
  now,
  onNotice,
}: RetireMeterRatioDialogProps) {
  const queryClient = useQueryClient();
  const [submitError, setSubmitError] = useState<string | null>(null);
  const nowRef = useRef(now);
  nowRef.current = now;

  const schema = useMemo(
    () => buildRetireRatioSchema(timezone, retiringRatio?.effectiveFrom),
    [timezone, retiringRatio?.effectiveFrom],
  );

  const form = useForm<RetireRatioFormValues>({
    resolver: zodResolver(schema),
    mode: 'onBlur',
    reValidateMode: 'onChange',
    defaultValues: {
      effectiveTo: siteInputValue(now, timezone),
      reason: '',
    },
  });

  useEffect(() => {
    if (retiringRatio) {
      form.reset({
        effectiveTo: siteInputValue(nowRef.current, timezone),
        reason: '',
      });
      setSubmitError(null);
    }
  }, [retiringRatio, timezone, form]);

  const retireRatioMutation = useMutation({
    mutationFn: (payload: {
      readonly versionId: string;
      readonly effectiveTo: string;
      readonly reason?: string;
    }) =>
      retireSiteMeterRatioVersion(siteId, csrfToken, payload.versionId, {
        effectiveTo: payload.effectiveTo,
        ...(payload.reason ? { reason: payload.reason } : {}),
      }),
    onSuccess: async (updated) => {
      await invalidateAfterRatioMutation(queryClient, siteId);
      const message = updated.recalcJobId
        ? '已终止该电表倍率版本，后台重算任务已加入队列'
        : '已终止该电表倍率版本';
      onNotice(message);
      notifySuccess(message);
      onClose();
    },
    onError: (error) => {
      setSubmitError(
        describeAccountingConfigError(error, '终止电表倍率版本失败，请稍后重试'),
      );
    },
  });

  const effectiveToError = form.formState.errors.effectiveTo;
  const reasonError = form.formState.errors.reason;

  return (
    <Dialog
      open={Boolean(retiringRatio)}
      onOpenChange={(nextOpen) => {
        if (retireRatioMutation.isPending) return;
        if (!nextOpen) onClose();
      }}
    >
      <DialogContent
        showCloseButton={!retireRatioMutation.isPending}
        onEscapeKeyDown={(event) => {
          if (retireRatioMutation.isPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (retireRatioMutation.isPending) event.preventDefault();
        }}
        className="sm:max-w-md"
      >
        <DialogHeader>
          <DialogTitle>终止倍率版本</DialogTitle>
          <DialogDescription>
            {retiringRatio
              ? `设置该版本（自 ${formatTime(retiringRatio.effectiveFrom, timezone)} 起生效，云端乘数 ×${formatMultiplier(retiringRatio.cloudMultiplier)}）的生效结束时间；终止后可为后续时段创建新的倍率版本。`
              : ''}
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          noValidate
          onSubmit={form.handleSubmit((values) => {
            if (!retiringRatio) return;
            setSubmitError(null);
            const parsedEnd = parseSiteMomentInput(values.effectiveTo, timezone);
            if (!parsedEnd) {
              form.setError('effectiveTo', { message: '请输入有效的生效结束时间' });
              return;
            }
            const trimmedReason = values.reason.trim();
            retireRatioMutation.mutate({
              versionId: retiringRatio.id,
              effectiveTo: parsedEnd.toISOString(),
              ...(trimmedReason ? { reason: trimmedReason } : {}),
            });
          })}
        >
          {submitError ? (
            <Alert variant="destructive">
              <AlertTitle>无法终止倍率版本</AlertTitle>
              <AlertDescription>{submitError}</AlertDescription>
            </Alert>
          ) : null}

          <Field data-invalid={Boolean(effectiveToError) || undefined}>
            <FieldLabel htmlFor="retire-effective-to">生效结束时间</FieldLabel>
            <Input
              id="retire-effective-to"
              type="datetime-local"
              disabled={retireRatioMutation.isPending}
              aria-invalid={effectiveToError ? 'true' : 'false'}
              aria-describedby={describedBy(
                'retire-effective-to-desc',
                effectiveToError ? 'retire-effective-to-error' : undefined,
              )}
              className="tabular-nums"
              {...form.register('effectiveTo')}
            />
            <FieldDescription id="retire-effective-to-desc">
              按站点时区（{timezone}）填写，采用左闭右开生效边界。
            </FieldDescription>
            <FieldError id="retire-effective-to-error" errors={[effectiveToError]} />
          </Field>

          <Field data-invalid={Boolean(reasonError) || undefined}>
            <FieldLabel htmlFor="retire-reason-input">变更说明（选填）</FieldLabel>
            <Input
              id="retire-reason-input"
              type="text"
              placeholder="例如：现场更换新互感器，终止旧倍率"
              disabled={retireRatioMutation.isPending}
              aria-invalid={reasonError ? 'true' : 'false'}
              aria-describedby={reasonError ? 'retire-reason-error' : undefined}
              onKeyDown={preventImeSubmit}
              {...form.register('reason')}
            />
            <FieldError id="retire-reason-error" errors={[reasonError]} />
          </Field>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={retireRatioMutation.isPending}
              onClick={onClose}
            >
              取消
            </Button>
            <Button
              type="submit"
              variant="destructive"
              disabled={retireRatioMutation.isPending || !retiringRatio}
            >
              {retireRatioMutation.isPending ? '正在终止…' : '确认终止'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
