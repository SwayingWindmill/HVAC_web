import { RefreshCw, Send } from 'lucide-react';
import type { Template, TemplateRevision } from '@/api/generated/platformGateway.gen';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { isOneOf } from '@/lib/typed';
import type { TemplatePayloadSummary } from './template-model';
import {
  formatTemplateDriverSummary,
  formatTimestamp,
  kindLabel,
  TEMPLATE_KINDS,
  type AssignmentDraft,
  type RevisionOption,
  type TargetOption,
} from './template-workbench-types';

export interface TemplateCatalogAssignCardProps {
  readonly allowed: boolean;
  readonly working: boolean;
  readonly assignmentDraft: AssignmentDraft;
  readonly targets: readonly TargetOption[];
  readonly revisionOptions: readonly RevisionOption[];
  readonly previewRevision: TemplateRevision | null;
  readonly previewSummary: TemplatePayloadSummary | null;
  readonly previewRevisionId: string | null;
  readonly templates: readonly Template[];
  readonly templatesLoading: boolean;
  readonly templatesFetching: boolean;
  readonly hasNextTemplatesPage: boolean;
  readonly isFetchingNextTemplates: boolean;
  readonly onAssignmentDraftChange: (patch: Partial<AssignmentDraft>, selectedRevisionId?: string) => void;
  readonly onAssign: () => void;
  readonly onLoadRevisionIntoReleaseDraft: (revision: TemplateRevision) => void;
  readonly onSelectPreviewRevision: (revisionId: string) => void;
  readonly onRefreshTemplates: () => void;
  readonly onFetchNextTemplates: () => void;
}

export function TemplateCatalogAssignCard({
  allowed,
  working,
  assignmentDraft,
  targets,
  revisionOptions,
  previewRevision,
  previewSummary,
  previewRevisionId,
  templates,
  templatesLoading,
  templatesFetching,
  hasNextTemplatesPage,
  isFetchingNextTemplates,
  onAssignmentDraftChange,
  onAssign,
  onLoadRevisionIntoReleaseDraft,
  onSelectPreviewRevision,
  onRefreshTemplates,
  onFetchNextTemplates,
}: TemplateCatalogAssignCardProps) {
  return (
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
              onAssignmentDraftChange({ targetType: value, targetId: '' });
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
            onValueChange={(value) => onAssignmentDraftChange({ targetId: value })}
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
              onAssignmentDraftChange(
                {
                  templateRevisionId: value,
                  ...(opt ? { targetType: opt.kind } : {}),
                },
                value,
              );
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
            onChange={(event) => onAssignmentDraftChange({ reason: event.currentTarget.value })}
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
          onClick={onAssign}
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
                  onClick={() => onLoadRevisionIntoReleaseDraft(previewRevision)}
                >
                  载入到发布草稿
                </Button>
              </div>
            </div>
            <div className="flex flex-wrap gap-1.5">
              <span className="rounded border bg-background px-2 py-0.5">
                {kindLabel(previewRevision.templateKind)}
              </span>
              {previewSummary.deviceType ? (
                <span className="rounded border bg-background px-2 py-0.5 font-mono">
                  {previewSummary.deviceType}
                </span>
              ) : null}
              {previewSummary.standardPointCount > 0 ? (
                <span className="rounded border bg-background px-2 py-0.5 tabular-nums">
                  标准点位 {previewSummary.standardPointCount} 项
                </span>
              ) : null}
              {previewSummary.capabilityCount > 0 ? (
                <span className="rounded border bg-background px-2 py-0.5 tabular-nums">
                  控制能力 {previewSummary.capabilityCount} 项
                </span>
              ) : null}
              {previewSummary.modbusDriverSummary ? (
                <span className="rounded border bg-background px-2 py-0.5 tabular-nums">
                  {formatTemplateDriverSummary(previewSummary.modbusDriverSummary)}
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
            <p className="text-xs text-muted-foreground">租户模板目录（{templates.length} 项）</p>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={templatesFetching}
              onClick={onRefreshTemplates}
            >
              <RefreshCw className={templatesFetching ? 'motion-safe:animate-spin' : undefined} />
              刷新目录
            </Button>
          </div>
          {templatesLoading ? (
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
                            onClick={() => onSelectPreviewRevision(revId)}
                          >
                            预览
                          </Button>
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            disabled={!allowed}
                            onClick={() =>
                              onAssignmentDraftChange(
                                {
                                  targetType: tpl.templateKind,
                                  targetId:
                                    assignmentDraft.targetType === tpl.templateKind
                                      ? assignmentDraft.targetId
                                      : '',
                                  templateRevisionId: revId,
                                  reason: `应用 ${tpl.templateKey} 第 ${tpl.latestRevisionNumber ?? 1} 版`,
                                },
                                revId,
                              )
                            }
                          >
                            选用
                          </Button>
                        </>
                      ) : null}
                    </div>
                  </div>
                );
              })}
              {hasNextTemplatesPage ? (
                <div className="flex justify-center pt-1">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={isFetchingNextTemplates}
                    onClick={onFetchNextTemplates}
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
  );
}
