import { useEffect, useMemo, useState } from 'react';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import { AuthenticatedShell } from './AuthenticatedShell';
import { REAL_FEATURE_MANIFEST } from './feature-manifest';
import { RealRuntimeFacts } from './RealRuntimeFacts';
import { resolveNavigation, resolveRoute, type RouteDecision } from './route-policy';
import { createShellRuntime, type ShellSnapshot } from './shell-runtime';
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
        <h1 id="real-config-title">Real 配置无效</h1>
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

function BootstrappingState({ config }: { config: RealRuntimeConfig }) {
  return (
    <section className="real-shell-card" aria-labelledby="real-bootstrap-title" data-testid="real-shell-bootstrapping">
      <p className="real-shell-eyebrow">REAL MODE · BOOTSTRAPPING</p>
      <h1 id="real-bootstrap-title">正在建立可信会话</h1>
      <p>Shell 正在读取服务器 Principal 与 Session。完成前不会挂载业务路由或 realtime 订阅。</p>
      <div className="real-shell-progress" role="status" aria-live="polite">正在验证身份边界…</div>
      <RealRuntimeFacts config={config} />
    </section>
  );
}

function LoginRequiredState({
  config,
  snapshot,
  beginLogin,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  beginLogin: () => void;
}) {
  const loggedOut = snapshot.reason === 'LOGOUT_COMPLETED' || snapshot.reason === 'SESSION_ALREADY_INVALID';
  return (
    <section className="real-shell-card" aria-labelledby="real-login-title" data-testid="real-shell-login-required">
      <p className="real-shell-eyebrow">REAL MODE · LOGIN REQUIRED</p>
      <h1 id="real-login-title">{loggedOut ? '服务器 Session 已撤销' : '需要登录'}</h1>
      <p>
        {loggedOut
          ? '受保护内存已清除。重新进入时将使用 Gateway 发起 OIDC 登录。'
          : '未发现可用的 BFF Session。应用不会在浏览器中收集用户名、密码或令牌。'}
      </p>
      <div className="real-shell-actions">
        <button type="button" onClick={beginLogin}>通过身份提供方登录</button>
      </div>
      <RealRuntimeFacts config={config} />
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
      <h1 id="real-unavailable-title">无法建立可信 Principal</h1>
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
  return decision.state === 'PLATFORM_ROUTE' ? undefined : decision;
}

function normalizedRouteState(decision: SiteShellDecision | RouteDecision | undefined): string | undefined {
  if (!decision) return undefined;
  if (decision.state === 'SITE_DISCOVERY_UNAVAILABLE') return 'UNAVAILABLE';
  if (decision.state === 'SITE_ROUTE_NOT_FOUND') return 'NOT_FOUND';
  return decision.state;
}

export default function RealApp({ config }: RealAppProps) {
  const client = useMemo(() => createPlatformGatewayClient(), []);
  const runtime = useMemo(() => createShellRuntime(client), [client]);
  const [snapshot, setSnapshot] = useState<ShellSnapshot>(() => runtime.current());

  useEffect(() => {
    const unsubscribe = runtime.subscribe(setSnapshot);
    void runtime.bootstrap(window.location.href);
    return () => {
      unsubscribe();
      runtime.dispose();
    };
  }, [runtime]);

  const pathname = window.location.pathname;
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
  const selectedSite = siteDecision?.state === 'READY' || siteDecision?.state === 'SITE_ROUTE_NOT_FOUND'
    ? siteDecision.context.site
    : undefined;
  const navigation = selectedSite && snapshot.principal
    ? [
      ...platformNavigation,
      ...buildSiteNavigation(selectedSite, snapshot.principal.authorization.capabilities),
    ]
    : platformNavigation;
  const redirectTarget = siteDecision?.state === 'REDIRECT' ? siteDecision.target : undefined;
  const routeState = normalizedRouteState(siteDecision ?? platformDecision);
  const displayedShellState = snapshot.state === 'READY' && routeState && routeState !== 'NOT_FOUND'
    ? routeState
    : snapshot.state;

  useEffect(() => {
    if (redirectTarget) window.location.replace(redirectTarget);
  }, [redirectTarget]);

  return (
    <main
      className={`real-shell-state${snapshot.state === 'READY' ? ' real-shell-state--authenticated' : ''}`}
      aria-label={REAL_SHELL_MARKER}
      data-build-graph={REAL_GRAPH_MARKER}
      data-shell-state={displayedShellState}
      data-route-state={routeState}
      data-protected-route-mounted={snapshot.state === 'READY' ? 'true' : 'false'}
    >
      {snapshot.state === 'BOOTSTRAPPING' ? <BootstrappingState config={config} /> : null}
      {snapshot.state === 'LOGIN_REQUIRED' ? (
        <LoginRequiredState config={config} snapshot={snapshot} beginLogin={() => runtime.beginLogin()} />
      ) : null}
      {snapshot.state === 'UNAVAILABLE' ? (
        <PrincipalUnavailableState config={config} snapshot={snapshot} retry={() => { void runtime.retry(); }} />
      ) : null}
      {snapshot.state === 'READY' && siteDecision ? (
        <SiteScopedShell
          config={config}
          snapshot={snapshot}
          navigation={navigation}
          decision={siteDecision}
          retry={() => { void runtime.retry(); }}
          logout={() => { void runtime.logout(); }}
        />
      ) : null}
      {snapshot.state === 'READY' && platformDecision ? (
        <AuthenticatedShell
          config={config}
          snapshot={snapshot}
          navigation={navigation}
          decision={platformDecision}
          retry={() => { void runtime.retry(); }}
          logout={() => { void runtime.logout(); }}
        />
      ) : null}
    </main>
  );
}
