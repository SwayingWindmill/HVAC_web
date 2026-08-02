import { useEffect, useRef, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react';
import {
  AlertOutlined,
  ApartmentOutlined,
  ControlOutlined,
  DashboardOutlined,
  DesktopOutlined,
  FundOutlined,
  InfoCircleOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  MoonOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  SunOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { Avatar, Badge, Button, Drawer, Grid, Popover, Tag, Tooltip } from 'antd';
import { useUi } from '@/store/ui';
import { FocusHeading } from './FocusHeading';
import { createIdleRealtimeStatus, realtimeStatusLabel } from './realtime-status';
import { RealRuntimeFacts } from './RealRuntimeFacts';
import type { RealNavigationItem } from './route-policy';
import type { RealRuntimeConfig } from './runtime-config';
import type { ShellSnapshot } from './shell-runtime';

const { useBreakpoint } = Grid;

const NAVIGATION_ICONS: Record<string, ReactNode> = {
  'site-entry': <ApartmentOutlined />,
  'site-dashboard': <DashboardOutlined />,
  'site-operations': <RobotOutlined />,
  'site-assets': <ApartmentOutlined />,
  'site-energy': <FundOutlined />,
  'site-alarms': <AlertOutlined />,
  'site-commands': <ControlOutlined />,
  'site-bigscreen': <DesktopOutlined />,
  system: <SettingOutlined />,
  alarms: <AlertOutlined />,
  'work-orders': <ControlOutlined />,
  'ai-investigation': <RobotOutlined />,
};

function ShellNavigation({
  items,
  pathname,
  collapsed,
  onNavigate,
}: {
  items: RealNavigationItem[];
  pathname: string;
  collapsed: boolean;
  onNavigate?: (target: string) => void;
}) {
  const navigate = (event: MouseEvent<HTMLAnchorElement>, path: string) => {
    if (!onNavigate) return;
    event.preventDefault();
    onNavigate(path);
  };

  return (
    <nav className="real-shell-navigation" aria-label="Real navigation" data-testid="real-navigation">
      {!collapsed ? <div className="real-shell-navigation-label">产品导航</div> : null}
      {items.map((item) => {
        const active = pathname === item.path || (item.path !== '/' && pathname.startsWith(`${item.path}/`));
        const link = (
          <a
            key={item.id}
            href={item.path}
            className={active ? 'real-shell-navigation-item real-shell-navigation-item--active' : 'real-shell-navigation-item'}
            data-feature-id={item.id}
            data-feature-kind={item.kind}
            data-feature-degraded={String(item.degraded)}
            data-feature-primary={String(item.primary === true)}
            aria-current={active ? 'page' : undefined}
            onClick={(event) => navigate(event, item.path)}
          >
            <span className="real-shell-navigation-icon" aria-hidden="true">
              {NAVIGATION_ICONS[item.id] ?? <DashboardOutlined />}
            </span>
            {!collapsed ? <span className="real-shell-navigation-copy">{item.label}</span> : null}
            {!collapsed && item.primary ? <span className="real-shell-navigation-badge">AI</span> : null}
            {!collapsed && item.kind === 'not-integrated' ? <small>规划中</small> : null}
            {!collapsed && item.degraded ? <small>降级</small> : null}
          </a>
        );
        return collapsed ? <Tooltip key={item.id} title={item.label} placement="right">{link}</Tooltip> : link;
      })}
    </nav>
  );
}

function ShellSidebar({
  navigation,
  pathname,
  collapsed,
  onNavigate,
}: {
  navigation: RealNavigationItem[];
  pathname: string;
  collapsed: boolean;
  onNavigate?: (target: string) => void;
}) {
  return (
    <div className={collapsed ? 'real-shell-sidebar-inner real-shell-sidebar-inner--collapsed' : 'real-shell-sidebar-inner'}>
      <div className="real-shell-brand">
        <img src="/quanlaihe-mark.svg" alt="" />
        {!collapsed ? (
          <div>
            <strong>泉来禾智慧能源</strong>
            <span>QUANLAIHE ENERGY</span>
          </div>
        ) : null}
      </div>
      <ShellNavigation items={navigation} pathname={pathname} collapsed={collapsed} onNavigate={onNavigate} />
      <div className="real-shell-sidebar-footer">
        <SafetyCertificateOutlined aria-hidden="true" />
        {!collapsed ? (
          <div>
            <strong>真实数据模式</strong>
            <span>权威身份与站点边界</span>
          </div>
        ) : null}
      </div>
    </div>
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
  const sidebarCollapsed = useUi((state) => state.sidebarCollapsed);
  const setSidebarCollapsed = useUi((state) => state.setSidebarCollapsed);
  const toggleSidebar = useUi((state) => state.toggleSidebar);
  const themeMode = useUi((state) => state.themeMode);
  const setThemeMode = useUi((state) => state.setThemeMode);

  useEffect(() => {
    if (mobile) setSidebarCollapsed(true);
  }, [mobile, setSidebarCollapsed]);

  const navigate = (target: string) => {
    if (mobile) setSidebarCollapsed(true);
    onNavigate?.(target);
  };
  const sidebar = (
    <ShellSidebar
      navigation={navigation}
      pathname={pathname}
      collapsed={!mobile && sidebarCollapsed}
      onNavigate={navigate}
    />
  );
  const realtimeBadgeStatus = realtime.state === 'live'
    ? 'success'
    : realtime.state === 'unavailable'
      ? 'error'
      : realtime.state === 'idle'
        ? 'default'
        : 'processing';
  const diagnostics = (
    <div className="real-shell-diagnostics">
      <div className="real-shell-diagnostics-title">
        <SafetyCertificateOutlined />
        <div><strong>可信运行信息</strong><span>仅用于诊断与审计</span></div>
      </div>
      <dl>
        <div><dt>Principal</dt><dd>{principal.principal.subject}</dd></div>
        <div><dt>Policy</dt><dd>{principal.authorization.policyRevision}</dd></div>
        <div><dt>Capabilities</dt><dd>{principal.authorization.capabilities.length}</dd></div>
        <div><dt>Scope generation</dt><dd>{protectedScope?.generation ?? 0}</dd></div>
      </dl>
      <RealRuntimeFacts config={config} />
    </div>
  );

  return (
    <section
      className={sidebarCollapsed && !mobile ? 'real-shell-layout real-shell-layout--collapsed' : 'real-shell-layout'}
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
      {mobile ? (
        <Drawer
          placement="left"
          open={!sidebarCollapsed}
          onClose={() => setSidebarCollapsed(true)}
          width={224}
          closable={false}
          forceRender
          rootClassName="real-shell-drawer"
          styles={{ body: { padding: 0 } }}
        >
          {sidebar}
        </Drawer>
      ) : <aside className="real-shell-sidebar">{sidebar}</aside>}

      <div className="real-shell-workspace">
        <header className="real-shell-header">
          <Button
            type="text"
            aria-label={sidebarCollapsed ? '展开导航' : '收起导航'}
            icon={sidebarCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            onClick={mobile ? () => setSidebarCollapsed(false) : toggleSidebar}
          />
          <div className="real-shell-identity-context">
            <div className="real-shell-breadcrumb">运行中心 <span>/</span> {siteLabel}</div>
            <div className="real-shell-title-row">
              <strong data-testid="real-shell-site">{siteLabel}</strong>
              <Tag bordered={false}>真实数据</Tag>
              <span className="real-shell-state-copy" data-testid="real-shell-state">READY</span>
            </div>
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
              <Badge status={realtimeBadgeStatus} />
              <div><strong>{realtimeLabel}</strong><small>{activeSite ? activeSite.displayName : '未订阅站点'}</small></div>
            </div>
            <Popover content={diagnostics} trigger="click" placement="bottomRight">
              <Tooltip title="可信运行信息">
                <Button type="text" aria-label="可信运行信息" icon={<InfoCircleOutlined />} />
              </Tooltip>
            </Popover>
            <Tooltip title={themeMode === 'dark' ? '切换浅色模式' : '切换深色模式'}>
              <Button
                type="text"
                aria-label="切换主题"
                icon={themeMode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
                onClick={() => setThemeMode(themeMode === 'dark' ? 'light' : 'dark')}
              />
            </Tooltip>
            <div className="real-shell-account">
              <Avatar icon={<UserOutlined />} />
              <div>
                <strong data-testid="real-shell-principal">{principal.principal.displayName}</strong>
                <span className="real-shell-principal-roles" data-testid="real-principal-roles">
                  {principal.principal.roles.join(', ') || '授权用户'}
                </span>
              </div>
            </div>
            <Tooltip title="退出登录">
              <Button
                type="text"
                danger
                icon={<LogoutOutlined />}
                onClick={logout}
                disabled={submitting}
                data-testid="real-logout-button"
                aria-label="退出登录"
              >
                {screens.xl ? (submitting ? '正在退出…' : '退出') : null}
              </Button>
            </Tooltip>
          </div>
        </header>

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
        </div>
      </div>
    </section>
  );
}
