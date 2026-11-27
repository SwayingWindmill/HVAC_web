import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { GitBranch, TableProperties, Terminal, TriangleAlert } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { FocusHeading } from '@/app/FocusHeading';
import { PanelLink } from '@/blocks/panel';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { buildHydraulicSchematic } from '@/features/operations/realtime/hydraulic-schematic-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { siteAutoControlLeaseQuery } from '@/features/optimization/auto-control-api';
import { useNow } from '@/hooks/use-now';
import {
  approveCommand,
  cancelCommand,
  CommandRequestError,
  commandKeys,
  reconcileCommand,
  SETTLED,
  siteCommandsQuery,
  WITHDRAWABLE,
  type Command,
  type ReconciledOutcome,
} from '../command-api';
import { commandSummary, needsPerson, STATUS_PRESENTATION } from '../command-presentation';
import { isSingleLoopCombined } from '../control-gates';
import { commandLabels, controlTargets, hasEmergencyRelease, plantUnits, type ControlTarget, type PlantUnit } from '../control-model';
import { activeLocks, activePriorityLocks, controlAlarmDeadLettersQuery, siteControlStateQuery, type SiteControlMode } from '../control-state';
import { ActiveCommandChannel } from './ActiveCommandChannel';
import { CommandActionConfirmDialog, type PendingCommandAction } from './CommandActionConfirmDialog';
import { commandFailure, ConfirmCommand, type PendingCommand } from './CommandConfirmation';
import { CommandHistoryDrawer, CommandSheet, StepUpDialog, type CommandActions, type ControlLogFilter } from './CommandLog';
import { AssetLockDialog, ControlModeDialog, type LockSubject } from './ControlBoundary';
import { ControlCockpitStrip } from './ControlCockpitStrip';
import { DeviceFaceplate } from './DeviceFaceplate';
import { PlantSwissTable } from './PlantSwissTable';
import { PlantTopologyCanvas, unitHasAbnormality, type CanvasSharedProps } from './PlantTopologyCanvas';
import { PriorityLockDialog } from './PriorityLockDialog';
import { SiteShutdownDialog, type ShutdownSubject } from './SiteShutdown';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/control');

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
  const deadLetters = useQuery({
    ...controlAlarmDeadLettersQuery(site.id),
    enabled: Boolean(site.id) && canManage,
  });

  const [viewMode, setViewMode] = useState<'pid' | 'swiss'>('pid');
  const [filterMode, setFilterMode] = useState<'all' | 'abnormal'>('all');
  const [manualSelectedAssetId, setManualSelectedAssetId] = useState<string | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [pending, setPending] = useState<PendingCommand | null>(null);
  const [pendingAction, setPendingAction] = useState<PendingCommandAction | null>(null);
  const [modeTarget, setModeTarget] = useState<SiteControlMode | null>(null);
  const [lockTarget, setLockTarget] = useState<ControlTarget | null>(null);
  const [priorityLockTarget, setPriorityLockTarget] = useState<ControlTarget | null>(null);
  const [stepUp, setStepUp] = useState<Command | null>(null);
  const [shutdown, setShutdown] = useState<ShutdownSubject | null>(null);

  const model = registry.data?.assetModel;
  const targets = useMemo(() => (model ? controlTargets(model, new Date(now)) : []), [model, now]);
  const grouped = useMemo(
    () => (model ? plantUnits(targets, model, new Date(now)) : { units: [], others: [] }),
    [targets, model, now],
  );
  const labels = useMemo(() => commandLabels(targets), [targets]);
  const devices = useMemo(() => new Map(plant.devices.map((device) => [device.deviceId, device])), [plant.devices]);
  const hydraulic = useMemo(() => buildHydraulicSchematic(plant), [plant]);
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
    onSuccess: () => {
      setPendingAction(null);
      toast.success('已批准');
      void settle();
    },
    onError: (error, command) => {
      setPendingAction(null);
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
    mutationFn: ({ commandId, outcome }: { commandId: string; outcome: ReconciledOutcome }) =>
      reconcileCommand(commandId, outcome, csrfToken),
    onSuccess: () => {
      setPendingAction(null);
      toast.success('已记录现场确认结果');
      void settle();
    },
    onError: (error) => {
      setPendingAction(null);
      commandFailure(error);
    },
  });

  const actions: CommandActions = {
    busy: approve.isPending || cancel.isPending || reconcile.isPending,
    canApprove: mode === 'REMOTE_AUTO',
    onApprove: (command) => setPendingAction({ kind: 'approve', command }),
    onCancel: (command) => cancel.mutate(command),
    onReconcile: (commandId, outcome) => {
      const command = list.find((item) => item.commandId === commandId);
      if (command) setPendingAction({ kind: 'reconcile', command, outcome });
      else reconcile.mutate({ commandId, outcome });
    },
  };

  const setSearch = (next: { status?: ControlLogFilter; inspect?: string }) =>
    void navigate({ search: (previous) => ({ ...previous, ...next }), replace: true });
  const inspect = (commandId: string | undefined) => setSearch({ inspect: commandId });

  const interlockFacts = useMemo(() => {
    const deviceIdByCode = new Map((model?.devices ?? []).map((device) => [device.code, device.id]));
    const targetByDevice = new Map(targets.map((target) => [target.deviceId, target]));
    const assetIdByDeviceId = new Map(targets.map((target) => [target.deviceId, target.assetId]));
    const runStateByDeviceCode = new Map<string, string>();
    const nameByDeviceCode = new Map<string, string>();
    for (const [code, deviceId] of deviceIdByCode) {
      const device = devices.get(deviceId);
      if (device) runStateByDeviceCode.set(code, device.runState);
      const target = targetByDevice.get(deviceId);
      if (target) nameByDeviceCode.set(code, target.assetType === 'CHILLER' ? `${target.assetName}主机` : target.assetName);
    }
    return { runStateByDeviceCode, nameByDeviceCode, deviceIdByCode, assetIdByDeviceId };
  }, [model, targets, devices]);

  const unitNameByAssetId = useMemo(() => {
    const map = new Map<string, string>();
    for (const unit of grouped.units) {
      map.set(unit.chiller.assetId, unit.chiller.assetName);
      for (const member of unit.members) map.set(member.assetId, unit.chiller.assetName);
    }
    const singleLoopName = grouped.units.length === 1 ? grouped.units[0]!.chiller.assetName : '独立回路设备';
    for (const other of grouped.others) map.set(other.assetId, singleLoopName);
    return map;
  }, [grouped]);

  const selectedTarget = useMemo(() => {
    if (targets.length === 0) return null;
    if (manualSelectedAssetId) {
      const manual = targets.find((candidate) => candidate.assetId === manualSelectedAssetId);
      if (manual) return manual;
    }
    const activeCmdTarget = targets.find((candidate) =>
      list.some((command) => !SETTLED.has(command.status) && command.deviceId === candidate.deviceId));
    if (activeCmdTarget) return activeCmdTarget;
    const lockedTarget = targets.find((candidate) => locks.has(candidate.assetId) || priorityLocks.has(candidate.deviceId));
    if (lockedTarget) return lockedTarget;
    return grouped.units[0]?.chiller ?? targets[0] ?? null;
  }, [targets, manualSelectedAssetId, list, locks, priorityLocks, grouped]);

  const abortSubject = (unit: PlantUnit): ShutdownSubject => {
    const points = new Set(
      [unit.chiller, ...unit.members].flatMap((target) =>
        [...target.actions, ...target.settings].map((point) => point.pointId),
      ),
    );
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
      .filter(
        (command) =>
          WITHDRAWABLE.has(command.status)
          && [...lockTarget.actions, ...lockTarget.settings].some((point) => point.pointId === command.pointId),
      )
      .map((command) => ({ commandId: command.commandId, text: `${describe(command)}（${STATUS_PRESENTATION[command.status].label}）` })),
  } : null;

  const refreshAll = () => {
    refresh();
    void commands.refetch();
    void controlState.refetch();
    if (canManage) void deadLetters.refetch();
  };

  const sharedCanvasProps: CanvasSharedProps = {
    units: grouped.units,
    others: grouped.others,
    targets,
    devices,
    hydraulic,
    mode,
    maintenance: controlState.data?.activeMaintenanceWindow ?? null,
    locks,
    priorityLocks,
    commands: list,
    runStateByDeviceCode: interlockFacts.runStateByDeviceCode,
    nameByDeviceCode: interlockFacts.nameByDeviceCode,
    deviceIdByCode: interlockFacts.deviceIdByCode,
    myId,
    timeZone: site.timezone,
    nowMs: now,
    canManage,
    selectedAssetId: selectedTarget?.assetId ?? null,
    filterMode,
    onSelectAsset: setManualSelectedAssetId,
    onCommand: setPending,
    onAbort: (unit) => setShutdown(abortSubject(unit)),
    onInspect: inspect,
  };

  const combinedSingle = isSingleLoopCombined(grouped.units, grouped.others);
  const abnormalUnitCount = grouped.units.filter((unit) =>
    unitHasAbnormality(unit, combinedSingle ? grouped.others : [], sharedCanvasProps),
  ).length;
  const groupCount = grouped.units.length > 0 ? grouped.units.length : targets.length;
  const drawerOpen = historyOpen || search.status !== undefined;

  return (
    <Main fluid className="min-h-0 flex-1 gap-0 bg-card px-0 pt-0 pb-0">
      <FocusHeading className="sr-only">运行控制</FocusHeading>

      {controlState.isPending ? (
        <Skeleton className="h-[88px] w-full rounded-none border-b border-border" />
      ) : controlState.isError ? (
        <section aria-label="控制方式" className="flex flex-wrap items-center gap-3 border-b border-border bg-card px-4 py-3">
          <span className="text-sm font-semibold">控制方式暂时读不到</span>
          <span className="text-xs text-muted-foreground">{controlState.error.message}</span>
          <Button variant="outline" size="sm" className="ml-auto" onClick={() => void controlState.refetch()}>重试</Button>
        </section>
      ) : (
        <ControlCockpitStrip
          state={controlState.data}
          units={grouped.units}
          devices={devices}
          plant={plant}
          hydraulic={hydraulic}
          liveMode={liveMode}
          commandCount={list.length}
          lockCount={locks.size}
          pendingCount={list.filter(needsPerson).length}
          myId={myId}
          timeZone={site.timezone}
          canManage={canManage}
          autoControlLease={autoControlLease.data}
          onPickMode={setModeTarget}
          onShutdown={() =>
            setShutdown({
              kind: 'site',
              emergencyAvailable: model ? hasEmergencyRelease(model) : false,
              withdrawCount: list.filter((command) => WITHDRAWABLE.has(command.status)).length,
            })
          }
          onOpenHistory={() => setHistoryOpen(true)}
          onRefresh={refreshAll}
        />
      )}

      <ActiveCommandChannel
        commands={list}
        labels={labels}
        assetIdByDeviceId={interlockFacts.assetIdByDeviceId}
        timeZone={site.timezone}
        actions={actions}
        onSelectAsset={setManualSelectedAssetId}
        onInspect={inspect}
      />

      {registry.isPending ? (
        <div className="grid grid-cols-1 gap-4 p-6 @2xl/main:grid-cols-2 @5xl/main:grid-cols-4">
          <Skeleton className="h-56" />
          <Skeleton className="h-56" />
          <Skeleton className="h-56" />
          <Skeleton className="h-56" />
        </div>
      ) : registry.isError ? (
        <div className="flex flex-col items-start gap-2 px-6 py-8">
          <p className="text-sm text-destructive-text">设备清单暂时读不到</p>
          <Button variant="outline" size="sm" onClick={() => void registry.refetch()}>重试</Button>
        </div>
      ) : targets.length === 0 || !selectedTarget ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-14 text-center">
          <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">
            <Terminal className="size-[18px]" aria-hidden="true" />
          </span>
          <p className="text-sm font-semibold">没有可控制的设备</p>
          <p className="text-xs text-muted-foreground">设备登记了控制点并关联到资产后，会出现在这里</p>
        </div>
      ) : (
        <div className="grid min-h-0 flex-1 grid-cols-1 divide-y divide-border @5xl/main:grid-cols-[minmax(0,1fr)_400px] @5xl/main:divide-x @5xl/main:divide-y-0">
          <div className="@container/canvas flex min-w-0 flex-col">
            <div className="flex h-10 shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border px-4">
              <div className="flex items-center gap-2">
                <ToggleGroup
                  type="single"
                  value={viewMode}
                  aria-label="画布视图"
                  onValueChange={(value) => { if (value === 'pid' || value === 'swiss') setViewMode(value); }}
                  className="rounded-md border border-border bg-muted/60 p-0.5"
                >
                  <ToggleGroupItem
                    value="pid"
                    className="h-6 gap-1 rounded-sm px-2 text-xs font-medium data-[state=on]:bg-card data-[state=on]:text-foreground data-[state=on]:shadow-xs"
                  >
                    <GitBranch className="size-3" aria-hidden="true" />
                    冷链水路拓扑
                  </ToggleGroupItem>
                  <ToggleGroupItem
                    value="swiss"
                    className="h-6 gap-1 rounded-sm px-2 text-xs font-medium data-[state=on]:bg-card data-[state=on]:text-foreground data-[state=on]:shadow-xs"
                  >
                    <TableProperties className="size-3" aria-hidden="true" />
                    精密横轨表
                  </ToggleGroupItem>
                </ToggleGroup>

                <ToggleGroup
                  type="single"
                  value={filterMode}
                  aria-label="机组筛选"
                  onValueChange={(value) => { if (value === 'all' || value === 'abnormal') setFilterMode(value); }}
                  className="hidden rounded-md border border-border bg-muted/60 p-0.5 sm:inline-flex"
                >
                  <ToggleGroupItem
                    value="all"
                    className="h-6 rounded-sm px-2 text-xs font-medium data-[state=on]:bg-card data-[state=on]:text-foreground data-[state=on]:shadow-xs"
                  >
                    全部 ({groupCount})
                  </ToggleGroupItem>
                  <ToggleGroupItem
                    value="abnormal"
                    className="h-6 rounded-sm px-2 text-xs font-medium data-[state=on]:bg-card data-[state=on]:text-foreground data-[state=on]:shadow-xs"
                  >
                    异常与锁定 ({abnormalUnitCount})
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>

              <div className="hidden items-center gap-3 text-[11px] text-muted-foreground @2xl/canvas:flex">
                <span className="inline-flex items-center gap-1"><span className="h-0.5 w-3 bg-chart-2" aria-hidden="true" />冷却水路</span>
                <span className="inline-flex items-center gap-1"><span className="h-0.5 w-3 bg-primary" aria-hidden="true" />冷冻水路</span>
                <span className="inline-flex items-center gap-1"><span className="size-1.5 rounded-full bg-success" aria-hidden="true" />运行</span>
              </div>
            </div>

            {viewMode === 'pid' ? (
              <PlantTopologyCanvas shared={sharedCanvasProps} />
            ) : (
              <PlantSwissTable shared={sharedCanvasProps} />
            )}
          </div>

          <DeviceFaceplate
            target={selectedTarget}
            unitName={unitNameByAssetId.get(selectedTarget.assetId) ?? site.displayName}
            device={devices.get(selectedTarget.deviceId)}
            mode={mode}
            maintenance={controlState.data?.activeMaintenanceWindow ?? null}
            locks={locks}
            priorityLocks={priorityLocks}
            commands={list}
            allTargets={targets}
            runStateByDeviceCode={interlockFacts.runStateByDeviceCode}
            nameByDeviceCode={interlockFacts.nameByDeviceCode}
            deviceIdByCode={interlockFacts.deviceIdByCode}
            labels={labels}
            actions={actions}
            myId={myId}
            timeZone={site.timezone}
            nowMs={now}
            canManage={canManage}
            onSelectAsset={setManualSelectedAssetId}
            onCommand={setPending}
            onLock={setLockTarget}
            onPriorityLock={setPriorityLockTarget}
            onInspect={inspect}
            onOpenHistory={() => setHistoryOpen(true)}
          />
        </div>
      )}

      {canManage && deadLetters.data && deadLetters.data.length > 0 ? (
        <div
          role="status"
          aria-label="控制告警死信提示"
          className="flex flex-wrap items-center justify-between gap-2 border-t border-warning/35 bg-warning-soft/45 px-4 py-1.5 text-xs text-foreground"
        >
          <span className="flex items-center gap-2">
            <TriangleAlert className="size-3.5 shrink-0 text-warning-text" aria-hidden="true" />
            <span>
              有 <span className="font-mono font-medium">{deadLetters.data.length}</span> 条控制告警投递失败待重投
            </span>
          </span>
          <PanelLink to="/system" search={{ tab: 'governance', site: site.id, section: 'control' }}>
            前往处理
          </PanelLink>
        </div>
      ) : null}

      <ConfirmCommand pending={pending} siteId={site.id} csrfToken={csrfToken} onClose={() => setPending(null)} />
      {controlState.data ? (
        <ControlModeDialog
          siteId={site.id}
          csrfToken={csrfToken}
          state={controlState.data}
          initial={modeTarget}
          commands={list}
          describe={describe}
          onClose={() => setModeTarget(null)}
          onShowUnsettled={() => {
            setModeTarget(null);
            setHistoryOpen(true);
            setSearch({ status: list.some((command) => command.status === 'OUTCOME_UNKNOWN') ? 'unknown' : 'inflight' });
          }}
        />
      ) : null}
      <CommandActionConfirmDialog
        pendingAction={pendingAction}
        summary={pendingAction ? describe(pendingAction.command) : undefined}
        busy={approve.isPending || reconcile.isPending}
        onConfirm={(action) => {
          if (action.kind === 'approve') approve.mutate(action.command);
          else reconcile.mutate({ commandId: action.command.commandId, outcome: action.outcome });
        }}
        onClose={() => setPendingAction(null)}
      />
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
      <StepUpDialog command={stepUp} describe={describe} onClose={() => setStepUp(null)} />
      <CommandHistoryDrawer
        open={drawerOpen}
        statusFilter={search.status}
        commands={list}
        isPending={commands.isPending}
        errorMessage={commands.isError ? commands.error.message : null}
        labels={labels}
        timeZone={site.timezone}
        selectedId={search.inspect}
        actions={actions}
        onFilterChange={(next) => setSearch({ status: next })}
        onRetry={() => void commands.refetch()}
        onInspect={inspect}
        onClose={() => {
          setHistoryOpen(false);
          if (search.status) setSearch({ status: undefined });
        }}
      />
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
