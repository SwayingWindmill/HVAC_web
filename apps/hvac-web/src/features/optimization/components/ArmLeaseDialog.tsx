import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useId, useMemo, useState, type KeyboardEvent } from 'react';
import { Controller, useForm, useWatch, type FieldErrors, type Resolver } from 'react-hook-form';
import { notifySuccess } from '@/lib/toast';
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
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import { CodeField } from '@/features/control/components/CommandConfirmation';
import { REASON_LIMIT } from '@/features/control/control-state';
import { stepUpErrorPresentation, type StepUpErrorPresentation } from '@/features/control/control-errors';
import {
  armSiteAutoControlLease,
  autoControlKeys,
  AutoControlRequestError,
  type SiteAutoControlLeaseState,
} from '../auto-control-api';
import {
  AUTO_CONTROL_EXECUTION_MODES,
  buildArmLeasePayload,
  EXECUTION_MODE_PRESENTATION,
  formatAutoControlCapability,
  initialArmLeaseValues,
  resolveAutoControlExecutionMode,
  SELECTABLE_LEASE_CAPABILITIES,
  validateArmLeaseForm,
  type ArmLeaseFieldName,
  type ArmLeaseFormValues,
  type ArmLeaseNumericField,
} from '../auto-control-model';

type ScalarArmLeaseField = Exclude<ArmLeaseFieldName, 'executionMode' | 'allowedCapabilities'>;

interface NumericFieldSpec {
  readonly name: ArmLeaseNumericField;
  readonly suffix: string;
  readonly label: string;
  readonly unit: string;
  readonly min?: number;
  readonly placeholder?: string;
  readonly step: string;
  readonly pairMax?: ArmLeaseNumericField;
  readonly hint?: string;
}

const LEASE_LIMIT_FIELDS: readonly NumericFieldSpec[] = [
  { name: 'expiresInHours', suffix: 'hours', label: '租约有效期', unit: '小时 (1 至 24)', step: '1' },
  { name: 'maxCommandsPerHour', suffix: 'max-per-hour', label: '每小时最多下发次数', unit: '次 (1 至 6)', step: '1' },
];

const CHILLER_GUARD_FIELDS: readonly NumericFieldSpec[] = [
  { name: 'supplyTempMinC', suffix: 'supply-min', label: '供水温度下限', unit: '°C (5.0 至 20.0)', min: 5, placeholder: '5.0', step: '0.1', pairMax: 'supplyTempMaxC' },
  { name: 'supplyTempMaxC', suffix: 'supply-max', label: '供水温度上限', unit: '°C (不高于 25.0)', min: 5, step: '0.1' },
  { name: 'maxSetpointStepC', suffix: 'max-step', label: '单次最大步长', unit: '°C (0.1 至 2.0)', step: '0.1' },
  { name: 'deadbandC', suffix: 'deadband', label: '调节死区', unit: '°C (0.05 至 2.0)', min: 0.05, placeholder: '0.2', step: '0.05' },
  { name: 'zoneTempMaxC', suffix: 'zone-max', label: '室内温度保护上限', unit: '°C (18.0 至 32.0)', step: '0.1' },
  { name: 'minRunningChillers', suffix: 'min-chillers', label: '最少运行冷机数', unit: '台 (>= 0)', step: '1' },
];

const PUMP_FREQ_FIELDS: readonly NumericFieldSpec[] = [
  { name: 'pumpFreqMinHz', suffix: 'pump-freq-min', label: '水泵下限频率', unit: 'Hz (20.0 至 50.0)', min: 20, step: '0.5', pairMax: 'pumpFreqMaxHz' },
  { name: 'pumpFreqMaxHz', suffix: 'pump-freq-max', label: '水泵上限频率', unit: 'Hz (25.0 至 60.0)', min: 25, step: '0.5' },
  { name: 'maxFreqStepHz', suffix: 'pump-freq-step', label: '单次调频步长', unit: 'Hz (不超过 10.0)', min: 0.1, step: '0.5' },
  { name: 'freqDeadbandHz', suffix: 'pump-freq-deadband', label: '调频防抖死区', unit: 'Hz (0.1 至 5.0)', min: 0.1, step: '0.1' },
];

const FAN_SPEED_FIELDS: readonly NumericFieldSpec[] = [
  { name: 'fanSpeedMinPct', suffix: 'fan-speed-min', label: '风机下限转速', unit: '% (15 至 100)', min: 15, step: '1', pairMax: 'fanSpeedMaxPct' },
  { name: 'fanSpeedMaxPct', suffix: 'fan-speed-max', label: '风机上限转速', unit: '% (20 至 100)', min: 20, step: '1' },
  { name: 'maxFanStepPct', suffix: 'fan-step', label: '单次调速步长', unit: '% (不超过 25)', min: 0.5, step: '1' },
  { name: 'fanDeadbandPct', suffix: 'fan-deadband', label: '调速防抖死区', unit: '% (0.5 至 15.0)', min: 0.5, step: '0.5' },
];

const ALL_NUMERIC_FIELDS: readonly NumericFieldSpec[] = [
  ...LEASE_LIMIT_FIELDS,
  ...CHILLER_GUARD_FIELDS,
  ...PUMP_FREQ_FIELDS,
  ...FAN_SPEED_FIELDS,
];

const SCALAR_FIELD_NAMES: readonly ScalarArmLeaseField[] = [
  ...ALL_NUMERIC_FIELDS.map((field) => field.name),
  'reason',
  'verificationCode',
];

const FIELD_FOCUS_SUFFIXES: readonly (readonly [ArmLeaseFieldName, string])[] = [
  ['expiresInHours', 'hours'],
  ['maxCommandsPerHour', 'max-per-hour'],
  ['supplyTempMinC', 'supply-min'],
  ['supplyTempMaxC', 'supply-max'],
  ['maxSetpointStepC', 'max-step'],
  ['deadbandC', 'deadband'],
  ['zoneTempMaxC', 'zone-max'],
  ['minRunningChillers', 'min-chillers'],
  ['pumpFreqMinHz', 'pump-freq-min'],
  ['pumpFreqMaxHz', 'pump-freq-max'],
  ['maxFreqStepHz', 'pump-freq-step'],
  ['freqDeadbandHz', 'pump-freq-deadband'],
  ['fanSpeedMinPct', 'fan-speed-min'],
  ['fanSpeedMaxPct', 'fan-speed-max'],
  ['maxFanStepPct', 'fan-step'],
  ['fanDeadbandPct', 'fan-deadband'],
  ['reason', 'reason'],
  ['verificationCode', 'code'],
];

const FREQUENCY_FIELDS: readonly ArmLeaseNumericField[] = ['pumpFreqMinHz', 'pumpFreqMaxHz', 'maxFreqStepHz', 'freqDeadbandHz'];
const FAN_FIELDS: readonly ArmLeaseNumericField[] = ['fanSpeedMinPct', 'fanSpeedMaxPct', 'maxFanStepPct', 'fanDeadbandPct'];

function guardImeEnter(event: KeyboardEvent<HTMLInputElement>) {
  if (event.key === 'Enter' && (event.nativeEvent.isComposing || event.keyCode === 229)) {
    event.preventDefault();
  }
}

type WritableFieldErrors = {
  -readonly [K in keyof FieldErrors<ArmLeaseFormValues>]: FieldErrors<ArmLeaseFormValues>[K];
};

const armLeaseResolver: Resolver<ArmLeaseFormValues> = (values, _context, options) => {
  const targetNames = options.names && options.names.length > 0 ? new Set(options.names) : null;
  const includeSubmitOnly = targetNames === null || targetNames.has('reason') || targetNames.has('verificationCode');
  const rawErrors = validateArmLeaseForm(values, { includeSubmitOnly });
  const errors: WritableFieldErrors = {};

  for (const field of SCALAR_FIELD_NAMES) {
    const message = rawErrors[field];
    if (message && (targetNames === null || targetNames.has(field))) {
      errors[field] = { type: 'validate', message };
    }
  }
  if (rawErrors.allowedCapabilities && (targetNames === null || targetNames.has('allowedCapabilities'))) {
    errors.allowedCapabilities = { type: 'validate', message: rawErrors.allowedCapabilities };
  }

  return Object.keys(errors).length > 0
    ? { values: {}, errors }
    : { values, errors: {} };
};

export function ArmLeaseDialog({
  siteId,
  csrfToken,
  leaseState,
  open,
  onOpenChange,
}: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly leaseState: SiteAutoControlLeaseState;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
}) {
  const [busy, setBusy] = useState(false);
  const seed = leaseState.activeLease ?? leaseState.latestLease;
  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen && !busy) onOpenChange(false); else if (nextOpen) onOpenChange(true); }}>
      <DialogContent
        showCloseButton={!busy}
        onEscapeKeyDown={(event) => {
          if (busy) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (busy) event.preventDefault();
        }}
        className="max-h-[90dvh] overflow-y-auto p-5 sm:max-w-[600px]"
      >
        {open ? (
          <ArmLeaseForm
            key={seed?.id ?? 'new'}
            siteId={siteId}
            csrfToken={csrfToken}
            activeRevision={leaseState.activeLease?.revision}
            seed={seed}
            isUpdate={leaseState.activeLease !== null}
            onBusyChange={setBusy}
            onClose={() => onOpenChange(false)}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function ArmLeaseForm({
  siteId,
  csrfToken,
  activeRevision,
  seed,
  isUpdate,
  onBusyChange,
  onClose,
}: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly activeRevision: number | undefined;
  readonly seed: SiteAutoControlLeaseState['activeLease'];
  readonly isUpdate: boolean;
  readonly onBusyChange: (busy: boolean) => void;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const baseId = useId();
  const [codeError, setCodeError] = useState<StepUpErrorPresentation | null>(null);
  const [formAlert, setFormAlert] = useState<string | null>(null);

  const form = useForm<ArmLeaseFormValues>({
    defaultValues: initialArmLeaseValues(seed),
    mode: 'onBlur',
    reValidateMode: 'onChange',
    resolver: armLeaseResolver,
    shouldFocusError: true,
  });

  const allowedCapabilities = useWatch({ control: form.control, name: 'allowedCapabilities' }) ?? [];
  const hasFrequencyCap = allowedCapabilities.includes('SET_FREQUENCY');
  const hasFanSpeedCap = allowedCapabilities.includes('SET_FAN_SPEED');

  const capabilityChoices = useMemo(() => {
    const merged = new Set<string>(SELECTABLE_LEASE_CAPABILITIES);
    for (const cap of allowedCapabilities) merged.add(cap);
    return Array.from(merged);
  }, [allowedCapabilities]);

  const mutation = useMutation({
    mutationFn: (payload: Parameters<typeof armSiteAutoControlLease>[1]) =>
      armSiteAutoControlLease(siteId, payload, csrfToken),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: (next, submitted) => {
      queryClient.setQueryData(autoControlKeys.lease(siteId), next);
      void queryClient.invalidateQueries({ queryKey: autoControlKeys.lease(siteId) });
      void queryClient.invalidateQueries({ queryKey: autoControlKeys.monitors(siteId) });
      const armedMode = resolveAutoControlExecutionMode(next.activeLease ?? { executionMode: submitted.executionMode });
      notifySuccess(
        armedMode === 'SHADOW'
          ? (isUpdate ? '已更新影子跟跑租约' : '已开启影子跟跑租约')
          : (isUpdate ? '已更新限时自动控制租约' : '已开启限时自动控制租约'),
      );
      onClose();
    },
    onError: (error) => {
      if (error instanceof AutoControlRequestError && error.code === 'STEP_UP_REQUIRED') {
        form.setValue('verificationCode', '', { shouldTouch: true });
        setCodeError(stepUpErrorPresentation(error.reason, true));
        return;
      }
      if (error instanceof AutoControlRequestError && error.code === 'REGISTRY_REVISION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: autoControlKeys.lease(siteId) });
      }
      setFormAlert(error instanceof AutoControlRequestError ? error.message : '开启自动控制租约失败，请稍后再试');
    },
  });

  const clearResolvedError = (name: ScalarArmLeaseField, includeSubmitOnly = false) => {
    if (!form.getFieldState(name).error) return;
    const nextErrors = validateArmLeaseForm(form.getValues(), { includeSubmitOnly });
    if (!nextErrors[name]) {
      form.clearErrors(name);
    }
  };

  const renderNumericGrid = (fields: readonly NumericFieldSpec[]) => (
    <div className="grid grid-cols-1 gap-x-3 gap-y-2 sm:grid-cols-2">
      {fields.map((spec) => {
        const id = `${baseId}-${spec.suffix}`;
        const errorMessage = form.formState.errors[spec.name]?.message;
        const registration = form.register(spec.name, {
          onChange: () => {
            setFormAlert(null);
            clearResolvedError(spec.name);
          },
          onBlur: () => {
            if (spec.pairMax) {
              const pairState = form.getFieldState(spec.pairMax);
              if (pairState.isTouched || pairState.error) {
                void form.trigger(spec.pairMax);
              }
            }
          },
        });
        return (
          <Field key={spec.name} data-invalid={errorMessage ? 'true' : undefined} className="gap-1.5">
            <FieldLabel htmlFor={id} className="flex w-full items-baseline justify-between gap-2 text-xs">
              <span>{spec.label}</span>
              <span className="font-normal text-muted-foreground">{spec.unit}</span>
            </FieldLabel>
            <Input
              id={id}
              type="number"
              inputMode="decimal"
              min={spec.min}
              placeholder={spec.placeholder}
              step={spec.step}
              aria-invalid={errorMessage ? 'true' : 'false'}
              aria-describedby={errorMessage ? `${id}-error` : spec.hint ? `${id}-hint` : undefined}
              onKeyDown={guardImeEnter}
              {...registration}
            />
            {errorMessage ? (
              <FieldError id={`${id}-error`} className="text-xs text-destructive-text">
                {errorMessage}
              </FieldError>
            ) : spec.hint ? (
              <FieldDescription id={`${id}-hint`} className="text-xs">
                {spec.hint}
              </FieldDescription>
            ) : null}
          </Field>
        );
      })}
    </div>
  );

  const handleValidSubmit = (values: ArmLeaseFormValues) => {
    if (mutation.isPending) return;
    setFormAlert(null);
    const built = buildArmLeasePayload(values, activeRevision);
    if (!built.ok) {
      for (const field of SCALAR_FIELD_NAMES) {
        const msg = built.errors[field];
        if (msg) form.setError(field, { type: 'validate', message: msg });
      }
      if (built.errors.allowedCapabilities) {
        form.setError('allowedCapabilities', { type: 'validate', message: built.errors.allowedCapabilities });
      }
      return;
    }
    setCodeError(null);
    mutation.mutate(built.payload);
  };

  const handleInvalidSubmit = (errors: FieldErrors<ArmLeaseFormValues>) => {
    setFormAlert(null);
    if (errors.verificationCode?.message) {
      setCodeError({ message: errors.verificationCode.message, showEnrollLink: false });
    }
    const firstInvalid = FIELD_FOCUS_SUFFIXES.find(([field]) => errors[field] !== undefined);
    const targetId = firstInvalid
      ? `${baseId}-${firstInvalid[1]}`
      : errors.allowedCapabilities && capabilityChoices[0]
        ? `${baseId}-cap-${capabilityChoices[0]}`
        : null;
    if (targetId) {
      document.getElementById(targetId)?.focus();
    }
  };

  const submitForm = form.handleSubmit(handleValidSubmit, handleInvalidSubmit);

  return (
    <form
      noValidate
      className="flex flex-col gap-3.5"
      onSubmit={(event) => {
        event.preventDefault();
        if (!mutation.isPending) void submitForm(event);
      }}
    >
      <DialogHeader className="gap-1">
        <DialogTitle>{isUpdate ? '调整自动控制租约边界' : '开启限时自动控制租约'}</DialogTitle>
        <DialogDescription>设置自动执行模式、物理安全边界与授权时长，校验动态口令后生效。</DialogDescription>
      </DialogHeader>

      <fieldset className="flex flex-col gap-1.5">
        <legend className="text-xs font-medium text-foreground">运行模式</legend>
        <Controller
          control={form.control}
          name="executionMode"
          render={({ field }) => (
            <RadioGroup
              name={field.name}
              value={field.value}
              onValueChange={(next) => {
                if (next === 'CLOSED_LOOP' || next === 'SHADOW') {
                  setFormAlert(null);
                  field.onChange(next);
                }
              }}
              aria-label="运行模式"
              className="grid-cols-1 gap-2 sm:grid-cols-2"
            >
              {AUTO_CONTROL_EXECUTION_MODES.map((mode) => {
                const option = EXECUTION_MODE_PRESENTATION[mode];
                const itemId = `${baseId}-mode-${mode}`;
                return (
                  <Label
                    key={mode}
                    htmlFor={itemId}
                    className="grid cursor-pointer grid-cols-[16px_minmax(0,1fr)] items-start gap-x-2.5 gap-y-1 rounded-lg border border-border px-3 py-2.5 font-normal transition-colors hover:border-input-hover has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary-soft"
                  >
                    <RadioGroupItem id={itemId} value={mode} className="mt-0.5" />
                    <span className="text-xs font-semibold text-foreground">{option.optionTitle}</span>
                    <span className="col-start-2 text-xs leading-relaxed text-secondary-foreground">
                      {option.optionDescription}
                    </span>
                  </Label>
                );
              })}
            </RadioGroup>
          )}
        />
      </fieldset>

      <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/20 p-3">
        <legend className="px-1 text-xs font-medium text-foreground">授权时效与频次</legend>
        {renderNumericGrid(LEASE_LIMIT_FIELDS)}
      </fieldset>

      <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/20 p-3">
        <legend className="px-1 text-xs font-medium text-foreground">冷机供水与安全护栏</legend>
        {renderNumericGrid(CHILLER_GUARD_FIELDS)}
      </fieldset>

      <fieldset className="flex flex-col gap-1.5">
        <legend className="text-xs font-medium text-foreground">允许自动下发的能力</legend>
        <Controller
          control={form.control}
          name="allowedCapabilities"
          render={({ field, fieldState }) => (
            <>
              <div
                aria-invalid={fieldState.error ? 'true' : 'false'}
                aria-describedby={fieldState.error ? `${baseId}-caps-error` : undefined}
                className="grid grid-cols-1 gap-2 rounded-lg border border-divider bg-muted/40 p-2.5 sm:grid-cols-3"
              >
                {capabilityChoices.map((cap) => {
                  const capId = `${baseId}-cap-${cap}`;
                  const checked = field.value.includes(cap);
                  return (
                    <div key={cap} className="flex items-center gap-2">
                      <Checkbox
                        id={capId}
                        checked={checked}
                        onCheckedChange={(state) => {
                          setFormAlert(null);
                          const next = state === true
                            ? Array.from(new Set([...field.value, cap]))
                            : field.value.filter((item) => item !== cap);
                          field.onChange(next);
                          if (!next.includes('SET_FREQUENCY')) form.clearErrors(FREQUENCY_FIELDS);
                          if (!next.includes('SET_FAN_SPEED')) form.clearErrors(FAN_FIELDS);
                          void form.trigger('allowedCapabilities');
                        }}
                      />
                      <Label htmlFor={capId} className="cursor-pointer text-xs font-normal text-foreground">
                        {formatAutoControlCapability(cap)}
                      </Label>
                    </div>
                  );
                })}
              </div>
              {fieldState.error?.message ? (
                <FieldError id={`${baseId}-caps-error`} className="text-xs text-destructive-text">
                  {fieldState.error.message}
                </FieldError>
              ) : null}
            </>
          )}
        />
      </fieldset>

      {hasFrequencyCap ? (
        <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/20 p-3">
          <legend className="px-1 text-xs font-medium text-foreground">水泵变频边界 (Hz)</legend>
          {renderNumericGrid(PUMP_FREQ_FIELDS)}
        </fieldset>
      ) : null}

      {hasFanSpeedCap ? (
        <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/20 p-3">
          <legend className="px-1 text-xs font-medium text-foreground">冷却塔风机边界 (%)</legend>
          {renderNumericGrid(FAN_SPEED_FIELDS)}
        </fieldset>
      ) : null}

      <fieldset className="flex flex-col gap-3 rounded-lg border border-divider bg-muted/20 p-3">
        <legend className="px-1 text-xs font-medium text-foreground">授权依据与身份核验</legend>
        <Controller
          control={form.control}
          name="reason"
          render={({ field, fieldState }) => {
            const reasonId = `${baseId}-reason`;
            const errMsg = fieldState.error?.message;
            return (
              <Field data-invalid={errMsg ? 'true' : undefined} className="gap-1.5">
                <FieldLabel htmlFor={reasonId} className="flex w-full justify-between text-xs">
                  <span>原因</span>
                  <span className="font-mono font-normal text-muted-foreground">{field.value.length}/{REASON_LIMIT}</span>
                </FieldLabel>
                <Textarea
                  id={reasonId}
                  name={field.name}
                  value={field.value}
                  placeholder="例如：白班冷负荷平稳，开启 8 小时自动供水温度优化"
                  maxLength={REASON_LIMIT}
                  aria-invalid={errMsg ? 'true' : 'false'}
                  aria-describedby={errMsg ? `${reasonId}-error` : undefined}
                  onBlur={field.onBlur}
                  onChange={(event) => {
                    setFormAlert(null);
                    field.onChange(event.target.value);
                    clearResolvedError('reason', true);
                  }}
                  className="min-h-[72px] resize-none"
                />
                {errMsg ? (
                  <FieldError id={`${reasonId}-error`} className="text-xs text-destructive-text">
                    {errMsg}
                  </FieldError>
                ) : null}
              </Field>
            );
          }}
        />

        <Controller
          control={form.control}
          name="verificationCode"
          render={({ field, fieldState }) => {
            const effectiveError = codeError?.message ?? fieldState.error?.message ?? null;
            return (
              <div onBlur={field.onBlur}>
                <CodeField
                  id={`${baseId}-code`}
                  value={field.value}
                  error={effectiveError}
                  showEnrollLink={codeError?.showEnrollLink}
                  onNavigateEnroll={onClose}
                  onSubmit={() => {
                    if (!mutation.isPending) void submitForm();
                  }}
                  onChange={(next) => {
                    setFormAlert(null);
                    setCodeError(null);
                    field.onChange(next);
                    clearResolvedError('verificationCode', true);
                  }}
                />
              </div>
            );
          }}
        />
      </fieldset>

      {formAlert ? (
        <Alert variant="destructive">
          <AlertTitle>未能保存自动控制租约</AlertTitle>
          <AlertDescription>{formAlert}</AlertDescription>
        </Alert>
      ) : null}

      <DialogFooter>
        <Button type="button" variant="outline" disabled={mutation.isPending} onClick={onClose}>
          取消
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          {mutation.isPending ? <Spinner data-icon="inline-start" /> : null}
          {mutation.isPending ? '正在提交' : isUpdate ? '确认更新租约' : '确认开启租约'}
        </Button>
      </DialogFooter>
    </form>
  );
}
