import { useEffect, useMemo, useState, type KeyboardEvent } from 'react';
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
import type { SiteEquipmentIndex } from '@/features/assets/use-device-names';
import { WINDOW_AGG_LABELS, WINDOW_TYPE_LABELS } from '../alarm-rule-dag';
import type { AlarmRuleCondition, AlarmRuleReading } from '../alarm-rules';

type ConditionKind = AlarmRuleCondition['kind'];
type CompareOperator = NonNullable<AlarmRuleCondition['operator']>;
type HysteresisDirection = NonNullable<AlarmRuleCondition['direction']>;
type WindowAgg = NonNullable<AlarmRuleCondition['agg']>;
type WindowType = NonNullable<AlarmRuleCondition['windowType']>;

const KIND_OPTIONS: ReadonlyArray<{ readonly value: ConditionKind; readonly label: string }> = [
  { value: 'COMPARE', label: '单一阈值比较' },
  { value: 'HYSTERESIS', label: '回差死区' },
  { value: 'RANGE', label: '安全区间' },
  { value: 'SLOPE', label: '斜率突变' },
  { value: 'WINDOW_AGG', label: '流式窗口聚合' },
  { value: 'EXPRESSION', label: '多点表达式' },
  { value: 'NO_DATA', label: '长期缺数' },
  { value: 'STALE', label: '读数过期' },
  { value: 'UNCHANGED', label: '数值停滞' },
  { value: 'CHANGED', label: '近期变化' },
  { value: 'REASONS', label: '诊断原因命中' },
  { value: 'AND', label: '全部满足（且）' },
  { value: 'OR', label: '任一满足（或）' },
];

const WINDOW_AGG_OPTIONS: ReadonlyArray<{ readonly value: WindowAgg; readonly label: string }> = [
  { value: 'AVG', label: WINDOW_AGG_LABELS.AVG },
  { value: 'MIN', label: WINDOW_AGG_LABELS.MIN },
  { value: 'MAX', label: WINDOW_AGG_LABELS.MAX },
  { value: 'SUM', label: WINDOW_AGG_LABELS.SUM },
  { value: 'COUNT', label: WINDOW_AGG_LABELS.COUNT },
  { value: 'STDDEV', label: WINDOW_AGG_LABELS.STDDEV },
  { value: 'P95', label: WINDOW_AGG_LABELS.P95 },
  { value: 'P99', label: WINDOW_AGG_LABELS.P99 },
];

const WINDOW_TYPE_OPTIONS: ReadonlyArray<{ readonly value: WindowType; readonly label: string }> = [
  { value: 'SLIDING', label: WINDOW_TYPE_LABELS.SLIDING },
  { value: 'TUMBLING', label: WINDOW_TYPE_LABELS.TUMBLING },
];

const COMPARE_OP_OPTIONS: ReadonlyArray<{ readonly value: CompareOperator; readonly label: string }> = [
  { value: 'GT', label: '大于 (>)' },
  { value: 'GTE', label: '大于等于 (>=)' },
  { value: 'LT', label: '小于 (<)' },
  { value: 'LTE', label: '小于等于 (<=)' },
  { value: 'EQ', label: '等于 (=)' },
  { value: 'NE', label: '不等于 (!=)' },
];

const SLOPE_OP_OPTIONS: ReadonlyArray<{ readonly value: CompareOperator; readonly label: string }> = [
  { value: 'GT', label: '上升超过 (>)' },
  { value: 'GTE', label: '上升不少于 (>=)' },
  { value: 'LT', label: '下降超过 (<)' },
  { value: 'LTE', label: '下降不少于 (<=)' },
];

const HYSTERESIS_DIRECTIONS: ReadonlyArray<{ readonly value: HysteresisDirection; readonly label: string }> = [
  { value: 'ABOVE', label: '上限越限' },
  { value: 'BELOW', label: '下限越限' },
];

type DiagnosticReason = NonNullable<AlarmRuleCondition['reasons']>[number];

const DIAGNOSTIC_REASON_OPTIONS: ReadonlyArray<{ readonly value: DiagnosticReason; readonly label: string }> = [
  { value: 'OUT_OF_RANGE', label: '超出量程' },
  { value: 'SPIKE', label: '跳变' },
  { value: 'SENTINEL', label: '通讯异常值' },
  { value: 'FLATLINE', label: '卡住' },
  { value: 'DERIVED_INPUT', label: '计算输入不可信' },
  { value: 'LOW_CONFIDENCE', label: '温差不确定度过大' },
  { value: 'SOURCE_UNTRUSTED', label: '采集源不可信' },
  { value: 'PROBE_OFFSET', label: '供回水零点偏差' },
  { value: 'PHYSICS_WET_BULB_DRY', label: '湿球高于干球' },
  { value: 'PHYSICS_WET_BULB_CONDENSER', label: '湿球不像湿球' },
  { value: 'PHYSICS_POWER', label: '功率超过铭牌' },
  { value: 'PHYSICS_COP', label: '能效超过卡诺上限' },
  { value: 'PHYSICS_CHW_DIRECTION', label: '冷冻水供回水接反' },
  { value: 'PHYSICS_CW_DIRECTION', label: '冷却水进出水接反' },
  { value: 'PHYSICS_HEAT_BALANCE', label: '热平衡对不上' },
];

export interface EquipmentReadingOption {
  readonly deviceId: string;
  readonly deviceName: string;
  readonly points: ReadonlyArray<{ readonly pointCode: string; readonly name: string }>;
}

export function buildEquipmentOptions(equipment: SiteEquipmentIndex): EquipmentReadingOption[] {
  const pointsByDevice = new Map<string, Array<{ pointCode: string; name: string }>>();
  for (const [key, info] of equipment.pointByReading) {
    const slash = key.indexOf('/');
    if (slash <= 0) continue;
    const deviceId = key.slice(0, slash);
    const pointCode = key.slice(slash + 1);
    const list = pointsByDevice.get(deviceId) ?? [];
    list.push({ pointCode, name: info.name });
    pointsByDevice.set(deviceId, list);
  }
  const options: EquipmentReadingOption[] = [];
  for (const [deviceId, deviceName] of equipment.nameByDevice) {
    options.push({
      deviceId,
      deviceName,
      points: pointsByDevice.get(deviceId) ?? [{ pointCode: 'supply_water_temperature', name: '供水温度' }],
    });
  }
  options.sort((a, b) => a.deviceName.localeCompare(b.deviceName, 'zh-CN'));
  return options;
}

function defaultReadingFrom(
  equipmentOptions: readonly EquipmentReadingOption[],
  fallback?: AlarmRuleReading,
): AlarmRuleReading {
  if (fallback) return { ...fallback };
  const firstDevice = equipmentOptions[0];
  return {
    deviceId: firstDevice?.deviceId ?? '018f3e00-3000-7000-8000-000000000001',
    pointCode: firstDevice?.points[0]?.pointCode ?? 'supply_water_temperature',
  };
}

export function createConditionForKind(
  kind: ConditionKind,
  equipmentOptions: readonly EquipmentReadingOption[],
  baseReading?: AlarmRuleReading,
): AlarmRuleCondition {
  const reading = defaultReadingFrom(equipmentOptions, baseReading);
  const matchingDevice = equipmentOptions.find((d) => d.deviceId === reading.deviceId) ?? equipmentOptions[0];
  const altPoint = matchingDevice?.points.find((p) => p.pointCode !== reading.pointCode)?.pointCode
    ?? equipmentOptions[1]?.points[0]?.pointCode
    ?? reading.pointCode;
  const secondReading: AlarmRuleReading = altPoint !== reading.pointCode
    ? { deviceId: reading.deviceId, pointCode: altPoint }
    : defaultReadingFrom(equipmentOptions.slice(1), reading);
  switch (kind) {
    case 'COMPARE':
      return { kind: 'COMPARE', reading, operator: 'GT', value: 12 };
    case 'RANGE':
      return { kind: 'RANGE', reading, minimum: 5, maximum: 15 };
    case 'HYSTERESIS':
      return { kind: 'HYSTERESIS', reading, direction: 'ABOVE', trigger: 12, reset: 10.5 };
    case 'SLOPE':
      return { kind: 'SLOPE', reading, operator: 'GT', delta: 2.5, windowSeconds: 300 };
    case 'WINDOW_AGG':
      return {
        kind: 'WINDOW_AGG',
        reading,
        agg: 'AVG',
        windowType: 'SLIDING',
        windowSeconds: 300,
        minSamples: 3,
        operator: 'GT',
        threshold: 12,
      };
    case 'EXPRESSION':
      return {
        kind: 'EXPRESSION',
        expression: 'a - b > 3',
        inputs: { a: reading, b: secondReading },
      };
    case 'NO_DATA':
    case 'STALE':
    case 'UNCHANGED':
    case 'CHANGED':
      return { kind, reading, ageSeconds: 300 };
    case 'AND':
    case 'OR':
      return {
        kind,
        children: [
          { kind: 'COMPARE', reading, operator: 'GT', value: 12 },
          { kind: 'COMPARE', reading: secondReading, operator: 'LTE', value: 20 },
        ],
      };
    case 'REASONS':
      return { kind: 'REASONS', reading, match: 'ANY', reasons: ['OUT_OF_RANGE'] };
  }
}

function parseCompareInputValue(raw: string): number | string | boolean {
  const trimmed = raw.trim();
  if (trimmed === 'true') return true;
  if (trimmed === 'false') return false;
  if (trimmed !== '') {
    const num = Number(trimmed);
    if (Number.isFinite(num)) return num;
  }
  return raw;
}

export function preventImeEnterSubmit(event: KeyboardEvent<HTMLInputElement>) {
  if (event.key === 'Enter' && (event.nativeEvent.isComposing || event.keyCode === 229)) {
    event.preventDefault();
  }
}

function NumericInput({
  ariaLabel,
  value,
  min,
  max,
  integerOnly = false,
  placeholder,
  onCommit,
}: {
  readonly ariaLabel: string;
  readonly value: number | undefined;
  readonly min?: number;
  readonly max?: number;
  readonly integerOnly?: boolean;
  readonly placeholder?: string;
  readonly onCommit: (next: number) => void;
}) {
  const [draft, setDraft] = useState(() => (value === undefined ? '' : String(value)));
  const [focused, setFocused] = useState(false);

  useEffect(() => {
    if (!focused) {
      setDraft(value === undefined ? '' : String(value));
    }
  }, [value, focused]);

  return (
    <Input
      type="text"
      inputMode="decimal"
      aria-label={ariaLabel}
      value={draft}
      placeholder={placeholder}
      onKeyDown={preventImeEnterSubmit}
      onFocus={() => setFocused(true)}
      onChange={(e) => {
        const nextText = e.target.value;
        setDraft(nextText);
        const trimmed = nextText.trim();
        if (trimmed === '' || trimmed === '-' || trimmed === '.' || trimmed === '-.') return;
        const parsed = integerOnly ? Number.parseInt(trimmed, 10) : Number(trimmed);
        if (!Number.isFinite(parsed)) return;
        let clamped = parsed;
        if (min !== undefined) clamped = Math.max(min, clamped);
        if (max !== undefined) clamped = Math.min(max, clamped);
        onCommit(clamped);
      }}
      onBlur={() => {
        setFocused(false);
        const trimmed = draft.trim();
        const parsed = integerOnly ? Number.parseInt(trimmed, 10) : Number(trimmed);
        if (trimmed === '' || !Number.isFinite(parsed)) {
          setDraft(value === undefined ? '' : String(value));
          return;
        }
        let clamped = parsed;
        if (min !== undefined) clamped = Math.max(min, clamped);
        if (max !== undefined) clamped = Math.min(max, clamped);
        setDraft(String(clamped));
        onCommit(clamped);
      }}
    />
  );
}

function ReadingPicker({
  ariaPrefix,
  reading,
  equipmentOptions,
  onChange,
}: {
  readonly ariaPrefix: string;
  readonly reading: AlarmRuleReading;
  readonly equipmentOptions: readonly EquipmentReadingOption[];
  readonly onChange: (next: AlarmRuleReading) => void;
}) {
  const knownDeviceIds = useMemo(() => new Set(equipmentOptions.map((d) => d.deviceId)), [equipmentOptions]);
  const selectedDevice = equipmentOptions.find((d) => d.deviceId === reading.deviceId);
  const points = selectedDevice?.points ?? [];
  const knownPointCodes = new Set(points.map((p) => p.pointCode));

  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
      <div className="flex flex-col gap-1">
        <span className="text-[11px] text-muted-foreground">设备</span>
        <Select
          value={reading.deviceId}
          onValueChange={(nextDeviceId) => {
            const dev = equipmentOptions.find((d) => d.deviceId === nextDeviceId);
            onChange({
              deviceId: nextDeviceId,
              pointCode: dev?.points[0]?.pointCode ?? reading.pointCode,
            });
          }}
        >
          <SelectTrigger className="w-full" aria-label={`${ariaPrefix}设备`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {!knownDeviceIds.has(reading.deviceId) ? (
              <SelectItem value={reading.deviceId}>当前配置设备</SelectItem>
            ) : null}
            {equipmentOptions.map((dev) => (
              <SelectItem key={dev.deviceId} value={dev.deviceId}>{dev.deviceName}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="flex flex-col gap-1">
        <span className="text-[11px] text-muted-foreground">监测点位</span>
        {points.length > 0 && knownPointCodes.has(reading.pointCode) ? (
          <Select
            value={reading.pointCode}
            onValueChange={(nextPointCode) => onChange({ ...reading, pointCode: nextPointCode })}
          >
            <SelectTrigger className="w-full" aria-label={`${ariaPrefix}监测点位`}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {points.map((pt) => (
                <SelectItem key={pt.pointCode} value={pt.pointCode}>{pt.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            aria-label={`${ariaPrefix}监测点位`}
            value={reading.pointCode}
            onKeyDown={preventImeEnterSubmit}
            onChange={(e) => onChange({ ...reading, pointCode: e.target.value })}
            placeholder="输入监测点位标识"
          />
        )}
      </div>
    </div>
  );
}

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
