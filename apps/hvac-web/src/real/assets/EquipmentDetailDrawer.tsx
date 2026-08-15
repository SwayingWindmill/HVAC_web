import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Descriptions, Divider, Drawer, Empty, InputNumber, Modal, Select, Space, Table, Tag, Timeline, Typography } from 'antd';
import { CheckCircleOutlined, ReloadOutlined } from '@ant-design/icons';
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
import type { S2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { getWorkOrder, transitionWorkOrder, type WorkOrderRequestOptions } from '@/api/work-orders';
import { formatTelemetryUnit } from '@/domain/centralPlantTelemetry';
import { commandStatusLabel, isTerminalCommandStatus } from '../real-commands-projection';
import type { ProtectedScopeRequestToken } from '../protected-scope';
import type { RealAssetsEquipmentRow, RealAssetsTelemetryPointRow } from './model';

const DeviceHistoryTrends = lazy(async () => {
  const module = await import('./DeviceHistoryTrends');
  return { default: module.DeviceHistoryTrends };
});

interface EquipmentDetailDrawerProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly row: RealAssetsEquipmentRow | null;
  readonly telemetryClient: S2TelemetryClient;
  readonly protectedGeneration: number;
  readonly protectedRequestToken: () => ProtectedScopeRequestToken;
  readonly routePolicyRevision: string | null;
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

function sourceWorkOrderFromLocation(): string {
  return new URLSearchParams(globalThis.location.search).get('sourceWorkOrder') ?? '';
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

function feedbackValue(control: ControlDefinition, equipment: RealAssetsEquipmentRow): string {
  const feedback = equipment.points.find((candidate) => candidate.point.pointCode === control.feedbackPointKey);
  if (!feedback?.current) return '未登记反馈点';
  return `${feedback.current.displayValue}${feedback.point.unit ? ` ${formatTelemetryUnit(feedback.point.unit)}` : ''}`;
}

function EquipmentControlCard({ site, principal, equipment, control }: {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  equipment: RealAssetsEquipmentRow;
  control: ControlDefinition;
}) {
  const queryClient = useQueryClient();
  const sourceWorkOrderId = sourceWorkOrderFromLocation();
  const sourceWorkOrderQuery = useQuery({
    queryKey: ['equipment-control-source-work-order', site.id, sourceWorkOrderId],
    queryFn: ({ signal }) => getWorkOrder(sourceWorkOrderId, workOrderOptions(principal, site, signal)),
    enabled: Boolean(sourceWorkOrderId) && principal.authorization.capabilities.includes('work-order.read'),
    staleTime: 10_000,
  });
  const feedback = equipment.points.find((candidate) => candidate.point.pointCode === control.feedbackPointKey);
  const feedbackNumber = feedback?.current?.state === 'PRESENT' ? Number(feedback.current.displayValue) : Number.NaN;
  const defaultValue = control.kind === 'NUMBER'
    ? (Number.isFinite(feedbackNumber) ? Math.min(control.maximum, Math.max(control.minimum, feedbackNumber)) : control.minimum)
    : 0;
  const [value, setValue] = useState(defaultValue);
  const [commandId, setCommandId] = useState('');
  const idempotencyKeyRef = useRef(crypto.randomUUID());

  useEffect(() => {
    setValue(defaultValue);
    setCommandId('');
    idempotencyKeyRef.current = crypto.randomUUID();
  }, [control.point.id, defaultValue]);

  const commandQuery = useQuery({
    queryKey: ['equipment-control-command', site.id, equipment.equipment.id, commandId],
    queryFn: ({ signal }) => getScopedCommand(commandId, scopedOptions(principal, site, signal)),
    enabled: Boolean(commandId),
    refetchInterval: (query) => query.state.data && !isTerminalCommandStatus(query.state.data.status) ? 1_000 : false,
    retry: 1,
  });

  const submitMutation = useMutation({
    mutationFn: () => createScopedCommand({
      equipmentId: equipment.equipment.id,
      commandPointId: control.point.id,
      parameters: control.kind === 'NUMBER' ? { [control.parameterKey]: value } : {},
    }, scopedOptions(principal, site, undefined, `asset-control-${idempotencyKeyRef.current}`)),
    onSuccess: (command) => {
      setCommandId(command.commandId);
      queryClient.setQueryData(['equipment-control-command', site.id, equipment.equipment.id, command.commandId], command);
      idempotencyKeyRef.current = crypto.randomUUID();
    },
  });

  const approveMutation = useMutation({
    mutationFn: (command: Command) => approveScopedCommand(command.commandId, scopedOptions(principal, site)),
    onSuccess: (command) => queryClient.setQueryData(['equipment-control-command', site.id, equipment.equipment.id, command.commandId], command),
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
      queryClient.setQueryData(['equipment-control-source-work-order', site.id, sourceWorkOrderId], workOrder);
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
  const submitControl = () => {
    if (control.capability === 'STOP') {
      Modal.confirm({
        title: `确认${actionLabel}${equipment.equipment.displayName}？`,
        content: '系统将创建受治理的 Command，并以权威反馈状态验证执行结果。',
        okText: '确认停止',
        cancelText: '取消',
        onOk: () => submitMutation.mutateAsync(),
      });
      return;
    }
    submitMutation.mutate();
  };
  return (
    <Card size="small" title={control.point.displayName} data-testid="equipment-control-capability">
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Descriptions size="small" column={1} bordered>
          <Descriptions.Item label="当前反馈">{feedbackValue(control, equipment)}</Descriptions.Item>
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
        {submitMutation.isError ? <Alert type="error" showIcon message="控制下发失败" description={commandErrorMessage(submitMutation.error)} /> : null}
        {commandQuery.isError ? <Alert type="error" showIcon message="控制状态不可用" description={commandErrorMessage(commandQuery.error)} /> : null}
        {command ? (
          <>
            <Divider style={{ margin: '4px 0' }} />
            <Space wrap>
              <Tag color={command.status === 'SUCCEEDED' ? 'green' : command.status === 'OUTCOME_UNKNOWN' ? 'orange' : 'blue'}>{commandStatusLabel(command.status)}</Tag>
              <Typography.Text copyable>{command.commandId}</Typography.Text>
              {command.status === 'AWAITING_APPROVAL' ? (
                <Button icon={<CheckCircleOutlined />} loading={approveMutation.isPending} onClick={() => approveMutation.mutate(command)}>批准</Button>
              ) : null}
            </Space>
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

export function EquipmentDetailDrawer({
  site,
  principal,
  row,
  telemetryClient,
  protectedGeneration,
  protectedRequestToken,
  routePolicyRevision,
  refreshing,
  onClose,
  onRefresh,
}: EquipmentDetailDrawerProps) {
  const controls = useMemo(() => row?.controlPoints.map(controlDefinition).filter((item): item is ControlDefinition => Boolean(item)) ?? [], [row]);
  const defaultHistoryDeviceId = row?.devices[0]?.device.id ?? '';
  const [historyDeviceId, setHistoryDeviceId] = useState(() => defaultHistoryDeviceId);
  useEffect(() => {
    setHistoryDeviceId(defaultHistoryDeviceId);
  }, [defaultHistoryDeviceId, row?.equipment.id]);
  const historyDevice = row?.devices.find((item) => item.device.id === historyDeviceId) ?? row?.devices[0] ?? null;
  const historyAllowed = principal.authorization.capabilities.includes('telemetry.history.read');
  const sessionCapability = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string;
  return (
    <Drawer
      width={760}
      open={Boolean(row)}
      onClose={onClose}
      destroyOnHidden
      title={row ? `${row.equipment.displayName} · ${row.equipment.equipmentType}` : 'Equipment'}
      extra={<Button icon={<ReloadOutlined />} loading={refreshing} onClick={onRefresh}>刷新</Button>}
    >
      {row ? (
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          <Alert type="info" showIcon message="Equipment 是运维与控制主对象" description="Sensor 与点位用于观测；只有 Registry 中绑定到本 Equipment 的 COMMAND / CONTROLS 点位会生成控制功能。" />
          <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
            <Descriptions.Item label="Equipment ID" span={2}><Typography.Text copyable>{row.equipment.id}</Typography.Text></Descriptions.Item>
            <Descriptions.Item label="编码">{row.equipment.code}</Descriptions.Item>
            <Descriptions.Item label="类型">{row.equipment.equipmentType}</Descriptions.Item>
            <Descriptions.Item label="区域">{row.area.state === 'bound' ? row.area.area.displayName : '未绑定'}</Descriptions.Item>
            <Descriptions.Item label="状态"><Tag>{row.operatingState}</Tag></Descriptions.Item>
          </Descriptions>

          <Typography.Title level={5}>设备功能</Typography.Title>
          {controls.length > 0 ? controls.map((control) => <EquipmentControlCard key={control.point.id} site={site} principal={principal} equipment={row} control={control} />) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该 Equipment 没有登记可控功能" />}

          <Typography.Title level={5}>运行状态与反馈</Typography.Title>
          <Table<RealAssetsTelemetryPointRow>
            rowKey={(item) => item.point.id}
            size="small"
            pagination={false}
            dataSource={row.points.filter((item) => item.point.pointType === 'STATE')}
            columns={[
              { title: '点位', render: (_, item) => item.label },
              { title: '类型', render: (_, item) => <Tag>{item.point.pointType}</Tag> },
              { title: '当前值', render: (_, item) => item.current ? `${item.current.displayValue}${item.point.unit ? ` ${formatTelemetryUnit(item.point.unit)}` : ''}` : '—' },
            ]}
            locale={{ emptyText: '没有状态点位' }}
          />

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

          <Typography.Title level={5}>Device Endpoints</Typography.Title>
          <Table
            rowKey={(item) => item.device.id}
            size="small"
            pagination={false}
            dataSource={[...row.devices]}
            columns={[
              { title: '端点', render: (_, item) => item.device.displayName },
              { title: '角色', render: (_, item) => item.binding.state === 'bound' ? item.binding.relationship.role : item.binding.state },
              { title: '通讯状态', render: (_, item) => <Tag>{item.operatingState}</Tag> },
              { title: 'Device ID', render: (_, item) => <Typography.Text copyable>{item.device.id}</Typography.Text> },
            ]}
          />

          <Typography.Title level={5}>历史趋势</Typography.Title>
          {historyDevice ? (
            <Space direction="vertical" size={12} style={{ width: '100%' }}>
              <Select
                value={historyDevice.device.id}
                onChange={setHistoryDeviceId}
                options={row.devices.map((item) => ({ value: item.device.id, label: `${item.device.displayName} · ${item.device.code}` }))}
                style={{ width: '100%' }}
                aria-label="选择历史趋势 Device Endpoint"
              />
              <Suspense fallback={<div className="real-assets-history__loading" role="status">正在加载历史趋势组件…</div>}>
                <DeviceHistoryTrends
                  site={site}
                  row={historyDevice}
                  principal={principal}
                  client={telemetryClient}
                  protectedGeneration={protectedGeneration}
                  protectedRequestToken={protectedRequestToken}
                  routePolicyRevision={routePolicyRevision}
                  historyAllowed={historyAllowed}
                  currentUnavailable={historyDevice.snapshotResult?.status === 'error'}
                  sessionCapability={sessionCapability}
                />
              </Suspense>
            </Space>
          ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该 Equipment 没有可查询历史的 Device Endpoint" />}

          <Typography.Title level={5}>观测与计算点</Typography.Title>
          <Table<RealAssetsTelemetryPointRow>
            rowKey={(item) => item.point.id}
            size="small"
            pagination={{ pageSize: 8, showSizeChanger: false }}
            dataSource={row.points.filter((item) => item.point.pointType !== 'COMMAND')}
            columns={[
              { title: '点位', render: (_, item) => item.label },
              { title: '类型', render: (_, item) => <Tag>{item.point.pointType}</Tag> },
              { title: 'Sensor', render: (_, item) => item.sensor?.displayName ?? '设备内部/计算' },
              { title: '当前值', render: (_, item) => item.current ? `${item.current.displayValue}${item.point.unit ? ` ${formatTelemetryUnit(item.point.unit)}` : ''}` : '—' },
            ]}
          />
        </Space>
      ) : null}
    </Drawer>
  );
}
