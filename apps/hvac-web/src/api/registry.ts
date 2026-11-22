import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { ZodError } from 'zod';
import { siteAssetModelQuery } from './site-asset-model';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type Asset,
  type AssetCollection,
  type AssetMutation,
  type AssignTemplateRequest,
  type Device,
  type DeviceBinding,
  type DeviceBindingCollection,
  type DeviceCollection,
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
  type Site,
  type SiteAssetModel,
  type SiteCollection,
  type SiteMutation,
  type Space,
  type SpaceMutation,
  type TelemetryPoint,
  type Template,
  type TemplateAssignment,
  type TemplateAssignmentCollection,
  type TemplateAssignmentListParams,
  type TemplateCollection,
  type TemplateKind,
  type TemplateListParams,
  type TemplateRevision,
  uuidV7Schema,
} from './generated/platformGateway.gen';

const client = createPlatformGatewayClient();
const DEFAULT_PAGE_SIZE = 50;
const MAX_NAVIGATION_PAGES = 12;

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

export type AuthorizedRegistrySite = Site;

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
    const { code, detail, retryable, traceId } = error.problem;
    switch (code) {
      case 'RESOURCE_NOT_FOUND':
        return { kind: 'not-found', title: '资源不可见或不存在', description: detail, retryable, traceId };
      case 'CURSOR_INVALID':
        return { kind: 'invalid-cursor', title: '列表已更新，请刷新重试', description: detail, retryable, traceId };
      case 'REGISTRY_UNAVAILABLE':
        return { kind: 'unavailable', title: '设备登记服务暂不可用', description: detail, retryable, traceId };
      case 'REGISTRY_TIMEOUT':
        return { kind: 'timeout', title: '读取设备登记超时', description: detail, retryable, traceId };
      case 'MAPPING_INVALID':
      case 'MAPPING_QUARANTINED':
        return { kind: 'mapping', title: '点位映射尚未就绪', description: detail, retryable, traceId };
      case 'REGISTRY_REVISION_CONFLICT':
        return { kind: 'revision-conflict', title: '资源已被其他操作更新', description: '你改的是旧版本。请刷新后再决定要不要重新修改。', retryable: false, traceId };
      case 'REGISTRY_BINDING_CONFLICT':
        return { kind: 'binding-conflict', title: '绑定变更冲突', description: detail, retryable: false, traceId };
      case 'REGISTRY_IMPORT_PLAN_INVALID':
        return { kind: 'import-conflict', title: '导入计划已失效', description: '请重新校验导入内容后再提交。', retryable: false, traceId };
      case 'REGISTRY_MUTATION_INVALID':
        return { kind: 'invalid-request', title: '输入校验未通过', description: detail, retryable: false, traceId };
      case 'REGISTRY_IDEMPOTENCY_CONFLICT':
      case 'TEMPLATE_REVISION_IMMUTABLE':
        return { kind: 'mutation-conflict', title: '登记保存冲突', description: detail, retryable: false, traceId };
      default:
        return { kind: 'unknown', title: '设备登记操作失败', description: detail, retryable, traceId };
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

/** A Site's asset model as the Registry holds it, for reading several Sites at once. */
export function registryAssetModelQuery(siteId: string, enabled = true) {
  return siteAssetModelQuery(siteId, {
    enabled: registryQueryEnabled(enabled),
    retry: retryRegistryQuery,
  });
}

export function useRegistryAssetModel(siteId: string | null, enabled = true) {
  return useQuery(
    siteAssetModelQuery(siteId, {
      enabled: registryQueryEnabled(enabled && Boolean(siteId)),
      retry: retryRegistryQuery,
    }),
  );
}

export function useRegistryAssets(siteId: string | null, enabled = true) {
  return useInfiniteQuery({
    queryKey: ['registry', 'sites', siteId, 'assets'],
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistryListParams = { limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      return (await client.listSiteAssets(registryId(siteId!), params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && Boolean(siteId)),
    retry: retryRegistryQuery,
  });
}

export function useRegistryAssetDetail(assetId: string | null, enabled = true) {
  return useQuery({
    queryKey: ['registry', 'asset', assetId],
    queryFn: async ({ signal }) => (await client.getAsset(registryId(assetId!), { signal })).data,
    enabled: registryQueryEnabled(enabled && Boolean(assetId)),
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

export function useRegistryDeviceDetail(deviceId: string | null, enabled = true) {
  return useQuery({
    queryKey: ['registry', 'device', deviceId],
    queryFn: async ({ signal }) => (await client.getDevice(registryId(deviceId!), { signal })).data,
    enabled: registryQueryEnabled(enabled && Boolean(deviceId)),
    retry: retryRegistryQuery,
  });
}

export function useRegistryDeviceBindings(siteId: string | null, enabled = true) {
  return useInfiniteQuery({
    queryKey: ['registry', 'sites', siteId, 'device-bindings'],
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistryListParams = { limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      return (await client.listSiteDeviceBindings(registryId(siteId!), params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && Boolean(siteId)),
    retry: retryRegistryQuery,
  });
}

async function collectCollection<T>(
  fetchPage: (params: RegistryListParams, signal: AbortSignal) => Promise<{ data: { items: T[]; hasMore: boolean; nextCursor: string | null } }>,
  signal: AbortSignal,
): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < MAX_NAVIGATION_PAGES; page += 1) {
    const result = await fetchPage({ limit: DEFAULT_PAGE_SIZE, cursor }, signal);
    items.push(...result.data.items);
    if (!result.data.hasMore || !result.data.nextCursor) return items;
    cursor = result.data.nextCursor;
  }
  throw new Error('读取登记列表超过最大分页上限');
}

export function useAuthorizedRegistrySites(enabled = true) {
  return useQuery({
    queryKey: ['registry', 'authorized-sites'],
    queryFn: async ({ signal }) => collectCollection<Site>(
      (params, requestSignal) => client.listSites(params, { signal: requestSignal }),
      signal,
    ),
    enabled: registryQueryEnabled(enabled),
    retry: retryRegistryQuery,
  });
}

export type RegistrySiteCollection = SiteCollection;
export type RegistrySiteAssetModel = SiteAssetModel;
export type RegistryAssetCollection = AssetCollection;
export type RegistryDeviceCollection = DeviceCollection;
export type RegistryDeviceBindingCollection = DeviceBindingCollection;
export type RegistryTemplateCollection = TemplateCollection;
export type RegistryTemplateAssignmentCollection = TemplateAssignmentCollection;
export type RegistryAsset = Asset;
export type RegistryDevice = Device;
export type RegistryDeviceBinding = DeviceBinding;
export type RegistryTemplate = Template;
export type RegistryTemplateRevision = TemplateRevision;
export type RegistryTemplateAssignment = TemplateAssignment;
export type RegistrySearchFilterParams = RegistrySearchParams;

export const registryTemplateKeys = {
  all: () => ['registry', 'templates'] as const,
  list: (templateKind?: TemplateKind) => ['registry', 'templates', templateKind ?? 'ALL'] as const,
  revision: (revisionId: string | null) => ['registry', 'template-revision', revisionId] as const,
  siteAssignments: (siteId: string | null, targetType?: TemplateKind, targetId?: string | null) =>
    ['registry', 'sites', siteId, 'template-assignments', targetType ?? 'ALL', targetId ?? 'ALL'] as const,
};

export const registryResourceFilterKeys = {
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

export async function getRegistrySpaceChildren(siteId: string, parentSpaceId: string | null, signal: AbortSignal): Promise<Space[]> {
  return collectCollection<Space>(
    (params, requestSignal) => client.listSiteSpaceChildren(registryId(siteId), parentSpaceId, params, { signal: requestSignal }),
    signal,
  );
}

export async function getRegistryDevicePoints(deviceId: string, signal: AbortSignal): Promise<TelemetryPoint[]> {
  return collectCollection<TelemetryPoint>(
    (params, requestSignal) => client.listDevicePoints(registryId(deviceId), params, { signal: requestSignal }),
    signal,
  );
}

export function useRegistrySpaceChildren(siteId: string | null, parentSpaceId: string | null, enabled = true) {
  return useInfiniteQuery({
    queryKey: ['registry', 'sites', siteId, 'spaces', parentSpaceId ?? 'root'],
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistryListParams = { limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      return (await client.listSiteSpaceChildren(registryId(siteId!), parentSpaceId, params, { signal })).data;
    },
    initialPageParam: INITIAL_CURSOR,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && Boolean(siteId)),
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
