import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  CheckCircle2,
  Cpu,
  RefreshCw,
  RotateCcw,
  Send,
  ShieldCheck,
  SlidersHorizontal,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Slider } from '@/components/ui/slider';

export interface ControlOptimizationConsoleProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime?: unknown;
  readonly searchState?: unknown;
  readonly onSearchChange?: (patch: unknown) => void;
}

const SAFETY_INTERLOCKS = [
  { name: '冷冻水防冻保护', status: '3.5°C 阈值保护生效', ok: true },
  { name: '冷却水进水低限', status: '18.0°C 保护底线', ok: true },
  { name: '泵机联锁逻辑', status: '先开泵后开机闭环', ok: true },
  { name: '最小启停时间间隔', status: '30min 延时防频启', ok: true },
  { name: '变频器过载保护', status: '无跳闸告警正常', ok: true },
  { name: '冷水水流开关', status: '闭合状态正常', ok: true },
  { name: '机房紧急停机', status: '硬件就绪待命', ok: true },
  { name: '双重指令审计', status: '高级运维员校验通过', ok: true },
];

const DISPATCH_HISTORY = [
  { time: '15:30:22', target: '冷水供水温度设定', action: '设定值微调', from: '7.0 °C', to: '7.5 °C', operator: '群控自动寻优', status: '已生效执行', ok: true },
  { time: '14:00:15', target: '2# 一次冷冻水泵', action: '频率寻优调节', from: '45.0 Hz', to: '47.0 Hz', operator: '群控自动寻优', status: '已生效执行', ok: true },
  { time: '11:00:00', target: '2# 离心冷水机组', action: '根据负荷加载开机', from: '停机待命', to: '并联运行', operator: '群控自动寻优', status: '已生效执行', ok: true },
  { time: '09:15:30', target: '冷却水进水温度设定', action: '湿球跟踪设定', from: '30.0 °C', to: '29.0 °C', operator: '林值班 (人工)', status: '已生效执行', ok: true },
];

export function ControlOptimizationConsole({ site }: ControlOptimizationConsoleProps) {
  const [chwTempSetpoint, setChwTempSetpoint] = useState<number>(7.5);
  const [cwTempSetpoint, setCwTempSetpoint] = useState<number>(29.0);
  const [isAiOptimizing, setIsAiOptimizing] = useState<boolean>(true);

  type DispatchHistoryItem = (typeof DISPATCH_HISTORY)[number];

  const dispatchColumns = useMemo<Array<ColumnDef<DataTableFeatures, DispatchHistoryItem>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选调度记录"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.time} ${row.original.target}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'time', header: '下发时间', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.time}</span> },
    { id: 'target', header: '控制对象', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.target}</span> },
    { id: 'action', header: '控制动作类型', cell: ({ row }) => <span className="text-muted-foreground">{row.original.action}</span> },
    { id: 'from', header: '变更前参数', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{row.original.from}</span> },
    { id: 'to', header: '新设定目标值', cell: ({ row }) => <span className="font-mono font-semibold text-foreground tabular-nums">{row.original.to}</span> },
    { id: 'operator', header: '操作责任人', cell: ({ row }) => <span className="text-foreground">{row.original.operator}</span> },
    { id: 'status', header: '执行状态', cell: ({ row }) => <StatusBadge tone={row.original.ok ? 'success' : 'destructive'} label={row.original.status} /> },
  ], []);

  const dispatchTable = useDataTable({
    key: 'surface-25-control-dispatch-history',
    data: [...DISPATCH_HISTORY],
    columns: dispatchColumns,
    pageSize: 10,
    getRowId: (row) => `${row.time}-${row.target}`,
  });

  return (
    <Main className="space-y-6" data-testid="control-optimization-console">
      {/* Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">控制优化</h1>
            <Badge variant="outline" className="gap-1.5 text-xs font-normal">
              <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
              群控自适应寻优
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">{site.displayName} · {site.timezone}</p>
        </div>

        <div className="flex items-center gap-2">
          <Button
            variant={isAiOptimizing ? 'secondary' : 'outline'}
            size="sm"
            onClick={() => setIsAiOptimizing(!isAiOptimizing)}
            className="h-8 gap-1.5 text-xs font-medium"
          >
            <Cpu className="size-3.5" />
            {isAiOptimizing ? '群控自动寻优中' : '手动干预模式'}
          </Button>
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs font-medium">
            <RotateCcw className="size-3.5" />
            复位基准
          </Button>
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs font-medium">
            <RefreshCw className="size-3.5" />
            刷新
          </Button>
        </div>
      </div>

      {/* Safety Interlock Matrix Strip */}
      <Card className="shadow-xs">
        <CardHeader className="pb-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <ShieldCheck className="size-4 text-emerald-600 dark:text-emerald-500" />
              <CardTitle className="text-sm font-semibold text-foreground">
                设备与系统安全硬联锁
              </CardTitle>
            </div>
            <Badge variant="outline" className="gap-1.5 text-xs font-normal text-muted-foreground">
              <span className="size-1.5 rounded-full bg-emerald-500" />
              全部联锁有效 (8/8)
            </Badge>
          </div>
          <CardDescription className="text-xs">
            控制指令下发前硬边界校验机制，确保设备物理安全与运行约束受控
          </CardDescription>
        </CardHeader>
        <CardContent className="pt-0">
          <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4 lg:grid-cols-8">
            {SAFETY_INTERLOCKS.map((item, idx) => (
              <div key={idx} className="rounded-lg border bg-muted/20 p-2.5 text-center transition-colors hover:bg-muted/40">
                <div className="flex items-center justify-center gap-1.5 text-xs font-medium text-foreground">
                  <CheckCircle2 className="size-3.5 text-emerald-600 dark:text-emerald-500 shrink-0" />
                  <span className="truncate">{item.name}</span>
                </div>
                <div className="text-[11px] text-muted-foreground truncate mt-1">{item.status}</div>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* 3 Parameter Control Cards */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Card 1: Chilled Water Temp Setpoint */}
        <Card className="shadow-xs flex flex-col justify-between">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <CardTitle className="text-sm font-semibold">冷水供水温度自寻优设定</CardTitle>
              <Badge variant="outline" className="text-xs font-normal">
                核心控制
              </Badge>
            </div>
            <CardDescription className="text-xs">
              动态提高出水温度以降低主机电耗
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-3 rounded-lg border bg-muted/20 p-3">
              <div>
                <span className="text-[11px] text-muted-foreground block">当前实测出水温</span>
                <span className="tabular-nums text-xl font-bold text-foreground">7.0 <span className="text-xs font-normal text-muted-foreground">°C</span></span>
              </div>
              <div className="text-right">
                <span className="text-[11px] text-muted-foreground block">推荐寻优设定</span>
                <span className="tabular-nums text-xl font-bold text-primary">7.8 <span className="text-xs font-normal text-muted-foreground">°C</span></span>
              </div>
            </div>

            <div className="space-y-2.5">
              <div className="flex justify-between items-center text-xs">
                <span className="text-muted-foreground">控制设定目标:</span>
                <span className="tabular-nums font-bold text-foreground text-sm">{chwTempSetpoint.toFixed(1)} °C</span>
              </div>
              <Slider
                value={[chwTempSetpoint]}
                min={6.0}
                max={9.0}
                step={0.1}
                onValueChange={(val) => setChwTempSetpoint(Number(val[0].toFixed(1)))}
                className="py-1"
              />
              <div className="flex justify-between text-[11px] text-muted-foreground tabular-nums">
                <span>6.0 °C (强冷)</span>
                <span>7.0 °C (基准)</span>
                <span>9.0 °C (节能)</span>
              </div>
            </div>

            <div className="rounded-md border bg-muted/30 p-3 text-xs text-muted-foreground space-y-1">
              <span className="font-medium text-foreground block">节能效益评估</span>
              <p className="leading-relaxed">
                出水温设定提升至 {chwTempSetpoint.toFixed(1)}°C，预计降低主机瞬时功耗约 3.2%（节约约 7.6 kW）。
              </p>
            </div>

            <Button className="w-full h-8 gap-1.5 text-xs font-medium">
              <Send className="size-3.5" />
              下发新出水温度设定值
            </Button>
          </CardContent>
        </Card>

        {/* Card 2: Cooling Water Temp Optimization */}
        <Card className="shadow-xs flex flex-col justify-between">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <CardTitle className="text-sm font-semibold">冷却水进水温度湿球逼近控制</CardTitle>
              <Badge variant="outline" className="text-xs font-normal">
                散热调优
              </Badge>
            </div>
            <CardDescription className="text-xs">
              根据湿球温度控制塔风机频率与逼近度
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-3 rounded-lg border bg-muted/20 p-3">
              <div>
                <span className="text-[11px] text-muted-foreground block">当前实测进水温</span>
                <span className="tabular-nums text-xl font-bold text-foreground">29.0 <span className="text-xs font-normal text-muted-foreground">°C</span></span>
              </div>
              <div className="text-right">
                <span className="text-[11px] text-muted-foreground block">室外湿球逼近极限</span>
                <span className="tabular-nums text-xl font-bold text-foreground">27.5 <span className="text-xs font-normal text-muted-foreground">°C</span></span>
              </div>
            </div>

            <div className="space-y-2.5">
              <div className="flex justify-between items-center text-xs">
                <span className="text-muted-foreground">冷却水控制目标:</span>
                <span className="tabular-nums font-bold text-foreground text-sm">{cwTempSetpoint.toFixed(1)} °C</span>
              </div>
              <Slider
                value={[cwTempSetpoint]}
                min={26.0}
                max={32.0}
                step={0.5}
                onValueChange={(val) => setCwTempSetpoint(Number(val[0].toFixed(1)))}
                className="py-1"
              />
              <div className="flex justify-between text-[11px] text-muted-foreground tabular-nums">
                <span>26.0 °C (低温)</span>
                <span>29.0 °C (额定)</span>
                <span>32.0 °C (高负荷)</span>
              </div>
            </div>

            <div className="rounded-md border bg-muted/30 p-3 text-xs text-muted-foreground space-y-1">
              <span className="font-medium text-foreground block">调优策略建议</span>
              <p className="leading-relaxed">
                当前室外湿球温度 24.2°C，将冷却塔风机频率提至 46Hz 可将进水温降至 28.0°C，提升主机 COP 4.1%。
              </p>
            </div>

            <Button variant="outline" className="w-full h-8 gap-1.5 text-xs font-medium">
              <SlidersHorizontal className="size-3.5" />
              同步冷却水群控策略
            </Button>
          </CardContent>
        </Card>

        {/* Card 3: Chiller Sequencing Optimization */}
        <Card className="shadow-xs flex flex-col justify-between">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <CardTitle className="text-sm font-semibold">主机群控加减载智能调度</CardTitle>
              <Badge variant="outline" className="text-xs font-normal">
                启停切换
              </Badge>
            </div>
            <CardDescription className="text-xs">
              基于负荷预测的机组启停台数调度
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="rounded-lg border bg-muted/20 p-3 space-y-2 text-xs">
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">当前运行编组:</span>
                <span className="font-semibold text-foreground">2 用 1 备 (CH-01 + CH-02)</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">当前总负荷率:</span>
                <span className="tabular-nums font-bold text-foreground">71.4% <span className="text-muted-foreground font-normal">(在最佳 COP 区间)</span></span>
              </div>
              <div className="flex items-center justify-between text-muted-foreground pt-1 border-t">
                <span>下一阶段调度预判:</span>
                <span className="font-medium text-foreground">17:30 预测卸载 CH-02</span>
              </div>
            </div>

            <div className="space-y-2 text-xs">
              <span className="text-muted-foreground font-medium block">各机组实时负载平衡:</span>
              <div className="space-y-2">
                <div className="space-y-1">
                  <div className="flex items-center justify-between text-[11px]">
                    <span className="font-medium">CH-01 (离心冷机)</span>
                    <span className="tabular-nums text-muted-foreground">78% 负荷 · COP 6.2</span>
                  </div>
                  <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
                    <div className="h-full rounded-full bg-primary" style={{ width: '78%' }} />
                  </div>
                </div>

                <div className="space-y-1">
                  <div className="flex items-center justify-between text-[11px]">
                    <span className="font-medium">CH-02 (离心冷机)</span>
                    <span className="tabular-nums text-muted-foreground">68% 负荷 · COP 5.8</span>
                  </div>
                  <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
                    <div className="h-full rounded-full bg-primary/70" style={{ width: '68%' }} />
                  </div>
                </div>
              </div>
            </div>

            <Button variant="outline" className="w-full h-8 gap-1.5 text-xs font-medium">
              <RotateCcw className="size-3.5" />
              重新计算负载平衡策略
            </Button>
          </CardContent>
        </Card>
      </div>

      {/* Command Dispatch History & Audit Log */}
      <DataTableBlock
        title="控制变更记录"
        actions={<Badge variant="outline" className="font-normal">审计链完整合规</Badge>}
      >
        <DataTable
          table={dispatchTable}
          tableAriaLabel="控制变更记录"
          getHeaderRowProps={() => ({ className: 'bg-muted/20' })}
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-12 text-center' :
              header.id === 'time' ? 'w-28' :
              header.id === 'from' || header.id === 'to' ? 'text-right' :
              header.id === 'status' ? 'w-28 text-center' :
              undefined,
          })}
          getRowProps={() => ({ className: 'hover:bg-muted/40' })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'select' || cell.column.id === 'status'
                ? 'text-center'
                : cell.column.id === 'from' || cell.column.id === 'to'
                  ? 'text-right'
                  : undefined,
          })}
          footer={<DataTablePagination table={dispatchTable} totalRows={DISPATCH_HISTORY.length} />}
        />
      </DataTableBlock>
    </Main>
  );
}
