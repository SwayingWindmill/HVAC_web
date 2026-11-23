import {
  PlatformApiError,
  createPlatformGatewayClient,
  type GatewayHostMetricsSummary,
  type GatewayLogBundleResult,
  type GatewayWaveformCapture,
  type GatewayWaveformRuleUpsertRequest,
  type PullGatewayLogsRequest,
  type TriggerWaveformCaptureRequest,
  type WaveformTriggerType,
} from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();

export const gatewayWaveformRulesKey = (gatewayId: string) =>
  ['gateway-waveform-rules', gatewayId] as const;

export const gatewayWaveformCapturesKey = (gatewayId: string) =>
  ['gateway-waveform-captures', gatewayId] as const;

export const gatewayWaveformCaptureDetailKey = (gatewayId: string, captureId: string) =>
  ['gateway-waveform-capture', gatewayId, captureId] as const;

export const gatewayHostMetricsKey = (gatewayId: string) =>
  ['gateway-host-metrics', gatewayId] as const;

export const WAVEFORM_TRIGGER_LABELS: Readonly<Record<WaveformTriggerType, string>> = {
  THRESHOLD_HIGH: '高限越限',
  THRESHOLD_LOW: '低限越限',
  DELTA_JUMP: '瞬间突变',
  TRIP_STATE: '跳机状态',
};

export const WAVEFORM_SOURCE_LABELS: Readonly<Record<'AUTO_RULE' | 'MANUAL', string>> = {
  AUTO_RULE: '规则自动触发',
  MANUAL: '运维手动抓拍',
};

export const WAVEFORM_STATUS_LABELS: Readonly<Record<'PENDING' | 'COMPLETED' | 'FAILED', string>> = {
  PENDING: '等待回流',
  COMPLETED: '已完成回流',
  FAILED: '回流失败',
};

const REMOTE_DIAG_ERRORS: Readonly<Record<string, string>> = {
  CREDENTIAL_FORBIDDEN: '当前账号缺少设备诊断或日志拉取权限（403）。',
  GATEWAY_NOT_FOUND: '未找到所选网关设备，请确认网关在线与登记状态。',
  GATEWAY_DIAGNOSTICS_OFFLINE: '边缘网关离线或未在超时时间内应答诊断请求。',
  WAVEFORM_DIAGNOSTICS_INVALID: '波形抓拍参数不符合校验范围，请核对后重试。',
  WAVEFORM_RULE_INVALID: '触发规则参数无效，请核对点位编码与前后窗口设置。',
  CSRF_INVALID: '安全令牌已失效，请刷新页面后重试。',
};

export function describeRemoteDiagError(error: unknown, fallback: string): string {
  if (error instanceof PlatformApiError) {
    if (error.problem.status === 403) {
      return '当前账号缺少执行远程日志拉取或抓拍下发的权限（403 已拒绝并记入审计）。';
    }
    const mapped = REMOTE_DIAG_ERRORS[error.problem.code];
    if (mapped) return mapped;
    if (error.problem.detail) return error.problem.detail;
  }
  return error instanceof Error && error.message ? error.message : fallback;
}

export const gatewayRemoteDiagApi = {
  listWaveformRules: async (gatewayId: string, signal?: AbortSignal) =>
    (await client.listGatewayWaveformRules(gatewayId, { signal })).data,
  createWaveformRule: async (
    gatewayId: string,
    body: GatewayWaveformRuleUpsertRequest,
    csrfToken: string,
  ) => (await client.createGatewayWaveformRule(gatewayId, body, csrfToken)).data,
  updateWaveformRule: async (
    gatewayId: string,
    ruleId: string,
    body: GatewayWaveformRuleUpsertRequest,
    csrfToken: string,
  ) => (await client.updateGatewayWaveformRule(gatewayId, ruleId, body, csrfToken)).data,
  deleteWaveformRule: async (gatewayId: string, ruleId: string, csrfToken: string) =>
    (await client.deleteGatewayWaveformRule(gatewayId, ruleId, csrfToken)).data,
  listWaveformCaptures: async (gatewayId: string, limit = 20, signal?: AbortSignal) =>
    (await client.listGatewayWaveformCaptures(gatewayId, { limit }, { signal })).data,
  getWaveformCapture: async (gatewayId: string, captureId: string, signal?: AbortSignal) =>
    (await client.getGatewayWaveformCapture(gatewayId, captureId, { signal })).data,
  triggerWaveformCapture: async (
    gatewayId: string,
    body: TriggerWaveformCaptureRequest,
    csrfToken: string,
  ) => (await client.triggerGatewayWaveformCapture(gatewayId, body, csrfToken)).data,
  pullLogs: async (gatewayId: string, body: PullGatewayLogsRequest, csrfToken: string) =>
    (await client.pullGatewayDiagnosticsLogs(gatewayId, body, csrfToken)).data,
  getHostMetrics: async (gatewayId: string, limit = 20, signal?: AbortSignal) =>
    (await client.getGatewayHostMetrics(gatewayId, { limit }, { signal })).data,
};

function sanitizeCsvCell(value: string): string {
  const safe = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value;
  return `"${safe.replace(/"/g, '""')}"`;
}

export function buildWaveformCSV(capture: GatewayWaveformCapture): string {
  const keys = Array.from(
    capture.samples.reduce((set, row) => {
      for (const k of Object.keys(row.values)) set.add(k);
      return set;
    }, new Set<string>()),
  ).sort();
  const header = ['timestamp_utc', 'sampled_at_ms', ...keys].map(sanitizeCsvCell).join(',');
  const rows = capture.samples.map((row) => {
    const iso = new Date(row.sampledAtMs).toISOString();
    const cols = [
      iso,
      String(row.sampledAtMs),
      ...keys.map((k) => {
        const v = row.values[k];
        return v === undefined ? '' : String(v);
      }),
    ];
    return cols.map(sanitizeCsvCell).join(',');
  });
  return [header, ...rows].join('\n');
}

export function triggerTextDownload(filename: string, mimeType: string, content: string): void {
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export type { GatewayHostMetricsSummary, GatewayLogBundleResult, GatewayWaveformCapture };
