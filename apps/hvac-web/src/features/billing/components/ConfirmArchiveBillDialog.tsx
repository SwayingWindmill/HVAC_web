import { useEffect, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Controller, useForm, useWatch } from 'react-hook-form';
import { notifySuccess } from '@/lib/toast';
import { z } from 'zod';
import type { UtilityBill } from '@/api/generated/platformGateway.gen';
import { platformGatewayClient } from '@/api/platform-client';
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
import { Field, FieldError, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { formatBillingMonthLabel, METER_SCOPE_LABEL } from '../billing-model';
import { billingKeys, describeBillingApiError } from '../query';
import { preventImeSubmit } from './billing-form-helpers';

const DEFAULT_CONFIRM_REASON = '已完成供电局抄表电量与电费核对，确认归档';

const confirmFormSchema = z.object({
  reason: z
    .string()
    .trim()
    .min(1, { message: '请填写确认归档原因' })
    .max(500, { message: '确认原因不能超过 500 字' }),
  acknowledged: z.boolean().refine((v) => v, {
    message: '请勾选确认后再归档锁定该张结算电费单',
  }),
});

type ConfirmFormValues = z.infer<typeof confirmFormSchema>;

export interface ConfirmArchiveBillDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly bill: UtilityBill | null;
  readonly siteId: string;
  readonly csrfToken: string;
}

export function ConfirmArchiveBillDialog({
  open,
  onOpenChange,
  bill,
  siteId,
  csrfToken,
}: ConfirmArchiveBillDialogProps) {
  const queryClient = useQueryClient();
  const [confirmError, setConfirmError] = useState<string | null>(null);

  const confirmForm = useForm<ConfirmFormValues>({
    resolver: zodResolver(confirmFormSchema),
    mode: 'onBlur',
    reValidateMode: 'onChange',
    defaultValues: {
      reason: DEFAULT_CONFIRM_REASON,
      acknowledged: false,
    },
  });

  const watchedAcknowledged = useWatch({
    control: confirmForm.control,
    name: 'acknowledged',
  });

  useEffect(() => {
    if (open) {
      confirmForm.reset({
        reason: DEFAULT_CONFIRM_REASON,
        acknowledged: false,
      });
      setConfirmError(null);
    }
  }, [open, confirmForm]);

  const confirmMutation = useMutation({
    mutationFn: async (values: ConfirmFormValues) => {
      if (!bill) throw new Error('Missing bill');
      const res = await platformGatewayClient.confirmSiteUtilityBill(
        siteId,
        bill.id,
        {
          meta: {
            expectedRevision: bill.revision,
            idempotencyKey: crypto.randomUUID(),
            reason: values.reason.trim(),
          },
        },
        csrfToken,
      );
      return res.data;
    },
    onSuccess: async (confirmed) => {
      await queryClient.invalidateQueries({ queryKey: billingKeys.billsRoot(siteId) });
      notifySuccess(
        `已确认归档 ${formatBillingMonthLabel(confirmed.billingMonth)}（${METER_SCOPE_LABEL[confirmed.meterScope]}）结算电费单`,
      );
      onOpenChange(false);
    },
    onError: (error) => {
      setConfirmError(describeBillingApiError(error, '确认归档失败，请稍后重试'));
    },
  });

  const reasonError = confirmForm.formState.errors.reason;
  const ackError = confirmForm.formState.errors.acknowledged;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!confirmMutation.isPending) onOpenChange(next);
      }}
    >
      <DialogContent
        showCloseButton={!confirmMutation.isPending}
        onEscapeKeyDown={(event) => {
          if (confirmMutation.isPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (confirmMutation.isPending) event.preventDefault();
        }}
        className="sm:max-w-md"
      >
        <DialogHeader>
          <DialogTitle>确认归档该张结算电费单吗？</DialogTitle>
          <DialogDescription>
            确认归档后，该张结算电费单将锁定为正式结算记录，不可再次编辑金额或抄表周期，并将同步展示在对应月份的能源报告中。
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          noValidate
          onSubmit={confirmForm.handleSubmit((values) => {
            setConfirmError(null);
            confirmMutation.mutate(values);
          })}
        >
          {confirmError ? (
            <Alert variant="destructive">
              <AlertTitle>无法确认归档</AlertTitle>
              <AlertDescription>{confirmError}</AlertDescription>
            </Alert>
          ) : null}

          <Field data-invalid={Boolean(reasonError) || undefined}>
            <FieldLabel htmlFor="confirm-bill-reason">归档说明</FieldLabel>
            <Input
              id="confirm-bill-reason"
              disabled={confirmMutation.isPending}
              aria-invalid={reasonError ? 'true' : 'false'}
              aria-describedby={reasonError ? 'confirm-bill-reason-error' : undefined}
              onKeyDown={preventImeSubmit}
              {...confirmForm.register('reason')}
            />
            <FieldError id="confirm-bill-reason-error" errors={[reasonError]} />
          </Field>

          <Field data-invalid={Boolean(ackError) || undefined}>
            <Controller
              control={confirmForm.control}
              name="acknowledged"
              render={({ field }) => (
                <label
                  htmlFor="confirm-bill-ack"
                  className="flex cursor-pointer items-start gap-2.5 rounded-md border border-border bg-muted/30 p-3 text-xs text-foreground"
                >
                  <Checkbox
                    id="confirm-bill-ack"
                    checked={field.value}
                    disabled={confirmMutation.isPending}
                    aria-invalid={ackError ? 'true' : 'false'}
                    aria-describedby={ackError ? 'confirm-bill-ack-error' : undefined}
                    onCheckedChange={(checked) => {
                      field.onChange(checked === true);
                      field.onBlur();
                    }}
                    className="mt-0.5"
                  />
                  <span>我已核对票面四项金额与抄表起止日期，知晓确认归档后永久锁定不可修改</span>
                </label>
              )}
            />
            <FieldError id="confirm-bill-ack-error" errors={[ackError]} />
          </Field>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={confirmMutation.isPending}
              onClick={() => onOpenChange(false)}
            >
              取消
            </Button>
            <Button type="submit" disabled={confirmMutation.isPending || !watchedAcknowledged}>
              {confirmMutation.isPending ? '正在归档…' : '确认归档'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
