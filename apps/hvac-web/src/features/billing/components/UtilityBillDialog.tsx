import { useEffect, useMemo, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useForm, useWatch } from 'react-hook-form';
import { notifySuccess } from '@/lib/toast';
import type { UtilityBill } from '@/api/generated/platformGateway.gen';
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
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import { siteTariffQuery } from '@/api/site-tariff';
import { platformGatewayClient } from '@/api/platform-client';
import { isOneOf } from '@/lib/typed';
import { formatBillingMonthLabel, METER_SCOPE_LABEL } from '../billing-model';
import {
  billingKeys,
  describeBillingApiError,
  isBillingRevisionConflict,
} from '../query';
import {
  buildUtilityBillInitialValues,
  buildUtilityBillMutationPayload,
  defaultCycleDates,
  hasExtraTicketDetails,
  METER_SCOPES,
  utilityBillFormSchema,
  type UtilityBillFormValues,
} from '../utility-bill-form';
import { describedBy, preventImeSubmit } from './billing-form-helpers';
import { CalendarDateInput } from './CalendarDateInput';
import { UtilityBillChargesSection } from './UtilityBillChargesSection';

export interface UtilityBillDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly defaultBillingMonth: string;
  readonly editingBill: UtilityBill | null;
  readonly monthOptions: readonly string[];
  readonly csrfToken: string;
  readonly onSaved: (bill: UtilityBill) => void;
}

export function UtilityBillDialog({
  open,
  onOpenChange,
  siteId,
  defaultBillingMonth,
  editingBill,
  monthOptions,
  csrfToken,
  onSaved,
}: UtilityBillDialogProps) {
  const queryClient = useQueryClient();
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [revisionConflict, setRevisionConflict] = useState(false);
  const [extraDetailsOpen, setExtraDetailsOpen] = useState(false);
  const tariffQuery = useQuery({ ...siteTariffQuery(siteId), enabled: open && !editingBill });

  const form = useForm<UtilityBillFormValues>({
    resolver: zodResolver(utilityBillFormSchema),
    mode: 'onBlur',
    reValidateMode: 'onChange',
    defaultValues: buildUtilityBillInitialValues(editingBill, defaultBillingMonth),
  });

  useEffect(() => {
    if (open) {
      form.reset(buildUtilityBillInitialValues(editingBill, defaultBillingMonth));
      setSubmitError(null);
      setRevisionConflict(false);
      setExtraDetailsOpen(hasExtraTicketDetails(editingBill));
    }
  }, [open, editingBill, defaultBillingMonth, form]);

  useEffect(() => {
    if (!open || editingBill || !tariffQuery.data) return;
    if (form.getFieldState('basicChargeMode').isDirty) return;
    const inferred = tariffQuery.data.periods.some((period) => period.demandRatePerKW > 0)
      ? 'MAX_DEMAND_KW'
      : 'NONE';
    form.setValue('basicChargeMode', inferred);
  }, [open, editingBill, tariffQuery.data, form]);

  const watchedMeterScope = useWatch({ control: form.control, name: 'meterScope' });
  const watchedBillingMonth = useWatch({ control: form.control, name: 'billingMonth' });
  const watchedReadingStart = useWatch({ control: form.control, name: 'readingStartDate' });
  const watchedReadingEnd = useWatch({ control: form.control, name: 'readingEndDate' });

  const selectableMonths = useMemo(() => {
    const set = new Set(monthOptions);
    if (watchedBillingMonth) set.add(watchedBillingMonth);
    return Array.from(set).sort((a, b) => b.localeCompare(a));
  }, [monthOptions, watchedBillingMonth]);

  const mutation = useMutation({
    mutationFn: async (values: UtilityBillFormValues) => {
      const body = buildUtilityBillMutationPayload(values, {
        expectedRevision: editingBill?.revision ?? 0,
        idempotencyKey: crypto.randomUUID(),
      });
      if (editingBill) {
        const res = await platformGatewayClient.updateSiteUtilityBill(
          siteId,
          editingBill.id,
          body,
          csrfToken,
        );
        return res.data;
      }
      const res = await platformGatewayClient.createSiteUtilityBill(siteId, body, csrfToken);
      return res.data;
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: billingKeys.billsRoot(siteId) });
      notifySuccess(
        editingBill
          ? `已更新 ${formatBillingMonthLabel(saved.billingMonth)} 结算电费单草稿`
          : `已录入 ${formatBillingMonthLabel(saved.billingMonth)} 结算电费单草稿`,
      );
      onOpenChange(false);
      onSaved(saved);
    },
    onError: (error) => {
      setRevisionConflict(isBillingRevisionConflict(error));
      setSubmitError(describeBillingApiError(error, '保存结算电费单失败，请稍后重试'));
    },
  });

  const handleOpenChange = (nextOpen: boolean) => {
    if (mutation.isPending) return;
    onOpenChange(nextOpen);
  };

  const errors = form.formState.errors;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        showCloseButton={!mutation.isPending}
        onEscapeKeyDown={(event) => {
          if (mutation.isPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (mutation.isPending) event.preventDefault();
        }}
        className="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
      >
        <DialogHeader>
          <DialogTitle>
            {editingBill ? '修改结算电费单草稿' : '录入供电局结算电费单'}
          </DialogTitle>
          <DialogDescription>
            按供电局发票或电费结算通知单填写抄表周期与四项电费构成。保存后为草稿状态，可在右侧核对抽屉对比平台电表读数后再确认归档。
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          noValidate
          onSubmit={form.handleSubmit((values) => {
            setSubmitError(null);
            setRevisionConflict(false);
            mutation.mutate(values);
          })}
        >
          {submitError ? (
            <Alert variant="destructive">
              <AlertTitle>无法保存结算电费单</AlertTitle>
              <AlertDescription className="space-y-2">
                <p>{submitError}</p>
                {revisionConflict ? (
                  <p className="text-xs">请关闭弹窗刷新最新草稿后再重新提交。</p>
                ) : null}
              </AlertDescription>
            </Alert>
          ) : null}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field data-invalid={Boolean(errors.billingMonth) || undefined}>
              <FieldLabel htmlFor="bill-month-select">结算归属账期</FieldLabel>
              <Select
                value={watchedBillingMonth}
                disabled={mutation.isPending}
                onValueChange={(value) => {
                  const prevMonth = form.getValues('billingMonth');
                  form.setValue('billingMonth', value, { shouldValidate: true });
                  if (!editingBill) {
                    const prevCycle = defaultCycleDates(prevMonth);
                    if (
                      form.getValues('readingStartDate') === prevCycle.start &&
                      form.getValues('readingEndDate') === prevCycle.end
                    ) {
                      const nextCycle = defaultCycleDates(value);
                      form.setValue('readingStartDate', nextCycle.start, { shouldValidate: true });
                      form.setValue('readingEndDate', nextCycle.end, { shouldValidate: true });
                    }
                  }
                }}
              >
                <SelectTrigger
                  id="bill-month-select"
                  aria-label="结算归属账期"
                  aria-invalid={errors.billingMonth ? 'true' : 'false'}
                  aria-describedby={errors.billingMonth ? 'bill-month-error' : undefined}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {selectableMonths.map((month) => (
                    <SelectItem key={month} value={month}>
                      {formatBillingMonthLabel(month)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldError id="bill-month-error" errors={[errors.billingMonth]} />
            </Field>

            <Field data-invalid={Boolean(errors.meterScope) || undefined}>
              <FieldLabel htmlFor="bill-scope-select">供电局表计范围</FieldLabel>
              <Select
                value={watchedMeterScope}
                disabled={mutation.isPending}
                onValueChange={(value) => {
                  if (!isOneOf(METER_SCOPES, value)) return;
                  form.setValue('meterScope', value, { shouldValidate: true });
                  if (value !== 'WHOLE_BUILDING') {
                    form.setValue('chillerShareOverridePercentInput', '');
                  }
                }}
              >
                <SelectTrigger
                  id="bill-scope-select"
                  aria-label="供电局表计范围"
                  aria-invalid={errors.meterScope ? 'true' : 'false'}
                  aria-describedby={describedBy(
                    'bill-scope-desc',
                    errors.meterScope ? 'bill-scope-error' : undefined,
                  )}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="CHILLER_PLANT">{METER_SCOPE_LABEL.CHILLER_PLANT}</SelectItem>
                  <SelectItem value="WHOLE_BUILDING">{METER_SCOPE_LABEL.WHOLE_BUILDING}</SelectItem>
                </SelectContent>
              </Select>
              <FieldDescription id="bill-scope-desc">
                {watchedMeterScope === 'CHILLER_PLANT'
                  ? '电费单仅计冷站专用变压器，将与平台冷站电表直接比对偏差'
                  : '电费单含公区/电梯/租户等整栋用电，将按冷站电量占比折算分摊'}
              </FieldDescription>
              <FieldError id="bill-scope-error" errors={[errors.meterScope]} />
            </Field>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <Field data-invalid={Boolean(errors.readingStartDate) || undefined}>
              <FieldLabel htmlFor="bill-reading-start">抄表起始日期</FieldLabel>
              <CalendarDateInput
                id="bill-reading-start"
                value={watchedReadingStart}
                disabled={mutation.isPending}
                invalid={Boolean(errors.readingStartDate)}
                describedBy={errors.readingStartDate ? 'bill-reading-start-error' : undefined}
                buttonAriaLabel="从日历选择抄表起始日期"
                onChange={(next) =>
                  form.setValue('readingStartDate', next, { shouldValidate: true })
                }
                onBlur={() => void form.trigger('readingStartDate')}
              />
              <FieldError id="bill-reading-start-error" errors={[errors.readingStartDate]} />
            </Field>

            <Field data-invalid={Boolean(errors.readingEndDate) || undefined}>
              <FieldLabel htmlFor="bill-reading-end">抄表截止日期（含当日）</FieldLabel>
              <CalendarDateInput
                id="bill-reading-end"
                value={watchedReadingEnd}
                disabled={mutation.isPending}
                invalid={Boolean(errors.readingEndDate)}
                describedBy={errors.readingEndDate ? 'bill-reading-end-error' : undefined}
                buttonAriaLabel="从日历选择抄表截止日期"
                onChange={(next) =>
                  form.setValue('readingEndDate', next, { shouldValidate: true })
                }
                onBlur={() => void form.trigger('readingEndDate')}
              />
              <FieldError id="bill-reading-end-error" errors={[errors.readingEndDate]} />
            </Field>

            <Field data-invalid={Boolean(errors.billedEnergyInput) || undefined}>
              <FieldLabel htmlFor="bill-energy-kwh">抄表结算电量（kWh）</FieldLabel>
              <Input
                id="bill-energy-kwh"
                inputMode="decimal"
                placeholder="例如 120000"
                disabled={mutation.isPending}
                aria-invalid={errors.billedEnergyInput ? 'true' : 'false'}
                aria-describedby={errors.billedEnergyInput ? 'bill-energy-kwh-error' : undefined}
                onKeyDown={preventImeSubmit}
                {...form.register('billedEnergyInput')}
              />
              <FieldError id="bill-energy-kwh-error" errors={[errors.billedEnergyInput]} />
            </Field>
          </div>

          <UtilityBillChargesSection
            form={form}
            disabled={mutation.isPending}
            extraDetailsOpen={extraDetailsOpen}
            onExtraDetailsOpenChange={setExtraDetailsOpen}
          />

          {watchedMeterScope === 'WHOLE_BUILDING' ? (
            <Field data-invalid={Boolean(errors.chillerShareOverridePercentInput) || undefined}>
              <FieldLabel htmlFor="bill-share-override">
                财务指定冷站分摊比例（%，选填）
              </FieldLabel>
              <Input
                id="bill-share-override"
                inputMode="decimal"
                placeholder="留空则按同抄表周期「冷站计量电量 ÷ 整栋抄表电量」自动折算"
                disabled={mutation.isPending}
                aria-invalid={errors.chillerShareOverridePercentInput ? 'true' : 'false'}
                aria-describedby={describedBy(
                  'bill-share-override-desc',
                  errors.chillerShareOverridePercentInput ? 'bill-share-override-error' : undefined,
                )}
                onKeyDown={preventImeSubmit}
                {...form.register('chillerShareOverridePercentInput')}
              />
              <FieldDescription id="bill-share-override-desc">
                仅当财务有固定冷站分摊口径（如 35%）时填写；留空时系统按实测电量占比与整栋综合单价自动分摊。
              </FieldDescription>
              <FieldError
                id="bill-share-override-error"
                errors={[errors.chillerShareOverridePercentInput]}
              />
            </Field>
          ) : null}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field data-invalid={Boolean(errors.invoiceNo) || undefined}>
              <FieldLabel htmlFor="bill-invoice-ref">供电局发票 / 单据号（选填）</FieldLabel>
              <Input
                id="bill-invoice-ref"
                placeholder="例如 GD-202610-0081"
                autoCorrect="off"
                autoCapitalize="none"
                spellCheck={false}
                disabled={mutation.isPending}
                aria-invalid={errors.invoiceNo ? 'true' : 'false'}
                aria-describedby={errors.invoiceNo ? 'bill-invoice-ref-error' : undefined}
                onKeyDown={preventImeSubmit}
                {...form.register('invoiceNo')}
              />
              <FieldError id="bill-invoice-ref-error" errors={[errors.invoiceNo]} />
            </Field>

            <Field data-invalid={Boolean(errors.reason) || undefined}>
              <FieldLabel htmlFor="bill-reason-input">变更原因</FieldLabel>
              <Input
                id="bill-reason-input"
                placeholder="例如：收到供电局 10 月结算通知单"
                disabled={mutation.isPending}
                aria-invalid={errors.reason ? 'true' : 'false'}
                aria-describedby={errors.reason ? 'bill-reason-error' : undefined}
                onKeyDown={preventImeSubmit}
                {...form.register('reason')}
              />
              <FieldError id="bill-reason-error" errors={[errors.reason]} />
            </Field>
          </div>

          <Field data-invalid={Boolean(errors.note) || undefined}>
            <FieldLabel htmlFor="bill-note-input">备注说明（选填）</FieldLabel>
            <Textarea
              id="bill-note-input"
              rows={2}
              placeholder="例如：本月功率因数 0.95 获力调电费减免 1,257 元"
              disabled={mutation.isPending}
              aria-invalid={errors.note ? 'true' : 'false'}
              aria-describedby={errors.note ? 'bill-note-error' : undefined}
              {...form.register('note')}
            />
            <FieldError id="bill-note-error" errors={[errors.note]} />
          </Field>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={mutation.isPending}
              onClick={() => handleOpenChange(false)}
            >
              取消
            </Button>
            <Button type="submit" disabled={mutation.isPending}>
              {mutation.isPending ? <Spinner data-icon="inline-start" /> : null}
              {mutation.isPending
                ? '正在保存…'
                : editingBill
                  ? '保存草稿修改'
                  : '保存为待确认草稿'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
