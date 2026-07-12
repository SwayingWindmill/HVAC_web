import { useMemo } from 'react';
import { useLocation } from 'react-router-dom';
import { canViewPath } from '@/auth/permissions';
import { isSuggestionPendingDecision, isWorkOrderActive, isWorkOrderSlaRisk } from '@/domain/opsMeta';
import { DEVICE_META } from '@/pages/Assets/meta';
import { fddList, useOps } from '@/store/ops';
import { ROLE_LABEL, useUi } from '@/store/ui';

export type AiApplicationContext = {
  route: string;
  pageTitle: string;
  pageDescription: string;
  buildingId: string;
  buildingLabel: string;
  role: string;
  roleLabel: string;
  demoMode: boolean;
  objectLabel?: string;
  scopeLabel: string;
  welcomeTitle: string;
  inputPlaceholder: string;
  attentionCount: number;
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
  welcomeTitle: string;
  inputPlaceholder: string;
  prompts: string[];
};

const BUILDING_LABELS: Record<string, string> = {
  b1: '总部大楼',
};

const PAGE_META: Record<string, PageMeta> = {
  '/dashboard': {
    title: '总览驾驶舱',
    description: '跨资产、诊断、工单、优化、能耗和成本的业务总览。',
    welcomeTitle: '梳理今天最值得关注的运维问题',
    inputPlaceholder: '询问今日运行状态、风险或值班重点',
    prompts: ['今天最需要关注什么？', '生成一份值班摘要', '当前有哪些高风险问题？'],
  },
  '/assets': {
    title: '设备与建筑',
    description: '设备资产、通讯网关、点位在线率和维护信息。',
    welcomeTitle: '分析设备状态与历史问题',
    inputPlaceholder: '询问当前设备的运行、故障或维护问题',
    prompts: ['哪些设备最需要关注？', '解释资产健康情况', '如何排查点位离线？'],
  },
  '/fdd': {
    title: '故障检测与诊断',
    description: '异常现象、根因假设、证据指标和推荐处置。',
    welcomeTitle: '结合诊断证据调查故障原因',
    inputPlaceholder: '询问诊断逻辑、证据或现场排查步骤',
    prompts: ['解释最严重的诊断', '给出现场排查步骤', '哪些诊断应该生成工单？'],
  },
  '/alarms': {
    title: '报警工单',
    description: '工单接手、派工、处理、SLA 风险和闭环进度。',
    welcomeTitle: '整理告警根因与工单优先级',
    inputPlaceholder: '询问告警、工单、SLA 或值班交接问题',
    prompts: ['生成值班交接摘要', '哪些工单存在 SLA 风险？', '给出工单处理优先级'],
  },
  '/optimize': {
    title: '节能优化建议',
    description: '优化收益、风险、舒适度影响、回滚条件和审批状态。',
    welcomeTitle: '审查节能建议的收益与风险',
    inputPlaceholder: '询问优化收益、风险、前置条件或回滚方案',
    prompts: ['哪些建议值得批准？', '检查当前建议的风险', '总结待审批优化策略'],
  },
  '/energy': {
    title: '能耗分析',
    description: '能耗趋势、基线、峰谷负荷、系统构成和设备贡献。',
    welcomeTitle: '调查当前周期的能耗变化',
    inputPlaceholder: '询问当前周期、设备贡献或峰谷负荷',
    prompts: ['为什么能耗升高？', '哪些设备贡献最大？', '分析峰谷负荷是否合理'],
  },
  '/cost': {
    title: '成本与绩效',
    description: '峰平谷电费、节能收益、碳减排、ROI 和回收周期。',
    welcomeTitle: '解释成本变化与节能绩效',
    inputPlaceholder: '询问费用、收益、碳减排或 ROI',
    prompts: ['总结当前节能收益', '峰时段费用是否过高？', '生成管理层汇报摘要'],
  },
  '/ai': {
    title: '禾苗 AI 运维助手',
    description: '全局浮动助手与完整工作台共用 Agent 上下文。',
    welcomeTitle: '跨模块调查智慧能源系统问题',
    inputPlaceholder: '描述需要调查的设备、能耗或运维问题',
    prompts: ['分析当前系统状态', '生成运营日报', '说明可用的数据来源'],
  },
  '/system': {
    title: '系统管理',
    description: '用户权限、站点资产、数据源、告警规则和审计日志。',
    welcomeTitle: '检查数据接入与系统治理状态',
    inputPlaceholder: '询问权限、数据源、规则或审计问题',
    prompts: ['检查系统接入状态', '总结当前权限模型', '哪些数据源处于降级状态？'],
  },
};

const ROUTES = ['/dashboard', '/assets', '/fdd', '/alarms', '/optimize', '/energy', '/cost', '/ai', '/system', '/bigscreen'];

function normalizeRoute(pathname: string) {
  const root = `/${pathname.replace(/^\//, '').split('/')[0] || 'dashboard'}`;
  return PAGE_META[root] ? root : '/dashboard';
}

function getEnergyPeriodLabel(pathname: string, params: URLSearchParams) {
  const level = pathname.split('/')[2];
  const levelLabel = level === 'year'
    ? '年度'
    : level === 'month'
      ? '月度'
      : level === 'week'
        ? '周度'
        : level === 'day'
          ? '日度'
          : undefined;
  const year = params.get('year');
  const month = params.get('month');
  const day = params.get('day');
  const week = params.get('week');
  const parts = [levelLabel];
  if (year) parts.push(`${year} 年`);
  if (month) parts.push(`${month} 月`);
  if (week) parts.push(`第 ${week} 周`);
  if (day) parts.push(`${day} 日`);
  return parts.filter(Boolean).join(' · ') || undefined;
}

function getObjectLabel(
  route: string,
  pathname: string,
  search: string,
  workOrders: ReturnType<typeof useOps.getState>['workOrders'],
  suggestions: ReturnType<typeof useOps.getState>['suggestions'],
) {
  const params = new URLSearchParams(search);
  const deviceId = params.get('device');
  const diagnosisId = params.get('diagnosis');
  const workOrderId = params.get('workOrder');
  const suggestionId = params.get('suggestion');

  if (deviceId) return DEVICE_META[deviceId]?.name ?? deviceId;
  if (diagnosisId) return fddList.find((item) => item.id === diagnosisId)?.phenomenon ?? diagnosisId;
  if (workOrderId) return workOrders.find((item) => item.id === workOrderId)?.title ?? workOrderId;
  if (suggestionId) return suggestions.find((item) => item.id === suggestionId)?.title ?? suggestionId;
  if (route === '/energy') return getEnergyPeriodLabel(pathname, params);
  return undefined;
}

export function useAiApplicationContext(): AiApplicationContext {
  const location = useLocation();
  const { buildingId, role, demoMode } = useUi();
  const workOrders = useOps((state) => state.workOrders);
  const suggestions = useOps((state) => state.suggestions);

  return useMemo(() => {
    const route = normalizeRoute(location.pathname);
    const meta = PAGE_META[route];
    const buildingLabel = BUILDING_LABELS[buildingId] ?? buildingId;
    const objectLabel = getObjectLabel(route, location.pathname, location.search, workOrders, suggestions);
    const highRiskDiagnoses = fddList.filter((item) => item.severity === 'critical' || item.severity === 'major').length;
    const slaRiskWorkOrders = workOrders.filter(isWorkOrderSlaRisk).length;
    const pendingOptimizations = suggestions.filter(isSuggestionPendingDecision).length;

    return {
      route,
      pageTitle: meta.title,
      pageDescription: meta.description,
      buildingId,
      buildingLabel,
      role,
      roleLabel: ROLE_LABEL[role],
      demoMode,
      objectLabel,
      scopeLabel: [buildingLabel, meta.title, objectLabel].filter(Boolean).join(' · '),
      welcomeTitle: meta.welcomeTitle,
      inputPlaceholder: objectLabel ? `询问「${objectLabel}」相关问题` : meta.inputPlaceholder,
      attentionCount: highRiskDiagnoses + slaRiskWorkOrders + pendingOptimizations,
      metrics: {
        activeWorkOrders: workOrders.filter(isWorkOrderActive).length,
        slaRiskWorkOrders,
        activeDiagnoses: fddList.length,
        highRiskDiagnoses,
        pendingOptimizations,
      },
      permittedRoutes: ROUTES.filter((path) => canViewPath(role, path)),
      suggestedPrompts: meta.prompts,
    };
  }, [buildingId, demoMode, location.pathname, location.search, role, suggestions, workOrders]);
}
