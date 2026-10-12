import { FactTile } from '@/blocks/fact-list';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { Minus, Play, Plus, RefreshCw, RotateCcw, Send, Square, Terminal } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader } from '@/blocks/page-header';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ButtonGroup } from '@/components/ui/button-group';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput, InputGroupText } from '@/components/ui/input-group';
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemTitle } from '@/components/ui/item';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Status, StatusIndicator, StatusLabel } from '@/components/ui/status';
import {
  Timeline,
  TimelineConnector,
  TimelineContent,
  TimelineDescription,
  TimelineHeader,
  TimelineItem,
  TimelineMarker,
  TimelineRail,
  TimelineTime,
  TimelineTitle,
} from '@/components/ui/timeline';
import { ReadingValue, deviceStatus } from '@/features/operations/realtime/device-presentation';
import type { PlantDevice, PlantReading } from '@/features/operations/realtime/plant-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import {
  approveCommand,
  commandKeys,
  reconcileCommand,
  siteCommandsQuery,
  type Command,
  type ReconciledOutcome,
} from '../command-api';
import { APPROVAL_LABEL, commandSummary, IN_FLIGHT, RISK_LABEL, STATUS_PRESENTATION } from '../command-presentation';
import { commandLabels, controlTargets, type CommandLabel, type ControlAction, type ControlSetting, type ControlTarget } from '../control-model';
import { commandFailure, ConfirmCommand, useSubmitCommand, type PendingCommand } from './CommandConfirmation';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/control');

const ACTION_ICON = { START: Play, STOP: Square, RESET_FAULT: RotateCcw } as const;
const ACTION_SHORT = { START: '启动', STOP: '停止', RESET_FAULT: '复位故障' } as const;

function useCommandActions(siteId: string, csrfToken: string) {
  const queryClient = useQueryClient();
  const settle = () => queryClient.invalidateQueries({ queryKey: commandKeys.site(siteId) });
  return {
    submit: useSubmitCommand(siteId, csrfToken),
    approve: useMutation({
      mutationFn: (commandId: string) => approveCommand(commandId, csrfToken),
      onSuccess: () => { toast.success('已批准'); void settle(); },
      onError: commandFailure,
    }),
    reconcile: useMutation({
      mutationFn: ({ commandId, outcome }: { commandId: string; outcome: ReconciledOutcome }) => reconcileCommand(commandId, outcome, csrfToken),
      onSuccess: () => { toast.success('已记录核对结果'); void settle(); },
      onError: commandFailure,
    }),
  };
}

function SettingControl({ setting, reading, onSubmit }: {
  readonly setting: ControlSetting;
  readonly reading: PlantReading | null;
  readonly onSubmit: (value: number) => void;
}) {
  const [draft, setDraft] = useState('');
  const value = draft === '' ? null : Number(draft);
  const steps = value === null ? 0 : (value - setting.minimum) / setting.step;
  const valid = value !== null && Number.isFinite(value) && value >= setting.minimum && value <= setting.maximum
    && Math.abs(steps - Math.round(steps)) < 1e-6;
  const base = value ?? (reading?.numeric ?? setting.minimum);
  const nudge = (direction: 1 | -1) => {
    const next = Math.min(setting.maximum, Math.max(setting.minimum, base + direction * setting.step));
    setDraft(String(Number(next.toFixed(6))));
  };
  const range = `${formatDecimal(setting.minimum, 1)}–${formatDecimal(setting.maximum, 1)}${setting.unit ? ` ${setting.unit}` : ''}`;
  return (
    <div className="grid gap-3 rounded-lg border bg-muted/40 p-3 @lg/card:grid-cols-[minmax(0,1fr)_auto] @lg/card:items-center">
      <div className="min-w-0">
        <p className="truncate text-sm font-medium">{setting.label}</p>
        <p className="text-xs text-muted-foreground">
          当前 <ReadingValue reading={reading} className="font-medium text-foreground" /> · 可设 {range}
        </p>
      </div>
      <form
        className="flex items-center gap-2"
        onSubmit={(event) => { event.preventDefault(); if (valid) onSubmit(value!); }}
      >
        <InputGroup className="w-44">
          <InputGroupAddon>
            <InputGroupButton size="icon-xs" aria-label="减小" onClick={() => nudge(-1)}><Minus /></InputGroupButton>
          </InputGroupAddon>
          <InputGroupInput
            inputMode="decimal"
            aria-label={`${setting.label}目标值`}
            aria-invalid={draft !== '' && !valid}
            placeholder={reading?.numeric != null ? formatDecimal(reading.numeric, setting.step < 1 ? 1 : 0) : '目标值'}
            value={draft}
            onChange={(event) => setDraft(event.target.value.trim())}
            className="text-center tabular-nums"
          />
          <InputGroupAddon align="inline-end">
            {setting.unit ? <InputGroupText>{setting.unit}</InputGroupText> : null}
            <InputGroupButton size="icon-xs" aria-label="增大" onClick={() => nudge(1)}><Plus /></InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
        <Button type="submit" size="sm" variant={valid ? 'default' : 'outline'} disabled={!valid}>
          <Send data-icon="inline-start" />
          下发
        </Button>
      </form>
    </div>
  );
}

function TargetCard({ target, device, timeZone, onCommand }: {
  readonly target: ControlTarget;
  readonly device: PlantDevice | undefined;
  readonly timeZone: string;
  readonly onCommand: (pending: PendingCommand) => void;
}) {
  const status = device ? deviceStatus(device) : null;
  const act = (action: ControlAction) => onCommand({ target, label: action.label, pointId: action.pointId, parameters: {} });
  return (
    <Card className="@container/card" role="region" aria-label={target.assetName}>
      <CardHeader>
        <CardTitle>{target.assetName}</CardTitle>
        <CardDescription>
          {device?.latestSampleAt ? `数据时间 ${formatTime(device.latestSampleAt, timeZone)}` : '暂无实时数据'}
        </CardDescription>
        {status ? <CardAction><StatusBadge tone={status.tone} label={status.label} /></CardAction> : null}
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {target.actions.length > 0 ? (
          <ButtonGroup aria-label={`${target.assetName} 启停与复位`}>
            {target.actions.map((action) => {
              const Icon = ACTION_ICON[action.capability];
              return (
                <Button key={action.pointId} variant="outline" size="sm" onClick={() => act(action)}>
                  <Icon data-icon="inline-start" />
                  {ACTION_SHORT[action.capability]}
                </Button>
              );
            })}
          </ButtonGroup>
        ) : null}
        {target.settings.map((setting) => {
          const reading = setting.feedbackSourceKey && device ? device.reading(setting.feedbackSourceKey) : null;
          return (
            <SettingControl
              key={setting.pointId}
              setting={setting}
              reading={reading}
              onSubmit={(value) => onCommand({
                target, label: setting.label, pointId: setting.pointId, parameters: { [setting.parameterKey]: value },
                change: { from: reading, to: value, unit: setting.unit },
              })}
            />
          );
        })}
      </CardContent>
    </Card>
  );
}

function CommandStatus({ command }: { readonly command: Command }) {
  const presentation = STATUS_PRESENTATION[command.status];
  return (
    <Status variant={presentation.variant}>
      <StatusIndicator />
      <StatusLabel>{presentation.label}</StatusLabel>
    </Status>
  );
}

function CommandLog({ commands, labels, timeZone, onInspect, onApprove, onReconcile, busy }: {
  readonly commands: readonly Command[];
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly timeZone: string;
  readonly onInspect: (commandId: string) => void;
  readonly onApprove: (commandId: string) => void;
  readonly onReconcile: (commandId: string, outcome: ReconciledOutcome) => void;
  readonly busy: boolean;
}) {
  if (commands.length === 0) {
    return (
      <Empty className="py-10">
        <EmptyHeader>
          <EmptyMedia variant="icon"><Terminal /></EmptyMedia>
          <EmptyTitle>还没有指令</EmptyTitle>
          <EmptyDescription>从左侧设备卡片下发的指令会出现在这里，包括审批和执行结果。</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  return (
    <ItemGroup className="gap-1">
      {commands.map((command) => {
        const summary = commandSummary(command, labels);
        return (
          <div role="listitem" key={command.commandId}>
            <Item size="sm" className="items-start hover:bg-muted/60">
              <ItemContent className="min-w-0 gap-1.5">
                <button type="button" className="text-left" onClick={() => onInspect(command.commandId)}>
                  <ItemTitle className="line-clamp-1">{summary.target} · {summary.title}</ItemTitle>
                </button>
                <ItemDescription className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                  <CommandStatus command={command} />
                  <span>{formatTime(command.createdAt, timeZone)}</span>
                  <span>{RISK_LABEL[command.risk]}</span>
                  {command.requiredApprovalCount > 0 ? <span>审批 {command.approvalCount}/{command.requiredApprovalCount}</span> : null}
                </ItemDescription>
              </ItemContent>
              <ItemActions>
                {command.status === 'AWAITING_APPROVAL' ? (
                  <Button size="sm" disabled={busy} onClick={() => onApprove(command.commandId)}>批准</Button>
                ) : command.status === 'OUTCOME_UNKNOWN' ? (
                  <ButtonGroup aria-label="核对执行结果">
                    <Button size="sm" variant="outline" disabled={busy} onClick={() => onReconcile(command.commandId, 'APPLIED')}>已生效</Button>
                    <Button size="sm" variant="outline" disabled={busy} onClick={() => onReconcile(command.commandId, 'NOT_APPLIED')}>未生效</Button>
                  </ButtonGroup>
                ) : null}
              </ItemActions>
            </Item>
          </div>
        );
      })}
    </ItemGroup>
  );
}

function CommandSheet({ command, labels, timeZone, onClose }: {
  readonly command: Command | undefined;
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly timeZone: string;
  readonly onClose: () => void;
}) {
  const summary = command ? commandSummary(command, labels) : null;
  return (
    <Sheet open={Boolean(command)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent className="overflow-y-auto">
        {command && summary ? (
          <>
            <SheetHeader>
              <SheetTitle>{summary.title}</SheetTitle>
              <SheetDescription>{summary.target}</SheetDescription>
            </SheetHeader>
            <div className="flex flex-col gap-6 px-4 pb-6">
              <dl className="grid grid-cols-2 gap-x-6 gap-y-4 text-sm">
                <FactTile label="状态"><CommandStatus command={command} /></FactTile>
                <FactTile label="风险">{RISK_LABEL[command.risk]}</FactTile>
                <FactTile label="审批">{APPROVAL_LABEL[command.approvalPolicy]}{command.requiredApprovalCount > 0 ? ` · ${command.approvalCount}/${command.requiredApprovalCount}` : ''}</FactTile>
                <FactTile label="提交时间">{formatTime(command.createdAt, timeZone)}</FactTile>
              </dl>
              <section aria-label="指令经过">
                <h3 className="mb-3 text-sm font-medium">经过</h3>
                <Timeline>
                  {command.transitions.map((transition, index) => (
                    <TimelineItem key={transition.version} status={index === command.transitions.length - 1 ? 'current' : 'done'}>
                      <TimelineRail>
                        <TimelineMarker />
                        {index < command.transitions.length - 1 ? <TimelineConnector /> : null}
                      </TimelineRail>
                      <TimelineContent>
                        <TimelineHeader>
                          <TimelineTitle>{STATUS_PRESENTATION[transition.toStatus].label}</TimelineTitle>
                          <TimelineTime dateTime={transition.occurredAt}>{formatTime(transition.occurredAt, timeZone)}</TimelineTime>
                        </TimelineHeader>
                        <TimelineDescription>{transition.actorType === 'PRINCIPAL' ? '人员操作' : '系统处理'}</TimelineDescription>
                      </TimelineContent>
                    </TimelineItem>
                  ))}
                </Timeline>
              </section>
            </div>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

export function ControlWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/control' });
  const now = useNow(60_000);
  const { plant, registry, refresh } = useRealtimePlant();
  const commands = useQuery(siteCommandsQuery(site.id));
  const actions = useCommandActions(site.id, principal.session.csrfToken);
  const [pending, setPending] = useState<PendingCommand | null>(null);

  const targets = useMemo(
    () => (registry.data ? controlTargets(registry.data.assetModel, new Date(now)) : []),
    [registry.data, now],
  );
  const labels = useMemo(() => commandLabels(targets), [targets]);
  const devices = useMemo(() => new Map(plant.devices.map((device) => [device.deviceId, device])), [plant.devices]);

  const list = commands.data ?? [];
  const awaiting = list.filter((command) => command.status === 'AWAITING_APPROVAL').length;
  const inFlight = list.filter((command) => IN_FLIGHT.has(command.status)).length;
  const unknown = list.filter((command) => command.status === 'OUTCOME_UNKNOWN').length;
  const settled = list.filter((command) => ['SUCCEEDED', 'FAILED'].includes(command.status));
  const succeeded = settled.filter((command) => command.status === 'SUCCEEDED').length;
  const inspect = (commandId: string | undefined) => void navigate({ search: (previous) => ({ ...previous, inspect: commandId }), replace: true });
  const busy = actions.approve.isPending || actions.reconcile.isPending;

  return (
    <Main>
      <PageHeader
        title="控制指令"
        description={`${site.displayName} · 指令先经风险评估，中高风险需审批后下发；结果不明的由值班人员核对`}
        actions={
          <Button variant="outline" size="sm" onClick={() => { refresh(); void commands.refetch(); }}>
            <RefreshCw data-icon="inline-start" />
            刷新
          </Button>
        }
      />

      <MetricGrid ariaLabel="指令概况">
        <MetricCard label="待审批" value={<MetricValue value={commands.data ? String(awaiting) : null} unit="条" />} lead={awaiting > 0 ? '等待有审批权限的人员批准' : '没有等待审批的指令'} detail="中高风险指令" />
        <MetricCard label="执行中" value={<MetricValue value={commands.data ? String(inFlight) : null} unit="条" />} lead={inFlight > 0 ? '正在校验或下发到设备' : '没有正在执行的指令'} detail="提交后到设备回执前" />
        <MetricCard label="待核对" value={<MetricValue value={commands.data ? String(unknown) : null} unit="条" />} lead={unknown > 0 ? '设备回执不明，请到现场确认' : '没有结果不明的指令'} detail="核对后记入台账" />
        <MetricCard
          label="执行成功率"
          value={<MetricValue value={settled.length === 0 ? null : formatDecimal((succeeded / settled.length) * 100, 0)} unit="%" />}
          lead={settled.length === 0 ? '还没有已完成的指令' : `已完成 ${settled.length} 条，失败 ${settled.length - succeeded} 条`}
          detail="最近 50 条指令"
        />
      </MetricGrid>

      <div className="grid items-start gap-4 md:gap-6 @5xl/main:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <section aria-label="设备控制" className="grid gap-4 @3xl/main:grid-cols-2">
          {registry.isPending ? (
            Array.from({ length: 4 }, (_, index) => <Skeleton key={index} className="h-56 rounded-xl" />)
          ) : registry.isError ? (
            <p className="col-span-full rounded-xl border py-16 text-center text-sm text-muted-foreground">设备台账暂不可用</p>
          ) : targets.length === 0 ? (
            <Empty className="col-span-full rounded-xl border">
              <EmptyHeader>
                <EmptyMedia variant="icon"><Terminal /></EmptyMedia>
                <EmptyTitle>没有可控制的设备</EmptyTitle>
                <EmptyDescription>设备登记了控制点并关联到资产后，会出现在这里。</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : targets.map((target) => (
            <TargetCard key={target.assetId} target={target} device={devices.get(target.deviceId)} timeZone={site.timezone} onCommand={setPending} />
          ))}
        </section>

        <Card className="@5xl/main:sticky @5xl/main:top-4">
          <CardHeader>
            <CardTitle>指令记录</CardTitle>
            <CardDescription>最近 50 条，执行中的每 3 秒刷新</CardDescription>
          </CardHeader>
          <CardContent>
            {commands.isPending ? <Skeleton className="h-64" /> : commands.isError ? (
              <p className="py-10 text-center text-sm text-muted-foreground">指令记录暂不可用</p>
            ) : (
              <CommandLog
                commands={list}
                labels={labels}
                timeZone={site.timezone}
                onInspect={inspect}
                onApprove={(commandId) => actions.approve.mutate(commandId)}
                onReconcile={(commandId, outcome) => actions.reconcile.mutate({ commandId, outcome })}
                busy={busy}
              />
            )}
          </CardContent>
        </Card>
      </div>

      <ConfirmCommand
        pending={pending}
        submitting={actions.submit.isPending}
        onCancel={() => setPending(null)}
        onConfirm={() => { if (pending) actions.submit.mutate(pending, { onSettled: () => setPending(null) }); }}
      />
      <CommandSheet
        command={list.find((command) => command.commandId === search.inspect)}
        labels={labels}
        timeZone={site.timezone}
        onClose={() => inspect(undefined)}
      />
    </Main>
  );
}
