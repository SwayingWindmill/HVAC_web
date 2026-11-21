import { useId, useMemo } from 'react';
import { AlertTriangle, GitBranch, Pencil, Play, Trash2 } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { describeDependencyGate, summarizeRuleActions } from '../alarm-operations';
import { SeverityBadge } from '../alarm-presentation';
import {
  EXECUTION_TARGET_LABELS,
  computeRuleDagLayout,
  type RuleDraftDelta,
} from '../alarm-rule-dag';
import {
  describeCondition,
  describeDevices,
  describeRaise,
  type AlarmRule,
  type ReadingNames,
} from '../alarm-rules';

const HOP_TITLES: Readonly<Record<number, string>> = {
  0: '第 0 跳 · 基础监测',
  1: '第 1 跳 · 一次联动',
  2: '第 2 跳 · 二次联动',
  3: '第 3 跳 · 三次联动',
};

const KIND_BADGE_LABELS: Readonly<Record<NonNullable<AlarmRule['ruleKind']>, string>> = {
  THRESHOLD: '阈值',
  SLOPE: '斜率',
  WINDOW: '窗口',
  EXPRESSION: '表达式',
};

export function AlarmRuleDagCanvas({
  rules,
  selectedRuleId,
  onSelectRule,
  onSimulateRule,
  onEditRule,
  onRetireRule,
  delta,
  canManage,
  names,
}: {
  readonly rules: readonly AlarmRule[];
  readonly selectedRuleId: string | null;
  readonly onSelectRule: (ruleId: string) => void;
  readonly onSimulateRule: (rule: AlarmRule) => void;
  readonly onEditRule?: (rule: AlarmRule) => void;
  readonly onRetireRule?: (ruleId: string) => void;
  readonly delta?: RuleDraftDelta;
  readonly canManage: boolean;
  readonly names: ReadingNames;
}) {
  const arrowId = useId().replace(/:/g, '');
  const arrowActiveId = `${arrowId}-active`;
  const layout = useMemo(() => computeRuleDagLayout(rules), [rules]);
  const ruleById = useMemo(() => new Map(rules.map((r) => [r.ruleId, r])), [rules]);
  const titleByRuleId = useMemo(() => new Map(rules.map((r) => [r.ruleId, r.title])), [rules]);

  const effectiveSelectedId = selectedRuleId && ruleById.has(selectedRuleId)
    ? selectedRuleId
    : rules[0]?.ruleId ?? null;
  const selectedRule = effectiveSelectedId ? ruleById.get(effectiveSelectedId) ?? null : null;

  const connectedNeighbors = useMemo(() => {
    const upstream = new Set<string>();
    const downstream = new Set<string>();
    if (!effectiveSelectedId) return { upstream, downstream };
    for (const edge of layout.edges) {
      if (edge.toRuleId === effectiveSelectedId) upstream.add(edge.fromRuleId);
      if (edge.fromRuleId === effectiveSelectedId) downstream.add(edge.toRuleId);
    }
    return { upstream, downstream };
  }, [effectiveSelectedId, layout.edges]);

  if (rules.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-divider bg-muted/20 px-6 py-12 text-center">
        <GitBranch className="size-6 text-muted-foreground" aria-hidden="true" />
        <p className="text-sm font-medium text-foreground">当前没有启用中的告警规则</p>
        <p className="text-xs text-muted-foreground">可通过顶部「新建规则」按钮创建首条规则，或从版本历史恢复。</p>
      </div>
    );
  }

  const hasGraphErrors = layout.cycleRuleIds.length > 0
    || layout.overDepthRuleIds.length > 0
    || layout.unknownDeps.length > 0;

  return (
    <div className="flex flex-col gap-3">
      {hasGraphErrors ? (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3.5 py-2.5 text-xs text-destructive-text"
        >
          <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <div className="flex flex-col gap-0.5">
            {layout.cycleRuleIds.length > 0 ? (
              <span>检测到循环依赖（涉及 {layout.cycleRuleIds.map((id) => `「${titleByRuleId.get(id) ?? id}」`).join('、')}），请解除闭环后再发布。</span>
            ) : null}
            {layout.overDepthRuleIds.length > 0 ? (
              <span>部分规则联动跳数超过上限（最多 3 跳），请调整前置依赖层级。</span>
            ) : null}
            {layout.unknownDeps.length > 0 ? (
              <span>存在引用缺失或自依赖的前置规则，请重新选择有效的上游规则。</span>
            ) : null}
          </div>
        </div>
      ) : null}

      <div
        role="region"
        aria-label="规则联动拓扑画布"
        tabIndex={0}
        className="overflow-x-auto rounded-lg border border-divider bg-muted/15"
      >
        <div
          className="flex items-center gap-4 border-b border-divider bg-card/80 px-6 py-2 text-xs text-muted-foreground"
          style={{ minWidth: `${layout.width}px` }}
        >
          {layout.columns.map((_, colIdx) => (
            <div key={colIdx} className="font-medium text-foreground" style={{ width: '272px', marginRight: colIdx < layout.columns.length - 1 ? '56px' : undefined }}>
              {HOP_TITLES[colIdx] ?? `第 ${colIdx} 跳`}
            </div>
          ))}
        </div>

        <div className="relative" style={{ width: `${layout.width}px`, height: `${layout.height}px` }}>
          <svg
            aria-hidden="true"
            width={layout.width}
            height={layout.height}
            className="pointer-events-none absolute inset-0"
          >
            <defs>
              <marker id={arrowId} markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto">
                <path d="M 0 0 L 8 3 L 0 6 z" className="fill-muted-foreground/60" />
              </marker>
              <marker id={arrowActiveId} markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto">
                <path d="M 0 0 L 8 3 L 0 6 z" className="fill-primary" />
              </marker>
            </defs>
            {layout.edges.map((edge) => {
              const highlighted = effectiveSelectedId !== null
                && (edge.fromRuleId === effectiveSelectedId || edge.toRuleId === effectiveSelectedId);
              return (
                <path
                  key={`${edge.fromRuleId}->${edge.toRuleId}`}
                  d={edge.pathD}
                  fill="none"
                  className={highlighted ? 'stroke-primary transition-colors' : 'stroke-muted-foreground/45 transition-colors'}
                  strokeWidth={highlighted ? 2 : 1.5}
                  markerEnd={`url(#${highlighted ? arrowActiveId : arrowId})`}
                />
              );
            })}
          </svg>

          {layout.columns.flatMap((colNodes) => colNodes).map((node) => {
            const { rule } = node;
            const isSelected = rule.ruleId === effectiveSelectedId;
            const isNeighbor = connectedNeighbors.upstream.has(rule.ruleId)
              || connectedNeighbors.downstream.has(rule.ruleId);
            const isAdded = delta?.addedIds.has(rule.ruleId) ?? false;
            const isChanged = delta?.changedIds.has(rule.ruleId) ?? false;
            const kindLabel = rule.ruleKind ? KIND_BADGE_LABELS[rule.ruleKind] : '阈值';
            const actionCount = rule.actions?.length ?? 0;
            const hasCycle = layout.cycleRuleIds.includes(rule.ruleId);

            return (
              <button
                key={rule.ruleId}
                type="button"
                aria-pressed={isSelected}
                onClick={() => onSelectRule(rule.ruleId)}
                style={{ left: `${node.x}px`, top: `${node.y}px`, width: `${node.width}px`, height: `${node.height}px` }}
                className={cn(
                  'absolute flex flex-col justify-between rounded-lg border bg-card p-3 text-left shadow-xs transition-all outline-none',
                  'hover:border-primary/60 focus-visible:ring-2 focus-visible:ring-ring',
                  isSelected && 'border-primary ring-2 ring-primary/25',
                  !isSelected && isNeighbor && 'border-primary/45 bg-primary/5',
                  hasCycle && 'border-destructive',
                )}
              >
                <div className="flex items-start justify-between gap-2">
                  <span className="line-clamp-2 text-xs font-medium leading-4 text-foreground">
                    {rule.title}
                  </span>
                  <SeverityBadge severity={rule.severity} />
                </div>

                <div className="truncate text-[11px] text-muted-foreground">
                  {describeDevices(rule, names) || '站点监测点'}
                </div>

                <div className="flex items-center justify-between gap-1.5">
                  <div className="flex flex-wrap items-center gap-1">
                    <Badge variant="outline" className="h-5 px-1.5 text-[11px] font-normal">
                      {kindLabel}
                    </Badge>
                    {rule.executionTarget && rule.executionTarget !== 'CLOUD' ? (
                      <Badge variant="outline" className="h-5 border-primary/35 bg-primary-soft px-1.5 text-[11px] font-normal text-primary">
                        {EXECUTION_TARGET_LABELS[rule.executionTarget]}
                      </Badge>
                    ) : null}
                    {actionCount > 0 ? (
                      <Badge variant="outline" className="h-5 border-primary/35 bg-primary-soft px-1.5 text-[11px] font-normal text-primary">
                        {actionCount} 动作
                      </Badge>
                    ) : null}
                  </div>
                  {isAdded ? (
                    <span className="rounded bg-primary-soft px-1.5 py-0.5 text-[11px] font-medium text-primary">新增</span>
                  ) : isChanged ? (
                    <span className="rounded bg-warning-soft px-1.5 py-0.5 text-[11px] font-medium text-warning-text">已修改</span>
                  ) : (
                    <span className="font-mono text-[11px] text-muted-foreground">跳数 {node.hopDepth}</span>
                  )}
                </div>
              </button>
            );
          })}
        </div>
      </div>

      {selectedRule ? (
        <div className="flex flex-wrap items-start justify-between gap-4 rounded-lg border border-divider bg-card px-4 py-3 text-xs">
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <div className="flex flex-wrap items-center gap-2">
              <SeverityBadge severity={selectedRule.severity} />
              <span className="text-sm font-medium text-foreground">{selectedRule.title}</span>
              {selectedRule.executionTarget && selectedRule.executionTarget !== 'CLOUD' ? (
                <Badge variant="outline" className="h-5 border-primary/35 bg-primary-soft px-1.5 text-[11px] font-normal text-primary">
                  {EXECUTION_TARGET_LABELS[selectedRule.executionTarget]}
                </Badge>
              ) : null}
              {(selectedRule.dependsOnRuleIds?.length ?? 0) > 0 ? (
                <Badge variant="outline" className="h-5 px-1.5 text-[11px] font-normal">
                  {describeDependencyGate(selectedRule.dependencyGate)}
                </Badge>
              ) : null}
            </div>
            <div className="text-muted-foreground">
              <span className="font-medium text-foreground">触发：</span>
              {describeRaise(selectedRule, names)}
            </div>
            <div className="text-muted-foreground">
              <span className="font-medium text-foreground">恢复：</span>
              {describeCondition(selectedRule.clear, names)}
            </div>
            {summarizeRuleActions(selectedRule.actions) ? (
              <div className="text-primary">{summarizeRuleActions(selectedRule.actions)}</div>
            ) : null}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => onSimulateRule(selectedRule)}>
              <Play data-icon="inline-start" className="size-3.5" aria-hidden="true" />
              模拟试算
            </Button>
            {canManage && onEditRule ? (
              <Button type="button" variant="outline" size="sm" onClick={() => onEditRule(selectedRule)}>
                <Pencil data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                编辑规则
              </Button>
            ) : null}
            {canManage && onRetireRule ? (
              <Button type="button" variant="destructive" size="sm" onClick={() => onRetireRule(selectedRule.ruleId)}>
                <Trash2 data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                停用规则
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
