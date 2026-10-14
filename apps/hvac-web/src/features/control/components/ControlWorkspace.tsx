import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { Link2, Minus, Play, Plus, RefreshCw, RotateCcw, Send, Square, Terminal } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { StatusBadge } from '@/components/common/StatusBadge';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { ButtonGroup } from '@/components/ui/button-group';
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput, InputGroupText } from '@/components/ui/input-group';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
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
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { assetsAssetTypeLabel } from '@/features/assets/model';
import { deviceStatus, formatClock, LiveIndicator, ReadingValue } from '@/features/operations/realtime/device-presentation';
import type { PlantDevice, PlantReading } from '@/features/operations/realtime/plant-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { useNow } from '@/hooks/use-now';
import { formatCompactTime, formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  approveCommand,
  commandKeys,
  reconcileCommand,
  SETTLED,
  siteCommandsQuery,
  type Command,
  type ReconciledOutcome,
} from '../command-api';
import { APPROVAL_LABEL, commandSummary, IN_FLIGHT, RISK_LABEL, STATUS_PRESENTATION } from '../command-presentation';
import { commandLabels, controlTargets, type CommandLabel, type ControlAction, type ControlSetting, type ControlTarget } from '../control-model';
import { CommandMark } from './CommandMark';
import { commandFailure, ConfirmCommand, useSubmitCommand, type PendingCommand } from './CommandConfirmation';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/control');

const ACTION_ICON = { START: Play, STOP: Square, RESET_FAULT: RotateCcw } as const;
const ACTION_SHORT = { START: '启动', STOP: '停止', RESET_FAULT: '复位故障' } as const;

type LogFilter = 'approval' | 'inflight' | 'unknown';

const FILTERS: readonly { readonly value: LogFilter | 'all'; readonly label: string; readonly matches: (command: Command) => boolean }[] = [
  { value: 'all', label: '全部', matches: () => true },
  { value: 'approval', label: '待审批', matches: (command) => command.status === 'AWAITING_APPROVAL' },
  { value: 'inflight', label: '执行中', matches: (command) => IN_FLIGHT.has(command.status) },
  { value: 'unknown', label: '待现场确认', matches: (command) => command.status === 'OUTCOME_UNKNOWN' },
];

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
      onSuccess: () => { toast.success('已记录现场确认结果'); void settle(); },
      onError: commandFailure,
    }),
  };
}

const formatRange = (setting: ControlSetting) => {
  const digits = setting.step < 1 ? 1 : 0;
  return `${formatDecimal(setting.minimum, 1)} – ${formatDecimal(setting.maximum, 1)}${setting.unit ? ` ${setting.unit}` : ''} · 步进 ${formatDecimal(setting.step, digits)}`;
};

/** The newest command on a point that has not settled yet, if any. */
const openCommandOn = (commands: readonly Command[], pointIds: readonly string[]) =>
  commands.find((command) => pointIds.includes(command.pointId) && !SETTLED.has(command.status));

function OpenCommand({ command, labels, onInspect }: {
  readonly command: Command | undefined;
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly onInspect: (commandId: string) => void;
}) {
  if (!command) return <span className="text-muted-foreground">—</span>;
  const value = Object.values(command.parameters)[0];
  const unit = labels.get(command.pointId)?.unit;
  return (
    <button
      type="button"
      onClick={() => onInspect(command.commandId)}
      className="inline-flex items-center gap-1.5 rounded-sm text-xs whitespace-nowrap outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring-soft"
    >
      <CommandMark status={command.status} />
      {value !== undefined ? <span className="font-medium tabular-nums">→ {formatDecimal(value, 1)}{unit ? ` ${unit}` : ''}</span> : <span className="font-medium">{labels.get(command.pointId)?.label}</span>}
      <span className="text-muted-foreground">{STATUS_PRESENTATION[command.status].label}</span>
    </button>
  );
}

function SettingRow({ setting, reading, open, labels, onInspect, onSubmit }: {
  readonly setting: ControlSetting;
  readonly reading: PlantReading | null;
  readonly open: Command | undefined;
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly onInspect: (commandId: string) => void;
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
  const submit = () => { if (valid) onSubmit(value!); };
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell className="pl-12">
        <span className="flex flex-col gap-0.5">
          <span className="text-sm font-medium">{setting.label}</span>
          <span className="text-xs text-muted-foreground">可设 {formatRange(setting)}</span>
        </span>
      </TableCell>
      <TableCell className="text-right text-sm font-semibold">
        <ReadingValue reading={reading} />
      </TableCell>
      <TableCell>
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
            onKeyDown={(event) => { if (event.key === 'Enter') submit(); }}
            className="text-center tabular-nums"
          />
          <InputGroupAddon align="inline-end">
            {setting.unit ? <InputGroupText>{setting.unit}</InputGroupText> : null}
            <InputGroupButton size="icon-xs" aria-label="增大" onClick={() => nudge(1)}><Plus /></InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
      </TableCell>
      <TableCell><OpenCommand command={open} labels={labels} onInspect={onInspect} /></TableCell>
      <TableCell className="pr-4 text-right">
        <Button size="sm" variant={valid ? 'default' : 'outline'} disabled={!valid} onClick={submit}>
          <Send data-icon="inline-start" />
          下发
        </Button>
      </TableCell>
    </TableRow>
  );
}

/** One controllable asset: its state and start/stop/reset on a header row, then a row per setpoint. */
function TargetRows({ target, typeLabel, device, commands, labels, timeZone, onInspect, onCommand }: {
  readonly target: ControlTarget;
  readonly typeLabel: string;
  readonly device: PlantDevice | undefined;
  readonly commands: readonly Command[];
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly timeZone: string;
  readonly onInspect: (commandId: string) => void;
  readonly onCommand: (pending: PendingCommand) => void;
}) {
  const status = device ? deviceStatus(device) : null;
  const statusFacts = [{ label: '设备状态', value: status ? `${status.label}${device?.connection === 'ONLINE' ? ' · 通信在线' : ''}` : '暂无实时数据' }];
  const act = (action: ControlAction) => onCommand({ target, label: action.label, pointId: action.pointId, parameters: {}, facts: statusFacts });
  const openAction = openCommandOn(commands, target.actions.map((action) => action.pointId));
  return (
    <TableBody aria-label={target.assetName} className="border-b border-divider last:border-b-0">
      <TableRow className="bg-muted/50 hover:bg-muted/50">
        <TableCell colSpan={5} className="px-4 py-2.5">
          <div className="flex items-center gap-3">
            <span className="text-sm font-semibold">{target.assetName}</span>
            <span className="text-xs text-muted-foreground">{typeLabel}</span>
            {status ? <StatusBadge tone={status.tone} label={status.label} /> : <span className="text-xs text-muted-foreground">暂无实时数据</span>}
            {openAction ? <OpenCommand command={openAction} labels={labels} onInspect={onInspect} /> : null}
            {target.actions.length > 0 ? (
              <ButtonGroup aria-label={`${target.assetName} 启停与复位`} className="ml-auto">
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
          </div>
        </TableCell>
      </TableRow>
      {target.settings.map((setting) => {
        const reading = setting.feedbackSourceKey && device ? device.reading(setting.feedbackSourceKey) : null;
        return (
          <SettingRow
            key={setting.pointId}
            setting={setting}
            reading={reading}
            open={openCommandOn(commands, [setting.pointId])}
            labels={labels}
            onInspect={onInspect}
            onSubmit={(value) => onCommand({
              target, label: setting.label, pointId: setting.pointId, parameters: { [setting.parameterKey]: value },
              change: { from: reading, to: value, unit: setting.unit },
              facts: [
                ...statusFacts,
                { label: '可设范围', value: formatRange(setting) },
                { label: '当前读数时间', value: formatClock(reading?.sampledAt ?? null, timeZone) },
              ],
            })}
          />
        );
      })}
    </TableBody>
  );
}

function approvalText(command: Command) {
  return command.requiredApprovalCount > 0 ? `${APPROVAL_LABEL[command.approvalPolicy]} · ${command.approvalCount}/${command.requiredApprovalCount}` : APPROVAL_LABEL[command.approvalPolicy];
}

function ReconcileButtons({ commandId, busy, onReconcile }: {
  readonly commandId: string;
  readonly busy: boolean;
  readonly onReconcile: (commandId: string, outcome: ReconciledOutcome) => void;
}) {
  return (
    <ButtonGroup aria-label="现场确认执行结果">
      <Button size="sm" variant="outline" disabled={busy} onClick={() => onReconcile(commandId, 'APPLIED')}>已生效</Button>
      <Button size="sm" variant="outline" disabled={busy} onClick={() => onReconcile(commandId, 'NOT_APPLIED')}>未生效</Button>
    </ButtonGroup>
  );
}

function CommandLog({ commands, labels, timeZone, selectedId, onInspect, onApprove, onReconcile, busy, filtered }: {
  readonly commands: readonly Command[];
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly timeZone: string;
  readonly selectedId: string | undefined;
  readonly onInspect: (commandId: string) => void;
  readonly onApprove: (commandId: string) => void;
  readonly onReconcile: (commandId: string, outcome: ReconciledOutcome) => void;
  readonly busy: boolean;
  readonly filtered: boolean;
}) {
  if (commands.length === 0) {
    return (
      <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
        <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><Terminal className="size-[18px]" aria-hidden="true" /></span>
        <p className="text-sm font-semibold">{filtered ? '没有这类指令' : '还没有指令'}</p>
        <p className="text-xs text-muted-foreground">{filtered ? '切换到「全部」查看其他指令' : '从左侧下发的指令会记在这里'}</p>
      </div>
    );
  }
  return (
    <ul aria-label="指令记录">
      {commands.map((command) => {
        const summary = commandSummary(command, labels);
        return (
          <li
            key={command.commandId}
            className={cn('grid grid-cols-[12px_minmax(0,1fr)_auto] gap-x-2.5 gap-y-1 border-b border-divider px-4 pt-3 pb-3', command.commandId === selectedId && 'bg-primary-soft')}
          >
            <span className="mt-[5px]"><CommandMark status={command.status} /></span>
            <button type="button" className="min-w-0 truncate text-left text-sm outline-none hover:underline focus-visible:underline" onClick={() => onInspect(command.commandId)}>
              <span className="font-semibold">{summary.target}</span> · {summary.title}
            </button>
            <span className="font-mono text-xs whitespace-nowrap text-subtle-foreground">{formatCompactTime(command.createdAt, timeZone)}</span>
            <span className="col-start-2 col-end-4 text-xs text-muted-foreground">
              {STATUS_PRESENTATION[command.status].label} · {RISK_LABEL[command.risk]} · {command.status === 'OUTCOME_UNKNOWN' ? '设备未返回执行结果' : approvalText(command)}
            </span>
            {command.status === 'AWAITING_APPROVAL' ? (
              <div className="col-start-2 col-end-4 mt-1 flex gap-2">
                <Button size="sm" disabled={busy} onClick={() => onApprove(command.commandId)}>批准</Button>
                <Button size="sm" variant="outline" onClick={() => onInspect(command.commandId)}>查看</Button>
              </div>
            ) : command.status === 'OUTCOME_UNKNOWN' ? (
              <div className="col-start-2 col-end-4 mt-1 flex items-center gap-2">
                <span className="text-xs text-muted-foreground">现场确认：</span>
                <ReconcileButtons commandId={command.commandId} busy={busy} onReconcile={onReconcile} />
              </div>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

function CommandSheet({ command, labels, timeZone, busy, onApprove, onReconcile, onClose }: {
  readonly command: Command | undefined;
  readonly labels: ReadonlyMap<string, CommandLabel>;
  readonly timeZone: string;
  readonly busy: boolean;
  readonly onApprove: (commandId: string) => void;
  readonly onReconcile: (commandId: string, outcome: ReconciledOutcome) => void;
  readonly onClose: () => void;
}) {
  const summary = command ? commandSummary(command, labels) : null;
  const copyLink = () => {
    void navigator.clipboard.writeText(window.location.href).then(() => toast.success('链接已复制'), () => toast.error('无法复制链接'));
  };
  return (
    <Sheet open={Boolean(command)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent className="overflow-y-auto sm:max-w-[440px]">
        {command && summary ? (
          <>
            <SheetHeader className="gap-2 border-b border-divider px-6 pt-5 pb-4">
              <div className="flex items-center gap-2 pr-8 text-xs">
                <span className="inline-flex items-center gap-1.5 font-medium"><CommandMark status={command.status} />{STATUS_PRESENTATION[command.status].label}</span>
                <span className="rounded-md bg-secondary px-1.5 leading-5 text-secondary-foreground">{RISK_LABEL[command.risk]}</span>
                <Button variant="ghost" size="icon-sm" className="ml-auto text-muted-foreground" aria-label="复制链接" onClick={copyLink}><Link2 className="size-3.5" /></Button>
              </div>
              <SheetTitle className="text-lg">{summary.title}</SheetTitle>
              <SheetDescription>{summary.target} · 提交于 <span className="font-mono">{formatTime(command.createdAt, timeZone)}</span></SheetDescription>
            </SheetHeader>
            <div className="flex flex-col">
              <section className="flex flex-col gap-3 border-b border-divider px-6 py-4">
                <h3 className="text-sm font-semibold">概况</h3>
                <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
                  <div className="flex flex-col gap-0.5"><dt className="text-xs text-muted-foreground">状态</dt><dd className="font-medium">{STATUS_PRESENTATION[command.status].label}</dd></div>
                  <div className="flex flex-col gap-0.5"><dt className="text-xs text-muted-foreground">风险</dt><dd className="font-medium">{RISK_LABEL[command.risk]}</dd></div>
                  <div className="flex flex-col gap-0.5"><dt className="text-xs text-muted-foreground">审批</dt><dd className="font-medium">{approvalText(command)}</dd></div>
                  <div className="flex flex-col gap-0.5"><dt className="text-xs text-muted-foreground">提交时间</dt><dd className="font-mono font-medium">{formatTime(command.createdAt, timeZone)}</dd></div>
                </dl>
              </section>
              {command.status === 'AWAITING_APPROVAL' ? (
                <section className="flex flex-col items-start gap-2.5 border-b border-divider px-6 py-4">
                  <h3 className="text-sm font-semibold">审批</h3>
                  <p className="text-sm leading-[22px] text-secondary-foreground">
                    {command.risk === 'HIGH' ? '高' : '中'}风险指令要{command.requiredApprovalCount > 1 ? `${command.requiredApprovalCount} 位` : '一位'}有审批权限的人员批准后才会下发。批准不等于已执行，是否执行成功以设备返回的结果为准。
                  </p>
                  <Button size="sm" disabled={busy} onClick={() => onApprove(command.commandId)}>批准</Button>
                </section>
              ) : command.status === 'OUTCOME_UNKNOWN' ? (
                <section className="flex flex-col items-start gap-2.5 border-b border-divider px-6 py-4">
                  <h3 className="text-sm font-semibold">现场确认</h3>
                  <p className="text-sm leading-[22px] text-secondary-foreground">设备没有返回执行结果。请到现场查看设备是否已按指令动作，再记录结果。</p>
                  <ReconcileButtons commandId={command.commandId} busy={busy} onReconcile={onReconcile} />
                </section>
              ) : null}
              <section aria-label="指令经过" className="flex flex-col gap-3 px-6 py-4">
                <h3 className="text-sm font-semibold">经过</h3>
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
                          <TimelineTime dateTime={transition.occurredAt} className="font-mono">{formatClock(transition.occurredAt, timeZone)}</TimelineTime>
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
  const { plant, registry, refresh, mode } = useRealtimePlant();
  const commands = useQuery(siteCommandsQuery(site.id));
  const actions = useCommandActions(site.id, principal.session.csrfToken);
  const [pending, setPending] = useState<PendingCommand | null>(null);

  const targets = useMemo(
    () => (registry.data ? controlTargets(registry.data.assetModel, new Date(now)) : []),
    [registry.data, now],
  );
  const labels = useMemo(() => commandLabels(targets), [targets]);
  const devices = useMemo(() => new Map(plant.devices.map((device) => [device.deviceId, device])), [plant.devices]);
  const assetTypes = useMemo(() => new Map(registry.data?.assetModel.assets.map((asset) => [asset.id, assetsAssetTypeLabel(asset.assetType)]) ?? []), [registry.data]);

  const list = commands.data ?? [];
  const filter = FILTERS.find((candidate) => candidate.value === (search.status ?? 'all'))!;
  const shown = list.filter(filter.matches);
  const settled = list.filter((command) => command.status === 'SUCCEEDED' || command.status === 'FAILED');
  const succeeded = settled.filter((command) => command.status === 'SUCCEEDED').length;
  const setSearch = (next: { status?: LogFilter; inspect?: string }) => void navigate({ search: (previous) => ({ ...previous, ...next }), replace: true });
  const inspect = (commandId: string | undefined) => setSearch({ inspect: commandId });
  const busy = actions.approve.isPending || actions.reconcile.isPending;
  const approve = (commandId: string) => actions.approve.mutate(commandId);
  const reconcile = (commandId: string, outcome: ReconciledOutcome) => actions.reconcile.mutate({ commandId, outcome });

  return (
    <Main>
      <PageHeader
        title="控制指令"
        description={site.displayName}
        meta={<LiveIndicator mode={mode} />}
        actions={(
          <>
            {plant.latestSampleAt ? <span className="text-xs text-muted-foreground">数据时间 <span className="font-mono">{formatClock(plant.latestSampleAt, site.timezone)}</span></span> : null}
            <Button variant="outline" size="sm" onClick={() => { refresh(); void commands.refetch(); }}>
              <RefreshCw data-icon="inline-start" />
              刷新
            </Button>
          </>
        )}
      />

      <div className="grid items-start gap-[18px] grid-cols-[minmax(0,1fr)_400px]">
        <Panel aria-label="设备控制">
          <PanelHeader title={<span className="flex items-center gap-1.5">设备控制<InfoTip>中高风险的指令要审批后才会下发；设备没返回结果的，由值班人员到现场确认</InfoTip></span>} />
          {registry.isPending ? (
            <div className="flex flex-col gap-2 p-4"><Skeleton className="h-12" /><Skeleton className="h-16" /><Skeleton className="h-12" /><Skeleton className="h-16" /></div>
          ) : registry.isError ? (
            <div className="flex flex-col items-start gap-2 px-4 py-6">
              <p className="text-sm text-destructive-text">设备清单暂时读不到</p>
              <Button variant="outline" size="sm" onClick={() => void registry.refetch()}>重试</Button>
            </div>
          ) : targets.length === 0 ? (
            <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
              <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground"><Terminal className="size-[18px]" aria-hidden="true" /></span>
              <p className="text-sm font-semibold">没有可控制的设备</p>
              <p className="text-xs text-muted-foreground">设备登记了控制点并关联到资产后，会出现在这里</p>
            </div>
          ) : (
            <Table aria-label="设备控制点" className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="pl-4">设备 / 控制点</TableHead>
                  <TableHead className="text-right">当前</TableHead>
                  <TableHead>目标值</TableHead>
                  <TableHead>进行中的指令</TableHead>
                  <TableHead className="pr-4"><span className="sr-only">下发</span></TableHead>
                </TableRow>
              </TableHeader>
              {targets.map((target) => (
                <TargetRows
                  key={target.assetId}
                  target={target}
                  typeLabel={assetTypes.get(target.assetId) ?? ''}
                  device={devices.get(target.deviceId)}
                  commands={list}
                  labels={labels}
                  timeZone={site.timezone}
                  onInspect={inspect}
                  onCommand={setPending}
                />
              ))}
            </Table>
          )}
        </Panel>

        <Panel aria-label="指令记录" className="sticky top-4 max-h-[calc(100svh-var(--header-height)-140px)]">
          <PanelHeader
            title="指令记录"
            caption={settled.length > 0 ? `近 50 条成功率 ${formatDecimal((succeeded / settled.length) * 100, 0)}%` : '近 50 条'}
          />
          <ToggleGroup
            type="single"
            value={filter.value}
            onValueChange={(value) => { if (value) setSearch({ status: value === 'all' ? undefined : value as LogFilter }); }}
            aria-label="指令状态"
            className="h-11 w-full shrink-0 justify-start gap-5 rounded-none border-b border-divider px-4"
          >
            {FILTERS.map((option) => (
              <ToggleGroupItem
                key={option.value}
                value={option.value}
                className="h-full flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
              >
                {option.label}
                {commands.isSuccess ? <span className="font-mono text-[11px] text-subtle-foreground">{list.filter(option.matches).length}</span> : null}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {commands.isPending ? (
              <div className="flex flex-col gap-2 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>
            ) : commands.isError ? (
              <div className="flex flex-col items-start gap-2 px-4 py-6">
                <p className="text-sm text-destructive-text">指令记录暂时读不到：{commands.error.message}</p>
                <Button variant="outline" size="sm" onClick={() => void commands.refetch()}>重试</Button>
              </div>
            ) : (
              <CommandLog
                commands={shown}
                labels={labels}
                timeZone={site.timezone}
                selectedId={search.inspect}
                onInspect={inspect}
                onApprove={approve}
                onReconcile={reconcile}
                busy={busy}
                filtered={filter.value !== 'all'}
              />
            )}
          </div>
        </Panel>
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
        busy={busy}
        onApprove={approve}
        onReconcile={reconcile}
        onClose={() => inspect(undefined)}
      />
    </Main>
  );
}
