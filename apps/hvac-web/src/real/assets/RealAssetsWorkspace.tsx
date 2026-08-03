import { useCallback, useEffect, useMemo, useRef, useState, type HTMLAttributes, type Key } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Badge, Button, Card, Col, Empty, Grid, Input, Row, Select, Space, Table, Tag, Tree, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ApartmentOutlined,
  ApiOutlined,
  ClusterOutlined,
  EyeOutlined,
  NodeIndexOutlined,
  ReloadOutlined,
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
import { REAL_ASSETS_CATALOG_REVISION } from './catalog';
import { DeviceDetailDrawer } from './DeviceDetailDrawer';
import {
  REAL_ASSETS_DETAIL_HISTORY_MARKER,
  isRealAssetsDetailHistoryState,
  parseRealAssetsDetailPath,
  realAssetsDevicePath,
  realAssetsListPath,
  resolveRealAssetsDetail,
  writeRealAssetsClipboard,
} from './detail';
import { runRealAssetsProtectedRequest } from './protected-request';
import {
  loadRealAssetsCurrentState,
  loadRealAssetsRegistry,
  realAssetsCurrentStateQueryKey,
  realAssetsRegistryQueryKey,
} from './data';
import {
  buildRealAssetsRows,
  isRealAssetsAttentionState,
  type RealAssetsDeviceRow,
  type RealAssetsOperatingState,
} from './model';
import { projectRealAssetsRealtimeRow } from './realtime';
import {
  createRealAssetsTelemetryRuntime,
  type RealAssetsTelemetryRuntime,
} from './telemetry-runtime';
import { useRealAssetsDeviceRealtime } from './useDeviceRealtime';
import './real-assets.css';

interface RealAssetsWorkspaceProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  requestedDeviceId?: string;
  protectedGeneration: number;
  protectedRequestToken: () => ProtectedScopeRequestToken;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  platformClient?: Pick<PlatformGatewayClient, 'listSiteEquipment' | 'listSiteDevices' | 'listSiteDeviceBindings'>;
  telemetryRuntime?: RealAssetsTelemetryRuntime;
}

type ListMode = 'attention' | 'all';
type HierarchySelection = 'all' | 'unbound' | 'ambiguous' | `equipment:${string}`;

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

function matchesHierarchy(row: RealAssetsDeviceRow, selection: HierarchySelection): boolean {
  if (selection === 'all') return true;
  if (selection === 'unbound') return row.binding.state === 'unbound';
  if (selection === 'ambiguous') return row.binding.state === 'ambiguous';
  return row.binding.state === 'bound' && row.binding.equipment.id === selection.slice('equipment:'.length);
}

function matchesSearch(row: RealAssetsDeviceRow, value: string): boolean {
  const query = value.trim().toLocaleLowerCase('zh-CN');
  if (!query) return true;
  const equipment = row.binding.state === 'bound' ? row.binding.equipment : undefined;
  return [row.device.id, row.device.code, row.device.displayName, row.device.deviceType, equipment?.code, equipment?.displayName]
    .some((candidate) => candidate?.toLocaleLowerCase('zh-CN').includes(query));
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
    detail: 'Registry 设备身份仍然可见，但当前状态服务无法确认。系统不会把服务故障转换为 Device UNKNOWN，也不会回退到 Demo、Legacy 或 ThingsBoard。',
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
  const quality = point.quality === 'SUSPECT' ? '可疑' : '良好';
  const sampledAt = point.sampledAt ? formatSampledAt(point.sampledAt, timeZone) : '采样时间不可用';
  return `${freshness} · ${quality} · ${sampledAt}`;
}

function BindingLabel({ row }: { row: RealAssetsDeviceRow }) {
  if (row.binding.state === 'bound') {
    return (
      <span>
        <strong>{row.binding.equipment.displayName}</strong>
        <small>{row.binding.binding.bindingRole}</small>
      </span>
    );
  }
  if (row.binding.state === 'ambiguous') return <span className="real-assets__binding-warning">绑定关系冲突</span>;
  return <span className="real-assets__muted">未绑定 Equipment</span>;
}

export function RealAssetsWorkspace({
  site,
  principal,
  requestedDeviceId,
  protectedGeneration,
  protectedRequestToken,
  registerProtectedResource,
  platformClient: providedPlatformClient,
  telemetryRuntime: providedTelemetryRuntime,
}: RealAssetsWorkspaceProps) {
  const queryClient = useQueryClient();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const platformClient = useMemo(() => providedPlatformClient ?? createPlatformGatewayClient(), [providedPlatformClient]);
  const telemetryRuntime = useMemo(() => providedTelemetryRuntime ?? createRealAssetsTelemetryRuntime(), [providedTelemetryRuntime]);
  const [listMode, setListMode] = useState<ListMode>('attention');
  const [search, setSearch] = useState('');
  const [hierarchySelection, setHierarchySelection] = useState<HierarchySelection>('all');
  const [telemetryPolicyRevision, setTelemetryPolicyRevision] = useState<string | null>(
    () => telemetryRuntime.currentRoutePolicyRevision(),
  );
  const [routePolicyEpoch, setRoutePolicyEpoch] = useState(0);
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(() => requestedDeviceId ?? null);
  const [actionFeedback, setActionFeedback] = useState<string | null>(null);
  const selectedDeviceIdRef = useRef<string | null>(selectedDeviceId);
  const pendingFocusDeviceIdRef = useRef<string | null>(null);
  const deviceTriggerRefs = useRef(new Map<string, HTMLElement>());
  const organizationId = principal.context.actingOrganizationId;
  const sessionCapability = principal.session.csrfToken;
  const capabilities = principal.authorization.capabilities;
  const registryAllowed = capabilities.includes('equipment.list') && capabilities.includes('device.list');
  const telemetryAllowed = capabilities.includes('telemetry.batch.read');
  const historyAllowed = capabilities.includes('telemetry.history.read');
  const realtimeAllowed = capabilities.includes('telemetry.subscribe');
  const queryRoot = useMemo(
    () => ['real-assets', protectedGeneration, organizationId, site.id] as const,
    [organizationId, protectedGeneration, site.id],
  );

  useEffect(() => {
    selectedDeviceIdRef.current = requestedDeviceId ?? null;
    setSelectedDeviceId(requestedDeviceId ?? null);
  }, [protectedGeneration, requestedDeviceId, site.id]);

  useEffect(() => {
    selectedDeviceIdRef.current = selectedDeviceId;
    setActionFeedback(null);
  }, [selectedDeviceId]);

  useEffect(() => {
    const handlePopState = () => {
      const parsed = parseRealAssetsDetailPath(window.location.pathname, site.id);
      if (parsed.state === 'outside') {
        window.location.reload();
        return;
      }
      const previousDeviceId = selectedDeviceIdRef.current;
      if (parsed.state === 'list') {
        if (previousDeviceId) pendingFocusDeviceIdRef.current = previousDeviceId;
        selectedDeviceIdRef.current = null;
        setSelectedDeviceId(null);
        return;
      }
      selectedDeviceIdRef.current = parsed.deviceId;
      setSelectedDeviceId(parsed.deviceId);
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
      setListMode('attention');
      setSearch('');
      setHierarchySelection('all');
      selectedDeviceIdRef.current = null;
      setSelectedDeviceId(null);
      setActionFeedback(null);
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
    queryKey: realAssetsRegistryQueryKey(protectedGeneration, organizationId, site.id, routePolicyEpoch),
    queryFn: ({ signal }) => {
      const scopeGuard = protectedRequestToken();
      if (scopeGuard.siteId !== site.id || scopeGuard.generation !== protectedGeneration) {
        throw new DOMException('Protected Site scope is not current.', 'AbortError');
      }
      return runRealAssetsProtectedRequest(scopeGuard, signal, (protectedSignal) => loadRealAssetsRegistry({
        client: platformClient,
        organizationId,
        siteId: site.id,
        signal: protectedSignal,
      }));
    },
    enabled: registryAllowed,
    staleTime: 60_000,
    retry: 1,
  });
  const devices = registry.data?.devices ?? [];
  const current = useQuery({
    queryKey: realAssetsCurrentStateQueryKey(protectedGeneration, organizationId, site.id, devices, routePolicyEpoch),
    queryFn: ({ signal }) => {
      const scopeGuard = protectedRequestToken();
      if (scopeGuard.siteId !== site.id || scopeGuard.generation !== protectedGeneration) {
        throw new DOMException('Protected Site scope is not current.', 'AbortError');
      }
      return runRealAssetsProtectedRequest(scopeGuard, signal, (protectedSignal) => loadRealAssetsCurrentState({
        client: telemetryRuntime.client,
        devices,
        organizationId,
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

  const rows = useMemo(() => buildRealAssetsRows({
    devices,
    equipment: registry.data?.equipment ?? [],
    bindings: registry.data?.bindings ?? [],
    snapshots: current.data?.byDeviceId,
  }), [current.data?.byDeviceId, devices, registry.data?.bindings, registry.data?.equipment]);
  const currentPending = telemetryAllowed && devices.length > 0 && current.isPending;
  const currentUnavailable = current.isError;
  const filteredRows = useMemo(() => rows.filter((row) => (
    matchesSearch(row, search)
    && matchesHierarchy(row, hierarchySelection)
    && (listMode === 'all' || currentPending || currentUnavailable || isRealAssetsAttentionState(row.operatingState))
  )), [currentPending, currentUnavailable, hierarchySelection, listMode, rows, search]);

  const counts = useMemo(() => ({
    total: rows.length,
    attention: currentPending || currentUnavailable ? null : rows.filter((row) => isRealAssetsAttentionState(row.operatingState)).length,
    offline: currentPending || currentUnavailable ? null : rows.filter((row) => row.operatingState === 'OFFLINE').length,
    normal: currentPending || currentUnavailable ? null : rows.filter((row) => row.operatingState === 'NORMAL').length,
  }), [currentPending, currentUnavailable, rows]);
  const equipmentCounts = useMemo(() => new Map((registry.data?.equipment ?? []).map((item) => [
    item.id,
    rows.filter((row) => row.binding.state === 'bound' && row.binding.equipment.id === item.id).length,
  ])), [registry.data?.equipment, rows]);
  const hierarchyTree = useMemo(() => [{
    key: 'all',
    title: (
      <span data-testid="real-assets-hierarchy-all">
        {site.displayName} · {rows.length}
      </span>
    ),
    children: [
      ...(registry.data?.equipment ?? []).map((item) => ({
        key: `equipment:${item.id}`,
        title: (
          <span data-testid="real-assets-hierarchy-equipment" data-equipment-id={item.id}>
            {item.displayName} · {equipmentCounts.get(item.id) ?? 0}
          </span>
        ),
      })),
      {
        key: 'unbound',
        title: (
          <span data-testid="real-assets-hierarchy-unbound">
            未绑定 Equipment · {rows.filter((row) => row.binding.state === 'unbound').length}
          </span>
        ),
      },
      {
        key: 'ambiguous',
        title: (
          <span data-testid="real-assets-hierarchy-ambiguous">
            绑定关系冲突 · {rows.filter((row) => row.binding.state === 'ambiguous').length}
          </span>
        ),
      },
    ],
  }], [equipmentCounts, registry.data?.equipment, rows, site.displayName]);
  const assetColumns = useMemo<ColumnsType<RealAssetsDeviceRow>>(() => {
    const columns: ColumnsType<RealAssetsDeviceRow> = [
      {
        title: '设备',
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
                <Tag>{row.device.deviceType}</Tag>
              </Space>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.device.code}</Typography.Text>
            </Space>
          </Button>
        ),
      },
      {
        title: '位置 / Equipment',
        key: 'equipment',
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
              <Typography.Text>{total > 0 ? `${available} / ${total}` : '未配置'}</Typography.Text>
              <Typography.Text type={rate !== null && rate < 100 ? 'warning' : 'secondary'} style={{ fontSize: 12 }}>
                {rate === null ? '关键点位目录待配置' : `${rate}% 可用`}
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
            aria-expanded={selectedDeviceId === row.device.id}
            onClick={() => openDeviceDetail(row.device.id)}
          >
            详情
          </Button>
        ),
      },
    ];
    return compactTable
      ? columns.filter((column) => ['device', 'equipment', 'state', 'points', 'action'].includes(String(column.key)))
      : columns;
  }, [compactTable, currentPending, currentUnavailable, selectedDeviceId, site.timezone]);
  const detailResolution = useMemo(() => resolveRealAssetsDetail(rows, selectedDeviceId), [rows, selectedDeviceId]);
  const detailRow = detailResolution.state === 'visible' ? detailResolution.row : null;
  const onRealtimeRevoked = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: queryRoot });
  }, [queryClient, queryRoot]);
  const realtime = useRealAssetsDeviceRealtime({
    row: detailRow,
    allowed: realtimeAllowed,
    protectedGeneration,
    authorizationEpoch: `${principal.session.id}:${principal.authorization.policyRevision}:${routePolicyEpoch}`,
    runtime: telemetryRuntime,
    protectedRequestToken,
    registerProtectedResource,
    onRevoked: onRealtimeRevoked,
  });
  const realtimeProjection = useMemo(
    () => detailRow ? projectRealAssetsRealtimeRow(detailRow, realtime.state) : null,
    [detailRow, realtime.state],
  );
  const drawerResolution = detailResolution.state === 'visible' && realtimeProjection
    ? { state: 'visible' as const, row: realtimeProjection.row }
    : detailResolution;

  useEffect(() => {
    if (selectedDeviceId !== null) return;
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
  }, [filteredRows, selectedDeviceId]);

  const openDeviceDetail = (deviceId: string) => {
    const target = realAssetsDevicePath(site.id, deviceId);
    const historyState = { marker: REAL_ASSETS_DETAIL_HISTORY_MARKER, siteId: site.id, deviceId };
    if (selectedDeviceIdRef.current) window.history.replaceState(historyState, '', target);
    else window.history.pushState(historyState, '', target);
    pendingFocusDeviceIdRef.current = deviceId;
    selectedDeviceIdRef.current = deviceId;
    setSelectedDeviceId(deviceId);
  };

  const closeDeviceDetail = () => {
    const deviceId = selectedDeviceIdRef.current;
    if (!deviceId) return;
    pendingFocusDeviceIdRef.current = deviceId;
    if (isRealAssetsDetailHistoryState(window.history.state, site.id, deviceId)) {
      window.history.back();
      return;
    }
    window.history.pushState(null, '', realAssetsListPath(site.id));
    selectedDeviceIdRef.current = null;
    setSelectedDeviceId(null);
  };

  const copyDetailValue = async (label: string, value: string) => {
    const writer = navigator.clipboard?.writeText
      ? navigator.clipboard.writeText.bind(navigator.clipboard)
      : undefined;
    const copied = await writeRealAssetsClipboard(writer, value);
    setActionFeedback(copied ? `${label}已复制。` : `${label}复制失败；浏览器未授予剪贴板权限。`);
  };

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
            description="当前 Principal 缺少 Equipment、Device 或 Telemetry batch read 的服务器能力投影。此状态不会尝试调用受保护数据接口。"
            data-retryable="false"
          />
        </PageScaffold>
      </section>
    );
  }

  if (registry.isPending) {
    return (
      <section className="real-assets" data-testid="real-site-route-assets" data-business-state="LOADING" data-site-id={site.id}>
        <PageScaffold
          title="设备与建筑"
          heading={<FocusHeading className="ops-page-title ant-typography"><Space><ApartmentOutlined />设备与建筑</Space></FocusHeading>}
          extra={<Tag color="processing">LOADING</Tag>}
          className="assets-page"
        >
          <Card variant="borderless"><div className="real-shell-progress" role="status" aria-live="polite">正在读取授权 Equipment、Device 与 DeviceBinding…</div></Card>
        </PageScaffold>
      </section>
    );
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
      data-total-device-count={String(rows.length)}
      data-filtered-device-count={String(filteredRows.length)}
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
        <Typography.Text type="secondary">{site.displayName} · 仅展示当前授权 Site 的 Registry 与 S2 当前状态。 · Acting Organization: {organizationId}</Typography.Text>
        <OperationsMetrics
          ariaLabel="Site 设备运行摘要"
          items={[
            { key: 'total', label: '可见 Device', value: counts.total, detail: 'Registry 授权集合', tone: 'accent' },
            { key: 'attention', label: '需关注', value: counts.attention ?? '—', detail: '离线、未知或关键点异常', tone: counts.attention ? 'warning' : 'positive' },
            { key: 'offline', label: '离线', value: counts.offline ?? '—', detail: '权威 Presence OFFLINE', tone: counts.offline ? 'critical' : 'default' },
            { key: 'normal', label: '正常', value: counts.normal ?? '—', detail: '当前状态正常', tone: 'positive' },
          ]}
        />

      {devices.length === 0 ? (
        <div className="real-assets__empty" role="status">
          当前 Site 尚未登记 Device。此状态来自成功的空 Registry 集合，不代表权限拒绝或服务不可用。
        </div>
      ) : null}
      {currentPending ? (
        <div className="real-assets__notice" role="status">已建立 Registry 身份，正在按最多 100 Device 的批次读取关键点位 Snapshot。</div>
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

      {devices.length > 0 ? (
        <Row gutter={[16, 16]} className="real-assets__workspace">
          <Col xs={24} lg={7} xl={6}>
            <Card
              variant="borderless"
              title={<OperationsPanelHeading icon={<ClusterOutlined />} title="建筑设备树" />}
              className="assets-hierarchy-card"
              styles={{ body: { padding: 12 } }}
            >
              <Tree
                defaultExpandAll
                blockNode
                treeData={hierarchyTree}
                selectedKeys={[hierarchySelection]}
                onSelect={(keys: Key[]) => {
                  const selected = String(keys[0] ?? 'all') as HierarchySelection;
                  setHierarchySelection(selected);
                }}
              />
            </Card>
          </Col>
          <Col xs={24} lg={17} xl={18}>
            <Card variant="borderless" className="assets-ledger-card" styles={{ body: { padding: 16 } }}>
              <Space direction="vertical" size={12} style={{ width: '100%' }}>
                <div className="ops-toolbar">
                  <OperationsPanelHeading icon={<NodeIndexOutlined />} title="设备台账" meta={`${filteredRows.length} 台`} />
                  <Space wrap>
                    <Input
                      allowClear
                      type="search"
                      data-testid="real-assets-search"
                      placeholder="搜索设备或 Equipment"
                      value={search}
                      onChange={(event) => setSearch(event.currentTarget.value)}
                      style={{ width: 260 }}
                    />
                    <Select
                      value={listMode}
                      onChange={setListMode}
                      options={[
                        { label: '需关注', value: 'attention' },
                        { label: '全部 Device', value: 'all' },
                      ]}
                      style={{ width: 130 }}
                    />
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
                      全部 Device
                    </Button>
                  </Space>
                </div>

                <div data-testid="real-assets-table-wrap">
                  <table className="real-assets__table real-shell-sr-only" aria-label="完整授权 Device 运行投影">
                    <tbody>
                      {filteredRows.map((row) => (
                        <tr
                          key={row.device.id}
                          data-device-id={row.device.id}
                          data-operating-state={currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operatingState}
                        >
                          <td>{row.device.displayName} {row.device.code} {row.device.deviceType}</td>
                          <td>{row.binding.state === 'bound' ? row.binding.equipment.displayName : row.binding.state === 'ambiguous' ? '绑定关系冲突' : '未绑定 Equipment'}</td>
                          <td>{currentUnavailable ? '状态不可用' : currentPending ? '读取中' : OPERATING_LABELS[row.operatingState]}</td>
                          <td>
                            <ul className="real-assets__points">
                              {row.points.map((point) => (
                                <li key={point.key}>{point.label} {point.displayValue}{point.unit ? ` ${point.unit}` : ''}</li>
                              ))}
                            </ul>
                          </td>
                          <td>{row.device.status} Revision {row.device.revision}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  <Table<RealAssetsDeviceRow>
                    rowKey={(row) => row.device.id}
                    size="middle"
                    columns={assetColumns}
                    dataSource={filteredRows}
                    pagination={{ pageSize: 8, showSizeChanger: false }}
                    scroll={{ x: compactTable ? 820 : 1240 }}
                    locale={{
                      emptyText: (
                        <Empty
                          image={Empty.PRESENTED_IMAGE_SIMPLE}
                          description={listMode === 'attention' && !currentPending && !currentUnavailable && rows.every((row) => row.operatingState === 'NORMAL')
                            ? '当前筛选范围内所有 Device 均为正常。切换到“全部 Device”可查看完整列表。'
                            : '当前筛选条件没有匹配的 Device。'}
                        />
                      ),
                    }}
                    onRow={(row) => ({
                      'data-device-id': row.device.id,
                      'data-operating-state': currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operatingState,
                    } as HTMLAttributes<HTMLTableRowElement>)}
                  />
                </div>
              </Space>
            </Card>
          </Col>
        </Row>
      ) : null}
      <DeviceDetailDrawer
        site={site}
        resolution={drawerResolution}
        currentPending={currentPending}
        currentUnavailable={currentUnavailable}
        refreshing={registry.isFetching || current.isFetching}
        routePolicyRevision={current.data?.routePolicyRevision ?? telemetryPolicyRevision}
        principal={principal}
        client={telemetryRuntime.client}
        protectedGeneration={protectedGeneration}
        protectedRequestToken={protectedRequestToken}
        historyAllowed={historyAllowed}
        sessionCapability={sessionCapability}
        realtime={realtime}
        realtimeProjection={realtimeProjection}
        actionFeedback={actionFeedback}
        onClose={closeDeviceDetail}
        onRefresh={() => {
          void registry.refetch();
          if (devices.length > 0) void current.refetch();
        }}
        onCopyDeviceId={() => {
          if (detailResolution.state === 'visible') {
            void copyDetailValue('Device ID', detailResolution.row.device.id);
          }
        }}
        onCopyDeepLink={() => {
          if (detailResolution.state === 'visible') {
            const deepLink = new URL(realAssetsDevicePath(site.id, detailResolution.row.device.id), window.location.origin).toString();
            void copyDetailValue('当前深链', deepLink);
          }
        }}
      />
      </PageScaffold>
    </section>
  );
}
