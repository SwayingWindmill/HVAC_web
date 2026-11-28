import { useId } from 'react';
import { Panel, PanelHeader } from '@/blocks/panel';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import type { ConcentratorAuditItem } from '@/api/generated/platformGateway.gen';
import { type ConcentratorEidDiffItem } from '../concentrator-config-model';
import {
  CONCENTRATOR_AUDIT_OPERATION_LABELS,
  CONCENTRATOR_AUDIT_OUTCOME_PRESENTATION,
  describeConcentratorError,
  formatConcentratorEidDisplay,
} from '../concentrator-model';
import { GovernanceBadge } from './GovernanceShared';

function formatAuditTime(iso: string): string {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}

export function ConcentratorAuditPanel({
  loading,
  audits,
}: {
  readonly loading: boolean;
  readonly audits: readonly ConcentratorAuditItem[];
}) {
  return (
    <Panel aria-label="近期集中器变更记录">
      <PanelHeader
        title="近期集中器变更记录"
        caption="记录参数调整与 EID 槽位映射下发到 Flash 的审计结果"
      />
      {loading ? (
        <div className="p-5">
          <Skeleton className="h-28 w-full" />
        </div>
      ) : audits.length === 0 ? (
        <div className="px-5 py-8 text-center text-sm text-muted-foreground">
          当前集中器尚无下发变更记录
        </div>
      ) : (
        <div className="overflow-x-auto">
          <Table aria-label="近期集中器变更记录">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">时间</TableHead>
                <TableHead>操作类型</TableHead>
                <TableHead>站号</TableHead>
                <TableHead>变更原因</TableHead>
                <TableHead className="pr-5 text-right">执行结果</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {audits.map((audit) => (
                <TableRow key={audit.id}>
                  <TableCell className="pl-5 font-mono text-xs whitespace-nowrap tabular-nums">
                    {formatAuditTime(audit.occurredAt)}
                  </TableCell>
                  <TableCell className="text-xs font-medium whitespace-nowrap">
                    {CONCENTRATOR_AUDIT_OPERATION_LABELS[audit.operation]}
                  </TableCell>
                  <TableCell className="font-mono text-xs tabular-nums">
                    UID {audit.unitId}
                  </TableCell>
                  <TableCell className="max-w-xs truncate text-xs text-muted-foreground" title={audit.reason}>
                    {audit.reason}
                  </TableCell>
                  <TableCell className="pr-5 text-right whitespace-nowrap">
                    <GovernanceBadge
                      status={CONCENTRATOR_AUDIT_OUTCOME_PRESENTATION[audit.outcome]}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </Panel>
  );
}

export function ConcentratorEidConfirmDialog({
  open,
  onOpenChange,
  submitting,
  eidDiff,
  reason,
  onReasonChange,
  stageIfOffline,
  onStageIfOfflineChange,
  submitError,
  canCommit,
  onConfirm,
}: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly submitting: boolean;
  readonly eidDiff: readonly ConcentratorEidDiffItem[];
  readonly reason: string;
  readonly onReasonChange: (next: string) => void;
  readonly stageIfOffline: boolean;
  readonly onStageIfOfflineChange: (next: boolean) => void;
  readonly submitError: unknown;
  readonly canCommit: boolean;
  readonly onConfirm: () => Promise<void>;
}) {
  const reasonId = useId();
  const stageIfOfflineId = useId();

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!submitting) onOpenChange(nextOpen);
      }}
    >
      <DialogContent
        className="sm:max-w-xl"
        showCloseButton={!submitting}
        onEscapeKeyDown={(event) => {
          if (submitting) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (submitting) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (submitting) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>确认写入 EID 槽位映射并持久化到 Flash？</DialogTitle>
          <DialogDescription>
            本次将仅下发以下 {eidDiff.length} 个变更槽位（按连续区段采用 Modbus FC16 写入），随后执行
            0x9C41 = 8 提交至集中器 Flash 永久生效。
          </DialogDescription>
        </DialogHeader>

        {eidDiff.length > 0 ? (
          <div className="max-h-60 overflow-y-auto rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>槽位号</TableHead>
                  <TableHead>原 EID</TableHead>
                  <TableHead>新 EID</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {eidDiff.map((change) => (
                  <TableRow key={change.slot}>
                    <TableCell className="font-mono text-xs font-medium tabular-nums">
                      槽位 {change.slot}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground tabular-nums">
                      {formatConcentratorEidDisplay(change.beforeEid)}
                    </TableCell>
                    <TableCell className="font-mono text-xs font-semibold text-foreground tabular-nums">
                      {formatConcentratorEidDisplay(change.afterEid)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : null}

        <div className="flex flex-col gap-2">
          <Label htmlFor={reasonId} className="text-xs font-medium">
            变更原因（必填）
          </Label>
          <Textarea
            id={reasonId}
            value={reason}
            disabled={submitting}
            maxLength={512}
            onChange={(event) => onReasonChange(event.currentTarget.value)}
            placeholder="请填写本次 EID 槽位调整原因（如：松雅轩现场补绑 CH24 新装无线温度探头 EID 101）"
          />
        </div>

        <div className="flex items-start gap-2.5 rounded-md border bg-muted/30 px-3 py-2.5">
          <Checkbox
            id={stageIfOfflineId}
            checked={stageIfOffline}
            disabled={submitting}
            onCheckedChange={(checked) => onStageIfOfflineChange(checked === true)}
            className="mt-0.5"
          />
          <Label htmlFor={stageIfOfflineId} className="flex flex-col items-start gap-0.5 text-xs font-normal leading-relaxed">
            <span className="font-medium text-foreground">若网关离线，暂存至配置影子（重连后自动同步）</span>
            <span className="text-muted-foreground">
              勾选后当网关通信通道不可达时，EID 映射变更会先写入配置影子并在网关重连后分段写入集中器 Flash。
            </span>
          </Label>
        </div>

        {submitError ? (
          <Alert variant="destructive">
            <AlertDescription>
              {describeConcentratorError(
                submitError,
                '200 槽 EID 映射写入集中器 Flash 失败，请核对网关连接与槽位参数后重试',
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        <DialogFooter>
          <Button
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button disabled={!canCommit || submitting || reason.trim().length === 0} onClick={() => void onConfirm()}>
            {submitting ? <Spinner data-icon="inline-start" /> : null}
            {submitting ? '正在提交…' : stageIfOffline ? '确认写入或离线暂存' : '确认写入 EID 到 Flash'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
