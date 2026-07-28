import { RealRuntimeFacts } from './RealRuntimeFacts';
import type { RealNavigationItem, RouteDecision } from './route-policy';
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

function HomeSurface({ snapshot }: { snapshot: ShellSnapshot }) {
  const principal = snapshot.principal!;
  return (
    <section
      className="real-route-surface"
      aria-labelledby="real-home-title"
      data-testid="real-route-home"
      data-route-state="READY"
      data-business-state="EMPTY"
    >
      <p className="real-shell-eyebrow">REAL MODE · AUTHENTICATED</p>
      <h1 id="real-home-title">可信 Shell 已就绪</h1>
      <p>当前首页尚未请求任何 Site 级业务数据。这是明确的空业务状态，不是服务不可用、权限拒绝或演示数据。</p>

      <dl className="real-shell-facts real-shell-principal-facts">
        <div><dt>Principal</dt><dd>{principal.principal.displayName}</dd></div>
        <div><dt>Subject</dt><dd>{principal.principal.subject}</dd></div>
        <div><dt>Acting Organization</dt><dd>{principal.context.actingOrganizationId}</dd></div>
        <div><dt>IAM policy revision</dt><dd>{principal.authorization.policyRevision}</dd></div>
        <div><dt>Session expires</dt><dd>{principal.session.expiresAt}</dd></div>
        <div><dt>描述性角色（不参与授权）</dt><dd>{principal.principal.roles.join(', ') || 'none'}</dd></div>
      </dl>

      <div className="real-shell-capabilities" aria-label="Effective capabilities">
        <strong>Effective capabilities ({principal.authorization.capabilities.length})</strong>
        {principal.authorization.capabilities.length ? (
          <ul>{principal.authorization.capabilities.map((capability) => <li key={capability}><code>{capability}</code></li>)}</ul>
        ) : <p>当前 Principal 没有已发布 Capability。</p>}
      </div>
    </section>
  );
}

function SystemSurface({ snapshot }: { snapshot: ShellSnapshot }) {
  const status = snapshot.platform?.status;
  return (
    <section
      className="real-route-surface"
      aria-labelledby="real-system-title"
      data-testid="real-route-system"
      data-route-state="READY"
      data-business-state="POPULATED"
    >
      <p className="real-shell-eyebrow">REAL MODE · IMPLEMENTED</p>
      <h1 id="real-system-title">系统状态</h1>
      <p>以下内容来自 Platform Gateway 的权威状态响应。</p>
      {status ? (
        <dl className="real-shell-facts">
          <div><dt>Service</dt><dd>{status.service}</dd></div>
          <div><dt>Status</dt><dd>{status.status}</dd></div>
          <div><dt>Implementation</dt><dd>{status.implementation}</dd></div>
          <div><dt>Version</dt><dd>{status.version}</dd></div>
          <div><dt>Route policy revision</dt><dd>{status.routePolicyRevision}</dd></div>
          <div><dt>Compatibility</dt><dd>{status.compatibilityMode}</dd></div>
        </dl>
      ) : null}
    </section>
  );
}

function ForbiddenSurface() {
  return (
    <section className="real-route-surface" aria-labelledby="real-forbidden-title" data-testid="real-route-forbidden" data-route-state="FORBIDDEN">
      <p className="real-shell-eyebrow">REAL MODE · ACCESS DENIED</p>
      <h1 id="real-forbidden-title">访问被拒绝</h1>
      <p>当前 Principal 无权打开此页面。为避免泄露受保护资源，此状态不说明目标功能或资源是否存在。</p>
      <a className="real-shell-link-action" href="/">返回首页</a>
    </section>
  );
}

function NotIntegratedSurface({ decision }: { decision: Extract<RouteDecision, { state: 'NOT_INTEGRATED' }> }) {
  return (
    <section className="real-route-surface" aria-labelledby="real-not-integrated-title" data-testid="real-route-not-integrated" data-route-state="NOT_INTEGRATED">
      <p className="real-shell-eyebrow">REAL MODE · NOT INTEGRATED</p>
      <h1 id="real-not-integrated-title">{decision.feature.label}尚未接入</h1>
      <p>当前部署没有该模块的权威后端。Real Build 不会加载 Demo 页面、Mock 数据或本地状态作为替代。</p>
      <div className="real-shell-capabilities">
        <strong>进入该产品区域所需 Capability</strong>
        <ul>{decision.feature.requiredCapabilities.map((capability) => <li key={capability}><code>{capability}</code></li>)}</ul>
      </div>
    </section>
  );
}

function RouteUnavailableSurface({ snapshot, retry }: { snapshot: ShellSnapshot; retry: () => void }) {
  const failure = snapshot.platform?.failure;
  return (
    <section className="real-route-surface" aria-labelledby="real-route-unavailable-title" data-testid="real-route-unavailable" data-route-state="UNAVAILABLE">
      <p className="real-shell-eyebrow">REAL MODE · UNAVAILABLE</p>
      <h1 id="real-route-unavailable-title">服务当前不可用</h1>
      <p>Principal 仍然有效，但该已实现路由依赖的服务器状态无法确认。系统不会显示缓存或演示业务值。</p>
      {failure ? (
        <div className="real-shell-problem" role="alert" data-retryable={String(failure.retryable)}>
          <strong>{failure.code}</strong>
          <span>{failure.detail}</span>
          {failure.traceId ? <code>traceId {failure.traceId}</code> : null}
        </div>
      ) : <div className="real-shell-progress" role="status">正在确认服务状态…</div>}
      <div className="real-shell-actions">
        <button type="button" onClick={retry}>重新检查服务</button>
      </div>
    </section>
  );
}

function DegradedSurface({ snapshot }: { snapshot: ShellSnapshot }) {
  const status = snapshot.platform?.status;
  return (
    <section className="real-route-surface" aria-labelledby="real-degraded-title" data-testid="real-route-degraded" data-route-state="DEGRADED">
      <p className="real-shell-eyebrow">REAL MODE · DEGRADED</p>
      <h1 id="real-degraded-title">服务处于降级状态</h1>
      <p>服务器已明确报告降级。该状态不同于完全不可用，也不会被表示为空业务数据。</p>
      {status ? (
        <dl className="real-shell-facts">
          <div><dt>Service</dt><dd>{status.service}</dd></div>
          <div><dt>Status</dt><dd>{status.status}</dd></div>
          <div><dt>Version</dt><dd>{status.version}</dd></div>
          <div><dt>Checked at</dt><dd>{status.checkedAt}</dd></div>
        </dl>
      ) : null}
    </section>
  );
}

function NotFoundSurface() {
  return (
    <section className="real-route-surface" aria-labelledby="real-not-found-title" data-testid="real-route-not-found" data-route-state="NOT_FOUND">
      <p className="real-shell-eyebrow">REAL MODE · 404</p>
      <h1 id="real-not-found-title">页面不存在</h1>
      <p>当前路径不属于此 Real Build 的公开路由。</p>
      <a className="real-shell-link-action" href="/">返回首页</a>
    </section>
  );
}

function RouteSurface({
  decision,
  snapshot,
  retry,
}: {
  decision: RouteDecision;
  snapshot: ShellSnapshot;
  retry: () => void;
}) {
  switch (decision.state) {
    case 'READY':
      return decision.feature.id === 'system' ? <SystemSurface snapshot={snapshot} /> : <HomeSurface snapshot={snapshot} />;
    case 'FORBIDDEN':
      return <ForbiddenSurface />;
    case 'NOT_INTEGRATED':
      return <NotIntegratedSurface decision={decision} />;
    case 'UNAVAILABLE':
      return <RouteUnavailableSurface snapshot={snapshot} retry={retry} />;
    case 'DEGRADED':
      return <DegradedSurface snapshot={snapshot} />;
    case 'NOT_FOUND':
      return <NotFoundSurface />;
  }
}

export function AuthenticatedShell({
  config,
  snapshot,
  navigation,
  decision,
  retry,
  logout,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  navigation: RealNavigationItem[];
  decision: RouteDecision;
  retry: () => void;
  logout: () => void;
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
        <RouteSurface decision={decision} snapshot={snapshot} retry={retry} />
        <RealRuntimeFacts config={config} />
      </div>
    </section>
  );
}
