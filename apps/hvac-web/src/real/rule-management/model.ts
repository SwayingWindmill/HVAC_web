import type {
  RuleAssignmentRequest,
  RuleBinding,
  RuleCatalog,
  RuleDraft,
  RuleNode,
  RuleNodeDefinition,
  RulePermission,
  RuleRevision,
  RuleValidationResult,
} from '@/api/generated/platformGateway.gen';

export const DEFAULT_RULE_BUDGETS = Object.freeze({
  maxNodes: 32,
  maxDepth: 16,
  maxFanout: 8,
  maxResourceCost: 128,
  maxAttempts: 3,
});

export function createEmptyRuleDraft(): RuleDraft {
  return {
    catalogVersion: 'core.v1',
    entryNodeId: '',
    nodes: [],
    edges: [],
    allowedPermissions: [],
    ...DEFAULT_RULE_BUDGETS,
  };
}

export function deriveRulePermissions(catalog: RuleCatalog | undefined, nodes: readonly RuleNode[]): RulePermission[] {
  if (!catalog) return [];
  const byId = new Map(catalog.definitions.map((definition) => [definition.id, definition]));
  const values = new Set<RulePermission>();
  for (const node of nodes) {
    const permission = byId.get(node.definitionId)?.requiredPermission;
    if (permission === 'owner.snapshot.read' || permission === 'alarm.intent.publish') values.add(permission);
  }
  return [...values].sort();
}

export function makeRuleNode(definition: RuleNodeDefinition, existing: readonly RuleNode[]): RuleNode {
  const used = new Set(existing.map((node) => node.id));
  let suffix = existing.length + 1;
  let id = `${definition.id}-${suffix}`;
  while (used.has(id)) {
    suffix += 1;
    id = `${definition.id}-${suffix}`;
  }
  const config: Record<string, unknown> = {};
  for (const field of definition.configFields) {
    switch (field.kind) {
      case 'STRING_LIST': config[field.name] = []; break;
      case 'NUMBER': config[field.name] = 0; break;
      case 'POSITIVE_INTEGER': config[field.name] = 1; break;
      case 'ENUM': config[field.name] = field.options?.[0] ?? ''; break;
      default: config[field.name] = '';
    }
  }
  return { id, definitionId: definition.id, config };
}

export function ruleDraftFingerprint(draft: RuleDraft): string {
  return JSON.stringify(draft);
}

export function canReleaseRuleDraft(validation: RuleValidationResult | null, validatedFingerprint: string | null, draft: RuleDraft): boolean {
  return Boolean(validation?.valid && validation.digest && validatedFingerprint === ruleDraftFingerprint(draft));
}

export function buildRollbackAssignment(binding: RuleBinding, revision: RuleRevision): RuleAssignmentRequest {
  if (binding.siteId === '' || binding.id === '' || revision.id === '') throw new Error('Rollback requires authoritative binding and released revision identities.');
  return {
    bindingId: binding.id,
    siteId: binding.siteId,
    ruleRevisionId: revision.id,
    priority: binding.priority,
  };
}

export function diffRuleDraft(draft: RuleDraft, revision: RuleRevision | null): string[] {
  if (!revision) return ['尚未选择用于比较的已发布 Revision。'];
  const lines: string[] = [];
  const draftNodes = new Map(draft.nodes.map((node) => [node.id, node]));
  const revisionNodes = new Map(revision.nodes.map((node) => [node.id, node]));
  for (const id of draftNodes.keys()) if (!revisionNodes.has(id)) lines.push(`+ Node ${id}`);
  for (const id of revisionNodes.keys()) if (!draftNodes.has(id)) lines.push(`- Node ${id}`);
  for (const [id, node] of draftNodes) {
    const previous = revisionNodes.get(id);
    if (previous && JSON.stringify(node) !== JSON.stringify(previous)) lines.push(`~ Node ${id}`);
  }
  const edgeKey = (edge: RuleDraft['edges'][number]) => `${edge.fromNode}:${edge.fromPort}->${edge.toNode}:${edge.toPort}`;
  const currentEdges = new Set(draft.edges.map(edgeKey));
  const previousEdges = new Set(revision.edges.map(edgeKey));
  for (const edge of currentEdges) if (!previousEdges.has(edge)) lines.push(`+ Edge ${edge}`);
  for (const edge of previousEdges) if (!currentEdges.has(edge)) lines.push(`- Edge ${edge}`);
  if (draft.entryNodeId !== revision.entryNodeId) lines.push(`~ Entry ${revision.entryNodeId} -> ${draft.entryNodeId}`);
  for (const key of ['maxNodes', 'maxDepth', 'maxFanout', 'maxResourceCost', 'maxAttempts'] as const) {
    if (draft[key] !== revision[key]) lines.push(`~ ${key}: ${revision[key]} -> ${draft[key]}`);
  }
  return lines.length ? lines : ['草稿与所选 Revision 没有结构差异。'];
}
