import { useMemo } from 'react';
import { PageHeader } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { Main } from '@/components/layout/Main';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { ChevronRight, LayoutGrid, List, RefreshCw, Search } from 'lucide-react';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Button } from '@/components/ui/button';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
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
import { EQUIPMENT_CATEGORIES, equipmentStatus, PLANT_CATEGORY_LABELS, type PlantCategory, type PlantDevice } from '../../realtime/plant-model';
import { useRealtimePlant } from '../../realtime/use-realtime-plant';
import { boundAssetId, deviceLocation, needsAttention } from '../device-ledger';
import { DeviceInspector } from './DeviceInspector';
import { DeviceTile } from './DeviceTile';

type StatusFilter = 'running' | 'stopped' | 'attention';

const STATUS_FILTERS: readonly { readonly value: StatusFilter | 'all'; readonly label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'running', label: '运行' },
  { value: 'stopped', label: '停机' },
  { value: 'attention', label: '需关注' },
];

const pageRoute = getRouteApi('/_app/_site/operations/systems-devices');

function matchesStatus(device: PlantDevice, status: StatusFilter | 'all' | undefined): boolean {
  if (status === 'running') return device.runState === 'RUNNING';
  if (status === 'stopped') return device.runState === 'STOPPED';
  if (status === 'attention') return needsAttention(device);
  return true;
}

/** How a section is doing, said in its header: everything fine, or what is not. */
function sectionSummary(devices: readonly PlantDevice[], equipment: boolean): string {
  const stale = devices.filter((device) => device.hasStaleData).length;
  const offline = devices.filter((device) => device.connection === 'OFFLINE').length;
  const faults = devices.filter((device) => device.runState === 'FAULT').length;
  const running = devices.filter((device) => device.runState === 'RUNNING').length;
  const issues = [faults ? `${faults} 台故障` : '', offline ? `${offline} 台离线` : '', stale ? `${stale} 台读数过期` : ''].filter(Boolean);
  if (issues.length > 0) return issues.join(' · ');
  return equipment ? (running === devices.length ? '全部运行' : `${running} 台运行`) : '全部在线';
}

export function SystemsDevicesWorkspace() {
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/systems-devices' });
  const { site, plant, mode, registry, current, currentUnavailable, refresh } = useRealtimePlant();
  const { principal } = pageRoute.useRouteContext();
  const loading = registry.isPending || (current.isPending && current.fetchStatus !== 'idle');
  const layout = search.layout ?? 'cards';

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
  const openFor = (device: PlantDevice) => {
    const assetId = boundAssetId(device);
    return assetId ? openWorkOrdersByAsset.get(assetId) ?? 0 : 0;
  };

  const query = search.q?.trim().toLowerCase() ?? '';
  const visible = plant.devices.filter((device) =>
    (!search.category || device.category === search.category)
    && matchesStatus(device, search.status)
    && (!query || device.name.toLowerCase().includes(query) || deviceLocation(device).toLowerCase().includes(query)));
  const sections = [
    { key: 'equipment', title: '冷站主设备', equipment: true, devices: visible.filter((device) => EQUIPMENT_CATEGORIES.has(device.category)) },
    { key: 'environment', title: '计量与环境', equipment: false, devices: visible.filter((device) => !EQUIPMENT_CATEGORIES.has(device.category)) },
  ].filter((section) => section.devices.length > 0);
  const counts: Readonly<Record<StatusFilter | 'all', number>> = {
    all: plant.devices.length,
    running: plant.devices.filter((device) => matchesStatus(device, 'running')).length,
    stopped: plant.devices.filter((device) => matchesStatus(device, 'stopped')).length,
    attention: plant.devices.filter(needsAttention).length,
  };
  const inspected = plant.devices.find((device) => device.deviceId === search.inspect);
  const equipment = equipmentStatus(plant);

  const setSearch = (patch: { q?: string; category?: PlantCategory; status?: StatusFilter; inspect?: string; layout?: 'cards' | 'table' }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }) });

  return (
    <Main>
      <PageHeader
        title="设备"
        description={site.displayName}
        meta={<LiveIndicator mode={mode} />}
        actions={(
          <>
            {plant.latestSampleAt ? <span className="text-xs text-muted-foreground">数据时间 <span className="font-mono">{formatClock(plant.latestSampleAt, site.timezone)}</span></span> : null}
            <Button variant="outline" onClick={refresh}>
              <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
            </Button>
          </>
        )}
      />

      {registry.isError ? (
        <p className="rounded-lg border border-destructive-soft bg-destructive-soft/40 px-4 py-3 text-sm text-destructive-text">设备清单暂时读不到：{registry.error.message}</p>
      ) : null}
      {currentUnavailable ? (
        <p className="rounded-lg border border-warning-soft bg-warning-soft/50 px-4 py-3 text-sm text-warning-text">当前读数暂时读不到；设备清单已加载，运行状态和读数暂不显示。</p>
      ) : null}

      {loading ? <Skeleton className="h-96 rounded-xl" /> : null}

      {!loading && registry.isSuccess && plant.devices.length === 0 ? (
        <Panel className="items-center gap-2 px-6 py-12 text-center">
          <p className="text-sm font-semibold">该站点还没有登记设备</p>
          <p className="text-xs text-muted-foreground">在 系统管理 → 资产与点位 登记设备和点位后，这里会列出它们的运行状态</p>
        </Panel>
      ) : null}

      {!loading && plant.devices.length > 0 ? (
        <>
          <div className="flex items-end gap-3 border-b">
            <ToggleGroup
              type="single"
              value={search.status ?? 'all'}
              onValueChange={(value) => { if (value) setSearch({ status: value === 'all' ? undefined : value as StatusFilter }); }}
              aria-label="运行状态"
              className="h-9 gap-5"
            >
              {STATUS_FILTERS.map((option) => (
                <ToggleGroupItem
                  key={option.value}
                  value={option.value}
                  className="h-9 flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
                >
                  {option.label}
                  <span className="font-mono text-[11px] text-subtle-foreground">{counts[option.value]}</span>
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            <div className="mb-2 ml-auto flex items-center gap-2">
              <InputGroup className="h-8 w-56">
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
                <SelectTrigger className="h-8 w-32" aria-label="设备类型"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="ALL">全部类型</SelectItem>
                  {plant.groups.map((group) => <SelectItem key={group.category} value={group.category}>{group.label}</SelectItem>)}
                </SelectContent>
              </Select>
              <ToggleGroup
                type="single"
                variant="outline"
                value={layout}
                onValueChange={(value) => { if (value) setSearch({ layout: value === 'cards' ? undefined : 'table' }); }}
                aria-label="显示方式"
                spacing={0}
                className="h-8"
              >
                <ToggleGroupItem value="cards" className="h-8 gap-1.5 px-2.5 text-xs"><LayoutGrid className="size-3.5" />卡片</ToggleGroupItem>
                <ToggleGroupItem value="table" className="h-8 gap-1.5 px-2.5 text-xs"><List className="size-3.5" />表格</ToggleGroupItem>
              </ToggleGroup>
            </div>
          </div>

          {sections.length === 0 ? (
            <p className="py-12 text-center text-sm text-muted-foreground">没有符合条件的设备</p>
          ) : layout === 'cards' ? sections.map((section) => (
            <section key={section.key} aria-label={section.title} className="flex flex-col gap-3">
              <h2 className="flex items-baseline gap-2.5 text-sm font-semibold">
                {section.title}
                <span className="text-xs font-normal text-muted-foreground">
                  {section.devices.length} 台 · {sectionSummary(section.devices, section.equipment)}
                </span>
              </h2>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3.5">
                {section.devices.map((device) => (
                  <DeviceTile
                    key={device.deviceId}
                    device={device}
                    openWorkOrders={openFor(device)}
                    selected={search.inspect === device.deviceId}
                    onOpen={() => setSearch({ inspect: device.deviceId })}
                  />
                ))}
              </div>
            </section>
          )) : (
            <Panel>
              <Table aria-label="设备清单">
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-4">设备</TableHead>
                    <TableHead className="w-32">安装位置</TableHead>
                    <TableHead className="w-32">状态</TableHead>
                    <TableHead className="w-28 text-right">功率</TableHead>
                    <TableHead className="w-28 text-right">点位正常</TableHead>
                    <TableHead className="w-28 text-right">未完成工单</TableHead>
                    <TableHead className="w-28 text-right">数据时间</TableHead>
                    <TableHead className="w-8" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {sections.flatMap((section) => [
                    <TableRow key={section.key} className="bg-muted hover:bg-muted">
                      <TableCell colSpan={8} className="py-1.5 pl-4 text-xs font-medium text-secondary-foreground">
                        {section.title}<span className="ml-2 font-normal text-muted-foreground">{section.devices.length} 台</span>
                      </TableCell>
                    </TableRow>,
                    ...section.devices.map((device) => {
                      const status = deviceStatus(device);
                      const powerKey = POWER_KEY[device.category];
                      const points = device.row.operational.points;
                      const healthy = points.filter((point) => point.freshness === 'FRESH' && point.quality === 'GOOD').length;
                      const Icon = CATEGORY_ICONS[device.category];
                      return (
                        <TableRow
                          key={device.deviceId}
                          className="cursor-pointer"
                          data-state={search.inspect === device.deviceId ? 'selected' : undefined}
                          onClick={() => setSearch({ inspect: device.deviceId })}
                        >
                          <TableCell className="pl-4">
                            <span className="flex items-center gap-2 font-medium">
                              {Icon ? <Icon className="size-3.5 text-muted-foreground" aria-hidden="true" /> : null}
                              {device.name}
                              <span className="text-xs font-normal text-muted-foreground">{PLANT_CATEGORY_LABELS[device.category]}</span>
                            </span>
                          </TableCell>
                          <TableCell>{deviceLocation(device)}</TableCell>
                          <TableCell><StatusBadge tone={status.tone} label={status.label} /></TableCell>
                          <TableCell className="text-right">
                            {powerKey ? <ReadingValue reading={device.reading(powerKey)} /> : <span className="text-muted-foreground">—</span>}
                          </TableCell>
                          <TableCell className={cn('text-right tabular-nums', healthy < points.length && 'text-warning-text')}>
                            {healthy} / {points.length}
                          </TableCell>
                          <TableCell className="text-right tabular-nums">
                            {openFor(device) > 0 ? openFor(device) : <span className="text-muted-foreground">—</span>}
                          </TableCell>
                          <TableCell className={cn('text-right font-mono text-xs', device.hasStaleData && 'text-warning-text')}>
                            {formatClock(device.latestSampleAt, site.timezone)}
                          </TableCell>
                          <TableCell><ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" /></TableCell>
                        </TableRow>
                      );
                    }),
                  ])}
                </TableBody>
              </Table>
            </Panel>
          )}
          <p className="sr-only">主要设备 {equipment.running} / {equipment.total} 台运行，{plant.onlineCount} / {plant.devices.length} 台在线</p>
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
