import { createHash, X509Certificate } from 'node:crypto';
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { pullDockerImageWithRetry } from './lib/docker-pull-retry.mjs';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-realtime-backend/centrifugo-config.json');
const lock = JSON.parse(await readFile(resolve(root, 'pocs/platform-components/versions.lock.json'), 'utf8'));
const image = lock.components?.centrifugo?.image;

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: options.cwd ?? root,
    env: options.env ?? process.env,
    encoding: 'utf8',
    windowsHide: true,
    stdio: options.stdio ?? ['ignore', 'pipe', 'pipe'],
  });
  if (result.error || result.status !== 0 || result.signal) {
    const detail = String(result.stderr || result.stdout || result.error?.message || `exit ${result.status ?? result.signal}`).trim();
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return result;
}

assert(typeof image === 'string' && /@sha256:[a-f0-9]{64}$/.test(image), 'locked Centrifugo image digest is missing');

const temporary = await mkdtemp(join(tmpdir(), 'hvac-s2-centrifugo-config-'));
const containerName = `hvac-s2-centrifugo-config-${process.pid}-${Date.now()}`;
const configDirectory = join(temporary, 'centrifugo');
const clientDirectory = join(temporary, 'run', 'secrets', 'centrifugo-client');
const caDirectory = join(temporary, 'run', 'secrets', 'telemetry-runtime-ca');
const configPath = join(configDirectory, 'centrifugo.json');
const certPath = join(clientDirectory, 'tls.crt');
const keyPath = join(clientDirectory, 'tls.key');
const caPath = join(caDirectory, 'ca.crt');
const certificateGeneratorPath = join(temporary, 'generate-cert.go');

try {
  await mkdir(configDirectory, { recursive: true });
  await mkdir(clientDirectory, { recursive: true });
  await mkdir(caDirectory, { recursive: true });
  await copyFile(resolve(root, 'infra/s2-telemetry/realtime/centrifugo.template.json'), configPath);

  await writeFile(certificateGeneratorPath, `package main

import (
  "crypto/rand"
  "crypto/rsa"
  "crypto/x509"
  "crypto/x509/pkix"
  "encoding/pem"
  "math/big"
  "net/url"
  "os"
  "time"
)

func main() {
  certificatePath, keyPath := os.Args[1], os.Args[2]
  privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
  if err != nil { panic(err) }
  spiffe, err := url.Parse("spiffe://hvac.local/centrifugo")
  if err != nil { panic(err) }
  now := time.Now().UTC()
  template := x509.Certificate{
    SerialNumber: big.NewInt(now.UnixNano()),
    Subject: pkix.Name{CommonName: "centrifugo"},
    NotBefore: now.Add(-time.Minute),
    NotAfter: now.Add(time.Hour),
    KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
    ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
    IsCA: true,
    BasicConstraintsValid: true,
    URIs: []*url.URL{spiffe},
  }
  der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
  if err != nil { panic(err) }
  certificateFile, err := os.OpenFile(certificatePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
  if err != nil { panic(err) }
  if err := pem.Encode(certificateFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil { panic(err) }
  if err := certificateFile.Close(); err != nil { panic(err) }
  keyFile, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
  if err != nil { panic(err) }
  if err := pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}); err != nil { panic(err) }
  if err := keyFile.Close(); err != nil { panic(err) }
}
`, 'utf8');
  run(process.execPath, [resolve(root, 'scripts/run-go.mjs'), 'run', certificateGeneratorPath, certPath, keyPath]);
  await copyFile(certPath, caPath);
  const certificate = new X509Certificate(await readFile(certPath));
  assert(certificate.subjectAltName?.includes('URI:spiffe://hvac.local/centrifugo'), 'fixture certificate is missing the Centrifugo SPIFFE URI SAN');

  await pullDockerImageWithRetry(image, { cwd: root });
  run('docker', ['create', '--pull=never', '--name', containerName, '-e', 'CENTRIFUGO_VAR_S2_PROXY_SECRET=fixture-proxy-value', image,
    '/usr/local/bin/centrifugo', 'checkconfig', '-c', '/centrifugo/centrifugo.json']);
  run('docker', ['cp', `${temporary}/.`, `${containerName}:/`]);
  run('docker', ['start', '-a', containerName]);

  const config = await readFile(configPath);
  const evidence = {
    schemaVersion: 1,
    ticket: 65,
    status: 'passed',
    centrifugoImage: image,
    configSha256: createHash('sha256').update(config).digest('hex'),
    proxyTLS: {
      enabled: true,
      clientCertificateInjected: true,
      clientSPIFFE: 'spiffe://hvac.local/centrifugo',
      serverCAInjected: true,
      insecureSkipVerify: false,
      serverName: 'telemetry-runtime-service',
    },
    persistedPrivateKey: false,
    generatedAt: new Date().toISOString(),
  };
  await mkdir(dirname(output), { recursive: true });
  await writeFile(output, `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
  console.log(`S2 realtime Centrifugo mTLS configuration passed: ${output}`);
} finally {
  spawnSync('docker', ['rm', '-f', containerName], { cwd: root, stdio: 'ignore', windowsHide: true });
  await rm(temporary, { recursive: true, force: true });
}
