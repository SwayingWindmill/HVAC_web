import { useMemo, useState } from 'react';
import { AssistantRequestError } from '@/api/agent-sessions';
import type { Site } from '@/api/generated/platformGateway.gen';
import type { AgentUsageDay } from '@/api/generated/operationsAgentSessions.gen';
import { Panel } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { toolPresentation } from '@/features/assistant/tool-labels';
import { cn } from '@/lib/utils';
import { failureReasonWords } from './failure-reasons';
import { useAssistantUsage } from './query';

function Notice({ children }: { readonly children: string }) {
  return <p className="px-5 py-10 text-center text-sm text-muted-foreground">{children}</p>;
}

const asked = (day: AgentUsageDay) => day.completed + day.failed + day.cancelled;
const seconds = (milliseconds: number | null) => (milliseconds === null ? '—' : `${(milliseconds / 1000).toFixed(1)} 秒`);
const tokens = (count: number) => (count >= 10_000 ? `${(count / 10_000).toFixed(1)} 万` : String(count));
const unfinished = (day: AgentUsageDay) => [day.failed ? `出错 ${day.failed}` : '', day.cancelled ? `被停下 ${day.cancelled}` : ''].filter(Boolean).join('、') || '—';
const dayLabel = (date: string) => `${Number(date.slice(5, 7))}月${Number(date.slice(8, 10))}日`;

/**
 * Each step's calls and failures over the given days, steps the panel names alike counted together,
 * with why they failed in words (#624), the commonest first.
 */
function stepTotals(days: readonly AgentUsageDay[]) {
  const totals = new Map<string, { calls: number; failed: number; reasons: Map<string, number> }>();
  for (const day of days) {
    for (const tool of day.tools) {
      const name = toolPresentation(tool.toolName).label;
      const total = totals.get(name) ?? { calls: 0, failed: 0, reasons: new Map<string, number>() };
      for (const { code, count } of tool.failureReasons) {
        const words = failureReasonWords(code);
        total.reasons.set(words, (total.reasons.get(words) ?? 0) + count);
      }
      totals.set(name, { calls: total.calls + tool.calls, failed: total.failed + tool.failed, reasons: total.reasons });
    }
  }
  return [...totals].map(([name, { calls, failed, reasons }]) => ({
    name,
    calls,
    failed,
    reasons: [...reasons].sort((left, right) => right[1] - left[1]).map(([words, count]) => `${words} ${count}`).join('、') || '—',
  })).sort((left, right) => right.failed - left.failed || right.calls - left.calls);
}

/**
 * How the AI assistant ran on a Site over the last 14 days (#597): per day how many answers
 * finished or did not, how long they took and the model usage, and each day's steps and which of
 * them failed. Figures and step names only, never a question or an answer.
 */
export function AssistantUsage({ sites, canRead, active }: {
  readonly sites: readonly Readonly<Site>[];
  readonly canRead: boolean;
  readonly active: boolean;
}) {
  const [siteId, setSiteId] = useState<string | undefined>();
  const [selectedDate, setSelectedDate] = useState<string | null>(null);
  const site = sites.find((candidate) => candidate.id === siteId) ?? sites[0];
  const usage = useAssistantUsage(site, canRead && active && !__HVAC_WEB_FRONTEND_REVIEW__);
  const forbidden = !canRead || (usage.error instanceof AssistantRequestError && usage.error.status === 403);
  const days = useMemo(() => [...(usage.data?.days ?? [])].reverse(), [usage.data]);
  const selectedDay = days.find((day) => day.date === selectedDate);
  const steps = useMemo(() => stepTotals(selectedDay ? [selectedDay] : days), [days, selectedDay]);
  const totalAsked = days.reduce((sum, day) => sum + asked(day), 0);
  const totalUnfinished = days.reduce((sum, day) => sum + day.failed + day.cancelled, 0);

  return (
    <Panel aria-label="AI 助手使用情况">
      <div className="flex h-12 items-center gap-3 border-b border-divider px-4">
        <Select value={site?.id} onValueChange={(value) => { setSiteId(value); setSelectedDate(null); }} disabled={sites.length === 0}>
          <SelectTrigger className="h-8 w-48" aria-label="站点"><SelectValue placeholder="没有授权站点" /></SelectTrigger>
          <SelectContent>
            {sites.map((candidate) => <SelectItem key={candidate.id} value={candidate.id}>{candidate.displayName}</SelectItem>)}
          </SelectContent>
        </Select>
        {usage.isSuccess ? (
          <span className="text-sm text-muted-foreground">
            最近 14 天回答 {totalAsked} 次{totalUnfinished ? `，其中 ${totalUnfinished} 次没答完` : ''}
          </span>
        ) : null}
        <span className="ml-auto text-xs text-muted-foreground">按站点当地日期，每天从 0 点算起</span>
      </div>
      {forbidden ? <Notice>当前账号没有查看操作记录的权限，看不到 AI 助手的使用情况</Notice>
        : __HVAC_WEB_FRONTEND_REVIEW__ ? <Notice>评审构建不连接 AI 助手</Notice>
        : site === undefined ? <Notice>没有授权站点</Notice>
        : usage.isPending ? <div className="flex flex-col gap-2 p-4"><Skeleton className="h-9" /><Skeleton className="h-9" /><Skeleton className="h-9" /></div>
        : usage.isError ? <Notice>AI 助手的使用情况暂时读不到</Notice>
        : totalAsked === 0 ? <Notice>最近 14 天没有人用 AI 助手</Notice>
        : (
          <div className="grid grid-cols-[minmax(0,3fr)_minmax(0,2fr)] divide-x divide-divider">
            <Table aria-label="每天的回答">
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-5">日期</TableHead>
                  <TableHead className="text-right">答完</TableHead>
                  <TableHead>没答完</TableHead>
                  <TableHead className="text-right">
                    <span className="inline-flex items-center gap-1">平均用时<InfoTip>按这天所有结束的回答算，包括出错和被停下的</InfoTip></span>
                  </TableHead>
                  <TableHead className="pr-5 text-right">
                    <span className="inline-flex items-center gap-1">模型用量<InfoTip>模型读入和写出的文字量，大约一个汉字算一个</InfoTip></span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {days.map((day) => (
                  <TableRow key={day.date} data-state={day.date === selectedDate ? 'selected' : undefined} className={cn(asked(day) === 0 && 'text-muted-foreground')}>
                    <TableCell className="pl-5">
                      <button
                        type="button"
                        className="font-medium hover:underline disabled:font-normal disabled:no-underline"
                        aria-pressed={day.date === selectedDate}
                        disabled={asked(day) === 0}
                        onClick={() => setSelectedDate(day.date === selectedDate ? null : day.date)}
                      >
                        {dayLabel(day.date)}
                      </button>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{day.completed}</TableCell>
                    <TableCell className={day.failed ? 'text-destructive-text' : undefined}>{unfinished(day)}</TableCell>
                    <TableCell className="text-right tabular-nums">{seconds(day.averageDurationMs)}</TableCell>
                    <TableCell className="pr-5 text-right tabular-nums">{tokens(day.inputTokens + day.outputTokens)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <div className="flex flex-col">
              <div className="flex h-10 items-center justify-between border-b border-divider px-5 text-sm">
                <span className="font-medium">{selectedDay ? `${dayLabel(selectedDay.date)}的步骤` : '最近 14 天的步骤'}</span>
                {selectedDay ? <Button size="sm" variant="ghost" onClick={() => setSelectedDate(null)}>看 14 天合计</Button> : <span className="text-xs text-muted-foreground">点日期看那一天</span>}
              </div>
              <Table aria-label="各个步骤">
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-5">步骤</TableHead>
                    <TableHead className="text-right">次数</TableHead>
                    <TableHead className="text-right">失败</TableHead>
                    <TableHead className="pr-5">没成功的原因</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {steps.map((step) => (
                    <TableRow key={step.name}>
                      <TableCell className="pl-5">{step.name}</TableCell>
                      <TableCell className="text-right tabular-nums">{step.calls}</TableCell>
                      <TableCell className={cn('text-right tabular-nums', step.failed && 'text-destructive-text')}>{step.failed}</TableCell>
                      <TableCell className="pr-5 text-muted-foreground">{step.reasons}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        )}
    </Panel>
  );
}
