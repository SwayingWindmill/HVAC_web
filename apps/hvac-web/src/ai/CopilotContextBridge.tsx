import {
  useAgentContext,
  useComponent,
  useConfigureSuggestions,
  useFrontendTool,
} from '@copilotkit/react-core/v2';
import { useNavigate } from 'react-router';
import { z } from 'zod';
import { canViewPath } from '@/auth/permissions';
import { useUi } from '@/store/ui';
import {
  AssetStatusCard,
  EnergyAnomalyCard,
  FddEvidenceCard,
  assetStatusCardSchema,
  energyAnomalyCardSchema,
  fddEvidenceCardSchema,
} from './AgentResultCards';
import { useAiApplicationContext } from './context';

export default function CopilotContextBridge() {
  const navigate = useNavigate();
  const role = useUi((state) => state.role);
  const context = useAiApplicationContext();

  useAgentContext({
    description: '当前 HVAC 管理平台页面、角色、建筑范围、选中对象、业务指标和允许访问的页面。回答时必须遵守角色权限，并说明所有设备控制需要人工审批。',
    value: context,
  });

  useConfigureSuggestions({
    suggestions: context.suggestedPrompts.map((prompt, index) => ({
      title: prompt,
      message: prompt,
      className: `hvac-copilot-suggestion hvac-copilot-suggestion-${index + 1}`,
    })),
    available: 'before-first-message',
    consumerAgentId: 'default',
  }, [context.route, context.objectLabel, context.suggestedPrompts.join('|')]);

  useComponent({
    name: 'render_asset_status_card',
    description: '在对话中展示一张只读的 HVAC 设备状态卡，包括负荷、COP、相对基线、主要发现和业务深链。',
    parameters: assetStatusCardSchema,
    render: AssetStatusCard,
    agentId: 'default',
    followUp: false,
  }, []);

  useComponent({
    name: 'render_energy_anomaly_card',
    description: '在对话中展示能耗异常调查卡，包括基线偏差、额外能耗、增量来源和关联页面入口。',
    parameters: energyAnomalyCardSchema,
    render: EnergyAnomalyCard,
    agentId: 'default',
    followUp: false,
  }, []);

  useComponent({
    name: 'render_fdd_evidence_card',
    description: '在对话中展示 FDD 证据卡，包括风险级别、置信度、数据完整率、证据项和业务深链。',
    parameters: fddEvidenceCardSchema,
    render: FddEvidenceCard,
    agentId: 'default',
    followUp: false,
  }, []);

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
