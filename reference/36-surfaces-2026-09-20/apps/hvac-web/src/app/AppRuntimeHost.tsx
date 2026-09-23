import { useEffect, useMemo, useState } from 'react';
import { RouterProvider } from '@tanstack/react-router';
import type { QueryClient } from '@tanstack/react-query';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import { useObservability } from './Observability';
import { RouteLoading } from './RouteLoading';
import { RuntimeFacts } from './RuntimeFacts';
import { FocusHeading } from './FocusHeading';
import { ShellRuntimeProvider } from './ShellRuntimeContext';
import { createBrowserShellEnvironment, createShellRuntime, type ShellSnapshot } from './shell-runtime';
import { createHvacRouter } from './router';
import type { RuntimeConfig, RuntimeConfigFailure } from './runtime-config';

const WEB_GRAPH_MARKER = 'HVAC_WEB_AUTHORITATIVE_GRAPH_V1';
const WEB_SHELL_MARKER = 'AUTHORITATIVE WEB SHELL';

export function ConfigurationBlocked({ failures }: { failures: RuntimeConfigFailure[] }) {
  return (
    <main className="real-shell-state" data-build-graph={WEB_GRAPH_MARKER} data-shell-state="UNAVAILABLE">
      <section className="real-shell-card" aria-labelledby="real-config-title">
        <p className="real-shell-eyebrow">STARTUP BLOCKED</p>
        <FocusHeading id="real-config-title">运行配置无效</FocusHeading>
        <p>应用已按失败关闭策略停止，未挂载业务路由或任何本地替代数据源。</p>
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

function LoginRequiredState({ snapshot, beginLogin }: { snapshot: ShellSnapshot; beginLogin: () => void }) {
  const loggedOut = snapshot.reason === 'LOGOUT_COMPLETED' || snapshot.reason === 'SESSION_ALREADY_INVALID';
  if (!loggedOut) {
    return (
      <RouteLoading
        label="正在前往登录"
        testId="real-shell-auth-redirect"
        routeState="LOGIN_REQUIRED"
        variant="shell"
      />
    );
  }

  return (
    <section className="real-login-shell" data-testid="real-shell-login-required">
      <div className="real-login-panel">
        <div className="real-login-brand">
          <img src="/quanlaihe-mark.svg" alt="" width="36" height="36" />
          <strong>泉来禾智慧能源</strong>
        </div>
        <p className="real-shell-eyebrow">账户安全</p>
        <FocusHeading>{snapshot.reason === 'SESSION_ALREADY_INVALID' ? '登录已失效' : '已安全退出'}</FocusHeading>
        <p>受保护的业务数据已从当前浏览器内存清除。重新验证身份后可继续进入能源运营工作台。</p>
        <div className="real-shell-actions">
          <button type="button" onClick={beginLogin}>重新登录</button>
        </div>
      </div>
    </section>
  );
}

function PrincipalUnavailableState({ config, snapshot, retry }: { config: RuntimeConfig; snapshot: ShellSnapshot; retry: () => void }) {
  return (
    <section className="real-shell-card" aria-labelledby="real-unavailable-title" data-testid="real-shell-unavailable">
      <p className="real-shell-eyebrow">PRINCIPAL UNAVAILABLE</p>
      <FocusHeading id="real-unavailable-title">无法建立可信 Principal</FocusHeading>
      <p>业务路由保持未挂载。系统不会使用本地替代数据或缓存身份。</p>
      {snapshot.failure ? (
        <div className="real-shell-problem" role="alert" data-retryable={String(snapshot.failure.retryable)}>
          <strong>身份服务暂时不可用</strong>
          <span>请稍后重试；系统不会使用缓存身份进入业务页面。</span>
        </div>
      ) : null}
      <div className="real-shell-actions">
        <button type="button" onClick={retry}>重试 Principal bootstrap</button>
      </div>
      <RuntimeFacts config={config} />
    </section>
  );
}

export function AppRuntimeHost({ config, queryClient }: { config: RuntimeConfig; queryClient: QueryClient }) {
  const observability = useObservability();
  const resources = useMemo(() => {
    const client = createPlatformGatewayClient();
    let routerNavigate: (target: string) => void = (target) => window.location.assign(target);
    const runtime = createShellRuntime(client, createBrowserShellEnvironment({
      navigate: (target) => routerNavigate(target),
    }));
    const publishRealtimeStatus = (update: Parameters<typeof runtime.publishRealtimeStatus>[0]) => {
      runtime.publishRealtimeStatus(update);
      observability.record({
        name: 'realtime_state',
        fields: { state: update.state, siteId: update.siteId },
      });
    };
    const router = createHvacRouter({ config, queryClient, runtime, publishRealtimeStatus });
    routerNavigate = (target) => {
      void router.navigate({ href: target, ignoreBlocker: true });
    };
    return { runtime, router };
  }, [config, observability, queryClient]);
  const [snapshot, setSnapshot] = useState<ShellSnapshot>(() => resources.runtime.current());

  useEffect(() => {
    const unsubscribe = resources.runtime.subscribe((next) => {
      setSnapshot(next);
      if (next.state === 'READY') void resources.router.invalidate();
    });
    void resources.runtime.bootstrap(window.location.href);
    return () => {
      unsubscribe();
      resources.runtime.dispose();
    };
  }, [resources]);

  const showSignInPage = window.location.pathname === '/sign-in' || snapshot.state === 'LOGIN_REQUIRED';
  return (
    <main
      className={`real-shell-state${snapshot.state === 'READY' && !showSignInPage ? ' real-shell-state--authenticated' : ''}${showSignInPage ? ' real-shell-state--login' : ''}`}
      aria-label={WEB_SHELL_MARKER}
      data-build-graph={WEB_GRAPH_MARKER}
      data-shell-state={showSignInPage ? 'LOGIN_REQUIRED' : snapshot.state}
      data-protected-route-mounted={snapshot.state === 'READY' && !showSignInPage ? 'true' : 'false'}
    >
      {snapshot.state === 'BOOTSTRAPPING' && !showSignInPage ? (
        <RouteLoading label="正在进入工作台" testId="real-shell-bootstrapping" routeState="BOOTSTRAPPING" variant="shell" />
      ) : null}
      {showSignInPage ? <LoginRequiredState snapshot={snapshot} beginLogin={() => resources.runtime.beginLogin()} /> : null}
      {snapshot.state === 'UNAVAILABLE' && !showSignInPage ? (
        <PrincipalUnavailableState config={config} snapshot={snapshot} retry={() => { void resources.runtime.retry(); }} />
      ) : null}
      {snapshot.state === 'READY' && !showSignInPage ? (
        <ShellRuntimeProvider runtime={resources.runtime}>
          <RouterProvider router={resources.router} />
        </ShellRuntimeProvider>
      ) : null}
    </main>
  );
}
