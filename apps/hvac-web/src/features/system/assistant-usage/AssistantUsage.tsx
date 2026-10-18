import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { AssistantRequestError, getAssistantUsage } from '@/api/agent-sessions';
import type { Site } from '@/api/generated/platformGateway.gen';
import type { AgentUsageDay } from '@/api/generated/operationsAgentSessions.gen';
import { Panel } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { toolPresentation } from '@/features/assistant/tool-labels';

function Notice({ children }: { readonly children: string }) {
  return <p className="px-5 py-10 text-center text-sm text-muted-foreground">{children}</p>;
}

const asked = (day: AgentUsageDay) => day.completed + day.failed + day.cancelled;
const seconds = (milliseconds: number | null) => (milliseconds === null ? '—' : `${(milliseconds / 1000).toFixed(1)} 秒`);
const tokens = (count: number) => (count >= 10_000 ? `${(count / 10_000).toFixed(1)} 万` : String(count));
const unfinished = (day: AgentUsageDay) => [day.failed ? `出错 ${day.failed}` : '', day.cancelled ? `被停下 ${day.cancelled}` : ''].filter(Boolean).join('、') || '—';
const dayLabel = (date: string) => `${Number(date.slice(5, 7))}月${Number(date.slice(8, 10))}日`;

/**
 * How the AI assistant ran on a Site over the last 14 days (#597): per day how many answers
 * finished or did not, how long they took and the model usage, and which reads failed. Figures and
 * read names only, never a question or an answer.
 */
export function AssistantUsage({ sites, canRead, active }: {
  readonly sites: readonly Readonly<Site>[];
  readonly canRead: boolean;
  readonly active: boolean;
}) {
  const [siteId, setSiteId] = useState<string | undefined>();
  const site = sites.find((candidate) => candidate.id === siteId) ?? sites[0];
  const usage = useQuery({
    queryKey: ['assistant', 'usage', site?.id, site?.timezone],
    queryFn: () => getAssistantUsage(site!.id, site!.timezone),
    enabled: canRead && active && site !== undefined && !__HVAC_WEB_FRONTEND_REVIEW__,
    retry: false,
  });
  const forbidden = !canRead || (usage.error instanceof AssistantRequestError && usage.error.status === 403);
  const days = useMemo(() => [...(usage.data?.days ?? [])].reverse(), [usage.data]);
  const tools = useMemo(() => {
    const totals = new Map<string, { calls: number; failed: number }>();
    for (const day of usage.data?.days ?? []) {
      for (const tool of day.tools) {
        const total = totals.get(tool.toolName) ?? { calls: 0, failed: 0 };
        totals.set(tool.toolName, { calls: total.calls + tool.calls, failed: total.failed + tool.failed });
      }
    }
    return [...totals].map(([toolName, total]) => ({ name: toolPresentation(toolName).label, ...total }))
      .sort((left, right) => right.failed - left.failed || right.calls - left.calls);
  }, [usage.data]);
  const totalAsked = days.reduce((sum, day) => sum + asked(day), 0);
  const totalUnfinished = days.reduce((sum, day) => sum + day.failed + day.cancelled, 0);

  return (
    <Panel aria-label="AI 助手使用情况">
      <div className="flex h-12 items-center gap-3 border-b border-divider px-4">
        <Select value={site?.id} onValueChange={setSiteId} disabled={sites.length === 0}>
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
                  <TableHead className="text-right">平均用时</TableHead>
                  <TableHead className="pr-5 text-right">
                    <span className="inline-flex items-center gap-1">模型用量<InfoTip>模型按 token 计量读入和写出的文字，大约一个汉字算一个</InfoTip></span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {days.map((day) => (
                  <TableRow key={day.date} className={asked(day) === 0 ? 'text-muted-foreground' : undefined}>
                    <TableCell className="pl-5">{dayLabel(day.date)}</TableCell>
                    <TableCell className="text-right tabular-nums">{day.completed}</TableCell>
                    <TableCell className={day.failed ? 'text-destructive-text' : undefined}>{unfinished(day)}</TableCell>
                    <TableCell className="text-right tabular-nums">{seconds(day.averageDurationMs)}</TableCell>
                    <TableCell className="pr-5 text-right tabular-nums">{tokens(day.inputTokens + day.outputTokens)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <Table aria-label="读数据的情况">
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-5">读的数据</TableHead>
                  <TableHead className="text-right">次数</TableHead>
                  <TableHead className="pr-5 text-right">失败</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tools.map((tool) => (
                  <TableRow key={tool.name}>
                    <TableCell className="pl-5">{tool.name}</TableCell>
                    <TableCell className="text-right tabular-nums">{tool.calls}</TableCell>
                    <TableCell className={tool.failed ? 'pr-5 text-right tabular-nums text-destructive-text' : 'pr-5 text-right tabular-nums'}>{tool.failed}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
    </Panel>
  );
}
