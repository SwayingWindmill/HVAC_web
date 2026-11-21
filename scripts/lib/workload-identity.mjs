import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import path from 'node:path';

// Internal CA mTLS workload identity; re-issues automatically if extensions change (#612).
export function ensureWorkloadIdentity(pki, name, spiffeId, dns) {
  const identity = path.join(pki, name);
  const file = (entry) => path.join(identity, entry);
  const extensions = [
    'basicConstraints=critical,CA:FALSE',
    'keyUsage=critical,digitalSignature',
    dns ? 'extendedKeyUsage=serverAuth,clientAuth' : 'extendedKeyUsage=clientAuth',
    dns ? `subjectAltName=URI:${spiffeId},DNS:${dns}` : `subjectAltName=URI:${spiffeId}`,
    '',
  ].join('\n');
  if (existsSync(file('tls.crt')) && existsSync(file('extensions.cnf')) && readFileSync(file('extensions.cnf'), 'utf8') === extensions) {
    if (statSync(identity).gid !== serviceGroup) shareWithServices(identity);
    return false;
  }
  mkdirSync(identity, { recursive: true, mode: 0o750 });
  writeFileSync(file('extensions.cnf'), extensions, { mode: 0o640 });
  execFileSync('openssl', ['ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', file('tls.key')]);
  // Readable by the container's non-root user through the group, as the other identities are.
  execFileSync('chmod', ['640', file('tls.key')]);
  execFileSync('openssl', ['req', '-new', '-key', file('tls.key'), '-subj', `/CN=${name}`, '-out', file('request.csr')]);
  execFileSync('openssl', ['x509', '-req', '-in', file('request.csr'), '-CA', path.join(pki, 'ca.crt'), '-CAkey', path.join(pki, 'ca.key'),
    '-CAserial', path.join(pki, 'ca.srl'), '-days', '365', '-sha256', '-extfile', file('extensions.cnf'), '-out', file('tls.crt')], { stdio: 'ignore' });
  shareWithServices(identity);
  return true;
}

const serviceGroup = 65532;

// The services run as 65532 and read their identity through its group, as the other identities
// are; the directory is created by the operator's own user, which cannot hand it to that group.
function shareWithServices(identity) {
  execFileSync('docker', ['run', '--rm', '--user', '0', '--entrypoint', 'chgrp', '-v', `${identity}:/identity`, 'postgres:16.4-bookworm', '-R', String(serviceGroup), '/identity'], { stdio: 'ignore' });
}

export const workloadIdentities = Object.freeze([
  { name: 'command', spiffeId: 'spiffe://hvac.local/command-service', dns: 'command-service' },
  { name: 'report', spiffeId: 'spiffe://hvac.local/report-service', dns: 'report-service' },
  { name: 'fdd', spiffeId: 'spiffe://hvac.local/fdd-service', dns: 'fdd-service' },
  { name: 'forecast', spiffeId: 'spiffe://hvac.local/forecast-service', dns: 'forecast-service' },
  { name: 'optimization', spiffeId: 'spiffe://hvac.local/optimization-service', dns: 'optimization-service' },
  { name: 'mv', spiffeId: 'spiffe://hvac.local/mv-service', dns: 'mv-service' },
  { name: 'host-edge-proxy', spiffeId: 'spiffe://hvac.local/host-edge-proxy' },
]);
