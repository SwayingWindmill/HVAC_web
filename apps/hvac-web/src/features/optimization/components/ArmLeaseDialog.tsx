import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useId, useMemo, useState, type KeyboardEvent } from 'react';
import { toast } from 'sonner';
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
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Spinner } from '@/components/ui/spinner';
import { CodeField } from '@/features/control/components/CommandConfirmation';
import { ReasonField } from '@/features/control/components/ControlBoundary';
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
  type ArmLeaseFieldErrors,
  type ArmLeaseFieldName,
  type ArmLeaseFormValues,
} from '../auto-control-model';

function guardImeEnter(event: KeyboardEvent<HTMLInputElement>) {
  if (event.key === 'Enter' && (event.nativeEvent.isComposing || event.keyCode === 229)) {
    event.preventDefault();
  }
}

function NumericField({
  id,
  label,
  unit,
  hint,
  min,
  placeholder,
  value,
  error,
  step = 'any',
  onChange,
  onBlur,
}: {
  readonly id: string;
  readonly label: string;
  readonly unit?: string;
  readonly hint?: string;
  readonly min?: number;
  readonly placeholder?: string;
  readonly value: string;
  readonly error: string | undefined;
  readonly step?: string;
  readonly onChange: (value: string) => void;
  readonly onBlur: () => void;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id} className="flex items-baseline justify-between gap-2 text-xs">
        <span>{label}</span>
        {unit ? <span className="font-normal text-muted-foreground">{unit}</span> : null}
      </Label>
      <Input
        id={id}
        type="number"
        inputMode="decimal"
        min={min}
        placeholder={placeholder}
        step={step}
        value={value}
        aria-invalid={error !== undefined}
        aria-describedby={error ? `${id}-error` : hint ? `${id}-hint` : undefined}
        onKeyDown={guardImeEnter}
        onChange={(event) => onChange(event.target.value)}
        onBlur={onBlur}
      />
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-xs text-destructive-text">{error}</p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">{hint}</p>
      ) : null}
    </div>
  );
}

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
] as const;

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
  const seed = leaseState.activeLease ?? leaseState.latestLease;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto p-5 sm:max-w-[600px]">
        {open ? (
          <ArmLeaseForm
            key={seed?.id ?? 'new'}
            siteId={siteId}
            csrfToken={csrfToken}
            activeRevision={leaseState.activeLease?.revision}
            seed={seed}
            isUpdate={leaseState.activeLease !== null}
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
  onClose,
}: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly activeRevision: number | undefined;
  readonly seed: SiteAutoControlLeaseState['activeLease'];
  readonly isUpdate: boolean;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const baseId = useId();
  const [values, setValues] = useState<ArmLeaseFormValues>(() => initialArmLeaseValues(seed));
  const [touched, setTouched] = useState<Partial<Record<ArmLeaseFieldName, boolean>>>({});
  const [fieldErrors, setFieldErrors] = useState<ArmLeaseFieldErrors>({});
  const [codeError, setCodeError] = useState<StepUpErrorPresentation | null>(null);
  const [formAlert, setFormAlert] = useState<string | null>(null);

  const capabilityChoices = useMemo(() => {
    const merged = new Set<string>(SELECTABLE_LEASE_CAPABILITIES);
    for (const cap of values.allowedCapabilities) merged.add(cap);
    return Array.from(merged);
  }, [values.allowedCapabilities]);

  const mutation = useMutation({
    mutationFn: (payload: Parameters<typeof armSiteAutoControlLease>[1]) =>
      armSiteAutoControlLease(siteId, payload, csrfToken),
    onSuccess: (next, submitted) => {
      queryClient.setQueryData(autoControlKeys.lease(siteId), next);
      void queryClient.invalidateQueries({ queryKey: autoControlKeys.lease(siteId) });
      void queryClient.invalidateQueries({ queryKey: autoControlKeys.monitors(siteId) });
      const armedMode = resolveAutoControlExecutionMode(next.activeLease ?? { executionMode: submitted.executionMode });
      toast.success(
        armedMode === 'SHADOW'
          ? (isUpdate ? '已更新影子跟跑租约' : '已开启影子跟跑租约')
          : (isUpdate ? '已更新限时自动控制租约' : '已开启限时自动控制租约'),
      );
      onClose();
    },
    onError: (error) => {
      if (error instanceof AutoControlRequestError && error.code === 'STEP_UP_REQUIRED') {
        setValues((prev) => ({ ...prev, verificationCode: '' }));
        setCodeError(stepUpErrorPresentation(error.reason, true));
        return;
      }
      if (error instanceof AutoControlRequestError && error.code === 'REGISTRY_REVISION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: autoControlKeys.lease(siteId) });
      }
      setFormAlert(error instanceof AutoControlRequestError ? error.message : '开启自动控制租约失败，请稍后再试');
    },
  });

  const updateField = <K extends keyof ArmLeaseFormValues>(field: K, nextValue: ArmLeaseFormValues[K]) => {
    const nextValues = { ...values, [field]: nextValue };
    setValues(nextValues);
    setFormAlert(null);
    if (field === 'verificationCode') {
      setCodeError(null);
    }
    if (touched[field]) {
      const nextValidation = validateArmLeaseForm(nextValues, { includeSubmitOnly: Boolean(touched.reason || touched.verificationCode) });
      setFieldErrors((prev) => ({ ...prev, [field]: nextValidation[field] }));
    }
  };

  const hasFrequencyCap = values.allowedCapabilities.includes('SET_FREQUENCY');
  const hasFanSpeedCap = values.allowedCapabilities.includes('SET_FAN_SPEED');

  const handleBlur = (field: ArmLeaseFieldName) => {
    setTouched((prev) => ({ ...prev, [field]: true }));
    const nextValidation = validateArmLeaseForm(values, { includeSubmitOnly: field === 'reason' || field === 'verificationCode' });
    setFieldErrors((prev) => ({
      ...prev,
      [field]: nextValidation[field],
      ...(field === 'supplyTempMinC' ? { supplyTempMaxC: touched.supplyTempMaxC ? nextValidation.supplyTempMaxC : prev.supplyTempMaxC } : {}),
      ...(field === 'pumpFreqMinHz' ? { pumpFreqMaxHz: touched.pumpFreqMaxHz ? nextValidation.pumpFreqMaxHz : prev.pumpFreqMaxHz } : {}),
      ...(field === 'fanSpeedMinPct' ? { fanSpeedMaxPct: touched.fanSpeedMaxPct ? nextValidation.fanSpeedMaxPct : prev.fanSpeedMaxPct } : {}),
    }));
  };

  const toggleCapability = (cap: string, checked: boolean) => {
    const next = checked
      ? Array.from(new Set([...values.allowedCapabilities, cap]))
      : values.allowedCapabilities.filter((item) => item !== cap);
    setTouched((prev) => ({ ...prev, allowedCapabilities: true }));
    const nextValues = { ...values, allowedCapabilities: next };
    setValues(nextValues);
    const nextValidation = validateArmLeaseForm(nextValues, { includeSubmitOnly: Boolean(touched.reason || touched.verificationCode) });
    setFieldErrors((prev) => ({
      ...prev,
      allowedCapabilities: nextValidation.allowedCapabilities,
      pumpFreqMinHz: nextValidation.pumpFreqMinHz,
      pumpFreqMaxHz: nextValidation.pumpFreqMaxHz,
      maxFreqStepHz: nextValidation.maxFreqStepHz,
      freqDeadbandHz: nextValidation.freqDeadbandHz,
      fanSpeedMinPct: nextValidation.fanSpeedMinPct,
      fanSpeedMaxPct: nextValidation.fanSpeedMaxPct,
      maxFanStepPct: nextValidation.maxFanStepPct,
      fanDeadbandPct: nextValidation.fanDeadbandPct,
    }));
  };

  const handleSubmit = () => {
    setFormAlert(null);
    const built = buildArmLeasePayload(values, activeRevision);
    if (!built.ok) {
      setFieldErrors(built.errors);
      setTouched({
        expiresInHours: true,
        supplyTempMinC: true,
        supplyTempMaxC: true,
        maxSetpointStepC: true,
        deadbandC: true,
        zoneTempMaxC: true,
        minRunningChillers: true,
        maxCommandsPerHour: true,
        pumpFreqMinHz: hasFrequencyCap,
        pumpFreqMaxHz: hasFrequencyCap,
        maxFreqStepHz: hasFrequencyCap,
        freqDeadbandHz: hasFrequencyCap,
        fanSpeedMinPct: hasFanSpeedCap,
        fanSpeedMaxPct: hasFanSpeedCap,
        maxFanStepPct: hasFanSpeedCap,
        fanDeadbandPct: hasFanSpeedCap,
        allowedCapabilities: true,
        reason: true,
        verificationCode: true,
      });
      if (built.errors.verificationCode) {
        setCodeError({ message: built.errors.verificationCode, showEnrollLink: false });
      }
      const firstInvalid = FIELD_FOCUS_SUFFIXES.find(([field]) => built.errors[field] !== undefined);
      const targetId = firstInvalid
        ? `${baseId}-${firstInvalid[1]}`
        : built.errors.allowedCapabilities && capabilityChoices[0]
          ? `${baseId}-cap-${capabilityChoices[0]}`
          : null;
      if (targetId) {
        document.getElementById(targetId)?.focus();
      }
      return;
    }
    setFieldErrors({});
    setCodeError(null);
    mutation.mutate(built.payload);
  };

  return (
    <form
      noValidate
      className="flex flex-col gap-3.5"
      onSubmit={(event) => {
        event.preventDefault();
        if (!mutation.isPending) handleSubmit();
      }}
    >
      <DialogHeader className="gap-1">
        <DialogTitle>{isUpdate ? '调整自动控制租约边界' : '开启限时自动控制租约'}</DialogTitle>
        <DialogDescription>设置自动执行模式、物理安全边界与授权时长，校验动态口令后生效。</DialogDescription>
      </DialogHeader>

      <fieldset className="flex flex-col gap-1.5">
        <legend className="text-xs font-medium text-foreground">运行模式</legend>
        <RadioGroup
          value={values.executionMode}
          onValueChange={(next) => {
            if (next === 'CLOSED_LOOP' || next === 'SHADOW') {
              updateField('executionMode', next);
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
      </fieldset>

      <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/20 p-3">
        <legend className="px-1 text-xs font-medium text-foreground">授权时效与频次</legend>
        <div className="grid grid-cols-1 gap-x-3 gap-y-2 sm:grid-cols-2">
          <NumericField
            id={`${baseId}-hours`}
            label="租约有效期"
            unit="小时 (1 至 24)"
            step="1"
            value={values.expiresInHours}
            error={fieldErrors.expiresInHours}
            onChange={(next) => updateField('expiresInHours', next)}
            onBlur={() => handleBlur('expiresInHours')}
          />
          <NumericField
            id={`${baseId}-max-per-hour`}
            label="每小时最多下发次数"
            unit="次 (1 至 6)"
            step="1"
            value={values.maxCommandsPerHour}
            error={fieldErrors.maxCommandsPerHour}
            onChange={(next) => updateField('maxCommandsPerHour', next)}
            onBlur={() => handleBlur('maxCommandsPerHour')}
          />
        </div>
      </fieldset>

      <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/20 p-3">
        <legend className="px-1 text-xs font-medium text-foreground">冷机供水与安全护栏</legend>
        <div className="grid grid-cols-1 gap-x-3 gap-y-2 sm:grid-cols-2">
          <NumericField
            id={`${baseId}-supply-min`}
            label="供水温度下限"
            unit="°C (5.0 至 20.0)"
            min={5}
            placeholder="5.0"
            step="0.1"
            value={values.supplyTempMinC}
            error={fieldErrors.supplyTempMinC}
            onChange={(next) => updateField('supplyTempMinC', next)}
            onBlur={() => handleBlur('supplyTempMinC')}
          />
          <NumericField
            id={`${baseId}-supply-max`}
            label="供水温度上限"
            unit="°C (不高于 25.0)"
            min={5}
            step="0.1"
            value={values.supplyTempMaxC}
            error={fieldErrors.supplyTempMaxC}
            onChange={(next) => updateField('supplyTempMaxC', next)}
            onBlur={() => handleBlur('supplyTempMaxC')}
          />
          <NumericField
            id={`${baseId}-max-step`}
            label="单次最大步长"
            unit="°C (0.1 至 2.0)"
            step="0.1"
            value={values.maxSetpointStepC}
            error={fieldErrors.maxSetpointStepC}
            onChange={(next) => updateField('maxSetpointStepC', next)}
            onBlur={() => handleBlur('maxSetpointStepC')}
          />
          <NumericField
            id={`${baseId}-deadband`}
            label="调节死区"
            unit="°C (0.05 至 2.0)"
            min={0.05}
            placeholder="0.2"
            step="0.05"
            value={values.deadbandC}
            error={fieldErrors.deadbandC}
            onChange={(next) => updateField('deadbandC', next)}
            onBlur={() => handleBlur('deadbandC')}
          />
          <NumericField
            id={`${baseId}-zone-max`}
            label="室内温度保护上限"
            unit="°C (18.0 至 32.0)"
            step="0.1"
            value={values.zoneTempMaxC}
            error={fieldErrors.zoneTempMaxC}
            onChange={(next) => updateField('zoneTempMaxC', next)}
            onBlur={() => handleBlur('zoneTempMaxC')}
          />
          <NumericField
            id={`${baseId}-min-chillers`}
            label="最少运行冷机数"
            unit="台 (>= 0)"
            step="1"
            value={values.minRunningChillers}
            error={fieldErrors.minRunningChillers}
            onChange={(next) => updateField('minRunningChillers', next)}
            onBlur={() => handleBlur('minRunningChillers')}
          />
        </div>
      </fieldset>

      <fieldset className="flex flex-col gap-1.5">
        <legend className="text-xs font-medium text-foreground">允许自动下发的能力</legend>
        <div className="grid grid-cols-1 gap-2 rounded-lg border border-divider bg-muted/40 p-2.5 sm:grid-cols-3">
          {capabilityChoices.map((cap) => {
            const capId = `${baseId}-cap-${cap}`;
            const checked = values.allowedCapabilities.includes(cap);
            return (
              <div key={cap} className="flex items-center gap-2">
                <Checkbox
                  id={capId}
                  checked={checked}
                  onCheckedChange={(state) => toggleCapability(cap, state === true)}
                />
                <Label htmlFor={capId} className="cursor-pointer text-xs font-normal text-foreground">
                  {formatAutoControlCapability(cap)}
                </Label>
              </div>
            );
          })}
        </div>
        {fieldErrors.allowedCapabilities ? (
          <p role="alert" className="text-xs text-destructive-text">{fieldErrors.allowedCapabilities}</p>
        ) : null}
      </fieldset>

      {hasFrequencyCap ? (
        <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/20 p-3">
          <legend className="px-1 text-xs font-medium text-foreground">水泵变频边界 (Hz)</legend>
          <div className="grid grid-cols-1 gap-x-3 gap-y-2 sm:grid-cols-2">
            <NumericField
              id={`${baseId}-pump-freq-min`}
              label="水泵下限频率"
              unit="Hz (20.0 至 50.0)"
              min={20}
              step="0.5"
              value={values.pumpFreqMinHz}
              error={fieldErrors.pumpFreqMinHz}
              onChange={(next) => updateField('pumpFreqMinHz', next)}
              onBlur={() => handleBlur('pumpFreqMinHz')}
            />
            <NumericField
              id={`${baseId}-pump-freq-max`}
              label="水泵上限频率"
              unit="Hz (25.0 至 60.0)"
              min={25}
              step="0.5"
              value={values.pumpFreqMaxHz}
              error={fieldErrors.pumpFreqMaxHz}
              onChange={(next) => updateField('pumpFreqMaxHz', next)}
              onBlur={() => handleBlur('pumpFreqMaxHz')}
            />
            <NumericField
              id={`${baseId}-pump-freq-step`}
              label="单次调频步长"
              unit="Hz (不超过 10.0)"
              min={0.1}
              step="0.5"
              value={values.maxFreqStepHz}
              error={fieldErrors.maxFreqStepHz}
              onChange={(next) => updateField('maxFreqStepHz', next)}
              onBlur={() => handleBlur('maxFreqStepHz')}
            />
            <NumericField
              id={`${baseId}-pump-freq-deadband`}
              label="调频防抖死区"
              unit="Hz (0.1 至 5.0)"
              min={0.1}
              step="0.1"
              value={values.freqDeadbandHz}
              error={fieldErrors.freqDeadbandHz}
              onChange={(next) => updateField('freqDeadbandHz', next)}
              onBlur={() => handleBlur('freqDeadbandHz')}
            />
          </div>
        </fieldset>
      ) : null}

      {hasFanSpeedCap ? (
        <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/20 p-3">
          <legend className="px-1 text-xs font-medium text-foreground">冷却塔风机边界 (%)</legend>
          <div className="grid grid-cols-1 gap-x-3 gap-y-2 sm:grid-cols-2">
            <NumericField
              id={`${baseId}-fan-speed-min`}
              label="风机下限转速"
              unit="% (15 至 100)"
              min={15}
              step="1"
              value={values.fanSpeedMinPct}
              error={fieldErrors.fanSpeedMinPct}
              onChange={(next) => updateField('fanSpeedMinPct', next)}
              onBlur={() => handleBlur('fanSpeedMinPct')}
            />
            <NumericField
              id={`${baseId}-fan-speed-max`}
              label="风机上限转速"
              unit="% (20 至 100)"
              min={20}
              step="1"
              value={values.fanSpeedMaxPct}
              error={fieldErrors.fanSpeedMaxPct}
              onChange={(next) => updateField('fanSpeedMaxPct', next)}
              onBlur={() => handleBlur('fanSpeedMaxPct')}
            />
            <NumericField
              id={`${baseId}-fan-step`}
              label="单次调速步长"
              unit="% (不超过 25)"
              min={0.5}
              step="1"
              value={values.maxFanStepPct}
              error={fieldErrors.maxFanStepPct}
              onChange={(next) => updateField('maxFanStepPct', next)}
              onBlur={() => handleBlur('maxFanStepPct')}
            />
            <NumericField
              id={`${baseId}-fan-deadband`}
              label="调速防抖死区"
              unit="% (0.5 至 15.0)"
              min={0.5}
              step="0.5"
              value={values.fanDeadbandPct}
              error={fieldErrors.fanDeadbandPct}
              onChange={(next) => updateField('fanDeadbandPct', next)}
              onBlur={() => handleBlur('fanDeadbandPct')}
            />
          </div>
        </fieldset>
      ) : null}

      <fieldset className="flex flex-col gap-3 rounded-lg border border-divider bg-muted/20 p-3">
        <legend className="px-1 text-xs font-medium text-foreground">授权依据与身份核验</legend>
        <div className="flex flex-col gap-1.5">
          <ReasonField
            id={`${baseId}-reason`}
            value={values.reason}
            onChange={(next) => updateField('reason', next)}
            placeholder="例如：白班冷负荷平稳，开启 8 小时自动供水温度优化"
          />
          {fieldErrors.reason ? (
            <p role="alert" className="text-xs text-destructive-text">{fieldErrors.reason}</p>
          ) : null}
        </div>

        <CodeField
          id={`${baseId}-code`}
          value={values.verificationCode}
          error={codeError?.message ?? fieldErrors.verificationCode ?? null}
          showEnrollLink={codeError?.showEnrollLink}
          onNavigateEnroll={onClose}
          onSubmit={() => { if (!mutation.isPending) handleSubmit(); }}
          onChange={(next) => updateField('verificationCode', next)}
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
