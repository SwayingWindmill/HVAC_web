import type {
  Asset,
  Capability,
  Device,
  Site,
  SiteAssetModel,
  Space as RegistrySpace,
  TelemetryPoint,
} from '@/api/generated/platformGateway.gen';
import { formatTelemetryUnit } from '@/domain/centralPlantTelemetry';
import type { ClimateZone } from '@/lib/climate-zone';
import type { RegistryAdminResourceType } from './model';
import { tagMapToEntries, type TagEntryDraft } from './tags-model';

export type ResourceRowBase = {
  key: string;
  id: string;
  siteId?: string;
  code: string;
  displayName: string;
  subtype: string;
  status: string;
  revision: number;
  tags?: Record<string, string>;
};

export type ResourceRow =
  | (ResourceRowBase & { type: 'SITE'; entity: Site })
  | (ResourceRowBase & { type: 'SPACE'; entity: RegistrySpace })
  | (ResourceRowBase & { type: 'ASSET'; entity: Asset })
  | (ResourceRowBase & { type: 'DEVICE'; entity: Device })
  | (ResourceRowBase & { type: 'POINT'; entity: TelemetryPoint });

export type EditTarget =
  | { type: 'SITE'; entity?: Site }
  | { type: 'SPACE'; entity?: RegistrySpace }
  | { type: 'ASSET'; entity?: Asset }
  | { type: 'DEVICE'; entity?: Device }
  | { type: 'POINT'; entity?: TelemetryPoint };

export function newEditTarget(type: RegistryAdminResourceType): EditTarget {
  return { type };
}

export function editTargetFromRow(row: ResourceRow): EditTarget {
  switch (row.type) {
    case 'SITE': return { type: 'SITE', entity: row.entity };
    case 'SPACE': return { type: 'SPACE', entity: row.entity };
    case 'ASSET': return { type: 'ASSET', entity: row.entity };
    case 'DEVICE': return { type: 'DEVICE', entity: row.entity };
    case 'POINT': return { type: 'POINT', entity: row.entity };
  }
}

export type EditDraft = {
  code: string;
  displayName: string;
  timezone: string;
  floorAreaM2: string;
  climateZone: ClimateZone | '';
  status: Site['status'];
  parentSpaceId: string;
  spaceType: RegistrySpace['spaceType'];
  assetType: string;
  deviceType: string;
  reportingDeviceId: string;
  sensorId: string;
  pointCode: string;
  sourceKey: string;
  pointType: TelemetryPoint['pointType'];
  valueType: TelemetryPoint['valueType'];
  unit: string;
  writable: boolean;
  sampleIntervalMs: number;
  publishIntervalMs: number;
  staleAfterMs: number;
  counterDecreaseMode: NonNullable<TelemetryPoint['counterDecreaseMode']> | '';
  counterRolloverModulus: number | null;
  tagEntries: TagEntryDraft[];
  reason: string;
};

export type RebindKind = 'DEVICE_ASSET' | 'ASSET_SPACE' | 'DEVICE_SPACE' | 'POINT_SUBJECT';

export type RebindDraft = {
  kind: RebindKind;
  sourceId: string;
  targetId: string;
  targetType: 'SITE' | 'SPACE' | 'ASSET';
  role: string;
  reason: string;
};

export const WRITE_CAPABILITY: Record<RegistryAdminResourceType, Capability> = {
  SITE: 'site.write',
  SPACE: 'space.write',
  ASSET: 'asset.write',
  DEVICE: 'device.write',
  POINT: 'point.write',
};

export type ResourceCopy = {
  readonly tabLabel: string;
  readonly tableTitle: string;
  readonly emptyLabel: string;
  readonly createLabel: string;
  readonly editTitle: string;
  readonly createReason: string;
  readonly updateReason: string;
  readonly createdMessage: string;
  readonly updatedMessage: string;
  readonly retireAction: string;
  readonly retireTitle: string;
  readonly retireConfirm: string;
  readonly retiredMessage: string;
};

export const RESOURCE_COPY: Readonly<Record<RegistryAdminResourceType, ResourceCopy>> = {
  SITE: {
    tabLabel: '站点',
    tableTitle: '站点',
    emptyLabel: '当前没有已登记的站点',
    createLabel: '创建站点',
    editTitle: '编辑站点',
    createReason: '创建站点',
    updateReason: '更新站点',
    createdMessage: '站点创建成功。',
    updatedMessage: '站点更新成功。',
    retireAction: '标记为退役',
    retireTitle: '退役此站点',
    retireConfirm: '确认退役',
    retiredMessage: '该站点已标记为退役。',
  },
  SPACE: {
    tabLabel: '空间',
    tableTitle: '空间',
    emptyLabel: '当前没有已登记的空间',
    createLabel: '创建空间',
    editTitle: '编辑空间',
    createReason: '创建空间',
    updateReason: '更新空间',
    createdMessage: '空间创建成功。',
    updatedMessage: '空间更新成功。',
    retireAction: '标记为退役',
    retireTitle: '退役此空间',
    retireConfirm: '确认退役',
    retiredMessage: '该空间已标记为退役。',
  },
  ASSET: {
    tabLabel: '资产',
    tableTitle: '资产',
    emptyLabel: '当前没有已登记的资产',
    createLabel: '创建资产',
    editTitle: '编辑资产',
    createReason: '创建资产',
    updateReason: '更新资产',
    createdMessage: '资产创建成功。',
    updatedMessage: '资产更新成功。',
    retireAction: '标记为退役',
    retireTitle: '退役此资产',
    retireConfirm: '确认退役',
    retiredMessage: '该资产已标记为退役。',
  },
  DEVICE: {
    tabLabel: '设备',
    tableTitle: '设备',
    emptyLabel: '当前没有已登记的设备',
    createLabel: '创建设备',
    editTitle: '编辑设备',
    createReason: '创建设备',
    updateReason: '更新设备',
    createdMessage: '设备创建成功。',
    updatedMessage: '设备更新成功。',
    retireAction: '标记为退役',
    retireTitle: '退役此设备',
    retireConfirm: '确认退役',
    retiredMessage: '该设备已标记为退役。',
  },
  POINT: {
    tabLabel: '点位',
    tableTitle: '点位',
    emptyLabel: '当前没有已登记的点位',
    createLabel: '创建点位',
    editTitle: '编辑点位',
    createReason: '创建点位',
    updateReason: '更新点位',
    createdMessage: '点位创建成功。',
    updatedMessage: '点位更新成功。',
    retireAction: '标记为退役',
    retireTitle: '退役此点位',
    retireConfirm: '确认退役',
    retiredMessage: '该点位已标记为退役。',
  },
};

export const RESOURCE_TYPES = ['SITE', 'SPACE', 'ASSET', 'DEVICE', 'POINT'] as const satisfies readonly RegistryAdminResourceType[];
export const CLIMATE_ZONES = ['SEVERE_COLD', 'COLD', 'HOT_SUMMER_COLD_WINTER', 'HOT_SUMMER_WARM_WINTER', 'MILD'] as const satisfies readonly ClimateZone[];
export const SPACE_TYPES = ['CAMPUS', 'BUILDING', 'FLOOR', 'ZONE', 'ROOM', 'PLANT_ROOM', 'ROOFTOP', 'OUTDOOR', 'TENANT_SPACE', 'OTHER'] as const satisfies readonly RegistrySpace['spaceType'][];
export const POINT_TYPES = ['TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND'] as const satisfies readonly TelemetryPoint['pointType'][];
export const VALUE_TYPES = ['BOOLEAN', 'NUMBER', 'STRING', 'JSON'] as const satisfies readonly TelemetryPoint['valueType'][];
export const COUNTER_DECREASE_MODES = ['RESET_TO_ZERO', 'ROLLOVER', 'INVALID'] as const satisfies readonly NonNullable<TelemetryPoint['counterDecreaseMode']>[];
export const ENTITY_STATUSES = ['ACTIVE', 'INACTIVE'] as const satisfies readonly Site['status'][];
export const REBIND_KINDS = ['DEVICE_ASSET', 'ASSET_SPACE', 'DEVICE_SPACE', 'POINT_SUBJECT'] as const satisfies readonly RebindKind[];
export const POINT_SUBJECT_TYPES = ['SITE', 'SPACE', 'ASSET'] as const satisfies readonly RebindDraft['targetType'][];
export const RESOURCE_STATUS_FILTERS = ['ALL', 'ACTIVE', 'INACTIVE', 'RETIRED'] as const;
export const SCOPE_OPTIONS = ['SITE', 'ALL_SITES'] as const;
export type ResourceStatusFilter = (typeof RESOURCE_STATUS_FILTERS)[number];

export function statusLabel(status: string): string {
  if (status === 'ACTIVE') return '启用';
  if (status === 'INACTIVE') return '停用';
  if (status === 'RETIRED') return '已退役';
  return '待确认';
}

export function statusTone(status: string): 'success' | 'destructive' | 'neutral' | 'warning' {
  if (status === 'ACTIVE') return 'success';
  if (status === 'INACTIVE') return 'neutral';
  if (status === 'RETIRED') return 'destructive';
  return 'warning';
}

export function pointTypeLabel(value: string): string {
  const labels: Record<string, string> = {
    TELEMETRY: '读数',
    COUNTER: '累计量',
    STATE: '状态',
    SETTING: '设定值',
    COMMAND: '控制指令',
  };
  return labels[value] ?? value;
}

export function valueTypeLabel(value: string): string {
  const labels: Record<string, string> = {
    NUMBER: '数值',
    BOOLEAN: '开关量',
    STRING: '文本',
    JSON: '结构化数据',
  };
  return labels[value] ?? value;
}

export function rebindRoleLabel(value: string): string {
  const labels: Record<string, string> = {
    CONTROLLER: '控制器',
    METER: '电表',
    SENSOR: '传感器',
    GATEWAY: '站点网关',
    SUPERVISORY_CONTROLLER: '群控器',
    INSTALLED_IN: '安装于',
    SERVES: '服务于',
    GATEWAY_FOR: '网关转发',
    SUPERVISES: '群控管理',
    DESCRIBES: '描述测量对象',
    CONTROLS: '控制测量对象',
  };
  return labels[value] ?? value;
}

export function spaceTypeLabel(value: string): string {
  const labels: Record<string, string> = {
    CAMPUS: '园区', BUILDING: '建筑', FLOOR: '楼层', ZONE: '区域', ROOM: '房间',
    PLANT_ROOM: '机房', ROOFTOP: '屋面', OUTDOOR: '室外', TENANT_SPACE: '租户空间', OTHER: '其他',
  };
  return labels[value] ?? value;
}

export function pointSubtypeLabel(point: TelemetryPoint): string {
  const unit = formatTelemetryUnit(point.unit);
  return `${pointTypeLabel(point.pointType)} · ${valueTypeLabel(point.valueType)}${unit ? `（${unit}）` : ''}`;
}

export function assetRowsToResourceRows(assets: readonly Asset[]): ResourceRow[] {
  return assets.map((value) => ({
    key: value.id,
    id: value.id,
    siteId: value.siteId,
    type: 'ASSET',
    code: value.code,
    displayName: value.displayName,
    subtype: value.assetType,
    status: value.status,
    revision: value.revision,
    tags: value.tags,
    entity: value,
  }));
}

export function deviceRowsToResourceRows(devices: readonly Device[]): ResourceRow[] {
  return devices.map((value) => ({
    key: value.id,
    id: value.id,
    siteId: value.siteId,
    type: 'DEVICE',
    code: value.code,
    displayName: value.displayName,
    subtype: value.deviceType,
    status: value.status,
    revision: value.revision,
    tags: value.tags,
    entity: value,
  }));
}

export function entityRows(site: Site, model: SiteAssetModel, type: RegistryAdminResourceType): ResourceRow[] {
  switch (type) {
    case 'SITE':
      return [{ key: site.id, id: site.id, siteId: site.id, type, code: site.code, displayName: site.displayName, subtype: site.timezone, status: site.status, revision: site.revision, entity: site }];
    case 'SPACE':
      return model.spaces.map((value) => ({ key: value.id, id: value.id, siteId: value.siteId, type, code: value.code, displayName: value.displayName, subtype: spaceTypeLabel(value.spaceType), status: value.status, revision: value.revision, entity: value }));
    case 'ASSET':
      return assetRowsToResourceRows(model.assets);
    case 'DEVICE':
      return deviceRowsToResourceRows(model.devices);
    case 'POINT':
      return model.telemetryPoints.map((value) => ({ key: value.id, id: value.id, siteId: value.siteId, type, code: value.pointCode, displayName: value.displayName, subtype: pointSubtypeLabel(value), status: value.status, revision: value.revision, entity: value }));
  }
}

export function defaultEditDraft(target: EditTarget): EditDraft {
  const copy = RESOURCE_COPY[target.type];
  const base: EditDraft = {
    code: '', displayName: '', timezone: 'Asia/Shanghai', floorAreaM2: '', climateZone: '', status: 'ACTIVE', parentSpaceId: '', spaceType: 'OTHER',
    assetType: '', deviceType: '', reportingDeviceId: '', sensorId: '', pointCode: '', sourceKey: '', pointType: 'TELEMETRY',
    valueType: 'NUMBER', unit: '', writable: false, sampleIntervalMs: 1000, publishIntervalMs: 5000, staleAfterMs: 15000,
    counterDecreaseMode: 'RESET_TO_ZERO', counterRolloverModulus: null, tagEntries: [], reason: target.entity ? copy.updateReason : copy.createReason,
  };
  if (!target.entity) return base;
  switch (target.type) {
    case 'SITE': {
      const value = target.entity;
      return {
        ...base, code: value.code, displayName: value.displayName, timezone: value.timezone, status: value.status,
        floorAreaM2: value.floorAreaM2 === null ? '' : String(value.floorAreaM2), climateZone: value.climateZone ?? '',
      };
    }
    case 'SPACE': {
      const value = target.entity;
      return { ...base, code: value.code, displayName: value.displayName, parentSpaceId: value.parentSpaceId ?? '', spaceType: value.spaceType, status: value.status };
    }
    case 'ASSET': {
      const value = target.entity;
      return { ...base, code: value.code, displayName: value.displayName, assetType: value.assetType, status: value.status, tagEntries: tagMapToEntries(value.tags) };
    }
    case 'DEVICE': {
      const value = target.entity;
      return { ...base, code: value.code, displayName: value.displayName, deviceType: value.deviceType, status: value.status, tagEntries: tagMapToEntries(value.tags) };
    }
    case 'POINT': {
      const value = target.entity;
      return {
        ...base,
        reportingDeviceId: value.reportingDeviceId,
        sensorId: value.sensorId ?? '',
        pointCode: value.pointCode,
        sourceKey: value.sourceKey,
        displayName: value.displayName,
        pointType: value.pointType,
        valueType: value.valueType,
        unit: value.unit ?? '',
        writable: value.writable,
        sampleIntervalMs: value.sampleIntervalMs,
        publishIntervalMs: value.publishIntervalMs,
        staleAfterMs: value.staleAfterMs,
        counterDecreaseMode: value.counterDecreaseMode ?? 'RESET_TO_ZERO',
        counterRolloverModulus: value.counterRolloverModulus ?? null,
        status: value.status,
      };
    }
  }
}

export function isEditDraftValid(editTarget: EditTarget | null, editDraft: EditDraft): boolean {
  if (!editTarget || !editDraft.reason.trim()) return false;
  if (editTarget.type === 'SITE') {
    const area = editDraft.floorAreaM2.trim();
    return Boolean(editDraft.code.trim() && editDraft.displayName.trim() && editDraft.timezone.trim()) && (area === '' || Number(area) > 0);
  }
  if (editTarget.type === 'SPACE') return Boolean(editDraft.code.trim() && editDraft.displayName.trim() && editDraft.spaceType);
  if (editTarget.type === 'ASSET') return Boolean(editDraft.code.trim() && editDraft.displayName.trim() && editDraft.assetType.trim());
  if (editTarget.type === 'DEVICE') return Boolean(editDraft.code.trim() && editDraft.displayName.trim() && editDraft.deviceType.trim());
  return Boolean(
    editDraft.reportingDeviceId && editDraft.pointCode.trim() && editDraft.sourceKey.trim() && editDraft.displayName.trim()
    && editDraft.pointType && editDraft.valueType && editDraft.sampleIntervalMs >= 100
    && editDraft.publishIntervalMs >= editDraft.sampleIntervalMs && editDraft.staleAfterMs >= editDraft.publishIntervalMs
    && (editDraft.pointType !== 'COUNTER' || editDraft.counterDecreaseMode)
    && (editDraft.counterDecreaseMode !== 'ROLLOVER' || (editDraft.counterRolloverModulus ?? 0) > 0),
  );
}

export function rebindRolesForKind(kind: RebindKind): readonly string[] {
  if (kind === 'DEVICE_ASSET') return ['CONTROLLER', 'METER', 'SENSOR', 'GATEWAY', 'SUPERVISORY_CONTROLLER'];
  if (kind === 'ASSET_SPACE') return ['INSTALLED_IN', 'SERVES'];
  if (kind === 'DEVICE_SPACE') return ['INSTALLED_IN', 'SERVES', 'GATEWAY_FOR', 'SUPERVISES'];
  return ['DESCRIBES', 'CONTROLS'];
}

export function selectOptionsForBinding(kind: RebindKind, model: SiteAssetModel, site: Site, targetType: RebindDraft['targetType']) {
  const devices = model.devices.map((value) => ({ id: value.id, label: `${value.displayName} · ${value.code}` }));
  const assets = model.assets.map((value) => ({ id: value.id, label: `${value.displayName} · ${value.code}` }));
  const spaces = model.spaces.map((value) => ({ id: value.id, label: `${value.displayName} · ${value.code}` }));
  const points = model.telemetryPoints.map((value) => ({ id: value.id, label: `${value.displayName} · ${value.pointCode}` }));
  if (kind === 'DEVICE_ASSET') return { sources: devices, targets: assets };
  if (kind === 'ASSET_SPACE') return { sources: assets, targets: spaces };
  if (kind === 'DEVICE_SPACE') return { sources: devices, targets: spaces };
  const targets = targetType === 'SITE'
    ? [{ id: site.id, label: site.displayName }]
    : targetType === 'SPACE' ? spaces : assets;
  return { sources: points, targets };
}
