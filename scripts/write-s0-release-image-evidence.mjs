import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const name = argument('name');
const image = argument('image');
const digest = argument('digest');
const trivyReport = argument('trivy');
const attestation = argument('github-attestation') ?? 'published';
const outputPath = resolve(root, argument('output') ?? `out/s0-release-evidence/images/${name}.json`);

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

assert(name && /^[a-z0-9-]+$/.test(name), 'image evidence requires a lowercase --name');
assert(image && /^[a-z0-9./_-]+$/.test(image), 'image evidence requires a registry --image without a tag or digest');
assert(digest && /^sha256:[a-f0-9]{64}$/.test(digest), 'image evidence requires --digest=sha256:<64 hex>');
assert(trivyReport && trivyReport.endsWith('.json'), 'image evidence requires --trivy=<json path>');
assert(['published', 'skipped-non-public'].includes(attestation), 'github attestation state is invalid');

const repository = process.env.GITHUB_REPOSITORY ?? 'SwayingWindmill/HVAC_web';
const runId = process.env.GITHUB_RUN_ID ?? null;
const runURL = runId ? `https://github.com/${repository}/actions/runs/${runId}` : null;
const evidence = {
  schemaVersion: 1,
  ticket: '08-s0-release-evidence',
  name,
  image,
  digest,
  immutableReference: `${image}@${digest}`,
  source: {
    repository,
    commit: process.env.GITHUB_SHA ?? null,
    ref: process.env.GITHUB_REF ?? null,
    workflow: process.env.GITHUB_WORKFLOW ?? null,
    workflowRef: process.env.GITHUB_WORKFLOW_REF ?? null,
    runId,
    runAttempt: process.env.GITHUB_RUN_ATTEMPT ?? null,
    runURL,
  },
  build: {
    sbom: { status: 'published-by-buildkit', storage: 'oci-attestation-and-workflow-build-record' },
    provenance: { status: 'published-by-buildkit', mode: 'max', storage: 'oci-attestation-and-workflow-build-record' },
  },
  security: {
    embeddedSecretScan: { status: 'passed', scanner: 'trivy', report: trivyReport },
    cosign: {
      status: 'verified',
      oidcIssuer: 'https://token.actions.githubusercontent.com',
      certificateIdentity: `https://github.com/${repository}/.github/workflows/s0-supply-chain.yml@${process.env.GITHUB_REF ?? 'unknown-ref'}`,
    },
    githubBuildAttestation: {
      status: attestation,
      subject: `${image}@${digest}`,
      repository,
    },
  },
  recordedAt: new Date().toISOString(),
};

await mkdir(dirname(outputPath), { recursive: true });
await writeFile(outputPath, `${JSON.stringify(evidence, null, 2)}\n`);
console.log(`Recorded S0 release image evidence for ${name}: ${image}@${digest}`);
