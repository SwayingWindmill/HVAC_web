import { lazy, Suspense, useEffect } from 'react';
import type { Capability, Site } from '@/api/generated/platformGateway.gen';
import { FocusHeading } from './FocusHeading';
import { RealShellChrome } from './RealShellChrome';
import type { RealNavigationItem } from './route-policy';
import type { ProtectedScopeDraft, ProtectedScopeRequestToken, ProtectedScopeResource } from './protected-scope';
import type { RealRuntimeConfig } from './runtime-config';
import type { ShellFailureView, ShellSnapshot } from './shell-runtime';
import { siteRoute, type SiteContext, type SiteRoutingDecision } from './site-routing';

const RealDashboard = lazy(async () => {
  const module = await import('./RealDashboard');
  return { default: module.RealDashboard };
});

const RealAssetsWorkspace = lazy(async () => {
  const module = await import('./assets/RealAssetsWorkspace');
  return { default: module.RealAssetsWorkspace };
});

const EnergyAnalytics = lazy(async () => {
  const module = await import('./EnergyAnalytics');
  return { default: module.EnergyAnalytics };
});

const RealCommands = lazy(async () => {
  const module = await import('./RealCommands');
  return { default: module.RealCommands };
});

const RealAlarms = lazy(async () => {
  const module = await import('./RealAlarms');
  return { default: module.RealAlarms };
});

const OperationsInvestigationPage = lazy(async () => {
  const module = await import('./OperationsInvestigationPage');
  return { default: module.OperationsInvestigationPage };
});

const RealFddPage = lazy(async () => {
  const module = await import('./RealProductPages');
  return { default: module.RealFddPage };
});

const RealOptimizePage = lazy(async () => {
  const module = await import('./RealProductPages');
  return { default: module.RealOptimizePage };
});

const RealCostPage = lazy(async () => {
  const module = await import('./RealProductPages');
  return { default: module.RealCostPage };
});

const RealAiLanding = lazy(async () => {
  const module = await import('./RealProductPages');
  return { default: module.RealAiLanding };
});

const RealBigScreenPage = lazy(async () => {
  const module = await import('./RealProductPages');
  return { default: module.RealBigScreenPage };
});

const OPERATIONS_ROUTE_CONTRACT = {
  label: 'Operations Workspace',
} as const;

type RoutedSiteDecision = Exclude<SiteRoutingDecision, { state: 'PLATFORM_ROUTE' }>;

export type SiteShellDecision =
  | RoutedSiteDecision
  | { state: 'SITE_DISCOVERY_CHECKING' }
  | { state: 'SITE_DISCOVERY_UNAVAILABLE'; failure?: ShellFailureView }
  | { state: 'SITE_SCOPE_ACTIVATING'; context: SiteContext };

function SiteChooserList({ sites }: { sites: readonly Readonly<Site>[] }) {
  return (
    <ul className="real-site-chooser-list" aria-label="Authorized Sites">
      {sites.map((site) => (
        <li key={site.id}>
          <a href={siteRoute(site, 'dashboard')} data-site-id={site.id}>
            <strong>{site.displayName}</strong>
            <span>{site.code}</span>
            <small>{site.timezone}</small>
          </a>
        </li>
      ))}
    </ul>
  );
}

function SiteScopeSwitcher({
  sites,
  currentSiteId,
  onNavigate,
}: {
  sites: readonly Readonly<Site>[];
  currentSiteId: string;
  onNavigate: (target: string) => void;
}) {
  if (sites.length < 2) return null;
  return (
    <section className="real-site-switcher" data-testid="real-site-switcher" aria-label="Switch authorized Site">
      <strong>Authorized Sites</strong>
      <div>
        {sites.map((site) => (
          <button
            key={site.id}
            type="button"
            data-site-switch-id={site.id}
            aria-current={site.id === currentSiteId ? 'page' : undefined}
            disabled={site.id === currentSiteId}
            onClick={() => onNavigate(siteRoute(site, 'dashboard'))}
          >
            {site.displayName}
          </button>
        ))}
      </div>
    </section>
  );
}

function SiteDiscoveryCheckingSurface() {
  return (
    <section className="real-route-surface" data-testid="real-site-discovery-checking" data-route-state="SITE_DISCOVERY_CHECKING">
      <p className="real-shell-eyebrow">REAL MODE · SITE DISCOVERY</p>
      <FocusHeading>正在读取授权 Site</FocusHeading>
      <p>Shell 正在从当前 Acting Organization 的 Registry 边界读取授权 Site。完成前不会挂载 Site 页面。</p>
      <div className="real-shell-progress" role="status" aria-live="polite">正在验证 Site scope…</div>
    </section>
  );
}

function SiteScopeActivatingSurface({ context }: { context: SiteContext }) {
  return (
    <section className="real-route-surface" data-testid="real-site-scope-activating" data-route-state="SITE_SCOPE_ACTIVATING">
      <p className="real-shell-eyebrow">REAL MODE · SITE SCOPE</p>
      <FocusHeading>正在激活受保护 Site scope</FocusHeading>
      <p>URL 与 Registry Site 已验证。Shell 正在建立新的 request generation 和受保护资源所有权，完成前不会渲染 Site 业务表面。</p>
      <dl className="real-shell-facts">
        <div><dt>Site</dt><dd>{context.site.displayName}</dd></div>
        <div><dt>Registry Site ID</dt><dd>{context.site.id}</dd></div>
      </dl>
      <div className="real-shell-progress" role="status" aria-live="polite">正在建立 Site lifecycle boundary…</div>
    </section>
  );
}

function SiteDiscoveryUnavailableSurface({ failure, retry }: { failure?: ShellFailureView; retry: () => void }) {
  return (
    <section className="real-route-surface" data-testid="real-site-discovery-unavailable" data-route-state="UNAVAILABLE">
      <p className="real-shell-eyebrow">REAL MODE · UNAVAILABLE</p>
      <FocusHeading>无法读取授权 Site</FocusHeading>
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
      <FocusHeading>当前账号没有授权 Site</FocusHeading>
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
      <FocusHeading>选择一个授权 Site</FocusHeading>
      <p>当前账号可进入多个 Site。Shell 不会静默选择列表第一项，也不会使用浏览器中保存的 building alias。</p>
      <SiteChooserList sites={sites} />
    </section>
  );
}

function SiteNotVisibleSurface({ sites }: { sites: readonly Readonly<Site>[] }) {
  return (
    <section className="real-route-surface" data-testid="real-site-not-visible" data-route-state="SITE_NOT_VISIBLE">
      <p className="real-shell-eyebrow">REAL MODE · SITE NOT VISIBLE</p>
      <FocusHeading>Site 不可见或不存在</FocusHeading>
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
      <FocusHeading>访问被拒绝</FocusHeading>
      <p>当前 Principal 无权打开此页面。此状态不说明目标 Site、功能或资源是否存在。</p>
      <a className="real-shell-link-action" href="/">返回 Site 入口</a>
    </section>
  );
}

function RedirectSurface() {
  return (
    <section className="real-route-surface" data-testid="real-site-redirect" data-route-state="REDIRECT">
      <p className="real-shell-eyebrow">REAL MODE · SITE REDIRECT</p>
      <FocusHeading>正在进入唯一授权 Site</FocusHeading>
      <div className="real-shell-progress" role="status" aria-live="polite">正在建立显式 Site URL…</div>
    </section>
  );
}

function SiteRouteNotFoundSurface({ decision }: { decision: Extract<SiteRoutingDecision, { state: 'SITE_ROUTE_NOT_FOUND' }> }) {
  return (
    <section className="real-route-surface" data-testid="real-site-route-not-found" data-route-state="NOT_FOUND">
      <p className="real-shell-eyebrow">REAL MODE · SITE 404</p>
      <FocusHeading>此 Site 页面不存在</FocusHeading>
      <p>URL Site 已通过 Registry 验证，但后续路径不属于当前 Real Build 的 Site 路由。</p>
      <a className="real-shell-link-action" href={siteRoute(decision.context.site, 'dashboard')}>返回 Site Dashboard</a>
    </section>
  );
}

function ReadySiteSurface({
  decision,
  snapshot,
  registerProtectedResource,
  protectedRequestToken,
  registerUnsavedDraft,
}: {
  decision: Extract<SiteRoutingDecision, { state: 'READY' }>;
  snapshot: ShellSnapshot;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  protectedRequestToken: () => ProtectedScopeRequestToken;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}) {
  if (decision.route === 'dashboard') {
    return (
      <section
        className="real-route-surface real-route-surface--dashboard"
        data-route-state="READY"
        data-site-id={decision.context.site.id}
        data-site-route="dashboard"
      >
        <Suspense fallback={(
          <div className="real-shell-progress" role="status" aria-live="polite">
            正在加载运行总览…
          </div>
        )}>
          <RealDashboard site={decision.context.site} principal={snapshot.principal!} />
        </Suspense>
      </section>
    );
  }

  if (decision.route === 'assets') {
    return (
      <Suspense fallback={(
        <section
          className="real-route-surface"
          data-testid="real-site-route-assets"
          data-route-state="READY"
          data-business-state="LOADING"
          data-site-id={decision.context.site.id}
        >
          <div className="real-shell-progress" role="status" aria-live="polite">正在加载资产运行工作台…</div>
        </section>
      )}>
        <RealAssetsWorkspace
          site={decision.context.site}
          principal={snapshot.principal!}
          requestedDeviceId={decision.deviceId}
          protectedGeneration={snapshot.protectedScope!.generation}
          protectedRequestToken={protectedRequestToken}
          registerProtectedResource={registerProtectedResource}
        />
      </Suspense>
    );
  }

  if (decision.route === 'energy') {
    return (
      <section
        className="real-route-surface real-route-surface--energy"
        data-testid="real-site-route-energy"
        data-route-state="READY"
        data-site-id={decision.context.site.id}
        data-site-route="energy"
      >
        <Suspense fallback={(
          <div className="real-shell-progress" role="status" aria-live="polite">
            正在加载能源分析界面…
          </div>
        )}>
          <EnergyAnalytics
            site={decision.context.site}
            principal={snapshot.principal!}
            initialPeriod={decision.energyPeriod}
          />
        </Suspense>
      </section>
    );
  }

  if (decision.route === 'optimize') {
    return (
      <Suspense fallback={<div className="real-shell-progress" role="status">正在加载节能优化工作台…</div>}>
        <RealOptimizePage site={decision.context.site} principal={snapshot.principal!} />
      </Suspense>
    );
  }

  if (decision.route === 'cost') {
    return (
      <Suspense fallback={<div className="real-shell-progress" role="status">正在加载成本绩效工作台…</div>}>
        <RealCostPage site={decision.context.site} principal={snapshot.principal!} />
      </Suspense>
    );
  }

  if (decision.route === 'ai') {
    const site = decision.context.site;
    return (
      <Suspense fallback={<div className="real-shell-progress" role="status">正在加载 AI 运维助手…</div>}>
        <RealAiLanding
          site={site}
          principal={snapshot.principal!}
          operationsPath={siteRoute(site, 'operations')}
        />
      </Suspense>
    );
  }

  if (decision.route === 'bigscreen') {
    return (
      <Suspense fallback={<div className="real-shell-progress" role="status">正在加载运行大屏…</div>}>
        <RealBigScreenPage site={decision.context.site} principal={snapshot.principal!} />
      </Suspense>
    );
  }

  if (decision.route === 'fdd') {
    return (
      <Suspense fallback={<div className="real-shell-progress" role="status">正在加载故障检测工作台…</div>}>
        <RealFddPage site={decision.context.site} principal={snapshot.principal!} />
      </Suspense>
    );
  }

  if (decision.route === 'alarms') {
    return (
      <section
        className="real-route-surface real-route-surface--alarms"
        data-testid="real-site-route-alarms"
        data-route-state="READY"
        data-site-id={decision.context.site.id}
        data-site-route="alarms"
      >
        <Suspense fallback={(
          <div className="real-shell-progress" role="status" aria-live="polite">
            正在加载 Alarm 界面…
          </div>
        )}>
          <RealAlarms
            site={decision.context.site}
            principal={snapshot.principal!}
            registerUnsavedDraft={registerUnsavedDraft}
            registerProtectedResource={registerProtectedResource}
          />
        </Suspense>
      </section>
    );
  }

  if (decision.route === 'operations') {
    return (
      <section
        className="real-route-surface real-route-surface--operations"
        data-route-state="READY"
        data-site-id={decision.context.site.id}
        data-site-route="operations"
        aria-label={OPERATIONS_ROUTE_CONTRACT.label}
      >
        <Suspense fallback={(
          <div className="real-shell-progress" role="status" aria-live="polite">
            正在加载 Operations Investigation…
          </div>
        )}>
          <OperationsInvestigationPage
            site={decision.context.site}
            principal={snapshot.principal!}
            registerProtectedResource={registerProtectedResource}
          />
        </Suspense>
      </section>
    );
  }

  if (decision.route === 'commands') {
    return (
      <section
        className="real-route-surface real-route-surface--commands"
        data-testid="real-site-route-commands"
        data-route-state="READY"
        data-site-id={decision.context.site.id}
        data-site-route="commands"
      >
        <Suspense fallback={(
          <div className="real-shell-progress" role="status" aria-live="polite">
            正在加载 Command 工作台…
          </div>
        )}>
          <RealCommands
            site={decision.context.site}
            principal={snapshot.principal!}
            initialCommandId={decision.commandId}
            registerUnsavedDraft={registerUnsavedDraft}
            registerProtectedResource={registerProtectedResource}
          />
        </Suspense>
      </section>
    );
  }

  return null;
}

function waitForSiteRoutePaint(): Promise<void> {
  return new Promise((resolve) => {
    let completed = false;
    const finish = () => {
      if (completed) return;
      completed = true;
      window.clearTimeout(timeout);
      resolve();
    };
    const timeout = window.setTimeout(finish, 250);
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(finish);
    });
  });
}

function ProtectedSiteRouteFrame({
  decision,
  snapshot,
  registerProtectedResource,
  protectedRequestToken,
  registerUnsavedDraft,
}: {
  decision: Extract<SiteRoutingDecision, { state: 'READY' }>;
  snapshot: ShellSnapshot;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  protectedRequestToken: () => ProtectedScopeRequestToken;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}) {
  useEffect(() => registerProtectedResource({
    id: `site-route-frame:${decision.context.site.id}:${decision.route}`,
    kind: 'temporary-state',
    purge: waitForSiteRoutePaint,
  }), [decision.context.site.id, decision.route, registerProtectedResource]);

  return (
    <ReadySiteSurface
      decision={decision}
      snapshot={snapshot}
      registerProtectedResource={registerProtectedResource}
      protectedRequestToken={protectedRequestToken}
      registerUnsavedDraft={registerUnsavedDraft}
    />
  );
}

function SiteSurface({
  decision,
  snapshot,
  retry,
  registerProtectedResource,
  protectedRequestToken,
  registerUnsavedDraft,
}: {
  decision: SiteShellDecision;
  snapshot: ShellSnapshot;
  retry: () => void;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  protectedRequestToken: () => ProtectedScopeRequestToken;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}) {
  switch (decision.state) {
    case 'SITE_DISCOVERY_CHECKING':
      return <SiteDiscoveryCheckingSurface />;
    case 'SITE_SCOPE_ACTIVATING':
      return <SiteScopeActivatingSurface context={decision.context} />;
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
      return (
        <ProtectedSiteRouteFrame
          decision={decision}
          snapshot={snapshot}
          registerProtectedResource={registerProtectedResource}
          protectedRequestToken={protectedRequestToken}
          registerUnsavedDraft={registerUnsavedDraft}
        />
      );
  }
}

export function buildSiteNavigation(
  site: Readonly<Site>,
  effectiveCapabilities: readonly Capability[],
): RealNavigationItem[] {
  if (!effectiveCapabilities.includes('site.read')) return [];
  const navigation: RealNavigationItem[] = [
    { id: 'site-dashboard', label: '总览驾驶舱', path: siteRoute(site, 'dashboard'), kind: 'link', degraded: false },
    { id: 'site-assets', label: '设备与建筑', path: siteRoute(site, 'assets'), kind: 'link', degraded: false },
    { id: 'site-commands', label: '设备控制', path: siteRoute(site, 'commands'), kind: 'link', degraded: false },
    { id: 'site-energy', label: '能耗分析', path: siteRoute(site, 'energy'), kind: 'link', degraded: false },
    { id: 'site-optimize', label: '节能优化', path: siteRoute(site, 'optimize'), kind: 'link', degraded: false },
    { id: 'site-fdd', label: '故障检测', path: siteRoute(site, 'fdd'), kind: 'link', degraded: false },
  ];
  if (effectiveCapabilities.includes('alarm.list')) {
    navigation.push({ id: 'site-alarms', label: '报警工单', path: siteRoute(site, 'alarms'), kind: 'link', degraded: false });
  }
  navigation.push(
    { id: 'site-ai', label: 'AI 运维助手', path: siteRoute(site, 'ai'), kind: 'link', degraded: false, primary: true },
    { id: 'site-cost', label: '成本与绩效', path: siteRoute(site, 'cost'), kind: 'link', degraded: false },
    { id: 'site-bigscreen', label: '演示大屏', path: siteRoute(site, 'bigscreen'), kind: 'link', degraded: false },
  );
  return navigation;
}

export function SiteScopedShell({
  config,
  snapshot,
  navigation,
  decision,
  retry,
  logout,
  onNavigate,
  confirmSiteNavigation,
  cancelSiteNavigation,
  registerProtectedResource,
  protectedRequestToken,
  registerUnsavedDraft,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  navigation: RealNavigationItem[];
  decision: SiteShellDecision;
  retry: () => void;
  logout: () => void;
  onNavigate: (target: string) => void;
  confirmSiteNavigation: () => void;
  cancelSiteNavigation: () => void;
  registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  protectedRequestToken: () => ProtectedScopeRequestToken;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}) {
  if (decision.state === 'READY' && decision.route === 'bigscreen') {
    return (
      <SiteSurface
        decision={decision}
        snapshot={snapshot}
        retry={retry}
        registerProtectedResource={registerProtectedResource}
        protectedRequestToken={protectedRequestToken}
        registerUnsavedDraft={registerUnsavedDraft}
      />
    );
  }

  return (
    <RealShellChrome
      config={config}
      snapshot={snapshot}
      navigation={navigation}
      logout={logout}
      onNavigate={onNavigate}
      confirmSiteNavigation={confirmSiteNavigation}
      cancelSiteNavigation={cancelSiteNavigation}
    >
      {decision.state === 'READY' ? (
        <SiteScopeSwitcher
          sites={snapshot.sites?.items ?? []}
          currentSiteId={decision.context.site.id}
          onNavigate={onNavigate}
        />
      ) : null}
      <SiteSurface
        decision={decision}
        snapshot={snapshot}
        retry={retry}
        registerProtectedResource={registerProtectedResource}
        protectedRequestToken={protectedRequestToken}
        registerUnsavedDraft={registerUnsavedDraft}
      />
    </RealShellChrome>
  );
}
