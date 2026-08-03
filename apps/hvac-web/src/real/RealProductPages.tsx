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
  CloudOutlined,
  DollarOutlined,
  ExperimentOutlined,
  FieldTimeOutlined,
  FundOutlined,
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
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { FocusHeading } from './FocusHeading';
import { siteRoute } from './site-routing';
import '@/styles/real-product-pages.css';

interface RealProductPageProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
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

const FDD_COLUMNS: ColumnsType<EmptyRow> = [
  { title: '诊断', key: 'diagnosis', width: 160 },
  { title: '设备 / 范围', key: 'device', width: 220 },
  { title: '故障现象', key: 'phenomenon', width: 260 },
  { title: '根因假设', key: 'cause', width: 220 },
  { title: '置信度', key: 'confidence', width: 130 },
  { title: '证据', key: 'evidence', width: 120 },
  { title: '工单状态', key: 'workOrder', width: 130 },
  { title: '操作', key: 'action', width: 150 },
];

const OPTIMIZE_COLUMNS: ColumnsType<EmptyRow> = [
  { title: '建议', key: 'suggestion', width: 270 },
  { title: '调整内容', key: 'diff', width: 210 },
  { title: '预计收益', key: 'saving', width: 180 },
  { title: '置信度', key: 'confidence', width: 130 },
  { title: '风险', key: 'risk', width: 100 },
  { title: '状态', key: 'status', width: 100 },
  { title: '审核人', key: 'reviewer', width: 120 },
  { title: '操作', key: 'action', width: 180 },
];

const COST_COLUMNS: ColumnsType<EmptyRow> = [
  { title: '建议', key: 'suggestion', width: 260 },
  { title: '预计节省', key: 'saving', width: 150 },
  { title: '批准时间', key: 'approvedAt', width: 180 },
  { title: '效果跟踪', key: 'tracking', width: 160 },
  { title: '状态', key: 'status', width: 120 },
];

export function RealFddPage({ site }: RealProductPageProps) {
  return (
    <ProductBoundary site={site} testId="real-site-route-fdd">
      <PageScaffold
        title="故障检测与诊断 FDD"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><BugOutlined />故障检测与诊断 FDD</Space></FocusHeading>}
        extra={<Tag>真实诊断服务待接入</Tag>}
      >
        <Alert
          type="info"
          showIcon
          icon={<BugOutlined />}
          message="FDD 只负责发现与诊断，不直接闭环；确认后生成工单，由报警工单承接派工、处理和完成确认。"
          description="当前站点尚未提供权威 FDD Read Model，因此保留 Demo 的指标、筛选、诊断列表和详情入口，不填充演示诊断。"
        />
        <OperationsMetrics items={[
          { label: '活跃诊断', value: '—', detail: '等待 FDD Read Model', icon: <BugOutlined />, tone: 'accent' },
          { label: '高风险', value: '—', detail: '不会从遥测自行推断故障', icon: <SafetyCertificateOutlined /> },
          { label: '已生成工单', value: '—', detail: '等待诊断与工单关联接口', icon: <NodeIndexOutlined /> },
          { label: '平均置信度', value: '—', suffix: '%', detail: '没有权威模型结果', icon: <ThunderboltOutlined /> },
        ]} />
        <Card variant="borderless" styles={{ body: { padding: 16 } }}>
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            <div className="ops-toolbar">
              <OperationsPanelHeading icon={<NodeIndexOutlined />} title="诊断列表" meta="0 条" />
              <Space wrap>
                <Input.Search disabled placeholder="搜索设备、现象、根因、建议" style={{ width: 280 }} />
                <Select disabled value="all" options={[{ label: '全部级别', value: 'all' }]} style={{ width: 130 }} />
                <Select disabled value="all" options={[{ label: '全部状态', value: 'all' }]} style={{ width: 130 }} />
              </Space>
            </div>
            <EmptyProductTable columns={FDD_COLUMNS} scroll={1450} description="FDD 权威诊断服务尚未接入；未使用 Demo 数据替代" />
          </Space>
        </Card>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function RealOptimizePage({ site }: RealProductPageProps) {
  return (
    <ProductBoundary site={site} testId="real-site-route-optimize">
      <PageScaffold
        title="节能优化建议"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><ThunderboltOutlined />节能优化建议</Space></FocusHeading>}
        extra={<Tag>真实优化服务待接入</Tag>}
      >
        <Alert
          type="info"
          showIcon
          icon={<ExperimentOutlined />}
          message="人在回路：建议先评估收益、舒适度影响和回滚条件；审批通过后仍需二次确认，绝不直接静默下发设备。"
          description="当前站点尚未提供权威 Optimization Read Model。页面指标、筛选、建议评估池和详情入口与 Demo 保持一致。"
        />
        <OperationsMetrics items={[
          { label: '预计节电', value: '—', suffix: 'kWh/天', detail: '等待优化建议数据', icon: <ThunderboltOutlined />, tone: 'accent' },
          { label: '预计节省', value: '—', detail: '没有权威收益估算', icon: <DollarOutlined /> },
          { label: '减排量', value: '—', suffix: 'kgCO₂/天', detail: '等待真实折算口径', icon: <CloudOutlined /> },
          { label: '待处理', value: '—', detail: '等待审批状态投影', icon: <FieldTimeOutlined /> },
        ]} />
        <Card variant="borderless" styles={{ body: { padding: 16 } }}>
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            <div className="ops-toolbar">
              <OperationsPanelHeading icon={<SafetyCertificateOutlined />} title="建议评估池" meta="0 条" />
              <Space wrap>
                <Input.Search disabled placeholder="搜索建议、设备、范围、审核人" style={{ width: 280 }} />
                <Select disabled value="all" options={[{ label: '全部类型', value: 'all' }]} style={{ width: 150 }} />
                <Select disabled value="all" options={[{ label: '全部状态', value: 'all' }]} style={{ width: 130 }} />
                <Select disabled value="all" options={[{ label: '全部风险', value: 'all' }]} style={{ width: 120 }} />
              </Space>
            </div>
            <EmptyProductTable columns={OPTIMIZE_COLUMNS} scroll={1360} description="Optimization 权威建议服务尚未接入；未使用 Demo 建议替代" />
          </Space>
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

export function RealBigScreenPage({ site }: RealProductPageProps) {
  const [scene, setScene] = useState<BigScreenScene>('overview');
  const [clock, setClock] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setClock(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const clockText = useMemo(() => clock.toLocaleTimeString('zh-CN', { hour12: false }), [clock]);

  return (
    <ConfigProvider theme={{ algorithm: antdTheme.darkAlgorithm }}>
      <ProductBoundary site={site} testId="real-site-route-bigscreen" state="READY">
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
                <span className="bigscreen-live-status"><i className="bigscreen-live-dot" />真实数据边界</span>
                <span className="bigscreen-meta-item is-optional">{site.timezone}</span>
                <strong className="bigscreen-clock">{clockText}</strong>
              </div>
            </header>

            <section className="bigscreen-kpi-band" aria-label="运行关键指标">
              {[
                ['实时功率', '—', '功率聚合待接入'],
                ['综合 COP', '—', 'COP 聚合待接入'],
                ['今日能耗', '—', 'Energy Analytics'],
                ['今日节能率', '—', '基线待接入'],
                ['设备在线', '—', 'Registry / Presence'],
                ['待处理工单', '—', 'Alarm lifecycle'],
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
                <BigScreenPanel eyebrow="ENERGY" title="能耗成本趋势">
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="大屏能源趋势投影待接入" />
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
                    {['冷水机组', '冷冻水泵', '冷却水泵', '冷却塔', '末端系统'].map((label) => (
                      <div className="bigscreen-device-item" key={label}>
                        <span className="bigscreen-device-head"><strong>{label}</strong><i /></span>
                        <span className="bigscreen-device-data"><strong>—</strong><span>等待真实聚合</span></span>
                      </div>
                    ))}
                  </div>
                </BigScreenPanel>
              </div>

              <div className="bigscreen-column bigscreen-column-right">
                <BigScreenPanel eyebrow="ASSET HEALTH" title="设备健康">
                  <div className="bigscreen-health-summary"><div className="bigscreen-health-score"><strong>—</strong><span>权威状态待汇总</span></div></div>
                </BigScreenPanel>
                <BigScreenPanel eyebrow="FDD" title="故障诊断">
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="FDD Read Model 待接入" />
                </BigScreenPanel>
                <BigScreenPanel eyebrow="ALARM" title="报警与工单">
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Alarm 大屏投影待接入" />
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
