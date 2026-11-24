import { Link } from '@tanstack/react-router';
import { CloudOff, Compass, Lock, Search, ServerCrash, TriangleAlert } from 'lucide-react';
import type { ReactNode } from 'react';
import { StateCard } from '@/blocks/state/state-card';
import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { canAccessNavItem } from '@/components/layout/app-navigation';
import { primaryRoleLabel } from '@/lib/role-labels';
import { FocusHeading } from './FocusHeading';
import { RouteAccessError } from './route-access';
import { ShellChrome } from './ShellChrome';
import { useShellRuntime, useShellSnapshot } from './ShellRuntimeContext';

// Route-level states: a page the principal may not open, a service that cannot be confirmed, a site
// that is not visible, a path that does not exist. Each says what is missing and the way onward.

function Heading({ children }: { readonly children: ReactNode }) {
  return <FocusHeading className="text-lg leading-[26px] font-semibold tracking-[-0.01em]">{children}</FocusHeading>;
}

/** A route state centred in the page area, below the shell's top bar. */
function Frame({ children }: { readonly children: ReactNode }) {
  return <div className="flex min-h-[calc(100svh-var(--header-height)-80px)] items-center justify-center px-7">{children}</div>;
}

export function ForbiddenSurface() {
  const snapshot = useShellSnapshot();
  const roles = snapshot.principal?.principal.roles ?? [];
  const capabilities = snapshot.principal?.authorization.capabilities;
  const preferredSiteId = snapshot.protectedScope?.siteId ?? snapshot.lastSiteId ?? snapshot.sites?.items?.[0]?.id;
  const canOverview = canAccessNavItem({ requiredCapabilities: ['site.read'] }, capabilities) && Boolean(preferredSiteId);
  const canSystem = canAccessNavItem({ requiredCapabilities: ['site.list'] }, capabilities);
  return (
    <Frame>
      <StateCard
        icon={Lock}
        data-testid="real-route-forbidden"
        data-route-state="FORBIDDEN"
        title={<Heading>没有查看权限</Heading>}
        description="当前账号不能打开这个页面，需要平台管理员开通。"
      >
        <span className="text-xs text-muted-foreground">当前角色：{primaryRoleLabel(roles)}</span>
        {canOverview ? (
          <Button variant="outline" size="sm" asChild><Link to="/">回到总览</Link></Button>
        ) : canSystem ? (
          <Button variant="outline" size="sm" asChild><Link to="/system">前往系统管理</Link></Button>
        ) : null}
      </StateCard>
    </Frame>
  );
}

export function NoAuthorizedSiteSurface() {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const principal = snapshot.principal!;
  return (
    <Frame>
      <StateCard
        icon={Compass}
        data-testid="real-site-none"
        data-route-state="NO_AUTHORIZED_SITE"
        title={<Heading>当前账号没有授权站点</Heading>}
        description={`${principal.principal.displayName} 还没有可进入的站点，请联系平台管理员确认站点授权。`}
      >
        <Button size="sm" onClick={() => { void runtime.retry(); }}>刷新站点授权</Button>
      </StateCard>
    </Frame>
  );
}

function PlatformUnavailableSurface() {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const failure = snapshot.platform?.failure;
  return (
    <Frame>
      <StateCard
        icon={ServerCrash}
        tone="danger"
        data-testid="real-route-unavailable"
        data-route-state="UNAVAILABLE"
        title={<Heading>服务暂时不可用</Heading>}
        description="登录仍然有效，但页面依赖的服务状态现在无法确认。可以稍后重新检查。"
      >
        {failure ? <span role="alert" data-retryable={String(failure.retryable)} className="sr-only">服务状态暂时无法确认</span> : null}
        <Button size="sm" onClick={() => { void runtime.retry(); }}>重新检查服务</Button>
      </StateCard>
    </Frame>
  );
}

function PlatformDegradedSurface() {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const status = snapshot.platform?.status;
  const canOpenSystem = canAccessNavItem({ requiredCapabilities: ['site.list'] }, snapshot.principal?.authorization.capabilities);
  return (
    <Frame>
      <StateCard
        icon={TriangleAlert}
        tone="warning"
        data-testid="real-route-degraded"
        data-route-state="DEGRADED"
        title={<Heading>部分功能暂时不可用</Heading>}
        description="服务器报告部分功能暂时不可用；受影响的数据会标出来，不会显示成空的。"
      >
        {status ? <span className="text-xs text-muted-foreground">版本 {status.version}</span> : null}
        <Button size="sm" onClick={() => { void runtime.retry(); }}>重新检查</Button>
        {canOpenSystem ? <Button variant="outline" size="sm" asChild><Link to="/system">查看平台服务</Link></Button> : null}
      </StateCard>
    </Frame>
  );
}

function SiteNotVisibleSurface() {
  const sites = useShellSnapshot().sites?.items ?? [];
  return (
    <Frame>
      <StateCard
        icon={Compass}
        data-testid="real-site-not-visible"
        data-route-state="SITE_NOT_VISIBLE"
        title={<Heading>找不到这个站点</Heading>}
        description="链接里的站点不在当前账号的授权范围内，或已被删除。可以从下面的站点继续。"
      >
        {sites.length > 0 ? (
          <ul aria-label="授权站点" className="flex flex-wrap justify-center gap-2">
            {sites.map((site) => (
              <li key={site.id}>
                <Button variant="outline" size="sm" asChild><Link to="/overview" search={{ site: site.id }}>{site.displayName}</Link></Button>
              </li>
            ))}
          </ul>
        ) : null}
      </StateCard>
    </Frame>
  );
}

export function SiteDiscoveryUnavailableSurface() {
  const runtime = useShellRuntime();
  return (
    <Frame>
      <StateCard
        icon={CloudOff}
        tone="danger"
        data-testid="real-site-discovery-unavailable"
        data-route-state="UNAVAILABLE"
        title={<Heading>授权站点暂时读不到</Heading>}
        description="登录仍然有效，但当前组织的站点目录无法确认。"
      >
        <Button size="sm" onClick={() => { void runtime.retry(); }}>重试</Button>
      </StateCard>
    </Frame>
  );
}

function isChunkLoadError(error: unknown): boolean {
  if (!error) return false;
  const message = error instanceof Error ? `${error.name} ${error.message}` : String(error);
  return /dynamically imported module|ChunkLoadError|Importing a module script failed|Unable to preload CSS/i.test(message);
}

function SiteScopeMismatchSurface() {
  return (
    <Frame>
      <StateCard
        icon={Compass}
        tone="warning"
        data-testid="real-site-scope-mismatch"
        data-route-state="UNAVAILABLE"
        title={<Heading>站点上下文已切换</Heading>}
        description="当前页面关联的站点和刚才打开的站点不一致，重新载入后即可恢复。"
      >
        <Button size="sm" onClick={() => window.location.reload()}>重新加载</Button>
      </StateCard>
    </Frame>
  );
}

export function RouteErrorSurface({ error, reset }: { error: unknown; reset?: () => void }) {
  if (error instanceof RouteAccessError) {
    switch (error.code) {
      case 'FORBIDDEN':
        return <ForbiddenSurface />;
      case 'PLATFORM_UNAVAILABLE':
        return <PlatformUnavailableSurface />;
      case 'PLATFORM_DEGRADED':
        return <PlatformDegradedSurface />;
      case 'SITE_DISCOVERY_UNAVAILABLE':
        return <SiteDiscoveryUnavailableSurface />;
      case 'SITE_NOT_VISIBLE':
        return <SiteNotVisibleSurface />;
      case 'SITE_SCOPE_MISMATCH':
        return <SiteScopeMismatchSurface />;
    }
  }
  const chunkError = isChunkLoadError(error);
  return (
    <Frame>
      <StateCard
        icon={TriangleAlert}
        tone="danger"
        data-testid="real-route-unexpected-error"
        data-route-state="ERROR"
        title={<Heading>{chunkError ? '页面资源需要刷新' : '页面暂时无法显示'}</Heading>}
        description={
          chunkError
            ? '系统版本刚刚更新或网络波动导致页面资源未完整加载，刷新页面即可恢复。'
            : '刚才在加载或显示这个页面时遇到异常，你可以重试当前操作或刷新页面。'
        }
      >
        {!chunkError && reset ? (
          <Button size="sm" onClick={() => { reset(); }}>重试</Button>
        ) : null}
        <Button variant={!chunkError && reset ? 'outline' : 'default'} size="sm" onClick={() => window.location.reload()}>
          刷新页面
        </Button>
      </StateCard>
    </Frame>
  );
}

/** Opens the header's page and device search, as Ctrl K does. */
function openSearch() {
  globalThis.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true }));
}

function NotFoundContent() {
  const snapshot = useShellSnapshot();
  const capabilities = snapshot.principal?.authorization.capabilities;
  const preferredSiteId = snapshot.protectedScope?.siteId ?? snapshot.lastSiteId;
  const site = (preferredSiteId ? snapshot.sites?.items?.find((item) => item.id === preferredSiteId) : undefined) ?? snapshot.sites?.items?.[0];
  const links = site
    ? ([
      ['实时运行', '/operations/realtime', ['site.read'], ['telemetry.snapshot.read', 'telemetry.batch.read']],
      ['告警', '/operations/alarms', ['site.read', 'alarm.list'], undefined],
      ['能耗分析', '/energy-analysis/consumption', ['site.read'], undefined],
      ['报告', '/reports', ['site.read'], undefined],
    ] as const).filter(([, , requiredCapabilities, anyCapabilities]) =>
      canAccessNavItem({ requiredCapabilities, anyCapabilities }, capabilities),
    )
    : [];
  const canOverview = canAccessNavItem({ requiredCapabilities: ['site.read'] }, capabilities) && Boolean(site);
  return (
    <Frame>
      <StateCard
        icon={Compass}
        data-testid="real-route-not-found"
        data-route-state="NOT_FOUND"
        title={<Heading>没有找到这个页面</Heading>}
        description="链接可能已过期，或这个对象已被删除。可以从下面的常用页面继续。"
      >
        {canOverview ? <Button size="sm" asChild><Link to="/">回到总览</Link></Button> : null}
        <Button variant={canOverview ? 'outline' : 'default'} size="sm" onClick={openSearch}>
          <Search data-icon="inline-start" />
          搜索页面与设备
          <Kbd>Ctrl K</Kbd>
        </Button>
        {site && links.length > 0 ? (
          <nav aria-label="常用页面" className="mt-3 flex w-full justify-center gap-4 text-sm">
            {links.map(([label, to]) => (
              <Link key={to} to={to} search={{ site: site.id }} className="text-primary hover:underline">{label}</Link>
            ))}
          </nav>
        ) : null}
      </StateCard>
    </Frame>
  );
}

export function NotFoundSurface() {
  return (
    <ShellChrome>
      <NotFoundContent />
    </ShellChrome>
  );
}
