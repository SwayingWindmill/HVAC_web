import React, { useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  BarChart3,
  CalendarDays,
  CheckCircle2,
  CircleDollarSign,
  CircleGauge,
  ClipboardList,
  Clock3,
  Gauge,
  Lightbulb,
  RefreshCw,
  Search,
  ShieldAlert,
  Target,
  Thermometer,
  TrendingDown,
  UserRound,
  Wrench,
  Zap,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/components/ui/input-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import '@/global.css';

const SITE = '东京中央冷站';

type Fact = {
  label: string;
  value: string;
  detail: string;
  icon: React.ComponentType<{ className?: string }>;
};

function ReviewHeader({ title, description }: { title: string; description: string }) {
  return (
    <header className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <div className="flex items-center gap-2 text-sm text-muted-foreground"><span>{SITE}</span><Badge variant="outline">DESIGN REVIEW</Badge></div>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-1 max-w-4xl text-sm text-muted-foreground">{description}</p>
      </div>
      <Button variant="outline" size="sm"><RefreshCw />刷新视图</Button>
    </header>
  );
}

function FactsStrip({ items, label }: { items: readonly Fact[]; label: string }) {
  return (
    <section className="overflow-hidden rounded-lg border bg-card" aria-label={label}>
      <div className="grid sm:grid-cols-2 xl:grid-cols-4">
        {items.map((item, index) => {
          const Icon = item.icon;
          return (
            <div key={item.label} className={cn('flex min-w-0 gap-3 px-4 py-4', index > 0 && 'border-t sm:border-l sm:border-t-0', index === 2 && 'sm:border-l-0 sm:border-t xl:border-l xl:border-t-0')}>
              <span className="grid size-8 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground"><Icon className="size-4" /></span>
              <div className="min-w-0"><p className="text-xs text-muted-foreground">{item.label}</p><strong className="mt-1 block truncate text-xl font-semibold tracking-tight tabular-nums">{item.value}</strong><p className="mt-1 truncate text-[11px] text-muted-foreground">{item.detail}</p></div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

function MiniLineChart({ primary, secondary, labels, primaryLabel, secondaryLabel }: { primary: number[]; secondary?: number[]; labels: string[]; primaryLabel: string; secondaryLabel?: string }) {
  const width = 720;
  const height = 220;
  const pad = 24;
  const all = secondary ? [...primary, ...secondary] : primary;
  const max = Math.max(...all);
  const min = Math.min(...all);
  const span = Math.max(1, max - min);
  const points = (values: number[]) => values.map((value, index) => {
    const x = pad + ((width - pad * 2) * index) / Math.max(1, values.length - 1);
    const y = pad + ((height - pad * 2) * (max - value)) / span;
    return `${x},${y}`;
  }).join(' ');
  return (
    <div className="min-w-0">
      <div className="mb-3 flex flex-wrap gap-4 text-[11px] text-muted-foreground"><span className="flex items-center gap-1.5"><span className="size-2 rounded-full bg-foreground" />{primaryLabel}</span>{secondary && secondaryLabel ? <span className="flex items-center gap-1.5"><span className="size-2 rounded-full border border-foreground" />{secondaryLabel}</span> : null}</div>
      <svg viewBox={`0 0 ${width} ${height}`} className="h-56 w-full" role="img" aria-label={`${primaryLabel}趋势`}>
        {[0, 1, 2, 3].map((line) => <line key={line} x1={pad} x2={width - pad} y1={pad + ((height - pad * 2) * line) / 3} y2={pad + ((height - pad * 2) * line) / 3} stroke="currentColor" className="text-border" strokeWidth="1" />)}
        {secondary ? <polyline points={points(secondary)} fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="5 5" className="text-muted-foreground" /> : null}
        <polyline points={points(primary)} fill="none" stroke="currentColor" strokeWidth="2.5" className="text-foreground" />
        {primary.map((value, index) => {
          const [x, y] = points(primary).split(' ')[index].split(',');
          return <circle key={`${value}-${index}`} cx={x} cy={y} r="3" fill="currentColor" className="text-foreground" />;
        })}
      </svg>
      <div className="mt-1 grid grid-cols-6 gap-1 text-center text-[10px] text-muted-foreground">{labels.map((label) => <span key={label}>{label}</span>)}</div>
    </div>
  );
}

const workOrders = [
  { id: 'wo-1', title: '冷冻水温差异常现场复核', source: '诊断中心', priority: '紧急', state: '处理中', owner: '李工', sla: '剩余 42 分钟', due: '今天 16:00', next: '核对 CH-02 蒸发器进出水温度', progress: 50, verification: '待验证', blocked: false, overdue: false, evidence: '现场温度复测 + 运行截图', problem: '冷冻水 ΔT 持续高于同负荷基线，需确认传感器偏差还是实际换热变化。' },
  { id: 'wo-2', title: 'CH-011 离线通信排查', source: '设备中心', priority: '高', state: '待处理', owner: '未指派', sla: '已超时 18 分钟', due: '今天 14:30', next: '确认网关在线与 BACnet 路由', progress: 0, verification: '待验证', blocked: false, overdue: true, evidence: '尚无现场证据', problem: 'CH-011 已离线 26 分钟，当前遥测不可用。' },
  { id: 'wo-3', title: '冷却水泵振动趋势复核', source: '设备巡检', priority: '中', state: '处理中', owner: '王工', sla: '剩余 3 小时', due: '今天 18:00', next: '补采轴承端振动 RMS', progress: 33, verification: '待验证', blocked: true, overdue: false, evidence: '已有 2 组手持仪测点', problem: '巡检发现振动升高，但固定测点不足以判定机械异常。' },
  { id: 'wo-4', title: 'AHU-07 过滤网压差检查', source: '告警中心', priority: '高', state: '待处理', owner: '赵工', sla: '剩余 1 小时', due: '今天 16:30', next: '现场读取过滤器前后压差', progress: 0, verification: '待验证', blocked: false, overdue: false, evidence: '告警触发窗口 14:10–14:26', problem: 'AHU-07 过滤网压差连续高于阈值，需要现场确认是否堵塞。' },
  { id: 'wo-5', title: '冷机房月度维护', source: '计划任务', priority: '低', state: '待处理', owner: '运维一组', sla: '2 天', due: '09/15 18:00', next: '执行月度维护清单', progress: 0, verification: '无需问题验证', blocked: false, overdue: false, evidence: '标准维护模板 v4', problem: '计划性维护，不来自异常。' },
  { id: 'wo-6', title: 'CH-03 故障复位后验证', source: '告警中心', priority: '紧急', state: '已完成', owner: '周工', sla: '按时完成', due: '今天 13:00', next: '复核 2 小时稳定运行证据', progress: 100, verification: '验证中', blocked: false, overdue: false, evidence: '复位记录 + 90 分钟稳定运行曲线', problem: 'CH-03 保护停机后已完成现场处理，但仍需验证是否真正恢复稳定。' },
] as const;

function WorkOrdersReview() {
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState('all');
  const [selectedId, setSelectedId] = useState(workOrders[0].id);
  const rows = useMemo(() => workOrders.filter((row) => {
    const matches = !search || [row.title, row.source, row.owner, row.next].join(' ').toLowerCase().includes(search.toLowerCase());
    return matches && (status === 'all' || row.state === status);
  }), [search, status]);
  const selected = workOrders.find((row) => row.id === selectedId) ?? workOrders[0];
  const facts: Fact[] = [
    { label: '紧急 / 高优先级', value: '4', detail: '需要本班次关注', icon: AlertTriangle },
    { label: '已超时', value: '1', detail: 'CH-011 离线排查', icon: Clock3 },
    { label: '未指派', value: '1', detail: '需要建立责任人', icon: UserRound },
    { label: '待验证完成', value: '1', detail: '完成 ≠ 已验证解决', icon: CheckCircle2 },
  ];
  return (
    <main className="mx-auto flex w-full max-w-[1750px] flex-col gap-5" data-testid="work-orders-review">
      <ReviewHeader title="工单中心" description="把问题转化为有责任、有 SLA、有执行证据和验证结果的工作；完成状态与问题是否真正解决保持分离。" />
      <FactsStrip items={facts} label="工单事实" />
      <div className="grid min-w-0 items-start gap-4 xl:grid-cols-[minmax(0,1.45fr)_380px]">
        <Card className="min-w-0 gap-0 py-0 shadow-none">
          <CardHeader className="border-b py-4"><CardTitle>工单台账</CardTitle><CardDescription>优先看下一动作、SLA 和责任，不暴露内部流程字段。</CardDescription></CardHeader>
          <CardContent className="p-0">
            <div className="flex flex-wrap gap-2 border-b p-3">
              <InputGroup className="min-w-72 flex-1 lg:max-w-md"><InputGroupInput placeholder="搜索工单 / 来源 / 责任人 / 下一动作" value={search} onChange={(event) => setSearch(event.currentTarget.value)} /><InputGroupAddon align="inline-start"><Search /></InputGroupAddon><InputGroupAddon align="inline-end"><InputGroupText>{rows.length} 条</InputGroupText></InputGroupAddon></InputGroup>
              <Select value={status} onValueChange={setStatus}><SelectTrigger className="w-36"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部状态</SelectItem><SelectItem value="待处理">待处理</SelectItem><SelectItem value="处理中">处理中</SelectItem><SelectItem value="已完成">已完成</SelectItem></SelectContent></Select>
            </div>
            <Table aria-label="工单台账"><TableHeader><TableRow><TableHead>工作</TableHead><TableHead>来源</TableHead><TableHead>优先级</TableHead><TableHead>状态</TableHead><TableHead>负责人</TableHead><TableHead>SLA</TableHead><TableHead>下一动作</TableHead><TableHead>截止</TableHead></TableRow></TableHeader><TableBody>{rows.map((row) => <TableRow key={row.id} className={cn('cursor-pointer', selected.id === row.id && 'bg-muted/60')} onClick={() => setSelectedId(row.id)}><TableCell className="max-w-56"><strong className="block truncate text-xs font-medium">{row.title}</strong>{row.blocked ? <span className="mt-1 inline-flex text-[10px] text-warning">等待外部条件</span> : null}</TableCell><TableCell className="text-xs text-muted-foreground">{row.source}</TableCell><TableCell><Badge variant={row.priority === '紧急' ? 'destructive' : 'outline'}>{row.priority}</Badge></TableCell><TableCell className="text-xs">{row.state}</TableCell><TableCell className={cn('text-xs', row.owner === '未指派' && 'font-medium text-destructive')}>{row.owner}</TableCell><TableCell className={cn('whitespace-nowrap text-xs', row.overdue && 'font-medium text-destructive')}>{row.sla}</TableCell><TableCell className="max-w-64 text-xs">{row.next}</TableCell><TableCell className="whitespace-nowrap text-xs text-muted-foreground">{row.due}</TableCell></TableRow>)}</TableBody></Table>
          </CardContent>
        </Card>
        <aside className="rounded-lg border bg-card p-4" aria-label="工单上下文">
          <div className="flex items-start justify-between gap-3"><div><p className="text-[11px] text-muted-foreground">{selected.source}</p><h2 className="mt-1 text-base font-semibold">{selected.title}</h2></div><Badge variant={selected.priority === '紧急' ? 'destructive' : 'outline'}>{selected.priority}</Badge></div>
          <div className="mt-4 space-y-4 text-xs">
            <section><span className="text-[11px] text-muted-foreground">问题陈述</span><p className="mt-1 leading-5">{selected.problem}</p></section>
            <section className="grid grid-cols-2 overflow-hidden rounded-md border"><div className="p-3"><span className="text-[10px] text-muted-foreground">负责人</span><strong className="mt-1 block">{selected.owner}</strong></div><div className="border-l p-3"><span className="text-[10px] text-muted-foreground">SLA</span><strong className={cn('mt-1 block', selected.overdue && 'text-destructive')}>{selected.sla}</strong></div></section>
            <section><div className="flex justify-between"><span className="text-muted-foreground">任务进度</span><strong className="tabular-nums">{selected.progress}%</strong></div><div className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full bg-foreground" style={{ width: `${selected.progress}%` }} /></div></section>
            <section><span className="text-[11px] text-muted-foreground">下一动作</span><strong className="mt-1 block font-medium">{selected.next}</strong></section>
            <section><span className="text-[11px] text-muted-foreground">执行证据</span><p className="mt-1 leading-5">{selected.evidence}</p></section>
            <section className="rounded-md border bg-muted/20 p-3"><span className="text-[11px] text-muted-foreground">问题验证</span><strong className="mt-1 block">{selected.verification}</strong><p className="mt-1 text-[11px] leading-4 text-muted-foreground">工单完成不会改变告警的物理 ACTIVE / CLEARED 事实。</p></section>
            <Button className="w-full" variant="outline"><ClipboardList />打开完整工单详情</Button>
          </div>
        </aside>
      </div>
    </main>
  );
}

const energyLoad = [410, 438, 486, 552, 618, 702, 768, 822, 790, 724, 650, 598];
const energyBaseline = [430, 460, 500, 570, 650, 730, 800, 860, 830, 770, 700, 640];
const energyLabels = ['00', '02', '04', '06', '08', '10', '12', '14', '16', '18', '20', '22'];
const energyBreakdown = [
  ['冷水机组', 4286, 49], ['冷冻水泵', 1508, 17], ['冷却水泵', 932, 11], ['冷却塔', 780, 9], ['AHU / 末端', 1220, 14],
] as const;

function EnergyReview() {
  const [range, setRange] = useState('today');
  const [comparison, setComparison] = useState('baseline');
  const facts: Fact[] = [
    { label: '期间用电', value: '8,726 kWh', detail: '00:00–当前', icon: Zap },
    { label: '峰值需量', value: '822 kW', detail: '14:00 出现', icon: Activity },
    { label: '相对基线', value: '-7.8%', detail: '基线是命名参考，不是实际值', icon: TrendingDown },
    { label: '异常负荷区间', value: '2', detail: '需要继续下钻贡献者', icon: AlertTriangle },
  ];
  return (
    <main className="mx-auto flex w-full max-w-[1700px] flex-col gap-5" data-testid="energy-review">
      <ReviewHeader title="能源分析" description="一个连续分析工作区：从站点聚合用能下钻到系统贡献者和异常时段，同时保持时间与比较上下文。" />
      <div className="flex flex-wrap gap-2 rounded-lg border bg-card p-3"><Select value={range} onValueChange={setRange}><SelectTrigger className="w-36"><CalendarDays /><SelectValue /></SelectTrigger><SelectContent><SelectItem value="today">今天</SelectItem><SelectItem value="week">最近 7 天</SelectItem><SelectItem value="month">本月</SelectItem></SelectContent></Select><Select value={comparison} onValueChange={setComparison}><SelectTrigger className="w-44"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="baseline">对比：运行基线</SelectItem><SelectItem value="previous">对比：上一周期</SelectItem><SelectItem value="none">不对比</SelectItem></SelectContent></Select><Badge variant="outline">能源类型：电</Badge><Badge variant="secondary">费用 / 水 / 气：未接入</Badge></div>
      <FactsStrip items={facts} label="能源事实" />
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.5fr)_minmax(360px,.75fr)]">
        <Card className="shadow-none"><CardHeader><CardTitle>站点负荷曲线</CardTitle><CardDescription>实际负荷与命名比较参考保持语义分离。</CardDescription></CardHeader><CardContent><MiniLineChart primary={energyLoad} secondary={comparison === 'none' ? undefined : energyBaseline} labels={energyLabels} primaryLabel="实际功率 kW" secondaryLabel={comparison === 'baseline' ? '运行基线 kW' : '上一周期 kW'} /></CardContent></Card>
        <Card className="shadow-none"><CardHeader><CardTitle>主要贡献者</CardTitle><CardDescription>当前期间电能分解</CardDescription></CardHeader><CardContent className="space-y-4">{energyBreakdown.map(([label, kwh, percent]) => <div key={label}><div className="flex items-center justify-between gap-3 text-xs"><span>{label}</span><strong className="tabular-nums">{kwh.toLocaleString()} kWh · {percent}%</strong></div><div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full bg-foreground" style={{ width: `${percent}%` }} /></div></div>)}</CardContent></Card>
      </div>
      <div className="grid gap-4 xl:grid-cols-2">
        <Card className="shadow-none"><CardHeader><CardTitle>偏差与异常时段</CardTitle><CardDescription>把异常定位到时间，而不是只给一个总量。</CardDescription></CardHeader><CardContent className="p-0"><Table><TableHeader><TableRow><TableHead>时段</TableHead><TableHead>实际</TableHead><TableHead>参考</TableHead><TableHead>偏差</TableHead><TableHead>主要贡献者</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell>09:40–10:20</TableCell><TableCell>735 kW</TableCell><TableCell>682 kW</TableCell><TableCell><Badge variant="outline">+7.8%</Badge></TableCell><TableCell>冷水机组</TableCell></TableRow><TableRow><TableCell>14:10–14:45</TableCell><TableCell>822 kW</TableCell><TableCell>760 kW</TableCell><TableCell><Badge variant="outline">+8.2%</Badge></TableCell><TableCell>冷水机组 + 冷却侧</TableCell></TableRow></TableBody></Table></CardContent></Card>
        <Card className="shadow-none"><CardHeader><CardTitle>相关运行事件</CardTitle><CardDescription>只展示有时间关联的权威事件，不宣称因果。</CardDescription></CardHeader><CardContent className="space-y-3 text-xs"><div className="rounded-md border p-3"><strong>09:52 · CH-03 负荷切换</strong><p className="mt-1 text-muted-foreground">发生在第一个偏差窗口内；是否导致能耗变化仍需结合效率分析。</p></div><div className="rounded-md border p-3"><strong>14:18 · 冷却水供水温度上升</strong><p className="mt-1 text-muted-foreground">与峰值需量时间重叠，可继续进入效率分析验证关系。</p></div><Button variant="outline" className="w-full">进入效率分析 <ArrowRight /></Button></CardContent></Card>
      </div>
    </main>
  );
}

const subsystem = [
  ['冷源系统', '5.72 COP', '较基线 -4.1%', '需关注'],
  ['冷冻水输配', '0.082 kW/RT', '较基线 +1.2%', '稳定'],
  ['冷却侧', '4.1 K approach', '较基线 +0.8 K', '需关注'],
  ['末端系统', '5.6 K ΔT', '较基线 -0.2 K', '稳定'],
] as const;
const equipmentRank = [
  ['CH-01', '5.94', '420 RT', '正常'], ['CH-02', '5.81', '388 RT', '正常'], ['CH-03', '4.92', '356 RT', '偏低'], ['CH-04', '—', '—', '未运行'],
] as const;

function EfficiencyReview() {
  const [system, setSystem] = useState('plant');
  const efficiency = [5.9, 5.8, 5.7, 5.6, 5.65, 5.58, 5.5, 5.42, 5.48, 5.38, 5.44, 5.5];
  const facts: Fact[] = [
    { label: '系统 COP', value: '5.50', detail: '制冷量 / 主机电功率', icon: CircleGauge },
    { label: '站房效率', value: '0.91 kW/RT', detail: '全站电功率 / 制冷量', icon: Gauge },
    { label: '冷冻水 ΔT', value: '5.6 K', detail: '回水 - 供水', icon: Thermometer },
    { label: '冷却塔逼近度', value: '4.1 K', detail: '出塔水温 - 湿球温度', icon: Activity },
  ];
  return (
    <main className="mx-auto flex w-full max-w-[1700px] flex-col gap-5" data-testid="efficiency-review">
      <ReviewHeader title="效率分析" description="把效率变化放回负荷、天气和运行模式上下文中解释，不制造综合效率分数。" />
      <div className="flex flex-wrap gap-2 rounded-lg border bg-card p-3"><Select value={system} onValueChange={setSystem}><SelectTrigger className="w-44"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="plant">系统：全站</SelectItem><SelectItem value="chiller">系统：冷源</SelectItem><SelectItem value="distribution">系统：输配</SelectItem></SelectContent></Select><Badge variant="outline">时间：今天</Badge><Badge variant="outline">归一化：原始值</Badge></div>
      <FactsStrip items={facts} label="效率事实" />
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.45fr)_minmax(380px,.8fr)]"><Card className="shadow-none"><CardHeader><CardTitle>系统效率趋势</CardTitle><CardDescription>COP，分母定义明确，不与“健康分”混合。</CardDescription></CardHeader><CardContent><MiniLineChart primary={efficiency} labels={energyLabels} primaryLabel="系统 COP" /></CardContent></Card><Card className="shadow-none"><CardHeader><CardTitle>子系统比较</CardTitle><CardDescription>只显示具备有效输入的指标。</CardDescription></CardHeader><CardContent className="space-y-3">{subsystem.map(([name, metric, variance, status]) => <div key={name} className="rounded-md border p-3"><div className="flex justify-between gap-3"><strong className="text-xs">{name}</strong><Badge variant={status === '需关注' ? 'outline' : 'secondary'}>{status}</Badge></div><div className="mt-2 flex items-end justify-between gap-3"><span className="text-lg font-semibold tabular-nums">{metric}</span><span className="text-[11px] text-muted-foreground">{variance}</span></div></div>)}</CardContent></Card></div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(420px,.85fr)]"><Card className="shadow-none"><CardHeader><CardTitle>设备效率排名</CardTitle><CardDescription>按相同时间窗口与相同口径比较。</CardDescription></CardHeader><CardContent className="p-0"><Table><TableHeader><TableRow><TableHead>设备</TableHead><TableHead>COP</TableHead><TableHead>当前负荷</TableHead><TableHead>判断</TableHead><TableHead /></TableRow></TableHeader><TableBody>{equipmentRank.map(([name, cop, load, status]) => <TableRow key={name}><TableCell className="font-medium">{name}</TableCell><TableCell>{cop}</TableCell><TableCell>{load}</TableCell><TableCell><Badge variant={status === '偏低' ? 'destructive' : 'outline'}>{status}</Badge></TableCell><TableCell><Button variant="ghost" size="sm">设备详情</Button></TableCell></TableRow>)}</TableBody></Table></CardContent></Card><Card className="shadow-none"><CardHeader><CardTitle>工程关系</CardTitle><CardDescription>负荷与效率关系，用于区分低负荷效应和真实效率下降。</CardDescription></CardHeader><CardContent><div className="relative h-64 rounded-md border bg-muted/10"><span className="absolute bottom-2 left-1/2 -translate-x-1/2 text-[10px] text-muted-foreground">负荷率 %</span><span className="absolute left-2 top-1/2 -rotate-90 text-[10px] text-muted-foreground">COP</span>{[[18,66],[28,52],[36,44],[48,37],[58,31],[67,28],[75,34],[84,41],[91,47],[62,55],[72,62]].map(([x,y], index) => <span key={index} className={cn('absolute size-3 rounded-full border border-background', index === 6 ? 'bg-destructive' : 'bg-foreground')} style={{ left: `${x}%`, bottom: `${y}%` }} />)}<div className="absolute right-3 top-3 rounded-md border bg-background/90 px-2 py-1 text-[10px] text-muted-foreground">CH-03 为当前异常点</div></div></CardContent></Card></div>
    </main>
  );
}

const opportunities = [
  { id: 'opp-1', title: '冷冻水供水温度重置', system: '冷源系统', evidence: '末端阀门裕量 + 负荷 + 供水温度', benefit: '320 kWh/日', risk: '低', status: '待评审', next: '工程复核约束', basis: '基于过去 14 天同负荷窗口，模拟 CHWS 从 7.0°C 提高至 7.6°C。', affected: '冷水机组群、二次泵、末端阀门', constraints: '末端最大阀位 < 85%；室内舒适度无恶化；禁止在除湿需求高时启用。', proposed: '将供水设定值分阶段提高 0.3°C，每阶段观察 30 分钟。' },
  { id: 'opp-2', title: '冷却塔风机群控优化', system: '冷却侧', evidence: '湿球 + 逼近度 + 风机功率', benefit: '180 kWh/日', risk: '中', status: '待评审', next: '验证湿球传感器', basis: '按当前逼近度与湿球预测，在 13:00–18:00 重新分配塔风机频率。', affected: 'CT-01~03、冷却水泵', constraints: '冷凝压力上限；湿球质量必须 GOOD；低负荷避免频繁启停。', proposed: '优先并联低频运行 3 台塔风机，而非 1 台高频运行。' },
  { id: 'opp-3', title: '夜间预冷策略收缩', system: '末端系统', evidence: '夜间负荷 + 室外温度 + 启动时刻', benefit: '260 kWh/日', risk: '低', status: '已评审', next: '创建优化方案', basis: '过去 10 个工作日中，05:00–05:40 负荷低于预冷需求边界。', affected: 'AHU 群、冷源系统', constraints: '工作日 08:00 前舒适度必须达标；极端天气禁用。', proposed: '把预冷起始时间从 05:00 延后至 05:30，并保留天气例外。' },
  { id: 'opp-4', title: '冷冻水泵压差重置', system: '输配系统', evidence: '阀位分布 + 压差 + 泵功率', benefit: '145 kWh/日', risk: '中', status: '证据不足', next: '补采末端阀位', basis: '当前仅有 62% 末端阀位覆盖率，不足以确认安全重置边界。', affected: 'CHWP-01~03、末端控制阀', constraints: '关键末端阀位覆盖需 ≥ 90%；压差下限必须保留。', proposed: '暂不形成可执行建议，先补齐末端阀位数据。' },
] as const;

function OpportunitiesReview() {
  const [search, setSearch] = useState('');
  const [risk, setRisk] = useState('all');
  const [selectedId, setSelectedId] = useState(opportunities[0].id);
  const rows = opportunities.filter((row) => (!search || [row.title, row.system, row.evidence].join(' ').includes(search)) && (risk === 'all' || row.risk === risk));
  const selected = opportunities.find((row) => row.id === selectedId) ?? opportunities[0];
  const facts: Fact[] = [
    { label: '有证据机会', value: '3', detail: '1 条仍需补证据', icon: Lightbulb },
    { label: '预计节电', value: '760 kWh/日', detail: '仅统计有计算依据的机会', icon: Zap },
    { label: '待工程评审', value: '2', detail: '尚未成为控制动作', icon: Wrench },
    { label: '可进入方案', value: '1', detail: '已完成初步工程评审', icon: Target },
  ];
  return (
    <main className="mx-auto flex w-full max-w-[1750px] flex-col gap-5" data-testid="opportunities-review">
      <ReviewHeader title="节能机会" description="按证据、收益、风险和下一动作排序机会；Opportunity 不是已批准控制命令。" />
      <FactsStrip items={facts} label="节能机会事实" />
      <div className="grid min-w-0 items-start gap-4 xl:grid-cols-[minmax(0,1.45fr)_400px]">
        <Card className="min-w-0 gap-0 py-0 shadow-none"><CardHeader className="border-b py-4"><CardTitle>机会台账</CardTitle><CardDescription>优先比较预计收益、风险和证据状态。</CardDescription></CardHeader><CardContent className="p-0"><div className="flex flex-wrap gap-2 border-b p-3"><InputGroup className="min-w-72 flex-1 lg:max-w-md"><InputGroupInput placeholder="搜索机会 / 系统 / 证据" value={search} onChange={(event) => setSearch(event.currentTarget.value)} /><InputGroupAddon align="inline-start"><Search /></InputGroupAddon><InputGroupAddon align="inline-end"><InputGroupText>{rows.length} 条</InputGroupText></InputGroupAddon></InputGroup><Select value={risk} onValueChange={setRisk}><SelectTrigger className="w-36"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部风险</SelectItem><SelectItem value="低">低风险</SelectItem><SelectItem value="中">中风险</SelectItem></SelectContent></Select></div><Table><TableHeader><TableRow><TableHead>机会</TableHead><TableHead>证据范围</TableHead><TableHead>预计收益</TableHead><TableHead>风险</TableHead><TableHead>状态</TableHead><TableHead>下一动作</TableHead></TableRow></TableHeader><TableBody>{rows.map((row) => <TableRow key={row.id} className={cn('cursor-pointer', row.id === selected.id && 'bg-muted/60')} onClick={() => setSelectedId(row.id)}><TableCell><strong className="block text-xs">{row.title}</strong><span className="text-[11px] text-muted-foreground">{row.system}</span></TableCell><TableCell className="max-w-64 text-xs text-muted-foreground">{row.evidence}</TableCell><TableCell className="whitespace-nowrap text-xs font-medium tabular-nums">{row.status === '证据不足' ? '—' : row.benefit}</TableCell><TableCell><Badge variant="outline">{row.risk}</Badge></TableCell><TableCell><Badge variant={row.status === '证据不足' ? 'secondary' : 'outline'}>{row.status}</Badge></TableCell><TableCell className="text-xs">{row.next}</TableCell></TableRow>)}</TableBody></Table></CardContent></Card>
        <aside className="rounded-lg border bg-card p-4" aria-label="节能机会上下文"><div className="flex items-start justify-between gap-3"><div><p className="text-[11px] text-muted-foreground">{selected.system}</p><h2 className="mt-1 text-base font-semibold">{selected.title}</h2></div><Badge variant="outline">风险 {selected.risk}</Badge></div><div className="mt-4 space-y-4 text-xs"><section><span className="text-[11px] text-muted-foreground">已验证证据</span><p className="mt-1 leading-5">{selected.evidence}</p></section><section className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">计算依据</span><p className="mt-1 leading-5">{selected.basis}</p>{selected.status !== '证据不足' ? <strong className="mt-2 block text-lg tabular-nums">{selected.benefit}</strong> : <Badge className="mt-2" variant="secondary">证据不足，不展示预计收益</Badge>}</section><section><span className="text-[11px] text-muted-foreground">影响对象</span><p className="mt-1">{selected.affected}</p></section><section><span className="text-[11px] text-muted-foreground">约束 / 风险</span><p className="mt-1 leading-5">{selected.constraints}</p></section><section className="rounded-md border bg-muted/20 p-3"><span className="text-[11px] text-muted-foreground">建议变化（不是当前控制状态）</span><strong className="mt-1 block leading-5">{selected.proposed}</strong></section><section><span className="text-[11px] text-muted-foreground">评审历史</span><p className="mt-1">今日 14:20 · 系统生成机会；尚无已批准控制动作。</p></section><Button className="w-full" disabled={selected.status === '证据不足'}>创建优化方案 <ArrowRight /></Button></div></aside>
      </div>
    </main>
  );
}

function App() {
  const page = new URLSearchParams(location.search).get('page') ?? 'work-orders';
  const content = page === 'energy' ? <EnergyReview /> : page === 'efficiency' ? <EfficiencyReview /> : page === 'opportunities' ? <OpportunitiesReview /> : <WorkOrdersReview />;
  return <div className="min-h-screen bg-background p-6 text-foreground">{content}</div>;
}

createRoot(document.getElementById('root')!).render(<React.StrictMode><App /></React.StrictMode>);
