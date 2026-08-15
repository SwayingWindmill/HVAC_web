import { useEffect, useMemo, useState } from 'react';
import {
  Badge,
  Button,
  Card,
  Col,
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
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { ProDescriptions, ProForm, ProFormSelect, ProFormText, ProTable, type ProColumns } from '@ant-design/pro-components';
import {
  ApiOutlined,
  ApartmentOutlined,
  AuditOutlined,
  CheckCircleOutlined,
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
import PlatformGatewayStatus from '@/components/PlatformGatewayStatus';
import AuthenticatedPrincipalStatus from '@/components/AuthenticatedPrincipalStatus';
import SessionAuditStatus from '@/components/SessionAuditStatus';
import PlatformRouteStatus from '@/components/PlatformRouteStatus';
import {
  OperationsActionFooter,
  OperationsDetailHeader,
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
  OperationsSectionIntro,
} from '@/components/OperationsUI';
import { can, type PermissionAction, type PermissionSubject } from '@/auth/permissions';
import { API_MODE } from '@/api/config';
import { BRAND, STATUS } from '@/theme/tokens';
import { useUi, type Role } from '@/store/ui';
import {
  mockUsers,
  mockAuditLogs,
  SCOPE_CATALOG,
  ROLE_LABEL,
  ROLE_COLOR,
  AUDIT_EVENT_LABEL,
  AUDIT_EVENT_TYPES,
  type SystemUser,
  type BackendRole,
  type AuditLog,
  type AuditResult,
} from '@/mock/system';
import RegistrySitePanel from './RegistrySitePanel';
import './System.css';

const { Text } = Typography;

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

type UserFormValues = {
  username?: string;
  email?: string;
  password?: string;
  role: BackendRole;
  scopes?: string[];
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

export default function System() {
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const { role } = useUi();
  const [activeTab, setActiveTab] = useState(INIT_TAB);
  const [users, setUsers] = useState<SystemUser[]>(mockUsers);

  const [userModal, setUserModal] = useState<null | { mode: 'create' } | { mode: 'edit'; user: SystemUser }>(null);
  const [userForm] = Form.useForm<UserFormValues>();

  useEffect(() => {
    const closeUserModalOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setUserModal(null);
    };
    window.addEventListener('keydown', closeUserModalOnEscape, true);
    return () => window.removeEventListener('keydown', closeUserModalOnEscape, true);
  }, []);

  const [evFilter, setEvFilter] = useState<string>('all');
  const [resFilter, setResFilter] = useState<AuditResult | 'all'>('all');
  const [kw, setKw] = useState('');

  const activeUsers = users.filter((user) => user.status === 'active').length;
  const adminUsers = users.filter((user) => user.role === 'ADMIN').length;
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
            setUsers((list) => list.map((item) => (item.id === userModal.user.id ? { ...item, role: values.role, scopes: values.scopes ?? [] } : item)));
            message.success('角色已更新（mock）');
          } else {
            const newUser: SystemUser = {
              id: `u${++nodeSeq}`,
              username: values.username!,
              email: values.email!,
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

  const userColumns: ProColumns<SystemUser>[] = [
    { title: '用户名', dataIndex: 'username', width: 130, render: (value) => <Text strong>{value}</Text> },
    { title: '邮箱', dataIndex: 'email', width: 190, render: (value) => <Text type="secondary">{value}</Text> },
    {
      title: '角色', dataIndex: 'role', width: 120,
      render: (_, user) => <Tag color={ROLE_COLOR[user.role]} style={{ fontWeight: 600 }}>{ROLE_LABEL[user.role]}</Tag>,
    },
    {
      title: '权限范围', dataIndex: 'scopes',
      render: (_, user) => {
        const shown = user.scopes.slice(0, 3);
        const rest = user.scopes.slice(3);
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
      render: (_, user) => user.status === 'active' ? <Tag color={STATUS.ok}>启用</Tag> : <Tag color={STATUS.err}>禁用</Tag>,
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

  const dataSourceColumns: ProColumns<DataSource>[] = [
    { title: '数据源', dataIndex: 'name', width: 220, render: (value, row) => <Space direction="vertical" size={0}><Text strong>{value}</Text><Text type="secondary" style={{ fontSize: 12 }}>{row.type} · {row.owner}</Text></Space> },
    { title: '端点', dataIndex: 'endpoint', render: (value) => <Text code>{value}</Text> },
    { title: '状态', dataIndex: 'status', width: 100, render: (_, source) => <Badge color={statusMeta[source.status].color} text={statusMeta[source.status].label} /> },
    { title: '延迟', dataIndex: 'latencyMs', width: 90, render: (value) => `${value} ms` },
    { title: '最后同步', dataIndex: 'lastSync', width: 110, render: (value) => <Text type="secondary">{value}</Text> },
  ];

  const ruleColumns: ProColumns<AlarmRule>[] = [
    { title: '规则', dataIndex: 'name', width: 220, render: (value, row) => <Space direction="vertical" size={0}><Text strong>{value}</Text><Text type="secondary" style={{ fontSize: 12 }}>{row.id} · {row.target}</Text></Space> },
    { title: '条件', dataIndex: 'condition', render: (value) => <Text>{value}</Text> },
    { title: '级别', dataIndex: 'severity', width: 100, render: (value) => <Tag color={value === 'critical' ? 'red' : value === 'major' ? 'gold' : 'blue'}>{value}</Tag> },
    { title: '通知', dataIndex: 'notify', width: 180, render: (_, rule) => <Space size={4} wrap>{rule.notify.map((item) => <Tag key={item}>{item}</Tag>)}</Space> },
    { title: '状态', dataIndex: 'status', width: 90, render: (_, rule) => <Tag color={ruleStatusMeta[rule.status].color}>{ruleStatusMeta[rule.status].label}</Tag> },
    { title: '更新时间', dataIndex: 'updatedAt', width: 110, render: (value) => <Text type="secondary">{value}</Text> },
  ];

  const filteredAudit = useMemo(() => {
    return mockAuditLogs.filter((item) => {
      if (evFilter !== 'all' && item.eventType !== evFilter) return false;
      if (resFilter !== 'all' && item.result !== resFilter) return false;
      if (kw && !(item.userId.includes(kw) || item.targetId.includes(kw) || item.action.includes(kw))) return false;
      return true;
    });
  }, [evFilter, resFilter, kw]);

  const auditColumns: ProColumns<AuditLog>[] = [
    { title: '时间', dataIndex: 'createdAt', width: 160, render: (_, log) => <Text style={{ fontSize: 12, fontFamily: 'monospace' }}>{new Date(log.createdAt).toLocaleString('zh-CN')}</Text> },
    { title: '操作人', dataIndex: 'userId', width: 100, render: (value) => <Text strong>{value}</Text> },
    { title: '事件类型', dataIndex: 'eventType', width: 110, render: (_, log) => <Tag color={BRAND.tealStrong}>{AUDIT_EVENT_LABEL[log.eventType] ?? log.eventType}</Tag> },
    { title: '结果', dataIndex: 'result', width: 90, render: (_, log) => log.result === 'SUCCESS' ? <Tag color={STATUS.ok}>成功</Tag> : <Tag color={STATUS.err}>失败</Tag> },
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
        meta="只读概览"
      />

      <OperationsMetrics
        items={[
          { label: '启用用户', value: activeUsers, suffix: `/ ${users.length}`, detail: '当前可登录用户', icon: <UserOutlined />, tone: 'accent' },
          { label: '管理员', value: adminUsers, detail: '拥有系统治理权限', icon: <SafetyCertificateOutlined /> },
          { label: 'Registry', value: 'S1', detail: 'Organization / Site / Equipment / Device', icon: <ApartmentOutlined /> },
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
            title={<OperationsPanelHeading title="服务健康" icon={<WifiOutlined />} meta={`1 个平台入口 · ${DATA_SOURCES.length} 个数据源`} />}
            variant="borderless"
          >
            <div className="system-health-list">
              <PlatformGatewayStatus />
              <AuthenticatedPrincipalStatus />
              <SessionAuditStatus />
              <PlatformRouteStatus />
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
        meta={`${activeUsers} 个启用账户`}
        actions={<Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建用户</Button>}
      />

      <Row gutter={[16, 16]} className="system-equal-row">
        <Col xs={24} xl={16}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading title="用户账户" meta={`${users.length} 个账户`} />}
          >
            <ProTable<SystemUser>
              rowKey="id"
              size="small"
              columns={visibleUserColumns}
              dataSource={users}
              search={false}
              options={{ density: true, fullScreen: true, setting: true, reload: false }}
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

  const siteTab = <RegistrySitePanel />;

  const integrationsTab = (
    <div className="system-tab-stack">
      <OperationsSectionIntro
        title="接口与数据源"
        icon={<ApiOutlined />}
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
        <ProTable<DataSource>
          rowKey="key"
          size="small"
          columns={visibleDataSourceColumns}
          dataSource={DATA_SOURCES}
          search={false}
          options={{ density: true, fullScreen: true, setting: true, reload: false }}
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
        <ProTable<AlarmRule>
          rowKey="id"
          size="small"
          columns={visibleRuleColumns}
          dataSource={ALARM_RULES}
          search={false}
          options={{ density: true, fullScreen: true, setting: true, reload: false }}
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
        </div>

        <ProTable<AuditLog>
          rowKey="id"
          size="small"
          columns={visibleAuditColumns}
          dataSource={filteredAudit}
          search={false}
          options={{ density: true, fullScreen: true, setting: true, reload: false }}
          pagination={{ pageSize: 8, showSizeChanger: false }}
          scroll={{ x: compactTable ? 760 : 1040 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的审计记录" /> }}
          expandable={{
            expandedRowRender: (row) => (
              <ProDescriptions<AuditLog>
                size="small"
                column={1}
                className="system-audit-detail"
                dataSource={row}
                columns={[
                  { title: 'traceId', dataIndex: 'traceId', render: (_, record) => <Text code>{record.traceId}</Text> },
                  { title: 'userAgent', dataIndex: 'userAgent' },
                  { title: 'details (jsonb)', dataIndex: 'details', render: (_, record) => <pre>{JSON.stringify(record.details, null, 2)}</pre> },
                ]}
              />
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
      extra={
        <Space size={8} wrap>
          <Tag icon={<SafetyCertificateOutlined />}>当前角色：{ROLE_LABEL[ROLE_MAP[role]]}</Tag>
          <Tag color={API_MODE === 'real' ? 'blue' : 'gold'}>
            {API_MODE === 'real' ? 'Registry 只读 · real' : 'Mock 写入 · 二次确认'}
          </Tag>
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
        forceRender
      >
        <div className="system-modal-note">
          <SafetyCertificateOutlined />
          <span>{userModal?.mode === 'edit' ? `正在修改 ${userModal.user.username} 的角色与 scope。提交后还会进行二次确认。` : '账户创建会授予登录能力与权限范围，提交后还会进行二次确认。'}</span>
        </div>
        <ProForm<UserFormValues> form={userForm} layout="vertical" submitter={false}>
          {userModal?.mode === 'create' && (
            <>
              <ProFormText name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]} placeholder="如 zhaomin" />
              <ProFormText name="email" label="邮箱" rules={[{ required: true, type: 'email', message: '请输入合法邮箱' }]} placeholder="name@corp.io" />
              <ProFormText
                name="password"
                label="初始密码"
                rules={[{ required: true, min: 6, message: '至少 6 位' }]}
                placeholder="初始密码"
                fieldProps={{ type: 'password', autoComplete: 'new-password' }}
              />
            </>
          )}
          <ProFormSelect
            name="role"
            label="角色"
            rules={[{ required: true }]}
            options={(['ADMIN', 'MAINTENANCE', 'READONLY'] as BackendRole[]).map((item) => ({ value: item, label: ROLE_LABEL[item] }))}
          />
          <ProFormSelect
            name="scopes"
            label="权限范围"
            mode="multiple"
            placeholder="选择 scope"
            options={SCOPE_CATALOG.map((scope) => ({ value: scope.key, label: `${scope.label}（${scope.key}）` }))}
          />
        </ProForm>
      </Modal>
    </PageScaffold>
  );
}
