import {
  AlertTriangle,
  CheckCircle2,
  CircleDotDashed,
  CircleDollarSign,
  Gauge,
  HeartPulse,
  Search,
  ShieldCheck,
  Zap,
} from 'lucide-react';

import type { Alarm } from '@/api/alarms';
import type { FDDFinding } from '@/api/intelligence';
import type { IssueInvestigation } from '@/api/issues';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

interface AlarmDiagnosisPanelProps {
  readonly alarm: Alarm;
  readonly finding?: FDDFinding;
  readonly investigation?: IssueInvestigation | null;
  readonly investigationLoading?: boolean;
  readonly timeZone: string;
}

function formatInstant(value: string, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function sourceLabel(finding: FDDFinding): string {
  if (finding.ruleRevisionId) return '规则诊断';
  if (finding.modelDeploymentRevisionId) return '模型诊断';
  return '已有诊断';
}

function nextVerification(alarm: Alarm, finding?: FDDFinding): string {
  if (finding?.qualityBlocker) return '先核实数据质量，再复核诊断结论';
  if (alarm.condition === 'ACTIVE') return '复核当前异常、关联工况与历史趋势';
  return '复核恢复后的稳定性与是否再次出现';
}

function hypothesisLabel(status: IssueInvestigation['hypotheses'][number]['status']): string {
  if (status === 'CONFIRMED') return '已确认';
  if (status === 'SUPPORTED') return '证据支持';
  if (status === 'WEAKENED') return '证据减弱';
  if (status === 'REJECTED') return '已排除';
  return '待验证';
}

function hypothesisClass(status: IssueInvestigation['hypotheses'][number]['status']): string {
  if (status === 'CONFIRMED') return 'border-emerald-500/35 bg-emerald-500/5 text-emerald-700 dark:text-emerald-400';
  if (status === 'SUPPORTED') return 'border-blue-500/35 bg-blue-500/5 text-blue-700 dark:text-blue-400';
  if (status === 'WEAKENED' || status === 'REJECTED') return 'text-muted-foreground';
  return 'border-amber-500/35 bg-amber-500/5 text-amber-700 dark:text-amber-400';
}

function reliabilityLabel(value: IssueInvestigation['impact']['reliabilityRisk']): string {
  if (value === 'CRITICAL') return '严重';
  if (value === 'HIGH') return '高';
  if (value === 'MEDIUM') return '中';
  if (value === 'LOW') return '低';
  return '—';
}

function verificationLabel(status: IssueInvestigation['verificationSteps'][number]['status']): string {
  if (status === 'PASSED') return '已通过';
  if (status === 'FAILED') return '未通过';
  if (status === 'IN_PROGRESS') return '验证中';
  return '待验证';
}

function impactValue(value: number | null, suffix: string, maximumFractionDigits = 0): string {
  if (value === null) return '—';
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits }).format(value)}${suffix}`;
}

export function AlarmDiagnosisPanel({
  alarm,
  finding,
  investigation,
  investigationLoading = false,
  timeZone,
}: AlarmDiagnosisPanelProps) {
  return (
    <section
      className="space-y-5 border-t pt-5"
      data-testid="alarm-diagnosis-panel"
      data-diagnosis-state={finding ? 'PUBLISHED' : 'PENDING'}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h4 className="text-sm font-semibold">诊断与原因排查</h4>
          <p className="mt-0.5 text-xs text-muted-foreground">先看诊断和证据，再判断原因；没有验证完成前，不直接下原因结论。</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {finding ? <Badge variant="outline">{sourceLabel(finding)}</Badge> : <Badge variant="outline">待诊断</Badge>}
          {finding?.qualityBlocker ? <Badge variant="destructive">证据待核实</Badge> : null}
          {investigation?.rootCause.status === 'CONFIRMED' ? (
            <Badge variant="secondary" className="gap-1">
              <ShieldCheck className="size-3" aria-hidden="true" />
              原因已确认
            </Badge>
          ) : null}
        </div>
      </div>

      {finding ? (
        <div className="rounded-lg border bg-muted/20 p-4">
          <div className="flex items-start gap-3">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background">
              <CheckCircle2 className="size-4 text-emerald-600 dark:text-emerald-400" aria-hidden="true" />
            </span>
            <div className="min-w-0 flex-1">
              <p className="text-xs font-medium text-muted-foreground">诊断结果</p>
              <p className="mt-1 text-sm font-semibold leading-6">{finding.findingType}</p>
              <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                <span>评估 {formatInstant(finding.evaluationFrom, timeZone)} → {formatInstant(finding.evaluationTo, timeZone)}</span>
                <span>{finding.evidenceIds.length} 项关联证据</span>
                <span>结果评分 {Math.round(finding.confidence * 100)}%</span>
              </div>
              <p className="mt-2 text-xs leading-5 text-muted-foreground">
                这个评分只描述本次诊断结果的可信程度，不代表“原因概率”，也不会改变告警本身的活动或恢复状态。
              </p>
            </div>
          </div>
        </div>
      ) : (
        <div className="rounded-lg border border-dashed bg-muted/20 p-4">
          <div className="flex items-start gap-3">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background">
              <Search className="size-4 text-muted-foreground" aria-hidden="true" />
            </span>
            <div>
              <p className="text-sm font-medium">当前还没有诊断结果</p>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                告警只说明异常已经发生，还需要结合运行工况、证据和历史趋势继续判断原因。
              </p>
            </div>
          </div>
        </div>
      )}

      {finding?.qualityBlocker ? (
        <div className="flex items-start gap-3 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-sm">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
          <div>
            <p className="font-medium text-amber-700 dark:text-amber-300">证据质量限制</p>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">{finding.qualityBlocker}。应先核实数据质量，再继续判断原因。</p>
          </div>
        </div>
      ) : null}

      {investigationLoading ? (
        <div className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">正在读取排查信息…</div>
      ) : investigation ? (
        <>
          <div>
            <div className="mb-2 flex items-center justify-between gap-3">
              <div>
                <h5 className="text-sm font-medium">影响</h5>
                <p className="mt-0.5 text-xs text-muted-foreground">用于决定先处理什么，不与告警严重度混为一谈。</p>
              </div>
              <span className="text-xs text-muted-foreground">更新 {formatInstant(investigation.updatedAt, timeZone)}</span>
            </div>
            <dl className="grid overflow-hidden rounded-lg border sm:grid-cols-2">
              <div className="border-b p-3 sm:border-r">
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><Zap className="size-3.5" aria-hidden="true" />可避免能耗</dt>
                <dd className="mt-1 text-sm font-semibold tabular-nums">{impactValue(investigation.impact.avoidableEnergyKwh, ' kWh')}</dd>
              </div>
              <div className="border-b p-3">
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><CircleDollarSign className="size-3.5" aria-hidden="true" />可避免成本</dt>
                <dd className="mt-1 text-sm font-semibold tabular-nums">
                  {investigation.impact.avoidableCost === null
                    ? '—'
                    : new Intl.NumberFormat('zh-CN', {
                      style: 'currency',
                      currency: investigation.impact.currency ?? 'CNY',
                      maximumFractionDigits: 0,
                    }).format(investigation.impact.avoidableCost)}
                </dd>
              </div>
              <div className="p-3 sm:border-r">
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><HeartPulse className="size-3.5" aria-hidden="true" />舒适影响</dt>
                <dd className="mt-1 text-sm font-semibold tabular-nums">{impactValue(investigation.impact.comfortImpactHours, ' h', 1)}</dd>
              </div>
              <div className="p-3">
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><Gauge className="size-3.5" aria-hidden="true" />可靠性风险</dt>
                <dd className="mt-1 text-sm font-semibold">{reliabilityLabel(investigation.impact.reliabilityRisk)}</dd>
              </div>
            </dl>
          </div>

          <div>
            <div className="mb-2">
              <h5 className="text-sm font-medium">可能原因</h5>
              <p className="mt-0.5 text-xs text-muted-foreground">每个判断都要说明依据、反证和下一步怎么验证。</p>
            </div>
            <div className="divide-y overflow-hidden rounded-lg border">
              {investigation.hypotheses.map((hypothesis) => (
                <article key={hypothesis.id} className="space-y-2 p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="text-sm font-medium">{hypothesis.title}</p>
                    <Badge variant="outline" className={cn('font-normal', hypothesisClass(hypothesis.status))}>
                      {hypothesisLabel(hypothesis.status)}
                    </Badge>
                  </div>
                  <p className="text-xs leading-5 text-muted-foreground">{hypothesis.rationale}</p>
                  <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                    <span>支持证据 {hypothesis.supportingEvidenceCount}</span>
                    <span>反证 {hypothesis.contradictingEvidenceCount}</span>
                  </div>
                  {hypothesis.nextVerification ? (
                    <div className="flex items-start gap-2 rounded-md bg-muted/40 px-2.5 py-2 text-xs leading-5">
                      <CircleDotDashed className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                      <span><strong className="font-medium">下一验证：</strong>{hypothesis.nextVerification}</span>
                    </div>
                  ) : null}
                </article>
              ))}
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div className={cn(
              'rounded-lg border p-3',
              investigation.rootCause.status === 'CONFIRMED' && 'border-emerald-500/30 bg-emerald-500/5',
            )}>
              <span className="text-xs text-muted-foreground">原因结论</span>
              <strong className="mt-1 block text-sm font-medium">
                {investigation.rootCause.status === 'CONFIRMED' ? investigation.rootCause.title : '原因尚未确认'}
              </strong>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                {investigation.rootCause.rationale ?? '只有关键证据验证通过后，才会显示为已确认原因。'}
              </p>
              {investigation.rootCause.confirmedBy ? (
                <p className="mt-2 text-xs text-muted-foreground">
                  {investigation.rootCause.confirmedBy} · {investigation.rootCause.confirmedAt ? formatInstant(investigation.rootCause.confirmedAt, timeZone) : '—'}
                </p>
              ) : null}
            </div>

            <div className="rounded-lg border p-3">
              <span className="text-xs text-muted-foreground">验证情况</span>
              <div className="mt-2 space-y-2">
                {investigation.verificationSteps.map((step) => (
                  <div key={step.id} className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="text-sm font-medium">{step.title}</p>
                      <p className="mt-0.5 text-xs leading-5 text-muted-foreground">{step.method}</p>
                    </div>
                    <Badge variant="outline" className="shrink-0 font-normal">{verificationLabel(step.status)}</Badge>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </>
      ) : (
        <div className="grid overflow-hidden rounded-lg border sm:grid-cols-2">
          <div className="border-b p-3 sm:border-r sm:border-b-0">
            <span className="text-xs text-muted-foreground">原因状态</span>
            <strong className="mt-1 block text-sm font-medium">尚未确认</strong>
          </div>
          <div className="p-3">
            <span className="text-xs text-muted-foreground">下一验证</span>
            <strong className="mt-1 block text-sm font-medium">{nextVerification(alarm, finding)}</strong>
          </div>
        </div>
      )}
    </section>
  );
}
