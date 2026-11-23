import {
  PlatformApiError,
  createPlatformGatewayClient,
  type AlarmRuleBaselineTemplate,
  type AlarmRuleTemplateDetail,
  type AlarmRuleTemplateRelease,
  type AlarmRuleTemplateRolloutPreview,
  type AlarmRuleTemplateRolloutRequest,
  type AlarmRuleTemplateRolloutResult,
  type AlarmRuleTemplateSpec,
  type CreateAlarmRuleTemplateRequest,
  type PublishAlarmRuleTemplateReleaseRequest,
  type ResetSiteAlarmRuleToTemplateRequest,
  type SiteAlarmRuleTemplateDriftReport,
  type UpdateAlarmRuleTemplateRequest,
} from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();

export const alarmTemplateKeys = {
  all: ['alarm-rule-templates'] as const,
  list: () => ['alarm-rule-templates', 'list'] as const,
  detail: (templateId: string) => ['alarm-rule-templates', 'detail', templateId] as const,
  siteDrift: (siteId: string) => ['alarms', siteId, 'rules', 'template-drift'] as const,
};

export function alarmRuleTemplatesQuery(enabled = true) {
  return {
    queryKey: alarmTemplateKeys.list(),
    queryFn: async ({ signal }: { readonly signal?: AbortSignal }) =>
      (await client.listAlarmRuleTemplates(undefined, { signal })).data.data.items,
    enabled,
  };
}

export function alarmRuleTemplateDetailQuery(templateId: string, enabled = true) {
  return {
    queryKey: alarmTemplateKeys.detail(templateId),
    queryFn: async ({ signal }: { readonly signal?: AbortSignal }) =>
      (await client.getAlarmRuleTemplate(templateId, { signal })).data.data,
    enabled: Boolean(templateId) && enabled,
  };
}

export function siteAlarmRuleTemplateDriftQuery(siteId: string, enabled = true) {
  return {
    queryKey: alarmTemplateKeys.siteDrift(siteId),
    queryFn: async ({ signal }: { readonly signal?: AbortSignal }) =>
      (await client.getSiteAlarmRuleTemplateDrift(siteId, { signal })).data.data,
    enabled: Boolean(siteId) && enabled,
  };
}

export const alarmTemplatesApi = {
  createTemplate: async (body: CreateAlarmRuleTemplateRequest, csrfToken: string) =>
    (await client.createAlarmRuleTemplate(body, csrfToken)).data.data,
  updateTemplate: async (templateId: string, body: UpdateAlarmRuleTemplateRequest, csrfToken: string) =>
    (await client.updateAlarmRuleTemplate(templateId, body, csrfToken)).data.data,
  publishRelease: async (templateId: string, body: PublishAlarmRuleTemplateReleaseRequest, csrfToken: string) =>
    (await client.publishAlarmRuleTemplateRelease(templateId, body, csrfToken)).data.data,
  updateBoundSites: async (templateId: string, siteIds: string[], csrfToken: string) =>
    (await client.updateAlarmRuleTemplateSites(templateId, siteIds, csrfToken)).data.data,
  previewRollout: async (templateId: string, body: AlarmRuleTemplateRolloutRequest) =>
    (await client.previewAlarmRuleTemplateRollout(templateId, body)).data.data,
  executeRollout: async (templateId: string, body: AlarmRuleTemplateRolloutRequest, csrfToken: string) =>
    (await client.executeAlarmRuleTemplateRollout(templateId, body, csrfToken)).data.data,
  resetSiteRule: async (siteId: string, ruleId: string, body: ResetSiteAlarmRuleToTemplateRequest, csrfToken: string) =>
    (await client.resetSiteAlarmRuleToTemplate(siteId, ruleId, body, csrfToken)).data.data,
};

export const TEMPLATE_SEVERITY_LABELS: Readonly<Record<'INFO' | 'WARNING' | 'MINOR' | 'MAJOR' | 'CRITICAL', string>> = {
  INFO: '提示',
  WARNING: '告警',
  MINOR: '一般',
  MAJOR: '重要',
  CRITICAL: '紧急',
};

export const TEMPLATE_EXECUTION_LABELS: Readonly<Record<'CLOUD' | 'EDGE' | 'CLOUD_AND_EDGE', string>> = {
  CLOUD: '云端评估',
  EDGE: '仅边缘网关',
  CLOUD_AND_EDGE: '云边协同',
};

export const TEMPLATE_FIELD_LABELS: Readonly<Record<string, string>> = {
  title: '规则名称',
  summary: '规则描述',
  severity: '严重程度',
  executionTarget: '执行位置',
  qualityPolicy: '数据质量策略',
  freshnessSeconds: '新鲜度窗口',
  triggerMode: '触发模式',
  durationSeconds: '持续时长',
  repeatCount: '连续次数',
  raise: '触发条件',
  clear: '恢复条件',
  actions: '联动动作',
};

const TEMPLATE_ERRORS: Readonly<Record<string, string>> = {
  FORBIDDEN: '当前账号缺少告警规则管理权限或跨站点操作权限（403）。',
  CSRF_INVALID: '安全令牌已失效，请刷新页面重试。',
  ALARM_TEMPLATE_CONFLICT: '模板编码已存在或状态发生并发冲突。',
  ALARM_TEMPLATE_INVALID: '模板规格参数不完整或校验失败，请核对输入。',
  ALARM_TEMPLATE_NO_RELEASE: '当前模板尚未发布基准版本，请先发布版本再预览下发。',
};

export function describeAlarmTemplateError(error: unknown, fallback: string): string {
  if (error instanceof PlatformApiError) {
    if (error.problem.status === 403) {
      return '当前账号缺少告警规则模板治理或跨站点授权（403 已拒绝）。';
    }
    const mapped = TEMPLATE_ERRORS[error.problem.code];
    if (mapped) return mapped;
    if (error.problem.detail) return error.problem.detail;
  }
  return error instanceof Error && error.message ? error.message : fallback;
}

export type {
  AlarmRuleBaselineTemplate,
  AlarmRuleTemplateDetail,
  AlarmRuleTemplateRelease,
  AlarmRuleTemplateRolloutPreview,
  AlarmRuleTemplateRolloutResult,
  AlarmRuleTemplateSpec,
  SiteAlarmRuleTemplateDriftReport,
};
