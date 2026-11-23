import { useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { History } from 'lucide-react';
import type {
  Capability,
  Site,
  SiteAssetModel,
  Template,
  TemplateAssignment,
  TemplateKind,
  TemplateRevision,
} from '@/api/generated/platformGateway.gen';
import {
  flattenRegistryPages,
  presentRegistryError,
  registryAdminApi,
  registryTemplateKeys,
  useRegistrySiteTemplateAssignments,
  useRegistryTemplateRevision,
  useRegistryTemplates,
} from '@/api/registry';
import { DataTableBlock } from '@/blocks/data-table';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useDataTable } from '@/hooks/use-data-table';
import { isOneOf } from '@/lib/typed';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';
import {
  buildReleaseReferencesFromEntries,
  createDefaultReferenceEntries,
  createEmptyStructuredDeviceDraft,
  parseStructuredDevicePayload,
  releaseReferencesToEntries,
  serializeStructuredDevicePayload,
  summarizeTemplatePayload,
  type StructuredDeviceTemplateDraft,
  type TemplateReferenceEntry,
} from './template-model';
import { TemplateCatalogAssignCard } from './TemplateCatalogAssignCard';
import { TemplateReleaseCard } from './TemplateReleaseCard';
import {
  ASSIGNMENT_FILTER_KINDS,
  formatTimestamp,
  kindLabel,
  parseObject,
  type AssignmentDraft,
  type PayloadEditorMode,
  type ReleaseDraft,
  type RevisionOption,
} from './template-workbench-types';

interface Props {
  site: Site;
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  csrfToken: string;
  onDirtyChange: (dirty: boolean) => void;
  onRefresh: () => Promise<void>;
}

export function RegistryTemplateWorkbench({ site, model, capabilities, csrfToken, onDirtyChange, onRefresh }: Props) {
  const queryClient = useQueryClient();
  const [payloadMode, setPayloadMode] = useState<PayloadEditorMode>('structured');
  const [releaseDraft, setReleaseDraft] = useState<ReleaseDraft>({
    templateKey: '',
    templateKind: 'DEVICE',
    payloadJson: '{}',
    reason: '发布登记模板版本',
  });
  const [structuredDraft, setStructuredDraft] = useState<StructuredDeviceTemplateDraft>(() =>
    createEmptyStructuredDeviceDraft(),
  );
  const [referenceEntries, setReferenceEntries] = useState<TemplateReferenceEntry[]>(() =>
    createDefaultReferenceEntries(),
  );
  const [assignmentDraft, setAssignmentDraft] = useState<AssignmentDraft>({
    targetType: 'DEVICE',
    targetId: '',
    templateRevisionId: '',
    reason: '',
  });
  const [assignmentFilterKind, setAssignmentFilterKind] = useState<'ALL' | TemplateKind>('ALL');
  const [selectedCatalogRevisionId, setSelectedCatalogRevisionId] = useState<string | null>(null);
  const [localRevisions, setLocalRevisions] = useState<Record<string, TemplateRevision>>({});
  const [releaseDirty, setReleaseDirty] = useState(false);
  const [assignmentDirty, setAssignmentDirty] = useState(false);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const allowed = capabilities.has('template.manage');
  const canReadTemplates =
    allowed || capabilities.has('asset.read') || capabilities.has('device.list');

  useEffect(() => onDirtyChange(releaseDirty || assignmentDirty), [releaseDirty, assignmentDirty, onDirtyChange]);

  const templatesQuery = useRegistryTemplates(undefined, canReadTemplates);
  const templates = flattenRegistryPages(templatesQuery.data);

  const assignmentsQuery = useRegistrySiteTemplateAssignments(
    site.id,
    assignmentFilterKind === 'ALL' ? {} : { targetType: assignmentFilterKind },
    canReadTemplates,
  );
  const assignments = flattenRegistryPages(assignmentsQuery.data);

  const previewRevisionId =
    assignmentDraft.templateRevisionId || selectedCatalogRevisionId || null;
  const previewRevisionQuery = useRegistryTemplateRevision(
    previewRevisionId,
    canReadTemplates && Boolean(previewRevisionId),
  );
  const previewRevision =
    (previewRevisionId ? localRevisions[previewRevisionId] : undefined) ??
    previewRevisionQuery.data ??
    null;

  const templateByLatestRevisionId = useMemo(() => {
    const map = new Map<string, Template>();
    for (const item of templates) {
      if (item.latestRevisionId) {
        map.set(item.latestRevisionId, item);
      }
    }
    return map;
  }, [templates]);

  const targets = useMemo(() => {
    if (assignmentDraft.targetType === 'ASSET') {
      return model.assets.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.code}` }));
    }
    if (assignmentDraft.targetType === 'DEVICE') {
      return model.devices.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.code}` }));
    }
    return model.telemetryPoints.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.pointCode}` }));
  }, [assignmentDraft.targetType, model]);

  const targetLabelById = useMemo(
    () =>
      new Map([
        ...model.assets.map((value) => [value.id, `${value.displayName} · ${value.code}`] as const),
        ...model.devices.map((value) => [value.id, `${value.displayName} · ${value.code}`] as const),
        ...model.telemetryPoints.map((value) => [value.id, `${value.displayName} · ${value.pointCode}`] as const),
      ]),
    [model],
  );

  const revisionOptions = useMemo(() => {
    const options = new Map<string, RevisionOption>();
    for (const tpl of templates) {
      if (!tpl.latestRevisionId || !tpl.latestRevisionNumber) continue;
      options.set(tpl.latestRevisionId, {
        id: tpl.latestRevisionId,
        label: `${tpl.templateKey} · 第 ${tpl.latestRevisionNumber} 版 · ${kindLabel(tpl.templateKind)}`,
        kind: tpl.templateKind,
      });
    }
    for (const rev of Object.values(localRevisions)) {
      options.set(rev.id, {
        id: rev.id,
        label: `${rev.templateKey} · 第 ${rev.revisionNumber} 版 · ${kindLabel(rev.templateKind)}`,
        kind: rev.templateKind,
      });
    }
    if (previewRevision) {
      options.set(previewRevision.id, {
        id: previewRevision.id,
        label: `${previewRevision.templateKey} · 第 ${previewRevision.revisionNumber} 版 · ${kindLabel(previewRevision.templateKind)}`,
        kind: previewRevision.templateKind,
      });
    }
    return Array.from(options.values());
  }, [templates, localRevisions, previewRevision]);

  const switchPayloadMode = (nextMode: PayloadEditorMode) => {
    if (nextMode === payloadMode || releaseDraft.templateKind !== 'DEVICE') return;
    setError(null);
    if (nextMode === 'json') {
      const built = serializeStructuredDevicePayload(structuredDraft);
      if (!built.ok) {
        setError(new Error(built.error));
        return;
      }
      setReleaseDraft((current) => ({
        ...current,
        payloadJson: JSON.stringify(built.payload, null, 2),
      }));
      setPayloadMode('json');
      return;
    }

    try {
      const parsedObj = parseObject(releaseDraft.payloadJson, '模板内容');
      const converted = parseStructuredDevicePayload(parsedObj);
      if (!converted.supported) {
        setError(new Error('当前 JSON 包含无法转回可视化字段的高级质量规则或自定义字段，已保留 JSON 编辑模式。'));
        return;
      }
      setStructuredDraft(converted.draft);
      setPayloadMode('structured');
    } catch (reason) {
      setError(reason);
    }
  };

  const loadRevisionIntoReleaseDraft = (rev: TemplateRevision) => {
    setError(null);
    setReleaseDraft((current) => ({
      ...current,
      templateKey: rev.templateKey,
      templateKind: rev.templateKind,
      payloadJson: JSON.stringify(rev.payload, null, 2),
      reason: `基于 ${rev.templateKey} 第 ${rev.revisionNumber} 版发布修订`,
    }));
    setReferenceEntries(releaseReferencesToEntries(rev.releaseReferences));
    if (rev.templateKind === 'DEVICE') {
      const parsed = parseStructuredDevicePayload(rev.payload);
      if (parsed.supported) {
        setStructuredDraft(parsed.draft);
        setPayloadMode('structured');
      } else {
        setPayloadMode('json');
      }
    } else {
      setPayloadMode('json');
    }
    setReleaseDirty(true);
  };

  const releaseTemplate = async () => {
    if (!releaseDraft.templateKey.trim() || !releaseDraft.reason.trim()) return;
    setWorking(true);
    setError(null);
    try {
      let payload: Record<string, unknown>;
      if (releaseDraft.templateKind === 'DEVICE' && payloadMode === 'structured') {
        const built = serializeStructuredDevicePayload(structuredDraft);
        if (!built.ok) throw new Error(built.error);
        payload = built.payload;
      } else {
        payload = parseObject(releaseDraft.payloadJson, '模板内容');
      }

      const refsResult = buildReleaseReferencesFromEntries(referenceEntries);
      if (!refsResult.ok) throw new Error(refsResult.error);

      const released = await registryAdminApi.releaseTemplate({
        templateKey: releaseDraft.templateKey.trim(),
        templateKind: releaseDraft.templateKind,
        payload,
        releaseReferences: refsResult.releaseReferences,
        meta: makeRegistryMutationMeta(0, releaseDraft.reason.trim(), newRegistryIdempotencyKey('template-release')),
      }, csrfToken);

      setLocalRevisions((current) => ({ ...current, [released.id]: released }));
      queryClient.setQueryData(registryTemplateKeys.revision(released.id), released);
      setSelectedCatalogRevisionId(released.id);
      setAssignmentDraft({
        targetType: released.templateKind,
        targetId: '',
        templateRevisionId: released.id,
        reason: `应用 ${released.templateKey} 第 ${released.revisionNumber} 版`,
      });
      setReleaseDraft({
        templateKey: '',
        templateKind: 'DEVICE',
        payloadJson: '{}',
        reason: '发布登记模板版本',
      });
      setStructuredDraft(createEmptyStructuredDeviceDraft());
      setReferenceEntries(createDefaultReferenceEntries());
      setPayloadMode('structured');
      setReleaseDirty(false);
      await queryClient.invalidateQueries({ queryKey: registryTemplateKeys.all() });
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const assignTemplate = async () => {
    if (!assignmentDraft.targetId || !assignmentDraft.templateRevisionId || !assignmentDraft.reason.trim()) return;
    setWorking(true);
    setError(null);
    try {
      await registryAdminApi.assignTemplate({
        siteId: site.id,
        targetType: assignmentDraft.targetType,
        targetId: assignmentDraft.targetId,
        templateRevisionId: assignmentDraft.templateRevisionId,
        effectiveAt: new Date().toISOString(),
        meta: makeRegistryMutationMeta(0, assignmentDraft.reason.trim(), newRegistryIdempotencyKey('template-assignment')),
      }, csrfToken);
      setAssignmentDraft((current) => ({ ...current, targetId: '', reason: '' }));
      setAssignmentDirty(false);
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ['registry', 'sites', site.id, 'template-assignments'],
        }),
        onRefresh(),
      ]);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const assignmentColumns = useMemo<Array<ColumnDef<DataTableFeatures, TemplateAssignment>>>(
    () => [
      {
        id: 'target',
        header: '应用对象',
        cell: ({ row }) => {
          const label = targetLabelById.get(row.original.targetId);
          return (
            <div>
              <strong className="block text-sm">{label ?? row.original.targetId.slice(0, 8)}</strong>
              {!label ? (
                <span className="font-mono text-xs text-muted-foreground">{row.original.targetId}</span>
              ) : null}
            </div>
          );
        },
      },
      {
        id: 'targetType',
        header: '对象类型',
        cell: ({ row }) => kindLabel(row.original.targetType),
      },
      {
        id: 'revision',
        header: '模板版本',
        cell: ({ row }) => {
          const localRev = localRevisions[row.original.templateRevisionId];
          if (localRev) {
            return (
              <span className="font-mono text-xs">
                {`${localRev.templateKey} · 第 ${localRev.revisionNumber} 版`}
              </span>
            );
          }
          const catalogTpl = templateByLatestRevisionId.get(row.original.templateRevisionId);
          if (catalogTpl) {
            return (
              <span className="font-mono text-xs">
                {`${catalogTpl.templateKey} · 第 ${catalogTpl.latestRevisionNumber ?? '?'} 版`}
              </span>
            );
          }
          return (
            <span className="font-mono text-xs text-muted-foreground">
              {row.original.templateRevisionId.slice(0, 8)}
            </span>
          );
        },
      },
      {
        id: 'validFrom',
        header: '生效时间',
        cell: ({ row }) => (
          <span className="font-mono text-xs tabular-nums">{formatTimestamp(row.original.validFrom)}</span>
        ),
      },
      {
        id: 'state',
        header: '状态',
        cell: ({ row }) =>
          row.original.validTo ? (
            <StatusBadge tone="neutral">已替代</StatusBadge>
          ) : (
            <StatusBadge tone="success">当前生效</StatusBadge>
          ),
      },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) => (
          <div className="flex flex-wrap gap-1">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setSelectedCatalogRevisionId(row.original.templateRevisionId)}
            >
              查看版本
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={!allowed}
              onClick={() => {
                setSelectedCatalogRevisionId(row.original.templateRevisionId);
                setAssignmentDraft({
                  targetType: row.original.targetType,
                  targetId: row.original.targetId,
                  templateRevisionId: row.original.templateRevisionId,
                  reason: '重新应用历史模板版本',
                });
                setAssignmentDirty(true);
              }}
            >
              回滚或重用
            </Button>
          </div>
        ),
        enableSorting: false,
        enableHiding: false,
      },
    ],
    [allowed, localRevisions, targetLabelById, templateByLatestRevisionId],
  );

  const assignmentTable = useDataTable({
    key: `registry-template-assignments-${site.id}-${assignmentFilterKind}`,
    data: [...assignments],
    columns: assignmentColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  const errorBanner = (() => {
    if (!error) return null;
    if (error instanceof Error && !(error instanceof Error && 'problem' in error)) {
      return { title: '模板输入校验未通过', description: error.message };
    }
    const presented = presentRegistryError(error);
    return { title: presented.title, description: presented.description };
  })();

  const previewSummary = previewRevision
    ? summarizeTemplatePayload(previewRevision.templateKind, previewRevision.payload)
    : null;

  return (
    <div className="space-y-4">
      {!allowed ? (
        <Alert>
          <AlertTitle>当前为只读模式</AlertTitle>
          <AlertDescription>当前用户没有模板维护权限；服务端仍会对每次模板操作独立授权。</AlertDescription>
        </Alert>
      ) : null}
      {errorBanner ? (
        <Alert variant="destructive">
          <AlertTitle>{errorBanner.title}</AlertTitle>
          <AlertDescription>{errorBanner.description}</AlertDescription>
        </Alert>
      ) : null}
      <Alert>
        <AlertTitle>模板版本发布后不可修改</AlertTitle>
        <AlertDescription>
          发布后形成全租户共用的不可变版本。将设备模板应用到具体设备时，服务端会自动实例化标准点位、质量检查参数及控制能力规格。
        </AlertDescription>
      </Alert>

      <div className="grid gap-4 xl:grid-cols-2">
        <TemplateReleaseCard
          allowed={allowed}
          working={working}
          payloadMode={payloadMode}
          releaseDraft={releaseDraft}
          structuredDraft={structuredDraft}
          referenceEntries={referenceEntries}
          onTemplateKeyChange={(value) => {
            setReleaseDraft((current) => ({ ...current, templateKey: value }));
            setReleaseDirty(true);
          }}
          onTemplateKindChange={(value) => {
            setReleaseDraft((current) => ({ ...current, templateKind: value }));
            if (value !== 'DEVICE') setPayloadMode('json');
            setReleaseDirty(true);
          }}
          onSwitchPayloadMode={switchPayloadMode}
          onStructuredDraftChange={(next) => {
            setStructuredDraft(next);
            setReleaseDirty(true);
          }}
          onPayloadJsonChange={(value) => {
            setReleaseDraft((current) => ({ ...current, payloadJson: value }));
            setReleaseDirty(true);
          }}
          onReferenceEntriesChange={(next) => {
            setReferenceEntries(next);
            setReleaseDirty(true);
          }}
          onReasonChange={(value) => {
            setReleaseDraft((current) => ({ ...current, reason: value }));
            setReleaseDirty(true);
          }}
          onRelease={() => void releaseTemplate()}
        />

        <TemplateCatalogAssignCard
          allowed={allowed}
          working={working}
          assignmentDraft={assignmentDraft}
          targets={targets}
          revisionOptions={revisionOptions}
          previewRevision={previewRevision}
          previewSummary={previewSummary}
          previewRevisionId={previewRevisionId}
          templates={templates}
          templatesLoading={templatesQuery.isLoading}
          templatesFetching={templatesQuery.isFetching}
          hasNextTemplatesPage={templatesQuery.hasNextPage}
          isFetchingNextTemplates={templatesQuery.isFetchingNextPage}
          onAssignmentDraftChange={(patch, selectedRevisionId) => {
            if (selectedRevisionId !== undefined) {
              setSelectedCatalogRevisionId(selectedRevisionId);
            }
            setAssignmentDraft((current) => ({ ...current, ...patch }));
            setAssignmentDirty(true);
          }}
          onAssign={() => void assignTemplate()}
          onLoadRevisionIntoReleaseDraft={loadRevisionIntoReleaseDraft}
          onSelectPreviewRevision={setSelectedCatalogRevisionId}
          onRefreshTemplates={() => void templatesQuery.refetch()}
          onFetchNextTemplates={() => void templatesQuery.fetchNextPage()}
        />
      </div>

      <DataTableBlock
        title="站点已应用模板"
        description="按生效时间逆序展示本站点资产、设备与点位绑定的不可变模板版本；支持查看内容或重新应用历史版本实现回滚。"
        controls={(
          <div className="flex flex-wrap items-center justify-between gap-2">
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={assignmentFilterKind}
              onValueChange={(value) => {
                if (isOneOf(ASSIGNMENT_FILTER_KINDS, value)) {
                  setAssignmentFilterKind(value);
                  assignmentTable.setPageIndex(0);
                }
              }}
              aria-label="筛选模板应用对象类型"
            >
              <ToggleGroupItem value="ALL" className="text-xs">全部</ToggleGroupItem>
              <ToggleGroupItem value="DEVICE" className="text-xs">设备</ToggleGroupItem>
              <ToggleGroupItem value="ASSET" className="text-xs">资产</ToggleGroupItem>
              <ToggleGroupItem value="POINT" className="text-xs">点位</ToggleGroupItem>
            </ToggleGroup>
            <Button
              variant="outline"
              size="sm"
              disabled={assignmentsQuery.isFetching}
              onClick={() => void assignmentsQuery.refetch()}
            >
              <History className="size-3.5" />
              刷新应用记录
            </Button>
          </div>
        )}
      >
        <DataTable
          table={assignmentTable}
          tableAriaLabel="站点已应用模板"
          empty="当前站点还没有模板应用记录"
          footer={<DataTablePagination table={assignmentTable} totalRows={assignments.length} />}
        />
        {assignmentsQuery.hasNextPage ? (
          <div className="mt-3 flex justify-center">
            <Button
              variant="outline"
              size="sm"
              disabled={assignmentsQuery.isFetchingNextPage}
              onClick={() => void assignmentsQuery.fetchNextPage()}
            >
              加载更多记录
            </Button>
          </div>
        ) : null}
      </DataTableBlock>
    </div>
  );
}
