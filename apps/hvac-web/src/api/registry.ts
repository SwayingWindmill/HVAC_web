import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { ZodError } from 'zod';
import { platformGatewayClient as client } from './platform-client';
import {
  PlatformApiError,
  type AssetMutation,
  type AssignTemplateRequest,
  type DeviceMutation,
  type ImportCommitRequest,
  type ImportPlanRequest,
  type PointMutation,
  type RebindRequest,
  type RegistryListParams,
  type RegistrySearchParams,
  type ReleaseTemplateRequest,
  type ReplaceDeviceHardwareRequest,
  type ReplaceDeviceHardwareResult,
  type RetireRequest,
  type SiteMutation,
  type SpaceMutation,
  type TemplateAssignmentListParams,
  type TemplateKind,
  type TemplateListParams,
  uuidV7Schema,
} from './generated/platformGateway.gen';

const DEFAULT_PAGE_SIZE = 50;

export type RegistryErrorKind =
  | 'not-found'
  | 'invalid-request'
  | 'invalid-cursor'
  | 'unavailable'
  | 'timeout'
  | 'mapping'
  | 'revision-conflict'
  | 'binding-conflict'
  | 'import-conflict'
  | 'mutation-conflict'
  | 'unknown';

export interface RegistryErrorPresentation {
  kind: RegistryErrorKind;
  title: string;
  description: string;
  retryable: boolean;
  traceId?: string;
}

const registryQueryEnabled = (enabled: boolean) => enabled;
const registryId = (value: string) => uuidV7Schema.parse(value);

const retryRegistryQuery = (failureCount: number, error: Error) => {
  if (failureCount >= 1 || error instanceof ZodError) return false;
  if (!(error instanceof PlatformApiError)) return true;
  return error.problem.retryable;
};

const nextCursor = (collection: { hasMore: boolean; nextCursor: string | null }) =>
  collection.hasMore ? collection.nextCursor ?? undefined : undefined;

const INITIAL_CURSOR: string | undefined = undefined;

export const flattenRegistryPages = <T>(data: { pages: Array<{ items: T[] }> } | undefined): T[] =>
  data?.pages.flatMap((page) => page.items) ?? [];

const HAS_CHINESE_RE = /[\u4e00-\u9fff]/;

function registryDescription(detail: string | undefined, fallback: string): string {
  if (detail && HAS_CHINESE_RE.test(detail)) return detail;
  return fallback;
}

export function presentRegistryError(error: unknown): RegistryErrorPresentation {
  if (error instanceof ZodError) {
    return {
      kind: 'invalid-request',
      title: '资源链接无效',
      description: '这条记录的编号不对，请从列表里重新打开它。',
      retryable: false,
    };
  }
  if (error instanceof PlatformApiError) {
    const { code, detail, retryable, traceId, status } = error.problem;
    if (status === 401 || code === 'AUTHENTICATION_REQUIRED' || code === 'SESSION_INVALID') {
      return { kind: 'unavailable', title: '登录已失效', description: '请重新登录后继续操作。', retryable: false, traceId };
    }
    if (status === 403 || code === 'FORBIDDEN') {
      return { kind: 'unknown', title: '没有这项操作权限', description: registryDescription(detail, '当前账号无权修改登记信息，请联系管理员开通。'), retryable: false, traceId };
    }
    switch (code) {
      case 'RESOURCE_NOT_FOUND':
        return { kind: 'not-found', title: '资源不可见或不存在', description: registryDescription(detail, '找不到这条登记记录，可能已被停用或你没有查看权限。'), retryable, traceId };
      case 'CURSOR_INVALID':
        return { kind: 'invalid-cursor', title: '列表已更新，请刷新重试', description: registryDescription(detail, '当前翻页位置已失效，请重新加载列表。'), retryable, traceId };
      case 'REGISTRY_UNAVAILABLE':
        return { kind: 'unavailable', title: '设备登记服务暂不可用', description: registryDescription(detail, '暂时无法读取或保存登记数据，请稍后再试。'), retryable, traceId };
      case 'REGISTRY_TIMEOUT':
        return { kind: 'timeout', title: '读取设备登记超时', description: registryDescription(detail, '请求响应超时，请稍后重试。'), retryable, traceId };
      case 'MAPPING_INVALID':
      case 'MAPPING_QUARANTINED':
        return { kind: 'mapping', title: '点位映射尚未就绪', description: registryDescription(detail, '点位对应关系还未配置好，请先核对点位绑定。'), retryable, traceId };
      case 'REGISTRY_REVISION_CONFLICT':
        return { kind: 'revision-conflict', title: '资源已被其他操作更新', description: '你改的是旧版本。请刷新后再决定要不要重新修改。', retryable: false, traceId };
      case 'REGISTRY_BINDING_CONFLICT':
        return { kind: 'binding-conflict', title: '绑定变更冲突', description: registryDescription(detail, '目标对象已有冲突绑定或正在被引用，请先调整关联关系。'), retryable: false, traceId };
      case 'REGISTRY_IMPORT_PLAN_INVALID':
        return { kind: 'import-conflict', title: '导入计划已失效', description: '请重新校验导入内容后再提交。', retryable: false, traceId };
      case 'REGISTRY_MUTATION_INVALID':
        return { kind: 'invalid-request', title: '输入校验未通过', description: registryDescription(detail, '填写的内容格式有误或与现有编码重复，请核对后再保存。'), retryable: false, traceId };
      case 'REGISTRY_IDEMPOTENCY_CONFLICT':
      case 'TEMPLATE_REVISION_IMMUTABLE':
        return { kind: 'mutation-conflict', title: '登记保存冲突', description: registryDescription(detail, '已发布的版本或同批次请求不能重复修改，请刷新后查看最新状态。'), retryable: false, traceId };
      default:
        return { kind: 'unknown', title: '设备登记操作失败', description: registryDescription(detail, '请求未能完成，请检查输入或稍后重试。'), retryable, traceId };
    }
  }
  return {
    kind: 'unavailable',
    title: '无法连接设备登记服务',
    description: '暂时读不到设备登记数据，请稍后重试。',
    retryable: true,
  };
}

export function useRegistrySites(enabled = true) {
  return useInfiniteQuery({
    queryKey: ['registry', 'sites'],
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistryListParams = { limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      return (await client.listSites(params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled),
    retry: retryRegistryQuery,
  });
}

export function useRegistrySite(siteId: string | null, enabled = true) {
  return useQuery({
    queryKey: ['registry', 'site', siteId],
    queryFn: async ({ signal }) => (await client.getSite(registryId(siteId!), { signal })).data,
    enabled: registryQueryEnabled(enabled && Boolean(siteId)),
    retry: retryRegistryQuery,
  });
}

export function useRegistryDevices(siteId: string | null, enabled = true) {
  return useInfiniteQuery({
    queryKey: ['registry', 'sites', siteId, 'devices'],
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistryListParams = { limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      return (await client.listSiteDevices(registryId(siteId!), params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && Boolean(siteId)),
    retry: retryRegistryQuery,
  });
}

export const registryTemplateKeys = {
  all: () => ['registry', 'templates'] as const,
  list: (templateKind?: TemplateKind) => ['registry', 'templates', templateKind ?? 'ALL'] as const,
  revision: (revisionId: string | null) => ['registry', 'template-revision', revisionId] as const,
  siteAssignments: (siteId: string | null, targetType?: TemplateKind, targetId?: string | null) =>
    ['registry', 'sites', siteId, 'template-assignments', targetType ?? 'ALL', targetId ?? 'ALL'] as const,
};

const registryResourceFilterKeys = {
  assets: (scope: 'all-sites' | string | null, params: Omit<RegistrySearchParams, 'limit' | 'cursor'>) =>
    ['registry', 'filtered-assets', scope, params] as const,
  devices: (scope: 'all-sites' | string | null, params: Omit<RegistrySearchParams, 'limit' | 'cursor'>) =>
    ['registry', 'filtered-devices', scope, params] as const,
};

export function useRegistryTemplates(templateKind?: TemplateKind, enabled = true) {
  return useInfiniteQuery({
    queryKey: registryTemplateKeys.list(templateKind),
    queryFn: async ({ pageParam, signal }) => {
      const params: TemplateListParams = { limit: DEFAULT_PAGE_SIZE };
      if (templateKind) params.templateKind = templateKind;
      if (typeof pageParam === 'string') params.cursor = pageParam;
      return (await client.listTemplates(params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled),
    retry: retryRegistryQuery,
  });
}

export function useRegistryTemplateRevision(revisionId: string | null, enabled = true) {
  return useQuery({
    queryKey: registryTemplateKeys.revision(revisionId),
    queryFn: async ({ signal }) => (await client.getTemplateRevision(registryId(revisionId!), { signal })).data,
    enabled: registryQueryEnabled(enabled && Boolean(revisionId)),
    retry: retryRegistryQuery,
  });
}

export function useRegistrySiteTemplateAssignments(
  siteId: string | null,
  params: { targetType?: TemplateKind; targetId?: string } = {},
  enabled = true,
) {
  return useInfiniteQuery({
    queryKey: registryTemplateKeys.siteAssignments(siteId, params.targetType, params.targetId),
    queryFn: async ({ pageParam, signal }) => {
      const query: TemplateAssignmentListParams = { limit: DEFAULT_PAGE_SIZE };
      if (params.targetType) query.targetType = params.targetType;
      if (params.targetId) query.targetId = params.targetId;
      if (typeof pageParam === 'string') query.cursor = pageParam;
      return (await client.listSiteTemplateAssignments(registryId(siteId!), query, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && Boolean(siteId)),
    retry: retryRegistryQuery,
  });
}

export function useRegistryFilteredAssets(
  siteId: string | null,
  allSites: boolean,
  filters: Omit<RegistrySearchParams, 'limit' | 'cursor' | 'siteId'>,
  enabled = true,
) {
  return useInfiniteQuery({
    queryKey: registryResourceFilterKeys.assets(allSites ? 'all-sites' : siteId, filters),
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistrySearchParams = { ...filters, limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      if (allSites) {
        return (await client.listAssets(params, { signal })).data;
      }
      return (await client.listSiteAssets(registryId(siteId!), params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && (allSites || Boolean(siteId))),
    retry: retryRegistryQuery,
  });
}

export function useRegistryFilteredDevices(
  siteId: string | null,
  allSites: boolean,
  filters: Omit<RegistrySearchParams, 'limit' | 'cursor' | 'siteId'>,
  enabled = true,
) {
  return useInfiniteQuery({
    queryKey: registryResourceFilterKeys.devices(allSites ? 'all-sites' : siteId, filters),
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistrySearchParams = { ...filters, limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      if (allSites) {
        return (await client.listDevices(params, { signal })).data;
      }
      return (await client.listSiteDevices(registryId(siteId!), params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && (allSites || Boolean(siteId))),
    retry: retryRegistryQuery,
  });
}

export function useRegistryDevicePoints(deviceId: string | null, enabled = true) {
  return useInfiniteQuery({
    queryKey: ['registry', 'devices', deviceId, 'points'],
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistryListParams = { limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      return (await client.listDevicePoints(registryId(deviceId!), params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && Boolean(deviceId)),
    retry: retryRegistryQuery,
  });
}

export const registryAdminApi = {
  createSite: async (body: SiteMutation, csrfToken: string) => (await client.createSite(body, csrfToken)).data,
  updateSite: async (siteId: string, body: SiteMutation, csrfToken: string) => (await client.updateSite(registryId(siteId), body, csrfToken)).data,
  createSpace: async (siteId: string, body: SpaceMutation, csrfToken: string) => (await client.createSiteSpace(registryId(siteId), body, csrfToken)).data,
  updateSpace: async (siteId: string, spaceId: string, body: SpaceMutation, csrfToken: string) => (await client.updateSiteSpace(registryId(siteId), registryId(spaceId), body, csrfToken)).data,
  createAsset: async (siteId: string, body: AssetMutation, csrfToken: string) => (await client.createSiteAsset(registryId(siteId), body, csrfToken)).data,
  updateAsset: async (siteId: string, assetId: string, body: AssetMutation, csrfToken: string) => (await client.updateSiteAsset(registryId(siteId), registryId(assetId), body, csrfToken)).data,
  createDevice: async (siteId: string, body: DeviceMutation, csrfToken: string) => (await client.createSiteDevice(registryId(siteId), body, csrfToken)).data,
  updateDevice: async (siteId: string, deviceId: string, body: DeviceMutation, csrfToken: string) => (await client.updateSiteDevice(registryId(siteId), registryId(deviceId), body, csrfToken)).data,
  replaceDeviceHardware: async (siteId: string, deviceId: string, body: ReplaceDeviceHardwareRequest, csrfToken: string): Promise<ReplaceDeviceHardwareResult> => (
    await client.replaceDeviceHardware(registryId(siteId), registryId(deviceId), body, csrfToken)
  ).data,
  createPoint: async (siteId: string, body: PointMutation, csrfToken: string) => (await client.createSitePoint(registryId(siteId), body, csrfToken)).data,
  updatePoint: async (siteId: string, pointId: string, body: PointMutation, csrfToken: string) => (await client.updateSitePoint(registryId(siteId), registryId(pointId), body, csrfToken)).data,
  rebind: async (siteId: string, body: RebindRequest, csrfToken: string) => (await client.rebindRegistryResource(registryId(siteId), body, csrfToken)).data,
  releaseTemplate: async (body: ReleaseTemplateRequest, csrfToken: string) => (await client.releaseRegistryTemplateRevision(body, csrfToken)).data,
  assignTemplate: async (body: AssignTemplateRequest, csrfToken: string) => (await client.assignRegistryTemplateRevision(body, csrfToken)).data,
  planImport: async (siteId: string, body: ImportPlanRequest, csrfToken: string) => (await client.planRegistryImport(registryId(siteId), body, csrfToken)).data,
  commitImport: async (siteId: string, body: ImportCommitRequest, csrfToken: string) => (await client.commitRegistryImport(registryId(siteId), body, csrfToken)).data,
  retire: async (siteId: string, body: RetireRequest, csrfToken: string) => (await client.beginRegistryRetirement(registryId(siteId), body, csrfToken)).data,
};
