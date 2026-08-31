import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router';
import { Alert, Button, Card, Col, Grid, Input, Row, Segmented, Space, Tag, Typography } from 'antd';
import { ApartmentOutlined, ClusterOutlined, NodeIndexOutlined, ReloadOutlined } from '@ant-design/icons';
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
} from './model';
import { AssetTable } from './AssetTable';
import { AssetsNavigation } from './AssetsNavigation';
import { DeviceTable } from './DeviceTable';
import {
  filterRealAssetsAssetRows,
  filterRealAssetsDeviceRows,
  indexRealAssetsHierarchy,
  summarizeRealAssetsDevices,
  type RealAssetsListMode,
} from './workspace-selectors';
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

type LedgerMode = 'devices' | 'asset';
type HierarchySelection = string;

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
  const [listMode, setListMode] = useState<RealAssetsListMode>('all');
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
  const hierarchyIndex = useMemo(() => indexRealAssetsHierarchy(hierarchyRoot), [hierarchyRoot]);
  const selectedHierarchy = hierarchyIndex.get(hierarchySelection);
  const selectedDeviceIds = useMemo(
    () => selectedHierarchy ? new Set(selectedHierarchy.deviceIds) : undefined,
    [selectedHierarchy],
  );
  const currentPending = telemetryAllowed && devices.length > 0 && current.isPending;
  const currentUnavailable = current.isError;
  const filteredRows = useMemo(() => filterRealAssetsDeviceRows({
    rows,
    search,
    selectedDeviceIds,
    listMode,
    currentPending,
    currentUnavailable,
  }), [currentPending, currentUnavailable, listMode, rows, search, selectedDeviceIds]);
  const filteredAssetRows = useMemo(() => filterRealAssetsAssetRows({
    rows: assetRows,
    search,
    selectedHierarchy,
    selectedDeviceIds,
    listMode,
    currentPending,
    currentUnavailable,
  }), [assetRows, currentPending, currentUnavailable, listMode, search, selectedDeviceIds, selectedHierarchy]);

  const selectedAssetId = selectedDetail?.kind === 'asset' ? selectedDetail.id : null;
  const selectedDeviceId = selectedDetail?.kind === 'device' ? selectedDetail.id : null;
  const counts = useMemo(
    () => summarizeRealAssetsDevices(rows, currentPending, currentUnavailable),
    [currentPending, currentUnavailable, rows],
  );
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
              <AssetsNavigation
                siteId={site.id}
                root={hierarchyRoot}
                selectedKey={hierarchySelection}
                onSelect={setHierarchySelection}
              />
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
                    <AssetTable
                      rows={filteredAssetRows}
                      selectedAssetId={selectedAssetId}
                      onOpen={openAssetDetail}
                      registerTrigger={(assetId, node) => {
                        if (node) assetTriggerRefs.current.set(assetId, node);
                        else assetTriggerRefs.current.delete(assetId);
                      }}
                    />
                  ) : (
                    <DeviceTable
                      rows={filteredRows}
                      allRows={rows}
                      compact={compactTable}
                      currentPending={currentPending}
                      currentUnavailable={currentUnavailable}
                      listMode={listMode}
                      selectedDeviceId={selectedDeviceId}
                      timeZone={site.timezone}
                      onOpen={openDeviceDetail}
                      registerTrigger={(deviceId, node) => {
                        if (node) deviceTriggerRefs.current.set(deviceId, node);
                        else deviceTriggerRefs.current.delete(deviceId);
                      }}
                    />
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
