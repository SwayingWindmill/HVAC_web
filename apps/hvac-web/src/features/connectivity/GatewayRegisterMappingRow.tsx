import { type HTMLAttributes, type ReactNode } from 'react';
import { Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { isOneOf } from '@/lib/typed';
import {
  MODBUS_BYTE_ORDER_LABELS,
  MODBUS_ENDIAN_OPTIONS,
  MODBUS_FUNCTION_CODE_LABELS,
  MODBUS_POLL_PRIORITIES,
  MODBUS_POLL_PRIORITY_PRESENTATION,
  MODBUS_RAW_TYPES,
  MODBUS_RAW_TYPE_LABELS,
  MODBUS_READ_FUNCTION_CODES,
  MODBUS_WORD_ORDER_LABELS,
  MODBUS_WRITE_FUNCTION_CODE_LABELS,
  defaultRegisterCountForRawType,
  type GatewayModbusRegisterDraft,
} from './gateway-config-shadow-model';

export interface SelectOption {
  readonly value: string;
  readonly label: string;
}

export function mapLabeledOptions<T extends string>(
  values: readonly T[],
  labels: Record<T, string>,
): readonly SelectOption[] {
  return values.map((value) => ({ value, label: labels[value] }));
}

const READ_FUNCTION_CODE_OPTIONS: readonly SelectOption[] = [
  { value: '3', label: MODBUS_FUNCTION_CODE_LABELS[3] },
  { value: '4', label: MODBUS_FUNCTION_CODE_LABELS[4] },
];
const POLL_PRIORITY_OPTIONS: readonly SelectOption[] = MODBUS_POLL_PRIORITIES.map((value) => ({
  value,
  label: MODBUS_POLL_PRIORITY_PRESENTATION[value].label,
}));
const RAW_TYPE_OPTIONS = mapLabeledOptions(MODBUS_RAW_TYPES, MODBUS_RAW_TYPE_LABELS);
const BYTE_ORDER_OPTIONS = mapLabeledOptions(MODBUS_ENDIAN_OPTIONS, MODBUS_BYTE_ORDER_LABELS);
const WORD_ORDER_OPTIONS = mapLabeledOptions(MODBUS_ENDIAN_OPTIONS, MODBUS_WORD_ORDER_LABELS);

function FieldBox({
  htmlFor,
  label,
  size = 'xs',
  className,
  children,
}: {
  readonly htmlFor: string;
  readonly label: string;
  readonly size?: 'xs' | '2xs';
  readonly className?: string;
  readonly children: ReactNode;
}) {
  return (
    <div className={className ? `grid gap-1 ${className}` : 'grid gap-1'}>
      <Label
        htmlFor={htmlFor}
        className={size === '2xs' ? 'text-[11px] text-muted-foreground' : 'text-xs text-muted-foreground'}
      >
        {label}
      </Label>
      {children}
    </div>
  );
}

export function LabeledSelect({
  id,
  label,
  size = 'xs',
  className,
  value,
  disabled,
  options,
  onValueChange,
}: {
  readonly id: string;
  readonly label: string;
  readonly size?: 'xs' | '2xs';
  readonly className?: string;
  readonly value: string;
  readonly disabled: boolean;
  readonly options: readonly SelectOption[];
  readonly onValueChange: (value: string) => void;
}) {
  return (
    <FieldBox htmlFor={id} label={label} size={size} className={className}>
      <Select value={value} disabled={disabled} onValueChange={onValueChange}>
        <SelectTrigger id={id} className="h-8 text-xs">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </FieldBox>
  );
}

export function LabeledInput({
  id,
  label,
  size = 'xs',
  className,
  value,
  placeholder,
  disabled,
  readOnly,
  type,
  inputMode,
  min,
  max,
  tabularNums,
  onChangeValue,
}: {
  readonly id: string;
  readonly label: string;
  readonly size?: 'xs' | '2xs';
  readonly className?: string;
  readonly value: string;
  readonly placeholder?: string;
  readonly disabled?: boolean;
  readonly readOnly?: boolean;
  readonly type?: string;
  readonly inputMode?: HTMLAttributes<HTMLInputElement>['inputMode'];
  readonly min?: number;
  readonly max?: number;
  readonly tabularNums?: boolean;
  readonly onChangeValue?: (nextValue: string) => void;
}) {
  const inputClasses = [
    'h-8 font-mono text-xs',
    readOnly ? 'bg-muted text-muted-foreground' : '',
    tabularNums ? 'tabular-nums' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <FieldBox htmlFor={id} label={label} size={size} className={className}>
      <Input
        id={id}
        type={type}
        inputMode={inputMode}
        min={min}
        max={max}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        readOnly={readOnly}
        className={inputClasses}
        onChange={onChangeValue ? (event) => onChangeValue(event.currentTarget.value) : undefined}
      />
    </FieldBox>
  );
}

export function RegisterMappingRow({
  reg,
  driverIndex,
  regIndex,
  regPrefix,
  disabled,
  canDelete,
  onUpdate,
  onDelete,
}: {
  readonly reg: GatewayModbusRegisterDraft;
  readonly driverIndex: number;
  readonly regIndex: number;
  readonly regPrefix: string;
  readonly disabled: boolean;
  readonly canDelete: boolean;
  readonly onUpdate: (patch: Partial<GatewayModbusRegisterDraft>) => void;
  readonly onDelete: () => void;
}) {
  const isReadOnlyFc4 = reg.functionCode === '4';
  const allowFc6 = reg.functionCode === '3' && reg.registerCount === '1';
  const writeValue = isReadOnlyFc4 ? 'NONE' : reg.writeFunctionCode || 'NONE';
  const writeOptions: readonly SelectOption[] = [
    { value: 'NONE', label: isReadOnlyFc4 ? '仅读取（输入寄存器只读）' : '仅读取（不写）' },
    ...(allowFc6 ? [{ value: '6', label: MODBUS_WRITE_FUNCTION_CODE_LABELS['6'] }] : []),
    ...(!isReadOnlyFc4 ? [{ value: '16', label: MODBUS_WRITE_FUNCTION_CODE_LABELS['16'] }] : []),
  ];

  return (
    <div
      role="group"
      aria-label={`寄存器 ${regIndex + 1}`}
      className="flex flex-col gap-2 rounded-md border bg-background p-2.5"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] font-medium text-muted-foreground">
          寄存器 #{regIndex + 1}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={`删除寄存器 ${driverIndex + 1}-${regIndex + 1}`}
          disabled={disabled || !canDelete}
          onClick={onDelete}
        >
          <Trash2 className="size-3.5" aria-hidden="true" />
        </Button>
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
        <LabeledInput
          id={`${regPrefix}-point`}
          label="点位编码"
          size="2xs"
          value={reg.pointCode}
          placeholder="chw_supply_temp"
          disabled={disabled}
          onChangeValue={(pointCode) => onUpdate({ pointCode })}
        />
        <LabeledSelect
          id={`${regPrefix}-fc`}
          label="读功能码"
          size="2xs"
          value={reg.functionCode}
          disabled={disabled}
          options={READ_FUNCTION_CODE_OPTIONS}
          onValueChange={(value) => {
            if (!isOneOf(MODBUS_READ_FUNCTION_CODES, value)) return;
            onUpdate(
              value === '4'
                ? { functionCode: '4', writeFunctionCode: '' }
                : { functionCode: '3' },
            );
          }}
        />
        <LabeledSelect
          id={`${regPrefix}-wfc`}
          label="写功能码（可选）"
          size="2xs"
          value={writeValue}
          disabled={disabled || isReadOnlyFc4}
          options={writeOptions}
          onValueChange={(value) => {
            if (value === 'NONE') {
              onUpdate({ writeFunctionCode: '' });
            } else if (value === '6' || value === '16') {
              onUpdate({ writeFunctionCode: value });
            }
          }}
        />
        <LabeledSelect
          id={`${regPrefix}-priority`}
          label="轮询优先级"
          size="2xs"
          value={reg.pollPriority}
          disabled={disabled}
          options={POLL_PRIORITY_OPTIONS}
          onValueChange={(value) => {
            if (isOneOf(MODBUS_POLL_PRIORITIES, value)) {
              onUpdate({ pollPriority: value });
            }
          }}
        />
      </div>

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
        <LabeledInput
          id={`${regPrefix}-addr`}
          label="寄存器地址"
          size="2xs"
          type="number"
          min={0}
          max={65535}
          value={reg.address}
          disabled={disabled}
          tabularNums
          onChangeValue={(address) => onUpdate({ address })}
        />
        <LabeledInput
          id={`${regPrefix}-count`}
          label="寄存器数量"
          size="2xs"
          type="number"
          value={reg.registerCount}
          readOnly
          tabularNums
        />
        <LabeledSelect
          id={`${regPrefix}-raw`}
          label="原始类型"
          size="2xs"
          value={reg.rawType}
          disabled={disabled}
          options={RAW_TYPE_OPTIONS}
          onValueChange={(value) => {
            if (!isOneOf(MODBUS_RAW_TYPES, value)) return;
            const nextCount = defaultRegisterCountForRawType(value);
            onUpdate({
              rawType: value,
              registerCount: nextCount,
              writeFunctionCode:
                nextCount === '2' && reg.writeFunctionCode === '6' ? '' : reg.writeFunctionCode,
              bitIndex: value === 'BIT_BOOL' ? reg.bitIndex || '0' : '',
              enumMapText: value === 'ENUM16' ? reg.enumMapText : '',
            });
          }}
        />
        <LabeledInput
          id={`${regPrefix}-scale`}
          label="倍率"
          size="2xs"
          inputMode="decimal"
          value={reg.scale}
          disabled={disabled}
          tabularNums
          onChangeValue={(scale) => onUpdate({ scale })}
        />
        <LabeledInput
          id={`${regPrefix}-offset`}
          label="偏置"
          size="2xs"
          inputMode="decimal"
          value={reg.offset}
          disabled={disabled}
          tabularNums
          onChangeValue={(offset) => onUpdate({ offset })}
        />
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
        <LabeledSelect
          id={`${regPrefix}-byte-order`}
          label="字节序"
          size="2xs"
          value={reg.byteOrder}
          disabled={disabled}
          options={BYTE_ORDER_OPTIONS}
          onValueChange={(value) => {
            if (isOneOf(MODBUS_ENDIAN_OPTIONS, value)) {
              onUpdate({ byteOrder: value });
            }
          }}
        />
        <LabeledSelect
          id={`${regPrefix}-word-order`}
          label="字序"
          size="2xs"
          value={reg.wordOrder}
          disabled={disabled}
          options={WORD_ORDER_OPTIONS}
          onValueChange={(value) => {
            if (isOneOf(MODBUS_ENDIAN_OPTIONS, value)) {
              onUpdate({ wordOrder: value });
            }
          }}
        />
        <LabeledInput
          id={`${regPrefix}-semantic`}
          label="语义通道（可选）"
          size="2xs"
          value={reg.semanticChannel}
          placeholder="chw.supply_temp"
          disabled={disabled}
          onChangeValue={(semanticChannel) => onUpdate({ semanticChannel })}
        />
        {reg.rawType === 'BIT_BOOL' ? (
          <LabeledInput
            id={`${regPrefix}-bit`}
            label="位索引 (0 至 15)"
            size="2xs"
            type="number"
            min={0}
            max={15}
            value={reg.bitIndex}
            disabled={disabled}
            tabularNums
            onChangeValue={(bitIndex) => onUpdate({ bitIndex })}
          />
        ) : null}
        {reg.rawType === 'ENUM16' ? (
          <LabeledInput
            id={`${regPrefix}-enum-map`}
            label="枚举映射（格式：数值=标签，逗号分隔）"
            size="2xs"
            className="sm:col-span-4"
            value={reg.enumMapText}
            placeholder="0=停机, 1=运行"
            disabled={disabled}
            onChangeValue={(enumMapText) => onUpdate({ enumMapText })}
          />
        ) : null}
      </div>
    </div>
  );
}
