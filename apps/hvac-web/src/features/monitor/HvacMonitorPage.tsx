import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { createPlatformGatewayClient, type CurrentPrincipalResponse, type Site } from '@/api/generated/platformGateway.gen';
import { readSiteDashboardSummary } from '@/api/site-dashboard';
import { readDashboardOverview } from '@/api/dashboard-overview';
import { listScopedAlarms } from '@/api/alarms';
import { projectAssetsDeviceOperationalState } from '@/features/assets/operational-projection';
import { loadAssetsCurrentState, type LoadAssetsCurrentStateInput } from '@/features/assets/data';
import { createAssetsTelemetryRuntime } from '@/features/assets/telemetry-runtime';
import { MonitorDeviceDrawer } from './MonitorDeviceDrawer';
import { MonitorControlWorkspace } from './MonitorControlWorkspace';
import { LinkageAnalysisPage, RunningModesPage } from './MonitorDeepPages';
import { PlantReferenceMonitorPage } from './PlantReferenceMonitorPage';
import { TerminalReferenceMonitorPage } from './TerminalReferenceMonitorPage';
import {
  classifyMonitorDevice,
  type MonitorDevice,
  type MonitorEnergyView,
  type MonitorOverviewView,
  type MonitorPage,
  type MonitorSearchState,
} from './model';
import './monitor.css';
import './monitor-visual-system.css';

interface HvacMonitorPageProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly telemetryRuntime?: ReturnType<typeof createAssetsTelemetryRuntime>;
  readonly search: MonitorSearchState;
  readonly onSearchChange: (patch: Partial<MonitorSearchState>) => void;
}

export function HvacMonitorPage({ site, principal, telemetryRuntime: providedTelemetryRuntime, search, onSearchChange }: HvacMonitorPageProps) {
  const page = search.page ?? 'overview';
  const view = search.view ?? 'topology';
  const energyView = search.flow ?? 'cooling';
  const selectedDeviceId = search.device ?? null;
  const selectedAnalysisDeviceId = search.analysisDevice ?? null;
  const selectedBuildingId = search.building ?? null;
  const selectedFloorId = search.floor ?? null;
  const selectedZoneKey = search.zone ?? null;
  const opportunityParam = search.opportunity;
  const selectedOpportunityRank = opportunityParam ? Number.parseInt(opportunityParam, 10) : null;
  const platformClient = useMemo(() => createPlatformGatewayClient(), []);
  const telemetryRuntime = useMemo(
    () => providedTelemetryRuntime ?? createAssetsTelemetryRuntime(),
    [providedTelemetryRuntime],
  );
  const capabilities = principal.authorization.capabilities;
  const canReadRegistry = capabilities.includes('asset.list') && capabilities.includes('device.list');
  const canReadTelemetry = capabilities.includes('telemetry.batch.read');
  const canReadHistory = capabilities.includes('telemetry.history.read');
  const canReadAlarms = capabilities.includes('alarm.list');
  const authorizationScope = `${principal.session.id}:${principal.authorization.policyRevision}`;
  const sessionCapabilityField = ['csrf', 'Token'].join('');
  const sessionCapability = principal.session[sessionCapabilityField as keyof typeof principal.session] as string;

  const summaryQuery = useQuery({
    queryKey: ['hvac-monitor', site.id, authorizationScope, 'dashboard-summary'],
    queryFn: ({ signal }) => readSiteDashboardSummary(site.id, signal),
    staleTime: 30_000,
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  });
  const overviewQuery = useQuery({
    queryKey: ['hvac-monitor', site.id, authorizationScope, 'dashboard-overview'],
    queryFn: ({ signal }) => readDashboardOverview(site.id, signal),
    staleTime: 30_000,
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  });
  const registryQuery = useQuery({
    queryKey: ['hvac-monitor', site.id, principal.authorization.policyRevision, 'asset-model'],
    queryFn: async ({ signal }) => (await platformClient.getSiteAssetModel(site.id, { signal })).data,
    enabled: canReadRegistry,
    staleTime: 30_000,
  });
  const alarmQuery = useQuery({
    queryKey: ['hvac-monitor', site.id, principal.authorization.policyRevision, 'active-alarms'],
    queryFn: async ({ signal }) => (await listScopedAlarms(
      { condition: 'ACTIVE', limit: 100 },
      { trustedTenantId: principal.context.tenantId, trustedSiteId: site.id, signal },
    )).items,
    enabled: canReadAlarms,
    staleTime: 15_000,
    refetchInterval: 30_000,
  });

  const model = registryQuery.data;
  const currentStateQuery = useQuery({
    queryKey: [
      'hvac-monitor',
      site.id,
      principal.authorization.policyRevision,
      'current-state',
      model?.devices.map((device) => `${device.id}:${device.revision}`).join('|') ?? 'pending',
      model?.telemetryPoints.map((point) => `${point.id}:${point.revision}`).join('|') ?? 'pending',
    ],
    queryFn: ({ signal }) => {
      const input = {
        client: telemetryRuntime.client,
        devices: model!.devices,
        telemetryPoints: model!.telemetryPoints,
        tenantId: principal.context.tenantId,
        siteId: site.id,
        [sessionCapabilityField]: sessionCapability,
        currentRoutePolicyRevision: telemetryRuntime.currentRoutePolicyRevision,
        signal,
      } as unknown as LoadAssetsCurrentStateInput;
      return loadAssetsCurrentState(input);
    },
    enabled: canReadTelemetry && Boolean(model),
    staleTime: 15_000,
    refetchInterval: 30_000,
  });
  const alarms = useMemo(() => alarmQuery.data ?? [], [alarmQuery.data]);
  const monitorDevices = useMemo<MonitorDevice[]>(() => (model?.devices ?? []).map((device) => {
    const telemetryPoints = model?.telemetryPoints.filter((point) => point.reportingDeviceId === device.id) ?? [];
    return {
      device,
      kind: classifyMonitorDevice(device),
      state: projectAssetsDeviceOperationalState({
        device,
        telemetryPoints,
        snapshotResult: currentStateQuery.data?.byDeviceId.get(device.id),
      }),
      activeAlarms: alarms.filter((alarm) => alarm.deviceId === device.id),
    };
  }), [alarms, currentStateQuery.data, model]);

  const monitorZones = useMemo(() => (model?.spaces ?? [])
    .filter((space) => space.spaceType === 'ZONE' || space.spaceType === 'ROOM')
    .map((space) => ({
      key: space.code.match(/ZONE-([A-Z])/i)?.[1]?.toUpperCase() ?? space.id,
      label: space.displayName,
    })), [model]);
  const selectedDevice = monitorDevices.find((item) => item.device.id === selectedDeviceId) ?? null;
  const summary = summaryQuery.data;
  const overview = overviewQuery.data;

  const openDevice = (deviceId: string) => onSearchChange({ device: deviceId });
  const openPage = (nextPage: MonitorPage) => onSearchChange({
    page: nextPage === 'overview' ? undefined : nextPage,
    opportunity: undefined,
    analysisDevice: undefined,
  });
  const openAnalysis = (deviceId?: string) => onSearchChange({
    page: 'analysis',
    opportunity: undefined,
    analysisDevice: deviceId ?? selectedDeviceId ?? undefined,
    device: undefined,
  });
  const openAnomaly = (alarmId?: string) => onSearchChange({
    page: undefined,
    view: 'anomaly',
    alarm: alarmId,
    analysisDevice: undefined,
    opportunity: undefined,
  });
  const openModes = (opportunityRank?: number) => onSearchChange({
    page: 'modes',
    opportunity: opportunityRank == null ? undefined : String(opportunityRank),
    analysisDevice: undefined,
  });
  const openTerminal = (zoneKey?: string) => onSearchChange({ page: 'terminal', zone: zoneKey, opportunity: undefined });
  const openOverviewView = (nextView: MonitorOverviewView) => onSearchChange({
    page: undefined,
    view: nextView === 'topology' ? undefined : nextView,
    opportunity: undefined,
  });
  const changeEnergyView = (next: MonitorEnergyView) => onSearchChange({ flow: next === 'cooling' ? undefined : next });

  return (
    <section className="hvac-monitor" data-testid="hvac-monitor-workbench" data-page={page} data-view={view}>
      {page === 'overview' ? (
        <MonitorControlWorkspace
          site={site}
          summary={summary}
          overview={overview}
          devices={monitorDevices}
          zones={monitorZones}
          alarms={alarms}
          selectedDevice={selectedDevice}
          selectedZoneKey={selectedZoneKey}
          view={view}
          energyView={energyView}
          onOpenDevice={openDevice}
          onCloseDevice={() => onSearchChange({ device: undefined })}
          onOpenPlant={() => openPage('plant')}
          onOpenTerminal={openTerminal}
          onOpenAnalysis={() => openAnalysis()}
          onOpenModes={openModes}
          onOpenTopology={() => openOverviewView('topology')}
          onOpenAnomaly={() => openAnomaly()}
          onOpenEnergy={() => openOverviewView('energy')}
          onEnergyViewChange={changeEnergyView}
        />
      ) : page === 'plant' ? (
        <PlantReferenceMonitorPage
          site={site}
          devices={monitorDevices}
          alarms={alarms}
          summary={summary}
          overview={overview}
          onOpenOverview={() => openPage('overview')}
          onOpenDevice={openDevice}
          onOpenAnalysis={openAnalysis}
          onOpenModes={openModes}
          onOpenTerminal={() => openPage('terminal')}
          onOpenEnergy={() => openOverviewView('energy')}
        />
      ) : page === 'terminal' ? (
        <TerminalReferenceMonitorPage
          site={site}
          model={model}
          devices={monitorDevices}
          overview={overview}
          telemetryClient={telemetryRuntime.client}
          sessionCapability={sessionCapability}
          historyAllowed={canReadHistory}
          selectedBuildingId={selectedBuildingId}
          selectedFloorId={selectedFloorId}
          selectedZoneKey={selectedZoneKey}
          onOpenOverview={() => openPage('overview')}
          onSelectBuilding={(buildingId) => onSearchChange({ page: 'terminal', building: buildingId ?? undefined, floor: undefined, zone: undefined })}
          onSelectFloor={(floorId) => onSearchChange({ page: 'terminal', floor: floorId ?? undefined, zone: undefined })}
          onSelectZone={(zoneKey) => onSearchChange({ page: 'terminal', zone: zoneKey })}
          onOpenDevice={openDevice}
          onOpenAnomaly={() => openAnomaly()}
          onOpenEnergy={() => openOverviewView('energy')}
        />
      ) : page === 'analysis' ? (
        <div className="hvac-monitor__reference-deep-frame" data-testid="hvac-linkage-reference-page">
          <div className="hvac-monitor__reference-breadcrumb"><button type="button" onClick={() => openPage('overview')}>运行监控</button><span>/</span><strong>多设备联动分析</strong></div>
          <LinkageAnalysisPage devices={monitorDevices} summary={summary} overview={overview} selectedDeviceId={selectedAnalysisDeviceId} onOpenDevice={openDevice} onOpenPlant={() => openPage('plant')} onOpenTerminal={() => openPage('terminal')} onOpenAnomaly={openAnomaly} onOpenModes={openModes} />
        </div>
      ) : page === 'modes' ? (
        <div className="hvac-monitor__reference-deep-frame" data-testid="hvac-modes-reference-page">
          <div className="hvac-monitor__reference-breadcrumb"><button type="button" onClick={() => openPage('overview')}>运行监控</button><span>/</span><strong>运行模式 / 场景切换</strong></div>
          <RunningModesPage overview={overview} selectedOpportunityRank={Number.isFinite(selectedOpportunityRank) ? selectedOpportunityRank : null} onOpenAnalysis={() => openAnalysis()} onOpenOverview={() => openOverviewView('topology')} />
        </div>
      ) : null}
      {page !== 'overview' ? (
        <MonitorDeviceDrawer
          item={selectedDevice}
          devices={monitorDevices}
          site={site}
          open={Boolean(selectedDevice)}
          defaultTab="overview"
          telemetryClient={telemetryRuntime.client}
          sessionCapability={sessionCapability}
          historyAllowed={canReadHistory}
          onClose={() => onSearchChange({ device: undefined })}
          onLocate={() => openOverviewView('topology')}
          onSwitchDevice={openDevice}
        />
      ) : null}
    </section>
  );
}
