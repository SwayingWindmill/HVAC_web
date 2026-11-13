import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import ts from 'typescript';

function loadTsModule(relativePath) {
  const fullPath = path.resolve(relativePath);
  const source = fs.readFileSync(fullPath, 'utf8');
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
      esModuleInterop: true,
    },
    fileName: fullPath,
  }).outputText;

  const module = { exports: {} };
  vm.runInNewContext(compiled, {
    module,
    exports: module.exports,
    require: createRequire(import.meta.url),
    URL,
    URLSearchParams,
    fetch: () => Promise.reject(new Error('network is not available in contract tests')),
  }, { filename: fullPath });
  return module.exports;
}

const {
  builtInRoleKeySchema,
  capabilitySchema,
  currentPrincipalResponseSchema,
} = loadTsModule('apps/hvac-web/src/api/generated/platformGateway.gen.ts');
const { CAPABILITY_CATALOG, capabilityGroups } = loadTsModule('apps/hvac-web/src/lib/capability-labels.ts');
const { ROLE_LABELS, ROLE_SUMMARIES } = loadTsModule('apps/hvac-web/src/lib/role-labels.ts');

function principalResponse(overrides = {}) {
  return {
    principalId: '01900000-0000-7000-8000-000000000101',
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
      tenantId: '01900000-0000-7000-8000-000000000001',
      audience: 'iam-service',
      policyRevision: 'gateway-delegation:4',
      delegationExpiresAt: '2026-07-28T00:00:00Z',
    },
    authorization: {
      capabilitySetVersion: 14,
      policyRevision: 'registry-read:7',
      capabilities: ['site.list', 'site.read', 'work-order.list', 'work-order.read'],
    },
    session: {
      id: 'session-1',
      expiresAt: '2026-07-28T01:00:00Z',
      csrfToken: '[REDACTED_SECRET]',
      idleTimeoutMs: 30 * 60 * 1000,
      revocationObjectiveMs: 5000,
      lastAuditMessageId: 'audit-1',
    },
    ...overrides,
  };
}

test('generated browser contract accepts IAM-authored effective capabilities', () => {
  const parsed = currentPrincipalResponseSchema.parse(principalResponse());
  assert.equal(parsed.authorization.policyRevision, 'registry-read:7');
  assert.deepEqual(Array.from(parsed.authorization.capabilities), ['site.list', 'site.read', 'work-order.list', 'work-order.read']);
  assert.deepEqual(Array.from(parsed.principal.roles), ['descriptive-role-only']);
});

test('generated browser contract rejects missing, duplicate, and unsupported capabilities', () => {
  const missing = principalResponse();
  delete missing.authorization;
  assert.equal(currentPrincipalResponseSchema.safeParse(missing).success, false);

  assert.equal(currentPrincipalResponseSchema.safeParse(principalResponse({
    authorization: {
      capabilitySetVersion: 14,
      policyRevision: 'registry-read:7',
      capabilities: ['site.read', 'site.read'],
    },
  })).success, false);

  assert.equal(currentPrincipalResponseSchema.safeParse(principalResponse({
    authorization: {
      capabilitySetVersion: 14,
      policyRevision: 'registry-read:7',
      capabilities: ['role.admin'],
    },
  })).success, false);
});

test('operator capability and built-in role catalogs match generated IAM schemas without unnamed or stale keys', () => {
  assert.deepEqual(Object.keys(CAPABILITY_CATALOG).sort(), [...capabilitySchema.options].sort());
  assert.equal(capabilityGroups(capabilitySchema.options).unnamed, 0);
  assert.equal(capabilityGroups(['future.unknown.capability']).unnamed, 1);

  assert.deepEqual(Object.keys(ROLE_LABELS).sort(), [...builtInRoleKeySchema.options].sort());
  assert.deepEqual(Array.from(ROLE_SUMMARIES, (item) => item.key).sort(), [...builtInRoleKeySchema.options].sort());
});
