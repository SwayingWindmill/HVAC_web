import { Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import { isOneOf } from '@/lib/typed';
import {
  DRIVER_CHANNEL_DATA_TYPES,
  DRIVER_CHANNEL_DATA_TYPE_LABELS,
  MODBUS_POLL_PRIORITIES,
  MODBUS_POLL_PRIORITY_PRESENTATION,
  SAFETY_BOUNDARY_LABELS,
  SAFETY_BOUNDARY_OPTIONS,
  type GatewayOPCUANodeDraft,
} from './gateway-config-shadow-model';
import {
  LabeledInput,
  LabeledSelect,
  type SelectOption,
  mapLabeledOptions,
} from './GatewayRegisterMappingRow';

const AUTO_VALUE = '__AUTO__';
const UNSET_BOUNDARY_VALUE = '__UNSET__';

const DATA_TYPE_OPTIONS: readonly SelectOption[] = [
  { value: AUTO_VALUE, label: '默认浮点数 (DOUBLE)' },
  ...mapLabeledOptions(DRIVER_CHANNEL_DATA_TYPES, DRIVER_CHANNEL_DATA_TYPE_LABELS),
];
const BOUNDARY_OPTIONS: readonly SelectOption[] = [
  { value: UNSET_BOUNDARY_VALUE, label: '未指定（按命名规则判定）' },
  ...mapLabeledOptions(SAFETY_BOUNDARY_OPTIONS, SAFETY_BOUNDARY_LABELS),
];
const POLL_PRIORITY_OPTIONS: readonly SelectOption[] = MODBUS_POLL_PRIORITIES.map((value) => ({
  value,
  label: MODBUS_POLL_PRIORITY_PRESENTATION[value].label,
}));

export function OPCUANodeMappingRow({
  node,
  driverIndex,
  nodeIndex,
  nodePrefix,
  disabled,
  canDelete,
  onUpdate,
  onDelete,
}: {
  readonly node: GatewayOPCUANodeDraft;
  readonly driverIndex: number;
  readonly nodeIndex: number;
  readonly nodePrefix: string;
  readonly disabled: boolean;
  readonly canDelete: boolean;
  readonly onUpdate: (patch: Partial<GatewayOPCUANodeDraft>) => void;
  readonly onDelete: () => void;
}) {
  const hardSafety = node.safetyBoundary === 'HARD_REALTIME_SAFETY';

  return (
    <div
      role="group"
      aria-label={`OPC-UA 节点 ${nodeIndex + 1}`}
      className="flex flex-col gap-2 rounded-md border bg-background p-2.5"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] font-medium text-muted-foreground">
          OPC-UA 节点 #{nodeIndex + 1}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={`删除 OPC-UA 节点 ${driverIndex + 1}-${nodeIndex + 1}`}
          disabled={disabled || !canDelete}
          onClick={onDelete}
        >
          <Trash2 className="size-3.5" aria-hidden="true" />
        </Button>
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
        <LabeledInput
          id={`${nodePrefix}-point`}
          label="点位编码"
          size="2xs"
          value={node.pointCode}
          placeholder="chw_supply_temp"
          disabled={disabled}
          onChangeValue={(pointCode) => onUpdate({ pointCode })}
        />
        <LabeledInput
          id={`${nodePrefix}-nodeid`}
          label="节点 ID (NodeId)"
          size="2xs"
          className="sm:col-span-2"
          value={node.nodeId}
          placeholder="ns=2;s=Chiller1.SupplyTemp"
          disabled={disabled}
          onChangeValue={(nodeId) => onUpdate({ nodeId })}
        />
        <LabeledInput
          id={`${nodePrefix}-ns`}
          label="命名空间索引（可选）"
          size="2xs"
          type="number"
          min={0}
          max={65535}
          value={node.namespaceIndex}
          placeholder="按节点 ID 解析"
          disabled={disabled}
          tabularNums
          onChangeValue={(namespaceIndex) => onUpdate({ namespaceIndex })}
        />
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
        <LabeledSelect
          id={`${nodePrefix}-prio`}
          label="轮询优先级"
          size="2xs"
          value={node.pollPriority}
          disabled={disabled}
          options={POLL_PRIORITY_OPTIONS}
          onValueChange={(value) => {
            if (isOneOf(MODBUS_POLL_PRIORITIES, value)) {
              onUpdate({ pollPriority: value });
            }
          }}
        />
        <LabeledSelect
          id={`${nodePrefix}-dtype`}
          label="数据类型（可选）"
          size="2xs"
          value={node.dataType || AUTO_VALUE}
          disabled={disabled}
          options={DATA_TYPE_OPTIONS}
          onValueChange={(value) => {
            if (value === AUTO_VALUE) {
              onUpdate({ dataType: '' });
            } else if (isOneOf(DRIVER_CHANNEL_DATA_TYPES, value)) {
              onUpdate({ dataType: value });
            }
          }}
        />
        <LabeledSelect
          id={`${nodePrefix}-safety`}
          label="安全边界"
          size="2xs"
          value={node.safetyBoundary || UNSET_BOUNDARY_VALUE}
          disabled={disabled}
          options={BOUNDARY_OPTIONS}
          onValueChange={(value) => {
            if (value === UNSET_BOUNDARY_VALUE) {
              onUpdate({ safetyBoundary: '' });
            } else if (isOneOf(SAFETY_BOUNDARY_OPTIONS, value)) {
              onUpdate(
                value === 'HARD_REALTIME_SAFETY'
                  ? { safetyBoundary: value, writable: false }
                  : { safetyBoundary: value },
              );
            }
          }}
        />
        <LabeledInput
          id={`${nodePrefix}-sem`}
          label="语义通道（可选）"
          size="2xs"
          value={node.semanticChannel}
          placeholder="SupplyTemp"
          disabled={disabled}
          onChangeValue={(semanticChannel) => onUpdate({ semanticChannel })}
        />
      </div>

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <LabeledInput
          id={`${nodePrefix}-scale`}
          label="倍率"
          size="2xs"
          inputMode="decimal"
          value={node.scale}
          disabled={disabled}
          tabularNums
          onChangeValue={(scale) => onUpdate({ scale })}
        />
        <LabeledInput
          id={`${nodePrefix}-offset`}
          label="偏置"
          size="2xs"
          inputMode="decimal"
          value={node.offset}
          disabled={disabled}
          tabularNums
          onChangeValue={(offset) => onUpdate({ offset })}
        />
        <LabeledInput
          id={`${nodePrefix}-unit`}
          label="单位（可选）"
          size="2xs"
          value={node.unit}
          placeholder="°C"
          disabled={disabled}
          onChangeValue={(unit) => onUpdate({ unit })}
        />
        <div className="flex flex-col justify-end gap-1.5 pb-1">
          <div className="flex items-center gap-2">
            <Checkbox
              id={`${nodePrefix}-writable`}
              checked={node.writable}
              disabled={disabled || hardSafety}
              onCheckedChange={(checked) => onUpdate({ writable: checked === true })}
            />
            <Label htmlFor={`${nodePrefix}-writable`} className="text-[11px] text-foreground">
              允许下发写入
            </Label>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id={`${nodePrefix}-sub`}
              checked={node.subscribe}
              disabled={disabled}
              onCheckedChange={(checked) => onUpdate({ subscribe: checked === true })}
            />
            <Label htmlFor={`${nodePrefix}-sub`} className="text-[11px] text-foreground">
              开启节点订阅
            </Label>
          </div>
        </div>
      </div>
    </div>
  );
}
