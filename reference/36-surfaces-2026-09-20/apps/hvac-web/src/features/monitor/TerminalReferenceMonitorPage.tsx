import { useMemo, type CSSProperties, type ReactNode } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { ApartmentOutlined, BuildOutlined, CloudOutlined, DashboardOutlined, EnvironmentOutlined, FireOutlined } from '@/shared/icons';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Empty, EmptyDescription } from '@/components/ui/empty';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { DataTable, DataTablePagination, StatusPillBadge, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import type { DashboardOverview } from '@/api/dashboard-overview';
import type { Site, SiteAssetModel } from '@/api/generated/platformGateway.gen';
import type { S2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { siteRoute } from '@/app/router-paths';
import { resolveDeviceBinding, resolveDeviceSpace } from '@/features/assets/model';
import {
  alarmSeverityBadgeClass,
  averagePointValues,
  deviceStateLabel,
  formatMonitorMetric,
  pointMetric,
  sumPointValues,
  type MonitorDevice,
} from './model';
import { MonitorHistoryPanel } from './MonitorHistoryPanel';
import './terminal-reference.css';

interface TerminalReferenceMonitorPageProps {
  readonly site: Readonly<Site>;
  readonly model: SiteAssetModel | undefined;
  readonly devices: readonly MonitorDevice[];
  readonly overview: DashboardOverview | undefined;
  readonly telemetryClient: S2TelemetryClient;
  readonly sessionCapability: string;
  readonly historyAllowed: boolean;
  readonly selectedBuildingId: string | null;
  readonly selectedFloorId: string | null;
  readonly selectedZoneKey: string | null;
  readonly onOpenOverview: () => void;
  readonly onSelectBuilding: (buildingId: string | null) => void;
  readonly onSelectFloor: (floorId: string | null) => void;
  readonly onSelectZone: (zoneKey: string) => void;
  readonly onOpenDevice: (deviceId: string) => void;
  readonly onOpenAnomaly: () => void;
  readonly onOpenEnergy: () => void;
}

function TerminalPanel({ title, action, children, className, id }: { readonly title: ReactNode; readonly action?: ReactNode; readonly children: ReactNode; readonly className?: string; readonly id?: string }) {
  return (
    <Card id={id} className={className}>
      <CardHeader className="border-b pb-3">
        <CardTitle>{title}</CardTitle>
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

function TerminalEmpty({ description }: { readonly description: string }) {
  return <Empty className="min-h-32 border"><EmptyDescription>{description}</EmptyDescription></Empty>;
}

function zoneContextKey(zone: SiteAssetModel['spaces'][number]): string {
  return zone.code.match(/ZONE-([A-Z])/i)?.[1]?.toUpperCase()
    ?? zone.displayName.match(/^([A-Z])\s*区/i)?.[1]?.toUpperCase()
    ?? zone.id;
}

function TerminalKpi({ label, value, note, icon, onClick }: {
  readonly label: string;
  readonly value: string;
  readonly note: string;
  readonly icon: ReactNode;
  readonly onClick: () => void;
}) {
  return (
    <button type="button" className="hvac-terminal-ref__kpi" onClick={onClick}>
      <span className="hvac-terminal-ref__kpi-icon">{icon}</span>
      <span><small>{label}</small><strong>{value}</strong><em>{note}</em></span>
    </button>
  );
}

export function TerminalReferenceMonitorPage({
  site,
  model,
  devices,
  overview,
  telemetryClient,
  sessionCapability,
  historyAllowed,
  selectedBuildingId,
  selectedFloorId,
  selectedZoneKey,
  onOpenOverview,
  onSelectBuilding,
  onSelectFloor,
  onSelectZone,
  onOpenDevice,
  onOpenAnomaly,
  onOpenEnergy,
}: TerminalReferenceMonitorPageProps) {
  const terminals = devices.filter((item) => item.kind === 'terminal');

  const buildings = model?.spaces.filter((space) => space.spaceType === 'BUILDING') ?? [];
  const floors = model?.spaces.filter((space) => space.spaceType === 'FLOOR') ?? [];
  const visibleFloors = selectedBuildingId ? floors.filter((space) => space.parentSpaceId === selectedBuildingId) : floors;
  const visibleFloorIds = new Set(visibleFloors.map((space) => space.id));
  const zones = (model?.spaces.filter((space) => space.spaceType === 'ZONE' || space.spaceType === 'ROOM') ?? []).filter((space) => {
    if (selectedFloorId) return space.parentSpaceId === selectedFloorId;
    if (selectedBuildingId && visibleFloorIds.size > 0) return Boolean(space.parentSpaceId && visibleFloorIds.has(space.parentSpaceId));
    return true;
  });
  const selectedZone = selectedZoneKey ? zones.find((zone) => zoneContextKey(zone) === selectedZoneKey) ?? null : null;

  const terminalBySpaceId = new Map<string, MonitorDevice[]>();
  if (model) {
    const assetById = new Map(model.assets.map((asset) => [asset.id, asset]));
    const spaceById = new Map(model.spaces.map((space) => [space.id, space]));
    for (const terminal of terminals) {
      const binding = resolveDeviceBinding(terminal.device, model.relationships, assetById);
      const spaceState = resolveDeviceSpace(terminal.device, binding, model.relationships, spaceById);
      if (spaceState.state !== 'bound') continue;
      const rows = terminalBySpaceId.get(spaceState.space.id) ?? [];
      rows.push(terminal);
      terminalBySpaceId.set(spaceState.space.id, rows);
    }
  }

  const zonePresentation = (zone: SiteAssetModel['spaces'][number]) => {
    const zoneDevices = terminalBySpaceId.get(zone.id) ?? [];
    const zoneAlarms = zoneDevices.flatMap((item) => item.activeAlarms);
    const severeAlarm = zoneAlarms.some((alarm) => alarm.currentSeverity === 'CRITICAL' || alarm.currentSeverity === 'MAJOR');
    const warningAlarm = !severeAlarm && zoneAlarms.some((alarm) => alarm.currentSeverity === 'WARNING' || alarm.currentSeverity === 'MINOR');
    const allOffline = zoneDevices.length > 0 && zoneDevices.every((item) => item.state.connection.state !== 'ONLINE');
    const temperature = averagePointValues(zoneDevices, ['zone_temperature', 'room_temperature', '区域温度', '室温']);
    const temperatureText = temperature.observed > 0 ? formatMonitorMetric(temperature.value, temperature.unit ?? '°C') : '温度待确认';
    const state = severeAlarm ? 'alarm' : warningAlarm ? 'warning' : allOffline ? 'offline' : 'unknown';
    const label = severeAlarm ? '异常' : warningAlarm ? '预警' : allOffline ? '离线' : '状态待确认';
    return { zoneDevices, zoneAlarms, temperatureText, state, label };
  };

  const online = terminals.filter((item) => item.state.connection.state === 'ONLINE').length;
  const averageRoomTemperature = averagePointValues(terminals, ['zone_temperature', 'room_temperature', '区域温度', '室温']);
  const terminalCoolingLoad = sumPointValues(terminals, ['cooling_load', '冷负荷']);
  const terminalAlarmDevices = terminals.filter((item) => item.activeAlarms.length > 0);
  const aggregateTerminalNodes = ['ahu', 'vav-fcu']
    .map((key) => overview?.topology.find((item) => item.key === key))
    .filter((item): item is NonNullable<typeof item> => Boolean(item));
  const aggregateTotal = aggregateTerminalNodes.length > 0 && aggregateTerminalNodes.every((item) => item.total != null)
    ? aggregateTerminalNodes.reduce((total, item) => total + (item.total ?? 0), 0)
    : null;
  const aggregateRunning = aggregateTerminalNodes.length > 0 && aggregateTerminalNodes.every((item) => item.running != null)
    ? aggregateTerminalNodes.reduce((total, item) => total + (item.running ?? 0), 0)
    : null;
  const trendDevice = terminals[0] ?? null;
  const selectedPresentation = selectedZone ? zonePresentation(selectedZone) : null;

  const terminalColumns = useMemo<Array<ColumnDef<DataTableFeatures, MonitorDevice>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选末端设备"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.device.displayName}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'device',
      header: '末端设备',
      cell: ({ row }) => (
        <Button variant="link" className="h-auto p-0 font-medium" onClick={() => onOpenDevice(row.original.device.id)}>
          {row.original.device.displayName}
        </Button>
      ),
    },
    {
      id: 'connection',
      header: '连接状态',
      cell: ({ row }) => (
        <StatusPillBadge
          tone={row.original.state.connection.state === 'ONLINE' ? 'success' : row.original.state.connection.state === 'OFFLINE' ? 'destructive' : 'neutral'}
          label={deviceStateLabel(row.original.state)}
        />
      ),
    },
    { id: 'roomTemp', header: '区域温度', cell: ({ row }) => <span className="font-mono tabular-nums">{pointMetric(row.original.state.points, ['室温', 'zone temperature', 'room temperature'])}</span> },
    { id: 'supplyAirTemp', header: '送风温度', cell: ({ row }) => <span className="font-mono tabular-nums">{pointMetric(row.original.state.points, ['送风温度', 'supply air'])}</span> },
    { id: 'valve', header: '风阀 / 水阀', cell: ({ row }) => <span className="font-mono tabular-nums">{pointMetric(row.original.state.points, ['阀门开度', 'valve', 'damper'])}</span> },
    {
      id: 'alarms',
      header: '告警',
      cell: ({ row }) => row.original.activeAlarms.length > 0 ? (
        <Button variant="link" className="h-auto p-0 font-semibold text-rose-600 dark:text-rose-400" asChild>
          <a href={`${siteRoute(site, 'alarms')}?deviceId=${encodeURIComponent(row.original.device.id)}&device=${encodeURIComponent(row.original.device.displayName)}&source=terminal-monitor&alarm=${encodeURIComponent(row.original.activeAlarms[0]!.alarmId)}`}>
            {row.original.activeAlarms.length}
          </a>
        </Button>
      ) : '—',
    },
  ], [onOpenDevice, site]);

  const terminalTable = useDataTable({
    key: 'surface-08-terminal-monitor-devices',
    data: [...terminals],
    columns: terminalColumns,
    pageSize: 10,
    getRowId: (row) => row.device.id,
  });

  return (
    <div className="hvac-terminal-ref" data-testid="hvac-terminal-reference-page">
      <div className="hvac-terminal-ref__breadcrumb">
        <Button variant="link" className="h-auto p-0" onClick={onOpenOverview}>运行监控</Button><span>/</span><strong>空调末端监控</strong>
      </div>

      <section className="hvac-terminal-ref__kpis" aria-label="空调末端关键指标">
        <TerminalKpi icon={<DashboardOutlined />} label="舒适度达标率" value={overview?.kpis.comfortRatePercent != null ? `${overview.kpis.comfortRatePercent.toFixed(1)}%` : '—'} note="查看区域舒适度" onClick={() => document.getElementById('terminal-ref-comfort')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })} />
        <TerminalKpi icon={<ApartmentOutlined />} label="区域数量" value={zones.length > 0 ? String(zones.length) : '—'} note="当前监控范围" onClick={() => document.getElementById('terminal-ref-floor')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })} />
        <TerminalKpi icon={<BuildOutlined />} label="在线设备" value={terminals.length > 0 ? `${online}/${terminals.length}` : aggregateRunning != null && aggregateTotal != null ? `${aggregateRunning}/${aggregateTotal}` : '—'} note="查看末端设备" onClick={() => document.getElementById('terminal-ref-devices')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })} />
        <TerminalKpi icon={<FireOutlined />} label="超温区域" value="—" note="当前无可验证聚合" onClick={onOpenAnomaly} />
        <TerminalKpi icon={<EnvironmentOutlined />} label="平均室温" value={formatMonitorMetric(averageRoomTemperature.value, averageRoomTemperature.unit ?? '°C')} note="查看温度趋势" onClick={() => document.getElementById('terminal-ref-trend')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })} />
        <TerminalKpi icon={<CloudOutlined />} label="区域总冷负荷" value={formatMonitorMetric(terminalCoolingLoad.value, terminalCoolingLoad.unit ?? 'kW')} note="查看系统能流" onClick={onOpenEnergy} />
      </section>

      <Card size="sm" className="hvac-terminal-ref__context-card">
        <CardContent>
          <div className="hvac-terminal-ref__context">
            <strong>监控范围</strong>
            <Select
              value={selectedBuildingId ?? '__all__'}
              disabled={buildings.length === 0}
              onValueChange={(value) => onSelectBuilding(value === '__all__' ? null : value)}
            >
              <SelectTrigger aria-label="选择楼栋" className="min-w-36">
                <SelectValue placeholder={buildings.length > 0 ? '全部楼栋' : '当前站点未登记楼栋'} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__all__">全部楼栋</SelectItem>
                {buildings.map((space) => <SelectItem key={space.id} value={space.id}>{space.displayName}</SelectItem>)}
              </SelectContent>
            </Select>
            <Select
              value={selectedFloorId ?? '__all__'}
              disabled={visibleFloors.length === 0}
              onValueChange={(value) => onSelectFloor(value === '__all__' ? null : value)}
            >
              <SelectTrigger aria-label="选择楼层" className="min-w-36">
                <SelectValue placeholder={visibleFloors.length > 0 ? '全部楼层' : '当前范围未登记楼层'} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__all__">全部楼层</SelectItem>
                {visibleFloors.map((space) => <SelectItem key={space.id} value={space.id}>{space.displayName}</SelectItem>)}
              </SelectContent>
            </Select>
            <span>{zones.length} 个可见区域</span>
            {selectedZone ? <Badge variant="outline">当前区域：{selectedZone.displayName}</Badge> : null}
          </div>
        </CardContent>
      </Card>

      <section className="hvac-terminal-ref__main-grid">
        <TerminalPanel id="terminal-ref-floor" title="楼层 / 区域状态" className="hvac-terminal-ref__floor-card">
          <div className={`hvac-terminal-ref__floor-plan${zones.length === 0 ? ' is-empty' : ''}`}>
            {zones.length > 0 ? zones.slice(0, 12).map((zone, index) => {
              const presentation = zonePresentation(zone);
              const zoneKey = zoneContextKey(zone);
              return (
                <button
                  type="button"
                  key={zone.id}
                  className={`is-${presentation.state}${selectedZone?.id === zone.id ? ' is-selected' : ''}`}
                  data-zone-index={index}
                  onClick={() => onSelectZone(zoneKey)}
                >
                  <span><strong>{zone.displayName}</strong><Badge variant="outline">{presentation.label}</Badge></span>
                  <b>{presentation.temperatureText}</b>
                  <small>{presentation.zoneDevices.length > 0 ? `${presentation.zoneDevices.length} 台末端设备` : '未关联末端设备'}</small>
                  {presentation.zoneAlarms.length > 0 ? <em>{presentation.zoneAlarms.length} 条活动告警</em> : null}
                </button>
              );
            }) : (
              <div className="hvac-terminal-ref__floor-empty">
                <div className="hvac-terminal-ref__floor-outline" aria-hidden="true"><i /><i /><i /><i /><i /><i /><i /><i /></div>
                <TerminalEmpty description="当前站点未登记楼栋 / 楼层 / 区域空间，无法生成区域状态图" />
              </div>
            )}
          </div>
          <div className="hvac-terminal-ref__legend">
            <span className="is-comfort">舒适</span><span className="is-cold">过冷</span><span className="is-hot">过热 / 异常</span><span className="is-offline">离线</span><span className="is-unknown">状态待确认</span>
          </div>
        </TerminalPanel>

        <div className="hvac-terminal-ref__right-stack">
          <TerminalPanel id="terminal-ref-comfort" title="区域舒适度" action={selectedZone ? <Button variant="link" size="sm" onClick={onOpenAnomaly}>异常定位</Button> : undefined}>
            <div className="hvac-terminal-ref__comfort">
              <div className="hvac-terminal-ref__comfort-ring" style={{ '--comfort-rate': `${overview?.kpis.comfortRatePercent ?? 0}%` } as CSSProperties}>
                <strong>{overview?.kpis.comfortRatePercent != null ? `${overview.kpis.comfortRatePercent.toFixed(1)}%` : '—'}</strong><span>达标率</span>
              </div>
              <div>
                <strong>{selectedZone?.displayName ?? '站点舒适度'}</strong>
                <span>{selectedPresentation ? `${selectedPresentation.label} · ${selectedPresentation.temperatureText}` : '当前为站点聚合结果'}</span>
                <small>{selectedZone ? '舒适状态待区域模型确认' : '选择区域查看设备、温度与告警'}</small>
              </div>
            </div>
          </TerminalPanel>

          <TerminalPanel
            title="末端告警"
            action={terminalAlarmDevices.length > 0 ? <Button variant="link" size="sm" asChild><a href={`${siteRoute(site, 'alarms')}?source=terminal-monitor`}>查看全部</a></Button> : undefined}
          >
            {terminalAlarmDevices.length > 0 ? (
              <div className="hvac-terminal-ref__alarms">
                {terminalAlarmDevices.slice(0, 3).map((item) => {
                  const alarm = item.activeAlarms[0]!;
                  return (
                    <a key={alarm.alarmId} href={`${siteRoute(site, 'alarms')}?deviceId=${encodeURIComponent(item.device.id)}&alarm=${encodeURIComponent(alarm.alarmId)}&source=terminal-monitor`}>
                      <Badge variant="outline" className={alarmSeverityBadgeClass[alarm.currentSeverity]}>{alarm.currentSeverity === 'CRITICAL' || alarm.currentSeverity === 'MAJOR' ? '异常' : '预警'}</Badge>
                      <span><strong>{alarm.title}</strong><small>{item.device.displayName}</small></span>
                    </a>
                  );
                })}
              </div>
            ) : <TerminalEmpty description="当前没有末端设备活动告警" />}
          </TerminalPanel>

          <TerminalPanel id="terminal-ref-trend" title="温度趋势">
            <MonitorHistoryPanel
              site={site}
              device={trendDevice}
              client={telemetryClient}
              sessionCapability={sessionCapability}
              historyAllowed={historyAllowed}
              preferredKeys={['zone_temperature', 'room_temperature', 'supply_air_temperature']}
            />
          </TerminalPanel>
        </div>
      </section>

      <TerminalPanel id="terminal-ref-devices" title="末端设备列表" className="hvac-terminal-ref__device-card">
        {terminals.length > 0 ? (
          <div className="space-y-4">
            <DataTable
              table={terminalTable}
              tableAriaLabel="末端设备列表"
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'select' ? 'w-12 text-center' :
                  header.id === 'device' ? 'min-w-40' :
                  header.id === 'connection' ? 'w-28 text-center' :
                  ['roomTemp', 'supplyAirTemp', 'valve'].includes(header.id) ? 'text-right' :
                  header.id === 'alarms' ? 'w-24 text-center' :
                  undefined,
              })}
              getRowProps={(row) => ({
                className: 'cursor-pointer',
                onClick: (event) => {
                  if (!(event.target as HTMLElement).closest('button,a,input,[role="checkbox"]')) onOpenDevice(row.original.device.id);
                },
              })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'select' || cell.column.id === 'connection' || cell.column.id === 'alarms'
                    ? 'text-center'
                    : ['roomTemp', 'supplyAirTemp', 'valve'].includes(cell.column.id)
                      ? 'text-right'
                      : undefined,
              })}
              footer={(
                <DataTablePagination
                  table={terminalTable}
                  totalRows={terminals.length}
                />
              )}
            />
          </div>
        ) : <TerminalEmpty description="当前没有可显示的末端设备" />}
      </TerminalPanel>
    </div>
  );
}
