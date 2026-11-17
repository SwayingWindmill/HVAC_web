import { useId } from 'react';
import { Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import {
  AUTONOMY_POLICY_BOUNDS,
  createDefaultAutonomyPolicyDraft,
  type GatewayAutonomyPolicyDraft,
} from './gateway-config-shadow-model';

type AutonomyNumericFieldKey = Exclude<keyof GatewayAutonomyPolicyDraft, 'enabled'>;

interface AutonomyNumericFieldDescriptor {
  readonly key: AutonomyNumericFieldKey;
  readonly idSuffix: string;
  readonly label: string;
  readonly unit: string;
  readonly bounds: { readonly min: number; readonly max: number };
  readonly inputMode: 'numeric' | 'decimal';
  readonly group: 'core' | 'frequency' | 'timing';
}

const AUTONOMY_NUMERIC_FIELDS: readonly AutonomyNumericFieldDescriptor[] = [
  {
    key: 'offlineGraceSeconds',
    idSuffix: 'grace',
    label: '断网宽限',
    unit: '秒',
    bounds: AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds,
    inputMode: 'numeric',
    group: 'core',
  },
  {
    key: 'defaultChilledWaterSupplySetpointC',
    idSuffix: 'setpoint',
    label: '冷冻水供水设定值',
    unit: '°C',
    bounds: AUTONOMY_POLICY_BOUNDS.defaultChilledWaterSupplySetpointC,
    inputMode: 'decimal',
    group: 'core',
  },
  {
    key: 'minPumpFrequencyHz',
    idSuffix: 'min-freq',
    label: '水泵最小频率',
    unit: 'Hz',
    bounds: AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz,
    inputMode: 'decimal',
    group: 'frequency',
  },
  {
    key: 'defaultPumpFrequencyHz',
    idSuffix: 'default-freq',
    label: '水泵默认频率',
    unit: 'Hz',
    bounds: AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz,
    inputMode: 'decimal',
    group: 'frequency',
  },
  {
    key: 'maxPumpFrequencyHz',
    idSuffix: 'max-freq',
    label: '水泵最大频率',
    unit: 'Hz',
    bounds: AUTONOMY_POLICY_BOUNDS.pumpFrequencyHz,
    inputMode: 'decimal',
    group: 'frequency',
  },
  {
    key: 'minRunSeconds',
    idSuffix: 'min-run',
    label: '最短运行保护',
    unit: '秒',
    bounds: AUTONOMY_POLICY_BOUNDS.minRunSeconds,
    inputMode: 'numeric',
    group: 'timing',
  },
  {
    key: 'minStopSeconds',
    idSuffix: 'min-stop',
    label: '最短停机保护',
    unit: '秒',
    bounds: AUTONOMY_POLICY_BOUNDS.minStopSeconds,
    inputMode: 'numeric',
    group: 'timing',
  },
  {
    key: 'snapshotIntervalSeconds',
    idSuffix: 'snapshot',
    label: '全量快照周期',
    unit: '秒',
    bounds: AUTONOMY_POLICY_BOUNDS.snapshotIntervalSeconds,
    inputMode: 'numeric',
    group: 'timing',
  },
];

const CORE_FIELDS = AUTONOMY_NUMERIC_FIELDS.filter((field) => field.group === 'core');
const FREQUENCY_FIELDS = AUTONOMY_NUMERIC_FIELDS.filter((field) => field.group === 'frequency');
const TIMING_FIELDS = AUTONOMY_NUMERIC_FIELDS.filter((field) => field.group === 'timing');

function patchAutonomyNumericField(
  policy: GatewayAutonomyPolicyDraft,
  key: AutonomyNumericFieldKey,
  value: string,
): GatewayAutonomyPolicyDraft {
  switch (key) {
    case 'offlineGraceSeconds':
      return { ...policy, offlineGraceSeconds: value };
    case 'defaultChilledWaterSupplySetpointC':
      return { ...policy, defaultChilledWaterSupplySetpointC: value };
    case 'minPumpFrequencyHz':
      return { ...policy, minPumpFrequencyHz: value };
    case 'defaultPumpFrequencyHz':
      return { ...policy, defaultPumpFrequencyHz: value };
    case 'maxPumpFrequencyHz':
      return { ...policy, maxPumpFrequencyHz: value };
    case 'minRunSeconds':
      return { ...policy, minRunSeconds: value };
    case 'minStopSeconds':
      return { ...policy, minStopSeconds: value };
    case 'snapshotIntervalSeconds':
      return { ...policy, snapshotIntervalSeconds: value };
  }
}

export function AutonomyPolicyEditorSection({
  autonomyPolicy,
  disabled,
  onChange,
}: {
  readonly autonomyPolicy: GatewayAutonomyPolicyDraft | null;
  readonly disabled: boolean;
  readonly onChange: (next: GatewayAutonomyPolicyDraft | null) => void;
}) {
  const baseId = useId();

  const renderFieldGroup = (fields: readonly AutonomyNumericFieldDescriptor[], columnsClass: string) => {
    if (!autonomyPolicy) return null;
    return (
      <div className={columnsClass}>
        {fields.map((field) => {
          const inputId = `${baseId}-${field.idSuffix}`;
          return (
            <div key={field.key} className="grid gap-1">
              <Label htmlFor={inputId} className="text-xs text-muted-foreground">
                {field.label} ({field.unit}，{field.bounds.min} 至 {field.bounds.max})
              </Label>
              <Input
                id={inputId}
                type={field.inputMode === 'numeric' ? 'number' : undefined}
                inputMode={field.inputMode === 'decimal' ? 'decimal' : undefined}
                min={field.inputMode === 'numeric' ? field.bounds.min : undefined}
                max={field.inputMode === 'numeric' ? field.bounds.max : undefined}
                value={autonomyPolicy[field.key]}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) =>
                  onChange(patchAutonomyNumericField(autonomyPolicy, field.key, event.currentTarget.value))
                }
              />
            </div>
          );
        })}
      </div>
    );
  };

  return (
    <section aria-label="断网自治策略设置" className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h4 className="text-xs font-semibold text-foreground">断网自治策略</h4>
        {autonomyPolicy ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => onChange(null)}
          >
            清除自治策略
          </Button>
        ) : (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => onChange(createDefaultAutonomyPolicyDraft())}
          >
            <Plus data-icon="inline-start" aria-hidden="true" />
            配置断网自治策略
          </Button>
        )}
      </div>

      {!autonomyPolicy ? (
        <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
          当前处于未配置状态；点击「配置断网自治策略」可设置断网宽限、默认冷冻水供水温度与水泵频率安全边界。
        </p>
      ) : (
        <div className="flex flex-col gap-3 rounded-lg border bg-muted/30 p-3">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-divider pb-2.5">
            <div className="flex items-center gap-2.5">
              <Switch
                id={`${baseId}-enabled`}
                checked={autonomyPolicy.enabled}
                disabled={disabled}
                onCheckedChange={(checked) => onChange({ ...autonomyPolicy, enabled: checked })}
              />
              <Label htmlFor={`${baseId}-enabled`} className="cursor-pointer text-xs font-medium">
                启用断网本地自治
              </Label>
            </div>
            <span
              className={cn(
                'inline-flex h-5.5 items-center rounded-md px-1.5 text-[11px] font-medium',
                autonomyPolicy.enabled
                  ? 'bg-success-soft text-success-text'
                  : 'bg-secondary text-secondary-foreground',
              )}
            >
              {autonomyPolicy.enabled ? '已启用本地自治' : '仅云端受控'}
            </span>
          </div>

          {renderFieldGroup(CORE_FIELDS, 'grid grid-cols-1 gap-2.5 sm:grid-cols-2')}
          {renderFieldGroup(FREQUENCY_FIELDS, 'grid grid-cols-1 gap-2.5 sm:grid-cols-3')}
          {renderFieldGroup(TIMING_FIELDS, 'grid grid-cols-1 gap-2.5 sm:grid-cols-3')}
        </div>
      )}
    </section>
  );
}
