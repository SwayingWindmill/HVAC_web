import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { ShieldCheck, ShieldOff, TriangleAlert } from 'lucide-react';
import { useId, useMemo, useState, type KeyboardEvent } from 'react';
import { toast } from 'sonner';
import { DataTableBlock } from '@/blocks/data-table';
import { FactTile } from '@/blocks/fact-list';
import { Panel, PanelHeader } from '@/blocks/panel';
import { StatusBadge } from '@/components/common/StatusBadge';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
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
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { CodeField } from '@/features/control/components/CommandConfirmation';
import { ReasonField } from '@/features/control/components/ControlBoundary';
import { stepUpErrorPresentation, type StepUpErrorPresentation } from '@/features/control/control-errors';
import { useDataTable } from '@/hooks/use-data-table';
import { useNow } from '@/hooks/use-now';
import { formatCompactTime, formatDecimal, formatTime } from '@/lib/operator-format';
import {
  armSiteAutoControlLease,
  autoControlKeys,
  AutoControlRequestError,
  revokeSiteAutoControlLease,
  siteAutoControlLeaseQuery,
  siteExecutionMonitorsQuery,
  type OptimizationExecutionMonitor,
  type SiteAutoControlLeaseState,
} from '../auto-control-api';
import {
  AUTO_CONTROL_EXECUTION_MODES,
  buildArmLeasePayload,
  EXECUTION_MODE_PRESENTATION,
  formatAutoControlCapability,
  formatAutoControlExecutionMode,
  formatAutoControlTripReason,
  formatGuardrailVerdict,
  formatMonitorDecisionTrace,
  formatMonitorExecutionMode,
  formatMonitorLoopLabel,
  formatMonitorNumericValue,
  initialArmLeaseValues,
  LEASE_STATUS_PRESENTATION,
  MONITOR_STATUS_PRESENTATION,
  resolveActiveLeaseBackoff,
  resolveAutoControlExecutionMode,
  resolveMonitorExecutionMode,
  resolveMonitorUnit,
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

function ArmLeaseDialog({
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
      <DialogContent className="max-h-[92dvh] overflow-y-auto p-5 sm:max-w-[600px]">
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
      return;
    }
    setFieldErrors({});
    setCodeError(null);
    mutation.mutate(built.payload);
  };

  return (
    <form
      noValidate
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        if (!mutation.isPending) handleSubmit();
      }}
    >
      <DialogHeader className="gap-1">
        <DialogTitle>{isUpdate ? '调整自动控制租约边界' : '开启限时自动控制租约'}</DialogTitle>
        <DialogDescription>
          租约有效期内，满足实时安全复核的优化建议将按所选运行模式执行；闭环模式自动下发并持续 30 分钟监护，影子模式仅校验与留痕对比。
        </DialogDescription>
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
        <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/30 p-3">
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
        <fieldset className="flex flex-col gap-2 rounded-lg border border-divider bg-muted/30 p-3">
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

function RevokeLeaseDialog({
  siteId,
  csrfToken,
  timeZone,
  leaseState,
  open,
  onOpenChange,
}: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly timeZone: string;
  readonly leaseState: SiteAutoControlLeaseState;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[480px]">
        {open && leaseState.activeLease ? (
          <RevokeLeaseForm
            siteId={siteId}
            csrfToken={csrfToken}
            timeZone={timeZone}
            activeLease={leaseState.activeLease}
            onClose={() => onOpenChange(false)}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function RevokeLeaseForm({
  siteId,
  csrfToken,
  timeZone,
  activeLease,
  onClose,
}: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly timeZone: string;
  readonly activeLease: NonNullable<SiteAutoControlLeaseState['activeLease']>;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const reasonId = useId();
  const [reason, setReason] = useState('');
  const [alertMessage, setAlertMessage] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: () =>
      revokeSiteAutoControlLease(
        siteId,
        { reason: reason.trim(), expectedRevision: activeLease.revision },
        csrfToken,
      ),
    onSuccess: (next) => {
      queryClient.setQueryData(autoControlKeys.lease(siteId), next);
      void queryClient.invalidateQueries({ queryKey: autoControlKeys.lease(siteId) });
      void queryClient.invalidateQueries({ queryKey: autoControlKeys.monitors(siteId) });
      toast.success('已撤回自动控制租约，恢复为人工确认下发');
      onClose();
    },
    onError: (error) => {
      if (error instanceof AutoControlRequestError && error.code === 'REGISTRY_REVISION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: autoControlKeys.lease(siteId) });
      }
      setAlertMessage(error instanceof AutoControlRequestError ? error.message : '撤回自动控制租约失败，请稍后再试');
    },
  });

  const ready = reason.trim().length > 0 && reason.trim().length <= 1000;

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        if (ready && !mutation.isPending) mutation.mutate();
      }}
    >
      <DialogHeader>
        <DialogTitle>撤回自动控制租约？</DialogTitle>
        <DialogDescription>
          撤回后，站点立即恢复为「人工确认下发」模式，平台不再自动向冷机下发优化建议。
        </DialogDescription>
      </DialogHeader>

      <div className="rounded-lg border border-divider bg-muted px-3.5 py-2.5 text-xs text-secondary-foreground">
        当前{resolveAutoControlExecutionMode(activeLease) === 'SHADOW' ? '影子跟跑' : '自动控制'}租约有效期至{' '}
        <span className="font-mono font-medium text-foreground">{formatTime(activeLease.expiresAt, timeZone)}</span>，
        供水范围 {formatDecimal(activeLease.supplyTempMinC, 1)} 至 {formatDecimal(activeLease.supplyTempMaxC, 1)} °C。
      </div>

      <ReasonField
        id={reasonId}
        value={reason}
        onChange={(next) => {
          setReason(next);
          setAlertMessage(null);
        }}
        placeholder="例如：夜间巡检，暂停自动下发"
      />

      {alertMessage ? (
        <Alert variant="destructive">
          <AlertTitle>未能撤回租约</AlertTitle>
          <AlertDescription>{alertMessage}</AlertDescription>
        </Alert>
      ) : null}

      <DialogFooter>
        <Button type="button" variant="outline" disabled={mutation.isPending} onClick={onClose}>
          取消
        </Button>
        <Button type="submit" variant="destructive" disabled={mutation.isPending || !ready}>
          {mutation.isPending ? <Spinner data-icon="inline-start" /> : null}
          {mutation.isPending ? '正在撤回' : '确认撤回租约'}
        </Button>
      </DialogFooter>
    </form>
  );
}

export function AutoControlPanel({
  siteId,
  timeZone,
  csrfToken,
  canRead,
  canManage,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly csrfToken: string;
  readonly canRead: boolean;
  readonly canManage: boolean;
}) {
  const [armOpen, setArmOpen] = useState(false);
  const [revokeOpen, setRevokeOpen] = useState(false);
  const now = useNow(30_000);

  const leaseQuery = useQuery({
    ...siteAutoControlLeaseQuery(siteId),
    enabled: canRead,
  });
  const monitorsQuery = useQuery({
    ...siteExecutionMonitorsQuery(siteId),
    enabled: canRead,
  });

  const monitorItems = monitorsQuery.data?.items ?? [];

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, OptimizationExecutionMonitor>>>(() => [
    {
      id: 'window',
      header: '观察窗口',
      cell: ({ row }) => (
        <div className="flex flex-col gap-1 font-mono text-xs tabular-nums">
          <span className="text-foreground">{formatCompactTime(row.original.startedAt, timeZone)} 至 {formatCompactTime(row.original.windowEndsAt, timeZone)}</span>
          <div className="flex flex-wrap items-center gap-1.5 font-sans">
            <Badge variant="secondary">
              {formatMonitorLoopLabel(row.original.loopKey, row.original.targetCapability)}
            </Badge>
            <span className="text-muted-foreground">{formatAutoControlCapability(row.original.targetCapability)}</span>
          </div>
        </div>
      ),
    },
    {
      id: 'temperatures',
      header: '设定值变化',
      cell: ({ row }) => {
        const {
          baselineSupplyTempC,
          appliedSupplyTempC,
          restoreSupplyTempC,
          rawRecommendedValue,
          clampedValue,
        } = row.original;
        const unit = resolveMonitorUnit(row.original);
        const rawVal = typeof rawRecommendedValue === 'number' ? rawRecommendedValue : undefined;
        const clampedVal = typeof clampedValue === 'number' ? clampedValue : undefined;
        const hasClampedDiff =
          rawVal !== undefined
          && clampedVal !== undefined
          && Math.abs(rawVal - clampedVal) >= 0.05;
        const showClampedOnly =
          !hasClampedDiff
          && clampedVal !== undefined
          && (appliedSupplyTempC === undefined || Math.abs(clampedVal - appliedSupplyTempC) >= 0.05);
        const showRawOnly =
          !hasClampedDiff
          && !showClampedOnly
          && rawVal !== undefined
          && (appliedSupplyTempC === undefined || Math.abs(rawVal - appliedSupplyTempC) >= 0.05);
        return (
          <div className="flex flex-col gap-0.5 text-xs tabular-nums">
            <span className="font-medium text-foreground">
              {formatMonitorNumericValue(baselineSupplyTempC, unit)} → {formatMonitorNumericValue(appliedSupplyTempC, unit)}
            </span>
            <span className="text-muted-foreground">
              回退目标 {formatMonitorNumericValue(restoreSupplyTempC, unit)}
            </span>
            {hasClampedDiff ? (
              <span className="text-warning-text">
                原建议 {formatMonitorNumericValue(rawVal, unit)}，限幅为 {formatMonitorNumericValue(clampedVal, unit)}
              </span>
            ) : showClampedOnly ? (
              <span className="text-muted-foreground">
                限幅目标 {formatMonitorNumericValue(clampedVal, unit)}
              </span>
            ) : showRawOnly ? (
              <span className="text-muted-foreground">
                原始建议 {formatMonitorNumericValue(rawVal, unit)}
              </span>
            ) : null}
          </div>
        );
      },
    },
    {
      id: 'limits',
      header: '租约安全边界',
      cell: ({ row }) => {
        const unit = resolveMonitorUnit(row.original);
        const loopSubject = unit === 'Hz' ? '频率' : unit === '%' ? '转速' : '供水';
        const suffix = unit === '%' ? '%' : unit === 'Hz' ? ' Hz' : ' °C';
        const minVal = row.original.supplyTempMinC;
        const maxText = `${formatDecimal(row.original.supplyTempMaxC, 1)}${suffix}`;
        return (
          <span className="text-xs text-muted-foreground tabular-nums">
            {minVal !== undefined
              ? `${loopSubject} ${formatDecimal(minVal, 1)} 至 ${maxText}`
              : `${loopSubject} ≤ ${maxText}`}
            {' · '}
            室内 ≤ {formatDecimal(row.original.zoneTempMaxC, 1)} °C
          </span>
        );
      },
    },
    {
      id: 'status',
      header: '监护状态',
      cell: ({ row }) => {
        const presentation = MONITOR_STATUS_PRESENTATION[row.original.status];
        const mode = resolveMonitorExecutionMode(row.original);
        return (
          <div className="flex flex-wrap items-center gap-1.5">
            <StatusBadge tone={presentation.tone} label={presentation.label} />
            <Badge variant={mode === 'SHADOW' ? 'default' : 'secondary'}>
              {formatMonitorExecutionMode(mode)}
            </Badge>
          </div>
        );
      },
    },
    {
      id: 'reason',
      header: '巡检与退出原因',
      cell: ({ row }) => {
        const verdictLabel = formatGuardrailVerdict(row.original.guardrailVerdict);
        const traceInfo = formatMonitorDecisionTrace(row.original);
        const primary = row.original.tripReason ? (
          <span className="text-xs text-destructive-text">
            {formatAutoControlTripReason(row.original.tripReason)}
          </span>
        ) : row.original.status === 'PENDING_SUBMIT' ? (
          <span className="text-xs text-muted-foreground">等待写入控制指令</span>
        ) : row.original.status === 'SHADOW_RECORDED' ? (
          <span className="text-xs text-muted-foreground">影子模式仅记录推荐轨迹，不下发设备</span>
        ) : row.original.status === 'OBSERVING' ? (
          <span className="text-xs text-muted-foreground">
            {row.original.lastCheckedAt
              ? `最近巡检 ${formatCompactTime(row.original.lastCheckedAt, timeZone)}`
              : '等待首次巡检'}
          </span>
        ) : row.original.status === 'VERIFIED' ? (
          <span className="text-xs text-muted-foreground">30 分钟内温度与功率均在安全范围内</span>
        ) : row.original.status === 'MANUAL_OVERRIDE' ? (
          <span className="text-xs text-warning-text">检测到现场或人工调参，AI 进入退避保护</span>
        ) : (
          <span className="text-xs text-muted-foreground">已记录保护处置结果</span>
        );
        if (!verdictLabel && !traceInfo) return primary;
        return (
          <div className="flex flex-col gap-0.5">
            {primary}
            {verdictLabel ? (
              <span className="text-xs text-muted-foreground">护栏：{verdictLabel}</span>
            ) : null}
            {traceInfo ? (
              <span className="text-xs text-muted-foreground" title={traceInfo.title}>
                {traceInfo.label}
              </span>
            ) : null}
          </div>
        );
      },
    },
    {
      id: 'actions',
      header: '关联指令',
      cell: ({ row }) => (
        <div className="flex flex-col items-end gap-1">
          {row.original.commandIntentId ? (
            <Link
              to="/operations/control"
              search={{ site: siteId, inspect: row.original.commandIntentId }}
              className="text-xs font-medium text-primary hover:underline"
            >
              查看下发指令 ›
            </Link>
          ) : (
            <span className="text-xs text-muted-foreground">无下行指令</span>
          )}
          {row.original.rollbackCommandIntentId ? (
            <Link
              to="/operations/control"
              search={{ site: siteId, inspect: row.original.rollbackCommandIntentId }}
              className="text-xs font-medium text-warning-text hover:underline"
            >
              查看回退指令 ›
            </Link>
          ) : null}
        </div>
      ),
    },
  ], [siteId, timeZone]);

  const table = useDataTable({
    key: `execution-monitors-${siteId}`,
    data: monitorItems,
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  if (!canRead) return null;

  const leaseState = leaseQuery.data;
  const isAuto = leaseState?.confirmationMode === 'AUTO_WITH_LEASE' && leaseState.activeLease !== null;
  const activeLease = leaseState?.activeLease ?? null;
  const latestLease = leaseState?.latestLease ?? null;
  const leaseExecutionMode = resolveAutoControlExecutionMode(activeLease);
  const isShadowLease = isAuto && leaseExecutionMode === 'SHADOW';
  const backoff = isAuto && activeLease ? resolveActiveLeaseBackoff(activeLease, now) : null;
  const deadbandDigits = activeLease?.deadbandC !== undefined
    && Math.abs(activeLease.deadbandC * 10 - Math.round(activeLease.deadbandC * 10)) > 0.001
    ? 2
    : 1;

  return (
    <div className="flex w-full min-w-0 flex-col gap-4">
      <Panel aria-label="自动控制租约">
        <PanelHeader
          className="h-auto min-h-12 flex-wrap py-2.5 sm:h-12 sm:py-0"
          title={(
            <span className="flex items-center gap-1.5">
              {isAuto ? (
                <ShieldCheck
                  className={isShadowLease ? 'size-4 text-primary' : 'size-4 text-success'}
                  aria-hidden="true"
                />
              ) : (
                <ShieldOff className="size-4 text-muted-foreground" aria-hidden="true" />
              )}
              自动控制租约
            </span>
          )}
          badge={
            leaseState ? (
              <StatusBadge
                tone={isShadowLease ? 'info' : isAuto ? 'success' : 'neutral'}
                label={isShadowLease ? '影子跟跑中' : isAuto ? '限时自动控制租约生效中' : '人工确认下发'}
              />
            ) : undefined
          }
          caption={isAuto && activeLease ? `有效期至 ${formatTime(activeLease.expiresAt, timeZone)}` : undefined}
          action={
            leaseState ? (
              canManage ? (
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant={isAuto ? 'outline' : 'default'}
                    onClick={() => setArmOpen(true)}
                  >
                    {isAuto ? '调整租约…' : '开启自动控制租约…'}
                  </Button>
                  {isAuto ? (
                    <Button
                      type="button"
                      size="sm"
                      variant="destructive"
                      onClick={() => setRevokeOpen(true)}
                    >
                      撤回租约
                    </Button>
                  ) : null}
                </div>
              ) : (
                <span className="text-xs text-muted-foreground">只读，需运行排班管理权限才能授权</span>
              )
            ) : undefined
          }
        />

        {leaseQuery.isPending ? (
          <div className="flex flex-col gap-2 p-4">
            <Skeleton className="h-14 rounded-lg" />
          </div>
        ) : leaseQuery.isError ? (
          <div className="flex flex-col items-start gap-2 px-5 py-6">
            <p className="text-sm font-semibold">自动控制租约状态暂时读不到</p>
            <p className="text-xs text-muted-foreground">
              {leaseQuery.error instanceof AutoControlRequestError ? leaseQuery.error.message : '请稍后重试'}
            </p>
            <Button variant="outline" size="sm" onClick={() => void leaseQuery.refetch()}>
              重试
            </Button>
          </div>
        ) : isAuto && activeLease ? (
          <div className="flex flex-col gap-3 px-5 py-4">
            {backoff ? (
              <Alert role="status" className="border-warning/40 bg-warning-soft text-warning-text">
                <TriangleAlert className="size-4" aria-hidden="true" />
                <AlertTitle>AI 自动调节退避保护中</AlertTitle>
                <AlertDescription className="text-warning-text">
                  {[
                    backoff.reasonText ?? '检测到人工干预设定值，当前暂停自动下发调温指令',
                    backoff.until ? `退避至 ${formatTime(backoff.until, timeZone)}` : null,
                  ]
                    .filter(Boolean)
                    .join('；')}
                </AlertDescription>
              </Alert>
            ) : null}
            <dl className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-4">
              <FactTile
                label="运行模式"
                valueClassName={
                  isShadowLease
                    ? 'text-sm font-semibold text-primary'
                    : 'text-sm font-semibold text-foreground'
                }
              >
                {formatAutoControlExecutionMode(leaseExecutionMode)}
              </FactTile>
              <FactTile
                label="供水温度范围"
                valueClassName="text-sm font-semibold text-foreground"
              >
                {`${formatDecimal(activeLease.supplyTempMinC, 1)} 至 ${formatDecimal(activeLease.supplyTempMaxC, 1)} °C`}
              </FactTile>
              <FactTile
                label="单次最大步长"
                valueClassName="text-sm font-semibold text-foreground"
              >
                {`≤ ${formatDecimal(activeLease.maxSetpointStepC, 1)} °C`}
              </FactTile>
              <FactTile
                label="室内温度上限"
                valueClassName="text-sm font-semibold text-foreground"
              >
                {`≤ ${formatDecimal(activeLease.zoneTempMaxC, 1)} °C`}
              </FactTile>
              <FactTile
                label="最少运行冷机"
                valueClassName="text-sm font-semibold text-foreground"
              >
                {`${activeLease.minRunningChillers} 台`}
              </FactTile>
              <FactTile
                label="每小时指令上限"
                valueClassName="text-sm font-semibold text-foreground"
              >
                {`${activeLease.maxCommandsPerHour} 次`}
              </FactTile>
              {(activeLease.allowedCapabilities.includes('SET_FREQUENCY')
                || activeLease.pumpFreqMinHz !== undefined
                || activeLease.pumpFreqMaxHz !== undefined) ? (
                <FactTile
                  label="水泵变频边界"
                  valueClassName="text-sm font-semibold text-foreground tabular-nums"
                >
                  {`${formatDecimal(activeLease.pumpFreqMinHz ?? 28.0, 1)} 至 ${formatDecimal(activeLease.pumpFreqMaxHz ?? 50.0, 1)} Hz · 步长 ≤ ${formatDecimal(activeLease.maxFreqStepHz ?? 2.5, 1)} Hz`}
                </FactTile>
              ) : null}
              {(activeLease.allowedCapabilities.includes('SET_FAN_SPEED')
                || activeLease.fanSpeedMinPct !== undefined
                || activeLease.fanSpeedMaxPct !== undefined) ? (
                <FactTile
                  label="风机转速边界"
                  valueClassName="text-sm font-semibold text-foreground tabular-nums"
                >
                  {`${formatDecimal(activeLease.fanSpeedMinPct ?? 25.0, 1)}% 至 ${formatDecimal(activeLease.fanSpeedMaxPct ?? 100.0, 1)}% · 步长 ≤ ${formatDecimal(activeLease.maxFanStepPct ?? 10.0, 1)}%`}
                </FactTile>
              ) : null}
              <div className="col-span-2 sm:col-span-3 lg:col-span-2">
                <FactTile
                  label="允许能力"
                  valueClassName="text-sm font-semibold text-foreground"
                >
                  {activeLease.allowedCapabilities.map(formatAutoControlCapability).join('、')}
                </FactTile>
              </div>
            </dl>
            <div className="flex flex-col gap-1 border-t border-divider pt-2.5 text-xs text-muted-foreground">
              <p>
                授权时段 <span className="font-mono">{formatTime(activeLease.armedAt, timeZone)}</span> 至{' '}
                <span className="font-mono">{formatTime(activeLease.expiresAt, timeZone)}</span>
                {activeLease.deadbandC !== undefined
                  ? `；调节死区 ${formatDecimal(activeLease.deadbandC, deadbandDigits)} °C`
                  : null}
                {isShadowLease
                  ? null
                  : '；每次自动下发前会再次复核实时温度、冷机负荷与联锁状态，并在之后持续监护 30 分钟。'}
              </p>
              {isShadowLease ? (
                <p className="font-medium text-primary">
                  当前租约处于影子跟跑模式：每周期执行完整物理护栏校验，仅记录推荐与限幅对照轨迹，不下发设备指令。
                </p>
              ) : null}
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-3 px-5 py-4">
            <p className="text-sm text-secondary-foreground">
              当前为人工确认下发模式。平台生成的供水温度优化建议不会自动执行，需由值班人员确认或转为人工调温工单。
            </p>
            {latestLease ? (
              <div className="flex flex-wrap items-center gap-2 rounded-lg border border-divider bg-muted px-3.5 py-2.5 text-xs text-secondary-foreground">
                <StatusBadge
                  tone={LEASE_STATUS_PRESENTATION[latestLease.status].tone}
                  label={`上次租约：${LEASE_STATUS_PRESENTATION[latestLease.status].label}`}
                />
                {latestLease.executionMode ? (
                  <Badge variant={latestLease.executionMode === 'SHADOW' ? 'default' : 'secondary'}>
                    {formatAutoControlExecutionMode(latestLease.executionMode)}
                  </Badge>
                ) : null}
                <span className="font-mono text-muted-foreground">
                  {formatTime(latestLease.updatedAt, timeZone)}
                </span>
                {latestLease.tripReason ? (
                  <span className="text-foreground">
                    原因：{formatAutoControlTripReason(latestLease.tripReason)}
                  </span>
                ) : null}
              </div>
            ) : null}
          </div>
        )}
      </Panel>

      <DataTableBlock
        title="30 分钟执行监护"
        description="限时自动控制下发后每分钟巡检实时供水温度、室内温度、冷机功率占比与遥测健康；越界或断采超过 5 分钟自动回退并退出租约"
        className="w-full min-w-0"
      >
        {monitorsQuery.isError ? (
          <Panel aria-label="30 分钟执行监护">
            <div className="flex flex-col items-start gap-2 px-5 py-6">
              <p className="text-sm font-semibold">执行监护记录暂时读不到</p>
              <Button variant="outline" size="sm" onClick={() => void monitorsQuery.refetch()}>
                重试
              </Button>
            </div>
          </Panel>
        ) : monitorsQuery.isPending ? (
          <Skeleton className="h-36 rounded-xl" />
        ) : (
          <DataTable
            table={table}
            tableAriaLabel="30 分钟执行监护"
            empty="还没有自动控制执行监护记录；开启租约并自动下发调温指令后会在此追踪 30 分钟观察窗口"
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'window'
                  ? 'pl-5'
                  : header.id === 'actions'
                    ? 'pr-5 text-right'
                    : undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'window'
                  ? 'pl-5 align-top whitespace-nowrap'
                  : cell.column.id === 'actions'
                    ? 'pr-5 text-right align-top whitespace-nowrap'
                    : cell.column.id === 'reason'
                      ? 'align-top whitespace-normal'
                      : 'align-top whitespace-nowrap',
            })}
          />
        )}
      </DataTableBlock>

      {leaseState ? (
        <>
          <ArmLeaseDialog
            siteId={siteId}
            csrfToken={csrfToken}
            leaseState={leaseState}
            open={armOpen}
            onOpenChange={setArmOpen}
          />
          <RevokeLeaseDialog
            siteId={siteId}
            csrfToken={csrfToken}
            timeZone={timeZone}
            leaseState={leaseState}
            open={revokeOpen}
            onOpenChange={setRevokeOpen}
          />
        </>
      ) : null}
    </div>
  );
}
