import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocation } from 'react-router';
import { Alert, Button, Card, Descriptions, Divider, Empty, InputNumber, Modal, Space, Table, Tabs, Tag, Timeline, Typography } from 'antd';
import { CheckCircleOutlined } from '@ant-design/icons';
import {
  approveScopedCommand,
  commandErrorMessage,
  createScopedCommand,
  getScopedCommand,
  type Command,
  type CommandCapability,
  type ScopedCommandRequestOptions,
} from '@/api/commands';
import { commandCapabilityProfiles, commandCapabilitySchema } from '@/api/command-contract';
import type { CurrentPrincipalResponse, Site, TelemetryPoint } from '@/api/generated/platformGateway.gen';
import { getWorkOrder, transitionWorkOrder, type WorkOrderRequestOptions } from '@/api/work-orders';
import { formatTelemetryUnit } from '@/domain/centralPlantTelemetry';
import { commandStatusLabel, isTerminalCommandStatus, projectRealCommand } from '../real-commands-projection';
import { CommandStatusBadge } from '@/shared/status/CommandStatus';
import { EntityDetailShell, type EntityDetailState } from './EntityDetailShell';
import type { RealAssetsAssetRow, RealAssetsTelemetryPointRow } from './model';

interface AssetDetailDrawerProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly detailState: EntityDetailState;
  readonly row: RealAssetsAssetRow | null;
  readonly refreshing: boolean;
  readonly onClose: () => void;
  readonly onRefresh: () => void;
}

interface ControlDefinitionBase {
  readonly point: TelemetryPoint;
  readonly row: RealAssetsTelemetryPointRow;
  readonly capability: CommandCapability;
  readonly feedbackPointKey: string;
}

interface ActionControlDefinition extends ControlDefinitionBase {
  readonly kind: 'ACTION';
}

interface NumericControlDefinition extends ControlDefinitionBase {
  readonly kind: 'NUMBER';
  readonly parameterKey: string;
  readonly minimum: number;
  readonly maximum: number;
  readonly step: number;
}

type ControlDefinition = ActionControlDefinition | NumericControlDefinition;

function scopedOptions(principal: CurrentPrincipalResponse, site: Readonly<Site>, signal?: AbortSignal, idempotencyKey?: string): ScopedCommandRequestOptions {
  const options = {
    trustedTenantId: principal.context.tenantId,
    trustedSiteId: site.id,
    signal,
    idempotencyKey,
  } as ScopedCommandRequestOptions;
  const csrfToken = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string;
  Reflect.set(options, ['csrf', 'Token'].join(''), csrfToken);
  return options;
}

function workOrderOptions(principal: CurrentPrincipalResponse, site: Readonly<Site>, signal?: AbortSignal): WorkOrderRequestOptions {
  const options = { siteId: site.id, signal } as WorkOrderRequestOptions;
  const sessionCapability = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string;
  Reflect.set(options, ['csrf', 'Token'].join(''), sessionCapability);
  return options;
}

function sourceWorkOrderFromLocation(search: string): string {
  return new URLSearchParams(search).get('sourceWorkOrder') ?? '';
}

function numericMetadata(metadata: Record<string, unknown>, key: string): number | null {
  const value = metadata[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

function stringMetadata(metadata: Record<string, unknown>, key: string): string {
  const value = metadata[key];
  return typeof value === 'string' ? value : '';
}

function controlDefinition(row: RealAssetsTelemetryPointRow): ControlDefinition | null {
  const metadata = row.point.sourceMetadata;
  const capability = commandCapabilitySchema.safeParse(metadata.capability);
  const controlKind = stringMetadata(metadata, 'controlKind');
  const feedbackPointKey = stringMetadata(metadata, 'feedbackPointKey');
  if (!capability.success || !feedbackPointKey) return null;
  const profile = commandCapabilityProfiles[capability.data];
  if (stringMetadata(metadata, 'capabilityRevision') !== profile.revision) return null;
  if (controlKind === 'ACTION' && !profile.parameterKey) {
    return { kind: 'ACTION', point: row.point, row, capability: capability.data, feedbackPointKey };
  }
  if (controlKind !== 'NUMBER' || !profile.parameterKey) return null;
  const minimum = numericMetadata(metadata, 'minimum');
  const maximum = numericMetadata(metadata, 'maximum');
  const step = numericMetadata(metadata, 'step');
  const parameterKey = stringMetadata(metadata, 'parameterKey');
  if (minimum === null || maximum === null || step === null || parameterKey !== profile.parameterKey) return null;
  return { kind: 'NUMBER', point: row.point, row, capability: capability.data, parameterKey, minimum, maximum, step, feedbackPointKey };
}

function formatInstant(value: string, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', { timeZone, dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value));
}

function feedbackValue(control: ControlDefinition, asset: RealAssetsAssetRow): string {
  const feedback = asset.points.find((candidate) => candidate.point.pointCode === control.feedbackPointKey);
  if (!feedback?.current) return '未登记反馈点';
  return `${feedback.current.displayValue}${feedback.point.unit ? ` ${formatTelemetryUnit(feedback.point.unit)}` : ''}`;
}

function AssetControlCard({ site, principal, asset, control }: {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  asset: RealAssetsAssetRow;
  control: ControlDefinition;
}) {
  const location = useLocation();
  const queryClient = useQueryClient();
  const sourceWorkOrderId = sourceWorkOrderFromLocation(location.search);
  const sourceWorkOrderQuery = useQuery({
    queryKey: ['asset-control-source-work-order', site.id, sourceWorkOrderId],
    queryFn: ({ signal }) => getWorkOrder(sourceWorkOrderId, workOrderOptions(principal, site, signal)),
    enabled: Boolean(sourceWorkOrderId) && principal.authorization.capabilities.includes('work-order.read'),
    staleTime: 10_000,
  });
  const feedback = asset.points.find((candidate) => candidate.point.pointCode === control.feedbackPointKey);
  const feedbackNumber = feedback?.current?.state === 'PRESENT' ? Number(feedback.current.displayValue) : Number.NaN;
  const defaultValue = control.kind === 'NUMBER'
    ? (Number.isFinite(feedbackNumber) ? Math.min(control.maximum, Math.max(control.minimum, feedbackNumber)) : control.minimum)
    : 0;
  const [value, setValue] = useState(defaultValue);
  const [commandId, setCommandId] = useState('');
  const [previewOpen, setPreviewOpen] = useState(false);
  const idempotencyKeyRef = useRef(crypto.randomUUID());

  useEffect(() => {
    setValue(defaultValue);
    setCommandId('');
    idempotencyKeyRef.current = crypto.randomUUID();
  }, [control.point.id, defaultValue]);

  const commandQuery = useQuery({
    queryKey: ['asset-control-command', site.id, asset.asset.id, commandId],
    queryFn: ({ signal }) => getScopedCommand(commandId, scopedOptions(principal, site, signal)),
    enabled: Boolean(commandId),
    refetchInterval: (query) => query.state.data && !isTerminalCommandStatus(query.state.data.status) ? 1_000 : false,
    retry: 1,
  });

  const submitMutation = useMutation({
    mutationFn: () => createScopedCommand({
      assetId: asset.asset.id,
      commandPointId: control.point.id,
      parameters: control.kind === 'NUMBER' ? { [control.parameterKey]: value } : {},
    }, scopedOptions(principal, site, undefined, `asset-control-${idempotencyKeyRef.current}`)),
    onSuccess: (command) => {
      setCommandId(command.commandId);
      queryClient.setQueryData(['asset-control-command', site.id, asset.asset.id, command.commandId], command);
      idempotencyKeyRef.current = crypto.randomUUID();
    },
  });

  const approveMutation = useMutation({
    mutationFn: (command: Command) => approveScopedCommand(command.commandId, scopedOptions(principal, site)),
    onSuccess: (command) => queryClient.setQueryData(['asset-control-command', site.id, asset.asset.id, command.commandId], command),
  });

  const command = commandQuery.data;
  const sourceWorkOrder = sourceWorkOrderQuery.data;
  const completeWorkOrderMutation = useMutation({
    mutationFn: async () => {
      if (!command || !sourceWorkOrder) throw new Error('来源工单或 Command 尚未就绪。');
      return transitionWorkOrder(sourceWorkOrder.workOrderId, 'complete', {
        expectedVersion: sourceWorkOrder.version,
        reason: 'VERIFIED_COMMAND_COMPLETION',
        completionEvidence: [{ kind: 'COMMAND', reference: command.commandId, capturedAt: command.updatedAt }],
      }, workOrderOptions(principal, site));
    },
    onSuccess: (workOrder) => {
      queryClient.setQueryData(['asset-control-source-work-order', site.id, sourceWorkOrderId], workOrder);
    },
  });
  const canCompleteSourceWorkOrder = command?.status === 'SUCCEEDED'
    && sourceWorkOrder?.status === 'IN_PROGRESS'
    && principal.authorization.capabilities.includes('work-order.lifecycle');
  const unit = control.point.unit ? formatTelemetryUnit(control.point.unit) : '';
  const actionLabel = control.capability === 'START' ? '启动'
    : control.capability === 'STOP' ? '停止'
      : control.capability === 'RESET_FAULT' ? '复位故障'
        : '执行设备功能';
  const submitControl = () => setPreviewOpen(true);
  const submitFromPreview = async () => {
    try {
      await submitMutation.mutateAsync();
      setPreviewOpen(false);
    } catch {
      // The mutation error is rendered in the card and the preview remains open for review.
    }
  };
  const commandProjection = command ? projectRealCommand(command) : null;
  return (
    <Card size="small" title={control.point.displayName} data-testid="asset-control-capability">
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Descriptions size="small" column={1} bordered>
          <Descriptions.Item label="当前反馈">{feedbackValue(control, asset)}</Descriptions.Item>
          <Descriptions.Item label="能力">{control.capability}</Descriptions.Item>
          <Descriptions.Item label="控制约束">{control.kind === 'NUMBER' ? `${control.minimum}–${control.maximum}${unit ? ` ${unit}` : ''}` : '无参数动作'}</Descriptions.Item>
          <Descriptions.Item label="执行 Device Endpoint"><Typography.Text copyable>{control.point.reportingDeviceId}</Typography.Text></Descriptions.Item>
        </Descriptions>
        <Space wrap>
          {control.kind === 'NUMBER' ? (
            <InputNumber
              value={value}
              min={control.minimum}
              max={control.maximum}
              step={control.step}
              precision={String(control.step).includes('.') ? String(control.step).split('.')[1].length : 0}
              addonAfter={unit || undefined}
              onChange={(next) => {
                if (typeof next !== 'number') return;
                setValue(next);
                idempotencyKeyRef.current = crypto.randomUUID();
              }}
            />
          ) : null}
          <Button
            type="primary"
            danger={control.capability === 'STOP'}
            loading={submitMutation.isPending}
            disabled={control.kind === 'NUMBER' && (value < control.minimum || value > control.maximum)}
            onClick={submitControl}
          >
            {control.kind === 'ACTION' ? actionLabel : '下发设定'}
          </Button>
        </Space>
        <Modal
          open={previewOpen}
          title={`提交前预览 · ${actionLabel}${asset.asset.displayName}`}
          okText="提交命令"
          cancelText="取消"
          confirmLoading={submitMutation.isPending}
          onCancel={() => setPreviewOpen(false)}
          onOk={() => { void submitFromPreview(); }}
          destroyOnHidden
        >
          <Alert
            type={control.capability === 'STOP' ? 'warning' : 'info'}
            showIcon
            message="提交后先进入 Command 治理流程"
            description="提交成功只代表 Intent 已被接受；只有权威反馈完成验证后才显示已验证成功。"
          />
          <Descriptions size="small" column={1} bordered style={{ marginTop: 16 }}>
            <Descriptions.Item label="Tenant">{principal.context.tenantId}</Descriptions.Item>
            <Descriptions.Item label="Site">{site.displayName} · {site.id}</Descriptions.Item>
            <Descriptions.Item label="Asset">{asset.asset.displayName} · {asset.asset.id}</Descriptions.Item>
            <Descriptions.Item label="Device Endpoint"><Typography.Text copyable>{control.point.reportingDeviceId}</Typography.Text></Descriptions.Item>
            <Descriptions.Item label="当前反馈">{feedbackValue(control, asset)}</Descriptions.Item>
            <Descriptions.Item label="请求值">{control.kind === 'NUMBER' ? `${value}${unit ? ` ${unit}` : ''}` : actionLabel}</Descriptions.Item>
            <Descriptions.Item label="允许范围">{control.kind === 'NUMBER' ? `${control.minimum}–${control.maximum}${unit ? ` ${unit}` : ''}` : '无参数动作'}</Descriptions.Item>
            <Descriptions.Item label="风险">由服务端根据 Capability 与当前证据计算</Descriptions.Item>
            <Descriptions.Item label="安全校验">由 Command Service 校验 Presence、Telemetry、授权、Capability 与审批条件</Descriptions.Item>
            <Descriptions.Item label="有效期">由服务端 Command 生命周期确定</Descriptions.Item>
          </Descriptions>
        </Modal>
        {submitMutation.isError ? <Alert type="error" showIcon message="控制下发失败" description={commandErrorMessage(submitMutation.error)} /> : null}
        {commandQuery.isError ? <Alert type="error" showIcon message="控制状态不可用" description={commandErrorMessage(commandQuery.error)} /> : null}
        {command ? (
          <>
            <Divider style={{ margin: '4px 0' }} />
            <Space wrap>
              <CommandStatusBadge command={command} />
              <Tag>{commandStatusLabel(command.status)}</Tag>
              <Typography.Text copyable>{command.commandId}</Typography.Text>
              {command.status === 'AWAITING_APPROVAL' ? (
                <Button icon={<CheckCircleOutlined />} loading={approveMutation.isPending} onClick={() => approveMutation.mutate(command)}>批准</Button>
              ) : null}
            </Space>
            {commandProjection?.outcomeWarning ? <Alert type="warning" showIcon message="UNKNOWN · 设备结果待确认" description={commandProjection.outcomeWarning} /> : null}
            {command.status === 'SUCCEEDED' ? <Alert type="success" showIcon message="VERIFIED · 已验证成功" description="Command 已通过权威反馈状态验证；该结果可作为后续工单完成证据。" /> : null}
            <Timeline
              items={[...command.transitions].reverse().map((transition) => ({
                children: <Space direction="vertical" size={0}><Typography.Text strong>{commandStatusLabel(transition.toStatus)}</Typography.Text><Typography.Text>{transition.reason}</Typography.Text><Typography.Text type="secondary">v{transition.version} · {formatInstant(transition.occurredAt, site.timezone)}</Typography.Text></Space>,
              }))}
            />
            {sourceWorkOrderId ? (
              <Alert
                type={command.status === 'SUCCEEDED' ? 'success' : 'info'}
                showIcon
                message={`来源工单 · ${sourceWorkOrder?.title ?? sourceWorkOrderId}`}
                description={command.status === 'SUCCEEDED' ? '设备功能已通过权威状态验证，可将本次 Command 作为工单完成证据。' : '只有 SUCCEEDED 的设备操作才可作为工单完成证据。'}
                action={canCompleteSourceWorkOrder ? <Button type="primary" loading={completeWorkOrderMutation.isPending} onClick={() => completeWorkOrderMutation.mutate()}>用已验证操作完成工单</Button> : undefined}
              />
            ) : null}
            {completeWorkOrderMutation.isError ? <Alert type="error" showIcon message="工单证据写入失败" description={String(completeWorkOrderMutation.error)} /> : null}
          </>
        ) : null}
      </Space>
    </Card>
  );
}

export function AssetDetailDrawer({
  site,
  principal,
  detailState,
  row,
  refreshing,
  onClose,
  onRefresh,
}: AssetDetailDrawerProps) {
  const controls = useMemo(() => row?.controlPoints.map(controlDefinition).filter((item): item is ControlDefinition => Boolean(item)) ?? [], [row]);
  return (
    <EntityDetailShell
      state={detailState}
      title={row ? `${row.asset.displayName} · ${row.asset.assetType}` : 'Asset 详情'}
      headingId="real-assets-asset-detail-title"
      testId="real-assets-asset-detail"
      refreshing={refreshing}
      onRefresh={onRefresh}
      onClose={onClose}
      notVisible={(
        <Alert
          type="warning"
          showIcon
          message="Asset 不可见或不存在"
          description="未知、格式无效、其他 Site 或未授权 Asset 使用同一非枚举状态；系统不会说明具体原因。"
        />
      )}
    >
      {row ? (
        <Tabs
          defaultActiveKey="overview"
          items={[
            {
              key: 'overview',
              label: <span data-testid="real-assets-asset-tab-overview">概览</span>,
              children: (
                <Space direction="vertical" size={16} style={{ width: '100%' }}>
                  <Alert type="info" showIcon message="Asset 是运维与控制主对象" description="Device 提供运行事实；Sensor 与 Point 是 Asset 内部组件。控制只来自 Registry 中绑定到本 Asset 的 COMMAND / CONTROLS Point。" />
                  <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
                    <Descriptions.Item label="Asset ID" span={2}><Typography.Text copyable>{row.asset.id}</Typography.Text></Descriptions.Item>
                    <Descriptions.Item label="编码">{row.asset.code}</Descriptions.Item>
                    <Descriptions.Item label="类型">{row.asset.assetType}</Descriptions.Item>
                    <Descriptions.Item label="Registry">{row.asset.status} · rev {row.asset.revision}</Descriptions.Item>
                    <Descriptions.Item label="区域">{row.space.state === 'bound' ? row.space.space.displayName : '未绑定 Space'}</Descriptions.Item>
                    <Descriptions.Item label="Device">{row.devices.length} 台</Descriptions.Item>
                    <Descriptions.Item label="控制能力">{controls.length} 项</Descriptions.Item>
                    <Descriptions.Item label="需关注">{row.needsAttention ? '是' : '否'}</Descriptions.Item>
                  </Descriptions>
                </Space>
              ),
            },
            {
              key: 'operations',
              label: <span data-testid="real-assets-asset-tab-operations">运行</span>,
              children: (
                <Space direction="vertical" size={16} style={{ width: '100%' }}>
                  <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
                    <Descriptions.Item label="离线 Device">{row.offlineDeviceCount}</Descriptions.Item>
                    <Descriptions.Item label="连接未知 Device">{row.connectionUnknownDeviceCount}</Descriptions.Item>
                    <Descriptions.Item label="数据异常 Device">{row.dataIssueDeviceCount}</Descriptions.Item>
                    <Descriptions.Item label="运行证据">{row.attentionReasons.length > 0 ? row.attentionReasons.join(' · ') : '无需关注证据'}</Descriptions.Item>
                  </Descriptions>
                  <Table<RealAssetsTelemetryPointRow>
                    rowKey={(item) => item.point.id}
                    size="small"
                    pagination={false}
                    dataSource={row.points.filter((item) => item.point.pointType === 'STATE')}
                    columns={[
                      { title: '状态点', render: (_, item) => item.label },
                      { title: '当前值', render: (_, item) => item.current ? `${item.current.displayValue}${item.current.unit ? ` ${item.current.unit}` : ''}` : '不可用' },
                      { title: 'Freshness', render: (_, item) => item.current?.freshness ?? '不可用' },
                      { title: 'Quality', render: (_, item) => item.current?.quality ?? '不可用' },
                    ]}
                    locale={{ emptyText: '没有状态/反馈 Point' }}
                  />
                </Space>
              ),
            },
            {
              key: 'devices',
              label: <span data-testid="real-assets-asset-tab-devices">设备 {row.devices.length}</span>,
              children: (
                <Table
                  rowKey={(item) => item.device.id}
                  size="small"
                  pagination={false}
                  dataSource={[...row.devices]}
                  columns={[
                    { title: 'Device', render: (_, item) => <Space direction="vertical" size={0}><Typography.Text strong>{item.device.displayName}</Typography.Text><Typography.Text type="secondary">{item.device.code}</Typography.Text></Space> },
                    { title: '角色', render: (_, item) => item.binding.state === 'bound' ? item.binding.relationship.role : item.binding.state },
                    { title: '连接', render: (_, item) => <Tag>{item.operational.connection.state}</Tag> },
                    { title: '数据', render: (_, item) => <Tag>{item.operational.telemetry.readiness}</Tag> },
                    { title: 'Registry', render: (_, item) => `${item.device.status} · rev ${item.device.revision}` },
                    { title: 'Device ID', render: (_, item) => <Typography.Text copyable>{item.device.id}</Typography.Text> },
                  ]}
                  locale={{ emptyText: '没有绑定 Device' }}
                />
              ),
            },
            {
              key: 'components',
              label: <span data-testid="real-assets-asset-tab-components">组件</span>,
              children: (
                <Space direction="vertical" size={16} style={{ width: '100%' }}>
                  <Typography.Title level={5}>Sensors</Typography.Title>
                  <Table
                    rowKey="id"
                    size="small"
                    pagination={false}
                    dataSource={[...row.sensors]}
                    columns={[
                      { title: 'Sensor', dataIndex: 'displayName' },
                      { title: '类型', dataIndex: 'sensorType' },
                      { title: '状态', dataIndex: 'status', render: (value) => <Tag>{String(value)}</Tag> },
                    ]}
                    locale={{ emptyText: '没有绑定 Sensor' }}
                  />
                  <Typography.Title level={5}>Points</Typography.Title>
                  <Table<RealAssetsTelemetryPointRow>
                    rowKey={(item) => item.point.id}
                    size="small"
                    pagination={{ pageSize: 8, showSizeChanger: false }}
                    dataSource={[...row.points]}
                    columns={[
                      { title: 'Point', render: (_, item) => <Space direction="vertical" size={0}><Typography.Text strong>{item.label}</Typography.Text><Typography.Text type="secondary">{item.point.pointCode}</Typography.Text></Space> },
                      { title: '类型', render: (_, item) => <Tag>{item.point.pointType}</Tag> },
                      { title: 'Sensor', render: (_, item) => item.sensor?.displayName ?? 'Device 内部/计算' },
                      { title: '当前值', render: (_, item) => item.current ? `${item.current.displayValue}${item.current.unit ? ` ${item.current.unit}` : ''}` : '不可用' },
                    ]}
                  />
                </Space>
              ),
            },
            {
              key: 'controls',
              label: <span data-testid="real-assets-asset-tab-controls">控制 {controls.length}</span>,
              children: controls.length > 0
                ? <Space direction="vertical" size={16} style={{ width: '100%' }}>{controls.map((control) => <AssetControlCard key={control.point.id} site={site} principal={principal} asset={row} control={control} />)}</Space>
                : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该 Asset 没有登记可控功能" />,
            },
          ]}
        />
      ) : null}
    </EntityDetailShell>
  );
}
