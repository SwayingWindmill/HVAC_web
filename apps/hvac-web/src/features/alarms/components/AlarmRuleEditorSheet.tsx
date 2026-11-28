import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { AlertTriangle, CheckCircle2, Play, Save } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import type { SiteEquipmentIndex } from '@/features/assets/use-device-names';
import type { AlarmRuleAction, AlarmRuleDependencyGate } from '../alarm-operations';
import {
  computeRuleDagLayout,
  evaluateLocalEdgeEligibility,
  newRuleUuidV7,
} from '../alarm-rule-dag';
import type { AlarmRule, AlarmRuleCondition, AlarmRuleSeverity } from '../alarm-rules';
import {
  AlarmRuleConditionEditor,
  buildEquipmentOptions,
  preventImeEnterSubmit,
} from './AlarmRuleConditionEditor';
import {
  createEmptyRule,
  EXECUTION_TARGET_OPTIONS,
  RULE_KIND_OPTIONS,
  SEVERITY_OPTIONS,
  TRIGGER_MODE_OPTIONS,
  validateConditionTree,
} from './alarm-rule-editor-validation';
import {
  AlarmRuleActionsSection,
  AlarmRuleDependenciesSection,
} from './AlarmRuleEditorSections';

export function AlarmRuleEditorSheet({
  open,
  mode,
  initialRule,
  allRules,
  equipment,
  onClose,
  onSaveDraft,
  onSimulateDraft,
}: {
  readonly open: boolean;
  readonly mode: 'create' | 'edit';
  readonly initialRule: AlarmRule | null;
  readonly allRules: readonly AlarmRule[];
  readonly equipment: SiteEquipmentIndex;
  readonly onClose: () => void;
  readonly onSaveDraft: (rule: AlarmRule) => void;
  readonly onSimulateDraft: (rule: AlarmRule) => void;
}) {
  const titleId = useId();
  const equipmentOptions = useMemo(() => buildEquipmentOptions(equipment), [equipment]);
  const equipmentRef = useRef(equipment);
  equipmentRef.current = equipment;
  const wasOpenRef = useRef(false);
  const lastInitialRuleIdRef = useRef<string | undefined>(undefined);
  const [ruleId, setRuleId] = useState('');
  const [title, setTitle] = useState('');
  const [severity, setSeverity] = useState<AlarmRuleSeverity>('MAJOR');
  const [ruleKind, setRuleKind] = useState<NonNullable<AlarmRule['ruleKind']>>('THRESHOLD');
  const [executionTarget, setExecutionTarget] = useState<NonNullable<AlarmRule['executionTarget']>>('CLOUD');
  const [triggerMode, setTriggerMode] = useState<AlarmRule['triggerMode']>('DURATION');
  const [durationSeconds, setDurationSeconds] = useState(300);
  const [repeatCount, setRepeatCount] = useState(3);
  const [raise, setRaise] = useState<AlarmRuleCondition>(() => createEmptyRule(equipment).raise);
  const [clear, setClear] = useState<AlarmRuleCondition>(() => createEmptyRule(equipment).clear);
  const [dependsOnRuleIds, setDependsOnRuleIds] = useState<string[]>([]);
  const [dependencyGate, setDependencyGate] = useState<AlarmRuleDependencyGate>('ALL_ACTIVE');
  const [actions, setActions] = useState<AlarmRuleAction[]>([]);
  const [validationError, setValidationError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      wasOpenRef.current = false;
      return;
    }
    const nextRuleKey = initialRule?.ruleId;
    if (wasOpenRef.current && lastInitialRuleIdRef.current === nextRuleKey) {
      return;
    }
    wasOpenRef.current = true;
    lastInitialRuleIdRef.current = nextRuleKey;
    const base = initialRule ?? createEmptyRule(equipmentRef.current);
    setRuleId(base.ruleId);
    setTitle(base.title);
    setSeverity(base.severity);
    setRuleKind(base.ruleKind ?? 'THRESHOLD');
    setExecutionTarget(base.executionTarget ?? 'CLOUD');
    setTriggerMode(base.triggerMode);
    setDurationSeconds(base.durationSeconds ?? 300);
    setRepeatCount(base.repeatCount ?? 3);
    setRaise(structuredClone(base.raise));
    setClear(structuredClone(base.clear));
    setDependsOnRuleIds([...(base.dependsOnRuleIds ?? [])]);
    setDependencyGate(base.dependencyGate ?? (base.dependsOnRuleIds && base.dependsOnRuleIds.length > 0 ? 'ALL_ACTIVE' : 'NONE'));
    setActions((base.actions ?? []).map((a) => ({
      ...a,
      ...(a.parameters ? { parameters: { ...a.parameters } } : {}),
    })));
    setValidationError(null);
  }, [open, initialRule]);

  const builtRule = useMemo<AlarmRule>(() => {
    const keepExplicitKind = initialRule?.ruleKind !== undefined || ruleKind !== 'THRESHOLD';
    const keepExplicitTarget = initialRule?.executionTarget !== undefined || executionTarget !== 'CLOUD';
    return {
      ...(initialRule ?? {}),
      ruleId: ruleId || newRuleUuidV7(),
      title: title.trim() || '未命名规则',
      severity,
      ...(keepExplicitKind ? { ruleKind } : { ruleKind: undefined }),
      ...(keepExplicitTarget ? { executionTarget } : { executionTarget: undefined }),
      triggerMode,
      ...(triggerMode === 'DURATION' ? { durationSeconds: Math.max(1, durationSeconds) } : { durationSeconds: undefined }),
      ...(triggerMode === 'REPEATING' ? { repeatCount: Math.max(1, repeatCount) } : { repeatCount: undefined }),
      dependsOnRuleIds: dependsOnRuleIds.length > 0 ? dependsOnRuleIds : undefined,
      dependencyGate: dependsOnRuleIds.length > 0 ? dependencyGate : undefined,
      actions: actions.length > 0 ? actions : undefined,
      raise,
      clear,
    };
  }, [initialRule, ruleId, title, severity, ruleKind, executionTarget, triggerMode, durationSeconds, repeatCount, dependsOnRuleIds, dependencyGate, actions, raise, clear]);

  const otherRules = useMemo(() => allRules.filter((r) => r.ruleId !== ruleId), [allRules, ruleId]);

  const dagPreview = useMemo(() => computeRuleDagLayout([...otherRules, builtRule]), [otherRules, builtRule]);

  const edgeEligibility = useMemo(
    () => evaluateLocalEdgeEligibility(builtRule, [...otherRules, builtRule]),
    [builtRule, otherRules],
  );

  const validateBeforeCommit = (): string | null => {
    if (!title.trim()) return '请输入规则名称';
    if (triggerMode === 'DURATION' && (!Number.isInteger(durationSeconds) || durationSeconds <= 0 || durationSeconds > 31_536_000)) {
      return '持续时长触发模式需要填写 1 至 31536000 之间的持续秒数';
    }
    if (triggerMode === 'REPEATING' && (!Number.isInteger(repeatCount) || repeatCount <= 0)) {
      return '连续命中触发模式需要填写大于 0 的连续次数';
    }
    const raiseErr = validateConditionTree('触发报警条件', raise);
    if (raiseErr) return raiseErr;
    const clearErr = validateConditionTree('自动恢复条件', clear);
    if (clearErr) return clearErr;
    if (dagPreview.cycleRuleIds.length > 0) return '当前上游依赖会导致循环引用，请取消部分依赖';
    if (dagPreview.overDepthRuleIds.length > 0) return '联动深度超过最大 3 跳限制，请减少级联深度';
    for (let i = 0; i < actions.length; i += 1) {
      const act = actions[i];
      if (act.kind === 'WORK_ORDER' && !(act.workOrderTitle ?? '').trim()) {
        return `第 ${i + 1} 个工单动作缺少工单标题`;
      }
      if (act.kind === 'PROTECTION_COMMAND' && (!(act.targetDeviceId ?? '').trim() || !(act.capability ?? '').trim())) {
        return `第 ${i + 1} 个保护指令动作缺少目标设备或能力标识`;
      }
      if (act.kind === 'WEBHOOK' && !(act.webhookUrl ?? '').startsWith('https://')) {
        return `第 ${i + 1} 个 Webhook 回调地址必须以 https:// 开头`;
      }
    }
    if (executionTarget !== 'CLOUD' && !edgeEligibility.eligible) {
      return edgeEligibility.message ?? '当前规则不满足边缘执行约束，请调整条件、动作或改为仅云端执行';
    }
    return null;
  };

  const handleSave = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const err = validateBeforeCommit();
    if (err) {
      setValidationError(err);
      return;
    }
    setValidationError(null);
    onSaveDraft(builtRule);
  };

  const handleSimulate = () => {
    const err = validateBeforeCommit();
    if (err) {
      setValidationError(err);
      return;
    }
    setValidationError(null);
    onSimulateDraft(builtRule);
  };

  return (
    <Sheet open={open} onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <SheetContent side="right" className="w-full overflow-y-auto data-[side=right]:sm:max-w-[640px]">
        <SheetHeader className="border-b border-divider">
          <SheetTitle>{mode === 'create' ? '新建告警规则' : '编辑告警规则'}</SheetTitle>
          <SheetDescription>
            保存后写入本地未发布草稿，可在确认差异及 DAG 校验无误后一键热发布，或先对草稿运行模拟试算。
          </SheetDescription>
        </SheetHeader>

        <form onSubmit={handleSave} className="flex flex-1 flex-col gap-4 p-4 text-xs">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={titleId} className="text-xs font-medium text-foreground">规则名称</Label>
            <Input
              id={titleId}
              value={title}
              maxLength={256}
              onKeyDown={preventImeEnterSubmit}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="例如：冷冻水供水温度过高保护"
            />
          </div>

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <span className="font-medium text-foreground">严重程度</span>
              <Select
                value={severity}
                onValueChange={(raw) => {
                  const matched = SEVERITY_OPTIONS.find((opt) => opt.value === raw);
                  if (matched) setSeverity(matched.value);
                }}
              >
                <SelectTrigger className="w-full" aria-label="严重程度"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {SEVERITY_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex flex-col gap-1">
              <span className="font-medium text-foreground">规则类型</span>
              <Select
                value={ruleKind}
                onValueChange={(raw) => {
                  const matched = RULE_KIND_OPTIONS.find((opt) => opt.value === raw);
                  if (matched) setRuleKind(matched.value);
                }}
              >
                <SelectTrigger className="w-full" aria-label="规则类型"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {RULE_KIND_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex flex-col gap-1">
              <span className="font-medium text-foreground">触发方式</span>
              <Select
                value={triggerMode}
                onValueChange={(raw) => {
                  const matched = TRIGGER_MODE_OPTIONS.find((opt) => opt.value === raw);
                  if (matched) setTriggerMode(matched.value);
                }}
              >
                <SelectTrigger className="w-full" aria-label="触发方式"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {TRIGGER_MODE_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex flex-col gap-1">
              <span className="font-medium text-foreground">执行位置</span>
              <Select
                value={executionTarget}
                onValueChange={(raw) => {
                  const matched = EXECUTION_TARGET_OPTIONS.find((opt) => opt.value === raw);
                  if (matched) setExecutionTarget(matched.value);
                }}
              >
                <SelectTrigger className="w-full" aria-label="执行位置"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {EXECUTION_TARGET_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          {executionTarget !== 'CLOUD' ? (
            edgeEligibility.eligible ? (
              <div className="flex items-start gap-2 rounded-lg border border-success/35 bg-success-soft px-3 py-2 text-xs text-success-text">
                <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <span>
                  {executionTarget === 'EDGE'
                    ? '符合边缘网关离线独立执行要求，将在边缘完成实时评估与保护控制指令联动，并在重连后同步告警事实。'
                    : '符合云边协同执行要求，可同时编入边缘网关规则包（离在线均就近执行）与云端全量规则链路。'}
                </span>
              </div>
            ) : (
              <div role="alert" className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning-soft px-3 py-2 text-xs text-warning-text">
                <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <span>{edgeEligibility.message ?? '当前配置暂不满足边缘执行约束，请调整后再保存。'}</span>
              </div>
            )
          ) : null}

          {triggerMode === 'DURATION' ? (
            <div className="flex items-center gap-2 rounded-lg border border-divider bg-muted/20 px-3 py-2">
              <span className="whitespace-nowrap text-foreground">持续保持时长（秒）：</span>
              <Input
                type="number"
                aria-label="持续保持时长（秒）"
                min={1}
                max={31536000}
                value={durationSeconds}
                onKeyDown={preventImeEnterSubmit}
                onChange={(e) => setDurationSeconds(Math.max(1, Number(e.target.value) || 1))}
              />
            </div>
          ) : null}

          {triggerMode === 'REPEATING' ? (
            <div className="flex items-center gap-2 rounded-lg border border-divider bg-muted/20 px-3 py-2">
              <span className="whitespace-nowrap text-foreground">连续命中采样次数：</span>
              <Input
                type="number"
                aria-label="连续命中采样次数"
                min={1}
                value={repeatCount}
                onKeyDown={preventImeEnterSubmit}
                onChange={(e) => setRepeatCount(Math.max(1, Number(e.target.value) || 1))}
              />
            </div>
          ) : null}

          <AlarmRuleConditionEditor
            label="触发报警条件"
            value={raise}
            onChange={setRaise}
            equipmentOptions={equipmentOptions}
          />

          <AlarmRuleConditionEditor
            label="自动恢复条件"
            value={clear}
            onChange={setClear}
            equipmentOptions={equipmentOptions}
          />

          <AlarmRuleDependenciesSection
            otherRules={otherRules}
            dependsOnRuleIds={dependsOnRuleIds}
            setDependsOnRuleIds={setDependsOnRuleIds}
            dependencyGate={dependencyGate}
            setDependencyGate={setDependencyGate}
            cycleRuleIds={dagPreview.cycleRuleIds}
            overDepthRuleIds={dagPreview.overDepthRuleIds}
          />

          <AlarmRuleActionsSection
            title={title}
            actions={actions}
            setActions={setActions}
            equipmentOptions={equipmentOptions}
          />

          {validationError ? (
            <div role="alert" className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive-text">
              {validationError}
            </div>
          ) : null}

          <SheetFooter className="border-t border-divider px-0 pt-3">
            <div className="flex flex-wrap items-center justify-end gap-2">
              <Button type="button" variant="outline" onClick={onClose}>
                取消
              </Button>
              <Button type="button" variant="outline" onClick={handleSimulate}>
                <Play data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                试算草稿
              </Button>
              <Button type="submit">
                <Save data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                保存至草稿
              </Button>
            </div>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  );
}
