import { useMemo } from 'react';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { ChevronRight, RefreshCw, Search } from 'lucide-react';
import { StatusBadge } from '@/components/common/StatusBadge';
import { MetricStrip } from '@/components/analysis/workspace-parts';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { openWorkOrdersQuery } from '@/features/work-orders/work-order-queries';
import { cn } from '@/lib/utils';
import {
  CATEGORY_ICONS,
  deviceStatus,
  formatClock,
  LiveIndicator,
  POWER_KEY,
  ReadingValue,
} from '../../realtime/device-presentation';
import { PLANT_CATEGORY_LABELS, type PlantCategory, type PlantDevice } from '../../realtime/plant-model';
import { useRealtimePlant } from '../../realtime/use-realtime-plant';
import { boundAssetId, deviceLocation, needsAttention } from '../device-ledger';
import { DeviceInspector } from './DeviceInspector';

type StatusFilter = 'running' | 'stopped' | 'attention';

const STATUS_FILTER_LABELS: Readonly<Record<StatusFilter, string>> = {
  running: '运行',
  stopped: '停机',
  attention: '需关注',
};

const pageRoute = getRouteApi('/_app/_site/operations/systems-devices');

function matchesStatus(device: PlantDevice, status: StatusFilter | undefined): boolean {
  if (status === 'running') return device.runState === 'RUNNING';
  if (status === 'stopped') return device.runState === 'STOPPED';
  if (status === 'attention') return needsAttention(device);
  return true;
}

export function SystemsDevicesWorkspace() {
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/systems-devices' });
  const { site, plant, mode, registry, current, currentUnavailable, refresh } = useRealtimePlant();
  const { principal } = pageRoute.useRouteContext();
  const loading = registry.isPending || (current.isPending && current.fetchStatus !== 'idle');

  // Open work orders raised against equipment, counted per Asset. Shares the work
  // center's open view and its cache.
  const workOrders = useQuery({
    ...openWorkOrdersQuery(site.id),
    enabled: principal.authorization.capabilities.includes('work-order.list'),
  });
  const openWorkOrdersByAsset = useMemo(() => {
    const counts = new Map<string, number>();
    for (const workOrder of workOrders.data ?? []) {
      for (const source of workOrder.sourceReferences) {
        if (source.domain === 'ASSET') counts.set(source.resourceId, (counts.get(source.resourceId) ?? 0) + 1);
      }
    }
    return counts;
  }, [workOrders.data]);

  const query = search.q?.trim().toLowerCase() ?? '';
  const groups = plant.groups
    .filter((group) => !search.category || group.category === search.category)
    .map((group) => ({
      ...group,
      devices: group.devices.filter((device) =>
        matchesStatus(device, search.status)
        && (!query || device.name.toLowerCase().includes(query) || deviceLocation(device).toLowerCase().includes(query))),
    }))
    .filter((group) => group.devices.length > 0);
  const inspected = plant.devices.find((device) => device.deviceId === search.inspect);

  const setSearch = (patch: { q?: string; category?: PlantCategory; status?: StatusFilter; inspect?: string }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }) });

  return (
    <Main className="space-y-5">
      <PageHeader
        title="系统与设备"
        actions={(
          <div className="flex items-center gap-4">
            <LiveIndicator mode={mode} />
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
          <AlertDescription>设备台账已加载，但当前观测值无法读取；运行状态与读数暂不显示。</AlertDescription>
        </Alert>
      ) : null}

      {loading ? <Skeleton className="h-96" /> : null}

      {!loading && registry.isSuccess && plant.devices.length === 0 ? (
        <Alert>
          <AlertTitle>该站点尚未登记设备</AlertTitle>
          <AlertDescription>在设备台账中登记设备与点位后，这里会列出它们的运行状态。</AlertDescription>
        </Alert>
      ) : null}

      {!loading && plant.devices.length > 0 ? (
        <>
          <MetricStrip
            items={[
              { label: '设备', value: plant.devices.length, unit: '台' },
              { label: '在线', value: plant.onlineCount, unit: '台' },
              { label: '运行', value: plant.devices.filter((device) => device.runState === 'RUNNING').length, unit: '台' },
              { label: '需关注', value: plant.devices.filter(needsAttention).length, unit: '台' },
            ]}
          />

          <div className="flex flex-wrap items-center gap-3">
            <InputGroup className="w-64">
              <InputGroupAddon><Search aria-hidden="true" /></InputGroupAddon>
              <InputGroupInput
                aria-label="搜索设备"
                placeholder="设备名称或位置"
                value={search.q ?? ''}
                onChange={(event) => setSearch({ q: event.target.value || undefined })}
              />
            </InputGroup>
            <Select
              value={search.category ?? 'ALL'}
              onValueChange={(value) => setSearch({ category: value === 'ALL' ? undefined : value as PlantCategory })}
            >
              <SelectTrigger className="w-36" aria-label="设备类型"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="ALL">全部类型</SelectItem>
                {plant.groups.map((group) => <SelectItem key={group.category} value={group.category}>{group.label}</SelectItem>)}
              </SelectContent>
            </Select>
            <Select
              value={search.status ?? 'ALL'}
              onValueChange={(value) => setSearch({ status: value === 'ALL' ? undefined : value as StatusFilter })}
            >
              <SelectTrigger className="w-32" aria-label="运行状态"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="ALL">全部状态</SelectItem>
                {(Object.keys(STATUS_FILTER_LABELS) as StatusFilter[]).map((status) => (
                  <SelectItem key={status} value={status}>{STATUS_FILTER_LABELS[status]}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <Card>
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-4">设备</TableHead>
                    <TableHead className="w-32">安装位置</TableHead>
                    <TableHead className="w-32">状态</TableHead>
                    <TableHead className="w-28 text-right">功率</TableHead>
                    <TableHead className="w-32 text-right">点位正常</TableHead>
                    <TableHead className="w-28 text-right">未完成工单</TableHead>
                    <TableHead className="w-28 text-right">数据时间</TableHead>
                    <TableHead className="w-8" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {groups.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={8} className="py-12 text-center text-sm text-muted-foreground">没有符合条件的设备</TableCell>
                    </TableRow>
                  ) : groups.flatMap((group) => {
                    const Icon = CATEGORY_ICONS[group.category];
                    return [
                      <TableRow key={group.category} className="bg-muted/30 hover:bg-muted/30">
                        <TableCell colSpan={8} className="py-1.5 pl-4">
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
                        const points = device.row.operational.points;
                        const healthy = points.filter((point) => point.freshness === 'FRESH' && point.quality === 'GOOD').length;
                        const assetId = boundAssetId(device);
                        const openWorkOrders = assetId ? openWorkOrdersByAsset.get(assetId) ?? 0 : 0;
                        return (
                          <TableRow
                            key={device.deviceId}
                            className="cursor-pointer"
                            data-state={search.inspect === device.deviceId ? 'selected' : undefined}
                            onClick={() => setSearch({ inspect: device.deviceId })}
                          >
                            <TableCell className="pl-4 font-medium">{device.name}</TableCell>
                            <TableCell>{deviceLocation(device)}</TableCell>
                            <TableCell><StatusBadge tone={status.tone} label={status.label} /></TableCell>
                            <TableCell className="text-right">
                              {powerKey ? <ReadingValue reading={device.reading(powerKey)} /> : <span className="text-muted-foreground">—</span>}
                            </TableCell>
                            <TableCell className={cn('text-right tabular-nums', healthy < points.length && 'text-amber-600 dark:text-amber-400')}>
                              {healthy} / {points.length}
                            </TableCell>
                            <TableCell className="text-right tabular-nums">
                              {openWorkOrders > 0 ? openWorkOrders : <span className="text-muted-foreground">—</span>}
                            </TableCell>
                            <TableCell className={cn('text-right tabular-nums', device.hasStaleData && 'text-amber-600 dark:text-amber-400')}>
                              {formatClock(device.latestSampleAt, site.timezone)}
                            </TableCell>
                            <TableCell><ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" /></TableCell>
                          </TableRow>
                        );
                      }),
                    ];
                  })}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      ) : null}

      <DeviceInspector
        device={inspected}
        categoryLabel={inspected ? PLANT_CATEGORY_LABELS[inspected.category] : ''}
        onClose={() => setSearch({ inspect: undefined })}
      />
    </Main>
  );
}
