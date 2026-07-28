import type { ReactNode } from 'react';
import { RealRuntimeFacts } from './RealRuntimeFacts';
import type { RealNavigationItem } from './route-policy';
import type { RealRuntimeConfig } from './runtime-config';
import type { ShellSnapshot } from './shell-runtime';

function ShellNavigation({ items, pathname }: { items: RealNavigationItem[]; pathname: string }) {
  return (
    <nav className="real-shell-navigation" aria-label="Real navigation" data-testid="real-navigation">
      {items.map((item) => (
        <a
          key={item.id}
          href={item.path}
          data-feature-id={item.id}
          data-feature-kind={item.kind}
          data-feature-degraded={String(item.degraded)}
          aria-current={pathname === item.path ? 'page' : undefined}
        >
          <span>{item.label}</span>
          {item.kind === 'not-integrated' ? <small>尚未接入</small> : null}
          {item.degraded ? <small>降级</small> : null}
        </a>
      ))}
    </nav>
  );
}

export function RealShellChrome({
  config,
  snapshot,
  navigation,
  logout,
  children,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  navigation: RealNavigationItem[];
  logout: () => void;
  children: ReactNode;
}) {
  const principal = snapshot.principal!;
  const submitting = snapshot.logout?.status === 'submitting';
  const pathname = window.location.pathname;

  return (
    <section
      className="real-shell-layout"
      data-testid="real-protected-shell"
      data-protected-route-mounted="true"
      data-policy-revision={principal.authorization.policyRevision}
      data-capability-count={String(principal.authorization.capabilities.length)}
    >
      <header className="real-shell-header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · AUTHORITATIVE SHELL</p>
          <strong>{principal.principal.displayName}</strong>
          <span className="real-shell-principal-roles" data-testid="real-principal-roles">
            描述性角色：{principal.principal.roles.join(', ') || 'none'}（不参与授权）
          </span>
        </div>
        <div className="real-shell-header-actions">
          <span>Policy {principal.authorization.policyRevision}</span>
          <button type="button" onClick={logout} disabled={submitting} data-testid="real-logout-button">
            {submitting ? '正在撤销服务器 Session…' : '退出登录'}
          </button>
        </div>
      </header>

      <ShellNavigation items={navigation} pathname={pathname} />

      <div className="real-shell-content">
        {snapshot.logout?.status === 'failed' ? (
          <div className="real-shell-problem" role="alert" data-testid="real-logout-failure" data-retryable={String(snapshot.logout.retryable)}>
            <strong>{snapshot.logout.code}</strong>
            <span>{snapshot.logout.detail}</span>
            {snapshot.logout.traceId ? <code>traceId {snapshot.logout.traceId}</code> : null}
          </div>
        ) : null}
        {children}
        <RealRuntimeFacts config={config} />
      </div>
    </section>
  );
}
