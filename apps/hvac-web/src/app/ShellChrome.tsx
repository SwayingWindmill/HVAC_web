import { useMemo, type ReactNode } from 'react';
import { useLocation, useRouter } from '@tanstack/react-router';
import type { Capability } from '@/api/generated/platformGateway.gen';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
import { buildAppNavigation } from '@/components/layout/app-navigation';
import { useShellNotifications } from '@/components/layout/use-shell-notifications';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { matchSurface, surfacePath, WIREFRAME_READY_SURFACE_IDS } from './surface-catalog';
import { createIdleRealtimeStatus, realtimeStatusPresentation } from './realtime-status';
import { useAppTheme } from './ThemeGate';
import type { ShellSnapshot } from './shell-runtime';
import { useShellRuntime, useShellSnapshot } from './ShellRuntimeContext';

function DraftConfirmation({ snapshot, confirm, cancel }: {
  readonly snapshot: ShellSnapshot;
  readonly confirm: () => void;
  readonly cancel: () => void;
}) {
  const transition = snapshot.siteTransition;
  if (!transition || transition.status !== 'confirmation-required') return null;

  return (
    <Dialog open onOpenChange={(open) => { if (!open) cancel(); }}>
      <DialogContent showCloseButton={false} data-testid="real-site-draft-confirmation">
        <DialogHeader>
          <DialogTitle>切换站点会丢弃未保存内容</DialogTitle>
          <DialogDescription>确认后，系统会结束当前站点的临时操作状态，再进入目标站点。</DialogDescription>
        </DialogHeader>
        {transition.dirtyDrafts?.length ? (
          <div className="rounded-md border bg-muted/30 px-3 py-2.5 text-sm">
            <p className="mb-2 font-medium">尚未保存</p>
            <ul className="space-y-1 text-muted-foreground">
              {transition.dirtyDrafts.map((draft) => <li key={draft.id}>• {draft.label}</li>)}
            </ul>
          </div>
        ) : null}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={cancel} data-testid="real-site-draft-cancel">留在当前站点</Button>
          <Button type="button" variant="destructive" onClick={confirm} data-testid="real-site-draft-confirm">丢弃并切换</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

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

function principalRoleLabel(role: string | undefined): string {
  if (!role) return '授权用户';
  const normalized = role.trim().toLowerCase();
  if (normalized === 'admin' || normalized === 'administrator') return '管理员';
  if (normalized === 'operator') return '运维员';
  if (normalized === 'viewer') return '查看员';
  return role;
}

export function ShellChrome({ children }: { readonly children: ReactNode }) {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const router = useRouter();
  const location = useLocation();
  const { resolvedMode: themeMode, setMode: setThemeMode } = useAppTheme();
  const principal = snapshot.principal!;
  const pathname = location.pathname;
  const transition = snapshot.siteTransition;
  const protectedScope = snapshot.protectedScope;
  const activeSite = protectedScope?.siteId ? snapshot.sites?.items?.find((candidate) => candidate.id === protectedScope.siteId) : undefined;
  const realtime = snapshot.realtime ?? createIdleRealtimeStatus();
  const realtimePresentation = realtimeStatusPresentation(realtime);
  const transitionBlocksContent = transition?.status === 'purging' || transition?.status === 'failed';
  const siteLabel = activeSite?.displayName ?? (transitionBlocksContent ? '暂无活动站点' : '平台范围');
  const capabilities = useMemo(() => new Set<string>(principal.authorization.capabilities), [principal.authorization.capabilities]);
  const appNavigation = useMemo(() => buildAppNavigation({ siteId: activeSite?.id, capabilities }), [activeSite?.id, capabilities]);
  const currentSurface = matchSurface(pathname);
  const pageTitle = currentSurface?.title ?? '智慧能源';
  const principalRole = principalRoleLabel(principal.principal.roles[0]);
  const sites = snapshot.sites?.items ?? [];

  const defaultSidebarOpen = useMemo(sidebarDefaultOpen, []);

  const notifications = useMemo(() => {
    const platformState = snapshot.platform?.state ?? 'checking';
    return Object.values(router.routesById).find((route) => {
      if (route.fullPath !== '/notifications') return false;
      const required = route.options.staticData?.requiredCapabilities ?? [];
      if (!required.every((capability: Capability) => capabilities.has(capability))) return false;
      return !route.options.staticData?.requiresPlatform || (platformState !== 'checking' && platformState !== 'unavailable');
    });
  }, [capabilities, router.routesById, snapshot.platform?.state]);
  const notificationInbox = useShellNotifications(Boolean(notifications), principal.principal.subject);

  const navigate = (target: string) => {
    void runtime.requestSiteNavigation(target);
  };

  const changeSite = (siteId: string) => {
    const site = sites.find((candidate) => candidate.id === siteId);
    if (!site) return;
    if (currentSurface?.scope === 'site' && WIREFRAME_READY_SURFACE_IDS.has(currentSurface.id)) {
      navigate(`${surfacePath(currentSurface.id, { siteId: site.id })}${location.searchStr}`);
      return;
    }
    navigate(surfacePath('03', { siteId: site.id }));
  };

  const retryHref = `${location.pathname}${location.searchStr}${location.hash}`;

  return (
    <SidebarProvider defaultOpen={defaultSidebarOpen}>
      <AppSidebar
        title="泉来禾智慧能源"
        pathname={pathname}
        groups={appNavigation.groups}
        siteId={activeSite?.id}
        siteLabel={siteLabel}
        siteOptions={sites.map((site) => ({ value: site.id, label: site.displayName }))}
        onSiteChange={changeSite}
        onNavigate={navigate}
      />
      <SidebarInset
        className="min-w-0"
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
        <AppHeader
          pageTitle={pageTitle}
          pageTitleAsHeading={currentSurface?.pattern !== 'detail'}
          scopeLabel={siteLabel}
          navigation={appNavigation.entries}
          principalName={principal.principal.displayName}
          principalRole={principalRole}
          themeMode={themeMode}
          submittingLogout={snapshot.logout?.status === 'submitting'}
          notificationCount={notificationInbox.count}
          notificationLabel={notifications ? '打开通知中心' : '当前账号无通知中心访问权限'}
          notificationDisabled={!notifications}
          realtimeLabel={realtimePresentation.label}
          realtimeState={realtime.state}
          onNavigate={navigate}
          onNotificationOpen={() => { if (notifications) navigate('/notifications'); }}
          onThemeToggle={() => setThemeMode(themeMode === 'dark' ? 'light' : 'dark')}
          onLogout={() => { void runtime.logout(); }}
        />

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

        <DraftConfirmation
          snapshot={snapshot}
          confirm={() => { void runtime.confirmSiteNavigation(); }}
          cancel={() => runtime.cancelSiteNavigation()}
        />

        {transition?.status === 'purging' ? <TransitionSurface state="purging" /> : null}
        {transition?.status === 'failed' ? <TransitionSurface state="failed" detail={transition.failure?.detail} retryHref={retryHref} /> : null}
        {!transitionBlocksContent ? <div className="min-w-0 flex-1">{children}</div> : null}
      </SidebarInset>
    </SidebarProvider>
  );
}
