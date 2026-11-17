import { useId } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  createEmptyPointMappingDraft,
  createEmptySamplingPolicyDraft,
  type GatewayPointMappingDraft,
  type GatewaySamplingPolicyDraft,
} from './gateway-config-shadow-model';

export function PointMappingsEditorSection({
  pointMappings,
  disabled,
  onChange,
}: {
  readonly pointMappings: readonly GatewayPointMappingDraft[];
  readonly disabled: boolean;
  readonly onChange: (next: readonly GatewayPointMappingDraft[]) => void;
}) {
  const baseId = useId();
  const updateRow = (draftId: string, patch: Partial<GatewayPointMappingDraft>) => {
    onChange(pointMappings.map((item) => (item.draftId === draftId ? { ...item, ...patch } : item)));
  };

  return (
    <section aria-label="点位映射设置" className="flex flex-col gap-2.5">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-semibold text-foreground">
          点位映射（{pointMappings.length} 条）
        </h4>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled}
          onClick={() => onChange([...pointMappings, createEmptyPointMappingDraft()])}
        >
          <Plus data-icon="inline-start" aria-hidden="true" />
          新增点位映射
        </Button>
      </div>
      {pointMappings.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
          暂无点位映射条目；点击「新增点位映射」添加边缘通道到标准设备点位的映射。
        </p>
      ) : (
        <div className="flex flex-col gap-2.5">
          {pointMappings.map((row, index) => {
            const rowPrefix = `${baseId}-${row.draftId}`;
            return (
              <div key={row.draftId} className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-muted-foreground">映射 #{index + 1}</span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    aria-label={`删除点位映射 ${index + 1}`}
                    disabled={disabled}
                    onClick={() => onChange(pointMappings.filter((item) => item.draftId !== row.draftId))}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                  </Button>
                </div>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                  <div className="grid gap-1">
                    <Label htmlFor={`${rowPrefix}-src`} className="text-xs text-muted-foreground">
                      源通道
                    </Label>
                    <Input
                      id={`${rowPrefix}-src`}
                      value={row.sourceChannel}
                      placeholder="rs485-u1:reg1"
                      disabled={disabled}
                      className="h-8 font-mono text-xs"
                      onChange={(event) => updateRow(row.draftId, { sourceChannel: event.currentTarget.value })}
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${rowPrefix}-dev`} className="text-xs text-muted-foreground">
                      主设备标识
                    </Label>
                    <Input
                      id={`${rowPrefix}-dev`}
                      value={row.primaryDeviceKey}
                      placeholder="chiller-01"
                      disabled={disabled}
                      className="h-8 font-mono text-xs"
                      onChange={(event) => updateRow(row.draftId, { primaryDeviceKey: event.currentTarget.value })}
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${rowPrefix}-pt`} className="text-xs text-muted-foreground">
                      主点位编码
                    </Label>
                    <Input
                      id={`${rowPrefix}-pt`}
                      value={row.primaryPointCode}
                      placeholder="supply_temp"
                      disabled={disabled}
                      className="h-8 font-mono text-xs"
                      onChange={(event) => updateRow(row.draftId, { primaryPointCode: event.currentTarget.value })}
                    />
                  </div>
                </div>
                <div className="grid gap-1">
                  <Label htmlFor={`${rowPrefix}-copy`} className="text-xs text-muted-foreground">
                    复用目标（可选，逗号分隔，格式 deviceKey:pointCode）
                  </Label>
                  <Input
                    id={`${rowPrefix}-copy`}
                    value={row.copyTargetsText}
                    placeholder="chiller-02:supply_temp"
                    disabled={disabled}
                    className="h-8 font-mono text-xs"
                    onChange={(event) => updateRow(row.draftId, { copyTargetsText: event.currentTarget.value })}
                  />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

export function SamplingPoliciesEditorSection({
  samplingPolicies,
  disabled,
  onChange,
}: {
  readonly samplingPolicies: readonly GatewaySamplingPolicyDraft[];
  readonly disabled: boolean;
  readonly onChange: (next: readonly GatewaySamplingPolicyDraft[]) => void;
}) {
  const baseId = useId();
  const updateRow = (draftId: string, patch: Partial<GatewaySamplingPolicyDraft>) => {
    onChange(samplingPolicies.map((item) => (item.draftId === draftId ? { ...item, ...patch } : item)));
  };

  return (
    <section aria-label="采集周期设置" className="flex flex-col gap-2.5">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-semibold text-foreground">
          采集周期（{samplingPolicies.length} 条）
        </h4>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled}
          onClick={() => onChange([...samplingPolicies, createEmptySamplingPolicyDraft()])}
        >
          <Plus data-icon="inline-start" aria-hidden="true" />
          新增采集周期
        </Button>
      </div>
      {samplingPolicies.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
          暂无采集周期规则；点击「新增采集周期」设置设备或点位的采样与上报频率。
        </p>
      ) : (
        <div className="flex flex-col gap-2.5">
          {samplingPolicies.map((row, index) => {
            const rowPrefix = `${baseId}-${row.draftId}`;
            return (
              <div key={row.draftId} className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-muted-foreground">采集规则 #{index + 1}</span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    aria-label={`删除采集周期 ${index + 1}`}
                    disabled={disabled}
                    onClick={() => onChange(samplingPolicies.filter((item) => item.draftId !== row.draftId))}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                  </Button>
                </div>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
                  <div className="grid gap-1">
                    <Label htmlFor={`${rowPrefix}-dev`} className="text-xs text-muted-foreground">
                      设备标识
                    </Label>
                    <Input
                      id={`${rowPrefix}-dev`}
                      value={row.deviceKey}
                      placeholder="chiller-01"
                      disabled={disabled}
                      className="h-8 font-mono text-xs"
                      onChange={(event) => updateRow(row.draftId, { deviceKey: event.currentTarget.value })}
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${rowPrefix}-pt`} className="text-xs text-muted-foreground">
                      点位编码（可选）
                    </Label>
                    <Input
                      id={`${rowPrefix}-pt`}
                      value={row.pointCode}
                      placeholder="全部点位"
                      disabled={disabled}
                      className="h-8 font-mono text-xs"
                      onChange={(event) => updateRow(row.draftId, { pointCode: event.currentTarget.value })}
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${rowPrefix}-sample`} className="text-xs text-muted-foreground">
                      采样间隔 (ms)
                    </Label>
                    <Input
                      id={`${rowPrefix}-sample`}
                      type="number"
                      min={100}
                      max={86400000}
                      value={row.sampleIntervalMs}
                      disabled={disabled}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(event) => updateRow(row.draftId, { sampleIntervalMs: event.currentTarget.value })}
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${rowPrefix}-pub`} className="text-xs text-muted-foreground">
                      上报间隔 (ms)
                    </Label>
                    <Input
                      id={`${rowPrefix}-pub`}
                      type="number"
                      min={100}
                      max={86400000}
                      value={row.publishIntervalMs}
                      disabled={disabled}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(event) => updateRow(row.draftId, { publishIntervalMs: event.currentTarget.value })}
                    />
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
