import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { GitBranch, Layers, Plus, Rocket, Upload } from 'lucide-react';
import type { Site } from '@/api/generated/platformGateway.gen';
import { Panel, PanelHeader } from '@/blocks/panel';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import { formatTime } from '@/lib/operator-format';
import {
  TEMPLATE_EXECUTION_LABELS,
  TEMPLATE_SEVERITY_LABELS,
  alarmRuleTemplateDetailQuery,
  alarmRuleTemplatesQuery,
  describeAlarmTemplateError,
} from '../alarm-templates-api';
import { AlarmTemplateEditorDialog } from './AlarmTemplateEditorDialog';
import { AlarmTemplateRolloutDialog } from './AlarmTemplateRolloutDialog';

export function AlarmTemplatesWorkbench({
  sites,
  timeZone,
  active,
  canManage,
  csrfToken,
}: {
  readonly sites: readonly Site[];
  readonly timeZone: string;
  readonly active: boolean;
  readonly canManage: boolean;
  readonly csrfToken: string;
}) {
  const [searchText, setSearchText] = useState('');
  const [selectedId, setSelectedId] = useState('');
  const [editorState, setEditorState] = useState<{ open: boolean; mode: 'create' | 'release' }>({
    open: false,
    mode: 'create',
  });
  const [rolloutOpen, setRolloutOpen] = useState(false);

  const listQuery = useQuery(alarmRuleTemplatesQuery(active));
  const templates = useMemo(() => listQuery.data ?? [], [listQuery.data]);
  const filtered = useMemo(() => {
    const q = searchText.trim().toLowerCase();
    if (!q) return templates;
    return templates.filter(
      (t) =>
        t.displayName.toLowerCase().includes(q)
        || t.templateKey.toLowerCase().includes(q)
        || t.deviceType.toLowerCase().includes(q),
    );
  }, [templates, searchText]);

  const activeTemplateId = filtered.some((t) => t.templateId === selectedId)
    ? selectedId
    : (filtered[0]?.templateId ?? '');

  const detailQuery = useQuery(alarmRuleTemplateDetailQuery(activeTemplateId, active && Boolean(activeTemplateId)));
  const activeTemplate = detailQuery.data?.template ?? filtered.find((t) => t.templateId === activeTemplateId) ?? null;
  const releases = detailQuery.data?.releases ?? [];
  const siteNames = useMemo(() => new Map(sites.map((s) => [s.id, s.displayName])), [sites]);

  return (
    <div className="flex flex-col gap-4">
      <Panel aria-label="跨站点告警规则模板库" className="p-5">
        <PanelHeader
          title="跨站点告警规则基准模板库"
          caption="按设备类型与物模型维护基准阈值、默认严重程度及云边执行位置，差异预检后向多个授权冷站下发并保留站点个性覆写。"
          action={
            <div className="flex flex-wrap items-center gap-2">
              <Input
                value={searchText}
                onChange={(e) => setSearchText(e.target.value)}
                placeholder="筛选模板名称或设备类型"
                aria-label="筛选规则模板"
                className="h-8 w-52"
              />
              <Button
                type="button"
                size="sm"
                disabled={!canManage}
                onClick={() => setEditorState({ open: true, mode: 'create' })}
              >
                <Plus data-icon="inline-start" aria-hidden="true" />
                新建基准模板
              </Button>
            </div>
          }
        />

        <div className="mt-4">
          {listQuery.isPending ? (
            <Skeleton className="h-32 rounded-lg" />
          ) : listQuery.isError ? (
            <Alert variant="destructive">
              <AlertDescription>{describeAlarmTemplateError(listQuery.error, '规则模板列表读取失败')}</AlertDescription>
            </Alert>
          ) : filtered.length === 0 ? (
            <p className="rounded-lg border border-dashed px-4 py-8 text-center text-xs text-muted-foreground">
              暂无符合条件的跨站点告警规则模板，请点击「新建基准模板」创建。
            </p>
          ) : (
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]">
              <ul aria-label="告警基准模板列表" className="flex flex-col gap-2">
                {filtered.map((tmpl) => {
                  const selected = tmpl.templateId === activeTemplateId;
                  return (
                    <li
                      key={tmpl.templateId}
                      className={`flex flex-wrap items-center justify-between gap-3 rounded-lg border px-4 py-3 text-xs transition-colors ${selected ? 'border-primary bg-primary/5' : 'bg-muted/20 hover:bg-muted/40'}`}
                    >
                      <button
                        type="button"
                        className="flex min-w-0 flex-1 flex-col gap-1 text-left"
                        onClick={() => setSelectedId(tmpl.templateId)}
                      >
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="text-sm font-semibold text-foreground">{tmpl.displayName}</span>
                          <Badge variant="outline" className="font-mono">{tmpl.templateKey}</Badge>
                          <Badge variant="outline" className="font-mono">v{tmpl.latestVersion}</Badge>
                        </div>
                        <div className="flex flex-wrap items-center gap-2 text-muted-foreground">
                          <span>设备 {tmpl.deviceType}</span>
                          <span>· {TEMPLATE_SEVERITY_LABELS[tmpl.defaultSeverity]}</span>
                          <span>· {TEMPLATE_EXECUTION_LABELS[tmpl.defaultExecutionTarget]}</span>
                          <span>· 已绑定 {tmpl.boundSiteIds.length} 站</span>
                        </div>
                      </button>
                      <div className="flex items-center gap-2">
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            setSelectedId(tmpl.templateId);
                            setRolloutOpen(true);
                          }}
                        >
                          <Rocket data-icon="inline-start" aria-hidden="true" />
                          差异预检与下发
                        </Button>
                      </div>
                    </li>
                  );
                })}
              </ul>

              {activeTemplate ? (
                <section aria-label="模板详情与版本历史" className="flex flex-col gap-3 rounded-lg border bg-muted/20 p-4 text-xs">
                  <div className="flex flex-wrap items-center justify-between gap-2 border-b border-divider pb-3">
                    <div className="flex items-center gap-2">
                      <Layers className="size-4 text-primary" aria-hidden="true" />
                      <div>
                        <h3 className="text-sm font-semibold">{activeTemplate.displayName}</h3>
                        <p className="text-muted-foreground">{activeTemplate.description || '统一设备基准告警策略'}</p>
                      </div>
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={!canManage}
                      onClick={() => setEditorState({ open: true, mode: 'release' })}
                    >
                      <Upload data-icon="inline-start" aria-hidden="true" />
                      发布新版本
                    </Button>
                  </div>

                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="text-muted-foreground">绑定站点：</span>
                    {activeTemplate.boundSiteIds.length === 0 ? (
                      <span className="text-muted-foreground">未绑定站点</span>
                    ) : (
                      activeTemplate.boundSiteIds.map((sid) => (
                        <Badge key={sid} variant="outline">{siteNames.get(sid) ?? sid}</Badge>
                      ))
                    )}
                  </div>

                  <div className="flex flex-col gap-2">
                    <div className="flex items-center gap-1.5 font-semibold">
                      <GitBranch className="size-3.5 text-primary" aria-hidden="true" />
                      不可变发布版本账本
                    </div>
                    {detailQuery.isPending ? (
                      <Skeleton className="h-20 rounded-md" />
                    ) : releases.length === 0 ? (
                      <p className="text-muted-foreground">暂无发布版本</p>
                    ) : (
                      <ul aria-label="模板发布版本列表" className="flex flex-col gap-1.5">
                        {releases.map((rel) => (
                          <li key={rel.releaseId} className="rounded border bg-background px-3 py-2">
                            <div className="flex items-center justify-between gap-2">
                              <span className="font-mono font-semibold">v{rel.version} · {TEMPLATE_EXECUTION_LABELS[rel.ruleSpec.executionTarget]}</span>
                              <span className="text-muted-foreground">{formatTime(rel.publishedAt, timeZone)}</span>
                            </div>
                            <div className="mt-1 text-muted-foreground">
                              {rel.changeNote || '基准参数版本快照'} · {rel.publishedByName || rel.publishedBy}
                            </div>
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                </section>
              ) : null}
            </div>
          )}
        </div>
      </Panel>

      {editorState.open ? (
        <AlarmTemplateEditorDialog
          key={`${editorState.mode}-${activeTemplateId}-${activeTemplate?.latestVersion ?? 0}`}
          open={editorState.open}
          mode={editorState.mode}
          template={editorState.mode === 'release' ? activeTemplate : null}
          sites={sites}
          csrfToken={csrfToken}
          onClose={() => setEditorState((prev) => ({ ...prev, open: false }))}
          onSaved={(tid) => setSelectedId(tid)}
        />
      ) : null}

      {rolloutOpen ? (
        <AlarmTemplateRolloutDialog
          key={`rollout-${activeTemplateId}-${activeTemplate?.latestVersion ?? 0}`}
          open={rolloutOpen}
          template={activeTemplate}
          sites={sites}
          canManage={canManage}
          csrfToken={csrfToken}
          onClose={() => setRolloutOpen(false)}
        />
      ) : null}
    </div>
  );
}
