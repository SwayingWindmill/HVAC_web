import { useCallback, useEffect, useMemo, useState } from 'react';
import { Database, FileUp, LayoutTemplate, Network, Plus } from 'lucide-react';
import type { Capability } from '@/api/generated/platformGateway.gen';
import {
  flattenRegistryPages,
  presentRegistryError,
  registryAdminApi,
  useRegistryAssetModel,
  useRegistrySite,
  useRegistrySites,
} from '@/api/registry';
import type { ProtectedScopeDraft } from '@/app/protected-scope';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { RegistryImportExportWorkbench } from './RegistryImportExportWorkbench';
import { RegistryResourceWorkbench } from './RegistryResourceWorkbench';
import { RegistryTemplateWorkbench } from './RegistryTemplateWorkbench';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';
import { confirmDiscardRegistryDraft, useRegistryDirtyGuard } from './useDirtyGuard';

interface Props {
  capabilities: readonly Capability[];
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}

type RegistryTab = 'resources' | 'templates' | 'import-export';

export function RegistryAdministration({ capabilities: capabilityList, registerUnsavedDraft }: Props) {
  const capabilities = useMemo(() => new Set(capabilityList), [capabilityList]);
  const sitesQuery = useRegistrySites(capabilities.has('site.list'));
  const sites = flattenRegistryPages(sitesQuery.data);
  const [selectedSiteId, setSelectedSiteId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<RegistryTab>('resources');
  const [dirty, setDirty] = useState(false);
  const [creatingSite, setCreatingSite] = useState(false);
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [createDraft, setCreateDraft] = useState({ code: '', displayName: '', timezone: 'Asia/Shanghai', reason: '创建首个站点' });
  const siteQuery = useRegistrySite(selectedSiteId);
  const modelQuery = useRegistryAssetModel(selectedSiteId);

  useRegistryDirtyGuard(dirty);

  useEffect(() => {
    if (!selectedSiteId && sites.length) setSelectedSiteId(sites[0].id);
    if (selectedSiteId && sites.length && !sites.some((site) => site.id === selectedSiteId)) setSelectedSiteId(sites[0].id);
  }, [selectedSiteId, sites]);

  useEffect(() => registerUnsavedDraft({
    id: 'registry-administration-draft',
    label: selectedSiteId ? '站点与设备登记管理' : '登记管理',
    isDirty: () => dirty,
  }), [dirty, registerUnsavedDraft, selectedSiteId]);

  const refresh = useCallback(async () => {
    await Promise.all([
      sitesQuery.refetch(),
      selectedSiteId ? siteQuery.refetch() : Promise.resolve(),
      selectedSiteId ? modelQuery.refetch() : Promise.resolve(),
    ]);
  }, [modelQuery, selectedSiteId, siteQuery, sitesQuery]);

  const changeSite = (siteId: string) => {
    if (!confirmDiscardRegistryDraft(dirty)) return;
    setDirty(false);
    setSelectedSiteId(siteId);
  };

  const changeTab = (key: RegistryTab) => {
    if (!confirmDiscardRegistryDraft(dirty)) return;
    setDirty(false);
    setActiveTab(key);
  };

  const createFirstSite = async () => {
    if (!createDraft.code.trim() || !createDraft.displayName.trim() || !createDraft.timezone.trim() || !createDraft.reason.trim()) return;
    setCreatingSite(true);
    setMutationError(null);
    try {
      const created = await registryAdminApi.createSite({
        code: createDraft.code.trim(),
        displayName: createDraft.displayName.trim(),
        timezone: createDraft.timezone.trim(),
        status: 'ACTIVE',
        meta: makeRegistryMutationMeta(0, createDraft.reason.trim(), newRegistryIdempotencyKey('site-create')),
      });
      await sitesQuery.refetch();
      setDirty(false);
      setCreateDraft({ code: '', displayName: '', timezone: 'Asia/Shanghai', reason: '创建首个站点' });
      setSelectedSiteId(created.id);
    } catch (reason) {
      setMutationError(reason);
    } finally {
      setCreatingSite(false);
    }
  };

  const site = siteQuery.data;
  const model = modelQuery.data;
  const loading = sitesQuery.isLoading || (Boolean(selectedSiteId) && (siteQuery.isLoading || modelQuery.isLoading));
  const error = mutationError ?? sitesQuery.error ?? siteQuery.error ?? modelQuery.error;
  const presentedError = error ? presentRegistryError(error) : null;

  if (loading && !site && !model) {
    return <Card><CardContent className="grid min-h-32 place-items-center text-sm text-muted-foreground">正在加载登记信息…</CardContent></Card>;
  }

  const tabs: ReadonlyArray<{ key: RegistryTab; label: string; icon: React.ReactNode }> = [
    { key: 'resources', label: '资源与绑定', icon: <Network /> },
    { key: 'templates', label: '模板版本', icon: <LayoutTemplate /> },
    { key: 'import-export', label: '导入与导出', icon: <FileUp /> },
  ];

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><Database className="size-4" />登记管理范围</CardTitle>
          <CardDescription>选择站点后管理空间、设备、测点、模板与受控导入导出。</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-3">
          <Select value={selectedSiteId ?? undefined} onValueChange={changeSite} disabled={sites.length === 0}>
            <SelectTrigger className="min-w-72" aria-label="选择管理站点"><SelectValue placeholder="选择站点" /></SelectTrigger>
            <SelectContent>{sites.map((value) => <SelectItem key={value.id} value={value.id}>{value.displayName} · {value.code}</SelectItem>)}</SelectContent>
          </Select>
          <Badge variant="outline">正式登记数据</Badge>
          {dirty ? <Badge variant="outline" className="border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">有未保存修改</Badge> : null}
        </CardContent>
      </Card>

      {presentedError ? <Alert variant="destructive"><AlertTitle>{presentedError.title}</AlertTitle><AlertDescription>{presentedError.description}</AlertDescription></Alert> : null}

      {!selectedSiteId || !site || !model ? (
        sites.length === 0 && capabilities.has('site.list') && capabilities.has('site.write') ? (
          <Card>
            <CardHeader><CardTitle className="flex items-center gap-2"><Plus className="size-4" />创建首个站点</CardTitle><CardDescription>站点创建由服务端登记系统确认并写入。</CardDescription></CardHeader>
            <CardContent className="space-y-4">
              <Alert><AlertTitle>当前还没有可管理站点</AlertTitle><AlertDescription>填写正式站点信息后提交；浏览器不会生成本地模拟站点。</AlertDescription></Alert>
              <div className="grid gap-4 md:grid-cols-2">
                <label className="grid gap-1.5 text-sm"><span>站点编码</span><Input value={createDraft.code} placeholder="central-plant" onChange={(event) => { setCreateDraft((current) => ({ ...current, code: event.target.value })); setDirty(true); }} /></label>
                <label className="grid gap-1.5 text-sm"><span>站点名称</span><Input value={createDraft.displayName} onChange={(event) => { setCreateDraft((current) => ({ ...current, displayName: event.target.value })); setDirty(true); }} /></label>
                <label className="grid gap-1.5 text-sm"><span>时区</span><Input value={createDraft.timezone} onChange={(event) => { setCreateDraft((current) => ({ ...current, timezone: event.target.value })); setDirty(true); }} /></label>
                <label className="grid gap-1.5 text-sm md:col-span-2"><span>创建原因</span><Textarea rows={2} value={createDraft.reason} onChange={(event) => { setCreateDraft((current) => ({ ...current, reason: event.target.value })); setDirty(true); }} /></label>
              </div>
              <Button disabled={creatingSite || !createDraft.code.trim() || !createDraft.displayName.trim() || !createDraft.timezone.trim() || !createDraft.reason.trim()} onClick={() => void createFirstSite()}>{creatingSite ? '正在创建…' : '创建站点'}</Button>
            </CardContent>
          </Card>
        ) : <div className="grid min-h-32 place-items-center rounded-md border border-dashed p-6 text-sm text-muted-foreground">当前没有可管理站点，或登记服务暂时不可用。</div>
      ) : (
        <div className="space-y-4">
          <div className="inline-flex flex-wrap rounded-md border bg-muted/30 p-1" role="tablist" aria-label="登记管理工作区">
            {tabs.map((tab) => <Button key={tab.key} size="sm" variant={activeTab === tab.key ? 'secondary' : 'ghost'} role="tab" aria-selected={activeTab === tab.key} onClick={() => changeTab(tab.key)}>{tab.icon}{tab.label}</Button>)}
          </div>
          {activeTab === 'resources' ? <RegistryResourceWorkbench site={site} model={model} capabilities={capabilities} onDirtyChange={setDirty} onRefresh={refresh} onSiteCreated={(siteId) => setSelectedSiteId(siteId)} /> : null}
          {activeTab === 'templates' ? <RegistryTemplateWorkbench site={site} model={model} capabilities={capabilities} onDirtyChange={setDirty} /> : null}
          {activeTab === 'import-export' ? <RegistryImportExportWorkbench site={site} model={model} capabilities={capabilities} onDirtyChange={setDirty} onRefresh={refresh} /> : null}
        </div>
      )}
    </div>
  );
}
