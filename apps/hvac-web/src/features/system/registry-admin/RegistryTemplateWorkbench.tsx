import { useEffect, useMemo, useState } from 'react';
import { History, LayoutTemplate, Send } from 'lucide-react';
import type { Capability, Site, SiteAssetModel, TemplateAssignment, TemplateKind, TemplateRevision } from '@/api/generated/platformGateway.gen';
import { presentRegistryError, registryAdminApi } from '@/api/registry';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';

interface Props {
  site: Site;
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  onDirtyChange: (dirty: boolean) => void;
}

type ReleaseDraft = {
  templateKey: string;
  templateKind: TemplateKind;
  payload: string;
  releaseReferences: string;
  reason: string;
};

type AssignmentDraft = {
  targetType: TemplateKind;
  targetId: string;
  templateRevisionId: string;
  reason: string;
};

function parseObject(source: string, label: string): Record<string, unknown> {
  const parsed: unknown = JSON.parse(source);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error(`${label} 必须是 JSON object。`);
  return parsed as Record<string, unknown>;
}

function parseStringMap(source: string): Record<string, string> {
  const value = parseObject(source, '发布引用');
  const entries = Object.entries(value);
  if (!entries.length || entries.some(([, item]) => typeof item !== 'string' || !item.trim())) throw new Error('发布引用必须包含至少一个非空字符串值。');
  return Object.fromEntries(entries) as Record<string, string>;
}

function kindLabel(kind: TemplateKind): string {
  if (kind === 'ASSET') return '设备资产';
  if (kind === 'DEVICE') return '设备端点';
  return '测点';
}

export function RegistryTemplateWorkbench({ site, model, capabilities, onDirtyChange }: Props) {
  const [releaseDraft, setReleaseDraft] = useState<ReleaseDraft>({ templateKey: '', templateKind: 'DEVICE', payload: '{}', releaseReferences: '{\n  "registrySchema": "v1"\n}', reason: '发布登记模板版本' });
  const [assignmentDraft, setAssignmentDraft] = useState<AssignmentDraft>({ targetType: 'DEVICE', targetId: '', templateRevisionId: '', reason: '' });
  const [releaseDirty, setReleaseDirty] = useState(false);
  const [assignmentDirty, setAssignmentDirty] = useState(false);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [releases, setReleases] = useState<TemplateRevision[]>([]);
  const [assignments, setAssignments] = useState<TemplateAssignment[]>([]);
  const allowed = capabilities.has('template.manage');

  useEffect(() => onDirtyChange(releaseDirty || assignmentDirty), [releaseDirty, assignmentDirty, onDirtyChange]);

  const targets = useMemo(() => {
    if (assignmentDraft.targetType === 'ASSET') return model.assets.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.code}` }));
    if (assignmentDraft.targetType === 'DEVICE') return model.devices.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.code}` }));
    return model.telemetryPoints.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.pointCode}` }));
  }, [assignmentDraft.targetType, model]);

  const targetLabelById = useMemo(() => new Map([
    ...model.assets.map((value) => [value.id, value.displayName] as const),
    ...model.devices.map((value) => [value.id, value.displayName] as const),
    ...model.telemetryPoints.map((value) => [value.id, value.displayName] as const),
  ]), [model]);
  const releaseById = useMemo(() => new Map(releases.map((release) => [release.id, release])), [releases]);

  const releaseTemplate = async () => {
    if (!releaseDraft.templateKey.trim() || !releaseDraft.reason.trim()) return;
    setWorking(true);
    setError(null);
    try {
      const released = await registryAdminApi.releaseTemplate({
        templateKey: releaseDraft.templateKey.trim(),
        templateKind: releaseDraft.templateKind,
        payload: parseObject(releaseDraft.payload, '模板内容'),
        releaseReferences: parseStringMap(releaseDraft.releaseReferences),
        meta: makeRegistryMutationMeta(0, releaseDraft.reason.trim(), newRegistryIdempotencyKey('template-release')),
      });
      setReleases((current) => [released, ...current.filter((item) => item.id !== released.id)]);
      setAssignmentDraft({
        targetType: released.templateKind,
        targetId: '',
        templateRevisionId: released.id,
        reason: `应用 ${released.templateKey} 第 ${released.revisionNumber} 版`,
      });
      setReleaseDraft({ templateKey: '', templateKind: 'DEVICE', payload: '{}', releaseReferences: '{\n  "registrySchema": "v1"\n}', reason: '发布登记模板版本' });
      setReleaseDirty(false);
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
      const assignment = await registryAdminApi.assignTemplate({
        siteId: site.id,
        targetType: assignmentDraft.targetType,
        targetId: assignmentDraft.targetId,
        templateRevisionId: assignmentDraft.templateRevisionId,
        effectiveAt: new Date().toISOString(),
        meta: makeRegistryMutationMeta(0, assignmentDraft.reason.trim(), newRegistryIdempotencyKey('template-assignment')),
      });
      setAssignments((current) => [assignment, ...current]);
      setAssignmentDraft((current) => ({ ...current, targetId: '', reason: '' }));
      setAssignmentDirty(false);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const presentedError = error ? presentRegistryError(error) : null;

  return (
    <div className="space-y-4">
      {!allowed ? <Alert><AlertTitle>当前为只读模式</AlertTitle><AlertDescription>当前用户没有模板维护权限；服务端仍会对每次模板操作独立授权。</AlertDescription></Alert> : null}
      {presentedError ? <Alert variant="destructive"><AlertTitle>{presentedError.title}</AlertTitle><AlertDescription>{presentedError.description}</AlertDescription></Alert> : null}
      <Alert><AlertTitle>模板版本发布后不可修改</AlertTitle><AlertDescription>编辑区只是发布前草稿。发布后会形成不可变版本；回滚通过重新应用历史版本完成，不会改写历史。</AlertDescription></Alert>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader><CardTitle className="flex items-center gap-2"><LayoutTemplate className="size-4" />发布模板版本</CardTitle><CardDescription>定义模板内容并发布一个新的不可变版本。</CardDescription></CardHeader>
          <CardContent className="space-y-4">
            <label className="grid gap-1.5 text-sm"><span>模板名称</span><Input disabled={!allowed} value={releaseDraft.templateKey} placeholder="ahu-standard" onChange={(event) => { setReleaseDraft((current) => ({ ...current, templateKey: event.target.value })); setReleaseDirty(true); }} /></label>
            <label className="grid gap-1.5 text-sm"><span>适用对象</span><Select disabled={!allowed} value={releaseDraft.templateKind} onValueChange={(value) => { setReleaseDraft((current) => ({ ...current, templateKind: value as TemplateKind })); setReleaseDirty(true); }}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{(['ASSET', 'DEVICE', 'POINT'] as TemplateKind[]).map((kind) => <SelectItem key={kind} value={kind}>{kindLabel(kind)}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
            <label className="grid gap-1.5 text-sm"><span>模板内容 JSON</span><Textarea disabled={!allowed} rows={8} className="font-mono text-xs" value={releaseDraft.payload} onChange={(event) => { setReleaseDraft((current) => ({ ...current, payload: event.target.value })); setReleaseDirty(true); }} /></label>
            <label className="grid gap-1.5 text-sm"><span>发布引用 JSON</span><Textarea disabled={!allowed} rows={4} className="font-mono text-xs" value={releaseDraft.releaseReferences} onChange={(event) => { setReleaseDraft((current) => ({ ...current, releaseReferences: event.target.value })); setReleaseDirty(true); }} /></label>
            <label className="grid gap-1.5 text-sm"><span>发布原因</span><Textarea disabled={!allowed} rows={2} value={releaseDraft.reason} onChange={(event) => { setReleaseDraft((current) => ({ ...current, reason: event.target.value })); setReleaseDirty(true); }} /></label>
            <Button disabled={!allowed || working || !releaseDraft.templateKey.trim() || !releaseDraft.reason.trim()} onClick={() => void releaseTemplate()}>{working ? '正在处理…' : '发布新版本'}</Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle className="flex items-center gap-2"><Send className="size-4" />应用或回滚模板</CardTitle><CardDescription>选择本次会话已知的模板版本并应用到登记对象。</CardDescription></CardHeader>
          <CardContent className="space-y-4">
            <label className="grid gap-1.5 text-sm"><span>对象类型</span><Select disabled={!allowed} value={assignmentDraft.targetType} onValueChange={(value) => { setAssignmentDraft((current) => ({ ...current, targetType: value as TemplateKind, targetId: '' })); setAssignmentDirty(true); }}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{(['ASSET', 'DEVICE', 'POINT'] as TemplateKind[]).map((kind) => <SelectItem key={kind} value={kind}>{kindLabel(kind)}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
            <label className="grid gap-1.5 text-sm"><span>应用对象</span><Select disabled={!allowed || targets.length === 0} value={assignmentDraft.targetId || undefined} onValueChange={(value) => { setAssignmentDraft((current) => ({ ...current, targetId: value })); setAssignmentDirty(true); }}><SelectTrigger><SelectValue placeholder="选择对象" /></SelectTrigger><SelectContent><SelectGroup>{targets.map((target) => <SelectItem key={target.value} value={target.value}>{target.label}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
            <label className="grid gap-1.5 text-sm"><span>模板版本</span><Select disabled={!allowed || releases.length === 0} value={assignmentDraft.templateRevisionId || undefined} onValueChange={(value) => { const release = releaseById.get(value); setAssignmentDraft((current) => ({ ...current, templateRevisionId: value, targetType: release?.templateKind ?? current.targetType })); setAssignmentDirty(true); }}><SelectTrigger><SelectValue placeholder="选择已发布版本" /></SelectTrigger><SelectContent><SelectGroup>{releases.map((release) => <SelectItem key={release.id} value={release.id}>{release.templateKey} · 第 {release.revisionNumber} 版 · {kindLabel(release.templateKind)}</SelectItem>)}</SelectGroup></SelectContent></Select></label>
            <label className="grid gap-1.5 text-sm"><span>应用原因</span><Textarea disabled={!allowed} rows={2} value={assignmentDraft.reason} onChange={(event) => { setAssignmentDraft((current) => ({ ...current, reason: event.target.value })); setAssignmentDirty(true); }} /></label>
            <Button disabled={!allowed || working || !assignmentDraft.targetId || !assignmentDraft.templateRevisionId || !assignmentDraft.reason.trim()} onClick={() => void assignTemplate()}>{working ? '正在处理…' : '创建新的应用区间'}</Button>

            <div className="border-t pt-4">
              <p className="mb-2 text-xs text-muted-foreground">当前只展示本次会话由服务端返回的模板版本；没有浏览器侧虚构模板目录。</p>
              <div className="space-y-2">
                {releases.length === 0 ? <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">本次会话尚未发布模板版本</div> : releases.map((release) => (
                  <div key={release.id} className="flex items-center justify-between gap-3 rounded-md border p-3">
                    <div><strong className="block text-sm">{release.templateKey}</strong><span className="text-xs text-muted-foreground">第 {release.revisionNumber} 版 · {kindLabel(release.templateKind)}</span></div>
                    <Button variant="ghost" size="sm" onClick={() => setAssignmentDraft((current) => ({ ...current, targetType: release.templateKind, targetId: '', templateRevisionId: release.id, reason: `应用 ${release.templateKey} 第 ${release.revisionNumber} 版` }))}>使用</Button>
                  </div>
                ))}
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader><CardTitle className="flex items-center gap-2"><History className="size-4" />本次会话的应用记录</CardTitle><CardDescription>用于快速复用或回滚到本次会话已知版本。</CardDescription></CardHeader>
        <CardContent className="space-y-2">
          {assignments.length === 0 ? <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">尚无新的模板应用记录</div> : assignments.map((assignment, index) => {
            const release = releaseById.get(assignment.templateRevisionId);
            return (
              <div key={`${assignment.validFrom}-${index}`} className="flex flex-wrap items-center justify-between gap-3 rounded-md border p-3">
                <div><strong className="block text-sm">{targetLabelById.get(assignment.targetId) ?? '已登记对象'}</strong><span className="text-xs text-muted-foreground">{kindLabel(assignment.targetType)} · {release ? `${release.templateKey} 第 ${release.revisionNumber} 版` : '已应用模板版本'} · 生效于 {new Date(assignment.validFrom).toLocaleString()}</span></div>
                <Button variant="outline" size="sm" onClick={() => { setAssignmentDraft({ targetType: assignment.targetType, targetId: assignment.targetId, templateRevisionId: assignment.templateRevisionId, reason: '重新应用已知模板版本' }); setAssignmentDirty(true); }}>再次应用</Button>
              </div>
            );
          })}
        </CardContent>
      </Card>
    </div>
  );
}
