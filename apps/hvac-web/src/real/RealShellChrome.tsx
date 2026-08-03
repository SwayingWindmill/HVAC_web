import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import {
  AlertOutlined,
  ApartmentOutlined,
  ApiOutlined,
  ControlOutlined,
  DashboardOutlined,
  DesktopOutlined,
  DollarOutlined,
  FundOutlined,
  BugOutlined,
  InfoCircleOutlined,
  LogoutOutlined,
  MoonOutlined,
  RobotOutlined,
  SettingOutlined,
  SunOutlined,
  ThunderboltOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { Avatar, Badge, Button, Divider, Grid, Layout, Popover, Select, Space, Tooltip } from 'antd';
import AppHeaderFrame from '@/layout/AppHeaderFrame';
import { ProductSidebar, type ProductMenuItem } from '@/layout/ProductSidebar';
import { FocusHeading } from './FocusHeading';
import { createIdleRealtimeStatus, realtimeStatusLabel } from './realtime-status';
import { RealRuntimeFacts } from './RealRuntimeFacts';
import { useRealTheme } from './RealTheme';
import type { RealNavigationItem } from './route-policy';
import type { RealRuntimeConfig } from './runtime-config';
import type { ShellSnapshot } from './shell-runtime';
import { siteRoute } from './site-routing';

const { Content } = Layout;
const { useBreakpoint } = Grid;

const NAVIGATION_ICONS: Record<string, ReactNode> = {
  'site-entry': <ApartmentOutlined />,
  'site-dashboard': <DashboardOutlined />,
  'site-operations': <RobotOutlined />,
  'site-assets': <ApartmentOutlined />,
  'site-commands': <ControlOutlined />,
  'site-energy': <FundOutlined />,
  'site-optimize': <ThunderboltOutlined />,
  'site-fdd': <BugOutlined />,
  'site-alarms': <AlertOutlined />,
  'site-ai': <RobotOutlined />,
  'site-cost': <DollarOutlined />,
  'site-bigscreen': <DesktopOutlined />,
  system: <SettingOutlined />,
  alarms: <AlertOutlined />,
  'work-orders': <ControlOutlined />,
  'ai-investigation': <RobotOutlined />,
};

const NAVIGATION_GROUPS = [
  {
    key: 'operations',
    label: '运营管理',
    ids: ['site-assets', 'site-commands', 'site-fdd', 'site-alarms', 'site-optimize', 'alarms', 'work-orders'],
  },
  {
    key: 'analytics',
    label: '分析中心',
    ids: ['site-energy', 'site-cost', 'site-ai', 'ai-investigation'],
  },
  {
    key: 'presentation',
    label: '展示',
    ids: ['site-bigscreen'],
  },
] as const;

function navigationItem(item: RealNavigationItem, active: boolean): ProductMenuItem {
  return {
    key: item.path,
    icon: NAVIGATION_ICONS[item.id] ?? <DashboardOutlined />,
    label: (
      <a
        href={item.path}
        data-feature-id={item.id}
        data-feature-kind={item.kind}
        data-feature-degraded={String(item.degraded)}
        data-feature-primary={String(item.primary === true)}
        aria-current={active ? 'page' : undefined}
        onClick={(event) => event.preventDefault()}
      >
        {item.label}
      </a>
    ),
  };
}

function navigationMatches(item: RealNavigationItem, pathname: string): boolean {
  if (item.id === 'site-energy') {
    const basePath = item.path.replace(/\/month$/, '');
    return pathname === basePath || pathname.startsWith(`${basePath}/`);
  }
  return pathname === item.path || (item.path !== '/' && pathname.startsWith(`${item.path}/`));
}

function buildRealSidebarItems(
  navigation: RealNavigationItem[],
  pathname: string,
  collapsed: boolean,
): { primaryItems: ProductMenuItem[]; systemItems: ProductMenuItem[]; selectedKey: string } {
  const selected = navigation.find((item) => navigationMatches(item, pathname));
  const selectedKey = selected?.path ?? pathname;
  const systemItems = navigation
    .filter((item) => item.id === 'system')
    .map((item) => navigationItem(item, navigationMatches(item, pathname)));
  const productItems = navigation.filter((item) => item.id !== 'system');
  const dashboardItems = productItems
    .filter((item) => item.id === 'site-dashboard' || item.id === 'site-entry')
    .map((item) => navigationItem(item, navigationMatches(item, pathname)));
  const groupedIds = new Set<string>(NAVIGATION_GROUPS.flatMap((group) => [...group.ids]));
  const ungrouped = productItems
    .filter((item) => item.id !== 'site-dashboard' && item.id !== 'site-entry' && !groupedIds.has(item.id))
    .map((item) => navigationItem(item, navigationMatches(item, pathname)));
  const groups = NAVIGATION_GROUPS.map((group) => ({
    type: 'group' as const,
    key: group.key,
    label: group.label,
    children: group.ids
      .map((id) => productItems.find((item) => item.id === id))
      .filter((item): item is RealNavigationItem => Boolean(item))
      .map((item) => navigationItem(item, navigationMatches(item, pathname))),
  })).filter((group) => group.children.length > 0);
  const flatGrouped = groups.flatMap((group) => group.children);

  return {
    primaryItems: collapsed
      ? [...dashboardItems, ...ungrouped, ...flatGrouped]
      : [...dashboardItems, ...ungrouped, ...groups],
    systemItems,
    selectedKey,
  };
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
      <p className="real-shell-eyebrow">未保存内容</p>
      <h2 id="real-site-draft-title">切换站点会丢弃未保存内容</h2>
      <p id="real-site-draft-detail">确认后，系统会先清理当前站点的受保护状态，再进入目标站点。</p>
      <ul>
        {transition.dirtyDrafts?.map((draft) => <li key={draft.id}>{draft.label}</li>)}
      </ul>
      <div className="real-shell-actions">
        <button ref={confirmRef} type="button" onClick={confirm} data-testid="real-site-draft-confirm">丢弃并切换</button>
        <button type="button" onClick={cancel} data-testid="real-site-draft-cancel">留在当前站点</button>
      </div>
    </section>
  );
}

function PurgingSurface() {
  return (
    <section className="real-route-surface" data-testid="real-site-purging" data-route-state="PURGING">
      <p className="real-shell-eyebrow">站点切换</p>
      <FocusHeading>正在清理当前站点</FocusHeading>
      <p>旧请求已中止，实时连接、站点缓存、选中资源和临时状态正在关闭。</p>
      <div className="real-shell-progress" role="status" aria-live="assertive">正在安全切换站点…</div>
    </section>
  );
}

function PurgeFailedSurface({ snapshot }: { snapshot: ShellSnapshot }) {
  const failure = snapshot.siteTransition?.failure;
  return (
    <section className="real-route-surface" data-testid="real-site-purge-failed" data-route-state="UNAVAILABLE">
      <p className="real-shell-eyebrow">站点切换失败</p>
      <FocusHeading>无法安全切换站点</FocusHeading>
      <p>旧站点范围已失效，但至少一个清理动作未确认完成。系统不会进入新站点，也不会重新显示旧数据。</p>
      {failure ? (
        <div className="real-shell-problem" role="alert" data-retryable="false">
          <strong>{failure.code}</strong>
          <span>{failure.detail}</span>
        </div>
      ) : null}
      <a className="real-shell-link-action" href={window.location.href}>重新建立可信站点范围</a>
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
  const screens = useBreakpoint();
  const mobile = screens.md === false;
  const compact = !screens.xl;
  const narrow = !screens.xl;
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const { resolvedMode: themeMode, setMode: setThemeMode } = useRealTheme();
  const toggleSidebar = () => setSidebarCollapsed((collapsed) => !collapsed);

  useEffect(() => {
    if (mobile) setSidebarCollapsed(true);
  }, [mobile, setSidebarCollapsed]);

  const navigate = (target: string) => {
    if (mobile) setSidebarCollapsed(true);
    onNavigate?.(target);
  };
  const menuCollapsed = mobile ? false : sidebarCollapsed;
  const sidebar = useMemo(
    () => buildRealSidebarItems(navigation, pathname, menuCollapsed),
    [menuCollapsed, navigation, pathname],
  );
  const sites = snapshot.sites?.items ?? [];
  const siteOptions = sites.map((site) => ({ value: site.id, label: site.displayName }));
  const bigscreen = navigation.find((item) => item.id === 'site-bigscreen');
  const realtimeStatus = realtime.state === 'live'
    ? 'success'
    : realtime.state === 'unavailable'
      ? 'error'
      : realtime.state === 'idle'
        ? 'default'
        : 'processing';
  const realtimeColor = realtime.state === 'unavailable' ? '#DC2626' : '#0FB5AE';
  const diagnostics = (
    <div className="real-shell-diagnostics">
      <strong>可信运行信息</strong>
      <span>Principal、Policy、Capability 和 Runtime 仅用于诊断与审计。</span>
      <Divider style={{ margin: '10px 0' }} />
      <dl>
        <div><dt>Principal</dt><dd>{principal.principal.subject}</dd></div>
        <div><dt>Policy</dt><dd>{principal.authorization.policyRevision}</dd></div>
        <div><dt>Capabilities</dt><dd>{principal.authorization.capabilities.length}</dd></div>
        <div><dt>Scope generation</dt><dd>{protectedScope?.generation ?? 0}</dd></div>
      </dl>
      <RealRuntimeFacts config={config} />
    </div>
  );
  const siteControl = (
    <Space size={10}>
      <span data-testid="real-shell-site-control">
        <Select
          aria-label="选择授权 Site"
          value={activeSite?.id}
          options={siteOptions}
          onChange={(siteId) => {
            const site = sites.find((candidate) => candidate.id === siteId);
            if (site) navigate(siteRoute(site, 'dashboard'));
          }}
          disabled={transitionBlocksContent || siteOptions.length === 0}
          placeholder={siteLabel}
          style={{ width: 210 }}
          variant="filled"
        />
      </span>
      {!compact ? <Badge status="success" text="真实数据" /> : null}
    </Space>
  );

  return (
    <Layout
      className="real-shell-layout"
      style={{ minHeight: '100vh', height: '100vh', overflow: 'hidden' }}
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
      <ProductSidebar
        collapsed={sidebarCollapsed}
        mobile={mobile}
        primaryItems={sidebar.primaryItems}
        systemItems={sidebar.systemItems}
        selectedKey={sidebar.selectedKey}
        onNavigate={navigate}
        onClose={() => setSidebarCollapsed(true)}
        navigationTestId="real-navigation"
      />

      <Layout style={{ minWidth: 0, minHeight: 0 }}>
        <AppHeaderFrame
          className="real-shell-header"
          sidebarCollapsed={sidebarCollapsed}
          onToggleSidebar={mobile ? () => setSidebarCollapsed(false) : toggleSidebar}
          compact={compact}
        >
          {narrow ? (
            <Popover trigger="click" placement="bottomLeft" content={<div style={{ width: 240 }}>{siteControl}</div>}>
              <Button size="small" icon={<ApartmentOutlined />}>{activeSite?.displayName ?? '选择站点'}</Button>
            </Popover>
          ) : siteControl}

          <div style={{ flex: 1, minWidth: 8 }} />

          <Tooltip title={realtime.siteId ? `当前订阅：${siteLabel}` : '当前没有活动 Site 订阅'}>
            <span
              className="real-shell-realtime"
              role="status"
              aria-live="polite"
              data-testid="real-realtime-status"
              data-realtime-state={realtime.state}
              data-realtime-site={realtime.siteId}
              style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 12, opacity: 0.85 }}
            >
              <ApiOutlined style={{ color: realtimeColor }} />
              <Badge status={realtimeStatus} />
              <span className={compact ? 'real-shell-sr-only' : undefined}>{realtimeLabel}</span>
            </span>
          </Tooltip>
          {bigscreen ? (
            <Tooltip title="进入运行大屏（全屏）">
              <Button type="text" icon={<DesktopOutlined />} onClick={() => navigate(bigscreen.path)} />
            </Tooltip>
          ) : null}
          <Popover content={diagnostics} trigger="click" placement="bottomRight">
            <Tooltip title="可信运行信息">
              <Button type="text" aria-label="可信运行信息" icon={<InfoCircleOutlined />} />
            </Tooltip>
          </Popover>
          <Tooltip title={themeMode === 'dark' ? '切浅色' : '切深色'}>
            <Button
              type="text"
              aria-label="切换主题"
              icon={themeMode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
              onClick={() => setThemeMode(themeMode === 'dark' ? 'light' : 'dark')}
            />
          </Tooltip>
          {!narrow ? (
            <Tooltip title={`${principal.principal.displayName} · ${principal.principal.roles.join(', ') || '授权用户'}`}>
              <Avatar style={{ background: '#0FB5AE' }} icon={<UserOutlined />} />
            </Tooltip>
          ) : null}
          <Tooltip title="退出登录">
            <Button
              type="text"
              danger
              icon={<LogoutOutlined />}
              onClick={logout}
              disabled={submitting}
              data-testid="real-logout-button"
              aria-label="退出登录"
            />
          </Tooltip>
          <span className="real-shell-sr-only" data-testid="real-shell-principal">{principal.principal.displayName}</span>
          <span className="real-shell-sr-only real-shell-principal-roles" data-testid="real-principal-roles">
            {principal.principal.roles.join(', ') || '授权用户'}
          </span>
          <span className="real-shell-sr-only" data-testid="real-shell-site">{siteLabel}</span>
          <span className="real-shell-sr-only" data-testid="real-shell-state">READY</span>
        </AppHeaderFrame>

        <Content
          className="app-content real-shell-content"
          style={{
            minWidth: 0,
            minHeight: 0,
            height: 'auto',
            flex: '1 1 auto',
            boxSizing: 'border-box',
            padding: '20px 20px 88px',
            overflow: 'auto',
          }}
        >
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
        </Content>
      </Layout>
    </Layout>
  );
}
