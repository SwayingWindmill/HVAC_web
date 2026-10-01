import { useMemo, type ReactNode } from 'react';
import { useLocation } from '@tanstack/react-router';
import { AppHeader } from '@/components/layout/AppHeader';
import { AppSidebar } from '@/components/layout/AppSidebar';
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
  const location = useLocation();
  const { resolvedMode: themeMode, setMode: setThemeMode } = useAppTheme();
  const principal = snapshot.principal!;
  const transition = snapshot.siteTransition;
  const protectedScope = snapshot.protectedScope;
  const activeSite = protectedScope?.siteId ? snapshot.sites?.items?.find((candidate) => candidate.id === protectedScope.siteId) : undefined;
  const realtime = snapshot.realtime ?? createIdleRealtimeStatus();
  const realtimePresentation = realtimeStatusPresentation(realtime);
  const transitionBlocksContent = transition?.status === 'purging' || transition?.status === 'failed';
  const siteLabel = activeSite?.displayName ?? (transitionBlocksContent ? '暂无活动站点' : '平台范围');
  const principalRole = principalRoleLabel(principal.principal.roles[0]);
  const defaultSidebarOpen = useMemo(sidebarDefaultOpen, []);

  const navigate = (target: string) => {
    void runtime.requestSiteNavigation(target);
  };

  const retryHref = `${location.pathname}${location.searchStr}${location.hash}`;

  return (
    <SidebarProvider defaultOpen={defaultSidebarOpen}>
      <AppSidebar
        title="智慧能源 SaaS 平台"
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
          principalName={principal.principal.displayName}
          principalSubject={principal.principal.subject}
          principalRole={principalRole}
          themeMode={themeMode}
          onThemeToggle={() => setThemeMode(themeMode === 'dark' ? 'light' : 'dark')}
          onNavigate={navigate}
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
