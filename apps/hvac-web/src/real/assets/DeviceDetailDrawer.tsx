import { lazy, Suspense, useEffect, useRef } from 'react';
import type { CurrentPrincipalResponse, Site } from '../../api/generated/platformGateway.gen.ts';
import type { DeviceObservationSnapshot, S2TelemetryClient } from '../../api/generated/s2Telemetry.gen.ts';
import type { ProtectedScopeRequestToken } from '../protected-scope.ts';
import { REAL_ASSETS_CATALOG_REVISION } from './catalog.ts';
import type { RealAssetsDetailResolution } from './detail.ts';
import type { RealAssetsDeviceRow, RealAssetsPointView } from './model.ts';
import { DeviceRealtimeStatus } from './DeviceRealtimeStatus.tsx';
import type { RealAssetsRealtimeProjection } from './realtime.ts';
import type { RealAssetsRealtimeResult } from './useDeviceRealtime.ts';

const DeviceHistoryTrends = lazy(async () => {
  const module = await import('./DeviceHistoryTrends.tsx');
  return { default: module.DeviceHistoryTrends };
});

interface DeviceDetailDrawerProps {
  readonly site: Readonly<Site>;
  readonly resolution: RealAssetsDetailResolution;
  readonly currentPending: boolean;
  readonly currentUnavailable: boolean;
  readonly refreshing: boolean;
  readonly routePolicyRevision: string | null;
  readonly principal: CurrentPrincipalResponse;
  readonly client: S2TelemetryClient;
  readonly protectedGeneration: number;
  readonly protectedRequestToken: () => ProtectedScopeRequestToken;
  readonly historyAllowed: boolean;
  readonly sessionCapability: string;
  readonly realtime: RealAssetsRealtimeResult;
  readonly realtimeProjection: RealAssetsRealtimeProjection | null;
  readonly actionFeedback: string | null;
  readonly onClose: () => void;
  readonly onRefresh: () => void;
  readonly onCopyDeviceId: () => void;
  readonly onCopyDeepLink: () => void;
}

const PRESENCE_LABELS = {
  ONLINE: '在线',
  OFFLINE: '离线',
  UNKNOWN: '未知',
} as const;

const DISPLAY_LABELS = {
  ONLINE: '在线',
  OFFLINE: '离线',
  STALE: '陈旧',
  UNKNOWN: '未知',
  UNAVAILABLE: '不可用',
} as const;

function formatInstant(value: string | null | undefined, timeZone: string): string {
  if (!value) return '不可用';
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return '不可用';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(parsed);
}

function pointStatus(point: RealAssetsPointView, timeZone: string): string {
  if (point.state === 'MISSING') {
    if (point.missingReason === 'ONLY_REJECTED_CANDIDATES') return '候选观测均未被接受';
    if (point.missingReason === 'POLICY_NOT_CONFIGURED') return '遥测策略未配置';
    return '尚无已接受观测';
  }
  const freshness = point.freshness === 'STALE' ? '陈旧' : '新鲜';
  const quality = point.quality === 'SUSPECT' ? '可疑' : '良好';
  return `${freshness} · ${quality} · 采样 ${formatInstant(point.sampledAt, timeZone)} · 接收 ${formatInstant(point.receivedAt, timeZone)}`;
}

function BindingFacts({ row }: { row: RealAssetsDeviceRow }) {
  const bindings = row.binding.state === 'bound'
    ? [row.binding]
    : row.binding.state === 'multi-bound'
      ? row.binding.bindings
      : [];
  const bindingInvalid = row.binding.state === 'unbound' || row.binding.state === 'ambiguous';
  return (
    <>
      <dl className="real-assets-detail__facts">
        <div><dt>Area</dt><dd>{row.area.state === 'bound' ? row.area.area.displayName : row.area.state === 'ambiguous' ? 'Area 关系冲突' : '未绑定 Area'}</dd></div>
        <div><dt>Area code</dt><dd>{row.area.state === 'bound' ? row.area.area.code : '—'}</dd></div>
        <div><dt>Equipment</dt><dd>{bindings.length > 0 ? bindings.map((item) => item.equipment.displayName).join('、') : row.binding.state === 'ambiguous' ? 'Equipment 关系冲突' : '未绑定 Equipment'}</dd></div>
        <div><dt>Equipment code</dt><dd>{bindings.length > 0 ? bindings.map((item) => item.equipment.code).join('、') : '—'}</dd></div>
        <div><dt>Binding role</dt><dd>{bindings.length > 0 ? bindings.map((item) => item.relationship.role).join('、') : '—'}</dd></div>
        <div><dt>Binding revision</dt><dd>{bindings.length > 0 ? bindings.map((item) => item.relationship.revision).join('、') : '—'}</dd></div>
        <div><dt>Registered Points</dt><dd>{row.registeredPointCount}</dd></div>
      </dl>
      {bindingInvalid || row.area.state !== 'bound' ? (
        <div className="real-assets-detail__notice" role="status">
          原子 Asset Model 未能建立唯一的 Area / Equipment 层级；详情不会猜测归属。
        </div>
      ) : null}
    </>
  );
}

function SnapshotFacts({ snapshot, site, routePolicyRevision }: {
  snapshot: DeviceObservationSnapshot;
  site: Readonly<Site>;
  routePolicyRevision: string | null;
}) {
  const currentState = snapshot.presence.currentState;
  const presence = currentState ? PRESENCE_LABELS[currentState] : '不适用';
  const displayState = snapshot.displayState;
  const display = displayState ? DISPLAY_LABELS[displayState] : '不适用';
  return (
    <>
      <dl className="real-assets-detail__facts">
        <div><dt>Presence</dt><dd>{presence}</dd></div>
        <div><dt>Presence applicability</dt><dd>{snapshot.presence.applicability}</dd></div>
        <div><dt>Last seen</dt><dd>{formatInstant(snapshot.presence.lastSeenAt, site.timezone)}</dd></div>
        <div><dt>Telemetry readiness</dt><dd>{snapshot.telemetryReadiness}</dd></div>
        <div><dt>S2 display</dt><dd>{display}</dd></div>
        <div><dt>Evaluation watermark</dt><dd>{formatInstant(snapshot.evaluatedAt, site.timezone)}</dd></div>
        <div><dt>Business revision</dt><dd>{snapshot.businessRevision}</dd></div>
        <div><dt>Presence policy revision</dt><dd>{snapshot.presence.policyRevision}</dd></div>
        <div><dt>Route policy revision</dt><dd>{routePolicyRevision ?? '不可用'}</dd></div>
      </dl>
      {snapshot.evaluationAvailability !== 'AVAILABLE' ? (
        <div className="real-assets-detail__notice real-assets-detail__notice--warning" role="status">
          当前状态评估不可用：{snapshot.availabilityReasons.join('、') || '未提供原因'}。
        </div>
      ) : null}
    </>
  );
}

function PointDetails({ row, site }: { row: RealAssetsDeviceRow; site: Readonly<Site> }) {
  if (row.snapshotResult?.status !== 'ok') {
    return (
      <div className="real-assets-detail__notice real-assets-detail__notice--warning" role="status">
        当前关键点位无法建立；不会用零值、历史值或空列表替代失败的 Snapshot。
      </div>
    );
  }
  if (row.profile.state === 'unconfigured') {
    return (
      <div className="real-assets-detail__notice" role="status">
        Device type `{row.device.deviceType}` 尚无版本化关键点位 profile。Registry、Presence 与通用当前状态事实仍然保留。
      </div>
    );
  }
  return (
    <ul className="real-assets-detail__points" aria-label={`${row.device.displayName} 当前关键点位`}>
      {row.points.map((point) => (
        <li
          key={point.key}
          data-point-state={point.state}
          data-point-freshness={point.freshness}
          data-point-quality={point.quality ?? 'NONE'}
        >
          <div>
            <strong>{point.label}</strong>
            <code>{point.key}</code>
          </div>
          <span>{point.displayValue}{point.unit ? ` ${point.unit}` : ''}</span>
          <small>{pointStatus(point, site.timezone)}</small>
          <small>Point policy revision: {point.policyRevision ?? '不可用'}</small>
          {point.qualityReasons.length > 0 ? <small>Quality reasons: {point.qualityReasons.join('、')}</small> : null}
        </li>
      ))}
    </ul>
  );
}

export function DeviceDetailDrawer({
  site,
  resolution,
  currentPending,
  currentUnavailable,
  refreshing,
  routePolicyRevision,
  principal,
  client,
  protectedGeneration,
  protectedRequestToken,
  historyAllowed,
  sessionCapability,
  realtime,
  realtimeProjection,
  actionFeedback,
  onClose,
  onRefresh,
  onCopyDeviceId,
  onCopyDeepLink,
}: DeviceDetailDrawerProps) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusKey = resolution.state === 'visible' ? resolution.row.device.id : resolution.state;

  useEffect(() => {
    headingRef.current?.focus({ preventScroll: true });
  }, [focusKey]);

  useEffect(() => {
    if (resolution.state === 'closed') return undefined;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      onClose();
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose, resolution.state]);

  if (resolution.state === 'closed') return null;

  const visible = resolution.state === 'visible';
  const row = visible ? resolution.row : undefined;
  const snapshot = row?.snapshotResult?.status === 'ok' ? row.snapshotResult.snapshot : undefined;

  return (
    <aside
      className="real-assets-detail"
      role="dialog"
      aria-modal="false"
      aria-labelledby="real-assets-detail-title"
      data-testid="real-assets-device-detail"
      data-detail-state={resolution.state}
    >
      <header className="real-assets-detail__header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · DEVICE CURRENT STATE</p>
          <h2 id="real-assets-detail-title" ref={headingRef} tabIndex={-1}>
            {row?.device.displayName ?? '设备不可见'}
          </h2>
          <p>{row ? `${row.device.code} · ${row.device.deviceType}` : '当前 Principal、Organization 与 Site 范围内无法验证该 Device。'}</p>
        </div>
        <button type="button" data-testid="real-assets-detail-close" onClick={onClose} aria-label="关闭设备详情">关闭</button>
      </header>

      {!row ? (
        <div className="real-assets-detail__not-visible" role="status">
          未知、格式无效、其他 Site 或未授权 Device 使用同一非枚举状态。页面不会确认对象是否存在。
        </div>
      ) : (
        <div className="real-assets-detail__body">
          <div className="real-assets-detail__actions" aria-label="设备详情操作">
            <button type="button" data-testid="real-assets-detail-refresh" onClick={onRefresh} disabled={refreshing}>{refreshing ? '刷新中…' : '刷新当前状态'}</button>
            <button type="button" data-testid="real-assets-detail-copy-id" onClick={onCopyDeviceId}>复制 Device ID</button>
            <button type="button" data-testid="real-assets-detail-copy-link" onClick={onCopyDeepLink}>复制当前深链</button>
          </div>
          {actionFeedback ? <div className="real-assets-detail__feedback" role="status">{actionFeedback}</div> : null}

          <section aria-labelledby="real-assets-detail-identity">
            <h3 id="real-assets-detail-identity">Registry 身份</h3>
            <dl className="real-assets-detail__facts">
              <div><dt>Device ID</dt><dd><code>{row.device.id}</code></dd></div>
              <div><dt>Device code</dt><dd>{row.device.code}</dd></div>
              <div><dt>Device type</dt><dd>{row.device.deviceType}</dd></div>
              <div><dt>Lifecycle</dt><dd>{row.device.status}</dd></div>
              <div><dt>Device revision</dt><dd>{row.device.revision}</dd></div>
              <div><dt>Owning Organization</dt><dd><code>{row.device.owningOrganizationId}</code></dd></div>
              <div><dt>Registry Site</dt><dd><code>{row.device.siteId}</code></dd></div>
              <div><dt>Point catalog revision</dt><dd>{REAL_ASSETS_CATALOG_REVISION}</dd></div>
            </dl>
          </section>

          <section aria-labelledby="real-assets-detail-hierarchy">
            <h3 id="real-assets-detail-hierarchy">层级上下文</h3>
            <BindingFacts row={row} />
          </section>

          <DeviceRealtimeStatus realtime={realtime} projection={realtimeProjection} site={site} />

          <section aria-labelledby="real-assets-detail-current">
            <h3 id="real-assets-detail-current">权威当前状态</h3>
            {currentPending && !snapshot ? <div className="real-assets-detail__notice" role="status">正在读取当前 Snapshot…</div> : null}
            {currentUnavailable ? (
              <div className="real-assets-detail__notice real-assets-detail__notice--warning" role="alert">
                {snapshot
                  ? 'Site 列表的 Current batch 暂不可用；详情仍展示 exact-key 实时会话最近一次权威 Snapshot。'
                  : 'Registry 身份仍然可见，但当前 Telemetry 服务不可用。系统不会用历史值、零值或不存在状态替代。'}
              </div>
            ) : null}
            {!snapshot && !currentPending && !currentUnavailable && row.snapshotResult?.status === 'error' ? (
              <div className="real-assets-detail__notice real-assets-detail__notice--warning" role="status">
                当前 Device Snapshot 无法在现有授权与关键点位范围内建立；Registry 身份仍保留。
              </div>
            ) : null}
            {!currentUnavailable && snapshot ? <SnapshotFacts snapshot={snapshot} site={site} routePolicyRevision={routePolicyRevision} /> : null}
          </section>

          <section aria-labelledby="real-assets-detail-points">
            <h3 id="real-assets-detail-points">当前关键点位</h3>
            {snapshot || (!currentPending && !currentUnavailable) ? <PointDetails row={row} site={site} /> : null}
          </section>

          <section aria-labelledby="real-assets-detail-history">
            <h3 id="real-assets-detail-history">关键点位短趋势</h3>
            <Suspense fallback={<div className="real-assets-history__loading" role="status" aria-live="polite">正在加载短趋势模块…</div>}>
              <DeviceHistoryTrends
                site={site}
                row={row}
                principal={principal}
                client={client}
                protectedGeneration={protectedGeneration}
                protectedRequestToken={protectedRequestToken}
                routePolicyRevision={routePolicyRevision}
                historyAllowed={historyAllowed}
                currentUnavailable={currentUnavailable}
                sessionCapability={sessionCapability}
              />
            </Suspense>
          </section>
        </div>
      )}
    </aside>
  );
}
