import { useEffect, useMemo, useState } from 'react';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { AlarmRuleReading } from '../alarm-rules';
import {
  preventImeEnterSubmit,
  type EquipmentReadingOption,
} from './alarm-rule-condition-options';

export function NumericInput({
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

export function ReadingPicker({
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
