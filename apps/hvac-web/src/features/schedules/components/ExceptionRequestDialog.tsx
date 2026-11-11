import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import type {
  OperatingExceptionKind,
  OperatingExceptionSlot,
} from '@/api/generated/platformGateway.gen';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { createOperatingException, scheduleErrorMessage, scheduleKeys } from '../query';
import {
  createClientUuidV7,
  EXCEPTION_KIND_LABELS,
  formatMinuteOfDay,
  parseTimeInputToMinute,
} from '../schedule-model';

interface ReplacementSlotRow {
  readonly key: string;
  readonly startTime: string;
  readonly endTime: string;
  readonly targetSupplyTempInput: string;
}

let slotRowSeq = 1;
function nextSlotRowKey(): string {
  slotRowSeq += 1;
  return `exc-slot-${slotRowSeq}`;
}

export interface ExceptionRequestInitial {
  readonly kind?: OperatingExceptionKind;
  readonly startDate?: string;
  readonly endDate?: string;
}

export interface ExceptionRequestDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly defaultDate: string;
  readonly initial?: ExceptionRequestInitial | null;
  readonly canManageSchedule: boolean;
}

export function ExceptionRequestDialog({
  open,
  onOpenChange,
  siteId,
  csrfToken,
  defaultDate,
  initial,
  canManageSchedule,
}: ExceptionRequestDialogProps) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState('');
  const [kind, setKind] = useState<OperatingExceptionKind>('HOLIDAY_SHUTDOWN');
  const [startDate, setStartDate] = useState(defaultDate);
  const [endDate, setEndDate] = useState(defaultDate);
  const [slotRows, setSlotRows] = useState<ReplacementSlotRow[]>([
    { key: 'default-1', startTime: '09:00', endTime: '18:00', targetSupplyTempInput: '7.5' },
  ]);
  const [reason, setReason] = useState('');
  const [autoApprove, setAutoApprove] = useState(canManageSchedule);
  const [errorText, setErrorText] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    const initKind = initial?.kind ?? 'HOLIDAY_SHUTDOWN';
    const initStart = initial?.startDate ?? defaultDate;
    const initEnd = initial?.endDate ?? initStart;
    setTitle('');
    setKind(initKind);
    setStartDate(initStart);
    setEndDate(initEnd);
    setSlotRows([
      { key: nextSlotRowKey(), startTime: '09:00', endTime: '18:00', targetSupplyTempInput: '7.5' },
    ]);
    setReason('');
    setAutoApprove(canManageSchedule);
    setErrorText(null);
  }, [open, initial, defaultDate, canManageSchedule]);

  const mutation = useMutation({
    mutationFn: (replacementSlots: OperatingExceptionSlot[]) =>
      createOperatingException(
        siteId,
        {
          title: title.trim(),
          kind,
          startDate,
          endDate,
          replacementSlots,
          ...(canManageSchedule ? { autoApprove } : {}),
          meta: {
            expectedRevision: 0,
            idempotencyKey: `exc-create-${createClientUuidV7()}`,
            reason: reason.trim(),
          },
        },
        csrfToken,
      ),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: scheduleKeys.exceptionsRoot(siteId) });
      toast.success(
        created.status === 'APPROVED' ? '已登记并立即生效运行例外' : '已提交运行例外申请，等待审批',
      );
      onOpenChange(false);
    },
    onError: (error) => {
      setErrorText(scheduleErrorMessage(error, '提交运行例外申请失败'));
    },
  });

  const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorText(null);

    const trimmedTitle = title.trim();
    if (!trimmedTitle || trimmedTitle.length > 100) {
      setErrorText('请填写 1–100 字以内的例外名称');
      return;
    }

    if (!/^\d{4}-\d{2}-\d{2}$/.test(startDate) || !/^\d{4}-\d{2}-\d{2}$/.test(endDate)) {
      setErrorText('请选择有效的开始日期与结束日期');
      return;
    }

    if (startDate > endDate) {
      setErrorText('结束日期不能早于开始日期');
      return;
    }

    const trimmedReason = reason.trim();
    if (!trimmedReason || trimmedReason.length > 500) {
      setErrorText('请填写申请原因（最多 500 字）');
      return;
    }

    const compiledSlots: OperatingExceptionSlot[] = [];
    if (kind === 'OVERTIME_COOLING') {
      if (slotRows.length === 0) {
        setErrorText('加班供冷例外至少需要填写一段当日供冷时段');
        return;
      }
      for (let i = 0; i < slotRows.length; i += 1) {
        const row = slotRows[i];
        const startMin = parseTimeInputToMinute(row.startTime, false);
        const endMin = parseTimeInputToMinute(row.endTime, true);
        if (startMin === null || endMin === null || startMin >= endMin) {
          setErrorText(`第 ${i + 1} 段供冷时段的起止时间无效（结束时间须晚于开始时间，可填 24:00）`);
          return;
        }
        let temp: number | undefined;
        if (row.targetSupplyTempInput.trim() !== '') {
          const parsed = Number(row.targetSupplyTempInput.trim());
          if (!Number.isFinite(parsed) || parsed < 4 || parsed > 20) {
            setErrorText(`第 ${i + 1} 段的交接参考供水温度须在 4.0–20.0 °C 之间`);
            return;
          }
          temp = Number(parsed.toFixed(1));
        }
        compiledSlots.push({
          startMinute: startMin,
          endMinute: endMin,
          ...(temp !== undefined ? { targetSupplyTempC: temp } : {}),
        });
      }

      const sorted = [...compiledSlots].sort((a, b) => a.startMinute - b.startMinute);
      for (let i = 1; i < sorted.length; i += 1) {
        if (sorted[i].startMinute < sorted[i - 1].endMinute) {
          setErrorText(
            `当日供冷时段存在重叠（${formatMinuteOfDay(sorted[i - 1].startMinute)}–${formatMinuteOfDay(sorted[i - 1].endMinute)} 与 ${formatMinuteOfDay(sorted[i].startMinute)}–${formatMinuteOfDay(sorted[i].endMinute)}）`,
          );
          return;
        }
      }
    }

    mutation.mutate(compiledSlots);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>申请运行例外</DialogTitle>
          <DialogDescription>
            已生效的运行例外将按站点本地日历日全天（00:00–24:00）覆盖常规周运行计划，首尾日期均包含在内。
          </DialogDescription>
        </DialogHeader>

        <form className="space-y-4" onSubmit={handleSubmit}>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="exc-title">例外名称</Label>
              <Input
                id="exc-title"
                value={title}
                maxLength={100}
                onChange={(event) => setTitle(event.target.value)}
                placeholder="例如：国庆假期冷站停机 / 周六展厅加班供冷"
              />
            </div>

            <div className="space-y-1.5">
              <Label>例外类型</Label>
              <Select
                value={kind}
                onValueChange={(val) => setKind(val as OperatingExceptionKind)}
              >
                <SelectTrigger aria-label="例外类型">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(Object.keys(EXCEPTION_KIND_LABELS) as OperatingExceptionKind[]).map((itemKind) => (
                    <SelectItem key={itemKind} value={itemKind}>
                      {EXCEPTION_KIND_LABELS[itemKind]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="exc-start-date">开始日期（含当日）</Label>
              <Input
                id="exc-start-date"
                type="date"
                value={startDate}
                onChange={(event) => {
                  const next = event.target.value;
                  setStartDate(next);
                  if (endDate < next) setEndDate(next);
                }}
                className="tabular-nums"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="exc-end-date">结束日期（含当日）</Label>
              <Input
                id="exc-end-date"
                type="date"
                value={endDate}
                onChange={(event) => setEndDate(event.target.value)}
                className="tabular-nums"
              />
            </div>
          </div>

          {kind === 'OVERTIME_COOLING' ? (
            <div className="space-y-2 rounded-md border border-border p-3">
              <div className="flex items-center justify-between">
                <div>
                  <Label>加班供冷时段（每日重复）</Label>
                  <p className="text-xs text-muted-foreground">
                    替代所选日期原有的常规周计划；参考供水温度仅作值班交接参考，不自动下发指令。
                  </p>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    setSlotRows((prev) => [
                      ...prev,
                      {
                        key: nextSlotRowKey(),
                        startTime: '18:00',
                        endTime: '22:00',
                        targetSupplyTempInput: '7.5',
                      },
                    ])
                  }
                >
                  <Plus data-icon="inline-start" />
                  添加时段
                </Button>
              </div>

              <div className="space-y-2">
                {slotRows.map((row, idx) => (
                  <div
                    key={row.key}
                    className="grid grid-cols-[1fr_1fr_1fr_36px] items-center gap-2"
                  >
                    <Input
                      aria-label={`加班时段 ${idx + 1} 开始时间`}
                      value={row.startTime}
                      onChange={(event) =>
                        setSlotRows((prev) =>
                          prev.map((r) =>
                            r.key === row.key ? { ...r, startTime: event.target.value } : r,
                          ),
                        )
                      }
                      placeholder="09:00"
                      className="tabular-nums"
                    />
                    <Input
                      aria-label={`加班时段 ${idx + 1} 结束时间`}
                      value={row.endTime}
                      onChange={(event) =>
                        setSlotRows((prev) =>
                          prev.map((r) =>
                            r.key === row.key ? { ...r, endTime: event.target.value } : r,
                          ),
                        )
                      }
                      placeholder="18:00"
                      className="tabular-nums"
                    />
                    <Input
                      aria-label={`加班时段 ${idx + 1} 参考供水温度`}
                      type="number"
                      min={4}
                      max={20}
                      step={0.1}
                      value={row.targetSupplyTempInput}
                      onChange={(event) =>
                        setSlotRows((prev) =>
                          prev.map((r) =>
                            r.key === row.key
                              ? { ...r, targetSupplyTempInput: event.target.value }
                              : r,
                          ),
                        )
                      }
                      placeholder="参考 °C"
                      className="tabular-nums"
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      disabled={slotRows.length <= 1}
                      aria-label={`删除加班时段 ${idx + 1}`}
                      onClick={() =>
                        setSlotRows((prev) => prev.filter((r) => r.key !== row.key))
                      }
                    >
                      <Trash2 className="size-4 text-muted-foreground" />
                    </Button>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="rounded-md border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
              所选日期将按全天（00:00–24:00）停机执行，自动屏蔽当日全部常规供冷时段（含前一日跨午夜延续至当日凌晨的时段）。
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="exc-reason">申请原因</Label>
            <Textarea
              id="exc-reason"
              rows={2}
              maxLength={500}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder="例如：租户周末展厅活动申请延长供冷 / 冷冻水主管阀门年度检修停机"
            />
          </div>

          {canManageSchedule ? (
            <label className="flex cursor-pointer items-center justify-between rounded-md border border-border px-3 py-2.5">
              <div className="space-y-0.5">
                <span className="text-sm font-medium text-foreground">提交并立即生效</span>
                <p className="text-xs text-muted-foreground">
                  关闭后将作为「待审批」申请提交，由其他管理员复核审批。
                </p>
              </div>
              <Switch
                checked={autoApprove}
                onCheckedChange={setAutoApprove}
                aria-label="提交并立即生效"
              />
            </label>
          ) : null}

          {errorText ? <p className="text-sm text-destructive">{errorText}</p> : null}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              取消
            </Button>
            <Button type="submit" disabled={mutation.isPending}>
              {mutation.isPending
                ? '正在提交…'
                : canManageSchedule && autoApprove
                  ? '提交并生效'
                  : '提交申请'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
