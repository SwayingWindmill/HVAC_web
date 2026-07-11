import { useAgentContext, useFrontendTool } from '@copilotkit/react-core/v2';
import { useNavigate } from 'react-router-dom';
import { z } from 'zod';
import { canViewPath } from '@/auth/permissions';
import { useUi } from '@/store/ui';
import { useAiApplicationContext } from './context';

export default function CopilotContextBridge() {
  const navigate = useNavigate();
  const role = useUi((state) => state.role);
  const context = useAiApplicationContext();

  useAgentContext({
    description: '当前 HVAC 管理平台页面、角色、建筑范围、业务指标和允许访问的页面。回答时必须遵守角色权限，并说明所有设备控制需要人工审批。',
    value: context,
  });

  useFrontendTool({
    name: 'navigate_to_page',
    description: '导航到当前用户有权访问的 HVAC 页面。只允许使用 permittedRoutes 中的路径。',
    parameters: z.object({
      path: z.string().describe('目标路由，例如 /fdd、/alarms、/energy。'),
    }),
    handler: async ({ path }) => {
      if (!canViewPath(role, path)) {
        return `当前角色无权访问 ${path}，未执行导航。`;
      }
      navigate(path);
      return `已导航到 ${path}。`;
    },
  }, [navigate, role]);

  useFrontendTool({
    name: 'open_ai_workspace',
    description: '打开完整 AI 中心工作台。',
    parameters: z.object({}),
    handler: async () => {
      if (!canViewPath(role, '/ai')) return '当前角色无权访问 AI 中心。';
      navigate('/ai');
      return '已打开 AI 中心。';
    },
  }, [navigate, role]);

  return null;
}
