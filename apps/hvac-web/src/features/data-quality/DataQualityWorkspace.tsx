import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  AlertTriangle,
  ArrowRight,
  Clock,
  Layers,
  Network,
  RefreshCw,
  Search,
  SlidersHorizontal,
  Wifi,
  ChevronRight,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DataTable,
  DataTableAdvancedToolbar,
  DataTableFilterList,
  DataTablePagination,
  DataTableSortList,
  type DataTableFeatures,
} from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';
import type { ExtendedColumnFilter, JoinOperator } from '@/lib/data-table-types';
import { getFiltersStateParser } from '@/lib/parsers';

export interface DataQualityWorkspaceProps {
  readonly siteId: string;
}

type PointQualityStatus = 'GOOD' | 'SUSPECT' | 'STALE' | 'OUT_OF_RANGE' | 'OFFLINE';

interface TelemetryPoint {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly deviceCode: string;
  readonly deviceName: string;
  readonly subsystem: string;
  readonly protocol: string;
  readonly sampleRateSec: number;
  readonly currentValue: number | string;
  readonly unit: string;
  readonly lastHeartbeat: string;
  readonly status: PointQualityStatus;
  readonly confidence: number;
  readonly jitterMs: number;
  readonly impactDescription: string;
  readonly affectedServices: readonly string[];
}

const TELEMETRY_POINTS: readonly TelemetryPoint[] = [
  {
    id: 'pt-001',
    code: 'CH-01.EvapFlowRate',
    name: '1#离心冷机 蒸发侧流量计',
    deviceCode: 'CH-01',
    deviceName: '1#离心冷水机组',
    subsystem: '冷冻水系统',
    protocol: 'Modbus TCP / 网关-01',
    sampleRateSec: 10,
    currentValue: '0.00 (波动异常)',
    unit: 'm³/h',
    lastHeartbeat: '2秒前 (14:32:10)',
    status: 'SUSPECT',
    confidence: 32,
    jitterMs: 145,
    impactDescription: '流量瞬时突变降为0，导致机组即时产冷量与 COP 计算严重失真。',
    affectedServices: ['冷机 COP 计算', '诊断规则 FDD-04', '节能机会 ECO-01'],
  },
  {
    id: 'pt-002',
    code: 'CH-02.CondInTemp',
    name: '2#离心冷机 冷凝进水温度',
    deviceCode: 'CH-02',
    deviceName: '2#离心冷水机组',
    subsystem: '冷却水系统',
    protocol: 'BACnet IP / 网关-02',
    sampleRateSec: 15,
    currentValue: 39.4,
    unit: '°C',
    lastHeartbeat: '5秒前 (14:32:07)',
    status: 'OUT_OF_RANGE',
    confidence: 45,
    jitterMs: 82,
    impactDescription: '进水温超出历史正常物理上界 37.0°C，判定传感器漂移或水路旁通。',
    affectedServices: ['冷却塔逼近度计算', '冷却水泵变频闭环'],
  },
  {
    id: 'pt-003',
    code: 'CT-03.FanVFDFeedback',
    name: '3#冷却塔 风机变频器频率回读',
    deviceCode: 'CT-03',
    deviceName: '3#超低噪冷却塔',
    subsystem: '冷却水系统',
    protocol: 'Modbus RTU / 串口服务器-01',
    sampleRateSec: 10,
    currentValue: '35.00 (恒定冻结)',
    unit: 'Hz',
    lastHeartbeat: '48分钟前 (13:44:12)',
    status: 'STALE',
    confidence: 15,
    jitterMs: 420,
    impactDescription: '点位数值已持续 48 分钟无任何抖动，串口通讯处于重试状态。',
    affectedServices: ['冷却塔群控联动', '控制事实总账回读校验'],
  },
  {
    id: 'pt-004',
    code: 'PMP-CHW-04.PowerkW',
    name: '4#冷冻一次泵 电能表有功功率',
    deviceCode: 'PMP-CHW-04',
    deviceName: '4#冷冻水循环泵',
    subsystem: '水泵输配系统',
    protocol: 'DL/T 645 / 网关-03',
    sampleRateSec: 30,
    currentValue: '通讯中断',
    unit: 'kW',
    lastHeartbeat: '2小时前 (12:30:00)',
    status: 'OFFLINE',
    confidence: 0,
    jitterMs: 0,
    impactDescription: '网关持续收到 CRC 校验错误帧，输配系统输送能效比计入缺省。',
    affectedServices: ['输配比 WT/RT 计算', '分项用电对账'],
  },
  {
    id: 'pt-005',
    code: 'CH-01.EvapLeavingTemp',
    name: '1#冷机 蒸发器出水温度',
    deviceCode: 'CH-01',
    deviceName: '1#离心冷水机组',
    subsystem: '冷冻水系统',
    protocol: 'Modbus TCP / 网关-01',
    sampleRateSec: 5,
    currentValue: 7.2,
    unit: '°C',
    lastHeartbeat: '1秒前 (14:32:11)',
    status: 'GOOD',
    confidence: 99,
    jitterMs: 24,
    impactDescription: '数据连续正常，方差处于历史基线内。',
    affectedServices: ['末端负荷自适应控制', '系统供水温监测'],
  },
  {
    id: 'pt-006',
    code: 'CH-01.EvapEnteringTemp',
    name: '1#冷机 蒸发器回水温度',
    deviceCode: 'CH-01',
    deviceName: '1#离心冷水机组',
    subsystem: '冷冻水系统',
    protocol: 'Modbus TCP / 网关-01',
    sampleRateSec: 5,
    currentValue: 12.1,
    unit: '°C',
    lastHeartbeat: '1秒前 (14:32:11)',
    status: 'GOOD',
    confidence: 99,
    jitterMs: 22,
    impactDescription: '数据连续正常，温差处于 4.9°C 设计温差范围内。',
    affectedServices: ['冷冻水大温差监测', '负荷估算'],
  },
  {
    id: 'pt-007',
    code: 'MTR-MAIN-01.TotalActivePower',
    name: '高压总进线柜 有功总功率',
    deviceCode: 'MTR-MAIN-01',
    deviceName: '10kV 变配电总电表',
    subsystem: '电能计量系统',
    protocol: 'IEC 61850 / 保护测控',
    sampleRateSec: 1,
    currentValue: 1284.6,
    unit: 'kW',
    lastHeartbeat: '即时 (14:32:12)',
    status: 'GOOD',
    confidence: 100,
    jitterMs: 8,
    impactDescription: '微秒级同步时钟对齐，基准负荷监测源。',
    affectedServices: ['站点总用能', '需量响应监测', '碳排放 Scope 2'],
  },
];

const DATA_QUALITY_SUBSYSTEM_OPTIONS = [...new Set(TELEMETRY_POINTS.map((point) => point.subsystem))]
  .map((value) => ({ label: value, value }));
const DATA_QUALITY_STATUS_OPTIONS = [
  { label: '正常有效', value: 'GOOD' },
  { label: '数据存疑', value: 'SUSPECT' },
  { label: '越限超标', value: 'OUT_OF_RANGE' },
  { label: '数值冻结', value: 'STALE' },
  { label: '设备离线', value: 'OFFLINE' },
] as const;
const DATA_QUALITY_FILTER_COLUMN_IDS = ['subsystem', 'status'] as const;
const DATA_QUALITY_FILTERS_QUERY_KEY = 'dataQualityFilters';
const DATA_QUALITY_JOIN_OPERATOR_QUERY_KEY = 'dataQualityJoinOperator';

function matchesDataQualityAdvancedFilter(
  point: TelemetryPoint,
  filter: ExtendedColumnFilter<TelemetryPoint>,
) {
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];
  const actual = filter.id === 'subsystem' ? point.subsystem : filter.id === 'status' ? point.status : undefined;
  if (actual === undefined) return true;
  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return values.includes(actual);
    case 'ne':
    case 'notInArray':
      return !values.includes(actual);
    case 'isEmpty':
      return actual.length === 0;
    case 'isNotEmpty':
      return actual.length > 0;
    default:
      return false;
  }
}

export function DataQualityWorkspace({ siteId: _siteId }: DataQualityWorkspaceProps) {
  const [selectedPoint, setSelectedPoint] = useState<TelemetryPoint | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [advancedFilters] = useQueryState(
    DATA_QUALITY_FILTERS_QUERY_KEY,
    getFiltersStateParser<TelemetryPoint>([...DATA_QUALITY_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    DATA_QUALITY_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const filteredPoints = useMemo(() => {
    return TELEMETRY_POINTS.filter((point) => {
      const q = searchQuery.trim().toLowerCase();
      const matchesSearch = !q || (
        point.name.toLowerCase().includes(q) ||
        point.code.toLowerCase().includes(q) ||
        point.deviceName.toLowerCase().includes(q) ||
        point.subsystem.toLowerCase().includes(q)
      );
      const filterMatches = advancedFilters.map((filter) => matchesDataQualityAdvancedFilter(point, filter));
      const matchesAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchesSearch && matchesAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, TelemetryPoint>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选遥测点位"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.name}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'code',
      accessorFn: (row) => row.code,
      meta: { label: '点位标识' },
      header: '点位标识 / 代码',
      cell: ({ row }) => <span className="font-mono font-semibold text-foreground">{row.original.code}</span>,
    },
    {
      id: 'name',
      accessorFn: (row) => row.name,
      meta: { label: '点位名称' },
      header: '点位名称与所属设备',
      cell: ({ row }) => (
        <div>
          <div className="font-medium text-foreground">{row.original.name}</div>
          <div className="text-[11px] text-muted-foreground">
            {row.original.deviceName} <span className="font-mono text-[10px]">({row.original.deviceCode})</span>
          </div>
        </div>
      ),
    },
    {
      id: 'subsystem',
      accessorFn: (row) => row.subsystem,
      enableColumnFilter: true,
      meta: { label: '子系统', variant: 'select', options: DATA_QUALITY_SUBSYSTEM_OPTIONS },
      header: '子系统 / 通信协议',
      cell: ({ row }) => (
        <div>
          <div className="font-medium text-foreground">{row.original.subsystem}</div>
          <div className="text-[11px] text-muted-foreground">{row.original.protocol}</div>
        </div>
      ),
    },
    {
      id: 'value',
      header: '当前实时读数',
      cell: ({ row }) => (
        <span className="font-mono font-medium text-foreground tabular-nums">
          {row.original.currentValue}{' '}
          <span className="text-[11px] font-normal text-muted-foreground">{row.original.unit}</span>
        </span>
      ),
    },
    {
      id: 'sample',
      accessorFn: (row) => row.sampleRateSec,
      meta: { label: '采样周期' },
      header: '采样周期 / 抖动',
      cell: ({ row }) => (
        <div className="text-[11px]">
          <div><span className="font-mono font-medium tabular-nums">{row.original.sampleRateSec}</span> 秒/次</div>
          <div className="font-mono text-muted-foreground tabular-nums">±{row.original.jitterMs} ms 抖动</div>
        </div>
      ),
    },
    {
      id: 'status',
      accessorFn: (row) => row.status,
      enableColumnFilter: true,
      meta: { label: '质量状态', variant: 'multiSelect', options: [...DATA_QUALITY_STATUS_OPTIONS] },
      header: '质量状态',
      cell: ({ row }) => (
        <StatusBadge
          tone={
            row.original.status === 'GOOD'
              ? 'success'
              : row.original.status === 'SUSPECT'
                ? 'warning'
                : row.original.status === 'OUT_OF_RANGE'
                  ? 'destructive'
                  : row.original.status === 'STALE'
                    ? 'warning'
                    : 'neutral'
          }
          pulse={row.original.status === 'SUSPECT' || row.original.status === 'OUT_OF_RANGE'}
          label={
            row.original.status === 'GOOD'
              ? '正常有效'
              : row.original.status === 'SUSPECT'
                ? '数据存疑'
                : row.original.status === 'OUT_OF_RANGE'
                  ? '越限超标'
                  : row.original.status === 'STALE'
                    ? '数值冻结'
                    : '设备离线'
          }
        />
      ),
    },
    {
      id: 'confidence',
      accessorFn: (row) => row.confidence,
      meta: { label: '置信度' },
      header: '置信度',
      cell: ({ row }) => (
        <div className="flex items-center justify-center gap-1.5">
          <span className="font-mono text-xs font-semibold tabular-nums">{row.original.confidence}%</span>
          <div className="h-1.5 w-8 overflow-hidden rounded-full bg-muted">
            <div
              className={`h-full ${row.original.confidence > 80 ? 'bg-emerald-500' : row.original.confidence > 40 ? 'bg-amber-500' : 'bg-destructive'}`}
              style={{ width: `${row.original.confidence}%` }}
            />
          </div>
        </div>
      ),
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          className="h-7 gap-0.5 px-2 text-xs"
          onClick={(event) => {
            event.stopPropagation();
            setSelectedPoint(row.original);
          }}
        >
          溯源
          <ChevronRight className="h-3 w-3" />
        </Button>
      ),
      enableSorting: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-31-data-quality',
    data: [...filteredPoints],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
    meta: {
      queryKeys: {
        page: 'dataQualityPage',
        perPage: 'dataQualityPerPage',
        sort: 'dataQualitySort',
        filters: DATA_QUALITY_FILTERS_QUERY_KEY,
        joinOperator: DATA_QUALITY_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* 1. Surface Page Header */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2">
            
            <span className="rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground">
              <span className="tabular-nums font-medium">500</span> 个监控测点
            </span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="gap-1.5" onClick={() => window.location.reload()}>
            <RefreshCw className="h-3.5 w-3.5" />
            刷新质量诊断
          </Button>
        </div>
      </div>

      {/* 2. Telemetry Observability Posture Strip (4 Fact Cards) */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">测点在线率</CardTitle>
            <Wifi className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight tabular-nums text-foreground">98.4%</span>
              <span className="text-xs text-muted-foreground tabular-nums">492/500 在线</span>
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
              <span>离线点位</span>
              <span className="text-foreground font-medium">1 点 (PMP-CHW-04 电表)</span>
            </div>
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-muted-foreground">采集驱动状态</span>
              <span className="text-foreground font-medium">Modbus/BACnet 正常</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">P95 传输时延</CardTitle>
            <Clock className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight tabular-nums text-foreground">1.4s</span>
              <span className="text-xs text-muted-foreground tabular-nums">均值 320ms</span>
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
              <span>边缘网关对齐</span>
              <span className="text-foreground font-medium">NTP 微秒级对齐</span>
            </div>
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-muted-foreground">网络采样抖动</span>
              <span className="tabular-nums text-foreground font-medium">P99 &lt; 420ms</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">异常与存疑点位</CardTitle>
            <AlertTriangle className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight tabular-nums text-foreground">4</span>
              <span className="text-xs text-muted-foreground">项异常点位拦截</span>
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
              <span>异常类型分布</span>
              <span className="text-[11px] text-foreground font-medium">1越限 / 1冻结 / 1突变</span>
            </div>
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-muted-foreground">处理建议</span>
              <span className="text-foreground font-medium">需派发就地校准</span>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">受影响下游计算链</CardTitle>
            <Layers className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-1.5">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight tabular-nums text-foreground">3</span>
              <span className="text-xs text-muted-foreground">条降级熔断保护</span>
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground pt-1">
              <span>受损派生指标</span>
              <span className="text-[11px] text-foreground font-medium truncate max-w-[150px]">COP / 逼近度 / 变频</span>
            </div>
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-muted-foreground">置信度防御</span>
              <span className="text-foreground font-medium">已阻断错误控制下发</span>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 3. Downstream Impact Lineage Section (下游指标计算受损推演) */}
      <Card className="shadow-xs">
        <CardHeader className="p-4 border-b">
          <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
            <div className="flex items-center gap-2">
              <Network className="size-4 text-muted-foreground" />
              <div>
                <CardTitle className="text-base font-semibold">
                  测点异常对下游计算与控制的影响
                </CardTitle>
                <CardDescription className="text-xs">
                  底层传感器读数异常时，系统将自动暂停相关能效计算与自动优化建议
                </CardDescription>
              </div>
            </div>
            <Badge variant="outline" className="font-normal gap-1.5 text-xs">
              <span className="size-1.5 rounded-full bg-emerald-500" />
              异常测点保护已生效
            </Badge>
          </div>
        </CardHeader>

        <CardContent className="p-0">
          <div className="grid grid-cols-1 md:grid-cols-3 divide-y md:divide-y-0 md:divide-x border-border/60">
            {/* Card 1 */}
            <div className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-mono text-xs font-semibold text-foreground">CH-01.EvapFlowRate</span>
                <Badge variant="outline" className="font-normal gap-1 text-[10px]">
                  <span className="size-1 rounded-full bg-rose-500" />
                  流量计突跳
                </Badge>
              </div>
              <div className="font-medium text-foreground text-xs">
                1#冷机蒸发器流量计瞬时归零 (0 m³/h)
              </div>
              <p className="text-muted-foreground text-[11px] leading-relaxed">
                瞬时流量突跳至 0，持续触发数据质量拦截规则 DQ-FLOW-01。
              </p>
              <div className="pt-2 border-t text-[11px] flex items-center gap-1.5 text-muted-foreground font-medium">
                <span>阻断链：</span>
                <ArrowRight className="size-3" />
                <span>COP 失真</span>
                <ArrowRight className="size-3" />
                <span>ECO-01 建议挂起</span>
              </div>
            </div>

            {/* Card 2 */}
            <div className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-mono text-xs font-semibold text-foreground">CH-02.CondInTemp</span>
                <Badge variant="outline" className="font-normal gap-1 text-[10px]">
                  <span className="size-1 rounded-full bg-amber-500" />
                  39.4°C 越限
                </Badge>
              </div>
              <div className="font-medium text-foreground text-xs">
                2#冷机冷凝进水温超出历史正常物理上界
              </div>
              <p className="text-muted-foreground text-[11px] leading-relaxed">
                超出设计上限 37.0°C，冷却水回水传感器出现 3.2°C 正向物理漂移。
              </p>
              <div className="pt-2 border-t text-[11px] flex items-center gap-1.5 text-muted-foreground font-medium">
                <span>阻断链：</span>
                <ArrowRight className="size-3" />
                <span>逼近度偏差 +3.2°C</span>
                <ArrowRight className="size-3" />
                <span>水泵变频调优降级</span>
              </div>
            </div>

            {/* Card 3 */}
            <div className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-mono text-xs font-semibold text-foreground">CT-03.FanVFD</span>
                <Badge variant="outline" className="font-normal gap-1 text-[10px]">
                  <span className="size-1 rounded-full bg-blue-500" />
                  35.0Hz 冻结
                </Badge>
              </div>
              <div className="font-medium text-foreground text-xs">
                3#冷却塔风机频率读数 48 分钟无抖动
              </div>
              <p className="text-muted-foreground text-[11px] leading-relaxed">
                串口网关通讯发生丢包重试，闭环控制无法获取即时实际物理频率。
              </p>
              <div className="pt-2 border-t text-[11px] flex items-center gap-1.5 text-muted-foreground font-medium">
                <span>阻断链：</span>
                <ArrowRight className="size-3" />
                <span>回读校验失败</span>
                <ArrowRight className="size-3" />
                <span>策略回退基准工频</span>
              </div>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 4. Telemetry Points Ledger */}
      <DataTableBlock
        title="测点质量"
      >

        <DataTable
          table={table}
          tableAriaLabel="测点质量"
          empty="没有符合当前筛选条件的遥测点位记录"
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-10' :
              header.id === 'code' ? 'w-[180px]' :
              header.id === 'subsystem' ? 'w-[160px]' :
              header.id === 'value' ? 'w-[140px] text-right' :
              header.id === 'sample' ? 'w-[130px]' :
              header.id === 'status' ? 'w-[120px] text-center' :
              header.id === 'confidence' ? 'w-[100px] text-center' :
              header.id === 'actions' ? 'w-[80px] text-right' :
              undefined,
          })}
          getRowProps={(row) => ({
            className: 'cursor-pointer',
            onClick: () => setSelectedPoint(row.original),
          })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'value' || cell.column.id === 'actions'
                ? 'text-right'
                : cell.column.id === 'status' || cell.column.id === 'confidence'
                  ? 'text-center'
                  : undefined,
          })}
          footer={(
            <DataTablePagination
              table={table}
              totalRows={filteredPoints.length}
            />
          )}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative w-48 sm:w-60">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索点位 / 编号 / 设备..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
                className="h-8 bg-background pl-8 text-xs"
              />
            </div>
            <DataTableSortList table={table} />
            <DataTableFilterList table={table} />
          </DataTableAdvancedToolbar>
        </DataTable>
      </DataTableBlock>

      {/* 5. Point Detail & Trace Sheet */}
      <Sheet open={Boolean(selectedPoint)} onOpenChange={(open) => !open && setSelectedPoint(null)}>
        <SheetContent className="sm:max-w-xl!">
          <SheetHeader>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-semibold text-muted-foreground">
                {selectedPoint?.code}
              </span>
              <span className="rounded bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                {selectedPoint?.subsystem}
              </span>
            </div>
            <SheetTitle className="text-base font-bold text-foreground mt-1">
              {selectedPoint?.name}
            </SheetTitle>
            <SheetDescription className="text-xs">
              所属设备: {selectedPoint?.deviceName} ({selectedPoint?.deviceCode}) · 通信驱动: {selectedPoint?.protocol}
            </SheetDescription>
          </SheetHeader>

          {selectedPoint && (
            <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 space-y-5 pt-4 text-xs">
              {/* Telemetry Facts Grid */}
              <div className="grid grid-cols-2 gap-3 rounded-lg border border-border/80 bg-muted/20 p-3.5">
                <div>
                  <div className="text-muted-foreground text-[11px]">最新实时读数</div>
                  <div className="mt-1 tabular-nums text-lg font-bold text-foreground">
                    {selectedPoint.currentValue} {selectedPoint.unit}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">最后更新时间</div>
                  <div className="mt-1 text-xs font-medium text-foreground">
                    {selectedPoint.lastHeartbeat}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">采样周期与时延抖动</div>
                  <div className="mt-1 text-xs tabular-nums text-foreground">
                    {selectedPoint.sampleRateSec}s 采样 / ±{selectedPoint.jitterMs}ms 抖动
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground text-[11px]">数据可信度</div>
                  <div className="mt-1 text-sm tabular-nums font-bold text-emerald-600">
                    {selectedPoint.confidence}%
                  </div>
                </div>
              </div>

              {/* Data Quality Impact Assessment */}
              <div className="rounded-lg border bg-muted/40 p-3.5 space-y-1.5">
                <div className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />
                  数据异常诊断与说明
                </div>
                <p className="text-muted-foreground leading-relaxed text-[11px]">
                  {selectedPoint.impactDescription}
                </p>
              </div>

              {/* Downstream Impacted Services */}
              <div className="space-y-2">
                <div className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                  <Layers className="h-3.5 w-3.5 text-muted-foreground" />
                  受影响下游业务与派生计算链
                </div>
                <div className="space-y-1.5">
                  {selectedPoint.affectedServices.map((service, idx) => (
                    <div key={idx} className="flex items-center justify-between rounded-md border border-border/80 p-2.5 text-xs bg-background">
                      <span className="font-medium text-foreground">{service}</span>
                      <Badge variant="outline" className="font-normal text-[10px] text-destructive border-destructive/30">
                        置信度降级 / 挂起
                      </Badge>
                    </div>
                  ))}
                </div>
              </div>

              {/* Action Buttons */}
              <div className="pt-3 border-t flex items-center justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => setSelectedPoint(null)}>
                  关闭
                </Button>
                <Button size="sm" className="gap-1.5">
                  <SlidersHorizontal className="h-3.5 w-3.5" />
                  下发就地传感器校准工单
                </Button>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
