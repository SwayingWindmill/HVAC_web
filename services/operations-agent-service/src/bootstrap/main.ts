import { createPublicKey, verify, type KeyObject } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createServer as createHttpServer } from 'node:http';
import { createServer as createHttpsServer, type ServerOptions } from 'node:https';
import type { IncomingMessage, ServerResponse } from 'node:http';
import type { TLSSocket } from 'node:tls';
import { Readable } from 'node:stream';
import { Agent, fetch as undiciFetch } from 'undici';

import type { AgentRunBudget } from '../agent/index.js';
import type { AuthorizationDecision } from '../application/index.js';
import {
  createProductionAgentSessionRuntime,
  type AgentSessionHttpAuthorizationInput,
  type AgentSessionHttpAuthorizer,
} from './index.js';

// Process entry of the Operations Agent service. The Platform Gateway is its only caller:
// it reaches the service over mTLS and forwards each Agent Session request with a signed
// delegation grant for the operator. The service reads platform data through the same
// owners every other workload uses, presenting its own workload certificate.

const required = (name: string): string => {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required.`);
  return value;
};

const AGENT_SPIFFE_ID = process.env.AGENT_SPIFFE_ID ?? 'spiffe://hvac.local/operations-agent-service';
const GATEWAY_SPIFFE_ID = process.env.AGENT_GATEWAY_SPIFFE_ID ?? 'spiffe://hvac.local/platform-gateway';
const AUDIENCE = 'operations-agent-service';
const ACTION = 'operations:investigate';

// What one question may cost. Tools read at most a month of history.
const BUDGET: AgentRunBudget = Object.freeze({
  maxModelCalls: 8,
  maxToolCalls: 16,
  maxWallClockMs: 120_000,
  maxParallelToolCalls: 4,
  maxQueryRangeMs: 31 * 86_400_000,
  maxToolResultRecords: 2_000,
  maxToolResultBytes: 128_000,
  maxInputTokens: 64_000,
  maxOutputTokens: 8_000,
});

// Capabilities that make the read tools available to the model. The Gateway authorizes
// every tool call against IAM before an owner answers, so a tool the operator may not use
// fails at that point rather than here.
const READ_TOOL_CAPABILITIES = Object.freeze([
  'site.read',
  'asset.list',
  'telemetry.snapshot.read',
  'analytics.energy-series.read',
]);

interface DelegationClaims {
  readonly version: number;
  readonly issuer: string;
  readonly subject: string;
  readonly subjectIssuer: string;
  readonly executingService: string;
  readonly audience: string;
  readonly tenantId: string;
  readonly actions: readonly string[];
  readonly scopes: readonly string[];
  readonly policyRevision: string;
  readonly sessionId: string;
  readonly issuedAt: number;
  readonly expiresAt: number;
  readonly tokenId: string;
}

/** Verifies a grant signed by the Gateway (libs/identitycontext SignDelegation). */
const verifyDelegation = (publicKey: KeyObject, token: string): DelegationClaims | null => {
  const [payload, signature, extra] = token.split('.');
  if (!payload || !signature || extra !== undefined) return null;
  const body = Buffer.from(payload, 'base64url');
  if (!verify('sha256', body, publicKey, Buffer.from(signature, 'base64url'))) return null;
  try {
    return JSON.parse(body.toString('utf8')) as DelegationClaims;
  } catch {
    return null;
  }
};

const createGrantAuthorizer = (publicKey: KeyObject): AgentSessionHttpAuthorizer => ({
  async authorize(input: AgentSessionHttpAuthorizationInput): Promise<AuthorizationDecision> {
    const deny = (reason: string): AuthorizationDecision => ({ decision: 'DENY', decisionId: '', reason });
    const claims = verifyDelegation(publicKey, input.gatewayDelegationGrant);
    if (!claims) return deny('grant-invalid');
    const now = Math.floor(Date.now() / 1000);
    if (claims.version !== 1
      || claims.issuer !== GATEWAY_SPIFFE_ID
      || claims.executingService !== GATEWAY_SPIFFE_ID
      || claims.audience !== AUDIENCE
      || claims.tenantId !== input.tenantId
      || claims.actions.length !== 1 || claims.actions[0] !== ACTION
      || !claims.scopes.includes(`site:${input.siteId}`)
      || claims.issuedAt > now + 5 || claims.expiresAt <= now || claims.expiresAt - claims.issuedAt > 60
      || !claims.subject || !claims.subjectIssuer || !claims.sessionId || !claims.tokenId) {
      return deny('grant-rejected');
    }
    return {
      decision: 'ALLOW',
      decisionId: claims.tokenId,
      delegationGrant: input.gatewayDelegationGrant,
      policyRevision: claims.policyRevision,
      capabilities: READ_TOOL_CAPABILITIES,
      auditActor: {
        actorType: 'OPERATOR',
        actorId: claims.subject,
        actorIssuer: claims.subjectIssuer,
        executingService: 'operations-agent-service',
        executingSpiffeId: AGENT_SPIFFE_ID,
      },
    };
  },
});

const peerSpiffeId = (request: IncomingMessage): string | null => {
  const certificate = (request.socket as TLSSocket).getPeerX509Certificate?.();
  const uris = certificate?.subjectAltName?.split(', ').filter((entry) => entry.startsWith('URI:')) ?? [];
  return uris.length === 1 ? uris[0]!.slice('URI:'.length) : null;
};

const toRequest = (request: IncomingMessage): Request => {
  const headers = new Headers();
  for (const [name, value] of Object.entries(request.headers)) {
    if (typeof value === 'string') headers.set(name, value);
  }
  const hasBody = request.method !== 'GET' && request.method !== 'HEAD';
  return new Request(`https://${AUDIENCE}${request.url ?? '/'}`, {
    method: request.method ?? 'GET',
    headers,
    ...(hasBody ? { body: Readable.toWeb(request) as ReadableStream, duplex: 'half' } : {}),
  } as RequestInit);
};

const writeResponse = async (response: Response, target: ServerResponse): Promise<void> => {
  target.writeHead(response.status, Object.fromEntries(response.headers));
  if (response.body === null) {
    target.end();
    return;
  }
  const body = Readable.fromWeb(response.body as never);
  target.on('close', () => body.destroy());
  body.pipe(target);
};

const main = async () => {
  const workload = {
    cert: readFileSync(required('AGENT_TLS_CERT')),
    key: readFileSync(required('AGENT_TLS_KEY')),
    ca: readFileSync(required('AGENT_TLS_CA')),
  };
  const ownerAgent = new Agent({ connect: { ...workload, minVersion: 'TLSv1.3' } });
  const ownerFetch = ((input: string | URL | Request, init?: RequestInit) =>
    undiciFetch(input as never, { ...(init as object), dispatcher: ownerAgent } as never)) as unknown as typeof fetch;
  const owner = (name: string) => ({ baseUrl: required(name), fetchImplementation: ownerFetch });

  const runtime = await createProductionAgentSessionRuntime({
    environment: process.env,
    persistence: {
      operationsConnectionString: required('OPERATIONS_DATABASE_URL'),
      checkpointsConnectionString: required('CHECKPOINTS_DATABASE_URL'),
      checkpointRetentionMs: 7 * 86_400_000,
    },
    owners: {
      registry: owner('REGISTRY_URL'),
      deviceTelemetry: owner('TELEMETRY_RUNTIME_URL'),
      energyAnalytics: owner('TELEMETRY_QUERY_URL'),
      gatewayToolAuthorization: owner('GATEWAY_INTERNAL_URL'),
    },
    authorizer: createGrantAuthorizer(createPublicKey(readFileSync(required('AGENT_DELEGATION_PUBLIC_KEY')))),
    budget: BUDGET,
  });

  const tlsOptions: ServerOptions = { ...workload, requestCert: true, rejectUnauthorized: true, minVersion: 'TLSv1.3' };
  const server = createHttpsServer(tlsOptions, (request, response) => {
    if (peerSpiffeId(request) !== GATEWAY_SPIFFE_ID) {
      response.writeHead(403, { 'Content-Type': 'application/problem+json' });
      response.end(JSON.stringify({ status: 403, code: 'OPERATIONS_AGENT_CALLER_FORBIDDEN' }));
      return;
    }
    runtime.handler.handle(toRequest(request))
      .then((result) => writeResponse(result, response))
      .catch(() => {
        if (!response.headersSent) response.writeHead(500);
        response.end();
      });
  });
  server.listen(Number(process.env.AGENT_LISTEN_PORT ?? 8451));

  // Readiness for the orchestrator; it carries no data.
  const diagnostics = createHttpServer((request, response) => {
    response.writeHead(request.url === '/health/ready' ? 200 : 404).end();
  });
  diagnostics.listen(Number(process.env.AGENT_DIAGNOSTICS_PORT ?? 19095));

  const shutdown = () => {
    server.close();
    diagnostics.close();
    void runtime.close().then(() => ownerAgent.close()).finally(() => process.exit(0));
  };
  process.on('SIGTERM', shutdown);
  process.on('SIGINT', shutdown);
};

await main();
