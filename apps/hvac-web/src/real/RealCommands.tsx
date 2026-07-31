import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

const COMMAND_QUERY_PARAMETER = 'command';
const DEFAULT_SETPOINT_C = 24;

function commandFromLocation(): string {
  return new URLSearchParams(globalThis.location.search).get(COMMAND_QUERY_PARAMETER) ?? '';
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
      <p className="real-shell-eyebrow">REAL MODE · SITE COMMANDS</p>
      <FocusHeading>设备命令</FocusHeading>
      <div className="real-commands__boundary" role="status">
        <strong>生产控制保持禁用</strong>
        <p>Command 公共路由已登记，但 Route Ownership Registry 仍为 disabled，生产流量为 0%。此页面不会向设备发送控制指令。</p>
      </div>
      <dl className="real-commands__facts" aria-label="Command authority boundary">
        <div><dt>Site</dt><dd>{site.displayName}</dd></div>
        <div><dt>Registry Site ID</dt><dd>{site.id}</dd></div>
        <div><dt>Acting Organization</dt><dd>{principal.context.actingOrganizationId}</dd></div>
        <div><dt>权威领域</dt><dd>Platform Gateway → Command Service</dd></div>
        <div><dt>生产流量</dt><dd>0%</dd></div>
        <div><dt>设备控制</dt><dd>未启用</dd></div>
      </dl>
      <p className="real-commands__honesty">此状态不是权限拒绝、设备离线或命令失败；它表示生产命令能力尚未完成激活和认证。</p>
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
  registerUnsavedDraft,
  registerProtectedResource,
}: RealCommandsProps) {
  const queryClient = useQueryClient();
  const organizationId = principal.context.actingOrganizationId;
  const queryPrefix = useMemo(() => commandQueryPrefix(organizationId, site.id), [organizationId, site.id]);
  const [selectedDeviceId, setSelectedDeviceId] = useState('');
  const [setpointC, setSetpointC] = useState(DEFAULT_SETPOINT_C);
  const [commandId, setCommandId] = useState(commandFromLocation);
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
    const parameters = new URLSearchParams(globalThis.location.search);
    parameters.set(COMMAND_QUERY_PARAMETER, nextCommandId);
    globalThis.history.pushState(null, '', `${globalThis.location.pathname}?${parameters.toString()}${globalThis.location.hash}`);
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
      <header className="real-commands__header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · SITE COMMANDS</p>
          <FocusHeading>设备命令</FocusHeading>
          <p>{site.displayName} · {site.code} · {site.timezone}</p>
        </div>
        <span className="real-commands__local-marker">LOCAL / NON-FORMAL / PRODUCTION DISABLED</span>
      </header>

      <div className="real-commands__boundary" role="status">
        <strong>本地 S3 集成环境</strong>
        <p>此工作台只连接本机受控 Gateway 和虚拟设备。提交表示 Command Intent 被服务器接收，不表示设备已经成功执行。</p>
      </div>

      {deviceQuery.isPending ? <div className="real-shell-progress" role="status">正在读取当前 Site 的受控设备目录…</div> : null}
      {deviceQuery.isError ? (
        <div className="real-shell-problem" role="alert">
          <strong>设备目录不可用</strong>
          <span>{commandErrorMessage(deviceQuery.error)}</span>
        </div>
      ) : null}

      {deviceQuery.data ? (
        <form
          className="real-commands__composer"
          data-testid="real-command-draft"
          onSubmit={(event) => {
            event.preventDefault();
            if (selectedDeviceId) createMutation.mutate();
          }}
        >
          <h2>提交温度设定 Command</h2>
          <label>
            受控设备
            <select
              data-testid="real-command-device"
              value={selectedDeviceId}
              onChange={(event) => {
                const deviceId = event.currentTarget.value;
                setSelectedDeviceId(deviceId);
                draftRef.current = { ...draftRef.current, deviceId };
                idempotencyKeyRef.current = crypto.randomUUID();
              }}
            >
              {deviceQuery.data.map((device) => (
                <option key={device.deviceId} value={device.deviceId}>{device.name} · {device.type}</option>
              ))}
            </select>
          </label>
          <label>
            目标设定值
            <span className="real-commands__number-field">
              <input
                data-testid="real-command-setpoint"
                type="number"
                min={16}
                max={30}
                step={0.5}
                value={setpointC}
                onChange={(event) => {
                  const value = event.currentTarget.valueAsNumber;
                  if (!Number.isFinite(value)) return;
                  setSetpointC(value);
                  draftRef.current = { ...draftRef.current, setpointC: value };
                  idempotencyKeyRef.current = crypto.randomUUID();
                }}
              />
              <span>°C</span>
            </span>
          </label>
          <button
            data-testid="real-command-submit"
            type="submit"
            disabled={!selectedDeviceId || createMutation.isPending || setpointC < 16 || setpointC > 30}
          >
            {createMutation.isPending ? '提交中…' : '提交 Command Intent'}
          </button>
          <p>Organization、Site、Principal、风险和执行约束均由服务器推导；浏览器不发送这些授权字段。</p>
        </form>
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
          <div className="real-commands__detail-heading">
            <div>
              <h2>Command 状态</h2>
              <p>{statusDescription(command)}</p>
            </div>
            <strong>{projection.statusLabel}</strong>
          </div>
          {projection.outcomeWarning ? <div className="real-commands__warning" role="alert">{projection.outcomeWarning}</div> : null}
          <dl className="real-commands__facts" aria-label="Command authoritative projection">
            <div><dt>Command ID</dt><dd><code>{command.commandId}</code></dd></div>
            <div><dt>Site ID</dt><dd><code>{command.siteId}</code></dd></div>
            <div><dt>Device ID</dt><dd><code>{command.deviceId}</code></dd></div>
            <div><dt>目标设定值</dt><dd>{command.setpointC.toFixed(1)} °C</dd></div>
            <div><dt>风险</dt><dd>{command.risk}</dd></div>
            <div><dt>审批</dt><dd>{command.approvalCount} / {command.requiredApprovalCount} · {command.approvalPolicy}</dd></div>
            <div><dt>S2 Snapshot Revision</dt><dd>{command.snapshotRevision}</dd></div>
            <div><dt>Command Version</dt><dd>{command.version}</dd></div>
          </dl>
          {projection.canApprove ? (
            <button
              data-testid="real-command-approve"
              type="button"
              disabled={approveMutation.isPending}
              onClick={() => approveMutation.mutate(command.commandId)}
            >
              {approveMutation.isPending ? '审批中…' : '批准 Command'}
            </button>
          ) : null}
          {approveMutation.isError ? <div className="real-shell-problem" role="alert">{commandErrorMessage(approveMutation.error)}</div> : null}

          <section className="real-commands__timeline" aria-labelledby="real-command-timeline-title">
            <h3 id="real-command-timeline-title">状态时间线</h3>
            <ol>
              {command.transitions.map((transition) => (
                <li key={transition.version}>
                  <strong>{commandStatusLabel(transition.toStatus)}</strong>
                  <span>{transition.reason}</span>
                  <small>{transition.actorType} · v{transition.version} · {formatInstant(transition.occurredAt, site.timezone)}</small>
                </li>
              ))}
            </ol>
          </section>
        </article>
      ) : null}
    </section>
  );
}
