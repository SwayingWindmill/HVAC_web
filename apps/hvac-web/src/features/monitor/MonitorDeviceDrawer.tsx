import { useEffect, useMemo, useState } from 'react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Empty, EmptyDescription } from '@/components/ui/empty';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import type { Site } from '@/api/generated/platformGateway.gen';
import type { S2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { siteRoute } from '@/app/router-paths';
import { assetsDevicePath } from '@/features/assets/detail';
import { OperationalDetailDrawer } from '@/shared/ui';
import { StatusBadge } from '@/components/status-badge';
import { MonitorHistoryPanel } from './MonitorHistoryPanel';
import { CHILLED_PUMP_ART, CHILLER_ART, COOLING_PUMP_ART, TOWER_ART } from './reference-equipment-art';
import {
  alarmSeverityBadgeClass,
  alarmSeverityLabel,
  deviceStateBadgeClass,
  deviceStateLabel,
  type MonitorDevice,
} from './model';

interface MonitorDeviceDrawerProps {
  readonly item: MonitorDevice | null;
  readonly devices: readonly MonitorDevice[];
  readonly site: Readonly<Site>;
  readonly open: boolean;
  readonly defaultTab?: 'overview' | 'realtime' | 'trend' | 'alarm' | 'maintenance';
  readonly telemetryClient: S2TelemetryClient;
  readonly sessionCapability: string;
  readonly historyAllowed: boolean;
  readonly onClose: () => void;
  readonly onLocate: () => void;
  readonly onSwitchDevice: (deviceId: string) => void;
}

function formatInstant(value: string | null, timeZone: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', { timeZone, dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value));
}

function DrawerEmpty({ description }: { readonly description: string }) {
  return (
    <Empty className="min-h-32 border">
      <EmptyDescription>{description}</EmptyDescription>
    </Empty>
  );
}

export function MonitorDeviceDrawer({
  item,
  devices,
  site,
  open,
  defaultTab = 'overview',
  telemetryClient,
  sessionCapability,
  historyAllowed,
  onClose,
  onLocate,
  onSwitchDevice,
}: MonitorDeviceDrawerProps) {
  const [activeTab, setActiveTab] = useState(defaultTab);
  useEffect(() => setActiveTab(defaultTab), [defaultTab]);

  const points = useMemo(
    () => item?.state.points.filter((point) => point.state !== 'UNAVAILABLE') ?? [],
    [item],
  );
  if (!item) return null;

  const equipmentArt = item.kind === 'chiller' ? CHILLER_ART
    : item.kind === 'tower' ? TOWER_ART
      : item.kind === 'cooling-pump' ? COOLING_PUMP_ART
        : item.kind === 'chilled-pump' ? CHILLED_PUMP_ART
          : null;

  const overview = (
    <div className="hvac-monitor__drawer-section">
      <section className="hvac-monitor__drawer-device-summary">
        {equipmentArt ? <div className="hvac-monitor__drawer-device-art"><img src={equipmentArt} alt="" aria-hidden="true" draggable={false} /></div> : null}
        <div className="hvac-monitor__drawer-device-facts">
          <strong>{item.device.displayName}</strong>
          <div className="flex flex-wrap items-center gap-1.5">
            <Badge variant="outline" className={deviceStateBadgeClass(item.state)}>{deviceStateLabel(item.state)}</Badge>
            {item.activeAlarms.length ? (
              <Badge variant="destructive">{item.activeAlarms.length} 条活动告警</Badge>
            ) : (
              <Badge variant="outline">无活动告警</Badge>
            )}
          </div>
          <small>最近在线：{formatInstant(item.state.connection.lastSeenAt, site.timezone)}</small>
        </div>
      </section>
      <section>
        <h3 className="mb-3 text-sm font-semibold">关键实时数据</h3>
        <div className="hvac-monitor__drawer-metrics">
          {points.length > 0 ? points.slice(0, 6).map((point) => (
            <div key={point.pointId}>
              <span>{point.label}</span>
              <strong>{point.displayValue}{point.unit ? ` ${point.unit}` : ''}</strong>
            </div>
          )) : <DrawerEmpty description="当前设备没有可显示的实时测点" />}
        </div>
      </section>
    </div>
  );

  const realtimeContent = points.length > 0 ? (
    <div className="overflow-hidden rounded-md border">
      <Table aria-label="设备实时测点">
        <TableHeader className="bg-muted/20">
          <TableRow className="hover:bg-transparent">
            <TableHead>测点</TableHead>
            <TableHead className="text-right">当前值</TableHead>
            <TableHead className="w-28 text-center">数据状态</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {points.map((point) => (
            <TableRow key={point.pointId}>
              <TableCell className="font-medium text-foreground">{point.label}</TableCell>
              <TableCell className="text-right font-mono tabular-nums">{point.displayValue}{point.unit ? ` ${point.unit}` : ''}</TableCell>
              <TableCell className="text-center">
                <StatusBadge
                  tone={point.state === 'PRESENT' ? 'success' : 'neutral'}
                  label={point.state === 'PRESENT' ? '可用' : '未观测'}
                />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  ) : <DrawerEmpty description="当前设备没有可显示的实时测点" />;

  const alarmContent = item.activeAlarms.length > 0 ? (
    <div className="hvac-monitor__drawer-alarm-list">
      {item.activeAlarms.map((alarm) => (
        <a key={alarm.alarmId} href={`${siteRoute(site, 'alarms')}?alarm=${encodeURIComponent(alarm.alarmId)}&source=hvac-monitor`}>
          <Badge variant="outline" className={alarmSeverityBadgeClass[alarm.currentSeverity]}>{alarmSeverityLabel[alarm.currentSeverity]}</Badge>
          <span><strong>{alarm.title}</strong><small>{alarm.summary}</small></span>
          <span aria-hidden="true">›</span>
        </a>
      ))}
    </div>
  ) : <DrawerEmpty description="当前设备没有活动告警" />;

  const maintenanceContent = (
    <div className="space-y-3">
      <DrawerEmpty description="维护记录在设备与工单中统一管理" />
      <Button variant="outline" className="w-full" asChild>
        <a href={`${assetsDevicePath(site.id, item.device.id)}?origin=monitor&tab=maintenance`}>查看设备维护记录</a>
      </Button>
      <Button variant="outline" className="w-full" asChild>
        <a href={`${siteRoute(site, 'work-orders')}?device=${encodeURIComponent(item.device.id)}&source=hvac-monitor`}>查看相关工单</a>
      </Button>
    </div>
  );

  return (
    <OperationalDetailDrawer
      open={open}
      onClose={onClose}
      rootClassName="hvac-monitor__drawer"
      title={item.device.displayName}
      subtitle="设备快速详情"
      status={<Badge variant="outline" className={deviceStateBadgeClass(item.state)}>{deviceStateLabel(item.state)}</Badge>}
      headerExtra={(
        <Select value={item.device.id} onValueChange={onSwitchDevice}>
          <SelectTrigger className="min-w-36" aria-label="切换设备">
            <SelectValue placeholder="选择设备" />
          </SelectTrigger>
          <SelectContent><SelectGroup>
            {devices.map((device) => <SelectItem key={device.device.id} value={device.device.id}>{device.device.displayName}</SelectItem>)}
          </SelectGroup></SelectContent>
        </Select>
      )}
      activeTabKey={activeTab}
      onTabChange={(key) => setActiveTab(key as typeof activeTab)}
      tabs={[
        { key: 'overview', label: '概览', children: overview },
        { key: 'realtime', label: '实时数据', children: realtimeContent },
        {
          key: 'trend',
          label: '趋势',
          children: (
            <MonitorHistoryPanel
              site={site}
              device={item}
              client={telemetryClient}
              sessionCapability={sessionCapability}
              historyAllowed={historyAllowed}
              preferredKeys={item.kind === 'chiller'
                ? ['chiller_power', 'chiller.power', 'chiller_cooling_capacity', 'chiller.cooling_capacity']
                : item.kind === 'terminal'
                  ? ['zone_temperature', 'room_temperature', 'supply_air_temperature']
                  : []}
            />
          ),
        },
        { key: 'alarm', label: '告警', children: alarmContent },
        { key: 'maintenance', label: '维护', children: maintenanceContent },
      ]}
      footer={(
        <div className="hvac-monitor__drawer-footer flex flex-wrap justify-end gap-2">
          <Button variant="outline" onClick={onLocate}>在拓扑中定位</Button>
          <Button variant="outline" asChild><a href={`${assetsDevicePath(site.id, item.device.id)}?origin=monitor`}>查看完整设备详情</a></Button>
          <Button asChild>
            <a href={`${siteRoute(site, 'alarms')}?deviceId=${encodeURIComponent(item.device.id)}&device=${encodeURIComponent(item.device.displayName)}&source=hvac-monitor`}>查看告警</a>
          </Button>
        </div>
      )}
    />
  );
}
