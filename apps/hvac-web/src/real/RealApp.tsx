import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { LoginFormPage } from '@ant-design/pro-components';
import { useLocation, useNavigate } from 'react-router';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import { useRealUiStore } from '@/stores/realUi';
import { useRealObservability } from '@/app/RealObservability';
import { AuthenticatedShell } from './AuthenticatedShell';
import { REAL_FEATURE_MANIFEST } from './feature-manifest';
import { FocusHeading } from './FocusHeading';
import { RealRouteLoading } from './RealRouteLoading';
import { RealRuntimeFacts } from './RealRuntimeFacts';
import { resolveNavigation, resolveRoute, type RouteDecision } from './route-policy';
import { createBrowserShellEnvironment, createShellRuntime, type ShellSnapshot } from './shell-runtime';
import { buildSiteNavigation, SiteScopedShell, type SiteShellDecision } from './SiteScopedShell';
import { resolveSiteRouting } from './site-routing';
import type { RealRuntimeConfig, RealRuntimeConfigFailure } from './runtime-config';
import './real-shell.css';

const REAL_GRAPH_MARKER = 'HVAC_WEB_REAL_GRAPH_V1';
const REAL_SHELL_MARKER = 'REAL MODE · AUTHORITATIVE SHELL';

interface RealAppProps {
  config: RealRuntimeConfig;
}

export function RealConfigurationBlocked({ failures }: { failures: RealRuntimeConfigFailure[] }) {
  return (
    <main className="real-shell-state" data-build-graph={REAL_GRAPH_MARKER} data-shell-state="UNAVAILABLE">
      <section className="real-shell-card" aria-labelledby="real-config-title">
        <p className="real-shell-eyebrow">REAL MODE · STARTUP BLOCKED</p>
        <FocusHeading id="real-config-title">Real 配置无效</FocusHeading>
        <p>应用已按失败关闭策略停止，未挂载业务路由、Demo 数据或 Mock 服务。</p>
        <ul className="real-shell-failures">
          {failures.map((failure) => (
            <li key={failure.code}>
              <strong>{failure.code}</strong>
              <span>{failure.detail}</span>
            </li>
          ))}
        </ul>
      </section>
    </main>
  );
}

function BootstrappingState() {
  return (
    <RealRouteLoading
      label="正在进入工作台"
      testId="real-shell-bootstrapping"
      routeState="BOOTSTRAPPING"
      variant="shell"
    />
  );
}

function LoginRequiredState({
  snapshot,
  beginLogin,
}: {
  snapshot: ShellSnapshot;
  beginLogin: () => void;
}) {
  const loggedOut = snapshot.reason === 'LOGOUT_COMPLETED' || snapshot.reason === 'SESSION_ALREADY_INVALID';
  if (!loggedOut) {
    return (
      <RealRouteLoading
        label="正在前往登录"
        testId="real-shell-auth-redirect"
        routeState="LOGIN_REQUIRED"
        variant="shell"
      />
    );
  }

  return (
    <section className="real-pro-login" data-testid="real-shell-login-required">
      <LoginFormPage
        logo="/quanlaihe-mark.svg"
        title="泉来禾智慧能源"
        subTitle="企业级实时能源运营平台"
        backgroundImageUrl="https://images.unsplash.com/photo-1642615835477-d303d7dc9ee9?w=2160&q=80"
        activityConfig={{
          title: '中央机房实时运营',
          subTitle: '设备、能耗、告警与操作统一进入受治理工作台。',
        }}
        submitter={{ searchConfig: { submitText: '重新登录' } }}
        onFinish={async () => {
          beginLogin();
          return true;
        }}
      />
    </section>
  );
}

function PrincipalUnavailableState({
  config,
  snapshot,
  retry,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  retry: () => void;
}) {
  return (
    <section className="real-shell-card" aria-labelledby="real-unavailable-title" data-testid="real-shell-unavailable">
      <p className="real-shell-eyebrow">REAL MODE · UNAVAILABLE</p>
      <FocusHeading id="real-unavailable-title">无法建立可信 Principal</FocusHeading>
      <p>业务路由保持未挂载。系统不会使用 Demo、Mock 或缓存身份作为替代。</p>
      {snapshot.failure ? (
        <div className="real-shell-problem" role="alert" data-retryable={String(snapshot.failure.retryable)}>
          <strong>{snapshot.failure.code}</strong>
          <span>{snapshot.failure.detail}</span>
          {snapshot.failure.traceId ? <code>traceId {snapshot.failure.traceId}</code> : null}
        </div>
      ) : null}
      <div className="real-shell-actions">
        <button type="button" onClick={retry}>重试 Principal bootstrap</button>
      </div>
      <RealRuntimeFacts config={config} />
    </section>
  );
}

function isSiteShellPath(pathname: string): boolean {
  return pathname === '/' || pathname === '/sites' || pathname.startsWith('/sites/');
}

function siteIdFromPathname(pathname: string): string | undefined {
  const segments = pathname.split('/').filter(Boolean);
  return segments[0] === 'sites' ? segments[1] : undefined;
}

function resolveSiteShellDecision(snapshot: ShellSnapshot, pathname: string): SiteShellDecision | undefined {
  if (!snapshot.principal || !isSiteShellPath(pathname)) return undefined;
  if (!snapshot.sites || snapshot.sites.state === 'checking') return { state: 'SITE_DISCOVERY_CHECKING' };
  if (snapshot.sites.state === 'forbidden') return { state: 'FORBIDDEN' };
  if (snapshot.sites.state === 'unavailable') {
    return { state: 'SITE_DISCOVERY_UNAVAILABLE', failure: snapshot.sites.failure };
  }
  const decision = resolveSiteRouting(
    pathname,
    snapshot.sites.items ?? [],
    snapshot.principal.authorization.capabilities,
  );
  if (decision.state === 'PLATFORM_ROUTE') return undefined;
  if (decision.state === 'READY' || decision.state === 'SITE_ROUTE_NOT_FOUND') {
    if (snapshot.siteTransition) return decision;
    const protectedScope = snapshot.protectedScope;
    if (protectedScope?.state === 'idle' && protectedScope.siteId === decision.context.site.id) {
      return decision;
    }
    return { state: 'SITE_SCOPE_ACTIVATING', context: decision.context };
  }
  return decision;
}

function normalizedRouteState(decision: SiteShellDecision | RouteDecision | undefined): string | undefined {
  if (!decision) return undefined;
  if (decision.state === 'SITE_DISCOVERY_UNAVAILABLE') return 'UNAVAILABLE';
  if (decision.state === 'SITE_ROUTE_NOT_FOUND') return 'NOT_FOUND';
  return decision.state;
}

export default function RealApp({ config }: RealAppProps) {
  const location = useLocation();
  const routerNavigate = useNavigate();
  const client = useMemo(() => createPlatformGatewayClient(), []);
  const setCurrentSiteId = useRealUiStore((state) => state.setCurrentSiteId);
  const observability = useRealObservability();
  const internalNavigationRef = useRef(false);
  const currentPathRef = useRef(location.pathname);
  currentPathRef.current = location.pathname;
  const routeNavigate = useCallback((target: string) => {
    const resolved = new URL(target, window.location.origin);
    if (resolved.origin === window.location.origin && !resolved.search && !resolved.hash) {
      if (resolved.pathname !== currentPathRef.current) internalNavigationRef.current = true;
      routerNavigate(resolved.pathname);
      return;
    }
    window.location.assign(target);
  }, [routerNavigate]);
  const runtime = useMemo(
    () => createShellRuntime(client, createBrowserShellEnvironment({ navigate: routeNavigate })),
    [client, routeNavigate],
  );
  const [snapshot, setSnapshot] = useState<ShellSnapshot>(() => runtime.current());
  const pathname = location.pathname;
  const currentLocation = `${location.pathname}${location.search}${location.hash}`;
  const previousLocationRef = useRef(currentLocation);

  useEffect(() => {
    const unsubscribe = runtime.subscribe(setSnapshot);
    void runtime.bootstrap(window.location.href);
    return () => {
      unsubscribe();
      runtime.dispose();
    };
  }, [runtime]);

  const navigate = useCallback((target: string) => {
    void runtime.requestSiteNavigation(target);
  }, [runtime]);
  const confirmSiteNavigation = useCallback(() => {
    void runtime.confirmSiteNavigation();
  }, [runtime]);
  const cancelSiteNavigation = useCallback(() => {
    runtime.cancelSiteNavigation();
  }, [runtime]);

  useEffect(() => {
    const previousLocation = previousLocationRef.current;
    previousLocationRef.current = currentLocation;
    if (previousLocation === currentLocation || internalNavigationRef.current) {
      internalNavigationRef.current = false;
      return;
    }

    const previousSiteId = siteIdFromPathname(new URL(previousLocation, window.location.origin).pathname);
    const nextSiteId = siteIdFromPathname(pathname);
    const activeSiteId = runtime.current().protectedScope?.siteId;
    if (previousSiteId && nextSiteId && previousSiteId !== nextSiteId && activeSiteId !== nextSiteId) {
      void runtime.requestSiteNavigation(currentLocation);
    }
  }, [currentLocation, pathname, runtime]);
  const registerProtectedResource = useCallback(
    (resource: Parameters<typeof runtime.registerProtectedResource>[0]) => runtime.registerProtectedResource(resource),
    [runtime],
  );
  const registerUnsavedDraft = useCallback(
    (draft: Parameters<typeof runtime.registerUnsavedDraft>[0]) => runtime.registerUnsavedDraft(draft),
    [runtime],
  );
  const protectedRequestToken = useCallback(() => runtime.protectedRequestToken(), [runtime]);
  const publishRealtimeStatus = useCallback(
    (update: Parameters<typeof runtime.publishRealtimeStatus>[0]) => {
      runtime.publishRealtimeStatus(update);
      observability.record({
        name: 'realtime_state',
        fields: { state: update.state, siteId: update.siteId },
      });
    },
    [observability, runtime],
  );

  const platformAvailability = snapshot.platform?.state ?? 'checking';
  const platformNavigation = snapshot.principal
    ? resolveNavigation(
      REAL_FEATURE_MANIFEST,
      snapshot.principal.authorization.capabilities,
      platformAvailability,
    )
    : [];
  const siteDecision = snapshot.state === 'READY'
    ? resolveSiteShellDecision(snapshot, pathname)
    : undefined;
  const platformDecision = snapshot.principal && !isSiteShellPath(pathname)
    ? resolveRoute(
      REAL_FEATURE_MANIFEST,
      pathname,
      snapshot.principal.authorization.capabilities,
      platformAvailability,
    )
    : undefined;
  const selectedSite = siteDecision?.state === 'READY'
    || siteDecision?.state === 'SITE_ROUTE_NOT_FOUND'
    || siteDecision?.state === 'SITE_SCOPE_ACTIVATING'
    ? siteDecision.context.site
    : undefined;
  const scopedPlatformNavigation = selectedSite
    ? platformNavigation.filter((item) => item.id === 'system')
    : platformNavigation;
  const navigation = selectedSite && snapshot.principal
    ? [
      ...scopedPlatformNavigation,
      ...buildSiteNavigation(selectedSite, snapshot.principal.authorization.capabilities),
    ]
    : platformNavigation;
  const redirectTarget = siteDecision?.state === 'REDIRECT' ? siteDecision.target : undefined;
  const routeState = normalizedRouteState(siteDecision ?? platformDecision);
  const showSignInPage = pathname === '/sign-in' || snapshot.state === 'LOGIN_REQUIRED';
  const displayedShellState = snapshot.state === 'READY' && routeState && routeState !== 'NOT_FOUND'
    ? routeState
    : snapshot.state;
  const siteToActivate = selectedSite?.id;

  useEffect(() => {
    setCurrentSiteId(selectedSite?.id ?? null);
    return () => setCurrentSiteId(null);
  }, [selectedSite?.id, setCurrentSiteId]);

  useEffect(() => {
    if (
      siteToActivate
      && snapshot.state === 'READY'
      && snapshot.protectedScope?.state === 'idle'
      && !snapshot.protectedScope.siteId
      && !snapshot.siteTransition
    ) {
      runtime.activateSiteScope(siteToActivate);
    }
  }, [runtime, siteToActivate, snapshot.protectedScope, snapshot.siteTransition, snapshot.state]);

  useEffect(() => {
    if (redirectTarget) void runtime.requestSiteNavigation(redirectTarget);
  }, [redirectTarget, runtime]);

  return (
    <main
      className={`real-shell-state${snapshot.state === 'READY' && !showSignInPage ? ' real-shell-state--authenticated' : ''}${showSignInPage ? ' real-shell-state--login' : ''}`}
      aria-label={REAL_SHELL_MARKER}
      data-build-graph={REAL_GRAPH_MARKER}
      data-shell-state={showSignInPage ? 'LOGIN_REQUIRED' : displayedShellState}
      data-route-state={showSignInPage ? 'LOGIN_REQUIRED' : routeState}
      data-protected-route-mounted={snapshot.state === 'READY' && !showSignInPage ? 'true' : 'false'}
    >
      {snapshot.state === 'BOOTSTRAPPING' && !showSignInPage ? <BootstrappingState /> : null}
      {showSignInPage ? <LoginRequiredState snapshot={snapshot} beginLogin={() => runtime.beginLogin()} /> : null}
      {snapshot.state === 'UNAVAILABLE' && !showSignInPage ? (
        <PrincipalUnavailableState config={config} snapshot={snapshot} retry={() => { void runtime.retry(); }} />
      ) : null}
      {snapshot.state === 'READY' && !showSignInPage && siteDecision ? (
        <SiteScopedShell
          config={config}
          snapshot={snapshot}
          navigation={navigation}
          decision={siteDecision}
          retry={() => { void runtime.retry(); }}
          logout={() => { void runtime.logout(); }}
          onNavigate={navigate}
          confirmSiteNavigation={confirmSiteNavigation}
          cancelSiteNavigation={cancelSiteNavigation}
          registerProtectedResource={registerProtectedResource}
          protectedRequestToken={protectedRequestToken}
          registerUnsavedDraft={registerUnsavedDraft}
          publishRealtimeStatus={publishRealtimeStatus}
        />
      ) : null}
      {snapshot.state === 'READY' && !showSignInPage && platformDecision ? (
        <AuthenticatedShell
          config={config}
          snapshot={snapshot}
          navigation={navigation}
          decision={platformDecision}
          retry={() => { void runtime.retry(); }}
          logout={() => { void runtime.logout(); }}
          onNavigate={navigate}
          confirmSiteNavigation={confirmSiteNavigation}
          cancelSiteNavigation={cancelSiteNavigation}
        />
      ) : null}
    </main>
  );
}
