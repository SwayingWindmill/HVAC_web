import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { RefreshCw, ShieldAlert, Terminal } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { formatClock, LiveIndicator, ReadingValue } from '@/features/operations/realtime/device-presentation';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { useNow } from '@/hooks/use-now';
import {
  approveCommand,
  cancelCommand,
  CommandRequestError,
  commandKeys,
  reconcileCommand,
  siteCommandsQuery,
  WITHDRAWABLE,
  type Command,
  type ReconciledOutcome,
} from '../command-api';
import { commandSummary, IN_FLIGHT, needsPerson, STATUS_PRESENTATION } from '../command-presentation';
import { commandLabels, controlTargets, hasEmergencyRelease, plantUnits, type ControlTarget, type PlantUnit } from '../control-model';
import { activeLocks, activePriorityLocks, controlAlarmDeadLettersQuery, siteControlStateQuery, type SiteControlMode } from '../control-state';
import { siteAutoControlLeaseQuery } from '@/features/optimization/auto-control-api';
import { CommandLog, CommandSheet, StepUpDialog, type CommandActions } from './CommandLog';
import { commandFailure, ConfirmCommand, type PendingCommand } from './CommandConfirmation';
import { ControlAlarmDeadLettersDialog } from './ControlAlarmDeadLettersDialog';
import { AssetLockDialog, ControlModeBar, ControlModeDialog, type LockSubject } from './ControlBoundary';
import { PlantUnitGrid, type PlantContext } from './PlantUnitGrid';
import { PriorityLockDialog } from './PriorityLockDialog';
import { SiteShutdownDialog, type ShutdownSubject } from './SiteShutdown';
import { SettingsDialog } from './SettingsDialog';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/control');

type LogFilter = 'approval' | 'inflight' | 'unknown';

const FILTERS: readonly { readonly value: LogFilter | 'all'; readonly label: string; readonly matches: (command: Command) => boolean }[] = [
  { value: 'all', label: '全部', matches: () => true },
  { value: 'approval', label: '待审批', matches: (command) => command.status === 'AWAITING_APPROVAL' },
  { value: 'inflight', label: '执行中', matches: (command) => IN_FLIGHT.has(command.status) },
  { value: 'unknown', label: '待现场确认', matches: (command) => command.status === 'OUTCOME_UNKNOWN' },
];

export function ControlWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/control' });
  const queryClient = useQueryClient();
  const now = useNow(60_000);
  const csrfToken = principal.session.csrfToken;
  const myId = principal.principalId;
  const canManage = principal.authorization.capabilities.includes('schedule.manage');
  const { plant, registry, refresh, mode: liveMode } = useRealtimePlant();
  const commands = useQuery(siteCommandsQuery(site.id));
  const controlState = useQuery(siteControlStateQuery(site.id));
  const autoControlLease = useQuery({
    ...siteAutoControlLeaseQuery(site.id),
    enabled: principal.authorization.capabilities.includes('site.read'),
  });
  const [pending, setPending] = useState<PendingCommand | null>(null);
  const [modeTarget, setModeTarget] = useState<SiteControlMode | null>(null);
  const [lockTarget, setLockTarget] = useState<ControlTarget | null>(null);
  const [priorityLockTarget, setPriorityLockTarget] = useState<ControlTarget | null>(null);
  const [deadLettersOpen, setDeadLettersOpen] = useState(false);
  const [settingsTarget, setSettingsTarget] = useState<ControlTarget | null>(null);
  const [stepUp, setStepUp] = useState<Command | null>(null);
  const [shutdown, setShutdown] = useState<ShutdownSubject | null>(null);

  const deadLetters = useQuery({
    ...controlAlarmDeadLettersQuery(site.id),
    enabled: Boolean(site.id),
  });
  const model = registry.data?.assetModel;
  const targets = useMemo(() => (model ? controlTargets(model, new Date(now)) : []), [model, now]);
  const grouped = useMemo(() => (model ? plantUnits(targets, model, new Date(now)) : { units: [], others: [] }), [targets, model, now]);
  const labels = useMemo(() => commandLabels(targets), [targets]);
  const devices = useMemo(() => new Map(plant.devices.map((device) => [device.deviceId, device])), [plant.devices]);
  const deviceNames = useMemo(
    () => new Map((model?.devices ?? []).map((device) => [device.id, device.displayName])),
    [model?.devices],
  );
  const locks = useMemo(() => activeLocks(controlState.data), [controlState.data]);
  const priorityLocks = useMemo(() => activePriorityLocks(controlState.data, now), [controlState.data, now]);
  const list = commands.data ?? [];
  const mode = controlState.data?.controlMode ?? 'REMOTE_AUTO';

  const settle = () => queryClient.invalidateQueries({ queryKey: commandKeys.site(site.id) });
  const describe = (command: Command) => {
    const summary = commandSummary(command, labels);
    return `${summary.target} · ${summary.title}`;
  };
  const approve = useMutation({
    mutationFn: (command: Command) => approveCommand(command.commandId, csrfToken),
    onSuccess: () => { toast.success('已批准'); void settle(); },
    onError: (error, command) => {
      if (error instanceof CommandRequestError && error.code === 'STEP_UP_REQUIRED') { setStepUp(command); return; }
      commandFailure(error);
      void settle();
    },
  });
  const cancel = useMutation({
    mutationFn: (command: Command) => cancelCommand(command.commandId, undefined, csrfToken),
    onSuccess: () => { toast.success('已撤回，不会下发'); void settle(); },
    onError: (error) => { commandFailure(error); void settle(); },
  });
  const reconcile = useMutation({
    mutationFn: ({ commandId, outcome }: { commandId: string; outcome: ReconciledOutcome }) => reconcileCommand(commandId, outcome, csrfToken),
    onSuccess: () => { toast.success('已记录现场确认结果'); void settle(); },
    onError: commandFailure,
  });
  const actions: CommandActions = {
    busy: approve.isPending || cancel.isPending || reconcile.isPending,
    canApprove: mode === 'REMOTE_AUTO',
    onApprove: (command) => approve.mutate(command),
    onCancel: (command) => cancel.mutate(command),
    onReconcile: (commandId, outcome) => reconcile.mutate({ commandId, outcome }),
  };

  const setSearch = (next: { status?: LogFilter; inspect?: string }) => void navigate({ search: (previous) => ({ ...previous, ...next }), replace: true });
  const inspect = (commandId: string | undefined) => setSearch({ inspect: commandId });
  const filter = FILTERS.find((candidate) => candidate.value === (search.status ?? 'all'))!;

  const interlockFacts = useMemo(() => {
    const deviceIdByCode = new Map((model?.devices ?? []).map((device) => [device.code, device.id]));
    const targetByDevice = new Map(targets.map((target) => [target.deviceId, target]));
    const runStateByDeviceCode = new Map<string, string>();
    const nameByDeviceCode = new Map<string, string>();
    for (const [code, deviceId] of deviceIdByCode) {
      const device = devices.get(deviceId);
      if (device) runStateByDeviceCode.set(code, device.runState);
      const target = targetByDevice.get(deviceId);
      if (target) nameByDeviceCode.set(code, target.assetType === 'CHILLER' ? `${target.assetName}主机` : target.assetName);
    }
    return { runStateByDeviceCode, nameByDeviceCode };
  }, [model, targets, devices]);
  const context: PlantContext = {
    mode, maintenance: controlState.data?.activeMaintenanceWindow ?? null, devices, ...interlockFacts, locks, priorityLocks, nowMs: now, commands: list, canLock: canManage,
    onCommand: setPending, onLock: setLockTarget, onPriorityLock: setPriorityLockTarget, onAbort: (unit) => setShutdown(abortSubject(unit)), onSettings: setSettingsTarget, onInspect: inspect,
  };

  // A unit is named to the gateway by its chiller's asset code, e.g. chiller-a.
  const abortSubject = (unit: PlantUnit): ShutdownSubject => {
    const points = new Set([unit.chiller, ...unit.members].flatMap((target) => [...target.actions, ...target.settings].map((point) => point.pointId)));
    return {
      kind: 'unit',
      unitCode: model?.assets.find((asset) => asset.id === unit.chiller.assetId)?.code ?? unit.chiller.assetId,
      unitName: unit.chiller.assetName,
      members: unit.members.map((member) => member.assetName),
      withdrawCount: list.filter((command) => WITHDRAWABLE.has(command.status) && points.has(command.pointId)).length,
    };
  };

  const lockSubject: LockSubject | null = lockTarget ? {
    assetId: lockTarget.assetId,
    assetName: lockTarget.assetName,
    lock: locks.get(lockTarget.assetId),
    withdrawable: list
      .filter((command) => WITHDRAWABLE.has(command.status) && [...lockTarget.actions, ...lockTarget.settings].some((point) => point.pointId === command.pointId))
      .map((command) => ({ commandId: command.commandId, text: `${describe(command)}（${STATUS_PRESENTATION[command.status].label}）` })),
  } : null;

  return (
    <Main>
      <PageHeader
        title="运行控制"
        description={site.displayName}
        meta={<LiveIndicator mode={liveMode} />}
        actions={(
          <>
            {plant.latestSampleAt ? <span className="text-xs text-muted-foreground">数据时间 <span className="font-mono">{formatClock(plant.latestSampleAt, site.timezone)}</span></span> : null}
            <Button
              type="button"
              variant={deadLetters.data && deadLetters.data.length > 0 ? 'destructive' : 'outline'}
              size="sm"
              onClick={() => setDeadLettersOpen(true)}
            >
              <ShieldAlert data-icon="inline-start" />
              {deadLetters.data && deadLetters.data.length > 0 ? `控制告警死信 (${deadLetters.data.length})` : '控制告警死信'}
            </Button>
            <Button variant="outline" size="sm" onClick={() => { refresh(); void commands.refetch(); void controlState.refetch(); void deadLetters.refetch(); }}>
              <RefreshCw data-icon="inline-start" />
              刷新
            </Button>
          </>
        )}
      />

      {controlState.isPending ? (
        <Skeleton className="h-[76px] rounded-xl" />
      ) : controlState.isError ? (
        <Panel aria-label="控制方式" className="flex-row items-center gap-3 px-4 py-3.5">
          <span className="text-sm font-semibold">控制方式暂时读不到</span>
          <span className="text-xs text-muted-foreground">{controlState.error.message}</span>
          <Button variant="outline" size="sm" className="ml-auto" onClick={() => void controlState.refetch()}>重试</Button>
        </Panel>
      ) : (
        <ControlModeBar
          state={controlState.data}
          lockCount={locks.size}
          pendingCount={list.filter(needsPerson).length}
          myId={myId}
          timeZone={site.timezone}
          canManage={canManage}
          autoControlLease={autoControlLease.data}
          onPick={setModeTarget}
          onShutdown={() => setShutdown({ kind: 'site', emergencyAvailable: model ? hasEmergencyRelease(model) : false, withdrawCount: list.filter((command) => WITHDRAWABLE.has(command.status)).length })}
        />
      )}

      <div className="grid grid-cols-[minmax(0,1fr)_352px] items-start gap-4">
        <Panel aria-label="机组与设备">
          <PanelHeader
            title={<span className="flex items-center gap-1.5">机组与设备<InfoTip>水路联锁：对应水泵和冷却塔都在运行才能开主机；主机停了才能停对应水泵和冷却塔。中高风险的指令要审批后才会下发。</InfoTip></span>}
            action={plant.totalPower ? (
              <span className="flex items-baseline gap-1.5"><span className="text-xs text-muted-foreground">冷站总功率</span><ReadingValue reading={plant.totalPower} className="text-base font-semibold" /></span>
            ) : undefined}
          />
          {registry.isPending ? (
            <div className="grid grid-cols-4 gap-4 p-4"><Skeleton className="h-64" /><Skeleton className="h-64" /><Skeleton className="h-64" /><Skeleton className="h-64" /></div>
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
            <PlantUnitGrid units={grouped.units} others={grouped.others} context={context} />
          )}
        </Panel>

        <Panel aria-label="指令记录" className="sticky top-4 max-h-[calc(100svh-var(--header-height)-140px)]">
          <PanelHeader title="指令记录" caption="近 50 条" />
          <ToggleGroup
            type="single"
            value={filter.value}
            onValueChange={(value) => {
              const matched = FILTERS.find((option) => option.value === value);
              if (matched) setSearch({ status: matched.value === 'all' ? undefined : matched.value });
            }}
            aria-label="指令状态"
            className="h-10 w-full shrink-0 justify-start gap-5 rounded-none border-b border-divider px-4"
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
                commands={list.filter(filter.matches)}
                labels={labels}
                timeZone={site.timezone}
                selectedId={search.inspect}
                filtered={filter.value !== 'all'}
                actions={actions}
                onInspect={inspect}
              />
            )}
          </div>
        </Panel>
      </div>

      <ConfirmCommand pending={pending} siteId={site.id} csrfToken={csrfToken} onClose={() => setPending(null)} />
      <SettingsDialog
        target={settingsTarget}
        device={settingsTarget ? devices.get(settingsTarget.deviceId) : undefined}
        timeZone={site.timezone}
        onClose={() => setSettingsTarget(null)}
        onCommand={setPending}
      />
      {controlState.data ? (
        <ControlModeDialog
          siteId={site.id}
          csrfToken={csrfToken}
          state={controlState.data}
          initial={modeTarget}
          commands={list}
          describe={describe}
          onClose={() => setModeTarget(null)}
          onShowUnsettled={() => { setModeTarget(null); setSearch({ status: 'unknown' }); }}
        />
      ) : null}
      {controlState.data ? (
        <SiteShutdownDialog siteId={site.id} csrfToken={csrfToken} state={controlState.data} subject={shutdown} onClose={() => setShutdown(null)} />
      ) : null}
      <AssetLockDialog siteId={site.id} csrfToken={csrfToken} subject={lockSubject} myId={myId} timeZone={site.timezone} onClose={() => setLockTarget(null)} />
      <PriorityLockDialog
        target={priorityLockTarget}
        activeLock={priorityLockTarget ? priorityLocks.get(priorityLockTarget.deviceId) : undefined}
        siteId={site.id}
        csrfToken={csrfToken}
        timeZone={site.timezone}
        nowMs={now}
        onClose={() => setPriorityLockTarget(null)}
      />
      <ControlAlarmDeadLettersDialog
        open={deadLettersOpen}
        siteId={site.id}
        canManage={canManage}
        csrfToken={csrfToken}
        deviceNames={deviceNames}
        timeZone={site.timezone}
        onClose={() => setDeadLettersOpen(false)}
      />
      <StepUpDialog command={stepUp} describe={describe} onClose={() => setStepUp(null)} />
      <CommandSheet
        command={list.find((command) => command.commandId === search.inspect)}
        labels={labels}
        timeZone={site.timezone}
        actions={actions}
        onClose={() => inspect(undefined)}
      />
    </Main>
  );
}
