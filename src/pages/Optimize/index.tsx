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
  Popconfirm,
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
  CloudOutlined,
  DollarOutlined,
  ExperimentOutlined,
  EyeOutlined,
  FieldTimeOutlined,
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
import { fddList, useOps } from '@/store/ops';
import { ROLE_LABEL, useUi } from '@/store/ui';
import { can, canViewPath, readonlyHint } from '@/auth/permissions';
import { ReadonlyNotice } from '@/components/PageState';
import { SUGGESTION_RISK_META, SUGGESTION_STATUS_META, SUGGESTION_TYPE_META } from '@/domain/opsMeta';
import { currencyCny } from '@/utils/format';
import { type Suggestion, type SuggestionRisk, type SuggestionStatus, type SuggestionType } from '@/mock/data';

type StatusFilter = SuggestionStatus | 'all' | 'reviewable';
type TypeFilter = SuggestionType | 'all';
type RiskFilter = SuggestionRisk | 'all';

const STATUS_OPTIONS: { label: string; value: StatusFilter }[] = [
  { label: '全部状态', value: 'all' },
  { label: '待我处理', value: 'reviewable' },
  { label: SUGGESTION_STATUS_META.draft.label, value: 'draft' },
  { label: SUGGESTION_STATUS_META.pending.label, value: 'pending' },
  { label: SUGGESTION_STATUS_META.approved.label, value: 'approved' },
  { label: SUGGESTION_STATUS_META.dispatched.label, value: 'dispatched' },
  { label: SUGGESTION_STATUS_META.rejected.label, value: 'rejected' },
];

const TYPE_OPTIONS: { label: string; value: TypeFilter }[] = [
  { label: '全部类型', value: 'all' },
  { label: SUGGESTION_TYPE_META.setpoint.label, value: 'setpoint' },
  { label: SUGGESTION_TYPE_META.schedule.label, value: 'schedule' },
];

const RISK_OPTIONS: { label: string; value: RiskFilter }[] = [
  { label: '全部风险', value: 'all' },
  { label: SUGGESTION_RISK_META.low.label, value: 'low' },
  { label: SUGGESTION_RISK_META.medium.label, value: 'medium' },
  { label: SUGGESTION_RISK_META.high.label, value: 'high' },
];

const isReviewable = (s: Suggestion) => s.status === 'pending' || s.status === 'approved';
const formatCurrency = currencyCny;

function decisionTimeline(s: Suggestion) {
  const step = SUGGESTION_STATUS_META[s.status].step;
  return [
    { color: 'blue', children: `生成建议：${s.createdAt}` },
    { color: step >= 1 ? 'gold' : 'gray', children: step >= 1 || s.status === 'rejected' ? `提交审批：${s.reviewer ?? '待分配审批人'}` : '等待提交审批' },
    { color: s.status === 'rejected' ? 'red' : step >= 2 ? 'green' : 'gray', children: s.status === 'rejected' ? '审批驳回，建议不可下发' : step >= 2 ? '审批通过，等待二次确认下发' : '等待审批结论' },
    { color: step >= 3 ? 'blue' : 'gray', children: step >= 3 ? '已下发策略，进入效果跟踪' : '未下发设备' },
  ];
}

export default function Optimize() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const suggestions = useOps((st) => st.suggestions);
  const { submitApproval, approve, reject, dispatch, simulateDispatch } = useOps();
  const { role, demoMode } = useUi();

  const [keyword, setKeyword] = useState('');
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [riskFilter, setRiskFilter] = useState<RiskFilter>('all');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const detailFocus = useOperationsDetailFocus();

  const selected = selectedId ? suggestions.find((s) => s.id === selectedId) ?? null : null;
  const canApprove = can(role, 'approve', 'optimization');
  const canReject = can(role, 'reject', 'optimization');
  const canDispatch = can(role, 'dispatch', 'optimization');
  const readonly = !canApprove && !canReject && !canDispatch;
  const suggestionParam = searchParams.get('suggestion');
  const linkedDiagnosis = selected
    ? fddList.find((entry) => entry.linkedSuggestionId === selected.id) ?? null
    : null;

  useEffect(() => {
    if (!suggestionParam) return;
    if (!suggestions.some((suggestion) => suggestion.id === suggestionParam)) {
      const next = new URLSearchParams(searchParams);
      next.delete('suggestion');
      setSearchParams(next, { replace: true });
      message.warning(`未找到优化建议 ${suggestionParam}`);
      return;
    }
    if (selectedId !== suggestionParam) {
      setTypeFilter('all');
      setStatusFilter('all');
      setRiskFilter('all');
      setKeyword('');
      setSelectedId(suggestionParam);
    }
  }, [searchParams, selectedId, setSearchParams, suggestionParam, suggestions]);

  const openSuggestion = (id: string, trigger?: HTMLElement) => {
    if (trigger) detailFocus.captureTrigger(trigger, id);
    const next = new URLSearchParams(searchParams);
    next.set('suggestion', id);
    setSearchParams(next, { replace: true });
    setSelectedId(id);
  };

  const closeSuggestion = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('suggestion');
    setSearchParams(next, { replace: true });
    setSelectedId(null);
    detailFocus.restoreFocus();
  };

  const summary = useMemo(() => {
    const totalSaving = suggestions.reduce((sum, s) => sum + s.saving.cny, 0);
    const totalKwh = suggestions.reduce((sum, s) => sum + s.saving.kwh, 0);
    const totalCo2 = suggestions.reduce((sum, s) => sum + s.saving.co2, 0);
    const pending = suggestions.filter((s) => s.status === 'pending').length;
    const approved = suggestions.filter((s) => s.status === 'approved').length;
    const dispatched = suggestions.filter((s) => s.status === 'dispatched').length;
    return { totalSaving, totalKwh, totalCo2, pending, approved, dispatched };
  }, [suggestions]);

  const rows = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return suggestions.filter((s) => {
      if (typeFilter !== 'all' && s.type !== typeFilter) return false;
      if (riskFilter !== 'all' && s.risk !== riskFilter) return false;
      if (statusFilter === 'reviewable' && !isReviewable(s)) return false;
      if (statusFilter !== 'all' && statusFilter !== 'reviewable' && s.status !== statusFilter) return false;
      if (!q) return true;
      return [s.id, s.title, s.device, s.scope, s.rationale, s.reviewer]
        .filter(Boolean)
        .some((value) => value!.toLowerCase().includes(q));
    });
  }, [keyword, riskFilter, statusFilter, suggestions, typeFilter]);

  const runAction = (suggestion: Suggestion) => {
    if (suggestion.status === 'draft' && canApprove) {
      submitApproval(suggestion.id);
      setStatusFilter('all');
      message.success(`${suggestion.id} 已提交审批`);
      return;
    }
    if (suggestion.status === 'pending' && canApprove) {
      approve(suggestion.id);
      setStatusFilter('all');
      message.success(`${suggestion.id} 已批准，等待下发`);
      return;
    }
    if (suggestion.status === 'approved' && canDispatch) {
      if (demoMode) simulateDispatch(suggestion.id);
      else dispatch(suggestion.id);
      setStatusFilter('all');
      message.success(`${suggestion.id} ${demoMode ? '已模拟下发' : '已下发'}`);
    }
  };

  const rejectAction = (suggestion: Suggestion) => {
    if (!canReject || suggestion.status !== 'pending') return;
    reject(suggestion.id);
    setStatusFilter('all');
    message.success(`${suggestion.id} 已驳回`);
  };

  const canRunPrimaryAction = (s: Suggestion) => {
    if (s.status === 'draft') return canApprove;
    if (s.status === 'pending') return canApprove;
    if (s.status === 'approved') return canDispatch;
    return false;
  };

  const primaryActionLabel = (s: Suggestion) => {
    if (s.status === 'draft') return '提交审批';
    if (s.status === 'pending') return '批准';
    if (s.status === 'approved') return demoMode ? '模拟下发' : '二次确认下发';
    if (s.status === 'dispatched') return '已下发';
    return '已驳回';
  };

  const columns: ColumnsType<Suggestion> = [
    {
      title: '建议',
      dataIndex: 'title',
      key: 'title',
      fixed: 'left',
      width: 270,
      render: (title: string, s) => (
        <Space direction="vertical" size={0}>
          <Space size={6} wrap>
            <Typography.Text strong>{title}</Typography.Text>
            <Tag color={SUGGESTION_TYPE_META[s.type].color}>{SUGGESTION_TYPE_META[s.type].label}</Tag>
          </Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }} copyable={{ text: s.id }}>{s.id} · {s.device}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '调整内容',
      key: 'diff',
      width: 210,
      render: (_, s) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{s.diff.param}</Typography.Text>
          <Typography.Text style={{ fontSize: 12 }}>
            <span style={{ textDecoration: 'line-through', opacity: 0.55 }}>{s.diff.current}{s.diff.unit}</span>
            {' → '}
            <span style={{ color: '#0FB5AE', fontWeight: 700 }}>{s.diff.proposed}{s.diff.unit}</span>
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: '预计收益',
      key: 'saving',
      width: 180,
      sorter: (a, b) => a.saving.cny - b.saving.cny,
      render: (_, s) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{formatCurrency(s.saving.cny)}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{s.saving.kwh} kWh · {s.saving.co2} kgCO₂</Typography.Text>
        </Space>
      ),
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
      title: '风险',
      dataIndex: 'risk',
      key: 'risk',
      width: 100,
      render: (risk: SuggestionRisk) => <Tag color={SUGGESTION_RISK_META[risk].color}>{SUGGESTION_RISK_META[risk].label}</Tag>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: SuggestionStatus) => <Tag color={SUGGESTION_STATUS_META[status].color}>{SUGGESTION_STATUS_META[status].label}</Tag>,
    },
    {
      title: '审核人',
      dataIndex: 'reviewer',
      key: 'reviewer',
      width: 120,
      render: (reviewer?: string) => reviewer ?? '待分配',
    },
    {
      title: '操作',
      key: 'action',
      fixed: 'right',
      width: 210,
      render: (_, s) => {
        const actionDisabled = !canRunPrimaryAction(s);
        const primary = s.status === 'approved' && !demoMode ? (
          <Popconfirm title="确认下发设备？此操作会进入策略执行链路" onConfirm={() => runAction(s)}>
            <Button size="small" type="primary" disabled={actionDisabled}>{primaryActionLabel(s)}</Button>
          </Popconfirm>
        ) : (
          <Button size="small" type="primary" disabled={actionDisabled} onClick={() => runAction(s)}>{primaryActionLabel(s)}</Button>
        );
        return (
          <Space size={6}>
            <Button
              size="small"
              icon={<EyeOutlined />}
              data-ops-detail-trigger={s.id}
              onClick={(event) => openSuggestion(s.id, event.currentTarget)}
            >
              详情
            </Button>
            {actionDisabled ? <Tooltip title={readonlyHint(role, 'optimization')}>{primary}</Tooltip> : primary}
            {s.status === 'pending' && <Button size="small" danger disabled={!canReject} onClick={() => rejectAction(s)}>驳回</Button>}
          </Space>
        );
      },
    },
  ];

  const tableColumns = compactTable
    ? columns.filter((column) => ['title', 'saving', 'confidence', 'status', 'action'].includes(String(column.key)))
    : columns;

  return (
    <PageScaffold
      title="节能优化建议"
      subtitle="把算法建议转成可审核、可回滚、可追踪的运行策略；所有下发动作必须人在回路。"
      eyebrow="策略与决策"
      extra={<Tag color={readonly ? 'default' : 'processing'}>{ROLE_LABEL[role]} · {readonly ? '只读' : '可审批'}</Tag>}
    >
      <Alert
        type="info"
        showIcon
        icon={<ExperimentOutlined />}
        message="人在回路：建议先评估收益、舒适度影响和回滚条件；审批通过后仍需二次确认或演示模拟，绝不直接静默下发设备。"
      />

      <OperationsMetrics
        items={[
          { label: '预计节电', value: summary.totalKwh, suffix: 'kWh/天', detail: '按当前建议池汇总', icon: <ThunderboltOutlined />, tone: 'accent' },
          { label: '预计节省', value: formatCurrency(summary.totalSaving), detail: '日收益，审批前估算', icon: <DollarOutlined />, tone: 'positive' },
          { label: '减排量', value: summary.totalCo2, suffix: 'kgCO₂/天', detail: '按节电量折算', icon: <CloudOutlined /> },
          { label: '待处理', value: summary.pending + summary.approved, detail: `${summary.pending} 条待审批 · ${summary.approved} 条待下发 · ${summary.dispatched} 条已下发`, icon: <FieldTimeOutlined />, tone: summary.pending + summary.approved ? 'warning' : 'positive' },
        ]}
      />

      <Card variant="borderless" styles={{ body: { padding: 16 } }}>
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <div className="ops-toolbar">
            <OperationsPanelHeading icon={<SafetyCertificateOutlined />} title="建议评估池" meta={`${rows.length} 条`} />
            <Space wrap>
              <Input.Search
                allowClear
                placeholder="搜索建议、设备、范围、审核人"
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
                style={{ width: 280 }}
              />
              <Select value={typeFilter} onChange={setTypeFilter} options={TYPE_OPTIONS} style={{ width: 150 }} />
              <Select value={statusFilter} onChange={setStatusFilter} options={STATUS_OPTIONS} style={{ width: 130 }} />
              <Select value={riskFilter} onChange={setRiskFilter} options={RISK_OPTIONS} style={{ width: 120 }} />
            </Space>
          </div>

          <Table<Suggestion>
            rowKey="id"
            size="middle"
            columns={tableColumns}
            dataSource={rows}
            pagination={{ pageSize: 8, showSizeChanger: false }}
            scroll={{ x: compactTable ? 900 : 1320 }}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的优化建议" /> }}
          />
        </Space>
      </Card>

      <Drawer
        rootClassName="ops-detail-drawer"
        title={selected ? (
          <OperationsDetailHeader
            eyebrow={`优化建议 · ${selected.createdAt}`}
            title={selected.title}
            subtitle={`${selected.device} · ${selected.scope}`}
            status={<Tag color={SUGGESTION_STATUS_META[selected.status].color}>{SUGGESTION_STATUS_META[selected.status].label}</Tag>}
            meta={<Typography.Text code>{selected.id}</Typography.Text>}
          />
        ) : '优化建议详情'}
        width={720}
        open={Boolean(selected)}
        onClose={closeSuggestion}
        afterOpenChange={(open) => {
          if (!open) detailFocus.restoreFocus();
        }}
        footer={selected ? (
          <OperationsActionFooter
            note={readonly ? '当前角色仅可查看建议，不能审批或下发。' : '状态变化会同步更新列表、详情和审批时间线。'}
          >
            <Button onClick={closeSuggestion}>关闭</Button>
            {linkedDiagnosis && canViewPath(role, '/fdd') ? (
              <Button
                icon={<BugOutlined />}
                onClick={() => navigate(`/fdd?diagnosis=${encodeURIComponent(linkedDiagnosis.id)}`)}
              >
                查看诊断
              </Button>
            ) : null}
            {linkedDiagnosis?.linkedAssetId && canViewPath(role, '/assets') ? (
              <Button
                icon={<ApartmentOutlined />}
                onClick={() => navigate(`/assets?device=${encodeURIComponent(linkedDiagnosis.linkedAssetId!)}`)}
              >
                查看资产
              </Button>
            ) : null}
            {selected.status === 'pending' ? (
              <Button danger disabled={!canReject} onClick={() => rejectAction(selected)}>驳回</Button>
            ) : null}
            {!['dispatched', 'rejected'].includes(selected.status) ? (
              selected.status === 'approved' && !demoMode ? (
                <Popconfirm
                  title="确认下发设备？"
                  description="策略将进入执行链路；请确认风险、舒适度影响与回滚条件均已复核。"
                  okText="确认下发"
                  cancelText="取消"
                  onConfirm={() => runAction(selected)}
                >
                  <Button type="primary" disabled={!canRunPrimaryAction(selected)}>{primaryActionLabel(selected)}</Button>
                </Popconfirm>
              ) : (
                <Button type="primary" disabled={!canRunPrimaryAction(selected)} onClick={() => runAction(selected)}>
                  {primaryActionLabel(selected)}
                </Button>
              )
            ) : null}
          </OperationsActionFooter>
        ) : null}
      >
        {selected ? (
          <div className="ops-detail-stack">
            <OperationsSummaryStrip
              ariaLabel="优化建议关键摘要"
              items={[
                { label: '预计节电', value: selected.saving.kwh, suffix: 'kWh/天', tone: 'accent' },
                { label: '预计节省', value: formatCurrency(selected.saving.cny), tone: 'positive' },
                { label: '减排量', value: selected.saving.co2, suffix: 'kgCO₂/天' },
                { label: '置信度', value: Math.round(selected.confidence * 100), suffix: '%', tone: selected.risk === 'high' ? 'warning' : 'default' },
              ]}
            />

            <OperationsDetailSection
              title="决策上下文"
              icon={<SafetyCertificateOutlined />}
              description="建议类型、风险、审核责任和收益回收周期。"
            >
              <Descriptions size="small" column={{ xs: 1, sm: 2 }} colon={false}>
                <Descriptions.Item label="类型"><Tag color={SUGGESTION_TYPE_META[selected.type].color}>{SUGGESTION_TYPE_META[selected.type].label}</Tag></Descriptions.Item>
                <Descriptions.Item label="风险"><Tag color={SUGGESTION_RISK_META[selected.risk].color}>{SUGGESTION_RISK_META[selected.risk].label}</Tag></Descriptions.Item>
                <Descriptions.Item label="设备">{selected.device}</Descriptions.Item>
                <Descriptions.Item label="范围">{selected.scope}</Descriptions.Item>
                <Descriptions.Item label="审核人">{selected.reviewer ?? '待分配'}</Descriptions.Item>
                <Descriptions.Item label="回收周期">{selected.paybackDays} 天</Descriptions.Item>
              </Descriptions>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="参数调整"
              icon={<ThunderboltOutlined />}
              description="审批和下发时必须使用同一参数口径。"
            >
              <div className="ops-detail-adjustment">
                <div className="ops-detail-adjustment-value">
                  <span className="ops-detail-adjustment-label">当前值 · {selected.diff.param}</span>
                  <strong>{selected.diff.current}{selected.diff.unit}</strong>
                </div>
                <span className="ops-detail-adjustment-arrow">→</span>
                <div className="ops-detail-adjustment-value">
                  <span className="ops-detail-adjustment-label">建议值</span>
                  <strong>{selected.diff.proposed}{selected.diff.unit}</strong>
                </div>
              </div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="算法依据"
              icon={<ExperimentOutlined />}
              description="解释建议为什么产生，以及使用了哪些运行假设。"
            >
              <div className="ops-detail-callout">{selected.rationale}</div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="舒适度影响"
              icon={<CloudOutlined />}
              description="审批人需要确认对室内环境和业务时段的影响。"
            >
              <div className={`ops-detail-callout ${selected.risk === 'high' ? 'is-critical' : selected.risk === 'medium' ? 'is-warning' : ''}`}>
                {selected.comfortImpact}
              </div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="回滚条件"
              icon={<FieldTimeOutlined />}
              description="执行异常或效果不达标时的恢复边界。"
            >
              <div className="ops-detail-callout is-warning">{selected.rollback}</div>
            </OperationsDetailSection>

            <OperationsDetailSection
              title="审批与执行进度"
              icon={<SafetyCertificateOutlined />}
              description="从建议生成到效果跟踪的决策链路。"
            >
              <OperationsTimeline items={decisionTimeline(selected)} />
            </OperationsDetailSection>

            {readonly ? (
              <ReadonlyNotice
                role={role}
                description="切换到内部研发角色后，可提交审批、批准、驳回或二次确认下发。"
              />
            ) : null}
          </div>
        ) : null}
      </Drawer>
    </PageScaffold>
  );
}
