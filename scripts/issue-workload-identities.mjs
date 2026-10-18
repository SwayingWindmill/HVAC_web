// Issues the mutual-TLS identities of workloads that call internal owners themselves, from the
// runtime's internal CA (PHASE1_RUNTIME_DIR). Existing identities are kept, so it can run on
// every deployment; local:up runs it too.
import { runtimePath } from './lib/local-environment.mjs';
import { ensureWorkloadIdentity, workloadIdentities } from './lib/workload-identity.mjs';

for (const { name, spiffeId, dns } of workloadIdentities) {
  if (ensureWorkloadIdentity(runtimePath('internal-pki'), name, spiffeId, dns)) console.log(`Issued the ${name} workload identity (${spiffeId}).`);
}
