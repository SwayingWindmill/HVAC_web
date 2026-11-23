import { PlatformApiError } from '../../api/generated/platformGateway.gen.ts';

export type GatewayConfigShadowSyncStatus = 'UNCONFIGURED' | 'PENDING_SYNC' | 'SYNCED' | 'DRIFTED';
export type GatewayConfigShadowAppliedStatus = 'APPLIED' | 'PARTIAL' | 'REJECTED';
export type GatewayConfigShadowDriftSection =
  | 'POINT_MAPPINGS'
  | 'SAMPLING_POLICIES'
  | 'CONCENTRATOR'
  | 'DRIVER_CONFIGS'
  | 'AUTONOMY_POLICY'
  | 'EDGE_RULES'
  | 'EDGE_MODELS';
export type GatewayConfigShadowDriftReason =
  | 'VALUE_MISMATCH'
  | 'MISSING_ON_EDGE'
  | 'UNEXPECTED_ON_EDGE'
  | 'EDGE_APPLY_FAILED';

export const CONFIG_SHADOW_SYNC_PRESENTATION: Readonly<
  Record<
    GatewayConfigShadowSyncStatus,
    {
      readonly label: string;
      readonly tone: 'neutral' | 'warning' | 'success' | 'danger';
    }
  >
> = {
  UNCONFIGURED: { label: '未配置', tone: 'neutral' },
  PENDING_SYNC: { label: '待同步下发', tone: 'warning' },
  SYNCED: { label: '云边已一致', tone: 'success' },
  DRIFTED: { label: '配置存在漂移', tone: 'danger' },
};

export const CONFIG_SHADOW_APPLIED_LABELS: Readonly<Record<GatewayConfigShadowAppliedStatus, string>> = {
  APPLIED: '已全量生效',
  PARTIAL: '部分生效',
  REJECTED: '边缘端已拒绝',
};

export const CONFIG_SHADOW_SECTION_LABELS: Readonly<Record<GatewayConfigShadowDriftSection, string>> = {
  POINT_MAPPINGS: '点位映射',
  SAMPLING_POLICIES: '采集周期',
  CONCENTRATOR: '集中器配置',
  DRIVER_CONFIGS: '协议驱动点表',
  AUTONOMY_POLICY: '断网自治策略',
  EDGE_RULES: '边缘规则包',
  EDGE_MODELS: '端侧模型与超参',
};

export const CONFIG_SHADOW_REASON_LABELS: Readonly<Record<GatewayConfigShadowDriftReason, string>> = {
  VALUE_MISMATCH: '配置值不一致',
  MISSING_ON_EDGE: '边缘端缺失',
  UNEXPECTED_ON_EDGE: '边缘端多出未登记项',
  EDGE_APPLY_FAILED: '边缘端应用失败',
};

export function describeGatewayConfigShadowError(
  error: unknown,
  fallback = '网关配置影子读取或保存失败，请稍后重试',
): string {
  if (error instanceof PlatformApiError) {
    if (error.problem.status === 409 || error.problem.code === 'CONFIG_SHADOW_REVISION_CONFLICT') {
      return '配置影子的期望修订已被其他操作更新，请刷新最新配置后再保存';
    }
    if (error.problem.code === 'GATEWAY_REVOKED') {
      return '该网关接入凭据已停用，无法修改配置影子；如需恢复请先重新启用网关';
    }
    if (error.problem.status === 403 || error.problem.code === 'CREDENTIAL_FORBIDDEN') {
      return '当前账号没有修改该网关配置影子的权限';
    }
    if (error.problem.status === 422 || error.problem.code === 'CONFIG_SHADOW_INVALID') {
      return error.problem.detail || '配置影子字段校验未通过，请检查点位映射与采集周期设置';
    }
    return error.problem.detail || fallback;
  }
  if (error instanceof Error && error.message.trim().length > 0) {
    return error.message;
  }
  return fallback;
}
