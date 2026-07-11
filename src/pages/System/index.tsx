import { useEffect, useMemo, useState } from 'react';
import {
  Badge,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  Form,
  Grid,
  Input,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Tree,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { DataNode } from 'antd/es/tree';
import {
  ApiOutlined,
  ApartmentOutlined,
  AuditOutlined,
  BlockOutlined,
  CheckCircleOutlined,
  ClusterOutlined,
  DatabaseOutlined,
  EditOutlined,
  ExclamationCircleOutlined,
  LockOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  StopOutlined,
  UserOutlined,
  WifiOutlined,
} from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsActionFooter,
  OperationsDetailHeader,
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
  OperationsSectionIntro,
} from '@/components/OperationsUI';
import { can, type PermissionAction, type PermissionSubject } from '@/auth/permissions';
import { BRAND, STATUS } from '@/theme/tokens';
import { useUi, type Role } from '@/store/ui';
import {
  mockUsers,
  mockSites,
  mockAssetTree,
  mockAuditLogs,
  SCOPE_CATALOG,
  ROLE_LABEL,
  ROLE_COLOR,
  AUDIT_EVENT_LABEL,
  AUDIT_EVENT_TYPES,
  type SystemUser,
  type BackendRole,
  type AssetNode,
  type AuditLog,
  type AuditResult,
} from '@/mock/system';
import './System.css';

const { Text } = Typography;

/* ---------- tree mutation helper (immutable) ---------- */
function insertNode(nodes: AssetNode[], parentKey: string | null, node: AssetNode): AssetNode[] {
  if (parentKey === null) return [...nodes, node];
  return nodes.map((item) => {
    if (item.key === parentKey) return { ...item, children: [...(item.children ?? []), node] };
    if (item.children) return { ...item, children: insertNode(item.children, parentKey, node) };
    return item;
  });
}

let nodeSeq = 100;

const INIT_TAB = (() => {
  if (typeof window === 'undefined') return 'overview';
  const tab = new URLSearchParams(window.location.search).get('tab');
  return ['overview', 'users', 'site', 'integrations', 'rules', 'audit'].includes(tab ?? '') ? tab! : 'overview';
})();

type IntegrationStatus = 'online' | 'degraded' | 'offline';
type DataSource = {
  key: string;
  name: string;
  type: string;
  endpoint: string;
  status: IntegrationStatus;
  latencyMs: number;
  lastSync: string;
  owner: string;
};

type RuleStatus = 'enabled' | 'disabled' | 'draft';
type AlarmRule = {
  id: string;
  name: string;
  target: string;
  condition: string;
  severity: string;
  status: RuleStatus;
  notify: string[];
  updatedAt: string;
};

type MatrixRow = {
  subject: PermissionSubject;
  label: string;
  action: PermissionAction;
};

const ROLE_MAP: Record<Role, BackendRole> = {
  demo: 'READONLY',
  ops: 'MAINTENANCE',
  rd: 'ADMIN',
};

const MATRIX_ROWS: MatrixRow[] = [
  { subject: 'dashboard', label: '总览驾驶舱', action: 'view' },
  { subject: 'assets', label: '设备与建筑', action: 'view' },
  { subject: 'energy', label: '能耗分析', action: 'view' },
  { subject: 'cost', label: '成本绩效', action: 'view' },
  { subject: 'fdd', label: '故障检测', action: 'view' },
  { subject: 'alarms', label: '报警工单', action: 'view' },
  { subject: 'workOrder', label: '工单创建', action: 'create' },
  { subject: 'workOrder', label: '工单流转', action: 'transition' },
  { subject: 'optimization', label: '优化审批', action: 'approve' },
  { subject: 'optimization', label: '优化下发', action: 'dispatch' },
  { subject: 'asset', label: '资产维护', action: 'manage' },
  { subject: 'system', label: '系统管理', action: 'view' },
];

const DATA_SOURCES: DataSource[] = [
  { key: 'rest', name: 'HVAC REST API', type: 'REST', endpoint: '/api/v1', status: 'online', latencyMs: 42, lastSync: '刚刚', owner: 'backend' },
  { key: 'ws', name: 'Telemetry Socket.IO', type: 'WebSocket', endpoint: '/ws/telemetry', status: 'online', latencyMs: 28, lastSync: '刚刚', owner: 'realtime' },
  { key: 'tb', name: 'ThingsBoard Asset Tree', type: 'Connector', endpoint: 'assets/tree', status: 'degraded', latencyMs: 186, lastSync: '3 分钟前', owner: 'integration' },
  { key: 'ai', name: 'AI Chat Gateway', type: 'SSE', endpoint: '/api/v1/ai/chat', status: 'degraded', latencyMs: 320, lastSync: 'Mock 模式', owner: 'ai' },
];

const ALARM_RULES: AlarmRule[] = [
  { id: 'RULE-001', name: '冷冻水出水温度偏离', target: '冷水机组', condition: '出水温度 - 设定值 > 2℃ 持续 10min', severity: 'critical', status: 'enabled', notify: ['运维主管', '值班组'], updatedAt: '今天 09:10' },
  { id: 'RULE-002', name: 'COP 低效运行', target: '冷水机组', condition: 'COP < 4.5 持续 30min', severity: 'major', status: 'enabled', notify: ['能源负责人'], updatedAt: '今天 08:40' },
  { id: 'RULE-003', name: '滤网压差偏高', target: 'AHU', condition: '压差 > 150Pa 持续 15min', severity: 'minor', status: 'enabled', notify: ['楼层维护'], updatedAt: '昨天 18:20' },
  { id: 'RULE-004', name: '夜间异常运行', target: '空调末端', condition: '非营业时段功率 > 20kW', severity: 'major', status: 'draft', notify: ['节能管理员'], updatedAt: '草稿' },
];

const statusMeta: Record<IntegrationStatus, { label: string; color: string }> = {
  online: { label: '在线', color: STATUS.ok },
  degraded: { label: '降级', color: STATUS.warn },
  offline: { label: '离线', color: STATUS.err },
};

const ruleStatusMeta: Record<RuleStatus, { label: string; color: string }> = {
  enabled: { label: '启用', color: 'green' },
  disabled: { label: '禁用', color: 'default' },
  draft: { label: '草稿', color: 'gold' },
};

const flattenAssets = (nodes: AssetNode[]): AssetNode[] => nodes.flatMap((node) => [node, ...(node.children ? flattenAssets(node.children) : [])]);

export default function System() {
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const { role } = useUi();
  const [activeTab, setActiveTab] = useState(INIT_TAB);
  const [users, setUsers] = useState<SystemUser[]>(mockUsers);
  const [tree, setTree] = useState<AssetNode[]>(mockAssetTree);
  const [siteId, setSiteId] = useState<string>(mockSites[0].id);

  const [userModal, setUserModal] = useState<null | { mode: 'create' } | { mode: 'edit'; user: SystemUser }>(null);
  const [userForm] = Form.useForm();

  const [assetModal, setAssetModal] = useState(false);
  const [assetForm] = Form.useForm();

  useEffect(() => {
    if (!userModal && !assetModal) return undefined;

    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      event.stopPropagation();
      if (userModal) setUserModal(null);
      if (assetModal) setAssetModal(false);
    };

    document.addEventListener('keydown', closeOnEscape, true);
    return () => document.removeEventListener('keydown', closeOnEscape, true);
  }, [assetModal, userModal]);

  const [evFilter, setEvFilter] = useState<string>('all');
  const [resFilter, setResFilter] = useState<AuditResult | 'all'>('all');
  const [kw, setKw] = useState('');

  const activeUsers = users.filter((user) => user.status === 'active').length;
  const adminUsers = users.filter((user) => user.role === 'ADMIN').length;
  const flatAssetNodes = useMemo(() => flattenAssets(tree), [tree]);
  const degradedSources = DATA_SOURCES.filter((source) => source.status !== 'online').length;
  const activeRules = ALARM_RULES.filter((rule) => rule.status === 'enabled').length;

  const openCreate = () => {
    userForm.resetFields();
    userForm.setFieldsValue({ role: 'READONLY', scopes: ['asset:read', 'device:read', 'telemetry:read'] });
    setUserModal({ mode: 'create' });
  };

  const openEdit = (user: SystemUser) => {
    userForm.resetFields();
    userForm.setFieldsValue({ role: user.role, scopes: user.scopes });
    setUserModal({ mode: 'edit', user });
  };

  const submitUser = () => {
    userForm.validateFields().then((values) => {
      const summary = userModal?.mode === 'edit'
        ? `确认将用户「${userModal.user.username}」的角色修改为「${ROLE_LABEL[values.role as BackendRole]}」？`
        : `确认创建用户「${values.username}」并授予角色「${ROLE_LABEL[values.role as BackendRole]}」？`;
      Modal.confirm({
        title: '二次确认（人在回路）',
        icon: <SafetyCertificateOutlined style={{ color: BRAND.teal }} />,
        content: summary,
        okText: '确认提交',
        cancelText: '取消',
        onOk: () => {
          if (userModal?.mode === 'edit') {
            setUsers((list) => list.map((item) => (item.id === userModal.user.id ? { ...item, role: values.role, scopes: values.scopes } : item)));
            message.success('角色已更新（mock）');
          } else {
            const newUser: SystemUser = {
              id: `u${++nodeSeq}`,
              username: values.username,
              email: values.email,
              role: values.role,
              scopes: values.scopes ?? [],
              status: 'active',
              lastLogin: '—',
            };
            setUsers((list) => [newUser, ...list]);
            message.success('用户已创建（mock）');
          }
          setUserModal(null);
        },
      });
    });
  };

  const toggleStatus = (user: SystemUser) => {
    setUsers((list) => list.map((item) => (item.id === user.id ? { ...item, status: item.status === 'active' ? 'disabled' : 'active' } : item)));
    message.success(user.status === 'active' ? '已禁用（mock）' : '已启用（mock）');
  };

  const openAddAsset = () => {
    assetForm.resetFields();
    assetForm.setFieldsValue({ parent: 'root', type: 'building' });
    setAssetModal(true);
  };

  const submitAsset = () => {
    assetForm.validateFields().then((values) => {
      Modal.confirm({
        title: '二次确认（人在回路）',
        icon: <SafetyCertificateOutlined style={{ color: BRAND.teal }} />,
        content: `确认在「${values.parent === 'root' ? '根' : values.parent}」下新增${values.type === 'building' ? '建筑' : values.type === 'zone' ? '分区' : '机组'}「${values.name}」？`,
        okText: '确认提交',
        cancelText: '取消',
        onOk: () => {
          const parentKey = values.parent === 'root' ? null : values.parent;
          const node: AssetNode = { key: `n${++nodeSeq}`, title: values.name, type: values.type };
          setTree((current) => insertNode(current, parentKey, node));
          message.success('节点已新增（mock）');
          setAssetModal(false);
        },
      });
    });
  };

  const userColumns: ColumnsType<SystemUser> = [
    { title: '用户名', dataIndex: 'username', width: 130, render: (value) => <Text strong>{value}</Text> },
    { title: '邮箱', dataIndex: 'email', width: 190, render: (value) => <Text type="secondary">{value}</Text> },
    {
      title: '角色', dataIndex: 'role', width: 120,
      render: (value: BackendRole) => <Tag color={ROLE_COLOR[value]} style={{ fontWeight: 600 }}>{ROLE_LABEL[value]}</Tag>,
    },
    {
      title: '权限范围', dataIndex: 'scopes',
      render: (scopes: string[]) => {
        const shown = scopes.slice(0, 3);
        const rest = scopes.slice(3);
        return (
          <Space size={4} wrap>
            {shown.map((key) => {
              const item = SCOPE_CATALOG.find((scope) => scope.key === key);
              return <Tag key={key} bordered={false}>{item?.label ?? key}</Tag>;
            })}
            {rest.length > 0 && (
              <Tooltip title={rest.map((key) => SCOPE_CATALOG.find((scope) => scope.key === key)?.label ?? key).join('、')}>
                <Tag bordered={false}>+{rest.length}</Tag>
              </Tooltip>
            )}
          </Space>
        );
      },
    },
    {
      title: '状态', dataIndex: 'status', width: 90,
      render: (value: SystemUser['status']) => value === 'active' ? <Tag color={STATUS.ok}>启用</Tag> : <Tag color={STATUS.err}>禁用</Tag>,
    },
    { title: '最近登录', dataIndex: 'lastLogin', width: 170, render: (value) => <Text type="secondary" style={{ fontSize: 12 }}>{value}</Text> },
    {
      title: '操作', key: 'op', width: 170, fixed: 'right',
      render: (_, user) => (
        <Space size={4}>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(user)}>改角色</Button>
          <Popconfirm
            title={user.status === 'active' ? '确认禁用该用户？' : '确认启用该用户？'}
            description={user.status === 'active' ? '禁用后该账户将无法登录，现有审计记录仍会保留。' : '启用后该账户将恢复当前角色与 scope 权限。'}
            okText={user.status === 'active' ? '确认禁用' : '确认启用'}
            cancelText="取消"
            okButtonProps={{ danger: user.status === 'active' }}
            onConfirm={() => toggleStatus(user)}
          >
            <Button size="small" icon={<StopOutlined />} danger={user.status === 'active'}>{user.status === 'active' ? '禁用' : '启用'}</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const matrixColumns: ColumnsType<MatrixRow> = [
    { title: '权限项', dataIndex: 'label', width: 180, render: (value, row) => <Space direction="vertical" size={0}><Text strong>{value}</Text><Text type="secondary" style={{ fontSize: 12 }}>{row.action} · {row.subject}</Text></Space> },
    ...(['demo', 'ops', 'rd'] as Role[]).map((roleKey) => ({
      title: `${ROLE_LABEL[ROLE_MAP[roleKey]]}`,
      key: roleKey,
      width: 100,
      align: 'center' as const,
      render: (_: unknown, row: MatrixRow) => can(roleKey, row.action, row.subject)
        ? <CheckCircleOutlined style={{ color: STATUS.ok }} />
        : <LockOutlined style={{ color: '#bfbfbf' }} />,
    })),
  ];

  const dataSourceColumns: ColumnsType<DataSource> = [
    { title: '数据源', dataIndex: 'name', width: 220, render: (value, row) => <Space direction="vertical" size={0}><Text strong>{value}</Text><Text type="secondary" style={{ fontSize: 12 }}>{row.type} · {row.owner}</Text></Space> },
    { title: '端点', dataIndex: 'endpoint', render: (value) => <Text code>{value}</Text> },
    { title: '状态', dataIndex: 'status', width: 100, render: (value: IntegrationStatus) => <Badge color={statusMeta[value].color} text={statusMeta[value].label} /> },
    { title: '延迟', dataIndex: 'latencyMs', width: 90, render: (value) => `${value} ms` },
    { title: '最后同步', dataIndex: 'lastSync', width: 110, render: (value) => <Text type="secondary">{value}</Text> },
  ];

  const ruleColumns: ColumnsType<AlarmRule> = [
    { title: '规则', dataIndex: 'name', width: 220, render: (value, row) => <Space direction="vertical" size={0}><Text strong>{value}</Text><Text type="secondary" style={{ fontSize: 12 }}>{row.id} · {row.target}</Text></Space> },
    { title: '条件', dataIndex: 'condition', render: (value) => <Text>{value}</Text> },
    { title: '级别', dataIndex: 'severity', width: 100, render: (value) => <Tag color={value === 'critical' ? 'red' : value === 'major' ? 'gold' : 'blue'}>{value}</Tag> },
    { title: '通知', dataIndex: 'notify', width: 180, render: (value: string[]) => <Space size={4} wrap>{value.map((item) => <Tag key={item}>{item}</Tag>)}</Space> },
    { title: '状态', dataIndex: 'status', width: 90, render: (value: RuleStatus) => <Tag color={ruleStatusMeta[value].color}>{ruleStatusMeta[value].label}</Tag> },
    { title: '更新时间', dataIndex: 'updatedAt', width: 110, render: (value) => <Text type="secondary">{value}</Text> },
  ];

  const treeData = useMemo(() => {
    const icon = (type: AssetNode['type']) =>
      type === 'building' ? <ApartmentOutlined style={{ color: BRAND.teal }} />
        : type === 'zone' ? <ClusterOutlined style={{ color: STATUS.info }} />
          : <BlockOutlined style={{ color: STATUS.warn }} />;
    const walk = (nodes: AssetNode[]): DataNode[] => nodes.map((node) => ({
      key: node.key,
      title: node.title,
      icon: icon(node.type),
      children: node.children ? walk(node.children) : undefined,
    }));
    return walk(tree);
  }, [tree]);

  const flatNodes = useMemo(() => {
    const out: { key: string; title: string }[] = [];
    const walk = (nodes: AssetNode[], depth: number) => nodes.forEach((node) => {
      out.push({ key: node.key, title: '　'.repeat(depth) + node.title });
      if (node.children) walk(node.children, depth + 1);
    });
    walk(tree, 0);
    return out;
  }, [tree]);

  const filteredAudit = useMemo(() => {
    return mockAuditLogs.filter((item) => {
      if (evFilter !== 'all' && item.eventType !== evFilter) return false;
      if (resFilter !== 'all' && item.result !== resFilter) return false;
      if (kw && !(item.userId.includes(kw) || item.targetId.includes(kw) || item.action.includes(kw))) return false;
      return true;
    });
  }, [evFilter, resFilter, kw]);

  const auditColumns: ColumnsType<AuditLog> = [
    { title: '时间', dataIndex: 'createdAt', width: 160, render: (value: string) => <Text style={{ fontSize: 12, fontFamily: 'monospace' }}>{new Date(value).toLocaleString('zh-CN')}</Text> },
    { title: '操作人', dataIndex: 'userId', width: 100, render: (value) => <Text strong>{value}</Text> },
    { title: '事件类型', dataIndex: 'eventType', width: 110, render: (value: string) => <Tag color={BRAND.tealStrong}>{AUDIT_EVENT_LABEL[value] ?? value}</Tag> },
    { title: '结果', dataIndex: 'result', width: 90, render: (value: AuditResult) => value === 'SUCCESS' ? <Tag color={STATUS.ok}>成功</Tag> : <Tag color={STATUS.err}>失败</Tag> },
    { title: '动作', dataIndex: 'action', render: (value) => <Text>{value}</Text> },
    { title: '目标', width: 160, render: (_, row) => <Text type="secondary" style={{ fontSize: 12 }}>{row.targetEntity}#{row.targetId}</Text> },
    { title: 'IP', dataIndex: 'ipAddress', width: 120, render: (value) => <Text type="secondary" style={{ fontSize: 12, fontFamily: 'monospace' }}>{value}</Text> },
  ];

  const visibleUserColumns = compactTable ? userColumns.filter((_, index) => [0, 2, 4, 6].includes(index)) : userColumns;
  const visibleMatrixColumns = compactTable ? matrixColumns.filter((_, index) => [0, 1, 3].includes(index)) : matrixColumns;
  const visibleDataSourceColumns = compactTable ? dataSourceColumns.filter((_, index) => [0, 2, 3].includes(index)) : dataSourceColumns;
  const visibleRuleColumns = compactTable ? ruleColumns.filter((_, index) => [0, 1, 2, 4].includes(index)) : ruleColumns;
  const visibleAuditColumns = compactTable ? auditColumns.filter((_, index) => [0, 1, 2, 3, 4].includes(index)) : auditColumns;

  const handleTabChange = (key: string) => {
    setActiveTab(key);
    const url = new URL(window.location.href);
    url.searchParams.set('tab', key);
    window.history.replaceState(null, '', `${url.pathname}${url.search}${url.hash}`);
  };

  const overviewTab = (
    <div className="system-tab-stack">
      <OperationsSectionIntro
        title="治理概览"
        icon={<SafetyCertificateOutlined />}
        description="集中查看身份权限、资产拓扑、数据接入和规则链路的当前治理状态。"
        meta="只读概览"
      />

      <OperationsMetrics
        items={[
          { label: '启用用户', value: activeUsers, suffix: `/ ${users.length}`, detail: '当前可登录用户', icon: <UserOutlined />, tone: 'accent' },
          { label: '管理员', value: adminUsers, detail: '拥有系统治理权限', icon: <SafetyCertificateOutlined /> },
          { label: '资产节点', value: flatAssetNodes.length, detail: '建筑、分区与设备节点', icon: <ApartmentOutlined /> },
          { label: '降级数据源', value: degradedSources, detail: degradedSources ? '需要检查集成状态' : '全部数据源在线', icon: <ApiOutlined />, tone: degradedSources ? 'warning' : 'positive' },
        ]}
      />

      <OperationsInsightBand
        title="治理边界"
        icon={<LockOutlined />}
        items={[
          { text: '用户、角色、资产和规则写入必须经过权限校验与二次确认。', tone: 'positive' },
          { text: '当前写操作仅在本地 mock 生效，不代表生产配置已改变。', tone: 'warning' },
          { text: '真实接入后必须为每次变更生成审计记录与 traceId。', tone: 'info' },
        ]}
      />

      <Row gutter={[16, 16]} className="system-equal-row">
        <Col xs={24} lg={12}>
          <Card
            title={<OperationsPanelHeading title="角色权限矩阵" icon={<LockOutlined />} meta="前端权限映射" />}
            variant="borderless"
          >
            <Table<MatrixRow>
              rowKey={(row) => `${row.subject}-${row.action}`}
              size="small"
              columns={visibleMatrixColumns}
              dataSource={MATRIX_ROWS}
              pagination={false}
              scroll={{ x: compactTable ? 420 : 520 }}
            />
          </Card>
        </Col>
        <Col xs={24} lg={12}>
          <Card
            title={<OperationsPanelHeading title="服务健康" icon={<WifiOutlined />} meta={`${DATA_SOURCES.length} 个数据源`} />}
            variant="borderless"
          >
            <div className="system-health-list">
              {DATA_SOURCES.map((item) => (
                <div className="system-health-row" key={item.key}>
                  <Badge color={statusMeta[item.status].color} />
                  <div className="system-health-copy">
                    <div className="system-health-title">
                      <Text strong>{item.name}</Text>
                      <Text type="secondary">{item.type}</Text>
                    </div>
                    <Text type="secondary">{item.endpoint} · {item.lastSync}</Text>
                  </div>
                  <div className="system-health-value">
                    <Text strong>{item.latencyMs} ms</Text>
                    <Text type="secondary">{statusMeta[item.status].label}</Text>
                  </div>
                </div>
              ))}
            </div>
          </Card>
        </Col>
      </Row>
    </div>
  );

  const usersTab = (
    <div className="system-tab-stack">
      <OperationsSectionIntro
        title="身份与访问控制"
        icon={<UserOutlined />}
        description="管理用户生命周期、后端角色与 scope 范围。禁用和提权属于高风险变更，必须保留确认与审计。"
        meta={`${activeUsers} 个启用账户`}
        actions={<Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建用户</Button>}
      />

      <Row gutter={[16, 16]} className="system-equal-row">
        <Col xs={24} xl={16}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading title="用户账户" meta={`${users.length} 个账户`} />}
          >
            <Table<SystemUser>
              rowKey="id"
              size="small"
              columns={visibleUserColumns}
              dataSource={users}
              pagination={{ pageSize: 6, showSizeChanger: false }}
              scroll={{ x: compactTable ? 640 : 980 }}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无用户账户" /> }}
            />
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading title="权限范围目录" icon={<SafetyCertificateOutlined />} meta={`${SCOPE_CATALOG.length} 项`} />}
          >
            <div className="system-card-note">
              Role 决定基础能力，scope 用于进一步收敛资源访问范围；两者都必须由后端鉴权执行。
            </div>
            <div className="system-scope-list">
              {SCOPE_CATALOG.map((scope) => (
                <div key={scope.key} className="ops-definition-row">
                  <div className="system-definition-title">
                    <Text strong>{scope.label}</Text>
                    <Text code>{scope.key}</Text>
                  </div>
                  <Text type="secondary">{scope.desc}</Text>
                </div>
              ))}
            </div>
          </Card>
        </Col>
      </Row>
    </div>
  );

  const siteTab = (
    <div className="system-tab-stack">
      <OperationsSectionIntro
        title="站点与资产拓扑"
        icon={<ApartmentOutlined />}
        description="维护建筑、分区和设备节点的层级关系。结构变更会影响遥测归属、FDD 对象和权限范围。"
        meta={`${flatAssetNodes.length} 个节点`}
        actions={
          <>
            <Select
              value={siteId}
              aria-label="选择站点"
              onChange={setSiteId}
              options={mockSites.map((site) => ({ value: site.id, label: site.name }))}
            />
            <Button type="primary" icon={<PlusOutlined />} onClick={openAddAsset}>新增节点</Button>
          </>
        }
      />

      <OperationsInsightBand
        title="结构变更"
        icon={<ClusterOutlined />}
        items={[
          { text: '新增节点当前仅写入本地 mock 树。', tone: 'warning' },
          { text: '生产写入必须校验父子类型、唯一名称与资源权限。', tone: 'info' },
          { text: '成功变更后应触发资产同步并生成审计记录。', tone: 'positive' },
        ]}
      />

      <Row gutter={[16, 16]} className="system-equal-row">
        <Col xs={24} lg={15}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading title="资产结构" meta={mockSites.find((site) => site.id === siteId)?.name} />}
          >
            <div className="system-tree-shell">
              <Tree showIcon defaultExpandAll treeData={treeData} />
            </div>
          </Card>
        </Col>
        <Col xs={24} lg={9}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading title="站点配置摘要" icon={<DatabaseOutlined />} />}
          >
            <Descriptions column={1} size="small" className="system-descriptions">
              <Descriptions.Item label="当前站点">{mockSites.find((site) => site.id === siteId)?.name}</Descriptions.Item>
              <Descriptions.Item label="站点 ID"><Text code>{siteId}</Text></Descriptions.Item>
              <Descriptions.Item label="资产节点">{flatAssetNodes.length}</Descriptions.Item>
              <Descriptions.Item label="同步模式">Mock Tree</Descriptions.Item>
              <Descriptions.Item label="目标接口"><Text code>GET /assets/tree</Text></Descriptions.Item>
              <Descriptions.Item label="写入策略">二次确认 + 审计日志</Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
      </Row>
    </div>
  );

  const integrationsTab = (
    <div className="system-tab-stack">
      <OperationsSectionIntro
        title="接口与数据源"
        icon={<ApiOutlined />}
        description="查看 REST、实时遥测、资产连接器与 AI 网关的健康度、同步状态和延迟。"
        meta={`${DATA_SOURCES.length - degradedSources} / ${DATA_SOURCES.length} 在线`}
      />

      <OperationsMetrics
        items={[
          { label: '数据源', value: DATA_SOURCES.length, detail: 'REST、WebSocket 与连接器', icon: <ApiOutlined /> },
          { label: '在线', value: DATA_SOURCES.filter((item) => item.status === 'online').length, detail: '当前健康连接', tone: 'positive' },
          { label: '降级', value: degradedSources, detail: degradedSources ? '存在延迟或 Mock 连接' : '当前无降级连接', tone: degradedSources ? 'warning' : 'positive' },
          { label: '平均延迟', value: Math.round(DATA_SOURCES.reduce((sum, item) => sum + item.latencyMs, 0) / DATA_SOURCES.length), suffix: 'ms', detail: '跨全部数据源计算', tone: 'accent' },
        ]}
      />

      <OperationsInsightBand
        title="接入状态"
        icon={<WifiOutlined />}
        items={DATA_SOURCES.filter((source) => source.status !== 'online').map((source) => ({
          key: source.key,
          text: `${source.name} 当前${statusMeta[source.status].label}，延迟 ${source.latencyMs} ms，最后同步：${source.lastSync}。`,
          tone: source.status === 'offline' ? 'critical' : 'warning',
        }))}
      />

      <Card
        title={<OperationsPanelHeading title="连接清单" meta={`${DATA_SOURCES.length} 个端点`} />}
        variant="borderless"
      >
        <Table<DataSource>
          rowKey="key"
          size="small"
          columns={visibleDataSourceColumns}
          dataSource={DATA_SOURCES}
          pagination={false}
          scroll={{ x: compactTable ? 520 : 760 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据源" /> }}
        />
      </Card>
    </div>
  );

  const rulesTab = (
    <div className="system-tab-stack">
      <OperationsSectionIntro
        title="告警与诊断规则"
        icon={<SettingOutlined />}
        description="治理阈值、持续时间、严重级别与通知对象。规则变更会直接影响 FDD、报警和工单链路。"
        meta={`${activeRules} 条启用`}
        actions={
          <Tooltip title="等待规则写入 API 与灰度发布能力">
            <Button icon={<PlusOutlined />} disabled>新增规则</Button>
          </Tooltip>
        }
      />

      <OperationsInsightBand
        title="规则发布边界"
        icon={<ExclamationCircleOutlined />}
        items={[
          { text: '生产规则必须经过二次确认、审计记录和灰度启用。', tone: 'warning' },
          { text: '草稿规则不参与 FDD、报警和通知计算。', tone: 'info' },
          { text: '修改阈值前应核对历史误报率与设备工况。', tone: 'positive' },
        ]}
      />

      <OperationsMetrics
        items={[
          { label: '规则总数', value: ALARM_RULES.length, detail: '覆盖冷站与末端设备' },
          { label: '启用规则', value: activeRules, detail: '当前参与告警与诊断链路', tone: 'positive' },
          { label: '草稿规则', value: ALARM_RULES.filter((rule) => rule.status === 'draft').length, detail: '尚未进入生产链路', tone: 'warning' },
        ]}
      />

      <Card
        title={<OperationsPanelHeading title="规则清单" meta={`${ALARM_RULES.length} 条`} />}
        variant="borderless"
      >
        <Table<AlarmRule>
          rowKey="id"
          size="small"
          columns={visibleRuleColumns}
          dataSource={ALARM_RULES}
          pagination={false}
          scroll={{ x: compactTable ? 720 : 980 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无告警规则" /> }}
        />
      </Card>
    </div>
  );

  const auditTab = (
    <div className="system-tab-stack">
      <OperationsSectionIntro
        title="操作审计"
        icon={<AuditOutlined />}
        description="追踪身份、资产、规则和配置变更。展开记录可查看 traceId、客户端与结构化详情。"
        meta={`${filteredAudit.length} 条匹配记录`}
      />

      <Card
        variant="borderless"
        title={<OperationsPanelHeading title="审计记录" meta="只读日志" />}
      >
        <div className="system-audit-toolbar" aria-label="审计筛选">
          <Select
            value={evFilter}
            aria-label="筛选事件类型"
            onChange={setEvFilter}
            options={[{ value: 'all', label: '全部事件' }, ...AUDIT_EVENT_TYPES.map((eventType) => ({ value: eventType, label: AUDIT_EVENT_LABEL[eventType] ?? eventType }))]}
          />
          <Select
            value={resFilter}
            aria-label="筛选执行结果"
            onChange={setResFilter}
            options={[{ value: 'all', label: '全部结果' }, { value: 'SUCCESS', label: '成功' }, { value: 'FAILURE', label: '失败' }]}
          />
          <Input.Search
            placeholder="操作人 / 目标 / 动作"
            allowClear
            aria-label="搜索审计记录"
            onChange={(event) => setKw(event.target.value)}
          />
          <Text type="secondary">点击行首展开按钮查看完整上下文。</Text>
        </div>

        <Table<AuditLog>
          rowKey="id"
          size="small"
          columns={visibleAuditColumns}
          dataSource={filteredAudit}
          pagination={{ pageSize: 8, showSizeChanger: false }}
          scroll={{ x: compactTable ? 760 : 1040 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的审计记录" /> }}
          expandable={{
            expandedRowRender: (row) => (
              <Descriptions size="small" column={1} className="system-audit-detail">
                <Descriptions.Item label="traceId"><Text code>{row.traceId}</Text></Descriptions.Item>
                <Descriptions.Item label="userAgent">{row.userAgent}</Descriptions.Item>
                <Descriptions.Item label="details (jsonb)"><pre>{JSON.stringify(row.details, null, 2)}</pre></Descriptions.Item>
              </Descriptions>
            ),
            rowExpandable: (row) => Boolean(row.traceId || row.userAgent || Object.keys(row.details).length),
          }}
        />
      </Card>
    </div>
  );

  return (
    <PageScaffold
      title="系统管理"
      subtitle="治理身份权限、站点资产、数据接入、告警规则和审计证据，所有高风险变更保持人在回路。"
      eyebrow="治理与配置"
      extra={
        <Space size={8} wrap>
          <Tag icon={<SafetyCertificateOutlined />}>当前角色：{ROLE_LABEL[ROLE_MAP[role]]}</Tag>
          <Tag color="gold">Mock 写入 · 二次确认</Tag>
        </Space>
      }
    >
      <Tabs
        className="system-governance-tabs"
        activeKey={activeTab}
        onChange={handleTabChange}
        tabBarGutter={8}
        items={[
          { key: 'overview', label: <span className="system-tab-label"><SafetyCertificateOutlined />治理概览</span>, children: overviewTab },
          { key: 'users', label: <span className="system-tab-label"><UserOutlined />用户权限</span>, children: usersTab },
          { key: 'site', label: <span className="system-tab-label"><ApartmentOutlined />站点资产</span>, children: siteTab },
          { key: 'integrations', label: <span className="system-tab-label"><ApiOutlined />数据接入</span>, children: integrationsTab },
          { key: 'rules', label: <span className="system-tab-label"><SettingOutlined />规则治理</span>, children: rulesTab },
          { key: 'audit', label: <span className="system-tab-label"><AuditOutlined />操作审计</span>, children: auditTab },
        ]}
      />

      <Modal
        open={Boolean(userModal)}
        title={
          <OperationsDetailHeader
            eyebrow="身份与访问控制"
            title={userModal?.mode === 'edit' ? '修改用户权限' : '新建用户'}
            subtitle={userModal?.mode === 'edit' ? `${userModal.user.username} · ${userModal.user.email}` : '创建账户并分配后端角色与 scope 范围。'}
          />
        }
        width={620}
        className="ops-detail-modal system-governance-modal"
        onCancel={() => setUserModal(null)}
        footer={
          <OperationsActionFooter note="提交后将进入二次确认，本次变更当前仅写入 mock 状态。">
            <Button onClick={() => setUserModal(null)}>取消</Button>
            <Button type="primary" onClick={submitUser}>审阅并确认</Button>
          </OperationsActionFooter>
        }
        destroyOnHidden
      >
        <div className="system-modal-note">
          <SafetyCertificateOutlined />
          <span>{userModal?.mode === 'edit' ? `正在修改 ${userModal.user.username} 的角色与 scope。提交后还会进行二次确认。` : '账户创建会授予登录能力与权限范围，提交后还会进行二次确认。'}</span>
        </div>
        <Form form={userForm} layout="vertical">
          {userModal?.mode === 'create' && (
            <>
              <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
                <Input placeholder="如 zhaomin" />
              </Form.Item>
              <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email', message: '请输入合法邮箱' }]}>
                <Input placeholder="name@corp.io" />
              </Form.Item>
              <Form.Item name="password" label="初始密码" rules={[{ required: true, min: 6, message: '至少 6 位' }]}>
                <Input.Password placeholder="初始密码" />
              </Form.Item>
            </>
          )}
          <Form.Item name="role" label="角色" rules={[{ required: true }]}>
            <Select options={(['ADMIN', 'MAINTENANCE', 'READONLY'] as BackendRole[]).map((item) => ({ value: item, label: ROLE_LABEL[item] }))} />
          </Form.Item>
          <Form.Item name="scopes" label="权限范围">
            <Select mode="multiple" placeholder="选择 scope" options={SCOPE_CATALOG.map((scope) => ({ value: scope.key, label: `${scope.label}（${scope.key}）` }))} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={assetModal}
        title={
          <OperationsDetailHeader
            eyebrow="站点与资产拓扑"
            title="新增资产节点"
            subtitle="选择父节点、资源类型和名称，提交后进入结构变更确认。"
          />
        }
        width={620}
        className="ops-detail-modal system-governance-modal"
        onCancel={() => setAssetModal(false)}
        footer={
          <OperationsActionFooter note="结构变更当前仅写入 mock 资产树，生产环境必须同步审计日志。">
            <Button onClick={() => setAssetModal(false)}>取消</Button>
            <Button type="primary" onClick={submitAsset}>审阅并确认</Button>
          </OperationsActionFooter>
        }
        destroyOnHidden
      >
        <div className="system-modal-note">
          <ClusterOutlined />
          <span>资产节点会影响遥测归属、诊断对象和权限范围。提交后还会进行二次确认。</span>
        </div>
        <Form form={assetForm} layout="vertical">
          <Form.Item name="parent" label="父节点" rules={[{ required: true }]}>
            <Select options={[{ value: 'root', label: '根（顶级建筑）' }, ...flatNodes.map((node) => ({ value: node.key, label: node.title }))]} />
          </Form.Item>
          <Form.Item name="type" label="节点类型" rules={[{ required: true }]}>
            <Select options={[{ value: 'building', label: '建筑' }, { value: 'zone', label: '分区' }, { value: 'unit', label: '机组/设备' }]} />
          </Form.Item>
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
            <Input placeholder="如 冷水机组 #3" />
          </Form.Item>
        </Form>
      </Modal>
    </PageScaffold>
  );
}
