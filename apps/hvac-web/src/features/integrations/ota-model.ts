import {
  PlatformApiError,
  type Device,
  type OTACampaignSummary,
  type OTADeviceTask,
  type OTAPackage,
} from '../../api/generated/platformGateway.gen.ts';
import type { GovernanceStatusPresentation } from './governance-shared-model.ts';
import { MAX_TAG_VALUE_LENGTH, TAG_KEY_PATTERN, type TagEntryDraft } from '../system/registry-admin/tags-model.ts';

export const OTA_MAX_PACKAGE_BYTES = 64 * 1024 * 1024;
export const OTA_HARDWARE_MODEL_MAX_LEN = 64;
export const OTA_MAX_HARDWARE_MODELS = 32;

const SEMVER_PATTERN = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
const SHA256_HEX_PATTERN = /^[0-9a-f]{64}$/i;

export const OTA_COMPONENT_LABELS: Readonly<Record<OTAPackage['component'], string>> = {
  EDGE_GATEWAY: '边缘守护进程',
  FIRMWARE: '设备固件',
};

export const OTA_ARCH_LABELS: Readonly<Record<OTAPackage['targetArch'], string>> = {
  armv7l: 'ARMv7 (armv7l)',
  arm64: 'ARM64 (arm64)',
  amd64: 'x86_64 (amd64)',
};

export const OTA_PACKAGE_STATUS_PRESENTATION: Readonly<Record<OTAPackage['status'], GovernanceStatusPresentation>> = {
  ACTIVE: {
    level: 'NORMAL',
    shape: '○',
    tone: 'success',
    label: '可用',
  },
  RETIRED: {
    level: 'NOTICE',
    shape: '■',
    tone: 'neutral',
    label: '已停用',
  },
};

export const OTA_CAMPAIGN_STATUS_PRESENTATION: Readonly<
  Record<OTACampaignSummary['status'], GovernanceStatusPresentation>
> = {
  CANARY_RUNNING: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '灰度进行中',
  },
  ROLLOUT_RUNNING: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '全量进行中',
  },
  PAUSED: {
    level: 'WARNING',
    shape: '■',
    tone: 'warning',
    label: '已暂停',
  },
  COMPLETED: {
    level: 'NORMAL',
    shape: '○',
    tone: 'success',
    label: '已完成',
  },
  ROLLED_BACK: {
    level: 'NOTICE',
    shape: '■',
    tone: 'neutral',
    label: '已回滚',
  },
  FAILED: {
    level: 'CRITICAL',
    shape: '◆',
    tone: 'danger',
    label: '已失败',
  },
};

export const OTA_PAUSE_REASON_LABELS: Readonly<Record<string, string>> = {
  MANUAL_PAUSE: '人工暂停',
  FAILURE_RATE_THRESHOLD_EXCEEDED: '失败率达到熔断阈值，已自动暂停',
  CANARY_AWAITING_PROMOTION: '灰度批已完成，等待手动确认放量',
};

export const OTA_TASK_WAVE_LABELS: Readonly<Record<OTADeviceTask['wave'], string>> = {
  CANARY: '灰度批',
  ROLLOUT: '全量批',
};

export const OTA_TASK_ACTION_LABELS: Readonly<Record<OTADeviceTask['desiredAction'], string>> = {
  APPLY: '升级',
  ROLLBACK: '回滚',
};

export const OTA_TASK_STAGE_PRESENTATION: Readonly<
  Record<OTADeviceTask['stage'], GovernanceStatusPresentation>
> = {
  PENDING: {
    level: 'NOTICE',
    shape: '■',
    tone: 'neutral',
    label: '等待下发',
  },
  DISPATCHED: {
    level: 'NOTICE',
    shape: '■',
    tone: 'neutral',
    label: '已下发',
  },
  DOWNLOADING: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '下载中',
  },
  VERIFYING: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '校验签名中',
  },
  APPLYING: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '切换版本中',
  },
  HEALTH_CHECKING: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '健康检查中',
  },
  SUCCEEDED: {
    level: 'NORMAL',
    shape: '○',
    tone: 'success',
    label: '升级成功',
  },
  FAILED: {
    level: 'CRITICAL',
    shape: '◆',
    tone: 'danger',
    label: '执行失败',
  },
  ROLLED_BACK: {
    level: 'NOTICE',
    shape: '■',
    tone: 'neutral',
    label: '已回滚',
  },
  CANCELLED: {
    level: 'NOTICE',
    shape: '■',
    tone: 'neutral',
    label: '已取消',
  },
};

export interface OtaWorkspaceSummary {
  readonly activeCampaigns: number;
  readonly canaryRunning: number;
  readonly rolloutRunning: number;
  readonly pausedCampaigns: number;
  readonly circuitBrokenCampaigns: number;
  readonly completedCampaigns: number;
  readonly activePackages: number;
  readonly totalPackages: number;
}

export interface SuggestedGatewayTag {
  readonly key: string;
  readonly value: string;
}

export function isOTACampaignActive(status: OTACampaignSummary['status']): boolean {
  return status === 'CANARY_RUNNING' || status === 'ROLLOUT_RUNNING';
}

export function formatOtaPauseReason(pauseReason: string | null | undefined): string | null {
  if (!pauseReason) return null;
  const trimmed = pauseReason.trim();
  if (!trimmed) return null;
  return OTA_PAUSE_REASON_LABELS[trimmed] ?? trimmed;
}

export function formatByteSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MiB`;
}

export function validateSemver(raw: string): string | null {
  const trimmed = raw.trim();
  if (!trimmed) return '请填写语义化版本号（如 1.5.0）';
  if (trimmed.length > 64) return '版本号长度不能超过 64 个字符';
  if (!SEMVER_PATTERN.test(trimmed)) {
    return '版本号格式须为 MAJOR.MINOR.PATCH，例如 1.5.0 或 1.5.0-rc.1';
  }
  return null;
}

export function validateOptionalSemver(raw: string): string | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  if (trimmed.length > 64) return '最低兼容版本号长度不能超过 64 个字符';
  if (!SEMVER_PATTERN.test(trimmed)) {
    return '最低兼容版本格式须为 MAJOR.MINOR.PATCH，例如 1.2.0';
  }
  return null;
}

export function validateSha256Hex(raw: string): string | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  if (!SHA256_HEX_PATTERN.test(trimmed)) {
    return 'SHA-256 校验值须为 64 位十六进制字符';
  }
  return null;
}

export function parseHardwareModelsInput(raw: string): readonly string[] {
  const seen = new Set<string>();
  const models: string[] = [];
  for (const part of raw.split(/[\s,，]+/)) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const key = trimmed.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    models.push(trimmed);
  }
  return models;
}

export function validateHardwareModelsInput(raw: string): string | null {
  const models = parseHardwareModelsInput(raw);
  if (models.length > OTA_MAX_HARDWARE_MODELS) {
    return `适配硬件型号最多填写 ${OTA_MAX_HARDWARE_MODELS} 个`;
  }
  for (const model of models) {
    if (model.length > OTA_HARDWARE_MODEL_MAX_LEN) {
      return `硬件型号「${model}」长度不能超过 ${OTA_HARDWARE_MODEL_MAX_LEN} 个字符`;
    }
  }
  return null;
}

export function validateSignaturePair(signingKeyId: string, signatureB64: string): string | null {
  const key = signingKeyId.trim();
  const sig = signatureB64.trim();
  if (!key && !sig) return null;
  if (!key || !sig) {
    return '外部签名密钥 ID 与 Ed25519 Base64 签名需同时填写，或均留空由平台自动签名';
  }
  if (key.length > 128) return '签名密钥 ID 不能超过 128 个字符';
  if (sig.length < 16 || sig.length > 512) return 'Base64 签名长度须在 16 至 512 个字符之间';
  return null;
}

export function normalizeTargetTagDrafts(entries: readonly TagEntryDraft[]): {
  readonly ok: true;
  readonly tags: Record<string, string>;
} | {
  readonly ok: false;
  readonly error: string;
} {
  const tags: Record<string, string> = {};
  const seen = new Set<string>();
  for (let i = 0; i < entries.length; i += 1) {
    const entry = entries[i]!;
    const key = entry.key.trim();
    const value = entry.value.trim();
    if (!key && !value) continue;
    if (!key || !TAG_KEY_PATTERN.test(key)) {
      return {
        ok: false,
        error: `第 ${i + 1} 行标签键无效：须以小写字母或数字开头，仅含小写字母、数字、._/-`,
      };
    }
    if (!value || value.length > MAX_TAG_VALUE_LENGTH) {
      return {
        ok: false,
        error: `标签「${key}」的值须为 1 至 ${MAX_TAG_VALUE_LENGTH} 个非空字符`,
      };
    }
    if (seen.has(key)) {
      return { ok: false, error: `标签键「${key}」重复` };
    }
    seen.add(key);
    tags[key] = value;
  }
  return { ok: true, tags };
}

export function summarizeOtaWorkspace(
  packages: readonly OTAPackage[],
  campaigns: readonly OTACampaignSummary[],
): OtaWorkspaceSummary {
  let canaryRunning = 0;
  let rolloutRunning = 0;
  let pausedCampaigns = 0;
  let circuitBrokenCampaigns = 0;
  let completedCampaigns = 0;

  for (const campaign of campaigns) {
    if (campaign.status === 'CANARY_RUNNING') canaryRunning += 1;
    else if (campaign.status === 'ROLLOUT_RUNNING') rolloutRunning += 1;
    else if (campaign.status === 'PAUSED') {
      pausedCampaigns += 1;
      if (campaign.pauseReason === 'FAILURE_RATE_THRESHOLD_EXCEEDED') {
        circuitBrokenCampaigns += 1;
      }
    } else if (campaign.status === 'COMPLETED') {
      completedCampaigns += 1;
    }
  }

  const activePackages = packages.filter((pkg) => pkg.status === 'ACTIVE').length;

  return {
    activeCampaigns: canaryRunning + rolloutRunning,
    canaryRunning,
    rolloutRunning,
    pausedCampaigns,
    circuitBrokenCampaigns,
    completedCampaigns,
    activePackages,
    totalPackages: packages.length,
  };
}

export function extractSuggestedTagsFromDevices(
  devices: readonly Device[],
  limit = 8,
): readonly SuggestedGatewayTag[] {
  const seen = new Set<string>();
  const suggestions: SuggestedGatewayTag[] = [];
  for (const device of devices) {
    if (device.deviceType !== 'GATEWAY' || !device.tags) continue;
    for (const [key, value] of Object.entries(device.tags)) {
      if (!key || !value) continue;
      const pairKey = `${key}=${value}`;
      if (seen.has(pairKey)) continue;
      seen.add(pairKey);
      suggestions.push({ key, value });
      if (suggestions.length >= limit) return suggestions;
    }
  }
  return suggestions;
}

const OTA_PROBLEM_MESSAGES: Readonly<Record<string, string>> = {
  CREDENTIAL_FORBIDDEN: '当前账号缺少设备读写权限，无法执行 OTA 操作。',
  FORBIDDEN: '当前账号缺少执行此 OTA 操作的权限。',
  CSRF_INVALID: '页面安全校验已过期，请刷新页面后重试。',
  CSRF_TOKEN_INVALID: '页面安全校验已过期，请刷新页面后重试。',
  OTA_PACKAGE_TOO_LARGE: '升级包体积超过 64 MiB 上限，请压缩或精简后再上传。',
  OTA_CHECKSUM_MISMATCH: '上传文件计算的 SHA-256 与预期校验值不一致，请核对文件是否完整。',
  OTA_NO_TARGETS: '没有匹配到符合条件的在线或已登记网关，请调整站点或标签筛选条件。',
  OTA_INVALID: '请求参数格式或版本约束不合法，请检查输入项后重试。',
  OTA_CONFLICT: '升级包或发布任务当前状态冲突（如相同版本已存在或任务处于不可变更状态）。',
  OTA_NOT_FOUND: '未找到指定的 OTA 升级包或发布任务。',
  OTA_REQUEST_INVALID: '提交的 OTA 请求参数不完整或格式错误。',
  OTA_UNAVAILABLE: 'OTA 升级服务暂未就绪，请稍后重试。',
  OTA_SERVICE_UNAVAILABLE: 'OTA 升级服务暂未就绪，请稍后重试。',
  CONNECTIVITY_UNAVAILABLE: '设备接入服务暂未就绪，请稍后重试。',
  OTA_UPSTREAM_ERROR: '设备接入服务处理超时或返回异常，请稍后重试。',
};

export function describeOtaError(error: unknown, fallback: string): string {
  if (error instanceof PlatformApiError) {
    const matched = OTA_PROBLEM_MESSAGES[error.problem.code];
    if (matched) return matched;
    if (error.problem.status === 403) return '当前账号没有操作 OTA 升级的权限，或页面安全校验已过期。';
    if (error.problem.status === 404) return '未找到目标升级包或发布任务。';
    if (error.problem.status === 409) return '升级包版本或任务状态冲突，请刷新后重试。';
    if (error.problem.status === 413) return '升级包体积超过 64 MiB 上限，请检查文件大小。';
    if (error.problem.status === 422 && error.problem.detail) return error.problem.detail;
  }
  return fallback;
}
