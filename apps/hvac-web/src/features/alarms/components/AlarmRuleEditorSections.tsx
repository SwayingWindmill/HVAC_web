import type { Dispatch, SetStateAction } from 'react';
import { AlertTriangle, Plus, Trash2 } from 'lucide-react';
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
import type { AlarmRuleAction, AlarmRuleDependencyGate } from '../alarm-operations';
import type { AlarmRule } from '../alarm-rules';
import {
  preventImeEnterSubmit,
  type EquipmentReadingOption,
} from './alarm-rule-condition-options';
import {
  ACTION_KIND_OPTIONS,
  ACTION_TRIGGER_OPTIONS,
  DEPENDENCY_GATE_OPTIONS,
  PROTECTION_CAPABILITY_OPTIONS,
  WORK_ORDER_PRIORITY_OPTIONS,
} from './alarm-rule-editor-validation';

export function AlarmRuleDependenciesSection({
  otherRules,
  dependsOnRuleIds,
  setDependsOnRuleIds,
  dependencyGate,
  setDependencyGate,
  cycleRuleIds,
  overDepthRuleIds,
}: {
  readonly otherRules: readonly AlarmRule[];
  readonly dependsOnRuleIds: string[];
  readonly setDependsOnRuleIds: Dispatch<SetStateAction<string[]>>;
  readonly dependencyGate: AlarmRuleDependencyGate;
  readonly setDependencyGate: (next: AlarmRuleDependencyGate) => void;
  readonly cycleRuleIds: readonly string[];
  readonly overDepthRuleIds: readonly string[];
}) {
  return (
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
      {cycleRuleIds.length > 0 || overDepthRuleIds.length > 0 ? (
        <div role="alert" className="flex items-center gap-1.5 text-destructive-text">
          <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
          <span>
            {cycleRuleIds.length > 0 ? '检测到联动环路，请取消冲突的前置规则。' : '当前依赖深度已超过 3 跳上限。'}
          </span>
        </div>
      ) : null}
    </div>
  );
}

export function AlarmRuleActionsSection({
  title,
  actions,
  setActions,
  equipmentOptions,
}: {
  readonly title: string;
  readonly actions: readonly AlarmRuleAction[];
  readonly setActions: Dispatch<SetStateAction<AlarmRuleAction[]>>;
  readonly equipmentOptions: readonly EquipmentReadingOption[];
}) {
  return (
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
  );
}
