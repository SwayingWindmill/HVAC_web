import { Drawer, Layout, Menu } from 'antd';
import type { MenuProps } from 'antd';
import './Sidebar.css';

const { Sider } = Layout;

export type ProductMenuItem = Required<MenuProps>['items'][number];

export interface ProductSidebarProps {
  collapsed: boolean;
  mobile: boolean;
  primaryItems: ProductMenuItem[];
  systemItems?: ProductMenuItem[];
  selectedKey: string;
  onNavigate: (key: string) => void;
  onClose: () => void;
  navigationTestId?: string;
}

function BrandMark() {
  return <img className="sidebar-brand-mark" src="/quanlaihe-mark.svg" alt="" />;
}

export function ProductSidebar({
  collapsed,
  mobile,
  primaryItems,
  systemItems = [],
  selectedKey,
  onNavigate,
  onClose,
  navigationTestId,
}: ProductSidebarProps) {
  const menuCollapsed = mobile ? false : collapsed;
  const content = (
    <>
      <div className={`sidebar-brand ${menuCollapsed ? 'sidebar-brand-collapsed' : ''}`}>
        <BrandMark />
        {!menuCollapsed && (
          <div className="sidebar-brand-copy">
            <div className="sidebar-brand-title">泉来禾智慧能源</div>
            <div className="sidebar-brand-subtitle">QUANLAIHE ENERGY</div>
          </div>
        )}
      </div>

      <div className="sidebar-navigation" data-testid={navigationTestId}>
        <Menu
          mode="inline"
          inlineCollapsed={menuCollapsed}
          selectedKeys={[selectedKey]}
          items={primaryItems}
          onClick={({ key }) => onNavigate(key)}
        />
      </div>

      {systemItems.length > 0 && (
        <div className="sidebar-system">
          {!menuCollapsed && <div className="sidebar-system-divider" />}
          <Menu
            mode="inline"
            inlineCollapsed={menuCollapsed}
            selectedKeys={[selectedKey]}
            items={systemItems}
            onClick={({ key }) => onNavigate(key)}
          />
        </div>
      )}
    </>
  );

  if (mobile) {
    return (
      <Drawer
        placement="left"
        open={!collapsed}
        onClose={onClose}
        width={224}
        closable={false}
        forceRender
        rootClassName="app-sidebar-drawer"
        styles={{ body: { padding: 0 } }}
      >
        <div className="app-sidebar app-sidebar-mobile">{content}</div>
      </Drawer>
    );
  }

  return (
    <Sider
      className="app-sidebar"
      collapsible
      collapsed={collapsed}
      trigger={null}
      width={224}
      collapsedWidth={64}
    >
      {content}
    </Sider>
  );
}
