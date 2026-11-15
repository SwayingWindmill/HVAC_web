import { useEffect, useId, useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { Eraser, RotateCcw, Save, Search, Wand2, X } from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { Panel, PanelHeader } from '@/blocks/panel';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { Alert, AlertDescription } from '@/components/ui/alert';
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
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';
import type {
  ConcentratorAuditItem,
  ConcentratorEidSlot,
} from '@/api/generated/platformGateway.gen';
import {
  CONCENTRATOR_AUDIT_OPERATION_LABELS,
  CONCENTRATOR_AUDIT_OUTCOME_PRESENTATION,
  MAX_CONCENTRATOR_EID,
  applyConcentratorEidBatchFill,
  buildConcentratorEidDiff,
  describeConcentratorError,
  detectDuplicateNonZeroEids,
  formatConcentratorEidDisplay,
  type ConcentratorChannelRow,
  type GovernanceStatusPresentation,
} from '../integrations-model';
import { GovernanceBadge } from './GovernanceShared';

type EidSlotFilter = 'ALL' | 'BOUND' | 'EMPTY' | 'CONFLICT';

const EID_FILTERS: readonly { readonly value: EidSlotFilter; readonly label: string }[] = [
  { value: 'ALL', label: '全部 200 槽' },
  { value: 'BOUND', label: '已绑定' },
  { value: 'EMPTY', label: '空槽位 (0)' },
  { value: 'CONFLICT', label: '冲突槽位' },
];

const EID_STATUS_CONFLICT: GovernanceStatusPresentation = {
  level: 'CRITICAL',
  shape: '◆',
  tone: 'danger',
  label: 'EID 冲突',
};

const EID_STATUS_BOUND: GovernanceStatusPresentation = {
  level: 'NORMAL',
  shape: '○',
  tone: 'success',
  label: '已绑定',
};

const EID_STATUS_UNBOUND: GovernanceStatusPresentation = {
  level: 'NOTICE',
  shape: '■',
  tone: 'neutral',
  label: '未绑定',
};

interface EidTableRow {
  readonly slot: number;
  readonly eid: number;
  readonly originalEid: number;
  readonly isConflict: boolean;
  readonly channelTemperatureC: number | null;
}

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

export interface ConcentratorEidSectionProps {
  readonly slots: readonly ConcentratorEidSlot[] | null;
  readonly channelRows: readonly ConcentratorChannelRow[];
  readonly audits: readonly ConcentratorAuditItem[];
  readonly loading: boolean;
  readonly canWrite: boolean;
  readonly submitting: boolean;
  readonly submitError: unknown;
  readonly onResetError: () => void;
  readonly onSubmitEids: (
    slots: readonly ConcentratorEidSlot[],
    reason: string,
  ) => Promise<void>;
}

export function ConcentratorEidSection({
  slots,
  channelRows,
  audits,
  loading,
  canWrite,
  submitting,
  submitError,
  onResetError,
  onSubmitEids,
}: ConcentratorEidSectionProps) {
  const searchInputId = useId();
  const batchStartSlotId = useId();
  const batchCountId = useId();
  const batchStartEidId = useId();
  const batchStepId = useId();
  const reasonId = useId();

  const [draftSlots, setDraftSlots] = useState<readonly ConcentratorEidSlot[]>(slots ?? []);
  const [slotFilter, setSlotFilter] = useState<EidSlotFilter>('ALL');
  const [draftSearch, setDraftSearch] = useState('');
  const [committedSearch, setCommittedSearch] = useState('');
  const [batchStartSlot, setBatchStartSlot] = useState('24');
  const [batchCount, setBatchCount] = useState('4');
  const [batchStartEid, setBatchStartEid] = useState('101');
  const [batchStep, setBatchStep] = useState('1');
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [reason, setReason] = useState('');

  useEffect(() => {
    setDraftSlots(slots ?? []);
  }, [slots]);

  const channelTempByChannel = useMemo(() => {
    const map = new Map<number, number | null>();
    for (const row of channelRows) {
      map.set(row.channel, row.temperatureC);
    }
    return map;
  }, [channelRows]);

  const originalBySlot = useMemo(() => {
    const map = new Map<number, number>();
    for (const item of slots ?? []) {
      map.set(item.slot, item.eid);
    }
    return map;
  }, [slots]);

  const conflictResult = useMemo(
    () => detectDuplicateNonZeroEids(draftSlots),
    [draftSlots],
  );

  const eidDiff = useMemo(
    () => (slots ? buildConcentratorEidDiff(slots, draftSlots) : []),
    [slots, draftSlots],
  );

  const hasInvalidValue = useMemo(
    () =>
      draftSlots.some(
        (entry) =>
          !Number.isInteger(entry.eid) ||
          entry.eid < 0 ||
          entry.eid > MAX_CONCENTRATOR_EID,
      ),
    [draftSlots],
  );

  const tableRows = useMemo<EidTableRow[]>(() => {
    const query = committedSearch.trim().toLowerCase();
    return draftSlots
      .map((entry) => ({
        slot: entry.slot,
        eid: entry.eid,
        originalEid: originalBySlot.get(entry.slot) ?? 0,
        isConflict: conflictResult.conflictingSlotSet.has(entry.slot),
        channelTemperatureC: channelTempByChannel.get(entry.slot) ?? null,
      }))
      .filter((row) => {
        if (slotFilter === 'BOUND' && row.eid <= 0) return false;
        if (slotFilter === 'EMPTY' && row.eid !== 0) return false;
        if (slotFilter === 'CONFLICT' && !row.isConflict) return false;
        if (!query) return true;
        const slotStr = String(row.slot);
        const eidStr = String(row.eid);
        const eidHex = row.eid.toString(16).toLowerCase();
        return (
          slotStr === query ||
          `槽位${slotStr}`.includes(query) ||
          eidStr.includes(query) ||
          eidHex.includes(query) ||
          `0x${eidHex}`.includes(query)
        );
      });
  }, [
    draftSlots,
    originalBySlot,
    conflictResult.conflictingSlotSet,
    channelTempByChannel,
    slotFilter,
    committedSearch,
  ]);

  const updateSingleSlot = (slotNum: number, rawValue: string) => {
    const trimmed = rawValue.trim();
    const nextValue = trimmed === '' ? 0 : Number(trimmed);
    if (!Number.isFinite(nextValue) || nextValue < 0 || nextValue > MAX_CONCENTRATOR_EID) return;
    const normalized = Math.trunc(nextValue);
    setDraftSlots((current) =>
      current.map((entry) => (entry.slot === slotNum ? { ...entry, eid: normalized } : entry)),
    );
  };

  const columns = useMemo<ColumnDef<DataTableFeatures, EidTableRow>[]>(
    () => [
      {
        id: 'slot',
        header: () => '映射槽位',
        cell: ({ row }) => {
          const item = row.original;
          const dirty = item.eid !== item.originalEid;
          return (
            <div className="flex items-center gap-2 font-mono text-sm tabular-nums">
              <span className="font-semibold text-foreground">
                槽位 {String(item.slot).padStart(3, '0')}
              </span>
              {dirty ? (
                <span className="rounded bg-primary-soft px-1.5 py-0.5 text-[11px] font-medium text-primary">
                  已修改
                </span>
              ) : null}
            </div>
          );
        },
      },
      {
        id: 'eidInput',
        header: () => '探头 EID (32 位十进制)',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <Input
              type="number"
              min={0}
              max={MAX_CONCENTRATOR_EID}
              disabled={!canWrite || submitting}
              aria-label={`槽位 ${item.slot} EID`}
              aria-invalid={item.isConflict}
              value={String(item.eid)}
              onChange={(event) => updateSingleSlot(item.slot, event.currentTarget.value)}
              className={cn(
                'h-8 max-w-44 font-mono text-sm tabular-nums',
                item.isConflict ? 'border-destructive focus-visible:border-destructive' : undefined,
              )}
            />
          );
        },
      },
      {
        id: 'hexAndTemp',
        header: () => '十六进制 / 通道实测',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="font-mono text-xs tabular-nums">
              <div className="text-foreground">{formatConcentratorEidDisplay(item.eid)}</div>
              {item.channelTemperatureC !== null ? (
                <div className="mt-0.5 text-muted-foreground">
                  CH {item.slot} 当前温度 {item.channelTemperatureC.toFixed(1)} °C
                </div>
              ) : null}
            </div>
          );
        },
      },
      {
        id: 'status',
        header: () => '绑定状态',
        cell: ({ row }) => {
          const item = row.original;
          if (item.isConflict) {
            return <GovernanceBadge status={EID_STATUS_CONFLICT} />;
          }
          if (item.eid > 0) {
            return <GovernanceBadge status={EID_STATUS_BOUND} />;
          }
          return <GovernanceBadge status={EID_STATUS_UNBOUND} />;
        },
      },
    ],
    [canWrite, submitting],
  );

  const table = useDataTable({
    key: 'integrations.concentrator-eids',
    data: tableRows,
    columns,
    pageSize: 25,
    getRowId: (row) => String(row.slot),
  });

  const handleBatchFill = () => {
    const startSlotNum = Number(batchStartSlot);
    const countNum = Number(batchCount);
    const startEidNum = Number(batchStartEid);
    const stepNum = Number(batchStep);
    setDraftSlots((current) =>
      applyConcentratorEidBatchFill(current, {
        startSlot: startSlotNum,
        count: countNum,
        startEid: startEidNum,
        step: stepNum,
      }),
    );
  };

  const handleBatchClear = () => {
    const startSlotNum = Number(batchStartSlot);
    const countNum = Number(batchCount);
    setDraftSlots((current) =>
      applyConcentratorEidBatchFill(current, {
        startSlot: startSlotNum,
        count: countNum,
        startEid: 0,
        step: 1,
      }),
    );
  };

  const conflictSummaryLines = useMemo(() => {
    const entries: string[] = [];
    for (const [eid, conflictSlots] of conflictResult.duplicatesByEid) {
      entries.push(`EID ${eid} 重复出现在槽位 ${conflictSlots.join('、')}`);
    }
    return entries;
  }, [conflictResult.duplicatesByEid]);

  const canCommit =
    canWrite &&
    eidDiff.length > 0 &&
    !conflictResult.hasConflicts &&
    !hasInvalidValue &&
    !submitting;

  const handleConfirmCommit = async () => {
    const trimmedReason = reason.trim();
    if (!canCommit || !trimmedReason) return;
    try {
      await onSubmitEids(draftSlots, trimmedReason);
      setConfirmOpen(false);
      setReason('');
    } catch {
      // Mutation error stays displayed inside the dialog via submitError without leaking an unhandled rejection
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <DataTableBlock
        title="200 槽 EID 映射"
        description="共 200 槽（1–200）：EID 为 0 表示未绑定，非零不可重复"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <ToggleGroup
              type="single"
              size="sm"
              variant="outline"
              value={slotFilter}
              onValueChange={(next) => {
                const matched = EID_FILTERS.find((item) => item.value === next);
                if (!matched) return;
                table.setPageIndex(0);
                setSlotFilter(matched.value);
              }}
              aria-label="槽位绑定筛选"
            >
              {EID_FILTERS.map((item) => (
                <ToggleGroupItem key={item.value} value={item.value} className="text-xs">
                  {item.label}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>

            <Button
              variant="outline"
              size="sm"
              disabled={eidDiff.length === 0 || submitting}
              onClick={() => setDraftSlots(slots ?? [])}
            >
              <RotateCcw data-icon="inline-start" />
              还原当前映射
            </Button>
            <Button
              size="sm"
              disabled={!canCommit}
              onClick={() => {
                onResetError();
                setConfirmOpen(true);
              }}
            >
              <Save data-icon="inline-start" />
              预览映射变更（{eidDiff.length}）
            </Button>
          </div>
        }
        controls={
          <div className="flex flex-col gap-3">
            {conflictResult.hasConflicts ? (
              <Alert variant="destructive">
                <AlertDescription>
                  存在重复的非零探头 EID，已阻止写入 Flash：{conflictSummaryLines.join('；')}。
                </AlertDescription>
              </Alert>
            ) : null}

            <div className="flex flex-wrap items-end justify-between gap-3 rounded-lg border bg-muted/30 px-4 py-3">
              <div className="flex flex-wrap items-end gap-3">
                <div className="flex flex-col gap-1">
                  <Label htmlFor={batchStartSlotId} className="text-xs">
                    起始槽位 (1–200)
                  </Label>
                  <Input
                    id={batchStartSlotId}
                    type="number"
                    min={1}
                    max={200}
                    disabled={!canWrite || submitting}
                    value={batchStartSlot}
                    onChange={(event) => setBatchStartSlot(event.currentTarget.value)}
                    className="h-8 w-24 font-mono text-xs tabular-nums"
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <Label htmlFor={batchCountId} className="text-xs">
                    连续数量
                  </Label>
                  <Input
                    id={batchCountId}
                    type="number"
                    min={1}
                    max={200}
                    disabled={!canWrite || submitting}
                    value={batchCount}
                    onChange={(event) => setBatchCount(event.currentTarget.value)}
                    className="h-8 w-24 font-mono text-xs tabular-nums"
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <Label htmlFor={batchStartEidId} className="text-xs">
                    起始 EID
                  </Label>
                  <Input
                    id={batchStartEidId}
                    type="number"
                    min={0}
                    max={MAX_CONCENTRATOR_EID}
                    disabled={!canWrite || submitting}
                    value={batchStartEid}
                    onChange={(event) => setBatchStartEid(event.currentTarget.value)}
                    className="h-8 w-32 font-mono text-xs tabular-nums"
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <Label htmlFor={batchStepId} className="text-xs">
                    递增步长
                  </Label>
                  <Input
                    id={batchStepId}
                    type="number"
                    min={0}
                    max={1000}
                    disabled={!canWrite || submitting}
                    value={batchStep}
                    onChange={(event) => setBatchStep(event.currentTarget.value)}
                    className="h-8 w-20 font-mono text-xs tabular-nums"
                  />
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!canWrite || submitting}
                  onClick={handleBatchFill}
                >
                  <Wand2 data-icon="inline-start" />
                  批量填充区间
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!canWrite || submitting}
                  onClick={handleBatchClear}
                >
                  <Eraser data-icon="inline-start" />
                  清空区间
                </Button>
              </div>

              <div className="flex items-center gap-2">
                <Label htmlFor={searchInputId} className="sr-only">
                  搜索槽位号或 EID
                </Label>
                <div className="relative w-56">
                  <Search
                    className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <Input
                    id={searchInputId}
                    value={draftSearch}
                    onChange={(event) => {
                      const next = event.currentTarget.value;
                      setDraftSearch(next);
                      if (next === '') {
                        table.setPageIndex(0);
                        setCommittedSearch('');
                      }
                    }}
                    onKeyDown={(event) => {
                      if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                      if (event.key === 'Enter') {
                        event.preventDefault();
                        table.setPageIndex(0);
                        setCommittedSearch(draftSearch);
                      }
                    }}
                    placeholder="搜槽位号或 EID…"
                    className="h-8 pl-8 text-xs"
                  />
                </div>
                {draftSearch || committedSearch ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      table.setPageIndex(0);
                      setDraftSearch('');
                      setCommittedSearch('');
                    }}
                  >
                    <X data-icon="inline-start" />
                    重置
                  </Button>
                ) : null}
              </div>
            </div>
          </div>
        }
      >
        {loading ? (
          <Skeleton className="h-64 w-full rounded-xl" />
        ) : (
          <DataTable
            table={table}
            tableAriaLabel="200 槽 EID 映射"
            empty={
              <div className="py-10 text-center text-sm text-muted-foreground">
                当前筛选条件下没有匹配的 EID 槽位
              </div>
            }
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'slot'
                  ? 'pl-5'
                  : header.id === 'status'
                    ? 'pr-5 text-right'
                    : undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'slot'
                  ? 'pl-5'
                  : cell.column.id === 'status'
                    ? 'pr-5 text-right whitespace-nowrap'
                    : undefined,
            })}
            footer={
              tableRows.length > 0 ? (
                <DataTablePagination
                  table={table}
                  totalRows={tableRows.length}
                  pageSizeOptions={[20, 25, 50]}
                />
              ) : null
            }
          />
        )}
      </DataTableBlock>

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

      <Dialog
        open={confirmOpen}
        onOpenChange={(open) => {
          if (!submitting) setConfirmOpen(open);
        }}
      >
        <DialogContent className="sm:max-w-xl">
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
              onChange={(event) => setReason(event.currentTarget.value)}
              placeholder="请填写本次 EID 槽位调整原因（如：松雅轩现场补绑 CH24 新装无线温度探头 EID 101）"
            />
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
              onClick={() => setConfirmOpen(false)}
            >
              取消
            </Button>
            <Button
              disabled={!canCommit || reason.trim().length === 0}
              onClick={() => void handleConfirmCommit()}
            >
              {submitting ? '正在写入 Flash…' : '确认写入 EID 到 Flash'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
