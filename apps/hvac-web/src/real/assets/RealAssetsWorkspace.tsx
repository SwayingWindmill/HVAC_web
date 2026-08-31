import { useEffect, useMemo, useRef, useState, type HTMLAttributes, type Key } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router';
import { Alert, Badge, Button, Card, Col, Empty, Grid, Input, Row, Segmented, Space, Table, Tag, Tree, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { DataNode } from 'antd/es/tree';
import {
  ApartmentOutlined,
  ApiOutlined,
  BlockOutlined,
  ClusterOutlined,
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
import type { AssetsDetailTarget } from '../site-routing';
import { REAL_ASSETS_CATALOG_REVISION } from './catalog';
import { AssetDetailDrawer } from './AssetDetailDrawer';
import { DeviceDetailDrawer } from './DeviceDetailDrawer';
import { RealAssetsLoadingSurface } from './RealAssetsLoadingSurface';
import { realAssetsAssetPath, realAssetsDevicePath, realAssetsListPath, resolveRealAssetsDetail } from './detail';
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
  realAssetsDeviceTypeLabel,
  type RealAssetsDeviceRow,
  type RealAssetsAssetRow,
  type RealAssetsHierarchyNode,
} from './model';
import {
  createRealAssetsTelemetryRuntime,
  type RealAssetsTelemetryRuntime,
} from './telemetry-runtime';
import './real-assets.css';

interface RealAssetsWorkspaceProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  requestedDetail?: AssetsDetailTarget;
  protectedGeneration: number;
  protectedRequestToken: () => ProtectedScopeRequestToken;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  publishRealtimeStatus: (update: RealtimeStatusUpdate) => void;
  platformClient?: Pick<PlatformGatewayClient, 'getSiteAssetModel'>;
  telemetryRuntime?: RealAssetsTelemetryRuntime;
}

type ListMode = 'attention' | 'all';
type LedgerMode = 'devices' | 'asset';
type HierarchySelection = string;

const CONNECTION_LABELS = {
  ONLINE: '在线',
  OFFLINE: '离线',
  UNKNOWN: '连接未知',
  NOT_APPLICABLE: '连接不适用',
  UNAVAILABLE: '连接判定不可用',
} as const;

const READINESS_LABELS = {
  CURRENT: '数据完整',
  DEGRADED: '数据退化',
  INCOMPLETE: '数据不完整',
  NOT_APPLICABLE: '数据不适用',
  UNAVAILABLE: '数据判定不可用',
} as const;

const FRESHNESS_LABELS = {
  FRESH: '新鲜',
  STALE: '陈旧',
  MISSING: '缺失',
  NOT_APPLICABLE: '不适用',
  UNAVAILABLE: '不可用',
} as const;

const QUALITY_LABELS = {
  GOOD: '质量良好',
  DEGRADED: '质量退化',
  NO_DATA: '无可用数据',
  NOT_APPLICABLE: '质量不适用',
  UNAVAILABLE: '质量不可用',
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

const HIERARCHY_ICONS = {
  site: <ApartmentOutlined />,
  space: <ClusterOutlined />,
  asset: <BlockOutlined />,
  device: <TabletOutlined />,
} as const;

function hierarchyDataNode(node: RealAssetsHierarchyNode): DataNode {
  return {
    key: node.key,
    icon: HIERARCHY_ICONS[node.kind],
    title: (
      <span
        className="real-assets-tree-node"
        data-testid={node.kind === 'site'
          ? 'real-assets-hierarchy-site'
          : node.kind === 'asset' && node.key.startsWith('asset:unbound:')
            ? 'real-assets-hierarchy-unbound'
            : `real-assets-hierarchy-${node.kind}`}
        data-asset-kind={node.kind}
        data-asset-id={node.kind === 'asset' && !node.key.includes('unbound:') ? node.key.slice('asset:'.length) : undefined}
        data-device-id={node.kind === 'device' ? node.deviceIds[0] : undefined}
        title={`${node.label}｜${node.meta}`}
      >
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

function pointEvidence(point: RealAssetsDeviceRow['operational']['points'][number], timeZone: string): string {
  if (point.state === 'UNAVAILABLE') return '当前状态服务无法确认该点位';
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
  requestedDetail,
  protectedGeneration,
  protectedRequestToken,
  registerProtectedResource,
  publishRealtimeStatus,
  platformClient: providedPlatformClient,
  telemetryRuntime: providedTelemetryRuntime,
}: RealAssetsWorkspaceProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const platformClient = useMemo(() => providedPlatformClient ?? createPlatformGatewayClient(), [providedPlatformClient]);
  const telemetryRuntime = useMemo(() => providedTelemetryRuntime ?? createRealAssetsTelemetryRuntime(), [providedTelemetryRuntime]);
  const [listMode, setListMode] = useState<ListMode>('all');
  const [ledgerMode, setLedgerMode] = useState<LedgerMode>('devices');
  const [search, setSearch] = useState('');
  const [hierarchySelection, setHierarchySelection] = useState<HierarchySelection>(`site:${site.id}`);
  const [telemetryPolicyRevision, setTelemetryPolicyRevision] = useState<string | null>(
    () => telemetryRuntime.currentRoutePolicyRevision(),
  );
  const [routePolicyEpoch, setRoutePolicyEpoch] = useState(0);
  const [selectedDetail, setSelectedDetail] = useState<AssetsDetailTarget | null>(() => requestedDetail ?? null);
  const selectedDetailRef = useRef<AssetsDetailTarget | null>(selectedDetail);
  const previousDetailRef = useRef<AssetsDetailTarget | null>(selectedDetail);
  const returnFocusAssetIdRef = useRef<string | null>(null);
  const returnFocusDeviceIdRef = useRef<string | null>(null);
  const assetTriggerRefs = useRef(new Map<string, HTMLElement>());
  const deviceTriggerRefs = useRef(new Map<string, HTMLElement>());
  const tenantId = site.tenantId;
  const sessionCapability = principal.session.csrfToken;
  const capabilities = principal.authorization.capabilities;
  const registryAllowed = capabilities.includes('asset.list') && capabilities.includes('device.list');
  const telemetryAllowed = capabilities.includes('telemetry.batch.read');
  const queryRoot = useMemo(
    () => ['real-assets', protectedGeneration, tenantId, site.id] as const,
    [tenantId, protectedGeneration, site.id],
  );

  useEffect(() => {
    const next = requestedDetail ?? null;
    selectedDetailRef.current = next;
    setSelectedDetail(next);
  }, [protectedGeneration, requestedDetail?.id, requestedDetail?.kind, site.id]);

  useEffect(() => {
    selectedDetailRef.current = selectedDetail;
  }, [selectedDetail]);

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
      setLedgerMode('devices');
      setSearch('');
      setHierarchySelection(`site:${site.id}`);
      selectedDetailRef.current = null;
      previousDetailRef.current = null;
      returnFocusAssetIdRef.current = null;
      returnFocusDeviceIdRef.current = null;
      setSelectedDetail(null);
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
    .filter((row) => row.operational.needsAttention)
    .map((row) => row.device.id)), [rows]);
  const filteredRows = useMemo(() => rows.filter((row) => (
    matchesSearch(row, search)
    && matchesHierarchy(row, selectedDeviceIds)
    && (listMode === 'all' || currentPending || currentUnavailable || attentionDeviceIds.has(row.device.id))
  )), [attentionDeviceIds, currentPending, currentUnavailable, listMode, rows, search, selectedDeviceIds]);
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
    if (listMode === 'attention' && !currentPending && !currentUnavailable && !row.needsAttention) return false;
    if (selectedAssetTreeId) return row.asset.id === selectedAssetTreeId;
    if (!selectedHierarchy || selectedHierarchy.kind === 'site') return true;
    return row.devices.some((device) => selectedDeviceIds?.has(device.device.id));
  }), [currentPending, currentUnavailable, assetRows, listMode, search, selectedDeviceIds, selectedAssetTreeId, selectedHierarchy]);

  const selectedAssetId = selectedDetail?.kind === 'asset' ? selectedDetail.id : null;
  const selectedDeviceId = selectedDetail?.kind === 'device' ? selectedDetail.id : null;
  const counts = useMemo(() => ({
    total: rows.length,
    attention: currentPending || currentUnavailable ? null : rows.filter((row) => row.operational.needsAttention).length,
    offline: currentPending || currentUnavailable ? null : rows.filter((row) => row.operational.connection.state === 'OFFLINE').length,
    healthyData: currentPending || currentUnavailable ? null : rows.filter((row) => (
      row.operational.telemetry.readiness === 'CURRENT'
      && row.operational.telemetry.freshness === 'FRESH'
      && row.operational.telemetry.quality === 'GOOD'
    )).length,
    connectionUnknown: currentPending || currentUnavailable ? null : rows.filter((row) => row.operational.connection.state === 'UNKNOWN').length,
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
            aria-expanded={selectedDeviceId === row.device.id}
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
        title: '数据状态',
        key: 'telemetry',
        width: 190,
        render: (_, row) => {
          if (currentPending) return <Badge status="processing" text="读取中" />;
          if (currentUnavailable) return <Badge status="error" text="状态服务不可用" />;
          const telemetry = row.operational.telemetry;
          const status = row.operational.needsAttention ? 'warning' : telemetry.readiness === 'CURRENT' ? 'success' : 'default';
          return (
            <Space direction="vertical" size={0}>
              <Badge status={status} text={READINESS_LABELS[telemetry.readiness]} />
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {FRESHNESS_LABELS[telemetry.freshness]} · {QUALITY_LABELS[telemetry.quality]}
              </Typography.Text>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {telemetry.presentPointCount}/{telemetry.registeredPointCount} Point 有当前证据
              </Typography.Text>
            </Space>
          );
        },
      },
      {
        title: '连接',
        key: 'communication',
        width: 190,
        render: (_, row) => {
          const connection = row.operational.connection;
          return (
            <Space direction="vertical" size={0}>
              <Tag icon={<ApiOutlined />} color={connection.state === 'ONLINE' ? 'processing' : connection.state === 'OFFLINE' ? 'error' : undefined}>
                {CONNECTION_LABELS[connection.state]}
              </Tag>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {connection.lastSeenAt ? formatSampledAt(connection.lastSeenAt, site.timezone) : '最后通讯未提供'}
              </Typography.Text>
            </Space>
          );
        },
      },
      {
        title: '当前值',
        key: 'points',
        width: 230,
        render: (_, row) => (
          <Space direction="vertical" size={2}>
            {row.operational.representativePoints.length > 0
              ? row.operational.representativePoints.map((point) => (
                <div key={point.pointId}>
                  <Typography.Text>{point.label} {point.displayValue}{point.unit ? ` ${point.unit}` : ''}</Typography.Text>
                  <Typography.Text type="secondary" style={{ display: 'block', fontSize: 12 }}>{pointEvidence(point, site.timezone)}</Typography.Text>
                </div>
              ))
              : <Typography.Text type="secondary">无可预览的 Registry Point</Typography.Text>}
          </Space>
        ),
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
            aria-expanded={selectedDeviceId === row.device.id}
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
  }, [compactTable, currentPending, currentUnavailable, selectedDeviceId, site.timezone]);
  const detailResolution = useMemo(
    () => resolveRealAssetsDetail(assetRows, rows, selectedDetail),
    [assetRows, rows, selectedDetail],
  );
  const assetDetailRow = detailResolution.state === 'visible' && detailResolution.kind === 'asset'
    ? detailResolution.row
    : null;
  const deviceDetailRow = detailResolution.state === 'visible' && detailResolution.kind === 'device'
    ? detailResolution.row
    : null;
  const assetDetailState: 'closed' | 'visible' | 'not-visible' = selectedDetail?.kind !== 'asset'
    ? 'closed'
    : assetDetailRow
      ? 'visible'
      : 'not-visible';
  const deviceDetailState: 'closed' | 'visible' | 'not-visible' = selectedDetail?.kind !== 'device'
    ? 'closed'
    : deviceDetailRow
      ? 'visible'
      : 'not-visible';

  useEffect(() => {
    const previousDetail = previousDetailRef.current;
    previousDetailRef.current = selectedDetail;
    if (selectedDetail !== null || !previousDetail) return;
    const targetId = previousDetail.kind === 'asset'
      ? returnFocusAssetIdRef.current ?? previousDetail.id
      : returnFocusDeviceIdRef.current ?? previousDetail.id;
    window.requestAnimationFrame(() => {
      const trigger = previousDetail.kind === 'asset'
        ? assetTriggerRefs.current.get(targetId)
        : deviceTriggerRefs.current.get(targetId);
      if (trigger) {
        trigger.focus({ preventScroll: true });
        return;
      }
      document.getElementById('real-assets-title')?.focus({ preventScroll: true });
    });
  }, [filteredAssetRows, filteredRows, selectedDetail]);

  const openAssetDetail = (assetId: string) => {
    const detail: AssetsDetailTarget = { kind: 'asset', id: assetId };
    returnFocusAssetIdRef.current = assetId;
    navigate(realAssetsAssetPath(site.id, assetId));
    selectedDetailRef.current = detail;
    setSelectedDetail(detail);
  };

  const openDeviceDetail = (deviceId: string) => {
    const detail: AssetsDetailTarget = { kind: 'device', id: deviceId };
    returnFocusDeviceIdRef.current = deviceId;
    navigate(realAssetsDevicePath(site.id, deviceId));
    selectedDetailRef.current = detail;
    setSelectedDetail(detail);
  };

  const closeDetail = () => {
    if (!selectedDetailRef.current) return;
    navigate(realAssetsListPath(site.id));
    selectedDetailRef.current = null;
    setSelectedDetail(null);
  };

  const assetColumns: ColumnsType<RealAssetsAssetRow> = [
    {
      title: 'Asset',
      key: 'asset',
      fixed: 'left',
      width: 250,
      render: (_, row) => (
        <Button
          type="link"
          data-testid="real-assets-open-asset"
          aria-haspopup="dialog"
          aria-expanded={selectedAssetId === row.asset.id}
          ref={(node) => {
            if (node) assetTriggerRefs.current.set(row.asset.id, node);
            else assetTriggerRefs.current.delete(row.asset.id);
          }}
          onClick={() => openAssetDetail(row.asset.id)}
        >
          <Space direction="vertical" size={0} align="start">
            <Typography.Text strong>{row.asset.displayName}</Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.asset.code} · {row.asset.assetType}</Typography.Text>
          </Space>
        </Button>
      ),
    },
    { title: '区域', key: 'space', width: 160, render: (_, row) => row.space.state === 'bound' ? row.space.space.displayName : '未绑定 Space' },
    {
      title: 'Device 状态',
      key: 'state',
      width: 190,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{row.offlineDeviceCount} 离线 · {row.dataIssueDeviceCount} 数据异常</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.connectionUnknownDeviceCount} 连接未知</Typography.Text>
        </Space>
      ),
    },
    {
      title: '设备',
      key: 'devices',
      width: 140,
      render: (_, row) => <Typography.Text>{row.devices.length} 台</Typography.Text>,
    },
    {
      title: '数据摘要',
      key: 'data',
      width: 190,
      render: (_, row) => (
        <Typography.Text type={row.dataIssueDeviceCount > 0 ? 'warning' : 'secondary'}>
          {row.dataIssueDeviceCount > 0 ? `${row.dataIssueDeviceCount} 台数据需关注` : '子设备数据无异常'}
        </Typography.Text>
      ),
    },
    { title: '控制能力', key: 'controls', width: 120, render: (_, row) => <Tag color={row.controlPoints.length > 0 ? 'blue' : undefined}>{row.controlPoints.length > 0 ? `${row.controlPoints.length} 项` : '无'}</Tag> },
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
          ariaLabel="Device Endpoint 运行摘要"
          items={[
            { key: 'total', label: '可见 Device Endpoint', value: counts.total, detail: '原子模型授权集合', tone: 'accent' },
            { key: 'healthy-data', label: '数据健康', value: counts.healthyData ?? '—', detail: 'CURRENT · FRESH · GOOD', tone: 'positive' },
            { key: 'attention', label: '需关注', value: counts.attention ?? '—', detail: '离线、陈旧、缺失或质量退化', tone: counts.attention ? 'warning' : 'positive' },
            { key: 'offline', label: '离线', value: counts.offline ?? '—', detail: '权威 Presence OFFLINE', tone: counts.offline ? 'critical' : 'default' },
            { key: 'connection-unknown', label: '连接未知', value: counts.connectionUnknown ?? '—', detail: 'Presence UNKNOWN，不等于异常', tone: 'default' },
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
                    title="资产与设备"
                    meta={ledgerMode === 'asset' ? `${filteredAssetRows.length} 个 Asset` : `${filteredRows.length} 个 Device`}
                  />
                  <Space wrap>
                    <Segmented<LedgerMode>
                      value={ledgerMode}
                      onChange={setLedgerMode}
                      options={[
                        { label: <span data-testid="real-assets-mode-devices">设备 {rows.length}</span>, value: 'devices' },
                        { label: <span data-testid="real-assets-mode-assets">资产 {assetRows.length}</span>, value: 'asset' },
                      ]}
                    />
                    <Input
                      allowClear
                      type="search"
                      data-testid="real-assets-search"
                      placeholder={ledgerMode === 'asset' ? '搜索资产、区域或设备' : '搜索设备、资产或区域'}
                      value={search}
                      onChange={(event) => setSearch(event.currentTarget.value)}
                      style={{ width: 280 }}
                    />
                    <Space size={4}>
                      <Button
                        data-testid="real-assets-list-attention"
                        type={listMode === 'attention' ? 'primary' : 'default'}
                        onClick={() => setListMode('attention')}
                      >
                        需关注
                      </Button>
                      <Button
                        data-testid="real-assets-list-all"
                        type={listMode === 'all' ? 'primary' : 'default'}
                        onClick={() => setListMode('all')}
                      >
                        全部
                      </Button>
                    </Space>
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
                  ) : (
                    <>
                      <table className="real-assets__table real-shell-sr-only" aria-label="完整授权通讯端点运行投影">
                        <tbody>
                          {filteredRows.map((row) => (
                            <tr
                              key={row.device.id}
                              data-device-id={row.device.id}
                              data-connection-state={currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operational.connection.state}
                              data-telemetry-readiness={currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operational.telemetry.readiness}
                              data-needs-attention={currentUnavailable || currentPending ? 'UNKNOWN' : String(row.operational.needsAttention)}
                            >
                              <td>{row.device.displayName} {row.device.code}</td>
                              <td>{assetBindingLabel(row.binding)}</td>
                              <td>{currentUnavailable ? '状态不可用' : currentPending ? '读取中' : CONNECTION_LABELS[row.operational.connection.state]}</td>
                              <td>{currentUnavailable ? '状态不可用' : currentPending ? '读取中' : READINESS_LABELS[row.operational.telemetry.readiness]}</td>
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
                              description={listMode === 'attention' && !currentPending && !currentUnavailable && rows.every((row) => !row.operational.needsAttention)
                                ? '当前筛选范围内没有离线、陈旧、缺失、不完整或质量退化的通讯端点。切换到“全部资产”可查看完整列表。'
                                : '当前筛选条件没有匹配的通讯端点。'}
                            />
                          ),
                        }}
                        onRow={(row) => ({
                          'data-device-id': row.device.id,
                          'data-connection-state': currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operational.connection.state,
                          'data-telemetry-readiness': currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operational.telemetry.readiness,
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
        detailState={assetDetailState}
        row={assetDetailRow}
        refreshing={registry.isFetching || current.isFetching}
        onClose={closeDetail}
        onRefresh={() => {
          void registry.refetch();
          if (devices.length > 0) void current.refetch();
        }}
      />
      <DeviceDetailDrawer
        site={site}
        principal={principal}
        detailState={deviceDetailState}
        row={deviceDetailRow}
        telemetryClient={telemetryRuntime.client}
        protectedGeneration={protectedGeneration}
        protectedRequestToken={protectedRequestToken}
        registerProtectedResource={registerProtectedResource}
        telemetryRuntime={telemetryRuntime}
        publishRealtimeStatus={publishRealtimeStatus}
        routePolicyRevision={telemetryPolicyRevision}
        refreshing={registry.isFetching || current.isFetching}
        onClose={closeDetail}
        onRefresh={() => {
          void registry.refetch();
          if (devices.length > 0) void current.refetch();
        }}
      />
      </PageScaffold>
    </section>
  );
}
