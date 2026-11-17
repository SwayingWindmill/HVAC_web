import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { AlertCircle, Plus, Play, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { formatTelemetryUnit } from '@/domain/centralPlantTelemetry';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { cn } from '@/lib/utils';
import {
  EVALUATION_EFFECT_LABELS,
  EVALUATION_STATUS_LABELS,
  formatQualityBlocker,
  type EvaluationDisplayContext,
} from '../alarm-presentation';
import {
  collectRuleReadings,
  describeRuleLinkage,
  describeRuleOrigin,
  simulateSiteAlarmRule,
  type AlarmRule,
  type AlarmRuleReading,
  type AlarmRuleSimulationResult,
  type ReadingNames,
} from '../alarm-rules';

const MAX_STEPS = 8;

const STATE_INPUT_ALIASES: Readonly<Record<string, string>> = {
  运行: 'RUNNING',
  停机: 'STOPPED',
  待机: 'STANDBY',
  故障: 'FAULT',
};

function readingKey(reading: AlarmRuleReading): string {
  return `${reading.deviceId}/${reading.pointCode}`;
}

function defaultReadingValue(reading: AlarmRuleReading): string {
  if (reading.pointCode.includes('run_state') || reading.pointCode.endsWith('state')) {
    return '运行';
  }
  return '10';
}

function initialStepsForRule(rule: AlarmRule): ReadonlyArray<Record<string, string>> {
  const readings = collectRuleReadings(rule);
  const baseRow = Object.fromEntries(readings.map((reading) => [readingKey(reading), defaultReadingValue(reading)]));
  return [
    { ...baseRow },
    { ...baseRow },
  ];
}

function parseSampleValue(raw: string): { readonly ok: true; readonly value: number | string | boolean } | { readonly ok: false; readonly message: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: false, message: '请输入采样数值或设备状态' };
  }
  if (trimmed === 'true' || trimmed === '是') return { ok: true, value: true };
  if (trimmed === 'false' || trimmed === '否') return { ok: true, value: false };
  if (trimmed in STATE_INPUT_ALIASES) return { ok: true, value: STATE_INPUT_ALIASES[trimmed] };
  const numeric = Number(trimmed);
  if (Number.isFinite(numeric)) return { ok: true, value: numeric };
  if (trimmed.length > 64) {
    return { ok: false, message: '输入内容不能超过 64 个字符' };
  }
  return { ok: true, value: trimmed };
}

function describeStepReason(
  step: AlarmRuleSimulationResult['steps'][number],
  displayContext: EvaluationDisplayContext,
): string {
  if (step.qualityBlocker) {
    return formatQualityBlocker(step.qualityBlocker, displayContext) ?? '条件暂不可判定';
  }
  if (step.matchedReasons && step.matchedReasons.length > 0) {
    return step.matchedReasons.join(' · ');
  }
  return step.active ? '告警持续保持' : '未满足触发条件';
}

export function AlarmRuleSimulateSheet({
  siteId,
  csrfToken,
  rule,
  titleByRuleId,
  displayContext,
  names,
  onClose,
}: {
  readonly siteId: string;
  readonly csrfToken: string;
  readonly rule: AlarmRule | null;
  readonly titleByRuleId: ReadonlyMap<string, string>;
  readonly displayContext: EvaluationDisplayContext;
  readonly names: ReadingNames;
  readonly onClose: () => void;
}) {
  const [steps, setSteps] = useState<Array<Record<string, string>>>([]);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [result, setResult] = useState<AlarmRuleSimulationResult | null>(null);

  const simulation = useMutation({
    mutationFn: (payload: Parameters<typeof simulateSiteAlarmRule>[1]) =>
      simulateSiteAlarmRule(siteId, payload, csrfToken),
    onSuccess: (data) => {
      setResult(data);
    },
  });
  const { reset: resetSimulation } = simulation;

  useEffect(() => {
    if (!rule) {
      setSteps([]);
      setFieldErrors({});
      setResult(null);
      resetSimulation();
      return;
    }
    setSteps(initialStepsForRule(rule).map((row) => ({ ...row })));
    setFieldErrors({});
    setResult(null);
    resetSimulation();
  }, [rule, resetSimulation]);

  const readings = rule ? collectRuleReadings(rule) : [];

  const handleFieldChange = (stepIndex: number, key: string, value: string) => {
    setSteps((prev) => prev.map((row, idx) => (idx === stepIndex ? { ...row, [key]: value } : row)));
    const errKey = `${stepIndex}:${key}`;
    if (fieldErrors[errKey]) {
      setFieldErrors((prev) => {
        const next = { ...prev };
        delete next[errKey];
        return next;
      });
    }
  };

  const handleAddStep = () => {
    if (!rule || steps.length >= MAX_STEPS) return;
    const template = steps.at(-1) ?? initialStepsForRule(rule)[0];
    setSteps((prev) => [...prev, { ...template }]);
  };

  const handleRemoveStep = (stepIndex: number) => {
    if (steps.length <= 1) return;
    setSteps((prev) => prev.filter((_, idx) => idx !== stepIndex));
    setFieldErrors({});
  };

  const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!rule) return;
    const nextErrors: Record<string, string> = {};
    const parsedSteps = steps.map((row, stepIndex) => ({
      readings: readings.map((reading) => {
        const key = readingKey(reading);
        const parsed = parseSampleValue(row[key] ?? '');
        if (!parsed.ok) {
          nextErrors[`${stepIndex}:${key}`] = parsed.message;
          return { deviceId: reading.deviceId, pointCode: reading.pointCode, value: 0 };
        }
        return {
          deviceId: reading.deviceId,
          pointCode: reading.pointCode,
          value: parsed.value,
          quality: 'GOOD' as const,
        };
      }),
    }));
    if (Object.keys(nextErrors).length > 0) {
      setFieldErrors(nextErrors);
      return;
    }
    setFieldErrors({});
    simulation.mutate({
      ruleId: rule.ruleId,
      steps: parsedSteps,
    });
  };

  return (
    <Sheet open={rule !== null} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-[560px]">
        <SheetHeader className="border-b border-divider">
          <SheetTitle>{rule ? `规则模拟试算：${rule.title}` : '规则模拟试算'}</SheetTitle>
          <SheetDescription>
            {rule
              ? `${describeRuleOrigin(rule)} · ${describeRuleLinkage(rule, titleByRuleId)}。在内存中推演连续采样序列，不产生真实告警。`
              : '输入多步采样值验证规则触发与恢复条件。'}
          </SheetDescription>
        </SheetHeader>

        {rule ? (
          <form onSubmit={handleSubmit} className="flex flex-1 flex-col gap-4 p-4">
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium text-muted-foreground">
                采样步序列（每步间隔 1 分钟，最多 {MAX_STEPS} 步）
              </span>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={handleAddStep}
                disabled={steps.length >= MAX_STEPS || simulation.isPending}
              >
                <Plus className="size-3.5" aria-hidden="true" />
                添加采样步
              </Button>
            </div>

            {readings.length === 0 ? (
              <p className="rounded-lg border border-divider p-3 text-sm text-muted-foreground">
                当前规则未绑定固定点位输入，可直接提交空白步序列验证调度或依赖状态。
              </p>
            ) : null}

            <div className="flex flex-col gap-3">
              {steps.map((row, stepIndex) => (
                <fieldset
                  key={`sim-step-${stepIndex}`}
                  className="flex flex-col gap-3 rounded-lg border border-divider bg-background/60 p-3"
                >
                  <div className="flex items-center justify-between">
                    <legend className="text-xs font-semibold text-foreground">
                      第 {stepIndex + 1} 步采样
                    </legend>
                    {steps.length > 1 ? (
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        aria-label={`删除第 ${stepIndex + 1} 步`}
                        onClick={() => handleRemoveStep(stepIndex)}
                        disabled={simulation.isPending}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                        移除
                      </Button>
                    ) : null}
                  </div>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    {readings.map((reading) => {
                      const key = readingKey(reading);
                      const inputId = `sim-${stepIndex}-${key.replace(/\//g, '-')}`;
                      const errKey = `${stepIndex}:${key}`;
                      const errMsg = fieldErrors[errKey];
                      const errId = `${inputId}-error`;
                      const devName = names.device(reading.deviceId);
                      const point = names.point(reading);
                      const displayUnit = formatTelemetryUnit(point.unit);
                      const labelText = displayUnit
                        ? `${devName} · ${point.name}（${displayUnit}）`
                        : `${devName} · ${point.name}`;
                      return (
                        <div key={key} className="flex flex-col justify-between gap-1.5">
                          <Label htmlFor={inputId} className="text-xs leading-snug text-foreground">
                            {labelText}
                          </Label>
                          <Input
                            id={inputId}
                            value={row[key] ?? ''}
                            aria-invalid={Boolean(errMsg)}
                            aria-describedby={errMsg ? errId : undefined}
                            onChange={(e) => handleFieldChange(stepIndex, key, e.target.value)}
                            onKeyDown={(e) => {
                              if (e.nativeEvent.isComposing) return;
                            }}
                          />
                          {errMsg ? (
                            <p id={errId} role="alert" className="text-xs text-destructive-text">
                              {errMsg}
                            </p>
                          ) : null}
                        </div>
                      );
                    })}
                  </div>
                </fieldset>
              ))}
            </div>

            {simulation.isError ? (
              <div
                role="alert"
                aria-live="polite"
                className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive-text"
              >
                <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <span>{simulation.error instanceof Error ? simulation.error.message : '模拟试算失败，请稍后重试'}</span>
              </div>
            ) : null}

            {result ? (
              <section className="flex flex-col gap-2" aria-label="试算结果明细区">
                <div className="flex items-center justify-between text-xs">
                  <span className="font-medium text-foreground">逐步推演结果</span>
                  <span className={cn(result.finalActive ? 'font-medium text-warning' : 'text-muted-foreground')}>
                    最终状态：{result.finalActive ? '告警保持活动' : '未触发 / 已恢复'}
                  </span>
                </div>
                <div
                  role="region"
                  aria-label="试算结果表格"
                  tabIndex={0}
                  className="overflow-x-auto rounded-lg border border-divider"
                >
                  <table aria-label="试算结果明细" className="w-full border-collapse text-left text-xs">
                    <thead className="border-b border-divider bg-muted/40 text-muted-foreground">
                      <tr>
                        <th scope="col" className="px-2.5 py-2 font-medium">步骤</th>
                        <th scope="col" className="px-2.5 py-2 font-medium">判定</th>
                        <th scope="col" className="px-2.5 py-2 font-medium">动作</th>
                        <th scope="col" className="px-2.5 py-2 font-medium">告警态</th>
                        <th scope="col" className="px-2.5 py-2 font-medium">说明</th>
                      </tr>
                    </thead>
                    <tbody>
                      {result.steps.map((step) => (
                        <tr key={step.step} className="border-b border-divider last:border-b-0">
                          <td className="px-2.5 py-2 tabular-nums">第 {step.step} 步</td>
                          <td className="px-2.5 py-2">{EVALUATION_STATUS_LABELS[step.status]}</td>
                          <td className="px-2.5 py-2">{EVALUATION_EFFECT_LABELS[step.effect]}</td>
                          <td className="px-2.5 py-2">{step.active ? '活动' : '未触发'}</td>
                          <td className="px-2.5 py-2 text-muted-foreground">{describeStepReason(step, displayContext)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>
            ) : null}

            <SheetFooter className="border-t border-divider px-0 pt-3">
              <div className="flex items-center justify-end gap-2">
                <Button type="button" variant="outline" onClick={onClose}>
                  关闭
                </Button>
                <Button type="submit" disabled={simulation.isPending}>
                  <Play className="size-3.5" aria-hidden="true" />
                  {simulation.isPending ? '试算中…' : '运行模拟试算'}
                </Button>
              </div>
            </SheetFooter>
          </form>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}
