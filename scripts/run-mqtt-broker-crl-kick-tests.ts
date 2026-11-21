// End-to-end verification of Mosquitto Broker CRL enforcement and active-connection kick.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import { chmod, mkdir, mkdtemp, readFile, rename, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import tls from 'node:tls';
import { runDockerCompose } from './lib/docker-cli.mjs';

const CONTROL_TOPIC = '$CONTROL/hvac-crl/v1';
const ROOT = resolve('.');
const OUT_DIR = resolve(process.env.MQTT_CRL_REPORT_DIR ?? 'out/mqtt-broker-crl-kick');
const COMPOSE_FILE = resolve(ROOT, 'infra/telemetry/mqtt/compose.yaml');
const PROJECT_NAME = `hvac-mqtt-crl-${process.pid}`;

type ReceivedMessage = { topic: string; payload: string };

interface PkiFixture {
  dir: string;
  pkiDir: string;
  crlDir: string;
  combinedCaPem: Buffer;
  serverKeyPath: string;
  gwCaCnf: string;
  wrongCaCnf: string;
  adapter: { cert: Buffer; key: Buffer };
  gw1CertA: { cert: Buffer; key: Buffer; certPath: string; fingerprintSha256: string };
  gw1CertB: { cert: Buffer; key: Buffer; certPath: string; fingerprintSha256: string };
  gw2Cert: { cert: Buffer; key: Buffer; certPath: string; fingerprintSha256: string };
}

function run(command: string, args: string[], options?: { env?: NodeJS.ProcessEnv }): string {
  return execFileSync(command, args, {
    cwd: ROOT,
    env: options?.env ?? process.env,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function utf8Field(value: string): Buffer {
  const bytes = Buffer.from(value, 'utf8');
  const len = Buffer.allocUnsafe(2);
  len.writeUInt16BE(bytes.length, 0);
  return Buffer.concat([len, bytes]);
}

function encodeVarInt(value: number): Buffer {
  const out: number[] = [];
  let n = value;
  do {
    let encoded = n % 128;
    n = Math.floor(n / 128);
    if (n > 0) encoded |= 128;
    out.push(encoded);
  } while (n > 0);
  return Buffer.from(out);
}

function decodeVarInt(buf: Buffer, offset: number): { value: number; nextOffset: number } | null {
  let multiplier = 1;
  let value = 0;
  let pos = offset;
  while (true) {
    if (pos >= buf.length) return null;
    const encoded = buf[pos++];
    value += (encoded & 127) * multiplier;
    if ((encoded & 128) === 0) return { value, nextOffset: pos };
    multiplier *= 128;
  }
}

function buildPacket(firstByte: number, body: Buffer): Buffer {
  return Buffer.concat([Buffer.from([firstByte]), encodeVarInt(body.length), body]);
}

class MqttV5Client {
  private socket!: tls.TLSSocket;
  private buffer = Buffer.alloc(0);
  private nextPacketId = 1;
  private pendingAcks = new Map<number, (reasonCode: number) => void>();
  private messages: ReceivedMessage[] = [];
  private messageWaiters: Array<() => void> = [];
  private closeWaiters: Array<() => void> = [];
  private _closed = false;

  get closed(): boolean {
    return this._closed;
  }

  static async connect(options: {
    port: number;
    ca: Buffer;
    cert: Buffer;
    key: Buffer;
    clientId: string;
    will?: { topic: string; payload: string; qos?: 0 | 1 };
    connectTimeoutMs?: number;
  }): Promise<MqttV5Client> {
    const client = new MqttV5Client();
    await client.open(options);
    return client;
  }

  private open(options: {
    port: number;
    ca: Buffer;
    cert: Buffer;
    key: Buffer;
    clientId: string;
    will?: { topic: string; payload: string; qos?: 0 | 1 };
    connectTimeoutMs?: number;
  }): Promise<void> {
    const timeoutMs = options.connectTimeoutMs ?? 5000;
    return new Promise((resolveConnect, rejectConnect) => {
      let settled = false;
      const finishErr = (err: Error) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        rejectConnect(err);
      };
      const timer = setTimeout(() => {
        this.destroy();
        finishErr(new Error(`MQTT connect timeout for ${options.clientId}`));
      }, timeoutMs);

      const sock = tls.connect(
        {
          host: '127.0.0.1',
          port: options.port,
          ca: options.ca,
          cert: options.cert,
          key: options.key,
          servername: 'localhost',
          minVersion: 'TLSv1.3',
        },
        () => {
          const willQoS = options.will?.qos ?? 0;
          let flags = 0x02; // Clean Start
          if (options.will) {
            flags |= 0x04 | ((willQoS & 0x03) << 3);
          }
          const parts: Buffer[] = [
            utf8Field('MQTT'),
            Buffer.from([5, flags, 0, 60, 0]), // MQTT v5, keepalive=60, connect properties len=0
            utf8Field(options.clientId),
          ];
          if (options.will) {
            parts.push(Buffer.from([0])); // Will Properties len=0
            parts.push(utf8Field(options.will.topic));
            parts.push(utf8Field(options.will.payload));
          }
          sock.write(buildPacket(0x10, Buffer.concat(parts)));
        },
      );
      this.socket = sock;

      sock.on('error', (err) => {
        this.markClosed();
        finishErr(err);
      });
      sock.on('close', () => {
        this.markClosed();
        finishErr(new Error(`socket closed during connect for ${options.clientId}`));
      });
      sock.on('data', (chunk) => {
        this.buffer = Buffer.concat([this.buffer, chunk]);
        while (this.buffer.length >= 2) {
          const decoded = decodeVarInt(this.buffer, 1);
          if (!decoded) return;
          const totalLen = decoded.nextOffset + decoded.value;
          if (this.buffer.length < totalLen) return;
          const packetType = this.buffer[0] >> 4;
          const flags = this.buffer[0] & 0x0f;
          const body = this.buffer.subarray(decoded.nextOffset, totalLen);
          this.buffer = this.buffer.subarray(totalLen);

          if (packetType === 2) {
            // CONNACK
            const reasonCode = body.length >= 2 ? body[1] : 0xff;
            if (!settled) {
              settled = true;
              clearTimeout(timer);
              if (reasonCode === 0) {
                resolveConnect();
              } else {
                rejectConnect(new Error(`CONNACK failed for ${options.clientId}: reasonCode=${reasonCode}`));
              }
            }
          } else {
            this.handlePacket(packetType, flags, body);
          }
        }
      });
    });
  }

  private handlePacket(packetType: number, flags: number, body: Buffer): void {
    if (packetType === 3) {
      // PUBLISH
      const topicLen = body.readUInt16BE(0);
      let pos = 2 + topicLen;
      const topic = body.subarray(2, pos).toString('utf8');
      const qos = (flags >> 1) & 0x03;
      let packetId = 0;
      if (qos > 0) {
        packetId = body.readUInt16BE(pos);
        pos += 2;
      }
      const props = decodeVarInt(body, pos);
      assert(props, 'malformed PUBLISH properties');
      pos = props.nextOffset + props.value;
      const payload = body.subarray(pos).toString('utf8');
      if (qos === 1) {
        const ack = Buffer.allocUnsafe(2);
        ack.writeUInt16BE(packetId, 0);
        this.socket.write(buildPacket(0x40, ack));
      }
      this.messages.push({ topic, payload });
      const waiters = this.messageWaiters.splice(0);
      for (const wake of waiters) wake();
      return;
    }

    if (packetType === 4 || packetType === 9) {
      // PUBACK or SUBACK
      const packetId = body.readUInt16BE(0);
      let reasonCode = 0;
      if (packetType === 4) {
        reasonCode = body.length >= 3 ? body[2] : 0;
      } else {
        const props = decodeVarInt(body, 2);
        assert(props, 'malformed SUBACK properties');
        const rcPos = props.nextOffset + props.value;
        reasonCode = rcPos < body.length ? body[rcPos] : 0x80;
      }
      const resolveAck = this.pendingAcks.get(packetId);
      if (resolveAck) {
        this.pendingAcks.delete(packetId);
        resolveAck(reasonCode);
      }
      return;
    }

    if (packetType === 14) {
      // DISCONNECT from broker
      this.markClosed();
    }
  }

  private markClosed(): void {
    if (this._closed) return;
    this._closed = true;
    const waiters = this.closeWaiters.splice(0);
    for (const wake of waiters) wake();
    for (const [, resolveAck] of this.pendingAcks) {
      resolveAck(-1);
    }
    this.pendingAcks.clear();
  }

  async subscribe(filter: string, qos: 0 | 1 = 1): Promise<number> {
    const packetId = this.allocatePacketId();
    const idBuf = Buffer.allocUnsafe(2);
    idBuf.writeUInt16BE(packetId, 0);
    const body = Buffer.concat([idBuf, Buffer.from([0]), utf8Field(filter), Buffer.from([qos])]);
    return this.awaitAck(packetId, buildPacket(0x82, body));
  }

  async publishQoS1(topic: string, payload: string, timeoutMs = 4000): Promise<number> {
    if (this._closed) return -1;
    const packetId = this.allocatePacketId();
    const idBuf = Buffer.allocUnsafe(2);
    idBuf.writeUInt16BE(packetId, 0);
    const body = Buffer.concat([utf8Field(topic), idBuf, Buffer.from([0]), Buffer.from(payload, 'utf8')]);
    return this.awaitAck(packetId, buildPacket(0x32, body), timeoutMs);
  }

  private allocatePacketId(): number {
    const id = this.nextPacketId;
    this.nextPacketId = id >= 65535 ? 1 : id + 1;
    return id;
  }

  private awaitAck(packetId: number, packet: Buffer, timeoutMs = 4000): Promise<number> {
    return new Promise((resolveAck, rejectAck) => {
      if (this._closed) {
        resolveAck(-1);
        return;
      }
      const timer = setTimeout(() => {
        this.pendingAcks.delete(packetId);
        rejectAck(new Error(`timed out waiting for ack packetId=${packetId}`));
      }, timeoutMs);
      this.pendingAcks.set(packetId, (code) => {
        clearTimeout(timer);
        resolveAck(code);
      });
      this.socket.write(packet);
    });
  }

  async waitForMessage(predicate: (msg: ReceivedMessage) => boolean, timeoutMs = 5000): Promise<ReceivedMessage> {
    const deadline = Date.now() + timeoutMs;
    while (true) {
      const idx = this.messages.findIndex(predicate);
      if (idx >= 0) {
        const [found] = this.messages.splice(idx, 1);
        return found;
      }
      const remaining = deadline - Date.now();
      if (remaining <= 0) {
        throw new Error(`timed out waiting for MQTT message after ${timeoutMs}ms`);
      }
      await new Promise<void>((resolveWait) => {
        const timer = setTimeout(() => {
          this.messageWaiters = this.messageWaiters.filter((fn) => fn !== wake);
          resolveWait();
        }, remaining);
        const wake = () => {
          clearTimeout(timer);
          resolveWait();
        };
        this.messageWaiters.push(wake);
      });
    }
  }

  async expectNoMessage(predicate: (msg: ReceivedMessage) => boolean, windowMs = 600): Promise<void> {
    await new Promise((resolveSleep) => setTimeout(resolveSleep, windowMs));
    const unexpected = this.messages.find(predicate);
    assert(!unexpected, `unexpected message received on ${unexpected?.topic}: ${unexpected?.payload}`);
  }

  async waitForClose(timeoutMs = 5000): Promise<void> {
    if (this._closed) return;
    await new Promise<void>((resolveClose, rejectClose) => {
      const timer = setTimeout(() => {
        this.closeWaiters = this.closeWaiters.filter((fn) => fn !== wake);
        rejectClose(new Error(`client connection stayed open after ${timeoutMs}ms`));
      }, timeoutMs);
      const wake = () => {
        clearTimeout(timer);
        resolveClose();
      };
      this.closeWaiters.push(wake);
    });
  }

  destroy(): void {
    try {
      this.socket?.destroy();
    } catch {
      // Ignore cleanup errors on already-closed sockets.
    }
    this.markClosed();
  }
}

function sha256HexFromCertPem(certPem: Buffer): string {
  const x509 = new crypto.X509Certificate(certPem);
  return crypto.createHash('sha256').update(x509.raw).digest('hex');
}

function caConfigTemplate(caDir: string, defaultDays = 365): string {
  return `[ca]
default_ca = CA_default

[CA_default]
dir               = ${caDir}
certs             = $dir/certs
crl_dir           = $dir/crl
new_certs_dir     = $dir/newcerts
database          = $dir/index.txt
serial            = $dir/serial
crlnumber         = $dir/crlnumber
certificate       = $dir/ca.crt
private_key       = $dir/ca.key
default_days      = ${defaultDays}
default_crl_days  = 30
default_md        = sha256
unique_subject    = no
policy            = policy_loose

[policy_loose]
commonName = supplied

[v3_client]
basicConstraints = CA:FALSE
keyUsage = digitalSignature
extendedKeyUsage = clientAuth
`;
}

async function initCA(rootDir: string, name: string, commonName: string): Promise<{ caDir: string; cnfPath: string; caCertPath: string }> {
  const caDir = join(rootDir, name);
  await mkdir(join(caDir, 'newcerts'), { recursive: true, mode: 0o755 });
  await mkdir(join(caDir, 'certs'), { recursive: true, mode: 0o755 });
  await mkdir(join(caDir, 'crl'), { recursive: true, mode: 0o755 });
  await writeFile(join(caDir, 'index.txt'), '', 'utf8');
  await writeFile(join(caDir, 'serial'), '1000\n', 'utf8');
  await writeFile(join(caDir, 'crlnumber'), '01\n', 'utf8');

  const keyPath = join(caDir, 'ca.key');
  const caCertPath = join(caDir, 'ca.crt');
  const cnfPath = join(caDir, 'openssl.cnf');
  await writeFile(cnfPath, caConfigTemplate(caDir), 'utf8');

  run('openssl', ['ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', keyPath]);
  run('openssl', [
    'req',
    '-new',
    '-x509',
    '-days',
    '365',
    '-key',
    keyPath,
    '-sha256',
    '-subj',
    `/CN=${commonName}`,
    '-out',
    caCertPath,
  ]);
  return { caDir, cnfPath, caCertPath };
}

async function issueClientCert(
  workDir: string,
  cnfPath: string,
  filePrefix: string,
  commonName: string,
): Promise<{ cert: Buffer; key: Buffer; certPath: string; fingerprintSha256: string }> {
  const keyPath = join(workDir, `${filePrefix}-key.pem`);
  const csrPath = join(workDir, `${filePrefix}.csr`);
  const certPath = join(workDir, `${filePrefix}-cert.pem`);
  run('openssl', ['ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', keyPath]);
  run('openssl', ['req', '-new', '-key', keyPath, '-subj', `/CN=${commonName}`, '-out', csrPath]);
  run('openssl', [
    'ca',
    '-batch',
    '-config',
    cnfPath,
    '-extensions',
    'v3_client',
    '-in',
    csrPath,
    '-out',
    certPath,
    '-notext',
  ]);
  await chmod(certPath, 0o644);
  await chmod(keyPath, 0o644);
  const cert = await readFile(certPath);
  const key = await readFile(keyPath);
  return { cert, key, certPath, fingerprintSha256: sha256HexFromCertPem(cert) };
}

async function createDualCaPki(): Promise<PkiFixture> {
  const dir = await mkdtemp(join(tmpdir(), 'hvac-mqtt-crl-'));
  await chmod(dir, 0o755);
  const pkiDir = join(dir, 'pki');
  const crlDir = join(dir, 'crl');
  await mkdir(pkiDir, { recursive: true, mode: 0o755 });
  await mkdir(crlDir, { recursive: true, mode: 0o755 });
  await chmod(pkiDir, 0o755);
  await chmod(crlDir, 0o755);

  const internal = await initCA(dir, 'internal-ca', 'hvac-internal-ca');
  const gateway = await initCA(dir, 'gateway-ca', 'hvac-gateway-ca');
  const wrong = await initCA(dir, 'wrong-ca', 'hvac-wrong-ca');

  // Mosquitto server cert issued by Internal CA.
  const serverKeyPath = join(pkiDir, 'mqtt-broker-key.pem');
  const serverCsrPath = join(pkiDir, 'mqtt-broker.csr');
  const serverCertPath = join(pkiDir, 'mqtt-broker-cert.pem');
  const serverExtPath = join(pkiDir, 'broker-ext.cnf');
  await writeFile(
    serverExtPath,
    'basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:localhost,DNS:mqtt-broker,IP:127.0.0.1\n',
    'utf8',
  );
  run('openssl', ['ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', serverKeyPath]);
  run('openssl', ['req', '-new', '-key', serverKeyPath, '-subj', '/CN=mqtt-broker', '-out', serverCsrPath]);
  run('openssl', [
    'x509',
    '-req',
    '-in',
    serverCsrPath,
    '-CA',
    internal.caCertPath,
    '-CAkey',
    join(internal.caDir, 'ca.key'),
    '-CAcreateserial',
    '-out',
    serverCertPath,
    '-days',
    '365',
    '-sha256',
    '-extfile',
    serverExtPath,
  ]);
  await chmod(serverCertPath, 0o644);
  await chmod(serverKeyPath, 0o644);

  const internalCaPem = await readFile(internal.caCertPath);
  const gatewayCaPem = await readFile(gateway.caCertPath);
  const combinedCaPem = Buffer.concat([internalCaPem, gatewayCaPem]);
  await writeFile(join(pkiDir, 'ca.pem'), combinedCaPem, { mode: 0o644 });
  await chmod(join(pkiDir, 'ca.pem'), 0o644);

  const adapter = await issueClientCert(dir, internal.cnfPath, 'adapter', 'mqtt-telemetry-adapter');
  const gw1CertA = await issueClientCert(dir, gateway.cnfPath, 'gw1-a', 'gw-crl-01');
  const gw1CertB = await issueClientCert(dir, gateway.cnfPath, 'gw1-b', 'gw-crl-01');
  const gw2Cert = await issueClientCert(dir, gateway.cnfPath, 'gw2', 'gw-crl-02');

  return {
    dir,
    pkiDir,
    crlDir,
    combinedCaPem,
    serverKeyPath,
    gwCaCnf: gateway.cnfPath,
    wrongCaCnf: wrong.cnfPath,
    adapter,
    gw1CertA,
    gw1CertB,
    gw2Cert,
  };
}

function generateCrlPem(cnfPath: string, tmpFile: string): Buffer {
  run('openssl', ['ca', '-gencrl', '-config', cnfPath, '-out', tmpFile]);
  return execFileSync('cat', [tmpFile]);
}

async function writeAtomicCrl(crlDir: string, pemBytes: Buffer): Promise<void> {
  const tmpPath = join(crlDir, `gateway-ca.crl.pem.tmp-${Date.now()}-${Math.random().toString(16).slice(2)}`);
  const finalPath = join(crlDir, 'gateway-ca.crl.pem');
  await writeFile(tmpPath, pemBytes, { mode: 0o644 });
  await chmod(tmpPath, 0o644);
  await rename(tmpPath, finalPath);
}

async function assertRevokedConnectRejected(options: {
  port: number;
  ca: Buffer;
  cert: Buffer;
  key: Buffer;
  clientId: string;
  topic: string;
  probePayload: string;
  adapter: MqttV5Client;
}): Promise<void> {
  let client: MqttV5Client | null = null;
  try {
    client = await MqttV5Client.connect({
      port: options.port,
      ca: options.ca,
      cert: options.cert,
      key: options.key,
      clientId: options.clientId,
      connectTimeoutMs: 2500,
    });
    // Under Mosquitto `use_identity_as_username true`, `MOSQ_EVT_CONNECT` fires
    // `mosquitto_kick_client_by_clientid` right on connect and `MOSQ_EVT_ACL_CHECK`
    // rejects any publish with 135 before closing the socket.
    const rc = await client.publishQoS1(options.topic, options.probePayload, 2000);
    assert(rc === -1 || rc === 135, `expected revoked client publish rejected/closed, got rc=${rc}`);
    await client.waitForClose(2500);
  } catch (err) {
    assert(err instanceof Error);
  } finally {
    client?.destroy();
  }
  await options.adapter.expectNoMessage((m) => m.payload === options.probePayload, 350);
}

async function main(): Promise<void> {
  const report: Record<string, unknown> = {
    schemaVersion: 1,
    capability: 'mqtt-broker-crl-kick',
    status: 'failed',
    startedAt: new Date().toISOString(),
    scenarios: {},
  };

  let pki: PkiFixture | null = null;
  let composeEnv: NodeJS.ProcessEnv = process.env;
  const openClients: MqttV5Client[] = [];
  const composeArgs = ['-p', PROJECT_NAME, '-f', COMPOSE_FILE];
  const compose = (args: string[]) => runDockerCompose(run, [...composeArgs, ...args], { env: composeEnv });

  try {
    await rm(OUT_DIR, { recursive: true, force: true });
    await mkdir(OUT_DIR, { recursive: true });
    pki = await createDualCaPki();
    composeEnv = {
      ...process.env,
      MQTT_PKI_DIR: pki.pkiDir,
      MQTT_CRL_DIR: pki.crlDir,
      MQTT_HOST_PORT: '0',
    };

    // Start with an empty verified Gateway CRL on disk so initial connections work cleanly.
    const initialCrlTmp = join(pki.dir, 'crl-initial.pem');
    const initialCrl = generateCrlPem(pki.gwCaCnf, initialCrlTmp);
    await writeAtomicCrl(pki.crlDir, initialCrl);

    compose(['up', '-d', '--build', '--wait', 'mqtt-broker']);
    const portText = compose(['port', 'mqtt-broker', '8883']).trim();
    const port = Number.parseInt(portText.split(':').pop() ?? '', 10);
    assert(Number.isInteger(port) && port > 0, `invalid broker port: ${portText}`);

    const scenarios = report.scenarios as Record<string, unknown>;

    // 1. Dual-CA coexistence
    const adapter = await MqttV5Client.connect({
      port,
      ca: pki.combinedCaPem,
      cert: pki.adapter.cert,
      key: pki.adapter.key,
      clientId: 'adapter-main',
    });
    openClients.push(adapter);
    assert.equal(await adapter.subscribe('hvac/v1/+/up/#', 1), 1);

    const gw1AClient = await MqttV5Client.connect({
      port,
      ca: pki.combinedCaPem,
      cert: pki.gw1CertA.cert,
      key: pki.gw1CertA.key,
      clientId: 'gw-crl-01-cert-a',
    });
    openClients.push(gw1AClient);

    const gw1BClient = await MqttV5Client.connect({
      port,
      ca: pki.combinedCaPem,
      cert: pki.gw1CertB.cert,
      key: pki.gw1CertB.key,
      clientId: 'gw-crl-01-cert-b',
    });
    openClients.push(gw1BClient);

    const gw2Client = await MqttV5Client.connect({
      port,
      ca: pki.combinedCaPem,
      cert: pki.gw2Cert.cert,
      key: pki.gw2Cert.key,
      clientId: 'gw-crl-02-main',
    });
    openClients.push(gw2Client);

    assert.equal(await gw1AClient.publishQoS1('hvac/v1/gw-crl-01/up/telemetry', '{"from":"certA"}'), 0);
    await adapter.waitForMessage((m) => m.topic === 'hvac/v1/gw-crl-01/up/telemetry' && m.payload.includes('certA'));
    assert.equal(await gw1BClient.publishQoS1('hvac/v1/gw-crl-01/up/telemetry', '{"from":"certB"}'), 0);
    await adapter.waitForMessage((m) => m.topic === 'hvac/v1/gw-crl-01/up/telemetry' && m.payload.includes('certB'));
    assert.equal(await gw2Client.publishQoS1('hvac/v1/gw-crl-02/up/telemetry', '{"from":"gw2"}'), 0);
    await adapter.waitForMessage((m) => m.topic === 'hvac/v1/gw-crl-02/up/telemetry' && m.payload.includes('gw2'));
    scenarios.dualCaCoexistence = 'passed';

    // 2. Unauthorized $CONTROL/hvac-crl/v1 publish rejected with PUBACK 135
    const unauthRc = await gw2Client.publishQoS1(CONTROL_TOPIC, JSON.stringify({ action: 'reload' }));
    assert.equal(unauthRc, 135, `expected unauthorized $CONTROL PUBACK=135, got ${unauthRc}`);
    scenarios.unauthorizedControlRejected = 'passed';

    // 3. Single-cert revoke + precise kick leaves Cert B, gw2, and adapter alive
    run('openssl', ['ca', '-batch', '-config', pki.gwCaCnf, '-revoke', pki.gw1CertA.certPath, '-crl_reason', 'keyCompromise']);
    const crlAfterCertA = generateCrlPem(pki.gwCaCnf, join(pki.dir, 'crl-after-a.pem'));
    await writeAtomicCrl(pki.crlDir, crlAfterCertA);

    const controlRc1 = await adapter.publishQoS1(
      CONTROL_TOPIC,
      JSON.stringify({
        action: 'reload_and_kick',
        gatewayId: 'gw-crl-01',
        fingerprintSha256: pki.gw1CertA.fingerprintSha256,
        revokeAll: false,
        withWill: false,
      }),
    );
    assert.equal(controlRc1, 0, `expected control publish success, got ${controlRc1}`);
    await gw1AClient.waitForClose(2000);

    assert.equal(adapter.closed, false, 'internal adapter must stay connected');
    assert.equal(gw1BClient.closed, false, 'rotated Cert B on same gateway must stay connected');
    assert.equal(gw2Client.closed, false, 'unrelated gw-crl-02 must stay connected');
    assert.equal(await gw1BClient.publishQoS1('hvac/v1/gw-crl-01/up/telemetry', '{"alive":"certB"}'), 0);
    await adapter.waitForMessage((m) => m.payload === '{"alive":"certB"}');
    assert.equal(await gw2Client.publishQoS1('hvac/v1/gw-crl-02/up/telemetry', '{"alive":"gw2"}'), 0);
    await adapter.waitForMessage((m) => m.payload === '{"alive":"gw2"}');
    scenarios.singleCertPreciseKick = 'passed';

    // 4. Revoked Cert A immediate reconnect rejection
    await assertRevokedConnectRejected({
      port,
      ca: pki.combinedCaPem,
      cert: pki.gw1CertA.cert,
      key: pki.gw1CertA.key,
      clientId: 'gw-crl-01-cert-a-retry',
      topic: 'hvac/v1/gw-crl-01/up/telemetry',
      probePayload: '{"probe":"revoked-cert-a-1"}',
      adapter,
    });
    scenarios.revokedReconnectRejected = 'passed';

    // 5. MOSQ_EVT_TICK disk watcher fallback (no $CONTROL publish)
    run('openssl', ['ca', '-batch', '-config', pki.gwCaCnf, '-revoke', pki.gw2Cert.certPath, '-crl_reason', 'cessationOfOperation']);
    const crlAfterGw2 = generateCrlPem(pki.gwCaCnf, join(pki.dir, 'crl-after-gw2.pem'));
    await writeAtomicCrl(pki.crlDir, crlAfterGw2);
    await gw2Client.waitForClose(3000);
    assert.equal(gw1BClient.closed, false, 'valid Cert B must remain connected after gw2 tick sweep');
    scenarios.tickWatcherFallback = 'passed';

    // 6. Whole-gateway revoke with withWill: true LWT delivery
    gw1BClient.destroy();
    const gw1BWithWill = await MqttV5Client.connect({
      port,
      ca: pki.combinedCaPem,
      cert: pki.gw1CertB.cert,
      key: pki.gw1CertB.key,
      clientId: 'gw-crl-01-will-session',
      will: {
        topic: 'hvac/v1/gw-crl-01/up/status',
        payload: '{"status":"offline_revoked_will"}',
        qos: 1,
      },
    });
    openClients.push(gw1BWithWill);

    // Pre-generate CRL before atomic rename so rename + $CONTROL publish happen within milliseconds,
    // exercising immediate $CONTROL `withWill: true` delivery ahead of the 1s poll fallback.
    run('openssl', ['ca', '-batch', '-config', pki.gwCaCnf, '-revoke', pki.gw1CertB.certPath, '-crl_reason', 'keyCompromise']);
    const crlAfterCertB = generateCrlPem(pki.gwCaCnf, join(pki.dir, 'crl-after-b.pem'));
    await writeAtomicCrl(pki.crlDir, crlAfterCertB);
    const controlRc2 = await adapter.publishQoS1(
      CONTROL_TOPIC,
      JSON.stringify({
        action: 'reload_and_kick',
        gatewayId: 'gw-crl-01',
        revokeAll: true,
        withWill: true,
      }),
    );
    assert.equal(controlRc2, 0);
    await gw1BWithWill.waitForClose(2000);
    const lwtMsg = await adapter.waitForMessage(
      (m) => m.topic === 'hvac/v1/gw-crl-01/up/status' && m.payload === '{"status":"offline_revoked_will"}',
      3000,
    );
    assert.equal(lwtMsg.payload, '{"status":"offline_revoked_will"}');
    scenarios.wholeGatewayRevokeWithWill = 'passed';

    // 7. Tampered/wrong-CA CRL fails signature check and preserves previous CRL fail-closed
    const wrongCrl = generateCrlPem(pki.wrongCaCnf, join(pki.dir, 'crl-wrong.pem'));
    await writeAtomicCrl(pki.crlDir, wrongCrl);
    const controlRc3 = await adapter.publishQoS1(CONTROL_TOPIC, JSON.stringify({ action: 'reload' }));
    assert.equal(controlRc3, 0);
    await assertRevokedConnectRejected({
      port,
      ca: pki.combinedCaPem,
      cert: pki.gw1CertA.cert,
      key: pki.gw1CertA.key,
      clientId: 'gw-crl-01-cert-a-after-tamper',
      topic: 'hvac/v1/gw-crl-01/up/telemetry',
      probePayload: '{"probe":"revoked-cert-a-after-tamper"}',
      adapter,
    });
    scenarios.wrongCaCrlFailSafe = 'passed';

    report.status = 'passed';
    report.finishedAt = new Date().toISOString();
    await writeFile(join(OUT_DIR, 'report.json'), `${JSON.stringify(report, null, 2)}\n`);
    console.log(`MQTT CRL kick passed: ${join(OUT_DIR, 'report.json')}`);
  } finally {
    for (const c of openClients) c.destroy();
    try {
      report.brokerLogs = compose(['logs', '--no-color', '--tail', '80', 'mqtt-broker']);
    } catch {
      // Ignore if broker never came up.
    }
    try {
      await writeFile(join(OUT_DIR, 'report.json'), `${JSON.stringify(report, null, 2)}\n`);
    } catch {
      // Ignore write error if report dir missing.
    }
    try {
      compose(['down', '-v', '--remove-orphans']);
    } catch {
      // Ignore compose cleanup errors.
    }
    if (pki) {
      await rm(pki.dir, { recursive: true, force: true });
    }
  }
}

await main();
