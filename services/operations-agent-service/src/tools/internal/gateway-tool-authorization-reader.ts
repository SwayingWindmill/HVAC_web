import {
  OwnerReadError,
  type OwnerReadInput,
  type ParallelReadRequest,
  type ToolAuthorizationReader,
} from '../../application/index.js';
import {
  fetchOwnerJson,
  hasExactKeys,
  isNonEmptyString,
  isRecord,
  normalizeOwnerReaderHttpConfig,
  type OwnerReaderHttpConfig,
} from './owner-http.js';

export type GatewayToolAuthorizationReaderConfig = OwnerReaderHttpConfig;

const path = '/internal/v1/operations/tool-authorization';

export const createGatewayToolAuthorizationReader = (
  input: GatewayToolAuthorizationReaderConfig,
): ToolAuthorizationReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  return Object.freeze({
    async authorize({
      request,
      context,
    }: OwnerReadInput<ParallelReadRequest>) {
      const serviceDelegation = context.authorization.delegationGrant;
      if (context.authorization.decision !== 'ALLOW'
        || serviceDelegation === undefined
        || serviceDelegation.trim().length === 0
        || context.investigationId.trim().length === 0
        || context.runId.trim().length === 0) {
        throw new OwnerReadError(
          'OWNER_REQUEST_INVALID',
          'The Operations service delegation context is incomplete.',
        );
      }
      const result = await fetchOwnerJson(config, {
        path,
        method: 'POST',
        headers: {
          Accept: 'application/json, application/problem+json',
          'Content-Type': 'application/json',
          'X-Delegation-Grant': serviceDelegation,
          'X-Request-ID': request.requestId,
          ...(context.authorization.traceparent === undefined
            ? {}
            : { traceparent: context.authorization.traceparent }),
          ...(context.authorization.tracestate === undefined
            ? {}
            : { tracestate: context.authorization.tracestate }),
        },
        body: JSON.stringify({
          investigationId: context.investigationId,
          runId: context.runId,
          request,
        }),
      });
      if (!isRecord(result)
        || !hasExactKeys(result, ['delegationGrant', 'policyRevision'])
        || !isNonEmptyString(result.delegationGrant)
        || !isNonEmptyString(result.policyRevision)
        || result.delegationGrant.length > 16_384
        || result.policyRevision.length > 256) {
        throw new OwnerReadError(
          'OWNER_RESPONSE_INVALID',
          'Platform Gateway returned an invalid Tool authorization grant.',
        );
      }
      return {
        delegationGrant: result.delegationGrant,
        policyRevision: result.policyRevision,
      };
    },
  });
};
