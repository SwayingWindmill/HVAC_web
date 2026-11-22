import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Gauge } from 'lucide-react';
import { FactTile } from '@/blocks/fact-list';
import { Panel, PanelHeader } from '@/blocks/panel';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { CanaryBindingsDialog } from '@/features/optimization/components/CanaryBindingsDialog';
import { NegativeSamplesDrawer } from '@/features/optimization/components/NegativeSamplesDrawer';
import { SolverSourceBadge } from '@/features/optimization/components/SolverSourceBadge';
import { siteOptimizationGovernanceSummaryQuery } from '@/features/optimization/query';
import { formatTime } from '@/lib/operator-format';

/** Engineering governance surface for optimization canary routing and guardrail negative-sample flywheel. */
export function OptimizationGovernancePanel({
  siteId,
  timeZone,
  active,
  canManage,
  csrfToken,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly active: boolean;
  readonly canManage: boolean;
  readonly csrfToken: string;
}) {
  const [canaryOpen, setCanaryOpen] = useState(false);
  const [samplesOpen, setSamplesOpen] = useState(false);
  const summaryQuery = useQuery(siteOptimizationGovernanceSummaryQuery(siteId, active));
  const summary = summaryQuery.data ?? null;

  const actions = (
    <div className="flex flex-wrap items-center gap-2">
      <Button type="button" variant="outline" size="sm" onClick={() => setCanaryOpen(true)}>
        管理灰度路由
      </Button>
      <Button type="button" variant="outline" size="sm" onClick={() => setSamplesOpen(true)}>
        查看负样本明细
      </Button>
    </div>
  );

  return (
    <>
      {summaryQuery.isPending ? (
        <Skeleton className="h-56 rounded-xl" />
      ) : summaryQuery.isError ? (
        <Panel aria-label="策略灰度与负样本">
          <PanelHeader title="策略灰度与负样本" action={actions} />
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">策略路由与负样本统计暂时读不到</p>
            <Button variant="outline" size="sm" onClick={() => void summaryQuery.refetch()}>
              重试
            </Button>
          </div>
        </Panel>
      ) : !summary ? (
        <Panel aria-label="策略灰度与负样本">
          <PanelHeader title="策略灰度与负样本" action={actions} />
          <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
            <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">
              <Gauge className="size-[18px]" aria-hidden="true" />
            </span>
            <p className="text-sm font-semibold">尚无近 14 天策略路由与负样本统计</p>
          </div>
        </Panel>
      ) : (
        <Panel aria-label="策略灰度与负样本">
          <PanelHeader
            title="策略灰度与负样本"
            badge={
              <SolverSourceBadge
                source={summary.activeSolver.source}
                label={summary.activeSolver.sourceLabel}
              />
            }
            caption={`近 ${summary.windowDays} 天统计 · 更新于 ${formatTime(summary.generatedAt, timeZone)}`}
            action={actions}
          />

          <div className="grid grid-cols-1 divide-y divide-divider lg:grid-cols-[minmax(0,260px)_minmax(0,1fr)] lg:divide-x lg:divide-y-0">
            <section aria-label="当前生效求解器" className="flex flex-col gap-3 px-5 py-4">
              <FactTile label="当前生效求解器" valueClassName="text-sm font-semibold text-foreground">
                <span>{summary.activeSolver.solverLabel}</span>
                <span className="mt-0.5 block text-xs font-normal text-muted-foreground">
                  {summary.activeSolver.loopLabel}
                  {summary.activeSolver.hasDeploymentBinding ? ' · 已绑定站点版本' : ''}
                </span>
              </FactTile>
            </section>

            <section aria-label="安全护栏负样本飞轮" className="flex flex-col gap-3 px-5 py-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="text-xs font-semibold text-foreground">安全护栏负样本飞轮</h3>
                <Badge variant={summary.flywheel.totalSamples > 0 ? 'warning' : 'secondary'}>
                  负样本 {summary.flywheel.totalSamples}
                </Badge>
              </div>
              <dl className="grid grid-cols-2 gap-x-5 gap-y-3 sm:grid-cols-3 lg:grid-cols-5">
                {summary.flywheel.items.map((item) => (
                  <FactTile
                    key={item.key}
                    label={item.label}
                    valueClassName={
                      item.count > 0
                        ? 'text-base font-semibold text-foreground'
                        : 'text-base font-semibold text-muted-foreground'
                    }
                  >
                    {item.count}
                  </FactTile>
                ))}
              </dl>
            </section>
          </div>
        </Panel>
      )}

      <CanaryBindingsDialog
        siteId={siteId}
        timeZone={timeZone}
        open={canaryOpen}
        onOpenChange={setCanaryOpen}
        canManage={canManage}
        csrfToken={csrfToken}
      />

      <NegativeSamplesDrawer
        siteId={siteId}
        timeZone={timeZone}
        open={samplesOpen}
        onOpenChange={setSamplesOpen}
      />
    </>
  );
}
