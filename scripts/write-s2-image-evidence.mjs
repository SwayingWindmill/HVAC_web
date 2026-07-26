import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const arg = (name, fallback = '') => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const name = arg('name');
const digest = arg('digest');
const user = arg('user');
const trivyPath = resolve(root, arg('trivy'));
const sbomPath = resolve(root, arg('sbom'));
const metadataPath = resolve(root, arg('build-metadata'));
const output = resolve(root, arg('output', `out/s2-image-evidence/${name}.json`));
const formalReleaseEligible = arg('formal', 'false') === 'true';
if (!/^(telemetry-runtime|telemetry-runtime-migrator)$/.test(name)) throw new Error('unsupported S2 image name');
if (!/^sha256:[0-9a-f]{64}$/.test(digest)) throw new Error('immutable image digest is required');
if (!user || user === '0' || user === 'root' || user.startsWith('0:')) throw new Error('production image must run as non-root');
const trivyRaw = await readFile(trivyPath);
const trivy = JSON.parse(trivyRaw.toString('utf8'));
const sbomRaw = await readFile(sbomPath);
const sbom = JSON.parse(sbomRaw.toString('utf8'));
const metadataRaw = await readFile(metadataPath);
const metadata = JSON.parse(metadataRaw.toString('utf8'));
if (sbom.bomFormat !== 'CycloneDX') throw new Error('CycloneDX SBOM is required');
if (!metadata || typeof metadata !== 'object') throw new Error('Buildx metadata is required');
let secretFindings = 0;
let highOrCriticalVulnerabilities = 0;
for (const result of trivy.Results ?? []) {
  secretFindings += (result.Secrets ?? []).length;
  highOrCriticalVulnerabilities += (result.Vulnerabilities ?? []).filter((entry) => ['HIGH', 'CRITICAL'].includes(entry.Severity)).length;
}
if (secretFindings !== 0 || highOrCriticalVulnerabilities !== 0) throw new Error(`${name} image scan failed`);
const provenance = arg('provenance');
const githubAttestation = arg('github-attestation');
if (formalReleaseEligible && (provenance !== 'buildkit-mode-max' || githubAttestation !== 'published')) {
  throw new Error('formal image evidence requires BuildKit mode=max and GitHub provenance');
}
const record = {
  schemaVersion: 1,
  status: 'passed',
  name,
  image: arg('image'),
  digest,
  user,
  secretFindings,
  highOrCriticalVulnerabilities,
  formalReleaseEligible,
  sbom: {
    format: 'CycloneDX',
    file: basename(sbomPath),
    sha256: createHash('sha256').update(sbomRaw).digest('hex'),
  },
  buildMetadata: {
    file: basename(metadataPath),
    sha256: createHash('sha256').update(metadataRaw).digest('hex'),
  },
  provenance,
  githubAttestation,
  repositorySha: process.env.GITHUB_SHA ?? 'local-uncommitted',
  workflowRunId: process.env.GITHUB_RUN_ID ?? 'local',
};
await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify(record, null, 2)}\n`);
console.log(`S2 image evidence written: ${output}`);
