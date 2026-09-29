import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  CheckCircle2,
  Cpu,
  Gauge,
  Search,
  Send,
  ShieldAlert,
  ShieldCheck,
  SlidersHorizontal,
  XCircle,
  Zap,
} from 'lucide-react';

import { Main } from '@/components/layout/Main';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { siteRoute } from '@/app/router-paths';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
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
import { useDataTable } from '@/hooks/use-data-table';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';

interface OptimizationPlansProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

interface SafetyCheckItem {
  id: string;
  name: string;
  category: string;
  status: 'PASSED' | 'WARNING' | 'FAILED';
  basis: string;
  operatorNotice: string;
}

const SAFETY_CHECKS: readonly SafetyCheckItem[] = [
  { id: 'sc-1', name: '用户控制与下发授权', category: '权限安全', status: 'PASSED', basis: '当前操作员具有 control.publish 权限', operatorNotice: '操作员凭据在有效期内' },
  { id: 'sc-2', name: '系统控制权与就绪状态', category: '控制权', status: 'PASSED', basis: '冷水机房自控柜处于 REMOTE_AUTO 状态', operatorNotice: '现场无物理钥匙锁定在 LOCAL 状态' },
  { id: 'sc-3', name: '冷冻/冷却水流物理联锁', category: '硬件联锁', status: 'PASSED', basis: '供水管靶式流量开关闭合，水流保护正常', operatorNotice: '水流状态双重传感器校验一致' },
  { id: 'sc-4', name: '设备通信健康度与新鲜度', category: '数据质量', status: 'PASSED', basis: '主控 PLC 通信时延 120ms，心跳周期正常', operatorNotice: '最近 10 分钟内无通信掉线记录' },
  { id: 'sc-5', name: '关键热工测点质量检验', category: '测点校验', status: 'PASSED', basis: '供回水温度传感器经校验无漂移失效', operatorNotice: '温差计算符合能量守恒' },
  { id: 'sc-6', name: '室内末端舒适度与湿度边界', category: '环境约束', status: 'WARNING', basis: 'B1 商业区相对湿度 63.5% (上限 65.0%)', operatorNotice: '出水温度建议重置幅度限制在 ≤ 8.0°C' },
  { id: 'sc-7', name: '备用设备就绪与冗余保障', category: '系统冗余', status: 'PASSED', basis: '3# 备用水泵与 2# 冷却塔处于热备用', operatorNotice: '故障自动倒泵逻辑已就绪' },
  { id: 'sc-8', name: '防频繁启停与温度防抖延时', category: '工艺保护', status: 'PASSED', basis: '距离上次主机出水温度调整已超 45 分钟', operatorNotice: '满足 30 分钟防抖工艺约束' },
];

export function OptimizationPlansConsole({ site }: OptimizationPlansProps) {
  const [approveDialogOpen, setApproveDialogOpen] = useState(false);
  const [rejectDialogOpen, setRejectDialogOpen] = useState(false);
  const [actionSuccess, setActionSuccess] = useState<string | null>(null);

  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<string>('ALL');
  const statusPills: readonly DataTableViewPillOption[] = useMemo(() => [
    { key: 'ALL', label: '全部检查', count: SAFETY_CHECKS.length },
    { key: 'PASSED', label: '检查通过', count: SAFETY_CHECKS.filter((s) => s.status === 'PASSED').length },
    { key: 'WARNING', label: '边界警示', count: SAFETY_CHECKS.filter((s) => s.status === 'WARNING').length },
    { key: 'FAILED', label: '禁止下发', count: SAFETY_CHECKS.filter((s) => s.status === 'FAILED').length },
  ], []);

  const filteredChecks = useMemo(() => {
    return SAFETY_CHECKS.filter((item) => {
      if (statusFilter !== 'ALL' && item.status !== statusFilter) return false;
      if (!search.trim()) return true;
      const q = search.toLowerCase();
      return item.name.toLowerCase().includes(q) || item.category.toLowerCase().includes(q) || item.basis.toLowerCase().includes(q);
    });
  }, [statusFilter, search]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, SafetyCheckItem>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部安全检查项"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.name}`}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'name', header: '检查维度', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span> },
    { id: 'category', header: '类别', cell: ({ row }) => <span className="text-sm text-muted-foreground">{row.original.category}</span> },
    {
      id: 'status',
      header: '检查状态',
      cell: ({ row }) => (
        <StatusPillBadge
          label={row.original.status === 'PASSED' ? '检查通过' : row.original.status === 'WARNING' ? '边界警示' : '禁止下发'}
          tone={row.original.status === 'PASSED' ? 'success' : row.original.status === 'WARNING' ? 'warning' : 'destructive'}
        />
      ),
    },
    { id: 'basis', header: '判定依据与实时数据', cell: ({ row }) => <span className="text-sm">{row.original.basis}</span> },
    { id: 'operatorNotice', header: '值班员注意说明', cell: ({ row }) => <span className="text-xs text-muted-foreground">{row.original.operatorNotice}</span> },
  ], []);

  const table = useDataTable({
    key: 'surface-22-optimization-safety-checks',
    data: [...filteredChecks],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  const passedCheckCount = SAFETY_CHECKS.filter((item) => item.status === 'PASSED').length;

  return (
    <Main className="space-y-6">
      {/* 1. Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">优化方案与策略评审</h1>
            <Badge variant="outline" className="text-xs font-normal">
              方案评审
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            查看节能优化建议与运行仿真预测，执行安全联锁核验与参数下发审批
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild>
            <a href={siteRoute(site, 'control')}>
              <SlidersHorizontal className="mr-1.5 size-3.5" />
              控制中心
            </a>
          </Button>
          <Button size="sm" onClick={() => setApproveDialogOpen(true)}>
            <Send className="mr-1.5 size-3.5" />
            批准下发方案
          </Button>
        </div>
      </div>

      {actionSuccess ? (
        <Alert>
          <CheckCircle2 className="size-4 text-emerald-600" />
          <AlertTitle>操作成功</AlertTitle>
          <AlertDescription>{actionSuccess}</AlertDescription>
        </Alert>
      ) : null}

      {/* 2. 4-Card Optimization Fact Strip */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">推荐优化方案</CardTitle>
            <Cpu className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-lg font-bold tracking-tight text-foreground line-clamp-1">供水温度自适应提升</div>
            <p className="text-xs text-muted-foreground mt-1">出水设定 7.0°C → 8.0°C · 主机减荷</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">预期系统 COP 提升</CardTitle>
            <Gauge className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">4.68 → 4.88</div>
              <Badge variant="outline" className="tabular-nums text-[10px] font-normal">+4.3%</Badge>
            </div>
            <p className="text-xs text-muted-foreground mt-1">基于未来 4h 冷负荷预测</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">预期今日节电量</CardTitle>
            <Zap className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">385.0 <span className="text-sm font-normal text-muted-foreground">kWh/天</span></div>
              <span className="text-xs text-muted-foreground font-medium">约省 ¥318/天</span>
            </div>
            <p className="text-xs text-muted-foreground mt-1">满足室内舒适度要求</p>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">安全前置检查通过率</CardTitle>
            <ShieldCheck className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <div className="text-2xl font-bold tracking-tight text-foreground tabular-nums">{passedCheckCount} / {SAFETY_CHECKS.length}</div>
              <Badge variant="outline" className="text-[10px] font-normal gap-1">
                <span className="size-1 rounded-full bg-amber-500" />
                1项关注
              </Badge>
            </div>
            <p className="text-xs text-muted-foreground mt-1">联锁正常 · 参数受控</p>
          </CardContent>
        </Card>
      </div>

      {/* 3. Main Optimization Plan & Simulation Workbench */}
      <div className="grid gap-6 lg:grid-cols-3">
        {/* Left 2 Cols: Simulation Comparison */}
        <Card className="shadow-xs lg:col-span-2">
          <CardHeader className="pb-4">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-lg">工况仿真对比与能耗预测</CardTitle>
                <CardDescription>对比当前基准与仿真优化设定值</CardDescription>
              </div>
              <Badge variant="outline">置信度：92.5%</Badge>
            </div>
          </CardHeader>
          <CardContent className="space-y-6">
            <div className="grid grid-cols-2 gap-4">
              {/* Baseline */}
              <div className="rounded-lg border p-4 bg-muted/20 space-y-3">
                <div className="flex items-center justify-between border-b pb-2">
                  <span className="text-sm font-semibold text-muted-foreground">当前运行工况 (基准)</span>
                  <Badge variant="outline" className="text-xs font-normal">现行设定</Badge>
                </div>
                <div className="space-y-2 text-sm">
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">冷冻供水设定:</span>
                    <strong className="font-semibold tabular-nums">7.0 °C</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">1# 主机运行功率:</span>
                    <strong className="font-semibold tabular-nums">342.5 kW</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">冷水主机综合 COP:</span>
                    <strong className="font-semibold tabular-nums">5.42</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">冷站系统综合 COP:</span>
                    <strong className="font-semibold tabular-nums">4.68</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">全系统实时功耗:</span>
                    <strong className="font-semibold tabular-nums">412.3 kW</strong>
                  </div>
                </div>
              </div>

              {/* Proposed Target */}
              <div className="rounded-lg border p-4 bg-muted/40 space-y-3">
                <div className="flex items-center justify-between border-b pb-2">
                  <span className="text-sm font-semibold text-foreground">推荐优化目标 (仿真)</span>
                  <Badge variant="outline" className="font-normal text-xs gap-1.5">
                    <span className="size-1.5 rounded-full bg-emerald-500" />
                    预期节能
                  </Badge>
                </div>
                <div className="space-y-2 text-sm">
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">冷冻供水设定:</span>
                    <strong className="font-semibold tabular-nums text-foreground">8.0 °C (+1.0°C)</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">1# 主机预测功率:</span>
                    <strong className="font-semibold tabular-nums text-foreground">324.0 kW (-18.5 kW)</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">冷水主机综合 COP:</span>
                    <strong className="font-semibold tabular-nums text-foreground">5.65 (+4.2%)</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">冷站系统综合 COP:</span>
                    <strong className="font-semibold tabular-nums text-foreground">4.88 (+4.3%)</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">全系统预测功耗:</span>
                    <strong className="font-semibold tabular-nums text-foreground">393.8 kW (-18.5 kW)</strong>
                  </div>
                </div>
              </div>
            </div>

            {/* Implementation Step Sequence */}
            <div className="space-y-3">
              <div className="text-sm font-semibold">执行步骤编排</div>
              <div className="grid grid-cols-4 gap-2 text-center text-xs">
                <div className="rounded-md border p-2 bg-muted/30">
                  <div className="font-semibold">Step 1</div>
                  <div className="text-muted-foreground mt-0.5">前置安全校验</div>
                </div>
                <div className="rounded-md border p-2 bg-muted/30">
                  <div className="font-semibold">Step 2</div>
                  <div className="text-muted-foreground mt-0.5">下发设定 7.5°C</div>
                </div>
                <div className="rounded-md border p-2 bg-muted/30">
                  <div className="font-semibold">Step 3</div>
                  <div className="text-muted-foreground mt-0.5">观察 15min 水流</div>
                </div>
                <div className="rounded-md border p-2 bg-muted/30">
                  <div className="font-semibold">Step 4</div>
                  <div className="text-muted-foreground mt-0.5">平稳升至 8.0°C</div>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Right 1 Col: Decision Actions & Operator Notes */}
        <Card className="shadow-xs flex flex-col justify-between">
          <CardHeader>
            <CardTitle className="text-base">方案决策与审批动作</CardTitle>
            <CardDescription>策略发布将作为控制指令传输至控制中心并留存审计</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="rounded-md border p-3 bg-muted/20 space-y-2 text-xs">
              <div className="flex items-center gap-1.5 font-semibold text-foreground">
                <ShieldAlert className="size-4 text-amber-500" />
                安全保护规则
              </div>
              <p className="text-muted-foreground leading-relaxed">
                方案下发后保持现场联锁生效。若末端不利环路温度超标，控制系统将自动回退至 7.0°C 设定值。
              </p>
            </div>

            <div className="space-y-2 text-xs text-muted-foreground">
              <div className="flex justify-between">
                <span>策略编号:</span>
                <span className="font-mono text-foreground">OPT-CHW-20260918-01</span>
              </div>
              <div className="flex justify-between">
                <span>生成时间:</span>
                <span className="text-foreground">今日 14:30</span>
              </div>
              <div className="flex justify-between">
                <span>算法模型:</span>
                <span className="text-foreground">机理模型与负荷预测</span>
              </div>
            </div>

            <div className="pt-2 flex flex-col gap-2">
              <Button onClick={() => setApproveDialogOpen(true)}>
                <CheckCircle2 className="mr-1.5 size-4" />
                批准并下发执行
              </Button>
              <Button variant="outline" onClick={() => setRejectDialogOpen(true)}>
                <XCircle className="mr-1.5 size-4" />
                驳回该方案
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 4. 8-Dimension Safety Check Matrix Table */}
      <Card className="shadow-xs">
        <CardHeader className="pb-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <div className="flex items-center gap-2">
                <CardTitle className="text-lg">安全边界前置检查</CardTitle>
                <Badge variant="outline" className="font-normal gap-1.5 text-xs">
                  <span className="size-1.5 rounded-full bg-emerald-500" />
                  前置判定通过 ({passedCheckCount}/{SAFETY_CHECKS.length})
                </Badge>
              </div>
              <CardDescription>核验权限、物理联锁与测点状态，确保具备下发条件</CardDescription>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative min-w-[200px]">
                <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  className="pl-8 h-9 text-sm"
                  placeholder="搜索检查维度 / 类别..."
                  value={search}
                  onChange={(e) => {
                    setSearch(e.target.value);
                    table.setPageIndex(0);
                  }}
                />
              </div>
              <DataTableViewPills
                options={statusPills}
                value={statusFilter}
                onValueChange={(v) => {
                  setStatusFilter(v);
                  table.setPageIndex(0);
                }}
              />
              <DataTableViewOptions table={table} />
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <DataTable
            table={table}
            tableAriaLabel="安全边界前置检查"
            empty="无匹配的安全检查维度"
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'select' ? 'w-[40px] px-3' :
                header.id === 'name' ? 'w-[180px]' :
                header.id === 'category' || header.id === 'status' ? 'w-[120px]' :
                header.id === 'basis' ? 'min-w-[280px]' :
                header.id === 'operatorNotice' ? 'min-w-[220px]' :
                undefined,
            })}
            getRowProps={() => ({ className: 'hover:bg-muted/50' })}
            getCellProps={(cell) => ({
              className: cell.column.id === 'select' ? 'px-3' : undefined,
            })}
            footer={(
              <DataTablePagination
                table={table}
                totalRows={filteredChecks.length}
                
                
                
                
                
              />
            )}
          />
        </CardContent>
      </Card>

      {/* 5. Approve Dialog */}
      <Dialog open={approveDialogOpen} onOpenChange={setApproveDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>批准并下发优化方案</DialogTitle>
            <DialogDescription>
              确认将「冷冻水供水温度自适应提升 (7.0°C → 8.0°C)」下发至控制中心？
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2 text-sm text-muted-foreground">
            <p>• 前置安全检查均已验证，符合下发条件。</p>
            <p>• 下发记录将计入系统控制审计日志。</p>
            <p>• 系统将分阶段阶梯升温，并实时监视回退条件。</p>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setApproveDialogOpen(false)}>取消</Button>
            <Button onClick={() => {
              setApproveDialogOpen(false);
              setActionSuccess('优化方案已批准并下发，系统开始执行阶梯设定。');
            }}>
              确认下发
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 6. Reject Dialog */}
      <Dialog open={rejectDialogOpen} onOpenChange={setRejectDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>驳回优化方案</DialogTitle>
            <DialogDescription>
              请填写驳回原因以备记录归档。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2 py-2 text-sm">
            <label className="text-xs font-medium text-muted-foreground">驳回说明</label>
            <textarea
              className="w-full h-20 rounded-md border p-2 text-sm"
              placeholder="例如：今日有重要租户高密会议活动，暂缓调高冷冻水出水温度。"
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRejectDialogOpen(false)}>取消</Button>
            <Button variant="destructive" onClick={() => {
              setRejectDialogOpen(false);
              setActionSuccess('方案已驳回，原因已归档记录。');
            }}>
              确认驳回
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Main>
  );
}
