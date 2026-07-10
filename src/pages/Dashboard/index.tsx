import { useEffect, useMemo, useState } from 'react';
import {
  Button,
  Card,
  Col,
  Row,
  Space,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import {
  CheckCircleOutlined,
  FullscreenOutlined,
  RightOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useOps, fddList } from '@/store/ops';
import { MOCK_KPI } from '@/api';
import { DEVICE_META, STATUS_MAP, type DevStatus, type DeviceAsset } from '@/pages/Assets/meta';
import {
  SUGGESTION_STATUS_META,
  isSuggestionPendingDecision,
  isWorkOrderActive,
  isWorkOrderSlaRisk,
} from '@/domain/opsMeta';
import { canViewPath } from '@/auth/permissions';
import { useUi } from '@/store/ui';
import { currencyCny } from '@/utils/format';
import KpiCards from './KpiCards';
import SetpointVsActual from './SetpointVsActual';
import PlantRunMatrix from './PlantRunMatrix';
import PerformanceTrend from './PerformanceTrend';
import OperationsActionCenter from './OperationsActionCenter';
import './Dashboard.css';

type AssetHealthRow = DeviceAsset & { id: string; status: DevStatus; pointRate: number };

type FocusItem = {
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

const BUILDING_LABELS: Record<string, string> = {
  b1: '总部大楼',
  b2: '研发中心',
};

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

export default function Dashboard() {
  const navigate = useNavigate();
  const { role, buildingId } = useUi();
  const workOrders = useOps((state) => state.workOrders);
  const suggestions = useOps((state) => state.suggestions);
  const [lastUpdated, setLastUpdated] = useState(() => new Date());
  const canView = (path: string) => canViewPath(role, path);

  useEffect(() => {
    const timer = window.setInterval(() => setLastUpdated(new Date()), 15_000);
    return () => window.clearInterval(timer);
  }, []);

  const assetRows = useMemo<AssetHealthRow[]>(() => Object.entries(DEVICE_META).map(([id, meta]) => {
    const status = STATUS_MAP[id];
    return {
      id,
      status,
      pointRate: Math.round((meta.onlinePoints / meta.pointCount) * 100),
      ...meta,
    };
  }), []);

  const assetSummary = useMemo(() => {
    const running = assetRows.filter((row) => row.status === 'running').length;
    const abnormal = assetRows.filter((row) => row.status !== 'running').length;
    const avgPointRate = Math.round(assetRows.reduce((sum, row) => sum + row.pointRate, 0) / Math.max(assetRows.length, 1));
    return { running, abnormal, avgPointRate };
  }, [assetRows]);

  const activeFdd = fddList.filter((item) => item.severity !== 'info').length;
  const highRiskFdd = fddList.filter((item) => item.severity === 'critical' || item.severity === 'major').length;
  const activeTickets = workOrders.filter(isWorkOrderActive).length;
  const slaRiskTickets = workOrders.filter(isWorkOrderSlaRisk).length;
  const pendingOptimizations = suggestions.filter(isSuggestionPendingDecision).length;
  const approvedSaving = suggestions
    .filter((item) => item.status === 'approved' || item.status === 'dispatched')
    .reduce((sum, item) => sum + item.saving.cny, 0);

  const todoItems = ([
    highRiskFdd > 0
      ? { type: 'warning', text: `${highRiskFdd} 条高风险 FDD 诊断需要确认`, path: '/fdd' }
      : { type: 'success', text: '高风险 FDD 已清零', path: '/fdd' },
    slaRiskTickets > 0
      ? { type: 'warning', text: `${slaRiskTickets} 张 SLA 风险工单待处理`, path: '/alarms' }
      : { type: 'success', text: '工单 SLA 风险可控', path: '/alarms' },
    pendingOptimizations > 0
      ? { type: 'info', text: `${pendingOptimizations} 条优化建议等待评审或下发`, path: '/optimize' }
      : { type: 'success', text: '暂无待决策优化建议', path: '/optimize' },
  ] satisfies FocusItem[]).filter((item) => canView(item.path));

  const focusItems: FocusItem[] = todoItems.length > 0
    ? todoItems
    : [{ type: 'success', text: '当前角色可见范围内运行指标稳定', path: '/dashboard' }];
  const attentionCount = focusItems.filter((item) => item.type !== 'success').length;
  const attentionDevices = assetRows
    .filter((row) => row.status !== 'running' || row.pointRate < 95)
    .sort((a, b) => (a.status === 'running' ? 1 : 0) - (b.status === 'running' ? 1 : 0) || a.pointRate - b.pointRate)
    .slice(0, 2);
  const attentionAverage = attentionDevices.length
    ? attentionDevices.reduce((sum, row) => sum + row.pointRate, 0) / attentionDevices.length
    : 100;

  const healthItems: HealthItem[] = [
    {
      key: 'availability',
      label: '系统可用率',
      value: `${assetSummary.avgPointRate}%`,
      detail: `${assetSummary.running} / ${assetRows.length} 台设备运行`,
      path: canView('/assets') ? '/assets' : undefined,
    },
    ...(canView('/fdd') ? [{
      key: 'diagnosis',
      label: '活跃诊断',
      value: `${activeFdd} 条`,
      detail: `${highRiskFdd} 条高风险需要确认`,
      path: '/fdd',
    }] : []),
    ...(canView('/alarms') ? [{
      key: 'tickets',
      label: '待处理工单',
      value: `${activeTickets} 张`,
      detail: `${slaRiskTickets} 张存在 SLA 风险`,
      path: '/alarms',
    }] : []),
    ...(canView('/optimize') ? [{
      key: 'optimization',
      label: '优化决策',
      value: `${pendingOptimizations} 条`,
      detail: `已批准收益 ${currencyCny(approvedSaving)}`,
      path: '/optimize',
    }] : []),
    {
      key: 'comfort',
      label: '舒适度达标率（演示）',
      value: '92%',
      detail: '演示口径，2 个区域温度轻微偏离',
    },
  ];

  return (
    <div className="dashboard-page">
      <Row gutter={[18, 18]} align="stretch">
        <Col xs={24} xl={17}>
          <section className="dashboard-hero">
            <div className="dashboard-hero-header">
              <div className="dashboard-hero-copy">
                <div className="dashboard-eyebrow">
                  <span className="dashboard-live-dot" />
                  实时运营概览
                </div>
                <Typography.Title level={2} className="dashboard-hero-title">
                  {BUILDING_LABELS[buildingId] ?? buildingId}智慧能源运营总览
                </Typography.Title>
                <Space className="dashboard-context-chips" size={[7, 7]} wrap>
                  <Tooltip title={`数据延迟 ${MOCK_KPI.dataLatency} 秒`}>
                    <span className="dashboard-context-chip is-live">
                      实时连接 · {lastUpdated.toLocaleTimeString('zh-CN', { hour12: false })} 更新
                    </span>
                  </Tooltip>
                  <span className="dashboard-context-chip is-secondary">{MOCK_KPI.controlMode}</span>
                  <span className="dashboard-context-chip is-secondary">室外湿球 {MOCK_KPI.wetBulb}°C</span>
                </Space>
              </div>
              <div className="dashboard-hero-actions">
                <Space size={8} wrap>
                  {canView('/ai') && (
                    <Button icon={<RobotOutlined />} onClick={() => navigate('/ai')}>AI 工作台</Button>
                  )}
                  {canView('/bigscreen') && (
                    <Button icon={<FullscreenOutlined />} type="primary" onClick={() => navigate('/bigscreen')}>进入大屏</Button>
                  )}
                </Space>
              </div>
            </div>
            <KpiCards />
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
                <button
                  type="button"
                  key={`${item.path}-${item.text}`}
                  className={`dashboard-focus-item is-${item.type}`}
                  onClick={() => navigate(item.path)}
                >
                  <span className="dashboard-focus-icon">{focusIcon(item.type)}</span>
                  <span className="dashboard-focus-text">{item.text}</span>
                  <RightOutlined style={{ fontSize: 11, opacity: 0.5 }} />
                </button>
              ))}
            </div>
          </Card>
        </Col>
      </Row>

      <Card variant="borderless" className="dashboard-health-card">
        <div className="dashboard-health-head">
          <Typography.Text strong>运营健康</Typography.Text>
          <Tag color={assetSummary.abnormal > 0 || attentionCount > 0 ? 'orange' : 'green'}>
            {assetSummary.abnormal > 0 || attentionCount > 0 ? '存在待处理项' : '整体健康'}
          </Tag>
        </div>
        <div className="dashboard-health-grid">
          {healthItems.map((item) => (
            <Tooltip key={item.key} title={item.detail} placement="top">
              <button
                type="button"
                className={`dashboard-health-item${item.path ? '' : ' is-static'}`}
                onClick={() => item.path && navigate(item.path)}
              >
                <span className="dashboard-health-content">
                  <span className="dashboard-health-label">{item.label}</span>
                  <span className="dashboard-health-value">{item.value}</span>
                </span>
              </button>
            </Tooltip>
          ))}
        </div>
      </Card>

      <SectionHeading title="核心运行分析" />
      <Row gutter={[18, 18]} align="stretch" className="dashboard-card-row dashboard-analysis-row">
        <Col xs={24} xl={16}><PerformanceTrend /></Col>
        <Col xs={24} xl={8}>
          <PlantRunMatrix onViewAll={canView('/assets') ? () => navigate('/assets') : undefined} />
        </Col>
      </Row>

      {(canView('/fdd') || canView('/alarms') || canView('/optimize')) && (
        <>
          <SectionHeading title="行动中心" />
          <Row gutter={[18, 18]} align="stretch" className="dashboard-card-row dashboard-action-row">
            {(canView('/fdd') || canView('/alarms')) && (
              <Col xs={24} xl={canView('/optimize') ? 14 : 24}><OperationsActionCenter /></Col>
            )}
            {canView('/optimize') && (
              <Col xs={24} xl={(canView('/fdd') || canView('/alarms')) ? 10 : 24}>
                <Card
                  variant="borderless"
                  className="dashboard-section-card dashboard-list-card dashboard-opportunity-card"
                  title={<Typography.Text strong>优化机会</Typography.Text>}
                  extra={<Typography.Link onClick={() => navigate('/optimize')}>进入评审 <RightOutlined /></Typography.Link>}
                >
                  <div className="dashboard-list-summary dashboard-list-summary-two">
                    <div><span>待评审建议</span><strong>{pendingOptimizations}<small> 条</small></strong></div>
                    <div><span>已批准日收益</span><strong>{currencyCny(approvedSaving)}<small>/天</small></strong></div>
                  </div>
                  <div className="dashboard-list-rows">
                    {[...suggestions]
                      .sort((a, b) => (b.saving.cny * b.confidence) - (a.saving.cny * a.confidence))
                      .slice(0, 3)
                      .map((item, index) => (
                        <button type="button" className="dashboard-data-row dashboard-opportunity-row" key={item.id} onClick={() => navigate('/optimize')}>
                          <span className="dashboard-opportunity-rank">{String(index + 1).padStart(2, '0')}</span>
                          <span className="dashboard-row-content">
                            <span className="dashboard-row-title">{item.title}</span>
                            <span className="dashboard-row-meta dashboard-opportunity-meta">
                              {item.device}<span>·</span>{item.saving.kwh} kWh/天<span>·</span>{Math.round(item.confidence * 100)}% 置信度
                              <span>·</span>
                              <em className={`dashboard-risk-text is-${item.risk}`}>{item.risk === 'low' ? '低风险' : item.risk === 'medium' ? '中风险' : '高风险'}</em>
                            </span>
                          </span>
                          <span className="dashboard-row-side">
                            <strong className="dashboard-side-value">{currencyCny(item.saving.cny)}<small>/天</small></strong>
                            <span className={`dashboard-side-label is-${item.status === 'approved' || item.status === 'dispatched' ? 'success' : item.status === 'pending' ? 'warning' : 'neutral'}`}>
                              {SUGGESTION_STATUS_META[item.status].label}
                            </span>
                          </span>
                        </button>
                      ))}
                  </div>
                </Card>
              </Col>
            )}
          </Row>
        </>
      )}

      <SectionHeading title="设备与控制异常" />
      <Row gutter={[18, 18]} align="stretch" className="dashboard-card-row dashboard-device-row">
        {canView('/assets') && (
          <Col xs={24} xl={14}>
            <Card
              variant="borderless"
              className="dashboard-section-card dashboard-list-card dashboard-attention-card"
              title={<Typography.Text strong>需关注设备</Typography.Text>}
              extra={<Typography.Link onClick={() => navigate('/assets')}>查看台账 <RightOutlined /></Typography.Link>}
            >
              <div className="dashboard-list-rows">
                {attentionDevices.length ? attentionDevices.map((row) => {
                  const issue = row.status === 'alarm'
                    ? '通讯中断风险'
                    : row.status === 'maintenance'
                      ? '维护期间点位缺失'
                      : '点位质量下降';
                  const healthGap = Math.max(0, 95 - row.pointRate);
                  return (
                    <button type="button" className="dashboard-data-row dashboard-device-row-item" key={row.id} onClick={() => navigate('/assets')}>
                      <span className={`dashboard-row-indicator is-${row.status === 'alarm' ? 'critical' : row.status === 'maintenance' ? 'warning' : 'neutral'}`} />
                      <span className="dashboard-row-content">
                        <span className="dashboard-row-title">{row.name}</span>
                        <span className="dashboard-row-reason">{issue}</span>
                        <span className="dashboard-row-meta">{row.zoneName}<span>·</span>最后通讯 {row.lastSeen}</span>
                      </span>
                      <span className="dashboard-row-side">
                        <strong className={`dashboard-side-value${row.pointRate < 95 ? ' is-warning' : ''}`}>{row.pointRate}%</strong>
                        <span className="dashboard-side-label is-warning">低于健康线 {healthGap}%</span>
                      </span>
                    </button>
                  );
                }) : (
                  <div className="dashboard-list-empty">暂无需要关注的设备</div>
                )}
              </div>
              {attentionDevices.length > 0 && (
                <div className="dashboard-list-footer dashboard-device-footer">
                  <span>{attentionDevices.length} 台设备需现场确认 · 平均点位在线率 {attentionAverage.toFixed(1)}%</span>
                </div>
              )}
            </Card>
          </Col>
        )}
        <Col xs={24} xl={canView('/assets') ? 10 : 24}><SetpointVsActual /></Col>
      </Row>
    </div>
  );
}
