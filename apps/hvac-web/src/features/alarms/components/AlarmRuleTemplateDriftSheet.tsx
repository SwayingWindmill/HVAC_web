import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RotateCcw } from 'lucide-react';
import { toast } from 'sonner';
import { ConfirmDiscardDialog } from '@/components/confirm-discard-dialog';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  alarmTemplateKeys,
  alarmTemplatesApi,
  describeAlarmTemplateError,
  siteAlarmRuleTemplateDriftQuery,
} from '../alarm-templates-api';

export function AlarmRuleTemplateDriftSheet({
  open,
  siteId,
  canManage,
  csrfToken,
  onClose,
  onResetCompleted,
}: {
  readonly open: boolean;
  readonly siteId: string;
  readonly canManage: boolean;
  readonly csrfToken: string;
  readonly onClose: () => void;
  readonly onResetCompleted: () => void;
}) {
  const queryClient = useQueryClient();
  const driftQuery = useQuery(siteAlarmRuleTemplateDriftQuery(siteId, open));
  const [pendingReset, setPendingReset] = useState<{ readonly ruleId: string; readonly ruleTitle: string } | null>(null);

  const resetMutation = useMutation({
    mutationFn: ({ ruleId, fields }: { readonly ruleId: string; readonly fields?: string[] }) =>
      alarmTemplatesApi.resetSiteRule(
        siteId,
        ruleId,
        { fields, changeNote: '站点运营核对后一键恢复基准模板默认值' },
        csrfToken,
      ),
    onSuccess: () => {
      setPendingReset(null);
      toast.success('已将该规则恢复为模板默认值并同步版本记录');
      void queryClient.invalidateQueries({ queryKey: alarmTemplateKeys.siteDrift(siteId) });
      onResetCompleted();
    },
    onError: (err) => {
      setPendingReset(null);
      toast.error(describeAlarmTemplateError(err, '恢复模板默认值失败'));
    },
  });

  const items = driftQuery.data?.items ?? [];

  return (
    <Sheet open={open} onOpenChange={(next) => { if (!next) onClose(); }}>
      <SheetContent side="right" className="w-full sm:max-w-[740px] overflow-y-auto">
        <SheetHeader>
          <SheetTitle>模板参数偏离核对与一键重置</SheetTitle>
          <SheetDescription>
            对照当前站点规则与关联基准模板的最新默认值；可保留个性阈值，或一键还原回模板基线。
          </SheetDescription>
        </SheetHeader>

        <div className="mt-4 flex flex-col gap-4 text-xs">
          {driftQuery.isPending ? (
            <Skeleton className="h-36 rounded-lg" />
          ) : driftQuery.isError ? (
            <Alert variant="destructive">
              <AlertDescription>{describeAlarmTemplateError(driftQuery.error, '偏离清单读取失败')}</AlertDescription>
            </Alert>
          ) : items.length === 0 ? (
            <p className="rounded-lg border border-dashed px-4 py-8 text-center text-muted-foreground">
              当前站点模板派生规则均与所属基准模板保持一致，无偏离项。
            </p>
          ) : (
            items.map((item) => (
              <section key={item.ruleId} aria-label={`规则偏离 ${item.ruleTitle}`} className="flex flex-col gap-3 rounded-lg border p-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-col gap-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-semibold">{item.ruleTitle}</span>
                      <Badge variant="outline">{item.templateName} v{item.templateVersion}</Badge>
                      {item.overriddenFields.length > 0 ? (
                        <Badge variant="warning">站点覆写 {item.overriddenFields.length} 项</Badge>
                      ) : null}
                    </div>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={!canManage || resetMutation.isPending}
                    onClick={() => setPendingReset({ ruleId: item.ruleId, ruleTitle: item.ruleTitle })}
                  >
                    <RotateCcw data-icon="inline-start" aria-hidden="true" />
                    重置为模板默认值
                  </Button>
                </div>

                <Table aria-label={`${item.ruleTitle} 参数偏离对照表`}>
                  <TableHeader>
                    <TableRow>
                      <TableHead>参数项</TableHead>
                      <TableHead>站点当前值</TableHead>
                      <TableHead>基准模板值</TableHead>
                      <TableHead>状态</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {item.diffs.filter((d) => d.drifted || d.isOverridden).map((d) => (
                      <TableRow key={d.field}>
                        <TableCell className="font-medium">{d.label}</TableCell>
                        <TableCell className="font-mono">{d.siteValue || '—'}</TableCell>
                        <TableCell className="font-mono text-muted-foreground">{d.templateValue || '—'}</TableCell>
                        <TableCell>
                          {d.isOverridden ? (
                            <Badge variant="warning">站点覆写</Badge>
                          ) : d.drifted ? (
                            <Badge variant="outline">待同步</Badge>
                          ) : (
                            <Badge variant="outline">一致</Badge>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </section>
            ))
          )}
        </div>
      </SheetContent>
      <ConfirmDiscardDialog
        open={pendingReset !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setPendingReset(null);
        }}
        busy={resetMutation.isPending}
        title={pendingReset ? `确认将「${pendingReset.ruleTitle}」重置为模板默认值？` : ''}
        description="重置后将覆盖本站点针对该规则的个性化阈值设定，并生成新的规则版本记录。"
        keepLabel="取消"
        discardLabel="确认重置"
        busyLabel="重置中…"
        onConfirmDiscard={() => {
          if (pendingReset) resetMutation.mutate({ ruleId: pendingReset.ruleId });
        }}
      />
    </Sheet>
  );
}
