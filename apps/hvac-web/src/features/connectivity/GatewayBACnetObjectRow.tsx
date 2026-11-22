import { Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import { isOneOf } from '@/lib/typed';
import {
  BACNET_OBJECT_TYPES,
  BACNET_OBJECT_TYPE_LABELS,
  DRIVER_CHANNEL_DATA_TYPES,
  DRIVER_CHANNEL_DATA_TYPE_LABELS,
  MODBUS_POLL_PRIORITIES,
  MODBUS_POLL_PRIORITY_PRESENTATION,
  SAFETY_BOUNDARY_LABELS,
  SAFETY_BOUNDARY_OPTIONS,
  isBACnetInputOnlyObjectType,
  type GatewayBACnetObjectDraft,
} from './gateway-config-shadow-model';
import {
  LabeledInput,
  LabeledSelect,
  type SelectOption,
  mapLabeledOptions,
} from './GatewayRegisterMappingRow';

const AUTO_VALUE = '__AUTO__';
const UNSET_BOUNDARY_VALUE = '__UNSET__';

const OBJECT_TYPE_OPTIONS = mapLabeledOptions(BACNET_OBJECT_TYPES, BACNET_OBJECT_TYPE_LABELS);
const DATA_TYPE_OPTIONS: readonly SelectOption[] = [
  { value: AUTO_VALUE, label: '按对象类型自动推断' },
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

export function BACnetObjectMappingRow({
  obj,
  driverIndex,
  objIndex,
  objPrefix,
  disabled,
  canDelete,
  onUpdate,
  onDelete,
}: {
  readonly obj: GatewayBACnetObjectDraft;
  readonly driverIndex: number;
  readonly objIndex: number;
  readonly objPrefix: string;
  readonly disabled: boolean;
  readonly canDelete: boolean;
  readonly onUpdate: (patch: Partial<GatewayBACnetObjectDraft>) => void;
  readonly onDelete: () => void;
}) {
  const inputOnly = isBACnetInputOnlyObjectType(obj.objectType);
  const hardSafety = obj.safetyBoundary === 'HARD_REALTIME_SAFETY';
  const writeLocked = disabled || inputOnly || hardSafety;

  return (
    <div
      role="group"
      aria-label={`BACnet 对象 ${objIndex + 1}`}
      className="flex flex-col gap-2 rounded-md border bg-background p-2.5"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] font-medium text-muted-foreground">
          BACnet 对象 #{objIndex + 1}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={`删除 BACnet 对象 ${driverIndex + 1}-${objIndex + 1}`}
          disabled={disabled || !canDelete}
          onClick={onDelete}
        >
          <Trash2 className="size-3.5" aria-hidden="true" />
        </Button>
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
        <LabeledInput
          id={`${objPrefix}-point`}
          label="点位编码"
          size="2xs"
          value={obj.pointCode}
          placeholder="supply_air_temp"
          disabled={disabled}
          onChangeValue={(pointCode) => onUpdate({ pointCode })}
        />
        <LabeledSelect
          id={`${objPrefix}-type`}
          label="对象类型"
          size="2xs"
          value={obj.objectType}
          disabled={disabled}
          options={OBJECT_TYPE_OPTIONS}
          onValueChange={(value) => {
            if (!isOneOf(BACNET_OBJECT_TYPES, value)) return;
            onUpdate(
              isBACnetInputOnlyObjectType(value)
                ? { objectType: value, writable: false }
                : { objectType: value },
            );
          }}
        />
        <LabeledInput
          id={`${objPrefix}-inst`}
          label="对象实例号"
          size="2xs"
          type="number"
          min={0}
          max={4194302}
          value={obj.objectInstance}
          disabled={disabled}
          tabularNums
          onChangeValue={(objectInstance) => onUpdate({ objectInstance })}
        />
        <LabeledInput
          id={`${objPrefix}-prop`}
          label="属性 ID"
          size="2xs"
          type="number"
          min={0}
          max={4194303}
          value={obj.propertyId}
          placeholder="85"
          disabled={disabled}
          tabularNums
          onChangeValue={(propertyId) => onUpdate({ propertyId })}
        />
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
        <LabeledSelect
          id={`${objPrefix}-prio`}
          label="轮询优先级"
          size="2xs"
          value={obj.pollPriority}
          disabled={disabled}
          options={POLL_PRIORITY_OPTIONS}
          onValueChange={(value) => {
            if (isOneOf(MODBUS_POLL_PRIORITIES, value)) {
              onUpdate({ pollPriority: value });
            }
          }}
        />
        <LabeledSelect
          id={`${objPrefix}-dtype`}
          label="数据类型（可选）"
          size="2xs"
          value={obj.dataType || AUTO_VALUE}
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
          id={`${objPrefix}-safety`}
          label="安全边界"
          size="2xs"
          value={obj.safetyBoundary || UNSET_BOUNDARY_VALUE}
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
          id={`${objPrefix}-sem`}
          label="语义通道（可选）"
          size="2xs"
          value={obj.semanticChannel}
          placeholder="SupplyAirTemp"
          disabled={disabled}
          onChangeValue={(semanticChannel) => onUpdate({ semanticChannel })}
        />
      </div>

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <LabeledInput
          id={`${objPrefix}-scale`}
          label="倍率"
          size="2xs"
          inputMode="decimal"
          value={obj.scale}
          disabled={disabled}
          tabularNums
          onChangeValue={(scale) => onUpdate({ scale })}
        />
        <LabeledInput
          id={`${objPrefix}-offset`}
          label="偏置"
          size="2xs"
          inputMode="decimal"
          value={obj.offset}
          disabled={disabled}
          tabularNums
          onChangeValue={(offset) => onUpdate({ offset })}
        />
        <LabeledInput
          id={`${objPrefix}-unit`}
          label="单位（可选）"
          size="2xs"
          value={obj.unit}
          placeholder="°C"
          disabled={disabled}
          onChangeValue={(unit) => onUpdate({ unit })}
        />
        <div className="flex flex-col justify-end gap-1.5 pb-1">
          <div className="flex items-center gap-2">
            <Checkbox
              id={`${objPrefix}-writable`}
              checked={obj.writable}
              disabled={writeLocked}
              onCheckedChange={(checked) => onUpdate({ writable: checked === true })}
            />
            <Label htmlFor={`${objPrefix}-writable`} className="text-[11px] text-foreground">
              允许下发写入
            </Label>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id={`${objPrefix}-cov`}
              checked={obj.covSubscribe}
              disabled={disabled}
              onCheckedChange={(checked) => onUpdate({ covSubscribe: checked === true })}
            />
            <Label htmlFor={`${objPrefix}-cov`} className="text-[11px] text-foreground">
              启用 COV 订阅
            </Label>
          </div>
        </div>
      </div>

      <LabeledInput
        id={`${objPrefix}-enum`}
        label="枚举映射（可选，值=标签）"
        size="2xs"
        value={obj.enumMapText}
        placeholder="0=关闭, 1=开启"
        disabled={disabled}
        onChangeValue={(enumMapText) => onUpdate({ enumMapText })}
      />
    </div>
  );
}
