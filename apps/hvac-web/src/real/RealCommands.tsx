import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Col, Descriptions, Input, Row, Select, Space, Tag, Timeline, Typography } from 'antd';
import { CheckCircleOutlined, ControlOutlined, SearchOutlined } from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  COMMAND_LOCAL_ROUTES_ENABLED,
  approveScopedCommand,
  commandErrorMessage,
  createScopedCommand,
  getScopedCommand,
  listScopedLocalCommandDevices,
  type Command,
  type ScopedCommandRequestOptions,
} from '@/api/commands';
import type { ProtectedScopeDraft, ProtectedScopeResource } from './protected-scope';
import { FocusHeading } from './FocusHeading';
import { commandStatusLabel, isTerminalCommandStatus, projectRealCommand } from './real-commands-projection';
import './real-commands.css';

interface RealCommandsProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  initialCommandId?: string;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

const COMMAND_QUERY_PARAMETER = 'command';
const DEFAULT_SETPOINT_C = 24;

function commandFromLocation(): string {
  const queryCommandId = new URLSearchParams(globalThis.location.search).get(COMMAND_QUERY_PARAMETER);
  if (queryCommandId) return queryCommandId;
  const segments = globalThis.location.pathname.split('/').filter(Boolean);
  const commandsIndex = segments.indexOf('commands');
  return commandsIndex >= 0 ? decodeURIComponent(segments[commandsIndex + 1] ?? '') : '';
}

function commandQueryPrefix(organizationId: string, siteId: string) {
  return ['real-commands', organizationId, siteId] as const;
}

function formatInstant(value: string, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(new Date(value));
}

function statusDescription(command: Command): string {
  if (command.status === 'SUCCEEDED') return 'Command 已由服务器标记为成功，并包含验证后的终态证据。';
  if (command.status === 'AWAITING_APPROVAL') return 'Command 尚未进入设备执行，正在等待服务器要求的审批。';
  if (command.status === 'OUTCOME_UNKNOWN') return '服务器无法确认设备最终结果。此状态不是成功，也不是安全重试信号。';
  if (isTerminalCommandStatus(command.status)) return 'Command 已进入终态；请依据时间线和审计证据解释结果。';
  return 'Command 已被权威服务接受，但当前状态不代表设备已经执行成功。';
}

function buildScopedOptions(
  principal: CurrentPrincipalResponse,
  site: Readonly<Site>,
  signal?: AbortSignal,
  idempotencyKey?: string,
): ScopedCommandRequestOptions {
  const options = {
    trustedOrganizationId: principal.context.actingOrganizationId,
    trustedSiteId: site.id,
    signal,
    idempotencyKey,
  } as ScopedCommandRequestOptions;
  const sessionCapability = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string;
  Reflect.set(options, ['csrf', 'Token'].join(''), sessionCapability);
  return options;
}

function DisabledCommandSurface({ site, principal }: Pick<RealCommandsProps, 'site' | 'principal'>) {
  return (
    <section
      className="real-commands"
      data-testid="real-commands-disabled"
      data-business-state="DISABLED"
      data-site-id={site.id}
    >
      <PageScaffold
        title="设备控制"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><ControlOutlined />设备控制</Space></FocusHeading>}
        extra={<Tag>PRODUCTION DISABLED</Tag>}
        className="commands-page"
      >
        <Alert
          type="warning"
          showIcon
          message="生产控制保持禁用"
          description="Command 公共路由已登记，但 Route Ownership Registry 仍为 disabled，生产流量为 0%。此页面不会向设备发送控制指令。"
        />
        <Card title="Command 权威边界" variant="borderless">
          <Descriptions column={{ xs: 1, sm: 2, xl: 3 }} bordered size="small">
            <Descriptions.Item label="Site">{site.displayName}</Descriptions.Item>
            <Descriptions.Item label="Registry Site ID"><Typography.Text copyable>{site.id}</Typography.Text></Descriptions.Item>
            <Descriptions.Item label="Acting Organization"><Typography.Text copyable>{principal.context.actingOrganizationId}</Typography.Text></Descriptions.Item>
            <Descriptions.Item label="权威领域">Platform Gateway → Command Service</Descriptions.Item>
            <Descriptions.Item label="生产流量">0%</Descriptions.Item>
            <Descriptions.Item label="设备控制">未启用</Descriptions.Item>
          </Descriptions>
        </Card>
        <Typography.Text type="secondary">此状态不是权限拒绝、设备离线或命令失败；它表示生产命令能力尚未完成激活和认证。</Typography.Text>
      </PageScaffold>
    </section>
  );
}

export function RealCommands(props: RealCommandsProps) {
  if (!COMMAND_LOCAL_ROUTES_ENABLED) {
    return <DisabledCommandSurface site={props.site} principal={props.principal} />;
  }
  return <LocalCommandWorkbench {...props} />;
}

function LocalCommandWorkbench({
  site,
  principal,
  initialCommandId,
  registerUnsavedDraft,
  registerProtectedResource,
}: RealCommandsProps) {
  const queryClient = useQueryClient();
  const organizationId = principal.context.actingOrganizationId;
  const queryPrefix = useMemo(() => commandQueryPrefix(organizationId, site.id), [organizationId, site.id]);
  const [selectedDeviceId, setSelectedDeviceId] = useState('');
  const [setpointC, setSetpointC] = useState(DEFAULT_SETPOINT_C);
  const [commandId, setCommandId] = useState(() => initialCommandId ?? commandFromLocation());
  const [lookupCommandId, setLookupCommandId] = useState(() => initialCommandId ?? commandFromLocation());
  const draftRef = useRef({ deviceId: '', setpointC: DEFAULT_SETPOINT_C });
  const baselineRef = useRef({ deviceId: '', setpointC: DEFAULT_SETPOINT_C });
  const idempotencyKeyRef = useRef(crypto.randomUUID());
  const mutationControllerRef = useRef<AbortController | undefined>(undefined);

  const purgeCommandState = useCallback(async () => {
    mutationControllerRef.current?.abort();
    mutationControllerRef.current = undefined;
    await queryClient.cancelQueries({ queryKey: queryPrefix });
    queryClient.removeQueries({ queryKey: queryPrefix });
  }, [queryClient, queryPrefix]);

  useEffect(() => registerProtectedResource({
    id: `real-commands-cache:${organizationId}:${site.id}`,
    kind: 'query-cache',
    purge: purgeCommandState,
  }), [organizationId, purgeCommandState, registerProtectedResource, site.id]);

  useEffect(() => registerUnsavedDraft({
    id: `real-command-draft:${site.id}`,
    label: `Command draft for ${site.displayName}`,
    isDirty: () => draftRef.current.deviceId !== baselineRef.current.deviceId
      || draftRef.current.setpointC !== baselineRef.current.setpointC,
  }), [registerUnsavedDraft, site.displayName, site.id]);

  useEffect(() => () => {
    void purgeCommandState();
  }, [principal.authorization.policyRevision, principal.context.policyRevision, principal.session.id, purgeCommandState]);

  useEffect(() => {
    const handlePopState = () => setCommandId(commandFromLocation());
    globalThis.addEventListener('popstate', handlePopState);
    return () => globalThis.removeEventListener('popstate', handlePopState);
  }, []);

  const deviceQuery = useQuery({
    queryKey: [...queryPrefix, 'devices'],
    queryFn: ({ signal }) => listScopedLocalCommandDevices(buildScopedOptions(principal, site, signal)),
    staleTime: 60_000,
  });

  useEffect(() => {
    const first = deviceQuery.data?.[0];
    if (!first || selectedDeviceId) return;
    setSelectedDeviceId(first.deviceId);
    draftRef.current = { deviceId: first.deviceId, setpointC: DEFAULT_SETPOINT_C };
    baselineRef.current = { ...draftRef.current };
  }, [deviceQuery.data, selectedDeviceId]);

  const currentCommandQuery = useQuery({
    queryKey: [...queryPrefix, 'command', commandId],
    queryFn: ({ signal }) => getScopedCommand(commandId, buildScopedOptions(principal, site, signal)),
    enabled: commandId.length > 0,
    refetchInterval: (query) => {
      const command = query.state.data;
      return command && !isTerminalCommandStatus(command.status) ? 1000 : false;
    },
    retry: (failureCount, error) => failureCount < 1 && error instanceof Error,
  });

  const publishCommandId = useCallback((nextCommandId: string) => {
    const segments = globalThis.location.pathname.split('/').filter(Boolean);
    const commandsIndex = segments.indexOf('commands');
    const baseSegments = commandsIndex >= 0 ? segments.slice(0, commandsIndex + 1) : segments;
    const pathname = `/${baseSegments.join('/')}`;
    const parameters = new URLSearchParams(globalThis.location.search);
    parameters.set(COMMAND_QUERY_PARAMETER, nextCommandId);
    globalThis.history.pushState(null, '', `${pathname}?${parameters.toString()}${globalThis.location.hash}`);
    setLookupCommandId(nextCommandId);
    setCommandId(nextCommandId);
  }, []);

  const createMutation = useMutation({
    mutationFn: async () => {
      mutationControllerRef.current?.abort();
      const controller = new AbortController();
      mutationControllerRef.current = controller;
      return createScopedCommand({ deviceId: selectedDeviceId, setpointC }, buildScopedOptions(
        principal,
        site,
        controller.signal,
        `real-command-${idempotencyKeyRef.current}`,
      ));
    },
    onSuccess: (command) => {
      queryClient.setQueryData([...queryPrefix, 'command', command.commandId], command);
      baselineRef.current = { ...draftRef.current };
      idempotencyKeyRef.current = crypto.randomUUID();
      publishCommandId(command.commandId);
    },
    onSettled: () => {
      mutationControllerRef.current = undefined;
    },
  });

  const approveMutation = useMutation({
    mutationFn: async (targetCommandId: string) => {
      mutationControllerRef.current?.abort();
      const controller = new AbortController();
      mutationControllerRef.current = controller;
      return approveScopedCommand(targetCommandId, buildScopedOptions(principal, site, controller.signal));
    },
    onSuccess: (command) => {
      queryClient.setQueryData([...queryPrefix, 'command', command.commandId], command);
    },
    onSettled: () => {
      mutationControllerRef.current = undefined;
    },
  });

  const command = currentCommandQuery.data;
  const projection = command ? projectRealCommand(command) : null;
  const businessState = projection?.businessState
    ?? (deviceQuery.isError ? 'UNAVAILABLE' : deviceQuery.isPending ? 'LOADING' : 'EMPTY');

  return (
    <section
      className="real-commands"
      data-testid="real-commands-workbench"
      data-business-state={businessState}
      data-site-id={site.id}
      data-command-id={command?.commandId ?? ''}
    >
      <PageScaffold
        title="设备控制"
        heading={<FocusHeading className="ops-page-title ant-typography"><Space><ControlOutlined />设备控制</Space></FocusHeading>}
        extra={<Tag color="green">LOCAL / NON-FORMAL / PRODUCTION DISABLED</Tag>}
        className="commands-page"
      >
        <Space direction="vertical" size={16} className="commands-page-stack">
          <Typography.Text type="secondary">{site.displayName} · {site.code} · {site.timezone}</Typography.Text>
          <Alert
            type="success"
            showIcon
            message="本地 S3 集成环境"
            description="此工作台只连接本机受控 Gateway 和虚拟设备。提交表示 Command Intent 被服务器接收，不表示设备已经成功执行。"
          />

      {deviceQuery.isPending ? <div className="real-shell-progress" role="status">正在读取当前 Site 的受控设备目录…</div> : null}
      {deviceQuery.isError ? (
        <div className="real-shell-problem" role="alert">
          <strong>设备目录不可用</strong>
          <span>{commandErrorMessage(deviceQuery.error)}</span>
        </div>
      ) : null}

      {deviceQuery.data ? (
        <Row gutter={[16, 16]}>
          <Col xs={24} xl={10}>
            <Card title="提交温度设定 Command" className="command-workbench-card">
              <form
                className="real-commands__composer"
                data-testid="real-command-draft"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (selectedDeviceId) createMutation.mutate();
                }}
              >
                <Space direction="vertical" size={16} style={{ width: '100%' }}>
                  <Space direction="vertical" size={6} style={{ width: '100%' }}>
                    <Typography.Text strong>受控设备</Typography.Text>
                    <Select
                      data-testid="real-command-device"
                      value={selectedDeviceId}
                      options={deviceQuery.data.map((device) => ({
                        value: device.deviceId,
                        label: `${device.name} · ${device.type}`,
                      }))}
                      onChange={(deviceId) => {
                        setSelectedDeviceId(deviceId);
                        draftRef.current = { ...draftRef.current, deviceId };
                        idempotencyKeyRef.current = crypto.randomUUID();
                      }}
                      style={{ width: '100%' }}
                    />
                  </Space>
                  <Space direction="vertical" size={6} style={{ width: '100%' }}>
                    <Typography.Text strong>目标设定值</Typography.Text>
                    <Input
                      data-testid="real-command-setpoint"
                      type="number"
                      min={16}
                      max={30}
                      step={0.5}
                      value={setpointC}
                      addonAfter="°C"
                      onChange={(event) => {
                        const value = event.currentTarget.valueAsNumber;
                        if (!Number.isFinite(value)) return;
                        setSetpointC(value);
                        draftRef.current = { ...draftRef.current, setpointC: value };
                        idempotencyKeyRef.current = crypto.randomUUID();
                      }}
                    />
                  </Space>
                  <Button
                    data-testid="real-command-submit"
                    htmlType="submit"
                    type="primary"
                    block
                    loading={createMutation.isPending}
                    disabled={!selectedDeviceId || setpointC < 16 || setpointC > 30}
                  >
                    提交 Command Intent
                  </Button>
                  <Typography.Text type="secondary">Organization、Site、Principal、风险和执行约束均由服务器推导；浏览器不发送这些授权字段。</Typography.Text>
                </Space>
              </form>
            </Card>
          </Col>
          <Col xs={24} xl={14}>
            <Card title="查询 Command" className="command-workbench-card">
              <Space direction="vertical" size={16} style={{ width: '100%' }}>
                <Typography.Text type="secondary">输入 Command ID 可恢复 Demo 相同的详情与状态时间线工作区。</Typography.Text>
                <Input
                  value={lookupCommandId}
                  placeholder="输入 Command ID"
                  prefix={<SearchOutlined />}
                  onChange={(event) => setLookupCommandId(event.currentTarget.value)}
                  onPressEnter={() => {
                    const value = lookupCommandId.trim();
                    if (value) publishCommandId(value);
                  }}
                />
                <Button
                  icon={<SearchOutlined />}
                  disabled={!lookupCommandId.trim()}
                  onClick={() => publishCommandId(lookupCommandId.trim())}
                >
                  查询权威状态
                </Button>
              </Space>
            </Card>
          </Col>
        </Row>
      ) : null}

      {createMutation.isError ? (
        <div className="real-shell-problem" role="alert">
          <strong>Command 提交失败</strong>
          <span>{commandErrorMessage(createMutation.error)}</span>
          <small>使用相同表单重试时会沿用同一 Idempotency-Key，避免创建重复 Intent。</small>
        </div>
      ) : null}

      {commandId && currentCommandQuery.isPending ? <div className="real-shell-progress" role="status">正在读取权威 Command 状态…</div> : null}
      {currentCommandQuery.isError ? (
        <div className="real-shell-problem" role="alert">
          <strong>无法读取 Command</strong>
          <span>{commandErrorMessage(currentCommandQuery.error)}</span>
        </div>
      ) : null}

      {command && projection ? (
        <article className="real-commands__detail" data-testid="real-command-detail" data-command-status={command.status}>
          <Space direction="vertical" size={16} className="command-detail-stack">
            {projection.outcomeWarning ? (
              <Alert type="warning" showIcon message="设备结果待确认" description={projection.outcomeWarning} />
            ) : null}
            <Card
              title="Command 权威状态"
              extra={projection.canApprove ? (
                <Button
                  data-testid="real-command-approve"
                  type="primary"
                  icon={<CheckCircleOutlined />}
                  loading={approveMutation.isPending}
                  onClick={() => approveMutation.mutate(command.commandId)}
                >
                  批准 Command
                </Button>
              ) : <Tag>{projection.statusLabel}</Tag>}
            >
              <Typography.Paragraph type="secondary">{statusDescription(command)}</Typography.Paragraph>
              <Descriptions column={{ xs: 1, sm: 2, xl: 3 }} bordered size="small" aria-label="Command authoritative projection">
                <Descriptions.Item label="Command ID" span={2}><Typography.Text copyable>{command.commandId}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="状态"><Tag>{projection.statusLabel}</Tag></Descriptions.Item>
                <Descriptions.Item label="Site ID"><Typography.Text copyable>{command.siteId}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="Device ID"><Typography.Text copyable>{command.deviceId}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="目标设定值">{command.setpointC.toFixed(1)} °C</Descriptions.Item>
                <Descriptions.Item label="风险"><Tag color={command.risk === 'HIGH' ? 'red' : command.risk === 'MEDIUM' ? 'gold' : 'green'}>{command.risk}</Tag></Descriptions.Item>
                <Descriptions.Item label="审批">{command.approvalCount} / {command.requiredApprovalCount} · {command.approvalPolicy}</Descriptions.Item>
                <Descriptions.Item label="S2 Snapshot Revision">{command.snapshotRevision}</Descriptions.Item>
                <Descriptions.Item label="Command Version">{command.version}</Descriptions.Item>
              </Descriptions>
            </Card>
            {approveMutation.isError ? <Alert type="error" showIcon message={commandErrorMessage(approveMutation.error)} /> : null}
            <Card title={<span id="real-command-timeline-title">状态时间线</span>} className="command-timeline-card">
              <Timeline
                items={command.transitions.map((transition) => ({
                  color: transition.toStatus === 'SUCCEEDED' ? 'green' : transition.toStatus === 'OUTCOME_UNKNOWN' ? 'orange' : 'blue',
                  children: (
                    <div className="command-timeline-item">
                      <Space wrap>
                        <Tag>{commandStatusLabel(transition.toStatus)}</Tag>
                        <Typography.Text strong>{transition.reason}</Typography.Text>
                        <Tag>{transition.actorType}</Tag>
                      </Space>
                      <Typography.Text type="secondary">v{transition.version} · {formatInstant(transition.occurredAt, site.timezone)}</Typography.Text>
                    </div>
                  ),
                }))}
              />
            </Card>
          </Space>
        </article>
      ) : null}
        </Space>
      </PageScaffold>
    </section>
  );
}
