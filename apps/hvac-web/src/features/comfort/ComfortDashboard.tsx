import {
  Droplets,
  RefreshCw,
  Smile,
  Thermometer,
  Wind,
} from 'lucide-react';
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';

export interface ComfortDashboardProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime?: unknown;
  readonly searchState?: unknown;
  readonly onSearchChange?: (patch: unknown) => void;
}

interface ZoneData {
  readonly id: string;
  readonly name: string;
  readonly floor: string;
  readonly temp: number;
  readonly humidity: number;
  readonly co2: number;
  readonly pm25: number;
  readonly status: 'comfort' | 'cool' | 'warm';
  readonly statusLabel: string;
  readonly recommendation?: string;
}

const ZONES: readonly ZoneData[] = [
  { id: '1f-a', name: '1F 开放办公区 A', floor: '1F', temp: 23.8, humidity: 50.2, co2: 480, pm25: 11, status: 'comfort', statusLabel: '高度舒适' },
  { id: '1f-b', name: '1F 开放办公区 B', floor: '1F', temp: 24.5, humidity: 52.1, co2: 520, pm25: 12, status: 'comfort', statusLabel: '高度舒适' },
  { id: '2f-conf', name: '2F 核心会议室 201', floor: '2F', temp: 23.2, humidity: 48.5, co2: 610, pm25: 14, status: 'comfort', statusLabel: '舒适 · 会议中' },
  { id: '2f-lab', name: '2F 核心研发实验室', floor: '2F', temp: 22.8, humidity: 45.0, co2: 450, pm25: 8, status: 'cool', statusLabel: '轻度偏凉 (可调升)', recommendation: '建议提高送风温度 0.8°C，可降低末端能耗 3.5%' },
  { id: '3f-exec', name: '3F 总裁办公室', floor: '3F', temp: 24.0, humidity: 51.0, co2: 490, pm25: 9, status: 'comfort', statusLabel: '高度舒适' },
  { id: '3f-act', name: '3F 员工休闲与健身区', floor: '3F', temp: 25.1, humidity: 55.4, co2: 580, pm25: 15, status: 'comfort', statusLabel: '舒适' },
];

const ZONE_CHART_DATA = ZONES.map((z) => ({
  name: z.name.replace(/^[0-9A-Z]+ /, ''),
  temp: z.temp,
  humidity: z.humidity,
  co2: z.co2,
}));

export function ComfortDashboard({ site }: ComfortDashboardProps) {
  return (
    <Main className="space-y-6" data-testid="comfort-dashboard">
      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">室内环境</h1>
            <Badge variant="outline" className="text-xs">
              ASHRAE 55
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">{site.displayName}</p>
        </div>

        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs">
            <RefreshCw className="size-3.5" />
            刷新环境监测
          </Button>
        </div>
      </div>

      {/* 4 Comfort KPI Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">热舒适综合达标率</CardTitle>
            <Smile className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">96.8%</span>
              <Badge variant="outline" className="gap-1 font-normal text-xs">
                <span className="size-1.5 rounded-full bg-emerald-500" />
                优级
              </Badge>
            </div>
            <div className="text-xs text-muted-foreground">
              PMV -0.15 · PPD 5.4%
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">全楼宇平均温度</CardTitle>
            <Thermometer className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">24.2</span>
              <span className="text-xs text-muted-foreground">°C</span>
              <span className="ml-auto text-xs text-muted-foreground">推荐 23.0 ~ 25.5°C</span>
            </div>
            <div className="text-xs text-muted-foreground">
              最大温差 1.5 K
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">全楼宇平均相对湿度</CardTitle>
            <Droplets className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">51.5</span>
              <span className="text-xs text-muted-foreground">%RH</span>
              <span className="ml-auto text-xs text-muted-foreground">推荐 45% ~ 60%</span>
            </div>
            <div className="text-xs text-muted-foreground">
              工况受控正常
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">室内空气综合品质 (IAQ)</CardTitle>
            <Wind className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground">优良</span>
              <span className="text-xs text-muted-foreground tabular-nums">AQI 22</span>
            </div>
            <div className="text-xs text-muted-foreground">
              CO₂ 510 ppm · PM2.5 11 μg/m³
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Spatial Comfort Cards Grid */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {ZONES.map((zone) => (
          <Card key={zone.id} className="shadow-xs">
            <CardHeader className="flex flex-row items-start justify-between pb-2">
              <div className="space-y-0.5">
                <CardTitle className="text-sm font-semibold">{zone.name}</CardTitle>
                <CardDescription className="text-xs">{zone.floor}</CardDescription>
              </div>
              <Badge variant="outline" className="gap-1.5 text-xs font-normal">
                <span className={cn('size-1.5 rounded-full', zone.status === 'comfort' ? 'bg-emerald-500' : 'bg-sky-500')} />
                {zone.statusLabel}
              </Badge>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="grid grid-cols-3 gap-2 rounded-md border bg-muted/20 p-2.5 text-center">
                <div>
                  <span className="text-[10px] text-muted-foreground">温度</span>
                  <p className="text-sm font-semibold text-foreground tabular-nums">{zone.temp} °C</p>
                </div>
                <div>
                  <span className="text-[10px] text-muted-foreground">湿度</span>
                  <p className="text-sm font-semibold text-foreground tabular-nums">{zone.humidity} %</p>
                </div>
                <div>
                  <span className="text-[10px] text-muted-foreground">CO2</span>
                  <p className="text-sm font-semibold text-foreground tabular-nums">{zone.co2} ppm</p>
                </div>
              </div>
              {zone.recommendation && (
                <div className="rounded-md border bg-muted/30 p-2 text-xs text-muted-foreground flex items-center gap-2">
                  <span className="font-medium text-foreground shrink-0">调优:</span>
                  <span className="truncate">{zone.recommendation}</span>
                </div>
              )}
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Temperature Distribution vs Comfort Band Chart */}
      <Card className="shadow-xs">
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-base font-semibold">各空间温度与 ASHRAE 舒适基准区间对标</CardTitle>
            <CardDescription className="text-xs">
              绿色阴影带为国家推荐办公热舒适目标区间 (23.0°C ~ 25.5°C)
            </CardDescription>
          </div>
          <div className="flex items-center gap-3 text-xs">
            <div className="flex items-center gap-1.5">
              <span className="size-2.5 rounded-full bg-primary" />
              <span className="text-muted-foreground">实测室温 (°C)</span>
            </div>
            <div className="flex items-center gap-1.5">
              <span className="h-2 w-4 rounded-xs bg-emerald-500/20 border border-emerald-500/40" />
              <span className="text-muted-foreground">合规舒适带</span>
            </div>
          </div>
        </CardHeader>
        <CardContent className="pt-4">
          <div className="h-[280px] w-full">
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={ZONE_CHART_DATA} margin={{ top: 15, right: 15, left: -15, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#e4e4e7" opacity={0.6} />
                <XAxis dataKey="name" tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} />
                <YAxis domain={[20, 28]} tickLine={false} axisLine={false} tick={{ fontSize: 11, fill: '#71717a' }} unit="°C" />
                <Tooltip
                  content={({ active, payload, label }) => {
                    if (!active || !payload || !payload.length) return null;
                    const d = payload[0].payload;
                    return (
                      <div className="rounded-lg border bg-popover p-2.5 text-xs shadow-md space-y-1">
                        <div className="font-semibold text-foreground border-b pb-1">{label}</div>
                        <div className="flex justify-between gap-4"><span>温度:</span><span className="tabular-nums font-semibold text-primary">{d.temp} °C</span></div>
                        <div className="flex justify-between gap-4"><span>湿度:</span><span className="tabular-nums font-medium text-foreground">{d.humidity} %RH</span></div>
                        <div className="flex justify-between gap-4"><span>CO2 浓度:</span><span className="tabular-nums font-medium text-foreground">{d.co2} ppm</span></div>
                      </div>
                    );
                  }}
                />
                <ReferenceArea y1={23.0} y2={25.5} fill="#10b981" fillOpacity={0.08} stroke="#10b981" strokeDasharray="3 3" strokeOpacity={0.4} />
                <Bar dataKey="temp" fill="#2563eb" radius={[6, 6, 0, 0]} maxBarSize={48} name="空间实测室温" />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </CardContent>
      </Card>
    </Main>
  );
}
