import { useMemo } from 'react';
import { roleLabels } from '@/lib/role-labels';
import { assetsRegistryStatusLabel } from '@/features/assets/model';
import { createIdleRealtimeStatus, realtimeStatusPresentation } from '@/app/realtime-status';
import { PageHeader } from '@/blocks/page-header';

import { getRouteApi } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { Building2, LockKeyhole, Plug, UserRound, Wifi } from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { FactStrip } from '@/blocks/fact-strip';
import type { ProtectedScopeDraft } from '@/app/protected-scope';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';
import { AuditLog } from './audit/AuditLog';
import { EmptyGovernanceState } from './EmptyGovernanceState';
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
};

type SiteRow = {
  key: string;
  displayName: string;
  code: string;
  timezone: string;
  status: string;
};

const systemRouteApi = getRouteApi('/_app/system');


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

const PLATFORM_STATE = { checking: '检查中', available: '正常', degraded: '部分降级', unavailable: '不可用' } as const;
const DIRECTORY_STATE = { checking: '读取中', available: '已同步', forbidden: '无权读取', unavailable: '暂不可用' } as const;

/** The timezone's own name, e.g. 中国标准时间, instead of its IANA identifier. */
function timezoneName(timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', { timeZone, timeZoneName: 'long' }).formatToParts(new Date()).find((part) => part.type === 'timeZoneName')?.value ?? timeZone;
}

export function SystemManagement({ snapshot, registerUnsavedDraft }: SystemManagementProps) {
  const search = systemRouteApi.useSearch();
  const navigate = systemRouteApi.useNavigate();
  const principal = snapshot.principal!;
  const platform = snapshot.platform?.status;
  const sites = snapshot.sites?.items ?? [];
  const activeTab = search.tab ?? 'overview';
  const platformState = platform ? (platform.status === 'ok' ? '正常' : '部分降级') : PLATFORM_STATE[snapshot.platform?.state ?? 'checking'];

  const principalRows = useMemo<PrincipalRow[]>(() => [{
    key: principal.principal.subject,
    displayName: principal.principal.displayName,
    roles: principal.principal.roles,
  }], [principal]);
  const siteRows = useMemo<SiteRow[]>(() => sites.map((site) => ({
    key: site.id,
    displayName: site.displayName,
    code: site.code,
    timezone: site.timezone,
    status: site.status,
  })), [sites]);

  const principalColumns = useMemo<Array<ColumnDef<DataTableFeatures, PrincipalRow>>>(() => [
    { id: 'user', header: '用户', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.displayName}</span> },
    { id: 'roles', header: '角色', cell: ({ row }) => <div className="flex flex-wrap gap-1">{roleLabels(row.original.roles).map((label) => <Badge key={label} variant="outline">{label}</Badge>)}</div> },
    { id: 'status', header: '状态', cell: () => <StatusBadge tone="success" label="当前会话" /> },
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
    { id: 'timezone', header: '时区', cell: ({ row }) => timezoneName(row.original.timezone) },
    { id: 'status', header: '状态', cell: ({ row }) => <StatusBadge tone={row.original.status === 'ACTIVE' ? 'success' : 'neutral'} label={assetsRegistryStatusLabel(row.original.status)} /> },
  ], []);

  const siteTable = useDataTable({
    key: 'system-management-sites',
    data: siteRows,
    columns: siteColumns,
    paginate: false,
    getRowId: (row) => row.key,
  });

  const auditViewer = useMemo(() => ({ subject: principal.principal.subject, displayName: principal.principal.displayName }), [principal]);

  const overview = (
    <div className="space-y-4">
      <FactStrip items={[
        { label: '授权用户', value: 1, detail: principal.principal.displayName, icon: <UserRound />, tone: 'accent' },
        { label: '授权站点', value: sites.length, detail: DIRECTORY_STATE[snapshot.sites?.state ?? 'checking'], icon: <Building2 />, tone: sites.length ? 'positive' : 'warning' },
        { label: '授权能力', value: principal.authorization.capabilities.length, detail: '当前会话可用的操作', icon: <LockKeyhole /> },
        { label: '平台状态', value: platformState, detail: platform?.version ? `版本 ${platform.version}` : '等待状态响应', icon: <Wifi />, tone: platform?.status === 'ok' ? 'positive' : 'warning' },
      ]} />
      <div className="grid gap-4 xl:grid-cols-[1.4fr_1fr]">
        <Card>
          <CardHeader><CardTitle>平台服务</CardTitle></CardHeader>
          <CardContent>
            <FactGrid items={[
              { label: '状态', value: platformState },
              { label: '版本', value: platform?.version ?? '未提供' },
            ]} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>当前用户</CardTitle></CardHeader>
          <CardContent>
            <FactGrid items={[
              { label: '显示名称', value: principal.principal.displayName },
              { label: '角色', value: roleLabels(principal.principal.roles).join('、') },
              { label: '授权能力', value: `${principal.authorization.capabilities.length} 项` },
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
      <DataTableBlock
        title={<span className="flex items-center gap-2"><UserRound className="size-4" />用户与角色</span>}
        description={`${principalRows.length} 个当前可验证用户`}
      >
        <DataTable
          table={principalTable}
          tableAriaLabel="用户与角色"
          getHeaderCellProps={(header) => ({ className: header.id === 'status' ? 'w-28 text-center' : undefined })}
          getCellProps={(cell) => ({ className: cell.column.id === 'status' ? 'text-center' : undefined })}
        >
          <div className="flex flex-wrap gap-2">
            <Input disabled placeholder="搜索用户或邮箱" className="max-w-64" />
            <div className="flex h-9 min-w-36 items-center rounded-md border bg-muted/30 px-3 text-sm text-muted-foreground">全部角色</div>
          </div>
        </DataTable>
      </DataTableBlock>
    </div>
  );

  const siteTab = (
    <DataTableBlock
      title={<span className="flex items-center gap-2"><Building2 className="size-4" />站点与租户</span>}
      description={`${siteRows.length} 个授权站点`}
    >
      {siteRows.length > 0 ? (
        <DataTable
          table={siteTable}
          tableAriaLabel="授权站点"
          getHeaderCellProps={(header) => ({
            className: header.id === 'status' ? 'w-28 text-center' : undefined,
          })}
          getCellProps={(cell) => ({
            className: cell.column.id === 'status' ? 'text-center' : undefined,
          })}
        />
      ) : <EmptyGovernanceState description="当前租户没有授权站点" />}
    </DataTableBlock>
  );

  const integrations = (
    <div className="space-y-4">
      <Alert>
        <AlertTitle>数据接入状态来自当前部署</AlertTitle>
        <AlertDescription>不会加载演示数据中预设的 REST、WebSocket、Provider 或 AI 数据源状态。</AlertDescription>
      </Alert>
      <Card>
        <CardHeader><CardTitle className="flex flex-wrap items-center gap-2"><Plug className="size-4" />数据接入 <span className="text-sm font-normal text-muted-foreground">当前可验证端点</span></CardTitle></CardHeader>
        <CardContent>
          <FactGrid items={[
            { label: '平台服务', value: platformState },
            { label: '站点目录', value: DIRECTORY_STATE[snapshot.sites?.state ?? 'checking'] },
            { label: '实时数据', value: realtimeStatusPresentation(snapshot.realtime ?? createIdleRealtimeStatus()).label },
          ]} />
        </CardContent>
      </Card>
    </div>
  );

  const audit = <AuditLog viewer={auditViewer} canRead={principal.authorization.capabilities.includes('audit.read')} active={activeTab === 'audit'} />;

  const items = [
    { key: 'overview', label: '系统概览', children: overview },
    { key: 'users', label: '用户与角色', children: users },
    { key: 'site', label: '站点与租户', children: siteTab },
    { key: 'registry', label: 'Registry 管理', children: <RegistryAdministration capabilities={principal.authorization.capabilities} registerUnsavedDraft={registerUnsavedDraft} /> },
    { key: 'integrations', label: '数据接入', children: integrations },
    { key: 'rules', label: '自动化规则', children: <RuleManagement principal={principal} sites={sites} registerUnsavedDraft={registerUnsavedDraft} /> },
    { key: 'audit', label: '审计日志', children: audit },
  ];
  const normalizedActiveTab = items.some((item) => item.key === activeTab) ? activeTab : 'overview';
  const handleTabChange = (key: string) => {
    void navigate({
      search: (previous) => ({ ...previous, tab: key === 'overview' ? undefined : key as NonNullable<typeof search.tab> }),
      replace: true,
    });
  };

  return (
    <section data-testid="real-route-system" data-route-state="READY" data-business-state="POPULATED">
      <Main className="space-y-4">
        <PageHeader title="系统管理" description="平台服务、用户与权限、站点、数据接入与审计。" />
        <Tabs value={normalizedActiveTab} onValueChange={handleTabChange}>
          <TabsList className="max-w-full overflow-x-auto">
            {items.map((item) => <TabsTrigger key={item.key} value={item.key}>{item.label}</TabsTrigger>)}
          </TabsList>
          {items.map((item) => <TabsContent key={item.key} value={item.key}>{item.children}</TabsContent>)}
        </Tabs>
      </Main>
    </section>
  );
}
