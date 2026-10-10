import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Link } from '@tanstack/react-router';
import { RefreshCw, Waves } from 'lucide-react';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
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
import { plantReading, type PlantReading, type PlantView } from '../plant-model';
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

function ConditionRow({ label, reading }: { readonly label: string; readonly reading: PlantReading | null }) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1.5 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <ReadingValue reading={reading} className="font-medium" />
    </div>
  );
}

function WaterSide({ plant }: { readonly plant: PlantView }) {
  return (
    <Card className="@container/card">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Waves className="size-4" aria-hidden="true" />水侧与室外工况</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4 @3xl/card:grid-cols-3 @3xl/card:gap-8">
        <section>
          <h3 className="text-xs font-medium text-muted-foreground">冷冻水</h3>
          <div className="mt-2 grid grid-cols-3 gap-2">
            {([
              ['供水', 'btu_meter.supply_water_temperature'],
              ['回水', 'btu_meter.return_water_temperature'],
              ['温差', 'btu_meter.temperature_difference'],
            ] as const).map(([label, key]) => (
              <div key={key}>
                <p className="text-xs text-muted-foreground">{label}</p>
                <p className="text-2xl font-semibold"><ReadingValue reading={plantReading(plant, key)} /></p>
              </div>
            ))}
          </div>
          <ConditionRow label="冷冻水流量" reading={plantReading(plant, 'btu_meter.flow_rate')} />
        </section>
        <section className="border-t pt-3 @3xl/card:border-t-0 @3xl/card:pt-0">
          <h3 className="text-xs font-medium text-muted-foreground">冷却水</h3>
          <ConditionRow label="进机组" reading={plantReading(plant, 'chiller.entering_cooling_water_temperature')} />
          <ConditionRow label="冷却塔逼近温度" reading={plantReading(plant, 'cooling_tower.approach_temperature')} />
        </section>
        <section className="border-t pt-3 @3xl/card:border-t-0 @3xl/card:pt-0">
          <h3 className="text-xs font-medium text-muted-foreground">室外</h3>
          <ConditionRow label="干球温度" reading={plantReading(plant, 'weather.ambient_dry_bulb_temperature')} />
          <ConditionRow label="湿球温度" reading={plantReading(plant, 'weather.ambient_wet_bulb_temperature')} />
          <ConditionRow label="相对湿度" reading={plantReading(plant, 'weather.relative_humidity')} />
        </section>
      </CardContent>
    </Card>
  );
}

function DeviceSnapshot({ plant, siteId, timezone }: { readonly plant: PlantView; readonly siteId: string; readonly timezone: string }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>设备运行快照</CardTitle>
      </CardHeader>
      <CardContent>
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

  return (
    <Main className="@container/main flex flex-col gap-4 md:gap-6">
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
          <MetricGrid ariaLabel="冷站实时概况">
            <MetricCard
              label="冷站总功率"
              value={<ReadingMetric reading={plant.totalPower} digits={1} unit="kW" />}
              lead={<>冷水机组 <ReadingValue reading={plantReading(plant, 'chiller.power')} /></>}
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
              lead={<>主机 COP <ReadingValue reading={plantReading(plant, 'chiller.cop')} /></>}
              detail="制冷量 ÷ 总功率"
            />
            <MetricCard
              label="设备在线"
              value={<MetricValue value={`${plant.onlineCount} / ${plant.devices.length}`} unit="台" />}
              lead={`${plant.devices.filter((device) => device.connection !== 'OFFLINE' && device.runState === 'RUNNING').length} 台运行`}
              detail={plant.devices.some((device) => device.hasStaleData) ? '部分设备数据过期' : '全部设备数据为最新'}
            />
          </MetricGrid>
          <div className="grid gap-4 md:gap-6 @6xl/main:grid-cols-[minmax(0,3fr)_minmax(18rem,1fr)]">
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
          </div>
          <DeviceSnapshot plant={plant} siteId={site.id} timezone={site.timezone} />
        </>
      ) : null}
    </Main>
  );
}
