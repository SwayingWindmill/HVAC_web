import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CalendarDays, Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { Panel, PanelHeader } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { wallTime } from '@/features/energy-analysis/consumption/model';
import { formatTime } from '@/lib/operator-format';
import {
  formatWeekdayShort,
  SITE_CALENDAR_DAY_TYPE_LABEL,
  SITE_CALENDAR_DAY_TYPE_VARIANT,
} from '../accounting-config-model';
import {
  deleteSiteAccountingHoliday,
  describeAccountingConfigError,
  siteAccountingHolidaysQuery,
  type SiteCalendarHoliday,
} from '../query';
import { invalidateAfterHolidayMutation } from './accounting-config-helpers';
import { SiteHolidayEditorDialog } from './SiteHolidayEditorDialog';

export interface SiteHolidaysPanelProps {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly canManageBilling: boolean;
  readonly now: Date;
  readonly onNotice: (message: string) => void;
}

export function SiteHolidaysPanel({
  siteId,
  timezone,
  csrfToken,
  canManageBilling,
  now,
  onNotice,
}: SiteHolidaysPanelProps) {
  const queryClient = useQueryClient();
  const currentYear = useMemo(() => wallTime(now, timezone).year, [now, timezone]);
  const [selectedYear, setSelectedYear] = useState<number>(() => currentYear);
  const [holidayDialogOpen, setHolidayDialogOpen] = useState(false);
  const [holidayEditTarget, setHolidayEditTarget] = useState<SiteCalendarHoliday | null>(null);
  const [deletingHoliday, setDeletingHoliday] = useState<SiteCalendarHoliday | null>(null);
  const [holidayDeleteError, setHolidayDeleteError] = useState<string | null>(null);

  const yearOptions = useMemo(() => {
    const years = new Set([currentYear - 1, currentYear, currentYear + 1, selectedYear]);
    return [...years].sort((a, b) => b - a);
  }, [currentYear, selectedYear]);

  const holidaysQuery = useQuery(siteAccountingHolidaysQuery(siteId, selectedYear));
  const holidays = holidaysQuery.data ?? [];

  const deleteHolidayMutation = useMutation({
    mutationFn: (date: string) => deleteSiteAccountingHoliday(siteId, csrfToken, date),
    onSuccess: async (result) => {
      await invalidateAfterHolidayMutation(queryClient, siteId);
      const message = result.recalcJobId
        ? `已删除 ${result.holidayDate} 日历覆盖，后台重算任务已加入队列`
        : `已删除 ${result.holidayDate} 日历覆盖`;
      onNotice(message);
      toast.success(message);
      setDeletingHoliday(null);
    },
    onError: (error) => {
      setHolidayDeleteError(
        describeAccountingConfigError(error, '删除站点节假日配置失败，请稍后重试'),
      );
    },
  });

  const openCreateHolidayDialog = () => {
    setHolidayEditTarget(null);
    setHolidayDialogOpen(true);
  };

  const openEditHolidayDialog = (holiday: SiteCalendarHoliday) => {
    setHolidayEditTarget(holiday);
    setHolidayDialogOpen(true);
  };

  return (
    <>
      <Panel role="region" aria-label="站点节假日日历">
        <PanelHeader
          title="站点节假日日历"
          className="h-auto min-h-12 flex-wrap py-2"
          action={
            <div className="flex flex-wrap items-center gap-2">
              <Select
                value={String(selectedYear)}
                onValueChange={(value) => {
                  const parsed = Number(value);
                  if (Number.isInteger(parsed) && parsed >= 2000 && parsed <= 2100) {
                    setSelectedYear(parsed);
                  }
                }}
              >
                <SelectTrigger className="h-8 w-28" aria-label="节假日年份">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {yearOptions.map((year) => (
                    <SelectItem key={year} value={String(year)}>
                      {year} 年
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>

              {canManageBilling ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={openCreateHolidayDialog}
                >
                  <Plus data-icon="inline-start" />
                  新增或更新节假日
                </Button>
              ) : null}
            </div>
          }
        />

        {holidaysQuery.isError ? (
          <StateCard
            tone="danger"
            title={<h3 className="text-base font-semibold">无法读取站点节假日日历</h3>}
            description={describeAccountingConfigError(
              holidaysQuery.error,
              '请检查网络连接或稍后重试',
            )}
          >
            <Button variant="outline" size="sm" onClick={() => void holidaysQuery.refetch()}>
              重试
            </Button>
          </StateCard>
        ) : holidaysQuery.isPending ? (
          <div className="p-5">
            <Skeleton className="h-36 w-full rounded-lg" />
          </div>
        ) : holidays.length === 0 ? (
          <StateCard
            icon={CalendarDays}
            title={
              <h3 className="text-sm font-medium text-foreground">
                {selectedYear} 年尚未配置节假日覆盖
              </h3>
            }
            description="默认按工作日与周末匹配分时电价"
          >
            {canManageBilling ? (
              <Button type="button" size="sm" variant="outline" onClick={openCreateHolidayDialog}>
                <Plus data-icon="inline-start" />
                新增或更新节假日
              </Button>
            ) : null}
          </StateCard>
        ) : (
          <Table aria-label="节假日日历列表">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-5">日期</TableHead>
                <TableHead>日期类型</TableHead>
                <TableHead>说明</TableHead>
                <TableHead>最近更新</TableHead>
                {canManageBilling ? <TableHead className="pr-5 text-right">操作</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {holidays.map((item) => {
                const badgeMeta = SITE_CALENDAR_DAY_TYPE_VARIANT[item.dayType];
                const weekday = formatWeekdayShort(item.holidayDate);
                return (
                  <TableRow key={item.id}>
                    <TableCell className="pl-5 font-mono text-xs font-medium tabular-nums whitespace-nowrap">
                      {item.holidayDate}
                      {weekday ? (
                        <span className="ml-2 font-sans font-normal text-muted-foreground">
                          {weekday}
                        </span>
                      ) : null}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      <Badge variant={badgeMeta.variant}>
                        {badgeMeta.marker} {SITE_CALENDAR_DAY_TYPE_LABEL[item.dayType]}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-foreground">
                      {item.label.trim() ? (
                        item.label
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground tabular-nums whitespace-nowrap">
                      {formatTime(item.updatedAt, timezone)}
                    </TableCell>
                    {canManageBilling ? (
                      <TableCell className="pr-5 text-right whitespace-nowrap">
                        <div className="inline-flex items-center justify-end gap-1">
                          <Button
                            type="button"
                            size="sm"
                            variant="ghost"
                            onClick={() => openEditHolidayDialog(item)}
                          >
                            <Pencil data-icon="inline-start" />
                            编辑
                          </Button>
                          <Button
                            type="button"
                            size="sm"
                            variant="ghost"
                            className="text-destructive-text hover:text-destructive-text"
                            onClick={() => {
                              setHolidayDeleteError(null);
                              setDeletingHoliday(item);
                            }}
                          >
                            <Trash2 data-icon="inline-start" />
                            删除
                          </Button>
                        </div>
                      </TableCell>
                    ) : null}
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </Panel>

      <SiteHolidayEditorDialog
        open={holidayDialogOpen}
        onOpenChange={setHolidayDialogOpen}
        siteId={siteId}
        timezone={timezone}
        csrfToken={csrfToken}
        now={now}
        selectedYear={selectedYear}
        editTarget={holidayEditTarget}
        onNotice={onNotice}
      />

      <Dialog
        open={Boolean(deletingHoliday)}
        onOpenChange={(nextOpen) => {
          if (deleteHolidayMutation.isPending) return;
          if (!nextOpen) setDeletingHoliday(null);
        }}
      >
        <DialogContent
          showCloseButton={!deleteHolidayMutation.isPending}
          onEscapeKeyDown={(event) => {
            if (deleteHolidayMutation.isPending) event.preventDefault();
          }}
          onPointerDownOutside={(event) => {
            if (deleteHolidayMutation.isPending) event.preventDefault();
          }}
          className="sm:max-w-md"
        >
          <DialogHeader>
            <DialogTitle>确认删除节假日</DialogTitle>
            <DialogDescription>
              {deletingHoliday
                ? `删除 ${deletingHoliday.holidayDate}（${SITE_CALENDAR_DAY_TYPE_LABEL[deletingHoliday.dayType]}）的覆盖配置后，该日将恢复按常规星期类型匹配分时电价，并触发后台重算。`
                : ''}
            </DialogDescription>
          </DialogHeader>

          {holidayDeleteError ? (
            <Alert variant="destructive">
              <AlertTitle>无法删除节假日</AlertTitle>
              <AlertDescription>{holidayDeleteError}</AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={deleteHolidayMutation.isPending}
              onClick={() => setDeletingHoliday(null)}
            >
              取消
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={deleteHolidayMutation.isPending || !deletingHoliday}
              onClick={() => {
                if (!deletingHoliday) return;
                setHolidayDeleteError(null);
                deleteHolidayMutation.mutate(deletingHoliday.holidayDate);
              }}
            >
              {deleteHolidayMutation.isPending ? '正在删除…' : '确认删除'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
