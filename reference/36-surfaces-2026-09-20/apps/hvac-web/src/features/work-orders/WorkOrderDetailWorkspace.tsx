import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Wrench,
  Clock,
  CheckCircle2,
  ArrowLeft,
  User,
  ShieldCheck,
  Package,
  Layers,
  Send,
  Timer,
  AlertTriangle,
  FileCheck2,
  Check,
} from 'lucide-react';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Progress } from '@/components/ui/progress';
import { Textarea } from '@/components/ui/textarea';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';

interface ChecklistItem {
  readonly id: string;
  readonly title: string;
  readonly requirement: string;
  readonly completed: boolean;
  readonly operator: string;
  readonly resultNotes: string;
  readonly timestamp?: string;
}

interface SparePart {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly specification: string;
  readonly quantity: number;
  readonly unit: string;
  readonly lotNumber: string;
}

interface TimelineEntry {
  readonly id: string;
  readonly time: string;
  readonly action: string;
  readonly operator: string;
  readonly detail: string;
  readonly status: 'done' | 'active' | 'pending';
}

export function WorkOrderDetailWorkspace({
  siteId,
  workOrderId = 'WO-202609-082',
}: {
  readonly siteId: string;
  readonly workOrderId?: string;
}) {
  const [checklist, setChecklist] = useState<readonly ChecklistItem[]>([
    {
      id: 'chk-1',
      title: '现场手/自动旋钮切换至检修位并进行电气安全挂牌上锁 (LOTO)',
      requirement: '断开执行机构 220V 动力电源并挂牌，用万用表测定输入端无电压',
      completed: true,
      operator: '张志远 (暖通技术员)',
      resultNotes: '已在 2# 控制柜完成断电与安全锁紧，实测电压 0.0V',
      timestamp: '2026-09-18 10:05',
    },
    {
      id: 'chk-2',
      title: '关闭阀前/后手动隔断蝶阀，使用红外测温仪复测阀体前后温差',
      requirement: '若完全关断，阀后管道温度应与环境温度趋同（温差应 > 2.5℃）',
      completed: true,
      operator: '张志远 (暖通技术员)',
      resultNotes: '阀前 8.1℃，阀后 8.3℃，温差仅 0.2℃，证实低水阻内漏回流严重',
      timestamp: '2026-09-18 10:30',
    },
    {
      id: 'chk-3',
      title: '拆卸阀盖检查阀芯与阀座密封面磨损情况',
      requirement: '密封面划痕深度不得超过 0.1mm，氟橡胶 O 型圈弹性良好',
      completed: true,
      operator: '张志远 (暖通技术员)',
      resultNotes: '原装 O 型密封圈严重硬化开裂，并在密封面发现焊渣硬质颗粒卡阻',
      timestamp: '2026-09-18 11:20',
    },
    {
      id: 'chk-4',
      title: '清理阀腔杂质并更换全新耐高温耐磨密封组件',
      requirement: '采用原厂 DN200 专用密封包，按对角十字对称顺序紧固法兰螺栓 (扭矩 85 N·m)',
      completed: true,
      operator: '李建军 (机修工)',
      resultNotes: '清理焊渣完成，更换原厂密封包并校准 85 N·m 紧固扭矩完成',
      timestamp: '2026-09-18 13:10',
    },
    {
      id: 'chk-5',
      title: '恢复执行机构电动控制，测试 0-100% 行程闭合严密性',
      requirement: '4-20mA 信号全行程响应时间 < 30 秒，0% 位反馈开关动作可靠',
      completed: false,
      operator: '待执行',
      resultNotes: '等待现场供电恢复后执行信号联动比对',
    },
    {
      id: 'chk-6',
      title: '调适功能验证 (MBCx)：系统大温差与二次泵能效比恢复测试',
      requirement: '冷冻水供回水大温差恢复至 ≥ 4.2℃，二次泵转速回落至 40Hz 以下',
      completed: false,
      operator: '待执行',
      resultNotes: '需冷站连续带负荷运行 30 分钟后记录遥测证据',
    },
  ]);

  const [spareParts] = useState<readonly SparePart[]>([
    {
      id: 'sp-1',
      code: 'SEAL-DN200-FKM',
      name: '耐高温氟橡胶密封包 (DN200 蝶阀专用)',
      specification: 'DN200 PN16 / Shore A 75',
      quantity: 1,
      unit: '套',
      lotNumber: 'LOT-2026-FKM-04',
    },
    {
      id: 'sp-2',
      code: 'ACT-POT-10K',
      name: '角行程阀位反馈电位器模块 (0-10V)',
      specification: '10kΩ 高精度角位移',
      quantity: 1,
      unit: '个',
      lotNumber: 'LOT-2026-POT-11',
    },
    {
      id: 'sp-3',
      code: 'LUB-VALVE-500',
      name: '高压阀杆特种润滑脂 500g',
      specification: 'NLGI 2 / 500g 罐装',
      quantity: 1,
      unit: '罐',
      lotNumber: 'LOT-2026-LUB-01',
    },
  ]);

  const [timeline] = useState<readonly TimelineEntry[]>([
    {
      id: 'tl-1',
      time: '09-18 09:12',
      action: 'FDD 诊断触发派单',
      operator: '诊断系统自动调度',
      detail: '触发项 FDD-202609-001 (冷冻水供回水大温差严重衰减至 2.1℃)',
      status: 'done',
    },
    {
      id: 'tl-2',
      time: '09-18 09:35',
      action: '值班长接单响应并指派',
      operator: '林值班 (运行长)',
      detail: '工单指派至抢修一组，主责维修人张志远，协同机修工李建军',
      status: 'done',
    },
    {
      id: 'tl-3',
      time: '09-18 10:15',
      action: '现场到达与停机隔离挂牌',
      operator: '张志远 / 李建军',
      detail: '关闭 2 号板换旁通管路上游闸阀，执行 LOTO 上锁挂牌安全规范',
      status: 'done',
    },
    {
      id: 'tl-4',
      time: '09-18 11:45',
      action: '解体拆修与密封套件更换',
      operator: '李建军 (机修工)',
      detail: '拆除阀体清理结垢杂质，更换密封件 LOT-2026-FKM-04，回装测试无外漏',
      status: 'done',
    },
    {
      id: 'tl-5',
      time: '09-18 14:00',
      action: '等待调适功能验证 (MBCx)',
      operator: '系统自动路由',
      detail: '关联功能测试项 MBCX-202609-12，需待冷冻站二次回路加压恢复后进行工况核验',
      status: 'active',
    },
  ]);

  const [fieldNote, setFieldNote] = useState('');
  const [notesHistory, setNotesHistory] = useState<readonly string[]>([
    '现场拆检发现冷却水回水管路有微量铁锈残渣，建议在下次季度停机保养时对冷冻水 Y 型过滤器进行彻底反冲洗。',
  ]);

  const toggleChecklist = (id: string) => {
    setChecklist((prev) =>
      prev.map((item) =>
        item.id === id ? { ...item, completed: !item.completed } : item
      )
    );
  };

  const handleAddNote = () => {
    if (!fieldNote.trim()) return;
    setNotesHistory((prev) => [fieldNote.trim(), ...prev]);
    setFieldNote('');
  };

  const completedCount = checklist.filter((item) => item.completed).length;
  const progressPercent = Math.round((completedCount / checklist.length) * 100);

  const spareColumns = useMemo<Array<ColumnDef<DataTableFeatures, SparePart>>>(() => [
    { id: 'code', header: '备件编码', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground">{row.original.code}</span> },
    { id: 'name', header: '备件名称', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span> },
    { id: 'specification', header: '规格型号与技术标准', cell: ({ row }) => <span className="text-[11px] text-muted-foreground">{row.original.specification}</span> },
    { id: 'quantity', header: '领用数量', cell: ({ row }) => <span className="font-mono font-bold text-foreground tabular-nums">{row.original.quantity} {row.original.unit}</span> },
    { id: 'lotNumber', header: '生产批号 / 溯源', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.lotNumber}</span> },
  ], []);

  const spareTable = useDataTable({
    key: 'surface-12-work-order-spare-parts',
    data: [...spareParts],
    columns: spareColumns,
    pageSize: 5,
    getRowId: (row) => row.id,
  });

  return (
    <Main className="space-y-6">
      {/* 顶部面包屑与标题区 */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2 text-xs text-muted-foreground mb-1">
            <Button variant="link" size="sm" asChild className="p-0 h-auto text-xs text-muted-foreground">
              <a href={`/sites/${siteId}/work-orders`} className="flex items-center gap-1">
                <ArrowLeft className="h-3.5 w-3.5" />
                返回工单中心
              </a>
            </Button>
            <span>/</span>
            <span>工单详情</span>
            <span>/</span>
            <span className="font-mono text-foreground font-medium">{workOrderId}</span>
          </div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">
              冷水二次泵旁通阀内漏与大温差衰减抢修
            </h1>
            <Badge variant="destructive" className="text-xs font-semibold">
              HIGH / 紧急
            </Badge>
            <Badge variant="outline" className="gap-1.5 text-xs font-normal">
              <span className="size-1.5 rounded-full bg-amber-500 animate-pulse" />
              待运行验证 (MBCx)
            </Badge>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild className="h-8 text-xs gap-1.5">
            <a href={`/sites/${siteId}/verifications`}>
              <ShieldCheck className="h-3.5 w-3.5" />
              查看关联验证 (MBCx)
            </a>
          </Button>
          <Button size="sm" className="h-8 text-xs gap-1.5">
            <CheckCircle2 className="h-3.5 w-3.5" />
            确认并归档工单
          </Button>
        </div>
      </div>

      {/* 工单主体：成熟的双栏工程工作台 */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
        {/* 左侧主要内容区 (8 栏 ~ 67%) */}
        <div className="lg:col-span-8 space-y-6">
          {/* 故障现象与根因遥测证据 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3 border-b">
              <div className="flex items-center justify-between">
                <div className="text-sm font-semibold text-foreground flex items-center gap-2">
                  <AlertTriangle className="h-4 w-4 text-muted-foreground" />
                  故障现象与排查依据
                </div>
                <Badge variant="outline" className="text-[11px] font-normal font-mono text-muted-foreground">
                  诊断规则触发
                </Badge>
              </div>
            </CardHeader>
            <CardContent className="pt-4">
              <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-xs">
                <div className="rounded-lg border bg-muted/20 p-3 space-y-1">
                  <div className="text-muted-foreground font-medium text-[11px]">根因诊断条目</div>
                  <div className="font-mono font-bold text-foreground">DIAG-FDD-08</div>
                  <div className="text-muted-foreground text-[11px]">板换二次侧温差异常衰减 (ΔT = 2.1℃)</div>
                </div>
                <div className="rounded-lg border bg-muted/20 p-3 space-y-1">
                  <div className="text-muted-foreground font-medium text-[11px]">触发告警事实</div>
                  <div className="font-mono font-bold text-foreground">ALM-202609-014</div>
                  <div className="text-muted-foreground text-[11px]">二次泵变频器长时间 50Hz 满载超限</div>
                </div>
                <div className="rounded-lg border bg-muted/20 p-3 space-y-1">
                  <div className="text-muted-foreground font-medium text-[11px]">受损能耗估算</div>
                  <div className="tabular-nums font-bold text-foreground">约 +380 kWh / 日</div>
                  <div className="text-muted-foreground text-[11px]">大温差衰减导致流量激增与高电耗</div>
                </div>
              </div>
            </CardContent>
          </Card>

          {/* 现场检修标准化作业清单 */}
          <Card className="shadow-xs overflow-hidden">
            <CardHeader className="pb-3 border-b">
              <div className="flex items-center justify-between">
                <div>
                  <CardTitle className="text-sm font-semibold flex items-center gap-2">
                    <Wrench className="h-4 w-4 text-primary" />
                    现场作业核对清单
                  </CardTitle>
                  <CardDescription className="text-xs mt-0.5">
                    现场作业人员按步骤核对、记录并确认处理结果
                  </CardDescription>
                </div>
                <div className="flex items-center gap-3">
                  <div className="w-28 space-y-1">
                    <div className="flex items-center justify-between text-[11px] tabular-nums text-muted-foreground">
                      <span>进度</span>
                      <span>{progressPercent}%</span>
                    </div>
                    <Progress value={progressPercent} className="h-1.5" />
                  </div>
                  <span className="font-mono text-xs font-semibold text-foreground">
                    {completedCount} / {checklist.length}
                  </span>
                </div>
              </div>
            </CardHeader>
            <CardContent className="p-0 divide-y">
              {checklist.map((item, idx) => (
                <div
                  key={item.id}
                  className={`p-4 transition-colors flex items-start gap-3.5 ${
                    item.completed ? 'bg-muted/10' : 'bg-card'
                  }`}
                >
                  <button
                    type="button"
                    onClick={() => toggleChecklist(item.id)}
                    className={`mt-0.5 h-5 w-5 rounded border flex items-center justify-center shrink-0 transition-colors ${
                      item.completed
                        ? 'bg-primary border-primary text-primary-foreground'
                        : 'border-muted-foreground/40 hover:border-foreground'
                    }`}
                  >
                    {item.completed && <Check className="h-3.5 w-3.5 stroke-[3]" />}
                  </button>

                  <div className="space-y-1 flex-1">
                    <div className="flex items-baseline justify-between">
                      <div className={`text-xs font-semibold ${item.completed ? 'text-foreground line-through opacity-70' : 'text-foreground'}`}>
                        {idx + 1}. {item.title}
                      </div>
                      {item.timestamp && (
                        <span className="font-mono text-[10px] text-muted-foreground shrink-0 ml-2">
                          {item.timestamp}
                        </span>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground leading-relaxed">
                      规范要求: {item.requirement}
                    </div>
                    {item.resultNotes && (
                      <div className="rounded bg-muted/40 p-2 text-[11px] text-foreground font-mono mt-1 border">
                        <span className="font-bold">实测记录:</span> {item.resultNotes}{' '}
                        <span className="text-muted-foreground">({item.operator})</span>
                      </div>
                    )}
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>

          {/* 消耗备件与物料核销台账 */}
          <Card className="shadow-xs overflow-hidden">
            <CardHeader className="pb-3 border-b">
              <div className="text-sm font-semibold text-foreground flex items-center gap-2">
                <Package className="h-4 w-4 text-primary" />
                领料备件与耗材消耗核销台账
              </div>
              <CardDescription className="text-xs mt-0.5">
                记录检修过程中实际消耗的机械密封、润滑剂与电气元件，同步扣减 ERP 库存
              </CardDescription>
            </CardHeader>
            <CardContent className="p-0">
              <DataTable
                table={spareTable}
                className="gap-0"
                tableAriaLabel="工单备件与耗材核销台账"
                getHeaderRowProps={() => ({ className: 'bg-muted/20 text-xs hover:bg-muted/20' })}
                getHeaderCellProps={(header) => ({
                  className:
                    header.id === 'code' ? 'w-[150px] font-medium' :
                    header.id === 'specification' ? 'w-[200px] font-medium' :
                    header.id === 'quantity' ? 'w-[90px] text-right font-medium' :
                    header.id === 'lotNumber' ? 'w-[140px] font-medium' :
                    'font-medium',
                })}
                getRowProps={() => ({ className: 'text-xs hover:bg-muted/30' })}
                getCellProps={(cell) => ({
                  className: cell.column.id === 'quantity' ? 'text-right' : undefined,
                })}
                footer={(
                  <DataTablePagination
                    table={spareTable}
                    totalRows={spareParts.length}
                  />
                )}
              />
            </CardContent>
          </Card>

          {/* 现场交接与维修日志记录 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm font-semibold text-foreground">
                现场交接日志与维保备忘记录
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <Textarea
                placeholder="补充现场异常发现、管路状况或交接班注意事项..."
                value={fieldNote}
                onChange={(e) => setFieldNote(e.target.value)}
                className="text-xs min-h-[72px]"
              />
              <div className="flex justify-end">
                <Button size="sm" onClick={handleAddNote} className="h-7 text-xs gap-1">
                  <Send className="h-3 w-3" /> 提交记录
                </Button>
              </div>

              {notesHistory.length > 0 && (
                <div className="space-y-2 pt-2 border-t">
                  {notesHistory.map((note, index) => (
                    <div key={index} className="rounded-md bg-muted/30 p-2.5 text-xs text-foreground space-y-1 border">
                      <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                        <span className="font-medium text-foreground">张志远 (暖通技术员)</span>
                        <span className="font-mono">2026-09-18 13:40</span>
                      </div>
                      <p className="text-muted-foreground leading-relaxed">{note}</p>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>

        {/* 右侧上下文检查器 (4 栏 ~ 33%) */}
        <div className="lg:col-span-4 space-y-6">
          {/* SLA 倒计时与履约状态卡 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3 border-b">
              <div className="flex items-center justify-between text-xs font-medium text-muted-foreground">
                <span className="flex items-center gap-1.5">
                  <Timer className="h-3.5 w-3.5 text-muted-foreground" />
                  SLA 履约时限倒计时
                </span>
                <span className="tabular-nums text-xs text-muted-foreground">到场 20min (达标)</span>
              </div>
            </CardHeader>
            <CardContent className="space-y-3 pt-4">
              <div className="tabular-nums text-2xl font-bold tracking-tight text-foreground">
                剩余 1小时24分
              </div>
              <div className="rounded-lg border bg-muted/20 p-2.5 text-xs space-y-1 text-muted-foreground">
                <div className="flex justify-between">
                  <span>标准 SLA 等级:</span>
                  <span className="font-medium text-foreground">HIGH (6.0h 完成)</span>
                </div>
                <div className="flex justify-between">
                  <span>下发时间:</span>
                  <span className="tabular-nums text-foreground">09-18 09:12</span>
                </div>
                <div className="flex justify-between">
                  <span>到期截止:</span>
                  <span className="tabular-nums text-foreground font-semibold">09-18 15:12</span>
                </div>
              </div>
            </CardContent>
          </Card>

          {/* 责任班组与执行人员 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3 border-b">
              <div className="text-xs font-medium text-muted-foreground flex items-center gap-1.5">
                <User className="h-3.5 w-3.5 text-muted-foreground" />
                责任班组与执行人
              </div>
            </CardHeader>
            <CardContent className="space-y-2 text-xs pt-3">
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">归属班组:</span>
                <span className="font-medium text-foreground">暖通运行抢修一组</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">主责维修人:</span>
                <span className="font-medium text-foreground">张志远 (暖通技术员)</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">协同机修工:</span>
                <span className="font-medium text-foreground">李建军 (机修工)</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">指派调度长:</span>
                <span className="font-medium text-foreground">林值班 (运行长)</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">复核总工:</span>
                <span className="font-medium text-primary">李明 (暖通高级工程师)</span>
              </div>
            </CardContent>
          </Card>

          {/* 关联工程资产 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3 border-b">
              <div className="text-xs font-medium text-muted-foreground flex items-center gap-1.5">
                <Layers className="h-3.5 w-3.5 text-muted-foreground" />
                关联物理资产与安装位置
              </div>
            </CardHeader>
            <CardContent className="space-y-2 text-xs pt-3">
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">目标受控设备:</span>
                <span className="font-mono font-bold text-foreground">VALVE-BYPASS-02</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">关联流体机组:</span>
                <span className="text-foreground"><span className="font-mono">CH-PUMP-02</span> (二次泵)</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">物理安装位置:</span>
                <span className="text-foreground">B2 地下冷冻机房管井</span>
              </div>
            </CardContent>
          </Card>

          {/* 全流程溯源时间线 */}
          <Card className="shadow-xs">
            <CardHeader className="pb-3 border-b">
              <div className="text-xs font-medium text-muted-foreground flex items-center gap-1.5">
                <Clock className="h-3.5 w-3.5 text-muted-foreground" />
                工单流转与处置事实时间线
              </div>
            </CardHeader>
            <CardContent className="pt-4">
              <div className="relative pl-4 space-y-4 border-l border-border/80 text-xs">
                {timeline.map((item) => (
                  <div key={item.id} className="relative">
                    <div
                      className={`absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full border-2 bg-background ${
                        item.status === 'done'
                          ? 'border-emerald-600 bg-emerald-600'
                          : item.status === 'active'
                          ? 'border-amber-500 bg-amber-500'
                          : 'border-muted-foreground'
                      }`}
                    />
                    <div className="flex items-center justify-between text-[11px]">
                      <span className="font-semibold text-foreground">{item.action}</span>
                      <span className="font-mono text-muted-foreground">{item.time}</span>
                    </div>
                    <div className="text-[10px] text-muted-foreground">{item.operator}</div>
                    <div className="text-[11px] text-muted-foreground mt-0.5">{item.detail}</div>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>

          {/* 双人复核归档操作块 */}
          <Card className="shadow-xs bg-muted/20">
            <CardHeader className="pb-2">
              <div className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                <FileCheck2 className="h-4 w-4 text-primary" />
                双人签批归档审核
              </div>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-xs text-muted-foreground leading-relaxed">
                连续运行 30 分钟且功能验证 (MBCx) 测得供回水温差 ΔT ≥ 4.2℃ 后，由值班人员与技术负责人共同签署归档。
              </p>
              <Button className="w-full h-8 text-xs gap-1.5 font-medium">
                <CheckCircle2 className="h-3.5 w-3.5" />
                签署完工归档单据
              </Button>
            </CardContent>
          </Card>
        </div>
      </div>
    </Main>
  );
}
