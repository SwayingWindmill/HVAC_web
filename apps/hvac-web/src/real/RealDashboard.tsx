import type { ReactNode } from 'react';
import { Button, Card, Col, Empty, Row, Space, Tag, Tooltip, Typography } from 'antd';
import {
  FullscreenOutlined,
  RightOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import type {
  CurrentPrincipalResponse,
  DashboardMetric as DashboardMetricValue,
  PresentationState,
  Site,
} from '@/api/generated/platformGateway.gen';
import { presentSiteDashboardError, useSiteDashboardSummary } from '@/api/site-dashboard';
import { FocusHeading } from './FocusHeading';
import { siteRoute } from './site-routing';
import '@/styles/dashboard-product.css';
import './real-dashboard.css';

interface RealDashboardProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

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

const STATE_LABEL: Record<PresentationState, string> = {
  READY: '完整',
  ATTENTION: '需要关注',
  NO_DATA: '暂无数据',
  PARTIAL: '数据不完整',
  STALE: '数据陈旧',
  SUSPECT: '质量可疑',
  UNAVAILABLE: '服务不可用',
  NOT_AUTHORIZED: '无权限',
  NOT_INTEGRATED: '未接入',
};

function formatInstant(value: string | null | undefined, timezone: string): string {
  if (!value) return '未提供';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(new Date(value));
}

function formatNumber(value: number | null, maximumFractionDigits = 1): string {
  if (value === null) return '—';
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits }).format(value);
}

function metricFoot(metric: DashboardMetricValue): string {
  if (metric.reason) return `${STATE_LABEL[metric.state]} · ${metric.reason}`;
  if (metric.aggregateWatermark) return `${STATE_LABEL[metric.state]} · 聚合水位 ${metric.aggregateWatermark}`;
  if (metric.dataWatermark) return `${STATE_LABEL[metric.state]} · 数据水位 ${metric.dataWatermark}`;
  return STATE_LABEL[metric.state];
}

function metricGood(state: PresentationState): boolean {
  return state === 'READY';
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
  return type === 'info' ? <ThunderboltOutlined /> : <SafetyCertificateOutlined />;
}

export function RealDashboard({ site, principal }: RealDashboardProps) {
  const summaryQuery = useSiteDashboardSummary(
    principal.context.tenantId,
    site.id,
    `${principal.session.id}:${principal.authorization.policyRevision}`,
  );
  const summary = summaryQuery.data;
  const error = summaryQuery.isError ? presentSiteDashboardError(summaryQuery.error) : null;
  const population = summary?.devicePopulation;
  const energy = summary?.slowMetrics.siteLocalDayEnergy;
  const power = summary?.fastMetrics.currentPower;
  const cop = summary?.slowMetrics.cop;
  const savings = summary?.slowMetrics.baselineSavings;
  const cost = summary?.slowMetrics.cost;
  const alarms = summary?.fastMetrics.openAlarms;

  const businessState = summaryQuery.isPending ? 'LOADING' : error ? 'UNAVAILABLE' : summary?.quality ?? 'UNAVAILABLE';
  const attentionCount = (population?.offline ?? 0) + (population?.stale ?? 0) + (alarms?.activeCount ?? 0);
  const availability = population?.availabilityPercent ?? null;
  const denominator = population?.denominator ?? null;
  const registered = population?.registered ?? 0;
  const visibleOperational = population?.observable ?? 0;

  const focusItems: FocusItem[] = summary ? [
    population?.state === 'READY' && (population.offline > 0 || population.stale > 0)
      ? { key: 'devices', type: 'warning', text: `${population.offline + population.stale} 台设备处于离线或陈旧状态`, path: siteRoute(site, 'assets') }
      : population?.state === 'READY'
        ? { key: 'devices', type: 'success', text: '设备 Population 完整，当前没有离线或陈旧设备', path: siteRoute(site, 'assets') }
        : { key: 'devices', type: 'warning', text: `设备 Population：${STATE_LABEL[population?.state ?? 'UNAVAILABLE']}`, path: siteRoute(site, 'assets') },
    alarms?.state === 'READY' && (alarms.activeCount ?? 0) > 0
      ? { key: 'alarms', type: 'warning', text: `${alarms.activeCount} 条活动告警`, path: siteRoute(site, 'alarms') }
      : alarms?.state === 'READY'
        ? { key: 'alarms', type: 'success', text: '当前没有活动告警', path: siteRoute(site, 'alarms') }
        : { key: 'alarms', type: 'info', text: `告警摘要：${STATE_LABEL[alarms?.state ?? 'UNAVAILABLE']}`, path: siteRoute(site, 'alarms') },
    { key: 'optimization', type: 'info', text: '优化建议只进入评审，不从 Dashboard 直接控制设备', path: siteRoute(site, 'optimize') },
  ] : [];

  const healthItems: HealthItem[] = summary ? [
    {
      key: 'availability',
      label: '系统可用率',
      value: availability === null ? '—' : `${formatNumber(availability, 1)}%`,
      detail: denominator === null
        ? `${STATE_LABEL[population!.state]}；Population 不完整时不发布站点比例`
        : `${population!.online} / ${denominator}，分母策略 ${population!.denominatorPolicy}`,
      path: siteRoute(site, 'assets'),
    },
    {
      key: 'population',
      label: '设备 Population',
      value: `${visibleOperational}/${registered}`,
      detail: `${STATE_LABEL[population!.state]}；已登记 ${registered}，可观察 ${visibleOperational}，未知 ${population!.unknown}，不可用 ${population!.unavailable}`,
      path: siteRoute(site, 'assets'),
    },
    {
      key: 'alarms',
      label: '活动告警',
      value: alarms?.activeCount === null || alarms?.activeCount === undefined ? '—' : String(alarms.activeCount),
      detail: alarms?.reason ?? `${STATE_LABEL[alarms?.state ?? 'UNAVAILABLE']} · 最高级别 ${alarms?.highestSeverity ?? '无'}`,
      path: siteRoute(site, 'alarms'),
    },
    {
      key: 'summary-quality',
      label: '摘要质量',
      value: STATE_LABEL[summary.quality],
      detail: `完整性 ${STATE_LABEL[summary.completeness]}；原因 ${summary.reasons.join('、') || '无'}`,
    },
  ] : [];

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
                  <span className="dashboard-context-chip is-secondary">权威 Summary</span>
                  <span className="dashboard-context-chip is-secondary">只读监控</span>
                  {summary ? <span className="dashboard-context-chip is-secondary">As of {formatInstant(summary.asOf, site.timezone)}</span> : null}
                </Space>
                <Typography.Text type="secondary" className="real-dashboard__authority-note">
                  Dashboard 与大屏共享同一 SiteDashboardSummary。未接入、无权限、部分数据和陈旧数据均显式展示，不在浏览器补造。
                </Typography.Text>
              </div>
              <div className="dashboard-hero-actions">
                <Space size={8} wrap>
                  <Button data-testid="real-dashboard-open-operations" icon={<RobotOutlined />} href={siteRoute(site, 'ai')}>AI 工作台</Button>
                  <Button icon={<FullscreenOutlined />} type="primary" href={siteRoute(site, 'bigscreen')}>进入大屏</Button>
                  <Button onClick={() => void summaryQuery.refetch()} loading={summaryQuery.isFetching}>刷新 Snapshot</Button>
                </Space>
              </div>
            </div>
            {error ? <div className="real-dashboard__problem" role="alert"><strong>{error.title}</strong><span>{error.description}</span></div> : null}
            <div className="dashboard-kpi-grid">
              <DashboardMetric title="实时功率" value={formatNumber(power?.value ?? null)} unit={power?.unit ?? 'kW'} foot={power ? metricFoot(power) : '正在读取'} primary good={power ? metricGood(power.state) : false} />
              <DashboardMetric title="综合 COP" value={formatNumber(cop?.value ?? null, 2)} foot={cop ? metricFoot(cop) : '正在读取'} good={cop ? metricGood(cop.state) : false} />
              <DashboardMetric title="今日能耗" value={formatNumber(energy?.value ?? null)} unit={energy?.unit ?? 'kWh'} foot={energy ? metricFoot(energy) : '正在读取'} good={energy ? metricGood(energy.state) : false} />
              <DashboardMetric title="今日节能" value={formatNumber(savings?.value ?? null)} unit={savings?.unit ?? '%'} foot={savings ? metricFoot(savings) : '正在读取'} good={savings ? metricGood(savings.state) : false} />
              <DashboardMetric title="成本" value={formatNumber(cost?.value ?? null)} unit={cost?.unit ?? undefined} foot={cost ? metricFoot(cost) : '正在读取'} good={cost ? metricGood(cost.state) : false} />
            </div>
          </section>
        </Col>

        <Col xs={24} xl={7}>
          <Card
            variant="borderless"
            className="dashboard-focus-card"
            title={<Typography.Text strong>今日运维重点</Typography.Text>}
            extra={<Tag color={businessState === 'READY' ? 'green' : businessState === 'ATTENTION' ? 'orange' : 'default'}>{summary ? STATE_LABEL[summary.quality] : '读取中'}</Tag>}
          >
            <div className="dashboard-focus-score">
              <div><Typography.Text>已知关注项</Typography.Text><div className="dashboard-focus-number">{summary ? attentionCount : '—'}</div></div>
            </div>
            <div className="dashboard-focus-list">
              {focusItems.map((item) => (
                <a key={item.key} className={`dashboard-focus-item is-${item.type}`} href={item.path}>
                  <span className="dashboard-focus-icon">{focusIcon(item.type)}</span>
                  <span className="dashboard-focus-text">{item.text}</span>
                  <RightOutlined style={{ fontSize: 11, opacity: 0.5 }} />
                </a>
              ))}
              {!summary && !error ? <div className="dashboard-list-empty">正在读取权威摘要…</div> : null}
            </div>
          </Card>
        </Col>
      </Row>

      <Card variant="borderless" className="dashboard-health-card">
        <div className="dashboard-health-head">
          <Typography.Text strong>运营健康与数据质量</Typography.Text>
          <Tag color={summary?.quality === 'READY' ? 'green' : summary?.quality === 'ATTENTION' ? 'orange' : 'default'}>
            {summary ? STATE_LABEL[summary.quality] : '未解析'}
          </Tag>
        </div>
        <div className="dashboard-health-grid">
          {healthItems.map((item) => (
            <Tooltip key={item.key} title={item.detail} placement="top">
              <a className={`dashboard-health-item${item.path ? '' : ' is-static'}`} href={item.path}>
                <span className="dashboard-health-content"><span className="dashboard-health-label">{item.label}</span><span className="dashboard-health-value">{item.value}</span></span>
              </a>
            </Tooltip>
          ))}
        </div>
      </Card>

      <SectionHeading title="权威数据边界" />
      <Row gutter={[18, 18]} align="stretch" className="dashboard-card-row dashboard-analysis-row">
        <Col xs={24} xl={14}>
          <Card variant="borderless" className="dashboard-section-card dashboard-performance-card" title={<Typography.Text strong>Summary 水位与完整性</Typography.Text>}>
            {summary ? (
              <div className="dashboard-performance-summary">
                <div><span>数据水位</span><strong>{formatInstant(summary.dataWatermark, site.timezone)}</strong></div>
                <div><span>聚合水位</span><strong>{formatInstant(summary.aggregateWatermark, site.timezone)}</strong></div>
                <div><span>生成时间</span><strong>{formatInstant(summary.generatedAt, site.timezone)}</strong></div>
                <div><span>完整性</span><strong>{STATE_LABEL[summary.completeness]}</strong></div>
              </div>
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未取得 SiteDashboardSummary" />}
          </Card>
        </Col>
        <Col xs={24} xl={10}>
          <Card variant="borderless" className="dashboard-section-card dashboard-plant-card" title={<Typography.Text strong>设备 Population</Typography.Text>} extra={<span className="dashboard-card-state">{population ? STATE_LABEL[population.state] : '读取中'}</span>}>
            {population ? (
              <div className="dashboard-plant-overview-rows">
                <div className="dashboard-plant-overview-row is-neutral"><div className="dashboard-plant-overview-row-head"><span className="dashboard-plant-overview-system"><strong>已登记</strong><span>{population.registered} 台</span></span><span className="dashboard-plant-overview-judgement">Registry</span></div></div>
                <div className="dashboard-plant-overview-row is-neutral"><div className="dashboard-plant-overview-row-head"><span className="dashboard-plant-overview-system"><strong>可观察</strong><span>{population.observable} 台</span></span><span className="dashboard-plant-overview-judgement">Telemetry</span></div></div>
                <div className="dashboard-plant-overview-row is-neutral"><div className="dashboard-plant-overview-row-head"><span className="dashboard-plant-overview-system"><strong>未知/不可用</strong><span>{population.unknown + population.unavailable} 台</span></span><span className="dashboard-plant-overview-judgement">不进入可用率分母</span></div></div>
              </div>
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="设备 Population 正在读取" />}
            <a className="dashboard-plant-link" href={siteRoute(site, 'assets')}>查看设备台账 <RightOutlined /></a>
          </Card>
        </Col>
      </Row>

      <SectionHeading title="行动中心" />
      <Row gutter={[18, 18]} align="stretch" className="dashboard-card-row dashboard-action-row">
        <Col xs={24} xl={14}>
          <Card variant="borderless" className="dashboard-section-card dashboard-list-card" title={<Typography.Text strong>告警与诊断</Typography.Text>} extra={<Typography.Link href={siteRoute(site, 'alarms')}>查看 Alarm Owner <RightOutlined /></Typography.Link>}>
            <div className="dashboard-list-rows">
              <a className="dashboard-data-row" href={siteRoute(site, 'alarms')}>
                <span className="dashboard-row-content"><span className="dashboard-row-title">活动告警</span><span className="dashboard-row-meta">{alarms?.reason ?? `状态 ${STATE_LABEL[alarms?.state ?? 'UNAVAILABLE']}`}</span></span>
                <span className="dashboard-row-side"><strong className="dashboard-side-value">{alarms?.activeCount ?? '—'}</strong><span className="dashboard-side-label">{alarms?.highestSeverity ?? ''}</span></span>
              </a>
              <a className="dashboard-data-row" href={siteRoute(site, 'fdd')}>
                <span className="dashboard-row-content"><span className="dashboard-row-title">故障检测与诊断</span><span className="dashboard-row-meta">FDD Read Model 由后续 Intelligence slice 接入</span></span>
                <span className="dashboard-row-side"><strong className="dashboard-side-value">—</strong></span>
              </a>
            </div>
          </Card>
        </Col>
        <Col xs={24} xl={10}>
          <Card variant="borderless" className="dashboard-section-card dashboard-list-card dashboard-opportunity-card" title={<Typography.Text strong>优化机会</Typography.Text>} extra={<Typography.Link href={siteRoute(site, 'optimize')}>进入评审 <RightOutlined /></Typography.Link>}>
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Dashboard 不直接下发控制；优化建议进入评审/Command Preview 后再执行" />
          </Card>
        </Col>
      </Row>
    </div>
  );
}
