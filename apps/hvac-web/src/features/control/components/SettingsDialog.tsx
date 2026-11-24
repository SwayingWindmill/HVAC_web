import { Minus, Plus, Send } from 'lucide-react';
import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput, InputGroupText } from '@/components/ui/input-group';
import { formatClock, ReadingValue } from '@/features/operations/realtime/device-presentation';
import type { PlantDevice, PlantReading } from '@/features/operations/realtime/plant-model';
import { formatDecimal } from '@/lib/operator-format';
import type { ControlSetting, ControlTarget } from '../control-model';
import type { PendingCommand } from './CommandConfirmation';

export const formatRange = (setting: ControlSetting) => {
  const digits = setting.step < 1 ? 1 : 0;
  return `${formatDecimal(setting.minimum, 1)} – ${formatDecimal(setting.maximum, 1)}${setting.unit ? ` ${setting.unit}` : ''} · 步进 ${formatDecimal(setting.step, digits)}`;
};

function SettingRow({ setting, reading, onSubmit }: {
  readonly setting: ControlSetting;
  readonly reading: PlantReading | null;
  readonly onSubmit: (value: number) => void;
}) {
  const [draft, setDraft] = useState('');
  const value = draft === '' ? null : Number(draft);
  const steps = value === null ? 0 : (value - setting.minimum) / setting.step;
  const valid = value !== null && Number.isFinite(value) && value >= setting.minimum && value <= setting.maximum
    && Math.abs(steps - Math.round(steps)) < 1e-6;
  const base = value ?? (reading?.numeric ?? setting.minimum);
  const nudge = (direction: 1 | -1) => {
    const rawSteps = (base - setting.minimum) / setting.step;
    const isOnGrid = Math.abs(rawSteps - Math.round(rawSteps)) < 1e-6;
    const nextStepIndex = isOnGrid
      ? Math.round(rawSteps) + direction
      : direction > 0
        ? Math.ceil(rawSteps)
        : Math.floor(rawSteps);
    const rawNext = setting.minimum + nextStepIndex * setting.step;
    const next = Math.min(setting.maximum, Math.max(setting.minimum, Number(rawNext.toFixed(6))));
    setDraft(String(Number(next.toFixed(6))));
  };
  const submit = () => { if (valid && value !== null) onSubmit(value); };
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 border-b border-divider py-3 last:border-b-0">
      <span className="flex flex-col gap-0.5">
        <span className="text-sm font-medium">{setting.label}</span>
        <span className="text-xs text-muted-foreground">可设 {formatRange(setting)}</span>
      </span>
      <span className="text-right text-sm font-semibold"><ReadingValue reading={reading} /></span>
      <div className="col-span-2 flex items-center gap-2">
        <InputGroup className="w-48">
          <InputGroupAddon>
            <InputGroupButton size="icon-xs" aria-label="减小" onClick={() => nudge(-1)}><Minus /></InputGroupButton>
          </InputGroupAddon>
          <InputGroupInput
            inputMode="decimal"
            aria-label={`${setting.label}目标值`}
            aria-invalid={draft !== '' && !valid}
            placeholder={reading?.numeric != null ? formatDecimal(reading.numeric, setting.step < 1 ? 1 : 0) : '目标值'}
            value={draft}
            onChange={(event) => setDraft(event.target.value.trim())}
            onKeyDown={(event) => {
              if (event.key !== 'Enter') return;
              event.preventDefault();
              if (event.nativeEvent.isComposing || event.keyCode === 229) return;
              submit();
            }}
            className="text-center tabular-nums"
          />
          <InputGroupAddon align="inline-end">
            {setting.unit ? <InputGroupText>{setting.unit}</InputGroupText> : null}
            <InputGroupButton size="icon-xs" aria-label="增大" onClick={() => nudge(1)}><Plus /></InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
        <Button size="sm" variant={valid ? 'default' : 'outline'} disabled={!valid} onClick={submit}>
          <Send data-icon="inline-start" />
          下发
        </Button>
        {draft !== '' && !valid ? <span className="text-xs text-destructive-text">超出可设范围或不是步进的整数倍</span> : null}
      </div>
    </li>
  );
}

/** One asset's setpoints, each with its current reading; sending one opens the shared confirmation. */
export function SettingsDialog({ target, device, timeZone, onClose, onCommand }: {
  readonly target: ControlTarget | null;
  readonly device: PlantDevice | undefined;
  readonly timeZone: string;
  readonly onClose: () => void;
  readonly onCommand: (pending: PendingCommand) => void;
}) {
  return (
    <Dialog open={target !== null} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="sm:max-w-[560px]">
        {target ? (
          <>
            <DialogHeader>
              <DialogTitle>调整 {target.assetName} 的设定值</DialogTitle>
              <DialogDescription>读数时间 <span className="font-mono">{formatClock(device?.latestSampleAt ?? null, timeZone)}</span></DialogDescription>
            </DialogHeader>
            <ul aria-label="设定值">
              {target.settings.map((setting) => {
                const reading = setting.feedbackSourceKey && device ? device.reading(setting.feedbackSourceKey) : null;
                return (
                  <SettingRow
                    key={setting.pointId}
                    setting={setting}
                    reading={reading}
                    onSubmit={(value) => {
                      onClose();
                      onCommand({
                        target,
                        title: `调整 ${target.assetName} 的${setting.label}？`,
                        label: setting.label,
                        pointId: setting.pointId,
                        parameters: { [setting.parameterKey]: value },
                        change: { from: reading, to: value, unit: setting.unit },
                        facts: [
                          { label: '可设范围', value: formatRange(setting) },
                          { label: '读数时间', value: formatClock(reading?.sampledAt ?? null, timeZone) },
                        ],
                      });
                    }}
                  />
                );
              })}
            </ul>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
