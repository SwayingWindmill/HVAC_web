import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Link } from '@tanstack/react-router';
import { RefreshCw } from 'lucide-react';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { FactList, FactRow } from '@/blocks/fact-list';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { equipmentStatus, plantReading, type EquipmentStatus, type PlantCategory, type PlantView } from '../plant-model';
import { useRealtimePlant } from '../use-realtime-plant';
import {
  CATEGORY_ICONS,
  deviceStatus,
  formatClock,
  KeyReadings,
  LiveIndicator,
  POWER_KEY,
  ReadingMetric,
  ReadingValue,
} from '../device-presentation';
import { PlantDiagram } from './PlantDiagram';

/** The summed current value of a key across a category; null unless every device reports it as current. */
function sumCurrent(plant: PlantView, category: PlantCategory, sourceKey: string): number | null {
  const readings = plant.devices.filter((device) => device.category === category).map((device) => device.reading(sourceKey));
  if (readings.length === 0 || readings.some((reading) => !reading?.current || reading.numeric === null)) return null;
  return readings.reduce((sum, reading) => sum + reading!.numeric!, 0);
}

function equipmentDetail(status: EquipmentStatus): string {
  const issues = [status.faults > 0 ? `${status.faults} 台故障` : null, status.offline > 0 ? `${status.offline} 台离线` : null].filter(Boolean);
  return issues.length > 0 ? issues.join('，') : '没有故障或离线设备';
}

function freshnessDetail(status: EquipmentStatus): string {
  const issues = [status.withoutData > 0 ? `${status.withoutData} 台无数据` : null, status.stale > 0 ? `${status.stale} 台数据过期` : null].filter(Boolean);
  return issues.length > 0 ? issues.join('，') : '数据均为最新';
}

const CONDITION_COLUMNS: readonly {
  readonly title: string;
  readonly tone: string;
  readonly headline: readonly [string, string];
  readonly rows: readonly (readonly [string, string])[];
}[] = [
  {
    title: '冷冻水',
    tone: 'bg-chart-1',
    headline: ['供回水温差', 'btu_meter.temperature_difference'],
    rows: [['供水', 'btu_meter.supply_water_temperature'], ['回水', 'btu_meter.return_water_temperature'], ['流量', 'btu_meter.flow_rate']],
  },
  {
    title: '冷却水',
    tone: 'bg-chart-2',
    headline: ['冷却塔逼近温度', 'cooling_tower.approach_temperature'],
    rows: [['出塔', 'cooling_tower.leaving_water_temperature'], ['进塔', 'cooling_tower.entering_water_temperature'], ['流量', 'cwp.flow_rate']],
  },
  {
    title: '室内与室外',
    tone: 'bg-muted-foreground/60',
    headline: ['室内温度', 'zone.temperature'],
    rows: [['室外干球', 'weather.ambient_dry_bulb_temperature'], ['室外湿球', 'weather.ambient_wet_bulb_temperature'], ['室外湿度', 'weather.relative_humidity']],
  },
];

/** The two water loops and the air they serve, each led by the reading an operator checks first. */
function WaterSide({ plant }: { readonly plant: PlantView }) {
  return (
    <Card role="region" aria-label="水侧与室内外工况">
      <CardHeader>
        <CardTitle>水侧与室内外工况</CardTitle>
        <CardDescription>冷冻水看温差，冷却水看逼近温度，末端看室内温度</CardDescription>
      </CardHeader>
      <CardContent className="grid grid-cols-3 divide-x">
        {CONDITION_COLUMNS.map((column) => (
          <section key={column.title} className="flex flex-col gap-4 px-6 first:pl-0 last:pr-0">
            <h3 className="flex items-center gap-2 text-sm font-medium">
              <span className={cn('size-2 rounded-[2px]', column.tone)} aria-hidden="true" />
              {column.title}
            </h3>
            <div>
              <p className="text-xs text-muted-foreground">{column.headline[0]}</p>
              <p className="mt-1 text-[1.75rem] leading-none font-medium tracking-tight">
                <ReadingValue reading={plantReading(plant, column.headline[1])} />
              </p>
            </div>
            <FactList>
              {column.rows.map(([label, key]) => (
                <FactRow key={key} label={label}><ReadingValue reading={plantReading(plant, key)} /></FactRow>
              ))}
            </FactList>
          </section>
        ))}
      </CardContent>
    </Card>
  );
}

function DeviceSnapshot({ plant, siteId, timezone }: { readonly plant: PlantView; readonly siteId: string; readonly timezone: string }) {
  return (
    <Card className="pb-0">
      <CardHeader>
        <CardTitle>设备运行快照</CardTitle>
        <CardDescription>按系统分组的当前读数，点击设备查看详情</CardDescription>
      </CardHeader>
      <CardContent className="border-t px-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[22%]">设备</TableHead>
              <TableHead className="w-[12%]">状态</TableHead>
              <TableHead className="w-[10%] text-right">功率</TableHead>
              <TableHead>关键工况</TableHead>
              <TableHead className="w-[10%] text-right">数据时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {plant.groups.map((group) => {
              const Icon = CATEGORY_ICONS[group.category];
              return [
                <TableRow key={group.category} className="bg-muted/30 hover:bg-muted/30">
                  <TableCell colSpan={5} className="py-1.5">
                    <span className="flex items-center gap-2 text-xs font-medium">
                      {Icon ? <Icon className="size-3.5" aria-hidden="true" /> : null}
                      {group.label}
                      <span className="font-normal text-muted-foreground">{group.devices.length} 台</span>
                    </span>
                  </TableCell>
                </TableRow>,
                ...group.devices.map((device) => {
                  const status = deviceStatus(device);
                  const powerKey = POWER_KEY[device.category];
                  return (
                    <TableRow key={device.deviceId}>
                      <TableCell className="font-medium">
                        <Link
                          to="/operations/systems-devices"
                          search={{ site: siteId, inspect: device.deviceId }}
                          className="hover:underline"
                        >
                          {device.name}
                        </Link>
                      </TableCell>
                      <TableCell><StatusBadge tone={status.tone} label={status.label} /></TableCell>
                      <TableCell className="text-right">{powerKey ? <ReadingValue reading={device.reading(powerKey)} /> : <span className="text-muted-foreground">—</span>}</TableCell>
                      <TableCell className="text-sm"><KeyReadings device={device} /></TableCell>
                      <TableCell className={cn('text-right tabular-nums', device.hasStaleData && 'text-amber-600 dark:text-amber-400')}>
                        {formatClock(device.latestSampleAt, timezone)}
                        {device.hasStaleData ? <span className="block text-[11px]">数据过期</span> : null}
                      </TableCell>
                    </TableRow>
                  );
                }),
              ];
            })}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

export function RealtimeWorkspace() {
  const { site, plant, mode, registry, current, currentUnavailable, refresh } = useRealtimePlant();
  const loading = registry.isPending || (current.isPending && current.fetchStatus !== 'idle');
  const equipment = equipmentStatus(plant);
  const chillerPower = sumCurrent(plant, 'CHILLER', 'chiller.power');

  return (
    <Main>
      <PageHeader
        title="实时运行"
        description={`${site.displayName} · 数据时间 ${formatClock(plant.latestSampleAt, site.timezone)}`}
        meta={<LiveIndicator mode={mode} />}
        actions={(
          <Button variant="outline" size="sm" onClick={refresh}>
            <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
          </Button>
        )}
      />

      {registry.isError ? (
        <Alert variant="destructive">
          <AlertTitle>设备台账暂不可用</AlertTitle>
          <AlertDescription>{registry.error.message}</AlertDescription>
        </Alert>
      ) : null}
      {currentUnavailable ? (
        <Alert>
          <AlertTitle>当前工况暂不可读</AlertTitle>
          <AlertDescription>设备台账已加载，但当前观测值无法读取；页面不会显示推测数值。</AlertDescription>
        </Alert>
      ) : null}

      {loading ? <Skeleton className="h-96" /> : null}

      {!loading && registry.isSuccess && plant.devices.length === 0 ? (
        <Alert>
          <AlertTitle>该站点尚未登记设备</AlertTitle>
          <AlertDescription>在设备台账中登记冷站设备与点位后，这里会显示实时工况。</AlertDescription>
        </Alert>
      ) : null}

      {!loading && plant.devices.length > 0 ? (
        <>
          <MetricGrid ariaLabel="冷站实时概况" className="@5xl/main:grid-cols-5">
            <MetricCard
              label="冷站总功率"
              value={<ReadingMetric reading={plant.totalPower} digits={1} unit="kW" />}
              lead={chillerPower === null ? '空调总电表实时读数' : `其中冷水机组 ${formatDecimal(chillerPower, 1)} kW`}
              detail="空调总电表"
            />
            <MetricCard
              label="瞬时制冷量"
              value={<ReadingMetric reading={plant.coolingCapacity} digits={0} unit="kW" />}
              lead={<>供回水温差 <ReadingValue reading={plantReading(plant, 'btu_meter.temperature_difference')} /></>}
              detail="冷量表"
            />
            <MetricCard
              label="冷站 COP"
              value={<MetricValue value={plant.plantCop === null ? null : formatDecimal(plant.plantCop, 2)} />}
              lead="制冷量 ÷ 总功率"
              detail="越高越省电"
            />
            <MetricCard
              label="主要设备运行"
              value={<MetricValue value={`${equipment.running} / ${equipment.total}`} unit="台" />}
              lead={equipmentDetail(equipment)}
              detail="冷机、水泵与冷却塔"
            />
            <MetricCard
              label="设备在线"
              value={<MetricValue value={`${plant.onlineCount} / ${plant.devices.length}`} unit="台" />}
              lead={freshnessDetail(equipment)}
              detail="按设备最近一次上报判断"
            />
          </MetricGrid>
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>冷站系统</CardTitle>
              <CardDescription>冷却水与冷冻水两个环路，点击设备查看详情</CardDescription>
            </CardHeader>
            <CardContent>
              <PlantDiagram plant={plant} siteId={site.id} />
            </CardContent>
          </Card>
          <WaterSide plant={plant} />
          <DeviceSnapshot plant={plant} siteId={site.id} timezone={site.timezone} />
        </>
      ) : null}
    </Main>
  );
}
