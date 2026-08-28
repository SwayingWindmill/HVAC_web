import { useEffect, useMemo, useRef, type KeyboardEvent, type ReactNode } from 'react';
import {
  AlertOutlined,
  ApartmentOutlined,
  ApiOutlined,
  BellOutlined,
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
} from '@ant-design/icons';
import { ProLayout, type MenuDataItem } from '@ant-design/pro-components';
import { Badge, Button, Divider, Grid, Popover, Select, Space, Tooltip } from 'antd';
import { useLocation } from 'react-router';
import { useRealUiStore } from '@/stores/realUi';
import { FocusHeading } from './FocusHeading';
import { createIdleRealtimeStatus, realtimeStatusLabel, realtimeStatusPresentation } from './realtime-status';
import { RealRuntimeFacts } from './RealRuntimeFacts';
import { useRealTheme } from './RealTheme';
import type { RealNavigationItem } from './route-policy';
import type { RealRuntimeConfig } from './runtime-config';
import type { ShellSnapshot } from './shell-runtime';
import { siteRoute } from './site-routing';

const { useBreakpoint } = Grid;

const NAVIGATION_ICONS: Record<string, ReactNode> = {
  'site-entry': <ApartmentOutlined />,
  'site-dashboard': <DashboardOutlined />,
  'site-operations': <RobotOutlined />,
  'site-assets': <ApartmentOutlined />,

  'site-energy': <FundOutlined />,
  'site-forecast': <FundOutlined />,
  'site-control': <ControlOutlined />,
  'site-optimize': <ThunderboltOutlined />,
  'site-fdd': <BugOutlined />,
  'site-alarms': <AlertOutlined />,
  'site-work-orders': <ControlOutlined />,
  'site-ai': <RobotOutlined />,
  'site-cost': <DollarOutlined />,
  'site-settlement': <DollarOutlined />,
  'site-bigscreen': <DesktopOutlined />,
  notifications: <BellOutlined />,
  system: <SettingOutlined />,
  alarms: <AlertOutlined />,
  'work-orders': <ControlOutlined />,
  'ai-investigation': <RobotOutlined />,
};

const NAVIGATION_GROUPS = [
  {
    key: 'operations',
    label: '运营管理',
    ids: ['site-assets', 'site-fdd', 'site-alarms', 'site-work-orders', 'site-control', 'site-optimize', 'alarms', 'work-orders'],
  },
  {
    key: 'analytics',
    label: '分析中心',
    ids: ['site-energy', 'site-forecast', 'site-cost', 'site-settlement', 'site-ai', 'ai-investigation'],
  },
  {
    key: 'presentation',
    label: '展示',
    ids: ['site-bigscreen'],
  },
] as const;

type HvacMenuDataItem = MenuDataItem & {
  featureId?: string;
  featureKind?: string;
  featureDegraded?: boolean;
  featurePrimary?: boolean;
};

function navigationItem(item: RealNavigationItem): HvacMenuDataItem {
  return {
    key: item.path,
    path: item.path,
    name: item.label,
    icon: NAVIGATION_ICONS[item.id] ?? <DashboardOutlined />,
    featureId: item.id,
    featureKind: item.kind,
    featureDegraded: item.degraded,
    featurePrimary: item.primary === true,
  };
}

function buildRealMenuItems(navigation: RealNavigationItem[]): HvacMenuDataItem[] {
  const systemItems = navigation
    .filter((item) => item.id === 'notifications' || item.id === 'system')
    .map(navigationItem);
  const productItems = navigation.filter((item) => item.id !== 'notifications' && item.id !== 'system');
  const dashboardItems = productItems
    .filter((item) => item.id === 'site-dashboard' || item.id === 'site-entry')
    .map(navigationItem);
  const groupedIds = new Set<string>(NAVIGATION_GROUPS.flatMap((group) => [...group.ids]));
  const ungrouped = productItems
    .filter((item) => item.id !== 'site-dashboard' && item.id !== 'site-entry' && !groupedIds.has(item.id))
    .map(navigationItem);
  const groups: HvacMenuDataItem[] = NAVIGATION_GROUPS.map((group) => ({
    key: group.key,
    name: group.label,
    children: group.ids
      .map((id) => productItems.find((item) => item.id === id))
      .filter((item): item is RealNavigationItem => Boolean(item))
      .map(navigationItem),
  })).filter((group) => (group.children?.length ?? 0) > 0);

  if (systemItems.length > 0) {
    groups.push({ key: 'system-group', name: '系统', children: systemItems });
  }
  return [...dashboardItems, ...ungrouped, ...groups];
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
  const location = useLocation();
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
      <a className="real-shell-link-action" href={`${location.pathname}${location.search}${location.hash}`}>重新建立可信站点范围</a>
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
  const pathname = useLocation().pathname;
  const transition = snapshot.siteTransition;
  const protectedScope = snapshot.protectedScope;
  const activeSite = protectedScope?.siteId
    ? snapshot.sites?.items?.find((candidate) => candidate.id === protectedScope.siteId)
    : undefined;
  const realtime = snapshot.realtime ?? createIdleRealtimeStatus();
  const realtimeLabel = realtimeStatusLabel(realtime);
  const realtimePresentation = realtimeStatusPresentation(realtime);
  const transitionBlocksContent = transition?.status === 'purging' || transition?.status === 'failed';
  const siteLabel = activeSite?.displayName ?? (transitionBlocksContent ? 'No active Site' : 'Platform scope');
  const tenantId = principal.context.tenantId;
  const screens = useBreakpoint();
  const compact = !screens.xl;
  const narrow = !screens.xl;
  const sidebarCollapsed = useRealUiStore((state) => state.sidebarCollapsed);
  const setSidebarCollapsed = useRealUiStore((state) => state.setSidebarCollapsed);
  const { resolvedMode: themeMode, setMode: setThemeMode } = useRealTheme();

  const navigate = (target: string) => onNavigate?.(target);
  const menuItems = useMemo(() => buildRealMenuItems(navigation), [navigation]);
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
      <span data-testid="real-shell-tenant" aria-label="当前 Tenant" className="real-shell-scope-tag">Tenant · {tenantId}</span>
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
    <div
      className="real-shell-layout"
      style={{ minHeight: '100dvh', height: '100dvh', overflow: 'hidden' }}
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
      <ProLayout
        className="real-pro-layout"
        title="泉来禾智慧能源"
        logo="/quanlaihe-mark.svg"
        layout="side"
        siderWidth={224}
        fixedHeader
        fixSiderbar
        breakpoint="lg"
        collapsed={sidebarCollapsed}
        onCollapse={setSidebarCollapsed}
        location={{ pathname }}
        route={{ path: '/', routes: menuItems }}
        menuItemRender={(item, dom) => {
          const menuItem = item as HvacMenuDataItem;
          if (!menuItem.path) return dom;
          return (
            <a
              href={menuItem.path}
              data-feature-id={menuItem.featureId}
              data-feature-kind={menuItem.featureKind}
              data-feature-degraded={String(menuItem.featureDegraded ?? false)}
              data-feature-primary={String(menuItem.featurePrimary ?? false)}
              onClick={(event) => {
                event.preventDefault();
                navigate(menuItem.path!);
              }}
            >
              {dom}
            </a>
          );
        }}
        headerContentRender={() => (
          narrow ? (
            <Popover trigger="click" placement="bottomLeft" content={<div style={{ width: 240 }}>{siteControl}</div>}>
              <Button size="small" icon={<ApartmentOutlined />}>{activeSite?.displayName ?? '选择站点'}</Button>
            </Popover>
          ) : siteControl
        )}
        actionsRender={() => [
          <Tooltip key="realtime" title={`${realtimePresentation.code} · ${realtimePresentation.label}：${realtimePresentation.detail}${realtime.siteId ? ` 当前订阅：${siteLabel}` : ''}`}>
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
              <span data-testid="real-realtime-code">{realtimePresentation.code}</span>
              <span>{realtimePresentation.label}</span>
              <span className="real-shell-sr-only">{realtimeLabel}</span>
            </span>
          </Tooltip>,
          bigscreen ? (
            <Tooltip key="bigscreen" title="进入运行大屏（全屏）">
              <Button type="text" icon={<DesktopOutlined />} onClick={() => navigate(bigscreen.path)} />
            </Tooltip>
          ) : null,
          <Popover key="diagnostics" content={diagnostics} trigger="click" placement="bottomRight">
            <Tooltip title="可信运行信息">
              <Button type="text" aria-label="可信运行信息" icon={<InfoCircleOutlined />} />
            </Tooltip>
          </Popover>,
          <Tooltip key="theme" title={themeMode === 'dark' ? '切浅色' : '切深色'}>
            <Button
              type="text"
              aria-label="切换主题"
              icon={themeMode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
              onClick={() => setThemeMode(themeMode === 'dark' ? 'light' : 'dark')}
            />
          </Tooltip>,
          <Tooltip key="logout" title="退出登录">
            <Button
              type="text"
              danger
              icon={<LogoutOutlined />}
              onClick={logout}
              disabled={submitting}
              data-testid="real-logout-button"
              aria-label="退出登录"
            />
          </Tooltip>,
          <span key="principal" className="real-shell-sr-only" data-testid="real-shell-principal">{principal.principal.displayName}</span>,
          <span key="roles" className="real-shell-sr-only real-shell-principal-roles" data-testid="real-principal-roles">
            {principal.principal.roles.join(', ') || '授权用户'}
          </span>,
          <span key="site" className="real-shell-sr-only" data-testid="real-shell-site">{siteLabel}</span>,
          <span key="state" className="real-shell-sr-only" data-testid="real-shell-state">READY</span>,
        ]}
        contentStyle={{ margin: 0, padding: 0, minHeight: 0, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}
      >
        <main
          className="app-content real-shell-content"
          style={{
            minWidth: 0,
            minHeight: 0,
            flex: '1 1 0',
            boxSizing: 'border-box',
            padding: '20px 20px 88px',
            overflowX: 'hidden',
            overflowY: 'auto',
            overscrollBehavior: 'contain',
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
        </main>
      </ProLayout>
    </div>
  );
}
