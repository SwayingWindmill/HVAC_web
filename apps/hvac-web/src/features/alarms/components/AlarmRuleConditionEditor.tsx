import { Plus, Trash2 } from 'lucide-react';
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
import type { AlarmRuleCondition } from '../alarm-rules';
import {
  COMPARE_OP_OPTIONS,
  DIAGNOSTIC_REASON_OPTIONS,
  HYSTERESIS_DIRECTIONS,
  KIND_OPTIONS,
  SLOPE_OP_OPTIONS,
  WINDOW_AGG_OPTIONS,
  WINDOW_TYPE_OPTIONS,
  buildEquipmentOptions,
  createConditionForKind,
  defaultReadingFrom,
  parseCompareInputValue,
  preventImeEnterSubmit,
  type EquipmentReadingOption,
} from './alarm-rule-condition-options';
import { NumericInput, ReadingPicker } from './AlarmRuleConditionInputs';

export { buildEquipmentOptions, createConditionForKind, preventImeEnterSubmit, type EquipmentReadingOption };

export function AlarmRuleConditionEditor({
  label,
  value,
  onChange,
  equipmentOptions,
  depth = 0,
}: {
  readonly label: string;
  readonly value: AlarmRuleCondition;
  readonly onChange: (next: AlarmRuleCondition) => void;
  readonly equipmentOptions: readonly EquipmentReadingOption[];
  readonly depth?: number;
}) {
  const kindOptions = depth >= 2
    ? KIND_OPTIONS.filter((opt) => opt.value !== 'AND' && opt.value !== 'OR')
    : KIND_OPTIONS;

  const fallbackReading = defaultReadingFrom(equipmentOptions, value.reading);
  const currentReading = value.reading ?? fallbackReading;

  const handleKindChange = (rawKind: string) => {
    const matched = KIND_OPTIONS.find((opt) => opt.value === rawKind);
    if (!matched || matched.value === value.kind) return;
    onChange(createConditionForKind(matched.value, equipmentOptions, value.reading));
  };

  return (
    <div className="flex flex-col gap-2.5 rounded-lg border border-divider bg-muted/20 p-3 text-xs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Label className="text-xs font-medium text-foreground">{label}</Label>
        <Select value={value.kind} onValueChange={handleKindChange}>
          <SelectTrigger className="w-44" aria-label={`${label}判定类型`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {kindOptions.map((opt) => (
              <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {value.kind === 'AND' || value.kind === 'OR' ? (
        <div className="flex flex-col gap-2 pl-2 border-l-2 border-primary/30">
          {(value.children ?? []).map((child, idx) => (
            <div key={idx} className="flex flex-col gap-1.5">
              <AlarmRuleConditionEditor
                label={`子条件 #${idx + 1}`}
                value={child}
                equipmentOptions={equipmentOptions}
                depth={depth + 1}
                onChange={(nextChild) => {
                  const nextChildren = [...(value.children ?? [])];
                  nextChildren[idx] = nextChild;
                  onChange({ ...value, children: nextChildren });
                }}
              />
              {(value.children?.length ?? 0) > 2 ? (
                <div className="flex justify-end">
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      const nextChildren = (value.children ?? []).filter((_, i) => i !== idx);
                      onChange({ ...value, children: nextChildren });
                    }}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                    移除子条件 #{idx + 1}
                  </Button>
                </div>
              ) : null}
            </div>
          ))}
          {(value.children?.length ?? 0) < 8 ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onChange({
                ...value,
                children: [...(value.children ?? []), createConditionForKind('COMPARE', equipmentOptions)],
              })}
            >
              <Plus data-icon="inline-start" className="size-3.5" aria-hidden="true" />
              添加子条件
            </Button>
          ) : null}
        </div>
      ) : null}

      {value.kind === 'COMPARE' ? (
        <>
          <ReadingPicker
            ariaPrefix={label}
            reading={currentReading}
            equipmentOptions={equipmentOptions}
            onChange={(nextReading) => onChange({ ...value, reading: nextReading })}
          />
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">比较方式</span>
              <Select
                value={value.operator ?? 'GT'}
                onValueChange={(rawOp) => {
                  const matched = COMPARE_OP_OPTIONS.find((opt) => opt.value === rawOp);
                  if (matched) onChange({ ...value, operator: matched.value });
                }}
              >
                <SelectTrigger className="w-full" aria-label={`${label}比较方式`}><SelectValue /></SelectTrigger>
                <SelectContent>
                  {COMPARE_OP_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">目标阈值</span>
              <Input
                aria-label={`${label}目标阈值`}
                value={value.value === undefined ? '' : String(value.value)}
                onKeyDown={preventImeEnterSubmit}
                onChange={(e) => onChange({ ...value, value: parseCompareInputValue(e.target.value) })}
                placeholder="输入数值、运行状态或布尔值"
              />
            </div>
          </div>
        </>
      ) : null}

      {value.kind === 'RANGE' ? (
        <>
          <ReadingPicker
            ariaPrefix={label}
            reading={currentReading}
            equipmentOptions={equipmentOptions}
            onChange={(nextReading) => onChange({ ...value, reading: nextReading })}
          />
          <div className="grid grid-cols-2 gap-2">
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">安全下限</span>
              <NumericInput
                ariaLabel={`${label}安全下限`}
                value={value.minimum}
                placeholder="下限"
                onCommit={(next) => onChange({ ...value, minimum: next })}
              />
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">安全上限</span>
              <NumericInput
                ariaLabel={`${label}安全上限`}
                value={value.maximum}
                placeholder="上限"
                onCommit={(next) => onChange({ ...value, maximum: next })}
              />
            </div>
          </div>
        </>
      ) : null}

      {value.kind === 'HYSTERESIS' ? (
        <>
          <ReadingPicker
            ariaPrefix={label}
            reading={currentReading}
            equipmentOptions={equipmentOptions}
            onChange={(nextReading) => onChange({ ...value, reading: nextReading })}
          />
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">越限方向</span>
              <Select
                value={value.direction ?? 'ABOVE'}
                onValueChange={(rawDir) => {
                  const matched = HYSTERESIS_DIRECTIONS.find((d) => d.value === rawDir);
                  if (matched) onChange({ ...value, direction: matched.value });
                }}
              >
                <SelectTrigger className="w-full" aria-label={`${label}越限方向`}><SelectValue /></SelectTrigger>
                <SelectContent>
                  {HYSTERESIS_DIRECTIONS.map((d) => (
                    <SelectItem key={d.value} value={d.value}>{d.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">触发阈值</span>
              <NumericInput
                ariaLabel={`${label}触发阈值`}
                value={value.trigger}
                placeholder="触发阈值"
                onCommit={(next) => onChange({ ...value, trigger: next })}
              />
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">回差恢复值</span>
              <NumericInput
                ariaLabel={`${label}回差恢复值`}
                value={value.reset}
                placeholder="回差恢复值"
                onCommit={(next) => onChange({ ...value, reset: next })}
              />
            </div>
          </div>
        </>
      ) : null}

      {value.kind === 'SLOPE' ? (
        <>
          <ReadingPicker
            ariaPrefix={label}
            reading={currentReading}
            equipmentOptions={equipmentOptions}
            onChange={(nextReading) => onChange({ ...value, reading: nextReading })}
          />
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">变化方向</span>
              <Select
                value={value.operator ?? 'GT'}
                onValueChange={(rawOp) => {
                  const matched = SLOPE_OP_OPTIONS.find((opt) => opt.value === rawOp);
                  if (matched) onChange({ ...value, operator: matched.value });
                }}
              >
                <SelectTrigger className="w-full" aria-label={`${label}变化方向`}><SelectValue /></SelectTrigger>
                <SelectContent>
                  {SLOPE_OP_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">变化幅度</span>
              <NumericInput
                ariaLabel={`${label}变化幅度`}
                value={value.delta}
                placeholder="变化幅度"
                onCommit={(next) => onChange({ ...value, delta: next })}
              />
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">时间窗 (秒)</span>
              <NumericInput
                ariaLabel={`${label}时间窗秒数`}
                value={value.windowSeconds ?? 300}
                min={1}
                max={86400}
                integerOnly
                placeholder="窗口秒数"
                onCommit={(next) => onChange({ ...value, windowSeconds: next })}
              />
            </div>
          </div>
        </>
      ) : null}

      {value.kind === 'WINDOW_AGG' ? (
        <>
          <ReadingPicker
            ariaPrefix={label}
            reading={currentReading}
            equipmentOptions={equipmentOptions}
            onChange={(nextReading) => onChange({ ...value, reading: nextReading })}
          />
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">聚合函数</span>
              <Select
                value={value.agg ?? 'AVG'}
                onValueChange={(rawAgg) => {
                  const matched = WINDOW_AGG_OPTIONS.find((opt) => opt.value === rawAgg);
                  if (!matched) return;
                  const nextMin = matched.value === 'STDDEV'
                    ? Math.max(2, value.minSamples ?? 2)
                    : (value.minSamples ?? 3);
                  onChange({ ...value, agg: matched.value, minSamples: nextMin });
                }}
              >
                <SelectTrigger className="w-full" aria-label={`${label}聚合函数`}><SelectValue /></SelectTrigger>
                <SelectContent>
                  {WINDOW_AGG_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">窗口类型</span>
              <Select
                value={value.windowType ?? 'SLIDING'}
                onValueChange={(rawType) => {
                  const matched = WINDOW_TYPE_OPTIONS.find((opt) => opt.value === rawType);
                  if (matched) onChange({ ...value, windowType: matched.value });
                }}
              >
                <SelectTrigger className="w-full" aria-label={`${label}窗口类型`}><SelectValue /></SelectTrigger>
                <SelectContent>
                  {WINDOW_TYPE_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">比较方式</span>
              <Select
                value={value.operator ?? 'GT'}
                onValueChange={(rawOp) => {
                  const matched = COMPARE_OP_OPTIONS.find((opt) => opt.value === rawOp);
                  if (matched) onChange({ ...value, operator: matched.value });
                }}
              >
                <SelectTrigger className="w-full" aria-label={`${label}窗口比较方式`}><SelectValue /></SelectTrigger>
                <SelectContent>
                  {COMPARE_OP_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">聚合阈值</span>
              <NumericInput
                ariaLabel={`${label}聚合阈值`}
                value={value.threshold ?? 0}
                placeholder="聚合阈值"
                onCommit={(next) => onChange({ ...value, threshold: next })}
              />
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">窗口时长 (秒)</span>
              <NumericInput
                ariaLabel={`${label}窗口时长秒数`}
                value={value.windowSeconds ?? 300}
                min={1}
                max={86400}
                integerOnly
                placeholder="1 至 86400"
                onCommit={(next) => onChange({ ...value, windowSeconds: next })}
              />
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">
                最小样本数{value.agg === 'STDDEV' ? ' (至少 2)' : ''}
              </span>
              <NumericInput
                ariaLabel={`${label}最小样本数`}
                value={value.minSamples ?? (value.agg === 'STDDEV' ? 2 : 3)}
                min={value.agg === 'STDDEV' ? 2 : 1}
                max={128}
                integerOnly
                placeholder="1 至 128"
                onCommit={(next) => onChange({ ...value, minSamples: next })}
              />
            </div>
          </div>
        </>
      ) : null}

      {value.kind === 'EXPRESSION' ? (
        <div className="flex flex-col gap-2">
          <Input
            aria-label={`${label}表达式`}
            value={value.expression ?? ''}
            onKeyDown={preventImeEnterSubmit}
            onChange={(e) => onChange({ ...value, expression: e.target.value })}
            placeholder="例如：a - b > 3"
          />
          <div className="flex flex-col gap-2">
            {Object.entries(value.inputs ?? {}).map(([alias, reading]) => (
              <div key={alias} className="flex flex-col gap-1.5 rounded border border-divider bg-card p-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-mono font-medium text-foreground">变量 {alias}</span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      const nextInputs = { ...(value.inputs ?? {}) };
                      delete nextInputs[alias];
                      onChange({ ...value, inputs: nextInputs });
                    }}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                    删除变量
                  </Button>
                </div>
                <ReadingPicker
                  ariaPrefix={`${label}变量 ${alias}`}
                  reading={reading}
                  equipmentOptions={equipmentOptions}
                  onChange={(nextReading) => onChange({
                    ...value,
                    inputs: { ...(value.inputs ?? {}), [alias]: nextReading },
                  })}
                />
              </div>
            ))}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                const existingKeys = new Set(Object.keys(value.inputs ?? {}));
                const nextVar = ['a', 'b', 'c', 'd', 'e', 'f'].find((c) => !existingKeys.has(c)) ?? `x${existingKeys.size + 1}`;
                onChange({
                  ...value,
                  inputs: { ...(value.inputs ?? {}), [nextVar]: defaultReadingFrom(equipmentOptions) },
                });
              }}
            >
              <Plus data-icon="inline-start" className="size-3.5" aria-hidden="true" />
              添加输入变量
            </Button>
          </div>
        </div>
      ) : null}

      {value.kind === 'NO_DATA' || value.kind === 'STALE' || value.kind === 'UNCHANGED' || value.kind === 'CHANGED' ? (
        <>
          <ReadingPicker
            ariaPrefix={label}
            reading={currentReading}
            equipmentOptions={equipmentOptions}
            onChange={(nextReading) => onChange({ ...value, reading: nextReading })}
          />
          <div className="flex items-center gap-2">
            <span className="whitespace-nowrap text-muted-foreground">阈值时长（秒）：</span>
            <NumericInput
              ariaLabel={`${label}阈值时长（秒）`}
              value={value.ageSeconds ?? 300}
              min={1}
              max={31_536_000}
              integerOnly
              onCommit={(next) => onChange({ ...value, ageSeconds: next })}
            />
          </div>
        </>
      ) : null}

      {value.kind === 'REASONS' ? (
        <>
          <ReadingPicker
            ariaPrefix={label}
            reading={currentReading}
            equipmentOptions={equipmentOptions}
            onChange={(nextReading) => onChange({ ...value, reading: nextReading })}
          />
          <div className="flex flex-col gap-2">
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">匹配方式</span>
              <Select
                value={value.match ?? 'ANY'}
                onValueChange={(rawMatch) => {
                  if (rawMatch === 'ANY' || rawMatch === 'NONE') {
                    onChange({ ...value, match: rawMatch });
                  }
                }}
              >
                <SelectTrigger className="w-full" aria-label={`${label}诊断匹配方式`}><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="ANY">命中任意原因</SelectItem>
                  <SelectItem value="NONE">未命中以下原因</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-[11px] text-muted-foreground">诊断原因（最多 8 项）</span>
              <div className="grid grid-cols-2 gap-1.5 rounded border border-divider bg-card p-2">
                {DIAGNOSTIC_REASON_OPTIONS.map((opt) => {
                  const selected = (value.reasons ?? []).includes(opt.value);
                  const atLimit = !selected && (value.reasons?.length ?? 0) >= 8;
                  return (
                    <Label key={opt.value} className="flex cursor-pointer items-center gap-1.5 font-normal text-foreground">
                      <Checkbox
                        checked={selected}
                        disabled={atLimit}
                        onCheckedChange={(checked) => {
                          const prev = value.reasons ?? [];
                          const next = checked === true
                            ? (prev.includes(opt.value) ? prev : [...prev, opt.value].slice(0, 8))
                            : prev.filter((item) => item !== opt.value);
                          onChange({ ...value, reasons: next });
                        }}
                      />
                      <span className="truncate text-[11px]">{opt.label}</span>
                    </Label>
                  );
                })}
              </div>
            </div>
          </div>
        </>
      ) : null}
    </div>
  );
}
