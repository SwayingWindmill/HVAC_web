import { useMemo, useState } from 'react';
import { useEffect } from 'react';
import { useLocation, useNavigate } from 'react-router';
import {
  Alert,
  Badge,
  Card,
  Col,
  Descriptions,
  Empty,
  Input,
  Row,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ApiOutlined,
  ApartmentOutlined,
  AuditOutlined,
  LockOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  UserOutlined,
  WifiOutlined,
} from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsMetrics,
  OperationsPanelHeading,
  OperationsSectionIntro,
} from '@/components/OperationsUI';
import { FocusHeading } from './FocusHeading';
import type { ShellSnapshot } from './shell-runtime';

interface RealSystemManagementProps {
  snapshot: ShellSnapshot;
}

type PrincipalRow = {
  key: string;
  subject: string;
  displayName: string;
  roles: readonly string[];
  tenantId: string;
  policyRevision: string;
};

type SiteRow = {
  key: string;
  displayName: string;
  code: string;
  timezone: string;
  status: string;
  revision: number;
};

const EMPTY_COLUMNS: ColumnsType<{ key: string }> = [];

function EmptyGovernanceTable({ description }: { description: string }) {
  return (
    <Table<{ key: string }>
      rowKey="key"
      columns={EMPTY_COLUMNS}
      dataSource={[]}
      pagination={false}
      locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={description} /> }}
    />
  );
}

export function RealSystemManagement({ snapshot }: RealSystemManagementProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const principal = snapshot.principal!;
  const platform = snapshot.platform?.status;
  const sites = snapshot.sites?.items ?? [];
  const [activeTab, setActiveTab] = useState(() => new URLSearchParams(location.search).get('tab') ?? 'overview');
  useEffect(() => {
    setActiveTab(new URLSearchParams(location.search).get('tab') ?? 'overview');
  }, [location.search]);
  const principalRows = useMemo<PrincipalRow[]>(() => [{
    key: principal.principal.subject,
    subject: principal.principal.subject,
    displayName: principal.principal.displayName,
    roles: principal.principal.roles,
    tenantId: principal.context.tenantId,
    policyRevision: principal.authorization.policyRevision,
  }], [principal]);
  const siteRows = useMemo<SiteRow[]>(() => sites.map((site) => ({
    key: site.id,
    displayName: site.displayName,
    code: site.code,
    timezone: site.timezone,
    status: site.status,
    revision: site.revision,
  })), [sites]);
  const principalColumns: ColumnsType<PrincipalRow> = [
    {
      title: '用户',
      dataIndex: 'displayName',
      width: 180,
      render: (value: string, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{value}</Typography.Text>
          <Typography.Text type="secondary" copyable={{ text: row.subject }} style={{ fontSize: 12 }}>{row.subject}</Typography.Text>
        </Space>
      ),
    },
    { title: '角色', dataIndex: 'roles', width: 180, render: (roles: readonly string[]) => <Space wrap>{roles.map((role) => <Tag key={role}>{role}</Tag>)}</Space> },
    { title: 'Tenant', dataIndex: 'tenantId', render: (value: string) => <Typography.Text code copyable>{value}</Typography.Text> },
    { title: 'Policy Revision', dataIndex: 'policyRevision', width: 180, render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
    { title: '状态', width: 100, render: () => <Badge status="success" text="当前会话" /> },
  ];
  const siteColumns: ColumnsType<SiteRow> = [
    { title: '站点', dataIndex: 'displayName', render: (value: string, row) => <Space direction="vertical" size={0}><Typography.Text strong>{value}</Typography.Text><Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.code}</Typography.Text></Space> },
    { title: '时区', dataIndex: 'timezone', width: 180 },
    { title: '状态', dataIndex: 'status', width: 110, render: (value: string) => <Tag color={value === 'ACTIVE' ? 'green' : 'default'}>{value}</Tag> },
    { title: 'Revision', dataIndex: 'revision', width: 100 },
  ];

  const overview = (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <OperationsSectionIntro title="治理概览" icon={<SafetyCertificateOutlined />} meta="真实会话与平台状态" />
      <OperationsMetrics items={[
        { label: '授权用户', value: 1, detail: principal.principal.displayName, icon: <UserOutlined />, tone: 'accent' },
        { label: '授权站点', value: sites.length, detail: snapshot.sites?.state ?? 'checking', icon: <ApartmentOutlined />, tone: sites.length ? 'positive' : 'warning' },
        { label: 'Capabilities', value: principal.authorization.capabilities.length, detail: principal.authorization.policyRevision, icon: <LockOutlined /> },
        { label: '平台状态', value: platform?.status ?? snapshot.platform?.state ?? 'checking', detail: platform?.version ?? '等待状态响应', icon: <WifiOutlined />, tone: platform?.status === 'ok' ? 'positive' : 'warning' },
      ]} />
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={14}>
          <Card title="Platform Gateway" variant="borderless">
            <Descriptions column={{ xs: 1, sm: 2 }} bordered size="small">
              <Descriptions.Item label="Service">{platform?.service ?? '未提供'}</Descriptions.Item>
              <Descriptions.Item label="Status">{platform?.status ?? snapshot.platform?.state ?? 'checking'}</Descriptions.Item>
              <Descriptions.Item label="Version">{platform?.version ?? '未提供'}</Descriptions.Item>
              <Descriptions.Item label="Implementation">{platform?.implementation ?? '未提供'}</Descriptions.Item>
              <Descriptions.Item label="Route Policy">{platform?.routePolicyRevision ?? '未提供'}</Descriptions.Item>
              <Descriptions.Item label="Compatibility">{platform?.compatibilityMode ?? '未提供'}</Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
        <Col xs={24} xl={10}>
          <Card title="当前 Principal" variant="borderless">
            <Descriptions column={1} bordered size="small">
              <Descriptions.Item label="Display Name">{principal.principal.displayName}</Descriptions.Item>
              <Descriptions.Item label="Subject"><Typography.Text copyable>{principal.principal.subject}</Typography.Text></Descriptions.Item>
              <Descriptions.Item label="Roles">{principal.principal.roles.join('、') || '无'}</Descriptions.Item>
              <Descriptions.Item label="Tenant"><Typography.Text copyable>{principal.context.tenantId}</Typography.Text></Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
      </Row>
    </Space>
  );

  const users = (
    <Card variant="borderless" styles={{ body: { padding: 16 } }}>
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <div className="ops-toolbar">
          <OperationsPanelHeading icon={<UserOutlined />} title="用户与角色" meta={`${principalRows.length} 个当前可验证用户`} />
          <Space wrap>
            <Input.Search disabled placeholder="搜索用户或邮箱" style={{ width: 260 }} />
            <Select disabled value="all" options={[{ label: '全部角色', value: 'all' }]} style={{ width: 140 }} />
          </Space>
        </div>
        <Alert type="info" showIcon message="Real 模式只展示服务器确认的当前 Principal" description="用户目录、创建、禁用与角色变更接口尚未接入，因此不会显示 Demo 用户或提供本地写操作。" />
        <Table rowKey="key" columns={principalColumns} dataSource={principalRows} pagination={false} scroll={{ x: 900 }} />
      </Space>
    </Card>
  );

  const siteTab = (
    <Card variant="borderless" styles={{ body: { padding: 16 } }}>
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <OperationsPanelHeading icon={<ApartmentOutlined />} title="站点与租户" meta={`${siteRows.length} 个授权站点`} />
        <Table rowKey="key" columns={siteColumns} dataSource={siteRows} pagination={false} locale={{ emptyText: <Empty description="当前 Tenant 没有授权站点" /> }} />
      </Space>
    </Card>
  );

  const integrations = (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Alert type="info" showIcon message="数据接入状态来自当前部署" description="不会加载 Demo 中预设的 REST、WebSocket、Provider 或 AI 数据源状态。" />
      <Card variant="borderless" title={<OperationsPanelHeading icon={<ApiOutlined />} title="数据接入" meta="当前可验证端点" />}>
        <Descriptions column={{ xs: 1, sm: 2, xl: 3 }} bordered size="small">
          <Descriptions.Item label="Platform Gateway"><Badge status={platform ? 'success' : 'processing'} text={platform?.status ?? 'checking'} /></Descriptions.Item>
          <Descriptions.Item label="Registry Sites"><Badge status={snapshot.sites?.state === 'available' ? 'success' : 'processing'} text={snapshot.sites?.state ?? 'checking'} /></Descriptions.Item>
          <Descriptions.Item label="Realtime"><Badge status={snapshot.realtime?.state === 'live' ? 'success' : 'processing'} text={snapshot.realtime?.state ?? 'idle'} /></Descriptions.Item>
          <Descriptions.Item label="Policy Revision">{principal.authorization.policyRevision}</Descriptions.Item>
          <Descriptions.Item label="Session"><Typography.Text code>{principal.session.id}</Typography.Text></Descriptions.Item>
          <Descriptions.Item label="Protected Scope">{snapshot.protectedScope?.state ?? 'idle'}</Descriptions.Item>
        </Descriptions>
      </Card>
    </Space>
  );

  const rules = (
    <Card variant="borderless" styles={{ body: { padding: 16 } }}>
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <OperationsPanelHeading icon={<SettingOutlined />} title="报警与自动化规则" meta="0 条权威规则" />
        <Alert type="warning" showIcon message="规则管理接口尚未接入" description="保留 Demo 的规则管理区域，但不会显示或编辑 Demo 规则。" />
        <EmptyGovernanceTable description="当前部署没有可验证的规则目录" />
      </Space>
    </Card>
  );

  const audit = (
    <Card variant="borderless" styles={{ body: { padding: 16 } }}>
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <div className="ops-toolbar">
          <OperationsPanelHeading icon={<AuditOutlined />} title="审计日志" meta="服务器审计查询待接入" />
          <Space wrap>
            <Input.Search disabled placeholder="搜索操作人、动作或目标" style={{ width: 280 }} />
            <Select disabled value="all" options={[{ label: '全部事件', value: 'all' }]} style={{ width: 140 }} />
            <Select disabled value="all" options={[{ label: '全部结果', value: 'all' }]} style={{ width: 120 }} />
          </Space>
        </div>
        <EmptyGovernanceTable description="审计日志接口尚未接入；未使用 Demo 审计记录替代" />
      </Space>
    </Card>
  );

  const items = [
    { key: 'overview', label: '治理概览', children: overview },
    { key: 'users', label: '用户与角色', children: users },
    { key: 'site', label: '站点与租户', children: siteTab },
    { key: 'integrations', label: '数据接入', children: integrations },
    { key: 'rules', label: '报警规则', children: rules },
    { key: 'audit', label: '审计日志', children: audit },
  ];

  return (
    <section data-testid="real-route-system" data-route-state="READY" data-business-state="POPULATED">
      <PageScaffold
        title="系统管理"
        heading={<FocusHeading id="real-system-title" className="ops-page-title ant-typography"><Space><SettingOutlined />系统管理</Space></FocusHeading>}
        extra={<Tag color="processing">REAL / AUTHORITATIVE</Tag>}
      >
        <Tabs
          activeKey={items.some((item) => item.key === activeTab) ? activeTab : 'overview'}
          items={items}
          onChange={(key) => {
            setActiveTab(key);
            const parameters = new URLSearchParams(location.search);
            parameters.set('tab', key);
            navigate(`${location.pathname}?${parameters.toString()}${location.hash}`, { replace: true });
          }}
        />
      </PageScaffold>
    </section>
  );
}
