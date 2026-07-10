import { useMemo } from 'react';
import { useLocation } from 'react-router-dom';
import { canViewPath } from '@/auth/permissions';
import { fddList, useOps } from '@/store/ops';
import { ROLE_LABEL, useUi } from '@/store/ui';
import { isSuggestionPendingDecision, isWorkOrderActive, isWorkOrderSlaRisk } from '@/domain/opsMeta';

export type AiApplicationContext = {
  route: string;
  pageTitle: string;
  pageDescription: string;
  buildingId: string;
  role: string;
  roleLabel: string;
  demoMode: boolean;
  metrics: {
    activeWorkOrders: number;
    slaRiskWorkOrders: number;
    activeDiagnoses: number;
    highRiskDiagnoses: number;
    pendingOptimizations: number;
  };
  permittedRoutes: string[];
  suggestedPrompts: string[];
};

type PageMeta = {
  title: string;
  description: string;
  prompts: string[];
};

const PAGE_META: Record<string, PageMeta> = {
  '/dashboard': {
    title: '总览驾驶舱',
    description: '跨资产、诊断、工单、优化、能耗和成本的业务总览。',
    prompts: ['今天最需要关注什么？', '生成一份值班摘要', '当前有哪些高风险问题？'],
  },
  '/assets': {
    title: '设备与建筑',
    description: '设备资产、通讯网关、点位在线率和维护信息。',
    prompts: ['哪些设备最需要关注？', '解释资产健康情况', '如何排查点位离线？'],
  },
  '/fdd': {
    title: '故障检测与诊断',
    description: '异常现象、根因假设、证据指标和推荐处置。',
    prompts: ['解释最严重的诊断', '给出现场排查步骤', '哪些诊断应该生成工单？'],
  },
  '/alarms': {
    title: '报警工单',
    description: '工单接手、派工、处理、SLA 风险和闭环进度。',
    prompts: ['生成值班交接摘要', '哪些工单存在 SLA 风险？', '给出工单处理优先级'],
  },
  '/optimize': {
    title: '节能优化建议',
    description: '优化收益、风险、舒适度影响、回滚条件和审批状态。',
    prompts: ['哪些建议值得批准？', '检查当前建议的风险', '总结待审批优化策略'],
  },
  '/energy': {
    title: '能耗分析',
    description: '能耗趋势、基线、峰谷负荷、系统构成和设备贡献。',
    prompts: ['为什么能耗升高？', '哪些设备贡献最大？', '分析峰谷负荷是否合理'],
  },
  '/cost': {
    title: '成本与绩效',
    description: '峰平谷电费、节能收益、碳减排、ROI 和回收周期。',
    prompts: ['总结当前节能收益', '峰时段费用是否过高？', '生成管理层汇报摘要'],
  },
  '/ai': {
    title: 'HVAC AI 运维助手',
    description: '全局抽屉与完整工作台共用会话的 HVAC 运维分析入口。',
    prompts: ['分析当前系统状态', '生成运营日报', '说明可用的数据来源'],
  },
  '/system': {
    title: '系统管理',
    description: '用户权限、站点资产、数据源、告警规则和审计日志。',
    prompts: ['检查系统接入状态', '总结当前权限模型', '哪些数据源处于降级状态？'],
  },
};

const ROUTES = ['/dashboard', '/assets', '/fdd', '/alarms', '/optimize', '/energy', '/cost', '/ai', '/system', '/bigscreen'];

function normalizeRoute(pathname: string) {
  const root = `/${pathname.replace(/^\//, '').split('/')[0] || 'dashboard'}`;
  return PAGE_META[root] ? root : '/dashboard';
}

export function useAiApplicationContext(): AiApplicationContext {
  const location = useLocation();
  const { buildingId, role, demoMode } = useUi();
  const workOrders = useOps((state) => state.workOrders);
  const suggestions = useOps((state) => state.suggestions);

  return useMemo(() => {
    const route = normalizeRoute(location.pathname);
    const meta = PAGE_META[route];
    return {
      route,
      pageTitle: meta.title,
      pageDescription: meta.description,
      buildingId,
      role,
      roleLabel: ROLE_LABEL[role],
      demoMode,
      metrics: {
        activeWorkOrders: workOrders.filter(isWorkOrderActive).length,
        slaRiskWorkOrders: workOrders.filter(isWorkOrderSlaRisk).length,
        activeDiagnoses: fddList.length,
        highRiskDiagnoses: fddList.filter((item) => item.severity === 'critical' || item.severity === 'major').length,
        pendingOptimizations: suggestions.filter(isSuggestionPendingDecision).length,
      },
      permittedRoutes: ROUTES.filter((path) => canViewPath(role, path)),
      suggestedPrompts: meta.prompts,
    };
  }, [buildingId, demoMode, location.pathname, role, suggestions, workOrders]);
}
