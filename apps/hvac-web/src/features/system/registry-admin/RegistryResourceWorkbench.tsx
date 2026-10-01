import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { Link2, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import type {
  Asset,
  Capability,
  Device,
  Site,
  SiteAssetModel,
  Space as RegistrySpace,
  TelemetryPoint,
} from '@/api/generated/platformGateway.gen';
import { DataTableBlock } from '@/blocks/data-table';
import {
  flattenRegistryPages,
  presentRegistryError,
  registryAdminApi,
  useRegistryDevicePoints,
} from '@/api/registry';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { confirmDiscardRegistryDraft } from './useDirtyGuard';
import {
  makeRegistryMutationMeta,
  newRegistryIdempotencyKey,
  type RegistryAdminResourceType,
} from './model';

interface Props {
  site: Site;
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  onDirtyChange: (dirty: boolean) => void;
  onRefresh: () => Promise<void>;
  onSiteCreated: (siteId: string) => void;
}

type ResourceEntity = Site | RegistrySpace | Asset | Device | TelemetryPoint;

type ResourceRow = {
  key: string;
  id: string;
  type: RegistryAdminResourceType;
  code: string;
  displayName: string;
  subtype: string;
  status: string;
  revision: number;
  entity: ResourceEntity;
};

type EditDraft = {
  code: string;
  displayName: string;
  timezone: string;
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
  reason: string;
};

type RebindKind = 'DEVICE_ASSET' | 'ASSET_SPACE' | 'DEVICE_SPACE' | 'POINT_SUBJECT';

type RebindDraft = {
  kind: RebindKind;
  sourceId: string;
  targetId: string;
  targetType: 'SITE' | 'SPACE' | 'ASSET';
  role: string;
  reason: string;
};

const WRITE_CAPABILITY: Record<RegistryAdminResourceType, Capability> = {
  SITE: 'site.write',
  SPACE: 'space.write',
  ASSET: 'asset.write',
  DEVICE: 'device.write',
  POINT: 'point.write',
};

const RESOURCE_LABEL: Record<RegistryAdminResourceType, string> = {
  SITE: '站点',
  SPACE: '空间',
  ASSET: '设备资产',
  DEVICE: '设备端点',
  POINT: '测点',
};

const SPACE_TYPES = ['CAMPUS', 'BUILDING', 'FLOOR', 'ZONE', 'ROOM', 'PLANT_ROOM', 'ROOFTOP', 'OUTDOOR', 'TENANT_SPACE', 'OTHER'];
const POINT_TYPES = ['TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND'];
const VALUE_TYPES = ['BOOLEAN', 'NUMBER', 'STRING', 'JSON'];

function statusLabel(status: string): string {
  if (status === 'ACTIVE') return '启用';
  if (status === 'INACTIVE') return '停用';
  if (status === 'RETIRED') return '已退役';
  return '待确认';
}

function statusTone(status: string): 'success' | 'destructive' | 'neutral' | 'warning' {
  if (status === 'ACTIVE') return 'success';
  if (status === 'INACTIVE') return 'neutral';
  if (status === 'RETIRED') return 'destructive';
  return 'warning';
}

function pointTypeLabel(value: string): string {
  const labels: Record<string, string> = {
    TELEMETRY: '遥测',
    COUNTER: '累计量',
    STATE: '状态',
    SETTING: '设定值',
    COMMAND: '控制命令',
  };
  return labels[value] ?? value;
}

function spaceTypeLabel(value: string): string {
  const labels: Record<string, string> = {
    CAMPUS: '园区', BUILDING: '建筑', FLOOR: '楼层', ZONE: '区域', ROOM: '房间',
    PLANT_ROOM: '机房', ROOFTOP: '屋面', OUTDOOR: '室外', TENANT_SPACE: '租户空间', OTHER: '其他',
  };
  return labels[value] ?? value;
}

function entityRows(site: Site, model: SiteAssetModel, type: RegistryAdminResourceType): ResourceRow[] {
  switch (type) {
    case 'SITE':
      return [{ key: site.id, id: site.id, type, code: site.code, displayName: site.displayName, subtype: site.timezone, status: site.status, revision: site.revision, entity: site }];
    case 'SPACE':
      return model.spaces.map((value) => ({ key: value.id, id: value.id, type, code: value.code, displayName: value.displayName, subtype: spaceTypeLabel(value.spaceType), status: value.status, revision: value.revision, entity: value }));
    case 'ASSET':
      return model.assets.map((value) => ({ key: value.id, id: value.id, type, code: value.code, displayName: value.displayName, subtype: value.assetType, status: value.status, revision: value.revision, entity: value }));
    case 'DEVICE':
      return model.devices.map((value) => ({ key: value.id, id: value.id, type, code: value.code, displayName: value.displayName, subtype: value.deviceType, status: value.status, revision: value.revision, entity: value }));
    case 'POINT':
      return model.telemetryPoints.map((value) => ({ key: value.id, id: value.id, type, code: value.pointCode, displayName: value.displayName, subtype: `${pointTypeLabel(value.pointType)} · ${value.valueType}`, status: value.status, revision: value.revision, entity: value }));
  }
}

function defaultEditDraft(type: RegistryAdminResourceType, entity?: ResourceEntity): EditDraft {
  const base: EditDraft = {
    code: '', displayName: '', timezone: 'Asia/Shanghai', status: 'ACTIVE', parentSpaceId: '', spaceType: 'OTHER',
    assetType: '', deviceType: '', reportingDeviceId: '', sensorId: '', pointCode: '', sourceKey: '', pointType: 'TELEMETRY',
    valueType: 'NUMBER', unit: '', writable: false, sampleIntervalMs: 1000, publishIntervalMs: 5000, staleAfterMs: 15000,
    counterDecreaseMode: 'RESET_TO_ZERO', counterRolloverModulus: null, reason: entity ? `更新${RESOURCE_LABEL[type]}` : `创建${RESOURCE_LABEL[type]}`,
  };
  if (!entity) return base;
  if (type === 'SITE') {
    const value = entity as Site;
    return { ...base, code: value.code, displayName: value.displayName, timezone: value.timezone, status: value.status };
  }
  if (type === 'SPACE') {
    const value = entity as RegistrySpace;
    return { ...base, code: value.code, displayName: value.displayName, parentSpaceId: value.parentSpaceId ?? '', spaceType: value.spaceType, status: value.status };
  }
  if (type === 'ASSET') {
    const value = entity as Asset;
    return { ...base, code: value.code, displayName: value.displayName, assetType: value.assetType, status: value.status };
  }
  if (type === 'DEVICE') {
    const value = entity as Device;
    return { ...base, code: value.code, displayName: value.displayName, deviceType: value.deviceType, status: value.status };
  }
  const value = entity as TelemetryPoint;
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

function selectOptionsForBinding(kind: RebindKind, model: SiteAssetModel, site: Site, targetType: RebindDraft['targetType']) {
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

export function RegistryResourceWorkbench({ site, model, capabilities, onDirtyChange, onRefresh, onSiteCreated }: Props) {
  const [resourceType, setResourceType] = useState<RegistryAdminResourceType>('SPACE');
  const [editTarget, setEditTarget] = useState<{ type: RegistryAdminResourceType; entity?: ResourceEntity } | null>(null);
  const [editDraft, setEditDraft] = useState<EditDraft>(() => defaultEditDraft('SPACE'));
  const [retireTarget, setRetireTarget] = useState<ResourceRow | null>(null);
  const [retireReason, setRetireReason] = useState('');
  const [rebindOpen, setRebindOpen] = useState(false);
  const [rebindDraft, setRebindDraft] = useState<RebindDraft>({ kind: 'DEVICE_ASSET', sourceId: '', targetId: '', targetType: 'ASSET', role: 'CONTROLLER', reason: '调整登记关系' });
  const [editDirty, setEditDirty] = useState(false);
  const [retireDirty, setRetireDirty] = useState(false);
  const [rebindDirty, setRebindDirty] = useState(false);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [resultMessage, setResultMessage] = useState<string | null>(null);
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(null);
  const pointsQuery = useRegistryDevicePoints(selectedDeviceId);
  const devicePoints = flattenRegistryPages(pointsQuery.data);
  const dirty = editDirty || retireDirty || rebindDirty;

  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);

  const rows = useMemo(() => entityRows(site, model, resourceType), [site, model, resourceType]);

  const resourceTypeOptions = useMemo(() => [
    { key: 'SPACE' as const, label: RESOURCE_LABEL.SPACE, count: model.spaces.length },
    { key: 'ASSET' as const, label: RESOURCE_LABEL.ASSET, count: model.assets.length },
    { key: 'DEVICE' as const, label: RESOURCE_LABEL.DEVICE, count: model.devices.length },
    { key: 'POINT' as const, label: RESOURCE_LABEL.POINT, count: model.telemetryPoints.length },
    { key: 'SITE' as const, label: RESOURCE_LABEL.SITE, count: 1 },
  ], [model]);

  const writeAllowed = capabilities.has(WRITE_CAPABILITY[resourceType]);
  const retireAllowed = capabilities.has('registry.retire');
  const bindingAllowed = capabilities.has('binding.write');
  const selectedDevice = useMemo(() => model.devices.find((device) => device.id === selectedDeviceId), [model.devices, selectedDeviceId]);

  const spaceChildrenByParent = useMemo(() => {
    const map = new Map<string, RegistrySpace[]>();
    for (const space of model.spaces) {
      const key = space.parentSpaceId ?? '__root__';
      map.set(key, [...(map.get(key) ?? []), space]);
    }
    return map;
  }, [model.spaces]);

  const closeEdit = () => {
    if (!confirmDiscardRegistryDraft(editDirty)) return;
    setEditTarget(null);
    setEditDirty(false);
  };
  const closeRetire = () => {
    if (!confirmDiscardRegistryDraft(retireDirty)) return;
    setRetireTarget(null);
    setRetireReason('');
    setRetireDirty(false);
  };
  const closeRebind = () => {
    if (!confirmDiscardRegistryDraft(rebindDirty)) return;
    setRebindOpen(false);
    setRebindDirty(false);
  };

  const openEditor = useCallback((type: RegistryAdminResourceType, entity?: ResourceEntity) => {
    setError(null);
    setEditTarget({ type, entity });
    setEditDraft(defaultEditDraft(type, entity));
    setEditDirty(false);
  }, []);

  const updateEdit = <K extends keyof EditDraft>(key: K, value: EditDraft[K]) => {
    setEditDraft((current) => {
      const next = { ...current, [key]: value };
      if (key === 'pointType') {
        if (value === 'COUNTER') {
          next.valueType = 'NUMBER';
          next.writable = false;
          next.counterDecreaseMode = next.counterDecreaseMode || 'RESET_TO_ZERO';
        } else if (value === 'COMMAND') {
          next.writable = true;
          next.counterDecreaseMode = '';
          next.counterRolloverModulus = null;
        } else {
          next.writable = false;
          next.counterDecreaseMode = '';
          next.counterRolloverModulus = null;
        }
      }
      if (key === 'counterDecreaseMode' && value !== 'ROLLOVER') next.counterRolloverModulus = null;
      return next;
    });
    setEditDirty(true);
  };

  const editValid = useMemo(() => {
    if (!editTarget || !editDraft.reason.trim()) return false;
    if (editTarget.type === 'SITE') return Boolean(editDraft.code.trim() && editDraft.displayName.trim() && editDraft.timezone.trim());
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
  }, [editDraft, editTarget]);

  const runEdit = async () => {
    if (!editTarget || !editValid) return;
    const { type, entity } = editTarget;
    const expectedRevision = entity?.revision ?? 0;
    const meta = makeRegistryMutationMeta(expectedRevision, editDraft.reason.trim(), newRegistryIdempotencyKey(`${type.toLowerCase()}-${entity ? 'update' : 'create'}`));
    setWorking(true);
    setError(null);
    try {
      let createdSiteId: string | null = null;
      if (type === 'SITE') {
        const body = { code: editDraft.code.trim(), displayName: editDraft.displayName.trim(), timezone: editDraft.timezone.trim(), status: editDraft.status, meta };
        const saved = entity ? await registryAdminApi.updateSite(entity.id, body) : await registryAdminApi.createSite(body);
        createdSiteId = entity ? null : saved.id;
      } else if (type === 'SPACE') {
        const body = { parentSpaceId: editDraft.parentSpaceId || undefined, code: editDraft.code.trim(), displayName: editDraft.displayName.trim(), spaceType: editDraft.spaceType, status: editDraft.status, meta };
        if (entity) await registryAdminApi.updateSpace(site.id, entity.id, body);
        else await registryAdminApi.createSpace(site.id, body);
      } else if (type === 'ASSET') {
        const body = { code: editDraft.code.trim(), displayName: editDraft.displayName.trim(), assetType: editDraft.assetType.trim(), status: editDraft.status, meta };
        if (entity) await registryAdminApi.updateAsset(site.id, entity.id, body);
        else await registryAdminApi.createAsset(site.id, body);
      } else if (type === 'DEVICE') {
        const body = { code: editDraft.code.trim(), displayName: editDraft.displayName.trim(), deviceType: editDraft.deviceType.trim(), status: editDraft.status, meta };
        if (entity) await registryAdminApi.updateDevice(site.id, entity.id, body);
        else await registryAdminApi.createDevice(site.id, body);
      } else {
        const body = {
          reportingDeviceId: editDraft.reportingDeviceId,
          sensorId: editDraft.sensorId || undefined,
          pointCode: editDraft.pointCode.trim(),
          sourceKey: editDraft.sourceKey.trim(),
          displayName: editDraft.displayName.trim(),
          pointType: editDraft.pointType,
          valueType: editDraft.valueType,
          unit: editDraft.unit.trim() || undefined,
          writable: editDraft.writable,
          sampleIntervalMs: editDraft.sampleIntervalMs,
          publishIntervalMs: editDraft.publishIntervalMs,
          staleAfterMs: editDraft.staleAfterMs,
          counterDecreaseMode: editDraft.pointType === 'COUNTER' ? editDraft.counterDecreaseMode || undefined : undefined,
          counterRolloverModulus: editDraft.pointType === 'COUNTER' && editDraft.counterDecreaseMode === 'ROLLOVER' ? editDraft.counterRolloverModulus ?? undefined : undefined,
          sourceMetadata: entity ? (entity as TelemetryPoint).sourceMetadata : {},
          status: editDraft.status,
          meta,
        };
        if (entity) await registryAdminApi.updatePoint(site.id, entity.id, body);
        else await registryAdminApi.createPoint(site.id, body);
      }
      setResultMessage(`${RESOURCE_LABEL[type]}${entity ? '更新' : '创建'}成功。`);
      setEditTarget(null);
      setEditDirty(false);
      await onRefresh();
      if (createdSiteId) onSiteCreated(createdSiteId);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const runRetire = async () => {
    if (!retireTarget || !retireReason.trim()) return;
    setWorking(true);
    setError(null);
    try {
      const result = await registryAdminApi.retire(site.id, {
        resourceType: retireTarget.type,
        resourceId: retireTarget.id,
        meta: makeRegistryMutationMeta(retireTarget.revision, retireReason.trim(), newRegistryIdempotencyKey('registry-retire')),
      });
      setResultMessage(result.status === 'COMPLETED'
        ? `${RESOURCE_LABEL[retireTarget.type]}已完成退役。`
        : `退役被 ${result.dependencyCount} 个活动依赖阻止，原有登记信息保持不变。`);
      setRetireTarget(null);
      setRetireReason('');
      setRetireDirty(false);
      await onRefresh();
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const rebindRoles = rebindDraft.kind === 'DEVICE_ASSET'
    ? ['CONTROLLER', 'METER', 'SENSOR', 'GATEWAY', 'SUPERVISORY_CONTROLLER']
    : rebindDraft.kind === 'ASSET_SPACE'
      ? ['INSTALLED_IN', 'SERVES']
      : rebindDraft.kind === 'DEVICE_SPACE'
        ? ['INSTALLED_IN', 'SERVES', 'GATEWAY_FOR', 'SUPERVISES']
        : ['DESCRIBES', 'CONTROLS'];
  const bindingOptions = selectOptionsForBinding(rebindDraft.kind, model, site, rebindDraft.targetType);

  const updateRebind = <K extends keyof RebindDraft>(key: K, value: RebindDraft[K]) => {
    setRebindDraft((current) => {
      const next = { ...current, [key]: value };
      if (key === 'kind') {
        next.sourceId = '';
        next.targetId = '';
        next.targetType = value === 'POINT_SUBJECT' ? 'ASSET' : next.targetType;
        next.role = value === 'DEVICE_ASSET' ? 'CONTROLLER' : value === 'POINT_SUBJECT' ? 'DESCRIBES' : 'INSTALLED_IN';
      }
      if (key === 'targetType') next.targetId = '';
      return next;
    });
    setRebindDirty(true);
  };

  const runRebind = async () => {
    if (!rebindDraft.sourceId || !rebindDraft.targetId || !rebindDraft.role || !rebindDraft.reason.trim()) return;
    setWorking(true);
    setError(null);
    try {
      await registryAdminApi.rebind(site.id, {
        kind: rebindDraft.kind,
        sourceId: rebindDraft.sourceId,
        targetId: rebindDraft.targetId,
        targetType: rebindDraft.kind === 'POINT_SUBJECT' ? rebindDraft.targetType : undefined,
        role: rebindDraft.role,
        effectiveAt: new Date().toISOString(),
        meta: makeRegistryMutationMeta(0, rebindDraft.reason.trim(), newRegistryIdempotencyKey('registry-rebind')),
      });
      setResultMessage('关系调整已完成；旧关系区间已关闭，并建立新的有效关系。');
      setRebindOpen(false);
      setRebindDirty(false);
      setRebindDraft({ kind: 'DEVICE_ASSET', sourceId: '', targetId: '', targetType: 'ASSET', role: 'CONTROLLER', reason: '调整登记关系' });
      await onRefresh();
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const resourceColumns = useMemo<Array<ColumnDef<DataTableFeatures, ResourceRow>>>(() => [
    {
      id: 'name',
      header: '名称',
      cell: ({ row }) => (
        <div>
          <strong className="block text-sm">{row.original.displayName}</strong>
          <span className="font-mono text-xs text-muted-foreground">{row.original.code}</span>
        </div>
      ),
    },
    { id: 'subtype', header: '类型', cell: ({ row }) => row.original.subtype },
    {
      id: 'status',
      header: '状态',
      cell: ({ row }) => <StatusBadge tone={statusTone(row.original.status)}>{statusLabel(row.original.status)}</StatusBadge>,
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <div className="flex flex-wrap gap-1">
          <Button
            variant="ghost"
            size="sm"
            disabled={!capabilities.has(WRITE_CAPABILITY[row.original.type]) || row.original.status === 'RETIRED'}
            onClick={() => openEditor(row.original.type, row.original.entity)}
          >
            <Pencil />编辑
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive"
            disabled={!retireAllowed || row.original.status === 'RETIRED'}
            onClick={() => {
              setRetireTarget(row.original);
              setRetireReason(`退役${RESOURCE_LABEL[row.original.type]} ${row.original.displayName}`);
              setRetireDirty(false);
            }}
          >
            <Trash2 />退役
          </Button>
          {row.original.type === 'DEVICE' ? (
            <Button variant="ghost" size="sm" onClick={() => setSelectedDeviceId(row.original.id)}>
              查看测点
            </Button>
          ) : null}
        </div>
      ),
      enableSorting: false,
      enableHiding: false,
    },
  ], [capabilities, openEditor, retireAllowed]);

  const resourceTable = useDataTable({
    key: `registry-resource-${resourceType}`,
    data: [...rows],
    columns: resourceColumns,
    pageSize: 10,
    getRowId: (row) => row.key,
  });

  const pointColumns = useMemo<Array<ColumnDef<DataTableFeatures, TelemetryPoint>>>(() => [
    {
      id: 'point',
      header: '测点',
      cell: ({ row }) => (
        <div>
          <strong className="block text-sm">{row.original.displayName}</strong>
          <span className="font-mono text-xs text-muted-foreground">{row.original.pointCode}</span>
        </div>
      ),
    },
    { id: 'sourceKey', header: '来源键', cell: ({ row }) => <span className="font-mono text-xs">{row.original.sourceKey}</span> },
    { id: 'type', header: '类型', cell: ({ row }) => <>{pointTypeLabel(row.original.pointType)} · {row.original.valueType}</> },
    { id: 'writable', header: '可写', cell: ({ row }) => row.original.writable ? '是' : '否' },
  ], []);

  const pointTable = useDataTable({
    key: `registry-device-points-${selectedDeviceId ?? 'none'}`,
    data: [...devicePoints],
    columns: pointColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  const presentedError = error ? presentRegistryError(error) : null;
  const pointListError = pointsQuery.error ? presentRegistryError(pointsQuery.error) : null;

  const renderSpaceBranch = (parentId: string, depth = 0): React.ReactNode => {
    const children = spaceChildrenByParent.get(parentId) ?? [];
    return children.map((space) => (
      <div key={space.id} style={{ paddingLeft: depth * 14 }}>
        <div className="flex items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/50">
          <span className="min-w-0 truncate">{space.displayName}</span>
          <span className="shrink-0 text-xs text-muted-foreground">{spaceTypeLabel(space.spaceType)}</span>
        </div>
        {renderSpaceBranch(space.id, depth + 1)}
      </div>
    ));
  };

  return (
    <div className="space-y-4">
      {presentedError ? <Alert variant="destructive"><AlertTitle>{presentedError.title}</AlertTitle><AlertDescription>{presentedError.description}</AlertDescription></Alert> : null}
      {resultMessage ? <Alert><AlertTitle>登记已更新</AlertTitle><AlertDescription>{resultMessage}</AlertDescription></Alert> : null}

      <div className="grid gap-4 xl:grid-cols-[320px_minmax(0,1fr)]">
        <Card>
          <CardHeader><CardTitle>空间层级</CardTitle><CardDescription>按站点登记关系查看空间结构。</CardDescription></CardHeader>
          <CardContent>{model.spaces.length > 0 ? <div className="space-y-1">{renderSpaceBranch('__root__')}</div> : <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">当前站点还没有空间登记</div>}</CardContent>
        </Card>

        <DataTableBlock
          className="min-w-0"
          title="登记资源"
          description="退役资源会保留历史记录"
          controls={(
            <div className="flex flex-wrap items-center justify-between gap-2">
              <Tabs
                value={resourceType}
                onValueChange={(val) => {
                  setResourceType(val as RegistryAdminResourceType);
                  resourceTable.setPageIndex(0);
                }}
              >
                <TabsList className="h-9">
                  {resourceTypeOptions.map((option) => (
                    <TabsTrigger key={option.key} value={option.key}>
                      {option.label} {option.count}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
              <div className="flex flex-wrap gap-2">
                <Button size="sm" disabled={!writeAllowed} onClick={() => openEditor(resourceType)}><Plus />创建{RESOURCE_LABEL[resourceType]}</Button>
                <Button size="sm" variant="outline" disabled={!bindingAllowed} onClick={() => { setRebindOpen(true); setRebindDirty(false); }}><Link2 />调整关系</Button>
              </div>
            </div>
          )}
        >
          <DataTable
            table={resourceTable}
            tableAriaLabel={`${RESOURCE_LABEL[resourceType]}登记资源`}
            empty={`当前没有${RESOURCE_LABEL[resourceType]}登记`}
            getHeaderCellProps={(header) => ({
              className: header.id === 'actions' ? 'w-64' : undefined,
            })}
            footer={<DataTablePagination table={resourceTable} totalRows={rows.length} />}
          />
        </DataTableBlock>
      </div>

      {selectedDeviceId ? (
        <DataTableBlock
          title={`${selectedDevice?.displayName ?? '设备'} · 测点`}
          actions={<Button variant="ghost" size="sm" onClick={() => setSelectedDeviceId(null)}><X />关闭</Button>}
        >
          {pointListError ? <Alert variant="destructive"><AlertTitle>{pointListError.title}</AlertTitle><AlertDescription>{pointListError.description}</AlertDescription></Alert> : null}
          {pointsQuery.isLoading ? <div className="grid min-h-24 place-items-center rounded-md border text-sm text-muted-foreground">正在读取测点…</div> : (
            <DataTable
              table={pointTable}
              tableAriaLabel={`${selectedDevice?.displayName ?? '设备'}测点登记`}
              empty="当前设备没有测点登记"
              footer={<DataTablePagination table={pointTable} totalRows={devicePoints.length} />}
            />
          )}
          {pointsQuery.hasNextPage ? <div className="flex justify-center"><Button variant="outline" onClick={() => void pointsQuery.fetchNextPage()} disabled={pointsQuery.isFetchingNextPage}><RefreshCw className={pointsQuery.isFetchingNextPage ? 'animate-spin' : undefined} />加载更多</Button></div> : null}
        </DataTableBlock>
      ) : null}

      <Dialog open={Boolean(editTarget)} onOpenChange={(open) => { if (!open) closeEdit(); }}>
        <DialogContent className="max-h-[88vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader><DialogTitle>{editTarget?.entity ? '编辑' : '创建'}{editTarget ? RESOURCE_LABEL[editTarget.type] : ''}</DialogTitle><DialogDescription>保存时服务端会再次检查权限、对象状态和并发版本。</DialogDescription></DialogHeader>
          {editTarget ? (
            <div className="grid gap-4">
              {editTarget.type !== 'POINT' ? <>
                <label className="grid gap-1.5 text-sm"><span>编码</span><Input value={editDraft.code} onChange={(event) => updateEdit('code', event.target.value)} /></label>
                <label className="grid gap-1.5 text-sm"><span>名称</span><Input value={editDraft.displayName} onChange={(event) => updateEdit('displayName', event.target.value)} /></label>
              </> : null}
              {editTarget.type === 'SITE' ? <label className="grid gap-1.5 text-sm"><span>时区</span><Input value={editDraft.timezone} onChange={(event) => updateEdit('timezone', event.target.value)} /></label> : null}
              {editTarget.type === 'SPACE' ? <>
                <label className="grid gap-1.5 text-sm"><span>空间类型</span><Select value={editDraft.spaceType} onValueChange={(value) => updateEdit('spaceType', value as RegistrySpace['spaceType'])}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{SPACE_TYPES.map((value) => <SelectItem key={value} value={value}>{spaceTypeLabel(value)}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
                <label className="grid gap-1.5 text-sm"><span>上级空间</span><Select value={editDraft.parentSpaceId || '__none__'} onValueChange={(value) => updateEdit('parentSpaceId', value === '__none__' ? '' : value)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="__none__">无上级空间</SelectItem>{model.spaces.filter((space) => space.id !== editTarget.entity?.id).map((space) => <SelectItem key={space.id} value={space.id}>{space.displayName} · {space.code}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
              </> : null}
              {editTarget.type === 'ASSET' ? <label className="grid gap-1.5 text-sm"><span>资产类型</span><Input value={editDraft.assetType} onChange={(event) => updateEdit('assetType', event.target.value)} /></label> : null}
              {editTarget.type === 'DEVICE' ? <label className="grid gap-1.5 text-sm"><span>设备类型</span><Input value={editDraft.deviceType} onChange={(event) => updateEdit('deviceType', event.target.value)} /></label> : null}
              {editTarget.type === 'POINT' ? <>
                <label className="grid gap-1.5 text-sm"><span>上报设备</span><Select value={editDraft.reportingDeviceId || undefined} onValueChange={(value) => updateEdit('reportingDeviceId', value)}><SelectTrigger><SelectValue placeholder="选择设备" /></SelectTrigger><SelectContent><SelectGroup>{model.devices.map((device) => <SelectItem key={device.id} value={device.id}>{device.displayName} · {device.code}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
                <label className="grid gap-1.5 text-sm"><span>物理传感器（可选）</span><Select value={editDraft.sensorId || '__none__'} onValueChange={(value) => updateEdit('sensorId', value === '__none__' ? '' : value)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="__none__">未绑定传感器</SelectItem>{model.sensors.map((sensor) => <SelectItem key={sensor.id} value={sensor.id}>{sensor.displayName} · {sensor.code}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
                <div className="grid gap-4 sm:grid-cols-2"><label className="grid gap-1.5 text-sm"><span>测点编码</span><Input value={editDraft.pointCode} onChange={(event) => updateEdit('pointCode', event.target.value)} /></label><label className="grid gap-1.5 text-sm"><span>来源键</span><Input value={editDraft.sourceKey} onChange={(event) => updateEdit('sourceKey', event.target.value)} /></label></div>
                <label className="grid gap-1.5 text-sm"><span>名称</span><Input value={editDraft.displayName} onChange={(event) => updateEdit('displayName', event.target.value)} /></label>
                <div className="grid gap-4 sm:grid-cols-2"><label className="grid gap-1.5 text-sm"><span>测点类型</span><Select value={editDraft.pointType} onValueChange={(value) => updateEdit('pointType', value as TelemetryPoint['pointType'])}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{POINT_TYPES.map((value) => <SelectItem key={value} value={value}>{pointTypeLabel(value)}</SelectItem>)}</SelectGroup></SelectContent></Select></label><label className="grid gap-1.5 text-sm"><span>值类型</span><Select disabled={editDraft.pointType === 'COUNTER'} value={editDraft.valueType} onValueChange={(value) => updateEdit('valueType', value as TelemetryPoint['valueType'])}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{VALUE_TYPES.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectGroup></SelectContent></Select></label></div>
                {editDraft.pointType === 'COUNTER' ? <div className="grid gap-4 sm:grid-cols-2"><label className="grid gap-1.5 text-sm"><span>累计量下降处理</span><Select value={editDraft.counterDecreaseMode} onValueChange={(value) => updateEdit('counterDecreaseMode', value as NonNullable<TelemetryPoint['counterDecreaseMode']>)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="RESET_TO_ZERO">复位归零</SelectItem><SelectItem value="ROLLOVER">量程回绕</SelectItem><SelectItem value="INVALID">视为无效</SelectItem></SelectGroup></SelectContent></Select></label><label className="grid gap-1.5 text-sm"><span>回绕模数</span><Input type="number" disabled={editDraft.counterDecreaseMode !== 'ROLLOVER'} min={0} value={editDraft.counterRolloverModulus ?? ''} onChange={(event) => updateEdit('counterRolloverModulus', event.target.value === '' ? null : Number(event.target.value))} /></label></div> : null}
                <label className="grid gap-1.5 text-sm"><span>单位（可选）</span><Input value={editDraft.unit} onChange={(event) => updateEdit('unit', event.target.value)} /></label>
                <div className="grid gap-4 sm:grid-cols-3"><label className="grid gap-1.5 text-sm"><span>采样间隔 ms</span><Input type="number" min={100} value={editDraft.sampleIntervalMs} onChange={(event) => updateEdit('sampleIntervalMs', Number(event.target.value))} /></label><label className="grid gap-1.5 text-sm"><span>发布间隔 ms</span><Input type="number" min={100} value={editDraft.publishIntervalMs} onChange={(event) => updateEdit('publishIntervalMs', Number(event.target.value))} /></label><label className="grid gap-1.5 text-sm"><span>陈旧阈值 ms</span><Input type="number" min={100} value={editDraft.staleAfterMs} onChange={(event) => updateEdit('staleAfterMs', Number(event.target.value))} /></label></div>
                <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={editDraft.writable} disabled={editDraft.pointType !== 'SETTING'} onChange={(event) => updateEdit('writable', event.target.checked)} /><span>允许作为设定值写入</span></label>
              </> : null}
              <label className="grid gap-1.5 text-sm"><span>状态</span><Select value={editDraft.status} onValueChange={(value) => updateEdit('status', value as Site['status'])}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="ACTIVE">启用</SelectItem><SelectItem value="INACTIVE">停用</SelectItem></SelectGroup></SelectContent></Select></label>
              <label className="grid gap-1.5 text-sm"><span>变更原因</span><Textarea rows={2} value={editDraft.reason} onChange={(event) => updateEdit('reason', event.target.value)} /></label>
              {editTarget.entity ? <Alert><AlertTitle>并发保护已启用</AlertTitle><AlertDescription>若此对象已被其他人更新，本次保存会被服务端拒绝，不会覆盖更新后的数据。</AlertDescription></Alert> : null}
            </div>
          ) : null}
          <DialogFooter><Button variant="outline" onClick={closeEdit}>取消</Button><Button disabled={!editValid || working} onClick={() => void runEdit()}>{working ? '正在保存…' : '保存'}</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(retireTarget)} onOpenChange={(open) => { if (!open) closeRetire(); }}>
        <DialogContent>
          <DialogHeader><DialogTitle>退役{retireTarget ? RESOURCE_LABEL[retireTarget.type] : ''}</DialogTitle><DialogDescription>退役会检查活动依赖；存在依赖时不会删除登记信息。</DialogDescription></DialogHeader>
          <Alert><AlertTitle>不会执行硬删除</AlertTitle><AlertDescription>服务端会先检查依赖关系，只有满足退役条件时才会完成状态变更。</AlertDescription></Alert>
          <label className="grid gap-1.5 text-sm"><span>退役原因</span><Textarea rows={3} value={retireReason} onChange={(event) => { setRetireReason(event.target.value); setRetireDirty(true); }} /></label>
          <DialogFooter><Button variant="outline" onClick={closeRetire}>取消</Button><Button variant="destructive" disabled={!retireReason.trim() || working} onClick={() => void runRetire()}>{working ? '正在处理…' : '确认退役'}</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={rebindOpen} onOpenChange={(open) => { if (!open) closeRebind(); else setRebindOpen(true); }}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader><DialogTitle>调整登记关系</DialogTitle><DialogDescription>选择已有登记对象建立新的有效关系，不需要手工填写内部标识。</DialogDescription></DialogHeader>
          <div className="grid gap-4">
            <label className="grid gap-1.5 text-sm"><span>关系类型</span><Select value={rebindDraft.kind} onValueChange={(value) => updateRebind('kind', value as RebindKind)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="DEVICE_ASSET">设备端点 → 设备资产</SelectItem><SelectItem value="ASSET_SPACE">设备资产 → 空间</SelectItem><SelectItem value="DEVICE_SPACE">设备端点 → 空间</SelectItem><SelectItem value="POINT_SUBJECT">测点 → 业务对象</SelectItem></SelectGroup></SelectContent></Select></label>
            {rebindDraft.kind === 'POINT_SUBJECT' ? <label className="grid gap-1.5 text-sm"><span>目标对象类型</span><Select value={rebindDraft.targetType} onValueChange={(value) => updateRebind('targetType', value as RebindDraft['targetType'])}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="SITE">站点</SelectItem><SelectItem value="SPACE">空间</SelectItem><SelectItem value="ASSET">设备资产</SelectItem></SelectGroup></SelectContent></Select></label> : null}
            <label className="grid gap-1.5 text-sm"><span>来源对象</span><Select value={rebindDraft.sourceId || undefined} onValueChange={(value) => updateRebind('sourceId', value)}><SelectTrigger><SelectValue placeholder="选择来源对象" /></SelectTrigger><SelectContent><SelectGroup>{bindingOptions.sources.map((option) => <SelectItem key={option.id} value={option.id}>{option.label}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
            <label className="grid gap-1.5 text-sm"><span>目标对象</span><Select value={rebindDraft.targetId || undefined} onValueChange={(value) => updateRebind('targetId', value)}><SelectTrigger><SelectValue placeholder="选择目标对象" /></SelectTrigger><SelectContent><SelectGroup>{bindingOptions.targets.map((option) => <SelectItem key={option.id} value={option.id}>{option.label}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
            <label className="grid gap-1.5 text-sm"><span>关系角色</span><Select value={rebindDraft.role} onValueChange={(value) => updateRebind('role', value)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{rebindRoles.map((role) => <SelectItem key={role} value={role}>{role}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
            <label className="grid gap-1.5 text-sm"><span>变更原因</span><Textarea rows={2} value={rebindDraft.reason} onChange={(event) => updateRebind('reason', event.target.value)} /></label>
          </div>
          <DialogFooter><Button variant="outline" onClick={closeRebind}>取消</Button><Button disabled={working || !rebindDraft.sourceId || !rebindDraft.targetId || !rebindDraft.role || !rebindDraft.reason.trim()} onClick={() => void runRebind()}>{working ? '正在保存…' : '保存关系'}</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
