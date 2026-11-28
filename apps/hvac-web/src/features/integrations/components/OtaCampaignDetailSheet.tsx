import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Pause, Play, RefreshCw, RotateCcw } from 'lucide-react';
import { notifySuccess } from '@/lib/toast';
import type { OTACampaignDetail, Site } from '@/api/generated/platformGateway.gen';
import { FactTile } from '@/blocks/fact-list';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
import { formatTagEntries } from '@/features/system/registry-admin/tags-model';
import {
  OTA_ARCH_LABELS,
  OTA_CAMPAIGN_STATUS_PRESENTATION,
  OTA_COMPONENT_LABELS,
  OTA_TASK_ACTION_LABELS,
  OTA_TASK_STAGE_PRESENTATION,
  OTA_TASK_WAVE_LABELS,
  describeOtaError,
  formatByteSize,
  formatOtaPauseReason,
  isOTACampaignActive,
} from '../ota-model';
import { otaApi, otaCampaignDetailQuery, otaKeys } from '../ota-query';
import { GovernanceBadge } from './GovernanceShared';

type CampaignActionKind = 'pause' | 'resume' | 'rollback';

const dateTimeFormat = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
});

function formatTime(iso: string | null | undefined): string {
  if (!iso) return '—';
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return '—';
  return dateTimeFormat.format(new Date(parsed));
}

function CampaignActionDialog({
  action,
  detail,
  reason,
  confirmed,
  isPending,
  error,
  onReasonChange,
  onConfirmedChange,
  onClose,
  onConfirm,
}: {
  readonly action: CampaignActionKind | null;
  readonly detail: OTACampaignDetail | null;
  readonly reason: string;
  readonly confirmed: boolean;
  readonly isPending: boolean;
  readonly error: unknown;
  readonly onReasonChange: (value: string) => void;
  readonly onConfirmedChange: (value: boolean) => void;
  readonly onClose: () => void;
  readonly onConfirm: () => void;
}) {
  const meta =
    action === 'rollback'
      ? {
          title: '紧急回滚这次发布？',
          description: `将取消 ${detail?.name ?? '当前任务'} 中尚未执行的待下发网关任务，并向已下发或已切换版本的在线网关发送回滚指令以切回上一可用槽位。`,
          confirmLabel: isPending ? '正在下发回滚…' : '确认回滚',
          reasonLabel: '回滚原因（可选）',
          reasonPlaceholder: '例如：灰度节点健康检查失败，先切回上一版本',
          destructive: true,
        }
      : action === 'pause'
        ? {
            title: '暂停这次发布任务？',
            description: '将停止向其余待升级网关推送升级阶段，已完成节点保持当前版本。可随时恢复放量或发起回滚。',
            confirmLabel: isPending ? '正在暂停…' : '确认暂停',
            reasonLabel: '暂停原因（可选）',
            reasonPlaceholder: '例如：等待现场值班确认后再放量',
            destructive: false,
          }
        : {
            title:
              detail?.pauseReason === 'CANARY_AWAITING_PROMOTION'
                ? '放量到全量批网关？'
                : '恢复这次发布任务？',
            description:
              detail?.pauseReason === 'CANARY_AWAITING_PROMOTION'
                ? '灰度批网关已完成升级。继续后将向全量批网关下发升级任务。'
                : '将继续向待执行的网关下发目标版本，请确认异常网关问题已排查或可接受。',
            confirmLabel: isPending
              ? '正在执行…'
              : detail?.pauseReason === 'CANARY_AWAITING_PROMOTION'
                ? '确认放量'
                : '恢复发布',
            reasonLabel: '操作说明（可选）',
            reasonPlaceholder: '例如：现场已核对运行状态，继续升级其余网关',
            destructive: false,
          };

  const canConfirm = action === 'rollback' ? confirmed : true;

  return (
    <AlertDialog
      open={Boolean(action)}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !isPending) onClose();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{meta.title}</AlertDialogTitle>
          <AlertDialogDescription>{meta.description}</AlertDialogDescription>
        </AlertDialogHeader>

        <div className="space-y-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ota-campaign-action-reason" className="text-xs font-medium">
              {meta.reasonLabel}
            </Label>
            <Input
              id="ota-campaign-action-reason"
              value={reason}
              maxLength={1000}
              placeholder={meta.reasonPlaceholder}
              disabled={isPending}
              onChange={(event) => onReasonChange(event.target.value)}
              onKeyDown={(event) => {
                if (event.key !== 'Enter') return;
                if (event.nativeEvent.isComposing || event.keyCode === 229) {
                  event.preventDefault();
                  return;
                }
                event.preventDefault();
              }}
            />
          </div>

          {action === 'rollback' ? (
            <div className="flex items-start gap-2 rounded-md border border-divider bg-muted/40 p-2.5">
              <Checkbox
                id="ota-rollback-confirm-checkbox"
                checked={confirmed}
                disabled={isPending}
                onCheckedChange={(checked) => onConfirmedChange(checked === true)}
              />
              <Label htmlFor="ota-rollback-confirm-checkbox" className="cursor-pointer text-xs leading-5 font-normal">
                我已确认受影响网关范围，并同意取消待执行节点、下发回滚指令。
              </Label>
            </div>
          ) : null}

          {error ? (
            <Alert variant="destructive">
              <AlertDescription>{describeOtaError(error, '操作未完成，请稍后重试。')}</AlertDescription>
            </Alert>
          ) : null}
        </div>

        <AlertDialogFooter>
          <AlertDialogCancel disabled={isPending}>取消</AlertDialogCancel>
          <AlertDialogAction
            variant={meta.destructive ? 'destructive' : 'default'}
            disabled={isPending || !canConfirm}
            onClick={(event) => {
              event.preventDefault();
              onConfirm();
            }}
          >
            {meta.confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function OtaCampaignDetailSheet({
  campaignId,
  sites,
  canWrite,
  csrfToken,
  onClose,
}: {
  readonly campaignId: string | undefined;
  readonly sites: readonly Site[];
  readonly canWrite: boolean;
  readonly csrfToken: string;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const detailQuery = useQuery(otaCampaignDetailQuery(campaignId, Boolean(campaignId)));

  const [pendingDialog, setPendingDialog] = useState<CampaignActionKind | null>(null);
  const [actionReason, setActionReason] = useState('');
  const [rollbackConfirmed, setRollbackConfirmed] = useState(false);

  const actionMutation = useMutation({
    mutationFn: async (input: { kind: CampaignActionKind; id: string; reason: string }) => {
      if (input.kind === 'pause') return otaApi.pauseCampaign(input.id, input.reason, csrfToken);
      if (input.kind === 'resume') return otaApi.resumeCampaign(input.id, input.reason, csrfToken);
      return otaApi.rollbackCampaign(input.id, input.reason, csrfToken);
    },
    onSuccess: (updated, variables) => {
      queryClient.setQueryData(otaKeys.campaignDetail(updated.id), updated);
      void queryClient.invalidateQueries({ queryKey: otaKeys.campaigns() });
      const labels: Readonly<Record<CampaignActionKind, string>> = {
        pause: '已暂停发布任务',
        resume: '已恢复发布任务',
        rollback: '已下发紧急回滚指令',
      };
      notifySuccess(labels[variables.kind]);
      setPendingDialog(null);
      setActionReason('');
      setRollbackConfirmed(false);
    },
  });

  const siteNameById = new Map(sites.map((site) => [site.id, site.displayName]));
  const detail = detailQuery.data ?? null;
  const active = detail ? isOTACampaignActive(detail.status) : false;
  const pauseText = detail ? formatOtaPauseReason(detail.pauseReason) : null;
  const formattedTags = detail ? formatTagEntries(detail.targetTags) : [];

  return (
    <Sheet
      open={Boolean(campaignId)}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) {
          setPendingDialog(null);
          setActionReason('');
          setRollbackConfirmed(false);
          actionMutation.reset();
          onClose();
        }
      }}
    >
      <SheetContent className="overflow-y-auto p-0 data-[side=right]:sm:max-w-[720px]">
        <SheetHeader className="border-b border-divider px-6 py-5">
          <div className="flex flex-wrap items-center justify-between gap-2 pr-8">
            <div className="min-w-0 space-y-1">
              <SheetTitle className="truncate" title={detail?.name}>
                {detail ? detail.name : '发布任务详情'}
              </SheetTitle>
              <SheetDescription>
                {detail
                  ? `${OTA_COMPONENT_LABELS[detail.package.component]} v${detail.package.version} · ${OTA_ARCH_LABELS[detail.package.targetArch]}`
                  : '查看每台网关的升级阶段、健康检查结果与灰度放量控制'}
              </SheetDescription>
            </div>
            {detail ? (
              <GovernanceBadge status={OTA_CAMPAIGN_STATUS_PRESENTATION[detail.status]} />
            ) : null}
          </div>
        </SheetHeader>

        <p className="sr-only" role="status" aria-live="polite">
          {detail
            ? `发布任务 ${detail.name} 当前状态为${OTA_CAMPAIGN_STATUS_PRESENTATION[detail.status].label}，成功 ${detail.succeededCount} 台，共 ${detail.totalCount} 台`
            : ''}
        </p>

        {detailQuery.isPending ? (
          <div className="space-y-4 p-6">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-48 w-full" />
          </div>
        ) : detailQuery.isError || !detail ? (
          <div className="p-6">
            <Alert variant="destructive">
              <AlertTitle>无法读取发布任务详情</AlertTitle>
              <AlertDescription className="mt-2 flex items-center justify-between gap-3">
                <span>{describeOtaError(detailQuery.error, '请检查网络连接或设备读取权限后重试。')}</span>
                <Button type="button" variant="outline" size="sm" onClick={() => void detailQuery.refetch()}>
                  重试
                </Button>
              </AlertDescription>
            </Alert>
          </div>
        ) : (
          <div className="flex flex-col">
            {detail.pauseReason === 'FAILURE_RATE_THRESHOLD_EXCEEDED' ? (
              <div className="border-b border-divider px-6 py-4">
                <Alert variant="destructive">
                  <AlertTitle>已触发失败率自动熔断</AlertTitle>
                  <AlertDescription>
                    当前失败率为 {detail.failureRatePct}%（熔断阈值 {detail.failureRateThresholdPct}%），系统已自动暂停后续放量。请先排查下方失败网关原因，再选择恢复发布或紧急回滚。
                  </AlertDescription>
                </Alert>
              </div>
            ) : pauseText ? (
              <div className="border-b border-divider px-6 py-4">
                <Alert>
                  <AlertTitle>任务已暂停</AlertTitle>
                  <AlertDescription>{pauseText}</AlertDescription>
                </Alert>
              </div>
            ) : null}

            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-divider px-6 py-3">
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span>
                  {active ? '任务进行中，每 5 秒自动刷新进度' : `最近更新：${formatTime(detail.updatedAt)}`}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={detailQuery.isFetching}
                  onClick={() => void detailQuery.refetch()}
                >
                  <RefreshCw data-icon="inline-start" className={detailQuery.isFetching ? 'motion-safe:animate-spin' : undefined} />
                  刷新
                </Button>
              </div>

              {canWrite ? (
                <div className="flex flex-wrap items-center gap-2">
                  {active ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        setActionReason('');
                        actionMutation.reset();
                        setPendingDialog('pause');
                      }}
                    >
                      <Pause data-icon="inline-start" />
                      暂停发布
                    </Button>
                  ) : null}

                  {detail.status === 'PAUSED' ? (
                    <Button
                      type="button"
                      size="sm"
                      onClick={() => {
                        setActionReason('');
                        actionMutation.reset();
                        setPendingDialog('resume');
                      }}
                    >
                      <Play data-icon="inline-start" />
                      {detail.pauseReason === 'CANARY_AWAITING_PROMOTION' ? '继续放量' : '恢复发布'}
                    </Button>
                  ) : null}

                  {detail.status !== 'ROLLED_BACK' ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="text-destructive-text"
                      onClick={() => {
                        setActionReason('');
                        setRollbackConfirmed(false);
                        actionMutation.reset();
                        setPendingDialog('rollback');
                      }}
                    >
                      <RotateCcw data-icon="inline-start" />
                      紧急回滚…
                    </Button>
                  ) : null}
                </div>
              ) : null}
            </div>

            <dl className="grid grid-cols-1 gap-4 border-b border-divider px-6 py-4 sm:grid-cols-3">
              <FactTile label="升级包与大小">
                <span className="font-mono">
                  v{detail.package.version} · {formatByteSize(detail.package.fileSizeBytes)}
                </span>
              </FactTile>
              <FactTile label="灰度与熔断">
                <span className="font-mono">
                  灰度 {detail.canaryPercent}% · 熔断 {detail.failureRateThresholdPct}%
                </span>
              </FactTile>
              <FactTile label="进度统计">
                <span className="font-mono">
                  成功 {detail.succeededCount}/{detail.totalCount} · 失败 {detail.failedCount} · 回滚 {detail.rolledBackCount}
                </span>
              </FactTile>
              <FactTile label="失败率">
                <span className={detail.failureRatePct >= detail.failureRateThresholdPct ? 'font-mono text-destructive-text' : 'font-mono'}>
                  {detail.failureRatePct}%
                </span>
              </FactTile>
              <FactTile label="健康检查超时">
                <span className="font-mono">{detail.healthCheckTimeoutSec} 秒</span>
              </FactTile>
              <FactTile label="自动放量">
                {detail.autoPromoteRollout ? '灰度成功后自动放量' : '需人工确认放量'}
              </FactTile>
              <div className="sm:col-span-3">
                <FactTile label="目标站点与标签">
                  {detail.targetSiteIds.length > 0
                    ? detail.targetSiteIds.map((id) => siteNameById.get(id) ?? id).join('、')
                    : '全部授权站点'}
                  {formattedTags.length > 0 ? ` · 标签：${formattedTags.join(' , ')}` : ''}
                </FactTile>
              </div>
              <div className="sm:col-span-3">
                <FactTile label="发布原因">{detail.reason}</FactTile>
              </div>
              <div className="sm:col-span-3">
                <FactTile label="校验指纹与签名密钥" valueClassName="font-mono text-xs break-all font-normal text-muted-foreground">
                  SHA-256: {detail.package.sha256} · 密钥 ID: {detail.package.signingKeyId}
                </FactTile>
              </div>
            </dl>

            <section className="space-y-3 px-6 py-4">
              <div className="flex items-center justify-between gap-2">
                <h3 className="text-sm font-semibold">网关执行进度（{detail.tasks.length} 台）</h3>
                <span className="text-xs text-muted-foreground tabular-nums">
                  进行中 {detail.inProgressCount} · 等待 {detail.pendingCount} · 已取消 {detail.cancelledCount}
                </span>
              </div>

              {detail.tasks.length === 0 ? (
                <p className="rounded-lg border border-divider bg-muted/30 px-4 py-6 text-center text-xs text-muted-foreground">
                  本次发布没有分配到网关设备任务。
                </p>
              ) : (
                <div className="max-h-96 overflow-auto rounded-lg border border-divider">
                  <Table aria-label="网关升级任务">
                    <TableHeader>
                      <TableRow>
                        <TableHead className="pl-3">网关</TableHead>
                        <TableHead>批次与动作</TableHead>
                        <TableHead>阶段与进度</TableHead>
                        <TableHead>版本</TableHead>
                        <TableHead className="pr-3">异常说明</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {detail.tasks.map((task) => {
                        const stagePresentation = OTA_TASK_STAGE_PRESENTATION[task.stage];
                        return (
                          <TableRow key={task.id}>
                            <TableCell className="pl-3 align-top">
                              <div className="flex max-w-56 min-w-0 flex-col gap-0.5">
                                <span
                                  className="truncate font-medium"
                                  title={task.gatewayName || task.gatewayCode}
                                >
                                  {task.gatewayName || task.gatewayCode}
                                </span>
                                <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                                  <span
                                    className={
                                      task.gatewayOnline
                                        ? 'inline-flex shrink-0 items-center gap-1 whitespace-nowrap text-success-text'
                                        : 'inline-flex shrink-0 items-center gap-1 whitespace-nowrap text-muted-foreground'
                                    }
                                  >
                                    <span
                                      className={
                                        task.gatewayOnline
                                          ? 'size-1.5 shrink-0 rounded-full bg-success'
                                          : 'size-1.5 shrink-0 rounded-full bg-subtle-foreground'
                                      }
                                      aria-hidden="true"
                                    />
                                    {task.gatewayOnline ? '在线' : '离线'}
                                  </span>
                                  <span className="min-w-0 truncate font-mono" title={task.gatewayCode}>
                                    {task.gatewayCode}
                                  </span>
                                </div>
                              </div>
                            </TableCell>
                            <TableCell className="align-top whitespace-nowrap text-xs">
                              <div>{OTA_TASK_WAVE_LABELS[task.wave]}</div>
                              <div className="text-muted-foreground">{OTA_TASK_ACTION_LABELS[task.desiredAction]}</div>
                            </TableCell>
                            <TableCell className="align-top">
                              <div className="flex flex-col gap-1.5">
                                <GovernanceBadge status={stagePresentation} />
                                <div className="flex items-center gap-2">
                                  <progress
                                    max={100}
                                    value={task.progressPct}
                                    aria-label={`${task.gatewayName || task.gatewayCode} 进度 ${task.progressPct}%`}
                                    className="h-2 w-20 accent-primary"
                                  />
                                  <span className="font-mono text-xs text-muted-foreground tabular-nums">
                                    {task.progressPct}%
                                  </span>
                                </div>
                              </div>
                            </TableCell>
                            <TableCell className="align-top font-mono text-xs whitespace-nowrap tabular-nums">
                              <div>{task.currentVersion || '—'}</div>
                              <div className="text-muted-foreground">→ {task.targetVersion}</div>
                            </TableCell>
                            <TableCell className="max-w-52 pr-3 align-top text-xs">
                              {task.errorMessage ? (
                                <span className="break-words text-destructive-text">{task.errorMessage}</span>
                              ) : (
                                <span className="text-muted-foreground">—</span>
                              )}
                            </TableCell>
                          </TableRow>
                        );
                      })}
                    </TableBody>
                  </Table>
                </div>
              )}
            </section>
          </div>
        )}

        <CampaignActionDialog
          action={pendingDialog}
          detail={detail}
          reason={actionReason}
          confirmed={rollbackConfirmed}
          isPending={actionMutation.isPending}
          error={actionMutation.error}
          onReasonChange={setActionReason}
          onConfirmedChange={setRollbackConfirmed}
          onClose={() => {
            setPendingDialog(null);
            actionMutation.reset();
          }}
          onConfirm={() => {
            if (!pendingDialog || !detail) return;
            actionMutation.mutate({ kind: pendingDialog, id: detail.id, reason: actionReason });
          }}
        />
      </SheetContent>
    </Sheet>
  );
}
