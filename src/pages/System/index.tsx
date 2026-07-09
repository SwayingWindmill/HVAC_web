import { useMemo, useState } from 'react';
import {
  Card, Tabs, Table, Tree, Modal, Form, Input, Select, Tag, Space, Button,
  Popconfirm, Typography, Descriptions, Tooltip, message, Row, Col,
} from 'antd';
import type { DataNode } from 'antd/es/tree';
import type { ColumnsType } from 'antd/es/table';
import {
  PlusOutlined, EditOutlined, StopOutlined, ApartmentOutlined,
  BlockOutlined, ClusterOutlined, SafetyCertificateOutlined,
  AuditOutlined, UserOutlined,
} from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import { BRAND, STATUS } from '@/theme/tokens';
import {
  mockUsers, mockSites, mockAssetTree, mockAuditLogs,
  SCOPE_CATALOG, ROLE_LABEL, ROLE_COLOR, AUDIT_EVENT_LABEL, AUDIT_EVENT_TYPES,
  type SystemUser, type BackendRole, type AssetNode, type AuditLog, type AuditResult,
} from '@/mock/system';

const { Text, Paragraph } = Typography;

/* ---------- tree mutation helper (immutable) ---------- */
function insertNode(nodes: AssetNode[], parentKey: string | null, node: AssetNode): AssetNode[] {
  if (parentKey === null) return [...nodes, node];
  return nodes.map((n) => {
    if (n.key === parentKey) return { ...n, children: [...(n.children ?? []), node] };
    if (n.children) return { ...n, children: insertNode(n.children, parentKey, node) };
    return n;
  });
}

let nodeSeq = 100;

// 允许通过 ?tab=users|site|audit 深链到指定标签页（演示/分享用）
const INIT_TAB = (() => {
  if (typeof window === 'undefined') return 'users';
  const t = new URLSearchParams(window.location.search).get('tab');
  return t === 'site' || t === 'audit' ? t : 'users';
})();

export default function System() {
  const [users, setUsers] = useState<SystemUser[]>(mockUsers);
  const [tree, setTree] = useState<AssetNode[]>(mockAssetTree);
  const [siteId, setSiteId] = useState<string>(mockSites[0].id);

  // user modal
  const [userModal, setUserModal] = useState<null | { mode: 'create' } | { mode: 'edit'; user: SystemUser }>(null);
  const [userForm] = Form.useForm();

  // asset modal
  const [assetModal, setAssetModal] = useState(false);
  const [assetForm] = Form.useForm();

  // audit filters
  const [evFilter, setEvFilter] = useState<string>('all');
  const [resFilter, setResFilter] = useState<AuditResult | 'all'>('all');
  const [kw, setKw] = useState('');

  /* ===================== Tab 1: 用户与角色 ===================== */
  const openCreate = () => {
    userForm.resetFields();
    userForm.setFieldsValue({ role: 'READONLY', scopes: ['asset:read', 'device:read', 'telemetry:read'] });
    setUserModal({ mode: 'create' });
  };
  const openEdit = (u: SystemUser) => {
    userForm.resetFields();
    userForm.setFieldsValue({ role: u.role, scopes: u.scopes });
    setUserModal({ mode: 'edit', user: u });
  };
  const submitUser = () => {
    userForm.validateFields().then((v) => {
      const summary = userModal?.mode === 'edit'
        ? `确认将用户「${userModal.user.username}」的角色修改为「${ROLE_LABEL[v.role as BackendRole]}」？`
        : `确认创建用户「${v.username}」并授予角色「${ROLE_LABEL[v.role as BackendRole]}」？`;
      Modal.confirm({
        title: '二次确认（人在回路）',
        icon: <SafetyCertificateOutlined style={{ color: BRAND.teal }} />,
        content: summary,
        okText: '确认提交',
        cancelText: '取消',
        onOk: () => {
          if (userModal?.mode === 'edit') {
            setUsers((list) => list.map((x) => (x.id === userModal.user.id ? { ...x, role: v.role, scopes: v.scopes } : x)));
            message.success('角色已更新（mock）');
          } else {
            const nu: SystemUser = {
              id: 'u' + ++nodeSeq,
              username: v.username,
              email: v.email,
              role: v.role,
              scopes: v.scopes ?? [],
              status: 'active',
              lastLogin: '—',
            };
            setUsers((list) => [nu, ...list]);
            message.success('用户已创建（mock）');
          }
          setUserModal(null);
        },
      });
    });
  };
  const toggleStatus = (u: SystemUser) => {
    setUsers((list) => list.map((x) => (x.id === u.id ? { ...x, status: x.status === 'active' ? 'disabled' : 'active' } : x)));
    message.success(u.status === 'active' ? '已禁用（mock）' : '已启用（mock）');
  };

  const userColumns: ColumnsType<SystemUser> = [
    { title: '用户名', dataIndex: 'username', render: (t) => <Text strong>{t}</Text> },
    { title: '邮箱', dataIndex: 'email', render: (t) => <Text type="secondary">{t}</Text> },
    {
      title: '角色', dataIndex: 'role',
      render: (r: BackendRole) => <Tag color={ROLE_COLOR[r]} style={{ fontWeight: 600 }}>{ROLE_LABEL[r]}</Tag>,
    },
    {
      title: '权限范围', dataIndex: 'scopes',
      render: (sc: string[]) => {
        const shown = sc.slice(0, 3);
        const rest = sc.slice(3);
        return (
          <Space size={4} wrap>
            {shown.map((k) => {
              const d = SCOPE_CATALOG.find((s) => s.key === k);
              return <Tag key={k} bordered={false}>{d?.label ?? k}</Tag>;
            })}
            {rest.length > 0 && (
              <Tooltip title={rest.map((k) => SCOPE_CATALOG.find((s) => s.key === k)?.label ?? k).join('、')}>
                <Tag bordered={false}>+{rest.length}</Tag>
              </Tooltip>
            )}
          </Space>
        );
      },
    },
    {
      title: '状态', dataIndex: 'status',
      render: (s: SystemUser['status']) =>
        s === 'active' ? <Tag color={STATUS.ok}>启用</Tag> : <Tag color={STATUS.err}>禁用</Tag>,
    },
    { title: '最近登录', dataIndex: 'lastLogin', render: (t) => <Text type="secondary" style={{ fontSize: 12 }}>{t}</Text> },
    {
      title: '操作', key: 'op',
      render: (_, u) => (
        <Space size={4}>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(u)}>改角色</Button>
          <Popconfirm title={u.status === 'active' ? '确认禁用该用户？' : '确认启用该用户？'} okText="确认" cancelText="取消" onConfirm={() => toggleStatus(u)}>
            <Button size="small" icon={<StopOutlined />} danger={u.status === 'active'}>{u.status === 'active' ? '禁用' : '启用'}</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const usersTab = (
    <Row gutter={[16, 16]} align="top">
      <Col xs={24} lg={16}>
      <Card
        title={<Space><UserOutlined style={{ color: BRAND.teal }} />用户列表</Space>}
        extra={<Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建用户</Button>}
      >
        <Table rowKey="id" size="small" columns={userColumns} dataSource={users} pagination={{ pageSize: 6 }} />
      </Card>
      </Col>
      <Col xs={24} lg={8}>
      <Card title={<Space><SafetyCertificateOutlined style={{ color: BRAND.teal }} />权限范围目录</Space>} size="small">
        <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 8 }}>
          9 个硬编码 scope（来自后端 auth-scopes.constants）。legacy 模式下仅 role 生效，scope 仅对 Logto 用户校验。
        </Paragraph>
        <Space direction="vertical" size={6} style={{ width: '100%' }}>
          {SCOPE_CATALOG.map((s) => (
            <div key={s.key} style={{ borderLeft: `3px solid ${BRAND.teal}`, paddingLeft: 8 }}>
              <Text strong style={{ fontSize: 13 }}>{s.label}</Text>
              <div><Text type="secondary" style={{ fontSize: 12 }}>{s.key}</Text></div>
              <div><Text type="secondary" style={{ fontSize: 12 }}>{s.desc}</Text></div>
            </div>
          ))}
        </Space>
      </Card>
      </Col>
    </Row>
  );

  /* ===================== Tab 2: 站点与资产 ===================== */
  const openAddAsset = () => {
    assetForm.resetFields();
    assetForm.setFieldsValue({ parent: 'root', type: 'building' });
    setAssetModal(true);
  };
  const submitAsset = () => {
    assetForm.validateFields().then((v) => {
      Modal.confirm({
        title: '二次确认（人在回路）',
        icon: <SafetyCertificateOutlined style={{ color: BRAND.teal }} />,
        content: `确认在「${v.parent === 'root' ? '根' : v.parent}」下新增${v.type === 'building' ? '建筑' : v.type === 'zone' ? '分区' : '机组'}「${v.name}」？`,
        okText: '确认提交',
        cancelText: '取消',
        onOk: () => {
          const parentKey = v.parent === 'root' ? null : v.parent;
          const node: AssetNode = { key: 'n' + ++nodeSeq, title: v.name, type: v.type };
          setTree((t) => insertNode(t, parentKey, node));
          message.success('节点已新增（mock）');
          setAssetModal(false);
        },
      });
    });
  };

  const treeData = useMemo(() => {
    const icon = (type: AssetNode['type']) =>
      type === 'building' ? <ApartmentOutlined style={{ color: BRAND.teal }} /> :
      type === 'zone' ? <ClusterOutlined style={{ color: STATUS.info }} /> :
      <BlockOutlined style={{ color: STATUS.warn }} />;
    const walk = (ns: AssetNode[]): DataNode[] => ns.map((n) => ({
      key: n.key,
      title: n.title,
      icon: icon(n.type),
      children: n.children ? walk(n.children) : undefined,
    }));
    return walk(tree);
  }, [tree]);

  // flatten for parent select
  const flatNodes = useMemo(() => {
    const out: { key: string; title: string }[] = [];
    const walk = (ns: AssetNode[], depth: number) => ns.forEach((n) => {
      out.push({ key: n.key, title: '　'.repeat(depth) + n.title });
      if (n.children) walk(n.children, depth + 1);
    });
    walk(tree, 0);
    return out;
  }, [tree]);

  const siteTab = (
    <Card
      title={<Space><ApartmentOutlined style={{ color: BRAND.teal }} />站点与资产结构</Space>}
      extra={
        <Space>
          <Select value={siteId} style={{ width: 180 }} onChange={setSiteId}
            options={mockSites.map((s) => ({ value: s.id, label: s.name }))} />
          <Button type="primary" icon={<PlusOutlined />} onClick={openAddAsset}>新增节点</Button>
        </Space>
      }
    >
      <Paragraph type="secondary" style={{ fontSize: 12 }}>
        资产层级由 ThingsBoard 资产树承载。v1 以 mock 树呈现（形状对齐 GET /assets/tree），写操作 mock。
      </Paragraph>
      <Tree showIcon defaultExpandAll treeData={treeData} style={{ background: 'transparent' }} />
    </Card>
  );

  /* ===================== Tab 3: 操作审计 ===================== */
  const filteredAudit = useMemo(() => {
    return mockAuditLogs.filter((a) => {
      if (evFilter !== 'all' && a.eventType !== evFilter) return false;
      if (resFilter !== 'all' && a.result !== resFilter) return false;
      if (kw && !(a.userId.includes(kw) || a.targetId.includes(kw) || a.action.includes(kw))) return false;
      return true;
    });
  }, [evFilter, resFilter, kw]);

  const auditColumns: ColumnsType<AuditLog> = [
    {
      title: '时间', dataIndex: 'createdAt', width: 160,
      render: (t: string) => <Text style={{ fontSize: 12, fontFamily: 'monospace' }}>{new Date(t).toLocaleString('zh-CN')}</Text>,
    },
    { title: '操作人', dataIndex: 'userId', width: 100, render: (t) => <Text strong>{t}</Text> },
    {
      title: '事件类型', dataIndex: 'eventType', width: 110,
      render: (t: string) => <Tag color={BRAND.tealStrong}>{AUDIT_EVENT_LABEL[t] ?? t}</Tag>,
    },
    {
      title: '结果', dataIndex: 'result', width: 90,
      render: (r: AuditResult) => r === 'SUCCESS'
        ? <Tag color={STATUS.ok}>成功</Tag>
        : <Tag color={STATUS.err}>失败</Tag>,
    },
    { title: '动作', dataIndex: 'action', render: (t) => <Text>{t}</Text> },
    {
      title: '目标', render: (_, r) => <Text type="secondary" style={{ fontSize: 12 }}>{r.targetEntity}#{r.targetId}</Text>,
    },
    { title: 'IP', dataIndex: 'ipAddress', width: 120, render: (t) => <Text type="secondary" style={{ fontSize: 12, fontFamily: 'monospace' }}>{t}</Text> },
    {
      title: '详情', key: 'details',
      render: (_, r) => Object.keys(r.details).length
        ? (
          <Tooltip title={<pre style={{ margin: 0, fontSize: 12 }}>{JSON.stringify(r.details, null, 2)}</pre>}>
            <Tag bordered={false}>查看</Tag>
          </Tooltip>
        )
        : <Text type="secondary">—</Text>,
    },
  ];

  const auditTab = (
    <Card title={<Space><AuditOutlined style={{ color: BRAND.teal }} />操作审计日志</Space>}
      extra={
        <Space wrap>
          <Select value={evFilter} style={{ width: 140 }} onChange={setEvFilter}
            options={[{ value: 'all', label: '全部事件' }, ...AUDIT_EVENT_TYPES.map((e) => ({ value: e, label: AUDIT_EVENT_LABEL[e] ?? e }))]} />
          <Select value={resFilter} style={{ width: 110 }} onChange={setResFilter}
            options={[{ value: 'all', label: '全部结果' }, { value: 'SUCCESS', label: '成功' }, { value: 'FAILURE', label: '失败' }]} />
          <Input.Search placeholder="操作人 / 目标 / 动作" allowClear onChange={(e) => setKw(e.target.value)} style={{ width: 200 }} />
        </Space>
      }
    >
      <Paragraph type="secondary" style={{ fontSize: 12 }}>
        审计写入已由后端 AuditLoggerService 自动落库，但读取端点（GET /audit）待补。v1 列表为 mock，形状对齐真实 audit_logs 实体。
      </Paragraph>
      <Table rowKey="id" size="small" columns={auditColumns} dataSource={filteredAudit}
        pagination={{ pageSize: 8 }} expandable={{
          expandedRowRender: (r) => (
            <Descriptions size="small" column={1} bordered>
              <Descriptions.Item label="traceId">{r.traceId}</Descriptions.Item>
              <Descriptions.Item label="userAgent">{r.userAgent}</Descriptions.Item>
              <Descriptions.Item label="details(jsonb)"><pre style={{ margin: 0, fontSize: 12 }}>{JSON.stringify(r.details, null, 2)}</pre></Descriptions.Item>
            </Descriptions>
          ),
        }} />
    </Card>
  );

  /* ===================== render ===================== */
  return (
    <PageScaffold
      title="系统管理"
      subtitle="RBAC 用户/角色 · 站点与资产 · 操作审计（v1）"
      extra={<Tag color="default" icon={<SafetyCertificateOutlined />}>v1 演示 · 写操作 mock</Tag>}
    >
      <Tabs
        defaultActiveKey={INIT_TAB}
        items={[
          { key: 'users', label: <span><UserOutlined /> 用户与角色</span>, children: usersTab },
          { key: 'site', label: <span><ApartmentOutlined /> 站点与资产</span>, children: siteTab },
          { key: 'audit', label: <span><AuditOutlined /> 操作审计</span>, children: auditTab },
        ]}
      />

      {/* 新建/改角色 弹窗 */}
      <Modal
        open={!!userModal} title={userModal?.mode === 'edit' ? '修改用户角色' : '新建用户'}
        okText="下一步" cancelText="取消" onCancel={() => setUserModal(null)} onOk={submitUser}
        destroyOnClose
      >
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
            <Select options={(['ADMIN', 'MAINTENANCE', 'READONLY'] as BackendRole[]).map((r) => ({ value: r, label: ROLE_LABEL[r] }))} />
          </Form.Item>
          <Form.Item name="scopes" label="权限范围">
            <Select mode="multiple" placeholder="选择 scope" options={SCOPE_CATALOG.map((s) => ({ value: s.key, label: `${s.label}（${s.key}）` }))} />
          </Form.Item>
        </Form>
      </Modal>

      {/* 新增资产节点 弹窗 */}
      <Modal
        open={assetModal} title="新增资产节点" okText="下一步" cancelText="取消"
        onCancel={() => setAssetModal(false)} onOk={submitAsset} destroyOnClose
      >
        <Form form={assetForm} layout="vertical">
          <Form.Item name="parent" label="父节点" rules={[{ required: true }]}>
            <Select options={[{ value: 'root', label: '根（顶级建筑）' }, ...flatNodes.map((n) => ({ value: n.key, label: n.title }))]} />
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
