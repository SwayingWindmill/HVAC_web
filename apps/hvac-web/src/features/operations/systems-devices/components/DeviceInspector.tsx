import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { Area, CartesianGrid, ComposedChart, Line, XAxis, YAxis } from 'recharts';
import { listWorkOrders } from '@/api/work-orders';
import { Fact } from '@/components/common/Fact';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Button } from '@/components/ui/button';
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { formatTelemetryUnit } from '@/domain/centralPlantTelemetry';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { deviceStatus, KEY_READINGS, POWER_KEY } from '../../realtime/device-presentation';
import type { PlantDevice } from '../../realtime/plant-model';
import { deviceDayHistoryQuery, MAX_HISTORY_KEYS } from '../device-history';
import { boundAssetId, deviceLocation } from '../device-ledger';

const siteRoute = getRouteApi('/_app/_site');

const CONNECTION_LABELS = { ONLINE: '在线', OFFLINE: '离线', UNKNOWN: '未知' } as const;

/** Numeric measurements worth a trend, the role's key readings first. */
function trendKeys(device: PlantDevice): string[] {
  const preferred = [POWER_KEY[device.category], ...KEY_READINGS[device.category]].filter((key): key is string => Boolean(key));
  const rank = (sourceKey: string) => {
    const index = preferred.indexOf(sourceKey);
    return index === -1 ? preferred.length : index;
  };
  return device.row.telemetryPoints
    .filter((point) => point.status === 'ACTIVE' && point.pointType === 'TELEMETRY' && point.valueType === 'NUMBER')
    .sort((left, right) => rank(left.sourceKey) - rank(right.sourceKey))
    .slice(0, MAX_HISTORY_KEYS)
    .map((point) => point.pointCode);
}

export function DeviceInspector({ device, categoryLabel, onClose }: {
  readonly device: PlantDevice | undefined;
  readonly categoryLabel: string;
  readonly onClose: () => void;
}) {
  return (
    <Sheet modal={false} open={Boolean(device)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent showOverlay={false} className="w-[560px] overflow-y-auto sm:max-w-[560px]">
        {device ? <DeviceDetail key={device.deviceId} device={device} categoryLabel={categoryLabel} /> : null}
      </SheetContent>
    </Sheet>
  );
}

function DeviceDetail({ device, categoryLabel }: { readonly device: PlantDevice; readonly categoryLabel: string }) {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const [creatingWorkOrder, setCreatingWorkOrder] = useState(false);
  const capabilities = principal.authorization.capabilities;
  const status = deviceStatus(device);
  const assetId = boundAssetId(device);
  const points = device.row.operational.points;

  const workOrders = useQuery({
    queryKey: workOrderKeys.source(site.id, 'ASSET', assetId ?? ''),
    queryFn: ({ signal }) => listWorkOrders({ sourceDomain: 'ASSET', sourceRef: assetId!, limit: 20 }, { siteId: site.id, signal }),
    enabled: Boolean(assetId) && capabilities.includes('work-order.list'),
  });

  return (
    <>
      <SheetHeader>
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground">{categoryLabel}</span>
          <StatusBadge tone={status.tone} label={status.label} />
        </div>
        <SheetTitle className="text-lg">{device.name}</SheetTitle>
        <SheetDescription>{deviceLocation(device)}</SheetDescription>
      </SheetHeader>

      <div className="space-y-6 px-4 pb-6">
        <dl className="divide-y rounded-lg border px-3">
          <Fact label="通信">{CONNECTION_LABELS[device.connection]}</Fact>
          <Fact label="最近数据">{formatTime(device.latestSampleAt ?? undefined, site.timezone)}</Fact>
          <Fact label="登记时间">{formatTime(device.row.device.createdAt, site.timezone)}</Fact>
        </dl>

        <section>
          <h3 className="mb-2 text-sm font-medium">当前读数</h3>
          {points.length === 0 ? (
            <p className="text-sm text-muted-foreground">该设备尚未登记点位</p>
          ) : (
            <dl className="grid grid-cols-2 gap-x-6 gap-y-0.5 rounded-lg border px-3 py-2">
              {points.map((point) => {
                const current = point.state === 'PRESENT' && point.freshness === 'FRESH' && point.quality === 'GOOD';
                return (
                  <div key={point.key} className="flex items-baseline justify-between gap-3 py-1 text-sm">
                    <dt className="text-muted-foreground">{point.label}</dt>
                    <dd className={cn('shrink-0 tabular-nums', !current && 'text-muted-foreground')} title={current ? undefined : '数据过期或质量降级'}>
                      {point.state === 'PRESENT' ? point.displayValue : '—'}
                      {point.state === 'PRESENT' && point.unit ? <span className="ml-0.5 text-xs text-muted-foreground">{point.unit}</span> : null}
                    </dd>
                  </div>
                );
              })}
            </dl>
          )}
        </section>

        <DayTrend device={device} />

        {assetId ? (
          <section>
            <div className="mb-2 flex items-center justify-between">
              <h3 className="text-sm font-medium">设备工单</h3>
              {capabilities.includes('work-order.create') ? (
                <Button size="sm" variant="outline" onClick={() => setCreatingWorkOrder(true)}>创建工单</Button>
              ) : null}
            </div>
            {!workOrders.data ? null : workOrders.data.items.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无工单</p>
            ) : (
              <ul className="divide-y rounded-lg border">
                {workOrders.data.items.map((workOrder) => (
                  <li key={workOrder.workOrderId} className="flex items-center justify-between gap-3 px-3 py-2 text-sm">
                    <Link
                      to="/operations/work-center"
                      search={{ site: site.id, view: 'all', inspect: workOrder.workOrderId }}
                      className="truncate hover:underline"
                    >
                      {workOrder.title}
                    </Link>
                    <span className="shrink-0 text-xs text-muted-foreground">{STATUS_LABELS[workOrder.status]}</span>
                  </li>
                ))}
              </ul>
            )}
            <CreateWorkOrderDialog
              open={creatingWorkOrder}
              onOpenChange={setCreatingWorkOrder}
              title={`为 ${device.name} 创建工单`}
              initial={{ title: `${device.name}：`, priority: 'MEDIUM', description: '' }}
              sourceReferences={[{ domain: 'ASSET', resourceId: assetId, relationship: 'ORIGIN' }]}
              onCreated={() => void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) })}
            />
          </section>
        ) : null}
      </div>
    </>
  );
}

function DayTrend({ device }: { readonly device: PlantDevice }) {
  const { site, principal } = siteRoute.useRouteContext();
  const keys = trendKeys(device);
  const [selected, setSelected] = useState(keys[0]);
  const history = useQuery({
    ...deviceDayHistoryQuery({
      siteId: site.id,
      deviceId: device.deviceId,
      keys,
      timezone: site.timezone,
      csrfToken: principal.session.csrfToken,
    }),
    enabled: keys.length > 0 && principal.authorization.capabilities.includes('telemetry.history.read'),
  });
  if (keys.length === 0) return null;

  const labelOf = (key: string) => device.row.operational.points.find((point) => point.key === key)?.label ?? key;
  const point = device.row.telemetryPoints.find((candidate) => candidate.pointCode === selected);
  const unit = formatTelemetryUnit(point?.unit);
  const hour = new Intl.DateTimeFormat('zh-CN', { timeZone: site.timezone, hour: '2-digit', minute: '2-digit', hour12: false });
  const data = (history.data?.get(selected) ?? []).map((bucket) => ({
    hour: hour.format(new Date(bucket.periodStart)),
    average: Number(bucket.average.toFixed(2)),
    range: [Number(bucket.minimum.toFixed(2)), Number(bucket.maximum.toFixed(2))],
  }));

  return (
    <section>
      <h3 className="mb-2 text-sm font-medium">最近 24 小时<span className="ml-2 text-xs font-normal text-muted-foreground">每小时平均值与范围</span></h3>
      <ToggleGroup
        type="single"
        variant="outline"
        size="sm"
        className="mb-3 flex-wrap justify-start"
        value={selected}
        onValueChange={(value) => { if (value) setSelected(value); }}
      >
        {keys.map((key) => <ToggleGroupItem key={key} value={key} className="text-xs">{labelOf(key)}</ToggleGroupItem>)}
      </ToggleGroup>
      {history.isPending ? (
        <Skeleton className="h-52" />
      ) : history.isError ? (
        <p className="text-sm text-destructive">历史数据暂不可读：{history.error.message}</p>
      ) : data.length === 0 ? (
        <p className="py-10 text-center text-sm text-muted-foreground">最近 24 小时没有记录</p>
      ) : (
        <ChartContainer
          className="aspect-auto h-52 w-full"
          config={{
            average: { label: `平均${unit ? `（${unit}）` : ''}`, color: 'var(--chart-1)' },
            range: { label: '最小–最大', color: 'var(--chart-1)' },
          }}
        >
          <ComposedChart accessibilityLayer data={data} margin={{ left: 4, right: 8 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="hour" axisLine={false} tickLine={false} minTickGap={24} />
            <YAxis axisLine={false} tickLine={false} width={44} domain={['auto', 'auto']} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Area isAnimationActive={false} dataKey="range" stroke="none" fill="var(--color-range)" fillOpacity={0.15} />
            <Line isAnimationActive={false} dataKey="average" stroke="var(--color-average)" strokeWidth={2} dot={false} />
          </ComposedChart>
        </ChartContainer>
      )}
    </section>
  );
}
