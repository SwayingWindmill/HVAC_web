import { useMemo } from 'react';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import {
  AlertTriangle,
  ArrowRight,
  Building2,
  Clock3,
  Droplets,
  Globe2,
  RefreshCw,
  Search,
  Server,
  Thermometer,
  Wind,
} from 'lucide-react';
import type { CurrentPrincipalResponse, Site, Space, TelemetryPoint } from '@/api/generated/platformGateway.gen';
import type { HvacRouterContext } from '@/app/router-context';
import { Main } from '@/components/layout/Main';
import { SurfaceFactStrip } from '@/components/layout/SurfaceFactStrip';
import { SurfaceContextBar, SurfaceContextItem } from '@/components/layout/SurfaceContextBar';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DataTable,
  DataTableViewPills,
  StatusPillBadge,
  DataTableViewOptions,
  DataTablePagination,
  type DataTableFeatures,
  type DataTableViewPillOption,
} from '@/components/data-table';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import type { AssetsDeviceRow, AssetsPointView } from '@/features/assets/model';
import { useSiteAssetsData } from '@/features/assets/use-site-assets-data';
import { freshnessLabel, qualityLabel } from '@/features/assets-workspace/presentation';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';

export interface ComfortSearchState {
  readonly view?: 'attention' | 'thermal' | 'air-quality';
  readonly q?: string;
  readonly area?: string;
  readonly data?: 'all' | 'available' | 'issue';
  readonly inspect?: string;
}

interface ComfortWorkspaceProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  readonly searchState: ComfortSearchState;
  readonly onSearchChange: (patch: Partial<ComfortSearchState>) => void;
}

interface EnvironmentalPoint {
  readonly device: AssetsDeviceRow;
  readonly point: TelemetryPoint;
  readonly current: AssetsPointView | null;
}

interface ComfortSpaceRow {
  readonly space: Space;
  readonly path: string;
  readonly devices: readonly AssetsDeviceRow[];
  readonly temperature: readonly EnvironmentalPoint[];
  readonly humidity: readonly EnvironmentalPoint[];
  readonly dataState: 'available' | 'issue' | 'unavailable';
}

const COMFORT_SPACE_TYPES = new Set<Space['spaceType']>(['ZONE', 'ROOM', 'TENANT_SPACE']);

function currentPoint(device: AssetsDeviceRow, point: TelemetryPoint): AssetsPointView | null {
  return device.operational.points.find((candidate) => candidate.key === point.pointCode) ?? null;
}

function collectEnvironmentalPoints(devices: readonly AssetsDeviceRow[], pointCode: 'temperature' | 'humidity'): readonly EnvironmentalPoint[] {
  return devices.flatMap((device) => device.telemetryPoints
    .filter((point) => point.pointCode === pointCode)
    .map((point) => ({ device, point, current: currentPoint(device, point) })));
}

function hasPointIssue(point: EnvironmentalPoint): boolean {
  const current = point.current;
  return !current
    || current.state !== 'PRESENT'
    || current.freshness !== 'FRESH'
    || current.quality !== 'GOOD';
}

function buildSpacePath(space: Space, byId: ReadonlyMap<string, Space>): string {
  const names: string[] = [];
  let current: Space | undefined = space;
  const visited = new Set<string>();
  while (current && !visited.has(current.id)) {
    visited.add(current.id);
    names.unshift(current.displayName);
    current = current.parentSpaceId ? byId.get(current.parentSpaceId) : undefined;
  }
  return names.join(' / ');
}

function buildComfortRows(spaces: readonly Space[], devices: readonly AssetsDeviceRow[], currentUnavailable: boolean): readonly ComfortSpaceRow[] {
  const byId = new Map(spaces.map((space) => [space.id, space] as const));
  const devicesBySpace = new Map<string, AssetsDeviceRow[]>();
  for (const device of devices) {
    if (device.space.state !== 'bound') continue;
    const list = devicesBySpace.get(device.space.space.id) ?? [];
    list.push(device);
    devicesBySpace.set(device.space.space.id, list);
  }

  return spaces
    .filter((space) => COMFORT_SPACE_TYPES.has(space.spaceType))
    .map((space) => {
      const spaceDevices = devicesBySpace.get(space.id) ?? [];
      const temperature = collectEnvironmentalPoints(spaceDevices, 'temperature');
      const humidity = collectEnvironmentalPoints(spaceDevices, 'humidity');
      const environmentalPoints = [...temperature, ...humidity];
      const dataState: ComfortSpaceRow['dataState'] = currentUnavailable || environmentalPoints.length === 0
        ? 'unavailable'
        : environmentalPoints.some(hasPointIssue)
          ? 'issue'
          : 'available';
      return {
        space,
        path: buildSpacePath(space, byId),
        devices: spaceDevices,
        temperature,
        humidity,
        dataState,
      };
    })
    .sort((left, right) => left.path.localeCompare(right.path, 'zh-CN', { numeric: true }));
}

function measurementSummary(points: readonly EnvironmentalPoint[]): { readonly value: string; readonly detail: string; readonly tone: string } {
  if (points.length === 0) return { value: '—', detail: '未接入', tone: 'text-muted-foreground' };
  const present = points.filter((item) => item.current?.state === 'PRESENT');
  if (present.length === 0) return { value: '—', detail: '暂无当前值', tone: 'text-warning' };
  if (present.length > 1) {
    const hasIssue = present.some(hasPointIssue);
    return { value: `${present.length} 个测点`, detail: hasIssue ? '部分数据需核查' : '查看空间详情', tone: hasIssue ? 'text-warning' : 'text-foreground' };
  }
  const current = present[0]!.current!;
  return {
    value: `${current.displayValue}${current.unit ? ` ${current.unit}` : ''}`,
    detail: current.freshness === 'FRESH' && current.quality === 'GOOD' ? '数据可用' : `${freshnessLabel(current.freshness)} · ${qualityLabel(current.quality ?? '')}`,
    tone: current.freshness === 'FRESH' && current.quality === 'GOOD' ? 'text-foreground' : 'text-warning',
  };
}

function dataStatePresentation(state: ComfortSpaceRow['dataState']) {
  if (state === 'available') return { label: '数据可用', className: 'text-foreground' };
  if (state === 'issue') return { label: '数据需核查', className: 'border-warning/35 bg-warning/10 text-warning' };
  return { label: '环境数据未接入', className: 'text-muted-foreground' };
}

function spaceAreaLabel(row: ComfortSpaceRow): string {
  const segments = row.path.split(' / ');
  return segments.length > 1 ? segments.at(-2) ?? '—' : '—';
}

function latestEnvironmentTimestamp(row: ComfortSpaceRow, timezone: string): string {
  const timestamps = [...row.temperature, ...row.humidity]
    .flatMap(({ current }) => current?.sampledAt ? [Date.parse(current.sampledAt)] : [])
    .filter(Number.isFinite);
  if (timestamps.length === 0) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(Math.max(...timestamps)));
}

function EnvironmentalPointList({ title, icon: Icon, points }: { readonly title: string; readonly icon: typeof Thermometer; readonly points: readonly EnvironmentalPoint[] }) {
  return (
    <section>
      <h3 className="flex items-center gap-2 text-sm font-semibold"><Icon className="size-4 text-muted-foreground" aria-hidden="true" />{title}</h3>
      {points.length > 0 ? (
        <div className="mt-2 divide-y rounded-md border bg-background">
          {points.map(({ device, point, current }) => (
            <div key={point.id} className="grid gap-1 px-3 py-2.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-3">
              <div className="min-w-0">
                <strong className="block truncate text-xs font-medium">{point.displayName}</strong>
                <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{device.device.displayName}</span>
              </div>
              <div className="text-left sm:text-right">
                <strong className="block text-xs font-medium tabular-nums">{current?.state === 'PRESENT' ? `${current.displayValue}${current.unit ? ` ${current.unit}` : ''}` : '—'}</strong>
                <span className="mt-0.5 block text-[11px] text-muted-foreground">{current?.state === 'PRESENT' ? `${freshnessLabel(current.freshness)} · ${qualityLabel(current.quality ?? '')}` : '暂无当前值'}</span>
              </div>
            </div>
          ))}
        </div>
      ) : <p className="mt-2 text-sm text-muted-foreground">未登记该环境测点。</p>}
    </section>
  );
}

function SpaceInspector({ site, row, open, onOpenChange }: { readonly site: Readonly<Site>; readonly row?: ComfortSpaceRow; readonly open: boolean; readonly onOpenChange: (open: boolean) => void }) {
  if (!row) return null;
  const temperature = measurementSummary(row.temperature);
  const humidity = measurementSummary(row.humidity);
  const series = [...row.temperature, ...row.humidity].map((item) => item.point.id).slice(0, 4).join(',');

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{row.space.displayName}</SheetTitle>
          <SheetDescription>{row.path}</SheetDescription>
        </SheetHeader>
        <SheetBody className="space-y-5">
          <div className="grid grid-cols-2 overflow-hidden rounded-md border text-sm">
            <div className="p-3"><span className="text-xs text-muted-foreground">占用上下文</span><strong className="mt-1 block font-medium">未接入</strong></div>
            <div className="border-l p-3"><span className="text-xs text-muted-foreground">运营目标</span><strong className="mt-1 block font-medium">未接入</strong></div>
            <div className="border-t p-3"><span className="text-xs text-muted-foreground">温度</span><strong className={cn('mt-1 block font-medium tabular-nums', temperature.tone)}>{temperature.value}</strong></div>
            <div className="border-l border-t p-3"><span className="text-xs text-muted-foreground">湿度</span><strong className={cn('mt-1 block font-medium tabular-nums', humidity.tone)}>{humidity.value}</strong></div>
          </div>

          <Alert>
            <AlertTriangle aria-hidden="true" />
            <AlertTitle>当前无法判断目标偏离</AlertTitle>
            <AlertDescription>该站点尚未接入占用上下文和舒适 / IAQ 目标策略。当前测量只能作为环境事实，不能升级为舒适达标或标准合规结论。</AlertDescription>
          </Alert>

          <EnvironmentalPointList title="温度测点" icon={Thermometer} points={row.temperature} />
          <EnvironmentalPointList title="湿度测点" icon={Droplets} points={row.humidity} />

          <section>
            <h3 className="flex items-center gap-2 text-sm font-semibold"><Server className="size-4 text-muted-foreground" aria-hidden="true" />关联设备</h3>
            {row.devices.length > 0 ? (
              <div className="mt-2 divide-y rounded-md border bg-background">
                {row.devices.map((device) => (
                  <div key={device.device.id} className="flex items-center justify-between gap-3 px-3 py-2.5 text-xs">
                    <span className="min-w-0 truncate font-medium">{device.device.displayName}</span>
                    <span className="shrink-0 text-muted-foreground">{device.profile.state === 'configured' ? device.profile.profile.title : '类型未配置'}</span>
                  </div>
                ))}
              </div>
            ) : <p className="mt-2 text-sm text-muted-foreground">当前空间没有关联设备。</p>}
          </section>
        </SheetBody>
        <SheetFooter className="flex-wrap">
          {series ? (
            <Button variant="outline" size="sm" asChild>
              <Link to="/sites/$siteId/trends" params={{ siteId: site.id }} search={{ series }}>查看趋势 <ArrowRight /></Link>
            </Button>
          ) : <Button variant="outline" size="sm" disabled>查看趋势 <ArrowRight /></Button>}
          <Button variant="outline" size="sm" asChild>
            <Link to="/sites/$siteId/operations" params={{ siteId: site.id }}>系统运行 <ArrowRight /></Link>
          </Button>
          <Button variant="outline" size="sm" asChild>
            <Link to="/sites/$siteId/devices" params={{ siteId: site.id }} search={{ scope: `space:${row.space.id}` }}>相关设备 <ArrowRight /></Link>
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}

function Loading() {
  return (
    <Main fluid className="space-y-5">
      <div className="flex justify-end gap-2"><Skeleton className="h-9 w-28" /><Skeleton className="h-9 w-32" /><Skeleton className="h-9 w-24" /></div>
      <Skeleton className="h-20 w-full rounded-lg" />
      <Skeleton className="h-40 w-full rounded-lg" />
      <Skeleton className="h-[520px] w-full rounded-lg" />
    </Main>
  );
}

export function ComfortWorkspace({ site, principal, runtime, searchState, onSearchChange }: ComfortWorkspaceProps) {
  const data = useSiteAssetsData({ site, principal, runtime });
  const spaces = data.registry.data?.assetModel.spaces ?? [];
  const allRows = useMemo(() => buildComfortRows(spaces, data.rows, data.currentUnavailable), [data.currentUnavailable, data.rows, spaces]);
  const view = searchState.view ?? 'attention';
  const query = (searchState.q ?? '').trim().toLocaleLowerCase('zh-CN');
  const areaFilter = searchState.area ?? 'all';
  const dataFilter = searchState.data ?? 'all';
  const areaOptions = useMemo(() => Array.from(new Set(allRows.map(spaceAreaLabel).filter((label) => label !== '—'))).sort((left, right) => left.localeCompare(right, 'zh-CN', { numeric: true })), [allRows]);

  const filteredRows = useMemo(() => allRows.filter((row) => {
    if (query && !`${row.space.displayName} ${row.path}`.toLocaleLowerCase('zh-CN').includes(query)) return false;
    if (areaFilter !== 'all' && spaceAreaLabel(row) !== areaFilter) return false;
    if (dataFilter === 'available' && row.dataState !== 'available') return false;
    if (dataFilter === 'issue' && row.dataState === 'available') return false;
    return true;
  }).sort((left, right) => {
    if (view !== 'attention') return left.path.localeCompare(right.path, 'zh-CN', { numeric: true });
    const rank = { issue: 0, unavailable: 1, available: 2 } as const;
    return rank[left.dataState] - rank[right.dataState] || left.path.localeCompare(right.path, 'zh-CN', { numeric: true });
  }), [allRows, areaFilter, dataFilter, query, view]);

  const inspected = allRows.find((row) => row.space.id === searchState.inspect);
  const measuredCount = allRows.filter((row) => row.temperature.some((point) => point.current?.state === 'PRESENT') || row.humidity.some((point) => point.current?.state === 'PRESENT')).length;
  const issueCount = allRows.filter((row) => row.dataState !== 'available').length;
  const currentStateLabel = data.currentPending
    ? '正在读取当前环境数据'
    : data.currentUnavailable
      ? '当前环境数据不可用'
      : '当前环境数据已更新';

  const viewPillOptions = useMemo<readonly DataTableViewPillOption[]>(() => [
    { id: 'attention', label: '待核查', count: issueCount },
    { id: 'thermal', label: '热环境', count: measuredCount },
    { id: 'air-quality', label: '空气质量 (未接入)', count: 0, disabled: true },
  ], [issueCount, measuredCount]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, ComfortSpaceRow>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={
            table.getIsAllPageRowsSelected()
              ? true
              : table.getIsSomePageRowsSelected()
                ? 'indeterminate'
                : false
          }
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选"
        />
      ),
      cell: ({ row }) => (
        <div onClick={(event) => event.stopPropagation()}>
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
            aria-label={`选择 ${row.original.space.displayName}`}
          />
        </div>
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'space',
      header: '空间',
      cell: ({ row }) => (
        <Button variant="link" className="h-auto max-w-full justify-start p-0 text-left text-xs font-medium" onClick={(event) => { event.stopPropagation(); onSearchChange({ inspect: row.original.space.id }); }}>
          <span className="truncate">{row.original.space.displayName}</span>
        </Button>
      ),
    },
    { id: 'area', header: '所属区域', cell: ({ row }) => <span className="text-xs text-muted-foreground">{spaceAreaLabel(row.original)}</span> },
    {
      id: 'temperature',
      header: '温度',
      cell: ({ row }) => {
        const measurement = measurementSummary(row.original.temperature);
        return <div className="text-xs"><strong className={cn('block font-mono font-medium tabular-nums', measurement.tone)}>{measurement.value}</strong><span className="mt-0.5 block text-[11px] text-muted-foreground">{measurement.detail}</span></div>;
      },
    },
    {
      id: 'humidity',
      header: '湿度',
      cell: ({ row }) => {
        const measurement = measurementSummary(row.original.humidity);
        return <div className="text-xs"><strong className={cn('block font-mono font-medium tabular-nums', measurement.tone)}>{measurement.value}</strong><span className="mt-0.5 block text-[11px] text-muted-foreground">{measurement.detail}</span></div>;
      },
    },
    { id: 'occupancy', header: '占用上下文', cell: () => <span className="text-xs text-muted-foreground">未接入</span> },
    { id: 'target', header: '运营目标', cell: () => <span className="text-xs text-muted-foreground">未接入</span> },
    { id: 'iaq', header: 'IAQ', cell: () => <span className="text-xs text-muted-foreground">未接入</span> },
    { id: 'devices', header: '关联设备', cell: ({ row }) => <span className="text-xs font-mono tabular-nums">{row.original.devices.length ? `${row.original.devices.length} 台` : '—'}</span> },
    {
      id: 'data',
      header: '数据状态',
      cell: ({ row }) => {
        const state = dataStatePresentation(row.original.dataState);
        const tone = row.original.dataState === 'available' ? 'success' : row.original.dataState === 'issue' ? 'warning' : 'neutral';
        return <StatusPillBadge label={state.label} tone={tone} />;
      },
    },
    { id: 'updated', header: '最近更新', cell: ({ row }) => <span className="whitespace-nowrap text-[11px] font-mono text-muted-foreground tabular-nums">{latestEnvironmentTimestamp(row.original, site.timezone)}</span> },
  ], [onSearchChange, site.timezone]);

  const table = useDataTable({
    key: 'comfort-zone-ledger',
    data: filteredRows,
    columns,
    pageSize: 10,
    getRowId: (comfortRow) => comfortRow.space.id,
  });

  if (data.registry.isPending) return <Loading />;

  if (data.registry.isError) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] w-full rounded-lg border bg-card shadow-xs">
          <EmptyHeader>
            <EmptyMedia variant="icon"><AlertTriangle className="text-destructive" aria-hidden="true" /></EmptyMedia>
            <EmptyTitle>空间环境数据暂时无法加载</EmptyTitle>
            <EmptyDescription>站点空间与设备清单暂不可用，请稍后重试。</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </Main>
    );
  }

  return (
    <Main fluid className="space-y-5" data-testid="comfort-workspace" data-site-id={site.id}>
      <SurfaceContextBar aria-label="舒适与室内环境上下文">
        <SurfaceContextItem emphasis><Building2 aria-hidden="true" />{site.displayName}</SurfaceContextItem>
        <SurfaceContextItem><Globe2 aria-hidden="true" />{site.timezone}</SurfaceContextItem>
        <SurfaceContextItem><Clock3 aria-hidden="true" />{currentStateLabel}</SurfaceContextItem>
        <Button variant="outline" size="sm" onClick={data.refresh}><RefreshCw className={cn((data.registry.isFetching || data.current.isFetching) && 'animate-spin')} />刷新</Button>
      </SurfaceContextBar>

      <Alert>
        <AlertTriangle aria-hidden="true" />
        <AlertTitle>当前只能评估环境数据，不能判断舒适或 IAQ 达标</AlertTitle>
        <AlertDescription>该站点尚未接入占用上下文和舒适 / IAQ 目标策略。温度、湿度等实测不会被自动解释为 ASHRAE 55 / 62.1 合规或健康结论。</AlertDescription>
      </Alert>

      <SurfaceFactStrip
        ariaLabel="空间环境概况"
        facts={[
          { label: '个空间', value: allRows.length.toLocaleString('zh-CN'), detail: '区域 / 房间 / 租户空间', icon: Building2 },
          { label: '有环境实测', value: measuredCount.toLocaleString('zh-CN'), detail: '存在温度或湿度当前值', icon: Thermometer },
          { label: '数据需核查', value: issueCount.toLocaleString('zh-CN'), detail: '缺测、延迟或质量问题', icon: AlertTriangle, tone: issueCount > 0 ? 'warning' : 'neutral' },
          { label: '占用上下文', value: '未接入', detail: '当前无法按占用判断', tone: 'warning' },
          { label: '运营目标', value: '未接入', detail: '当前无法判断目标偏离', tone: 'warning' },
          { label: 'IAQ 维度', value: '未接入', detail: '空气质量分析不可用', icon: Wind, tone: 'warning' },
        ]}
      />

      <Card className="min-w-0 overflow-hidden shadow-xs" aria-label="空间环境台账">
        <CardHeader className="border-b">
          <CardTitle>空间环境台账</CardTitle>
          <CardDescription>{filteredRows.length} 个空间符合当前筛选条件</CardDescription>
        </CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b p-3">
          <div className="flex flex-wrap items-center gap-3">
            <DataTableViewPills
              options={viewPillOptions}
              value={view}
              onValueChange={(val) => onSearchChange({ view: val as ComfortSearchState['view'], inspect: undefined })}
            />
            <InputGroup className="min-w-64 flex-1 lg:max-w-sm">
              <InputGroupInput value={searchState.q ?? ''} onChange={(event) => onSearchChange({ q: event.currentTarget.value || undefined, inspect: undefined })} placeholder="搜索空间名称" aria-label="搜索空间" />
              <InputGroupAddon align="inline-start"><Search /></InputGroupAddon>
            </InputGroup>
            <Select value={areaFilter} onValueChange={(value) => onSearchChange({ area: value === 'all' ? undefined : value, inspect: undefined })}>
              <SelectTrigger className="w-[150px]" aria-label="筛选所属区域"><SelectValue placeholder="全部区域" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部区域</SelectItem>
                {areaOptions.map((area) => <SelectItem key={area} value={area}>{area}</SelectItem>)}
              </SelectContent>
            </Select>
            <Select value={dataFilter} onValueChange={(value) => onSearchChange({ data: value as ComfortSearchState['data'], inspect: undefined })}>
              <SelectTrigger className="w-[150px]" aria-label="筛选数据状态"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部数据状态</SelectItem>
                <SelectItem value="available">数据可用</SelectItem>
                <SelectItem value="issue">需要核查</SelectItem>
              </SelectContent>
            </Select>
            <Button variant="outline" size="sm" onClick={() => onSearchChange({ q: undefined, area: undefined, data: 'all', inspect: undefined })}>重置</Button>
          </div>
          <DataTableViewOptions table={table} />
        </div>

        {view === 'air-quality' ? (
          <Empty className="min-h-80 border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon"><Wind aria-hidden="true" /></EmptyMedia>
              <EmptyTitle>空气质量测量维度尚未接入</EmptyTitle>
              <EmptyDescription>当前没有正式 CO₂ / PM2.5 / TVOC 等 IAQ 数据合同，因此不会用温度或单一传感器替代空气质量结论。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : filteredRows.length === 0 ? (
          <Empty className="min-h-80 border-0">
            <EmptyHeader>
              <EmptyMedia variant="icon"><Search aria-hidden="true" /></EmptyMedia>
              <EmptyTitle>没有符合条件的空间</EmptyTitle>
              <EmptyDescription>调整搜索或数据状态筛选后再试。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <DataTable
            table={table}
            role="region"
            aria-label="空间环境台账，可横向滚动"
            tabIndex={0}
            className="gap-0"
            tableAriaLabel="空间环境台账"
            getHeaderCellProps={() => ({
              className: 'h-10 whitespace-nowrap text-[11px] font-medium text-muted-foreground',
            })}
            getRowProps={(row) => ({
              className: cn('cursor-pointer', row.original.space.id === inspected?.space.id && 'bg-muted/50'),
              'data-state': row.original.space.id === inspected?.space.id ? 'selected' : undefined,
              onClick: () => onSearchChange({ inspect: row.original.space.id }),
            })}
            getCellProps={() => ({ className: 'py-2.5' })}
            footer={(
              <DataTablePagination
                table={table}
                totalRows={filteredRows.length}
                
                
                
                
                
              />
            )}
          />
        )}
      </Card>

      <SpaceInspector site={site} row={inspected} open={Boolean(inspected)} onOpenChange={(open) => { if (!open) onSearchChange({ inspect: undefined }); }} />
    </Main>
  );
}
