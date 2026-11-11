import { useEffect, useMemo, useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { CalendarIcon, ChevronDown } from 'lucide-react';
import { useForm, useWatch } from 'react-hook-form';
import { toast } from 'sonner';
import { z } from 'zod';
import type {
  UtilityBill,
  UtilityBillBasicChargeMode,
  UtilityBillMeterScope,
  UtilityBillMutation,
  UtilityBillTouBreakdown,
} from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Calendar } from '@/components/ui/calendar';
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible';
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
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  BASIC_CHARGE_MODE_LABEL,
  computeUtilityBillTotalPayable,
  formatBillingMonthLabel,
  METER_SCOPE_LABEL,
  resolveReadingDateWindow,
} from '../billing-model';
import {
  billingGatewayClient,
  billingKeys,
  describeBillingApiError,
  isBillingRevisionConflict,
} from '../query';

const nonNegativeMoneyPattern = /^\d+(?:\.\d{1,2})?$/;
const signedMoneyPattern = /^-?\d+(?:\.\d{1,2})?$/;
const datePattern = /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;

function parseIsoCalendarDate(value: string): Date | undefined {
  if (!datePattern.test(value)) return undefined;
  const [y, m, d] = value.split('-').map(Number);
  const dt = new Date(y!, m! - 1, d!);
  if (dt.getFullYear() !== y || dt.getMonth() !== m! - 1 || dt.getDate() !== d) {
    return undefined;
  }
  return dt;
}

function formatIsoCalendarDate(date: Date): string {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}

interface CalendarDateInputProps {
  readonly id: string;
  readonly value: string;
  readonly disabled?: boolean;
  readonly buttonAriaLabel: string;
  readonly onChange: (nextValue: string) => void;
  readonly onBlur: () => void;
}

function CalendarDateInput({
  id,
  value,
  disabled,
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
        onChange={(event) => onChange(event.target.value)}
        onBlur={onBlur}
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
                setOpen(false);
              }
            }}
          />
        </PopoverContent>
      </Popover>
    </div>
  );
}

const optionalNonNegativeKwhField = z
  .string()
  .trim()
  .refine((v) => v === '' || (nonNegativeMoneyPattern.test(v) && Number.isFinite(Number(v))), {
    message: '分项电量须为非负数字，最多保留两位小数',
  });

const utilityBillFormSchema = z
  .object({
    billingMonth: z
      .string()
      .trim()
      .regex(/^\d{4}-(0[1-9]|1[0-2])$/, { message: '请选择有效的结算账期（YYYY-MM）' }),
    meterScope: z.enum(['CHILLER_PLANT', 'WHOLE_BUILDING']),
    readingStartDate: z
      .string()
      .trim()
      .regex(datePattern, { message: '请输入抄表起始日期（YYYY-MM-DD）' }),
    readingEndDate: z
      .string()
      .trim()
      .regex(datePattern, { message: '请输入抄表截止日期（YYYY-MM-DD）' }),
    billedEnergyInput: z
      .string()
      .trim()
      .min(1, { message: '请输入供电局抄表结算电量' })
      .refine((v) => nonNegativeMoneyPattern.test(v) && Number.isFinite(Number(v)), {
        message: '电量须为非负数字，最多保留两位小数',
      }),
    touSuperPeakKwhInput: optionalNonNegativeKwhField,
    touPeakKwhInput: optionalNonNegativeKwhField,
    touFlatKwhInput: optionalNonNegativeKwhField,
    touValleyKwhInput: optionalNonNegativeKwhField,
    energyChargeInput: z
      .string()
      .trim()
      .min(1, { message: '请输入票面电度电费' })
      .refine((v) => nonNegativeMoneyPattern.test(v) && Number.isFinite(Number(v)), {
        message: '电度电费须为非负金额，最多保留两位小数',
      }),
    basicChargeMode: z.enum(['NONE', 'TRANSFORMER_CAPACITY_KVA', 'MAX_DEMAND_KW']),
    basicChargeQuantityInput: z.string().trim(),
    basicChargeInput: z
      .string()
      .trim()
      .min(1, { message: '请输入基本电费（单一制填 0）' })
      .refine((v) => nonNegativeMoneyPattern.test(v) && Number.isFinite(Number(v)), {
        message: '基本电费须为非负金额，最多保留两位小数',
      }),
    standardPowerFactorInput: z
      .string()
      .trim()
      .refine(
        (v) => {
          if (v === '') return true;
          const n = Number(v);
          return /^\d+(?:\.\d{1,2})?$/.test(v) && Number.isFinite(n) && n >= 0.7 && n <= 0.99;
        },
        { message: '考核标准功率因数须在 0.70 至 0.99 之间' },
      ),
    actualPowerFactorInput: z
      .string()
      .trim()
      .refine(
        (v) => {
          if (v === '') return true;
          const n = Number(v);
          return /^\d+(?:\.\d{1,3})?$/.test(v) && Number.isFinite(n) && n >= 0 && n <= 1;
        },
        { message: '实际功率因数须在 0 至 1.000 之间' },
      ),
    powerFactorAdjustmentInput: z
      .string()
      .trim()
      .min(1, { message: '请输入力调电费（奖励填负数，无奖罚填 0）' })
      .refine((v) => signedMoneyPattern.test(v) && Number.isFinite(Number(v)), {
        message: '力调电费须为有效金额（支持负数），最多保留两位小数',
      }),
    otherChargesInput: z
      .string()
      .trim()
      .min(1, { message: '请输入其他代征及附加费用（无则填 0）' })
      .refine((v) => signedMoneyPattern.test(v) && Number.isFinite(Number(v)), {
        message: '其他费用须为有效金额（支持负数退补），最多保留两位小数',
      }),
    chillerShareOverridePercentInput: z.string().trim(),
    utilityAccountNo: z.string().trim().max(100, { message: '供电户号不能超过 100 个字符' }),
    invoiceNo: z.string().trim().max(100, { message: '发票或单据编号不能超过 100 个字符' }),
    note: z.string().trim().max(1000, { message: '备注说明不能超过 1000 字' }),
    reason: z
      .string()
      .trim()
      .min(1, { message: '请填写录入或修改结算电费单的原因' })
      .max(500, { message: '原因不能超过 500 字' }),
  })
  .superRefine((values, ctx) => {
    const readingWindow = resolveReadingDateWindow(
      values.readingStartDate,
      values.readingEndDate,
      'UTC',
    );
    if (!readingWindow) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['readingEndDate'],
        message: '抄表截止日期不能早于起始日期，且日期必须真实有效',
      });
    }

    const basicCharge = Number(values.basicChargeInput);
    if (values.basicChargeMode === 'NONE') {
      if (basicCharge !== 0) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['basicChargeInput'],
          message: '选择无基本电费（单一制）时，基本电费必须为 0',
        });
      }
    } else {
      const qty = Number(values.basicChargeQuantityInput);
      if (
        !values.basicChargeQuantityInput ||
        !nonNegativeMoneyPattern.test(values.basicChargeQuantityInput) ||
        !Number.isFinite(qty) ||
        qty <= 0
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['basicChargeQuantityInput'],
          message:
            values.basicChargeMode === 'TRANSFORMER_CAPACITY_KVA'
              ? '请输入大于 0 的计费变压器容量（kVA）'
              : '请输入大于 0 的结算最大需量（kW）',
        });
      }
    }

    if (values.meterScope === 'WHOLE_BUILDING' && values.chillerShareOverridePercentInput !== '') {
      const pct = Number(values.chillerShareOverridePercentInput);
      if (!Number.isFinite(pct) || pct <= 0 || pct > 100) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['chillerShareOverridePercentInput'],
          message: '财务指定冷站分摊比例须在 0.01% 至 100% 之间（留空则按电量实测占比自动计算）',
        });
      }
    }

    const energy = Number(values.energyChargeInput);
    const pf = Number(values.powerFactorAdjustmentInput);
    const other = Number(values.otherChargesInput);
    if (
      Number.isFinite(energy) &&
      Number.isFinite(basicCharge) &&
      Number.isFinite(pf) &&
      Number.isFinite(other)
    ) {
      const total = computeUtilityBillTotalPayable({
        energyChargeCny: energy,
        basicChargeCny: basicCharge,
        powerFactorAdjustmentCny: pf,
        otherChargesCny: other,
      });
      if (total < 0) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['otherChargesInput'],
          message: '四项费用合计应收总电费不能小于 0 元',
        });
      }
    }
  });

type UtilityBillFormValues = z.infer<typeof utilityBillFormSchema>;

function defaultCycleDates(billingMonth: string): { start: string; end: string } {
  const [year, month] = billingMonth.split('-').map(Number);
  const days = new Date(Date.UTC(year!, month!, 0)).getUTCDate();
  return {
    start: `${billingMonth}-01`,
    end: `${billingMonth}-${String(days).padStart(2, '0')}`,
  };
}

function hasExtraTicketDetails(bill: UtilityBill | null): boolean {
  if (!bill) return false;
  return Boolean(
    bill.utilityAccountNo ||
      bill.standardPowerFactor !== null ||
      bill.actualPowerFactor !== null ||
      bill.touBreakdown?.sharpKwh !== undefined ||
      bill.touBreakdown?.peakKwh !== undefined ||
      bill.touBreakdown?.flatKwh !== undefined ||
      bill.touBreakdown?.valleyKwh !== undefined,
  );
}

function buildInitialValues(
  editingBill: UtilityBill | null,
  defaultBillingMonth: string,
): UtilityBillFormValues {
  if (editingBill) {
    return {
      billingMonth: editingBill.billingMonth,
      meterScope: editingBill.meterScope,
      readingStartDate: editingBill.readingStartDate,
      readingEndDate: editingBill.readingEndDate,
      billedEnergyInput: String(editingBill.billedEnergyKwh),
      touSuperPeakKwhInput:
        editingBill.touBreakdown?.sharpKwh !== undefined
          ? String(editingBill.touBreakdown.sharpKwh)
          : '',
      touPeakKwhInput:
        editingBill.touBreakdown?.peakKwh !== undefined
          ? String(editingBill.touBreakdown.peakKwh)
          : '',
      touFlatKwhInput:
        editingBill.touBreakdown?.flatKwh !== undefined
          ? String(editingBill.touBreakdown.flatKwh)
          : '',
      touValleyKwhInput:
        editingBill.touBreakdown?.valleyKwh !== undefined
          ? String(editingBill.touBreakdown.valleyKwh)
          : '',
      energyChargeInput: String(editingBill.energyChargeCny),
      basicChargeMode: editingBill.basicChargeMode,
      basicChargeQuantityInput:
        editingBill.basicChargeQuantity !== null ? String(editingBill.basicChargeQuantity) : '',
      basicChargeInput: String(editingBill.basicChargeCny),
      standardPowerFactorInput:
        editingBill.standardPowerFactor !== null ? String(editingBill.standardPowerFactor) : '',
      actualPowerFactorInput:
        editingBill.actualPowerFactor !== null ? String(editingBill.actualPowerFactor) : '',
      powerFactorAdjustmentInput: String(editingBill.powerFactorAdjustmentCny),
      otherChargesInput: String(editingBill.otherChargesCny),
      chillerShareOverridePercentInput:
        editingBill.chillerShareOverrideRatio !== null
          ? String(Number((editingBill.chillerShareOverrideRatio * 100).toFixed(2)))
          : '',
      utilityAccountNo: editingBill.utilityAccountNo ?? '',
      invoiceNo: editingBill.invoiceNo ?? '',
      note: editingBill.note ?? '',
      reason: '核对并修正结算电费单草稿',
    };
  }
  const cycle = defaultCycleDates(defaultBillingMonth);
  return {
    billingMonth: defaultBillingMonth,
    meterScope: 'CHILLER_PLANT',
    readingStartDate: cycle.start,
    readingEndDate: cycle.end,
    billedEnergyInput: '',
    touSuperPeakKwhInput: '',
    touPeakKwhInput: '',
    touFlatKwhInput: '',
    touValleyKwhInput: '',
    energyChargeInput: '',
    basicChargeMode: 'MAX_DEMAND_KW',
    basicChargeQuantityInput: '',
    basicChargeInput: '0',
    standardPowerFactorInput: '',
    actualPowerFactorInput: '',
    powerFactorAdjustmentInput: '0',
    otherChargesInput: '0',
    chillerShareOverridePercentInput: '',
    utilityAccountNo: '',
    invoiceNo: '',
    note: '',
    reason: '录入供电局月度结算电费单',
  };
}

function buildTouBreakdown(values: UtilityBillFormValues): UtilityBillTouBreakdown | undefined {
  const out: {
    sharpKwh?: number;
    peakKwh?: number;
    flatKwh?: number;
    valleyKwh?: number;
  } = {};
  if (values.touSuperPeakKwhInput !== '') {
    out.sharpKwh = Number(Number(values.touSuperPeakKwhInput).toFixed(2));
  }
  if (values.touPeakKwhInput !== '') {
    out.peakKwh = Number(Number(values.touPeakKwhInput).toFixed(2));
  }
  if (values.touFlatKwhInput !== '') {
    out.flatKwh = Number(Number(values.touFlatKwhInput).toFixed(2));
  }
  if (values.touValleyKwhInput !== '') {
    out.valleyKwh = Number(Number(values.touValleyKwhInput).toFixed(2));
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

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

  const form = useForm<UtilityBillFormValues>({
    resolver: zodResolver(utilityBillFormSchema),
    mode: 'onBlur',
    defaultValues: buildInitialValues(editingBill, defaultBillingMonth),
  });

  useEffect(() => {
    if (open) {
      form.reset(buildInitialValues(editingBill, defaultBillingMonth));
      setSubmitError(null);
      setRevisionConflict(false);
      setExtraDetailsOpen(hasExtraTicketDetails(editingBill));
    }
  }, [open, editingBill, defaultBillingMonth, form]);

  const watchedMeterScope = useWatch({ control: form.control, name: 'meterScope' });
  const watchedBasicMode = useWatch({ control: form.control, name: 'basicChargeMode' });
  const watchedBillingMonth = useWatch({ control: form.control, name: 'billingMonth' });
  const watchedReadingStart = useWatch({ control: form.control, name: 'readingStartDate' });
  const watchedReadingEnd = useWatch({ control: form.control, name: 'readingEndDate' });
  const watchedEnergyCharge = useWatch({ control: form.control, name: 'energyChargeInput' });
  const watchedBasicCharge = useWatch({ control: form.control, name: 'basicChargeInput' });
  const watchedPfAdjustment = useWatch({
    control: form.control,
    name: 'powerFactorAdjustmentInput',
  });
  const watchedOtherCharges = useWatch({ control: form.control, name: 'otherChargesInput' });

  const liveTotalPayable = useMemo(() => {
    const parseOrZero = (raw: string) => {
      const n = Number(raw);
      return Number.isFinite(n) ? n : 0;
    };
    return computeUtilityBillTotalPayable({
      energyChargeCny: parseOrZero(watchedEnergyCharge),
      basicChargeCny: parseOrZero(watchedBasicCharge),
      powerFactorAdjustmentCny: parseOrZero(watchedPfAdjustment),
      otherChargesCny: parseOrZero(watchedOtherCharges),
    });
  }, [watchedEnergyCharge, watchedBasicCharge, watchedPfAdjustment, watchedOtherCharges]);

  const selectableMonths = useMemo(() => {
    const set = new Set(monthOptions);
    if (watchedBillingMonth) set.add(watchedBillingMonth);
    return Array.from(set).sort((a, b) => b.localeCompare(a));
  }, [monthOptions, watchedBillingMonth]);

  const mutation = useMutation({
    mutationFn: async (values: UtilityBillFormValues) => {
      const totalPayableCny = computeUtilityBillTotalPayable({
        energyChargeCny: Number(values.energyChargeInput),
        basicChargeCny: Number(values.basicChargeInput),
        powerFactorAdjustmentCny: Number(values.powerFactorAdjustmentInput),
        otherChargesCny: Number(values.otherChargesInput),
      });
      const touBreakdown = buildTouBreakdown(values);

      const body: UtilityBillMutation = {
        billingMonth: values.billingMonth,
        meterScope: values.meterScope,
        readingStartDate: values.readingStartDate,
        readingEndDate: values.readingEndDate,
        billedEnergyKwh: Number(Number(values.billedEnergyInput).toFixed(2)),
        ...(touBreakdown ? { touBreakdown } : {}),
        energyChargeCny: Number(Number(values.energyChargeInput).toFixed(2)),
        basicChargeMode: values.basicChargeMode,
        ...(values.basicChargeMode !== 'NONE'
          ? { basicChargeQuantity: Number(Number(values.basicChargeQuantityInput).toFixed(2)) }
          : {}),
        basicChargeCny:
          values.basicChargeMode === 'NONE'
            ? 0
            : Number(Number(values.basicChargeInput).toFixed(2)),
        ...(values.standardPowerFactorInput !== ''
          ? { standardPowerFactor: Number(Number(values.standardPowerFactorInput).toFixed(2)) }
          : {}),
        ...(values.actualPowerFactorInput !== ''
          ? { actualPowerFactor: Number(Number(values.actualPowerFactorInput).toFixed(3)) }
          : {}),
        powerFactorAdjustmentCny: Number(Number(values.powerFactorAdjustmentInput).toFixed(2)),
        otherChargesCny: Number(Number(values.otherChargesInput).toFixed(2)),
        totalPayableCny,
        ...(values.meterScope === 'WHOLE_BUILDING' &&
        values.chillerShareOverridePercentInput.trim() !== ''
          ? {
              chillerShareOverrideRatio: Number(
                (Number(values.chillerShareOverridePercentInput) / 100).toFixed(4),
              ),
            }
          : {}),
        ...(values.utilityAccountNo.trim()
          ? { utilityAccountNo: values.utilityAccountNo.trim() }
          : {}),
        ...(values.invoiceNo.trim() ? { invoiceNo: values.invoiceNo.trim() } : {}),
        ...(values.note.trim() ? { note: values.note.trim() } : {}),
        meta: {
          expectedRevision: editingBill?.revision ?? 0,
          idempotencyKey: crypto.randomUUID(),
          reason: values.reason.trim(),
        },
      };

      if (editingBill) {
        const res = await billingGatewayClient.updateSiteUtilityBill(
          siteId,
          editingBill.id,
          body,
          csrfToken,
        );
        return res.data;
      }
      const res = await billingGatewayClient.createSiteUtilityBill(siteId, body, csrfToken);
      return res.data;
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: billingKeys.billsRoot(siteId) });
      toast.success(
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
            {editingBill ? '如何修改结算电费单草稿？' : '如何录入供电局结算电费单？'}
          </DialogTitle>
          <DialogDescription>
            按供电局发票或电费结算通知单填写抄表周期与四项电费构成。保存后为草稿状态，可在右侧核对抽屉对比平台电表读数后再确认归档。
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
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
            <Field data-invalid={Boolean(form.formState.errors.billingMonth) || undefined}>
              <FieldLabel htmlFor="bill-month-select">结算归属账期</FieldLabel>
              <Select
                value={watchedBillingMonth}
                disabled={mutation.isPending}
                onValueChange={(value) =>
                  form.setValue('billingMonth', value, { shouldValidate: true })
                }
              >
                <SelectTrigger id="bill-month-select" aria-label="结算归属账期">
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
              <FieldError errors={[form.formState.errors.billingMonth]} />
            </Field>

            <Field data-invalid={Boolean(form.formState.errors.meterScope) || undefined}>
              <FieldLabel htmlFor="bill-scope-select">供电局表计范围</FieldLabel>
              <Select
                value={watchedMeterScope}
                disabled={mutation.isPending}
                onValueChange={(value) => {
                  const scope = value as UtilityBillMeterScope;
                  form.setValue('meterScope', scope, { shouldValidate: true });
                  if (scope !== 'WHOLE_BUILDING') {
                    form.setValue('chillerShareOverridePercentInput', '');
                  }
                }}
              >
                <SelectTrigger id="bill-scope-select" aria-label="供电局表计范围">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="CHILLER_PLANT">{METER_SCOPE_LABEL.CHILLER_PLANT}</SelectItem>
                  <SelectItem value="WHOLE_BUILDING">{METER_SCOPE_LABEL.WHOLE_BUILDING}</SelectItem>
                </SelectContent>
              </Select>
              <FieldDescription>
                {watchedMeterScope === 'CHILLER_PLANT'
                  ? '电费单仅计冷站专用变压器，将与平台冷站电表直接比对偏差'
                  : '电费单含公区/电梯/租户等整栋用电，将按冷站电量占比折算分摊'}
              </FieldDescription>
              <FieldError errors={[form.formState.errors.meterScope]} />
            </Field>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <Field data-invalid={Boolean(form.formState.errors.readingStartDate) || undefined}>
              <FieldLabel htmlFor="bill-reading-start">抄表起始日期</FieldLabel>
              <CalendarDateInput
                id="bill-reading-start"
                value={watchedReadingStart}
                disabled={mutation.isPending}
                buttonAriaLabel="从日历选择抄表起始日期"
                onChange={(next) =>
                  form.setValue('readingStartDate', next, { shouldValidate: true })
                }
                onBlur={() => void form.trigger('readingStartDate')}
              />
              <FieldError errors={[form.formState.errors.readingStartDate]} />
            </Field>

            <Field data-invalid={Boolean(form.formState.errors.readingEndDate) || undefined}>
              <FieldLabel htmlFor="bill-reading-end">抄表截止日期（含当日）</FieldLabel>
              <CalendarDateInput
                id="bill-reading-end"
                value={watchedReadingEnd}
                disabled={mutation.isPending}
                buttonAriaLabel="从日历选择抄表截止日期"
                onChange={(next) =>
                  form.setValue('readingEndDate', next, { shouldValidate: true })
                }
                onBlur={() => void form.trigger('readingEndDate')}
              />
              <FieldError errors={[form.formState.errors.readingEndDate]} />
            </Field>

            <Field data-invalid={Boolean(form.formState.errors.billedEnergyInput) || undefined}>
              <FieldLabel htmlFor="bill-energy-kwh">抄表结算电量（kWh）</FieldLabel>
              <Input
                id="bill-energy-kwh"
                inputMode="decimal"
                placeholder="例如 120000"
                disabled={mutation.isPending}
                {...form.register('billedEnergyInput')}
              />
              <FieldError errors={[form.formState.errors.billedEnergyInput]} />
            </Field>
          </div>

          <div className="space-y-3 rounded-lg border border-border bg-muted/25 p-3.5">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <span className="text-xs font-semibold text-foreground">两部制电费四项构成</span>
              <span className="text-xs text-muted-foreground tabular-nums">
                自动合计应收总电费：
                <strong className="ml-1 text-sm font-semibold text-foreground">
                  ¥{formatDecimal(liveTotalPayable, 2)}
                </strong>
              </span>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <Field data-invalid={Boolean(form.formState.errors.energyChargeInput) || undefined}>
                <FieldLabel htmlFor="bill-energy-charge">1. 电度电费（元）</FieldLabel>
                <Input
                  id="bill-energy-charge"
                  inputMode="decimal"
                  placeholder="例如 98400"
                  disabled={mutation.isPending}
                  {...form.register('energyChargeInput')}
                />
                <FieldError errors={[form.formState.errors.energyChargeInput]} />
              </Field>

              <Field data-invalid={Boolean(form.formState.errors.basicChargeMode) || undefined}>
                <FieldLabel htmlFor="bill-basic-mode">2. 基本电费计费方式</FieldLabel>
                <Select
                  value={watchedBasicMode}
                  disabled={mutation.isPending}
                  onValueChange={(value) => {
                    const nextMode = value as UtilityBillBasicChargeMode;
                    form.setValue('basicChargeMode', nextMode, { shouldValidate: true });
                    if (nextMode === 'NONE') {
                      form.setValue('basicChargeInput', '0', { shouldValidate: true });
                      form.setValue('basicChargeQuantityInput', '');
                    }
                  }}
                >
                  <SelectTrigger id="bill-basic-mode" aria-label="基本电费计费方式">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="MAX_DEMAND_KW">
                      {BASIC_CHARGE_MODE_LABEL.MAX_DEMAND_KW}
                    </SelectItem>
                    <SelectItem value="TRANSFORMER_CAPACITY_KVA">
                      {BASIC_CHARGE_MODE_LABEL.TRANSFORMER_CAPACITY_KVA}
                    </SelectItem>
                    <SelectItem value="NONE">{BASIC_CHARGE_MODE_LABEL.NONE}</SelectItem>
                  </SelectContent>
                </Select>
                <FieldError errors={[form.formState.errors.basicChargeMode]} />
              </Field>

              {watchedBasicMode !== 'NONE' ? (
                <Field
                  data-invalid={
                    Boolean(form.formState.errors.basicChargeQuantityInput) || undefined
                  }
                >
                  <FieldLabel htmlFor="bill-basic-qty">
                    {watchedBasicMode === 'TRANSFORMER_CAPACITY_KVA'
                      ? '计费变压器容量（kVA）'
                      : '结算最大需量（kW）'}
                  </FieldLabel>
                  <Input
                    id="bill-basic-qty"
                    inputMode="decimal"
                    placeholder={watchedBasicMode === 'TRANSFORMER_CAPACITY_KVA' ? '2000' : '620'}
                    disabled={mutation.isPending}
                    {...form.register('basicChargeQuantityInput')}
                  />
                  <FieldError errors={[form.formState.errors.basicChargeQuantityInput]} />
                </Field>
              ) : null}

              <Field data-invalid={Boolean(form.formState.errors.basicChargeInput) || undefined}>
                <FieldLabel htmlFor="bill-basic-charge">基本电费金额（元）</FieldLabel>
                <Input
                  id="bill-basic-charge"
                  inputMode="decimal"
                  disabled={mutation.isPending || watchedBasicMode === 'NONE'}
                  {...form.register('basicChargeInput')}
                />
                <FieldError errors={[form.formState.errors.basicChargeInput]} />
              </Field>

              <Field
                data-invalid={
                  Boolean(form.formState.errors.powerFactorAdjustmentInput) || undefined
                }
              >
                <FieldLabel htmlFor="bill-pf-adjustment">3. 力调电费（元，奖励填负数）</FieldLabel>
                <Input
                  id="bill-pf-adjustment"
                  inputMode="decimal"
                  placeholder="例如 -1257.00"
                  disabled={mutation.isPending}
                  {...form.register('powerFactorAdjustmentInput')}
                />
                <FieldError errors={[form.formState.errors.powerFactorAdjustmentInput]} />
              </Field>

              <Field data-invalid={Boolean(form.formState.errors.otherChargesInput) || undefined}>
                <FieldLabel htmlFor="bill-other-charges">4. 其他代征及附加（元）</FieldLabel>
                <Input
                  id="bill-other-charges"
                  inputMode="decimal"
                  placeholder="例如 357.00"
                  disabled={mutation.isPending}
                  {...form.register('otherChargesInput')}
                />
                <FieldError errors={[form.formState.errors.otherChargesInput]} />
              </Field>
            </div>
          </div>

          <Collapsible open={extraDetailsOpen} onOpenChange={setExtraDetailsOpen}>
            <div className="rounded-lg border border-border px-3.5 py-2.5">
              <CollapsibleTrigger asChild>
                <button
                  type="button"
                  className="flex w-full items-center justify-between text-left text-xs font-medium text-foreground"
                >
                  <span>票面峰平谷分项电量、功率因数与供电户号（选填）</span>
                  <ChevronDown
                    className={cn(
                      'size-4 text-muted-foreground transition-transform',
                      extraDetailsOpen && 'rotate-180',
                    )}
                    aria-hidden="true"
                  />
                </button>
              </CollapsibleTrigger>
              <CollapsibleContent className="mt-3 space-y-3 border-t border-divider pt-3">
                <div className="grid gap-3 sm:grid-cols-3">
                  <Field
                    data-invalid={Boolean(form.formState.errors.utilityAccountNo) || undefined}
                  >
                    <FieldLabel htmlFor="bill-account-no">供电户号（选填）</FieldLabel>
                    <Input
                      id="bill-account-no"
                      placeholder="例如 0800123456"
                      disabled={mutation.isPending}
                      {...form.register('utilityAccountNo')}
                    />
                    <FieldError errors={[form.formState.errors.utilityAccountNo]} />
                  </Field>

                  <Field
                    data-invalid={
                      Boolean(form.formState.errors.standardPowerFactorInput) || undefined
                    }
                  >
                    <FieldLabel htmlFor="bill-std-pf">考核标准功率因数（选填）</FieldLabel>
                    <Input
                      id="bill-std-pf"
                      inputMode="decimal"
                      placeholder="例如 0.90"
                      disabled={mutation.isPending}
                      {...form.register('standardPowerFactorInput')}
                    />
                    <FieldError errors={[form.formState.errors.standardPowerFactorInput]} />
                  </Field>

                  <Field
                    data-invalid={
                      Boolean(form.formState.errors.actualPowerFactorInput) || undefined
                    }
                  >
                    <FieldLabel htmlFor="bill-act-pf">实际功率因数（选填）</FieldLabel>
                    <Input
                      id="bill-act-pf"
                      inputMode="decimal"
                      placeholder="例如 0.95"
                      disabled={mutation.isPending}
                      {...form.register('actualPowerFactorInput')}
                    />
                    <FieldError errors={[form.formState.errors.actualPowerFactorInput]} />
                  </Field>
                </div>

                <div className="grid gap-3 sm:grid-cols-4">
                  <Field
                    data-invalid={
                      Boolean(form.formState.errors.touSuperPeakKwhInput) || undefined
                    }
                  >
                    <FieldLabel htmlFor="bill-tou-super-peak">尖峰电量（kWh）</FieldLabel>
                    <Input
                      id="bill-tou-super-peak"
                      inputMode="decimal"
                      disabled={mutation.isPending}
                      {...form.register('touSuperPeakKwhInput')}
                    />
                    <FieldError errors={[form.formState.errors.touSuperPeakKwhInput]} />
                  </Field>

                  <Field
                    data-invalid={Boolean(form.formState.errors.touPeakKwhInput) || undefined}
                  >
                    <FieldLabel htmlFor="bill-tou-peak">峰段电量（kWh）</FieldLabel>
                    <Input
                      id="bill-tou-peak"
                      inputMode="decimal"
                      disabled={mutation.isPending}
                      {...form.register('touPeakKwhInput')}
                    />
                    <FieldError errors={[form.formState.errors.touPeakKwhInput]} />
                  </Field>

                  <Field
                    data-invalid={Boolean(form.formState.errors.touFlatKwhInput) || undefined}
                  >
                    <FieldLabel htmlFor="bill-tou-flat">平段电量（kWh）</FieldLabel>
                    <Input
                      id="bill-tou-flat"
                      inputMode="decimal"
                      disabled={mutation.isPending}
                      {...form.register('touFlatKwhInput')}
                    />
                    <FieldError errors={[form.formState.errors.touFlatKwhInput]} />
                  </Field>

                  <Field
                    data-invalid={Boolean(form.formState.errors.touValleyKwhInput) || undefined}
                  >
                    <FieldLabel htmlFor="bill-tou-valley">谷段电量（kWh）</FieldLabel>
                    <Input
                      id="bill-tou-valley"
                      inputMode="decimal"
                      disabled={mutation.isPending}
                      {...form.register('touValleyKwhInput')}
                    />
                    <FieldError errors={[form.formState.errors.touValleyKwhInput]} />
                  </Field>
                </div>
              </CollapsibleContent>
            </div>
          </Collapsible>

          {watchedMeterScope === 'WHOLE_BUILDING' ? (
            <Field
              data-invalid={
                Boolean(form.formState.errors.chillerShareOverridePercentInput) || undefined
              }
            >
              <FieldLabel htmlFor="bill-share-override">
                财务指定冷站分摊比例（%，选填）
              </FieldLabel>
              <Input
                id="bill-share-override"
                inputMode="decimal"
                placeholder="留空则按同抄表周期「冷站计量电量 ÷ 整栋抄表电量」自动折算"
                disabled={mutation.isPending}
                {...form.register('chillerShareOverridePercentInput')}
              />
              <FieldDescription>
                仅当财务有固定冷站分摊口径（如 35%）时填写；留空时系统按实测电量占比与整栋综合单价自动分摊。
              </FieldDescription>
              <FieldError errors={[form.formState.errors.chillerShareOverridePercentInput]} />
            </Field>
          ) : null}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field data-invalid={Boolean(form.formState.errors.invoiceNo) || undefined}>
              <FieldLabel htmlFor="bill-invoice-ref">供电局发票 / 单据号（选填）</FieldLabel>
              <Input
                id="bill-invoice-ref"
                placeholder="例如 GD-202610-0081"
                disabled={mutation.isPending}
                {...form.register('invoiceNo')}
              />
              <FieldError errors={[form.formState.errors.invoiceNo]} />
            </Field>

            <Field data-invalid={Boolean(form.formState.errors.reason) || undefined}>
              <FieldLabel htmlFor="bill-reason-input">变更原因</FieldLabel>
              <Input
                id="bill-reason-input"
                placeholder="例如：收到供电局 10 月结算通知单"
                disabled={mutation.isPending}
                {...form.register('reason')}
              />
              <FieldError errors={[form.formState.errors.reason]} />
            </Field>
          </div>

          <Field data-invalid={Boolean(form.formState.errors.note) || undefined}>
            <FieldLabel htmlFor="bill-note-input">备注说明（选填）</FieldLabel>
            <Textarea
              id="bill-note-input"
              rows={2}
              placeholder="例如：本月功率因数 0.95 获力调电费减免 1,257 元"
              disabled={mutation.isPending}
              {...form.register('note')}
            />
            <FieldError errors={[form.formState.errors.note]} />
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
