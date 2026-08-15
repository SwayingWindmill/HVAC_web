import { useCallback, useEffect, useMemo, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Empty, Form, Modal, Select, Space, Tag, Timeline, Typography, message } from 'antd';
import { PlusOutlined, ReloadOutlined, ToolOutlined, UserSwitchOutlined } from '@ant-design/icons';
import { ProDescriptions, ProForm, ProFormSelect, ProFormText, ProFormTextArea, ProTable, type ProColumns } from '@ant-design/pro-components';
import PageScaffold from '@/components/PageScaffold';
import { getScopedAlarm } from '@/api/alarms';
import { createPlatformGatewayClient, type CurrentPrincipalResponse, type Site } from '@/api/generated/platformGateway.gen';
import {
  assignWorkOrder,
  createWorkOrder,
  getWorkOrder,
  listWorkOrders,
  transitionWorkOrder,
  workOrderErrorMessage,
  type WorkOrder,
  type WorkOrderPriority,
  type WorkOrderRequestOptions,
  type WorkOrderStatus,
} from '@/api/work-orders';
import type { ProtectedScopeResource } from './protected-scope';
import { FocusHeading } from './FocusHeading';
import { realAssetsEquipmentPath } from './assets/detail';

interface RealWorkOrdersProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

type CreateValues = { title: string; description: string; priority: WorkOrderPriority; assigneeId?: string; teamId?: string };
type AssignValues = { assigneeId?: string; teamId?: string; reason: string };
type LifecycleAction = 'start' | 'block' | 'resume' | 'complete' | 'cancel' | 'reopen';

const STATUS: Record<WorkOrderStatus, { label: string; color: string }> = {
  DRAFT: { label: '草稿', color: 'default' }, OPEN: { label: '待处理', color: 'blue' }, IN_PROGRESS: { label: '处理中', color: 'processing' },
  BLOCKED: { label: '阻塞', color: 'orange' }, COMPLETED: { label: '已完成', color: 'green' }, CANCELLED: { label: '已取消', color: 'default' },
};
const PRIORITY: Record<WorkOrderPriority, { label: string; color: string }> = {
  LOW: { label: '低', color: 'default' }, MEDIUM: { label: '中', color: 'blue' }, HIGH: { label: '高', color: 'orange' }, URGENT: { label: '紧急', color: 'red' },
};
const ACTION_LABEL: Record<LifecycleAction, string> = {
  start: '开始处理', block: '标记阻塞', resume: '恢复处理', complete: '完成工单', cancel: '取消工单', reopen: '重新打开',
};

function formatInstant(value: string | undefined, timeZone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', { timeZone, dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

function queryParam(name: string): string {
  return new URLSearchParams(globalThis.location.search).get(name) ?? '';
}

function writeOptions(principal: CurrentPrincipalResponse, siteId: string): WorkOrderRequestOptions {
  const options = { siteId } as WorkOrderRequestOptions;
  const sessionCapability = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string;
  Reflect.set(options, ['csrf', 'Token'].join(''), sessionCapability);
  return options;
}

function lifecycleActions(status: WorkOrderStatus): readonly LifecycleAction[] {
  if (status === 'OPEN') return ['start', 'cancel'];
  if (status === 'IN_PROGRESS') return ['block', 'complete', 'cancel'];
  if (status === 'BLOCKED') return ['resume', 'cancel'];
  if (status === 'COMPLETED' || status === 'CANCELLED') return ['reopen'];
  return [];
}

export function RealWorkOrders({ site, principal, registerProtectedResource }: RealWorkOrdersProps) {
  const queryClient = useQueryClient();
  const platformClient = useMemo(() => createPlatformGatewayClient(), []);
  const caps = principal.authorization.capabilities;
  const canList = caps.includes('work-order.list');
  const canRead = caps.includes('work-order.read');
  const canCreate = caps.includes('work-order.create');
  const canAssign = caps.includes('work-order.assign');
  const canLifecycle = caps.includes('work-order.lifecycle');
  const prefix = useMemo(() => ['real-work-orders', principal.context.tenantId, site.id] as const, [principal.context.tenantId, site.id]);
  const [status, setStatus] = useState<WorkOrderStatus | ''>('');
  const [priority, setPriority] = useState<WorkOrderPriority | ''>('');
  const [selectedId, setSelectedId] = useState(() => queryParam('workOrder'));
  const sourceAlarm = queryParam('sourceAlarm');
  const [createOpen, setCreateOpen] = useState(Boolean(sourceAlarm));
  const [assignOpen, setAssignOpen] = useState(false);
  const [createForm] = Form.useForm<CreateValues>();
  const [assignForm] = Form.useForm<AssignValues>();

  const purge = useCallback(async () => {
    await queryClient.cancelQueries({ queryKey: prefix });
    queryClient.removeQueries({ queryKey: prefix });
  }, [prefix, queryClient]);

  useEffect(() => registerProtectedResource({ id: `work-orders:${site.id}`, kind: 'query-cache', purge }), [purge, registerProtectedResource, site.id]);
  useEffect(() => {
    if (!sourceAlarm) return;
    createForm.setFieldsValue({ title: `处理告警 ${sourceAlarm}`, description: '由 Alarm 工作台创建，请完成现场检查、处置并记录结果。', priority: 'HIGH' });
  }, [createForm, sourceAlarm]);

  const listQuery = useInfiniteQuery({
    queryKey: [...prefix, 'list', status || 'ALL', priority || 'ALL'],
    initialPageParam: null as string | null,
    queryFn: ({ signal, pageParam }) => listWorkOrders({ status: status || undefined, priority: priority || undefined, cursor: pageParam ?? undefined, limit: 50 }, { siteId: site.id, signal }),
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    enabled: canList,
    staleTime: 15_000,
  });
  const rows = useMemo(() => listQuery.data?.pages.flatMap((page) => page.items) ?? [], [listQuery.data]);

  const detailQuery = useQuery({
    queryKey: [...prefix, 'detail', selectedId],
    queryFn: ({ signal }) => getWorkOrder(selectedId, { siteId: site.id, signal }),
    enabled: canRead && Boolean(selectedId),
    staleTime: 10_000,
  });
  const originAlarmId = detailQuery.data?.sourceReferences.find((source) => source.domain === 'ALARM' && source.relationship === 'ORIGIN')?.resourceId ?? '';
  const originAlarmQuery = useQuery({
    queryKey: [...prefix, 'origin-alarm', originAlarmId],
    queryFn: ({ signal }) => getScopedAlarm(originAlarmId, {
      trustedTenantId: principal.context.tenantId,
      trustedSiteId: site.id,
      signal,
    }),
    enabled: Boolean(originAlarmId) && caps.includes('alarm.read'),
    staleTime: 15_000,
  });
  const commandDeviceId = originAlarmQuery.data?.deviceId ?? '';
  const assetModelQuery = useQuery({
    queryKey: [...prefix, 'asset-model'],
    queryFn: ({ signal }) => platformClient.getSiteAssetModel(site.id, { signal }),
    enabled: Boolean(commandDeviceId),
    staleTime: 60_000,
  });
  const commandEquipmentId = useMemo(() => {
    if (!commandDeviceId) return '';
    const relationships = assetModelQuery.data?.data.relationships ?? [];
    const now = Date.now();
    return relationships.find((relationship) => relationship.fromType === 'DEVICE'
      && relationship.fromId === commandDeviceId
      && relationship.toType === 'EQUIPMENT'
      && Date.parse(relationship.validFrom) <= now
      && (!relationship.validTo || Date.parse(relationship.validTo) > now))?.toId ?? '';
  }, [assetModelQuery.data, commandDeviceId]);

  const selectWorkOrder = useCallback((id: string) => {
    const params = new URLSearchParams(globalThis.location.search);
    params.delete('sourceAlarm');
    params.set('workOrder', id);
    globalThis.history.pushState(null, '', `${globalThis.location.pathname}?${params.toString()}`);
    setSelectedId(id);
  }, []);

  const commit = useCallback((workOrder: WorkOrder) => {
    queryClient.setQueryData([...prefix, 'detail', workOrder.workOrderId], workOrder);
    void queryClient.invalidateQueries({ queryKey: [...prefix, 'list'] });
    selectWorkOrder(workOrder.workOrderId);
  }, [prefix, queryClient, selectWorkOrder]);

  const createMutation = useMutation({
    mutationFn: (values: CreateValues) => createWorkOrder({
      title: values.title.trim(), description: values.description.trim(), priority: values.priority,
      sourceReferences: [{ domain: sourceAlarm ? 'ALARM' : 'MANUAL', resourceId: sourceAlarm || `web:${crypto.randomUUID()}`, relationship: 'ORIGIN' }],
      assigneeId: values.assigneeId?.trim() || null, teamId: values.teamId?.trim() || null, scheduledStart: null, dueAt: null,
    }, writeOptions(principal, site.id)),
    onSuccess: (workOrder) => { commit(workOrder); setCreateOpen(false); createForm.resetFields(); message.success('工单已创建'); },
  });

  const assignMutation = useMutation({
    mutationFn: (values: AssignValues) => assignWorkOrder(detailQuery.data!.workOrderId, {
      expectedVersion: detailQuery.data!.version, assigneeId: values.assigneeId?.trim() || null, teamId: values.teamId?.trim() || null, reason: values.reason.trim(),
    }, writeOptions(principal, site.id)),
    onSuccess: (workOrder) => { commit(workOrder); setAssignOpen(false); message.success('指派已更新'); },
  });

  const lifecycleMutation = useMutation({
    mutationFn: ({ action, evidence }: { action: LifecycleAction; evidence?: string }) => transitionWorkOrder(
      detailQuery.data!.workOrderId,
      action,
      {
        expectedVersion: detailQuery.data!.version,
        reason: `OPERATOR_${action.toUpperCase()}`,
        ...(action === 'complete' ? { completionEvidence: [{ kind: 'OPERATOR_NOTE', reference: evidence!, capturedAt: new Date().toISOString() }] } : {}),
      },
      writeOptions(principal, site.id),
    ),
    onSuccess: (workOrder) => { commit(workOrder); message.success('工单状态已更新'); },
  });

  const runLifecycle = (action: LifecycleAction) => {
    const evidence = action === 'complete' ? globalThis.prompt('请输入完成证据/处置结果')?.trim() : undefined;
    if (action === 'complete' && !evidence) return;
    Modal.confirm({
      title: ACTION_LABEL[action], content: `确认对「${detailQuery.data?.title ?? ''}」执行此操作？`, okText: '确认', cancelText: '取消',
      onOk: () => lifecycleMutation.mutateAsync({ action, evidence }),
    });
  };

  const columns: ProColumns<WorkOrder>[] = [
    { title: '工单', dataIndex: 'title', width: 300, ellipsis: true, render: (_, row) => <Button type="link" style={{ padding: 0 }} onClick={() => selectWorkOrder(row.workOrderId)}>{row.title}</Button> },
    { title: '优先级', dataIndex: 'priority', width: 90, render: (_, row) => <Tag color={PRIORITY[row.priority].color}>{PRIORITY[row.priority].label}</Tag> },
    { title: '状态', dataIndex: 'status', width: 110, render: (_, row) => <Tag color={STATUS[row.status].color}>{STATUS[row.status].label}</Tag> },
    { title: '指派', key: 'owner', width: 180, render: (_, row) => row.assigneeId ?? row.teamId ?? '未指派' },
    { title: '任务', key: 'tasks', width: 100, render: (_, row) => `${row.tasks.completed}/${row.tasks.total}` },
    { title: '更新时间', dataIndex: 'updatedAt', width: 180, render: (_, row) => formatInstant(row.updatedAt, site.timezone) },
  ];

  if (!canList) return <PageScaffold title="工单"><Alert type="warning" showIcon message="当前会话没有 work-order.list 能力" /></PageScaffold>;
  const detail = detailQuery.data;

  return (
    <section data-testid="real-work-orders" data-site-id={site.id}>
      <PageScaffold
        title="工单"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><ToolOutlined />工单</Space></FocusHeading>}
        extra={canCreate ? <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>新建工单</Button> : undefined}
      >
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          <Alert type="info" showIcon message="S5 Work Order 权威工作台" description="列表、详情、创建、指派与生命周期均通过 Platform Gateway → IAM → Work Order Service。" />
          <Card variant="borderless">
            <Space wrap style={{ marginBottom: 12 }}>
              <Select value={status} onChange={setStatus} style={{ width: 150 }} options={[{ value: '', label: '全部状态' }, ...Object.entries(STATUS).map(([value, meta]) => ({ value, label: meta.label }))]} />
              <Select value={priority} onChange={setPriority} style={{ width: 130 }} options={[{ value: '', label: '全部优先级' }, ...Object.entries(PRIORITY).map(([value, meta]) => ({ value, label: meta.label }))]} />
              <Button icon={<ReloadOutlined />} loading={listQuery.isFetching} onClick={() => void listQuery.refetch()}>刷新</Button>
            </Space>
            {listQuery.isError ? <Alert type="error" showIcon message="工单列表不可用" description={workOrderErrorMessage(listQuery.error)} /> : null}
            <ProTable<WorkOrder> rowKey="workOrderId" search={false} options={{ density: true, setting: true, reload: false }} columns={columns} dataSource={rows} pagination={false} loading={listQuery.isPending} scroll={{ x: 1050 }} locale={{ emptyText: <Empty description="当前筛选条件下没有工单" /> }} />
            {listQuery.hasNextPage ? <Button block loading={listQuery.isFetchingNextPage} onClick={() => void listQuery.fetchNextPage()}>加载更多</Button> : null}
          </Card>

          {selectedId ? <Card title="工单详情" variant="borderless">
            {detailQuery.isError ? <Alert type="error" showIcon message="工单详情不可用" description={workOrderErrorMessage(detailQuery.error)} /> : null}
            {detail ? <Space direction="vertical" size={16} style={{ width: '100%' }}>
              <div><Typography.Title level={4}>{detail.title}</Typography.Title><Typography.Paragraph type="secondary">{detail.description}</Typography.Paragraph></div>
              <ProDescriptions<WorkOrder> dataSource={detail} bordered column={{ xs: 1, sm: 2 }} columns={[
                { title: '状态', dataIndex: 'status', render: (_, row) => <Tag color={STATUS[row.status].color}>{STATUS[row.status].label}</Tag> },
                { title: '优先级', dataIndex: 'priority', render: (_, row) => <Tag color={PRIORITY[row.priority].color}>{PRIORITY[row.priority].label}</Tag> },
                { title: '指派人', dataIndex: 'assigneeId', renderText: (value) => value ?? '未指派' },
                { title: '团队', dataIndex: 'teamId', renderText: (value) => value ?? '未指派' },
                { title: '计划开始', dataIndex: 'scheduledStart', renderText: (value) => formatInstant(value, site.timezone) },
                { title: '截止时间', dataIndex: 'dueAt', renderText: (value) => formatInstant(value, site.timezone) },
                { title: '来源', key: 'source', span: 2, render: (_, row) => row.sourceReferences.map((source) => <Tag key={`${source.domain}:${source.resourceId}`}>{source.domain} · {source.resourceId}</Tag>) },
                { title: '版本', dataIndex: 'version' }, { title: '更新时间', dataIndex: 'updatedAt', renderText: (value) => formatInstant(value, site.timezone) },
              ]} />
              <Space wrap>
                {canAssign && !['COMPLETED', 'CANCELLED'].includes(detail.status) ? <Button icon={<UserSwitchOutlined />} onClick={() => { assignForm.setFieldsValue({ assigneeId: detail.assigneeId, teamId: detail.teamId, reason: 'OPERATOR_ASSIGNMENT' }); setAssignOpen(true); }}>指派</Button> : null}
                {commandEquipmentId ? (
                  <Button
                    href={`${realAssetsEquipmentPath(site.id, commandEquipmentId)}?sourceWorkOrder=${encodeURIComponent(detail.workOrderId)}`}
                    disabled={detail.status !== 'IN_PROGRESS'}
                  >
                    处理设备功能
                  </Button>
                ) : null}
                {canLifecycle ? lifecycleActions(detail.status).map((action) => <Button key={action} type={action === 'complete' ? 'primary' : 'default'} onClick={() => runLifecycle(action)}>{ACTION_LABEL[action]}</Button>) : null}
              </Space>
              <Typography.Title level={5}>生命周期</Typography.Title>
              <Timeline items={[...detail.timeline].reverse().map((event) => ({ children: <Space direction="vertical" size={0}><Typography.Text strong>{event.operation} · {STATUS[event.toStatus].label}</Typography.Text><Typography.Text>{event.reason}</Typography.Text><Typography.Text type="secondary">{event.actorId} · v{event.version} · {formatInstant(event.occurredAt, site.timezone)}</Typography.Text></Space> }))} />
            </Space> : <Typography.Text type="secondary">正在读取工单详情…</Typography.Text>}
          </Card> : null}
        </Space>
      </PageScaffold>

      <Modal open={createOpen} title={sourceAlarm ? '从 Alarm 创建工单' : '新建工单'} footer={null} onCancel={() => setCreateOpen(false)} destroyOnHidden>
        <ProForm<CreateValues> form={createForm} submitter={{ searchConfig: { submitText: '创建工单', resetText: '取消' }, onReset: () => setCreateOpen(false) }} onFinish={async (values) => { await createMutation.mutateAsync(values); return true; }}>
          <ProFormText name="title" label="标题" rules={[{ required: true }]} fieldProps={{ maxLength: 256 }} />
          <ProFormTextArea name="description" label="描述" rules={[{ required: true }]} fieldProps={{ maxLength: 4096, rows: 4 }} />
          <ProFormSelect name="priority" label="优先级" initialValue="MEDIUM" options={Object.entries(PRIORITY).map(([value, meta]) => ({ value, label: meta.label }))} />
          <ProFormText name="assigneeId" label="指派人 ID" placeholder="可选" />
          <ProFormText name="teamId" label="团队 ID" placeholder="可选" />
        </ProForm>
      </Modal>

      <Modal open={assignOpen} title="指派工单" footer={null} onCancel={() => setAssignOpen(false)} destroyOnHidden>
        <ProForm<AssignValues> form={assignForm} submitter={{ searchConfig: { submitText: '保存', resetText: '取消' }, onReset: () => setAssignOpen(false) }} onFinish={async (values) => { await assignMutation.mutateAsync(values); return true; }}>
          <ProFormText name="assigneeId" label="指派人 ID" /><ProFormText name="teamId" label="团队 ID" />
          <ProFormText name="reason" label="原因" rules={[{ required: true }]} fieldProps={{ maxLength: 256 }} />
        </ProForm>
      </Modal>
    </section>
  );
}
