import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

// A workload's mutual-TLS identity, issued by the runtime's internal CA like every other:
// <pki>/<name>/tls.{crt,key} carrying the SPIFFE ID. A workload that also serves (dns) can be
// reached under that name. Issued once; an existing identity is kept.
export function ensureWorkloadIdentity(pki, name, spiffeId, dns) {
  const identity = path.join(pki, name);
  if (existsSync(path.join(identity, 'tls.crt'))) return false;
  mkdirSync(identity, { recursive: true, mode: 0o750 });
  const file = (entry) => path.join(identity, entry);
  writeFileSync(file('extensions.cnf'), [
    'basicConstraints=critical,CA:FALSE',
    'keyUsage=critical,digitalSignature',
    dns ? 'extendedKeyUsage=serverAuth,clientAuth' : 'extendedKeyUsage=clientAuth',
    dns ? `subjectAltName=URI:${spiffeId},DNS:${dns}` : `subjectAltName=URI:${spiffeId}`,
    '',
  ].join('\n'), { mode: 0o640 });
  execFileSync('openssl', ['ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', file('tls.key')]);
  // Readable by the container's non-root user through the group, as the other identities are.
  execFileSync('chmod', ['640', file('tls.key')]);
  execFileSync('openssl', ['req', '-new', '-key', file('tls.key'), '-subj', `/CN=${name}`, '-out', file('request.csr')]);
  execFileSync('openssl', ['x509', '-req', '-in', file('request.csr'), '-CA', path.join(pki, 'ca.crt'), '-CAkey', path.join(pki, 'ca.key'),
    '-CAserial', path.join(pki, 'ca.srl'), '-days', '365', '-sha256', '-extfile', file('extensions.cnf'), '-out', file('tls.crt')], { stdio: 'ignore' });
  return true;
}

// Workloads that call internal owners themselves, or serve them over mutual TLS, and so need
// their own identity.
export const workloadIdentities = Object.freeze([
  { name: 'report', spiffeId: 'spiffe://hvac.local/report-service' },
  { name: 'optimization', spiffeId: 'spiffe://hvac.local/optimization-service' },
  { name: 'mv', spiffeId: 'spiffe://hvac.local/mv-service' },
  // FDD serves the Gateway and the Operations Agent over mutual TLS (#595).
  { name: 'fdd', spiffeId: 'spiffe://hvac.local/fdd-service', dns: 'fdd-service' },
]);
