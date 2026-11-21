import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { AlertCircle, AlertTriangle, CheckCircle2, Rocket } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ruleToDraft } from '../alarm-rule-dag';
import {
  CHANGED_FIELD_LABELS,
  RULE_CHANGE_LABELS,
  SiteRuleAuthoringError,
  diffSiteAlarmRules,
  formatRuleValidationIssue,
  publishSiteAlarmRules,
  type AlarmRule,
  type AlarmRuleDiffResult,
  type AlarmRulePublishResult,
} from '../alarm-rules';

export function AlarmRulePublishDiffDialog({
  open,
  siteId,
  csrfToken,
  expectedRevision,
  draftRules,
  titleByRuleId,
  onClose,
  onPublished,
  onReloadLatest,
}: {
  readonly open: boolean;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly expectedRevision: number;
  readonly draftRules: readonly AlarmRule[];
  readonly titleByRuleId?: ReadonlyMap<string, string>;
  readonly onClose: () => void;
  readonly onPublished: (result: AlarmRulePublishResult) => void;
  readonly onReloadLatest: () => void;
}) {
  const [changeNote, setChangeNote] = useState('');
  const [diffResult, setDiffResult] = useState<AlarmRuleDiffResult | null>(null);

  const diffMutation = useMutation({
    mutationFn: () => diffSiteAlarmRules(siteId, draftRules.map(ruleToDraft), csrfToken),
    onSuccess: (data) => setDiffResult(data),
  });

  const publishMutation = useMutation({
    mutationFn: () => publishSiteAlarmRules(
      siteId,
      {
        expectedRevision,
        changeNote: changeNote.trim() || undefined,
        rules: draftRules.map(ruleToDraft),
      },
      csrfToken,
    ),
    onSuccess: (result) => {
      onPublished(result);
    },
  });

  const triggerDiff = diffMutation.mutate;
  const resetDiff = diffMutation.reset;
  const resetPublish = publishMutation.reset;

  useEffect(() => {
    if (!open) {
      setDiffResult(null);
      setChangeNote('');
      resetDiff();
      resetPublish();
      return;
    }
    triggerDiff();
  }, [open, triggerDiff, resetDiff, resetPublish]);

  const revisionChangedInDiff = diffResult !== null && diffResult.currentRevision !== expectedRevision;
  const conflictDetected = (publishMutation.error instanceof SiteRuleAuthoringError && publishMutation.error.status === 409)
    || revisionChangedInDiff;

  const changedEntries = diffResult?.changes.filter((entry) => entry.change !== 'UNCHANGED') ?? [];

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen && !publishMutation.isPending) onClose(); }}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>确认热发布站点告警规则？</DialogTitle>
          <DialogDescription>
            当前基线版本 v{expectedRevision}。仅新增、修改与停用的规则会更新，未改动规则保留原有持续计时与连续计数。
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          {diffMutation.isPending ? (
            <p className="text-xs text-muted-foreground">正在校验规则 DAG 拓扑结构并计算影响范围…</p>
          ) : null}

          {diffMutation.isError ? (
            <div role="alert" className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive-text">
              <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <span>{diffMutation.error instanceof Error ? diffMutation.error.message : '无法计算规则变更差异'}</span>
            </div>
          ) : null}

          {conflictDetected ? (
            <div role="alert" className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive-text">
              <span>
                {revisionChangedInDiff
                  ? `服务端最新版本号已变更为 v${diffResult.currentRevision}，当前草稿基于旧版本 v${expectedRevision}。`
                  : `服务端规则版本已在其他会话中更新，当前草稿基于旧版本 v${expectedRevision}，请重新加载最新版本。`}
              </span>
              <Button type="button" variant="outline" size="sm" onClick={onReloadLatest}>
                重新加载最新版本
              </Button>
            </div>
          ) : null}

          {diffResult ? (
            <>
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <Badge variant="outline">新增 {diffResult.summary.added}</Badge>
                <Badge variant="outline">修改 {diffResult.summary.changed}</Badge>
                <Badge variant="outline">停用 {diffResult.summary.retired}</Badge>
                <Badge variant="outline">保持不变 {diffResult.summary.unchanged}</Badge>
                <span className="text-muted-foreground">最大联动跳数：{diffResult.graph.maxHopDepth}</span>
              </div>

              {!diffResult.valid && diffResult.issues.length > 0 ? (
                <div role="alert" className="flex flex-col gap-1.5 rounded-lg border border-destructive/40 bg-destructive/10 px-3.5 py-2.5 text-xs text-destructive-text">
                  <div className="flex items-center gap-1.5 font-medium">
                    <AlertTriangle className="size-4" aria-hidden="true" />
                    <span>校验未通过（请修正以下问题后重试）</span>
                  </div>
                  <ul className="list-disc space-y-1 pl-4">
                    {diffResult.issues.map((issue, idx) => (
                      <li key={`${issue.code}-${idx}`}>{formatRuleValidationIssue(issue, titleByRuleId)}</li>
                    ))}
                  </ul>
                </div>
              ) : null}

              {changedEntries.length > 0 ? (
                <div className="flex max-h-60 flex-col gap-2 overflow-y-auto rounded-lg border border-divider p-3">
                  {changedEntries.map((entry) => (
                    <div key={`${entry.change}-${entry.ruleId}`} className="flex flex-col gap-1 border-b border-divider pb-2 last:border-b-0 last:pb-0">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="text-xs font-medium text-foreground">{entry.title}</span>
                        <div className="flex items-center gap-1.5">
                          {entry.willClearActiveAlarm ? (
                            <span className="rounded bg-warning-soft px-1.5 py-0.5 text-[11px] font-medium text-warning-text">
                              停用将自动恢复活动告警
                            </span>
                          ) : null}
                          <Badge variant={entry.change === 'RETIRED' ? 'destructive' : 'outline'}>
                            {RULE_CHANGE_LABELS[entry.change]}
                          </Badge>
                        </div>
                      </div>
                      {entry.changedFields.length > 0 ? (
                        <p className="text-[11px] text-muted-foreground">
                          变更字段：{entry.changedFields.map((f) => CHANGED_FIELD_LABELS[f] ?? f).join('、')}
                        </p>
                      ) : null}
                    </div>
                  ))}
                </div>
              ) : diffResult.valid ? (
                <div className="flex items-center gap-2 rounded-lg border border-divider bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
                  <CheckCircle2 className="size-4 text-primary" aria-hidden="true" />
                  <span>草稿内容与当前生效版本一致，无需写入新版本。</span>
                </div>
              ) : null}

              <div className="flex flex-col gap-1.5">
                <Label htmlFor="rule-publish-change-note" className="text-xs font-medium text-foreground">
                  变更说明（选填）
                </Label>
                <Input
                  id="rule-publish-change-note"
                  value={changeNote}
                  maxLength={500}
                  onChange={(e) => setChangeNote(e.target.value)}
                  placeholder="例如：上调冷机供水温度阈值并增加工单联动动作"
                />
              </div>
            </>
          ) : null}

          {publishMutation.isError && !conflictDetected ? (
            <div role="alert" className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive-text">
              <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <span>{publishMutation.error instanceof Error ? publishMutation.error.message : '热发布失败，请稍后重试'}</span>
            </div>
          ) : null}
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" disabled={publishMutation.isPending} onClick={onClose}>
            取消
          </Button>
          <Button
            type="button"
            disabled={!diffResult?.valid || conflictDetected || publishMutation.isPending}
            onClick={() => publishMutation.mutate()}
          >
            <Rocket data-icon="inline-start" className="size-3.5" aria-hidden="true" />
            {publishMutation.isPending ? '热发布中…' : '确认热发布'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
