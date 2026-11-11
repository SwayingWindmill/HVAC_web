import { useEffect, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { toast } from 'sonner';
import { z } from 'zod';
import type { MonthlyElectricityBudget } from '@/api/generated/platformGateway.gen';
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
import { Textarea } from '@/components/ui/textarea';
import { formatDecimal } from '@/lib/operator-format';
import { formatBillingMonthLabel } from '../billing-model';
import {
  billingGatewayClient,
  billingKeys,
  describeBillingApiError,
  isBillingRevisionConflict,
} from '../query';

const budgetFormSchema = z.object({
  budgetAmountInput: z
    .string()
    .trim()
    .min(1, { message: '请输入月度电费预算金额' })
    .refine(
      (val) => {
        const parsed = Number(val);
        return Number.isFinite(parsed) && parsed >= 0 && parsed <= 999_999_999_999.99;
      },
      { message: '预算金额不能小于 0 元' },
    )
    .refine((val) => /^\d+(?:\.\d{1,2})?$/.test(val), {
      message: '金额最多保留两位小数',
    }),
  note: z.string().trim().max(500, { message: '预算说明不能超过 500 字' }),
  reason: z
    .string()
    .trim()
    .min(1, { message: '请填写本次设置或调整预算的原因' })
    .max(500, { message: '变更原因不能超过 500 字' }),
});

type BudgetFormValues = z.infer<typeof budgetFormSchema>;

export interface BudgetEditorDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly billingMonth: string;
  readonly currentBudget: MonthlyElectricityBudget | null;
  readonly csrfToken: string;
  readonly onRefreshBudget: () => Promise<unknown>;
}

export function BudgetEditorDialog({
  open,
  onOpenChange,
  siteId,
  billingMonth,
  currentBudget,
  csrfToken,
  onRefreshBudget,
}: BudgetEditorDialogProps) {
  const queryClient = useQueryClient();
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [revisionConflict, setRevisionConflict] = useState(false);

  const form = useForm<BudgetFormValues>({
    resolver: zodResolver(budgetFormSchema),
    mode: 'onBlur',
    defaultValues: {
      budgetAmountInput: currentBudget ? String(currentBudget.budgetAmountCny) : '',
      note: currentBudget?.note ?? '',
      reason: currentBudget ? '调整月度冷站电费预算' : '设置月度冷站电费预算',
    },
  });

  useEffect(() => {
    if (open) {
      form.reset({
        budgetAmountInput: currentBudget ? String(currentBudget.budgetAmountCny) : '',
        note: currentBudget?.note ?? '',
        reason: currentBudget ? '调整月度冷站电费预算' : '设置月度冷站电费预算',
      });
      setSubmitError(null);
      setRevisionConflict(false);
    }
  }, [open, currentBudget, billingMonth, form]);

  const saveMutation = useMutation({
    mutationFn: async (values: BudgetFormValues) => {
      const response = await billingGatewayClient.saveSiteMonthlyElectricityBudget(
        siteId,
        {
          billingMonth,
          budgetAmountCny: Number(Number(values.budgetAmountInput).toFixed(2)),
          note: values.note.trim(),
          meta: {
            expectedRevision: currentBudget?.revision ?? 0,
            idempotencyKey: crypto.randomUUID(),
            reason: values.reason.trim(),
          },
        },
        csrfToken,
      );
      return response.data;
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: billingKeys.budgetsRoot(siteId) });
      toast.success(
        `已保存 ${formatBillingMonthLabel(saved.billingMonth)} 电费预算（¥${formatDecimal(saved.budgetAmountCny, 2)}）`,
      );
      onOpenChange(false);
    },
    onError: (error) => {
      setRevisionConflict(isBillingRevisionConflict(error));
      setSubmitError(describeBillingApiError(error, '保存月度电费预算失败，请稍后重试'));
    },
  });

  const handleDialogOpenChange = (nextOpen: boolean) => {
    if (saveMutation.isPending) return;
    onOpenChange(nextOpen);
  };

  return (
    <Dialog open={open} onOpenChange={handleDialogOpenChange}>
      <DialogContent
        showCloseButton={!saveMutation.isPending}
        onEscapeKeyDown={(event) => {
          if (saveMutation.isPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (saveMutation.isPending) event.preventDefault();
        }}
        className="sm:max-w-lg"
      >
        <DialogHeader>
          <DialogTitle>如何设置月度电费预算？</DialogTitle>
          <DialogDescription>
            为 {formatBillingMonthLabel(billingMonth)} 设置冷站电费管理参考线。预算仅用于对比累计估算电费与全月预计费用，不会自动限制设备运行。
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={form.handleSubmit((values) => {
            setSubmitError(null);
            setRevisionConflict(false);
            saveMutation.mutate(values);
          })}
        >
          {submitError ? (
            <Alert variant="destructive">
              <AlertTitle>无法保存月度电费预算</AlertTitle>
              <AlertDescription className="space-y-2">
                <p>{submitError}</p>
                {revisionConflict ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      void onRefreshBudget().then(() => {
                        setRevisionConflict(false);
                        setSubmitError(null);
                      });
                    }}
                  >
                    刷新最新预算后重填
                  </Button>
                ) : null}
              </AlertDescription>
            </Alert>
          ) : null}

          <Field data-invalid={Boolean(form.formState.errors.budgetAmountInput) || undefined}>
            <FieldLabel htmlFor="budget-amount-input">月度电费预算（元）</FieldLabel>
            <Input
              id="budget-amount-input"
              inputMode="decimal"
              placeholder="例如 180000"
              disabled={saveMutation.isPending}
              {...form.register('budgetAmountInput')}
            />
            <FieldDescription>
              按站点自然月（1 日 00:00 至月末 24:00）统计，最多保留两位小数。
            </FieldDescription>
            <FieldError errors={[form.formState.errors.budgetAmountInput]} />
          </Field>

          <Field data-invalid={Boolean(form.formState.errors.note) || undefined}>
            <FieldLabel htmlFor="budget-note-input">预算口径说明（选填）</FieldLabel>
            <Textarea
              id="budget-note-input"
              rows={2}
              placeholder="例如：含夏季高温负荷上浮预留，按冷站专变口径编制"
              disabled={saveMutation.isPending}
              {...form.register('note')}
            />
            <FieldError errors={[form.formState.errors.note]} />
          </Field>

          <Field data-invalid={Boolean(form.formState.errors.reason) || undefined}>
            <FieldLabel htmlFor="budget-reason-input">变更原因</FieldLabel>
            <Input
              id="budget-reason-input"
              placeholder="例如：按年度物业能源预算核定 10 月额度"
              disabled={saveMutation.isPending}
              {...form.register('reason')}
            />
            <FieldError errors={[form.formState.errors.reason]} />
          </Field>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={saveMutation.isPending}
              onClick={() => handleDialogOpenChange(false)}
            >
              取消
            </Button>
            <Button type="submit" disabled={saveMutation.isPending}>
              {saveMutation.isPending ? '正在保存…' : '保存月度预算'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
