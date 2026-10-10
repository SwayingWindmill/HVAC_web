import type { ReactNode } from 'react';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Link } from '@tanstack/react-router';
import { ArrowLeft, ArrowRight, RefreshCw, Snowflake, Waves } from 'lucide-react';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { MetricStrip } from '@/components/analysis/workspace-parts';
import { cn } from '@/lib/utils';
import {
  plantReading,
  type PlantCategory,
  type PlantDevice,
  type PlantReading,
  type PlantView,
} from '../plant-model';
import { useRealtimePlant } from '../use-realtime-plant';
import {
  CATEGORY_ICONS,
  deviceStatus,
  formatClock,
  KEY_READINGS,
  KeyReadings,
  LiveIndicator,
  POWER_KEY,
  ReadingValue,
} from '../device-presentation';

// Inside a plant node the equipment is already named, so readings use short labels.
const NODE_LABELS: Readonly<Record<string, string>> = {
  power: '功率',
  flow_rate: '流量',
  frequency: '频率',
  compressor_load: '负载率',
  leaving_chilled_water_temperature: '出水温度',
  fan_speed: '风机转速',
  leaving_water_temperature: '出塔水温',
};

function nodeLabel(sourceKey: string, fallback: string): string {
  return NODE_LABELS[sourceKey.slice(sourceKey.indexOf('.') + 1)] ?? fallback;
}

function PlantNode({ device }: { readonly device: PlantDevice }) {
  const status = deviceStatus(device);
  const powerKey = POWER_KEY[device.category];
  const readings = [powerKey, ...KEY_READINGS[device.category].slice(0, 2)]
    .filter((key): key is string => Boolean(key))
    .flatMap((key) => {
      const reading = device.reading(key);
      return reading ? [{ ...reading, label: nodeLabel(key, reading.label) }] : [];
    });
  return (
    <div className="rounded-lg border bg-background p-3">
      <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-1">
        <span className="text-sm font-medium">{device.name}</span>
        <StatusBadge tone={status.tone} label={status.label} />
      </div>
      <dl className="mt-2 space-y-1 text-xs">
        {readings.map((reading) => (
          <div key={reading.label} className="flex items-baseline justify-between gap-3">
            <dt className="whitespace-nowrap text-muted-foreground">{reading.label}</dt>
            <dd className="whitespace-nowrap text-sm font-medium"><ReadingValue reading={reading} /></dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function PlantColumn({ title, devices }: { readonly title: string; readonly devices: readonly PlantDevice[] }) {
  return (
    <section className="flex min-w-0 flex-col gap-2">
      <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
      {devices.length > 0
        ? devices.map((device) => <PlantNode key={device.deviceId} device={device} />)
        : <p className="rounded-lg border border-dashed px-3 py-4 text-center text-xs text-muted-foreground">未登记设备</p>}
    </section>
  );
}

function LoopConnector({ label, supply, ret }: {
  readonly label: string;
  readonly supply: PlantReading | null;
  readonly ret: PlantReading | null;
}) {
  return (
    <div className="flex w-28 flex-col items-center justify-center gap-2 self-center text-sm">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <span className="flex items-center gap-1.5">
        <span className="text-xs text-muted-foreground">供</span>
        <ReadingValue reading={supply} className="font-medium" />
        <ArrowRight className="size-3.5 text-muted-foreground" aria-hidden="true" />
      </span>
      <span className="flex items-center gap-1.5">
        <ArrowLeft className="size-3.5 text-muted-foreground" aria-hidden="true" />
        <span className="text-xs text-muted-foreground">回</span>
        <ReadingValue reading={ret} className="font-medium" />
      </span>
    </div>
  );
}

function PlantFlow({ plant }: { readonly plant: PlantView }) {
  const byCategory = (category: PlantCategory) => plant.groups.find((group) => group.category === category)?.devices ?? [];
  return (
    <Card className="col-span-12 xl:col-span-8">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Snowflake className="size-4" aria-hidden="true" />冷站系统</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto_minmax(0,1fr)] items-start gap-2">
          <PlantColumn title="冷却侧" devices={[...byCategory('COOLING_TOWER'), ...byCategory('COOLING_WATER_PUMP')]} />
          <LoopConnector
            label="冷却水"
            supply={plantReading(plant, 'cooling_tower.leaving_water_temperature')}
            ret={plantReading(plant, 'cooling_tower.entering_water_temperature')}
          />
          <PlantColumn title="冷水机组" devices={byCategory('CHILLER')} />
          <LoopConnector
            label="冷冻水"
            supply={plantReading(plant, 'btu_meter.supply_water_temperature')}
            ret={plantReading(plant, 'btu_meter.return_water_temperature')}
          />
          <PlantColumn title="冷冻侧" devices={byCategory('CHILLED_WATER_PUMP')} />
        </div>
      </CardContent>
    </Card>
  );
}

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
    <Card className="col-span-12 xl:col-span-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Waves className="size-4" aria-hidden="true" />水侧与室外工况</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
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
        <section className="border-t pt-3">
          <h3 className="text-xs font-medium text-muted-foreground">冷却水</h3>
          <ConditionRow label="进机组" reading={plantReading(plant, 'chiller.entering_cooling_water_temperature')} />
          <ConditionRow label="冷却塔逼近温度" reading={plantReading(plant, 'cooling_tower.approach_temperature')} />
        </section>
        <section className="border-t pt-3">
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

function metricValue(reading: PlantReading | null): ReactNode {
  return <ReadingValue reading={reading ? { ...reading, unit: null } : null} />;
}

export function RealtimeWorkspace() {
  const { site, plant, mode, registry, current, currentUnavailable, refresh } = useRealtimePlant();
  const loading = registry.isPending || (current.isPending && current.fetchStatus !== 'idle');

  return (
    <Main className="space-y-5">
      <PageHeader
        title="实时运行"
        actions={(
          <div className="flex items-center gap-4">
            <LiveIndicator mode={mode} />
            <span className="text-xs text-muted-foreground">数据时间 <span className="tabular-nums text-foreground">{formatClock(plant.latestSampleAt, site.timezone)}</span></span>
            <Button variant="outline" size="sm" onClick={refresh}>
              <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
            </Button>
          </div>
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
          <MetricStrip
            items={[
              { label: '冷站总功率', value: metricValue(plant.totalPower), unit: 'kW' },
              { label: '瞬时制冷量', value: metricValue(plant.coolingCapacity), unit: 'kW' },
              {
                label: '冷站 COP',
                value: plant.plantCop === null ? <span className="text-muted-foreground">—</span> : plant.plantCop.toFixed(2),
              },
              { label: '设备在线', value: `${plant.onlineCount} / ${plant.devices.length}`, unit: '台' },
            ]}
          />
          <div className="grid grid-cols-12 gap-5">
            <PlantFlow plant={plant} />
            <WaterSide plant={plant} />
          </div>
          <DeviceSnapshot plant={plant} siteId={site.id} timezone={site.timezone} />
        </>
      ) : null}
    </Main>
  );
}
