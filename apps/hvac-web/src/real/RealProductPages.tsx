import { useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  ConfigProvider,
  Descriptions,
  Empty,
  Input,
  Progress,
  Row,
  Segmented,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  theme as antdTheme,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  AlertOutlined,
  ApartmentOutlined,
  BugOutlined,
  CalendarOutlined,
  CloudOutlined,
  ControlOutlined,
  DollarOutlined,
  ExperimentOutlined,
  FieldTimeOutlined,
  FileDoneOutlined,
  FundOutlined,
  LineChartOutlined,
  NodeIndexOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
} from '@/components/OperationsUI';
import type { CurrentPrincipalResponse, DashboardMetric, Site } from '@/api/generated/platformGateway.gen';
import { useSiteDashboardSummary } from '@/api/site-dashboard';
import {
  getLatestOptimizationRecommendation,
  getSiteLoadForecast,
  listSiteFDDFindings,
  type FDDFinding,
  type PublishedForecast,
  type PublishedRecommendation,
} from '@/api/intelligence';
import { boundaryMeta } from '@/features/real-read-model-boundary';
import { FDD_READ_MODEL_BOUNDARY } from '@/features/fdd/capability';
import { FORECAST_READ_MODEL_BOUNDARY } from '@/features/forecast/capability';
import { OPTIMIZATION_READ_MODEL_BOUNDARY } from '@/features/optimization/capability';
import { SETTLEMENT_READ_MODEL_BOUNDARY } from '@/features/settlement/capability';
import { FocusHeading } from './FocusHeading';
import { siteRoute } from './site-routing';
import '@/styles/real-product-pages.css';

interface RealProductPageProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

function useIntelligenceResource<T>(siteId: string, load: (siteId: string, signal?: AbortSignal) => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    load(siteId, controller.signal)
      .then((value) => setData(value))
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Intelligence 服务暂时不可用。');
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [siteId, load]);
  return { data, loading, error };
}

function numberValue(object: Record<string, unknown>, key: string): number | null {
  const value = object[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

function stringValue(object: Record<string, unknown>, key: string): string | null {
  const value = object[key];
  return typeof value === 'string' && value.length > 0 ? value : null;
}

type EmptyRow = { key: string };

function ProductBoundary({
  children,
  site,
  testId,
  state = 'NOT_INTEGRATED',
}: {
  children: ReactNode;
  site: Readonly<Site>;
  testId: string;
  state?: string;
}) {
  return (
    <section data-testid={testId} data-business-state={state} data-site-id={site.id}>
      {children}
    </section>
  );
}

function EmptyProductTable({
  columns,
  description,
  scroll,
}: {
  columns: ColumnsType<EmptyRow>;
  description: string;
  scroll?: number;
}) {
  return (
    <Table<EmptyRow>
      rowKey="key"
      columns={columns}
      dataSource={[]}
      pagination={false}
      scroll={scroll ? { x: scroll } : undefined}
      locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={description} /> }}
    />
  );
}

const COST_COLUMNS: ColumnsType<EmptyRow> = [
  { title: '建议', key: 'suggestion', width: 260 },
  { title: '预计节省', key: 'saving', width: 150 },
  { title: '批准时间', key: 'approvedAt', width: 180 },
  { title: '效果跟踪', key: 'tracking', width: 160 },
  { title: '状态', key: 'status', width: 120 },
];

export function RealFddPage({ site }: RealProductPageProps) {
  const { data: findings, loading, error } = useIntelligenceResource<FDDFinding[]>(site.id, listSiteFDDFindings);
  const rows = findings ?? [];
  const linkedWorkOrders = rows.filter((finding) => Boolean(finding.workOrderId)).length;
  const highRisk = rows.filter((finding) => finding.confidence >= 0.8).length;
  const averageConfidence = rows.length > 0 ? Math.round(rows.reduce((sum, finding) => sum + finding.confidence, 0) / rows.length * 100) : 0;
  const columns: ColumnsType<FDDFinding> = [
    { title: '诊断', dataIndex: 'findingType', width: 220, render: (value: string) => <Typography.Text strong>{value}</Typography.Text> },
    { title: 'Asset', dataIndex: 'assetId', width: 300, ellipsis: true },
    { title: '评估窗口', key: 'window', width: 300, render: (_, row) => `${new Date(row.evaluationFrom).toLocaleString()} → ${new Date(row.evaluationTo).toLocaleString()}` },
    { title: '置信度', dataIndex: 'confidence', width: 120, render: (value: number) => `${Math.round(value * 100)}%` },
    { title: '证据', key: 'evidence', width: 100, render: (_, row) => `${row.evidenceIds.length} 项` },
    { title: 'Rule / Model Revision', key: 'revision', width: 300, render: (_, row) => row.ruleRevisionId || row.modelDeploymentRevisionId || '—' },
    { title: 'Alarm', dataIndex: 'alarmId', width: 280, render: (value?: string) => value || '未关联' },
    { title: 'Work Order', dataIndex: 'workOrderId', width: 280, render: (value?: string) => value || '未关联' },
  ];
  return (
    <ProductBoundary site={site} testId="real-site-route-fdd" state={FDD_READ_MODEL_BOUNDARY.status}>
      <PageScaffold
        title="故障检测与诊断 FDD"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><BugOutlined />故障检测与诊断 FDD</Space></FocusHeading>}
        extra={<Tag>{boundaryMeta(FDD_READ_MODEL_BOUNDARY)}</Tag>}
      >
        <Alert
          type={error ? 'error' : 'info'}
          showIcon
          icon={<BugOutlined />}
          message={error ? 'FDD 权威结果读取失败' : 'FDD Finding 是证据化诊断事实，不等同于 Alarm 或 Work Order。'}
          description={error ?? '每条 Finding 都保留评估窗口、证据、Rule/Model Revision 和显式 Alarm/Work Order 链接；检测本身不会直接产生控制动作。'}
        />
        <OperationsMetrics items={[
          { label: '活跃诊断', value: loading ? '…' : rows.length, detail: '权威 FDD Finding', icon: <BugOutlined />, tone: 'accent' },
          { label: '高置信 Finding', value: loading ? '…' : highRisk, detail: 'confidence ≥ 80%', icon: <SafetyCertificateOutlined /> },
          { label: '已关联工单', value: loading ? '…' : linkedWorkOrders, detail: '显式 Work Order 链接', icon: <NodeIndexOutlined /> },
          { label: '平均置信度', value: loading ? '…' : averageConfidence, suffix: '%', detail: rows.length ? '来自已发布 Finding' : '当前无 Finding', icon: <ThunderboltOutlined /> },
        ]} />
        <Card variant="borderless" styles={{ body: { padding: 16 } }}>
          <OperationsPanelHeading icon={<NodeIndexOutlined />} title="诊断列表" meta={`${rows.length} 条`} />
          <Table<FDDFinding> rowKey="id" loading={loading} columns={columns} dataSource={rows} pagination={{ pageSize: 20 }} scroll={{ x: 1800 }} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前 Site 没有已发布 FDD Finding" /> }} />
        </Card>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function RealOptimizePage({ site }: RealProductPageProps) {
  const { data: published, loading, error } = useIntelligenceResource<PublishedRecommendation | null>(site.id, getLatestOptimizationRecommendation);
  const recommendation = published?.recommendation ?? null;
  const energySaving = recommendation ? numberValue(recommendation.expectedImpact, 'energySavingKWhPerDay') : null;
  const costSaving = recommendation ? numberValue(recommendation.expectedImpact, 'costSavingPerDay') : null;
  const risk = recommendation ? stringValue(recommendation.risk, 'level') : null;
  const candidateSupply = recommendation ? numberValue(recommendation.candidate, 'supplyTempC') : null;
  const revalidationState = recommendation?.currentStateRevalidation?.accepted ? '已复核' : '未复核';
  return (
    <ProductBoundary site={site} testId="real-site-route-optimize" state={OPTIMIZATION_READ_MODEL_BOUNDARY.status}>
      <PageScaffold
        title="节能优化建议"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><ThunderboltOutlined />节能优化建议</Space></FocusHeading>}
        extra={<Tag>{boundaryMeta(OPTIMIZATION_READ_MODEL_BOUNDARY)}</Tag>}
      >
        <Alert
          type={error ? 'error' : 'info'}
          showIcon
          icon={<ExperimentOutlined />}
          message={error ? 'Optimization 权威建议读取失败' : 'Recommendation 不是 Command。审批通过后仍必须进行独立当前状态复核。'}
          description={error ?? '页面只展示已 PUBLISHED 的 Recommendation：收益、约束、不确定性、风险、回滚和验证计划都必须完整；这里没有直接下发控制的按钮。'}
        />
        <OperationsMetrics items={[
          { label: '预计节电', value: loading ? '…' : energySaving ?? '—', suffix: 'kWh/天', detail: recommendation ? '模型预期收益' : '当前无已发布建议', icon: <ThunderboltOutlined />, tone: 'accent' },
          { label: '预计节省', value: loading ? '…' : costSaving ?? '—', detail: recommendation ? '同一冻结基线' : '当前无已发布建议', icon: <DollarOutlined /> },
          { label: '风险', value: loading ? '…' : risk ?? '—', detail: recommendation ? 'Recommendation 风险评估' : '当前无已发布建议', icon: <SafetyCertificateOutlined /> },
          { label: '当前状态复核', value: loading ? '…' : recommendation ? revalidationState : '—', detail: '创建 Command 前必须通过', icon: <FieldTimeOutlined /> },
        ]} />
        {recommendation ? (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <Card variant="borderless" title={<OperationsPanelHeading icon={<SafetyCertificateOutlined />} title="最新 Recommendation" meta={`${published?.runStatus ?? '—'} · ${recommendation.approval}`} />}>
              <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
                <Descriptions.Item label="Run ID">{published?.runId}</Descriptions.Item>
                <Descriptions.Item label="Recommendation ID">{recommendation.id}</Descriptions.Item>
                <Descriptions.Item label="Deployment Revision">{recommendation.deploymentRevisionId}</Descriptions.Item>
                <Descriptions.Item label="Input Snapshot">{recommendation.inputSnapshotId}</Descriptions.Item>
                <Descriptions.Item label="Candidate Supply Temp">{candidateSupply === null ? '—' : `${candidateSupply} °C`}</Descriptions.Item>
                <Descriptions.Item label="Risk">{risk ?? '—'}</Descriptions.Item>
                <Descriptions.Item label="Approval">{recommendation.approval}</Descriptions.Item>
                <Descriptions.Item label="Command Intent">{recommendation.commandIntentId || '无；Recommendation 未直接产生 Command'}</Descriptions.Item>
              </Descriptions>
            </Card>
            <Row gutter={[16, 16]}>
              <Col xs={24} xl={12}><Card title="约束 / 不确定性" variant="borderless"><pre className="real-json-evidence">{JSON.stringify({ constraints: recommendation.constraints, uncertainty: recommendation.uncertainty, risk: recommendation.risk }, null, 2)}</pre></Card></Col>
              <Col xs={24} xl={12}><Card title="回滚 / 验证计划" variant="borderless"><pre className="real-json-evidence">{JSON.stringify({ rollbackPlan: recommendation.rollbackPlan, verificationPlan: recommendation.verificationPlan }, null, 2)}</pre></Card></Col>
            </Row>
          </Space>
        ) : !loading && !error ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前 Site 没有已发布 Optimization Recommendation" /> : null}
      </PageScaffold>
    </ProductBoundary>
  );
}

export function RealForecastPage({ site }: RealProductPageProps) {
  const { data: forecast, loading, error } = useIntelligenceResource<PublishedForecast | null>(site.id, getSiteLoadForecast);
  const points = forecast?.points ?? [];
  const first = points[0];
  const last = points[points.length - 1];
  const columns: ColumnsType<PublishedForecast['points'][number]> = [
    { title: '预测时间', dataIndex: 'forecast_for', width: 220, render: (value: string) => new Date(value).toLocaleString() },
    { title: 'Horizon', dataIndex: 'horizon_minutes', width: 120, render: (value: number) => `${value} min` },
    { title: '预测值', dataIndex: 'value', width: 140, render: (value: number, row) => `${value.toFixed(1)} ${row.unit}` },
    { title: '下界', dataIndex: 'lower_bound', width: 120, render: (value: number | null, row) => value === null ? '—' : `${value.toFixed(1)} ${row.unit}` },
    { title: '上界', dataIndex: 'upper_bound', width: 120, render: (value: number | null, row) => value === null ? '—' : `${value.toFixed(1)} ${row.unit}` },
    { title: '质量', dataIndex: 'quality', width: 120, render: (value: string) => <Tag color={value === 'FALLBACK' ? 'warning' : value === 'VALID' ? 'success' : 'processing'}>{value}</Tag> },
    { title: 'Model Version', key: 'model', width: 320, render: (_, row) => `${row.model_version_id} · r${row.model_version}` },
    { title: 'Input Snapshot', dataIndex: 'input_snapshot_id', width: 300, ellipsis: true },
  ];
  return (
    <ProductBoundary site={site} testId="real-site-route-forecast" state={FORECAST_READ_MODEL_BOUNDARY.status}>
      <PageScaffold
        title="预测与基线"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><LineChartOutlined />预测与基线</Space></FocusHeading>}
        extra={<Tag>{boundaryMeta(FORECAST_READ_MODEL_BOUNDARY)}</Tag>}
      >
        <Alert
          type={error ? 'error' : forecast?.snapshot.quality === 'FALLBACK' ? 'warning' : 'info'}
          showIcon
          icon={<CalendarOutlined />}
          message={error ? 'Forecast 权威结果读取失败' : forecast?.snapshot.quality === 'FALLBACK' ? '当前结果为 FALLBACK，不是模型预测。' : '预测来自已 PERSISTED Forecast Snapshot。'}
          description={error ?? (forecast?.snapshot.quality === 'FALLBACK' ? '历史样本不足时只返回最后观测值，且不提供伪造的不确定性区间；UI 必须显式区分。' : '每个点保留 Model/Feature/Input/Topology provenance，并展示模型不确定性区间。')}
        />
        <OperationsMetrics items={[
          { label: '预测状态', value: loading ? '…' : forecast?.snapshot.quality ?? '—', detail: forecast ? forecast.snapshot.target : '当前无已发布预测', icon: <LineChartOutlined />, tone: 'accent' },
          { label: '预测点数', value: loading ? '…' : points.length, detail: first && last ? `${new Date(first.forecast_for).toLocaleString()} → ${new Date(last.forecast_for).toLocaleString()}` : '当前无已发布预测', icon: <FieldTimeOutlined /> },
          { label: '模型版本', value: loading ? '…' : first ? `r${first.model_version}` : '—', detail: first?.model_version_id ?? '当前无模型结果', icon: <SafetyCertificateOutlined /> },
          { label: '不确定性', value: loading ? '…' : first?.lower_bound !== null && first?.lower_bound !== undefined ? '有区间' : '无区间', detail: forecast?.snapshot.quality === 'FALLBACK' ? 'Fallback 不伪造区间' : '95% 回归残差带', icon: <FundOutlined /> },
        ]} />
        {forecast ? (
          <Card variant="borderless" title={<OperationsPanelHeading icon={<LineChartOutlined />} title="预测契约状态" meta={boundaryMeta(FORECAST_READ_MODEL_BOUNDARY)} />}>
            <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
              <Descriptions.Item label="Target">{forecast.snapshot.target}</Descriptions.Item>
              <Descriptions.Item label="Forecast Origin">{new Date(forecast.snapshot.forecastOrigin).toLocaleString()}</Descriptions.Item>
              <Descriptions.Item label="Window">{new Date(forecast.snapshot.windowStart).toLocaleString()} → {new Date(forecast.snapshot.windowEnd).toLocaleString()}</Descriptions.Item>
              <Descriptions.Item label="Snapshot">{forecast.snapshot.snapshotId}</Descriptions.Item>
              <Descriptions.Item label="Deployment">{forecast.snapshot.deploymentId}</Descriptions.Item>
              <Descriptions.Item label="Model Version">{forecast.snapshot.modelVersionId}</Descriptions.Item>
              <Descriptions.Item label="Quality / Fallback"><Tag color={forecast.snapshot.quality === 'FALLBACK' ? 'warning' : 'success'}>{forecast.snapshot.quality}</Tag></Descriptions.Item>
              <Descriptions.Item label="Site Timezone">{site.timezone}</Descriptions.Item>
            </Descriptions>
          </Card>
        ) : null}
        <Card variant="borderless" title={<OperationsPanelHeading icon={<FundOutlined />} title="预测序列" meta={`${points.length} 条`} />}>
          <Table<PublishedForecast['points'][number]> rowKey="forecast_id" loading={loading} columns={columns} dataSource={points} pagination={{ pageSize: 24 }} scroll={{ x: 1600 }} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前 Site 没有已发布 SITE_LOAD Forecast" /> }} />
        </Card>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function RealControlPage({ site }: RealProductPageProps) {
  return (
    <ProductBoundary site={site} testId="real-site-route-control">
      <PageScaffold
        title="能源控制"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><ControlOutlined />能源控制</Space></FocusHeading>}
        extra={<Tag>受治理 Command</Tag>}
      >
        <Alert
          type="info"
          showIcon
          message="所有控制动作先预览，再提交到 Command 治理流程"
          description="页面不会直接调用设备或 Provider。提交前必须确认 Tenant、Site、Asset、Device Endpoint、当前反馈、请求值、允许范围、风险、安全校验和有效期；最终结果以权威反馈验证为准。"
        />
        <OperationsMetrics items={[
          { label: '控制范围', value: site.displayName, detail: `Site · ${site.id}`, icon: <ApartmentOutlined />, tone: 'accent' },
          { label: '授权主体', value: '当前 Principal', detail: '由 Session / IAM 派生' },
          { label: '风险评估', value: '服务端计算', detail: '不由浏览器猜测', icon: <SafetyCertificateOutlined /> },
          { label: '执行结果', value: '权威反馈验证', detail: 'SUCCEEDED 才能作为完成证据', icon: <ControlOutlined /> },
        ]} />
        <Card variant="borderless" title={<OperationsPanelHeading icon={<SafetyCertificateOutlined />} title="控制契约" meta="SITE SCOPED" />}>
          <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
            <Descriptions.Item label="Tenant">{site.tenantId}</Descriptions.Item>
            <Descriptions.Item label="Site">{site.displayName} · {site.id}</Descriptions.Item>
            <Descriptions.Item label="Authority">Platform Gateway / Command Service</Descriptions.Item>
            <Descriptions.Item label="Mode">Preview → Submit → Validate → Dispatch → Verify</Descriptions.Item>
            <Descriptions.Item label="Safety">S2 当前证据、授权、Capability、审批与服务端约束</Descriptions.Item>
            <Descriptions.Item label="Unknown outcome">不可自动重发，先核对设备状态和审计证据</Descriptions.Item>
          </Descriptions>
        </Card>
        <Card variant="borderless" title={<OperationsPanelHeading icon={<ApartmentOutlined />} title="选择受控 Asset" meta="从权威资产模型进入" />}>
          <Typography.Paragraph type="secondary">真实设备功能和当前反馈只在 Asset 详情中展示。当前 Command 列表 Read Model 尚未接入，因此不会填充演示命令或虚构执行状态。</Typography.Paragraph>
          <Button type="primary" href={siteRoute(site, 'assets')}>前往设备与建筑</Button>
        </Card>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function RealCostPage({ site }: RealProductPageProps) {
  return (
    <ProductBoundary site={site} testId="real-site-route-cost">
      <PageScaffold
        title="成本与绩效"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><DollarOutlined />成本与绩效</Space></FocusHeading>}
        extra={<Segmented disabled value="month" options={[{ label: '日', value: 'day' }, { label: '周', value: 'week' }, { label: '月', value: 'month' }]} />}
      >
        <Alert
          type="info"
          showIcon
          message="成本、预算、收益和碳绩效必须来自权威财务与能源模型。"
          description="当前保留 Demo 的全部分析区块，不使用浏览器估算、演示电价或演示金额替代。"
        />
        <OperationsMetrics items={[
          { label: '今日电费', value: '—', detail: '等待成本聚合', icon: <DollarOutlined />, tone: 'accent' },
          { label: '累计节能收益', value: '—', detail: '等待优化效果归因', icon: <ThunderboltOutlined />, tone: 'positive' },
          { label: '累计减碳', value: '—', suffix: 'kgCO₂', detail: '等待真实折算口径', icon: <CloudOutlined /> },
          { label: '投资回报 ROI', value: '—', suffix: '%', detail: '等待成本与收益基线', icon: <FundOutlined /> },
        ]} />
        <OperationsInsightBand
          title="成本诊断"
          icon={<SafetyCertificateOutlined />}
          items={[
            { text: '峰平谷电价和预算基线尚未接入，当前不计算成本异常。', tone: 'info' },
            { text: '节能收益必须与已批准优化建议和真实执行结果关联。', tone: 'positive' },
          ]}
        />
        <Row gutter={[16, 16]}>
          <Col xs={24} xl={15}>
            <Card title={<OperationsPanelHeading icon={<FundOutlined />} title="能耗成本趋势" meta="当前周期" />} variant="borderless" className="ops-chart-card real-product-chart-card">
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="成本时序 Read Model 尚未接入" />
            </Card>
          </Col>
          <Col xs={24} xl={9}>
            <Card title={<OperationsPanelHeading icon={<DollarOutlined />} title="今日峰平谷费用结构" />} variant="borderless" className="ops-chart-card real-product-chart-card">
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="峰平谷电价与分时费用尚未接入" />
            </Card>
          </Col>
        </Row>
        <Row gutter={[16, 16]}>
          <Col xs={24} xl={8}>
            <Card title="年度节能达标率" variant="borderless">
              <Progress type="dashboard" percent={0} format={() => '—'} />
              <Typography.Paragraph type="secondary">年度目标与基线尚未接入。</Typography.Paragraph>
            </Card>
          </Col>
          <Col xs={24} xl={16}>
            <Card title="峰平谷成本明细" variant="borderless">
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="分时成本明细尚未接入" />
            </Card>
          </Col>
        </Row>
        <Row gutter={[16, 16]}>
          <Col xs={24} xl={12}>
            <Card title="年度节能收益" variant="borderless" className="real-product-chart-card">
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="年度节能收益归因尚未接入" />
            </Card>
          </Col>
          <Col xs={24} xl={12}>
            <Card title="碳减排折算" variant="borderless" className="real-product-chart-card">
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="碳排放因子和折算口径尚未接入" />
            </Card>
          </Col>
        </Row>
        <Card variant="borderless" title={<OperationsPanelHeading icon={<ThunderboltOutlined />} title="已批准节能建议" meta="0 条" />}>
          <EmptyProductTable columns={COST_COLUMNS} scroll={900} description="没有可验证的已批准节能建议" />
        </Card>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function RealSettlementPage({ site }: RealProductPageProps) {
  return (
    <ProductBoundary site={site} testId="real-site-route-settlement">
      <PageScaffold
        title="结算与对账"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><FileDoneOutlined />结算与对账</Space></FocusHeading>}
        extra={<Tag>{boundaryMeta(SETTLEMENT_READ_MODEL_BOUNDARY)}</Tag>}
      >
        <Alert
          type="info"
          showIcon
          message={`${SETTLEMENT_READ_MODEL_BOUNDARY.label} 尚未接入`}
          description={`结算能力接入前必须提供：${SETTLEMENT_READ_MODEL_BOUNDARY.requiredFields.join('、')}。当前不计算金额、不伪造锁定或修订结果。`}
        />
        <OperationsMetrics items={[
          { label: '结算周期', value: '—', detail: '等待周期与 Site 时区', icon: <CalendarOutlined />, tone: 'accent' },
          { label: '锁定状态', value: '—', detail: 'OPEN / LOCKED / REVISED 尚未确认', icon: <SafetyCertificateOutlined /> },
          { label: '修订版本', value: '—', detail: '等待 revision 与 lineage', icon: <FileDoneOutlined /> },
          { label: '对账差异', value: '—', detail: '等待 reconciliation 结果', icon: <DollarOutlined /> },
        ]} />
        <Card variant="borderless" title={<OperationsPanelHeading icon={<FileDoneOutlined />} title="结算事实边界" meta={boundaryMeta(SETTLEMENT_READ_MODEL_BOUNDARY)} />}>
          <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
            <Descriptions.Item label="Period">—</Descriptions.Item>
            <Descriptions.Item label="Status">—</Descriptions.Item>
            <Descriptions.Item label="Revision">—</Descriptions.Item>
            <Descriptions.Item label="Source Reading Lineage">—</Descriptions.Item>
            <Descriptions.Item label="Tariff Version">—</Descriptions.Item>
            <Descriptions.Item label="Reconciliation">—</Descriptions.Item>
            <Descriptions.Item label="Correction History">—</Descriptions.Item>
            <Descriptions.Item label="Site Timezone">{site.timezone}</Descriptions.Item>
          </Descriptions>
        </Card>
        <OperationsInsightBand
          title="当前不可计算"
          icon={<SafetyCertificateOutlined />}
          items={[
            { text: '空值表示结算能力尚未接入，不表示费用为 0。', tone: 'info' },
            { text: '只有已锁定周期才能作为成本、绩效和收益归因的稳定输入。', tone: 'positive' },
          ]}
        />
      </PageScaffold>
    </ProductBoundary>
  );
}

export function RealAiLanding({
  site,
  principal,
  operationsPath,
}: RealProductPageProps & { operationsPath: string }) {
  return (
    <ProductBoundary site={site} testId="real-site-route-ai" state="READY">
      <PageScaffold
        title="AI 运维助手"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><RobotOutlined />AI 运维助手</Space></FocusHeading>}
        extra={<Tag color="processing">真实 Operations Agent</Tag>}
      >
        <Alert
          type="info"
          showIcon
          message="AI 助手只使用当前站点的权威调查、证据和工具活动。"
          description="Plan、Evidence、Finding、Tool activity 与 Operator Input 均来自 Gateway projection，不读取 Demo 会话。"
        />
        <OperationsMetrics items={[
          { label: '进行中调查', value: '进入工作台查看', detail: '按当前 Site 隔离', icon: <RobotOutlined />, tone: 'accent' },
          { label: '证据来源', value: '权威投影', detail: '不从浏览器补造' },
          { label: '人工确认', value: '必需', detail: '关键结论由操作员确认', icon: <SafetyCertificateOutlined /> },
          { label: '工具执行', value: '受控', detail: '受 Capability 与 Site Scope 约束', icon: <ExperimentOutlined /> },
        ]} />
        <Row gutter={[16, 16]} align="stretch" className="real-ai-workspace">
          <Col xs={24} xl={6}>
            <Card title="会话与任务" variant="borderless" className="real-ai-panel">
              <Space direction="vertical" size={12} style={{ width: '100%' }}>
                <Input.Search disabled placeholder="搜索调查或设备" />
                <Select disabled value="all" options={[{ label: '全部状态', value: 'all' }]} style={{ width: '100%' }} />
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="调查列表由 Operations Workspace 提供" />
              </Space>
            </Card>
          </Col>
          <Col xs={24} xl={12}>
            <Card title="AI 调查工作台" variant="borderless" className="real-ai-panel real-ai-panel--primary">
              <div className="real-ai-assistant-intro">
                <RobotOutlined />
                <Typography.Title level={4}>面向当前 Site 的运维调查</Typography.Title>
                <Typography.Paragraph type="secondary">
                  创建、恢复并审阅调查。Agent 的计划、证据、发现、工具活动和操作员输入均保留可审计来源。
                </Typography.Paragraph>
              </div>
              <div className="real-ai-prompt-placeholder">
                <Input.TextArea disabled rows={5} placeholder="在调查工作台中输入设备现象、时间范围和需要验证的问题" />
                <Button type="primary" icon={<RobotOutlined />} href={operationsPath}>进入调查工作台</Button>
              </div>
            </Card>
          </Col>
          <Col xs={24} xl={6}>
            <Space direction="vertical" size={16} style={{ width: '100%' }}>
              <Card title="当前上下文" variant="borderless" className="real-ai-panel">
                <Descriptions column={1} size="small">
                  <Descriptions.Item label="Site">{site.displayName}</Descriptions.Item>
                  <Descriptions.Item label="时区">{site.timezone}</Descriptions.Item>
                  <Descriptions.Item label="用户">{principal.principal.displayName}</Descriptions.Item>
                  <Descriptions.Item label="Capabilities">{principal.authorization.capabilities.length}</Descriptions.Item>
                </Descriptions>
              </Card>
              <Card title="关联业务" variant="borderless" className="real-ai-panel">
                <Space direction="vertical" style={{ width: '100%' }}>
                  <Button block href={siteRoute(site, 'assets')}>查看设备台账</Button>
                  <Button block href={siteRoute(site, 'fdd')}>查看 FDD 证据</Button>
                  <Button block href={siteRoute(site, 'alarms')}>进入报警工单</Button>
                  <Button block href={siteRoute(site, 'optimize')}>评审优化建议</Button>
                </Space>
              </Card>
            </Space>
          </Col>
        </Row>
      </PageScaffold>
    </ProductBoundary>
  );
}

type BigScreenScene = 'overview' | 'energy' | 'operations';

function BigScreenPanel({ title, eyebrow, children, accent = false }: {
  title: string;
  eyebrow: string;
  children: ReactNode;
  accent?: boolean;
}) {
  return (
    <section className={`bigscreen-panel${accent ? ' is-accent' : ''}`}>
      <header className="bigscreen-panel-header">
        <span className="bigscreen-panel-heading">
          <small className="bigscreen-panel-eyebrow">{eyebrow}</small>
          <strong className="bigscreen-panel-title">{title}</strong>
        </span>
      </header>
      <div className="bigscreen-panel-body">{children}</div>
    </section>
  );
}

function formatBigScreenMetric(metric: DashboardMetric | undefined, fallbackUnit = ''): [string, string] {
  if (!metric || metric.value === null) return ['—', metric?.reason ?? '未提供权威值'];
  const value = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(metric.value);
  const unit = metric.unit ?? fallbackUnit;
  return [`${value}${unit ? ` ${unit}` : ''}`, metric.state];
}

export function RealBigScreenPage({ site, principal }: RealProductPageProps) {
  const [scene, setScene] = useState<BigScreenScene>('overview');
  const [clock, setClock] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setClock(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const clockText = useMemo(() => clock.toLocaleTimeString('zh-CN', { hour12: false }), [clock]);
  const summaryQuery = useSiteDashboardSummary(
    principal.context.tenantId,
    site.id,
    `${principal.session.id}:${principal.authorization.policyRevision}`,
  );
  const summary = summaryQuery.data;
  const [powerValue, powerState] = formatBigScreenMetric(summary?.fastMetrics.currentPower, 'kW');
  const [copValue, copState] = formatBigScreenMetric(summary?.slowMetrics.cop);
  const [energyValue, energyState] = formatBigScreenMetric(summary?.slowMetrics.siteLocalDayEnergy, 'kWh');
  const [savingsValue, savingsState] = formatBigScreenMetric(summary?.slowMetrics.baselineSavings, '%');
  const availabilityValue = summary?.devicePopulation.availabilityPercent == null
    ? '—'
    : `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(summary.devicePopulation.availabilityPercent)}%`;
  const alarmValue = summary?.fastMetrics.openAlarms.activeCount == null ? '—' : String(summary.fastMetrics.openAlarms.activeCount);

  return (
    <ConfigProvider theme={{ algorithm: antdTheme.darkAlgorithm }}>
      <ProductBoundary site={site} testId="real-site-route-bigscreen" state={summaryQuery.isPending ? 'LOADING' : summary?.quality ?? 'UNAVAILABLE'}>
        <main className="bigscreen-shell real-bigscreen-shell">
          <span className="real-shell-sr-only" data-testid="real-shell-site">{site.displayName}</span>
          <div className="bigscreen-stage">
            <header className="bigscreen-header">
              <div className="bigscreen-brand">
                <div className="bigscreen-brand-mark"><FundOutlined /></div>
                <div className="bigscreen-brand-copy">
                  <span className="bigscreen-eyebrow">HVAC INTELLIGENT OPERATIONS</span>
                  <h1>智慧能源运行大屏</h1>
                  <p>{site.displayName} · {site.code}</p>
                </div>
              </div>
              <nav className="bigscreen-scenes" aria-label="大屏场景">
                {([
                  ['overview', '运行总览'],
                  ['energy', '能耗分析'],
                  ['operations', '运维行动'],
                ] as [BigScreenScene, string][]).map(([value, label]) => (
                  <button key={value} type="button" className={`bigscreen-scene-button${scene === value ? ' is-active' : ''}`} onClick={() => setScene(value)}>{label}</button>
                ))}
              </nav>
              <div className="bigscreen-header-meta">
                <span className="bigscreen-live-status">权威 Summary · {summary?.quality ?? (summaryQuery.isPending ? 'LOADING' : 'UNAVAILABLE')}</span>
                <span className="bigscreen-meta-item is-optional">{summary?.asOf ? `As of ${summary.asOf}` : site.timezone}</span>
                <strong className="bigscreen-clock">{clockText}</strong>
              </div>
            </header>

            <section className="bigscreen-kpi-band" aria-label="运行关键指标">
              {[
                ['实时功率', powerValue, powerState],
                ['综合 COP', copValue, copState],
                ['今日能耗', energyValue, energyState],
                ['今日节能', savingsValue, savingsState],
                ['设备可用率', availabilityValue, summary?.devicePopulation.state ?? 'UNAVAILABLE'],
                ['活动告警', alarmValue, summary?.fastMetrics.openAlarms.state ?? 'UNAVAILABLE'],
              ].map(([label, value, sub]) => (
                <div className="bigscreen-kpi-card" key={label}>
                  <span className="bigscreen-kpi-icon"><ThunderboltOutlined /></span>
                  <span className="bigscreen-kpi-copy">
                    <span className="bigscreen-kpi-label">{label}</span>
                    <strong className="bigscreen-kpi-value">{value}</strong>
                    <span className="bigscreen-kpi-sub">{sub}</span>
                  </span>
                </div>
              ))}
            </section>

            <section className="bigscreen-body" data-scene={scene}>
              <div className="bigscreen-column bigscreen-column-left">
                <BigScreenPanel eyebrow="ENERGY" title="站点本地日能耗">
                  {summary ? (
                    <Descriptions column={1} size="small">
                      <Descriptions.Item label="本地日能耗">{energyValue}</Descriptions.Item>
                      <Descriptions.Item label="状态">{energyState}</Descriptions.Item>
                      <Descriptions.Item label="数据水位">{summary.slowMetrics.siteLocalDayEnergy.dataWatermark ?? '未提供'}</Descriptions.Item>
                      <Descriptions.Item label="聚合水位">{summary.slowMetrics.siteLocalDayEnergy.aggregateWatermark ?? '未提供'}</Descriptions.Item>
                    </Descriptions>
                  ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="SiteDashboardSummary 尚未加载" />}
                </BigScreenPanel>
                <BigScreenPanel eyebrow="PERFORMANCE" title="系统效率与基线">
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="COP 与节能基线待接入" />
                </BigScreenPanel>
                <BigScreenPanel eyebrow="COST" title="峰平谷费用结构">
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="分时成本模型待接入" />
                </BigScreenPanel>
              </div>

              <div className="bigscreen-main">
                <BigScreenPanel eyebrow="CENTRAL PLANT" title="冷源系统运行总览" accent>
                  <div className="bigscreen-system-meta">
                    <span>Site <strong>{site.displayName}</strong></span>
                    <span>数据边界 <strong className="is-accent">Registry · S2 · Energy</strong></span>
                  </div>
                  <div className="bigscreen-system-canvas">
                    <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="3D 系统拓扑和大屏专用聚合尚未接入；不会加载 Demo 场景数据" />
                  </div>
                  <div className="bigscreen-device-rail">
                    {[
                      ['已登记', summary?.devicePopulation.registered ?? '—', 'Registry'],
                      ['在线', summary?.devicePopulation.online ?? '—', 'Telemetry'],
                      ['离线', summary?.devicePopulation.offline ?? '—', 'Telemetry'],
                      ['陈旧', summary?.devicePopulation.stale ?? '—', 'Telemetry'],
                      ['未知/不可用', summary ? summary.devicePopulation.unknown + summary.devicePopulation.unavailable : '—', '不进入可用率分母'],
                    ].map(([label, value, detail]) => (
                      <div className="bigscreen-device-item" key={label}>
                        <span className="bigscreen-device-head"><strong>{label}</strong><i /></span>
                        <span className="bigscreen-device-data"><strong>{value}</strong><span>{detail}</span></span>
                      </div>
                    ))}
                  </div>
                </BigScreenPanel>
              </div>

              <div className="bigscreen-column bigscreen-column-right">
                <BigScreenPanel eyebrow="ASSET HEALTH" title="设备健康">
                  <div className="bigscreen-health-summary"><div className="bigscreen-health-score"><strong>{availabilityValue}</strong><span>{summary?.devicePopulation.state ?? 'UNAVAILABLE'} · 分母 {summary?.devicePopulation.denominator ?? '未发布'}</span></div></div>
                </BigScreenPanel>
                <BigScreenPanel eyebrow="FDD" title="故障诊断">
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="FDD Read Model 待接入" />
                </BigScreenPanel>
                <BigScreenPanel eyebrow="ALARM" title="活动告警">
                  {summary ? (
                    <Descriptions column={1} size="small">
                      <Descriptions.Item label="活动告警">{alarmValue}</Descriptions.Item>
                      <Descriptions.Item label="最高级别">{summary.fastMetrics.openAlarms.highestSeverity ?? '无'}</Descriptions.Item>
                      <Descriptions.Item label="状态">{summary.fastMetrics.openAlarms.state}</Descriptions.Item>
                      <Descriptions.Item label="水位">{summary.fastMetrics.openAlarms.watermark ?? '未提供'}</Descriptions.Item>
                    </Descriptions>
                  ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Alarm 摘要尚未加载" />}
                </BigScreenPanel>
                <BigScreenPanel eyebrow="OPTIMIZATION" title="优化机会">
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Optimization Read Model 待接入" />
                </BigScreenPanel>
              </div>
            </section>

            <footer className="bigscreen-footer">
              <span className="bigscreen-footer-item"><AlertOutlined /><strong>所有空值均表示真实能力未接入，不代表 0</strong></span>
              <span className="bigscreen-footer-item"><ApartmentOutlined />{site.timezone}</span>
              <Button className="bigscreen-exit-button" href={siteRoute(site, 'dashboard')}>退出大屏</Button>
            </footer>
          </div>
        </main>
      </ProductBoundary>
    </ConfigProvider>
  );
}
