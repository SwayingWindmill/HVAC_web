import {
  Activity,
  ArrowLeft,
  Download,
  Gauge,
  RefreshCw,
  ShieldCheck,
  Snowflake,
  Zap,
} from 'lucide-react';
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { Main } from '@/components/layout/Main';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';

export interface DeviceDetailConsoleProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime?: unknown;
  readonly deviceId?: string;
  readonly onBack?: () => void;
}

const THERMAL_PARAMETER_ROWS = [
  { name: '蒸发饱和压力', val: '348 kPa', limit: '320 - 420 kPa', ok: true },
  { name: '冷凝饱和压力', val: '892 kPa', limit: '750 - 1100 kPa', ok: true },
  { name: '压缩机导叶开度', val: '72.4 %', limit: '15 - 100 %', ok: true },
  { name: '润滑油温度', val: '52.8 °C', limit: '45 - 65 °C', ok: true },
  { name: '润滑油压差', val: '185 kPa', limit: '> 140 kPa', ok: true },
  { name: '压缩机排气温度', val: '76.2 °C', limit: '< 95 °C', ok: true },
] as const;

const CHILLER_24H_TELEMETRY = Array.from({ length: 24 }, (_, i) => {
  const hour = `${String(i).padStart(2, '0')}:00`;
  const loadFactor = i >= 8 && i <= 18 ? 0.78 + Math.sin((i - 8) / 3) * 0.1 : 0.45;
  const power = Math.round(320 * loadFactor);
  const coolingRT = Number((power * 1.88).toFixed(1));
  const cop = Number((coolingRT * 3.517 / power).toFixed(2));
  const supplyTemp = Number((7.0 + Math.sin(i) * 0.15).toFixed(2));
  const returnTemp = Number((7.0 + 5.3 * loadFactor).toFixed(2));

  return {
    hour,
    power,
    coolingRT,
    cop,
    supplyTemp,
    returnTemp,
  };
});

export function DeviceDetailConsole({ onBack }: DeviceDetailConsoleProps) {
  return (
    <Main className="space-y-6" data-testid="device-detail-console">
      {/* Top Breadcrumb & Actions */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          {onBack && (
            <Button variant="outline" size="sm" onClick={onBack} className="h-8 gap-1 text-xs">
              <ArrowLeft className="size-3.5" />
              返回设备
            </Button>
          )}
          <div>
            <div className="flex items-center gap-2.5">
              <h1 className="text-2xl font-bold tracking-tight text-foreground">
                1# 变频离心冷水机组 (CH-01)
              </h1>
              <StatusBadge tone="success">
                群控闭环运行中
              </StatusBadge>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              资产编码：EQ-CH-2026-001 · 安装位置：中央冷站 B1 机房 · 额定制冷量：600 RT (2,110 kW)
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs">
            <Download className="size-3.5" />
            导出遥测快照
          </Button>
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs">
            <RefreshCw className="size-3.5" />
            刷新
          </Button>
        </div>
      </div>

      {/* 4 Key Realtime Parameters Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">实时轴功率</CardTitle>
            <Zap className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">240.5</span>
              <span className="text-xs text-muted-foreground">kW</span>
            </div>
            <div className="text-xs text-muted-foreground">
              额定功率 320 kW · 负荷率 <strong className="tabular-nums text-foreground">75.2%</strong>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">输出制冷量与 COP</CardTitle>
            <Gauge className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">452.8</span>
              <span className="text-xs text-muted-foreground">RT</span>
              <span className="ml-auto text-xs tabular-nums font-medium text-foreground">COP 6.62</span>
            </div>
            <div className="text-xs text-muted-foreground">
              高效运行工况 · 达一级能效标准
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">蒸发器冷水出水温</CardTitle>
            <Snowflake className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">7.0</span>
              <span className="text-xs text-muted-foreground">°C</span>
              <span className="ml-auto text-xs text-muted-foreground">设定: 7.0 °C</span>
            </div>
            <div className="text-xs text-muted-foreground">
              回水 12.3 °C · 换热温差 <strong className="tabular-nums text-foreground">5.3 K</strong>
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">冷凝器冷却进水温</CardTitle>
            <Activity className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">29.4</span>
              <span className="text-xs text-muted-foreground">°C</span>
              <span className="ml-auto text-xs text-muted-foreground">逼近度 2.8 K</span>
            </div>
            <div className="text-xs text-muted-foreground">
              出水 34.2 °C · 温差 <strong className="tabular-nums text-foreground">4.8 K</strong>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 24h Machine Operational Telemetry Profile */}
      <Card className="shadow-xs">
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-base font-semibold">24小时工况遥测曲线</CardTitle>
          </div>
          <div className="flex items-center gap-3 text-xs">
            <div className="flex items-center gap-1.5">
              <span className="size-2.5 rounded-full bg-blue-600" />
              <span className="text-muted-foreground">主机轴功率 (kW)</span>
            </div>
            <div className="flex items-center gap-1.5">
              <span className="size-2.5 rounded-full bg-emerald-500" />
              <span className="text-muted-foreground">综合 COP</span>
            </div>
          </div>
        </CardHeader>
        <CardContent className="pt-4">
          <div className="h-[300px] w-full">
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={CHILLER_24H_TELEMETRY} margin={{ top: 10, right: 10, left: -15, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#e4e4e7" opacity={0.6} />
                <XAxis dataKey="hour" tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} />
                <YAxis yAxisId="left" tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} domain={[0, 350]} />
                <YAxis yAxisId="right" orientation="right" tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} domain={[4, 8]} />
                <Tooltip
                  content={({ active, payload, label }) => {
                    if (!active || !payload || !payload.length) return null;
                    const d = payload[0].payload;
                    return (
                      <div className="rounded-lg border bg-popover p-2.5 text-xs shadow-md space-y-1">
                        <div className="font-semibold text-foreground border-b pb-1">{label} 主机工况</div>
                        <div className="flex justify-between gap-4"><span>功率:</span><span className="tabular-nums font-medium">{d.power} kW</span></div>
                        <div className="flex justify-between gap-4"><span>制冷量:</span><span className="tabular-nums font-medium">{d.coolingRT} RT</span></div>
                        <div className="flex justify-between gap-4 text-emerald-600"><span>COP:</span><span className="tabular-nums font-semibold">{d.cop}</span></div>
                        <div className="flex justify-between gap-4"><span>供/回水温:</span><span className="tabular-nums">{d.supplyTemp}°C / {d.returnTemp}°C</span></div>
                      </div>
                    );
                  }}
                />
                <Area yAxisId="left" type="monotone" dataKey="power" stroke="#2563eb" fill="#2563eb" fillOpacity={0.12} strokeWidth={2} name="主机功率" />
                <Line yAxisId="right" type="monotone" dataKey="cop" stroke="#10b981" strokeWidth={2} dot={false} name="能效 COP" />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </CardContent>
      </Card>

      {/* Internal Subsystems & Nameplate Specifications */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* Operating Envelope Parameters */}
        <Card className="gap-0 py-0 shadow-xs">
          <CardHeader className="border-b py-4">
            <CardTitle className="text-base font-semibold">核心热力学运行参数</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <Table aria-label="设备核心热力学运行参数">
              <TableHeader className="bg-muted/20">
                <TableRow className="hover:bg-transparent">
                  <TableHead>物理参数</TableHead>
                  <TableHead className="text-right">当前实测值</TableHead>
                  <TableHead className="text-right">设计安全区间</TableHead>
                  <TableHead className="text-center">工况状态</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {THERMAL_PARAMETER_ROWS.map((row) => (
                  <TableRow key={row.name}>
                    <TableCell className="font-medium text-foreground">{row.name}</TableCell>
                    <TableCell className="text-right font-mono font-semibold tabular-nums text-foreground">{row.val}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums text-muted-foreground">{row.limit}</TableCell>
                    <TableCell className="text-center"><StatusBadge tone="success">正常</StatusBadge></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        {/* Nameplate & Lifecycle Specs */}
        <Card className="shadow-xs">
          <CardHeader className="pb-3">
            <CardTitle className="text-base font-semibold">设备信息</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="grid grid-cols-2 gap-3 text-xs">
              <div className="rounded-lg border bg-muted/20 p-3 space-y-1">
                <span className="text-muted-foreground">设备型号与品牌</span>
                <p className="font-semibold text-foreground">Trane 变频双级离心式冷机</p>
              </div>
              <div className="rounded-lg border bg-muted/20 p-3 space-y-1">
                <span className="text-muted-foreground">制冷工质 / 充注量</span>
                <p className="font-semibold text-foreground tabular-nums">R134a / 280 kg</p>
              </div>
              <div className="rounded-lg border bg-muted/20 p-3 space-y-1">
                <span className="text-muted-foreground">累计运行时间</span>
                <p className="font-semibold text-foreground tabular-nums">14,280 小时</p>
              </div>
              <div className="rounded-lg border bg-muted/20 p-3 space-y-1">
                <span className="text-muted-foreground">上次保养时间</span>
                <p className="font-semibold text-foreground">2026-08-15 (由开利维保)</p>
              </div>
            </div>

            <div className="rounded-lg border bg-muted/40 p-3 text-xs space-y-1">
              <div className="flex items-center gap-1.5 font-semibold text-foreground">
                <ShieldCheck className="size-4 text-muted-foreground" />
                <span>综合机组健康度：98 / 100 分</span>
              </div>
              <p className="text-muted-foreground leading-relaxed">
                机组各机械轴系振动、油质清洁度、蒸发/冷凝换热端无明显污垢衰减，预计下次计划保养在 <strong>2026年11月</strong>。
              </p>
            </div>
          </CardContent>
        </Card>
      </div>
    </Main>
  );
}
