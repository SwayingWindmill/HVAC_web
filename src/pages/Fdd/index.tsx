import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Grid,
  Input,
  Progress,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ApartmentOutlined,
  BugOutlined,
  CheckCircleOutlined,
  EyeOutlined,
  FileDoneOutlined,
  NodeIndexOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsActionFooter,
  OperationsDetailHeader,
  OperationsDetailSection,
  OperationsMetrics,
  OperationsPanelHeading,
  OperationsSummaryStrip,
  OperationsTimeline,
  useOperationsDetailFocus,
} from '@/components/OperationsUI';
import { useOps, fddList } from '@/store/ops';
import { ROLE_LABEL, useUi } from '@/store/ui';
import { SEVERITY_LABEL, SEVERITY_TONE, type Severity } from '@/theme/tokens';
import { can, canViewPath, readonlyHint } from '@/auth/permissions';
import { ReadonlyNotice } from '@/components/PageState';
import { TICKET_STATUS_META } from '@/domain/opsMeta';
import type { FddEntry, WorkOrder } from '@/mock/data';

type SeverityFilter = Severity | 'all';
type WorkOrderFilter = 'all' | 'generated' | 'notGenerated';

const SEVERITY_OPTIONS: { label: string; value: SeverityFilter }[] = [
  { label: '全部级别', value: 'all' },
  { label: SEVERITY_LABEL.critical, value: 'critical' },
  { label: SEVERITY_LABEL.major, value: 'major' },
  { label: SEVERITY_LABEL.minor, value: 'minor' },
  { label: SEVERITY_LABEL.info, value: 'info' },
];

const WORK_ORDER_OPTIONS: { label: string; value: WorkOrderFilter }[] = [
  { label: '全部状态', value: 'all' },
  { label: '未生成工单', value: 'notGenerated' },
  { label: '已生成工单', value: 'generated' },
];

const severityWeight: Record<Severity, number> = {
  critical: 4,
  major: 3,
  minor: 2,
  info: 1,
};

const isHighRisk = (entry: FddEntry) => entry.severity === 'critical' || entry.severity === 'major';

function buildTimeline(entry: FddEntry, workOrder?: WorkOrder) {
  return [
    { color: 'blue', children: `诊断触发：${entry.ts}` },
    { color: 'gold', children: `根因假设：${entry.rootCause}` },
    { color: workOrder ? 'green' : 'gray', children: workOrder ? `已生成工单 ${workOrder.id}` : '等待人工确认后生成工单' },
    {
      color: workOrder?.status === 'done' ? 'green' : workOrder ? 'blue' : 'gray',
      children: workOrder ? `工单状态：${TICKET_STATUS_META[workOrder.status].label}` : '未进入工单闭环',
    },
  ];
}

export default function Fdd() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const { role } = useUi();
  const { generateWorkOrder, generatedFddIds, workOrders } = useOps();

  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>('all');
  const [workOrderFilter, setWorkOrderFilter] = useState<WorkOrderFilter>('all');
  const [keyword, setKeyword] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const detailFocus = useOperationsDetailFocus();

  const canGenerate = can(role, 'create', 'workOrder');
  const selected = selectedId ? fddList.find((f) => f.id === selectedId) ?? null : null;
  const selectedWorkOrder = selected
    ? workOrders.find((order) => order.sourceFddId === selected.id) ?? null
    : null;
  const diagnosisParam = searchParams.get('diagnosis');

  useEffect(() => {
    if (!diagnosisParam) return;
    if (!fddList.some((entry) => entry.id === diagnosisParam)) {
      const next = new URLSearchParams(searchParams);
      next.delete('diagnosis');
      setSearchParams(next, { replace: true });
      message.warning(`未找到诊断 ${diagnosisParam}`);
      return;
    }
    if (selectedId !== diagnosisParam) setSelectedId(diagnosisParam);
  }, [diagnosisParam, searchParams, selectedId, setSearchParams]);

  const openDiagnosis = (id: string, trigger?: HTMLElement) => {
    if (trigger) detailFocus.captureTrigger(trigger, id);
    const next = new URLSearchParams(searchParams);
    next.set('diagnosis', id);
    setSearchParams(next, { replace: true });
    setSelectedId(id);
  };

  const closeDiagnosis = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('diagnosis');
    setSearchParams(next, { replace: true });
    setSelectedId(null);
    detailFocus.restoreFocus();
  };

  const summary = useMemo(() => {
    const highRisk = fddList.filter(isHighRisk).length;
    const generated = fddList.filter((entry) => (
      generatedFddIds.includes(entry.id) || workOrders.some((order) => order.sourceFddId === entry.id)
    )).length;
    const avgConfidence = Math.round((fddList.reduce((sum, f) => sum + f.confidence, 0) / Math.max(fddList.length, 1)) * 100);
    return { total: fddList.length, highRisk, generated, avgConfidence };
  }, [generatedFddIds, workOrders]);

  const rows = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return fddList
      .filter((f) => {
        const generated = generatedFddIds.includes(f.id);
        if (severityFilter !== 'all' && f.severity !== severityFilter) return false;
        if (workOrderFilter === 'generated' && !generated) return false;
        if (workOrderFilter === 'notGenerated' && generated) return false;
        if (!q) return true;
        return [f.id, f.device, f.phenomenon, f.rootCause, f.scope, f.recommended, f.linkedSuggestionId]
          .filter(Boolean)
          .some((value) => value!.toLowerCase().includes(q));
      })
      .sort((a, b) => severityWeight[b.severity] - severityWeight[a.severity]);
  }, [generatedFddIds, keyword, severityFilter, workOrderFilter]);

  const generateAndOpenAlarms = (entry: FddEntry) => {
    if (!canGenerate) return;
    const workOrderId = generateWorkOrder(entry);
    message.success(`已生成工单 ${workOrderId}`);
    navigate(`/alarms?workOrder=${encodeURIComponent(workOrderId)}`);
  };

  const columns: ColumnsType<FddEntry> = [
    {
      title: '诊断',
      dataIndex: 'id',
      key: 'id',
      fixed: 'left',
      width: 160,
      render: (id: string, entry) => (
        <Space direction="vertical" size={0}>
          <Space size={6}>
            <Typography.Text strong copyable={{ text: id }}>{id}</Typography.Text>
            <Tag color={SEVERITY_TONE[entry.severity]}>{SEVERITY_LABEL[entry.severity]}</Tag>
          </Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{entry.ts}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '设备 / 范围',
      dataIndex: 'device',
      key: 'device',
      width: 220,
      render: (device: string, entry) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{device}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{entry.scope}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '故障现象',
      dataIndex: 'phenomenon',
      key: 'phenomenon',
      width: 260,
      render: (phenomenon: string, entry) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{phenomenon}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{entry.impact}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '根因假设',
      dataIndex: 'rootCause',
      key: 'rootCause',
      width: 220,
    },
    {
      title: '置信度',
      dataIndex: 'confidence',
      key: 'confidence',
      width: 130,
      sorter: (a, b) => a.confidence - b.confidence,
      render: (confidence: number) => <Progress percent={Math.round(confidence * 100)} size="small" />,
    },
    {
      title: '证据',
      dataIndex: 'evidence',
      key: 'evidence',
      width: 120,
      render: (evidence: FddEntry['evidence']) => <Tag color="processing">{evidence.length} 项指标</Tag>,
    },
    {
      title: '工单状态',
      key: 'workOrder',
      width: 130,
      render: (_, entry) => {
        const workOrder = workOrders.find((order) => order.sourceFddId === entry.id);
        return workOrder
          ? <Tag icon={<CheckCircleOutlined />} color={TICKET_STATUS_META[workOrder.status].color}>{TICKET_STATUS_META[workOrder.status].label}</Tag>
          : <Tag color="default">未生成</Tag>;
      },
    },
    {
      title: '操作',
      key: 'action',
      width: 210,
      fixed: 'right',
      render: (_, entry) => {
        const workOrder = workOrders.find((order) => order.sourceFddId === entry.id);
        const workOrderButton = workOrder ? (
          <Button size="small" type="primary" onClick={() => navigate(`/alarms?workOrder=${encodeURIComponent(workOrder.id)}`)}>
            查看工单
          </Button>
        ) : (
          <Button size="small" type="primary" disabled={!canGenerate} onClick={() => generateAndOpenAlarms(entry)}>
            生成工单
          </Button>
        );
        return (
          <Space size={6}>
            <Button
              size="small"
              icon={<EyeOutlined />}
              data-ops-detail-trigger={entry.id}
              onClick={(event) => openDiagnosis(entry.id, event.currentTarget)}
            >
              详情
            </Button>
            {canGenerate || workOrder ? workOrderButton : <Tooltip title={readonlyHint(role, 'workOrder')}>{workOrderButton}</Tooltip>}
          </Space>
        );
      },
    },
  ];

  const tableColumns = compactTable
    ? columns.filter((column) => ['id', 'device', 'confidence', 'workOrder', 'action'].includes(String(column.key)))
    : columns;

  return (
    <PageScaffold
      title="故障检测与诊断 FDD"
      extra={<Tag color={canGenerate ? 'processing' : 'default'}>{ROLE_LABEL[role]} · {canGenerate ? '可生成工单' : '只读'}</Tag>}
    >
      <Alert
        type="info"
        showIcon
        icon={<BugOutlined />}
        message="FDD 只负责发现与诊断，不直接闭环；确认后生成工单，由 /alarms 承接派工、处理和完成确认。"
      />

      <OperationsMetrics
        items={[
          { label: '活跃诊断', value: summary.total, detail: '按严重度优先展示', icon: <BugOutlined />, tone: 'accent' },
          { label: '高风险', value: summary.highRisk, detail: summary.highRisk ? '需要人工确认根因与影响' : '当前无高风险诊断', icon: <SafetyCertificateOutlined />, tone: summary.highRisk ? 'critical' : 'positive' },
          { label: '已生成工单', value: summary.generated, detail: `${summary.total - summary.generated} 条尚未进入闭环`, icon: <FileDoneOutlined />, tone: summary.generated ? 'positive' : 'default' },
          { label: '平均置信度', value: summary.avgConfidence, suffix: '%', detail: '模型结论仍需人工复核', icon: <ThunderboltOutlined /> },
        ]}
      />

      <Card variant="borderless" styles={{ body: { padding: 16 } }}>
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <div className="ops-toolbar">
            <OperationsPanelHeading icon={<NodeIndexOutlined />} title="诊断列表" meta={`${rows.length} 条`} />
            <Space wrap>
              <Input.Search
                allowClear
                placeholder="搜索设备、现象、根因、建议"
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
                style={{ width: 280 }}
              />
              <Select value={severityFilter} onChange={setSeverityFilter} options={SEVERITY_OPTIONS} style={{ width: 130 }} />
              <Select value={workOrderFilter} onChange={setWorkOrderFilter} options={WORK_ORDER_OPTIONS} style={{ width: 130 }} />
            </Space>
          </div>

          <Table<FddEntry>
            rowKey="id"
            size="middle"
            columns={tableColumns}
            dataSource={rows}
            pagination={{ pageSize: 8, showSizeChanger: false }}
            scroll={{ x: compactTable ? 860 : 1420 }}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的诊断" /> }}
          />
        </Space>
      </Card>

      <Drawer
        rootClassName="ops-detail-drawer"
        title={selected ? (
          <OperationsDetailHeader
            eyebrow={`FDD 诊断 · ${selected.ts}`}
            title={selected.device}
            subtitle={`${selected.phenomenon} · ${selected.scope}`}
            status={<Tag color={SEVERITY_TONE[selected.severity]}>{SEVERITY_LABEL[selected.severity]}</Tag>}
            meta={<Typography.Text code>{selected.id}</Typography.Text>}
          />
        ) : '诊断详情'}
        width={720}
        open={Boolean(selected)}
        onClose={closeDiagnosis}
        afterOpenChange={(open) => {
          if (!open) detailFocus.restoreFocus();
        }}
        footer={selected ? (
          <OperationsActionFooter
            note={selectedWorkOrder
              ? `已进入工单 ${selectedWorkOrder.id}，当前状态：${TICKET_STATUS_META[selectedWorkOrder.status].label}。`
              : canGenerate ? '生成工单后将进入报警工单闭环。' : '当前角色仅可查看诊断证据，不能生成工单。'}
          >
            <Button onClick={closeDiagnosis}>关闭</Button>
            {selected.linkedAssetId && canViewPath(role, '/assets') ? (
              <Button
                icon={<ApartmentOutlined />}
                onClick={() => navigate(`/assets?device=${encodeURIComponent(selected.linkedAssetId!)}`)}
              >
                查看资产
              </Button>
            ) : null}
            {selected.linkedSuggestionId && canViewPath(role, '/optimize') ? (
              <Button
                icon={<ThunderboltOutlined />}
                onClick={() => navigate(`/optimize?suggestion=${encodeURIComponent(selected.linkedSuggestionId!)}`)}
              >
                查看优化建议
              </Button>
            ) : null}
            {selectedWorkOrder ? (
              <Button
                type="primary"
                onClick={() => navigate(`/alarms?workOrder=${encodeURIComponent(selectedWorkOrder.id)}`)}
              >
                查看工单
              </Button>
            ) : (
              <Button type="primary" disabled={!canGenerate} onClick={() => generateAndOpenAlarms(selected)}>
                生成工单
              </Button>
            )}
          </OperationsActionFooter>
        ) : null}
      >
        {selected ? (
          <div className="ops-detail-stack">
            <OperationsSummaryStrip
              ariaLabel="诊断关键摘要"
              items={[
                { label: '严重级', value: SEVERITY_LABEL[selected.severity], tone: isHighRisk(selected) ? 'critical' : 'warning' },
                { label: '诊断置信度', value: Math.round(selected.confidence * 100), suffix: '%', tone: 'accent' },
                { label: '证据指标', value: selected.evidence.length, suffix: '项' },
                {
                  label: '工单状态',
                  value: selectedWorkOrder ? TICKET_STATUS_META[selectedWorkOrder.status].label : '待确认',
                  tone: selectedWorkOrder?.status === 'done' ? 'positive' : selectedWorkOrder ? 'accent' : 'warning',
                },
              ]}
            />

            <OperationsDetailSection
              title="诊断结论"
              icon={<BugOutlined />}
              description="现象、根因假设与关联业务对象。"
            >
              <Descriptions size="small" column={{ xs: 1, sm: 2 }} colon={false}>
                <Descriptions.Item label="设备">{selected.device}</Descriptions.Item>
                <Descriptions.Item label="范围">{selected.scope}</Descriptions.Item>
                <Descriptions.Item label="关联资产"><Typography.Text code>{selected.linkedAssetId ?? '未绑定'}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="关联优化"><Typography.Text code>{selected.linkedSuggestionId ?? '无'}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="故障现象" span={2}>{selected.phenomenon}</Descriptions.Item>
                <Descriptions.Item label="根因假设" span={2}>{selected.rootCause}</Descriptions.Item>
              </Descriptions>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="影响评估"
              icon={<SafetyCertificateOutlined />}
              description="对运行、能耗和服务水平的潜在影响。"
            >
              <div className={`ops-detail-callout ${isHighRisk(selected) ? 'is-critical' : 'is-warning'}`}>
                {selected.impact}
              </div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="证据指标"
              icon={<NodeIndexOutlined />}
              description="支持当前根因假设的观测数据。"
              extra={`${selected.evidence.length} 项`}
            >
              <div className="ops-detail-list">
                {selected.evidence.map((item) => (
                  <div className="ops-detail-list-row" key={item.name}>
                    <span className="ops-detail-list-label">{item.name}</span>
                    <span className="ops-detail-list-value">{item.value}</span>
                  </div>
                ))}
              </div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="推荐处置"
              icon={<ThunderboltOutlined />}
              description="生成工单前应由运维人员复核现场条件。"
            >
              <div className="ops-detail-callout">{selected.recommended}</div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="诊断到工单链路"
              icon={<FileDoneOutlined />}
              description="记录从诊断触发到处置闭环的当前位置。"
            >
              <OperationsTimeline items={buildTimeline(selected, selectedWorkOrder ?? undefined)} />
            </OperationsDetailSection>

            {!canGenerate ? (
              <ReadonlyNotice
                role={role}
                description="切换到安装/运维或内部研发角色后，可将诊断结论生成工单。"
              />
            ) : null}
          </div>
        ) : null}
      </Drawer>
    </PageScaffold>
  );
}
