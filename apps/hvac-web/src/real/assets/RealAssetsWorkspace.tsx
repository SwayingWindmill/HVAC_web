import { useEffect, useMemo, useRef, useState, type HTMLAttributes, type Key } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Badge, Button, Card, Col, Empty, Grid, Input, Row, Segmented, Select, Space, Table, Tag, Tree, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { DataNode } from 'antd/es/tree';
import {
  ApartmentOutlined,
  ApiOutlined,
  BlockOutlined,
  ClusterOutlined,
  DatabaseOutlined,
  EyeOutlined,
  NodeIndexOutlined,
  ReloadOutlined,
  RightOutlined,
  TabletOutlined,
} from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import { OperationsMetrics, OperationsPanelHeading } from '@/components/OperationsUI';
import {
  createPlatformGatewayClient,
  type CurrentPrincipalResponse,
  type PlatformGatewayClient,
  type Site,
} from '../../api/generated/platformGateway.gen';
import { S2TelemetryClientError } from '../../api/generated/s2Telemetry.gen';
import { presentRegistryError } from '../../api/registry';
import { FocusHeading } from '../FocusHeading';
import type { ProtectedScopeRequestToken, ProtectedScopeResource } from '../protected-scope';
import type { RealtimeStatusUpdate } from '../realtime-status';
import { REAL_ASSETS_CATALOG_REVISION } from './catalog';
import { AssetDetailDrawer } from './EquipmentDetailDrawer';
import { RealAssetsLoadingSurface } from './RealAssetsLoadingSurface';
import {
  REAL_ASSETS_DETAIL_HISTORY_MARKER,
  isRealAssetsDetailHistoryState,
  parseRealAssetsDetailPath,
  realAssetsAssetPath,
  realAssetsListPath,
  resolveRealAssetsDetail,
} from './detail';
import { runRealAssetsProtectedRequest } from './protected-request';
import {
  loadRealAssetsCurrentState,
  loadRealAssetsRegistry,
  realAssetsCurrentStateQueryKey,
  realAssetsRegistryQueryKey,
} from './data';
import {
  buildRealAssetsAssetRows,
  buildRealAssetsHierarchy,
  buildRealAssetsPointRows,
  buildRealAssetsRows,
  isRealAssetsAttentionState,
  realAssetsDeviceTypeLabel,
  realAssetsPointTypeLabel,
  realAssetsSensorTypeLabel,
  realAssetsTelemetryPointMeta,
  type RealAssetsDeviceRow,
  type RealAssetsAssetRow,
  type RealAssetsHierarchyNode,
  type RealAssetsOperatingState,
  type RealAssetsTelemetryPointRow,
} from './model';
import {
  createRealAssetsTelemetryRuntime,
  type RealAssetsTelemetryRuntime,
} from './telemetry-runtime';
import './real-assets.css';

interface RealAssetsWorkspaceProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  requestedAssetId?: string;
  protectedGeneration: number;
  protectedRequestToken: () => ProtectedScopeRequestToken;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  publishRealtimeStatus: (update: RealtimeStatusUpdate) => void;
  platformClient?: Pick<PlatformGatewayClient, 'getSiteAssetModel'>;
  telemetryRuntime?: RealAssetsTelemetryRuntime;
}

type ListMode = 'attention' | 'all';
type LedgerMode = 'asset' | 'devices' | 'points';
type HierarchySelection = string;

const OPERATING_LABELS: Record<RealAssetsOperatingState, string> = {
  UNKNOWN: '未知',
  OFFLINE: '离线',
  ATTENTION: '需关注',
  NORMAL: '正常',
};

const S2_DISPLAY_LABELS = {
  ONLINE: '在线',
  OFFLINE: '离线',
  STALE: '陈旧',
  UNKNOWN: '未知',
  UNAVAILABLE: '不可用',
} as const;

function matchesHierarchy(row: RealAssetsDeviceRow, selectedDeviceIds: ReadonlySet<string> | undefined): boolean {
  return selectedDeviceIds === undefined || selectedDeviceIds.has(row.device.id);
}

function assetBindings(binding: RealAssetsDeviceRow['binding']) {
  if (binding.state === 'bound') return [binding];
  if (binding.state === 'multi-bound') return binding.bindings;
  return [];
}

function assetBindingLabel(binding: RealAssetsDeviceRow['binding']): string {
  const bindings = assetBindings(binding);
  if (bindings.length > 0) return bindings.map((item) => item.asset.displayName).join('、');
  return binding.state === 'ambiguous' ? '绑定关系冲突' : '未绑定 Asset';
}

function matchesSearch(row: RealAssetsDeviceRow, value: string): boolean {
  const query = value.trim().toLocaleLowerCase('zh-CN');
  if (!query) return true;
  const space = row.space.state === 'bound' ? row.space.space : undefined;
  const asset = assetBindings(row.binding).map((item) => item.asset);
  return [
    row.device.id,
    row.device.code,
    row.device.displayName,
    row.device.deviceType,
    space?.code,
    space?.displayName,
    ...asset.flatMap((item) => [item.code, item.displayName]),
  ].some((candidate) => candidate?.toLocaleLowerCase('zh-CN').includes(query));
}

function matchesPointSearch(row: RealAssetsTelemetryPointRow, value: string): boolean {
  const query = value.trim().toLocaleLowerCase('zh-CN');
  if (!query) return true;
  const space = row.space.state === 'bound' ? row.space.space : undefined;
  const asset = assetBindings(row.binding).map((item) => item.asset);
  return [
    row.label,
    row.point.id,
    row.point.pointCode,
    row.point.sourceKey,
    row.device.code,
    row.device.displayName,
    row.sensor?.code,
    row.sensor?.displayName,
    space?.displayName,
    ...asset.flatMap((item) => [item.code, item.displayName]),
  ].some((candidate) => candidate?.toLocaleLowerCase('zh-CN').includes(query));
}

const HIERARCHY_ICONS = {
  site: <ApartmentOutlined />,
  space: <ClusterOutlined />,
  asset: <BlockOutlined />,
  device: <TabletOutlined />,
  sensor: <ApiOutlined />,
  point: <DatabaseOutlined />,
  'virtual-sensor': <ApiOutlined />,
} as const;

function hierarchyDataNode(node: RealAssetsHierarchyNode): DataNode {
  return {
    key: node.key,
    icon: HIERARCHY_ICONS[node.kind],
    title: (
      <span className="real-assets-tree-node" data-asset-kind={node.kind} title={`${node.label}｜${node.meta}`}>
        {node.label}
      </span>
    ),
    children: node.children.map(hierarchyDataNode),
  };
}

function telemetryFailure(error: unknown): { title: string; detail: string; retryable: boolean } {
  if (error instanceof S2TelemetryClientError) {
    return {
      title: error.problem.code === 'RESOURCE_NOT_FOUND' ? '设备集合已发生授权变化' : '当前设备状态暂不可用',
      detail: error.problem.detail,
      retryable: error.problem.retryable,
    };
  }
  return {
    title: '当前设备状态连接失败',
    detail: 'Registry 设备身份仍然可见，但当前状态服务无法确认。系统不会把服务故障转换为 Device UNKNOWN，也不会回退到 Demo、Legacy 或 Provider 直读。',
    retryable: true,
  };
}

function formatSampledAt(value: string, timeZone: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return '采样时间不可用';
  return `采样 ${new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(parsed)}`;
}

function pointEvidence(point: RealAssetsDeviceRow['points'][number], timeZone: string): string {
  if (point.state === 'MISSING') {
    if (point.missingReason === 'ONLY_REJECTED_CANDIDATES') return '候选观测均未被接受';
    if (point.missingReason === 'POLICY_NOT_CONFIGURED') return '遥测策略未配置';
    return '尚无已接受观测';
  }
  const freshness = point.freshness === 'STALE' ? '陈旧' : '新鲜';
  const quality = point.quality === 'GOOD' ? '良好' : point.quality ?? '未知';
  const sampledAt = point.sampledAt ? formatSampledAt(point.sampledAt, timeZone) : '采样时间不可用';
  return `${freshness} · ${quality} · ${sampledAt}`;
}

function BindingLabel({ row }: { row: RealAssetsDeviceRow }) {
  const spaceLabel = row.space.state === 'bound'
    ? row.space.space.displayName
    : row.space.state === 'ambiguous'
      ? 'Space 关系冲突'
      : '未绑定 Space';
  const assetLabel = assetBindingLabel(row.binding);
  const bindingMeta = row.binding.state === 'bound'
    ? row.binding.relationship.role
    : row.binding.state === 'multi-bound'
      ? `${row.binding.bindings.length} 个 Asset`
      : '';
  const warning = row.space.state !== 'bound' || row.binding.state === 'unbound' || row.binding.state === 'ambiguous';
  return (
    <span className={warning ? 'real-assets__binding-warning' : undefined}>
      <strong>{spaceLabel}</strong>
      <small>{assetLabel}{bindingMeta ? ` · ${bindingMeta}` : ''}</small>
    </span>
  );
}

export function RealAssetsWorkspace({
  site,
  principal,
  requestedAssetId,
  protectedGeneration,
  protectedRequestToken,
  registerProtectedResource,
  publishRealtimeStatus,
  platformClient: providedPlatformClient,
  telemetryRuntime: providedTelemetryRuntime,
}: RealAssetsWorkspaceProps) {
  const queryClient = useQueryClient();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const platformClient = useMemo(() => providedPlatformClient ?? createPlatformGatewayClient(), [providedPlatformClient]);
  const telemetryRuntime = useMemo(() => providedTelemetryRuntime ?? createRealAssetsTelemetryRuntime(), [providedTelemetryRuntime]);
  const [listMode, setListMode] = useState<ListMode>('all');
  const [ledgerMode, setLedgerMode] = useState<LedgerMode>('asset');
  const [search, setSearch] = useState('');
  const [hierarchySelection, setHierarchySelection] = useState<HierarchySelection>(`site:${site.id}`);
  const [telemetryPolicyRevision, setTelemetryPolicyRevision] = useState<string | null>(
    () => telemetryRuntime.currentRoutePolicyRevision(),
  );
  const [routePolicyEpoch, setRoutePolicyEpoch] = useState(0);
  const [selectedAssetId, setSelectedAssetId] = useState<string | null>(() => requestedAssetId ?? null);
  const selectedAssetIdRef = useRef<string | null>(selectedAssetId);
  const pendingFocusDeviceIdRef = useRef<string | null>(null);
  const deviceTriggerRefs = useRef(new Map<string, HTMLElement>());
  const tenantId = site.tenantId;
  const sessionCapability = principal.session.csrfToken;
  const capabilities = principal.authorization.capabilities;
  const registryAllowed = capabilities.includes('equipment.list') && capabilities.includes('device.list');
  const telemetryAllowed = capabilities.includes('telemetry.batch.read');
  const queryRoot = useMemo(
    () => ['real-assets', protectedGeneration, tenantId, site.id] as const,
    [tenantId, protectedGeneration, site.id],
  );

  useEffect(() => {
    selectedAssetIdRef.current = requestedAssetId ?? null;
    setSelectedAssetId(requestedAssetId ?? null);
  }, [protectedGeneration, requestedAssetId, site.id]);

  useEffect(() => {
    selectedAssetIdRef.current = selectedAssetId;
  }, [selectedAssetId]);

  useEffect(() => {
    const handlePopState = () => {
      const parsed = parseRealAssetsDetailPath(window.location.pathname, site.id);
      if (parsed.state === 'outside') {
        window.location.reload();
        return;
      }
      const previousDeviceId = selectedAssetIdRef.current;
      if (parsed.state === 'list') {
        if (previousDeviceId) pendingFocusDeviceIdRef.current = previousDeviceId;
        selectedAssetIdRef.current = null;
        setSelectedAssetId(null);
        return;
      }
      selectedAssetIdRef.current = parsed.assetId;
      setSelectedAssetId(parsed.assetId);
    };
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, [site.id]);

  useEffect(() => {
    const purgeQueryCache = async () => {
      await queryClient.cancelQueries({ queryKey: queryRoot });
      queryClient.removeQueries({ queryKey: queryRoot });
    };
    const unregister = registerProtectedResource({
      id: `real-assets-query-cache:${protectedGeneration}:${site.id}`,
      kind: 'query-cache',
      purge: purgeQueryCache,
    });
    return () => {
      unregister();
      void purgeQueryCache();
    };
  }, [protectedGeneration, queryClient, queryRoot, registerProtectedResource, site.id]);

  useEffect(() => registerProtectedResource({
    id: `real-assets-selection:${protectedGeneration}:${site.id}`,
    kind: 'selection',
    purge: () => {
      setListMode('all');
      setLedgerMode('asset');
      setSearch('');
      setHierarchySelection(`site:${site.id}`);
      selectedAssetIdRef.current = null;
      setSelectedAssetId(null);
      },
  }), [protectedGeneration, registerProtectedResource, site.id]);

  useEffect(() => {
    let active = true;
    const unsubscribe = telemetryRuntime.subscribeRoutePolicyChange((_previousRevision, nextRevision) => {
      telemetryRuntime.live.purge();
      void queryClient.cancelQueries({ queryKey: queryRoot }).then(() => {
        if (!active) return;
        queryClient.removeQueries({ queryKey: queryRoot });
        setTelemetryPolicyRevision(nextRevision);
        setRoutePolicyEpoch((currentEpoch) => currentEpoch + 1);
      });
    });
    return () => {
      active = false;
      unsubscribe();
    };
  }, [queryClient, queryRoot, telemetryRuntime]);

  const registry = useQuery({
    queryKey: realAssetsRegistryQueryKey(protectedGeneration, tenantId, site.id, routePolicyEpoch),
    queryFn: ({ signal }) => {
      const scopeGuard = protectedRequestToken();
      if (scopeGuard.siteId !== site.id || scopeGuard.generation !== protectedGeneration) {
        throw new DOMException('Protected Site scope is not current.', 'AbortError');
      }
      return runRealAssetsProtectedRequest(scopeGuard, signal, (protectedSignal) => loadRealAssetsRegistry({
        client: platformClient,
        tenantId,
        siteId: site.id,
        signal: protectedSignal,
      }));
    },
    enabled: registryAllowed,
    staleTime: 60_000,
    retry: 1,
  });
  const assetModel = registry.data?.assetModel;
  const devices = assetModel?.devices ?? [];
  const telemetryPoints = assetModel?.telemetryPoints ?? [];
  const current = useQuery({
    queryKey: realAssetsCurrentStateQueryKey(protectedGeneration, tenantId, site.id, devices, telemetryPoints, routePolicyEpoch),
    queryFn: ({ signal }) => {
      const scopeGuard = protectedRequestToken();
      if (scopeGuard.siteId !== site.id || scopeGuard.generation !== protectedGeneration) {
        throw new DOMException('Protected Site scope is not current.', 'AbortError');
      }
      return runRealAssetsProtectedRequest(scopeGuard, signal, (protectedSignal) => loadRealAssetsCurrentState({
        client: telemetryRuntime.client,
        devices,
        telemetryPoints,
        tenantId,
        siteId: site.id,
        csrfToken: sessionCapability,
        currentRoutePolicyRevision: telemetryRuntime.currentRoutePolicyRevision,
        signal: protectedSignal,
      }));
    },
    enabled: telemetryAllowed && registry.isSuccess && devices.length > 0,
    staleTime: 15_000,
    retry: (failureCount, error) => failureCount < 1 && (!(error instanceof S2TelemetryClientError) || error.problem.retryable),
  });

  const rows = useMemo(() => assetModel ? buildRealAssetsRows({
    assetModel,
    snapshots: current.data?.byDeviceId,
  }) : [], [assetModel, current.data?.byDeviceId]);
  const pointRows = useMemo(() => assetModel ? buildRealAssetsPointRows({
    assetModel,
    deviceRows: rows,
  }) : [], [assetModel, rows]);
  const assetRows = useMemo(() => assetModel ? buildRealAssetsAssetRows({
    assetModel,
    deviceRows: rows,
    pointRows,
  }) : [], [assetModel, pointRows, rows]);
  const hierarchyRoot = useMemo(
    () => assetModel ? buildRealAssetsHierarchy(assetModel, site.displayName) : null,
    [assetModel, site.displayName],
  );
  const hierarchyIndex = useMemo(() => {
    const index = new Map<string, RealAssetsHierarchyNode>();
    const visit = (node: RealAssetsHierarchyNode) => {
      index.set(node.key, node);
      node.children.forEach(visit);
    };
    if (hierarchyRoot) visit(hierarchyRoot);
    return index;
  }, [hierarchyRoot]);
  const selectedHierarchy = hierarchyIndex.get(hierarchySelection);
  const selectedDeviceIds = selectedHierarchy ? new Set(selectedHierarchy.deviceIds) : undefined;
  const selectedPointIds = selectedHierarchy ? new Set(selectedHierarchy.pointIds) : undefined;
  const hierarchyTree = useMemo<DataNode[]>(
    () => hierarchyRoot ? [hierarchyDataNode(hierarchyRoot)] : [],
    [hierarchyRoot],
  );
  const hierarchyExpandedKeys = useMemo(() => hierarchyRoot
    ? [
      hierarchyRoot.key,
      ...hierarchyRoot.children.filter((node) => node.kind === 'space').map((node) => node.key),
    ]
    : [], [hierarchyRoot]);
  const currentPending = telemetryAllowed && devices.length > 0 && current.isPending;
  const currentUnavailable = current.isError;
  const attentionDeviceIds = useMemo(() => new Set(rows
    .filter((row) => isRealAssetsAttentionState(row.operatingState))
    .map((row) => row.device.id)), [rows]);
  const filteredRows = useMemo(() => rows.filter((row) => (
    matchesSearch(row, search)
    && matchesHierarchy(row, selectedDeviceIds)
    && (listMode === 'all' || currentPending || currentUnavailable || attentionDeviceIds.has(row.device.id))
  )), [attentionDeviceIds, currentPending, currentUnavailable, listMode, rows, search, selectedDeviceIds]);
  const filteredPointRows = useMemo(() => pointRows.filter((row) => (
    matchesPointSearch(row, search)
    && (selectedPointIds === undefined || selectedPointIds.has(row.point.id))
    && (listMode === 'all' || currentPending || currentUnavailable || attentionDeviceIds.has(row.device.id))
  )), [attentionDeviceIds, currentPending, currentUnavailable, listMode, pointRows, search, selectedPointIds]);
  const selectedAssetTreeId = selectedHierarchy?.kind === 'asset'
    ? selectedHierarchy.key.slice('asset:'.length)
    : null;
  const filteredAssetRows = useMemo(() => assetRows.filter((row) => {
    const query = search.trim().toLocaleLowerCase('zh-CN');
    const matchesQuery = !query || [
      row.asset.id,
      row.asset.code,
      row.asset.displayName,
      row.asset.assetType,
      row.space.state === 'bound' ? row.space.space.displayName : '',
      ...row.devices.flatMap((device) => [device.device.code, device.device.displayName]),
      ...row.sensors.flatMap((sensor) => [sensor.code, sensor.displayName]),
    ].some((value) => value.toLocaleLowerCase('zh-CN').includes(query));
    if (!matchesQuery) return false;
    if (listMode === 'attention' && !currentPending && !currentUnavailable && row.operatingState === 'NORMAL') return false;
    if (selectedAssetTreeId) return row.asset.id === selectedAssetTreeId;
    if (!selectedHierarchy || selectedHierarchy.kind === 'site') return true;
    return row.devices.some((device) => selectedDeviceIds?.has(device.device.id));
  }), [currentPending, currentUnavailable, assetRows, listMode, search, selectedDeviceIds, selectedAssetTreeId, selectedHierarchy]);

  const counts = useMemo(() => ({
    total: rows.length,
    attention: currentPending || currentUnavailable ? null : rows.filter((row) => isRealAssetsAttentionState(row.operatingState)).length,
    offline: currentPending || currentUnavailable ? null : rows.filter((row) => row.operatingState === 'OFFLINE').length,
    normal: currentPending || currentUnavailable ? null : rows.filter((row) => row.operatingState === 'NORMAL').length,
  }), [currentPending, currentUnavailable, rows]);
  const deviceColumns = useMemo<ColumnsType<RealAssetsDeviceRow>>(() => {
    const columns: ColumnsType<RealAssetsDeviceRow> = [
      {
        title: '通讯端点',
        key: 'device',
        fixed: 'left',
        width: 250,
        render: (_, row) => (
          <Button
            type="link"
            className="real-assets__device-link"
            data-testid="real-assets-open-device"
            aria-haspopup="dialog"
            aria-expanded={selectedAssetId === row.device.id}
            ref={(node) => {
              if (node) deviceTriggerRefs.current.set(row.device.id, node);
              else deviceTriggerRefs.current.delete(row.device.id);
            }}
            onClick={() => openDeviceDetail(row.device.id)}
          >
            <Space direction="vertical" size={0} align="start">
              <Space size={6} wrap>
                <Typography.Text strong>{row.device.displayName}</Typography.Text>
                <Tag>{realAssetsDeviceTypeLabel(row.device.deviceType)}</Tag>
              </Space>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.device.code}</Typography.Text>
            </Space>
          </Button>
        ),
      },
      {
        title: '区域 / 设备',
        key: 'asset',
        width: 210,
        render: (_, row) => <BindingLabel row={row} />,
      },
      {
        title: '状态',
        key: 'state',
        width: 150,
        render: (_, row) => {
          if (currentPending) return <Badge status="processing" text="读取中" />;
          if (currentUnavailable) return <Badge status="error" text="状态不可用" />;
          const status = row.operatingState === 'NORMAL'
            ? 'success'
            : row.operatingState === 'OFFLINE'
              ? 'error'
              : row.operatingState === 'ATTENTION'
                ? 'warning'
                : 'default';
          return <Badge status={status} text={OPERATING_LABELS[row.operatingState]} />;
        },
      },
      {
        title: '通讯',
        key: 'communication',
        width: 190,
        render: (_, row) => {
          const snapshot = row.snapshotResult?.status === 'ok' ? row.snapshotResult.snapshot : null;
          return (
            <Space direction="vertical" size={0}>
              <Tag icon={<ApiOutlined />} color={snapshot?.displayState === 'ONLINE' ? 'processing' : undefined}>
                {snapshot?.displayState ? S2_DISPLAY_LABELS[snapshot.displayState] : '未提供'}
              </Tag>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {snapshot?.presence.lastSeenAt ? formatSampledAt(snapshot.presence.lastSeenAt, site.timezone) : '最后通讯未提供'}
              </Typography.Text>
            </Space>
          );
        },
      },
      {
        title: '关键点位',
        key: 'points',
        width: 180,
        render: (_, row) => {
          const available = row.points.filter((point) => point.state === 'PRESENT').length;
          const total = row.points.length;
          const rate = total > 0 ? Math.round((available / total) * 100) : null;
          return (
            <Space direction="vertical" size={0}>
              <Typography.Text>{row.registeredPointCount} 个 Registry Point</Typography.Text>
              <Typography.Text>{total > 0 ? `${available} / ${total} 关键点可用` : '关键点目录未配置'}</Typography.Text>
              <Typography.Text type={rate !== null && rate < 100 ? 'warning' : 'secondary'} style={{ fontSize: 12 }}>
                {rate === null ? '运行投影不影响完整点位拓扑' : `${rate}% 可用`}
              </Typography.Text>
              <ul className="real-assets__point-preview" aria-label={`${row.device.displayName} 关键点位证据`}>
                {row.points.map((point) => (
                  <li key={point.key}>
                    <span>{point.label} {point.displayValue}{point.unit ? ` ${point.unit}` : ''}</span>
                    <small>{pointEvidence(point, site.timezone)}</small>
                  </li>
                ))}
              </ul>
            </Space>
          );
        },
      },
      {
        title: 'Registry',
        key: 'registry',
        width: 150,
        render: (_, row) => (
          <Space direction="vertical" size={0}>
            <Tag color={row.device.status === 'ACTIVE' ? 'green' : 'default'}>{row.device.status}</Tag>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>Revision {row.device.revision}</Typography.Text>
          </Space>
        ),
      },
      {
        title: '操作',
        key: 'action',
        fixed: 'right',
        width: 110,
        render: (_, row) => (
          <Button
            size="small"
            type="primary"
            ghost
            icon={<EyeOutlined />}
            data-testid="real-assets-open-device"
            aria-haspopup="dialog"
            aria-expanded={selectedAssetId === row.device.id}
            onClick={() => openDeviceDetail(row.device.id)}
          >
            详情
          </Button>
        ),
      },
    ];
    return compactTable
      ? columns.filter((column) => ['device', 'asset', 'state', 'points', 'action'].includes(String(column.key)))
      : columns;
  }, [compactTable, currentPending, currentUnavailable, selectedAssetId, site.timezone]);
  const pointColumns = useMemo<ColumnsType<RealAssetsTelemetryPointRow>>(() => {
    const columns: ColumnsType<RealAssetsTelemetryPointRow> = [
      {
        title: '点位',
        key: 'point',
        fixed: 'left',
        width: 260,
        render: (_, row) => (
          <Space direction="vertical" size={0} align="start">
            <Space size={6} wrap>
              <Typography.Text strong>{row.label}</Typography.Text>
              <Tag>{realAssetsPointTypeLabel(row.point.pointType)}</Tag>
            </Space>
            <Typography.Text type="secondary" className="real-assets__technical-key">{row.point.pointCode}</Typography.Text>
          </Space>
        ),
      },
      {
        title: '通讯端点 / 传感器',
        key: 'source',
        width: 240,
        render: (_, row) => (
          <Space direction="vertical" size={0} align="start">
            <Button type="link" className="real-assets__inline-device-link" onClick={() => openDeviceDetail(row.device.id)}>
              {row.device.displayName}
            </Button>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {row.sensor
                ? `${row.sensor.displayName} · ${realAssetsSensorTypeLabel(row.sensor.sensorType)}`
                : '设备直连 Point'}
            </Typography.Text>
          </Space>
        ),
      },
      {
        title: '当前值',
        key: 'current',
        width: 190,
        render: (_, row) => {
          if (currentPending) return <Badge status="processing" text="读取中" />;
          if (currentUnavailable || !row.current) return <Badge status="error" text="状态不可用" />;
          if (row.current.state === 'MISSING') return <Badge status="warning" text={row.current.displayValue} />;
          return (
            <Space direction="vertical" size={0} align="start">
              <Typography.Text strong>
                {row.current.displayValue}{row.current.unit ? ` ${row.current.unit}` : ''}
              </Typography.Text>
              <Typography.Text type={(row.current.quality !== null && row.current.quality !== 'GOOD') || row.current.freshness === 'STALE' ? 'warning' : 'secondary'} style={{ fontSize: 12 }}>
                {pointEvidence(row.current, site.timezone)}
              </Typography.Text>
            </Space>
          );
        },
      },
      {
        title: '区域 / 设备',
        key: 'location',
        width: 220,
        render: (_, row) => (
          <Space direction="vertical" size={0} align="start">
            <Typography.Text>{row.space.state === 'bound' ? row.space.space.displayName : '区域未建立'}</Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>{assetBindingLabel(row.binding)}</Typography.Text>
          </Space>
        ),
      },
      {
        title: '采集配置',
        key: 'configuration',
        width: 170,
        render: (_, row) => (
          <Space direction="vertical" size={0} align="start">
            <Typography.Text>{realAssetsTelemetryPointMeta(row.point)}</Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              采样 {Math.round(row.point.sampleIntervalMs / 100) / 10}s · 发布 {Math.round(row.point.publishIntervalMs / 100) / 10}s
            </Typography.Text>
          </Space>
        ),
      },
      {
        title: '操作',
        key: 'action',
        fixed: 'right',
        width: 110,
        render: (_, row) => (
          <Button
            size="small"
            type="primary"
            ghost
            icon={<EyeOutlined />}
            aria-haspopup="dialog"
            aria-expanded={selectedAssetId === row.device.id}
            onClick={() => openDeviceDetail(row.device.id)}
          >
            设备详情
          </Button>
        ),
      },
    ];
    return compactTable
      ? columns.filter((column) => ['point', 'source', 'current', 'action'].includes(String(column.key)))
      : columns;
  }, [compactTable, currentPending, currentUnavailable, selectedAssetId, site.timezone]);
  const detailResolution = useMemo(
    () => resolveRealAssetsDetail(assetRows, selectedAssetId),
    [assetRows, selectedAssetId],
  );
  const detailRow = detailResolution.state === 'visible' ? detailResolution.row : null;

  useEffect(() => {
    if (selectedAssetId !== null) return;
    const deviceId = pendingFocusDeviceIdRef.current;
    if (!deviceId) return;
    pendingFocusDeviceIdRef.current = null;
    window.requestAnimationFrame(() => {
      const trigger = deviceTriggerRefs.current.get(deviceId);
      if (trigger) {
        trigger.focus({ preventScroll: true });
        return;
      }
      document.getElementById('real-assets-title')?.focus({ preventScroll: true });
    });
  }, [filteredRows, selectedAssetId]);

  const openAssetDetail = (assetId: string) => {
    const target = realAssetsAssetPath(site.id, assetId);
    const historyState = { marker: REAL_ASSETS_DETAIL_HISTORY_MARKER, siteId: site.id, assetId };
    if (selectedAssetIdRef.current) window.history.replaceState(historyState, '', target);
    else window.history.pushState(historyState, '', target);
    selectedAssetIdRef.current = assetId;
    setSelectedAssetId(assetId);
  };

  const openDeviceDetail = (deviceId: string) => {
    const row = rows.find((candidate) => candidate.device.id === deviceId);
    const binding = row ? assetBindings(row.binding)[0] : undefined;
    if (binding) openAssetDetail(binding.asset.id);
  };

  const closeAssetDetail = () => {
    const assetId = selectedAssetIdRef.current;
    if (!assetId) return;
    if (isRealAssetsDetailHistoryState(window.history.state, site.id, assetId)) {
      window.history.back();
      return;
    }
    window.history.pushState(null, '', realAssetsListPath(site.id));
    selectedAssetIdRef.current = null;
    setSelectedAssetId(null);
  };

  const assetColumns: ColumnsType<RealAssetsAssetRow> = [
    {
      title: 'Asset',
      key: 'asset',
      fixed: 'left',
      width: 250,
      render: (_, row) => (
        <Button type="link" onClick={() => openAssetDetail(row.asset.id)}>
          <Space direction="vertical" size={0} align="start">
            <Typography.Text strong>{row.asset.displayName}</Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.asset.code} · {row.asset.assetType}</Typography.Text>
          </Space>
        </Button>
      ),
    },
    { title: '区域', key: 'space', width: 160, render: (_, row) => row.space.state === 'bound' ? row.space.space.displayName : '未绑定 Space' },
    { title: '运行状态', key: 'state', width: 130, render: (_, row) => <Tag>{OPERATING_LABELS[row.operatingState]}</Tag> },
    {
      title: 'Device Endpoints',
      key: 'devices',
      width: 230,
      render: (_, row) => row.devices.length > 0
        ? <Space direction="vertical" size={0}>{row.devices.map((device) => <Typography.Text key={device.device.id}>{device.device.displayName} · {device.binding.state === 'bound' ? device.binding.relationship.role : device.binding.state}</Typography.Text>)}</Space>
        : <Typography.Text type="secondary">未绑定通讯端点</Typography.Text>,
    },
    { title: 'Sensors', key: 'sensors', width: 100, render: (_, row) => `${row.sensors.length} 个` },
    { title: '点位', key: 'points', width: 120, render: (_, row) => `${row.points.length} 个` },
    { title: '设备功能', key: 'controls', width: 120, render: (_, row) => <Tag color={row.controlPoints.length > 0 ? 'blue' : undefined}>{row.controlPoints.length} 项</Tag> },
    { title: '操作', key: 'action', fixed: 'right', width: 100, render: (_, row) => <Button size="small" type="primary" ghost icon={<EyeOutlined />} onClick={() => openAssetDetail(row.asset.id)}>详情</Button> },
  ];

  const businessState = !registryAllowed || !telemetryAllowed
    ? 'FORBIDDEN'
    : registry.isPending
      ? 'LOADING'
      : registry.isError
        ? 'REGISTRY_UNAVAILABLE'
        : devices.length === 0
          ? 'EMPTY'
          : currentUnavailable
            ? 'TELEMETRY_UNAVAILABLE'
            : currentPending
              ? 'CURRENT_STATE_LOADING'
              : current.data?.partial
                ? 'PARTIAL'
                : filteredRows.length === 0
                  ? 'FILTER_EMPTY'
                  : 'READY';

  if (!registryAllowed || !telemetryAllowed) {
    return (
      <section className="real-assets" data-testid="real-site-route-assets" data-business-state="FORBIDDEN" data-site-id={site.id}>
        <PageScaffold
          title="设备与建筑"
          heading={<FocusHeading className="ops-page-title ant-typography"><Space><ApartmentOutlined />设备与建筑</Space></FocusHeading>}
          extra={<Tag color="error">FORBIDDEN</Tag>}
          className="assets-page"
        >
          <Alert
            type="error"
            showIcon
            message="资产运行工作台不可用"
            description="当前 Principal 缺少 Asset Model 所需 Registry read 或 Telemetry batch read 的服务器能力投影。此状态不会尝试调用受保护数据接口。"
            data-retryable="false"
          />
        </PageScaffold>
      </section>
    );
  }

  if (registry.isPending) {
    return <RealAssetsLoadingSurface siteId={site.id} />;
  }

  if (registry.isError) {
    const failure = presentRegistryError(registry.error);
    return (
      <section className="real-assets" data-testid="real-site-route-assets" data-business-state="REGISTRY_UNAVAILABLE" data-site-id={site.id}>
        {failure.retryable ? (
          <button type="button" className="real-shell-sr-only" onClick={() => { void registry.refetch(); }}>重试 Registry</button>
        ) : null}
        <PageScaffold
          title={failure.title}
          heading={<FocusHeading className="ops-page-title ant-typography"><Space><ApartmentOutlined />{failure.title}</Space></FocusHeading>}
          extra={<Tag color="error">REGISTRY UNAVAILABLE</Tag>}
          className="assets-page"
        >
          <Alert
            type="error"
            showIcon
            message={failure.title}
            description={<Space direction="vertical"><span>{failure.description}</span>{failure.traceId ? <code>traceId {failure.traceId}</code> : null}</Space>}
            action={failure.retryable ? <Button icon={<ReloadOutlined />} onClick={() => { void registry.refetch(); }}>重试 Registry</Button> : undefined}
            data-retryable={String(failure.retryable)}
          />
        </PageScaffold>
      </section>
    );
  }

  return (
    <section
      className="real-assets"
      data-testid="real-site-route-assets"
      data-business-state={businessState}
      data-site-id={site.id}
      data-catalog-revision={REAL_ASSETS_CATALOG_REVISION}
      data-registry-policy-revision={registry.data?.routePolicyRevision ?? 'unavailable'}
      data-telemetry-policy-revision={current.data?.routePolicyRevision ?? telemetryPolicyRevision ?? 'unavailable'}
      data-current-request-count={String(current.data?.requestCount ?? 0)}
      data-detail-state={detailResolution.state}
      data-space-count={String(assetModel?.counts.spaces ?? 0)}
      data-asset-count={String(assetModel?.counts.assets ?? 0)}
      data-device-endpoint-count={String(assetModel?.counts.deviceEndpoints ?? 0)}
      data-sensor-count={String(assetModel?.counts.physicalSensors ?? 0)}
      data-telemetry-point-count={String(assetModel?.counts.points ?? 0)}
      data-total-device-count={String(rows.length)}
      data-filtered-device-count={String(filteredRows.length)}
      data-point-ledger-count={String(pointRows.length)}
      data-filtered-point-count={String(filteredPointRows.length)}
      data-ledger-mode={ledgerMode}
      data-list-mode={listMode}
      data-hierarchy-selection={hierarchySelection}
    >
      <PageScaffold
        title="设备与建筑"
        heading={<FocusHeading id="real-assets-title" className="ops-page-title ant-typography"><Space><ApartmentOutlined />设备与建筑</Space></FocusHeading>}
        extra={(
          <Space wrap>
            <Tag color={businessState === 'READY' ? 'green' : businessState === 'EMPTY' ? 'default' : 'orange'}>{businessState}</Tag>
            <div className="real-assets__header">
              <Button
                icon={<ReloadOutlined />}
                loading={registry.isFetching || current.isFetching}
                onClick={() => {
                  void registry.refetch();
                  if (devices.length > 0) void current.refetch();
                }}
              >
                刷新
              </Button>
            </div>
          </Space>
        )}
        className="assets-page"
      >
        <Typography.Text type="secondary">{site.displayName} · 原子 Asset Model 与 S2 当前状态均限定在当前 Tenant / Site。 · Tenant: {tenantId}</Typography.Text>
        <OperationsMetrics
          ariaLabel="Site 原子资产模型摘要"
          items={[
            { key: 'spaces', label: 'Space', value: assetModel?.counts.spaces ?? 0, detail: '空间层级', tone: 'accent' },
            { key: 'asset', label: 'Asset', value: assetModel?.counts.assets ?? 0, detail: '物理设备', tone: 'default' },
            { key: 'device-endpoints', label: 'Device Endpoint', value: assetModel?.counts.deviceEndpoints ?? 0, detail: '通信端点', tone: 'default' },
            { key: 'sensors', label: 'Sensor', value: assetModel?.counts.physicalSensors ?? 0, detail: '物理测量单元', tone: 'default' },
            { key: 'points', label: 'Point', value: assetModel?.counts.points ?? 0, detail: '标准点位目录', tone: 'accent' },
          ]}
        />
        <OperationsMetrics
          ariaLabel="Device Endpoint 运行摘要"
          items={[
            { key: 'total', label: '可见 Device Endpoint', value: counts.total, detail: '原子模型授权集合', tone: 'accent' },
            { key: 'attention', label: '需关注', value: counts.attention ?? '—', detail: '离线、未知或关键点异常', tone: counts.attention ? 'warning' : 'positive' },
            { key: 'offline', label: '离线', value: counts.offline ?? '—', detail: '权威 Presence OFFLINE', tone: counts.offline ? 'critical' : 'default' },
            { key: 'normal', label: '正常', value: counts.normal ?? '—', detail: '当前状态正常', tone: 'positive' },
          ]}
        />

      {devices.length === 0 ? (
        <div className="real-assets__empty" role="status">
          当前 Site 的原子 Asset Model 尚未登记 Device Endpoint；Space、Asset、Sensor 与 Point 计数仍保持权威展示。
        </div>
      ) : null}
      {currentPending ? (
        <div className="real-assets__notice" role="status">已建立原子资产模型，正在按通讯端点批量读取全部已登记点位的当前状态。</div>
      ) : null}
      {currentUnavailable ? (() => {
        const failure = telemetryFailure(current.error);
        return (
          <div className="real-assets__problem" role="alert" data-retryable={String(failure.retryable)}>
            <strong>{failure.title}</strong>
            <span>{failure.detail}</span>
            {failure.retryable ? <button type="button" onClick={() => { void current.refetch(); }}>重试当前状态</button> : null}
          </div>
        );
      })() : null}
      {current.data?.partial ? (
        <div className="real-assets__notice real-assets__notice--warning" role="status">
          部分 Device Snapshot 不可用；成功 Device 保留权威状态，失败项独立标记，不使用历史值或零填充。
        </div>
      ) : null}

      {hierarchyRoot ? (
        <Row gutter={[16, 16]} className="real-assets__workspace">
          <Col xs={24} lg={7} xl={6}>
            <Card
              variant="borderless"
              title={<OperationsPanelHeading icon={<ClusterOutlined />} title="资产导航" />}
              className="assets-hierarchy-card"
            >
              <div className="assets-hierarchy-card__scroll" role="navigation" aria-label="资产层级导航">
                <Tree
                  key={site.id}
                  className="real-assets-navigation-tree"
                  showIcon
                  blockNode
                  defaultExpandedKeys={hierarchyExpandedKeys}
                  switcherIcon={({ isLeaf }) => isLeaf ? null : <RightOutlined className="real-assets-tree-switcher" />}
                  treeData={hierarchyTree}
                  selectedKeys={[hierarchySelection]}
                  onSelect={(keys: Key[]) => {
                    const selected = String(keys[0] ?? `site:${site.id}`) as HierarchySelection;
                    setHierarchySelection(selected);
                    const node = hierarchyIndex.get(selected);
                    if (node?.kind === 'point' || node?.kind === 'sensor' || node?.kind === 'virtual-sensor') setLedgerMode('points');
                    else if (node?.kind === 'device') setLedgerMode('devices');
                    else setLedgerMode('asset');
                  }}
                />
              </div>
            </Card>
          </Col>
          <Col xs={24} lg={17} xl={18}>
            <Card variant="borderless" className="assets-ledger-card" styles={{ body: { padding: 16 } }}>
              <Space direction="vertical" size={12} style={{ width: '100%' }}>
                <div className="ops-toolbar">
                  <OperationsPanelHeading
                    icon={<NodeIndexOutlined />}
                    title="资产台账"
                    meta={ledgerMode === 'asset' ? `${filteredAssetRows.length} 个 Asset` : ledgerMode === 'points' ? `${filteredPointRows.length} 个点位` : `${filteredRows.length} 个通讯端点`}
                  />
                  <Space wrap>
                    <Segmented<LedgerMode>
                      value={ledgerMode}
                      onChange={setLedgerMode}
                      options={[
                        { label: `Asset ${assetRows.length}`, value: 'asset' },
                        { label: `通讯端点 ${rows.length}`, value: 'devices' },
                        { label: `点位 ${pointRows.length}`, value: 'points' },
                      ]}
                    />
                    <Input
                      allowClear
                      type="search"
                      data-testid="real-assets-search"
                      placeholder={ledgerMode === 'asset' ? '搜索 Asset、区域或端点' : ledgerMode === 'points' ? '搜索点位、传感器或通讯端点' : '搜索通讯端点或 Asset'}
                      value={search}
                      onChange={(event) => setSearch(event.currentTarget.value)}
                      style={{ width: 280 }}
                    />
                    <Select
                      value={listMode}
                      onChange={setListMode}
                      options={[
                        { label: '需关注资产', value: 'attention' },
                        { label: '全部资产', value: 'all' },
                      ]}
                      style={{ width: 130 }}
                    />
                  </Space>
                </div>

                <div data-testid="real-assets-table-wrap" data-ledger-mode={ledgerMode}>
                  {ledgerMode === 'asset' ? (
                    <Table<RealAssetsAssetRow>
                      rowKey={(row) => row.asset.id}
                      size="middle"
                      columns={assetColumns}
                      dataSource={filteredAssetRows}
                      pagination={{ pageSize: 10, showSizeChanger: false, showTotal: (total) => `共 ${total} 个 Asset` }}
                      scroll={{ x: 1150 }}
                      locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前层级或筛选条件没有匹配的 Asset。" /> }}
                      onRow={(row) => ({ 'data-asset-id': row.asset.id } as HTMLAttributes<HTMLTableRowElement>)}
                    />
                  ) : ledgerMode === 'points' ? (
                    <>
                      <table className="real-assets__table real-shell-sr-only" aria-label="完整授权点位台账">
                        <tbody>
                          {filteredPointRows.map((row) => (
                            <tr key={row.point.id} data-point-id={row.point.id} data-device-id={row.device.id}>
                              <td>{row.label}</td>
                              <td>{row.point.pointCode}</td>
                              <td>{row.device.displayName}</td>
                              <td>{row.sensor?.displayName ?? '设备直连或计算点'}</td>
                              <td>{row.current?.displayValue ?? '状态不可用'}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                      <Table<RealAssetsTelemetryPointRow>
                        rowKey={(row) => row.point.id}
                        size="middle"
                        columns={pointColumns}
                        dataSource={filteredPointRows}
                        pagination={{ pageSize: 15, showSizeChanger: false, showTotal: (total) => `共 ${total} 个点位` }}
                        scroll={{ x: compactTable ? 820 : 1180 }}
                        locale={{
                          emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前层级或筛选条件没有匹配的点位。" />,
                        }}
                        onRow={(row) => ({
                          'data-point-id': row.point.id,
                          'data-device-id': row.device.id,
                        } as HTMLAttributes<HTMLTableRowElement>)}
                      />
                    </>
                  ) : (
                    <>
                      <table className="real-assets__table real-shell-sr-only" aria-label="完整授权通讯端点运行投影">
                        <tbody>
                          {filteredRows.map((row) => (
                            <tr
                              key={row.device.id}
                              data-device-id={row.device.id}
                              data-operating-state={currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operatingState}
                            >
                              <td>{row.device.displayName} {row.device.code}</td>
                              <td>{assetBindingLabel(row.binding)}</td>
                              <td>{currentUnavailable ? '状态不可用' : currentPending ? '读取中' : OPERATING_LABELS[row.operatingState]}</td>
                              <td>{row.registeredPointCount} 个点位</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                      <Table<RealAssetsDeviceRow>
                        rowKey={(row) => row.device.id}
                        size="middle"
                        columns={deviceColumns}
                        dataSource={filteredRows}
                        pagination={{ pageSize: 8, showSizeChanger: false }}
                        scroll={{ x: compactTable ? 820 : 1240 }}
                        locale={{
                          emptyText: (
                            <Empty
                              image={Empty.PRESENTED_IMAGE_SIMPLE}
                              description={listMode === 'attention' && !currentPending && !currentUnavailable && rows.every((row) => row.operatingState === 'NORMAL')
                                ? '当前筛选范围内所有通讯端点均为正常。切换到“全部资产”可查看完整列表。'
                                : '当前筛选条件没有匹配的通讯端点。'}
                            />
                          ),
                        }}
                        onRow={(row) => ({
                          'data-device-id': row.device.id,
                          'data-operating-state': currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operatingState,
                        } as HTMLAttributes<HTMLTableRowElement>)}
                      />
                    </>
                  )}
                </div>
              </Space>
            </Card>
          </Col>
        </Row>
      ) : null}
      <AssetDetailDrawer
        site={site}
        principal={principal}
        row={detailRow}
        telemetryClient={telemetryRuntime.client}
        protectedGeneration={protectedGeneration}
        protectedRequestToken={protectedRequestToken}
        registerProtectedResource={registerProtectedResource}
        telemetryRuntime={telemetryRuntime}
        publishRealtimeStatus={publishRealtimeStatus}
        routePolicyRevision={telemetryPolicyRevision}
        refreshing={registry.isFetching || current.isFetching}
        onClose={closeAssetDetail}
        onRefresh={() => {
          void registry.refetch();
          if (devices.length > 0) void current.refetch();
        }}
      />
      </PageScaffold>
    </section>
  );
}
