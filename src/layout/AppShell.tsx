import { Layout } from 'antd';
import { Outlet } from 'react-router-dom';
import Sidebar from './Sidebar';
import TopHeader from './Header';
import GlobalAiAssistant from '@/ai/GlobalAiAssistant';

const { Content } = Layout;

export default function AppShell() {
  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sidebar />
      <Layout>
        <TopHeader />
        <Content style={{ padding: '20px 20px 88px', overflow: 'auto' }}>
          <Outlet />
        </Content>
        <GlobalAiAssistant />
      </Layout>
    </Layout>
  );
}
