import { Link } from '@tanstack/react-router';
import type { Site } from '@/api/generated/platformGateway.gen';
import { FocusHeading } from './FocusHeading';
import { RouteAccessError } from './route-access';
import { useShellRuntime, useShellSnapshot } from './ShellRuntimeContext';

export function ForbiddenSurface() {
  return (
    <section className="real-route-surface" data-testid="real-route-forbidden" data-route-state="FORBIDDEN">
      <p className="real-shell-eyebrow">ACCESS DENIED</p>
      <FocusHeading>访问被拒绝</FocusHeading>
      <p>当前 Principal 无权打开此页面。此状态不会泄露目标资源是否存在。</p>
      <Link className="real-shell-link-action" to="/">返回站点入口</Link>
    </section>
  );
}

export function SiteChooserSurface({ sites }: { sites: readonly Readonly<Site>[] }) {
  return (
    <section className="real-route-surface" data-testid="real-site-chooser" data-route-state="CHOOSE_SITE">
      <p className="real-shell-eyebrow">CHOOSE SITE</p>
      <FocusHeading>选择一个授权站点</FocusHeading>
      <p>当前账号可进入多个站点。系统不会静默选择第一个站点。</p>
      <ul className="real-site-chooser-list" aria-label="授权站点">
        {sites.map((site) => (
          <li key={site.id}>
            <Link to="/sites/$siteId/overview" params={{ siteId: site.id }} data-site-id={site.id}>
              <strong>{site.displayName}</strong>
              <span>{site.code}</span>
              <small>{site.timezone}</small>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function NoAuthorizedSiteSurface() {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const principal = snapshot.principal!;
  return (
    <section className="real-route-surface" data-testid="real-site-none" data-route-state="NO_AUTHORIZED_SITE">
      <p className="real-shell-eyebrow">NO AUTHORIZED SITE</p>
      <FocusHeading>当前账号没有授权站点</FocusHeading>
      <p>{principal.principal.displayName} 当前没有可进入的站点。请联系平台管理员确认站点授权。</p>
      <button type="button" onClick={() => { void runtime.retry(); }}>刷新站点授权</button>
    </section>
  );
}

function PlatformUnavailableSurface() {
  const runtime = useShellRuntime();
  const snapshot = useShellSnapshot();
  const failure = snapshot.platform?.failure;
  return (
    <section className="real-route-surface" data-testid="real-route-unavailable" data-route-state="UNAVAILABLE">
      <p className="real-shell-eyebrow">UNAVAILABLE</p>
      <FocusHeading>服务当前不可用</FocusHeading>
      <p>Principal 仍然有效，但页面依赖的服务状态无法确认。</p>
      {failure ? (
        <div className="real-shell-problem" role="alert" data-retryable={String(failure.retryable)}>
          <strong>服务状态暂时无法确认</strong>
          <span>请稍后重新检查；当前登录状态仍然有效。</span>
        </div>
      ) : null}
      <button type="button" onClick={() => { void runtime.retry(); }}>重新检查服务</button>
    </section>
  );
}

function PlatformDegradedSurface() {
  const snapshot = useShellSnapshot();
  const status = snapshot.platform?.status;
  return (
    <section className="real-route-surface" data-testid="real-route-degraded" data-route-state="DEGRADED">
      <p className="real-shell-eyebrow">DEGRADED</p>
      <FocusHeading>服务处于降级状态</FocusHeading>
      <p>服务器已明确报告降级；该状态不会被表示为空业务数据。</p>
      {status ? <p>{status.service} · {status.version}</p> : null}
    </section>
  );
}

function SiteNotVisibleSurface() {
  const sites = useShellSnapshot().sites?.items ?? [];
  return (
    <section className="real-route-surface" data-testid="real-site-not-visible" data-route-state="SITE_NOT_VISIBLE">
      <p className="real-shell-eyebrow">SITE NOT VISIBLE</p>
      <FocusHeading>站点不可见或不存在</FocusHeading>
      <p>系统无法在当前 Tenant 的授权站点集合中验证 URL 中的站点。</p>
      {sites.length > 0 ? <SiteChooserSurface sites={sites} /> : null}
    </section>
  );
}

export function SiteDiscoveryUnavailableSurface() {
  const runtime = useShellRuntime();
  return (
    <section className="real-route-surface" data-testid="real-site-discovery-unavailable" data-route-state="UNAVAILABLE">
      <p className="real-shell-eyebrow">UNAVAILABLE</p>
      <FocusHeading>无法读取授权站点</FocusHeading>
      <p>Principal 仍然有效，但当前 Tenant 的 Registry Site 集合无法确认。</p>
      <button type="button" onClick={() => { void runtime.retry(); }}>重试站点发现</button>
    </section>
  );
}

export function RouteErrorSurface({ error }: { error: unknown }) {
  if (error instanceof RouteAccessError) {
    switch (error.code) {
      case 'FORBIDDEN':
        return <ForbiddenSurface />;
      case 'PLATFORM_UNAVAILABLE':
        return <PlatformUnavailableSurface />;
      case 'PLATFORM_DEGRADED':
        return <PlatformDegradedSurface />;
      case 'SITE_DISCOVERY_UNAVAILABLE':
        return <SiteDiscoveryUnavailableSurface />;
      case 'SITE_NOT_VISIBLE':
        return <SiteNotVisibleSurface />;
      case 'SITE_SCOPE_MISMATCH':
        return <SiteDiscoveryUnavailableSurface />;
    }
  }
  throw error;
}

export function NotFoundSurface() {
  return (
    <section className="real-route-surface" data-testid="real-route-not-found" data-route-state="NOT_FOUND">
      <p className="real-shell-eyebrow">404</p>
      <FocusHeading>页面不存在</FocusHeading>
      <p>当前路径不属于本产品的公开路由。</p>
      <Link className="real-shell-link-action" to="/">返回站点入口</Link>
    </section>
  );
}
