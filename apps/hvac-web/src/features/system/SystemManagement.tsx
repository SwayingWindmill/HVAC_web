import { useMemo, type ReactNode } from 'react';
import { useQueries } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { Check, ChevronRight } from 'lucide-react';
import { registryAssetModelQuery } from '@/api/registry';
import { createIdleRealtimeStatus, realtimeStatusPresentation } from '@/app/realtime-status';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { Main } from '@/components/layout/Main';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { CAPABILITY_GROUPS, capabilityGroups } from '@/lib/capability-labels';
import { CLIMATE_ZONE_LABEL } from '@/lib/climate-zone';
import { formatDecimal } from '@/lib/operator-format';
import { roleLabels } from '@/lib/role-labels';
import { cn } from '@/lib/utils';
import { AssistantUsage } from './assistant-usage/AssistantUsage';
import { AuditLog } from './audit/AuditLog';
import { RegistryAdministration } from './registry-admin/RegistryAdministration';
import { RuleManagement } from './rule-management/RuleManagement';
import type { RegisterUnsavedDraft } from './unsaved-drafts';
import { UserAdministration } from './users/UserAdministration';

const systemRouteApi = getRouteApi('/_app/system');

type Health = 'good' | 'warn' | 'idle';
const HEALTH: Record<Health, { readonly chip: string; readonly dot: string }> = {
  good: { chip: 'bg-success-soft text-success-text', dot: 'bg-success' },
  warn: { chip: 'bg-warning-soft text-warning-text', dot: 'bg-warning' },
  idle: { chip: 'bg-secondary text-secondary-foreground', dot: 'bg-subtle-foreground' },
};

const PLATFORM_STATE = { checking: '检查中', available: '正常', degraded: '部分降级', unavailable: '不可用' } as const;
const DIRECTORY_STATE = { checking: '读取中', available: '已同步', forbidden: '无权读取', unavailable: '暂时读不到' } as const;
const SITE_STATUS = { ACTIVE: '已启用', INACTIVE: '已停用', RETIRED: '已退役' } as const;

function StateChip({ health, children }: { readonly health: Health; readonly children: ReactNode }) {
  return (
    <span className={cn('inline-flex h-5.5 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap', HEALTH[health].chip)}>
      <span className={cn('size-1.5 rounded-full', HEALTH[health].dot)} aria-hidden="true" />
      {children}
    </span>
  );
}

function Figure({ label, value, unit, children }: { readonly label: string; readonly value: ReactNode; readonly unit?: string; readonly children?: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-5 py-4">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <span className="flex items-center text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
        {value}
        {unit ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
      </span>
      {children ? <span className="truncate text-xs text-muted-foreground">{children}</span> : null}
    </div>
  );
}

function Avatar({ name, className }: { readonly name: string; readonly className?: string }) {
  return <span className={cn('flex size-8 shrink-0 items-center justify-center rounded-full bg-primary-soft text-xs font-semibold text-primary', className)} aria-hidden="true">{name.slice(0, 1)}</span>;
}

/** The offset and the zone's own name, e.g. UTC+8 中国标准时间, instead of its IANA identifier. */
function timezoneLabel(timeZone: string): string {
  const parts = (style: 'long' | 'shortOffset') => new Intl.DateTimeFormat(style === 'long' ? 'zh-CN' : 'en-US', { timeZone, timeZoneName: style }).formatToParts(new Date()).find((part) => part.type === 'timeZoneName')?.value;
  const offset = parts('shortOffset')?.replace('GMT', 'UTC') ?? '';
  return `${offset === 'UTC' ? 'UTC+0' : offset} ${parts('long') ?? timeZone}`.trim();
}

const clock = (value: string) => new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));

export function SystemManagement({ snapshot, registerUnsavedDraft }: { readonly snapshot: ShellSnapshot; readonly registerUnsavedDraft: RegisterUnsavedDraft }) {
  const search = systemRouteApi.useSearch();
  const navigate = systemRouteApi.useNavigate();
  const principal = snapshot.principal!;
  const platform = snapshot.platform?.status;
  const sites = useMemo(() => snapshot.sites?.items ?? [], [snapshot.sites?.items]);
  const activeTab = search.tab ?? 'overview';
  const platformState = platform ? (platform.status === 'ok' ? '正常' : '部分降级') : PLATFORM_STATE[snapshot.platform?.state ?? 'checking'];
  const platformHealth: Health = platform ? (platform.status === 'ok' ? 'good' : 'warn') : snapshot.platform?.state === 'unavailable' ? 'warn' : 'idle';
  const directoryState = DIRECTORY_STATE[snapshot.sites?.state ?? 'checking'];
  const realtime = realtimeStatusPresentation(snapshot.realtime ?? createIdleRealtimeStatus());
  const capabilities = principal.authorization.capabilities;
  const roles = roleLabels(principal.principal.roles).join('、');

  // Each Site's registered devices, for the gateway and device counts.
  const models = useQueries({ queries: sites.map((site) => registryAssetModelQuery(site.id, capabilities.includes('device.list'))) });
  const modelOf = new Map(sites.map((site, index) => [site.id, models[index]?.data]));
  const countsReady = models.length > 0 && models.every((model) => model.isSuccess);
  const gatewaysOf = (siteId: string) => modelOf.get(siteId)?.devices.filter((device) => device.deviceType === 'GATEWAY' && device.status === 'ACTIVE').length ?? null;
  const devicesOf = (siteId: string) => modelOf.get(siteId)?.devices.filter((device) => device.deviceType !== 'GATEWAY' && device.status === 'ACTIVE').length ?? null;
  const total = (count: (siteId: string) => number | null) => (countsReady ? sites.reduce((sum, site) => sum + (count(site.id) ?? 0), 0) : null);
  const integrationsLink = <Link to="/settings/integrations" className="text-primary hover:underline">集成管理 ›</Link>;

  const overview = (
    <div className="flex flex-col gap-[18px]">
      <Panel aria-label="系统概况" className="grid grid-cols-4 divide-x divide-divider">
        <Figure label="平台状态" value={<StateChip health={platformHealth}>{platformState}</StateChip>}>
          {platform?.version ? `版本 ${platform.version}` : '等待状态响应'}
        </Figure>
        <Figure label="授权站点" value={String(sites.length)} unit="个">{sites.map((site) => site.displayName).join('、') || '当前没有授权站点'}</Figure>
        <Figure label="现场网关" value={total(gatewaysOf) === null ? '—' : String(total(gatewaysOf))} unit="个">{integrationsLink}</Figure>
        <Figure label="我的权限" value={String(capabilities.length)} unit="项">{roles}</Figure>
      </Panel>
      <div className="grid items-start gap-[18px] grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)]">
        <Panel aria-label="平台服务">
          <PanelHeader title="平台服务" caption={platform?.checkedAt ? `检查于 ${clock(platform.checkedAt)}` : undefined} />
          <Table aria-label="平台服务">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">服务</TableHead>
                <TableHead>作用</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="pr-5">版本</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow className="hover:bg-transparent">
                <TableCell className="pl-5 font-medium">平台网关</TableCell>
                <TableCell className="text-muted-foreground">页面与接口的统一入口</TableCell>
                <TableCell><StateChip health={platformHealth}>{platformState}</StateChip></TableCell>
                <TableCell className="pr-5 font-mono text-xs">{platform?.version ?? '—'}</TableCell>
              </TableRow>
              <TableRow className="hover:bg-transparent">
                <TableCell className="pl-5 font-medium">站点目录</TableCell>
                <TableCell className="text-muted-foreground">授权站点与组织</TableCell>
                <TableCell><StateChip health={snapshot.sites?.state === 'available' ? 'good' : snapshot.sites?.state === 'checking' ? 'idle' : 'warn'}>{directoryState}</StateChip></TableCell>
                <TableCell className="pr-5 text-muted-foreground">—</TableCell>
              </TableRow>
              <TableRow className="hover:bg-transparent">
                <TableCell className="pl-5 font-medium">实时推送</TableCell>
                <TableCell className="text-muted-foreground">站点设备读数的推送通道</TableCell>
                <TableCell><StateChip health={realtime.code === 'CONNECTED' ? 'good' : realtime.code === 'DISCONNECTED' ? 'idle' : 'warn'}>{realtime.label}</StateChip></TableCell>
                <TableCell className="pr-5 text-muted-foreground">—</TableCell>
              </TableRow>
            </TableBody>
          </Table>
          <p className="border-t border-divider px-5 py-2.5 text-xs text-muted-foreground">现场到页面的完整数据链路见 {integrationsLink}</p>
        </Panel>
        <Panel aria-label="当前登录">
          <PanelHeader title="当前登录" />
          <div className="flex flex-col gap-4 px-5 py-4">
            <span className="flex items-center gap-3">
              <Avatar name={principal.principal.displayName} className="size-10 text-sm" />
              <span className="flex min-w-0 flex-col">
                <span className="text-sm font-semibold">{principal.principal.displayName}</span>
                <span className="truncate text-xs text-muted-foreground">{principal.principal.email}</span>
              </span>
            </span>
            <dl className="grid grid-cols-2 gap-x-5 gap-y-3">
              <div><dt className="text-xs text-muted-foreground">角色</dt><dd className="text-sm font-medium">{roles}</dd></div>
              <div><dt className="text-xs text-muted-foreground">授权能力</dt><dd className="text-sm font-medium">{capabilities.length} 项</dd></div>
              <div><dt className="text-xs text-muted-foreground">会话到期</dt><dd className="font-mono text-sm font-medium">{clock(principal.session.expiresAt)}</dd></div>
              <div><dt className="text-xs text-muted-foreground">闲置自动退出</dt><dd className="text-sm font-medium">{formatDecimal(principal.session.idleTimeoutMs / 60_000, 0)} 分钟</dd></div>
            </dl>
          </div>
        </Panel>
      </div>
    </div>
  );

  const { groups, unnamed } = capabilityGroups(capabilities);
  const selfView = (
    <div className="flex flex-col gap-[18px]">
      <Panel aria-label="用户">
        <PanelHeader title="用户" />
        <Table aria-label="用户与角色">
          <TableHeader>
            <TableRow>
              <TableHead className="pl-5">用户</TableHead>
              <TableHead>角色</TableHead>
              <TableHead>站点范围</TableHead>
              <TableHead className="pr-5">状态</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow className="hover:bg-transparent">
              <TableCell className="pl-5">
                <span className="flex items-center gap-3">
                  <Avatar name={principal.principal.displayName} />
                  <span className="flex flex-col">
                    <span className="font-semibold">{principal.principal.displayName}</span>
                    <span className="text-xs text-muted-foreground">{principal.principal.email}</span>
                  </span>
                </span>
              </TableCell>
              <TableCell>{roles}</TableCell>
              <TableCell>{sites.length ? `${sites.length} 个授权站点` : '没有授权站点'}</TableCell>
              <TableCell className="pr-5"><StateChip health="good">当前登录</StateChip></TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </Panel>
      <Panel aria-label={`${roles} 的权限`}>
        <PanelHeader title={`${roles} 的权限`} caption={`当前登录用户的角色${unnamed ? ` · 另有 ${unnamed} 项未命名的权限` : ''}`} />
        <div className="grid grid-cols-3 divide-x divide-divider">
          {CAPABILITY_GROUPS.map((group) => (
            <div key={group} className="flex flex-col gap-2 px-5 py-4">
              <span className="flex items-center gap-2 text-sm font-semibold">{group}<span className="font-mono text-xs font-normal text-subtle-foreground">{groups.get(group)!.length}</span></span>
              {groups.get(group)!.length === 0 ? <span className="text-xs text-muted-foreground">无</span> : groups.get(group)!.map((label) => (
                <span key={label} className="flex items-center gap-2 text-sm"><Check className="size-3.5 text-success" aria-hidden="true" />{label}</span>
              ))}
            </div>
          ))}
        </div>
      </Panel>
    </div>
  );

  // Administrators manage everyone; others see their own account and what it may do.
  const users = capabilities.includes('iam.admin')
    ? <UserAdministration csrfToken={principal.session.csrfToken} sites={sites} />
    : selfView;

  const siteTab = (
    <div className="flex flex-col gap-[18px]">
      <Panel aria-label="站点概况" className="grid grid-cols-4 divide-x divide-divider">
        <Figure label="站点" value={String(sites.length)} unit="个">{sites.length ? `${sites.filter((site) => site.status === 'ACTIVE').length} 个已启用` : '当前没有授权站点'}</Figure>
        <Figure label="现场网关" value={total(gatewaysOf) === null ? '—' : String(total(gatewaysOf))} unit="个">{integrationsLink}</Figure>
        <Figure label="设备" value={total(devicesOf) === null ? '—' : String(total(devicesOf))} unit="台">{sites.length > 1 ? `${sites.length} 个站点合计` : '已登记的现场设备'}</Figure>
        <Figure label="站点目录" value={<StateChip health={snapshot.sites?.state === 'available' ? 'good' : 'warn'}>{directoryState}</StateChip>}>授权站点由身份服务确定</Figure>
      </Panel>
      <Panel aria-label="站点">
        <PanelHeader title="站点" caption="名称、时区、建筑面积和气候区在「资产与点位」里维护" />
        {sites.length === 0 ? <p className="px-5 py-10 text-center text-sm text-muted-foreground">当前组织没有授权站点</p> : (
          <Table aria-label="授权站点">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">站点</TableHead>
                <TableHead>时区</TableHead>
                <TableHead className="text-right">建筑面积</TableHead>
                <TableHead>气候区</TableHead>
                <TableHead className="text-right">设备</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="pr-5"><span className="sr-only">维护</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {sites.map((site) => (
                <TableRow key={site.id} className="hover:bg-transparent">
                  <TableCell className="pl-5">
                    <span className="flex items-center gap-3">
                      <Avatar name={site.displayName} />
                      <span className="flex flex-col">
                        <span className="font-semibold">{site.displayName}</span>
                        <span className="font-mono text-xs text-muted-foreground">{site.code}</span>
                      </span>
                    </span>
                  </TableCell>
                  <TableCell>{timezoneLabel(site.timezone)}</TableCell>
                  <TableCell className="text-right tabular-nums">{site.floorAreaM2 === null ? <span className="text-muted-foreground">未登记</span> : `${formatDecimal(site.floorAreaM2, 0)} m²`}</TableCell>
                  <TableCell>{site.climateZone ? CLIMATE_ZONE_LABEL[site.climateZone] : <span className="text-muted-foreground">未登记</span>}</TableCell>
                  <TableCell className="text-right tabular-nums">{devicesOf(site.id) === null ? '—' : `${devicesOf(site.id)} 台`}</TableCell>
                  <TableCell><StateChip health={site.status === 'ACTIVE' ? 'good' : 'idle'}>{SITE_STATUS[site.status]}</StateChip></TableCell>
                  <TableCell className="pr-5 text-right">
                    <button type="button" className="inline-flex items-center text-xs font-medium text-primary hover:underline" onClick={() => setTab('registry')}>
                      资产与点位<ChevronRight className="size-3.5" aria-hidden="true" />
                    </button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>
    </div>
  );

  const auditViewer = { subject: principal.principal.subject, displayName: principal.principal.displayName };
  const items = [
    { key: 'overview', label: '系统概览', children: overview },
    { key: 'users', label: '用户与角色', children: users },
    { key: 'site', label: '站点与组织', children: siteTab },
    { key: 'registry', label: '资产与点位', children: <RegistryAdministration capabilities={capabilities} registerUnsavedDraft={registerUnsavedDraft} /> },
    { key: 'rules', label: '自动化规则', children: <RuleManagement principal={principal} sites={sites} registerUnsavedDraft={registerUnsavedDraft} /> },
    { key: 'audit', label: '操作记录', children: <AuditLog viewer={auditViewer} canRead={capabilities.includes('audit.read')} active={activeTab === 'audit'} /> },
    { key: 'assistant', label: 'AI 助手', children: <AssistantUsage sites={sites} canRead={capabilities.includes('audit.read')} active={activeTab === 'assistant'} /> },
  ] as const;
  const normalizedActiveTab = items.some((item) => item.key === activeTab) ? activeTab : 'overview';
  function setTab(key: string) {
    void navigate({
      search: (previous) => ({ ...previous, tab: key === 'overview' ? undefined : key as NonNullable<typeof search.tab> }),
      replace: true,
    });
  }

  return (
    <Main>
      <PageHeader title="系统管理" description="平台级" />
      <Tabs value={normalizedActiveTab} onValueChange={setTab} className="gap-[18px]">
        <div className="border-b">
          <TabsList variant="line" className="-mb-px h-10 gap-6 px-0">
            {items.map((item) => <TabsTrigger key={item.key} value={item.key} className="flex-none px-0">{item.label}</TabsTrigger>)}
          </TabsList>
        </div>
        {items.map((item) => <TabsContent key={item.key} value={item.key}>{item.children}</TabsContent>)}
      </Tabs>
    </Main>
  );
}
