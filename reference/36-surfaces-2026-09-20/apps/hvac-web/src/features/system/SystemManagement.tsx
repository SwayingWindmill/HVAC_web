import { useMemo } from 'react';

import { getRouteApi } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { Building2, LockKeyhole, Plug, ScrollText, Settings, ShieldCheck, UserRound, Wifi } from 'lucide-react';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsMetrics,
  OperationsPanelHeading,
  OperationsSectionIntro,
} from '@/components/OperationsUI';
import { FocusHeading } from '@/app/FocusHeading';
import type { ProtectedScopeDraft } from '@/app/protected-scope';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { DataTable, StatusPillBadge, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import { RegistryAdministration } from './registry-admin/RegistryAdministration';
import { RuleManagement } from './rule-management/RuleManagement';

interface SystemManagementProps {
  snapshot: ShellSnapshot;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}

type PrincipalRow = {
  key: string;
  displayName: string;
  roles: readonly string[];
  policyRevision: string;
};

type SiteRow = {
  key: string;
  displayName: string;
  code: string;
  timezone: string;
  status: string;
  revision: number;
};

const systemRouteApi = getRouteApi('/_app/system');

function EmptyGovernanceState({ description }: { readonly description: string }) {
  return (
    <div className="grid min-h-32 place-items-center rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
      {description}
    </div>
  );
}

function FactGrid({ items }: { readonly items: ReadonlyArray<{ label: string; value: string }> }) {
  return (
    <dl className="grid overflow-hidden rounded-md border sm:grid-cols-2">
      {items.map((item, index) => (
        <div key={item.label} className={`min-w-0 p-3 ${index % 2 === 1 ? 'sm:border-l' : ''} ${index >= 2 ? 'border-t' : ''}`}>
          <dt className="text-xs text-muted-foreground">{item.label}</dt>
          <dd className="mt-1 break-words text-sm font-medium">{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function SystemManagement({ snapshot, registerUnsavedDraft }: SystemManagementProps) {
  const search = systemRouteApi.useSearch();
  const navigate = systemRouteApi.useNavigate();
  const principal = snapshot.principal!;
  const platform = snapshot.platform?.status;
  const sites = snapshot.sites?.items ?? [];
  const activeTab = search.tab ?? 'overview';

  const principalRows = useMemo<PrincipalRow[]>(() => [{
    key: principal.principal.subject,
    displayName: principal.principal.displayName,
    roles: principal.principal.roles,
    policyRevision: principal.authorization.policyRevision,
  }], [principal]);
  const siteRows = useMemo<SiteRow[]>(() => sites.map((site) => ({
    key: site.id,
    displayName: site.displayName,
    code: site.code,
    timezone: site.timezone,
    status: site.status,
    revision: site.revision,
  })), [sites]);

  const principalColumns = useMemo<Array<ColumnDef<DataTableFeatures, PrincipalRow>>>(() => [
    { id: 'user', header: '用户', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.displayName}</span> },
    { id: 'roles', header: '角色', cell: ({ row }) => <div className="flex flex-wrap gap-1">{row.original.roles.map((role) => <Badge key={role} variant="outline">{role}</Badge>)}</div> },
    { id: 'policyRevision', header: '策略修订', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.policyRevision}</span> },
    { id: 'status', header: '状态', cell: () => <StatusPillBadge tone="success" label="当前会话" /> },
  ], []);

  const principalTable = useDataTable({
    key: 'system-management-principals',
    data: principalRows,
    columns: principalColumns,
    paginate: false,
    getRowId: (row) => row.key,
  });

  const siteColumns = useMemo<Array<ColumnDef<DataTableFeatures, SiteRow>>>(() => [
    {
      id: 'site',
      header: '站点',
      cell: ({ row }) => <div><strong className="block text-sm font-medium text-foreground">{row.original.displayName}</strong><span className="text-xs text-muted-foreground">{row.original.code}</span></div>,
    },
    { id: 'timezone', header: '时区', cell: ({ row }) => row.original.timezone },
    { id: 'status', header: '状态', cell: ({ row }) => <StatusPillBadge tone={row.original.status === 'ACTIVE' ? 'success' : 'neutral'} label={row.original.status === 'ACTIVE' ? '启用' : row.original.status} /> },
    { id: 'revision', header: '修订', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.revision}</span> },
  ], []);

  const siteTable = useDataTable({
    key: 'system-management-sites',
    data: siteRows,
    columns: siteColumns,
    paginate: false,
    getRowId: (row) => row.key,
  });

  const overview = (
    <div className="space-y-4">
      <OperationsSectionIntro title="治理概览" icon={<ShieldCheck />} meta="真实会话与平台状态" />
      <OperationsMetrics items={[
        { label: '授权用户', value: 1, detail: principal.principal.displayName, icon: <UserRound />, tone: 'accent' },
        { label: '授权站点', value: sites.length, detail: snapshot.sites?.state ?? 'checking', icon: <Building2 />, tone: sites.length ? 'positive' : 'warning' },
        { label: '授权能力', value: principal.authorization.capabilities.length, detail: `策略修订 ${principal.authorization.policyRevision}`, icon: <LockKeyhole /> },
        { label: '平台状态', value: platform?.status ?? snapshot.platform?.state ?? 'checking', detail: platform?.version ?? '等待状态响应', icon: <Wifi />, tone: platform?.status === 'ok' ? 'positive' : 'warning' },
      ]} />
      <div className="grid gap-4 xl:grid-cols-[1.4fr_1fr]">
        <Card>
          <CardHeader><CardTitle>平台服务</CardTitle></CardHeader>
          <CardContent>
            <FactGrid items={[
              { label: '服务', value: platform?.service ?? '未提供' },
              { label: '状态', value: platform?.status ?? snapshot.platform?.state ?? '检查中' },
              { label: '版本', value: platform?.version ?? '未提供' },
              { label: '实现', value: platform?.implementation ?? '未提供' },
              { label: '路由策略修订', value: platform?.routePolicyRevision == null ? '未提供' : String(platform.routePolicyRevision) },
              { label: '兼容模式', value: platform?.compatibilityMode ?? '未提供' },
            ]} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>当前用户</CardTitle></CardHeader>
          <CardContent>
            <FactGrid items={[
              { label: '显示名称', value: principal.principal.displayName },
              { label: '角色', value: principal.principal.roles.join('、') || '无' },
              { label: '授权能力', value: `${principal.authorization.capabilities.length} 项` },
              { label: '策略修订', value: principal.authorization.policyRevision },
            ]} />
          </CardContent>
        </Card>
      </div>
    </div>
  );

  const users = (
    <div className="space-y-3">
      <Alert>
        <AlertTitle>仅展示服务器确认的当前用户</AlertTitle>
        <AlertDescription>用户目录、创建、禁用与角色变更接口尚未接入，因此不会展示本地模拟用户，也不会提供浏览器侧写操作。</AlertDescription>
      </Alert>
      <Card>
        <CardHeader>
          <OperationsPanelHeading icon={<UserRound />} title="用户与角色" meta={`${principalRows.length} 个当前可验证用户`} />
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap gap-2">
            <Input disabled placeholder="搜索用户或邮箱" className="max-w-64" />
            <div className="flex h-9 min-w-36 items-center rounded-md border bg-muted/30 px-3 text-sm text-muted-foreground">全部角色</div>
          </div>
          <div className="rounded-md border">
            <DataTable
              table={principalTable}
              className="gap-0"
              tableAriaLabel="用户与角色"
              getHeaderCellProps={(header) => ({ className: header.id === 'status' ? 'w-28 text-center' : undefined })}
              getCellProps={(cell) => ({ className: cell.column.id === 'status' ? 'text-center' : undefined })}
            />
          </div>
        </CardContent>
      </Card>
    </div>
  );

  const siteTab = (
    <Card>
      <CardHeader><OperationsPanelHeading icon={<Building2 />} title="站点与租户" meta={`${siteRows.length} 个授权站点`} /></CardHeader>
      <CardContent>
        {siteRows.length > 0 ? (
          <div className="rounded-md border">
            <DataTable
              table={siteTable}
              className="gap-0"
              tableAriaLabel="授权站点"
              getHeaderCellProps={(header) => ({
                className: header.id === 'status' ? 'w-28 text-center' : header.id === 'revision' ? 'text-right' : undefined,
              })}
              getCellProps={(cell) => ({
                className: cell.column.id === 'status' ? 'text-center' : cell.column.id === 'revision' ? 'text-right' : undefined,
              })}
            />
          </div>
        ) : <EmptyGovernanceState description="当前租户没有授权站点" />}
      </CardContent>
    </Card>
  );

  const integrations = (
    <div className="space-y-4">
      <Alert>
        <AlertTitle>数据接入状态来自当前部署</AlertTitle>
        <AlertDescription>不会加载演示数据中预设的 REST、WebSocket、Provider 或 AI 数据源状态。</AlertDescription>
      </Alert>
      <Card>
        <CardHeader><OperationsPanelHeading icon={<Plug />} title="数据接入" meta="当前可验证端点" /></CardHeader>
        <CardContent>
          <FactGrid items={[
            { label: '平台服务', value: platform?.status ?? '检查中' },
            { label: '站点目录', value: snapshot.sites?.state ?? '检查中' },
            { label: '实时数据', value: snapshot.realtime?.state ?? '空闲' },
            { label: '策略修订', value: principal.authorization.policyRevision },
            { label: '受保护范围', value: snapshot.protectedScope?.state ?? '空闲' },
          ]} />
        </CardContent>
      </Card>
    </div>
  );

  const audit = (
    <Card>
      <CardHeader><OperationsPanelHeading icon={<ScrollText />} title="审计日志" meta="服务器审计查询待接入" /></CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap gap-2">
          <Input disabled placeholder="搜索操作人、动作或目标" className="max-w-72" />
          <div className="flex h-9 min-w-36 items-center rounded-md border bg-muted/30 px-3 text-sm text-muted-foreground">全部事件</div>
          <div className="flex h-9 min-w-28 items-center rounded-md border bg-muted/30 px-3 text-sm text-muted-foreground">全部结果</div>
        </div>
        <EmptyGovernanceState description="审计日志接口尚未接入；未使用演示审计记录替代" />
      </CardContent>
    </Card>
  );

  const items = [
    { key: 'overview', label: '治理概览', children: overview },
    { key: 'users', label: '用户与角色', children: users },
    { key: 'site', label: '站点与租户', children: siteTab },
    { key: 'registry', label: 'Registry 管理', children: <RegistryAdministration capabilities={principal.authorization.capabilities} registerUnsavedDraft={registerUnsavedDraft} /> },
    { key: 'integrations', label: '数据接入', children: integrations },
    { key: 'rules', label: '自动化规则', children: <RuleManagement principal={principal} sites={sites} registerUnsavedDraft={registerUnsavedDraft} /> },
    { key: 'audit', label: '审计日志', children: audit },
  ];
  const normalizedActiveTab = items.some((item) => item.key === activeTab) ? activeTab : 'overview';
  const activeContent = items.find((item) => item.key === normalizedActiveTab)?.children ?? overview;
  const handleTabChange = (key: string) => {
    void navigate({
      search: (previous) => ({ ...previous, tab: key === 'overview' ? undefined : key as NonNullable<typeof search.tab> }),
      replace: true,
    });
  };

  return (
    <section data-testid="real-route-system" data-route-state="READY" data-business-state="POPULATED">
      <PageScaffold
        title="系统管理"
        heading={<FocusHeading id="real-system-title" className="ops-page-title"><span className="inline-flex items-center gap-2"><Settings className="size-5" />系统管理</span></FocusHeading>}
        extra={<Badge variant="outline">权威数据</Badge>}
        tabList={items.map(({ key, label }) => ({ key, tab: label }))}
        tabActiveKey={normalizedActiveTab}
        onTabChange={handleTabChange}
      >
        {activeContent}
      </PageScaffold>
    </section>
  );
}
