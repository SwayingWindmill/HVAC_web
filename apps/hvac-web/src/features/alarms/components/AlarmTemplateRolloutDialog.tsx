import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, CheckCircle2, Rocket, ShieldCheck } from 'lucide-react';
import { toast } from 'sonner';
import type { Site } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import {
  TEMPLATE_FIELD_LABELS,
  alarmTemplateKeys,
  alarmTemplatesApi,
  describeAlarmTemplateError,
  type AlarmRuleBaselineTemplate,
  type AlarmRuleTemplateRolloutResult,
} from '../alarm-templates-api';

const ROLLOUT_ACTION_LABELS: Readonly<Record<'UPDATE' | 'CREATE' | 'UNCHANGED' | 'SKIPPED', string>> = {
  UPDATE: '待更新',
  CREATE: '待新增',
  UNCHANGED: '无变更',
  SKIPPED: '已跳过',
};

export function AlarmTemplateRolloutDialog({
  open,
  template,
  sites,
  canManage,
  csrfToken,
  onClose,
}: {
  readonly open: boolean;
  readonly template: AlarmRuleBaselineTemplate | null;
  readonly sites: readonly Site[];
  readonly canManage: boolean;
  readonly csrfToken: string;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [selectedSiteIds, setSelectedSiteIds] = useState<string[]>(
    () => (template?.boundSiteIds && template.boundSiteIds.length > 0 ? [...template.boundSiteIds] : sites.map((s) => s.id)),
  );
  const [changeNote, setChangeNote] = useState('跨站基准版本差异预检后受控下发');
  const [rolloutResult, setRolloutResult] = useState<AlarmRuleTemplateRolloutResult | null>(null);

  const siteNames = useMemo(() => new Map(sites.map((s) => [s.id, s.displayName])), [sites]);

  const previewQuery = useQuery({
    queryKey: ['alarm-template-rollout-preview', template?.templateId ?? '', template?.latestVersion ?? 0, selectedSiteIds.join(',')],
    queryFn: () =>
      alarmTemplatesApi.previewRollout(template!.templateId, {
        version: template!.latestVersion,
        siteIds: selectedSiteIds,
      }),
    enabled: open && Boolean(template) && (template?.latestVersion ?? 0) > 0 && selectedSiteIds.length > 0,
  });

  const bindSitesMutation = useMutation({
    mutationFn: () => alarmTemplatesApi.updateBoundSites(template!.templateId, selectedSiteIds, csrfToken),
    onSuccess: () => {
      toast.success('模板绑定站点范围已保存');
      void queryClient.invalidateQueries({ queryKey: alarmTemplateKeys.all });
    },
    onError: (err) => toast.error(describeAlarmTemplateError(err, '保存绑定站点失败')),
  });

  const rolloutMutation = useMutation({
    mutationFn: () =>
      alarmTemplatesApi.executeRollout(
        template!.templateId,
        { version: template!.latestVersion, siteIds: selectedSiteIds, changeNote: changeNote.trim() || undefined },
        csrfToken,
      ),
    onSuccess: (res) => {
      setRolloutResult(res);
      toast.success(`已完成下发：同步 ${res.totalAppliedSites} 个站点，保留 ${res.totalPreservedOverrides} 处站点覆写`);
      void queryClient.invalidateQueries({ queryKey: alarmTemplateKeys.all });
      for (const sid of selectedSiteIds) {
        void queryClient.invalidateQueries({ queryKey: ['alarms', sid, 'rules'] });
      }
    },
    onError: (err) => toast.error(describeAlarmTemplateError(err, '受控下发失败')),
  });

  const toggleSite = (siteId: string) => {
    setSelectedSiteIds((prev) => (prev.includes(siteId) ? prev.filter((id) => id !== siteId) : [...prev, siteId]));
  };

  if (!template) return null;
  const preview = previewQuery.data ?? null;

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent className="sm:max-w-[760px]">
        <DialogHeader>
          <DialogTitle>多站点差异预检与受控下发 · {template.displayName}</DialogTitle>
          <DialogDescription>
            基准版本 v{template.latestVersion} · 下发会自动保留站点个性化覆写并高亮冲突项。
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 max-h-[65vh] overflow-y-auto pr-1 text-xs">
          <section aria-label="目标站点范围" className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3">
            <div className="flex items-center justify-between gap-2">
              <span className="font-semibold">绑定与下发目标站点</span>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!canManage || bindSitesMutation.isPending}
                onClick={() => bindSitesMutation.mutate()}
              >
                保存绑定范围
              </Button>
            </div>
            <div className="flex flex-wrap gap-3">
              {sites.map((s) => (
                <label key={s.id} className="inline-flex items-center gap-1.5 cursor-pointer">
                  <Checkbox checked={selectedSiteIds.includes(s.id)} onCheckedChange={() => toggleSite(s.id)} />
                  <span>{s.displayName}</span>
                </label>
              ))}
            </div>
          </section>

          {previewQuery.isPending ? (
            <Skeleton className="h-32 rounded-lg" />
          ) : previewQuery.isError ? (
            <Alert variant="destructive">
              <AlertDescription>{describeAlarmTemplateError(previewQuery.error, '无法计算多站点差异预览')}</AlertDescription>
            </Alert>
          ) : preview ? (
            <section aria-label="多站点差异预览" className="flex flex-col gap-3">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="outline">预计更新站点 {preview.totalChangedSites}</Badge>
                <Badge variant="outline">
                  <ShieldCheck className="mr-1 size-3" aria-hidden="true" />
                  自动保留站点覆写 {preview.totalPreservedOverrides} 处
                </Badge>
                {preview.totalConflicts > 0 ? (
                  <Badge variant="warning">
                    <AlertTriangle className="mr-1 size-3" aria-hidden="true" />
                    冲突高亮 {preview.totalConflicts} 项
                  </Badge>
                ) : null}
              </div>

              {preview.sites.map((sitePrev) => (
                <div key={sitePrev.siteId} className="flex flex-col gap-2 rounded-lg border p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-semibold">{siteNames.get(sitePrev.siteId) ?? sitePrev.siteId}</span>
                      <Badge variant="outline" className="font-mono">规则包 v{sitePrev.bundleRevision}</Badge>
                    </div>
                    <span className="text-muted-foreground tabular-nums">
                      匹配 {sitePrev.matchedRuleCount} 条 · 更新 {sitePrev.willUpdateCount} · 保留覆写 {sitePrev.preservedOverrideCount}
                    </span>
                  </div>
                  {sitePrev.note ? <p className="text-muted-foreground">{sitePrev.note}</p> : null}
                  {sitePrev.ruleDiffs.map((rd, idx) => (
                    <div key={`${sitePrev.siteId}-${rd.ruleId || idx}`} className="flex flex-col gap-1.5 rounded-md border bg-muted/20 p-2.5">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="font-medium">{rd.ruleTitle}</span>
                        <Badge variant={rd.action === 'UPDATE' ? 'default' : 'outline'}>{ROLLOUT_ACTION_LABELS[rd.action] ?? rd.action}</Badge>
                      </div>
                      {rd.changedFields.length > 0 ? (
                        <div className="text-muted-foreground">
                          同步更新字段：{rd.changedFields.map((f) => TEMPLATE_FIELD_LABELS[f] ?? f).join('、')}
                        </div>
                      ) : null}
                      {rd.preservedOverrides.length > 0 ? (
                        <div className="text-primary">
                          已保留站点覆写：{rd.preservedOverrides.map((f) => TEMPLATE_FIELD_LABELS[f] ?? f).join('、')}
                        </div>
                      ) : null}
                      {rd.conflicts.map((cf, cIdx) => (
                        <div
                          key={`${cf.ruleId}-${cf.field}-${cIdx}`}
                          className="flex flex-wrap items-center justify-between gap-2 rounded border border-warning/40 bg-warning/10 px-2.5 py-1.5 text-xs"
                        >
                          <span>
                            冲突字段「{cf.fieldLabel}」：站点覆写 <span className="font-mono font-semibold">{cf.siteValue}</span> vs 模板基线{' '}
                            <span className="font-mono">{cf.templateValue}</span>
                          </span>
                          <Badge variant="warning">保留站点值</Badge>
                        </div>
                      ))}
                    </div>
                  ))}
                </div>
              ))}
            </section>
          ) : null}

          {rolloutResult ? (
            <Alert>
              <CheckCircle2 className="size-4 text-primary" />
              <AlertDescription>
                下发批次已完成：已同步 {rolloutResult.totalAppliedSites} 个站点（保留 {rolloutResult.totalPreservedOverrides} 处覆写），版本记录均已记入「跨站模板下发」。
              </AlertDescription>
            </Alert>
          ) : null}

          <div className="flex flex-col gap-1">
            <label htmlFor="rollout-note" className="font-medium">下发审计说明</label>
            <Input id="rollout-note" value={changeNote} onChange={(e) => setChangeNote(e.target.value)} />
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>关闭</Button>
          <Button
            type="button"
            disabled={!canManage || rolloutMutation.isPending || selectedSiteIds.length === 0 || template.latestVersion === 0}
            onClick={() => rolloutMutation.mutate()}
          >
            <Rocket data-icon="inline-start" aria-hidden="true" />
            {rolloutMutation.isPending ? '正在下发…' : '确认受控下发'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
