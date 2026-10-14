import { useEffect, useMemo, useState } from 'react';
import { RouterProvider } from '@tanstack/react-router';
import type { QueryClient } from '@tanstack/react-query';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import { createFrontendReviewPlatformClient } from './frontend-review-platform-client';
import { useObservability } from './Observability';
import { RouteLoading } from './RouteLoading';
import { RuntimeFacts } from './RuntimeFacts';
import { SignInForm, SignInNotice, readSignInRequest } from './SignInPage';
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
  if (window.location.pathname === '/sign-in') {
    const request = readSignInRequest(window.location.search);
    if (request.kind === 'form') return <SignInForm request={request} />;
    if (request.kind === 'expired') {
      return <SignInNotice title="登录已超时" description="登录页打开太久了，请重新登录。" action="重新登录" onAction={beginLogin} />;
    }
  }
  if (snapshot.reason === 'LOGOUT_COMPLETED') {
    return <SignInNotice title="已退出登录" description="这台电脑上的业务数据已经清除。" action="重新登录" onAction={beginLogin} />;
  }
  if (snapshot.reason === 'SESSION_ALREADY_INVALID') {
    return <SignInNotice title="登录已失效" description="请重新登录后继续使用。" action="重新登录" onAction={beginLogin} />;
  }
  if (window.location.pathname === '/sign-in' && snapshot.state === 'LOGIN_REQUIRED') {
    return <SignInNotice title="登录" description="使用平台账号登录。" action="登录" onAction={beginLogin} />;
  }
  return (
    <RouteLoading
      label="正在前往登录"
      testId="real-shell-auth-redirect"
      routeState="LOGIN_REQUIRED"
      variant="shell"
    />
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
    const client = __HVAC_WEB_FRONTEND_REVIEW__
      ? createFrontendReviewPlatformClient()
      : createPlatformGatewayClient();
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
  // The authenticated shell's SidebarInset is the page's main landmark; other states are their own.
  const Root = snapshot.state === 'READY' && !showSignInPage ? 'div' : 'main';
  return (
    <Root
      className={`real-shell-state${snapshot.state === 'READY' && !showSignInPage ? ' real-shell-state--authenticated' : ''}${showSignInPage ? ' real-shell-state--login' : ''}`}
      aria-label={Root === 'main' ? WEB_SHELL_MARKER : undefined}
      data-build-graph={WEB_GRAPH_MARKER}
      data-shell-state={showSignInPage ? 'LOGIN_REQUIRED' : snapshot.state}
      data-protected-route-mounted={snapshot.state === 'READY' && !showSignInPage ? 'true' : 'false'}
    >
      {snapshot.state === 'BOOTSTRAPPING' && !showSignInPage ? (
        <RouteLoading label="正在加载系统" testId="real-shell-bootstrapping" routeState="BOOTSTRAPPING" variant="shell" />
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
    </Root>
  );
}
