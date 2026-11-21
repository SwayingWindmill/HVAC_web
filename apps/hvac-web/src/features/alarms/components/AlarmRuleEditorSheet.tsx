import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { AlertTriangle, CheckCircle2, Play, Plus, Save, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
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
import type { AlarmRuleAction, AlarmRuleActionKind, AlarmRuleActionTrigger, AlarmRuleDependencyGate } from '../alarm-operations';
import {
  computeRuleDagLayout,
  evaluateLocalEdgeEligibility,
  newRuleUuidV7,
} from '../alarm-rule-dag';
import type { AlarmRule, AlarmRuleCondition, AlarmRuleSeverity } from '../alarm-rules';
import {
  AlarmRuleConditionEditor,
  buildEquipmentOptions,
  createConditionForKind,
  preventImeEnterSubmit,
} from './AlarmRuleConditionEditor';

const SEVERITY_OPTIONS: ReadonlyArray<{ readonly value: AlarmRuleSeverity; readonly label: string }> = [
  { value: 'CRITICAL', label: '紧急' },
  { value: 'MAJOR', label: '重要' },
  { value: 'MINOR', label: '次要' },
  { value: 'WARNING', label: '预警' },
  { value: 'INFO', label: '提示' },
];

const RULE_KIND_OPTIONS: ReadonlyArray<{ readonly value: NonNullable<AlarmRule['ruleKind']>; readonly label: string }> = [
  { value: 'THRESHOLD', label: '阈值触发' },
  { value: 'SLOPE', label: '斜率变化' },
  { value: 'WINDOW', label: '流式窗口' },
  { value: 'EXPRESSION', label: '多点表达式' },
];

const EXECUTION_TARGET_OPTIONS: ReadonlyArray<{ readonly value: NonNullable<AlarmRule['executionTarget']>; readonly label: string }> = [
  { value: 'CLOUD', label: '仅云端执行' },
  { value: 'EDGE', label: '仅边缘网关执行' },
  { value: 'CLOUD_AND_EDGE', label: '云边协同执行' },
];

const TRIGGER_MODE_OPTIONS: ReadonlyArray<{ readonly value: AlarmRule['triggerMode']; readonly label: string }> = [
  { value: 'SIMPLE', label: '瞬时满足立即触发' },
  { value: 'DURATION', label: '持续时长达标后触发' },
  { value: 'REPEATING', label: '连续多次命中后触发' },
];

const DEPENDENCY_GATE_OPTIONS: ReadonlyArray<{ readonly value: AlarmRuleDependencyGate; readonly label: string }> = [
  { value: 'NONE', label: '仅标注关系（无前置阻断）' },
  { value: 'ALL_ACTIVE', label: '需全部上游告警均处于活动态' },
  { value: 'ANY_ACTIVE', label: '任一上游告警处于活动态即可' },
];

const ACTION_KIND_OPTIONS: ReadonlyArray<{ readonly value: AlarmRuleActionKind; readonly label: string }> = [
  { value: 'WORK_ORDER', label: '自动创建运维工单' },
  { value: 'PROTECTION_COMMAND', label: '下发设备保护控制指令' },
  { value: 'WEBHOOK', label: '回调外部 Webhook 接口' },
];

const ACTION_TRIGGER_OPTIONS: ReadonlyArray<{ readonly value: AlarmRuleActionTrigger; readonly label: string }> = [
  { value: 'RAISE', label: '触发告警时' },
  { value: 'CLEAR', label: '自动恢复时' },
  { value: 'BOTH', label: '触发与恢复均执行' },
];

const WORK_ORDER_PRIORITY_OPTIONS: ReadonlyArray<{
  readonly value: NonNullable<AlarmRuleAction['workOrderPriority']>;
  readonly label: string;
}> = [
  { value: 'LOW', label: '低' },
  { value: 'MEDIUM', label: '中' },
  { value: 'HIGH', label: '高' },
  { value: 'URGENT', label: '紧急' },
];

const PROTECTION_CAPABILITY_OPTIONS: ReadonlyArray<{
  readonly value: string;
  readonly label: string;
  readonly paramKey?: string;
  readonly paramLabel?: string;
  readonly defaultVal?: number;
  readonly min?: number;
  readonly max?: number;
  readonly step?: number;
}> = [
  { value: 'STOP', label: '停机保护' },
  { value: 'START', label: '联动启动' },
  { value: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', label: '设定冷冻水温度目标', paramKey: 'setpointC', paramLabel: '设定温度 (°C)', defaultVal: 8, min: 5, max: 12, step: 0.5 },
  { value: 'SET_FREQUENCY', label: '调整频率', paramKey: 'frequencyHz', paramLabel: '目标频率 (Hz)', defaultVal: 40, min: 20, max: 50, step: 0.5 },
  { value: 'SET_OPENING', label: '调整阀位开度', paramKey: 'openingPct', paramLabel: '目标开度 (%)', defaultVal: 50, min: 0, max: 100, step: 1 },
  { value: 'SET_TEMPERATURE_SETPOINT', label: '设定温度目标', paramKey: 'setpointC', paramLabel: '设定温度 (°C)', defaultVal: 24, min: 16, max: 30, step: 0.5 },
];

function validateConditionTree(prefix: string, cond: AlarmRuleCondition): string | null {
  switch (cond.kind) {
    case 'AND':
    case 'OR': {
      const children = cond.children ?? [];
      if (children.length < 2) return `${prefix}的组合逻辑至少需要包含 2 条子条件`;
      for (let i = 0; i < children.length; i += 1) {
        const err = validateConditionTree(`${prefix}第 ${i + 1} 项`, children[i]);
        if (err) return err;
      }
      return null;
    }
    case 'COMPARE': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      const numericOp = cond.operator === 'GT' || cond.operator === 'GTE' || cond.operator === 'LT' || cond.operator === 'LTE';
      if (numericOp && (typeof cond.value !== 'number' || !Number.isFinite(cond.value))) {
        return `${prefix}选择大小比较符号时，目标阈值必须为有效数值`;
      }
      if (cond.value === undefined || cond.value === '') return `${prefix}缺少比较阈值`;
      return null;
    }
    case 'RANGE': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (cond.minimum === undefined || cond.maximum === undefined || !Number.isFinite(cond.minimum) || !Number.isFinite(cond.maximum)) {
        return `${prefix}需要填写有效的区间上下限`;
      }
      if (cond.minimum > cond.maximum) return `${prefix}的安全区间下限不能大于上限`;
      return null;
    }
    case 'HYSTERESIS': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (cond.trigger === undefined || cond.reset === undefined || !Number.isFinite(cond.trigger) || !Number.isFinite(cond.reset)) {
        return `${prefix}需要填写有效的触发阈值与回差恢复值`;
      }
      if ((cond.direction ?? 'ABOVE') === 'ABOVE' && cond.reset >= cond.trigger) {
        return `${prefix}为上限越限迟滞时，回差恢复值必须严格小于触发阈值`;
      }
      if (cond.direction === 'BELOW' && cond.reset <= cond.trigger) {
        return `${prefix}为下限越限迟滞时，回差恢复值必须严格大于触发阈值`;
      }
      return null;
    }
    case 'SLOPE': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (cond.delta === undefined || !Number.isFinite(cond.delta)) return `${prefix}需要填写有效的斜率变化幅度`;
      if (!cond.windowSeconds || cond.windowSeconds < 1 || cond.windowSeconds > 86400) {
        return `${prefix}的斜率时间窗必须在 1 至 86400 秒之间`;
      }
      return null;
    }
    case 'WINDOW_AGG': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (!cond.agg || !cond.windowType || !cond.operator) return `${prefix}的窗口聚合参数不完整`;
      if (cond.threshold === undefined || !Number.isFinite(cond.threshold)) return `${prefix}需要填写有效的聚合阈值`;
      if (!cond.windowSeconds || !Number.isInteger(cond.windowSeconds) || cond.windowSeconds < 1 || cond.windowSeconds > 86400) {
        return `${prefix}的窗口时长必须在 1 至 86400 秒之间`;
      }
      const minAllowed = cond.agg === 'STDDEV' ? 2 : 1;
      if (!cond.minSamples || !Number.isInteger(cond.minSamples) || cond.minSamples < minAllowed || cond.minSamples > 128) {
        return `${prefix}的最小样本数必须在 ${minAllowed} 至 128 之间`;
      }
      return null;
    }
    case 'EXPRESSION': {
      if (!(cond.expression ?? '').trim()) return `${prefix}的多点表达式不能为空`;
      const inputs = Object.values(cond.inputs ?? {});
      if (inputs.length === 0 || inputs.some((r) => !r.deviceId || !r.pointCode.trim())) {
        return `${prefix}的多点表达式至少需要绑定 1 个完整输入变量`;
      }
      return null;
    }
    case 'NO_DATA':
    case 'STALE':
    case 'UNCHANGED':
    case 'CHANGED':
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (!cond.ageSeconds || cond.ageSeconds < 1 || cond.ageSeconds > 31_536_000) {
        return `${prefix}的阈值时长必须在 1 至 31536000 秒之间`;
      }
      return null;
    case 'REASONS':
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (!cond.reasons || cond.reasons.length === 0) return `${prefix}至少需要配置 1 项诊断原因`;
      return null;
  }
}

function createEmptyRule(equipment: SiteEquipmentIndex): AlarmRule {
  const equipmentOptions = buildEquipmentOptions(equipment);
  const reading = {
    deviceId: equipmentOptions[0]?.deviceId ?? '018f3e00-3000-7000-8000-000000000001',
    pointCode: equipmentOptions[0]?.points[0]?.pointCode ?? 'supply_water_temperature',
  };
  return {
    ruleId: newRuleUuidV7(),
    title: '新增供水温度监测规则',
    severity: 'MAJOR',
    ruleKind: 'THRESHOLD',
    triggerMode: 'DURATION',
    durationSeconds: 300,
    dependsOnRuleIds: [],
    dependencyGate: 'NONE',
    actions: [],
    raise: createConditionForKind('COMPARE', equipmentOptions, reading),
    clear: { kind: 'COMPARE', reading, operator: 'LT', value: 10 },
  };
}

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

          <div className="flex flex-col gap-2 rounded-lg border border-divider bg-card p-3">
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium text-foreground">上游因果依赖（DAG 联动）</span>
              <span className="text-[11px] text-muted-foreground">最多级联 3 跳</span>
            </div>
            {otherRules.length === 0 ? (
              <span className="text-muted-foreground">站点暂无其他可选前置规则。</span>
            ) : (
              <div className="flex max-h-36 flex-col gap-1.5 overflow-y-auto">
                {otherRules.map((candidate) => {
                  const checked = dependsOnRuleIds.includes(candidate.ruleId);
                  return (
                    <Label key={candidate.ruleId} className="flex cursor-pointer items-center gap-2 font-normal text-foreground">
                      <Checkbox
                        checked={checked}
                        onCheckedChange={(next) => {
                          setDependsOnRuleIds((prev) =>
                            next === true
                              ? (prev.includes(candidate.ruleId) ? prev : [...prev, candidate.ruleId])
                              : prev.filter((id) => id !== candidate.ruleId),
                          );
                        }}
                      />
                      <span className="truncate">{candidate.title}</span>
                    </Label>
                  );
                })}
              </div>
            )}
            {dependsOnRuleIds.length > 0 ? (
              <div className="flex flex-col gap-1 pt-1">
                <span className="text-[11px] text-muted-foreground">依赖门控模式</span>
                <Select
                  value={dependencyGate}
                  onValueChange={(raw) => {
                    const matched = DEPENDENCY_GATE_OPTIONS.find((opt) => opt.value === raw);
                    if (matched) setDependencyGate(matched.value);
                  }}
                >
                  <SelectTrigger className="w-full" aria-label="依赖门控模式"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {DEPENDENCY_GATE_OPTIONS.map((opt) => (
                      <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ) : null}
            {dagPreview.cycleRuleIds.length > 0 || dagPreview.overDepthRuleIds.length > 0 ? (
              <div role="alert" className="flex items-center gap-1.5 text-destructive-text">
                <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
                <span>
                  {dagPreview.cycleRuleIds.length > 0 ? '检测到联动环路，请取消冲突的前置规则。' : '当前依赖深度已超过 3 跳上限。'}
                </span>
              </div>
            ) : null}
          </div>

          <div className="flex flex-col gap-2 rounded-lg border border-divider bg-card p-3">
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium text-foreground">自动执行动作（最多 4 项）</span>
              {actions.length < 4 ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setActions((prev) => [
                    ...prev,
                    { kind: 'WORK_ORDER', triggerOn: 'RAISE', workOrderPriority: 'HIGH', workOrderTitle: title.trim() || '设备异常巡检工单' },
                  ])}
                >
                  <Plus data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                  添加动作
                </Button>
              ) : null}
            </div>

            {actions.length === 0 ? (
              <span className="text-muted-foreground">未配置自动联动动作（仅记录并推送告警）。</span>
            ) : (
              <div className="flex flex-col gap-2">
                {actions.map((action, idx) => (
                  <div key={idx} className="flex flex-col gap-2 rounded-md border border-divider bg-muted/20 p-2.5">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="font-medium text-foreground">动作 #{idx + 1}</span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() => setActions((prev) => prev.filter((_, i) => i !== idx))}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                        移除
                      </Button>
                    </div>

                    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                      <Select
                        value={action.kind}
                        onValueChange={(raw) => {
                          const matched = ACTION_KIND_OPTIONS.find((opt) => opt.value === raw);
                          if (!matched) return;
                          const triggerOn = action.triggerOn ?? 'RAISE';
                          const nextAction: AlarmRuleAction = matched.value === 'WORK_ORDER'
                            ? { kind: 'WORK_ORDER', triggerOn, workOrderPriority: 'HIGH', workOrderTitle: title.trim() || '设备异常巡检工单' }
                            : matched.value === 'PROTECTION_COMMAND'
                              ? { kind: 'PROTECTION_COMMAND', triggerOn, targetDeviceId: equipmentOptions[0]?.deviceId ?? '018f3e00-3000-7000-8000-000000000001', capability: 'STOP' }
                              : { kind: 'WEBHOOK', triggerOn, webhookUrl: '' };
                          setActions((prev) => prev.map((item, i) => (i === idx ? nextAction : item)));
                        }}
                      >
                        <SelectTrigger className="w-full" aria-label={`动作 #${idx + 1} 类型`}><SelectValue /></SelectTrigger>
                        <SelectContent>
                          {ACTION_KIND_OPTIONS.map((opt) => (
                            <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                          ))}
                        </SelectContent>
                      </Select>

                      <Select
                        value={action.triggerOn ?? 'RAISE'}
                        onValueChange={(raw) => {
                          const matched = ACTION_TRIGGER_OPTIONS.find((opt) => opt.value === raw);
                          if (!matched) return;
                          setActions((prev) => prev.map((item, i) => (i === idx ? { ...item, triggerOn: matched.value } : item)));
                        }}
                      >
                        <SelectTrigger className="w-full" aria-label={`动作 #${idx + 1} 触发时机`}><SelectValue /></SelectTrigger>
                        <SelectContent>
                          {ACTION_TRIGGER_OPTIONS.map((opt) => (
                            <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>

                    {action.kind === 'WORK_ORDER' ? (
                      <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                        <Select
                          value={action.workOrderPriority ?? 'HIGH'}
                          onValueChange={(raw) => {
                            const matched = WORK_ORDER_PRIORITY_OPTIONS.find((opt) => opt.value === raw);
                            if (!matched) return;
                            setActions((prev) => prev.map((item, i) => (i === idx ? { ...item, workOrderPriority: matched.value } : item)));
                          }}
                        >
                          <SelectTrigger className="w-full" aria-label={`动作 #${idx + 1} 工单优先级`}><SelectValue /></SelectTrigger>
                          <SelectContent>
                            {WORK_ORDER_PRIORITY_OPTIONS.map((opt) => (
                              <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        <Input
                          className="sm:col-span-2"
                          aria-label={`动作 #${idx + 1} 工单标题`}
                          value={action.workOrderTitle ?? ''}
                          maxLength={200}
                          onKeyDown={preventImeEnterSubmit}
                          onChange={(e) => setActions((prev) => prev.map((item, i) => (i === idx ? { ...item, workOrderTitle: e.target.value } : item)))}
                          placeholder="工单标题"
                        />
                      </div>
                    ) : null}

                    {action.kind === 'PROTECTION_COMMAND' ? (() => {
                      const capSpec = PROTECTION_CAPABILITY_OPTIONS.find((opt) => opt.value === (action.capability ?? 'STOP'));
                      const paramKey = capSpec?.paramKey;
                      const currentParamVal = paramKey ? (action.parameters?.[paramKey] ?? capSpec.defaultVal ?? 0) : undefined;
                      return (
                        <div className="flex flex-col gap-2">
                          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                            <Select
                              value={action.targetDeviceId ?? equipmentOptions[0]?.deviceId ?? ''}
                              onValueChange={(devId) => setActions((prev) => prev.map((item, i) => (i === idx ? { ...item, targetDeviceId: devId } : item)))}
                            >
                              <SelectTrigger className="w-full" aria-label={`动作 #${idx + 1} 目标设备`}><SelectValue /></SelectTrigger>
                              <SelectContent>
                                {equipmentOptions.map((dev) => (
                                  <SelectItem key={dev.deviceId} value={dev.deviceId}>{dev.deviceName}</SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                            <Select
                              value={action.capability ?? 'STOP'}
                              onValueChange={(cap) => {
                                const spec = PROTECTION_CAPABILITY_OPTIONS.find((opt) => opt.value === cap);
                                setActions((prev) => prev.map((item, i) => (i === idx ? {
                                  ...item,
                                  capability: cap,
                                  parameters: spec?.paramKey && spec.defaultVal !== undefined
                                    ? { [spec.paramKey]: spec.defaultVal }
                                    : undefined,
                                } : item)));
                              }}
                            >
                              <SelectTrigger className="w-full" aria-label={`动作 #${idx + 1} 控制能力`}><SelectValue /></SelectTrigger>
                              <SelectContent>
                                {PROTECTION_CAPABILITY_OPTIONS.map((opt) => (
                                  <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </div>
                          {paramKey && capSpec ? (
                            <div className="flex items-center gap-2">
                              <span className="whitespace-nowrap text-muted-foreground">{capSpec.paramLabel}：</span>
                              <Input
                                type="number"
                                aria-label={`动作 #${idx + 1} ${capSpec.paramLabel}`}
                                min={capSpec.min}
                                max={capSpec.max}
                                step={capSpec.step ?? 'any'}
                                value={currentParamVal}
                                onKeyDown={preventImeEnterSubmit}
                                onChange={(e) => {
                                  const nextVal = Number(e.target.value);
                                  setActions((prev) => prev.map((item, i) => (i === idx ? {
                                    ...item,
                                    parameters: { [paramKey]: Number.isFinite(nextVal) ? nextVal : (capSpec.defaultVal ?? 0) },
                                  } : item)));
                                }}
                              />
                            </div>
                          ) : null}
                        </div>
                      );
                    })() : null}

                    {action.kind === 'WEBHOOK' ? (
                      <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                        <Input
                          className="sm:col-span-2"
                          aria-label={`动作 #${idx + 1} 回调地址`}
                          value={action.webhookUrl ?? ''}
                          maxLength={512}
                          onKeyDown={preventImeEnterSubmit}
                          onChange={(e) => setActions((prev) => prev.map((item, i) => (i === idx ? { ...item, webhookUrl: e.target.value } : item)))}
                          placeholder="输入 https:// 开头的回调地址"
                        />
                        <Input
                          aria-label={`动作 #${idx + 1} 密钥引用`}
                          value={action.secretRef ?? ''}
                          maxLength={128}
                          onKeyDown={preventImeEnterSubmit}
                          onChange={(e) => setActions((prev) => prev.map((item, i) => (i === idx ? { ...item, secretRef: e.target.value || undefined } : item)))}
                          placeholder="密钥引用（选填）"
                        />
                      </div>
                    ) : null}
                  </div>
                ))}
              </div>
            )}
          </div>

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
