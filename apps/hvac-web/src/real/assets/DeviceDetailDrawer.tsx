import { lazy, Suspense, useEffect, useMemo, useRef } from 'react';
import { Alert, Button, Descriptions, Drawer, Empty, Space, Table, Tabs, Tag, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import type { CurrentPrincipalResponse, Site, TelemetryPoint } from '@/api/generated/platformGateway.gen';
import type { S2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import type { ProtectedScopeRequestToken, ProtectedScopeResource } from '../protected-scope';
import type { RealtimeStatusUpdate, RealtimeSubscriptionState } from '../realtime-status';
import { DeviceRealtimeStatus } from './DeviceRealtimeStatus';
import { realAssetsDevicePath, writeRealAssetsClipboard } from './detail';
import type { RealAssetsDeviceRow } from './model';
import type { RealAssetsPointView } from './operational-projection';
import { projectRealAssetsRealtimeRow } from './realtime';
import type { RealAssetsTelemetryRuntime } from './telemetry-runtime';
import { useRealAssetsDeviceRealtime, type RealAssetsRealtimeResult } from './useDeviceRealtime';

const DeviceHistoryTrends = lazy(async () => {
  const module = await import('./DeviceHistoryTrends');
  return { default: module.DeviceHistoryTrends };
});

interface DeviceDetailDrawerProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly detailState: 'closed' | 'visible' | 'not-visible';
  readonly row: RealAssetsDeviceRow | null;
  readonly telemetryClient: S2TelemetryClient;
  readonly protectedGeneration: number;
  readonly protectedRequestToken: () => ProtectedScopeRequestToken;
  readonly registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  readonly telemetryRuntime: RealAssetsTelemetryRuntime;
  readonly publishRealtimeStatus: (update: RealtimeStatusUpdate) => void;
  readonly routePolicyRevision: string | null;
  readonly refreshing: boolean;
  readonly onClose: () => void;
  readonly onRefresh: () => void;
}

function shellRealtimeState(realtime: RealAssetsRealtimeResult): RealtimeSubscriptionState {
  if (realtime.state?.status === 'live') return 'live';
  if (realtime.state?.status === 'snapshot') {
    return realtime.state.reason === 'reconnecting' ? 'reconnecting' : 'resync-required';
  }
  if (realtime.state?.status === 'unavailable') return 'unavailable';
  if (realtime.phase === 'opening') return 'connecting';
  if (realtime.phase === 'error') return 'unavailable';
  return 'idle';
}

function formatInstant(value: string | null, timeZone: string): string {
  if (!value) return '未提供';
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return '不可用';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(parsed);
}

function bindingLabel(row: RealAssetsDeviceRow): string {
  if (row.binding.state === 'bound') return row.binding.asset.displayName;
  if (row.binding.state === 'multi-bound') return row.binding.bindings.map((binding) => binding.asset.displayName).join('、');
  if (row.binding.state === 'ambiguous') return 'Asset 关系冲突';
  return '未绑定 Asset';
}

function pointFreshnessTag(point: RealAssetsPointView): React.ReactNode {
  const warning = point.freshness !== 'FRESH';
  return <Tag color={warning ? 'warning' : 'success'}>{point.freshness}</Tag>;
}

function pointQualityTag(point: RealAssetsPointView): React.ReactNode {
  if (point.state === 'UNAVAILABLE') return <Tag color="error">UNAVAILABLE</Tag>;
  if (point.state === 'MISSING') return <Tag>NO_DATA</Tag>;
  return <Tag color={point.quality === 'GOOD' ? 'success' : 'warning'}>{point.quality ?? 'NO_DATA'}</Tag>;
}

function PointTable({ row, timeZone }: { row: RealAssetsDeviceRow; timeZone: string }) {
  return (
    <div data-testid="real-assets-current-points">
      <Table<RealAssetsPointView>
        rowKey={(point) => point.pointId}
        size="small"
        pagination={{ pageSize: 10, showSizeChanger: false }}
        dataSource={[...row.operational.points]}
        columns={[
          {
            title: 'Point',
            render: (_, point) => (
              <Space direction="vertical" size={0}>
                <Typography.Text strong>{point.label}</Typography.Text>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>{point.key}</Typography.Text>
              </Space>
            ),
          },
          { title: '当前值', render: (_, point) => `${point.displayValue}${point.unit ? ` ${point.unit}` : ''}` },
          { title: 'Freshness', render: (_, point) => pointFreshnessTag(point) },
          { title: 'Quality', render: (_, point) => pointQualityTag(point) },
          { title: '采样时间', render: (_, point) => formatInstant(point.sampledAt, timeZone) },
        ]}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该 Device 没有登记可读 Point" /> }}
      />
    </div>
  );
}

function RegistryPointTable({ points }: { points: readonly TelemetryPoint[] }) {
  return (
    <Table<TelemetryPoint>
      rowKey="id"
      size="small"
      pagination={{ pageSize: 10, showSizeChanger: false }}
      dataSource={[...points]}
      columns={[
        {
          title: 'Point',
          render: (_, point) => (
            <Space direction="vertical" size={0}>
              <Typography.Text strong>{point.displayName}</Typography.Text>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>{point.pointCode}</Typography.Text>
            </Space>
          ),
        },
        { title: '类型', render: (_, point) => <Tag>{point.pointType}</Tag> },
        { title: '值类型', dataIndex: 'valueType' },
        { title: 'Sensor', render: (_, point) => point.sensorId ?? 'Device 内部/计算' },
        { title: '采样', render: (_, point) => `${point.sampleIntervalMs} ms` },
        { title: '发布', render: (_, point) => `${point.publishIntervalMs} ms` },
        { title: 'Stale', render: (_, point) => `${point.staleAfterMs} ms` },
      ]}
    />
  );
}

export function DeviceDetailDrawer({
  site,
  principal,
  detailState,
  row,
  telemetryClient,
  protectedGeneration,
  protectedRequestToken,
  registerProtectedResource,
  telemetryRuntime,
  publishRealtimeStatus,
  routePolicyRevision,
  refreshing,
  onClose,
  onRefresh,
}: DeviceDetailDrawerProps) {
  const realtime = useRealAssetsDeviceRealtime({
    row,
    allowed: principal.authorization.capabilities.includes('telemetry.subscribe'),
    protectedGeneration,
    authorizationEpoch: principal.authorization.policyRevision,
    runtime: telemetryRuntime,
    protectedRequestToken,
    registerProtectedResource,
  });
  const realtimeProjection = useMemo(
    () => row ? projectRealAssetsRealtimeRow(row, realtime.state) : null,
    [row, realtime.state],
  );
  const shellState = shellRealtimeState(realtime);
  useEffect(() => {
    publishRealtimeStatus({ state: shellState, siteId: site.id });
    return () => publishRealtimeStatus({ state: 'idle', siteId: site.id });
  }, [publishRealtimeStatus, shellState, site.id]);

  const historyAllowed = principal.authorization.capabilities.includes('telemetry.history.read');
  const sessionCapability = principal.session.csrfToken;
  const titleRef = useRef<HTMLHeadingElement | null>(null);
  useEffect(() => {
    if (detailState === 'closed') return;
    const handle = window.requestAnimationFrame(() => titleRef.current?.focus({ preventScroll: true }));
    return () => window.cancelAnimationFrame(handle);
  }, [detailState, row?.device.id]);
  const copyDeviceId = () => {
    if (!row) return;
    void writeRealAssetsClipboard(navigator.clipboard?.writeText?.bind(navigator.clipboard), row.device.id);
  };
  const copyDeviceLink = () => {
    if (!row) return;
    const path = realAssetsDevicePath(site.id, row.device.id);
    void writeRealAssetsClipboard(navigator.clipboard?.writeText?.bind(navigator.clipboard), `${location.origin}${path}`);
  };

  return (
    <Drawer
      width={760}
      rootClassName="ops-detail-drawer"
      open={detailState !== 'closed'}
      onClose={onClose}
      destroyOnHidden
      title={(
        <Typography.Title id="real-assets-detail-title" ref={titleRef} tabIndex={-1} level={4} style={{ margin: 0 }}>
          {row ? `${row.device.displayName} · ${row.device.code}` : 'Device 详情'}
        </Typography.Title>
      )}
      footer={(
        <Space wrap style={{ width: '100%', justifyContent: 'flex-end' }}>
          <Button data-testid="real-assets-detail-copy-id" disabled={!row} onClick={copyDeviceId}>复制 ID</Button>
          <Button data-testid="real-assets-detail-copy-link" disabled={!row} onClick={copyDeviceLink}>复制链接</Button>
          <Button data-testid="real-assets-detail-refresh" icon={<ReloadOutlined />} loading={refreshing} disabled={!row} onClick={onRefresh}>刷新</Button>
          <Button data-testid="real-assets-detail-close" onClick={onClose}>关闭</Button>
        </Space>
      )}
      data-testid="real-assets-device-detail"
      data-detail-state={detailState}
    >
      {detailState === 'not-visible' ? (
        <Alert
          type="warning"
          showIcon
          message="Device 不可见或不存在"
          description="未知、格式无效、其他 Site 或未授权 Device 使用同一非枚举状态；系统不会说明具体原因，也不会建立实时订阅。"
        />
      ) : row ? (
        <Tabs
          defaultActiveKey="overview"
          items={[
            {
              key: 'overview',
              label: <span data-testid="real-assets-detail-tab-overview">概览</span>,
              children: (
                <Space direction="vertical" size={16} style={{ width: '100%' }}>
                  {row.profile.state === 'unconfigured' ? (
                    <Alert type="info" showIcon message="未配置展示 Profile" description="该 Device 仍按 Registry Point 与 S2 权威状态完整展示；Profile 只影响展示增强，不决定可见性或运行判定。" />
                  ) : null}
                  <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
                    <Descriptions.Item label="Device ID" span={2}><Typography.Text copyable>{row.device.id}</Typography.Text></Descriptions.Item>
                    <Descriptions.Item label="类型">{row.device.deviceType}</Descriptions.Item>
                    <Descriptions.Item label="Registry">{row.device.status} · rev {row.device.revision}</Descriptions.Item>
                    <Descriptions.Item label="区域">{row.space.state === 'bound' ? row.space.space.displayName : '未绑定 Space'}</Descriptions.Item>
                    <Descriptions.Item label="Asset">{bindingLabel(row)}</Descriptions.Item>
                    <Descriptions.Item label="连接"><Tag>{row.operational.connection.state}</Tag></Descriptions.Item>
                    <Descriptions.Item label="数据"><Tag>{row.operational.telemetry.readiness}</Tag></Descriptions.Item>
                    <Descriptions.Item label="Freshness">{row.operational.telemetry.freshness}</Descriptions.Item>
                    <Descriptions.Item label="Quality">{row.operational.telemetry.quality}</Descriptions.Item>
                    <Descriptions.Item label="Point 证据" span={2}>{row.operational.telemetry.presentPointCount} / {row.operational.telemetry.registeredPointCount} 有当前证据</Descriptions.Item>
                    <Descriptions.Item label="Business revision">{row.snapshotResult?.status === 'ok' ? row.snapshotResult.snapshot.businessRevision : '不可用'}</Descriptions.Item>
                    <Descriptions.Item label="Route policy">{routePolicyRevision ?? '未提供'}</Descriptions.Item>
                  </Descriptions>
                  <section aria-labelledby="real-assets-detail-current">
                    <Typography.Title id="real-assets-detail-current" level={5}>代表性当前值</Typography.Title>
                    {row.snapshotResult?.status === 'error' ? (
                      <Alert type="warning" showIcon message="Current Snapshot 不可用" description={row.snapshotResult.problem.detail} />
                    ) : null}
                    {row.operational.representativePoints.length > 0 ? (
                      <Space wrap>
                        {row.operational.representativePoints.map((point) => (
                          <Tag key={point.pointId} color={point.freshness === 'STALE' || point.quality !== 'GOOD' ? 'warning' : undefined}>
                            {point.label} · {point.displayValue}{point.unit ? ` ${point.unit}` : ''} · {point.freshness === 'STALE' ? '陈旧' : point.freshness}{point.quality ? ` · ${point.quality}` : ''}
                          </Tag>
                        ))}
                      </Space>
                    ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有可预览的当前值" />}
                  </section>
                </Space>
              ),
            },
            {
              key: 'current',
              label: <span data-testid="real-assets-detail-tab-current">当前数据</span>,
              children: <PointTable row={row} timeZone={site.timezone} />,
            },
            {
              key: 'trends',
              label: <span data-testid="real-assets-detail-tab-trends">趋势</span>,
              children: (
                <Suspense fallback={<div className="real-assets-history__loading" role="status">正在加载历史趋势组件…</div>}>
                  <DeviceHistoryTrends
                    site={site}
                    row={row}
                    principal={principal}
                    client={telemetryClient}
                    protectedGeneration={protectedGeneration}
                    protectedRequestToken={protectedRequestToken}
                    routePolicyRevision={routePolicyRevision}
                    historyAllowed={historyAllowed}
                    currentUnavailable={row.snapshotResult?.status === 'error'}
                    sessionCapability={sessionCapability}
                  />
                </Suspense>
              ),
            },
            {
              key: 'connection',
              label: <span data-testid="real-assets-detail-tab-connection">连接</span>,
              children: (
                <Space direction="vertical" size={16} style={{ width: '100%' }}>
                  <DeviceRealtimeStatus realtime={realtime} projection={realtimeProjection} site={site} />
                  <Descriptions bordered size="small" column={1}>
                    <Descriptions.Item label="Presence applicability">{row.operational.connection.applicability}</Descriptions.Item>
                    <Descriptions.Item label="Presence state">{row.operational.connection.state}</Descriptions.Item>
                    <Descriptions.Item label="Last seen">{formatInstant(row.operational.connection.lastSeenAt, site.timezone)}</Descriptions.Item>
                    <Descriptions.Item label="Presence policy revision">{row.operational.connection.policyRevision ?? '未提供'}</Descriptions.Item>
                  </Descriptions>
                </Space>
              ),
            },
            {
              key: 'configuration',
              label: <span data-testid="real-assets-detail-tab-configuration">配置</span>,
              children: (
                <Space direction="vertical" size={16} style={{ width: '100%' }}>
                  <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
                    <Descriptions.Item label="Device ID" span={2}><Typography.Text copyable>{row.device.id}</Typography.Text></Descriptions.Item>
                    <Descriptions.Item label="Device code">{row.device.code}</Descriptions.Item>
                    <Descriptions.Item label="Device type">{row.device.deviceType}</Descriptions.Item>
                    <Descriptions.Item label="Registry lifecycle">{row.operational.registryLifecycle}</Descriptions.Item>
                    <Descriptions.Item label="Registry revision">{row.device.revision}</Descriptions.Item>
                    <Descriptions.Item label="Telemetry Points">{row.telemetryPoints.length}</Descriptions.Item>
                  </Descriptions>
                  <RegistryPointTable points={row.telemetryPoints} />
                </Space>
              ),
            },
          ]}
        />
      ) : null}
    </Drawer>
  );
}
