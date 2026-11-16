import { createPrivateKey, createPublicKey, randomBytes, sign, X509Certificate } from 'node:crypto';
import * as fs from 'node:fs';
import * as https from 'node:https';
import { isIP as netIsIp } from 'node:net';
import * as path from 'node:path';
import { mergeTransportCaCertificates, type NodeRedFlowItem } from './gateway-nodered-tunnel.ts';

export const DEFAULT_RENEW_THRESHOLD_DAYS = 14;
const MS_PER_DAY = 24 * 60 * 60 * 1000;

export interface CertificateCheckResult {
  renewed: boolean;
  reason: 'valid_beyond_threshold' | 'renewed';
  gatewayId: string;
  remainingMs: number;
  expiresAt: string;
}

export interface RenewGatewayCertificateOptions {
  renewUrl: string;
  certPath: string;
  keyPath: string;
  caPath: string;
  thresholdDays?: number;
  nowMs?: number;
  rejectUnauthorized?: boolean;
  onRenewed?: (result: CertificateCheckResult) => Promise<void> | void;
}

function encodeDerLength(len: number): Buffer {
  if (len < 0x80) {
    return Buffer.from([len]);
  }
  const bytes: number[] = [];
  let n = len;
  while (n > 0) {
    bytes.unshift(n & 0xff);
    n >>= 8;
  }
  return Buffer.from([0x80 | bytes.length, ...bytes]);
}

function derTlv(tag: number, content: Buffer): Buffer {
  return Buffer.concat([Buffer.from([tag]), encodeDerLength(content.length), content]);
}

export function extractCommonName(subject: string): string {
  for (const part of subject.split(/\r?\n|,/)) {
    const trimmed = part.trim();
    if (trimmed.startsWith('CN=')) {
      return trimmed.slice(3).trim();
    }
  }
  throw new Error(`certificate subject lacks CN: ${subject}`);
}

export function getCertificateRemainingMs(certPem: string | Buffer, nowMs = Date.now()): number {
  const cert = new X509Certificate(certPem);
  return Date.parse(cert.validTo) - nowMs;
}

export function shouldRenewCertificate(
  certPem: string | Buffer,
  thresholdDays = DEFAULT_RENEW_THRESHOLD_DAYS,
  nowMs = Date.now(),
): { renew: boolean; remainingMs: number; gatewayId: string; expiresAt: string } {
  const cert = new X509Certificate(certPem);
  const validToMs = Date.parse(cert.validTo);
  const remainingMs = validToMs - nowMs;
  const gatewayId = extractCommonName(cert.subject);
  return {
    renew: remainingMs <= thresholdDays * MS_PER_DAY,
    remainingMs,
    gatewayId,
    expiresAt: new Date(validToMs).toISOString(),
  };
}

export function buildPkcs10CsrPem(commonName: string, privateKeyPem: string | Buffer): string {
  const privKey = createPrivateKey(privateKeyPem);
  const spkiDer = createPublicKey(privKey).export({ type: 'spki', format: 'der' });

  const cnOid = Buffer.from([0x06, 0x03, 0x55, 0x04, 0x03]); // 2.5.4.3 (CN)
  const cnVal = derTlv(0x0c, Buffer.from(commonName, 'utf8')); // UTF8String
  const attrTypeAndVal = derTlv(0x30, Buffer.concat([cnOid, cnVal]));
  const rdn = derTlv(0x31, attrTypeAndVal);
  const subject = derTlv(0x30, rdn);
  const versionZero = derTlv(0x02, Buffer.from([0x00]));
  const attrs = derTlv(0xa0, Buffer.alloc(0));

  const reqInfo = derTlv(0x30, Buffer.concat([versionZero, subject, spkiDer, attrs]));

  let sigAlgDer: Buffer;
  if (privKey.asymmetricKeyType === 'ec') {
    // ecdsa-with-SHA256 (1.2.840.10045.4.3.2)
    sigAlgDer = derTlv(0x30, Buffer.from([0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x02]));
  } else if (privKey.asymmetricKeyType === 'rsa') {
    // sha256WithRSAEncryption (1.2.840.113549.1.1.11) + NULL
    sigAlgDer = derTlv(
      0x30,
      Buffer.from([0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0b, 0x05, 0x00]),
    );
  } else {
    throw new Error(`unsupported gateway key type: ${privKey.asymmetricKeyType ?? 'unknown'}`);
  }

  const signature = sign('sha256', reqInfo, privKey);
  const bitString = derTlv(0x03, Buffer.concat([Buffer.from([0x00]), signature]));
  const csrDer = derTlv(0x30, Buffer.concat([reqInfo, sigAlgDer, bitString]));

  const b64 = csrDer.toString('base64');
  const lines = b64.match(/.{1,64}/g) ?? [];
  return `-----BEGIN CERTIFICATE REQUEST-----\n${lines.join('\n')}\n-----END CERTIFICATE REQUEST-----\n`;
}

function atomicWriteFile(filePath: string, content: string, mode = 0o644): void {
  const dir = path.dirname(filePath);
  const base = path.basename(filePath);
  const tmpPath = path.join(dir, `.${base}.tmp-${process.pid}-${randomBytes(6).toString('hex')}`);
  const fd = fs.openSync(tmpPath, 'wx', mode);
  try {
    fs.writeSync(fd, content, 0, 'utf8');
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
  try {
    fs.renameSync(tmpPath, filePath);
  } catch (err) {
    try {
      fs.unlinkSync(tmpPath);
    } catch {
      // ignore temp cleanup failure after failed rename
    }
    throw err;
  }
}

function postRenewRequest(
  renewUrl: string,
  csrPem: string,
  certPem: string,
  keyPem: string,
  caPem: string,
  rejectUnauthorized: boolean,
): Promise<{ certificatePem: string; caPem: string; expiresAt: string }> {
  return new Promise((resolve, reject) => {
    const target = new URL(renewUrl);
    const body = JSON.stringify({ csrPem });
    const req = https.request(
      {
        hostname: target.hostname,
        port: target.port ? Number(target.port) : 443,
        path: `${target.pathname}${target.search}`,
        method: 'POST',
        cert: certPem,
        key: keyPem,
        ca: mergeTransportCaCertificates(caPem),
        ...(netIsIp(target.hostname) === 0 ? { servername: target.hostname } : {}),
        rejectUnauthorized,
        headers: {
          'Content-Type': 'application/json',
          'Content-Length': Buffer.byteLength(body),
        },
        timeout: 15_000,
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (chunk: Buffer) => chunks.push(chunk));
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8');
          if ((res.statusCode ?? 0) < 200 || (res.statusCode ?? 0) >= 300) {
            reject(new Error(`gateway renew failed HTTP ${res.statusCode ?? 0}: ${text}`));
            return;
          }
          try {
            const parsed = JSON.parse(text) as { certificatePem?: string; caPem?: string; expiresAt?: string };
            if (!parsed.certificatePem || !parsed.caPem || !parsed.expiresAt) {
              reject(new Error('gateway renew response missing required PEM/expiry fields'));
              return;
            }
            resolve({ certificatePem: parsed.certificatePem, caPem: parsed.caPem, expiresAt: parsed.expiresAt });
          } catch (err) {
            reject(err);
          }
        });
      },
    );
    req.on('timeout', () => {
      req.destroy(new Error('gateway renew request timed out'));
    });
    req.on('error', reject);
    req.write(body);
    req.end();
  });
}

export async function checkAndRenewGatewayCertificate(
  options: RenewGatewayCertificateOptions,
): Promise<CertificateCheckResult> {
  const nowMs = options.nowMs ?? Date.now();
  const thresholdDays = options.thresholdDays ?? DEFAULT_RENEW_THRESHOLD_DAYS;
  const certPem = fs.readFileSync(options.certPath, 'utf8');
  const status = shouldRenewCertificate(certPem, thresholdDays, nowMs);

  if (!status.renew) {
    return {
      renewed: false,
      reason: 'valid_beyond_threshold',
      gatewayId: status.gatewayId,
      remainingMs: status.remainingMs,
      expiresAt: status.expiresAt,
    };
  }

  const keyPem = fs.readFileSync(options.keyPath, 'utf8');
  const currentCaPem = fs.readFileSync(options.caPath, 'utf8');
  const csrPem = buildPkcs10CsrPem(status.gatewayId, keyPem);

  const issued = await postRenewRequest(
    options.renewUrl,
    csrPem,
    certPem,
    keyPem,
    currentCaPem,
    options.rejectUnauthorized ?? true,
  );

  const currentCa = new X509Certificate(currentCaPem);
  const nextCert = new X509Certificate(issued.certificatePem);
  const nextCa = new X509Certificate(issued.caPem);
  if (!nextCa.raw.equals(currentCa.raw)) {
    throw new Error('renewed response CA certificate does not match pinned CA');
  }
  if (!nextCert.checkIssued(currentCa) || !nextCert.verify(currentCa.publicKey)) {
    throw new Error('renewed certificate failed pinned CA signature verification');
  }
  const validFromMs = Date.parse(nextCert.validFrom);
  const validToMs = Date.parse(nextCert.validTo);
  if (!Number.isFinite(validFromMs) || !Number.isFinite(validToMs) || validFromMs > nowMs || validToMs <= nowMs) {
    throw new Error('renewed certificate validity window is invalid');
  }
  const expectedPubDer = createPublicKey(createPrivateKey(keyPem)).export({ type: 'spki', format: 'der' });
  const certPubDer = nextCert.publicKey.export({ type: 'spki', format: 'der' });
  if (!expectedPubDer.equals(certPubDer)) {
    throw new Error('renewed certificate public key does not match gateway private key');
  }
  if (extractCommonName(nextCert.subject) !== status.gatewayId) {
    throw new Error('renewed certificate CN does not match gateway identity');
  }

  atomicWriteFile(options.certPath, issued.certificatePem, 0o644);

  const result: CertificateCheckResult = {
    renewed: true,
    reason: 'renewed',
    gatewayId: status.gatewayId,
    remainingMs: Date.parse(nextCert.validTo) - nowMs,
    expiresAt: new Date(Date.parse(nextCert.validTo)).toISOString(),
  };
  await options.onRenewed?.(result);
  return result;
}

export function buildGatewayCertAutorenewFlow(config: {
  renewUrl: string;
  certPath: string;
  keyPath: string;
  caPath: string;
  modulePath?: string;
}): NodeRedFlowItem[] {
  const tabId = 'tab-platform-cert';
  const injectId = 'inject-platform-cert-check';
  const fnId = 'fn-platform-cert-renew';
  const moduleRequire = config.modulePath ?? '/root/hvac-platform/gateway-cert-autorenew.js';
  const funcLines = [
    `const { checkAndRenewGatewayCertificate } = require(${JSON.stringify(moduleRequire)});`,
    `const result = await checkAndRenewGatewayCertificate({`,
    `  renewUrl: ${JSON.stringify(config.renewUrl)},`,
    `  certPath: ${JSON.stringify(config.certPath)},`,
    `  keyPath: ${JSON.stringify(config.keyPath)},`,
    `  caPath: ${JSON.stringify(config.caPath)},`,
    `  onRenewed: async () => {`,
    `    const tunnelClient = global.get('hvacTunnelClient') || context.flow.get('hvacTunnelClient');`,
    `    if (tunnelClient && typeof tunnelClient.reloadCredentials === 'function') { await tunnelClient.reloadCredentials(); }`,
    `  },`,
    `});`,
    `node.status({ fill: result.renewed ? 'blue' : 'green', shape: 'dot', text: result.renewed ? 'renewed' : 'valid' });`,
    `return { ...msg, payload: result };`,
  ];

  return [
    { id: tabId, type: 'tab', label: '平台·证书', disabled: false, info: '14-day mTLS certificate auto-renewal' },
    {
      id: injectId,
      type: 'inject',
      z: tabId,
      name: '每12小时检查证书',
      props: [{ p: 'payload', vt: 'date' }],
      repeat: '43200',
      once: true,
      onceDelay: 5,
      wires: [[fnId]],
    },
    {
      id: fnId,
      type: 'function',
      z: tabId,
      name: '证书<=14天自动续签',
      func: funcLines.join('\n'),
      outputs: 1,
      wires: [[]],
    },
  ];
}
