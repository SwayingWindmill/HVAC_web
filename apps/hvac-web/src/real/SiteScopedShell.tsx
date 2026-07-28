import type { Capability, Site } from '@/api/generated/platformGateway.gen';
import { RealShellChrome } from './RealShellChrome';
import type { RealNavigationItem } from './route-policy';
import type { RealRuntimeConfig } from './runtime-config';
import type { ShellFailureView, ShellSnapshot } from './shell-runtime';
import { siteRoute, type SiteRoutingDecision } from './site-routing';

type RoutedSiteDecision = Exclude<SiteRoutingDecision, { state: 'PLATFORM_ROUTE' }>;

export type SiteShellDecision =
  | RoutedSiteDecision
  | { state: 'SITE_DISCOVERY_CHECKING' }
  | { state: 'SITE_DISCOVERY_UNAVAILABLE'; failure?: ShellFailureView };

function SiteChooserList({ sites }: { sites: readonly Readonly<Site>[] }) {
  return (
    <ul className="real-site-chooser-list" aria-label="Authorized Sites">
      {sites.map((site) => (
        <li key={site.id}>
          <a href={siteRoute(site, 'assets')} data-site-id={site.id}>
            <strong>{site.displayName}</strong>
            <span>{site.code}</span>
            <small>{site.timezone}</small>
          </a>
        </li>
      ))}
    </ul>
  );
}

function SiteDiscoveryCheckingSurface() {
  return (
    <section className="real-route-surface" data-testid="real-site-discovery-checking" data-route-state="SITE_DISCOVERY_CHECKING">
      <p className="real-shell-eyebrow">REAL MODE · SITE DISCOVERY</p>
      <h1>正在读取授权 Site</h1>
      <p>Shell 正在从当前 Acting Organization 的 Registry 边界读取授权 Site。完成前不会挂载 Site 页面。</p>
      <div className="real-shell-progress" role="status" aria-live="polite">正在验证 Site scope…</div>
    </section>
  );
}

function SiteDiscoveryUnavailableSurface({ failure, retry }: { failure?: ShellFailureView; retry: () => void }) {
  return (
    <section className="real-route-surface" data-testid="real-site-discovery-unavailable" data-route-state="UNAVAILABLE">
      <p className="real-shell-eyebrow">REAL MODE · UNAVAILABLE</p>
      <h1>无法读取授权 Site</h1>
      <p>Principal 仍然有效，但当前 Organization 的 Registry Site 集合无法确认。系统不会使用缓存 Site 或本地 building alias。</p>
      {failure ? (
        <div className="real-shell-problem" role="alert" data-retryable={String(failure.retryable)}>
          <strong>{failure.code}</strong>
          <span>{failure.detail}</span>
          {failure.traceId ? <code>traceId {failure.traceId}</code> : null}
        </div>
      ) : null}
      <div className="real-shell-actions">
        <button type="button" onClick={retry}>重试 Site discovery</button>
      </div>
    </section>
  );
}

function NoAuthorizedSiteSurface({ snapshot, retry }: { snapshot: ShellSnapshot; retry: () => void }) {
  const principal = snapshot.principal!;
  return (
    <section className="real-route-surface" data-testid="real-site-none" data-route-state="NO_AUTHORIZED_SITE">
      <p className="real-shell-eyebrow">REAL MODE · NO AUTHORIZED SITE</p>
      <h1>当前账号没有授权 Site</h1>
      <p>Shell 已验证当前 Acting Organization，但 Registry 没有返回此 Principal 可进入的 Site。</p>
      <dl className="real-shell-facts">
        <div><dt>Account</dt><dd>{principal.principal.displayName}</dd></div>
        <div><dt>Subject</dt><dd>{principal.principal.subject}</dd></div>
        <div><dt>Acting Organization</dt><dd>{principal.context.actingOrganizationId}</dd></div>
      </dl>
      <div className="real-shell-actions">
        <button type="button" onClick={retry}>刷新授权 Site</button>
        <a className="real-shell-link-action" href="#real-site-help">查看帮助</a>
      </div>
      <div id="real-site-help" className="real-site-help" tabIndex={-1}>
        <strong>帮助</strong>
        <p>请联系当前 Organization 的管理员确认 Site membership 与 IAM policy，然后重试。此页面不会自动创建或选择 Site。</p>
      </div>
    </section>
  );
}

function SiteChooserSurface({ sites }: { sites: readonly Readonly<Site>[] }) {
  return (
    <section className="real-route-surface" data-testid="real-site-chooser" data-route-state="CHOOSE_SITE">
      <p className="real-shell-eyebrow">REAL MODE · CHOOSE SITE</p>
      <h1>选择一个授权 Site</h1>
      <p>当前账号可进入多个 Site。Shell 不会静默选择列表第一项，也不会使用浏览器中保存的 building alias。</p>
      <SiteChooserList sites={sites} />
    </section>
  );
}

function SiteNotVisibleSurface({ sites }: { sites: readonly Readonly<Site>[] }) {
  return (
    <section className="real-route-surface" data-testid="real-site-not-visible" data-route-state="SITE_NOT_VISIBLE">
      <p className="real-shell-eyebrow">REAL MODE · SITE NOT VISIBLE</p>
      <h1>Site 不可见或不存在</h1>
      <p>Shell 无法在当前 Acting Organization 的授权 Registry 集合中验证 URL Site。为避免泄露，不说明具体原因，也不会自动切换 Scope。</p>
      {sites.length > 0 ? (
        <>
          <h2>选择其他授权 Site</h2>
          <SiteChooserList sites={sites} />
        </>
      ) : null}
    </section>
  );
}

function GenericForbiddenSurface() {
  return (
    <section className="real-route-surface" data-testid="real-route-forbidden" data-route-state="FORBIDDEN">
      <p className="real-shell-eyebrow">REAL MODE · ACCESS DENIED</p>
      <h1>访问被拒绝</h1>
      <p>当前 Principal 无权打开此页面。此状态不说明目标 Site、功能或资源是否存在。</p>
      <a className="real-shell-link-action" href="/">返回 Site 入口</a>
    </section>
  );
}

function RedirectSurface() {
  return (
    <section className="real-route-surface" data-testid="real-site-redirect" data-route-state="REDIRECT">
      <p className="real-shell-eyebrow">REAL MODE · SITE REDIRECT</p>
      <h1>正在进入唯一授权 Site</h1>
      <div className="real-shell-progress" role="status" aria-live="polite">正在建立显式 Site URL…</div>
    </section>
  );
}

function SiteRouteNotFoundSurface({ decision }: { decision: Extract<SiteRoutingDecision, { state: 'SITE_ROUTE_NOT_FOUND' }> }) {
  return (
    <section className="real-route-surface" data-testid="real-site-route-not-found" data-route-state="NOT_FOUND">
      <p className="real-shell-eyebrow">REAL MODE · SITE 404</p>
      <h1>此 Site 页面不存在</h1>
      <p>URL Site 已通过 Registry 验证，但后续路径不属于当前 Real Build 的 Site 路由。</p>
      <a className="real-shell-link-action" href={siteRoute(decision.context.site, 'assets')}>返回 Site Assets</a>
    </section>
  );
}

const SITE_ROUTE_COPY = {
  assets: {
    eyebrow: 'REAL MODE · SITE ASSETS',
    title: 'Assets',
    detail: 'Assets 路由已绑定到验证后的 SiteContext。资产数据仍由 Registry、设备与遥测域的权威接口负责。',
  },
  commands: {
    eyebrow: 'REAL MODE · SITE COMMANDS',
    title: 'Commands',
    detail: 'Commands 路由已绑定到验证后的 SiteContext。命令授权、审批与执行仍由服务器端 Command 域负责。',
  },
  bigscreen: {
    eyebrow: 'REAL MODE · SITE BIGSCREEN',
    title: 'BigScreen',
    detail: 'BigScreen 已限定到当前验证 Site；后续只能消费与普通 Site UI 相同的权威 Read Model。',
  },
} as const;

function ReadySiteSurface({ decision }: { decision: Extract<SiteRoutingDecision, { state: 'READY' }> }) {
  const copy = SITE_ROUTE_COPY[decision.route];
  return (
    <section
      className="real-route-surface"
      data-testid={`real-site-route-${decision.route}`}
      data-route-state="READY"
      data-business-state="EMPTY"
      data-site-id={decision.context.site.id}
      data-site-route={decision.route}
    >
      <p className="real-shell-eyebrow">{copy.eyebrow}</p>
      <h1>{copy.title}</h1>
      <p>{copy.detail}</p>
      <dl className="real-shell-facts">
        <div><dt>Site</dt><dd>{decision.context.site.displayName}</dd></div>
        <div><dt>Registry Site ID</dt><dd>{decision.context.site.id}</dd></div>
        <div><dt>Site code</dt><dd>{decision.context.site.code}</dd></div>
        <div><dt>Timezone</dt><dd>{decision.context.site.timezone}</dd></div>
        <div><dt>Acting Organization</dt><dd>{decision.context.actingOrganizationId}</dd></div>
      </dl>
      <p>当前业务数据状态为 EMPTY；这不代表权限拒绝、服务不可用或 Demo 数据。</p>
    </section>
  );
}

function SiteSurface({
  decision,
  snapshot,
  retry,
}: {
  decision: SiteShellDecision;
  snapshot: ShellSnapshot;
  retry: () => void;
}) {
  switch (decision.state) {
    case 'SITE_DISCOVERY_CHECKING':
      return <SiteDiscoveryCheckingSurface />;
    case 'SITE_DISCOVERY_UNAVAILABLE':
      return <SiteDiscoveryUnavailableSurface failure={decision.failure} retry={retry} />;
    case 'NO_AUTHORIZED_SITE':
      return <NoAuthorizedSiteSurface snapshot={snapshot} retry={retry} />;
    case 'CHOOSE_SITE':
      return <SiteChooserSurface sites={decision.sites} />;
    case 'REDIRECT':
      return <RedirectSurface />;
    case 'SITE_NOT_VISIBLE':
      return <SiteNotVisibleSurface sites={snapshot.sites?.items ?? []} />;
    case 'FORBIDDEN':
      return <GenericForbiddenSurface />;
    case 'SITE_ROUTE_NOT_FOUND':
      return <SiteRouteNotFoundSurface decision={decision} />;
    case 'READY':
      return <ReadySiteSurface decision={decision} />;
  }
}

export function buildSiteNavigation(
  site: Readonly<Site>,
  effectiveCapabilities: readonly Capability[],
): RealNavigationItem[] {
  if (!effectiveCapabilities.includes('site.read')) return [];
  return [
    { id: 'site-assets', label: 'Assets', path: siteRoute(site, 'assets'), kind: 'link', degraded: false },
    { id: 'site-commands', label: 'Commands', path: siteRoute(site, 'commands'), kind: 'link', degraded: false },
    { id: 'site-bigscreen', label: 'BigScreen', path: siteRoute(site, 'bigscreen'), kind: 'link', degraded: false },
  ];
}

export function SiteScopedShell({
  config,
  snapshot,
  navigation,
  decision,
  retry,
  logout,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  navigation: RealNavigationItem[];
  decision: SiteShellDecision;
  retry: () => void;
  logout: () => void;
}) {
  return (
    <RealShellChrome config={config} snapshot={snapshot} navigation={navigation} logout={logout}>
      <SiteSurface decision={decision} snapshot={snapshot} retry={retry} />
    </RealShellChrome>
  );
}
