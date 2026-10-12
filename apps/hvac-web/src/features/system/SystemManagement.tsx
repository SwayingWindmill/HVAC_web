import { useMemo } from 'react';
import { roleLabels } from '@/lib/role-labels';
import { assetsRegistryStatusLabel } from '@/features/assets/model';
import { createIdleRealtimeStatus, realtimeStatusPresentation } from '@/app/realtime-status';
import { PageHeader } from '@/blocks/page-header';

import { getRouteApi } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { DataTableBlock } from '@/blocks/data-table';
import { FactList, FactRow } from '@/blocks/fact-list';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import type { ProtectedScopeDraft } from '@/app/protected-scope';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Status, StatusIndicator } from '@/components/ui/status';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import { AuditLog } from './audit/AuditLog';
import { EmptyGovernanceState } from './EmptyGovernanceState';
import { RegistryAdministration } from './registry-admin/RegistryAdministration';
import { DesignSystem } from './design-system/DesignSystem';
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
    { id: 'status', header: '状态', cell: () => <Status variant="success"><StatusIndicator />当前会话</Status> },
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
    { id: 'status', header: '状态', cell: ({ row }) => <Status variant={row.original.status === 'ACTIVE' ? 'success' : 'default'}><StatusIndicator />{assetsRegistryStatusLabel(row.original.status)}</Status> },
  ], []);

  const siteTable = useDataTable({
    key: 'system-management-sites',
    data: siteRows,
    columns: siteColumns,
    paginate: false,
    getRowId: (row) => row.key,
  });

  const auditViewer = useMemo(() => ({ subject: principal.principal.subject, displayName: principal.principal.displayName }), [principal]);

  const realtimeLabel = realtimeStatusPresentation(snapshot.realtime ?? createIdleRealtimeStatus()).label;
  const directoryState = DIRECTORY_STATE[snapshot.sites?.state ?? 'checking'];
  const overview = (
    <div className="flex flex-col gap-6">
      <MetricGrid ariaLabel="系统概况">
        <MetricCard
          label="平台状态"
          value={<MetricValue value={platformState} />}
          badge={platform ? <Status variant={platform.status === 'ok' ? 'success' : 'warning'}><StatusIndicator />{platform.status === 'ok' ? '运行中' : '需关注'}</Status> : undefined}
          lead={platform?.version ? `版本 ${platform.version}` : '等待状态响应'}
          detail="网关、身份与业务服务"
        />
        <MetricCard
          label="授权站点"
          value={<MetricValue value={String(sites.length)} unit="个" />}
          lead={sites.map((site) => site.displayName).join('、') || '当前没有授权站点'}
          detail={`站点目录${directoryState}`}
        />
        <MetricCard
          label="授权能力"
          value={<MetricValue value={String(principal.authorization.capabilities.length)} unit="项" />}
          lead="当前会话可执行的操作"
          detail={`角色：${roleLabels(principal.principal.roles).join('、')}`}
        />
        <MetricCard
          label="实时数据"
          value={<MetricValue value={realtimeLabel} />}
          lead="站点设备读数的推送通道"
          detail="打开站点页面后建立连接"
        />
      </MetricGrid>
      <div className="grid gap-6 @5xl/main:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>当前用户</CardTitle>
            <CardDescription>本次登录由身份服务确认</CardDescription>
          </CardHeader>
          <CardContent>
            <FactList>
              <FactRow label="显示名称">{principal.principal.displayName}</FactRow>
              <FactRow label="角色">{roleLabels(principal.principal.roles).join('、')}</FactRow>
              <FactRow label="授权能力">{principal.authorization.capabilities.length} 项</FactRow>
            </FactList>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>平台服务</CardTitle>
            <CardDescription>当前部署对外提供的服务状态</CardDescription>
          </CardHeader>
          <CardContent>
            <FactList>
              <FactRow label="平台">{platformState}</FactRow>
              <FactRow label="版本">{platform?.version ?? '未提供'}</FactRow>
              <FactRow label="站点目录">{directoryState}</FactRow>
            </FactList>
          </CardContent>
        </Card>
      </div>
    </div>
  );

  const users = (
    <DataTableBlock title="用户与角色" description="用户目录接入前，只列出当前登录的用户">
      <DataTable
        table={principalTable}
        tableAriaLabel="用户与角色"
        getHeaderCellProps={(header) => ({ className: header.id === 'status' ? 'w-28' : undefined })}
      />
    </DataTableBlock>
  );

  const siteTab = (
    <DataTableBlock title="站点与租户" description={`${siteRows.length} 个授权站点`}>
      {siteRows.length > 0 ? (
        <DataTable
          table={siteTable}
          tableAriaLabel="授权站点"
          getHeaderCellProps={(header) => ({ className: header.id === 'status' ? 'w-28' : undefined })}
        />
      ) : <EmptyGovernanceState description="当前租户没有授权站点" />}
    </DataTableBlock>
  );

  const integrations = (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>数据接入</CardTitle>
        <CardDescription>平台到站点数据的各段连接</CardDescription>
      </CardHeader>
      <CardContent>
        <FactList>
          <FactRow label="平台服务">{platformState}</FactRow>
          <FactRow label="站点目录">{directoryState}</FactRow>
          <FactRow label="实时数据">{realtimeLabel}</FactRow>
        </FactList>
      </CardContent>
    </Card>
  );

  const audit = <AuditLog viewer={auditViewer} canRead={principal.authorization.capabilities.includes('audit.read')} active={activeTab === 'audit'} />;

  const items = [
    { key: 'overview', label: '系统概览', children: overview },
    { key: 'users', label: '用户与角色', children: users },
    { key: 'site', label: '站点与租户', children: siteTab },
    { key: 'registry', label: '资产与点位', children: <RegistryAdministration capabilities={principal.authorization.capabilities} registerUnsavedDraft={registerUnsavedDraft} /> },
    { key: 'integrations', label: '数据接入', children: integrations },
    { key: 'rules', label: '自动化规则', children: <RuleManagement principal={principal} sites={sites} registerUnsavedDraft={registerUnsavedDraft} /> },
    { key: 'audit', label: '审计日志', children: audit },
    { key: 'design', label: '设计系统', children: <DesignSystem /> },
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
      <Main>
        <PageHeader title="系统管理" description="平台服务、用户与权限、站点、数据接入与审计" />
        <Tabs value={normalizedActiveTab} onValueChange={handleTabChange} className="gap-6">
          <div className="border-b">
            <TabsList variant="line" className="-mb-px h-10 gap-6 px-0">
              {items.map((item) => <TabsTrigger key={item.key} value={item.key} className="flex-none px-0">{item.label}</TabsTrigger>)}
            </TabsList>
          </div>
          {items.map((item) => <TabsContent key={item.key} value={item.key}>{item.children}</TabsContent>)}
        </Tabs>
      </Main>
    </section>
  );
}
