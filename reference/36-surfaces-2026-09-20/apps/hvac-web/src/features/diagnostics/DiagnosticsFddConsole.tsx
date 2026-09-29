import {
  Activity,
  Flame,
  Lightbulb,
  RefreshCw,
  ShieldCheck,
  Stethoscope,
  Wrench,
} from 'lucide-react';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';

export interface DiagnosticsFddConsoleProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime?: unknown;
  readonly searchState?: unknown;
  readonly onSearchChange?: (patch: unknown) => void;
}

interface FddFinding {
  readonly id: string;
  readonly title: string;
  readonly category: string;
  readonly confidence: number;
  readonly severity: 'HIGH' | 'MEDIUM' | 'LOW';
  readonly weeklyLossCny: number;
  readonly wastedKWh: number;
  readonly impact: string;
  readonly evidence: readonly string[];
  readonly remediation: string;
}

const FINDINGS: readonly FddFinding[] = [
  {
    id: 'fdd-01',
    title: '冷水输配系统“大流量、小温差”综合征 (Low Delta-T Syndrome)',
    category: '冷源输配系统',
    confidence: 94,
    severity: 'HIGH',
    weeklyLossCny: 680,
    wastedKWh: 860,
    impact: '造成水泵长期高频输送无效流量，且迫使主机在较低负荷下并联开机，系统能效比 COP 降低 14.5%',
    evidence: [
      '冷水供回水实测温差仅 2.8 K (设计温差 5.0 K)，供回水温差不足达标率 56%',
      '二次冷冻泵持续运行在 48.5 Hz，水力输配系数 (WTF) 明显偏低',
      '压差旁通阀检测到存在 12% 持续微漏旁通流量，引起回水温度被稀释下降',
    ],
    remediation: '建议对旁通控制阀实施零位校准；并将二次泵压差设定由恒定 140 kPa 调整为末端最不利环路动态温差寻优。',
  },
  {
    id: 'fdd-02',
    title: '冷却塔填料积垢导致散热逼近度异常增大',
    category: '冷凝散热系统',
    confidence: 88,
    severity: 'MEDIUM',
    weeklyLossCny: 420,
    wastedKWh: 540,
    impact: '冷凝水出水温度比设计湿球逼近度高 1.6°C，导致冷机冷凝饱和压力上升 85 kPa，主机功耗上升约 4.2%',
    evidence: [
      'CT-01 散热逼近度由标称 3.2 K 扩大至 4.8 K，热交换效率下降 18%',
      '冷却水进出水温差由设计 5.0 K 衰减至 3.9 K',
      '布水器水流分布不均，红外测温显示填料局部存在干斑与结垢',
    ],
    remediation: '安排清洗冷却塔填料与喷头布水器；投放阻垢杀菌剂改善循环水质。',
  },
  {
    id: 'fdd-03',
    title: '夜间非工时间循环水泵恒速空载运行 (无功损耗)',
    category: '运行控制调度',
    confidence: 98,
    severity: 'MEDIUM',
    weeklyLossCny: 320,
    wastedKWh: 440,
    impact: '凌晨 01:00 - 05:30 建筑负荷清零期间，冷冻水泵仍保持 42 Hz 运转，属于典型非生产性无效用能',
    evidence: [
      '监测周期内夜间空调末端阀门全关，冷站瞬时负荷低于 15 kW',
      'CHWP-01 水泵电机仍输出 18 kW 轴功率，夜间累计产生 85 kWh/天 冗余电耗',
      '排程控制策略中缺失“末端负荷清零自休眠”判定触发逻辑',
    ],
    remediation: '下发控制策略优化指令，增加末端阀位连锁休眠逻辑，自动在夜间关闭循环泵。',
  },
];

export function DiagnosticsFddConsole({ site }: DiagnosticsFddConsoleProps) {
  return (
    <Main className="space-y-6" data-testid="diagnostics-fdd-console">
      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">诊断中心</h1>
            <Badge variant="outline" className="text-xs">
              FDD 诊断在线
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {site.displayName} · 运行工况与能效故障诊断
          </p>
        </div>

        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-xs">
            <RefreshCw className="size-3.5" />
            重新检测
          </Button>
        </div>
      </div>

      {/* 4 FDD Executive Health KPI Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">系统运行健康评分</CardTitle>
            <Stethoscope className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">89.2</span>
              <span className="text-xs text-muted-foreground">/ 100 分</span>
            </div>
            <div className="text-xs text-muted-foreground">
              3 项待处理异常
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">预估每周能耗浪费损失</CardTitle>
            <Flame className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">¥1,420</span>
              <span className="text-xs text-muted-foreground">/ 周</span>
            </div>
            <div className="text-xs text-muted-foreground">
              折合 1,840 kWh/周
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">已激活诊断规则库</CardTitle>
            <ShieldCheck className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">36</span>
              <span className="text-xs text-muted-foreground">项规则</span>
            </div>
            <div className="text-xs text-muted-foreground">
              冷机 / 水泵 / 冷却塔 / 末端
            </div>
          </CardContent>
        </Card>

        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">已处置修复率</CardTitle>
            <Wrench className="size-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight text-foreground tabular-nums">87.5%</span>
              <Badge variant="outline" className="gap-1 font-normal text-xs">
                <span className="size-1.5 rounded-full bg-emerald-500" />
                已处置
              </Badge>
            </div>
            <div className="text-xs text-muted-foreground">
              本月已调优 7 项
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Detailed Diagnostic Findings Cards */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold text-foreground">当前诊断发现 (3 项)</h2>
        </div>

        {FINDINGS.map((finding) => (
          <Card key={finding.id} className="shadow-xs">
            <CardHeader className="pb-3 border-b">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                <div>
                  <div className="flex items-center gap-2">
                    <Badge variant="outline" className="gap-1.5 text-xs font-normal">
                      <span className={cn('size-1.5 rounded-full', finding.severity === 'HIGH' ? 'bg-destructive' : 'bg-amber-500')} />
                      {finding.severity === 'HIGH' ? '高损耗' : '中损耗'}
                    </Badge>
                    <Badge variant="outline" className="border-border text-muted-foreground text-xs">
                      置信度 {finding.confidence}%
                    </Badge>
                    <span className="text-xs text-muted-foreground">{finding.category}</span>
                  </div>
                  <CardTitle className="text-base font-semibold mt-1.5 text-foreground">{finding.title}</CardTitle>
                </div>

                <div className="flex items-center gap-2 shrink-0">
                  <div className="text-right mr-2">
                    <span className="text-[11px] text-muted-foreground block">预估损失</span>
                    <strong className="text-sm font-semibold text-foreground tabular-nums">¥{finding.weeklyLossCny} / 周</strong>
                  </div>
                  <Button variant="default" size="sm" className="h-8 gap-1.5 text-xs">
                    <Wrench className="size-3.5" />
                    派发工单
                  </Button>
                </div>
              </div>
            </CardHeader>

            <CardContent className="space-y-3 pt-3">
              {/* Impact summary */}
              <div className="text-xs text-muted-foreground leading-relaxed">
                <span className="text-foreground font-medium">影响: </span>
                {finding.impact}
              </div>

              {/* Evidence Chain */}
              <div className="rounded-md border bg-muted/20 p-2.5 text-xs space-y-1">
                <span className="font-medium text-foreground flex items-center gap-1.5">
                  <Activity className="size-3.5 text-muted-foreground" />
                  异常特征与测点依据:
                </span>
                <ul className="space-y-0.5 text-muted-foreground pl-5 list-disc">
                  {finding.evidence.map((ev, idx) => (
                    <li key={idx} className="leading-relaxed">{ev}</li>
                  ))}
                </ul>
              </div>

              {/* Remediation Action */}
              <div className="rounded-md border bg-muted/30 p-2.5 text-xs flex items-center gap-2">
                <Lightbulb className="size-3.5 text-muted-foreground shrink-0" />
                <div className="text-muted-foreground">
                  <span className="font-medium text-foreground">建议对策: </span>
                  {finding.remediation}
                </div>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </Main>
  );
}
