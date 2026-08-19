import { FocusHeading } from './FocusHeading';
import type { ProtectedScopeDraft } from './protected-scope';
import { RealShellChrome } from './RealShellChrome';
import { RealSystemManagement } from './RealSystemManagement';
import type { RealNavigationItem, RouteDecision } from './route-policy';
import type { RealRuntimeConfig } from './runtime-config';
import type { ShellSnapshot } from './shell-runtime';

function SystemSurface({ snapshot, registerUnsavedDraft }: { snapshot: ShellSnapshot; registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void }) {
  return <RealSystemManagement snapshot={snapshot} registerUnsavedDraft={registerUnsavedDraft} />;
}

function ForbiddenSurface() {
  return (
    <section className="real-route-surface" aria-labelledby="real-forbidden-title" data-testid="real-route-forbidden" data-route-state="FORBIDDEN">
      <p className="real-shell-eyebrow">REAL MODE · ACCESS DENIED</p>
      <FocusHeading id="real-forbidden-title">访问被拒绝</FocusHeading>
      <p>当前 Principal 无权打开此页面。为避免泄露受保护资源，此状态不说明目标功能或资源是否存在。</p>
      <a className="real-shell-link-action" href="/">返回 Site 入口</a>
    </section>
  );
}

function NotIntegratedSurface({ decision }: { decision: Extract<RouteDecision, { state: 'NOT_INTEGRATED' }> }) {
  return (
    <section className="real-route-surface" aria-labelledby="real-not-integrated-title" data-testid="real-route-not-integrated" data-route-state="NOT_INTEGRATED">
      <p className="real-shell-eyebrow">REAL MODE · NOT INTEGRATED</p>
      <FocusHeading id="real-not-integrated-title">{decision.feature.label}尚未接入</FocusHeading>
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
      <FocusHeading id="real-route-unavailable-title">服务当前不可用</FocusHeading>
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
      <FocusHeading id="real-degraded-title">服务处于降级状态</FocusHeading>
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
      <FocusHeading id="real-not-found-title">页面不存在</FocusHeading>
      <p>当前路径不属于此 Real Build 的公开路由。</p>
      <a className="real-shell-link-action" href="/">返回 Site 入口</a>
    </section>
  );
}

function RouteSurface({ decision, snapshot, retry, registerUnsavedDraft }: { decision: RouteDecision; snapshot: ShellSnapshot; retry: () => void; registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void }) {
  switch (decision.state) {
    case 'READY':
      return <SystemSurface snapshot={snapshot} registerUnsavedDraft={registerUnsavedDraft} />;
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
  onNavigate,
  confirmSiteNavigation,
  cancelSiteNavigation,
  registerUnsavedDraft,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  navigation: RealNavigationItem[];
  decision: RouteDecision;
  retry: () => void;
  logout: () => void;
  onNavigate: (target: string) => void;
  confirmSiteNavigation: () => void;
  cancelSiteNavigation: () => void;
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}) {
  return (
    <RealShellChrome
      config={config}
      snapshot={snapshot}
      navigation={navigation}
      logout={logout}
      onNavigate={onNavigate}
      confirmSiteNavigation={confirmSiteNavigation}
      cancelSiteNavigation={cancelSiteNavigation}
    >
      <RouteSurface decision={decision} snapshot={snapshot} retry={retry} registerUnsavedDraft={registerUnsavedDraft} />
    </RealShellChrome>
  );
}
