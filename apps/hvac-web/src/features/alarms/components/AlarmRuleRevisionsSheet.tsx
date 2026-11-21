import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { AlertCircle, History, RotateCcw } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import {
  BUNDLE_SOURCE_LABELS,
  SiteRuleAuthoringError,
  rollbackSiteAlarmRules,
  siteAlarmRuleRevisionsQuery,
  type AlarmRulePublishResult,
} from '../alarm-rules';

function formatRevisionTime(iso: string, timeZone: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date);
}

export function AlarmRuleRevisionsSheet({
  open,
  siteId,
  canManage,
  csrfToken,
  timeZone,
  onClose,
  onRolledBack,
}: {
  readonly open: boolean;
  readonly siteId: string;
  readonly canManage: boolean;
  readonly csrfToken: string;
  readonly timeZone: string;
  readonly onClose: () => void;
  readonly onRolledBack: (result: AlarmRulePublishResult) => void;
}) {
  const [confirmingRevision, setConfirmingRevision] = useState<number | null>(null);
  const [rollbackNote, setRollbackNote] = useState('');

  const revisionsQuery = useQuery({
    ...siteAlarmRuleRevisionsQuery(siteId, 30),
    enabled: open,
  });

  const rollbackMutation = useMutation({
    mutationFn: ({ targetRevision, expectedRevision, note }: {
      readonly targetRevision: number;
      readonly expectedRevision: number;
      readonly note: string;
    }) => rollbackSiteAlarmRules(
      siteId,
      targetRevision,
      { expectedRevision, changeNote: note.trim() || undefined },
      csrfToken,
    ),
    onSuccess: (result) => {
      setConfirmingRevision(null);
      setRollbackNote('');
      onRolledBack(result);
    },
  });

  const currentRevision = revisionsQuery.data?.currentRevision ?? 0;

  const isConflictError = rollbackMutation.error instanceof SiteRuleAuthoringError && rollbackMutation.error.status === 409;

  return (
    <Sheet open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen && !rollbackMutation.isPending) {
        setConfirmingRevision(null);
        setRollbackNote('');
        rollbackMutation.reset();
        onClose();
      }
    }}>
      <SheetContent side="right" className="w-full overflow-y-auto data-[side=right]:sm:max-w-[560px]">
        <SheetHeader className="border-b border-divider">
          <SheetTitle className="flex items-center gap-2">
            <History className="size-4" aria-hidden="true" />
            <span>规则版本账本</span>
          </SheetTitle>
          <SheetDescription>
            每次热发布、文件同步或版本回滚均生成不可变规则快照。回滚操作支持按版本号无损恢复并保留未改动规则状态。
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-1 flex-col gap-3 p-4">
          {revisionsQuery.isPending ? (
            <div className="flex flex-col gap-2">
              <Skeleton className="h-20" />
              <Skeleton className="h-20" />
            </div>
          ) : revisionsQuery.isError ? (
            <p className="text-xs text-destructive-text">暂时无法读取版本历史，请稍后再试。</p>
          ) : revisionsQuery.data.items.length === 0 ? (
            <div className="rounded-lg border border-dashed border-divider bg-muted/20 p-6 text-center text-xs text-muted-foreground">
              当前站点还没有规则包发布记录，首次热发布或文件同步后将在此显示历史版本。
            </div>
          ) : (
            revisionsQuery.data.items.map((rev) => {
              const isCurrent = rev.bundleRevision === currentRevision;
              const isConfirming = confirmingRevision === rev.bundleRevision;
              return (
                <div
                  key={rev.bundleRevision}
                  className="flex flex-col gap-2 rounded-lg border border-divider bg-card p-3.5 text-xs"
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="font-mono text-sm font-semibold text-foreground">v{rev.bundleRevision}</span>
                      {isCurrent ? (
                        <Badge variant="default" className="h-5 px-1.5 text-[11px]">当前生效</Badge>
                      ) : null}
                      <Badge variant="outline" className="h-5 px-1.5 text-[11px] font-normal">
                        {BUNDLE_SOURCE_LABELS[rev.source]}
                      </Badge>
                      {rev.rolledBackFromRevision ? (
                        <span className="text-[11px] text-muted-foreground">恢复自 v{rev.rolledBackFromRevision}</span>
                      ) : null}
                    </div>
                    <span className="text-[11px] text-muted-foreground tabular-nums">
                      {formatRevisionTime(rev.publishedAt, timeZone)}
                    </span>
                  </div>

                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground">
                    <span>操作人：{rev.publishedByName || '系统维护任务'}</span>
                    <span>规则总数：{rev.ruleCount} 条</span>
                    <span>
                      新增 {rev.summary.added} · 修改 {rev.summary.changed} · 停用 {rev.summary.retired}
                    </span>
                  </div>

                  {rev.changeNote ? (
                    <p className="rounded bg-muted/30 px-2.5 py-1.5 text-foreground">{rev.changeNote}</p>
                  ) : null}

                  {canManage && !isCurrent ? (
                    isConfirming ? (
                      <div className="flex flex-col gap-2 rounded-md border border-divider bg-muted/20 p-2.5">
                        <span className="font-medium text-foreground">确认从 v{currentRevision} 回滚至 v{rev.bundleRevision}？</span>
                        <Input
                          aria-label={`回滚至 v${rev.bundleRevision} 原因说明`}
                          value={rollbackNote}
                          maxLength={500}
                          onChange={(e) => setRollbackNote(e.target.value)}
                          placeholder={`回滚原因（选填，默认记为回滚至版本 v${rev.bundleRevision}）`}
                        />
                        <div className="flex justify-end gap-2">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={() => {
                              setConfirmingRevision(null);
                              setRollbackNote('');
                            }}
                          >
                            取消
                          </Button>
                          <Button
                            type="button"
                            size="sm"
                            disabled={rollbackMutation.isPending}
                            onClick={() => rollbackMutation.mutate({
                              targetRevision: rev.bundleRevision,
                              expectedRevision: currentRevision,
                              note: rollbackNote,
                            })}
                          >
                            {rollbackMutation.isPending ? '回滚中…' : '确认回滚'}
                          </Button>
                        </div>
                      </div>
                    ) : (
                      <div className="flex justify-end">
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            rollbackMutation.reset();
                            setRollbackNote('');
                            setConfirmingRevision(rev.bundleRevision);
                          }}
                        >
                          <RotateCcw data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                          回滚到此版本
                        </Button>
                      </div>
                    )
                  ) : null}
                </div>
              );
            })
          )}

          {rollbackMutation.isError ? (
            <div role="alert" className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive-text">
              <div className="flex items-start gap-2">
                <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <span>
                  {isConflictError
                    ? '站点规则版本已在其他会话中变更，请重新加载版本列表后再发起回滚。'
                    : (rollbackMutation.error instanceof Error ? rollbackMutation.error.message : '回滚失败，请刷新后再试')}
                </span>
              </div>
              {isConflictError ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    rollbackMutation.reset();
                    setConfirmingRevision(null);
                    void revisionsQuery.refetch();
                  }}
                >
                  重新加载版本列表
                </Button>
              ) : null}
            </div>
          ) : null}
        </div>

        <SheetFooter className="border-t border-divider px-4 py-3">
          <Button type="button" variant="outline" disabled={rollbackMutation.isPending} onClick={onClose}>
            关闭
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
