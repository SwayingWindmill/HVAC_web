import { AlarmClock, BellRing, CircleDollarSign, Gauge, Repeat2, SearchCheck, Zap } from 'lucide-react';

import type { IssuePerformance, IssuePerformancePeriod } from '@/api/issues';
import { FactStrip } from '@/blocks/fact-strip';
import { BarList } from '@/components/ui/bar-list';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { HeatmapChart } from '@/components/ui/heatmap-chart';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { formatDurationMs } from './alarm-center-model';

interface AlarmPerformanceViewProps {
  readonly data: IssuePerformance | null | undefined;
  readonly period: IssuePerformancePeriod;
  readonly onPeriodChange: (period: IssuePerformancePeriod) => void;
  readonly loading: boolean;
  readonly error: boolean;
}

function percent(value: number): string {
  return new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 0 }).format(value);
}

function formatSeconds(value: number | null): string {
  return value === null ? '—' : formatDurationMs(value * 1000);
}

function money(value: number | null, currency: string | null): string {
  if (value === null) return '—';
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: currency ?? 'CNY',
    maximumFractionDigits: 0,
  }).format(value);
}

export function AlarmPerformanceView({
  data,
  period,
  onPeriodChange,
  loading,
  error,
}: AlarmPerformanceViewProps) {
  return (
    <section className="space-y-4" data-testid="alarm-performance-view" aria-label="告警分析">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold">告警分析</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">看触发频率、响应效率、重复问题与高发时段，反向优化规则和运维投入。</p>
        </div>
        <Select value={period} onValueChange={(value) => onPeriodChange(value as IssuePerformancePeriod)}>
          <SelectTrigger className="h-8 w-[118px] text-xs" aria-label="告警绩效周期">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="7d">最近 7 天</SelectItem>
              <SelectItem value="30d">最近 30 天</SelectItem>
              <SelectItem value="90d">最近 90 天</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>

      {loading ? (
        <div className="min-h-[420px] animate-pulse rounded-lg border bg-muted/20" aria-label="正在加载告警分析" />
      ) : error ? (
        <Empty className="min-h-[420px] rounded-lg border">
          <EmptyMedia variant="icon"><Gauge aria-hidden="true" /></EmptyMedia>
          <EmptyHeader>
            <EmptyTitle>告警分析暂不可用</EmptyTitle>
            <EmptyDescription>绩效聚合读取失败；告警处置队列不受影响。</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : !data ? (
        <Empty className="min-h-[420px] rounded-lg border">
          <EmptyMedia variant="icon"><Gauge aria-hidden="true" /></EmptyMedia>
          <EmptyHeader>
            <EmptyTitle>告警分析数据尚未接入</EmptyTitle>
            <EmptyDescription>前端目标态已经定义，后端需要提供触发、确认、恢复、诊断覆盖、Flood、重复问题和影响聚合。</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
          <FactStrip
            ariaLabel="告警绩效关键指标"
            items={[
              {
                key: 'triggered',
                label: '触发次数',
                value: data.totalTriggered,
                suffix: '次',
                icon: <BellRing />,
              },
              {
                key: 'mtta',
                label: '平均确认',
                value: formatSeconds(data.averageAcknowledgeSeconds),
                icon: <AlarmClock />,
              },
              {
                key: 'mttr',
                label: '平均恢复',
                value: formatSeconds(data.averageResolutionSeconds),
                icon: <Gauge />,
              },
              {
                key: 'diagnosis',
                label: '诊断覆盖',
                value: percent(data.diagnosisCoverage),
                icon: <SearchCheck />,
              },
            ]}
          />

          <dl className="grid overflow-hidden rounded-lg border text-sm sm:grid-cols-2 xl:grid-cols-4">
            <div className="border-b p-3 sm:border-r xl:border-b-0">
              <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><Repeat2 className="size-3.5" aria-hidden="true" />重复问题率</dt>
              <dd className="mt-1 font-semibold tabular-nums">{percent(data.repeatIssueRate)}</dd>
              <p className="mt-1 text-xs text-muted-foreground">同类问题在周期内再次出现的比例</p>
            </div>
            <div className="border-b p-3 xl:border-r xl:border-b-0">
              <dt className="text-xs text-muted-foreground">告警集中爆发</dt>
              <dd className="mt-1 font-semibold tabular-nums">{data.floodWindows} 个</dd>
              <p className="mt-1 text-xs text-muted-foreground">短时间集中爆发，需要检查关联故障与规则设计</p>
            </div>
            <div className="border-b p-3 sm:border-r sm:border-b-0">
              <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><Zap className="size-3.5" aria-hidden="true" />可避免能耗</dt>
              <dd className="mt-1 font-semibold tabular-nums">
                {data.avoidableEnergyKwh === null ? '—' : `${new Intl.NumberFormat('zh-CN').format(data.avoidableEnergyKwh)} kWh`}
              </dd>
              <p className="mt-1 text-xs text-muted-foreground">由诊断影响模型聚合，不从告警次数推算</p>
            </div>
            <div className="p-3">
              <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><CircleDollarSign className="size-3.5" aria-hidden="true" />可避免成本</dt>
              <dd className="mt-1 font-semibold tabular-nums">{money(data.avoidableCost, data.currency)}</dd>
              <p className="mt-1 text-xs text-muted-foreground">帮助把技术故障转化为业务优先级</p>
            </div>
          </dl>

          <div className="grid gap-4 xl:grid-cols-[minmax(0,1.65fr)_minmax(280px,0.85fr)]">
            <section className="min-w-0 rounded-lg border p-4" aria-labelledby="alarm-heatmap-title">
              <div className="mb-4">
                <h3 id="alarm-heatmap-title" className="text-sm font-semibold">告警高发时段</h3>
                <p className="mt-0.5 text-xs text-muted-foreground">按星期 × 时段查看触发密度，用于识别负荷、日程或控制策略相关模式。</p>
              </div>
              <HeatmapChart
                rows={data.heatmap.rows}
                columns={data.heatmap.columns}
                columnLabelEvery={2}
                unit="次"
                renderTooltip={(datum) => (
                  <>
                    <span className="font-medium tabular-nums">{datum.value} 次触发</span>
                    <span className="block text-background/70">{datum.rowLabel} · {datum.columnLabel}</span>
                  </>
                )}
              />
            </section>

            <section className="rounded-lg border p-4" aria-labelledby="alarm-contributors-title">
              <div className="mb-4">
                <h3 id="alarm-contributors-title" className="text-sm font-semibold">高频贡献对象</h3>
                <p className="mt-0.5 text-xs text-muted-foreground">优先找“总在报警”的设备，而不是只盯最严重的一条。</p>
              </div>
              <BarList
                data={data.topContributors.map((item) => ({
                  key: item.key,
                  name: item.label,
                  value: item.triggeredCount,
                  meta: item.averageResolutionSeconds === null ? undefined : `平均恢复 ${formatSeconds(item.averageResolutionSeconds)}`,
                }))}
                valueFormatter={(value) => `${new Intl.NumberFormat('zh-CN').format(value)} 次`}
              />
            </section>
          </div>

          <section className="rounded-lg border p-4" aria-labelledby="recurring-issues-title">
            <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
              <div>
                <h3 id="recurring-issues-title" className="text-sm font-semibold">重复问题</h3>
                <p className="mt-0.5 text-xs text-muted-foreground">把反复出现且持续造成能耗影响的问题提升为整改优先项。</p>
              </div>
              <span className="text-xs text-muted-foreground">确认率 {percent(data.acknowledgementRate)} · 期末活动 {data.activeAtEnd} 项</span>
            </div>
            <BarList
              data={data.recurringIssues.map((item) => ({
                key: item.key,
                name: item.label,
                value: item.occurrences,
                meta: item.avoidableEnergyKwh === null ? undefined : `${new Intl.NumberFormat('zh-CN').format(item.avoidableEnergyKwh)} kWh 可避免`,
              }))}
              valueFormatter={(value) => `${new Intl.NumberFormat('zh-CN').format(value)} 次`}
            />
          </section>
        </>
      )}
    </section>
  );
}
