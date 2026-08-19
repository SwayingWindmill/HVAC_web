import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { ZodError } from 'zod';
import { API_MODE } from './config';
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
  type ReleaseTemplateRequest,
  type RetireRequest,
  type Site,
  type SiteAssetModel,
  type SiteCollection,
  type SiteMutation,
  type Space,
  type SpaceMutation,
  type TelemetryPoint,
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

const registryQueryEnabled = (enabled: boolean) => API_MODE === 'real' && enabled;
const registryId = (value: string) => uuidV7Schema.parse(value);

const retryRegistryQuery = (failureCount: number, error: Error) => {
  if (failureCount >= 1 || error instanceof ZodError) return false;
  if (!(error instanceof PlatformApiError)) return true;
  return error.problem.retryable;
};

const nextCursor = (collection: { hasMore: boolean; nextCursor: string | null }) =>
  collection.hasMore ? collection.nextCursor ?? undefined : undefined;

export const flattenRegistryPages = <T>(data: { pages: Array<{ items: T[] }> } | undefined): T[] =>
  data?.pages.flatMap((page) => page.items) ?? [];

export function presentRegistryError(error: unknown): RegistryErrorPresentation {
  if (error instanceof ZodError) {
    return {
      kind: 'invalid-request',
      title: '资源链接无效',
      description: '资源标识必须是平台生成的 UUIDv7。请从授权 Registry 列表重新打开该资源。',
      retryable: false,
    };
  }
  if (error instanceof PlatformApiError) {
    const { code, detail, retryable, traceId } = error.problem;
    switch (code) {
      case 'RESOURCE_NOT_FOUND':
        return { kind: 'not-found', title: '资源不可见或不存在', description: detail, retryable, traceId };
      case 'CURSOR_INVALID':
        return { kind: 'invalid-cursor', title: '分页游标已失效', description: detail, retryable, traceId };
      case 'REGISTRY_UNAVAILABLE':
        return { kind: 'unavailable', title: 'Registry 暂不可用', description: detail, retryable, traceId };
      case 'REGISTRY_TIMEOUT':
        return { kind: 'timeout', title: 'Registry 请求超时', description: detail, retryable, traceId };
      case 'MAPPING_INVALID':
      case 'MAPPING_QUARANTINED':
        return { kind: 'mapping', title: 'Registry 映射尚未就绪', description: detail, retryable, traceId };
      case 'REGISTRY_REVISION_CONFLICT':
        return { kind: 'revision-conflict', title: '资源已被其他操作更新', description: '当前编辑基于旧 Revision。请重新加载权威数据后再决定是否重做本次修改。', retryable: false, traceId };
      case 'REGISTRY_BINDING_CONFLICT':
        return { kind: 'binding-conflict', title: '绑定变更冲突', description: detail, retryable: false, traceId };
      case 'REGISTRY_IMPORT_PLAN_INVALID':
        return { kind: 'import-conflict', title: '导入计划已失效', description: '必须重新执行 dry-run；旧计划不会被强制提交。', retryable: false, traceId };
      case 'REGISTRY_IDEMPOTENCY_CONFLICT':
      case 'TEMPLATE_REVISION_IMMUTABLE':
        return { kind: 'mutation-conflict', title: 'Registry 写入冲突', description: detail, retryable: false, traceId };
      default:
        return { kind: 'unknown', title: 'Registry 请求失败', description: detail, retryable, traceId };
    }
  }
  return {
    kind: 'unavailable',
    title: 'Registry 连接失败',
    description: '无法通过 Platform Gateway 读取权威 Registry 数据。真实模式不会回退到本地演示数据。',
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
    initialPageParam: undefined as string | undefined,
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

export function useRegistryAssetModel(siteId: string | null, enabled = true) {
  return useQuery({
    queryKey: ['registry', 'sites', siteId, 'asset-model'],
    queryFn: async ({ signal }) => (await client.getSiteAssetModel(registryId(siteId!), { signal })).data,
    enabled: registryQueryEnabled(enabled && Boolean(siteId)),
    retry: retryRegistryQuery,
  });
}

export function useRegistryAssets(siteId: string | null, enabled = true) {
  return useInfiniteQuery({
    queryKey: ['registry', 'sites', siteId, 'assets'],
    queryFn: async ({ pageParam, signal }) => {
      const params: RegistryListParams = { limit: DEFAULT_PAGE_SIZE };
      if (typeof pageParam === 'string') params.cursor = pageParam;
      return (await client.listSiteAssets(registryId(siteId!), params, { signal })).data;
    },
    initialPageParam: undefined as string | undefined,
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
    initialPageParam: undefined as string | undefined,
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
    initialPageParam: undefined as string | undefined,
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
  throw new Error('Registry navigation exceeded its bounded page budget');
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
export type RegistryAsset = Asset;
export type RegistryDevice = Device;
export type RegistryDeviceBinding = DeviceBinding;

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
    initialPageParam: undefined as string | undefined,
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
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
    enabled: registryQueryEnabled(enabled && Boolean(deviceId)),
    retry: retryRegistryQuery,
  });
}

export const registryAdminApi = {
  createSite: async (body: SiteMutation) => (await client.createSite(body)).data,
  updateSite: async (siteId: string, body: SiteMutation) => (await client.updateSite(registryId(siteId), body)).data,
  createSpace: async (siteId: string, body: SpaceMutation) => (await client.createSiteSpace(registryId(siteId), body)).data,
  updateSpace: async (siteId: string, spaceId: string, body: SpaceMutation) => (await client.updateSiteSpace(registryId(siteId), registryId(spaceId), body)).data,
  createAsset: async (siteId: string, body: AssetMutation) => (await client.createSiteAsset(registryId(siteId), body)).data,
  updateAsset: async (siteId: string, assetId: string, body: AssetMutation) => (await client.updateSiteAsset(registryId(siteId), registryId(assetId), body)).data,
  createDevice: async (siteId: string, body: DeviceMutation) => (await client.createSiteDevice(registryId(siteId), body)).data,
  updateDevice: async (siteId: string, deviceId: string, body: DeviceMutation) => (await client.updateSiteDevice(registryId(siteId), registryId(deviceId), body)).data,
  createPoint: async (siteId: string, body: PointMutation) => (await client.createSitePoint(registryId(siteId), body)).data,
  updatePoint: async (siteId: string, pointId: string, body: PointMutation) => (await client.updateSitePoint(registryId(siteId), registryId(pointId), body)).data,
  rebind: async (siteId: string, body: RebindRequest) => (await client.rebindRegistryResource(registryId(siteId), body)).data,
  releaseTemplate: async (body: ReleaseTemplateRequest) => (await client.releaseRegistryTemplateRevision(body)).data,
  assignTemplate: async (body: AssignTemplateRequest) => (await client.assignRegistryTemplateRevision(body)).data,
  planImport: async (siteId: string, body: ImportPlanRequest) => (await client.planRegistryImport(registryId(siteId), body)).data,
  commitImport: async (siteId: string, body: ImportCommitRequest) => (await client.commitRegistryImport(registryId(siteId), body)).data,
  retire: async (siteId: string, body: RetireRequest) => (await client.beginRegistryRetirement(registryId(siteId), body)).data,
};
