import { useCallback, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { Cpu, GitBranch, History, List, Pencil, Plus, Rocket, RotateCcw, Trash2 } from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { ConfirmDiscardDialog } from '@/components/confirm-discard-dialog';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useSiteEquipment } from '@/features/assets/use-device-names';
import { useDataTable } from '@/hooks/use-data-table';
import { alarmKeys } from '../alarm-api';
import { describeDependencyGate, summarizeRuleActions } from '../alarm-operations';
import { SeverityBadge, severityRank } from '../alarm-presentation';
import { computeDraftDelta, EXECUTION_TARGET_LABELS } from '../alarm-rule-dag';
import {
  describeCondition,
  describeDevices,
  describeRaise,
  describeRuleLinkage,
  describeRuleOrigin,
  siteAlarmRulesBundleQuery,
  type AlarmRule,
  type AlarmRulePublishResult,
  type ReadingNames,
} from '../alarm-rules';
import { AlarmRuleDagCanvas } from './AlarmRuleDagCanvas';
import { AlarmRuleEdgeCompileSheet } from './AlarmRuleEdgeCompileSheet';
import { AlarmRuleEditorSheet } from './AlarmRuleEditorSheet';
import { AlarmRulePublishDiffDialog } from './AlarmRulePublishDiffDialog';
import { AlarmRuleRevisionsSheet } from './AlarmRuleRevisionsSheet';
import { AlarmRuleSimulateSheet } from './AlarmRuleSimulateSheet';
import { AlarmRuleTemplateDriftSheet } from './AlarmRuleTemplateDriftSheet';

/** Site alarm rules workbench: list view, interactive causal DAG canvas, draft authoring, dry-run diff, hot-publish and rollback. */
export function AlarmRulesTable({
  siteId,
  canManage,
  canReadGatewayShadow = false,
  canManageGatewayShadow = false,
  csrfToken,
  timeZone,
}: {
  readonly siteId: string;
  readonly canManage: boolean;
  readonly canReadGatewayShadow?: boolean;
  readonly canManageGatewayShadow?: boolean;
  readonly csrfToken: string;
  readonly timeZone: string;
}) {
  const queryClient = useQueryClient();
  const [viewMode, setViewMode] = useState<'table' | 'dag'>('table');
  const [draftRules, setDraftRules] = useState<AlarmRule[] | null>(null);
  const [selectedDagRuleId, setSelectedDagRuleId] = useState<string | null>(null);
  const [editorState, setEditorState] = useState<{
    readonly open: boolean;
    readonly mode: 'create' | 'edit';
    readonly rule: AlarmRule | null;
  }>({ open: false, mode: 'create', rule: null });
  const [simulating, setSimulating] = useState<{ readonly rule: AlarmRule; readonly isDraft: boolean } | null>(null);
  const [diffDialogOpen, setDiffDialogOpen] = useState(false);
  const [revisionsOpen, setRevisionsOpen] = useState(false);
  const [edgeCompileOpen, setEdgeCompileOpen] = useState(false);
  const [driftSheetOpen, setDriftSheetOpen] = useState(false);
  const [discardDraftConfirmOpen, setDiscardDraftConfirmOpen] = useState(false);
  const [pendingRetireRule, setPendingRetireRule] = useState<{
    readonly rule: AlarmRule;
    readonly dependents: readonly string[];
  } | null>(null);
  const [publishNotice, setPublishNotice] = useState<string | null>(null);

  const bundleQuery = useQuery(siteAlarmRulesBundleQuery(siteId));
  const equipment = useSiteEquipment(siteId);
  const names = useMemo<ReadingNames>(() => ({
    device: (deviceId) => equipment.nameByDevice.get(deviceId) ?? '未登记设备',
    point: (reading) => equipment.pointByReading.get(`${reading.deviceId}/${reading.pointCode}`) ?? { name: '未登记读数' },
  }), [equipment]);

  const publishedRules = useMemo(() => bundleQuery.data?.items ?? [], [bundleQuery.data?.items]);
  const bundleRevision = bundleQuery.data?.bundleRevision ?? 0;
  const activeRules = draftRules ?? publishedRules;
  const delta = useMemo(() => computeDraftDelta(publishedRules, activeRules), [publishedRules, activeRules]);

  const sorted = useMemo(() => [...activeRules].sort((left, right) =>
    severityRank(left.severity) - severityRank(right.severity) || left.title.localeCompare(right.title, 'zh-CN')), [activeRules]);
  const titleByRuleId = useMemo(
    () => new Map(sorted.map((rule) => [rule.ruleId, rule.title])),
    [sorted],
  );

  const handleOpenCreate = useCallback(() => {
    setPublishNotice(null);
    setEditorState({ open: true, mode: 'create', rule: null });
  }, []);

  const handleOpenEdit = useCallback((rule: AlarmRule) => {
    setPublishNotice(null);
    setEditorState({ open: true, mode: 'edit', rule });
  }, []);

  const handleSaveDraftRule = useCallback((updatedRule: AlarmRule) => {
    setDraftRules((prev) => {
      const base = prev ?? [...publishedRules];
      const idx = base.findIndex((r) => r.ruleId === updatedRule.ruleId);
      if (idx >= 0) {
        return base.map((item, i) => (i === idx ? updatedRule : item));
      }
      return [...base, updatedRule];
    });
    setSelectedDagRuleId(updatedRule.ruleId);
    setEditorState({ open: false, mode: 'create', rule: null });
  }, [publishedRules]);

  const applyRetireRule = useCallback((ruleId: string) => {
    setPublishNotice(null);
    setDraftRules((prev) => {
      const base = prev ?? [...publishedRules];
      return base
        .filter((r) => r.ruleId !== ruleId)
        .map((r) => {
          if (!r.dependsOnRuleIds?.includes(ruleId)) return r;
          const nextDeps = r.dependsOnRuleIds.filter((id) => id !== ruleId);
          return {
            ...r,
            dependsOnRuleIds: nextDeps,
            dependencyGate: nextDeps.length > 0 ? r.dependencyGate : 'NONE',
          };
        });
    });
  }, [publishedRules]);

  const handleRetireRule = useCallback((ruleId: string) => {
    const target = activeRules.find((r) => r.ruleId === ruleId);
    if (!target) return;
    const dependents = activeRules
      .filter((r) => r.ruleId !== ruleId && r.dependsOnRuleIds?.includes(ruleId))
      .map((r) => r.title);
    setPendingRetireRule({ rule: target, dependents });
  }, [activeRules]);

  const handleOpenSimulate = useCallback((rule: AlarmRule) => {
    const isDraftRule = delta.addedIds.has(rule.ruleId) || delta.changedIds.has(rule.ruleId);
    setSimulating({ rule, isDraft: isDraftRule });
  }, [delta.addedIds, delta.changedIds]);

  const handleSyncedBundleResult = useCallback(async (result: AlarmRulePublishResult, verb: string) => {
    setDraftRules(null);
    setDiffDialogOpen(false);
    setRevisionsOpen(false);
    setPublishNotice(`已${verb}规则包至版本 v${result.bundleRevision}（共 ${result.items.length} 条生效规则）。`);
    await queryClient.invalidateQueries({ queryKey: ['alarms', siteId, 'rules'] });
    await queryClient.invalidateQueries({ queryKey: ['alarms', siteId, 'rule-revisions'] });
    await queryClient.invalidateQueries({ queryKey: alarmKeys.all(siteId) });
  }, [queryClient, siteId]);

  const handleReloadLatest = useCallback(async () => {
    setDraftRules(null);
    setDiffDialogOpen(false);
    await queryClient.invalidateQueries({ queryKey: ['alarms', siteId, 'rules'] });
    await queryClient.invalidateQueries({ queryKey: ['alarms', siteId, 'rule-revisions'] });
  }, [queryClient, siteId]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, AlarmRule>>>(() => [
    {
      id: 'title',
      header: '规则',
      cell: ({ row }) => {
        const isAdded = delta.addedIds.has(row.original.ruleId);
        const isChanged = delta.changedIds.has(row.original.ruleId);
        const target = row.original.executionTarget ?? 'CLOUD';
        return (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="font-medium text-foreground">{row.original.title}</span>
            {target !== 'CLOUD' ? (
              <Badge variant="outline" className="h-5 border-primary/35 bg-primary-soft px-1.5 text-[11px] font-normal text-primary">
                {EXECUTION_TARGET_LABELS[target]}
              </Badge>
            ) : null}
            {isAdded ? <Badge variant="default" className="h-5 px-1.5 text-[11px]">草稿新增</Badge> : null}
            {isChanged ? <Badge variant="warning" className="h-5 px-1.5 text-[11px]">草稿修改</Badge> : null}
          </div>
        );
      },
    },
    { id: 'devices', header: '设备', cell: ({ row }) => describeDevices(row.original, names) },
    { id: 'raise', header: '什么时候报警', cell: ({ row }) => describeRaise(row.original, names) },
    { id: 'clear', header: '什么时候恢复', cell: ({ row }) => describeCondition(row.original.clear, names) },
    {
      id: 'lineage',
      header: '来源与联动',
      cell: ({ row }) => {
        const hasDeps = (row.original.dependsOnRuleIds?.length ?? 0) > 0;
        const actionSummary = summarizeRuleActions(row.original.actions);
        return (
          <div className="flex flex-col gap-0.5 text-xs">
            <span className="text-foreground">{describeRuleOrigin(row.original)}</span>
            <span className="text-muted-foreground">
              {describeRuleLinkage(row.original, titleByRuleId)}
              {hasDeps ? `（${describeDependencyGate(row.original.dependencyGate)}）` : ''}
            </span>
            {actionSummary ? <span className="text-primary">{actionSummary}</span> : null}
          </div>
        );
      },
    },
    { id: 'severity', header: '严重程度', cell: ({ row }) => <SeverityBadge severity={row.original.severity} /> },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => handleOpenSimulate(row.original)}
          >
            模拟试算
          </Button>
          {canManage ? (
            <>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => handleOpenEdit(row.original)}
              >
                <Pencil data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                编辑
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={`停用规则 ${row.original.title}`}
                onClick={() => handleRetireRule(row.original.ruleId)}
              >
                <Trash2 className="size-3.5 text-destructive-text" aria-hidden="true" />
              </Button>
            </>
          ) : null}
        </div>
      ),
    },
  ], [canManage, delta.addedIds, delta.changedIds, handleOpenEdit, handleOpenSimulate, handleRetireRule, names, titleByRuleId]);

  const table = useDataTable({ key: `alarm-rules-${siteId}`, data: sorted, columns, paginate: false, getRowId: (rule) => rule.ruleId });

  return (
    <DataTableBlock>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <div className="inline-flex rounded-md border border-divider bg-muted/30 p-0.5" role="group" aria-label="规则视图切换">
            <Button
              type="button"
              variant={viewMode === 'table' ? 'default' : 'ghost'}
              size="sm"
              aria-pressed={viewMode === 'table'}
              onClick={() => setViewMode('table')}
            >
              <List data-icon="inline-start" className="size-3.5" aria-hidden="true" />
              列表视图
            </Button>
            <Button
              type="button"
              variant={viewMode === 'dag' ? 'default' : 'ghost'}
              size="sm"
              aria-pressed={viewMode === 'dag'}
              onClick={() => setViewMode('dag')}
            >
              <GitBranch data-icon="inline-start" className="size-3.5" aria-hidden="true" />
              拓扑画布
            </Button>
          </div>
          <Badge variant="outline" className="font-mono">
            {bundleRevision > 0 ? `生效版本 v${bundleRevision}` : '未归档基线'}
          </Badge>
          {bundleQuery.data ? (
            <Badge variant="outline" className="tabular-nums">{activeRules.length} 条规则</Badge>
          ) : null}
          <Button type="button" variant="outline" size="sm" onClick={() => setRevisionsOpen(true)}>
            <History data-icon="inline-start" className="size-3.5" aria-hidden="true" />
            版本历史
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={() => setEdgeCompileOpen(true)}>
            <Cpu data-icon="inline-start" className="size-3.5" aria-hidden="true" />
            边缘编译下发
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={() => setDriftSheetOpen(true)}>
            模板偏离核对
          </Button>
        </div>

        {canManage ? (
          <Button type="button" size="sm" onClick={handleOpenCreate}>
            <Plus data-icon="inline-start" className="size-3.5" aria-hidden="true" />
            新建规则
          </Button>
        ) : null}
      </div>

      {publishNotice ? (
        <div className="mb-3 flex items-center justify-between gap-2 rounded-lg border border-primary/35 bg-primary/10 px-3.5 py-2 text-xs text-foreground">
          <span>{publishNotice}</span>
          <Button type="button" variant="ghost" size="sm" onClick={() => setPublishNotice(null)}>知道了</Button>
        </div>
      ) : null}

      {delta.dirty ? (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-lg border border-primary/40 bg-primary/5 px-3.5 py-2.5 text-xs">
          <span className="font-medium text-foreground">
            未发布草稿：新增 {delta.addedCount} 条 · 修改 {delta.changedCount} 条 · 停用 {delta.retiredCount} 条
          </span>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setDiscardDraftConfirmOpen(true)}>
              <RotateCcw data-icon="inline-start" className="size-3.5" aria-hidden="true" />
              放弃草稿
            </Button>
            <Button type="button" size="sm" onClick={() => setDiffDialogOpen(true)}>
              <Rocket data-icon="inline-start" className="size-3.5" aria-hidden="true" />
              预览差异并热发布
            </Button>
          </div>
        </div>
      ) : null}

      {bundleQuery.isError ? (
        <p className="text-sm text-destructive-text">规则暂时读不到，稍后点「刷新」再看。</p>
      ) : bundleQuery.isPending ? (
        <div className="flex flex-col gap-2"><Skeleton className="h-12" /><Skeleton className="h-12" /><Skeleton className="h-12" /></div>
      ) : viewMode === 'dag' ? (
        <AlarmRuleDagCanvas
          rules={sorted}
          selectedRuleId={selectedDagRuleId}
          onSelectRule={setSelectedDagRuleId}
          onSimulateRule={handleOpenSimulate}
          onEditRule={handleOpenEdit}
          onRetireRule={handleRetireRule}
          delta={delta}
          canManage={canManage}
          names={names}
        />
      ) : (
        <DataTable
          table={table}
          tableAriaLabel="正在生效的规则"
          empty="这个站点还没有发布告警规则"
          getHeaderCellProps={(header) => ({
            className: header.id === 'title'
              ? 'w-48'
              : header.id === 'devices'
                ? 'w-44'
                : header.id === 'lineage'
                  ? 'w-52'
                  : header.id === 'severity'
                    ? 'w-24'
                    : header.id === 'actions'
                      ? 'w-48'
                      : undefined,
          })}
          getCellProps={(cell) => ({
            className: cell.column.id === 'raise' || cell.column.id === 'clear' || cell.column.id === 'devices' || cell.column.id === 'lineage'
              ? 'whitespace-normal align-top text-muted-foreground'
              : 'align-top',
          })}
        />
      )}

      <AlarmRuleSimulateSheet
        siteId={siteId}
        csrfToken={csrfToken}
        rule={simulating?.rule ?? null}
        isDraft={simulating?.isDraft ?? false}
        titleByRuleId={titleByRuleId}
        displayContext={{ deviceNames: equipment.nameByDevice, pointByReading: equipment.pointByReading }}
        names={names}
        onClose={() => setSimulating(null)}
      />

      <AlarmRuleEditorSheet
        open={editorState.open}
        mode={editorState.mode}
        initialRule={editorState.rule}
        allRules={activeRules}
        equipment={equipment}
        onClose={() => setEditorState({ open: false, mode: 'create', rule: null })}
        onSaveDraft={handleSaveDraftRule}
        onSimulateDraft={(draftRule) => {
          handleSaveDraftRule(draftRule);
          setSimulating({ rule: draftRule, isDraft: true });
        }}
      />

      <AlarmRulePublishDiffDialog
        open={diffDialogOpen}
        siteId={siteId}
        csrfToken={csrfToken}
        expectedRevision={bundleRevision}
        draftRules={activeRules}
        titleByRuleId={titleByRuleId}
        onClose={() => setDiffDialogOpen(false)}
        onPublished={(result) => void handleSyncedBundleResult(result, '热发布')}
        onReloadLatest={() => void handleReloadLatest()}
      />

      <AlarmRuleRevisionsSheet
        open={revisionsOpen}
        siteId={siteId}
        canManage={canManage}
        csrfToken={csrfToken}
        timeZone={timeZone}
        onClose={() => setRevisionsOpen(false)}
        onRolledBack={(result) => void handleSyncedBundleResult(result, '回滚')}
      />

      <AlarmRuleEdgeCompileSheet
        open={edgeCompileOpen}
        siteId={siteId}
        canManage={canManage}
        canReadGatewayShadow={canReadGatewayShadow}
        canManageGatewayShadow={canManageGatewayShadow}
        csrfToken={csrfToken}
        hasUnsavedDraft={delta.dirty}
        draftRules={delta.dirty ? activeRules : undefined}
        publishedBundleRevision={bundleRevision}
        onClose={() => setEdgeCompileOpen(false)}
      />
      <AlarmRuleTemplateDriftSheet
        open={driftSheetOpen}
        siteId={siteId}
        canManage={canManage}
        csrfToken={csrfToken}
        onClose={() => setDriftSheetOpen(false)}
        onResetCompleted={() => {
          void queryClient.invalidateQueries({ queryKey: ['alarms', siteId, 'rules'] });
          void queryClient.invalidateQueries({ queryKey: ['alarms', siteId, 'rule-revisions'] });
        }}
      />
      <ConfirmDiscardDialog
        open={discardDraftConfirmOpen}
        onOpenChange={setDiscardDraftConfirmOpen}
        title="确认放弃未发布的规则草稿吗？"
        description={`当前草稿包含新增 ${delta.addedCount} 条、修改 ${delta.changedCount} 条、停用 ${delta.retiredCount} 条改动，放弃后将恢复至已发布版本。`}
        keepLabel="继续编辑"
        discardLabel="确认放弃"
        onConfirmDiscard={() => {
          setDraftRules(null);
          setDiscardDraftConfirmOpen(false);
        }}
      />
      <ConfirmDiscardDialog
        open={pendingRetireRule !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setPendingRetireRule(null);
        }}
        title={pendingRetireRule ? `确认停用规则「${pendingRetireRule.rule.title}」？` : ''}
        description={pendingRetireRule && pendingRetireRule.dependents.length > 0
          ? `停用后将在草稿中移除该规则，并同步解除以下下游规则的前置依赖：${pendingRetireRule.dependents.join('、')}。`
          : '停用后将在草稿中移除该规则；热发布生效前可随时通过放弃草稿恢复。'}
        keepLabel="取消"
        discardLabel="确认停用"
        onConfirmDiscard={() => {
          if (pendingRetireRule) applyRetireRule(pendingRetireRule.rule.ruleId);
          setPendingRetireRule(null);
        }}
      />
    </DataTableBlock>
  );
}
