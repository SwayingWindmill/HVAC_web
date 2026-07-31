import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import ts from 'typescript';

const generatedPath = path.resolve('apps/hvac-web/src/api/generated/platformGateway.gen.ts');
const source = fs.readFileSync(generatedPath, 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2020,
    esModuleInterop: true,
  },
  fileName: generatedPath,
}).outputText;

const module = { exports: {} };
vm.runInNewContext(compiled, {
  module,
  exports: module.exports,
  require: createRequire(import.meta.url),
  URL,
  URLSearchParams,
  fetch: () => Promise.reject(new Error('network is not available in contract tests')),
}, { filename: generatedPath });

const { currentPrincipalResponseSchema } = module.exports;

function principalResponse(overrides = {}) {
  return {
    principal: {
      subject: 'fixture-user',
      issuer: 'https://issuer.example.test',
      displayName: 'Fixture User',
      email: 'fixture@example.test',
      roles: ['descriptive-role-only'],
    },
    context: {
      initiatingPrincipal: {
        subject: 'fixture-user',
        issuer: 'https://issuer.example.test',
        displayName: 'Fixture User',
        email: 'fixture@example.test',
        roles: ['descriptive-role-only'],
      },
      executingServicePrincipal: {
        service: 'platform-gateway',
        spiffeId: 'spiffe://hvac.local/platform-gateway',
      },
      actingOrganizationId: '01900000-0000-7000-8000-000000000001',
      audience: 'iam-service',
      policyRevision: 'gateway-delegation:4',
      delegationExpiresAt: '2026-07-28T00:00:00Z',
    },
    authorization: {
      capabilitySetVersion: 2,
      policyRevision: 'registry-read:7',
      capabilities: ['organization.list', 'site.read'],
    },
    session: {
      id: 'session-1',
      expiresAt: '2026-07-28T01:00:00Z',
      csrfToken: '[REDACTED_SECRET]',
      revocationObjectiveMs: 5000,
      lastAuditMessageId: 'audit-1',
    },
    ...overrides,
  };
}

test('generated browser contract accepts IAM-authored effective capabilities', () => {
  const parsed = currentPrincipalResponseSchema.parse(principalResponse());
  assert.equal(parsed.authorization.policyRevision, 'registry-read:7');
  assert.deepEqual(Array.from(parsed.authorization.capabilities), ['organization.list', 'site.read']);
  assert.deepEqual(Array.from(parsed.principal.roles), ['descriptive-role-only']);
});

test('generated browser contract rejects missing, duplicate, and unsupported capabilities', () => {
  const missing = principalResponse();
  delete missing.authorization;
  assert.equal(currentPrincipalResponseSchema.safeParse(missing).success, false);

  assert.equal(currentPrincipalResponseSchema.safeParse(principalResponse({
    authorization: {
      capabilitySetVersion: 2,
      policyRevision: 'registry-read:7',
      capabilities: ['site.read', 'site.read'],
    },
  })).success, false);

  assert.equal(currentPrincipalResponseSchema.safeParse(principalResponse({
    authorization: {
      capabilitySetVersion: 2,
      policyRevision: 'registry-read:7',
      capabilities: ['role.admin'],
    },
  })).success, false);
});
