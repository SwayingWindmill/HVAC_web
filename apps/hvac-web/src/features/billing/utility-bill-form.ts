import { z } from 'zod';
import type {
  UtilityBill,
  UtilityBillBasicChargeMode,
  UtilityBillMeterScope,
  UtilityBillMutation,
} from '../../api/generated/platformGateway.gen.ts';
import {
  computeUtilityBillTotalPayable,
  hasTouBreakdownValues,
  resolveReadingDateWindow,
  type UtilityBillTouBreakdownWithDeepValley,
} from './billing-model.ts';

export const METER_SCOPES = [
  'CHILLER_PLANT',
  'WHOLE_BUILDING',
] as const satisfies readonly UtilityBillMeterScope[];

export const BASIC_CHARGE_MODES = [
  'NONE',
  'TRANSFORMER_CAPACITY_KVA',
  'MAX_DEMAND_KW',
] as const satisfies readonly UtilityBillBasicChargeMode[];

const nonNegativeMoneyPattern = /^\d+(?:\.\d{1,2})?$/;
const signedMoneyPattern = /^-?\d+(?:\.\d{1,2})?$/;
const datePattern = /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;

export function parseIsoCalendarDate(value: string): Date | undefined {
  if (!datePattern.test(value)) return undefined;
  const [y, m, d] = value.split('-').map(Number);
  const dt = new Date(y!, m! - 1, d!);
  if (dt.getFullYear() !== y || dt.getMonth() !== m! - 1 || dt.getDate() !== d) {
    return undefined;
  }
  return dt;
}

export function formatIsoCalendarDate(date: Date): string {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}

const optionalNonNegativeKwhField = z
  .string()
  .trim()
  .refine(
    (v) => v === '' || (nonNegativeMoneyPattern.test(v) && Number.isFinite(Number(v))),
    {
      message: '分项电量须为非负数字，最多保留两位小数',
    },
  );

export const utilityBillFormSchema = z
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
    touDeepValleyKwhInput: optionalNonNegativeKwhField,
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

    if (
      values.meterScope === 'WHOLE_BUILDING' &&
      values.chillerShareOverridePercentInput !== ''
    ) {
      const pct = Number(values.chillerShareOverridePercentInput);
      if (!Number.isFinite(pct) || pct <= 0 || pct > 100) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['chillerShareOverridePercentInput'],
          message:
            '财务指定冷站分摊比例须在 0.01% 至 100% 之间（留空则按电量实测占比自动计算）',
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

export type UtilityBillFormValues = z.infer<typeof utilityBillFormSchema>;

export function defaultCycleDates(billingMonth: string): { start: string; end: string } {
  const [year, month] = billingMonth.split('-').map(Number);
  const days = new Date(Date.UTC(year!, month!, 0)).getUTCDate();
  return {
    start: `${billingMonth}-01`,
    end: `${billingMonth}-${String(days).padStart(2, '0')}`,
  };
}

export function hasExtraTicketDetails(bill: UtilityBill | null): boolean {
  if (!bill) return false;
  return Boolean(
    bill.utilityAccountNo ||
      bill.standardPowerFactor !== null ||
      bill.actualPowerFactor !== null ||
      hasTouBreakdownValues(bill.touBreakdown),
  );
}

export function buildUtilityBillInitialValues(
  editingBill: UtilityBill | null,
  defaultBillingMonth: string,
): UtilityBillFormValues {
  if (editingBill) {
    const touBreakdown: UtilityBillTouBreakdownWithDeepValley | undefined =
      editingBill.touBreakdown;
    return {
      billingMonth: editingBill.billingMonth,
      meterScope: editingBill.meterScope,
      readingStartDate: editingBill.readingStartDate,
      readingEndDate: editingBill.readingEndDate,
      billedEnergyInput: String(editingBill.billedEnergyKwh),
      touSuperPeakKwhInput:
        touBreakdown?.sharpKwh !== undefined ? String(touBreakdown.sharpKwh) : '',
      touPeakKwhInput: touBreakdown?.peakKwh !== undefined ? String(touBreakdown.peakKwh) : '',
      touFlatKwhInput: touBreakdown?.flatKwh !== undefined ? String(touBreakdown.flatKwh) : '',
      touValleyKwhInput:
        touBreakdown?.valleyKwh !== undefined ? String(touBreakdown.valleyKwh) : '',
      touDeepValleyKwhInput:
        touBreakdown?.deepValleyKwh !== undefined ? String(touBreakdown.deepValleyKwh) : '',
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
    touDeepValleyKwhInput: '',
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

export function buildUtilityBillTouBreakdown(
  values: UtilityBillFormValues,
): UtilityBillTouBreakdownWithDeepValley | undefined {
  const out: {
    sharpKwh?: number;
    peakKwh?: number;
    flatKwh?: number;
    valleyKwh?: number;
    deepValleyKwh?: number;
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
  if (values.touDeepValleyKwhInput !== '') {
    out.deepValleyKwh = Number(Number(values.touDeepValleyKwhInput).toFixed(2));
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

export function buildUtilityBillMutationPayload(
  values: UtilityBillFormValues,
  meta: { readonly expectedRevision: number; readonly idempotencyKey: string },
): UtilityBillMutation {
  const totalPayableCny = computeUtilityBillTotalPayable({
    energyChargeCny: Number(values.energyChargeInput),
    basicChargeCny: Number(values.basicChargeInput),
    powerFactorAdjustmentCny: Number(values.powerFactorAdjustmentInput),
    otherChargesCny: Number(values.otherChargesInput),
  });
  const touBreakdown = buildUtilityBillTouBreakdown(values);

  return {
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
      values.basicChargeMode === 'NONE' ? 0 : Number(Number(values.basicChargeInput).toFixed(2)),
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
      expectedRevision: meta.expectedRevision,
      idempotencyKey: meta.idempotencyKey,
      reason: values.reason.trim(),
    },
  };
}
