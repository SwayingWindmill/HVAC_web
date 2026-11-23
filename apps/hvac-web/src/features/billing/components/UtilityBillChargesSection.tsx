import { useMemo } from 'react';
import { ChevronDown } from 'lucide-react';
import { useWatch, type UseFormReturn } from 'react-hook-form';
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible';
import { Field, FieldError, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { formatDecimal } from '@/lib/operator-format';
import { isOneOf } from '@/lib/typed';
import { cn } from '@/lib/utils';
import { BASIC_CHARGE_MODE_LABEL, computeUtilityBillTotalPayable } from '../billing-model';
import { BASIC_CHARGE_MODES, type UtilityBillFormValues } from '../utility-bill-form';
import { preventImeSubmit } from './billing-form-helpers';

export interface UtilityBillChargesSectionProps {
  readonly form: UseFormReturn<UtilityBillFormValues>;
  readonly disabled: boolean;
  readonly extraDetailsOpen: boolean;
  readonly onExtraDetailsOpenChange: (open: boolean) => void;
}

export function UtilityBillChargesSection({
  form,
  disabled,
  extraDetailsOpen,
  onExtraDetailsOpenChange,
}: UtilityBillChargesSectionProps) {
  const watchedBasicMode = useWatch({ control: form.control, name: 'basicChargeMode' });
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

  const errors = form.formState.errors;

  return (
    <>
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
          <Field data-invalid={Boolean(errors.energyChargeInput) || undefined}>
            <FieldLabel htmlFor="bill-energy-charge">1. 电度电费（元）</FieldLabel>
            <Input
              id="bill-energy-charge"
              inputMode="decimal"
              placeholder="例如 98400"
              disabled={disabled}
              aria-invalid={errors.energyChargeInput ? 'true' : 'false'}
              aria-describedby={errors.energyChargeInput ? 'bill-energy-charge-error' : undefined}
              onKeyDown={preventImeSubmit}
              {...form.register('energyChargeInput')}
            />
            <FieldError id="bill-energy-charge-error" errors={[errors.energyChargeInput]} />
          </Field>

          <Field data-invalid={Boolean(errors.basicChargeMode) || undefined}>
            <FieldLabel htmlFor="bill-basic-mode">2. 基本电费计费方式</FieldLabel>
            <Select
              value={watchedBasicMode}
              disabled={disabled}
              onValueChange={(value) => {
                if (!isOneOf(BASIC_CHARGE_MODES, value)) return;
                form.setValue('basicChargeMode', value, { shouldValidate: true });
                if (value === 'NONE') {
                  form.setValue('basicChargeInput', '0', { shouldValidate: true });
                  form.setValue('basicChargeQuantityInput', '');
                }
              }}
            >
              <SelectTrigger
                id="bill-basic-mode"
                aria-label="基本电费计费方式"
                aria-invalid={errors.basicChargeMode ? 'true' : 'false'}
                aria-describedby={errors.basicChargeMode ? 'bill-basic-mode-error' : undefined}
              >
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
            <FieldError id="bill-basic-mode-error" errors={[errors.basicChargeMode]} />
          </Field>

          {watchedBasicMode !== 'NONE' ? (
            <Field data-invalid={Boolean(errors.basicChargeQuantityInput) || undefined}>
              <FieldLabel htmlFor="bill-basic-qty">
                {watchedBasicMode === 'TRANSFORMER_CAPACITY_KVA'
                  ? '计费变压器容量（kVA）'
                  : '结算最大需量（kW）'}
              </FieldLabel>
              <Input
                id="bill-basic-qty"
                inputMode="decimal"
                placeholder={watchedBasicMode === 'TRANSFORMER_CAPACITY_KVA' ? '2000' : '620'}
                disabled={disabled}
                aria-invalid={errors.basicChargeQuantityInput ? 'true' : 'false'}
                aria-describedby={
                  errors.basicChargeQuantityInput ? 'bill-basic-qty-error' : undefined
                }
                onKeyDown={preventImeSubmit}
                {...form.register('basicChargeQuantityInput')}
              />
              <FieldError
                id="bill-basic-qty-error"
                errors={[errors.basicChargeQuantityInput]}
              />
            </Field>
          ) : null}

          <Field data-invalid={Boolean(errors.basicChargeInput) || undefined}>
            <FieldLabel htmlFor="bill-basic-charge">基本电费金额（元）</FieldLabel>
            <Input
              id="bill-basic-charge"
              inputMode="decimal"
              disabled={disabled || watchedBasicMode === 'NONE'}
              aria-invalid={errors.basicChargeInput ? 'true' : 'false'}
              aria-describedby={errors.basicChargeInput ? 'bill-basic-charge-error' : undefined}
              onKeyDown={preventImeSubmit}
              {...form.register('basicChargeInput')}
            />
            <FieldError id="bill-basic-charge-error" errors={[errors.basicChargeInput]} />
          </Field>

          <Field data-invalid={Boolean(errors.powerFactorAdjustmentInput) || undefined}>
            <FieldLabel htmlFor="bill-pf-adjustment">3. 力调电费（元，奖励填负数）</FieldLabel>
            <Input
              id="bill-pf-adjustment"
              inputMode="decimal"
              placeholder="例如 -1257.00"
              disabled={disabled}
              aria-invalid={errors.powerFactorAdjustmentInput ? 'true' : 'false'}
              aria-describedby={
                errors.powerFactorAdjustmentInput ? 'bill-pf-adjustment-error' : undefined
              }
              onKeyDown={preventImeSubmit}
              {...form.register('powerFactorAdjustmentInput')}
            />
            <FieldError
              id="bill-pf-adjustment-error"
              errors={[errors.powerFactorAdjustmentInput]}
            />
          </Field>

          <Field data-invalid={Boolean(errors.otherChargesInput) || undefined}>
            <FieldLabel htmlFor="bill-other-charges">4. 其他代征及附加（元）</FieldLabel>
            <Input
              id="bill-other-charges"
              inputMode="decimal"
              placeholder="例如 357.00"
              disabled={disabled}
              aria-invalid={errors.otherChargesInput ? 'true' : 'false'}
              aria-describedby={errors.otherChargesInput ? 'bill-other-charges-error' : undefined}
              onKeyDown={preventImeSubmit}
              {...form.register('otherChargesInput')}
            />
            <FieldError id="bill-other-charges-error" errors={[errors.otherChargesInput]} />
          </Field>
        </div>
      </div>

      <Collapsible open={extraDetailsOpen} onOpenChange={onExtraDetailsOpenChange}>
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
              <Field data-invalid={Boolean(errors.utilityAccountNo) || undefined}>
                <FieldLabel htmlFor="bill-account-no">供电户号（选填）</FieldLabel>
                <Input
                  id="bill-account-no"
                  placeholder="例如 0800123456"
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  disabled={disabled}
                  aria-invalid={errors.utilityAccountNo ? 'true' : 'false'}
                  aria-describedby={errors.utilityAccountNo ? 'bill-account-no-error' : undefined}
                  onKeyDown={preventImeSubmit}
                  {...form.register('utilityAccountNo')}
                />
                <FieldError id="bill-account-no-error" errors={[errors.utilityAccountNo]} />
              </Field>

              <Field data-invalid={Boolean(errors.standardPowerFactorInput) || undefined}>
                <FieldLabel htmlFor="bill-std-pf">考核标准功率因数（选填）</FieldLabel>
                <Input
                  id="bill-std-pf"
                  inputMode="decimal"
                  placeholder="例如 0.90"
                  disabled={disabled}
                  aria-invalid={errors.standardPowerFactorInput ? 'true' : 'false'}
                  aria-describedby={errors.standardPowerFactorInput ? 'bill-std-pf-error' : undefined}
                  onKeyDown={preventImeSubmit}
                  {...form.register('standardPowerFactorInput')}
                />
                <FieldError id="bill-std-pf-error" errors={[errors.standardPowerFactorInput]} />
              </Field>

              <Field data-invalid={Boolean(errors.actualPowerFactorInput) || undefined}>
                <FieldLabel htmlFor="bill-act-pf">实际功率因数（选填）</FieldLabel>
                <Input
                  id="bill-act-pf"
                  inputMode="decimal"
                  placeholder="例如 0.95"
                  disabled={disabled}
                  aria-invalid={errors.actualPowerFactorInput ? 'true' : 'false'}
                  aria-describedby={errors.actualPowerFactorInput ? 'bill-act-pf-error' : undefined}
                  onKeyDown={preventImeSubmit}
                  {...form.register('actualPowerFactorInput')}
                />
                <FieldError id="bill-act-pf-error" errors={[errors.actualPowerFactorInput]} />
              </Field>
            </div>

            <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
              <Field data-invalid={Boolean(errors.touSuperPeakKwhInput) || undefined}>
                <FieldLabel htmlFor="bill-tou-super-peak">尖峰电量（kWh）</FieldLabel>
                <Input
                  id="bill-tou-super-peak"
                  inputMode="decimal"
                  disabled={disabled}
                  aria-invalid={errors.touSuperPeakKwhInput ? 'true' : 'false'}
                  aria-describedby={
                    errors.touSuperPeakKwhInput ? 'bill-tou-super-peak-error' : undefined
                  }
                  onKeyDown={preventImeSubmit}
                  {...form.register('touSuperPeakKwhInput')}
                />
                <FieldError
                  id="bill-tou-super-peak-error"
                  errors={[errors.touSuperPeakKwhInput]}
                />
              </Field>

              <Field data-invalid={Boolean(errors.touPeakKwhInput) || undefined}>
                <FieldLabel htmlFor="bill-tou-peak">峰段电量（kWh）</FieldLabel>
                <Input
                  id="bill-tou-peak"
                  inputMode="decimal"
                  disabled={disabled}
                  aria-invalid={errors.touPeakKwhInput ? 'true' : 'false'}
                  aria-describedby={errors.touPeakKwhInput ? 'bill-tou-peak-error' : undefined}
                  onKeyDown={preventImeSubmit}
                  {...form.register('touPeakKwhInput')}
                />
                <FieldError id="bill-tou-peak-error" errors={[errors.touPeakKwhInput]} />
              </Field>

              <Field data-invalid={Boolean(errors.touFlatKwhInput) || undefined}>
                <FieldLabel htmlFor="bill-tou-flat">平段电量（kWh）</FieldLabel>
                <Input
                  id="bill-tou-flat"
                  inputMode="decimal"
                  disabled={disabled}
                  aria-invalid={errors.touFlatKwhInput ? 'true' : 'false'}
                  aria-describedby={errors.touFlatKwhInput ? 'bill-tou-flat-error' : undefined}
                  onKeyDown={preventImeSubmit}
                  {...form.register('touFlatKwhInput')}
                />
                <FieldError id="bill-tou-flat-error" errors={[errors.touFlatKwhInput]} />
              </Field>

              <Field data-invalid={Boolean(errors.touValleyKwhInput) || undefined}>
                <FieldLabel htmlFor="bill-tou-valley">谷段电量（kWh）</FieldLabel>
                <Input
                  id="bill-tou-valley"
                  inputMode="decimal"
                  disabled={disabled}
                  aria-invalid={errors.touValleyKwhInput ? 'true' : 'false'}
                  aria-describedby={errors.touValleyKwhInput ? 'bill-tou-valley-error' : undefined}
                  onKeyDown={preventImeSubmit}
                  {...form.register('touValleyKwhInput')}
                />
                <FieldError id="bill-tou-valley-error" errors={[errors.touValleyKwhInput]} />
              </Field>

              <Field data-invalid={Boolean(errors.touDeepValleyKwhInput) || undefined}>
                <FieldLabel htmlFor="bill-tou-deep-valley">深谷电量（kWh）</FieldLabel>
                <Input
                  id="bill-tou-deep-valley"
                  inputMode="decimal"
                  disabled={disabled}
                  aria-invalid={errors.touDeepValleyKwhInput ? 'true' : 'false'}
                  aria-describedby={
                    errors.touDeepValleyKwhInput ? 'bill-tou-deep-valley-error' : undefined
                  }
                  onKeyDown={preventImeSubmit}
                  {...form.register('touDeepValleyKwhInput')}
                />
                <FieldError
                  id="bill-tou-deep-valley-error"
                  errors={[errors.touDeepValleyKwhInput]}
                />
              </Field>
            </div>
          </CollapsibleContent>
        </div>
      </Collapsible>
    </>
  );
}
