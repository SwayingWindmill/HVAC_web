import { useEffect, useRef, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react';
import { FocusHeading } from './FocusHeading';
import { createIdleRealtimeStatus, realtimeStatusLabel } from './realtime-status';
import { RealRuntimeFacts } from './RealRuntimeFacts';
import type { RealNavigationItem } from './route-policy';
import type { RealRuntimeConfig } from './runtime-config';
import type { ShellSnapshot } from './shell-runtime';

function ShellNavigation({
  items,
  pathname,
  onNavigate,
}: {
  items: RealNavigationItem[];
  pathname: string;
  onNavigate?: (target: string) => void;
}) {
  const navigate = (event: MouseEvent<HTMLAnchorElement>, path: string) => {
    if (!onNavigate) return;
    event.preventDefault();
    onNavigate(path);
  };

  return (
    <nav className="real-shell-navigation" aria-label="Real navigation" data-testid="real-navigation">
      {items.map((item) => (
        <a
          key={item.id}
          href={item.path}
          data-feature-id={item.id}
          data-feature-kind={item.kind}
          data-feature-degraded={String(item.degraded)}
          data-feature-primary={String(item.primary === true)}
          aria-current={pathname === item.path ? 'page' : undefined}
          onClick={(event) => navigate(event, item.path)}
        >
          <span>{item.label}</span>
          {item.primary ? <small>Primary</small> : null}
          {item.kind === 'not-integrated' ? <small>尚未接入</small> : null}
          {item.degraded ? <small>降级</small> : null}
        </a>
      ))}
    </nav>
  );
}

function DraftConfirmation({
  snapshot,
  confirm,
  cancel,
}: {
  snapshot: ShellSnapshot;
  confirm: () => void;
  cancel: () => void;
}) {
  const transition = snapshot.siteTransition;
  const confirmRef = useRef<HTMLButtonElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    previousFocusRef.current = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    confirmRef.current?.focus({ preventScroll: true });
    return () => {
      if (previousFocusRef.current?.isConnected) previousFocusRef.current.focus({ preventScroll: true });
    };
  }, []);

  if (!transition || transition.status !== 'confirmation-required') return null;
  const handleKeyboard = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      cancel();
      return;
    }
    if (event.key !== 'Tab') return;
    const controls = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not([disabled])'));
    if (controls.length === 0) return;
    event.preventDefault();
    const currentIndex = Math.max(0, controls.indexOf(document.activeElement as HTMLButtonElement));
    const direction = event.shiftKey ? -1 : 1;
    controls[(currentIndex + direction + controls.length) % controls.length]?.focus();
  };
  return (
    <section
      className="real-site-transition-dialog"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="real-site-draft-title"
      aria-describedby="real-site-draft-detail"
      data-testid="real-site-draft-confirmation"
      onKeyDown={handleKeyboard}
    >
      <p className="real-shell-eyebrow">REAL MODE · UNSAVED DRAFTS</p>
      <h2 id="real-site-draft-title">切换 Site 会丢弃未保存内容</h2>
      <p id="real-site-draft-detail">确认后，Shell 会先清理当前 Site 的受保护状态，再进入目标 Scope。</p>
      <ul>
        {transition.dirtyDrafts?.map((draft) => <li key={draft.id}>{draft.label}</li>)}
      </ul>
      <div className="real-shell-actions">
        <button ref={confirmRef} type="button" onClick={confirm} data-testid="real-site-draft-confirm">丢弃并切换</button>
        <button type="button" onClick={cancel} data-testid="real-site-draft-cancel">留在当前 Site</button>
      </div>
    </section>
  );
}

function PurgingSurface() {
  return (
    <section className="real-route-surface" data-testid="real-site-purging" data-route-state="PURGING">
      <p className="real-shell-eyebrow">REAL MODE · PURGING SITE SCOPE</p>
      <FocusHeading>正在清理当前 Site</FocusHeading>
      <p>旧请求已中止，realtime、Site cache、选中资源和业务临时状态正在关闭。新 Site 在清理完成前不会渲染。</p>
      <div className="real-shell-progress" role="status" aria-live="assertive">正在撤销旧 Site generation…</div>
    </section>
  );
}

function PurgeFailedSurface({ snapshot }: { snapshot: ShellSnapshot }) {
  const failure = snapshot.siteTransition?.failure;
  return (
    <section className="real-route-surface" data-testid="real-site-purge-failed" data-route-state="UNAVAILABLE">
      <p className="real-shell-eyebrow">REAL MODE · PURGE FAILED</p>
      <FocusHeading>无法安全切换 Site</FocusHeading>
      <p>旧 Scope 已失效，但至少一个清理动作未确认完成。Shell 不会进入新 Site，也不会重新显示旧 Site 数据。</p>
      {failure ? (
        <div className="real-shell-problem" role="alert" data-retryable="false">
          <strong>{failure.code}</strong>
          <span>{failure.detail}</span>
        </div>
      ) : null}
      <a className="real-shell-link-action" href={window.location.href}>重新建立可信 Scope</a>
    </section>
  );
}

export function RealShellChrome({
  config,
  snapshot,
  navigation,
  logout,
  onNavigate,
  confirmSiteNavigation,
  cancelSiteNavigation,
  children,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  navigation: RealNavigationItem[];
  logout: () => void;
  onNavigate?: (target: string) => void;
  confirmSiteNavigation?: () => void;
  cancelSiteNavigation?: () => void;
  children: ReactNode;
}) {
  const principal = snapshot.principal!;
  const submitting = snapshot.logout?.status === 'submitting';
  const pathname = window.location.pathname;
  const transition = snapshot.siteTransition;
  const protectedScope = snapshot.protectedScope;
  const activeSite = protectedScope?.siteId
    ? snapshot.sites?.items?.find((candidate) => candidate.id === protectedScope.siteId)
    : undefined;
  const realtime = snapshot.realtime ?? createIdleRealtimeStatus();
  const realtimeLabel = realtimeStatusLabel(realtime);
  const transitionBlocksContent = transition?.status === 'purging' || transition?.status === 'failed';
  const siteLabel = activeSite?.displayName ?? (transitionBlocksContent ? 'No active Site' : 'Platform scope');

  return (
    <section
      className="real-shell-layout"
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
      <header className="real-shell-header">
        <div className="real-shell-identity-context">
          <p className="real-shell-eyebrow">REAL MODE · AUTHORITATIVE SHELL</p>
          <strong data-testid="real-shell-principal">{principal.principal.displayName}</strong>
          <span className="real-shell-principal-roles" data-testid="real-principal-roles">
            描述性角色：{principal.principal.roles.join(', ') || 'none'}（不参与授权）
          </span>
          <dl className="real-shell-header-context">
            <div><dt>Site</dt><dd data-testid="real-shell-site">{siteLabel}</dd></div>
            <div><dt>Shell</dt><dd data-testid="real-shell-state">READY</dd></div>
          </dl>
        </div>
        <div className="real-shell-header-actions">
          <div
            className="real-shell-realtime"
            role="status"
            aria-live="polite"
            data-testid="real-realtime-status"
            data-realtime-state={realtime.state}
            data-realtime-site={realtime.siteId}
          >
            <strong>Realtime</strong>
            <span>{realtimeLabel}</span>
            <small>{realtime.siteId && activeSite ? `Site subscription: ${activeSite.displayName}` : 'No active Site subscription'}</small>
          </div>
          <span>Policy {principal.authorization.policyRevision}</span>
          <button type="button" onClick={logout} disabled={submitting} data-testid="real-logout-button">
            {submitting ? '正在撤销服务器 Session…' : '退出登录'}
          </button>
        </div>
      </header>

      <ShellNavigation items={navigation} pathname={pathname} onNavigate={onNavigate} />

      <div className="real-shell-content">
        {snapshot.logout?.status === 'failed' ? (
          <div className="real-shell-problem" role="alert" data-testid="real-logout-failure" data-retryable={String(snapshot.logout.retryable)}>
            <strong>{snapshot.logout.code}</strong>
            <span>{snapshot.logout.detail}</span>
            {snapshot.logout.traceId ? <code>traceId {snapshot.logout.traceId}</code> : null}
          </div>
        ) : null}
        {transition?.status === 'confirmation-required' && confirmSiteNavigation && cancelSiteNavigation ? (
          <DraftConfirmation snapshot={snapshot} confirm={confirmSiteNavigation} cancel={cancelSiteNavigation} />
        ) : null}
        {transition?.status === 'purging' ? <PurgingSurface /> : null}
        {transition?.status === 'failed' ? <PurgeFailedSurface snapshot={snapshot} /> : null}
        {!transitionBlocksContent ? children : null}
        <RealRuntimeFacts config={config} />
      </div>
    </section>
  );
}
