import { useMemo, type ReactNode } from 'react';
import { primaryRoleLabel } from '@/lib/role-labels';
import { useLocation, useRouter } from '@tanstack/react-router';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
import { NavigationProgress, SkipToMain } from '@/components/layout/NavigationProgress';
import { Toaster } from '@/components/ui/sonner';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { AssistantPanel } from '@/features/assistant/components/AssistantPanel';
import { useAssistantPanel } from '@/features/assistant/assistant-panel-store';
import { createIdleRealtimeStatus, realtimeStatusPresentation } from './realtime-status';
import { useAppTheme } from './ThemeGate';
import { useShellRuntime, useShellSnapshot } from './ShellRuntimeContext';

function TransitionSurface({ state, detail, retryHref }: { readonly state: 'purging' | 'failed'; readonly detail?: string; readonly retryHref?: string }) {
  if (state === 'purging') {
    return (
      <div className="mx-auto grid min-h-[55vh] w-full max-w-2xl place-items-center px-6 text-center" data-testid="real-site-purging" data-route-state="PURGING">
        <div>
          <p className="text-sm font-medium text-muted-foreground">站点切换</p>
          <h1 className="mt-2 text-2xl font-semibold tracking-tight">正在切换站点</h1>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">正在结束当前站点的临时操作并载入目标站点。</p>
          <div className="mt-5 text-sm text-muted-foreground" role="status" aria-live="polite">正在切换…</div>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto grid min-h-[55vh] w-full max-w-2xl place-items-center px-6 text-center" data-testid="real-site-purge-failed" data-route-state="UNAVAILABLE">
      <div>
        <p className="text-sm font-medium text-destructive">站点切换未完成</p>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight">无法安全切换站点</h1>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">为避免显示错误站点的数据，系统已经停止切换。{detail ? ` ${detail}` : ''}</p>
        {retryHref ? <Button className="mt-5" asChild><a href={retryHref}>重新尝试</a></Button> : null}
      </div>
    </div>
  );
}

function sidebarDefaultOpen(): boolean {
  if (typeof document === 'undefined') return true;
  const value = document.cookie
    .split('; ')
    .find((entry) => entry.startsWith('sidebar_state='))
    ?.split('=')[1];
  return value !== 'false';
}


export function ShellChrome({ children }: { readonly children: ReactNode }) {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const location = useLocation();
  const { resolvedMode: themeMode } = useAppTheme();
  const principal = snapshot.principal!;
  const transition = snapshot.siteTransition;
  const protectedScope = snapshot.protectedScope;
  const activeSite = protectedScope?.siteId ? snapshot.sites?.items?.find((candidate) => candidate.id === protectedScope.siteId) : undefined;
  const realtime = snapshot.realtime ?? createIdleRealtimeStatus();
  const realtimePresentation = realtimeStatusPresentation(realtime);
  const transitionBlocksContent = transition !== undefined;
  const siteLabel = activeSite?.displayName ?? (transitionBlocksContent ? '暂无活动站点' : '平台范围');
  const principalRole = primaryRoleLabel(principal.principal.roles);
  const defaultSidebarOpen = useMemo(sidebarDefaultOpen, []);
  const assistantOpen = useAssistantPanel((state) => state.open);
  const toggleAssistant = useAssistantPanel((state) => state.toggle);

  const router = useRouter();
  // Through the router, so a page's unsaved-draft blocker sees it; the _app blocker hands leaving the Site scope to the runtime.
  const navigate = (target: string) => {
    void router.navigate({ href: target });
  };

  const retryHref = `${location.pathname}${location.searchStr}${location.hash}`;

  return (
    <SidebarProvider defaultOpen={defaultSidebarOpen} className="flex-col">
      <SkipToMain />
      <NavigationProgress />
      <AppHeader
        siteId={activeSite?.id}
        principalName={principal.principal.displayName}
        principalRole={principalRole}
        csrfToken={principal.session.csrfToken}
        onNavigate={navigate}
        onLogout={() => { void runtime.logout(); }}
        assistant={activeSite === undefined ? undefined : { open: assistantOpen, onToggle: toggleAssistant }}
      />
      <div className="flex flex-1">
      <AppSidebar
        className="top-(--header-height) h-[calc(100svh-var(--header-height))]! print:hidden"
        onNavigate={navigate}
      />
      <SidebarInset
        className="min-w-0 print:m-0 print:rounded-none print:border-0 print:shadow-none"
        data-testid="real-protected-shell"
        data-protected-route-mounted="true"
        data-policy-revision={principal.authorization.policyRevision}
        data-capability-count={String(principal.authorization.capabilities.length)}
        data-protected-scope-state={protectedScope?.state ?? 'idle'}
        data-protected-scope-site={protectedScope?.siteId}
        data-protected-scope-generation={String(protectedScope?.generation ?? 0)}
        data-protected-resource-count={String(protectedScope?.resourceCount ?? 0)}
        data-site-transition={transition?.status ?? 'idle'}
        data-realtime-state={realtime.state}
        data-realtime-site={realtime.siteId}
      >
        <span className="sr-only" role="status" aria-live="polite" data-testid="real-realtime-status" data-realtime-state={realtime.state} data-realtime-site={realtime.siteId}>
          {realtimePresentation.label}
          <span data-testid="real-realtime-code" data-code={realtimePresentation.code} aria-hidden="true" />
        </span>
        <span className="sr-only" data-testid="real-shell-principal">{principal.principal.displayName}</span>
        <span className="sr-only" data-testid="real-principal-roles">{principalRole}</span>
        <span className="sr-only" data-testid="real-shell-site">{siteLabel}</span>

        {snapshot.logout?.status === 'failed' ? (
          <Alert variant="destructive" className="mx-4 mt-4 w-auto" data-testid="real-logout-failure">
            <AlertTitle>退出登录失败</AlertTitle>
            <AlertDescription>{snapshot.logout.detail}</AlertDescription>
          </Alert>
        ) : null}

        {transition?.status === 'purging' ? <TransitionSurface state="purging" /> : null}
        {transition?.status === 'failed' ? <TransitionSurface state="failed" detail={transition.failure?.detail} retryHref={retryHref} /> : null}
        {!transitionBlocksContent ? <div id="content" tabIndex={-1} className="min-w-0 flex-1 outline-none">{children}</div> : null}
      </SidebarInset>
      {assistantOpen && activeSite !== undefined && !transitionBlocksContent ? (
        <AssistantPanel key={activeSite.id} siteId={activeSite.id} siteName={activeSite.displayName} timezone={activeSite.timezone} csrfToken={principal.session.csrfToken} />
      ) : null}
      </div>
      <Toaster theme={themeMode} position="bottom-right" richColors closeButton />
    </SidebarProvider>
  );
}
