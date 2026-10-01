import { useMemo, useState } from 'react';
import { Bell, FlaskConical, History, Link2, LoaderCircle, Settings2, ShieldCheck } from 'lucide-react';
import type {
  CurrentPrincipalResponse,
  RuleExecutionEvidence,
  RuleRevision,
  Site,
} from '@/api/generated/platformGateway.gen';
import {
  presentRuleError,
  useRuleBindings,
  useRuleExecutionEvidence,
  useRuleRevisions,
} from '@/api/rules';
import { StatusBadge } from '@/components/status-badge';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';

interface AlarmRulesPanelProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

interface RuleConfigFact {
  nodeId: string;
  key: string;
  value: string;
}

const recoveryFieldMatcher = /(clear|recover|recovery|hysteresis|reset)/i;
const durationFieldMatcher = /(duration|window|hold|debounce|seconds|minutes|period)/i;
const fieldGroups = [
  { key: 'trigger', label: '触发条件', matcher: (key: string) => /(trigger|threshold|predicate|operator|compare|condition|limit)/i.test(key) && !recoveryFieldMatcher.test(key) },
  { key: 'recovery', label: '恢复条件', matcher: (key: string) => recoveryFieldMatcher.test(key) && !durationFieldMatcher.test(key) },
  { key: 'duration', label: '持续时间', matcher: (key: string) => durationFieldMatcher.test(key) },
  { key: 'suppression', label: '抑制策略', matcher: (key: string) => /(suppress|suppression|inhibit|maintenance|mute)/i.test(key) },
  { key: 'recipient', label: '通知对象', matcher: (key: string) => /(recipient|target|contact|assignee|audience)/i.test(key) },
  { key: 'channel', label: '通知渠道', matcher: (key: string) => /(channel|notification|notify|webhook|email|sms)/i.test(key) },
] as const;

function displayValue(value: unknown): string {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'string') return value || '—';
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) return value.map(displayValue).join('、') || '—';
  return '已配置';
}

function revisionConfigFacts(revision: RuleRevision | null): RuleConfigFact[] {
  if (!revision) return [];
  return revision.nodes.flatMap((node) => Object.entries(node.config).map(([key, value]) => ({
    nodeId: node.id,
    key,
    value: displayValue(value),
  })));
}

function presentConfigFact(fact: RuleConfigFact): string {
  switch (fact.key) {
    case 'durationMinutes': return `触发 ${fact.value} 分钟`;
    case 'clearDurationMinutes': return `恢复 ${fact.value} 分钟`;
    default: return fact.value;
  }
}

function statusLabel(status: RuleExecutionEvidence['status']): string {
  switch (status) {
    case 'READY': return '待执行';
    case 'RUNNING': return '执行中';
    case 'WAITING': return '等待条件';
    case 'BLOCKED_EFFECT': return '动作受阻';
    case 'SUCCEEDED': return '执行成功';
    case 'DEAD': return '执行终止';
    case 'QUARANTINED': return '已隔离';
    case 'FAILED': return '执行失败';
  }
}

function statusTone(status: RuleExecutionEvidence['status']): 'destructive' | 'warning' | 'success' | 'neutral' {
  if (status === 'FAILED' || status === 'DEAD') return 'destructive';
  if (status === 'WAITING' || status === 'BLOCKED_EFFECT' || status === 'QUARANTINED') return 'warning';
  if (status === 'SUCCEEDED') return 'success';
  return 'neutral';
}

function RuleExecutionStatusBadge({ status }: { status: RuleExecutionEvidence['status'] }) {
  return <StatusBadge tone={statusTone(status)}>{statusLabel(status)}</StatusBadge>;
}

function formatRuleTime(value: string, timezone: string, style: 'short' | 'medium' = 'short'): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    dateStyle: style,
    timeStyle: 'short',
  }).format(new Date(value));
}

export function AlarmRulesPanel({ site, principal }: AlarmRulesPanelProps) {
  const canManage = principal.authorization.capabilities.includes('rule.manage');
  const tenantId = principal.context.tenantId;
  const sessionId = principal.session.id;
  const policyRevision = principal.authorization.policyRevision;
  const revisionsQuery = useRuleRevisions(tenantId, sessionId, policyRevision, undefined, canManage);
  const bindingsQuery = useRuleBindings(tenantId, sessionId, policyRevision, site.id, canManage);
  const evidenceQuery = useRuleExecutionEvidence(tenantId, sessionId, policyRevision, site.id, canManage);
  const [selectedBindingId, setSelectedBindingId] = useState('');

  const revisions = revisionsQuery.data ?? [];
  const bindings = bindingsQuery.data ?? [];
  const evidence = evidenceQuery.data ?? [];
  const revisionById = useMemo(() => new Map(revisions.map((revision) => [revision.id, revision])), [revisions]);
  const orderedBindings = useMemo(() => bindings.slice().sort((left, right) => Number(right.active) - Number(left.active)
    || Date.parse(right.createdAt) - Date.parse(left.createdAt)), [bindings]);
  const selectedBinding = orderedBindings.find((binding) => binding.id === selectedBindingId) ?? orderedBindings[0] ?? null;
  const selectedRevision = selectedBinding ? revisionById.get(selectedBinding.ruleRevisionId) ?? null : null;
  const relatedRevisions = useMemo(() => selectedRevision
    ? revisions.filter((revision) => revision.ruleId === selectedRevision.ruleId).sort((left, right) => right.revision - left.revision)
    : [], [revisions, selectedRevision]);
  const selectedEvidence = useMemo(() => selectedBinding
    ? evidence.filter((item) => item.bindingId === selectedBinding.id).sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt))
    : [], [evidence, selectedBinding]);
  const latestEvidence = selectedEvidence[0] ?? null;
  const configFacts = useMemo(() => revisionConfigFacts(selectedRevision), [selectedRevision]);

  const visibleError = revisionsQuery.error ?? bindingsQuery.error ?? evidenceQuery.error;
  const error = visibleError ? presentRuleError(visibleError) : null;
  const loading = revisionsQuery.isPending || bindingsQuery.isPending || evidenceQuery.isPending;
  const activeBindings = bindings.filter((binding) => binding.active).length;

  if (!canManage) {
    return (
      <Card className="shadow-none">
        <CardHeader>
          <div className="mb-1 grid size-9 place-items-center rounded-md bg-warning/10 text-warning"><ShieldCheck className="size-4" /></div>
          <CardTitle>当前会话没有规则管理能力</CardTitle>
          <CardDescription>当前账号无权查看或管理规则版本、站点绑定和执行记录。告警页不会用静态示例代替真实规则。</CardDescription>
          <CardAction><Button variant="outline" size="sm" asChild><a href="/system?tab=rules">进入系统管理</a></Button></CardAction>
        </CardHeader>
      </Card>
    );
  }

  if (error && bindings.length === 0 && revisions.length === 0) {
    return <div className="rounded-md border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive"><strong>{error.title}</strong><p className="mt-1 text-xs">{error.description}</p></div>;
  }

  return (
    <div className="alarm-center__rules-workbench space-y-4" data-testid="alarm-rules-workbench">
      {error ? <div className="rounded-md border border-warning/30 bg-warning/5 p-3 text-xs text-warning"><strong>{error.title}</strong><p className="mt-1">{error.description}</p></div> : null}

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {[
          ['站点绑定', bindings.length, '当前站点规则'],
          ['当前生效', activeBindings, '正在参与判断'],
          ['发布版本', revisions.length, '已发布不可变版本'],
          ['执行记录', evidence.length, latestEvidence ? `最新：${statusLabel(latestEvidence.status)}` : '暂无执行记录'],
        ].map(([label, value, note]) => (
          <Card key={label} size="sm" className="shadow-none"><CardHeader className="gap-0"><CardTitle className="text-xs font-medium text-muted-foreground">{label}</CardTitle></CardHeader><CardContent><strong className="text-2xl font-semibold tabular-nums">{value}</strong><p className="mt-1 text-[11px] text-muted-foreground">{note}</p></CardContent></Card>
        ))}
      </div>

      <div className="grid gap-4 xl:grid-cols-[320px_minmax(0,1fr)]">
        <Card className="shadow-none">
          <CardHeader><CardTitle>规则绑定</CardTitle></CardHeader>
          <CardContent className="space-y-1">
            {loading && orderedBindings.length === 0 ? <div className="flex min-h-40 items-center justify-center gap-2 text-xs text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />正在读取规则绑定</div>
              : orderedBindings.length > 0 ? orderedBindings.map((binding) => {
                const revision = revisionById.get(binding.ruleRevisionId);
                const selected = binding.id === selectedBinding?.id;
                return (
                  <button key={binding.id} type="button" className={cn('w-full rounded-md px-3 py-2 text-left hover:bg-muted/50', selected && 'bg-muted')} onClick={() => setSelectedBindingId(binding.id)}>
                    <div className="flex items-center gap-2"><Badge variant="outline" className={binding.active ? 'border-success/30 text-success' : 'text-muted-foreground'}>{binding.active ? '生效' : '已退役'}</Badge><strong className="min-w-0 flex-1 truncate text-xs font-medium">{revision ? `告警规则 · r${revision.revision}` : '告警规则'}</strong></div>
                    <p className="mt-1 text-[11px] text-muted-foreground">优先级 {binding.priority} · {binding.active ? '当前参与告警判断' : '已停止参与告警判断'}</p>
                  </button>
                );
              }) : <div className="grid min-h-40 place-items-center text-xs text-muted-foreground">当前站点没有规则绑定</div>}
          </CardContent>
        </Card>

        <div className="min-w-0 space-y-4">
          {selectedBinding && selectedRevision ? (
            <>
              <Card className="shadow-none">
                <CardHeader>
                  <CardTitle className="flex items-center gap-2"><Badge variant="outline" className={selectedBinding.active ? 'border-success/30 text-success' : undefined}>{selectedBinding.active ? '已生效' : '已退役'}</Badge>告警规则 · r{selectedRevision.revision}</CardTitle>
                  <CardDescription>{site.displayName} · 优先级 {selectedBinding.priority}</CardDescription>
                  <CardAction className="flex gap-2"><Button variant="outline" size="sm" asChild><a href="/system?tab=rules"><FlaskConical />测试规则</a></Button><Button size="sm" asChild><a href="/system?tab=rules"><Settings2 />规则管理</a></Button></CardAction>
                </CardHeader>
                <CardContent className="space-y-5">
                  <section>
                    <div className="mb-3 flex items-center justify-between"><div className="flex items-center gap-2"><ShieldCheck className="size-4 text-muted-foreground" /><h3 className="text-xs font-medium">触发、恢复与通知配置</h3></div><span className="text-[11px] text-muted-foreground">当前已发布版本</span></div>
                    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                      {fieldGroups.map((group) => {
                        const matching = configFacts.filter((fact) => group.matcher(fact.key));
                        return (
                          <div key={group.key} className="rounded-md border p-3" data-rule-group={group.key}>
                            <span className="block text-[11px] text-muted-foreground">{group.label}</span>
                            {matching.length > 0 ? matching.slice(0, 3).map((fact) => <strong key={`${fact.nodeId}:${fact.key}`} className="mt-1 block text-xs font-medium">{presentConfigFact(fact)}</strong>) : <strong className="mt-1 block text-xs font-medium text-muted-foreground">—</strong>}
                            <small className="mt-1 block text-[10px] text-muted-foreground">{matching.length > 0 ? '当前发布版本' : '当前版本未配置此项'}</small>
                          </div>
                        );
                      })}
                    </div>
                  </section>
                  <div className="flex items-start gap-3 rounded-md border bg-muted/20 p-3"><Bell className="mt-0.5 size-4 shrink-0 text-muted-foreground" /><div><strong className="text-xs font-medium">通知配置以规则版本为准</strong><p className="mt-1 text-[11px] leading-5 text-muted-foreground">只有当前已发布规则中实际配置的通知对象和渠道才会显示；通知是否成功送达请在通知中查看。</p></div><Button className="ml-auto shrink-0" variant="outline" size="xs" asChild><a href="/notifications">通知</a></Button></div>
                </CardContent>
              </Card>

              <Card className="gap-0 py-0 shadow-none">
                <CardHeader className="border-b py-4"><CardTitle className="flex items-center gap-2"><Link2 className="size-4 text-muted-foreground" />执行状态</CardTitle><CardAction>{latestEvidence ? <RuleExecutionStatusBadge status={latestEvidence.status} /> : <Badge variant="outline">暂无执行</Badge>}</CardAction></CardHeader>
                <CardContent className="space-y-4">
                  <div className="grid grid-cols-2 overflow-hidden rounded-md border text-xs xl:grid-cols-4">
                    {[
                      ['当前版本', `r${selectedRevision.revision}`],
                      ['优先级', String(selectedBinding.priority)],
                      ['生效时间', formatRuleTime(selectedBinding.createdAt, site.timezone, 'medium')],
                      ['当前状态', selectedBinding.active ? '生效' : `已退役${selectedBinding.retiredAt ? ` · ${formatRuleTime(selectedBinding.retiredAt, site.timezone)}` : ''}`],
                    ].map(([label, value], index) => <div key={label} className={cn('p-3', index > 0 && 'border-l', index >= 2 && 'max-xl:border-t')}><span className="block text-[11px] text-muted-foreground">{label}</span><strong className="mt-1 block text-xs font-medium">{value}</strong></div>)}
                  </div>
                  {evidenceQuery.isPending ? <div className="flex min-h-28 items-center justify-center gap-2 text-xs text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />正在读取执行记录</div>
                    : selectedEvidence.length > 0 ? (
                      <Table aria-label="规则执行记录">
                        <TableHeader>
                          <TableRow className="hover:bg-transparent">
                            <TableHead>判断状态</TableHead>
                            <TableHead>结果</TableHead>
                            <TableHead>更新时间</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {selectedEvidence.slice(0, 8).map((item) => (
                            <TableRow key={item.executionId}>
                              <TableCell><RuleExecutionStatusBadge status={item.status} /></TableCell>
                              <TableCell>{item.status === 'SUCCEEDED' ? '规则判断已完成' : statusLabel(item.status)}</TableCell>
                              <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">{formatRuleTime(item.updatedAt, site.timezone)}</TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    ) : <div className="grid min-h-28 place-items-center text-xs text-muted-foreground">当前规则绑定没有执行记录</div>}
                </CardContent>
              </Card>

              <Card className="gap-0 py-0 shadow-none">
                <CardHeader className="border-b py-4"><CardTitle className="flex items-center gap-2"><History className="size-4 text-muted-foreground" />规则版本</CardTitle><CardDescription>已发布版本不可修改</CardDescription></CardHeader>
                <CardContent className="p-0">
                  <Table aria-label="规则版本历史">
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>版本</TableHead>
                        <TableHead>状态</TableHead>
                        <TableHead>说明</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {relatedRevisions.slice(0, 10).map((revision) => (
                        <TableRow key={revision.id}>
                          <TableCell className="font-mono font-medium tabular-nums">
                            r{revision.revision}
                            {revision.id === selectedRevision?.id ? <Badge variant="outline" className="ml-2">当前</Badge> : null}
                          </TableCell>
                          <TableCell><StatusBadge tone="success">已发布</StatusBadge></TableCell>
                          <TableCell className="text-muted-foreground">{revision.id === selectedRevision?.id ? '当前站点正在使用' : '历史已发布版本'}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            </>
          ) : <Card className="shadow-none"><CardContent className="grid min-h-64 place-items-center text-xs text-muted-foreground">{loading ? <span className="flex items-center gap-2"><LoaderCircle className="size-4 animate-spin" />正在读取规则详情</span> : '选择一个站点规则绑定查看规则与通知配置'}</CardContent></Card>}
        </div>
      </div>
    </div>
  );
}
