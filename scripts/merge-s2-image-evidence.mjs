import { copyFile, mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const input = resolve(root, process.argv.find((value) => value.startsWith('--input='))?.slice(8) ?? 'out/s2-image-evidence');
const outputDirectory = resolve(root, process.argv.find((value) => value.startsWith('--output='))?.slice(9) ?? 'out/s2-release-evidence');
const files = (await readdir(input)).filter((name) => /^(telemetry-runtime|telemetry-runtime-migrator)\.json$/.test(name)).sort();
if (files.length !== 2) throw new Error(`expected runtime and migrator image evidence, found ${files.length}`);
const images = [];
await mkdir(outputDirectory, { recursive: true });
for (const file of files) {
  const record = JSON.parse(await readFile(resolve(input, file), 'utf8'));
  if (!record.name || !/^sha256:[0-9a-f]{64}$/.test(record.digest) || !record.user || record.user === '0' || record.user === 'root') {
    throw new Error(`invalid S2 production image evidence: ${file}`);
  }
  if (record.secretFindings !== 0 || record.highOrCriticalVulnerabilities !== 0) throw new Error(`S2 image gate failed: ${file}`);
  if (record.sbom?.format !== 'CycloneDX' || !/^[0-9a-f]{64}$/.test(record.sbom?.sha256 ?? '')) throw new Error(`S2 SBOM evidence is invalid: ${file}`);
  if (!/^[0-9a-f]{64}$/.test(record.buildMetadata?.sha256 ?? '')) throw new Error(`S2 Buildx metadata evidence is invalid: ${file}`);
  await copyFile(resolve(input, record.sbom.file), resolve(outputDirectory, record.sbom.file));
  await copyFile(resolve(input, record.buildMetadata.file), resolve(outputDirectory, record.buildMetadata.file));
  images.push(record);
}
await writeFile(resolve(outputDirectory, 'production-image-report.json'), `${JSON.stringify({ schemaVersion: 1, status: 'passed', images }, null, 2)}\n`);
await writeFile(resolve(outputDirectory, 'sbom-provenance-report.json'), `${JSON.stringify({
  schemaVersion: 1,
  status: 'passed',
  formalReleaseEligible: images.every((image) => image.formalReleaseEligible === true),
  images: images.map(({ name, digest, sbom, buildMetadata, provenance, githubAttestation, formalReleaseEligible }) => ({ name, digest, sbom, buildMetadata, provenance, githubAttestation, formalReleaseEligible })),
  buildKitProvenanceRequiredForFormalRelease: true,
  githubAttestationRequiredForFormalRelease: true,
}, null, 2)}\n`);
console.log(`S2 image evidence merged: ${outputDirectory}`);
