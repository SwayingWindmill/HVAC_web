import { useEffect, useMemo, useState } from 'react';
import {
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Grid,
  Input,
  Segmented,
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
  ClockCircleOutlined,
  EyeOutlined,
  FieldTimeOutlined,
  FilterOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
  ToolOutlined,
  UserSwitchOutlined,
} from '@ant-design/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsActionFooter,
  OperationsDetailHeader,
  OperationsDetailSection,
  OperationsMetrics,
  OperationsSummaryStrip,
  OperationsTimeline,
  useOperationsDetailFocus,
} from '@/components/OperationsUI';
import { useOps } from '@/store/ops';
import { ROLE_LABEL, useUi } from '@/store/ui';
import { SEVERITY_LABEL, SEVERITY_TONE, type Severity } from '@/theme/tokens';
import { can, canViewPath, readonlyHint } from '@/auth/permissions';
import { ReadonlyNotice } from '@/components/PageState';
import { TICKET_STATUS_META, isWorkOrderActive, isWorkOrderSlaRisk } from '@/domain/opsMeta';
import type { TicketStatus, WorkOrder } from '@/mock/data';

type QuickFilter = 'all' | 'active' | 'risk' | 'done';
type SeverityFilter = Severity | 'all';
type StatusFilter = TicketStatus | 'all';
type SourceFilter = WorkOrder['source'] | 'all';

const QUICK_FILTERS: { label: string; value: QuickFilter }[] = [
  { label: '全部', value: 'all' },
  { label: '待处理', value: 'active' },
  { label: 'SLA 风险', value: 'risk' },
  { label: '已完成', value: 'done' },
];

const SEVERITY_OPTIONS: { label: string; value: SeverityFilter }[] = [
  { label: '全部级别', value: 'all' },
  { label: SEVERITY_LABEL.critical, value: 'critical' },
  { label: SEVERITY_LABEL.major, value: 'major' },
  { label: SEVERITY_LABEL.minor, value: 'minor' },
  { label: SEVERITY_LABEL.info, value: 'info' },
];

const STATUS_OPTIONS: { label: string; value: StatusFilter }[] = [
  { label: '全部状态', value: 'all' },
  { label: TICKET_STATUS_META.open.label, value: 'open' },
  { label: TICKET_STATUS_META.assigned.label, value: 'assigned' },
  { label: TICKET_STATUS_META.doing.label, value: 'doing' },
  { label: TICKET_STATUS_META.done.label, value: 'done' },
];

const SOURCE_OPTIONS: { label: string; value: SourceFilter }[] = [
  { label: '全部来源', value: 'all' },
  { label: '告警', value: 'alarm' },
  { label: 'FDD', value: 'fdd' },
];

const isSlaRisk = isWorkOrderSlaRisk;

const priorityLabel = (severity: Severity) => {
  if (severity === 'critical') return 'P1';
  if (severity === 'major') return 'P2';
  if (severity === 'minor') return 'P3';
  return 'P4';
};

const sourceTag = (source: WorkOrder['source']) => (
  <Tag color={source === 'fdd' ? 'geekblue' : 'default'}>{source === 'fdd' ? 'FDD' : '告警'}</Tag>
);

const reached = (current: TicketStatus, target: TicketStatus) => TICKET_STATUS_META[current].step >= TICKET_STATUS_META[target].step;

function buildTimeline(order: WorkOrder) {
  return [
    { color: 'blue', children: `创建工单：${order.createdAt}` },
    { color: reached(order.status, 'assigned') ? 'gold' : 'gray', children: reached(order.status, 'assigned') ? `已派工：${order.assignee ?? '运维值班组'}` : '等待接手 / 派工' },
    { color: reached(order.status, 'doing') ? 'blue' : 'gray', children: reached(order.status, 'doing') ? '现场处理中' : '等待开始处理' },
    { color: reached(order.status, 'done') ? 'green' : 'gray', children: reached(order.status, 'done') ? '已完成闭环' : '等待闭环确认' },
  ];
}

export default function Alarms() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const workOrders = useOps((st) => st.workOrders);
  const setTicketStatus = useOps((st) => st.setTicketStatus);
  const role = useUi((st) => st.role);

  const [quickFilter, setQuickFilter] = useState<QuickFilter>('active');
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>('all');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [sourceFilter, setSourceFilter] = useState<SourceFilter>('all');
  const [keyword, setKeyword] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const detailFocus = useOperationsDetailFocus();

  const canOperate = can(role, 'transition', 'workOrder');
  const selected = selectedId ? workOrders.find((w) => w.id === selectedId) ?? null : null;
  const workOrderParam = searchParams.get('workOrder');

  useEffect(() => {
    if (!workOrderParam) return;
    const target = workOrders.find((order) => order.id === workOrderParam);
    if (!target) {
      const next = new URLSearchParams(searchParams);
      next.delete('workOrder');
      setSearchParams(next, { replace: true });
      message.warning(`未找到工单 ${workOrderParam}`);
      return;
    }
    if (selectedId !== workOrderParam) {
      setQuickFilter('all');
      setSeverityFilter('all');
      setStatusFilter('all');
      setSourceFilter('all');
      setKeyword('');
      setSelectedId(workOrderParam);
    }
  }, [searchParams, selectedId, setSearchParams, workOrderParam, workOrders]);

  const openWorkOrder = (id: string, trigger?: HTMLElement) => {
    if (trigger) detailFocus.captureTrigger(trigger, id);
    const next = new URLSearchParams(searchParams);
    next.set('workOrder', id);
    setSearchParams(next, { replace: true });
    setSelectedId(id);
  };

  const closeWorkOrder = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('workOrder');
    setSearchParams(next, { replace: true });
    setSelectedId(null);
    detailFocus.restoreFocus();
  };

  const summary = useMemo(() => {
    const active = workOrders.filter(isWorkOrderActive).length;
    const done = workOrders.filter((w) => w.status === 'done').length;
    const risk = workOrders.filter(isSlaRisk).length;
    const fdd = workOrders.filter((w) => w.source === 'fdd').length;
    return { total: workOrders.length, active, done, risk, fdd };
  }, [workOrders]);

  const rows = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return workOrders.filter((w) => {
      if (quickFilter === 'active' && w.status === 'done') return false;
      if (quickFilter === 'done' && w.status !== 'done') return false;
      if (quickFilter === 'risk' && !isSlaRisk(w)) return false;
      if (severityFilter !== 'all' && w.severity !== severityFilter) return false;
      if (statusFilter !== 'all' && w.status !== statusFilter) return false;
      if (sourceFilter !== 'all' && w.source !== sourceFilter) return false;
      if (!q) return true;
      return [w.id, w.device, w.title, w.description, w.assignee, w.location]
        .filter(Boolean)
        .some((value) => value!.toLowerCase().includes(q));
    });
  }, [keyword, quickFilter, severityFilter, sourceFilter, statusFilter, workOrders]);

  const advanceStatus = (order: WorkOrder) => {
    const next = TICKET_STATUS_META[order.status].next;
    if (!next || !canOperate) return;
    setTicketStatus(order.id, next);
    if (next === 'done') {
      setQuickFilter('all');
      setStatusFilter('all');
    }
    message.success(`${order.id} 已更新为${TICKET_STATUS_META[next].label}`);
  };

  const columns: ColumnsType<WorkOrder> = [
    {
      title: '工单',
      dataIndex: 'id',
      key: 'id',
      width: 120,
      fixed: 'left',
      render: (id: string, order) => (
        <Space direction="vertical" size={0}>
          <Typography.Text copyable={{ text: id }} strong>{id}</Typography.Text>
          <Space size={4}>
            {sourceTag(order.source)}
            <Tag color={isSlaRisk(order) ? 'red' : 'default'}>{priorityLabel(order.severity)}</Tag>
          </Space>
        </Space>
      ),
    },
    {
      title: '设备 / 位置',
      dataIndex: 'device',
      key: 'device',
      width: 200,
      render: (device: string, order) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{device}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{order.location ?? '未绑定位置'}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '严重级',
      dataIndex: 'severity',
      key: 'severity',
      width: 92,
      filters: [
        { text: SEVERITY_LABEL.critical, value: 'critical' },
        { text: SEVERITY_LABEL.major, value: 'major' },
        { text: SEVERITY_LABEL.minor, value: 'minor' },
        { text: SEVERITY_LABEL.info, value: 'info' },
      ],
      onFilter: (value, order) => order.severity === value,
      render: (severity: Severity) => <Tag color={SEVERITY_TONE[severity]}>{SEVERITY_LABEL[severity]}</Tag>,
    },
    {
      title: '标题 / 影响',
      key: 'title',
      ellipsis: true,
      render: (_, order) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{order.title}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{order.description}</Typography.Text>
          {order.impact && <Typography.Text type="secondary" style={{ fontSize: 12 }}>影响：{order.impact}</Typography.Text>}
        </Space>
      ),
    },
    {
      title: '负责人',
      dataIndex: 'assignee',
      key: 'assignee',
      width: 110,
      render: (assignee?: string) => assignee ? <Tag icon={<UserSwitchOutlined />} color="processing">{assignee}</Tag> : <Tag>待分配</Tag>,
    },
    {
      title: 'SLA',
      dataIndex: 'dueAt',
      key: 'dueAt',
      width: 120,
      render: (dueAt: string | undefined, order) => (
        <Typography.Text type={isSlaRisk(order) ? 'danger' : 'secondary'} style={{ fontSize: 12 }}>
          {dueAt ?? '未设置'}
        </Typography.Text>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: TicketStatus) => <Tag color={TICKET_STATUS_META[status].color}>{TICKET_STATUS_META[status].label}</Tag>,
    },
    {
      title: '操作',
      key: 'action',
      width: 170,
      fixed: 'right',
      render: (_, order) => {
        const meta = TICKET_STATUS_META[order.status];
        const action = meta.next ? (
          <Button size="small" type="primary" disabled={!canOperate} onClick={() => advanceStatus(order)}>
            {meta.nextLabel}
          </Button>
        ) : (
          <Button size="small" disabled icon={<CheckCircleOutlined />}>已闭环</Button>
        );
        return (
          <Space size={6}>
            <Button
              size="small"
              icon={<EyeOutlined />}
              data-ops-detail-trigger={order.id}
              onClick={(event) => openWorkOrder(order.id, event.currentTarget)}
            >
              详情
            </Button>
            {canOperate ? action : <Tooltip title={readonlyHint(role, 'workOrder')}>{action}</Tooltip>}
          </Space>
        );
      },
    },
  ];

  const tableColumns = compactTable
    ? columns.filter((column) => ['id', 'device', 'severity', 'status', 'action'].includes(String(column.key)))
    : columns;

  return (
    <PageScaffold
      title="报警工单"
      subtitle="告警与 FDD 诊断统一进入工单闭环：接手、派工、处理、完成，确保人始终在回路中。"
      eyebrow="事件与闭环"
      extra={<Tag color={canOperate ? 'processing' : 'default'}>{ROLE_LABEL[role]} · {canOperate ? '可处置' : '只读'}</Tag>}
    >
      <OperationsMetrics
        items={[
          { label: '工单总数', value: summary.total, detail: `${summary.done} 张已完成闭环`, icon: <ToolOutlined /> },
          { label: '待处理', value: summary.active, detail: summary.active ? '按严重度与 SLA 排序' : '当前无待处理工单', icon: <ClockCircleOutlined />, tone: summary.active ? 'accent' : 'positive' },
          { label: 'SLA 风险', value: summary.risk, detail: summary.risk ? '需要优先接手或推进' : '当前 SLA 风险可控', icon: <FieldTimeOutlined />, tone: summary.risk ? 'critical' : 'positive' },
          { label: 'FDD 转工单', value: summary.fdd, detail: `${summary.total - summary.fdd} 张来自直接告警`, icon: <SafetyCertificateOutlined /> },
        ]}
      />

      <Card variant="borderless" styles={{ body: { padding: 16 } }}>
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <div className="ops-toolbar">
            <Segmented<QuickFilter> value={quickFilter} onChange={(value) => setQuickFilter(value as QuickFilter)} options={QUICK_FILTERS} />
            <Space size={8} wrap>
              <Input.Search
                allowClear
                placeholder="搜索工单、设备、位置、负责人"
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
                style={{ width: 260 }}
              />
              <Select value={sourceFilter} onChange={setSourceFilter} options={SOURCE_OPTIONS} style={{ width: 120 }} />
              <Select value={severityFilter} onChange={setSeverityFilter} options={SEVERITY_OPTIONS} style={{ width: 120 }} />
              <Select value={statusFilter} onChange={setStatusFilter} options={STATUS_OPTIONS} style={{ width: 120 }} />
              <Tooltip title="当前为前端 Mock 筛选，后续接入 API 后迁移到服务端查询参数。">
                <FilterOutlined style={{ opacity: 0.55 }} />
              </Tooltip>
            </Space>
          </div>

          {!canOperate && (
            <ReadonlyNotice
              role={role}
              description="可以查看报警、FDD 来源、触发规则和处置建议，但不能接手、派工或关闭工单。"
            />
          )}

          <Table<WorkOrder>
            rowKey="id"
            size="middle"
            columns={tableColumns}
            dataSource={rows}
            pagination={{ pageSize: 8, showSizeChanger: false }}
            scroll={{ x: compactTable ? 782 : 1080 }}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的工单" /> }}
          />
        </Space>
      </Card>

      <Drawer
        rootClassName="ops-detail-drawer"
        title={selected ? (
          <OperationsDetailHeader
            eyebrow={`报警工单 · ${selected.createdAt}`}
            title={selected.title}
            subtitle={`${selected.device} · ${selected.location ?? '未绑定位置'}`}
            status={<Tag color={TICKET_STATUS_META[selected.status].color}>{TICKET_STATUS_META[selected.status].label}</Tag>}
            meta={<Typography.Text code>{selected.id}</Typography.Text>}
          />
        ) : '工单详情'}
        width={720}
        open={Boolean(selected)}
        onClose={closeWorkOrder}
        afterOpenChange={(open) => {
          if (!open) detailFocus.restoreFocus();
        }}
        footer={selected ? (
          <OperationsActionFooter
            note={canOperate ? '状态推进会同步更新列表、详情和来源诊断。' : '当前角色仅可查看工单，不能改变处理状态。'}
          >
            <Button onClick={closeWorkOrder}>关闭</Button>
            {selected.sourceFddId && canViewPath(role, '/fdd') ? (
              <Button
                icon={<BugOutlined />}
                onClick={() => navigate(`/fdd?diagnosis=${encodeURIComponent(selected.sourceFddId!)}`)}
              >
                查看诊断
              </Button>
            ) : null}
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
            {TICKET_STATUS_META[selected.status].next ? (
              <Button type="primary" disabled={!canOperate} onClick={() => advanceStatus(selected)}>
                {TICKET_STATUS_META[selected.status].nextLabel}
              </Button>
            ) : null}
          </OperationsActionFooter>
        ) : null}
      >
        {selected ? (
          <div className="ops-detail-stack">
            <OperationsSummaryStrip
              ariaLabel="工单关键摘要"
              items={[
                { label: '优先级', value: priorityLabel(selected.severity), tone: isSlaRisk(selected) ? 'critical' : 'warning' },
                { label: '当前状态', value: TICKET_STATUS_META[selected.status].label, tone: selected.status === 'done' ? 'positive' : 'accent' },
                { label: '负责人', value: selected.assignee ?? '待分配', tone: selected.assignee ? 'default' : 'warning' },
                { label: 'SLA', value: selected.dueAt ?? '未设置', tone: isSlaRisk(selected) ? 'critical' : 'default' },
              ]}
            />

            <OperationsDetailSection
              title="工单上下文"
              icon={<ToolOutlined />}
              description="来源、设备、位置与触发规则。"
            >
              <Descriptions size="small" column={{ xs: 1, sm: 2 }} colon={false}>
                <Descriptions.Item label="来源">{sourceTag(selected.source)}</Descriptions.Item>
                <Descriptions.Item label="严重级"><Tag color={SEVERITY_TONE[selected.severity]}>{SEVERITY_LABEL[selected.severity]}</Tag></Descriptions.Item>
                {selected.sourceFddId ? (
                  <Descriptions.Item label="来源诊断"><Typography.Text code>{selected.sourceFddId}</Typography.Text></Descriptions.Item>
                ) : null}
                {selected.linkedAssetId ? (
                  <Descriptions.Item label="关联资产"><Typography.Text code>{selected.linkedAssetId}</Typography.Text></Descriptions.Item>
                ) : null}
                <Descriptions.Item label="设备">{selected.device}</Descriptions.Item>
                <Descriptions.Item label="负责人">{selected.assignee ?? '待分配'}</Descriptions.Item>
                <Descriptions.Item label="位置" span={2}>{selected.location ?? '未绑定'}</Descriptions.Item>
                <Descriptions.Item label="触发规则" span={2}>{selected.rule ?? '未配置规则说明'}</Descriptions.Item>
                <Descriptions.Item label="创建时间">{selected.createdAt}</Descriptions.Item>
                <Descriptions.Item label="SLA 截止">{selected.dueAt ?? '未设置'}</Descriptions.Item>
              </Descriptions>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="影响评估"
              icon={<SafetyCertificateOutlined />}
              description="当前事件对设备运行和服务水平的影响。"
            >
              <div className={`ops-detail-callout ${isSlaRisk(selected) ? 'is-critical' : 'is-warning'}`}>
                {selected.impact ?? selected.description}
              </div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="建议处置"
              icon={<UserSwitchOutlined />}
              description="现场执行前应由负责人核对安全条件。"
            >
              <div className="ops-detail-callout">
                {selected.recommendation ?? '建议现场核查设备运行状态，并在处理后补充闭环说明。'}
              </div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="闭环进度"
              icon={<ClockCircleOutlined />}
              description="接手、处理和完成确认的当前进度。"
            >
              <OperationsTimeline items={buildTimeline(selected)} />
            </OperationsDetailSection>

            {!canOperate ? (
              <ReadonlyNotice
                role={role}
                description="当前角色可以查看工单证据和处置建议，但不能接手、推进或关闭工单。"
              />
            ) : null}
          </div>
        ) : null}
      </Drawer>
    </PageScaffold>
  );
}
