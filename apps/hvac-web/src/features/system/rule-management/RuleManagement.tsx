import { useEffect, useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { GitBranch, History, Play, Plus, Save, ShieldCheck, Trash2 } from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import type {
  CurrentPrincipalResponse,
  RuleBinding,
  RuleDraft,
  RuleEdge,
  RuleNode,
  RuleNodeDefinition,
  RuleRevision,
  RuleSimulationResult,
  RuleValidationResult,
  Site,
} from '@/api/generated/platformGateway.gen';
import {
  presentRuleError,
  ruleManagementApi,
  useRuleBindings,
  useRuleCatalog,
  useRuleExecutionEvidence,
  useRuleRevisions,
} from '@/api/rules';
import type { ProtectedScopeDraft } from '@/app/protected-scope';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';
import { Textarea } from '@/components/ui/textarea';
import {
  buildRollbackAssignment,
  canReleaseRuleDraft,
  createEmptyRuleDraft,
  deriveRulePermissions,
  diffRuleDraft,
  makeRuleNode,
  ruleDraftFingerprint,
} from './model';
import { confirmDiscardRuleDraft, useRuleDirtyGuard } from './useDirtyGuard';

interface Props {
  principal: CurrentPrincipalResponse;
  sites: readonly Site[];
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}

type WorkspaceTab = 'editor' | 'lifecycle' | 'evidence';

type SimulationInput = {
  eventId: string;
  schema: string;
  subjectType: string;
  subjectId: string;
  occurredAt: string;
  payload: string;
  frozenFactsRevision: string;
  frozenFacts: string;
};

const createSimulationInput = (): SimulationInput => ({
  eventId: `rule-simulation-${Date.now()}`,
  schema: 'telemetry.point.observed.v1',
  subjectType: 'POINT',
  subjectId: 'simulation-subject',
  occurredAt: new Date().toISOString(),
  payload: '{\n  "value": 42\n}',
  frozenFactsRevision: '',
  frozenFacts: '[]',
});

function executionStatusLabel(status: string): string {
  if (status === 'READY') return '已准备';
  if (status === 'RUNNING') return '执行中';
  if (status === 'WAITING') return '等待条件';
  if (status === 'BLOCKED_EFFECT') return '效果受阻';
  if (status === 'SUCCEEDED') return '成功';
  if (status === 'DEAD') return '终止';
  if (status === 'QUARANTINED') return '已隔离';
  if (status === 'FAILED') return '失败';
  return '未知';
}

function revisionDraft(revision: RuleRevision): RuleDraft {
  return {
    ruleId: revision.ruleId,
    catalogVersion: revision.catalogVersion,
    entryNodeId: revision.entryNodeId,
    nodes: revision.nodes.map((node) => ({ ...node, config: { ...node.config } })),
    edges: revision.edges.map((edge) => ({ ...edge })),
    allowedPermissions: [...revision.allowedPermissions],
    maxNodes: revision.maxNodes,
    maxDepth: revision.maxDepth,
    maxFanout: revision.maxFanout,
    maxResourceCost: revision.maxResourceCost,
    maxAttempts: revision.maxAttempts,
  };
}

function nodeLabel(node: RuleNode, nodes: readonly RuleNode[]): string {
  const index = Math.max(0, nodes.findIndex((candidate) => candidate.id === node.id));
  return `${node.definitionId} · 节点 ${index + 1}`;
}

export function RuleManagement({ principal, sites, registerUnsavedDraft }: Props) {
  const canManage = principal.authorization.capabilities.includes('rule.manage');
  const tenantId = principal.context.tenantId;
  const sessionId = principal.session.id;
  const policyRevision = principal.authorization.policyRevision;
  const [selectedSiteId, setSelectedSiteId] = useState<string | null>(sites[0]?.id ?? null);
  const [activeTab, setActiveTab] = useState<WorkspaceTab>('editor');
  const [draft, setDraft] = useState<RuleDraft>(() => createEmptyRuleDraft());
  const [dirty, setDirty] = useState(false);
  const [validation, setValidation] = useState<RuleValidationResult | null>(null);
  const [validatedFingerprint, setValidatedFingerprint] = useState<string | null>(null);
  const [selectedRevisionId, setSelectedRevisionId] = useState<string | null>(null);
  const [diffLines, setDiffLines] = useState<string[]>([]);
  const [simulationInput, setSimulationInput] = useState<SimulationInput>(() => createSimulationInput());
  const [simulationResult, setSimulationResult] = useState<RuleSimulationResult | null>(null);
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [working, setWorking] = useState<string | null>(null);
  const [retireBinding, setRetireBinding] = useState<RuleBinding | null>(null);
  const [retireReason, setRetireReason] = useState('停止当前规则应用');
  const [releaseConfirmOpen, setReleaseConfirmOpen] = useState(false);

  useRuleDirtyGuard(dirty);
  const catalogQuery = useRuleCatalog(tenantId, sessionId, policyRevision, canManage);
  const revisionsQuery = useRuleRevisions(tenantId, sessionId, policyRevision, undefined, canManage);
  const bindingsQuery = useRuleBindings(tenantId, sessionId, policyRevision, selectedSiteId, canManage);
  const evidenceQuery = useRuleExecutionEvidence(tenantId, sessionId, policyRevision, selectedSiteId, canManage);
  const catalog = catalogQuery.data;
  const revisions = revisionsQuery.data ?? [];
  const bindings = bindingsQuery.data ?? [];
  const evidence = evidenceQuery.data ?? [];
  const selectedRevision = revisions.find((revision) => revision.id === selectedRevisionId) ?? null;
  const definitionById = useMemo(() => new Map(catalog?.definitions.map((definition) => [definition.id, definition]) ?? []), [catalog]);
  const revisionById = useMemo(() => new Map(revisions.map((revision) => [revision.id, revision])), [revisions]);

  useEffect(() => {
    if (!sites.length) {
      setSelectedSiteId(null);
      return;
    }
    if (!selectedSiteId || !sites.some((site) => site.id === selectedSiteId)) setSelectedSiteId(sites[0].id);
  }, [selectedSiteId, sites]);

  useEffect(() => registerUnsavedDraft({
    id: 'rule-management-draft',
    label: '自动化规则管理',
    isDirty: () => dirty,
  }), [dirty, registerUnsavedDraft]);

  const invalidateValidation = () => {
    setValidation(null);
    setValidatedFingerprint(null);
    setSimulationResult(null);
  };

  const replaceDraft = (next: RuleDraft, markDirty = true) => {
    setDraft({ ...next, allowedPermissions: deriveRulePermissions(catalog, next.nodes) });
    setDirty(markDirty);
    invalidateValidation();
  };

  const updateNode = (nodeId: string, update: (node: RuleNode) => RuleNode) => {
    replaceDraft({ ...draft, nodes: draft.nodes.map((node) => node.id === nodeId ? update(node) : node) });
  };

  const addNode = (definitionId: string) => {
    const definition = definitionById.get(definitionId);
    if (!definition) return;
    const node = makeRuleNode(definition, draft.nodes);
    replaceDraft({ ...draft, entryNodeId: draft.entryNodeId || node.id, nodes: [...draft.nodes, node] });
  };

  const removeNode = (nodeId: string) => {
    replaceDraft({
      ...draft,
      entryNodeId: draft.entryNodeId === nodeId ? '' : draft.entryNodeId,
      nodes: draft.nodes.filter((node) => node.id !== nodeId),
      edges: draft.edges.filter((edge) => edge.fromNode !== nodeId && edge.toNode !== nodeId),
    });
  };

  const addEdge = () => {
    const fromNode = draft.nodes[0];
    const toNode = draft.nodes[1];
    if (!fromNode || !toNode) return;
    const fromPort = Object.keys(definitionById.get(fromNode.definitionId)?.outputs ?? {})[0];
    const toPort = Object.keys(definitionById.get(toNode.definitionId)?.inputs ?? {})[0];
    if (!fromPort || !toPort) return;
    replaceDraft({ ...draft, edges: [...draft.edges, { fromNode: fromNode.id, fromPort, toNode: toNode.id, toPort }] });
  };

  const updateEdge = (index: number, patch: Partial<RuleEdge>) => {
    replaceDraft({ ...draft, edges: draft.edges.map((edge, edgeIndex) => edgeIndex === index ? { ...edge, ...patch } : edge) });
  };

  const validate = async () => {
    setWorking('validate');
    setMutationError(null);
    try {
      const result = await ruleManagementApi.validate(draft, principal.session);
      setValidation(result);
      setValidatedFingerprint(ruleDraftFingerprint(draft));
    } catch (error) {
      setMutationError(error);
    } finally {
      setWorking(null);
    }
  };

  const simulate = async () => {
    setWorking('simulate');
    setMutationError(null);
    try {
      const result = await ruleManagementApi.simulate({
        draft,
        event: {
          eventId: simulationInput.eventId,
          schema: simulationInput.schema,
          siteId: selectedSiteId ?? undefined,
          subjectType: simulationInput.subjectType,
          subjectId: simulationInput.subjectId,
          occurredAt: simulationInput.occurredAt,
          payload: JSON.parse(simulationInput.payload) as unknown,
        },
        frozenFactsRevision: simulationInput.frozenFactsRevision || undefined,
        frozenFacts: JSON.parse(simulationInput.frozenFacts) as never[],
      }, principal.session);
      setSimulationResult(result);
    } catch (error) {
      setMutationError(error);
    } finally {
      setWorking(null);
    }
  };

  const release = async () => {
    if (!canReleaseRuleDraft(validation, validatedFingerprint, draft)) return;
    setWorking('release');
    setMutationError(null);
    try {
      const released = await ruleManagementApi.release(draft, principal.session);
      const nextDraft = revisionDraft(released);
      setDraft(nextDraft);
      setDirty(false);
      setValidation({ valid: true, digest: released.digest });
      setValidatedFingerprint(ruleDraftFingerprint(nextDraft));
      setSelectedRevisionId(released.id);
      setReleaseConfirmOpen(false);
      await revisionsQuery.refetch();
    } catch (error) {
      setMutationError(error);
    } finally {
      setWorking(null);
    }
  };

  const assignRevision = async (revision: RuleRevision) => {
    if (!selectedSiteId) return;
    setWorking(`assign:${revision.id}`);
    setMutationError(null);
    try {
      const activeBinding = bindings.find((binding) => binding.active && revisionById.get(binding.ruleRevisionId)?.ruleId === revision.ruleId);
      await ruleManagementApi.assign(activeBinding ? buildRollbackAssignment(activeBinding, revision) : {
        siteId: selectedSiteId,
        ruleRevisionId: revision.id,
        priority: 0,
      }, principal.session);
      await bindingsQuery.refetch();
    } catch (error) {
      setMutationError(error);
    } finally {
      setWorking(null);
    }
  };

  const retire = async () => {
    if (!retireBinding) return;
    setWorking(`retire:${retireBinding.id}`);
    setMutationError(null);
    try {
      await ruleManagementApi.retire(retireBinding.id, { siteId: retireBinding.siteId, reason: retireReason }, principal.session);
      setRetireBinding(null);
      await bindingsQuery.refetch();
    } catch (error) {
      setMutationError(error);
    } finally {
      setWorking(null);
    }
  };

  const changeSite = (siteId: string) => {
    if (!confirmDiscardRuleDraft(dirty)) return;
    setDirty(false);
    setSelectedSiteId(siteId);
  };

  const changeTab = (next: WorkspaceTab) => {
    if (next !== activeTab && !confirmDiscardRuleDraft(dirty)) return;
    setActiveTab(next);
  };

  type EvidenceRow = (typeof evidence)[number];

  const revisionColumns = useMemo<Array<ColumnDef<DataTableFeatures, RuleRevision>>>(() => [
    { id: 'revision', header: '版本', cell: ({ row }) => <strong className="font-semibold text-foreground">第 {row.original.revision} 版</strong> },
    { id: 'structure', header: '结构', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.nodes.length} 个节点 · {row.original.edges.length} 条连线</span> },
    { id: 'permissions', header: '权限需求', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.allowedPermissions.length} 项</span> },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <div className="flex flex-wrap gap-1">
          <Button size="sm" variant="ghost" onClick={() => { if (confirmDiscardRuleDraft(dirty)) { replaceDraft(revisionDraft(row.original), false); setSelectedRevisionId(row.original.id); setActiveTab('editor'); } }}>作为草稿打开</Button>
          <Button size="sm" variant="ghost" onClick={() => { setSelectedRevisionId(row.original.id); setDiffLines(diffRuleDraft(draft, row.original)); setActiveTab('editor'); }}>比较差异</Button>
          <Button size="sm" disabled={!selectedSiteId || working === `assign:${row.original.id}`} onClick={() => void assignRevision(row.original)}>{working === `assign:${row.original.id}` ? '正在应用…' : '应用 / 回滚到此版本'}</Button>
        </div>
      ),
      enableSorting: false,
    },
  ], [dirty, draft, selectedSiteId, working]);

  const revisionTable = useDataTable({
    key: 'rule-management-revisions',
    data: [...revisions],
    columns: revisionColumns,
    paginate: false,
    getRowId: (row) => row.id,
  });

  const bindingColumns = useMemo<Array<ColumnDef<DataTableFeatures, RuleBinding>>>(() => [
    { id: 'revision', header: '规则版本', cell: ({ row }) => <span className="font-medium text-foreground">{revisionById.get(row.original.ruleRevisionId) ? `第 ${revisionById.get(row.original.ruleRevisionId)!.revision} 版` : '已发布版本'}</span> },
    { id: 'priority', header: '优先级', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.priority}</span> },
    { id: 'status', header: '状态', cell: ({ row }) => <StatusBadge tone={row.original.active ? 'success' : 'neutral'} label={row.original.active ? '生效中' : '已退役'} /> },
    { id: 'actions', header: '操作', cell: ({ row }) => row.original.active ? <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => setRetireBinding(row.original)}>停止应用</Button> : null, enableSorting: false },
  ], [revisionById]);

  const bindingTable = useDataTable({
    key: `rule-management-bindings-${selectedSiteId ?? 'none'}`,
    data: [...bindings],
    columns: bindingColumns,
    paginate: false,
    getRowId: (row) => `${row.id}:${row.revision}`,
  });

  const evidenceColumns = useMemo<Array<ColumnDef<DataTableFeatures, EvidenceRow>>>(() => [
    {
      id: 'status',
      header: '状态',
      cell: ({ row }) => {
        const tone = row.original.status === 'SUCCEEDED' ? 'success' : row.original.status === 'FAILED' || row.original.status === 'DEAD' ? 'destructive' : row.original.status === 'WAITING' || row.original.status === 'BLOCKED_EFFECT' || row.original.status === 'QUARANTINED' ? 'warning' : 'neutral';
        return <StatusBadge tone={tone} label={executionStatusLabel(row.original.status)} />;
      },
    },
    { id: 'terminalCode', header: '终止原因', cell: ({ row }) => row.original.terminalCode ?? '—' },
    { id: 'trace', header: '判断轨迹', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.trace.length} 项</span> },
    { id: 'effects', header: '效果记录', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.effects.length} 项</span> },
    { id: 'updatedAt', header: '更新时间', cell: ({ row }) => <span className="font-mono tabular-nums">{new Date(row.original.updatedAt).toLocaleString()}</span> },
  ], []);

  const evidenceTable = useDataTable({
    key: `rule-management-evidence-${selectedSiteId ?? 'none'}`,
    data: [...evidence],
    columns: evidenceColumns,
    paginate: false,
    getRowId: (row) => `${row.updatedAt}:${row.status}:${row.terminalCode ?? 'none'}`,
  });

  if (!canManage) {
    return <Alert><AlertTitle>当前为只读状态</AlertTitle><AlertDescription>当前用户没有自动化规则维护权限，因此不会读取或修改规则数据。</AlertDescription></Alert>;
  }

  const visibleError = mutationError ?? catalogQuery.error ?? revisionsQuery.error ?? bindingsQuery.error ?? evidenceQuery.error;
  const errorPresentation = visibleError ? presentRuleError(visibleError) : null;

  return (
    <div className="space-y-4">
      {errorPresentation ? <Alert variant="destructive"><AlertTitle>{errorPresentation.title}</AlertTitle><AlertDescription>{errorPresentation.description}</AlertDescription></Alert> : null}

      <Card>
        <CardHeader><CardTitle className="flex items-center gap-2"><GitBranch className="size-4" />自动化规则</CardTitle><CardDescription>规则发布后形成不可变版本；应用、回滚和退役均保留独立历史。</CardDescription></CardHeader>
        <CardContent className="flex flex-wrap items-center gap-3">
          <Select value={selectedSiteId ?? undefined} onValueChange={changeSite} disabled={sites.length === 0}>
            <SelectTrigger className="min-w-72"><SelectValue placeholder="选择站点" /></SelectTrigger>
            <SelectContent><SelectGroup>{sites.map((site) => <SelectItem key={site.id} value={site.id}>{site.displayName} · {site.code}</SelectItem>)}</SelectGroup></SelectContent>
          </Select>
          <Badge variant="outline">已发布规则</Badge>
          {dirty ? <Badge variant="outline" className="border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">有未保存修改</Badge> : null}
        </CardContent>
      </Card>

      <div className="inline-flex flex-wrap rounded-md border bg-muted/30 p-1" role="tablist" aria-label="规则管理工作区">
        <Button size="sm" variant={activeTab === 'editor' ? 'secondary' : 'ghost'} role="tab" aria-selected={activeTab === 'editor'} onClick={() => changeTab('editor')}>规则设计</Button>
        <Button size="sm" variant={activeTab === 'lifecycle' ? 'secondary' : 'ghost'} role="tab" aria-selected={activeTab === 'lifecycle'} onClick={() => changeTab('lifecycle')}>版本与应用</Button>
        <Button size="sm" variant={activeTab === 'evidence' ? 'secondary' : 'ghost'} role="tab" aria-selected={activeTab === 'evidence'} onClick={() => changeTab('evidence')}>运行证据</Button>
      </div>

      {activeTab === 'editor' ? (
        <RuleEditor
          catalog={catalog?.definitions ?? []}
          draft={draft}
          validation={validation}
          selectedRevision={selectedRevision}
          diffLines={diffLines}
          simulationInput={simulationInput}
          simulationResult={simulationResult}
          working={working}
          onAddNode={addNode}
          onRemoveNode={removeNode}
          onNodeChange={updateNode}
          onDraftChange={replaceDraft}
          onAddEdge={addEdge}
          onEdgeChange={updateEdge}
          onDiff={() => setDiffLines(diffRuleDraft(draft, selectedRevision))}
          onSimulationInput={setSimulationInput}
          onValidate={() => void validate()}
          onSimulate={() => void simulate()}
          onRelease={() => setReleaseConfirmOpen(true)}
          canRelease={canReleaseRuleDraft(validation, validatedFingerprint, draft)}
          definitionById={definitionById}
        />
      ) : null}

      {activeTab === 'lifecycle' ? (
        <div className="space-y-4">
          <DataTableBlock
            title="已发布版本"
            description={`${revisions.length} 个已发布版本`}
          >
            <DataTable
              table={revisionTable}
              tableAriaLabel="已发布规则版本"
              empty="尚无已发布规则版本"
              getHeaderCellProps={(header) => ({
                className: header.id === 'actions' ? 'w-80' : undefined,
              })}
            />
          </DataTableBlock>

          <DataTableBlock
            title="当前站点规则应用"
            description="切换或回滚不会改写历史版本"
          >
            <DataTable
              table={bindingTable}
              tableAriaLabel="当前站点规则应用"
              empty="当前站点没有规则应用记录"
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'priority' ? 'text-right' :
                  header.id === 'status' ? 'w-28 text-center' :
                  header.id === 'actions' ? 'w-32' :
                  undefined,
              })}
              getCellProps={(cell) => ({
                className:
                  cell.column.id === 'priority' ? 'text-right' :
                  cell.column.id === 'status' ? 'text-center' :
                  undefined,
              })}
            />
          </DataTableBlock>
        </div>
      ) : null}

      {activeTab === 'evidence' ? (
        <DataTableBlock
          title="规则运行证据"
          description="完整记录可在审计中查看"
        >
          {!selectedSiteId ? (
            <div className="rounded-md border p-5 text-sm text-muted-foreground">选择站点后查看运行证据</div>
          ) : (
            <DataTable
              table={evidenceTable}
              tableAriaLabel="规则运行证据"
              empty="当前站点没有规则执行证据"
              getHeaderCellProps={(header) => ({
                className: header.id === 'status' ? 'w-28 text-center' : undefined,
              })}
              getCellProps={(cell) => ({
                className: cell.column.id === 'status' ? 'text-center' : undefined,
              })}
            />
          )}
        </DataTableBlock>
      ) : null}

      <Dialog open={Boolean(retireBinding)} onOpenChange={(open) => { if (!open) setRetireBinding(null); }}>
        <DialogContent>
          <DialogHeader><DialogTitle>停止当前规则应用</DialogTitle><DialogDescription>停止应用只追加退役记录，不修改已发布规则版本。</DialogDescription></DialogHeader>
          <Alert><AlertTitle>历史版本保持不变</AlertTitle><AlertDescription>该操作只结束当前应用关系；需要恢复时可以重新应用已有版本。</AlertDescription></Alert>
          <label className="grid gap-1.5 text-sm"><span>停止原因</span><Textarea rows={3} maxLength={256} value={retireReason} onChange={(event) => setRetireReason(event.target.value)} /></label>
          <DialogFooter><Button variant="outline" onClick={() => setRetireBinding(null)}>取消</Button><Button variant="destructive" disabled={!retireReason.trim() || Boolean(retireBinding && working === `retire:${retireBinding.id}`)} onClick={() => void retire()}>{retireBinding && working === `retire:${retireBinding.id}` ? '正在处理…' : '确认停止'}</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={releaseConfirmOpen} onOpenChange={setReleaseConfirmOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>发布新的规则版本</DialogTitle><DialogDescription>发布后该版本不可修改。后续调整会形成新的版本。</DialogDescription></DialogHeader>
          <Alert><AlertTitle>请确认已经完成服务端校验</AlertTitle><AlertDescription>只有当前草稿与最近一次通过校验的内容一致时才能发布。</AlertDescription></Alert>
          <DialogFooter><Button variant="outline" onClick={() => setReleaseConfirmOpen(false)}>取消</Button><Button disabled={working === 'release'} onClick={() => void release()}>{working === 'release' ? '正在发布…' : '发布不可变版本'}</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

interface EditorProps {
  catalog: readonly RuleNodeDefinition[];
  draft: RuleDraft;
  validation: RuleValidationResult | null;
  selectedRevision: RuleRevision | null;
  diffLines: readonly string[];
  simulationInput: SimulationInput;
  simulationResult: RuleSimulationResult | null;
  working: string | null;
  canRelease: boolean;
  definitionById: ReadonlyMap<string, RuleNodeDefinition>;
  onAddNode(definitionId: string): void;
  onRemoveNode(nodeId: string): void;
  onNodeChange(nodeId: string, update: (node: RuleNode) => RuleNode): void;
  onDraftChange(draft: RuleDraft): void;
  onAddEdge(): void;
  onEdgeChange(index: number, patch: Partial<RuleEdge>): void;
  onDiff(): void;
  onSimulationInput(value: SimulationInput): void;
  onValidate(): void;
  onSimulate(): void;
  onRelease(): void;
}

function RuleEditor(props: EditorProps) {
  const { catalog, draft, validation, selectedRevision, diffLines, simulationInput, simulationResult, working, canRelease, definitionById } = props;
  return (
    <div className="space-y-4">
      <Alert><AlertTitle>浏览器只编辑受约束的规则草稿</AlertTitle><AlertDescription>节点只能来自批准目录；所需权限由节点自动推导，不能在浏览器中写入任意执行代码或绕过权限边界。</AlertDescription></Alert>

      <Card>
        <CardHeader><CardTitle>添加规则节点</CardTitle><CardDescription>从已批准节点目录选择能力加入当前规则。</CardDescription></CardHeader>
        <CardContent>
          <Select onValueChange={props.onAddNode}>
            <SelectTrigger className="max-w-md"><SelectValue placeholder="选择可用节点" /></SelectTrigger>
            <SelectContent><SelectGroup>{catalog.map((definition) => <SelectItem key={definition.id} value={definition.id}>{definition.id} · 资源成本 {definition.resourceCost}</SelectItem>)}</SelectGroup></SelectContent>
          </Select>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>规则节点</CardTitle><CardDescription>{draft.nodes.length} 个节点。</CardDescription></CardHeader>
        <CardContent className="space-y-3">
          {draft.nodes.length === 0 ? <div className="rounded-md border border-dashed p-5 text-sm text-muted-foreground">从批准目录添加节点</div> : draft.nodes.map((node) => <RuleNodeEditor key={node.id} node={node} nodes={draft.nodes} definition={definitionById.get(node.definitionId)} onChange={(update) => props.onNodeChange(node.id, update)} onRemove={() => props.onRemoveNode(node.id)} />)}
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>入口与连线</CardTitle><CardDescription>每条连线必须连接兼容的输入与输出端口。</CardDescription></CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-end gap-3">
            <label className="grid min-w-72 gap-1.5 text-sm"><span>入口节点</span><Select value={draft.entryNodeId || undefined} onValueChange={(entryNodeId) => props.onDraftChange({ ...draft, entryNodeId })}><SelectTrigger><SelectValue placeholder="选择入口节点" /></SelectTrigger><SelectContent><SelectGroup>{draft.nodes.map((node) => <SelectItem key={node.id} value={node.id}>{nodeLabel(node, draft.nodes)}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
            <Button variant="outline" disabled={draft.nodes.length < 2} onClick={props.onAddEdge}><Plus />添加连线</Button>
          </div>
          <div className="space-y-2">{draft.edges.length === 0 ? <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">当前没有节点连线</div> : draft.edges.map((edge, index) => <RuleEdgeEditor key={`${index}-${edge.fromNode}-${edge.toNode}`} edge={edge} nodes={draft.nodes} definitionById={definitionById} onChange={(patch) => props.onEdgeChange(index, patch)} onRemove={() => props.onDraftChange({ ...draft, edges: draft.edges.filter((_, edgeIndex) => edgeIndex !== index) })} />)}</div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>执行预算与权限需求</CardTitle><CardDescription>资源预算限制规则复杂度；权限需求由当前节点自动推导。</CardDescription></CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
            {([
              ['maxNodes', '最大节点数'],
              ['maxDepth', '最大深度'],
              ['maxFanout', '最大分支数'],
              ['maxResourceCost', '最大资源成本'],
              ['maxAttempts', '最大尝试次数'],
            ] as const).map(([key, label]) => <label className="grid gap-1.5 text-sm" key={key}><span>{label}</span><Input type="number" min={1} value={draft[key]} onChange={(event) => props.onDraftChange({ ...draft, [key]: Math.max(1, Number(event.target.value) || 1) })} /></label>)}
          </div>
          <div className="flex flex-wrap gap-1">{draft.allowedPermissions.length ? draft.allowedPermissions.map((permission) => <Badge key={permission} variant="outline">{permission}</Badge>) : <Badge variant="outline">无需额外业务权限</Badge>}</div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>校验、测试与发布</CardTitle><CardDescription>发布前必须通过服务端校验；模拟不会产生正式业务效果。</CardDescription></CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap gap-2">
            <Button disabled={working === 'validate'} onClick={props.onValidate}><ShieldCheck />{working === 'validate' ? '正在校验…' : '校验规则'}</Button>
            <Button variant="outline" disabled={!selectedRevision} onClick={props.onDiff}><History />比较已发布版本</Button>
            <Button variant="outline" disabled={working === 'simulate'} onClick={props.onSimulate}><Play />{working === 'simulate' ? '正在模拟…' : '模拟执行'}</Button>
            <Button disabled={!canRelease || working === 'release'} onClick={props.onRelease}><Save />发布新版本</Button>
          </div>

          {validation ? <Alert variant={validation.valid ? 'default' : 'destructive'}><AlertTitle>{validation.valid ? '服务端校验通过' : '规则校验未通过'}</AlertTitle><AlertDescription>{validation.valid ? '当前草稿可以进入发布确认。' : (validation.error?.detail ?? '请检查规则结构和配置。')}</AlertDescription></Alert> : null}

          {diffLines.length ? <div className="rounded-md border bg-muted/20 p-3"><strong className="mb-2 block text-sm">草稿与已发布版本差异</strong><pre className="max-h-64 overflow-auto whitespace-pre-wrap text-xs">{diffLines.join('\n')}</pre></div> : null}

          <div className="grid gap-4 lg:grid-cols-2">
            <label className="grid gap-1.5 text-sm"><span>模拟事件内容 JSON</span><Textarea rows={7} className="font-mono text-xs" value={simulationInput.payload} onChange={(event) => props.onSimulationInput({ ...simulationInput, payload: event.target.value, eventId: `rule-simulation-${Date.now()}`, occurredAt: new Date().toISOString() })} /></label>
            <div className="rounded-md border p-4">
              {simulationResult ? (
                <dl className="mt-3 grid gap-3 text-sm">
                  <div className="flex justify-between gap-4">
                    <dt className="text-muted-foreground">状态</dt>
                    <dd>
                      <StatusBadge
                        tone={simulationResult.status === 'SUCCEEDED' ? 'success' : simulationResult.status === 'FAILED' || simulationResult.status === 'DEAD' ? 'destructive' : simulationResult.status === 'WAITING' || simulationResult.status === 'BLOCKED_EFFECT' || simulationResult.status === 'QUARANTINED' ? 'warning' : 'neutral'}
                        label={executionStatusLabel(simulationResult.status)}
                      />
                    </dd>
                  </div>
                  <div className="flex justify-between gap-4"><dt className="text-muted-foreground">终止原因</dt><dd>{simulationResult.terminalCode ?? '—'}</dd></div>
                  <div className="flex justify-between gap-4"><dt className="text-muted-foreground">判断轨迹</dt><dd>{simulationResult.trace.length} 项</dd></div>
                  <div className="flex justify-between gap-4"><dt className="text-muted-foreground">效果记录</dt><dd>{simulationResult.effects.length} 项</dd></div>
                </dl>
              ) : <p className="mt-2 text-sm text-muted-foreground">尚未运行模拟。</p>}
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function RuleNodeEditor({ node, nodes, definition, onChange, onRemove }: { node: RuleNode; nodes: readonly RuleNode[]; definition?: RuleNodeDefinition; onChange(update: (node: RuleNode) => RuleNode): void; onRemove(): void }) {
  return (
    <div className="rounded-md border p-4">
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3"><div><strong className="block text-sm">{nodeLabel(node, nodes)}</strong><span className="text-xs text-muted-foreground">输入 {Object.keys(definition?.inputs ?? {}).length} · 输出 {Object.keys(definition?.outputs ?? {}).length} · 资源成本 {definition?.resourceCost ?? 0}</span></div><Button variant="ghost" size="sm" className="text-destructive hover:text-destructive" onClick={onRemove}><Trash2 />移除</Button></div>
      <div className="grid gap-4 lg:grid-cols-[280px_minmax(0,1fr)]">
        <dl className="grid content-start overflow-hidden rounded-md border text-sm">
          <div className="grid grid-cols-[86px_1fr] gap-2 p-2"><dt className="text-muted-foreground">输入</dt><dd>{Object.entries(definition?.inputs ?? {}).map(([key, type]) => `${key}:${type}`).join('、') || '无'}</dd></div>
          <div className="grid grid-cols-[86px_1fr] gap-2 border-t p-2"><dt className="text-muted-foreground">输出</dt><dd>{Object.entries(definition?.outputs ?? {}).map(([key, type]) => `${key}:${type}`).join('、') || '无'}</dd></div>
          <div className="grid grid-cols-[86px_1fr] gap-2 border-t p-2"><dt className="text-muted-foreground">权限</dt><dd>{definition?.requiredPermission ?? '无需额外权限'}</dd></div>
        </dl>
        <div className="grid gap-3">{(definition?.configFields ?? []).map((field) => <RuleConfigEditor key={field.name} field={field} value={node.config[field.name]} onValue={(value) => onChange((current) => ({ ...current, config: { ...current.config, [field.name]: value } }))} />)}{!definition?.configFields.length ? <span className="text-sm text-muted-foreground">此节点没有可编辑配置。</span> : null}</div>
      </div>
    </div>
  );
}

function RuleConfigEditor({ field, value, onValue }: { field: RuleNodeDefinition['configFields'][number]; value: unknown; onValue(value: unknown): void }) {
  if (field.kind === 'ENUM') {
    return <label className="grid gap-1.5 text-sm"><span>{field.name}</span><Select value={typeof value === 'string' ? value : undefined} onValueChange={onValue}><SelectTrigger><SelectValue placeholder="请选择" /></SelectTrigger><SelectContent><SelectGroup>{(field.options ?? []).map((option) => <SelectItem key={option} value={option}>{option}</SelectItem>)}</SelectGroup></SelectContent></Select></label>;
  }
  if (field.kind === 'NUMBER' || field.kind === 'POSITIVE_INTEGER') {
    return <label className="grid gap-1.5 text-sm"><span>{field.name}</span><Input type="number" min={field.kind === 'POSITIVE_INTEGER' ? 1 : undefined} value={typeof value === 'number' ? value : ''} onChange={(event) => onValue(event.target.value === '' ? (field.kind === 'POSITIVE_INTEGER' ? 1 : 0) : Number(event.target.value))} /></label>;
  }
  return <label className="grid gap-1.5 text-sm"><span>{field.name}</span><Input value={field.kind === 'STRING_LIST' ? Array.isArray(value) ? value.join(', ') : '' : typeof value === 'string' ? value : ''} onChange={(event) => onValue(field.kind === 'STRING_LIST' ? event.target.value.split(',').map((item) => item.trim()).filter(Boolean) : event.target.value)} /></label>;
}

function RuleEdgeEditor({ edge, nodes, definitionById, onChange, onRemove }: { edge: RuleEdge; nodes: readonly RuleNode[]; definitionById: ReadonlyMap<string, RuleNodeDefinition>; onChange(patch: Partial<RuleEdge>): void; onRemove(): void }) {
  const fromDefinition = definitionById.get(nodes.find((node) => node.id === edge.fromNode)?.definitionId ?? '');
  const toDefinition = definitionById.get(nodes.find((node) => node.id === edge.toNode)?.definitionId ?? '');
  return (
    <div className="grid gap-3 rounded-md border p-3 lg:grid-cols-[1fr_180px_auto_1fr_180px_auto] lg:items-end">
      <label className="grid gap-1.5 text-sm"><span>来源节点</span><Select value={edge.fromNode} onValueChange={(fromNode) => { const definition = definitionById.get(nodes.find((node) => node.id === fromNode)?.definitionId ?? ''); onChange({ fromNode, fromPort: Object.keys(definition?.outputs ?? {})[0] ?? '' }); }}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{nodes.map((node) => <SelectItem key={node.id} value={node.id}>{nodeLabel(node, nodes)}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
      <label className="grid gap-1.5 text-sm"><span>输出端口</span><Select value={edge.fromPort} onValueChange={(fromPort) => onChange({ fromPort })}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{Object.keys(fromDefinition?.outputs ?? {}).map((port) => <SelectItem key={port} value={port}>{port}:{fromDefinition?.outputs[port]}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
      <span className="hidden pb-2 text-muted-foreground lg:inline">→</span>
      <label className="grid gap-1.5 text-sm"><span>目标节点</span><Select value={edge.toNode} onValueChange={(toNode) => { const definition = definitionById.get(nodes.find((node) => node.id === toNode)?.definitionId ?? ''); onChange({ toNode, toPort: Object.keys(definition?.inputs ?? {})[0] ?? '' }); }}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{nodes.map((node) => <SelectItem key={node.id} value={node.id}>{nodeLabel(node, nodes)}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
      <label className="grid gap-1.5 text-sm"><span>输入端口</span><Select value={edge.toPort} onValueChange={(toPort) => onChange({ toPort })}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{Object.keys(toDefinition?.inputs ?? {}).map((port) => <SelectItem key={port} value={port}>{port}:{toDefinition?.inputs[port]}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
      <Button variant="ghost" className="text-destructive hover:text-destructive" onClick={onRemove}><Trash2 />移除</Button>
    </div>
  );
}
