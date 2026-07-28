export const RMS_REQUIRED_BROWSER_SCENARIOS = Object.freeze([
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
]);

export const RMS_REQUIRED_CERTIFICATION_GATES = Object.freeze([
  'contracts',
  'shell-tests',
  'go-tests',
  'typecheck',
  'real-graph',
  'real-build',
  'demo-build',
  'bundle',
  'browser',
]);

function assert(condition, message) {
  if (!condition) throw new Error(`Invalid RMS-08 certification evidence: ${message}`);
}

function deepFreeze(value) {
  if (!value || typeof value !== 'object' || Object.isFrozen(value)) return value;
  for (const child of Object.values(value)) deepFreeze(child);
  return Object.freeze(value);
}

function assertZero(value, message) {
  assert(Number.isInteger(value) && value === 0, message);
}

export function buildRealShellCertificationEnvelope(input) {
  assert(input && typeof input === 'object', 'input is required');
  assert(/^[0-9a-f]{40}$/.test(input.repositorySha ?? ''), 'repository SHA must be immutable');

  assert(Array.isArray(input.gates), 'certification gate results are required');
  const gatesByName = new Map(input.gates.map((gate) => [gate.name, gate]));
  for (const name of RMS_REQUIRED_CERTIFICATION_GATES) {
    const gate = gatesByName.get(name);
    assert(gate?.passed === true, `certification gate ${name} did not pass`);
    assert(Number.isFinite(gate.durationMs) && gate.durationMs >= 0, `certification gate ${name} has no duration`);
  }

  const contract = input.contract ?? {};
  assert(typeof contract.generatorVersion === 'string' && contract.generatorVersion.length > 0, 'contract generator version is required');
  assert(/^[0-9a-f]{64}$/.test(contract.contractSha256 ?? ''), 'contract SHA-256 is required');
  assert(contract.generatedClientDrift === false, 'generated client drift must be zero');

  const graph = input.graph ?? {};
  assert(graph.passed === true, 'Real dependency graph did not pass');
  assert(Array.isArray(graph.violations) && graph.violations.length === 0, 'Real dependency graph contains violations');
  assert(Array.isArray(graph.files) && graph.files.length > 0, 'Real dependency graph contains no files');

  const bundle = input.bundle ?? {};
  assert(bundle.passed === true && bundle.real?.passed === true, 'Real bundle audit did not pass');
  assert(Array.isArray(bundle.real?.forbidden) && bundle.real.forbidden.length === 0, 'Real bundle contains forbidden Demo or Mock symbols');
  assert(Array.isArray(bundle.real?.missingRequired) && bundle.real.missingRequired.length === 0, 'Real bundle is missing required markers');

  const browser = input.browser ?? {};
  assert(browser.passed === true, 'browser audit did not pass');
  for (const scenario of RMS_REQUIRED_BROWSER_SCENARIOS) {
    assert(browser.scenarios?.[scenario]?.passed === true, `browser scenario ${scenario} did not pass`);
  }

  const network = browser.network ?? {};
  assert(Number.isInteger(network.requestCount) && network.requestCount > 0, 'browser request inventory is empty');
  assertZero(network.browserAuthorizationHeaderCount, 'browser supplied an Authorization header');
  assertZero(network.browserOrganizationAuthorityHeaderCount, 'browser-supplied authority includes an Organization header');
  assertZero(network.browserSiteAuthorityHeaderCount, 'browser-supplied authority includes a Site header');
  assertZero(network.browserOtherAuthorityHeaderCount, 'browser-supplied authority includes another authority header');

  const storage = browser.storage ?? {};
  assert(Array.isArray(storage.samples) && storage.samples.length > 0, 'browser storage inventory is empty');
  assertZero(storage.persistedSensitivePayloadCount, 'browser persisted sensitive payloads');
  for (const category of ['token', 'csrf', 'principal', 'registry', 'telemetry', 'command']) {
    assertZero(storage.sensitiveCategories?.[category], `browser persisted ${category} data`);
  }
  for (const sample of storage.samples) {
    assertZero(sample.localStorageEntries, `localStorage was not empty for ${sample.label ?? 'unknown sample'}`);
    assertZero(sample.sessionStorageEntries, `sessionStorage was not empty for ${sample.label ?? 'unknown sample'}`);
  }

  assert(Array.isArray(browser.failures) && browser.failures.length > 0, 'failure evidence is empty');
  for (const failure of browser.failures) {
    assert(typeof failure.code === 'string' && failure.code.length > 0, 'failure code is missing');
    assert(/^[0-9a-f]{32}$/.test(failure.traceId ?? ''), `failure ${failure.code} has no trace ID`);
    assert(failure.fixtureFallback === false, `failure ${failure.code} used fixture fallback`);
  }

  const policy = input.policy ?? {};
  assert(policy.schemaVersion === 1 && policy.artifactMode === 'real', 'release policy must select Real artifacts');
  assert(policy.rollback?.previousRealArtifactOnly === true, 'rollback must select a previous Real artifact only');
  assert(policy.rollback?.demoFallbackAllowed === false, 'Demo fallback must remain disabled');
  assert(policy.rollback?.failClosedOnSessionOrCapabilityFailure === true, 'rollback must fail closed on Session or capability failure');
  assert(policy.commandTraffic?.productionTrafficChanged === false, 'S3 production Command traffic must remain unchanged');
  assert(policy.commandTraffic?.formalProductionClaim === false, 'formal production claim must remain false');
  assert(policy.commandTraffic?.s3CertificationChanged === false, 'S3 certification claims must remain unchanged');

  return deepFreeze({
    schemaVersion: 1,
    certification: 'RMS-08_REAL_MODE_SHELL',
    repositorySha: input.repositorySha,
    passed: true,
    formalProductionClaim: false,
    contract: {
      generatorVersion: contract.generatorVersion,
      contractSha256: contract.contractSha256,
      generatedClientDrift: false,
    },
    evidence: {
      gates: {
        passedCount: RMS_REQUIRED_CERTIFICATION_GATES.length,
        results: RMS_REQUIRED_CERTIFICATION_GATES.map((name) => ({
          name,
          passed: true,
          durationMs: gatesByName.get(name).durationMs,
        })),
      },
      graph: {
        passed: true,
        reachableFileCount: graph.files.length,
        violationCount: 0,
      },
      bundle: {
        passed: true,
        forbiddenDemoMockCount: 0,
        missingRequiredMarkerCount: 0,
      },
      browser: {
        passed: true,
        scenarioCount: RMS_REQUIRED_BROWSER_SCENARIOS.length,
        scenarios: Object.fromEntries(RMS_REQUIRED_BROWSER_SCENARIOS.map((name) => [name, true])),
        requestCount: network.requestCount,
        browserSuppliedAuthorityHeaderCount: 0,
        storageSampleCount: storage.samples.length,
        persistedSensitivePayloadCount: 0,
        persistedSensitiveCategories: {
          token: 0,
          csrf: 0,
          principal: 0,
          registry: 0,
          telemetry: 0,
          command: 0,
        },
        failureEvidenceCount: browser.failures.length,
      },
    },
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
  });
}
