import { createHash } from 'node:crypto';

import {
  AgentToolError,
  type AgentRunContext,
  type AgentRunSite,
  type AgentToolErrorCode,
} from '../../agent/index.js';
import {
  OwnerReadError,
  type AuthorizationDecision,
  type OwnerReadContext,
  type OwnerReadErrorCode,
  type RegistryReadRequest,
  type RegistryReader,
  type ToolAuthorizableReadRequest,
  type ToolAuthorizationReader,
} from '../../application/index.js';
import type {
  RegistryAssetDto,
  RegistryAssetModelDto,
  RegistryOwnerPayload,
  RegistrySiteDto,
} from './registry-owner-reader.js';

export interface EquipmentPointSummary {
  readonly key: string;
  readonly name: string;
  readonly unit: string | null;
  readonly kind: string;
}

export interface EquipmentSummary {
  readonly deviceId: string;
  readonly name: string;
  readonly machine: string | null;
  readonly serves?: readonly string[];
  readonly points: readonly EquipmentPointSummary[];
}

export interface RunSiteSnapshotSite {
  readonly site: RegistrySiteDto;
  readonly agentSite: AgentRunSite;
}

export interface RunSiteSnapshotAssetModel {
  readonly model: RegistryAssetModelDto;
  readonly assetsById: ReadonlyMap<string, RegistryAssetDto>;
  readonly equipment: readonly EquipmentSummary[];
  readonly deviceNamesById: ReadonlyMap<string, string>;
}

export interface RunSiteSnapshot {
  getSite(context: AgentRunContext): Promise<RunSiteSnapshotSite>;
  getAssetModel(context: AgentRunContext): Promise<RunSiteSnapshotAssetModel>;
}

export interface CreateRunSiteSnapshotInput {
  readonly authorization: AuthorizationDecision;
  readonly toolAuthorizationReader: ToolAuthorizationReader;
  readonly registryReader: RegistryReader;
}

const POINT_KIND_WORDS: Readonly<Record<string, string>> = Object.freeze({
  TELEMETRY: '读数',
  COUNTER: '累计量',
  STATE: '状态',
});

// Units as the device page writes them (formatTelemetryUnit in the Web): Registry stores UCUM codes.
const UNIT_WORDS: Readonly<Record<string, string>> = Object.freeze({
  Cel: '°C',
  degC: '°C',
  'm3/h': 'm³/h',
});

export const pointKind = (pointType: string): string => POINT_KIND_WORDS[pointType] ?? '读数';

export const unitWords = <T extends string | null | undefined>(unit: T): T | string => (
  unit ? UNIT_WORDS[unit] ?? unit : unit
);

// A refused argument or a result too large to hand over is the Tool's own wording, so the model is
// told what to change; every other failure reaches it as a bare code.
export const fail = (code: AgentToolErrorCode, message: string = code): never => {
  throw new AgentToolError(
    code,
    message,
    code === 'TOOL_ARGUMENTS_INVALID' || code === 'TOOL_RESULT_TOO_LARGE' ? message : undefined,
  );
};

const ownerErrorCode = (code: OwnerReadErrorCode): AgentToolErrorCode => {
  switch (code) {
    case 'OWNER_REQUEST_INVALID':
      return 'TOOL_OWNER_REQUEST_REJECTED';
    case 'OWNER_RESOURCE_NOT_FOUND':
      return 'TOOL_OWNER_RESOURCE_NOT_FOUND';
    case 'OWNER_READ_TIMEOUT':
      return 'TOOL_OWNER_TIMEOUT';
    case 'OWNER_READ_UNAVAILABLE':
      return 'TOOL_OWNER_UNAVAILABLE';
    case 'OWNER_RESPONSE_TOO_LARGE':
      return 'TOOL_RESULT_TOO_LARGE';
    case 'OWNER_RESPONSE_INVALID':
      return 'TOOL_OWNER_RESPONSE_INVALID';
  }
};

export const normalizeToolFailure = (error: unknown): AgentToolError => {
  if (error instanceof AgentToolError) return error;
  if (error instanceof OwnerReadError) {
    const code = ownerErrorCode(error.code);
    return new AgentToolError(code, code);
  }
  return new AgentToolError('TOOL_EXECUTION_FAILED');
};

export const ownerReadRequestId = (
  context: AgentRunContext,
  reader: string,
  ownerRequest: Omit<ToolAuthorizableReadRequest, 'requestId'>,
): string => {
  const digest = createHash('sha256')
    .update(JSON.stringify([context.runId, reader, ownerRequest]))
    .digest('hex');
  return `agent-tool:${digest}`;
};

const baseOwnerContext = (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
): OwnerReadContext => {
  if (authorization.decision !== 'ALLOW'
    || authorization.delegationGrant === undefined
    || authorization.delegationGrant.trim().length === 0) {
    fail('TOOL_UNAUTHORIZED', 'The server authorization context is incomplete.');
  }
  return {
    sessionId: context.sessionId,
    runId: context.runId,
    scope: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      assetId: null,
      deviceId: null,
    },
    authorization,
    correlationId: context.correlationId,
    ...(context.signal === undefined ? {} : { signal: context.signal }),
  };
};

export const authorizeOwnerRead = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  reader: ToolAuthorizationReader,
  request: ToolAuthorizableReadRequest,
): Promise<OwnerReadContext> => {
  const base = baseOwnerContext(context, authorization);
  const scoped = request.tool === 'telemetry.current.getDeviceObservationSnapshot'
    || request.tool === 'telemetry.history.aggregateDeviceHistory'
    ? {
      ...base,
      scope: { ...base.scope, deviceId: request.input.deviceId },
    }
    : base;
  const grant = await reader.authorize({ request, context: scoped });
  if (grant.delegationGrant.trim().length === 0) {
    fail('TOOL_UNAUTHORIZED', 'Owner authorization returned an empty grant.');
  }
  return {
    ...scoped,
    authorization: {
      ...authorization,
      delegationGrant: grant.delegationGrant,
      toolDelegationGrants: {
        ...authorization.toolDelegationGrants,
        [request.tool]: grant.delegationGrant,
      },
      ...(grant.policyRevision === undefined ? {} : { policyRevision: grant.policyRevision }),
    },
  };
};

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const isSitePayload = (
  payload: unknown,
): payload is Extract<RegistryOwnerPayload, { kind: 'SITE' }> => (
  isRecord(payload)
  && payload.kind === 'SITE'
  && isRecord(payload.site)
  && typeof payload.site.displayName === 'string'
  && typeof payload.site.timezone === 'string'
);

const isAssetModelPayload = (
  payload: unknown,
): payload is Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }> => (
  isRecord(payload)
  && payload.kind === 'SITE_ASSET_MODEL'
  && isRecord(payload.model)
  && Array.isArray(payload.model.assets)
  && Array.isArray(payload.model.devices)
  && Array.isArray(payload.model.telemetryPoints)
  && Array.isArray(payload.model.relationships)
);

/** Each device with the machine it belongs to, what that asset serves, and its active points. */
export const describeEquipment = (
  model: RegistryAssetModelDto,
  knownAssets?: ReadonlyMap<string, RegistryAssetDto>,
): readonly EquipmentSummary[] => {
  const assetsById = knownAssets ?? new Map(model.assets.map((asset) => [asset.id, asset]));
  const servesByAssetId = new Map<string, string[]>();
  for (const relationship of model.relationships) {
    if (relationship.status !== 'ACTIVE'
      || relationship.fromType !== 'ASSET'
      || relationship.toType !== 'ASSET'
      || relationship.role !== 'SERVES') {
      continue;
    }
    const served = assetsById.get(relationship.toId);
    if (served === undefined) continue;
    const targets = servesByAssetId.get(relationship.fromId);
    if (targets === undefined) {
      servesByAssetId.set(relationship.fromId, [served.displayName]);
    } else if (!targets.includes(served.displayName)) {
      targets.push(served.displayName);
    }
  }

  return model.devices.map((device) => {
    const link = model.relationships.find((relationship) => relationship.status === 'ACTIVE'
      && relationship.fromType === 'DEVICE' && relationship.fromId === device.id && relationship.toType === 'ASSET');
    const asset = link === undefined ? undefined : assetsById.get(link.toId);
    const serves = asset === undefined ? undefined : servesByAssetId.get(asset.id);
    return {
      deviceId: device.id,
      name: device.displayName,
      machine: asset === undefined ? null : asset.displayName,
      ...(serves === undefined || serves.length === 0 ? {} : { serves }),
      points: model.telemetryPoints
        .filter((point) => point.reportingDeviceId === device.id && point.status === 'ACTIVE')
        .map((point) => ({
          key: point.pointCode,
          name: point.displayName,
          unit: unitWords(point.unit),
          kind: pointKind(point.pointType),
        })),
    };
  });
};

const singleflight = <T>(
  cache: Map<string, Promise<T>>,
  runId: string,
  loader: () => Promise<T>,
): Promise<T> => {
  const existing = cache.get(runId);
  if (existing !== undefined) return existing;
  const pending = loader().catch((error: unknown) => {
    if (cache.get(runId) === pending) {
      cache.delete(runId);
    }
    throw error;
  });
  cache.set(runId, pending);
  return pending;
};

/** Per-Run Registry Site and Asset Model snapshot with lazy singleflight deduplication keyed by runId. */
export const createRunSiteSnapshot = ({
  authorization,
  toolAuthorizationReader,
  registryReader,
}: CreateRunSiteSnapshotInput): RunSiteSnapshot => {
  const siteReads = new Map<string, Promise<RunSiteSnapshotSite>>();
  const assetModelReads = new Map<string, Promise<RunSiteSnapshotAssetModel>>();

  const loadSite = async (context: AgentRunContext): Promise<RunSiteSnapshotSite> => {
    const draft = {
      tool: 'registry.getSite' as const,
      input: { siteId: context.siteId },
    };
    const request: RegistryReadRequest = {
      ...draft,
      requestId: ownerReadRequestId(context, 'site', draft),
    };
    const ownerContext = await authorizeOwnerRead(context, authorization, toolAuthorizationReader, request);
    const result = await registryReader.read({ request, context: ownerContext });
    if (!isSitePayload(result.payload)) return fail('TOOL_OWNER_RESPONSE_INVALID');
    const { site } = result.payload;
    return Object.freeze({
      site,
      agentSite: Object.freeze({ name: site.displayName, timezone: site.timezone }),
    });
  };

  const loadAssetModel = async (context: AgentRunContext): Promise<RunSiteSnapshotAssetModel> => {
    const draft = {
      tool: 'registry.getSiteAssetModel' as const,
      input: { siteId: context.siteId },
    };
    const request: RegistryReadRequest = {
      ...draft,
      requestId: ownerReadRequestId(context, 'equipment_list', draft),
    };
    const ownerContext = await authorizeOwnerRead(context, authorization, toolAuthorizationReader, request);
    const result = await registryReader.read({ request, context: ownerContext });
    if (!isAssetModelPayload(result.payload)) return fail('TOOL_OWNER_RESPONSE_INVALID');
    const { model } = result.payload;
    const assetsById = new Map(model.assets.map((asset) => [asset.id, asset]));
    const equipment = describeEquipment(model, assetsById);
    const deviceNamesById = new Map(equipment.map((device) => [device.deviceId, device.name]));
    return Object.freeze({
      model,
      assetsById,
      equipment,
      deviceNamesById,
    });
  };

  return Object.freeze({
    getSite(context: AgentRunContext) {
      return singleflight(siteReads, context.runId, () => loadSite(context));
    },
    getAssetModel(context: AgentRunContext) {
      return singleflight(assetModelReads, context.runId, () => loadAssetModel(context));
    },
  });
};
