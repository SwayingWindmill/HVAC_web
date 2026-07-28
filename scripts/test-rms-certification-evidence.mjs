import assert from 'node:assert/strict';
import test from 'node:test';
import { buildRealShellCertificationEnvelope } from './rms-certification-evidence-lib.mjs';

const requiredScenarios = [
  'login',
  'one-site',
  'many-sites',
  'zero-sites',
  'invalid-site',
  'capability-denial',
  'not-integrated',
  'site-switching',
  'session-expiration',
  'logout',
  'mobile',
];

const requiredGates = [
  'contracts',
  'shell-tests',
  'go-tests',
  'typecheck',
  'real-graph',
  'real-build',
  'demo-build',
  'bundle',
  'browser',
];

function validInput() {
  return {
    repositorySha: '1'.repeat(40),
    gates: requiredGates.map((name) => ({ name, passed: true, durationMs: 1 })),
    contract: {
      generatorVersion: '6.0.0',
      generatedClientDrift: false,
      contractSha256: '2'.repeat(64),
    },
    graph: {
      passed: true,
      violations: [],
      files: ['apps/hvac-web/src/real/main.tsx'],
    },
    bundle: {
      passed: true,
      real: { passed: true, forbidden: [], missingRequired: [] },
      demo: { passed: true },
    },
    browser: {
      passed: true,
      scenarios: Object.fromEntries(requiredScenarios.map((name) => [name, { passed: true }])),
      network: {
        requestCount: 24,
        browserAuthorizationHeaderCount: 0,
        browserOrganizationAuthorityHeaderCount: 0,
        browserSiteAuthorityHeaderCount: 0,
        browserOtherAuthorityHeaderCount: 0,
      },
      storage: {
        samples: [{ label: 'logout success', localStorageEntries: 0, sessionStorageEntries: 0 }],
        persistedSensitivePayloadCount: 0,
        sensitiveCategories: {
          token: 0,
          csrf: 0,
          principal: 0,
          registry: 0,
          telemetry: 0,
          command: 0,
        },
      },
      failures: [
        { code: 'SESSION_STORE_UNAVAILABLE', traceId: '0'.repeat(32), fixtureFallback: false },
      ],
    },
    policy: {
      schemaVersion: 1,
      artifactMode: 'real',
      rollback: {
        previousRealArtifactOnly: true,
        demoFallbackAllowed: false,
        failClosedOnSessionOrCapabilityFailure: true,
      },
      commandTraffic: {
        productionTrafficChanged: false,
        formalProductionClaim: false,
        s3CertificationChanged: false,
      },
    },
  };
}

test('builds a non-production Real Shell certification envelope from passing evidence', () => {
  const envelope = buildRealShellCertificationEnvelope(validInput());
  assert.equal(envelope.schemaVersion, 1);
  assert.equal(envelope.certification, 'RMS-08_REAL_MODE_SHELL');
  assert.equal(envelope.passed, true);
  assert.equal(envelope.formalProductionClaim, false);
  assert.equal(envelope.commandTraffic.productionTrafficChanged, false);
  assert.equal(envelope.rollback.previousRealArtifactOnly, true);
  assert.equal(envelope.rollback.failClosedOnSessionOrCapabilityFailure, true);
  assert.equal(envelope.evidence.gates.passedCount, requiredGates.length);
  assert.equal(envelope.evidence.browser.scenarioCount, requiredScenarios.length);
  assert.equal(Object.isFrozen(envelope), true);
});

test('rejects a missing or failed certification gate', () => {
  const missing = validInput();
  missing.gates = missing.gates.filter((gate) => gate.name !== 'go-tests');
  assert.throws(
    () => buildRealShellCertificationEnvelope(missing),
    /gate go-tests/i,
  );

  const failed = validInput();
  failed.gates.find((gate) => gate.name === 'real-build').passed = false;
  assert.throws(
    () => buildRealShellCertificationEnvelope(failed),
    /gate real-build/i,
  );
});

test('rejects missing browser coverage and browser-supplied authority', () => {
  const missingScenario = validInput();
  delete missingScenario.browser.scenarios.mobile;
  assert.throws(
    () => buildRealShellCertificationEnvelope(missingScenario),
    /browser scenario mobile/i,
  );

  const authorityHeader = validInput();
  authorityHeader.browser.network.browserSiteAuthorityHeaderCount = 1;
  assert.throws(
    () => buildRealShellCertificationEnvelope(authorityHeader),
    /browser-supplied authority/i,
  );
});

test('rejects persisted sensitive data, fixture fallback, and unsafe rollback claims', () => {
  const persisted = validInput();
  persisted.browser.storage.persistedSensitivePayloadCount = 1;
  assert.throws(
    () => buildRealShellCertificationEnvelope(persisted),
    /persisted sensitive/i,
  );

  const persistedPrincipal = validInput();
  persistedPrincipal.browser.storage.sensitiveCategories.principal = 1;
  assert.throws(
    () => buildRealShellCertificationEnvelope(persistedPrincipal),
    /persisted principal/i,
  );

  const fixtureFallback = validInput();
  fixtureFallback.browser.failures[0].fixtureFallback = true;
  assert.throws(
    () => buildRealShellCertificationEnvelope(fixtureFallback),
    /fixture fallback/i,
  );

  const unsafeRollback = validInput();
  unsafeRollback.policy.rollback.demoFallbackAllowed = true;
  assert.throws(
    () => buildRealShellCertificationEnvelope(unsafeRollback),
    /Demo fallback/i,
  );

  const openFailure = validInput();
  openFailure.policy.rollback.failClosedOnSessionOrCapabilityFailure = false;
  assert.throws(
    () => buildRealShellCertificationEnvelope(openFailure),
    /fail closed/i,
  );

  const formalClaim = validInput();
  formalClaim.policy.commandTraffic.formalProductionClaim = true;
  assert.throws(
    () => buildRealShellCertificationEnvelope(formalClaim),
    /formal production claim/i,
  );
});
