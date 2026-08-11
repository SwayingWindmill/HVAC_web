import { readFile, readdir } from 'node:fs/promises';
import { resolve, relative, join } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

async function markdownFiles(path) {
  const absolute = resolve(root, path);
  const result = [];
  async function walk(directory) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      if (['node_modules', 'out', '.git', '.worktrees'].includes(entry.name)) continue;
      const full = join(directory, entry.name);
      if (entry.isDirectory()) await walk(full);
      else if (entry.isFile() && entry.name.endsWith('.md')) result.push(relative(root, full).replaceAll('\\', '/'));
    }
  }
  await walk(absolute);
  return result;
}

const canonical = {
  readme: await read('README.md'),
  architecture: await read('docs/architecture/phase1-overall-architecture.md'),
  scopePolicy: await read('docs/architecture/document-scope-policy.md'),
  deployment: await read('deploy/platform/phase1/README.md'),
  alignment: await read('docs/operations/phase1-deployment-alignment.md'),
};

assert(canonical.readme.includes('Phase 1 canonical deployment 是 **Linux Server + Docker Compose**'), 'README must identify Linux Server + Docker Compose as the Phase 1 canonical deployment');
assert(canonical.readme.includes('docs/architecture/phase1-overall-architecture.md'), 'README must link the Phase 1 overall architecture baseline');
assert(canonical.architecture.includes('一级架构只表达职责'), 'overall architecture must keep implementation stage names out of L1');
assert(canonical.architecture.includes('Phase 1 canonical deployment'), 'overall architecture must define the canonical Phase 1 deployment');
assert(canonical.architecture.includes('Docker Compose'), 'overall architecture must keep Docker Compose as the Phase 1 orchestration baseline');
assert(canonical.architecture.includes('Kubernetes') && canonical.architecture.includes('不作为 Phase 1 前置'), 'overall architecture must explicitly defer Kubernetes as a Phase 1 prerequisite');
assert(canonical.deployment.includes('Kubernetes/Kustomize assets elsewhere in the repository are future-stage or certification references'), 'canonical deployment README must bound retained Kubernetes assets');
assert(canonical.scopePolicy.includes('CANONICAL_ARCHITECTURE'), 'document scope policy must define CANONICAL_ARCHITECTURE');
assert(canonical.scopePolicy.includes('CANONICAL_PHASE1_DEPLOYMENT'), 'document scope policy must define CANONICAL_PHASE1_DEPLOYMENT');
assert(canonical.scopePolicy.includes('CERTIFICATION_REFERENCE'), 'document scope policy must define CERTIFICATION_REFERENCE');
assert(canonical.scopePolicy.includes('LOCAL_FIXTURE'), 'document scope policy must define LOCAL_FIXTURE');
assert(canonical.scopePolicy.includes('FUTURE_STAGE'), 'document scope policy must define FUTURE_STAGE');
assert(canonical.alignment.includes('Linux Server + Docker Compose'), 'deployment alignment document must preserve the document-defined Phase 1 deployment model');

const scopedDocuments = {
  'docs/operations/s0-delivery.md': ['Scope: CERTIFICATION_REFERENCE', 'not the canonical Phase 1 deployment model'],
  'docs/operations/s0-release-evidence.md': ['Scope: CERTIFICATION_REFERENCE', 'does **not** define the canonical Phase 1 deployment'],
  'docs/operations/s0-security-failure-gates.md': ['Scope: CERTIFICATION_REFERENCE', 'not a Phase 1 deployment prerequisite'],
  'docs/operations/s0-observability.md': ['Scope: CERTIFICATION_REFERENCE', 'Redpanda is not required by the canonical Phase 1 deployment'],
  'docs/security/s0-durable-session-audit.md': ['Scope: CERTIFICATION_REFERENCE', 'does not make Kafka/Redpanda a Phase 1 Production dependency'],
  'deploy/s3/target/README.md': ['Scope: CERTIFICATION_REFERENCE', 'not the canonical Phase 1 platform deployment'],
  'deploy/s3/local/README.md': ['Scope: LOCAL_FIXTURE', 'not the canonical Phase 1 deployment'],
  'deploy/s3/local-thingsboard/README.md': ['Scope: LOCAL_FIXTURE', 'not a Phase 1 Production topology'],
};

for (const [path, markers] of Object.entries(scopedDocuments)) {
  const content = await read(path);
  for (const marker of markers) assert(content.includes(marker), `${path} is missing document-scope marker: ${marker}`);
}

const files = ['README.md', ...(await markdownFiles('docs')), ...(await markdownFiles('deploy'))];
const prohibitedStatements = [
  /Phase 1\s+(must use|requires?)\s+Kubernetes/i,
  /Kubernetes\s+(is|remains)\s+required\s+for\s+Phase 1/i,
  /第一阶段\s*(必须|要求)\s*(使用|采用)?\s*Kubernetes/i,
  /Production\s+(must use|requires?)\s+(Kafka|Redpanda)/i,
  /Phase 1\s+(must use|requires?)\s+(Kafka|Redpanda)/i,
  /(Kafka|Redpanda)\s+(is|remains)\s+required\s+for\s+Phase 1/i,
];

for (const path of files) {
  const content = await read(path);
  for (const pattern of prohibitedStatements) {
    assert(!pattern.test(content), `${path} contains a statement that promotes a deferred/compatibility component into a Phase 1 requirement: ${pattern}`);
  }

  if ((path.startsWith('docs/operations/s0-') || path.startsWith('docs/security/s0-')) && /Redpanda|Kubernetes/.test(content)) {
    assert(content.includes('Scope: CERTIFICATION_REFERENCE'), `${path} mentions Kubernetes/Redpanda but is missing CERTIFICATION_REFERENCE scope`);
  }
  if (path.startsWith('deploy/s3/') && path.endsWith('README.md') && /Kubernetes|kind cluster|ClusterIP/.test(content)) {
    assert(/Scope: (CERTIFICATION_REFERENCE|LOCAL_FIXTURE)/.test(content), `${path} mentions Kubernetes but is missing certification/local-fixture scope`);
  }
}

if (failures.length > 0) {
  console.error(`Phase 1 document consistency check failed:\n${failures.map((failure) => `- ${failure}`).join('\n')}`);
  process.exit(1);
}

console.log(`Phase 1 document consistency check passed: markdownFiles=${files.length}, scopedLegacyDocs=${Object.keys(scopedDocuments).length}`);
