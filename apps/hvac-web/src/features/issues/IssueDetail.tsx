import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  ClipboardCheck,
  Gauge,
  Search,
  TrendingUp,
  UserRound,
  Wrench,
} from 'lucide-react';

import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  getIssueInvestigation,
  getIssueQueue,
  type IssueInvestigation,
  type IssueQueueItem,
} from '@/api/issues';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty';
import { Skeleton } from '@/components/ui/skeleton';
import { formatInstant, severityLabel } from '@/features/alarms/alarm-center-model';
import { cn } from '@/lib/utils';

interface IssueDetailProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly issueId: string;
  readonly onBack: () => void;
}

function stateLabel(state: IssueQueueItem['state']): string {
  if (state === 'OPEN') return '待处理';
  if (state === 'INVESTIGATING') return '排查中';
  if (state === 'ACTION_PENDING') return '待执行';
  if (state === 'VERIFYING') return '验证中';
  return '已解决';
}

function diagnosisLabel(state: IssueQueueItem['diagnosisState']): string {
  if (state === 'ROOT_CAUSE_CONFIRMED') return '原因已确认';
  if (state === 'EVIDENCE_LIMITED') return '证据待核实';
  if (state === 'PUBLISHED') return '已有诊断';
  return '待诊断';
}

function causeStatusLabel(status: IssueInvestigation['hypotheses'][number]['status']): string {
  if (status === 'CONFIRMED') return '已确认';
  if (status === 'SUPPORTED') return '证据支持';
  if (status === 'WEAKENED') return '证据不足';
  if (status === 'REJECTED') return '已排除';
  return '待验证';
}

function verificationStatusLabel(status: IssueInvestigation['verificationSteps'][number]['status']): string {
  if (status === 'PASSED') return '已通过';
  if (status === 'FAILED') return '未通过';
  if (status === 'IN_PROGRESS') return '进行中';
  return '待验证';
}

function riskLabel(risk: IssueQueueItem['impact']['reliabilityRisk']): string {
  if (risk === 'CRITICAL') return '严重';
  if (risk === 'HIGH') return '高';
  if (risk === 'MEDIUM') return '中';
  if (risk === 'LOW') return '低';
  return '待评估';
}

function formatMoney(value: number | null, currency: string | null): string {
  if (value === null) return '待评估';
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: currency ?? 'CNY',
    maximumFractionDigits: 0,
  }).format(value);
}

function LoadingState() {
  return (
    <Main fluid className="space-y-5 pb-16">
      <Skeleton className="h-16 w-full" />
      <Skeleton className="h-32 w-full" />
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1.45fr)_minmax(300px,.75fr)]">
        <Skeleton className="h-[520px]" />
        <Skeleton className="h-[420px]" />
      </div>
    </Main>
  );
}

export function IssueDetail({ site, principal, issueId, onBack }: IssueDetailProps) {
  const queueQuery = useQuery({
    queryKey: ['issues', site.id, 'detail-queue', issueId],
    queryFn: ({ signal }) => getIssueQueue(site.id, { scope: 'all' }, signal),
    staleTime: 20_000,
  });

  const issue = useMemo(
    () => queueQuery.data?.items.find((item) => item.issueId === issueId),
    [issueId, queueQuery.data?.items],
  );

  const primaryAlarmId = issue?.alarmIds[0] ?? '';
  const investigationQuery = useQuery({
    queryKey: ['issues', site.id, 'detail-investigation', primaryAlarmId],
    queryFn: ({ signal }) => getIssueInvestigation(site.id, primaryAlarmId, signal),
    enabled: Boolean(primaryAlarmId),
    staleTime: 30_000,
  });

  if (queueQuery.isPending) return <LoadingState />;

  if (queueQuery.isError) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] rounded-lg border">
          <EmptyMedia variant="icon"><CircleAlert aria-hidden="true" /></EmptyMedia>
          <EmptyHeader>
            <EmptyTitle>问题详情暂时无法读取</EmptyTitle>
            <EmptyDescription>问题列表读取失败，可以返回列表继续处理原始告警。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent><Button variant="outline" onClick={onBack}><ArrowLeft />返回问题列表</Button></EmptyContent>
        </Empty>
      </Main>
    );
  }

  if (!queueQuery.data) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] rounded-lg border">
          <EmptyMedia variant="icon"><Search aria-hidden="true" /></EmptyMedia>
          <EmptyHeader>
            <EmptyTitle>完整问题详情尚未接入</EmptyTitle>
            <EmptyDescription>当前环境还没有问题归并数据；原始告警仍可正常使用。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent><Button variant="outline" onClick={onBack}><ArrowLeft />返回问题列表</Button></EmptyContent>
        </Empty>
      </Main>
    );
  }

  if (!issue) {
    return (
      <Main fluid>
        <Empty className="min-h-[520px] rounded-lg border">
          <EmptyMedia variant="icon"><CircleAlert aria-hidden="true" /></EmptyMedia>
          <EmptyHeader>
            <EmptyTitle>找不到这个问题</EmptyTitle>
            <EmptyDescription>它可能已经合并、归档，或不在当前站点授权范围内。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent><Button variant="outline" onClick={onBack}><ArrowLeft />返回问题列表</Button></EmptyContent>
        </Empty>
      </Main>
    );
  }

  const investigation = investigationQuery.data;
  const canReadWorkOrders = principal.authorization.capabilities.includes('work-order.list');
  const deviceId = issue.object.type === 'DEVICE' ? issue.object.id : null;

  return (
    <Main fluid className="space-y-5 pb-16" data-testid="issue-durable-detail">
      <header className="flex flex-col gap-4 border-b pb-5 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 items-start gap-3">
          <Button variant="outline" size="sm" className="mt-0.5 h-8 shrink-0" onClick={onBack}>
            <ArrowLeft aria-hidden="true" />
            返回
          </Button>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline">{severityLabel[issue.highestSeverity]}</Badge>
              <Badge variant="secondary">{stateLabel(issue.state)}</Badge>
              <Badge variant="outline">{diagnosisLabel(issue.diagnosisState)}</Badge>
            </div>
            <h1 className="mt-2 text-2xl font-semibold tracking-tight">{issue.title}</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {issue.object.label}
              {issue.object.locationLabel ? ` · ${issue.object.locationLabel}` : ''}
              {issue.assignee ? ` · 负责人：${issue.assignee}` : ' · 未指派'}
            </p>
          </div>
        </div>
        <Button variant="outline" size="sm" asChild>
          <Link
            to="/sites/$siteId/operations/trends"
            params={{ siteId: site.id }}
            search={{ eventTypes: 'alarm', timeStart: issue.firstDetectedAt, timeEnd: issue.updatedAt }}
          >
            <TrendingUp aria-hidden="true" />
            查看趋势
          </Link>
        </Button>
      </header>

      <section className="grid overflow-hidden rounded-lg border sm:grid-cols-2 xl:grid-cols-4" aria-label="问题关键信息">
        {[
          ['首次发现', formatInstant(issue.firstDetectedAt, site.timezone)],
          ['最近变化', formatInstant(issue.updatedAt, site.timezone)],
          ['关联告警', `${issue.alarmIds.length} 条 · ${issue.activeAlarmCount} 条活动`],
          ['确认情况', issue.unacknowledgedAlarmCount ? `${issue.unacknowledgedAlarmCount} 条未确认` : '相关告警均已确认'],
        ].map(([label, value], index) => (
          <div key={label} className={cn('px-4 py-3', index > 0 && 'border-t sm:border-l sm:border-t-0', index === 2 && 'sm:border-l-0 xl:border-l')}>
            <p className="text-xs text-muted-foreground">{label}</p>
            <p className="mt-1 text-sm font-medium">{value}</p>
          </div>
        ))}
      </section>

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1.45fr)_minmax(300px,.75fr)]">
        <div className="space-y-5">
          <section className="space-y-3 rounded-lg border p-4" aria-labelledby="related-alarms-title">
            <div>
              <h2 id="related-alarms-title" className="text-base font-semibold">关联告警</h2>
              <p className="mt-1 text-sm text-muted-foreground">{issue.grouping.reason}</p>
            </div>
            <div className="divide-y overflow-hidden rounded-md border">
              {issue.signals.map((signal) => (
                <div key={signal.alarmId} className="flex flex-col gap-2 px-3 py-3 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <Badge variant="outline" className="font-normal">{severityLabel[signal.severity]}</Badge>
                      <span className="font-medium">{signal.title}</span>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {signal.condition === 'ACTIVE' ? '活动中' : '已恢复'} · {signal.acknowledged ? '已确认' : '未确认'}
                    </p>
                  </div>
                  <time className="shrink-0 text-xs tabular-nums text-muted-foreground">
                    {formatInstant(signal.updatedAt, site.timezone)}
                  </time>
                </div>
              ))}
            </div>
            <p className="text-xs leading-5 text-muted-foreground">
              这些告警因为共同对象、时间关系或运行关系被放在一起排查；关联本身不代表已经证明因果关系。
            </p>
          </section>

          <section className="space-y-3 rounded-lg border p-4" aria-labelledby="cause-title">
            <div className="flex items-start justify-between gap-3">
              <div>
                <h2 id="cause-title" className="text-base font-semibold">原因排查</h2>
                <p className="mt-1 text-sm text-muted-foreground">每个判断都要有依据、反证和下一步验证。</p>
              </div>
              {investigation?.rootCause.status === 'CONFIRMED' ? (
                <Badge className="gap-1"><CheckCircle2 className="size-3" aria-hidden="true" />原因已确认</Badge>
              ) : (
                <Badge variant="outline">原因尚未确认</Badge>
              )}
            </div>

            {investigationQuery.isPending ? (
              <Skeleton className="h-44 w-full" />
            ) : investigationQuery.isError ? (
              <p className="rounded-md border border-dashed px-3 py-5 text-sm text-muted-foreground">排查信息暂时无法读取。</p>
            ) : investigation?.hypotheses.length ? (
              <div className="space-y-3">
                {investigation.hypotheses.map((cause) => (
                  <article key={cause.id} className="rounded-md border p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <h3 className="text-sm font-medium">{cause.title}</h3>
                      <Badge variant="secondary">{causeStatusLabel(cause.status)}</Badge>
                    </div>
                    <p className="mt-2 text-sm leading-6 text-muted-foreground">{cause.rationale}</p>
                    <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      <span>支持证据 {cause.supportingEvidenceCount}</span>
                      <span>反证 {cause.contradictingEvidenceCount}</span>
                    </div>
                    {cause.nextVerification ? (
                      <p className="mt-2 text-xs leading-5"><strong className="font-medium">下一步：</strong>{cause.nextVerification}</p>
                    ) : null}
                  </article>
                ))}
              </div>
            ) : (
              <p className="rounded-md border border-dashed px-3 py-5 text-sm text-muted-foreground">当前还没有形成可展示的原因判断。</p>
            )}

            {investigation?.rootCause.status === 'CONFIRMED' ? (
              <div className="rounded-md bg-muted/35 p-3">
                <p className="text-xs font-medium text-muted-foreground">已确认原因</p>
                <p className="mt-1 text-sm font-medium">{investigation.rootCause.title}</p>
                {investigation.rootCause.rationale ? <p className="mt-1 text-sm leading-6 text-muted-foreground">{investigation.rootCause.rationale}</p> : null}
              </div>
            ) : null}
          </section>

          <section className="space-y-3 rounded-lg border p-4" aria-labelledby="verification-title">
            <div>
              <h2 id="verification-title" className="text-base font-semibold">验证情况</h2>
              <p className="mt-1 text-sm text-muted-foreground">处理完成不等于问题已经解决；需要用运行数据验证效果。</p>
            </div>
            {investigation?.verificationSteps.length ? (
              <div className="divide-y overflow-hidden rounded-md border">
                {investigation.verificationSteps.map((step) => (
                  <div key={step.id} className="px-3 py-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="text-sm font-medium">{step.title}</span>
                      <Badge variant="outline">{verificationStatusLabel(step.status)}</Badge>
                    </div>
                    <p className="mt-1 text-xs leading-5 text-muted-foreground">{step.method}</p>
                    <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      {step.owner ? <span>负责人：{step.owner}</span> : null}
                      {step.dueAt ? <span>计划：{formatInstant(step.dueAt, site.timezone)}</span> : null}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <p className="rounded-md border border-dashed px-3 py-5 text-sm text-muted-foreground">当前还没有验证步骤。</p>
            )}
          </section>
        </div>

        <aside className="space-y-5">
          <section className="space-y-3 rounded-lg border p-4" aria-labelledby="next-action-title">
            <div className="flex items-center gap-2">
              <ClipboardCheck className="size-4 text-muted-foreground" aria-hidden="true" />
              <h2 id="next-action-title" className="text-base font-semibold">下一步</h2>
            </div>
            <p className="text-sm leading-6">{issue.nextAction ?? '等待负责人确定下一步处理。'}</p>
          </section>

          <section className="space-y-3 rounded-lg border p-4" aria-labelledby="impact-title">
            <div className="flex items-center gap-2">
              <Gauge className="size-4 text-muted-foreground" aria-hidden="true" />
              <h2 id="impact-title" className="text-base font-semibold">影响</h2>
            </div>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
              <div>
                <dt className="text-xs text-muted-foreground">可避免能耗</dt>
                <dd className="mt-1 font-medium tabular-nums">{issue.impact.avoidableEnergyKwh === null ? '待评估' : `${new Intl.NumberFormat('zh-CN').format(issue.impact.avoidableEnergyKwh)} kWh`}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">可避免成本</dt>
                <dd className="mt-1 font-medium tabular-nums">{formatMoney(issue.impact.avoidableCost, issue.impact.currency)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">舒适影响</dt>
                <dd className="mt-1 font-medium tabular-nums">{issue.impact.comfortImpactHours === null ? '待评估' : `${issue.impact.comfortImpactHours} 小时`}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">可靠性风险</dt>
                <dd className="mt-1 font-medium">{riskLabel(issue.impact.reliabilityRisk)}</dd>
              </div>
            </dl>
          </section>

          <section className="space-y-3 rounded-lg border p-4" aria-labelledby="owner-title">
            <div className="flex items-center gap-2">
              <UserRound className="size-4 text-muted-foreground" aria-hidden="true" />
              <h2 id="owner-title" className="text-base font-semibold">处理责任</h2>
            </div>
            <p className="text-sm">{issue.assignee ?? '当前未指派负责人'}</p>
          </section>

          <section className="space-y-3 rounded-lg border p-4" aria-labelledby="continue-title">
            <h2 id="continue-title" className="text-base font-semibold">继续处理</h2>
            <div className="grid gap-2">
              <Button variant="outline" size="sm" asChild className="justify-between">
                <Link to="/sites/$siteId/operations" params={{ siteId: site.id }}>
                  系统运行<ArrowRight aria-hidden="true" />
                </Link>
              </Button>
              {deviceId ? (
                <Button variant="outline" size="sm" asChild className="justify-between">
                  <Link to="/sites/$siteId/devices/$deviceId" params={{ siteId: site.id, deviceId }}>
                    设备详情<ArrowRight aria-hidden="true" />
                  </Link>
                </Button>
              ) : null}
              {canReadWorkOrders ? (
                <Button variant="outline" size="sm" asChild className="justify-between">
                  <Link
                    to="/sites/$siteId/work-orders"
                    params={{ siteId: site.id }}
                    search={{ sourceAlarm: primaryAlarmId, source: 'alarm' }}
                  >
                    进入工单<Wrench aria-hidden="true" />
                  </Link>
                </Button>
              ) : null}
            </div>
          </section>
        </aside>
      </div>
    </Main>
  );
}
