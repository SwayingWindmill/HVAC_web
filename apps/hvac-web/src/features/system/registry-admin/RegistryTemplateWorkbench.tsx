import { useEffect, useId, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { History, LayoutTemplate, Plus, RefreshCw, Send, Trash2 } from 'lucide-react';
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
  registryTemplateKeys,
  registryAdminApi,
  useRegistrySiteTemplateAssignments,
  useRegistryTemplateRevision,
  useRegistryTemplates,
} from '@/api/registry';
import { DataTableBlock } from '@/blocks/data-table';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useDataTable } from '@/hooks/use-data-table';
import { isOneOf, isPlainRecord } from '@/lib/typed';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';
import {
  CAPABILITY_APPROVAL_POLICIES,
  CAPABILITY_RISK_LEVELS,
  CAPABILITY_STATUSES,
  STANDARD_POINT_TYPES,
  STANDARD_VALUE_TYPES,
  TEMPLATE_COUNTER_DECREASE_MODES,
  buildReleaseReferencesFromEntries,
  createDefaultReferenceEntries,
  createEmptyCapabilityProfileDraft,
  createEmptyStandardPointDraft,
  createEmptyStructuredDeviceDraft,
  parseStructuredDevicePayload,
  releaseReferencesToEntries,
  serializeStructuredDevicePayload,
  summarizeTemplatePayload,
  type CapabilityProfileDraft,
  type StandardPointDraft,
  type StructuredDeviceTemplateDraft,
  type TemplateReferenceEntry,
} from './template-model';

interface Props {
  site: Site;
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  csrfToken: string;
  onDirtyChange: (dirty: boolean) => void;
  onRefresh: () => Promise<void>;
}

type PayloadEditorMode = 'structured' | 'json';

type ReleaseDraft = {
  templateKey: string;
  templateKind: TemplateKind;
  payloadJson: string;
  reason: string;
};

type AssignmentDraft = {
  targetType: TemplateKind;
  targetId: string;
  templateRevisionId: string;
  reason: string;
};

const TEMPLATE_KINDS = ['ASSET', 'DEVICE', 'POINT'] as const satisfies readonly TemplateKind[];
const ASSIGNMENT_FILTER_KINDS = ['ALL', 'ASSET', 'DEVICE', 'POINT'] as const;

function parseObject(source: string, label: string): Record<string, unknown> {
  const parsed: unknown = JSON.parse(source);
  if (!isPlainRecord(parsed)) throw new Error(`${label}格式有误，请填写键值对形式的 JSON（以 { 开头）。`);
  return parsed;
}

function kindLabel(kind: TemplateKind): string {
  if (kind === 'ASSET') return '资产';
  if (kind === 'DEVICE') return '设备';
  return '点位';
}

function approvalPolicyLabel(policy: string): string {
  if (policy === 'SINGLE_APPROVER') return '单人审批';
  if (policy === 'TWO_PERSON') return '双人复核';
  return '无需审批';
}

function riskLevelLabel(level: string): string {
  if (level === 'CRITICAL') return '严重风险';
  if (level === 'HIGH') return '高风险';
  if (level === 'MEDIUM') return '中风险';
  return '低风险';
}

const ASSIGNMENT_TIME = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
});

function formatTimestamp(value: string): string {
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return value;
  return ASSIGNMENT_TIME.format(ms);
}

export function RegistryTemplateWorkbench({ site, model, capabilities, csrfToken, onDirtyChange, onRefresh }: Props) {
  const queryClient = useQueryClient();
  const structuredGroupId = useId();
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
    const options = new Map<string, { id: string; label: string; kind: TemplateKind }>();
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

  const updatePoint = (index: number, patch: Partial<StandardPointDraft>) => {
    setStructuredDraft((current) => ({
      ...current,
      standardPoints: current.standardPoints.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    }));
    setReleaseDirty(true);
  };

  const updateCapability = (index: number, patch: Partial<CapabilityProfileDraft>) => {
    setStructuredDraft((current) => ({
      ...current,
      capabilityProfiles: current.capabilityProfiles.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    }));
    setReleaseDirty(true);
  };

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
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <LayoutTemplate className="size-4" />
              发布模板版本
            </CardTitle>
            <CardDescription>
              设备模板支持结构化配置标准点位、质量检查与控制能力，也可切换为高级 JSON 编辑。
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="grid gap-1.5 text-sm">
                <span>模板名称</span>
                <Input
                  disabled={!allowed}
                  value={releaseDraft.templateKey}
                  placeholder="chiller-standard"
                  onChange={(event) => {
                    const value = event.currentTarget.value;
                    setReleaseDraft((current) => ({ ...current, templateKey: value }));
                    setReleaseDirty(true);
                  }}
                />
              </label>
              <label className="grid gap-1.5 text-sm">
                <span>适用对象</span>
                <Select
                  disabled={!allowed}
                  value={releaseDraft.templateKind}
                  onValueChange={(value) => {
                    if (!isOneOf(TEMPLATE_KINDS, value)) return;
                    setReleaseDraft((current) => ({ ...current, templateKind: value }));
                    if (value !== 'DEVICE') setPayloadMode('json');
                    setReleaseDirty(true);
                  }}
                >
                  <SelectTrigger aria-label="适用对象">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {TEMPLATE_KINDS.map((kind) => (
                        <SelectItem key={kind} value={kind}>
                          {kindLabel(kind)}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </label>
            </div>

            {releaseDraft.templateKind === 'DEVICE' ? (
              <div className="flex items-center justify-between gap-2 border-y border-divider py-2">
                <span id={structuredGroupId} className="text-xs font-medium text-muted-foreground">
                  模板内容格式
                </span>
                <ToggleGroup
                  type="single"
                  variant="outline"
                  size="sm"
                  value={payloadMode}
                  onValueChange={(val) => {
                    if (val === 'structured' || val === 'json') switchPayloadMode(val);
                  }}
                  aria-labelledby={structuredGroupId}
                >
                  <ToggleGroupItem value="structured" className="text-xs">
                    结构化配置
                  </ToggleGroupItem>
                  <ToggleGroupItem value="json" className="text-xs">
                    高级 JSON
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>
            ) : null}

            {releaseDraft.templateKind === 'DEVICE' && payloadMode === 'structured' ? (
              <div className="space-y-4">
                <label className="grid gap-1.5 text-sm">
                  <span>适用设备类型（deviceType，可选）</span>
                  <Input
                    disabled={!allowed}
                    value={structuredDraft.deviceType}
                    placeholder="CHILLER"
                    className="font-mono text-xs"
                    onChange={(event) => {
                      const value = event.currentTarget.value;
                      setStructuredDraft((current) => ({ ...current, deviceType: value }));
                      setReleaseDirty(true);
                    }}
                  />
                </label>

                <div className="space-y-2.5">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-semibold text-foreground">
                      标准点位定义（{structuredDraft.standardPoints.length} 项）
                    </span>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={!allowed}
                      onClick={() => {
                        setStructuredDraft((current) => ({
                          ...current,
                          standardPoints: [...current.standardPoints, createEmptyStandardPointDraft()],
                        }));
                        setReleaseDirty(true);
                      }}
                    >
                      <Plus data-icon="inline-start" />
                      新增标准点位
                    </Button>
                  </div>

                  {structuredDraft.standardPoints.length === 0 ? (
                    <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
                      未配置标准点位；点击「新增标准点位」后，模板应用到设备时会自动物化点位登记与质量规则。
                    </p>
                  ) : (
                    <div className="space-y-2.5">
                      {structuredDraft.standardPoints.map((pt, index) => (
                        <div key={index} className="space-y-2 rounded-lg border bg-muted/30 p-3">
                          <div className="flex items-center justify-between">
                            <span className="text-xs font-medium text-muted-foreground">点位 #{index + 1}</span>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon-xs"
                              disabled={!allowed}
                              aria-label={`删除标准点位 ${index + 1}`}
                              onClick={() => {
                                setStructuredDraft((current) => ({
                                  ...current,
                                  standardPoints: current.standardPoints.filter((_, i) => i !== index),
                                }));
                                setReleaseDirty(true);
                              }}
                            >
                              <Trash2 className="size-3.5" />
                            </Button>
                          </div>
                          <div className="grid gap-2 sm:grid-cols-3">
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">点位编码 (pointCode)</span>
                              <Input
                                disabled={!allowed}
                                value={pt.pointCode}
                                placeholder="chw_supply_temp"
                                className="h-8 font-mono text-xs"
                                onChange={(e) => updatePoint(index, { pointCode: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">显示名称</span>
                              <Input
                                disabled={!allowed}
                                value={pt.displayName}
                                placeholder="冷冻水供水温度"
                                className="h-8 text-xs"
                                onChange={(e) => updatePoint(index, { displayName: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">来源键（可选）</span>
                              <Input
                                disabled={!allowed}
                                value={pt.sourceKey}
                                placeholder="chw_supply_temp"
                                className="h-8 font-mono text-xs"
                                onChange={(e) => updatePoint(index, { sourceKey: e.currentTarget.value })}
                              />
                            </label>
                          </div>
                          <div className="grid gap-2 sm:grid-cols-4">
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">点位分类</span>
                              <Select
                                disabled={!allowed}
                                value={pt.pointType}
                                onValueChange={(value) => {
                                  if (!isOneOf(STANDARD_POINT_TYPES, value)) return;
                                  updatePoint(index, {
                                    pointType: value,
                                    writable: value === 'COMMAND' ? true : value === 'SETTING' ? pt.writable : false,
                                    valueType: value === 'COUNTER' ? 'NUMBER' : pt.valueType,
                                    counterDecreaseMode: value === 'COUNTER' ? pt.counterDecreaseMode || 'RESET_TO_ZERO' : '',
                                  });
                                }}
                              >
                                <SelectTrigger className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {STANDARD_POINT_TYPES.map((t) => (
                                    <SelectItem key={t} value={t} className="text-xs">
                                      {t}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">值类型</span>
                              <Select
                                disabled={!allowed || pt.pointType === 'COUNTER'}
                                value={pt.valueType}
                                onValueChange={(value) => {
                                  if (!isOneOf(STANDARD_VALUE_TYPES, value)) return;
                                  updatePoint(index, { valueType: value });
                                }}
                              >
                                <SelectTrigger className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {STANDARD_VALUE_TYPES.map((vt) => (
                                    <SelectItem key={vt} value={vt} className="text-xs">
                                      {vt}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">单位（可选）</span>
                              <Input
                                disabled={!allowed}
                                value={pt.unit}
                                placeholder="degC"
                                className="h-8 font-mono text-xs"
                                onChange={(e) => updatePoint(index, { unit: e.currentTarget.value })}
                              />
                            </label>
                            <div className="flex items-end pb-1">
                              <div className="flex items-center gap-2">
                                <Checkbox
                                  id={`point-writable-${index}`}
                                  checked={pt.pointType === 'COMMAND' ? true : pt.writable}
                                  disabled={!allowed || pt.pointType !== 'SETTING'}
                                  onCheckedChange={(checked) => updatePoint(index, { writable: checked === true })}
                                />
                                <Label htmlFor={`point-writable-${index}`} className="text-xs font-normal">
                                  允许写入
                                </Label>
                              </div>
                            </div>
                          </div>
                          {pt.pointType === 'COUNTER' ? (
                            <div className="grid gap-2 sm:grid-cols-2">
                              <label className="grid gap-1 text-xs">
                                <span className="text-muted-foreground">累计量下降处理</span>
                                <Select
                                  disabled={!allowed}
                                  value={pt.counterDecreaseMode || 'RESET_TO_ZERO'}
                                  onValueChange={(value) => {
                                    if (!isOneOf(TEMPLATE_COUNTER_DECREASE_MODES, value)) return;
                                    updatePoint(index, { counterDecreaseMode: value });
                                  }}
                                >
                                  <SelectTrigger className="h-8 text-xs">
                                    <SelectValue />
                                  </SelectTrigger>
                                  <SelectContent>
                                    <SelectItem value="RESET_TO_ZERO">RESET_TO_ZERO</SelectItem>
                                    <SelectItem value="ROLLOVER">ROLLOVER</SelectItem>
                                    <SelectItem value="INVALID">INVALID</SelectItem>
                                  </SelectContent>
                                </Select>
                              </label>
                              <label className="grid gap-1 text-xs">
                                <span className="text-muted-foreground">回绕模数（ROLLOVER 必填）</span>
                                <Input
                                  type="number"
                                  disabled={!allowed || pt.counterDecreaseMode !== 'ROLLOVER'}
                                  value={pt.counterRolloverModulus}
                                  className="h-8 font-mono text-xs tabular-nums"
                                  onChange={(e) => updatePoint(index, { counterRolloverModulus: e.currentTarget.value })}
                                />
                              </label>
                            </div>
                          ) : null}
                          <div className="grid gap-2 sm:grid-cols-3">
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">采样 ms</span>
                              <Input
                                type="number"
                                min={100}
                                disabled={!allowed}
                                value={pt.sampleIntervalMs}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updatePoint(index, { sampleIntervalMs: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">发布 ms</span>
                              <Input
                                type="number"
                                min={100}
                                disabled={!allowed}
                                value={pt.publishIntervalMs}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updatePoint(index, { publishIntervalMs: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">陈旧 ms</span>
                              <Input
                                type="number"
                                min={100}
                                disabled={!allowed}
                                value={pt.staleAfterMs}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updatePoint(index, { staleAfterMs: e.currentTarget.value })}
                              />
                            </label>
                          </div>
                          <div className="grid gap-2 sm:grid-cols-3">
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">数值下限（可选）</span>
                              <Input
                                inputMode="decimal"
                                disabled={!allowed || pt.valueType !== 'NUMBER'}
                                value={pt.minimumNumber}
                                placeholder="0"
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updatePoint(index, { minimumNumber: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">数值上限（可选）</span>
                              <Input
                                inputMode="decimal"
                                disabled={!allowed || pt.valueType !== 'NUMBER'}
                                value={pt.maximumNumber}
                                placeholder="100"
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updatePoint(index, { maximumNumber: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">卡死告警秒数（可选）</span>
                              <Input
                                type="number"
                                min={60}
                                disabled={!allowed}
                                value={pt.flatlineSeconds}
                                placeholder="300"
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updatePoint(index, { flatlineSeconds: e.currentTarget.value })}
                              />
                            </label>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>

                <div className="space-y-2.5">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-semibold text-foreground">
                      控制能力规格（{structuredDraft.capabilityProfiles.length} 项）
                    </span>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={!allowed}
                      onClick={() => {
                        setStructuredDraft((current) => ({
                          ...current,
                          capabilityProfiles: [...current.capabilityProfiles, createEmptyCapabilityProfileDraft()],
                        }));
                        setReleaseDirty(true);
                      }}
                    >
                      <Plus data-icon="inline-start" />
                      新增控制能力
                    </Button>
                  </div>

                  {structuredDraft.capabilityProfiles.length === 0 ? (
                    <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
                      未配置控制能力规格；适用于仅采集遥测而不下发写指令的设备。
                    </p>
                  ) : (
                    <div className="space-y-2.5">
                      {structuredDraft.capabilityProfiles.map((cap, index) => (
                        <div key={index} className="space-y-2 rounded-lg border bg-muted/30 p-3">
                          <div className="flex items-center justify-between">
                            <span className="text-xs font-medium text-muted-foreground">能力 #{index + 1}</span>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon-xs"
                              disabled={!allowed}
                              aria-label={`删除控制能力 ${index + 1}`}
                              onClick={() => {
                                setStructuredDraft((current) => ({
                                  ...current,
                                  capabilityProfiles: current.capabilityProfiles.filter((_, i) => i !== index),
                                }));
                                setReleaseDirty(true);
                              }}
                            >
                              <Trash2 className="size-3.5" />
                            </Button>
                          </div>
                          <div className="grid gap-2 sm:grid-cols-3">
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">能力名称</span>
                              <Input
                                disabled={!allowed}
                                value={cap.capabilityName}
                                placeholder="set_chw_setpoint"
                                className="h-8 font-mono text-xs"
                                onChange={(e) => updateCapability(index, { capabilityName: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">能力版本</span>
                              <Input
                                disabled={!allowed}
                                value={cap.capabilityRevision}
                                placeholder="v1"
                                className="h-8 font-mono text-xs"
                                onChange={(e) => updateCapability(index, { capabilityRevision: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">连接器类型</span>
                              <Input
                                disabled={!allowed}
                                value={cap.connectorKind}
                                placeholder="MODBUS_TCP"
                                className="h-8 font-mono text-xs"
                                onChange={(e) => updateCapability(index, { connectorKind: e.currentTarget.value })}
                              />
                            </label>
                          </div>
                          <div className="grid gap-2 sm:grid-cols-4">
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">规范单位</span>
                              <Input
                                disabled={!allowed}
                                value={cap.canonicalUnit}
                                placeholder="degC"
                                className="h-8 font-mono text-xs"
                                onChange={(e) => updateCapability(index, { canonicalUnit: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">最小值</span>
                              <Input
                                inputMode="decimal"
                                disabled={!allowed}
                                value={cap.minimumValue}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updateCapability(index, { minimumValue: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">最大值</span>
                              <Input
                                inputMode="decimal"
                                disabled={!allowed}
                                value={cap.maximumValue}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updateCapability(index, { maximumValue: e.currentTarget.value })}
                              />
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">最大步长</span>
                              <Input
                                inputMode="decimal"
                                disabled={!allowed}
                                value={cap.maximumDelta}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(e) => updateCapability(index, { maximumDelta: e.currentTarget.value })}
                              />
                            </label>
                          </div>
                          <div className="grid gap-2 sm:grid-cols-3">
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">状态</span>
                              <Select
                                disabled={!allowed}
                                value={cap.status}
                                onValueChange={(value) => {
                                  if (isOneOf(CAPABILITY_STATUSES, value)) updateCapability(index, { status: value });
                                }}
                              >
                                <SelectTrigger className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  <SelectItem value="VERIFIED">VERIFIED</SelectItem>
                                  <SelectItem value="DRAFT">DRAFT</SelectItem>
                                </SelectContent>
                              </Select>
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">风险等级</span>
                              <Select
                                disabled={!allowed}
                                value={cap.riskLevel}
                                onValueChange={(value) => {
                                  if (isOneOf(CAPABILITY_RISK_LEVELS, value)) updateCapability(index, { riskLevel: value });
                                }}
                              >
                                <SelectTrigger className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {CAPABILITY_RISK_LEVELS.map((lv) => (
                                    <SelectItem key={lv} value={lv}>
                                      {riskLevelLabel(lv)} ({lv})
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </label>
                            <label className="grid gap-1 text-xs">
                              <span className="text-muted-foreground">审批策略</span>
                              <Select
                                disabled={!allowed}
                                value={cap.approvalPolicy}
                                onValueChange={(value) => {
                                  if (isOneOf(CAPABILITY_APPROVAL_POLICIES, value)) {
                                    updateCapability(index, { approvalPolicy: value });
                                  }
                                }}
                              >
                                <SelectTrigger className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {CAPABILITY_APPROVAL_POLICIES.map((ap) => (
                                    <SelectItem key={ap} value={ap}>
                                      {approvalPolicyLabel(ap)}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </label>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            ) : (
              <label className="grid gap-1.5 text-sm">
                <span>模板内容 JSON</span>
                <Textarea
                  disabled={!allowed}
                  rows={8}
                  className="font-mono text-xs"
                  value={releaseDraft.payloadJson}
                  onChange={(event) => {
                    const value = event.currentTarget.value;
                    setReleaseDraft((current) => ({ ...current, payloadJson: value }));
                    setReleaseDirty(true);
                  }}
                />
              </label>
            )}

            <div className="space-y-2 border-t border-divider pt-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-semibold text-foreground">发布引用（Key-Value）</span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!allowed}
                  onClick={() => {
                    setReferenceEntries((current) => [...current, { key: '', value: '' }]);
                    setReleaseDirty(true);
                  }}
                >
                  <Plus data-icon="inline-start" />
                  添加引用
                </Button>
              </div>
              <div className="space-y-1.5">
                {referenceEntries.map((entry, index) => (
                  <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
                    <Input
                      disabled={!allowed}
                      aria-label={`发布引用键 ${index + 1}`}
                      placeholder="registrySchema"
                      value={entry.key}
                      className="h-8 font-mono text-xs"
                      onChange={(event) => {
                        const val = event.currentTarget.value;
                        setReferenceEntries((current) =>
                          current.map((item, i) => (i === index ? { ...item, key: val } : item)),
                        );
                        setReleaseDirty(true);
                      }}
                    />
                    <Input
                      disabled={!allowed}
                      aria-label={`发布引用值 ${index + 1}`}
                      placeholder="v1"
                      value={entry.value}
                      className="h-8 font-mono text-xs"
                      onChange={(event) => {
                        const val = event.currentTarget.value;
                        setReferenceEntries((current) =>
                          current.map((item, i) => (i === index ? { ...item, value: val } : item)),
                        );
                        setReleaseDirty(true);
                      }}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      disabled={!allowed || referenceEntries.length <= 1}
                      aria-label={`删除发布引用 ${index + 1}`}
                      onClick={() => {
                        setReferenceEntries((current) => current.filter((_, i) => i !== index));
                        setReleaseDirty(true);
                      }}
                    >
                      <Trash2 className="size-3.5" />
                    </Button>
                  </div>
                ))}
              </div>
            </div>

            <label className="grid gap-1.5 text-sm">
              <span>发布原因</span>
              <Textarea
                disabled={!allowed}
                rows={2}
                value={releaseDraft.reason}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setReleaseDraft((current) => ({ ...current, reason: value }));
                  setReleaseDirty(true);
                }}
              />
            </label>
            <Button
              disabled={!allowed || working || !releaseDraft.templateKey.trim() || !releaseDraft.reason.trim()}
              onClick={() => void releaseTemplate()}
            >
              {working ? '正在处理…' : '发布新版本'}
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Send className="size-4" />
              模板目录与应用
            </CardTitle>
            <CardDescription>
              从持久化模板目录选择已发布版本预览详情并应用到当前站点的资产、设备或点位。
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <label className="grid gap-1.5 text-sm">
              <span>对象类型</span>
              <Select
                disabled={!allowed}
                value={assignmentDraft.targetType}
                onValueChange={(value) => {
                  if (!isOneOf(TEMPLATE_KINDS, value)) return;
                  setAssignmentDraft((current) => ({ ...current, targetType: value, targetId: '' }));
                  setAssignmentDirty(true);
                }}
              >
                <SelectTrigger aria-label="应用对象类型">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {TEMPLATE_KINDS.map((kind) => (
                      <SelectItem key={kind} value={kind}>
                        {kindLabel(kind)}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </label>

            <label className="grid gap-1.5 text-sm">
              <span>应用对象</span>
              <Select
                disabled={!allowed || targets.length === 0}
                value={assignmentDraft.targetId || undefined}
                onValueChange={(value) => {
                  setAssignmentDraft((current) => ({ ...current, targetId: value }));
                  setAssignmentDirty(true);
                }}
              >
                <SelectTrigger aria-label="选择应用对象">
                  <SelectValue placeholder="选择对象" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {targets.map((target) => (
                      <SelectItem key={target.value} value={target.value}>
                        {target.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </label>

            <label className="grid gap-1.5 text-sm">
              <span>已发布模板版本</span>
              <Select
                disabled={!allowed || revisionOptions.length === 0}
                value={assignmentDraft.templateRevisionId || undefined}
                onValueChange={(value) => {
                  const opt = revisionOptions.find((item) => item.id === value);
                  setSelectedCatalogRevisionId(value);
                  setAssignmentDraft((current) => ({
                    ...current,
                    templateRevisionId: value,
                    targetType: opt?.kind ?? current.targetType,
                  }));
                  setAssignmentDirty(true);
                }}
              >
                <SelectTrigger aria-label="选择模板版本">
                  <SelectValue placeholder="选择已发布版本" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {revisionOptions.map((option) => (
                      <SelectItem key={option.id} value={option.id}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </label>

            <label className="grid gap-1.5 text-sm">
              <span>应用原因</span>
              <Textarea
                disabled={!allowed}
                rows={2}
                value={assignmentDraft.reason}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setAssignmentDraft((current) => ({ ...current, reason: value }));
                  setAssignmentDirty(true);
                }}
              />
            </label>

            <Button
              disabled={
                !allowed ||
                working ||
                !assignmentDraft.targetId ||
                !assignmentDraft.templateRevisionId ||
                !assignmentDraft.reason.trim()
              }
              onClick={() => void assignTemplate()}
            >
              {working ? '正在处理…' : '从现在起应用'}
            </Button>

            {previewRevision && previewSummary ? (
              <div className="space-y-2 rounded-lg border bg-muted/30 p-3 text-xs">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <strong className="font-mono text-foreground">
                    {previewRevision.templateKey} · 第 {previewRevision.revisionNumber} 版
                  </strong>
                  <div className="flex items-center gap-1.5">
                    <span className="font-mono text-muted-foreground tabular-nums">
                      {formatTimestamp(previewRevision.releasedAt)}
                    </span>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={!allowed}
                      onClick={() => loadRevisionIntoReleaseDraft(previewRevision)}
                    >
                      载入到发布草稿
                    </Button>
                  </div>
                </div>
                <div className="flex flex-wrap gap-1.5">
                  <span className="rounded bg-background px-2 py-0.5 border">
                    {kindLabel(previewRevision.templateKind)}
                  </span>
                  {previewSummary.deviceType ? (
                    <span className="rounded bg-background px-2 py-0.5 border font-mono">
                      {previewSummary.deviceType}
                    </span>
                  ) : null}
                  {previewSummary.standardPointCount > 0 ? (
                    <span className="rounded bg-background px-2 py-0.5 border tabular-nums">
                      标准点位 {previewSummary.standardPointCount} 项
                    </span>
                  ) : null}
                  {previewSummary.capabilityCount > 0 ? (
                    <span className="rounded bg-background px-2 py-0.5 border tabular-nums">
                      控制能力 {previewSummary.capabilityCount} 项
                    </span>
                  ) : null}
                  {previewSummary.modbusDriverSummary ? (
                    <span className="rounded border bg-background px-2 py-0.5 tabular-nums">
                      Modbus 驱动 ({previewSummary.modbusDriverSummary.driverTypeLabel}) ·{' '}
                      {previewSummary.modbusDriverSummary.registerCount} 寄存器
                    </span>
                  ) : null}
                </div>
                <div className="text-muted-foreground">
                  发布引用：
                  {Object.entries(previewRevision.releaseReferences)
                    .map(([k, v]) => `${k}=${v}`)
                    .join(' · ')}
                </div>
              </div>
            ) : null}

            <div className="space-y-2 border-t border-divider pt-3">
              <div className="flex items-center justify-between">
                <p className="text-xs text-muted-foreground">
                  租户模板目录（{templates.length} 项）
                </p>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={templatesQuery.isFetching}
                  onClick={() => void templatesQuery.refetch()}
                >
                  <RefreshCw className={templatesQuery.isFetching ? 'motion-safe:animate-spin' : undefined} />
                  刷新目录
                </Button>
              </div>
              {templatesQuery.isLoading ? (
                <div className="rounded-md border p-4 text-xs text-muted-foreground">正在读取模板目录…</div>
              ) : templates.length === 0 ? (
                <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
                  当前租户尚未发布模板版本
                </div>
              ) : (
                <div className="max-h-60 space-y-2 overflow-y-auto pr-1">
                  {templates.map((tpl) => {
                    const revId = tpl.latestRevisionId;
                    const selected = Boolean(revId && previewRevisionId === revId);
                    return (
                      <div
                        key={tpl.id}
                        className="flex items-center justify-between gap-3 rounded-md border p-2.5"
                      >
                        <div className="min-w-0">
                          <strong className="block truncate font-mono text-xs">{tpl.templateKey}</strong>
                          <span className="text-xs text-muted-foreground tabular-nums">
                            {tpl.latestRevisionNumber ? `最新第 ${tpl.latestRevisionNumber} 版` : '待发布'} · {kindLabel(tpl.templateKind)}
                          </span>
                        </div>
                        <div className="flex shrink-0 items-center gap-1">
                          {revId ? (
                            <>
                              <Button
                                type="button"
                                variant={selected ? 'outline' : 'ghost'}
                                size="sm"
                                onClick={() => setSelectedCatalogRevisionId(revId)}
                              >
                                预览
                              </Button>
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                disabled={!allowed}
                                onClick={() => {
                                  setSelectedCatalogRevisionId(revId);
                                  setAssignmentDraft((current) => ({
                                    ...current,
                                    targetType: tpl.templateKind,
                                    targetId: current.targetType === tpl.templateKind ? current.targetId : '',
                                    templateRevisionId: revId,
                                    reason: `应用 ${tpl.templateKey} 第 ${tpl.latestRevisionNumber ?? 1} 版`,
                                  }));
                                  setAssignmentDirty(true);
                                }}
                              >
                                选用
                              </Button>
                            </>
                          ) : null}
                        </div>
                      </div>
                    );
                  })}
                  {templatesQuery.hasNextPage ? (
                    <div className="flex justify-center pt-1">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={templatesQuery.isFetchingNextPage}
                        onClick={() => void templatesQuery.fetchNextPage()}
                      >
                        加载更多模板
                      </Button>
                    </div>
                  ) : null}
                </div>
              )}
            </div>
          </CardContent>
        </Card>
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
