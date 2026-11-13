import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { flattenRegistryPages, useRegistryDevices, useRegistrySites } from '@/api/registry';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { StateCard } from '@/blocks/state/state-card';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useNow } from '@/hooks/use-now';
import { isOneOf } from '@/lib/typed';
import { GatewaysTab } from './components/GatewaysTab';
import { MeterInspectionSheet } from './components/MeterInspectionSheet';
import { MeterReconciliationTab } from './components/MeterReconciliationTab';
import { SensorCalibrationTab } from './components/SensorCalibrationTab';
import { SensorInspectionSheet } from './components/SensorInspectionSheet';
import {
  buildMeterReconciliationModel,
  buildSensorCalibrationModel,
  findMainElectricityCounterDevices,
  type IntegrationsTabView,
  type SensorCalibrationFilter,
} from './integrations-model';
import {
  checkMetersHourlyQuery,
  siteHourlyEnergyPairQuery,
  siteIntegrationsAssetModelQuery,
  siteIntegrationsSnapshotsQuery,
} from './query';

const route = getRouteApi('/_app/settings/integrations');

const HOUR_MS = 3_600_000;
const INTEGRATIONS_VIEWS = ['gateways', 'meters', 'sensors'] as const satisfies readonly IntegrationsTabView[];

export function IntegrationsWorkspace() {
  const snapshot = useShellSnapshot();
  const principal = snapshot.principal!;
  const search = route.useSearch();
  const navigate = route.useNavigate();

  const nowMs = useNow(60_000);
  const now = useMemo(() => new Date(nowMs), [nowMs]);
  const windowToMs = Math.floor(nowMs / HOUR_MS) * HOUR_MS + HOUR_MS;
  const windowFromMs = windowToMs - 24 * HOUR_MS;

  const sitesQuery = useRegistrySites();
  const sites = flattenRegistryPages(sitesQuery.data);
  const siteId = search.site ?? sites[0]?.id ?? null;
  const currentSite = sites.find((site) => site.id === siteId) ?? null;
  const siteName = currentSite?.displayName ?? '';
  const siteTenantId = currentSite?.tenantId ?? principal.context.tenantId;
  const siteTimezone = currentSite?.timezone ?? 'Asia/Shanghai';

  const devicesQuery = useRegistryDevices(siteId);
  const allDevices = flattenRegistryPages(devicesQuery.data);

  const capabilities = principal.authorization.capabilities;
  const canWriteDevices = capabilities.includes('device.write');
  const canReadAssets = capabilities.includes('asset.read');
  const canReadSnapshots =
    capabilities.includes('telemetry.batch.read') ||
    capabilities.includes('telemetry.snapshot.read');
  const canReadHistory = capabilities.includes('telemetry.history.read');
  const canReadWorkOrders = capabilities.includes('work-order.list');
  const canCreateWorkOrder = capabilities.includes('work-order.create');
  const csrfToken = principal.session.csrfToken;

  const activeView: IntegrationsTabView =
    search.view === 'meters' || search.view === 'sensors' ? search.view : 'gateways';
  const needsGovernanceData = activeView === 'meters' || activeView === 'sensors';
  const sensorFilter: SensorCalibrationFilter = search.filter ?? 'ALL';

  const assetModelQuery = useQuery(
    siteIntegrationsAssetModelQuery(siteId, needsGovernanceData && canReadAssets),
  );

  const snapshotsQuery = useQuery(
    siteIntegrationsSnapshotsQuery({
      tenantId: siteTenantId,
      siteId,
      devices: assetModelQuery.data?.devices ?? [],
      telemetryPoints: assetModelQuery.data?.telemetryPoints ?? [],
      csrfToken,
      enabled:
        needsGovernanceData &&
        canReadSnapshots &&
        Boolean(assetModelQuery.data),
    }),
  );

  const electricityHourlyQuery = useQuery(
    siteHourlyEnergyPairQuery({
      tenantId: siteTenantId,
      siteId,
      timezone: siteTimezone,
      energyType: 'electricity',
      breakdownDevice: true,
      fromMs: windowFromMs,
      toMs: windowToMs,
      csrfToken,
      enabled: activeView === 'meters' && canReadAssets && Boolean(siteId),
    }),
  );

  const coolingHourlyQuery = useQuery(
    siteHourlyEnergyPairQuery({
      tenantId: siteTenantId,
      siteId,
      timezone: siteTimezone,
      energyType: 'cooling',
      fromMs: windowFromMs,
      toMs: windowToMs,
      csrfToken,
      enabled: activeView === 'meters' && canReadAssets && Boolean(siteId),
    }),
  );

  const isSynthesizedElectricity = Boolean(
    electricityHourlyQuery.data?.validOnly.metadata.synthesized ||
      electricityHourlyQuery.data?.validAndSuspect.metadata.synthesized,
  );
  const checkMeterTargets = useMemo(
    () => (assetModelQuery.data ? findMainElectricityCounterDevices(assetModelQuery.data) : []),
    [assetModelQuery.data],
  );
  const checkMetersHistoryQuery = useQuery(
    checkMetersHourlyQuery({
      siteId,
      targets: checkMeterTargets,
      timezone: siteTimezone,
      fromMs: windowFromMs,
      toMs: windowToMs,
      csrfToken,
      enabled:
        activeView === 'meters' &&
        canReadHistory &&
        isSynthesizedElectricity &&
        checkMeterTargets.length > 0,
    }),
  );

  const meterModel = useMemo(() => {
    if (activeView !== 'meters' || !siteId || !assetModelQuery.data) return null;
    return buildMeterReconciliationModel({
      siteId,
      timezone: siteTimezone,
      assetModel: assetModelQuery.data,
      snapshots: snapshotsQuery.data,
      electricityValid: electricityHourlyQuery.data?.validOnly ?? null,
      electricityWithSuspect: electricityHourlyQuery.data?.validAndSuspect ?? null,
      coolingValid: coolingHourlyQuery.data?.validOnly ?? null,
      coolingWithSuspect: coolingHourlyQuery.data?.validAndSuspect ?? null,
      checkMeterHourlyByDeviceId: checkMetersHistoryQuery.data,
      window: { fromMs: windowFromMs, toMs: windowToMs },
      now,
    });
  }, [
    activeView,
    siteId,
    siteTimezone,
    assetModelQuery.data,
    snapshotsQuery.data,
    electricityHourlyQuery.data,
    coolingHourlyQuery.data,
    checkMetersHistoryQuery.data,
    windowFromMs,
    windowToMs,
    now,
  ]);

  const sensorModel = useMemo(() => {
    if (activeView !== 'sensors' || !siteId || !assetModelQuery.data) return null;
    return buildSensorCalibrationModel({
      siteId,
      timezone: siteTimezone,
      assetModel: assetModelQuery.data,
      snapshots: snapshotsQuery.data,
      window: { fromMs: windowFromMs, toMs: windowToMs },
      now,
    });
  }, [
    activeView,
    siteId,
    siteTimezone,
    assetModelQuery.data,
    snapshotsQuery.data,
    windowFromMs,
    windowToMs,
    now,
  ]);

  const inspectedMeter = useMemo(() => {
    if (activeView !== 'meters' || !search.inspect || !meterModel) return null;
    return (
      meterModel.rows.find(
        (row) => row.deviceCode === search.inspect || row.deviceId === search.inspect,
      ) ?? null
    );
  }, [activeView, search.inspect, meterModel]);

  const inspectedSensor = useMemo(() => {
    if (activeView !== 'sensors' || !search.inspect || !sensorModel) return null;
    return (
      sensorModel.rows.find(
        (row) => row.inspectKey === search.inspect || row.pointId === search.inspect,
      ) ?? null
    );
  }, [activeView, search.inspect, sensorModel]);

  const setInspect = (nextInspect: string | undefined) => {
    void navigate({
      search: (prev) => ({
        ...prev,
        inspect: nextInspect,
      }),
    });
  };

  return (
    <Main className="gap-4">
      <PageHeader
        title="集成管理"
        meta={siteName ? <PageMeta>{siteName}</PageMeta> : undefined}
        description="核查现场网关证书、计量表计逐时数据连续性与传感器探头零点校准建议"
        actions={
          <>
            <Select
              value={siteId ?? ''}
              onValueChange={(nextSite) => {
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    site: nextSite,
                    inspect: undefined,
                  }),
                });
              }}
            >
              <SelectTrigger aria-label="站点" className="h-8 w-48">
                <SelectValue placeholder="选择站点" />
              </SelectTrigger>
              <SelectContent>
                {sites.map((site) => (
                  <SelectItem key={site.id} value={site.id}>
                    {site.displayName}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {sitesQuery.hasNextPage ? (
              <Button
                variant="outline"
                size="sm"
                disabled={sitesQuery.isFetchingNextPage}
                onClick={() => void sitesQuery.fetchNextPage()}
              >
                加载更多站点
              </Button>
            ) : null}
          </>
        }
      />

      {sitesQuery.isError || devicesQuery.isError ? (
        <Alert variant="destructive">
          <AlertDescription>
            登记信息暂时读不到，请确认当前站点权限和服务状态。
          </AlertDescription>
        </Alert>
      ) : null}

      <Tabs
        value={activeView}
        onValueChange={(next) => {
          if (!isOneOf(INTEGRATIONS_VIEWS, next)) return;
          void navigate({
            search: (prev) => ({
              ...prev,
              view: next === 'gateways' ? undefined : next,
              inspect: undefined,
            }),
          });
        }}
        className="flex flex-col gap-4"
      >
        <TabsList variant="line">
          <TabsTrigger value="gateways">现场网关</TabsTrigger>
          <TabsTrigger value="meters">电表数据核对</TabsTrigger>
          <TabsTrigger value="sensors">传感器校准</TabsTrigger>
        </TabsList>

        <TabsContent value="gateways" className="mt-0">
          <GatewaysTab
            siteId={siteId}
            siteName={siteName}
            allDevices={allDevices}
            devicesPending={devicesQuery.isPending}
            devicesError={devicesQuery.isError}
            sitesError={sitesQuery.isError}
            hasNextDevicesPage={devicesQuery.hasNextPage}
            isFetchingNextDevicesPage={devicesQuery.isFetchingNextPage}
            onFetchNextDevicesPage={() => void devicesQuery.fetchNextPage()}
            canWrite={canWriteDevices}
            csrfToken={csrfToken}
            inspectParam={activeView === 'gateways' ? search.inspect : undefined}
            onInspectChange={setInspect}
          />
        </TabsContent>

        <TabsContent value="meters" className="mt-0">
          {!canReadAssets ? (
            <StateCard
              title={<h2 className="text-base font-semibold">当前账号无权读取表计配置</h2>}
              description="查看计量表计与点位登记需要站点资产读取权限，请联系管理员分配相应角色。"
            />
          ) : (
            <MeterReconciliationTab
              model={meterModel}
              isAssetLoading={assetModelQuery.isPending}
              isAssetError={assetModelQuery.isError}
              assetError={assetModelQuery.error}
              isEnergyLoading={electricityHourlyQuery.isPending}
              isEnergyError={electricityHourlyQuery.isError}
              energyError={electricityHourlyQuery.error}
              canReadSnapshots={canReadSnapshots}
              isSnapshotsError={snapshotsQuery.isError}
              onRetry={() => {
                void assetModelQuery.refetch();
                void snapshotsQuery.refetch();
                void electricityHourlyQuery.refetch();
                void coolingHourlyQuery.refetch();
              }}
              onInspectMeter={(deviceCode) => setInspect(deviceCode)}
            />
          )}
        </TabsContent>

        <TabsContent value="sensors" className="mt-0">
          {!canReadAssets ? (
            <StateCard
              title={<h2 className="text-base font-semibold">当前账号无权读取传感器与点位配置</h2>}
              description="查看传感器登记与点位校准状态需要站点资产读取权限，请联系管理员分配相应角色。"
            />
          ) : (
            <SensorCalibrationTab
              model={sensorModel}
              isAssetLoading={assetModelQuery.isPending}
              isAssetError={assetModelQuery.isError}
              assetError={assetModelQuery.error}
              canReadSnapshots={canReadSnapshots}
              isSnapshotsError={snapshotsQuery.isError}
              filter={sensorFilter}
              onFilterChange={(nextFilter) => {
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    filter: nextFilter === 'ALL' ? undefined : nextFilter,
                    inspect: undefined,
                  }),
                });
              }}
              onRetry={() => {
                void assetModelQuery.refetch();
                void snapshotsQuery.refetch();
              }}
              onInspectSensor={(inspectKey) => setInspect(inspectKey)}
            />
          )}
        </TabsContent>
      </Tabs>

      {siteId ? (
        <>
          <MeterInspectionSheet
            row={inspectedMeter}
            siteId={siteId}
            timezone={siteTimezone}
            windowFromMs={windowFromMs}
            windowToMs={windowToMs}
            csrfToken={csrfToken}
            canReadHistory={canReadHistory}
            canReadWorkOrders={canReadWorkOrders}
            canCreateWorkOrder={canCreateWorkOrder}
            onClose={() => setInspect(undefined)}
          />
          <SensorInspectionSheet
            row={inspectedSensor}
            siteId={siteId}
            timezone={siteTimezone}
            windowFromMs={windowFromMs}
            windowToMs={windowToMs}
            csrfToken={csrfToken}
            canReadHistory={canReadHistory}
            canReadWorkOrders={canReadWorkOrders}
            canCreateWorkOrder={canCreateWorkOrder}
            onClose={() => setInspect(undefined)}
          />
        </>
      ) : null}
    </Main>
  );
}
