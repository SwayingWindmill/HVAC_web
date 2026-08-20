import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type {
  CurrentPrincipalResponse,
  RuleBinding,
  RuleDraft,
  RuleEdge,
  RuleExecutionEvidence,
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
import type { ProtectedScopeDraft } from '../protected-scope';
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

const statusColor = (status: string) => {
  if (status === 'SUCCEEDED') return 'green';
  if (status === 'QUARANTINED' || status === 'WAITING' || status === 'BLOCKED_EFFECT') return 'orange';
  if (status === 'DEAD' || status === 'FAILED') return 'red';
  return 'blue';
};

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

export function RuleManagement({ principal, sites, registerUnsavedDraft }: Props) {
  const canManage = principal.authorization.capabilities.includes('rule.manage');
  const tenantId = principal.context.tenantId;
  const sessionId = principal.session.id;
  const policyRevision = principal.authorization.policyRevision;
  const [selectedSiteId, setSelectedSiteId] = useState<string | null>(sites[0]?.id ?? null);
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
  const [retireReason, setRetireReason] = useState('停止当前 Rule assignment');

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
    label: selectedSiteId ? `Rule 管理 · ${selectedSiteId}` : 'Rule 管理',
    isDirty: () => dirty,
  }), [dirty, registerUnsavedDraft, selectedSiteId]);

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

  if (!canManage) {
    return <Alert type="warning" showIcon message="当前 Principal 没有 rule.manage capability" description="浏览器不会读取 Rule owner 数据，也不会回退到本地规则或 Demo 规则。" />;
  }

  const visibleError = mutationError ?? catalogQuery.error ?? revisionsQuery.error ?? bindingsQuery.error ?? evidenceQuery.error;
  const errorPresentation = visibleError ? presentRuleError(visibleError) : null;

  const revisionColumns: ColumnsType<RuleRevision> = [
    { title: 'Rule / Revision', render: (_, row) => <Space direction="vertical" size={0}><Typography.Text code>{row.ruleId}</Typography.Text><Typography.Text type="secondary">Revision {row.revision}</Typography.Text></Space> },
    { title: '结构', render: (_, row) => `${row.nodes.length} nodes / ${row.edges.length} edges` },
    { title: 'Digest', dataIndex: 'digest', render: (value: string) => <Typography.Text code copyable={{ text: value }}>{value.slice(0, 12)}…</Typography.Text> },
    { title: '操作', render: (_, row) => <Space wrap>
      <Button size="small" onClick={() => { if (confirmDiscardRuleDraft(dirty)) { replaceDraft(revisionDraft(row), false); setSelectedRevisionId(row.id); } }}>作为草稿打开</Button>
      <Button size="small" onClick={() => { setSelectedRevisionId(row.id); setDiffLines(diffRuleDraft(draft, row)); }}>Diff</Button>
      <Button size="small" type="primary" disabled={!selectedSiteId} loading={working === `assign:${row.id}`} onClick={() => void assignRevision(row)}>Assign / Rollback</Button>
    </Space> },
  ];

  const bindingColumns: ColumnsType<RuleBinding> = [
    { title: 'Binding', dataIndex: 'id', render: (value: string) => <Typography.Text code>{value.slice(0, 12)}…</Typography.Text> },
    { title: 'Revision', dataIndex: 'revision' },
    { title: 'Rule Revision', render: (_, row) => { const revision = revisionById.get(row.ruleRevisionId); return revision ? `${revision.ruleId.slice(0, 12)}… / r${revision.revision}` : row.ruleRevisionId; } },
    { title: 'Priority', dataIndex: 'priority' },
    { title: '状态', render: (_, row) => <Tag color={row.active ? 'green' : 'default'}>{row.active ? 'ACTIVE' : 'RETIRED'}</Tag> },
    { title: '操作', render: (_, row) => row.active ? <Button danger size="small" onClick={() => setRetireBinding(row)}>Retire</Button> : null },
  ];

  const evidenceColumns: ColumnsType<RuleExecutionEvidence> = [
    { title: 'Execution', dataIndex: 'executionId', render: (value: string) => <Typography.Text code copyable>{value}</Typography.Text> },
    { title: '状态', dataIndex: 'status', render: (value: string) => <Tag color={statusColor(value)}>{value}</Tag> },
    { title: 'Terminal', dataIndex: 'terminalCode', render: (value?: string) => value ?? '—' },
    { title: 'Binding Revision', dataIndex: 'bindingRevision' },
    { title: 'Updated', dataIndex: 'updatedAt' },
  ];

  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    {errorPresentation ? <Alert type="error" showIcon message={errorPresentation.title} description={errorPresentation.description} /> : null}
    <Card>
      <Space wrap>
        <Select style={{ minWidth: 300 }} value={selectedSiteId ?? undefined} placeholder="选择授权 Site" options={sites.map((site) => ({ value: site.id, label: `${site.displayName} · ${site.code}` }))} onChange={changeSite} />
        <Tag color="blue">rule-runtime-service owner</Tag>
        <Tag>generated OpenAPI client</Tag>
        {dirty ? <Tag color="orange">unsaved draft protected</Tag> : null}
      </Space>
    </Card>
    <Tabs destroyOnHidden items={[
      { key: 'editor', label: 'Typed Graph Draft', children: <RuleEditor catalog={catalog?.definitions ?? []} draft={draft} validation={validation} selectedRevision={selectedRevision} diffLines={diffLines} simulationInput={simulationInput} simulationResult={simulationResult} working={working} onAddNode={addNode} onRemoveNode={removeNode} onNodeChange={updateNode} onDraftChange={replaceDraft} onAddEdge={addEdge} onEdgeChange={updateEdge} onDiff={() => setDiffLines(diffRuleDraft(draft, selectedRevision))} onSimulationInput={setSimulationInput} onValidate={() => void validate()} onSimulate={() => void simulate()} onRelease={() => void release()} canRelease={canReleaseRuleDraft(validation, validatedFingerprint, draft)} definitionById={definitionById} /> },
      { key: 'lifecycle', label: 'Release / Assign / Rollback', children: <Space direction="vertical" size={16} style={{ width: '100%' }}><Card title={`Released RuleRevisions · ${revisions.length}`}><Table rowKey="id" columns={revisionColumns} dataSource={revisions} pagination={{ pageSize: 10 }} scroll={{ x: 1000 }} /></Card><Card title={`Current Rule bindings · ${bindings.length}`}><Table rowKey={(row) => `${row.id}:${row.revision}`} columns={bindingColumns} dataSource={bindings} pagination={false} scroll={{ x: 900 }} /></Card></Space> },
      { key: 'evidence', label: 'Trace / Dead / Quarantine', children: <Card title="Rule execution evidence">{!selectedSiteId ? <Empty description="选择 Site 后读取 scoped evidence" /> : <Table rowKey="executionId" columns={evidenceColumns} dataSource={evidence} pagination={{ pageSize: 10 }} expandable={{ expandedRowRender: (row) => <Row gutter={[12, 12]}><Col xs={24} xl={12}><Typography.Text strong>Trace</Typography.Text><pre style={{ overflow: 'auto' }}>{JSON.stringify(row.trace, null, 2)}</pre></Col><Col xs={24} xl={12}><Typography.Text strong>Effects</Typography.Text><pre style={{ overflow: 'auto' }}>{JSON.stringify(row.effects, null, 2)}</pre></Col></Row> }} locale={{ emptyText: <Empty description="当前 Site 没有 Rule execution evidence" /> }} scroll={{ x: 900 }} />}</Card> },
    ]} />
    <Modal title="Retire Rule binding" open={Boolean(retireBinding)} onCancel={() => setRetireBinding(null)} onOk={() => void retire()} confirmLoading={Boolean(retireBinding && working === `retire:${retireBinding.id}`)} okButtonProps={{ danger: true, disabled: !retireReason.trim() }}>
      <Alert type="warning" showIcon message="Retire 只追加当前 Binding Revision 的 retirement 证据，不修改历史 RuleRevision。" style={{ marginBottom: 12 }} />
      <Input.TextArea rows={3} value={retireReason} maxLength={256} onChange={(event) => setRetireReason(event.target.value)} />
    </Modal>
  </Space>;
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
  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    <Alert type="info" showIcon message="浏览器只编辑 typed draft；Rule Runtime 编译结果才决定能否发布" description="节点来自 core.v1 Catalog；requiredPermission 自动推导，不能手写任意权限、执行代码或敏感配置。" />
    <Card title="Typed Node Catalog"><Select<string> style={{ minWidth: 320 }} placeholder="选择批准的 NodeDefinition" value={undefined} onSelect={props.onAddNode} options={catalog.map((definition) => ({ value: definition.id, label: `${definition.id} · cost ${definition.resourceCost}` }))} /></Card>
    <Card title="Graph Nodes">{!draft.nodes.length ? <Empty description="从 approved catalog 添加节点" /> : <Space direction="vertical" size={12} style={{ width: '100%' }}>{draft.nodes.map((node) => <RuleNodeEditor key={node.id} node={node} definition={definitionById.get(node.definitionId)} onChange={(update) => props.onNodeChange(node.id, update)} onRemove={() => props.onRemoveNode(node.id)} />)}</Space>}</Card>
    <Card title="Entry & Typed Edges" extra={<Button disabled={draft.nodes.length < 2} onClick={props.onAddEdge}>添加 Edge</Button>}>
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <div><Typography.Text>Entry Node</Typography.Text><Select style={{ width: '100%' }} value={draft.entryNodeId || undefined} options={draft.nodes.map((node) => ({ value: node.id, label: node.id }))} onChange={(entryNodeId) => props.onDraftChange({ ...draft, entryNodeId })} /></div>
        {draft.edges.map((edge, index) => <RuleEdgeEditor key={`${index}-${edge.fromNode}-${edge.toNode}`} edge={edge} nodes={draft.nodes} definitionById={definitionById} onChange={(patch) => props.onEdgeChange(index, patch)} onRemove={() => props.onDraftChange({ ...draft, edges: draft.edges.filter((_, edgeIndex) => edgeIndex !== index) })} />)}
      </Space>
    </Card>
    <Card title="Compile Budget & Required Permissions"><Row gutter={[12, 12]}>{(['maxNodes', 'maxDepth', 'maxFanout', 'maxResourceCost', 'maxAttempts'] as const).map((key) => <Col xs={12} md={8} xl={4} key={key}><Typography.Text>{key}</Typography.Text><InputNumber style={{ width: '100%' }} min={1} value={draft[key]} onChange={(value) => props.onDraftChange({ ...draft, [key]: value ?? 1 })} /></Col>)}<Col xs={24}><Space wrap>{draft.allowedPermissions.length ? draft.allowedPermissions.map((permission) => <Tag key={permission} color="blue">{permission}</Tag>) : <Tag>no owner permission</Tag>}</Space></Col></Row></Card>
    <Card title="Validate / Diff / Test / Simulate / Release">
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Space wrap><Button type="primary" loading={working === 'validate'} onClick={props.onValidate}>Validate</Button><Button disabled={!selectedRevision} onClick={props.onDiff}>Diff selected release</Button><Button loading={working === 'simulate'} onClick={props.onSimulate}>Test / Simulate</Button><Popconfirm title="发布后 RuleRevision 不可变；后续修改会创建新的 Revision。" disabled={!canRelease} onConfirm={props.onRelease}><Button type="primary" disabled={!canRelease} loading={working === 'release'}>Release immutable revision</Button></Popconfirm></Space>
        {validation ? <Alert type={validation.valid ? 'success' : 'error'} showIcon message={validation.valid ? `Server validation passed · ${validation.digest}` : `${validation.error?.code ?? 'INVALID'} · ${validation.error?.detail ?? ''}`} /> : null}
        {diffLines.length ? <Card size="small" title="Draft Diff"><pre style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{diffLines.join('\n')}</pre></Card> : null}
        <Row gutter={[12, 12]}>
          <Col xs={24} md={12}><Typography.Text>Event ID</Typography.Text><Input value={simulationInput.eventId} onChange={(event) => props.onSimulationInput({ ...simulationInput, eventId: event.target.value })} /></Col>
          <Col xs={24} md={12}><Typography.Text>Event Schema</Typography.Text><Input value={simulationInput.schema} onChange={(event) => props.onSimulationInput({ ...simulationInput, schema: event.target.value })} /></Col>
          <Col xs={24} md={8}><Typography.Text>Subject Type</Typography.Text><Input value={simulationInput.subjectType} onChange={(event) => props.onSimulationInput({ ...simulationInput, subjectType: event.target.value })} /></Col>
          <Col xs={24} md={8}><Typography.Text>Subject ID</Typography.Text><Input value={simulationInput.subjectId} onChange={(event) => props.onSimulationInput({ ...simulationInput, subjectId: event.target.value })} /></Col>
          <Col xs={24} md={8}><Typography.Text>Occurred At</Typography.Text><Input value={simulationInput.occurredAt} onChange={(event) => props.onSimulationInput({ ...simulationInput, occurredAt: event.target.value })} /></Col>
          <Col xs={24} md={12}><Typography.Text>Event Payload</Typography.Text><Input.TextArea rows={6} value={simulationInput.payload} onChange={(event) => props.onSimulationInput({ ...simulationInput, payload: event.target.value })} /></Col>
          <Col xs={24} md={12}><Typography.Text>Frozen Owner Facts</Typography.Text><Input value={simulationInput.frozenFactsRevision} placeholder="facts revision" onChange={(event) => props.onSimulationInput({ ...simulationInput, frozenFactsRevision: event.target.value })} /><Input.TextArea rows={5} value={simulationInput.frozenFacts} onChange={(event) => props.onSimulationInput({ ...simulationInput, frozenFacts: event.target.value })} /></Col>
        </Row>
        {simulationResult ? <Card size="small" title={`Replay result · ${simulationResult.status}`}><pre style={{ margin: 0, overflow: 'auto' }}>{JSON.stringify(simulationResult, null, 2)}</pre></Card> : null}
      </Space>
    </Card>
  </Space>;
}

function RuleNodeEditor({ node, definition, onChange, onRemove }: { node: RuleNode; definition?: RuleNodeDefinition; onChange(update: (node: RuleNode) => RuleNode): void; onRemove(): void }) {
  return <Card size="small" title={`${node.id} · ${node.definitionId}`} extra={<Button danger size="small" onClick={onRemove}>移除</Button>}>
    <Row gutter={[12, 12]}><Col xs={24} lg={8}><Descriptions size="small" column={1} bordered><Descriptions.Item label="Inputs">{Object.entries(definition?.inputs ?? {}).map(([key, type]) => `${key}:${type}`).join('、') || '—'}</Descriptions.Item><Descriptions.Item label="Outputs">{Object.entries(definition?.outputs ?? {}).map(([key, type]) => `${key}:${type}`).join('、') || '—'}</Descriptions.Item><Descriptions.Item label="Permission">{definition?.requiredPermission ?? 'none'}</Descriptions.Item></Descriptions></Col><Col xs={24} lg={16}><Space direction="vertical" size={8} style={{ width: '100%' }}>{(definition?.configFields ?? []).map((field) => <RuleConfigEditor key={field.name} field={field} value={node.config[field.name]} onValue={(value) => onChange((current) => ({ ...current, config: { ...current.config, [field.name]: value } }))} />)}{!definition?.configFields.length ? <Typography.Text type="secondary">此 NodeDefinition 没有可编辑配置。</Typography.Text> : null}</Space></Col></Row>
  </Card>;
}

function RuleConfigEditor({ field, value, onValue }: { field: RuleNodeDefinition['configFields'][number]; value: unknown; onValue(value: unknown): void }) {
  if (field.kind === 'ENUM') return <div><Typography.Text>{field.name}</Typography.Text><Select style={{ width: '100%' }} value={typeof value === 'string' ? value : undefined} options={(field.options ?? []).map((option) => ({ value: option, label: option }))} onChange={onValue} /></div>;
  if (field.kind === 'NUMBER' || field.kind === 'POSITIVE_INTEGER') return <div><Typography.Text>{field.name}</Typography.Text><InputNumber style={{ width: '100%' }} min={field.kind === 'POSITIVE_INTEGER' ? 1 : undefined} value={typeof value === 'number' ? value : undefined} onChange={(next) => onValue(next ?? (field.kind === 'POSITIVE_INTEGER' ? 1 : 0))} /></div>;
  return <div><Typography.Text>{field.name}</Typography.Text><Input value={field.kind === 'STRING_LIST' ? Array.isArray(value) ? value.join(', ') : '' : typeof value === 'string' ? value : ''} onChange={(event) => onValue(field.kind === 'STRING_LIST' ? event.target.value.split(',').map((item) => item.trim()).filter(Boolean) : event.target.value)} /></div>;
}

function RuleEdgeEditor({ edge, nodes, definitionById, onChange, onRemove }: { edge: RuleEdge; nodes: readonly RuleNode[]; definitionById: ReadonlyMap<string, RuleNodeDefinition>; onChange(patch: Partial<RuleEdge>): void; onRemove(): void }) {
  const fromDefinition = definitionById.get(nodes.find((node) => node.id === edge.fromNode)?.definitionId ?? '');
  const toDefinition = definitionById.get(nodes.find((node) => node.id === edge.toNode)?.definitionId ?? '');
  return <Space wrap align="end"><div><Typography.Text>From</Typography.Text><Select style={{ width: 190 }} value={edge.fromNode} options={nodes.map((node) => ({ value: node.id, label: node.id }))} onChange={(fromNode) => { const definition = definitionById.get(nodes.find((node) => node.id === fromNode)?.definitionId ?? ''); onChange({ fromNode, fromPort: Object.keys(definition?.outputs ?? {})[0] ?? '' }); }} /></div><div><Typography.Text>Port</Typography.Text><Select style={{ width: 150 }} value={edge.fromPort} options={Object.keys(fromDefinition?.outputs ?? {}).map((port) => ({ value: port, label: `${port}:${fromDefinition?.outputs[port]}` }))} onChange={(fromPort) => onChange({ fromPort })} /></div><Typography.Text>→</Typography.Text><div><Typography.Text>To</Typography.Text><Select style={{ width: 190 }} value={edge.toNode} options={nodes.map((node) => ({ value: node.id, label: node.id }))} onChange={(toNode) => { const definition = definitionById.get(nodes.find((node) => node.id === toNode)?.definitionId ?? ''); onChange({ toNode, toPort: Object.keys(definition?.inputs ?? {})[0] ?? '' }); }} /></div><div><Typography.Text>Port</Typography.Text><Select style={{ width: 150 }} value={edge.toPort} options={Object.keys(toDefinition?.inputs ?? {}).map((port) => ({ value: port, label: `${port}:${toDefinition?.inputs[port]}` }))} onChange={(toPort) => onChange({ toPort })} /></div><Button danger onClick={onRemove}>移除</Button></Space>;
}
