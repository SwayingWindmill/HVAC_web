import { useEffect, useMemo, useState } from 'react';
import { RouterProvider } from '@tanstack/react-router';
import type { QueryClient } from '@tanstack/react-query';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import { BrandMark } from '@/components/brand-mark';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { createFrontendReviewPlatformClient } from './frontend-review-platform-client';
import { useObservability } from './Observability';
import { RouteLoading } from './RouteLoading';
import { RuntimeFacts } from './RuntimeFacts';
import { SignInForm, SignInNotice, readSignInRequest } from './SignInPage';
import { FocusHeading } from './FocusHeading';
import { ShellRuntimeProvider } from './ShellRuntimeContext';
import { createBrowserShellEnvironment, createShellRuntime, type ShellSnapshot } from './shell-runtime';
import { shouldInvalidateRouter } from './shell-policy';
import { createHvacRouter } from './router';
import type { RuntimeConfig, RuntimeConfigFailure } from './runtime-config';

const WEB_GRAPH_MARKER = 'HVAC_WEB_AUTHORITATIVE_GRAPH_V1';
const WEB_SHELL_MARKER = 'AUTHORITATIVE WEB SHELL';

export function ConfigurationBlocked({ failures }: { failures: RuntimeConfigFailure[] }) {
  return (
    <main
      className="grid min-h-svh place-items-center bg-background p-6"
      data-build-graph={WEB_GRAPH_MARKER}
      data-shell-state="UNAVAILABLE"
    >
      <section
        className="flex w-full max-w-md flex-col gap-4 rounded-xl border border-border bg-card p-6 shadow-xs"
        aria-labelledby="real-config-title"
      >
        <div className="flex items-center gap-2.5 text-primary">
          <BrandMark className="size-6 shrink-0" />
          <p className="text-xs font-medium text-destructive-text">无法启动</p>
        </div>
        <div className="space-y-1">
          <FocusHeading id="real-config-title" className="text-lg font-semibold tracking-tight text-foreground">
            运行配置无效
          </FocusHeading>
          <p className="text-sm text-muted-foreground">请联系管理员检查当前部署配置。</p>
        </div>
        <ul className="divide-y divide-divider rounded-lg border border-border bg-muted/40">
          {failures.map((failure) => (
            <li key={failure.code} className="flex flex-col gap-0.5 px-3.5 py-2.5">
              <strong className="font-mono text-xs font-medium text-foreground">{failure.code}</strong>
              <span className="text-xs text-muted-foreground">{failure.detail}</span>
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
    <section
      className="flex w-full max-w-md flex-col gap-4 rounded-xl border border-border bg-card p-6 shadow-xs"
      aria-labelledby="real-unavailable-title"
      data-testid="real-shell-unavailable"
    >
      <div className="flex items-center gap-2.5 text-primary">
        <BrandMark className="size-6 shrink-0" />
        <p className="text-xs font-medium text-muted-foreground">登录身份</p>
      </div>
      <div className="space-y-1">
        <FocusHeading id="real-unavailable-title" className="text-lg font-semibold tracking-tight text-foreground">
          无法确认登录身份
        </FocusHeading>
        <p className="text-sm text-muted-foreground">暂时无法验证当前登录会话，请稍后重试。</p>
      </div>
      {snapshot.failure ? (
        <Alert variant="destructive" data-retryable={String(snapshot.failure.retryable)}>
          <AlertTitle>身份服务暂时不可用</AlertTitle>
          <AlertDescription>连接恢复后点击重试即可继续使用。</AlertDescription>
        </Alert>
      ) : null}
      <div className="flex items-center gap-2">
        <Button type="button" onClick={retry}>重试</Button>
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
    let previous = resources.runtime.current();
    const unsubscribe = resources.runtime.subscribe((next) => {
      setSnapshot(next);
      if (shouldInvalidateRouter(previous, next)) void resources.router.invalidate();
      previous = next;
    });
    void resources.runtime.bootstrap(window.location.href);
    return () => {
      unsubscribe();
      resources.runtime.dispose();
    };
  }, [resources]);

  const showSignInPage = window.location.pathname === '/sign-in' || snapshot.state === 'LOGIN_REQUIRED';
  const waitingForSites = snapshot.state === 'READY' && !showSignInPage && snapshot.sites?.state === 'checking';
  const bootstrapping = (snapshot.state === 'BOOTSTRAPPING' || waitingForSites) && !showSignInPage;
  const shellReady = snapshot.state === 'READY' && !waitingForSites && !showSignInPage;
  const centeredUnavailable = snapshot.state === 'UNAVAILABLE' && !showSignInPage;
  // The authenticated shell's SidebarInset is the page's main landmark; other states are their own.
  const Root = shellReady ? 'div' : 'main';
  return (
    <Root
      className={cn(
        'min-h-svh',
        centeredUnavailable && 'grid place-items-center bg-background p-6',
      )}
      aria-label={Root === 'main' ? WEB_SHELL_MARKER : undefined}
      data-build-graph={WEB_GRAPH_MARKER}
      data-shell-state={showSignInPage ? 'LOGIN_REQUIRED' : waitingForSites ? 'BOOTSTRAPPING' : snapshot.state}
      data-protected-route-mounted={shellReady ? 'true' : 'false'}
    >
      {bootstrapping ? (
        <RouteLoading label="正在加载系统" testId="real-shell-bootstrapping" routeState="BOOTSTRAPPING" variant="shell" />
      ) : null}
      {showSignInPage ? <LoginRequiredState snapshot={snapshot} beginLogin={() => resources.runtime.beginLogin()} /> : null}
      {snapshot.state === 'UNAVAILABLE' && !showSignInPage ? (
        <PrincipalUnavailableState config={config} snapshot={snapshot} retry={() => { void resources.runtime.retry(); }} />
      ) : null}
      {shellReady ? (
        <ShellRuntimeProvider runtime={resources.runtime}>
          <RouterProvider router={resources.router} />
        </ShellRuntimeProvider>
      ) : null}
    </Root>
  );
}
