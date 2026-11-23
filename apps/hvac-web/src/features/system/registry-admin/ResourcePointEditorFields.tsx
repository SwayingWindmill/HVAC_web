import type { SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { isOneOf } from '@/lib/typed';
import {
  COUNTER_DECREASE_MODES,
  POINT_TYPES,
  VALUE_TYPES,
  pointTypeLabel,
  valueTypeLabel,
  type EditDraft,
} from './resource-workbench-model';

export interface ResourcePointEditorFieldsProps {
  readonly model: SiteAssetModel;
  readonly editDraft: EditDraft;
  readonly onUpdateEdit: <K extends keyof EditDraft>(key: K, value: EditDraft[K]) => void;
}

export function ResourcePointEditorFields({
  model,
  editDraft,
  onUpdateEdit,
}: ResourcePointEditorFieldsProps) {
  return (
    <>
      <label className="grid gap-1.5 text-sm">
        <span>上报设备</span>
        <Select value={editDraft.reportingDeviceId || undefined} onValueChange={(value) => onUpdateEdit('reportingDeviceId', value)}>
          <SelectTrigger><SelectValue placeholder="选择设备" /></SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {model.devices.map((device) => (
                <SelectItem key={device.id} value={device.id}>{device.displayName} · {device.code}</SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </label>

      <label className="grid gap-1.5 text-sm">
        <span>物理传感器（可选）</span>
        <Select value={editDraft.sensorId || '__none__'} onValueChange={(value) => onUpdateEdit('sensorId', value === '__none__' ? '' : value)}>
          <SelectTrigger><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="__none__">未绑定传感器</SelectItem>
              {model.sensors.map((sensor) => (
                <SelectItem key={sensor.id} value={sensor.id}>{sensor.displayName} · {sensor.code}</SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </label>

      <div className="grid gap-4 sm:grid-cols-2">
        <label className="grid gap-1.5 text-sm">
          <span>点位编码</span>
          <Input
            value={editDraft.pointCode}
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
            onChange={(event) => onUpdateEdit('pointCode', event.target.value)}
          />
        </label>
        <label className="grid gap-1.5 text-sm">
          <span>来源键</span>
          <Input
            value={editDraft.sourceKey}
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
            onChange={(event) => onUpdateEdit('sourceKey', event.target.value)}
          />
        </label>
      </div>

      <label className="grid gap-1.5 text-sm">
        <span>名称</span>
        <Input value={editDraft.displayName} onChange={(event) => onUpdateEdit('displayName', event.target.value)} />
      </label>

      <div className="grid gap-4 sm:grid-cols-2">
        <label className="grid gap-1.5 text-sm">
          <span>点位类型</span>
          <Select
            value={editDraft.pointType}
            onValueChange={(value) => {
              if (isOneOf(POINT_TYPES, value)) onUpdateEdit('pointType', value);
            }}
          >
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {POINT_TYPES.map((value) => (
                  <SelectItem key={value} value={value}>{pointTypeLabel(value)}</SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </label>
        <label className="grid gap-1.5 text-sm">
          <span>值类型</span>
          <Select
            disabled={editDraft.pointType === 'COUNTER'}
            value={editDraft.valueType}
            onValueChange={(value) => {
              if (isOneOf(VALUE_TYPES, value)) onUpdateEdit('valueType', value);
            }}
          >
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {VALUE_TYPES.map((value) => (
                  <SelectItem key={value} value={value}>{valueTypeLabel(value)}</SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </label>
      </div>

      {editDraft.pointType === 'COUNTER' ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="grid gap-1.5 text-sm">
            <span>累计量下降处理</span>
            <Select
              value={editDraft.counterDecreaseMode}
              onValueChange={(value) => {
                if (isOneOf(COUNTER_DECREASE_MODES, value)) onUpdateEdit('counterDecreaseMode', value);
              }}
            >
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="RESET_TO_ZERO">复位归零</SelectItem>
                  <SelectItem value="ROLLOVER">量程回绕</SelectItem>
                  <SelectItem value="INVALID">视为无效</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </label>
          <label className="grid gap-1.5 text-sm">
            <span>回绕模数</span>
            <Input
              type="number"
              disabled={editDraft.counterDecreaseMode !== 'ROLLOVER'}
              min={0}
              value={editDraft.counterRolloverModulus ?? ''}
              onChange={(event) => onUpdateEdit('counterRolloverModulus', event.target.value === '' ? null : Number(event.target.value))}
            />
          </label>
        </div>
      ) : null}

      <label className="grid gap-1.5 text-sm">
        <span>单位（可选）</span>
        <Input
          value={editDraft.unit}
          autoCorrect="off"
          autoCapitalize="none"
          spellCheck={false}
          onChange={(event) => onUpdateEdit('unit', event.target.value)}
        />
      </label>

      <div className="grid gap-4 sm:grid-cols-3">
        <label className="grid gap-1.5 text-sm">
          <span>采样间隔 ms</span>
          <Input type="number" min={100} value={editDraft.sampleIntervalMs} onChange={(event) => onUpdateEdit('sampleIntervalMs', Number(event.target.value))} />
        </label>
        <label className="grid gap-1.5 text-sm">
          <span>发布间隔 ms</span>
          <Input type="number" min={100} value={editDraft.publishIntervalMs} onChange={(event) => onUpdateEdit('publishIntervalMs', Number(event.target.value))} />
        </label>
        <label className="grid gap-1.5 text-sm">
          <span>陈旧阈值 ms</span>
          <Input type="number" min={100} value={editDraft.staleAfterMs} onChange={(event) => onUpdateEdit('staleAfterMs', Number(event.target.value))} />
        </label>
      </div>

      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={editDraft.writable}
          disabled={editDraft.pointType !== 'SETTING'}
          onChange={(event) => onUpdateEdit('writable', event.target.checked)}
        />
        <span>允许作为设定值写入</span>
      </label>
    </>
  );
}
