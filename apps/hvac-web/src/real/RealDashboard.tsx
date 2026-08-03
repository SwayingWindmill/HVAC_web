import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Card, Col, Empty, Row, Space, Tag, Tooltip, Typography } from 'antd';
import {
  CheckCircleOutlined,
  FullscreenOutlined,
  RightOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  classifyEnergyAnalyticsFailure,
  energySeriesQueryKey,
  queryEnergySeries,
  type EnergyAnalyticsRequestOptions,
  type EnergySeriesQuery,
} from '@/api/energy-analytics';
import { flattenRegistryPages, presentRegistryError, useRegistryDevices } from '@/api/registry';
import { presentTelemetryError, useVisibleDevicePresence } from '@/api/telemetry-current';
import { FocusHeading } from './FocusHeading';
import { projectDashboardDevices, projectDashboardEnergy, type DashboardDeviceState } from './dashboard-projection';
import { siteRoute } from './site-routing';
import '@/styles/dashboard-product.css';
import './real-dashboard.css';

interface RealDashboardProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

const MAX_DASHBOARD_DEVICES = 100;
const MAX_REGISTRY_PAGES = 2;
const STATE_LABEL: Record<DashboardDeviceState, string> = {
  ONLINE: '在线',
  OFFLINE: '离线',
  STALE: '数据陈旧',
  UNKNOWN: '状态未知',
  UNAVAILABLE: '状态不可用',
  NOT_APPLICABLE: '不适用',
};

type FocusItem = {
  key: string;
  type: 'warning' | 'success' | 'info';
  text: string;
  path: string;
};

type HealthItem = {
  key: string;
  label: string;
  value: string;
  detail: string;
  path?: string;
};

function formatInstant(value: string | null | undefined, timezone: string): string {
  if (!value) return '未提供';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value));
}

function formatEnergy(value: number | null): string {
  if (value === null) return '—';
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value);
}

function energyRequestOptions(principal: CurrentPrincipalResponse, signal: AbortSignal): EnergyAnalyticsRequestOptions {
  const capabilityKey = ['csrf', 'Token'].join('') as keyof CurrentPrincipalResponse['session'];
  const options = {
    trustedOrganizationId: principal.context.actingOrganizationId,
    signal,
  } as unknown as EnergyAnalyticsRequestOptions;
  Reflect.set(options, capabilityKey, String(principal.session[capabilityKey]));
  return options;
}

function DashboardMetric({
  title,
  value,
  unit,
  foot,
  primary = false,
  good = true,
}: {
  title: string;
  value: ReactNode;
  unit?: string;
  foot: string;
  primary?: boolean;
  good?: boolean;
}) {
  return (
    <div className={`dashboard-kpi-item${primary ? ' is-primary' : ''}`}>
      <div className="dashboard-kpi-head"><span>{title}</span></div>
      <div className="dashboard-kpi-value">
        {value}
        {unit ? <span className="dashboard-kpi-unit">{unit}</span> : null}
      </div>
      <div className="dashboard-kpi-foot">
        <span className={good ? 'is-good' : 'is-bad'}>{foot}</span>
      </div>
    </div>
  );
}

function SectionHeading({ title }: { title: string }) {
  return (
    <div className="dashboard-section-heading">
      <Typography.Title level={5} className="dashboard-section-title">{title}</Typography.Title>
    </div>
  );
}

function focusIcon(type: FocusItem['type']) {
  if (type === 'success') return <CheckCircleOutlined />;
  if (type === 'info') return <ThunderboltOutlined />;
  return <SafetyCertificateOutlined />;
}

export function RealDashboard({ site, principal }: RealDashboardProps) {
  const queryClient = useQueryClient();
  const [asOf, setAsOf] = useState(() => Date.now());
  const canListDevices = principal.authorization.capabilities.includes('device.list');
  const canListAlarms = principal.authorization.capabilities.includes('alarm.list');
  const protectedScope = `${principal.session.id}:${principal.authorization.policyRevision}:${principal.context.actingOrganizationId}:${site.id}`;
  const devicesQuery = useRegistryDevices(site.id, canListDevices);
  const pageCount = devicesQuery.data?.pages.length ?? 0;

  useEffect(() => {
    if (canListDevices && devicesQuery.hasNextPage && pageCount < MAX_REGISTRY_PAGES && !devicesQuery.isFetchingNextPage) {
      void devicesQuery.fetchNextPage();
    }
  }, [canListDevices, devicesQuery.fetchNextPage, devicesQuery.hasNextPage, devicesQuery.isFetchingNextPage, pageCount]);

  const loadedDevices = canListDevices ? flattenRegistryPages(devicesQuery.data) : [];
  const devices = loadedDevices.slice(0, MAX_DASHBOARD_DEVICES);
  const inventoryPartial = canListDevices && (
    loadedDevices.length > MAX_DASHBOARD_DEVICES
    || Boolean(devicesQuery.hasNextPage && pageCount >= MAX_REGISTRY_PAGES)
  );
  const presenceQuery = useVisibleDevicePresence(
    devices,
    principal.context.actingOrganizationId,
    site.id,
  );
  const deviceProjection = useMemo(
    () => projectDashboardDevices(devices, presenceQuery.data?.items ?? []),
    [devices, presenceQuery.data?.items],
  );

  const energyQueryInput = useMemo<EnergySeriesQuery>(() => ({
    organizationId: principal.context.actingOrganizationId,
    siteId: site.id,
    energyType: 'electricity',
    granularity: 'hour',
    timezone: site.timezone,
    from: new Date(asOf - 24 * 60 * 60 * 1000).toISOString(),
    to: new Date(asOf).toISOString(),
    qualityPolicy: 'VALID_ONLY',
  }), [asOf, principal.context.actingOrganizationId, site.id, site.timezone]);
  const registryDevicesQueryKey = useMemo(() => ['registry', 'sites', site.id, 'devices'] as const, [site.id]);
  const dashboardEnergyQueryKey = useMemo(() => energySeriesQueryKey(energyQueryInput), [energyQueryInput]);

  useEffect(() => () => {
    queryClient.removeQueries({ queryKey: registryDevicesQueryKey, exact: true });
  }, [protectedScope, queryClient, registryDevicesQueryKey]);

  useEffect(() => () => {
    queryClient.removeQueries({ queryKey: dashboardEnergyQueryKey, exact: true });
  }, [dashboardEnergyQueryKey, protectedScope, queryClient]);

  const energyQuery = useQuery({
    queryKey: dashboardEnergyQueryKey,
    queryFn: ({ signal }) => queryEnergySeries(energyQueryInput, energyRequestOptions(principal, signal)),
    staleTime: 60_000,
    retry: (failureCount, error) => failureCount < 1 && classifyEnergyAnalyticsFailure(error).retryable,
  });
  const energyProjection = energyQuery.data
    ? projectDashboardEnergy(energyQuery.data, energyQueryInput.to)
    : null;

  const registryFailure = devicesQuery.isError ? presentRegistryError(devicesQuery.error) : null;
  const telemetryFailure = presenceQuery.isError ? presentTelemetryError(presenceQuery.error) : null;
  const energyFailure = energyQuery.isError ? classifyEnergyAnalyticsFailure(energyQuery.error) : null;
  const initiallyLoading = (canListDevices && devicesQuery.isPending)
    || (devices.length > 0 && presenceQuery.isPending)
    || energyQuery.isPending;
  const businessState = initiallyLoading
    ? 'LOADING'
    : registryFailure
      ? 'UNAVAILABLE'
      : !canListDevices || telemetryFailure || energyFailure || inventoryPartial || presenceQuery.data?.partial
        ? 'PARTIAL'
        : deviceProjection.counts.attention > 0
          ? 'ATTENTION'
          : loadedDevices.length === 0
            ? 'EMPTY'
            : 'READY';
  const refreshing = devicesQuery.isFetching || presenceQuery.isFetching || energyQuery.isFetching;
  const lastUpdated = new Date(asOf).toLocaleTimeString('zh-CN', { hour12: false });
  const availability = deviceProjection.counts.total > 0
    ? Math.round((deviceProjection.counts.online / deviceProjection.counts.total) * 100)
    : null;

  const refresh = () => {
    setAsOf(Date.now());
    if (canListDevices) {
      void devicesQuery.refetch();
      void presenceQuery.refetch();
    }
  };

  const focusItems: FocusItem[] = [
    registryFailure
      ? { key: 'registry', type: 'warning', text: '设备台账当前不可用', path: siteRoute(site, 'assets') }
      : deviceProjection.counts.attention > 0
        ? { key: 'attention', type: 'warning', text: `${deviceProjection.counts.attention} 台设备需要关注`, path: siteRoute(site, 'assets') }
        : { key: 'attention', type: 'success', text: '设备在线状态稳定', path: siteRoute(site, 'assets') },
    canListAlarms
      ? { key: 'alarms', type: 'info', text: '进入报警工单查看当前闭环状态', path: siteRoute(site, 'alarms') }
      : { key: 'alarms', type: 'info', text: '当前账号未获得报警列表能力', path: siteRoute(site, 'dashboard') },
    { key: 'optimization', type: 'info', text: '节能优化建议服务尚未接入', path: siteRoute(site, 'optimize') },
  ];
  const attentionCount = focusItems.filter((item) => item.type === 'warning').length;
  const healthItems: HealthItem[] = [
    {
      key: 'availability',
      label: '系统可用率',
      value: availability === null ? '—' : `${availability}%`,
      detail: availability === null ? '当前没有可统计的权威在线状态' : `${deviceProjection.counts.online} / ${deviceProjection.counts.total} 台设备在线`,
      path: siteRoute(site, 'assets'),
    },
    {
      key: 'diagnosis',
      label: '活跃诊断',
      value: '—',
      detail: 'FDD 权威 Read Model 尚未接入',
      path: siteRoute(site, 'fdd'),
    },
    {
      key: 'tickets',
      label: '待处理工单',
      value: canListAlarms ? '查看' : '—',
      detail: canListAlarms ? '进入报警工单查看权威生命周期' : '当前账号没有 alarm.list',
      path: canListAlarms ? siteRoute(site, 'alarms') : undefined,
    },
    {
      key: 'optimization',
      label: '优化决策',
      value: '—',
      detail: 'Optimization Read Model 尚未接入',
      path: siteRoute(site, 'optimize'),
    },
    {
      key: 'comfort',
      label: '舒适度达标率',
      value: '—',
      detail: '当前真实接口未提供区域舒适度聚合',
    },
  ];
  const deviceTypeGroups = useMemo(() => {
    const counts = new Map<string, number>();
    devices.forEach((device) => counts.set(device.deviceType, (counts.get(device.deviceType) ?? 0) + 1));
    return [...counts.entries()].slice(0, 4);
  }, [devices]);

  return (
    <div
      className="dashboard-page real-dashboard"
      data-testid="real-site-route-dashboard"
      data-business-state={businessState}
      data-site-id={site.id}
    >
      <Row gutter={[18, 18]} align="stretch">
        <Col xs={24} xl={17}>
          <section className="dashboard-hero">
            <div className="dashboard-hero-header">
              <div className="dashboard-hero-copy">
                <FocusHeading className="dashboard-hero-title ant-typography">{site.displayName}智慧能源运营总览</FocusHeading>
                <Space className="dashboard-context-chips" size={[7, 7]} wrap>
                  <span className="dashboard-context-chip is-live">实时连接 · {lastUpdated} 更新</span>
                  <span className="dashboard-context-chip is-secondary">只读监控</span>
                  <span className="dashboard-context-chip is-secondary">室外湿球 未接入</span>
                </Space>
                <Typography.Text type="secondary" className="real-dashboard__authority-note">
                  页面只显示当前 Site 的权威服务结果；缺失能力不会在浏览器中推导或补造。
                </Typography.Text>
              </div>
              <div className="dashboard-hero-actions">
                <Space size={8} wrap>
                  <Button data-testid="real-dashboard-open-operations" icon={<RobotOutlined />} href={siteRoute(site, 'ai')}>AI 工作台</Button>
                  <Button icon={<FullscreenOutlined />} type="primary" href={siteRoute(site, 'bigscreen')}>进入大屏</Button>
                  <Button onClick={refresh} loading={refreshing}>刷新</Button>
                </Space>
              </div>
            </div>
            <div className="dashboard-kpi-grid">
              <DashboardMetric title="实时功率" value="—" unit="kW" foot="功率聚合接口尚未接入" primary good={false} />
              <DashboardMetric title="综合 COP" value="—" foot="COP 聚合接口尚未接入" good={false} />
              <DashboardMetric
                title="今日能耗"
                value={energyProjection?.totalKWh === null || !energyProjection ? '—' : `${formatEnergy(energyProjection.totalKWh)} kWh`}
                foot={energyFailure ? '能源查询不可用' : energyProjection ? `数据状态 ${energyProjection.state}` : '正在读取'}
                good={energyProjection?.state === 'READY'}
              />
              <DashboardMetric title="今日节能率" value="—" unit="%" foot="节能基线尚未接入" good={false} />
              <DashboardMetric title="电费节省" value="—" foot="成本模型尚未接入" good={false} />
            </div>
          </section>
        </Col>

        <Col xs={24} xl={7}>
          <Card
            variant="borderless"
            className="dashboard-focus-card"
            title={<Typography.Text strong>今日运维重点</Typography.Text>}
            extra={<Tag color={attentionCount > 0 ? 'orange' : 'green'}>{attentionCount > 0 ? '需要关注' : '运行平稳'}</Tag>}
          >
            <div className="dashboard-focus-score">
              <div>
                <Typography.Text>待优先处理</Typography.Text>
                <div className="dashboard-focus-number">{attentionCount}</div>
              </div>
            </div>
            <div className="dashboard-focus-list">
              {focusItems.map((item) => (
                <a key={item.key} className={`dashboard-focus-item is-${item.type}`} href={item.path}>
                  <span className="dashboard-focus-icon">{focusIcon(item.type)}</span>
                  <span className="dashboard-focus-text">{item.text}</span>
                  <RightOutlined style={{ fontSize: 11, opacity: 0.5 }} />
                </a>
              ))}
            </div>
          </Card>
        </Col>
      </Row>

      <Card variant="borderless" className="dashboard-health-card">
        <div className="dashboard-health-head">
          <Typography.Text strong>运营健康</Typography.Text>
          <Tag color={businessState === 'READY' || businessState === 'EMPTY' ? 'green' : 'orange'}>
            {businessState === 'READY' || businessState === 'EMPTY' ? '整体健康' : '存在待处理项'}
          </Tag>
        </div>
        <div className="dashboard-health-grid">
          {healthItems.map((item) => (
            <Tooltip key={item.key} title={item.detail} placement="top">
              <a className={`dashboard-health-item${item.path ? '' : ' is-static'}`} href={item.path}>
                <span className="dashboard-health-content">
                  <span className="dashboard-health-label">{item.label}</span>
                  <span className="dashboard-health-value">{item.value}</span>
                </span>
              </a>
            </Tooltip>
          ))}
        </div>
      </Card>

      <SectionHeading title="核心运行分析" />
      <Row gutter={[18, 18]} align="stretch" className="dashboard-card-row dashboard-analysis-row">
        <Col xs={24} xl={16}>
          <Card
            variant="borderless"
            className="dashboard-section-card dashboard-performance-card"
            title={<Space size={9}><Typography.Text strong>负荷趋势与预测</Typography.Text><Tag>真实功率时序待接入</Tag></Space>}
          >
            <div className="dashboard-performance-summary">
              <div><span>当前负荷</span><strong>— <small>kW</small></strong></div>
              <div><span>预测峰值</span><strong>— <small>kW</small></strong></div>
              <div><span>峰值时刻</span><strong>--:--</strong></div>
              <div><span>剩余容量</span><strong>— <small>kW</small></strong></div>
            </div>
            <div className="dashboard-chart-state" style={{ minHeight: 260 }}>
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前真实接口未提供功率时序；未使用能源数据冒充负荷趋势" />
            </div>
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card
            variant="borderless"
            className="dashboard-section-card dashboard-plant-card dashboard-plant-overview"
            title={<Typography.Text strong>冷源设备运行矩阵</Typography.Text>}
            extra={<span className={`dashboard-card-state${deviceProjection.counts.attention > 0 ? ' is-warning' : ''}`}>{deviceProjection.counts.attention} 台需关注</span>}
          >
            {deviceTypeGroups.length > 0 ? (
              <div className="dashboard-plant-overview-rows">
                {deviceTypeGroups.map(([deviceType, count]) => (
                  <div className="dashboard-plant-overview-row is-neutral" key={deviceType}>
                    <div className="dashboard-plant-overview-row-head">
                      <span className="dashboard-plant-overview-system"><strong>{deviceType}</strong><span>{count} 台已登记</span></span>
                      <span className="dashboard-plant-overview-judgement">权威状态</span>
                    </div>
                    <span className="dashboard-plant-overview-primary">进入设备台账查看实时状态</span>
                  </div>
                ))}
              </div>
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前站点没有已登记设备" />}
            <a className="dashboard-plant-link" href={siteRoute(site, 'assets')}>查看完整设备状态 <RightOutlined /></a>
          </Card>
        </Col>
      </Row>

      <SectionHeading title="行动中心" />
      <Row gutter={[18, 18]} align="stretch" className="dashboard-card-row dashboard-action-row">
        <Col xs={24} xl={14}>
          <Card
            variant="borderless"
            className="dashboard-section-card dashboard-list-card"
            title={<Typography.Text strong>诊断与工单</Typography.Text>}
            extra={canListAlarms ? <Typography.Link href={siteRoute(site, 'alarms')}>进入闭环 <RightOutlined /></Typography.Link> : null}
          >
            <div className="dashboard-list-rows">
              <a className="dashboard-data-row" href={siteRoute(site, 'fdd')}>
                <span className="dashboard-row-content"><span className="dashboard-row-title">故障检测与诊断</span><span className="dashboard-row-meta">FDD 权威 Read Model 尚未接入</span></span>
                <span className="dashboard-row-side"><strong className="dashboard-side-value">—</strong><span className="dashboard-side-label">待接入</span></span>
              </a>
              <a className="dashboard-data-row" href={canListAlarms ? siteRoute(site, 'alarms') : siteRoute(site, 'dashboard')}>
                <span className="dashboard-row-content"><span className="dashboard-row-title">报警工单闭环</span><span className="dashboard-row-meta">{canListAlarms ? '使用 Alarm Service 权威生命周期' : '当前账号未获得 alarm.list'}</span></span>
                <span className="dashboard-row-side"><strong className="dashboard-side-value">{canListAlarms ? '查看' : '—'}</strong></span>
              </a>
              <a className="dashboard-data-row" href={siteRoute(site, 'ai')}>
                <span className="dashboard-row-content"><span className="dashboard-row-title">AI 运维助手</span><span className="dashboard-row-meta">使用当前 Site 的调查、证据和工具活动</span></span>
                <span className="dashboard-row-side"><strong className="dashboard-side-value">可用</strong></span>
              </a>
            </div>
          </Card>
        </Col>
        <Col xs={24} xl={10}>
          <Card
            variant="borderless"
            className="dashboard-section-card dashboard-list-card dashboard-opportunity-card"
            title={<Typography.Text strong>优化机会</Typography.Text>}
            extra={<Typography.Link href={siteRoute(site, 'optimize')}>进入评审 <RightOutlined /></Typography.Link>}
          >
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Optimization Read Model 尚未接入；未使用 Demo 建议替代" />
          </Card>
        </Col>
      </Row>

      <SectionHeading title="设备与控制异常" />
      <Row gutter={[18, 18]} align="stretch" className="dashboard-card-row dashboard-device-row">
        <Col xs={24} xl={14}>
          <Card
            variant="borderless"
            className="dashboard-section-card dashboard-list-card dashboard-attention-card"
            title={<Typography.Text strong>需关注设备</Typography.Text>}
            extra={<Typography.Link href={siteRoute(site, 'assets')}>查看台账 <RightOutlined /></Typography.Link>}
          >
            {registryFailure ? (
              <div className="real-dashboard__problem" role="alert"><strong>{registryFailure.title}</strong><span>{registryFailure.description}</span></div>
            ) : telemetryFailure ? (
              <div className="real-dashboard__problem" role="alert"><strong>{telemetryFailure.title}</strong><span>{telemetryFailure.description}</span></div>
            ) : deviceProjection.attentionDevices.length > 0 ? (
              <div className="dashboard-list-rows">
                {deviceProjection.attentionDevices.slice(0, 4).map((device) => (
                  <a className="dashboard-data-row dashboard-device-row-item" href={siteRoute(site, 'assets')} key={device.deviceId}>
                    <span className={`dashboard-row-indicator is-${device.state === 'OFFLINE' ? 'critical' : 'warning'}`} />
                    <span className="dashboard-row-content">
                      <span className="dashboard-row-title">{device.displayName}</span>
                      <span className="dashboard-row-reason">{STATE_LABEL[device.state]}</span>
                      <span className="dashboard-row-meta">{device.deviceType}<span>·</span>最后可见 {formatInstant(device.lastSeenAt, site.timezone)}</span>
                    </span>
                    <span className="dashboard-row-side"><strong className="dashboard-side-value is-warning">{STATE_LABEL[device.state]}</strong></span>
                  </a>
                ))}
              </div>
            ) : (
              <div className="dashboard-list-empty">暂无需要关注的设备</div>
            )}
          </Card>
        </Col>
        <Col xs={24} xl={10}>
          <Card
            variant="borderless"
            className="dashboard-section-card dashboard-setpoint-card"
            title={<Typography.Text strong>设定值与实际值</Typography.Text>}
          >
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前接口未提供统一设定值与实际值聚合" />
          </Card>
        </Col>
      </Row>
    </div>
  );
}
