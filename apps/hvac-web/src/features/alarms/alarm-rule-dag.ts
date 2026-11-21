import type {
  AlarmRule,
  AlarmRuleCondition,
  AlarmRuleDraft,
  AlarmRuleGraphEdge,
  AlarmRuleValidationIssue,
} from '../../api/generated/platformGateway.gen.ts';

type AlarmRuleSeverity = AlarmRule['severity'];

export const MAX_RULE_LINKAGE_HOPS = 3;

export const EXECUTION_TARGET_LABELS: Readonly<Record<NonNullable<AlarmRule['executionTarget']>, string>> = {
  CLOUD: '云端',
  EDGE: '边缘',
  CLOUD_AND_EDGE: '云边协同',
};

export const WINDOW_AGG_LABELS: Readonly<Record<NonNullable<AlarmRuleCondition['agg']>, string>> = {
  AVG: '平均值',
  MIN: '最小值',
  MAX: '最大值',
  SUM: '求和',
  COUNT: '样本计数',
  STDDEV: '标准差',
  P95: '95 分位',
  P99: '99 分位',
};

export const WINDOW_TYPE_LABELS: Readonly<Record<NonNullable<AlarmRuleCondition['windowType']>, string>> = {
  SLIDING: '滑动窗口',
  TUMBLING: '滚动窗口',
};

export const EDGE_ELIGIBILITY_REASON_LABELS: Readonly<Record<string, string>> = {
  CLOUD_ONLY_TARGET: '执行位置设为仅云端，不下发至边缘网关。',
  EDGE_SCHEDULE_NOT_SUPPORTED: '绑定站点运行日程的规则仅在云端评估，不支持编译到边缘网关。',
  EDGE_UNSUPPORTED_CONDITION_KIND: '包含缺数、过期、停滞、变化检测或诊断原因等条件，当前仅支持在云端评估。',
  EDGE_CLOUD_ONLY_ACTION: '仅边缘执行模式下不能配置工单或外部 Webhook 动作，请改为云边协同模式或移除相关动作。',
  EDGE_UPSTREAM_NOT_ON_EDGE: '启用了上游告警联动门控，但部分上游规则仅在云端执行，无法编入边缘规则包。',
  EDGE_BUNDLE_LIMIT_EXCEEDED: '单个边缘规则包最多容纳 128 条规则，超出部分仅在云端执行。',
};

export interface DagNodePosition {
  readonly rule: AlarmRule;
  readonly hopDepth: number;
  readonly column: number;
  readonly row: number;
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

export interface DagEdgePath {
  readonly fromRuleId: string;
  readonly toRuleId: string;
  readonly pathD: string;
  readonly midX: number;
  readonly midY: number;
}

export interface DagGraphLayout {
  readonly width: number;
  readonly height: number;
  readonly columns: ReadonlyArray<ReadonlyArray<DagNodePosition>>;
  readonly nodeById: ReadonlyMap<string, DagNodePosition>;
  readonly edges: ReadonlyArray<DagEdgePath>;
  readonly maxHopDepth: number;
  readonly cycleRuleIds: ReadonlyArray<string>;
  readonly overDepthRuleIds: ReadonlyArray<string>;
  readonly unknownDeps: ReadonlyArray<AlarmRuleGraphEdge>;
}

export interface RuleDraftDelta {
  readonly dirty: boolean;
  readonly addedIds: ReadonlySet<string>;
  readonly changedIds: ReadonlySet<string>;
  readonly retiredIds: ReadonlySet<string>;
  readonly addedCount: number;
  readonly changedCount: number;
  readonly retiredCount: number;
}

const NODE_WIDTH = 272;
const NODE_HEIGHT = 116;
const COLUMN_GAP = 72;
const ROW_GAP = 24;
const PADDING_X = 24;
const PADDING_Y = 24;

const SEVERITY_SORT: Readonly<Record<AlarmRuleSeverity, number>> = {
  CRITICAL: 0,
  MAJOR: 1,
  MINOR: 2,
  WARNING: 3,
  INFO: 4,
};

/** Generates a standards-compliant UUIDv7 identifier for unsaved draft alarm rules. */
export function newRuleUuidV7(nowMs = Date.now()): string {
  const bytes = new Uint8Array(16);
  if (typeof globalThis.crypto?.getRandomValues === 'function') {
    globalThis.crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i += 1) {
      bytes[i] = Math.floor(Math.random() * 256);
    }
  }
  const ts = BigInt(Math.max(0, Math.floor(nowMs)));
  bytes[0] = Number((ts >> 40n) & 0xffn);
  bytes[1] = Number((ts >> 32n) & 0xffn);
  bytes[2] = Number((ts >> 24n) & 0xffn);
  bytes[3] = Number((ts >> 16n) & 0xffn);
  bytes[4] = Number((ts >> 8n) & 0xffn);
  bytes[5] = Number(ts & 0xffn);
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/** Converts an AlarmRule projection into an AlarmRuleDraft payload accepted by diff/publish/simulate. */
export function ruleToDraft(rule: AlarmRule): AlarmRuleDraft {
  return {
    ruleId: rule.ruleId,
    title: rule.title,
    ...(rule.alarmType ? { alarmType: rule.alarmType } : {}),
    ...(rule.summary ? { summary: rule.summary } : {}),
    severity: rule.severity,
    ...(rule.ruleKind ? { ruleKind: rule.ruleKind } : {}),
    ...(rule.executionTarget ? { executionTarget: rule.executionTarget } : {}),
    ...(rule.templateKey ? { templateKey: rule.templateKey } : {}),
    ...(rule.overriddenFields && rule.overriddenFields.length > 0 ? { overriddenFields: [...rule.overriddenFields] } : {}),
    ...(rule.dependsOnRuleIds && rule.dependsOnRuleIds.length > 0 ? { dependsOnRuleIds: [...rule.dependsOnRuleIds] } : {}),
    ...(rule.dependsOnRuleIds && rule.dependsOnRuleIds.length > 0 && rule.dependencyGate ? { dependencyGate: rule.dependencyGate } : {}),
    ...(rule.actions && rule.actions.length > 0 ? { actions: rule.actions.map((a) => ({ ...a })) } : {}),
    ...(rule.qualityPolicy ? { qualityPolicy: rule.qualityPolicy } : {}),
    ...(rule.freshnessSeconds ? { freshnessSeconds: rule.freshnessSeconds } : {}),
    triggerMode: rule.triggerMode,
    ...(rule.triggerMode === 'DURATION' && rule.durationSeconds !== undefined ? { durationSeconds: rule.durationSeconds } : {}),
    ...(rule.triggerMode === 'REPEATING' && rule.repeatCount !== undefined ? { repeatCount: rule.repeatCount } : {}),
    raise: structuredClone(rule.raise),
    clear: structuredClone(rule.clear),
  };
}

function stableNormalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableNormalize);
  if (value && typeof value === 'object') {
    const entries = Object.entries(value)
      .filter(([, v]) => v !== undefined)
      .sort(([a], [b]) => a.localeCompare(b));
    return Object.fromEntries(entries.map(([k, v]) => [k, stableNormalize(v)]));
  }
  return value;
}

export function ruleDraftSignature(rule: AlarmRule): string {
  const draft = ruleToDraft(rule);
  if (draft.dependsOnRuleIds) {
    draft.dependsOnRuleIds = [...draft.dependsOnRuleIds].sort();
  }
  if (draft.ruleKind === 'THRESHOLD') {
    delete draft.ruleKind;
  }
  if (draft.executionTarget === 'CLOUD') {
    delete draft.executionTarget;
  }
  return JSON.stringify(stableNormalize(draft));
}

/** Summarizes unsaved differences between published site rules and working workbench draft rules. */
export function computeDraftDelta(
  published: readonly AlarmRule[],
  draft: readonly AlarmRule[],
): RuleDraftDelta {
  const pubById = new Map(published.map((r) => [r.ruleId, ruleDraftSignature(r)]));
  const draftById = new Map(draft.map((r) => [r.ruleId, ruleDraftSignature(r)]));
  const addedIds = new Set<string>();
  const changedIds = new Set<string>();
  const retiredIds = new Set<string>();

  for (const [id, sig] of draftById) {
    const prevSig = pubById.get(id);
    if (!prevSig) {
      addedIds.add(id);
    } else if (prevSig !== sig) {
      changedIds.add(id);
    }
  }
  for (const id of pubById.keys()) {
    if (!draftById.has(id)) {
      retiredIds.add(id);
    }
  }
  return {
    dirty: addedIds.size > 0 || changedIds.size > 0 || retiredIds.size > 0,
    addedIds,
    changedIds,
    retiredIds,
    addedCount: addedIds.size,
    changedCount: changedIds.size,
    retiredCount: retiredIds.size,
  };
}

/**
 * Computes layered DAG coordinates (grouped by hop depth 0..3) plus client-side cycle,
 * max-hop (> 3), and dangling dependency diagnostics for immediate feedback before diffing.
 */
export function computeRuleDagLayout(rules: readonly AlarmRule[]): DagGraphLayout {
  const byId = new Map(rules.map((r) => [r.ruleId, r]));
  const inDegree = new Map<string, number>();
  const downstream = new Map<string, string[]>();
  const unknownDeps: AlarmRuleGraphEdge[] = [];

  for (const rule of rules) {
    inDegree.set(rule.ruleId, 0);
    downstream.set(rule.ruleId, []);
  }

  for (const rule of rules) {
    const seen = new Set<string>();
    for (const depId of rule.dependsOnRuleIds ?? []) {
      if (seen.has(depId)) continue;
      seen.add(depId);
      if (depId === rule.ruleId) {
        unknownDeps.push({ fromRuleId: depId, toRuleId: rule.ruleId });
        continue;
      }
      if (!byId.has(depId)) {
        unknownDeps.push({ fromRuleId: depId, toRuleId: rule.ruleId });
        continue;
      }
      inDegree.set(rule.ruleId, (inDegree.get(rule.ruleId) ?? 0) + 1);
      downstream.get(depId)?.push(rule.ruleId);
    }
  }

  const queue: string[] = [];
  for (const [id, deg] of inDegree) {
    if (deg === 0) queue.push(id);
  }
  queue.sort((a, b) => compareRuleOrder(byId.get(a)!, byId.get(b)!));

  const topoOrder: string[] = [];
  const depthMap = new Map<string, number>();
  for (const id of queue) depthMap.set(id, 0);

  while (queue.length > 0) {
    const current = queue.shift()!;
    topoOrder.push(current);
    const nextDepth = (depthMap.get(current) ?? 0) + 1;
    for (const nextId of downstream.get(current) ?? []) {
      depthMap.set(nextId, Math.max(depthMap.get(nextId) ?? 0, nextDepth));
      const rem = (inDegree.get(nextId) ?? 0) - 1;
      inDegree.set(nextId, rem);
      if (rem === 0) {
        queue.push(nextId);
        queue.sort((a, b) => compareRuleOrder(byId.get(a)!, byId.get(b)!));
      }
    }
  }

  const cycleRuleIds = rules.filter((r) => !topoOrder.includes(r.ruleId)).map((r) => r.ruleId);
  for (const id of cycleRuleIds) {
    depthMap.set(id, Math.max(1, Math.min(MAX_RULE_LINKAGE_HOPS, byId.get(id)?.hopDepth ?? 1)));
  }

  let maxHopDepth = 0;
  const overDepthRuleIds: string[] = [];
  for (const [id, depth] of depthMap) {
    if (depth > maxHopDepth) maxHopDepth = depth;
    if (depth > MAX_RULE_LINKAGE_HOPS) overDepthRuleIds.push(id);
  }

  const colCount = Math.max(1, Math.min(MAX_RULE_LINKAGE_HOPS + 1, maxHopDepth + 1));
  const grouped: AlarmRule[][] = Array.from({ length: colCount }, () => []);
  for (const rule of rules) {
    const rawDepth = depthMap.get(rule.ruleId) ?? 0;
    const col = Math.max(0, Math.min(colCount - 1, rawDepth));
    grouped[col].push(rule);
  }
  for (const bucket of grouped) {
    bucket.sort(compareRuleOrder);
  }

  const nodeById = new Map<string, DagNodePosition>();
  const columns: DagNodePosition[][] = [];
  let maxRows = 1;
  grouped.forEach((bucket, column) => {
    if (bucket.length > maxRows) maxRows = bucket.length;
    const colNodes: DagNodePosition[] = bucket.map((rule, row) => {
      const pos: DagNodePosition = {
        rule,
        hopDepth: depthMap.get(rule.ruleId) ?? 0,
        column,
        row,
        x: PADDING_X + column * (NODE_WIDTH + COLUMN_GAP),
        y: PADDING_Y + row * (NODE_HEIGHT + ROW_GAP),
        width: NODE_WIDTH,
        height: NODE_HEIGHT,
      };
      nodeById.set(rule.ruleId, pos);
      return pos;
    });
    columns.push(colNodes);
  });

  const edges: DagEdgePath[] = [];
  for (const rule of rules) {
    const target = nodeById.get(rule.ruleId);
    if (!target) continue;
    for (const depId of rule.dependsOnRuleIds ?? []) {
      const source = nodeById.get(depId);
      if (!source) continue;
      const startX = source.x + source.width;
      const startY = source.y + source.height / 2;
      const endX = target.x;
      const endY = target.y + target.height / 2;
      const dx = Math.max(36, Math.abs(endX - startX) * 0.5);
      const pathD = `M ${startX} ${startY} C ${startX + dx} ${startY}, ${endX - dx} ${endY}, ${endX} ${endY}`;
      edges.push({
        fromRuleId: depId,
        toRuleId: rule.ruleId,
        pathD,
        midX: (startX + endX) / 2,
        midY: (startY + endY) / 2,
      });
    }
  }

  const width = Math.max(640, PADDING_X * 2 + colCount * NODE_WIDTH + Math.max(0, colCount - 1) * COLUMN_GAP);
  const height = Math.max(220, PADDING_Y * 2 + maxRows * NODE_HEIGHT + Math.max(0, maxRows - 1) * ROW_GAP);

  return {
    width,
    height,
    columns,
    nodeById,
    edges,
    maxHopDepth,
    cycleRuleIds,
    overDepthRuleIds,
    unknownDeps,
  };
}

function compareRuleOrder(left: AlarmRule, right: AlarmRule): number {
  const sevDiff = SEVERITY_SORT[left.severity] - SEVERITY_SORT[right.severity];
  if (sevDiff !== 0) return sevDiff;
  const titleDiff = left.title.localeCompare(right.title, 'zh-CN');
  if (titleDiff !== 0) return titleDiff;
  return left.ruleId.localeCompare(right.ruleId);
}

const VALIDATION_ISSUE_LABELS: Readonly<Record<string, string>> = {
  RULE_CYCLE_DETECTED: '规则联动关系存在循环依赖，请移除冲突的前置规则。',
  UNKNOWN_DEPENDENCY: '引用了不存在或已停用的前置规则，请重新检查联动依赖。',
  MAX_LINKAGE_HOPS_EXCEEDED: '规则联动链深度超过最大 3 跳限制，请减少级联层级。',
  DEVICE_LOOP_DETECTED: '联动保护动作的目标设备不能与同一因果链上的监测设备构成控制回环。',
  ALARM_TYPE_IMMUTABLE: '已发布或历史停用规则的告警类型标识不可修改。',
  INVALID_ALARM_TYPE: '规则告警类型标识不合法，请调整后重试。',
  INVALID_FRESHNESS_SECONDS: '读数有效时长阈值必须大于 0 秒。',
  INVALID_TRIGGER_MODE: '触发方式及其持续时长或连续命中次数配置不合法，请检查后重试。',
  INVALID_DEPENDENCIES: '上游因果依赖列表中存在自引用、重复规则或非法标识。',
  INVALID_DEPENDENCY_GATE: '仅在配置上游依赖规则时才可选择“需全部”或“任一”联动门控。',
  INVALID_ACTIONS: '自动执行动作参数不完整或超出允许上限。',
  INVALID_RAISE_CONDITION: '触发报警条件参数不完整或超出允许范围，请核对监测点与阈值。',
  INVALID_CLEAR_CONDITION: '自动恢复条件参数不完整或超出允许范围，请核对监测点与阈值。',
  INVALID_TITLE: '规则名称不能为空且需控制在长度限制以内。',
  INVALID_SEVERITY: '规则严重程度选项无效。',
  INVALID_QUALITY_POLICY: '数据质量门控选项无效。',
  INVALID_CHANGE_NOTE: '变更说明超出长度限制或包含不支持的字符。',
  INVALID_RULE_ID: '规则标识格式无效，请重新创建规则。',
  DUPLICATE_RULE_ID: '草稿中存在重复的规则标识，请去除重复项后重试。',
  INVALID_RULE_BUNDLE: '规则包大小或规则总数超出站点限制。',
  RULE_GRAPH_INVALID: '规则因果依赖图校验未通过，请检查联动关系。',
  EDGE_RULE_INELIGIBLE: '所选边缘或云边协同规则不满足边缘执行编译约束，请检查条件、联动动作或上游依赖。',
};

function findUnsupportedEdgeConditionKind(cond: AlarmRuleCondition): string | null {
  switch (cond.kind) {
    case 'AND':
    case 'OR':
      for (const child of cond.children ?? []) {
        const bad = findUnsupportedEdgeConditionKind(child);
        if (bad) return bad;
      }
      return null;
    case 'COMPARE':
    case 'RANGE':
    case 'HYSTERESIS':
    case 'SLOPE':
    case 'WINDOW_AGG':
    case 'EXPRESSION':
      return null;
    default:
      return cond.kind;
  }
}

export interface LocalEdgeEligibilityResult {
  readonly eligible: boolean;
  readonly reasonCode?: string;
  readonly message?: string;
}

/**
 * Mirrors backend CheckEdgeRuleEligibility in modules/alarm/pkg/alarmservice/edge_compile.go
 * so operators receive instant feedback while authoring EDGE or CLOUD_AND_EDGE rules.
 */
export function evaluateLocalEdgeEligibility(
  rule: AlarmRule | AlarmRuleDraft,
  allRules: readonly (AlarmRule | AlarmRuleDraft)[],
  visited = new Set<string>(),
): LocalEdgeEligibilityResult {
  const target = rule.executionTarget ?? 'CLOUD';
  if (target !== 'EDGE' && target !== 'CLOUD_AND_EDGE') {
    return {
      eligible: false,
      reasonCode: 'CLOUD_ONLY_TARGET',
      message: EDGE_ELIGIBILITY_REASON_LABELS.CLOUD_ONLY_TARGET,
    };
  }
  const badRaise = findUnsupportedEdgeConditionKind(rule.raise);
  if (badRaise) {
    return {
      eligible: false,
      reasonCode: 'EDGE_UNSUPPORTED_CONDITION_KIND',
      message: EDGE_ELIGIBILITY_REASON_LABELS.EDGE_UNSUPPORTED_CONDITION_KIND,
    };
  }
  const badClear = findUnsupportedEdgeConditionKind(rule.clear);
  if (badClear) {
    return {
      eligible: false,
      reasonCode: 'EDGE_UNSUPPORTED_CONDITION_KIND',
      message: EDGE_ELIGIBILITY_REASON_LABELS.EDGE_UNSUPPORTED_CONDITION_KIND,
    };
  }
  if (target === 'EDGE') {
    for (const action of rule.actions ?? []) {
      if (action.kind !== 'PROTECTION_COMMAND') {
        return {
          eligible: false,
          reasonCode: 'EDGE_CLOUD_ONLY_ACTION',
          message: EDGE_ELIGIBILITY_REASON_LABELS.EDGE_CLOUD_ONLY_ACTION,
        };
      }
    }
  }
  if (rule.dependencyGate === 'ALL_ACTIVE' || rule.dependencyGate === 'ANY_ACTIVE') {
    const byId = new Map(allRules.filter((r) => Boolean(r.ruleId)).map((r) => [r.ruleId!, r]));
    if (rule.ruleId) visited.add(rule.ruleId);
    for (const depId of rule.dependsOnRuleIds ?? []) {
      if (visited.has(depId)) continue;
      const upstream = byId.get(depId);
      if (!upstream) {
        return {
          eligible: false,
          reasonCode: 'EDGE_UPSTREAM_NOT_ON_EDGE',
          message: EDGE_ELIGIBILITY_REASON_LABELS.EDGE_UPSTREAM_NOT_ON_EDGE,
        };
      }
      const nextVisited = new Set(visited);
      nextVisited.add(depId);
      if (!evaluateLocalEdgeEligibility(upstream, allRules, nextVisited).eligible) {
        return {
          eligible: false,
          reasonCode: 'EDGE_UPSTREAM_NOT_ON_EDGE',
          message: EDGE_ELIGIBILITY_REASON_LABELS.EDGE_UPSTREAM_NOT_ON_EDGE,
        };
      }
    }
  }
  return { eligible: true };
}

/** Formats backend rule validation issues into plain operator Chinese without leaking UUIDs or internal codes. */
export function formatRuleValidationIssue(
  issue: AlarmRuleValidationIssue,
  titleByRuleId?: ReadonlyMap<string, string>,
): string {
  const base = VALIDATION_ISSUE_LABELS[issue.code]
    ?? (/[\u4e00-\u9fff]/.test(issue.message) ? issue.message : '规则参数或依赖结构不符合发布要求，请检查后重试。');
  if (issue.ruleId) {
    const title = titleByRuleId?.get(issue.ruleId);
    if (title) return `「${title}」：${base}`;
  }
  return base;
}
