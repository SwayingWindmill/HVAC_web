import { Layout } from 'antd';
import { Outlet, useLocation } from 'react-router-dom';
import Sidebar from './Sidebar';
import TopHeader from './Header';
import GlobalAiAssistant from '@/ai/GlobalAiAssistant';

const { Content } = Layout;

export default function AppShell() {
  const location = useLocation();
  const isAiWorkspace = location.pathname === '/ai';

  return (
    <Layout style={{ minHeight: '100vh', height: '100vh', overflow: 'hidden' }}>
      <Sidebar />
      <Layout style={{ minWidth: 0, minHeight: 0 }}>
        <TopHeader />
        <Content
          className={isAiWorkspace ? 'app-content app-content-ai' : 'app-content'}
          style={{
            minWidth: 0,
            minHeight: 0,
            height: 'auto',
            flex: '1 1 auto',
            boxSizing: 'border-box',
            padding: isAiWorkspace ? '20px' : '20px 20px 88px',
            overflow: isAiWorkspace ? 'hidden' : 'auto',
          }}
        >
          <Outlet />
        </Content>
        <GlobalAiAssistant />
      </Layout>
    </Layout>
  );
}
