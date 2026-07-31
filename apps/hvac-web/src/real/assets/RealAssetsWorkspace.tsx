import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
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
  type RealAssetsAttentionReason,
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

const ATTENTION_REASON_LABELS: Record<RealAssetsAttentionReason, string> = {
  CURRENT_STATE_UNAVAILABLE: '当前状态不可用',
  CURRENT_STATE_NOT_VISIBLE: '当前状态不可见',
  POINT_CATALOG_CONTRACT_DRIFT: '关键点位目录与公共契约不一致',
  POINT_CATALOG_UNCONFIGURED: '关键点位目录未配置',
  PRESENCE_UNKNOWN: 'Presence 未确定',
  PRESENCE_OFFLINE: 'Presence 离线',
  TELEMETRY_STALE: '存在陈旧数据',
  TELEMETRY_SUSPECT: '存在可疑数据',
  CRITICAL_POINT_MISSING: '关键点位缺失',
  TELEMETRY_INCOMPLETE: '当前状态不完整',
};

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

function OperatingState({ row, pending, unavailable }: { row: RealAssetsDeviceRow; pending: boolean; unavailable: boolean }) {
  if (pending) return <span className="real-assets__state real-assets__state--loading">读取中</span>;
  if (unavailable) return <span className="real-assets__state real-assets__state--unavailable">状态服务不可用</span>;
  const ownerDisplayState = row.snapshotResult?.status === 'ok' ? row.snapshotResult.snapshot.displayState : null;
  return (
    <div className="real-assets__operating-state">
      <span className={`real-assets__state real-assets__state--${row.operatingState.toLowerCase()}`}>
        {OPERATING_LABELS[row.operatingState]}
      </span>
      <small className="real-assets__owner-display">
        S2 display: {ownerDisplayState ? S2_DISPLAY_LABELS[ownerDisplayState] : '不适用'}
      </small>
      {row.attentionReasons.length > 0 ? (
        <ul aria-label="关注原因">
          {row.attentionReasons.map((reason) => <li key={reason}>{ATTENTION_REASON_LABELS[reason]}</li>)}
        </ul>
      ) : null}
    </div>
  );
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

function PointSummary({
  row,
  pending,
  unavailable,
  timeZone,
}: {
  row: RealAssetsDeviceRow;
  pending: boolean;
  unavailable: boolean;
  timeZone: string;
}) {
  if (pending) return <span className="real-assets__muted">正在读取关键点位…</span>;
  if (unavailable) return <span className="real-assets__muted">当前点位状态不可用</span>;
  if (row.profile.state === 'unconfigured') {
    return <span className="real-assets__profile-unconfigured">未配置此 Device 类型的关键点位目录</span>;
  }
  return (
    <ul className="real-assets__points" aria-label={`${row.device.displayName} 关键点位`}>
      {row.points.map((point) => (
        <li
          key={point.key}
          data-point-state={point.state}
          data-point-freshness={point.freshness}
          data-point-quality={point.quality ?? 'NONE'}
        >
          <span>{point.label}</span>
          <strong>{point.displayValue}{point.unit ? ` ${point.unit}` : ''}</strong>
          <small>{pointEvidence(point, timeZone)}</small>
        </li>
      ))}
    </ul>
  );
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
  const deviceTriggerRefs = useRef(new Map<string, HTMLButtonElement>());
  const organizationId = principal.context.actingOrganizationId;
  const sessionCapability = principal.session.csrfToken;
  const capabilities = principal.authorization.capabilities;
  const registryAllowed = capabilities.includes('equipment.list') && capabilities.includes('device.list');
  const telemetryAllowed = capabilities.includes('telemetry.batch.read');
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
        <p className="real-shell-eyebrow">REAL MODE · SITE ASSETS</p>
        <FocusHeading>资产运行工作台不可用</FocusHeading>
        <div className="real-shell-problem" role="alert" data-retryable="false">
          <span>当前 Principal 缺少 Equipment、Device 或 Telemetry batch read 的服务器能力投影。此状态不会尝试调用受保护数据接口。</span>
        </div>
      </section>
    );
  }

  if (registry.isPending) {
    return (
      <section className="real-assets" data-testid="real-site-route-assets" data-business-state="LOADING" data-site-id={site.id}>
        <p className="real-shell-eyebrow">REAL MODE · SITE ASSETS</p>
        <FocusHeading>资产运行工作台</FocusHeading>
        <div className="real-shell-progress" role="status" aria-live="polite">正在读取授权 Equipment、Device 与 DeviceBinding…</div>
      </section>
    );
  }

  if (registry.isError) {
    const failure = presentRegistryError(registry.error);
    return (
      <section className="real-assets" data-testid="real-site-route-assets" data-business-state="REGISTRY_UNAVAILABLE" data-site-id={site.id}>
        <p className="real-shell-eyebrow">REAL MODE · SITE ASSETS</p>
        <FocusHeading>{failure.title}</FocusHeading>
        <div className="real-shell-problem" role="alert" data-retryable={String(failure.retryable)}>
          <span>{failure.description}</span>
          {failure.traceId ? <code>traceId {failure.traceId}</code> : null}
        </div>
        {failure.retryable ? <button type="button" onClick={() => { void registry.refetch(); }}>重试 Registry</button> : null}
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
    >
      <header className="real-assets__header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · SITE ASSETS</p>
          <FocusHeading id="real-assets-title">资产运行工作台</FocusHeading>
          <p>{site.displayName} · 仅展示当前授权 Site 的 Registry 与 S2 当前状态。</p>
          <small>Acting Organization: {organizationId}</small>
        </div>
        <button
          type="button"
          onClick={() => {
            void registry.refetch();
            if (devices.length > 0) void current.refetch();
          }}
          disabled={registry.isFetching || current.isFetching}
        >
          {registry.isFetching || current.isFetching ? '刷新中…' : '刷新'}
        </button>
      </header>

      <dl className="real-assets__metrics" aria-label="Site 设备运行摘要">
        <div><dt>可见 Device</dt><dd>{counts.total}</dd></div>
        <div><dt>需关注</dt><dd>{counts.attention ?? '—'}</dd></div>
        <div><dt>离线</dt><dd>{counts.offline ?? '—'}</dd></div>
        <div><dt>正常</dt><dd>{counts.normal ?? '—'}</dd></div>
      </dl>

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
        <div className="real-assets__workspace">
          <aside className="real-assets__hierarchy" aria-label="Site Equipment hierarchy">
            <h2>资产层级</h2>
            <button type="button" className={hierarchySelection === 'all' ? 'is-active' : ''} onClick={() => setHierarchySelection('all')}>
              {site.displayName}<span>{rows.length}</span>
            </button>
            {(registry.data?.equipment ?? []).map((item) => (
              <button
                key={item.id}
                type="button"
                className={hierarchySelection === `equipment:${item.id}` ? 'is-active' : ''}
                onClick={() => setHierarchySelection(`equipment:${item.id}`)}
              >
                {item.displayName}<span>{equipmentCounts.get(item.id) ?? 0}</span>
              </button>
            ))}
            <button type="button" className={hierarchySelection === 'unbound' ? 'is-active' : ''} onClick={() => setHierarchySelection('unbound')}>
              未绑定 Equipment<span>{rows.filter((row) => row.binding.state === 'unbound').length}</span>
            </button>
            <button type="button" className={hierarchySelection === 'ambiguous' ? 'is-active' : ''} onClick={() => setHierarchySelection('ambiguous')}>
              绑定关系冲突<span>{rows.filter((row) => row.binding.state === 'ambiguous').length}</span>
            </button>
          </aside>

          <div className="real-assets__ledger">
            <form className="real-assets__filters" aria-label="Device 筛选" onSubmit={(event) => event.preventDefault()}>
              <fieldset>
                <legend>列表范围</legend>
                <label><input type="radio" name="assets-list-mode" checked={listMode === 'attention'} onChange={() => setListMode('attention')} />需关注</label>
                <label><input type="radio" name="assets-list-mode" checked={listMode === 'all'} onChange={() => setListMode('all')} />全部 Device</label>
              </fieldset>
              <label>
                搜索 Device 或 Equipment
                <input type="search" value={search} onChange={(event) => setSearch(event.currentTarget.value)} />
              </label>
            </form>

            {filteredRows.length === 0 ? (
              <div className="real-assets__empty" role="status">
                {listMode === 'attention' && !currentPending && !currentUnavailable && rows.every((row) => row.operatingState === 'NORMAL')
                  ? '当前筛选范围内所有 Device 均为正常。切换到“全部 Device”可查看完整列表。'
                  : '当前筛选条件没有匹配的 Device。'}
              </div>
            ) : (
              <div className="real-assets__table-wrap">
                <table className="real-assets__table">
                  <caption>授权 Device 运行列表</caption>
                  <thead>
                    <tr><th scope="col">Device</th><th scope="col">Equipment</th><th scope="col">运行状态</th><th scope="col">关键点位</th><th scope="col">Registry</th></tr>
                  </thead>
                  <tbody>
                    {filteredRows.map((row) => (
                      <tr
                        key={row.device.id}
                        data-device-id={row.device.id}
                        data-operating-state={currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operatingState}
                      >
                        <th scope="row">
                          <button
                            type="button"
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
                            <strong>{row.device.displayName}</strong>
                            <span>{row.device.code} · {row.device.deviceType}</span>
                            <code>{row.device.id}</code>
                          </button>
                        </th>
                        <td><BindingLabel row={row} /></td>
                        <td><OperatingState row={row} pending={currentPending} unavailable={currentUnavailable} /></td>
                        <td><PointSummary row={row} pending={currentPending} unavailable={currentUnavailable} timeZone={site.timezone} /></td>
                        <td>
                          <span className={`real-assets__lifecycle real-assets__lifecycle--${row.device.status.toLowerCase()}`}>{row.device.status}</span>
                          <small>rev {row.device.revision}</small>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      ) : null}
      <DeviceDetailDrawer
        site={site}
        resolution={drawerResolution}
        currentPending={currentPending}
        currentUnavailable={currentUnavailable}
        refreshing={registry.isFetching || current.isFetching}
        routePolicyRevision={current.data?.routePolicyRevision ?? telemetryPolicyRevision}
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
    </section>
  );
}
