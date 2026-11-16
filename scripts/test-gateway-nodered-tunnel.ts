import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import {
  createPrivateKey,
  createPublicKey,
  generateKeyPairSync,
  randomBytes,
  sign,
  X509Certificate,
} from 'node:crypto';
import * as fs from 'node:fs';
import * as http from 'node:http';
import * as net from 'node:net';
import * as os from 'node:os';
import * as path from 'node:path';
import type { Duplex } from 'node:stream';
import { after, test } from 'node:test';
import * as tls from 'node:tls';
import { pathToFileURL } from 'node:url';
import {
  ALLOWED_TARGET_PORT,
  decodeFramesFromBuffer,
  encodeFrame,
  FrameType,
  GatewayTunnelClient,
  MAX_FRAME_PAYLOAD,
  ProtectedFlowTabError,
  TUNNEL_PROTOCOL_NAME,
  validateProtectedFlowTabs,
  buildGatewayTunnelFlow,
  type TunnelFrame,
} from '../edge/node-red/gateway-nodered-tunnel.ts';
import {
  buildGatewayCertAutorenewFlow,
  buildPkcs10CsrPem,
  checkAndRenewGatewayCertificate,
  shouldRenewCertificate,
} from '../edge/node-red/gateway-cert-autorenew.ts';
import {
  buildRemoteModulePayload,
  syncSongyaxuanEnvelopeViaApi,
} from '../scripts/sync-songyaxuan-nodered.ts';

function derLen(len: number): Buffer {
  if (len < 0x80) return Buffer.from([len]);
  const bytes: number[] = [];
  let n = len;
  while (n > 0) {
    bytes.unshift(n & 0xff);
    n >>= 8;
  }
  return Buffer.from([0x80 | bytes.length, ...bytes]);
}

function derTlv(tag: number, content: Buffer): Buffer {
  return Buffer.concat([Buffer.from([tag]), derLen(content.length), content]);
}

function derIntPositive(bytes: Buffer): Buffer {
  let i = 0;
  while (i < bytes.length - 1 && bytes[i] === 0 && ((bytes[i + 1] ?? 0) & 0x80) === 0) {
    i += 1;
  }
  const trimmed = bytes.subarray(i);
  const padded = (trimmed[0] ?? 0) & 0x80 ? Buffer.concat([Buffer.from([0x00]), trimmed]) : trimmed;
  return derTlv(0x02, padded);
}

function derUtcTime(d: Date): Buffer {
  const pad = (n: number) => String(n).padStart(2, '0');
  const yy = String(d.getUTCFullYear()).slice(-2);
  const text = `${yy}${pad(d.getUTCMonth() + 1)}${pad(d.getUTCDate())}${pad(d.getUTCHours())}${pad(d.getUTCMinutes())}${pad(d.getUTCSeconds())}Z`;
  return derTlv(0x17, Buffer.from(text, 'ascii'));
}

function derNameCN(cn: string): Buffer {
  const oid = Buffer.from([0x06, 0x03, 0x55, 0x04, 0x03]);
  const val = derTlv(0x0c, Buffer.from(cn, 'utf8'));
  return derTlv(0x30, derTlv(0x31, derTlv(0x30, Buffer.concat([oid, val]))));
}

function derExt(oidBytes: number[], critical: boolean, valueDer: Buffer): Buffer {
  const oid = derTlv(0x06, Buffer.from(oidBytes));
  const parts = critical ? [oid, derTlv(0x01, Buffer.from([0xff])), derTlv(0x04, valueDer)] : [oid, derTlv(0x04, valueDer)];
  return derTlv(0x30, Buffer.concat(parts));
}

function issueSignedX509Cert(opts: {
  subjectCN: string;
  issuerCN: string;
  publicKeyPem: string;
  signerKeyPem: string;
  notBefore: Date;
  notAfter: Date;
  isCA?: boolean;
  sanLocalhost?: boolean;
  extKeyUsageServer?: boolean;
  extKeyUsageClient?: boolean;
}): string {
  const version = derTlv(0xa0, derTlv(0x02, Buffer.from([0x02]))); // v3
  const serial = derIntPositive(randomBytes(8));
  // ecdsa-with-SHA256 1.2.840.10045.4.3.2
  const sigAlg = derTlv(0x30, Buffer.from([0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x02]));
  const validity = derTlv(0x30, Buffer.concat([derUtcTime(opts.notBefore), derUtcTime(opts.notAfter)]));
  const spki = createPublicKey(opts.publicKeyPem).export({ type: 'spki', format: 'der' });

  const exts: Buffer[] = [];
  // BasicConstraints 2.5.29.19
  exts.push(
    derExt(
      [0x55, 0x1d, 0x13],
      true,
      derTlv(0x30, opts.isCA ? derTlv(0x01, Buffer.from([0xff])) : Buffer.alloc(0)),
    ),
  );
  // KeyUsage 2.5.29.15 (NamedBitList DER requires minimal unused-bit padding: bit 5 = 0x04 has 2 trailing zeros).
  const kuBits = opts.isCA ? Buffer.from([0x02, 0x04]) : Buffer.from([0x07, 0x80]);
  exts.push(derExt([0x55, 0x1d, 0x0f], true, derTlv(0x03, kuBits)));

  if (opts.extKeyUsageServer || opts.extKeyUsageClient) {
    const oids: Buffer[] = [];
    if (opts.extKeyUsageServer) oids.push(Buffer.from([0x06, 0x08, 0x2b, 0x06, 0x01, 0x05, 0x05, 0x07, 0x03, 0x01]));
    if (opts.extKeyUsageClient) oids.push(Buffer.from([0x06, 0x08, 0x2b, 0x06, 0x01, 0x05, 0x05, 0x07, 0x03, 0x02]));
    exts.push(derExt([0x55, 0x1d, 0x25], false, derTlv(0x30, Buffer.concat(oids))));
  }

  if (opts.sanLocalhost) {
    const dnsName = derTlv(0x82, Buffer.from('localhost', 'ascii'));
    const ipBytes = derTlv(0x87, Buffer.from([127, 0, 0, 1]));
    exts.push(derExt([0x55, 0x1d, 0x11], false, derTlv(0x30, Buffer.concat([dnsName, ipBytes]))));
  }

  const extWrapper = derTlv(0xa3, derTlv(0x30, Buffer.concat(exts)));
  const tbs = derTlv(
    0x30,
    Buffer.concat([
      version,
      serial,
      sigAlg,
      derNameCN(opts.issuerCN),
      validity,
      derNameCN(opts.subjectCN),
      spki,
      extWrapper,
    ]),
  );

  const signature = sign('sha256', tbs, createPrivateKey(opts.signerKeyPem));
  const certDer = derTlv(
    0x30,
    Buffer.concat([tbs, sigAlg, derTlv(0x03, Buffer.concat([Buffer.from([0x00]), signature]))]),
  );
  const lines = certDer.toString('base64').match(/.{1,64}/g) ?? [];
  return `-----BEGIN CERTIFICATE-----\n${lines.join('\n')}\n-----END CERTIFICATE-----\n`;
}

function createTestPKI(gatewayId: string, leafValidityDays: number) {
  const caKeys = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const srvKeys = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const gwKeys = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const now = Date.now();
  const caPriv = caKeys.privateKey.export({ type: 'pkcs8', format: 'pem' }) as string;
  const caPub = caKeys.publicKey.export({ type: 'spki', format: 'pem' }) as string;
  const caPem = issueSignedX509Cert({
    subjectCN: 'Test Gateway CA',
    issuerCN: 'Test Gateway CA',
    publicKeyPem: caPub,
    signerKeyPem: caPriv,
    notBefore: new Date(now - 3600_000),
    notAfter: new Date(now + 365 * 86400_000),
    isCA: true,
  });
  const srvPem = issueSignedX509Cert({
    subjectCN: 'localhost',
    issuerCN: 'Test Gateway CA',
    publicKeyPem: srvKeys.publicKey.export({ type: 'spki', format: 'pem' }) as string,
    signerKeyPem: caPriv,
    notBefore: new Date(now - 3600_000),
    notAfter: new Date(now + 90 * 86400_000),
    sanLocalhost: true,
    extKeyUsageServer: true,
  });
  const gwPem = issueSignedX509Cert({
    subjectCN: gatewayId,
    issuerCN: 'Test Gateway CA',
    publicKeyPem: gwKeys.publicKey.export({ type: 'spki', format: 'pem' }) as string,
    signerKeyPem: caPriv,
    notBefore: new Date(now - 3600_000),
    notAfter: new Date(now + leafValidityDays * 86400_000),
    extKeyUsageClient: true,
  });
  return {
    caPem,
    caPriv,
    srvPem,
    srvKey: srvKeys.privateKey.export({ type: 'pkcs8', format: 'pem' }) as string,
    gwPem,
    gwPub: gwKeys.publicKey.export({ type: 'spki', format: 'pem' }) as string,
    gwKey: gwKeys.privateKey.export({ type: 'pkcs8', format: 'pem' }) as string,
  };
}

interface TunnelStreamHandle {
  write: (data: Buffer | string) => void;
  end: () => void;
  close: () => void;
  onData: (fn: (chunk: Buffer) => void) => void;
  readUntilEnd: () => Promise<Buffer>;
}

interface HubHarness {
  port: number;
  openStream: (targetPort: number) => Promise<TunnelStreamHandle>;
  requestRawOpen: (streamId: number, payload: Buffer) => Promise<TunnelFrame>;
  sendPing: () => void;
  getSeenPings: () => number;
  close: () => Promise<void>;
}

async function startNodeUpgradeHub(
  pki: ReturnType<typeof createTestPKI>,
  renewCalls: string[],
  overrideRenew?: () => { certificatePem: string; caPem: string },
): Promise<HubHarness> {
  let activeTunnelSock: tls.TLSSocket | null = null;
  let rxBuf: Buffer = Buffer.alloc(0);
  let nextStreamId = 1;
  let seenPings = 0;

  const allTlsSockets = new Set<tls.TLSSocket>();
  const pendingOpens = new Map<
    number,
    { resolve: (frame: TunnelFrame) => void; reject: (err: Error) => void }
  >();
  const streamHandlers = new Map<
    number,
    { onData: (chunk: Buffer) => void; onEnd: () => void; onReset: () => void }
  >();

  let connectWaiters: Array<() => void> = [];

  const dispatchFrame = (frame: TunnelFrame) => {
    switch (frame.type) {
      case FrameType.OPEN_OK:
      case FrameType.OPEN_ERR: {
        const waiter = pendingOpens.get(frame.streamId);
        if (waiter) {
          pendingOpens.delete(frame.streamId);
          waiter.resolve(frame);
        }
        break;
      }
      case FrameType.DATA: {
        streamHandlers.get(frame.streamId)?.onData(frame.payload);
        break;
      }
      case FrameType.EOF: {
        streamHandlers.get(frame.streamId)?.onEnd();
        break;
      }
      case FrameType.RST: {
        const handler = streamHandlers.get(frame.streamId);
        streamHandlers.delete(frame.streamId);
        handler?.onReset();
        break;
      }
      case FrameType.PING: {
        seenPings += 1;
        activeTunnelSock?.write(encodeFrame({ type: FrameType.PONG, streamId: frame.streamId, payload: frame.payload }));
        break;
      }
      case FrameType.PONG:
        break;
    }
  };

  const tlsSrv = tls.createServer({
    cert: pki.srvPem,
    key: pki.srvKey,
    ca: pki.caPem,
    requestCert: true,
    rejectUnauthorized: true,
    ALPNProtocols: ['http/1.1'],
  });

  // Minimal HTTP/1.1 parser over tls.createServer socket so Upgrade sockets don't hit http.Server timeout guards.
  tlsSrv.on('secureConnection', (sock) => {
    sock.setNoDelay(true);
    allTlsSockets.add(sock);
    sock.once('close', () => {
      allTlsSockets.delete(sock);
    });
    let headBuf: Buffer = Buffer.alloc(0);
    const onInitialData = (chunk: Buffer) => {
      headBuf = Buffer.concat([headBuf, chunk]);
      const marker = headBuf.indexOf('\r\n\r\n');
      if (marker === -1) return;
      const headerText = headBuf.subarray(0, marker).toString('latin1');
      const rest = Buffer.from(headBuf.subarray(marker + 4));
      const [requestLine, ...headerLines] = headerText.split('\r\n');
      const headers = new Map<string, string>();
      for (const line of headerLines) {
        const idx = line.indexOf(':');
        if (idx > 0) headers.set(line.slice(0, idx).trim().toLowerCase(), line.slice(idx + 1).trim());
      }

      if (requestLine?.startsWith(`GET /gateway/v1/tunnel `) && headers.get('upgrade') === TUNNEL_PROTOCOL_NAME) {
        sock.off('data', onInitialData);
        sock.write(
          `HTTP/1.1 101 Switching Protocols\r\nUpgrade: ${TUNNEL_PROTOCOL_NAME}\r\nConnection: Upgrade\r\n\r\n`,
        );
        activeTunnelSock = sock;
        rxBuf = rest;
        sock.on('data', (d: Buffer) => {
          rxBuf = rxBuf.length === 0 ? d : Buffer.concat([rxBuf, d]);
          const decoded = decodeFramesFromBuffer(rxBuf);
          rxBuf = decoded.remainder;
          for (const f of decoded.frames) dispatchFrame(f);
        });
        sock.once('close', () => {
          if (activeTunnelSock === sock) activeTunnelSock = null;
          for (const [, waiter] of pendingOpens) waiter.reject(new Error('tunnel closed'));
          pendingOpens.clear();
          for (const [id, handler] of streamHandlers) {
            streamHandlers.delete(id);
            handler.onReset();
          }
        });
        for (const w of connectWaiters.splice(0)) w();
        return;
      }

      if (requestLine?.startsWith('POST /gateway/v1/renew ')) {
        const contentLen = Number(headers.get('content-length') ?? '0');
        if (rest.length < contentLen) return; // wait more body
        sock.off('data', onInitialData);
        const bodyJson = JSON.parse(rest.subarray(0, contentLen).toString('utf8')) as { csrPem: string };
        renewCalls.push(bodyJson.csrPem);
        const expires = new Date(Date.now() + 90 * 86400_000);
        const override = overrideRenew?.();
        const nextCert =
          override?.certificatePem ??
          issueSignedX509Cert({
            subjectCN: '01a14000-4000-7000-8000-000000000100',
            issuerCN: 'Test Gateway CA',
            publicKeyPem: pki.gwPub,
            signerKeyPem: pki.caPriv,
            notBefore: new Date(Date.now() - 60_000),
            notAfter: expires,
            extKeyUsageClient: true,
          });
        const respBody = JSON.stringify({
          certificatePem: nextCert,
          caPem: override?.caPem ?? pki.caPem,
          brokerUrl: 'tls://mqtt.local:8883',
          expiresAt: expires.toISOString(),
        });
        sock.end(
          `HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${Buffer.byteLength(respBody)}\r\nConnection: close\r\n\r\n${respBody}`,
        );
      }
    };
    sock.on('data', onInitialData);
  });

  await new Promise<void>((resolve) => tlsSrv.listen(0, '127.0.0.1', resolve));
  const addr = tlsSrv.address() as net.AddressInfo;

  const waitConnected = () =>
    activeTunnelSock
      ? Promise.resolve()
      : new Promise<void>((resolve) => connectWaiters.push(resolve));

  return {
    port: addr.port,
    async requestRawOpen(streamId: number, payload: Buffer): Promise<TunnelFrame> {
      await waitConnected();
      return await new Promise<TunnelFrame>((resolve, reject) => {
        pendingOpens.set(streamId, { resolve, reject });
        activeTunnelSock?.write(encodeFrame({ type: FrameType.OPEN, streamId, payload }));
      });
    },
    async openStream(targetPort: number): Promise<TunnelStreamHandle> {
      await waitConnected();
      const streamId = nextStreamId;
      nextStreamId += 2;
      const p = Buffer.allocUnsafe(2);
      p.writeUInt16BE(targetPort, 0);
      const reply = await new Promise<TunnelFrame>((resolve, reject) => {
        pendingOpens.set(streamId, { resolve, reject });
        activeTunnelSock?.write(encodeFrame({ type: FrameType.OPEN, streamId, payload: p }));
      });
      if (reply.type === FrameType.OPEN_ERR) {
        throw new Error(reply.payload.toString('utf8'));
      }

      const chunks: Buffer[] = [];
      let ended = false;
      let dataListener: ((chunk: Buffer) => void) | null = null;
      let endResolve: ((buf: Buffer) => void) | null = null;
      const finish = () => {
        if (!ended) {
          ended = true;
          endResolve?.(Buffer.concat(chunks));
          endResolve = null;
        }
      };

      streamHandlers.set(streamId, {
        onData: (chunk) => {
          if (dataListener) {
            dataListener(chunk);
          } else {
            chunks.push(chunk);
          }
        },
        onEnd: finish,
        onReset: finish,
      });

      return {
        write: (data: Buffer | string) => {
          const buf = Buffer.isBuffer(data) ? data : Buffer.from(data, 'utf8');
          for (let off = 0; off < buf.length; off += MAX_FRAME_PAYLOAD) {
            activeTunnelSock?.write(
              encodeFrame({
                type: FrameType.DATA,
                streamId,
                payload: buf.subarray(off, Math.min(buf.length, off + MAX_FRAME_PAYLOAD)),
              }),
            );
          }
        },
        end: () => {
          activeTunnelSock?.write(encodeFrame({ type: FrameType.EOF, streamId, payload: Buffer.alloc(0) }));
        },
        close: () => {
          streamHandlers.delete(streamId);
          finish();
          activeTunnelSock?.write(encodeFrame({ type: FrameType.RST, streamId, payload: Buffer.alloc(0) }));
        },
        onData: (fn: (chunk: Buffer) => void) => {
          dataListener = fn;
          for (const queued of chunks.splice(0)) {
            fn(queued);
          }
        },
        readUntilEnd: () => {
          if (ended) {
            return Promise.resolve(Buffer.concat(chunks));
          }
          return new Promise<Buffer>((resolve) => {
            endResolve = resolve;
          });
        },
      };
    },
    sendPing: () => {
      activeTunnelSock?.write(encodeFrame({ type: FrameType.PING, streamId: 0, payload: Buffer.alloc(0) }));
    },
    getSeenPings: () => seenPings,
    async close() {
      activeTunnelSock = null;
      for (const s of allTlsSockets) {
        s.destroy();
      }
      allTlsSockets.clear();
      await new Promise<void>((res) => tlsSrv.close(() => res()));
    },
  };
}

function decodeChunkedHttpBody(raw: Buffer): Buffer {
  const parts: Buffer[] = [];
  let pos = 0;
  while (pos < raw.length) {
    const lineEnd = raw.indexOf('\r\n', pos);
    if (lineEnd === -1) break;
    const sizeHex = raw.subarray(pos, lineEnd).toString('ascii').split(';', 1)[0]?.trim() ?? '0';
    const chunkLen = Number.parseInt(sizeHex, 16);
    if (Number.isNaN(chunkLen) || chunkLen === 0) break;
    const start = lineEnd + 2;
    parts.push(raw.subarray(start, start + chunkLen));
    pos = start + chunkLen + 2;
  }
  return Buffer.concat(parts);
}

async function sendHttpOverTunnelStream(
  stream: TunnelStreamHandle,
  rawRequest: string,
): Promise<{ status: number; head: string; body: Buffer }> {
  stream.write(rawRequest);
  stream.end();
  const full = await stream.readUntilEnd();
  stream.close();
  const marker = full.indexOf('\r\n\r\n');
  assert.ok(marker !== -1, `missing HTTP response headers: ${full.toString('utf8')}`);
  const head = full.subarray(0, marker).toString('latin1');
  const status = Number(head.split(/\s+/)[1] ?? 0);
  const rawBody = full.subarray(marker + 4);
  const body = /\r\ntransfer-encoding:\s*chunked/i.test(head) ? decodeChunkedHttpBody(rawBody) : rawBody;
  return { status, head, body };
}

const tempDirs: string[] = [];
after(() => {
  for (const dir of tempDirs) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('tunnel codec rejects oversized frames cleanly', () => {
  const frame = encodeFrame({ type: FrameType.DATA, streamId: 9, payload: Buffer.from('hello-nodered') });
  const { frames, remainder } = decodeFramesFromBuffer(Buffer.concat([frame, frame.subarray(0, 4)]));
  assert.equal(frames.length, 1);
  assert.equal(frames[0]?.streamId, 9);
  assert.equal(frames[0]?.payload.toString('utf8'), 'hello-nodered');
  assert.equal(remainder.length, 4);

  assert.throws(
    () => encodeFrame({ type: FrameType.DATA, streamId: 1, payload: Buffer.alloc(MAX_FRAME_PAYLOAD + 1) }),
    RangeError,
  );
});

test('end-to-end GatewayTunnelClient: HTTP, WebSocket /comms, 1880-only guard, keepalive, mux & <50ms p95 perf', async (t) => {
  const gatewayId = '01a14000-4000-7000-8000-000000000100';
  const pki = createTestPKI(gatewayId, 60);
  const renewCalls: string[] = [];
  const hub = await startNodeUpgradeHub(pki, renewCalls);

  // Mock local Node-RED (1880 equivalent bound ephemarally via targetPort mapping).
  const mockNodeRed = http.createServer((req, res) => {
    if (req.url === '/red/settings') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ httpNodeRoot: '/api', version: '4.0.0' }));
      return;
    }
    const chunks: Buffer[] = [];
    req.on('data', (c: Buffer) => chunks.push(c));
    req.on('end', () => {
      res.writeHead(200, { 'Content-Type': 'application/octet-stream' });
      res.end(Buffer.concat(chunks));
    });
  });
  const upgradeSockets = new Set<Duplex>();
  mockNodeRed.on('connection', (sock) => sock.setNoDelay(true));
  mockNodeRed.on('upgrade', (req, socket) => {
    assert.equal(req.url, '/comms');
    upgradeSockets.add(socket);
    socket.once('close', () => upgradeSockets.delete(socket));
    socket.on('error', () => undefined);
    socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n');
    socket.on('data', (d: Buffer) => socket.write(Buffer.from(`ws-ack:${d.toString('utf8')}`)));
  });
  await new Promise<void>((res) => mockNodeRed.listen(0, '127.0.0.1', res));
  const nodeRedPort = (mockNodeRed.address() as net.AddressInfo).port;

  let dialCalls = 0;
  let connected = false;
  const disconnectErrors: string[] = [];
  const client = new GatewayTunnelClient({
    tunnelUrl: `https://127.0.0.1:${hub.port}/gateway/v1/tunnel`,
    targetHost: '127.0.0.1',
    targetPort: nodeRedPort,
    pingIntervalMs: 40,
    readTimeoutMs: 500,
    dialTarget: (p, host) => {
      dialCalls += 1;
      return net.createConnection({ host, port: p, allowHalfOpen: true, noDelay: true });
    },
    loadCredentials: () => ({ certPem: pki.gwPem, keyPem: pki.gwKey, caPem: pki.caPem }),
    onConnected: () => {
      connected = true;
    },
    onDisconnected: (err) => {
      if (err) disconnectErrors.push(err.message);
    },
  });
  client.start();

  t.after(async () => {
    await client.stop();
    await hub.close();
    for (const sock of upgradeSockets) sock.destroy();
    upgradeSockets.clear();
    mockNodeRed.closeAllConnections?.();
    await new Promise<void>((res) => mockNodeRed.close(() => res()));
  });

  // 1. HTTP GET + POST round-trip
  const s1 = await hub.openStream(ALLOWED_TARGET_PORT);
  const getResp = await sendHttpOverTunnelStream(
    s1,
    'GET /red/settings HTTP/1.1\r\nHost: 127.0.0.1:18800\r\nConnection: close\r\n\r\n',
  );
  assert.equal(getResp.status, 200);
  assert.match(getResp.body.toString('utf8'), /"version":"4\.0\.0"/);
  assert.equal(connected, true);

  // 2. WebSocket /comms bidirectional upgrade stream
  const sWs = await hub.openStream(ALLOWED_TARGET_PORT);
  await new Promise<void>((resolve) => {
    let stage = 0;
    sWs.onData((d: Buffer) => {
      const text = d.toString('utf8');
      if (stage === 0 && text.includes('101 Switching Protocols')) {
        stage = 1;
        sWs.write('deploy-ping');
        return;
      }
      if (stage === 1 && text.includes('ws-ack:deploy-ping')) {
        sWs.close();
        resolve();
      }
    });
    sWs.write('GET /comms HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n');
  });

  // 3. Reject non-1880 ports WITHOUT dialing
  const beforeForbiddenDials = dialCalls;
  for (const forbidden of [22, 80, 1883]) {
    const p = Buffer.allocUnsafe(2);
    p.writeUInt16BE(forbidden, 0);
    const errFrame = await hub.requestRawOpen(900 + forbidden, p);
    assert.equal(errFrame.type, FrameType.OPEN_ERR);
    assert.match(errFrame.payload.toString('utf8'), /forbidden/);
  }
  assert.equal(dialCalls, beforeForbiddenDials, 'edge must not dial socket on non-1880 ports');

  // 4. Keepalive PING/PONG check
  await new Promise((r) => setTimeout(r, 120));
  assert.ok(hub.getSeenPings() >= 1, 'edge must emit periodic PING heartbeats');

  // 5. Perf baseline: 20 concurrent 4 KiB exchanges -> p95 < 50 ms
  const payload4k = randomBytes(4096).toString('hex').slice(0, 4096);
  const latencies = await Promise.all(
    Array.from({ length: 20 }, async () => {
      const t0 = performance.now();
      const st = await hub.openStream(ALLOWED_TARGET_PORT);
      const req =
        `POST /red/echo HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: ${payload4k.length}\r\nConnection: close\r\n\r\n${payload4k}`;
      const res = await sendHttpOverTunnelStream(st, req);
      assert.equal(res.status, 200);
      assert.equal(res.body.toString('utf8'), payload4k);
      return performance.now() - t0;
    }),
  );
  assert.deepEqual(disconnectErrors, []);
  const sorted = [...latencies].sort((a, b) => a - b);
  const p95 = sorted[Math.min(sorted.length - 1, Math.ceil(sorted.length * 0.95) - 1)] ?? 0;
  t.diagnostic(`20-concurrent 4KiB mux tunnel latency p95=${p95.toFixed(2)}ms max=${(sorted.at(-1) ?? 0).toFixed(2)}ms`);
  assert.ok(p95 < 50, `expected p95 < 50ms, got ${p95.toFixed(2)}ms`);
});

test('certificate auto-renewal triggers <= 14d, skips > 14d, verifies key/CA, and writes atomically', async (t) => {
  const gatewayId = '01a14000-4000-7000-8000-000000000100';
  const renewCalls: string[] = [];

  // Scenario A: > 14d remaining skips without network call
  const validPki = createTestPKI(gatewayId, 45);
  const dirSkip = fs.mkdtempSync(path.join(os.tmpdir(), 'hvac-cert-skip-'));
  tempDirs.push(dirSkip);
  fs.writeFileSync(path.join(dirSkip, 'gateway.crt'), validPki.gwPem);
  fs.writeFileSync(path.join(dirSkip, 'gateway.key'), validPki.gwKey);
  fs.writeFileSync(path.join(dirSkip, 'ca.crt'), validPki.caPem);

  const hubSkip = await startNodeUpgradeHub(validPki, renewCalls);
  t.after(() => hubSkip.close());
  const skipResult = await checkAndRenewGatewayCertificate({
    renewUrl: `https://127.0.0.1:${hubSkip.port}/gateway/v1/renew`,
    certPath: path.join(dirSkip, 'gateway.crt'),
    keyPath: path.join(dirSkip, 'gateway.key'),
    caPath: path.join(dirSkip, 'ca.crt'),
    rejectUnauthorized: true,
  });
  assert.equal(skipResult.renewed, false);
  assert.equal(skipResult.reason, 'valid_beyond_threshold');
  assert.equal(renewCalls.length, 0);

  // Scenario B: <= 14d remaining generates valid PKCS#10 CSR, renews over mTLS, verifies key/CA, writes atomically
  const expiringPki = createTestPKI(gatewayId, 10);
  assert.equal(shouldRenewCertificate(expiringPki.gwPem).renew, true);
  const dirRenew = fs.mkdtempSync(path.join(os.tmpdir(), 'hvac-cert-renew-'));
  tempDirs.push(dirRenew);
  const certPath = path.join(dirRenew, 'gateway.crt');
  const keyPath = path.join(dirRenew, 'gateway.key');
  const caPath = path.join(dirRenew, 'ca.crt');
  fs.writeFileSync(certPath, expiringPki.gwPem);
  fs.writeFileSync(keyPath, expiringPki.gwKey);
  fs.writeFileSync(caPath, expiringPki.caPem);

  const renewLog: string[] = [];
  const hubRenew = await startNodeUpgradeHub(expiringPki, renewLog);
  t.after(() => hubRenew.close());
  let callbackCalled = false;
  const renewRes = await checkAndRenewGatewayCertificate({
    renewUrl: `https://127.0.0.1:${hubRenew.port}/gateway/v1/renew`,
    certPath,
    keyPath,
    caPath,
    rejectUnauthorized: true,
    onRenewed: () => {
      callbackCalled = true;
    },
  });
  assert.equal(renewRes.renewed, true);
  assert.equal(callbackCalled, true);
  assert.equal(renewLog.length, 1);
  assert.match(renewLog[0] ?? '', /BEGIN CERTIFICATE REQUEST/);

  const updatedPem = fs.readFileSync(certPath, 'utf8');
  assert.notEqual(updatedPem, expiringPki.gwPem);
  const updatedX509 = new X509Certificate(updatedPem);
  const pinnedCaX509 = new X509Certificate(expiringPki.caPem);
  assert.ok(updatedX509.checkIssued(pinnedCaX509));
  assert.ok(updatedX509.verify(pinnedCaX509.publicKey));
  assert.equal(shouldRenewCertificate(updatedPem).renew, false);
  const leftoverTmp = fs.readdirSync(dirRenew).filter((f) => f.includes('.tmp-'));
  assert.equal(leftoverTmp.length, 0, 'no temporary files may remain after atomic rename');

  // Scenario C & D: reject forged issuer signature and unpinned CA replacement without mutating disk files.
  const rogueCaKeys = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const rogueCaPriv = rogueCaKeys.privateKey.export({ type: 'pkcs8', format: 'pem' }) as string;
  const rogueCaPub = rogueCaKeys.publicKey.export({ type: 'spki', format: 'pem' }) as string;
  const rogueCaPem = issueSignedX509Cert({
    subjectCN: 'Test Gateway CA',
    issuerCN: 'Test Gateway CA',
    publicKeyPem: rogueCaPub,
    signerKeyPem: rogueCaPriv,
    notBefore: new Date(Date.now() - 60_000),
    notAfter: new Date(Date.now() + 365 * 86400_000),
    isCA: true,
  });
  const forgedLeaf = issueSignedX509Cert({
    subjectCN: gatewayId,
    issuerCN: 'Test Gateway CA',
    publicKeyPem: expiringPki.gwPub,
    signerKeyPem: rogueCaPriv,
    notBefore: new Date(Date.now() - 60_000),
    notAfter: new Date(Date.now() + 90 * 86400_000),
    extKeyUsageClient: true,
  });

  const dirBad = fs.mkdtempSync(path.join(os.tmpdir(), 'hvac-cert-forged-'));
  tempDirs.push(dirBad);
  const badCertPath = path.join(dirBad, 'gateway.crt');
  const badKeyPath = path.join(dirBad, 'gateway.key');
  const badCaPath = path.join(dirBad, 'ca.crt');
  fs.writeFileSync(badCertPath, expiringPki.gwPem);
  fs.writeFileSync(badKeyPath, expiringPki.gwKey);
  fs.writeFileSync(badCaPath, expiringPki.caPem);

  let returnRogueCa = false;
  const hubForged = await startNodeUpgradeHub(expiringPki, [], () => ({
    certificatePem: forgedLeaf,
    caPem: returnRogueCa ? rogueCaPem : expiringPki.caPem,
  }));
  t.after(() => hubForged.close());

  await assert.rejects(
    () =>
      checkAndRenewGatewayCertificate({
        renewUrl: `https://127.0.0.1:${hubForged.port}/gateway/v1/renew`,
        certPath: badCertPath,
        keyPath: badKeyPath,
        caPath: badCaPath,
        rejectUnauthorized: true,
      }),
    /pinned CA signature verification/,
  );
  assert.equal(fs.readFileSync(badCertPath, 'utf8'), expiringPki.gwPem);

  returnRogueCa = true;
  await assert.rejects(
    () =>
      checkAndRenewGatewayCertificate({
        renewUrl: `https://127.0.0.1:${hubForged.port}/gateway/v1/renew`,
        certPath: badCertPath,
        keyPath: badKeyPath,
        caPath: badCaPath,
        rejectUnauthorized: true,
      }),
    /does not match pinned CA/,
  );
  assert.equal(fs.readFileSync(badCaPath, 'utf8'), expiringPki.caPem);
  assert.equal(fs.readFileSync(badCertPath, 'utf8'), expiringPki.gwPem);

  // Sanity check CSR format directly
  const sampleCsr = buildPkcs10CsrPem(gatewayId, expiringPki.gwKey);
  assert.ok(sampleCsr.startsWith('-----BEGIN CERTIFICATE REQUEST-----\n'));
  assert.ok(sampleCsr.endsWith('-----END CERTIFICATE REQUEST-----\n'));
});

test('deploy guard protects ET1010·机组启停 and 平台·常驻隧道 and builds expected tabs', () => {
  const tunnelFlow = buildGatewayTunnelFlow({
    tunnelUrl: 'https://hvac.example.com/gateway/v1/tunnel',
    certPath: '/root/hvac-platform/gateway.crt',
    keyPath: '/root/hvac-platform/gateway.key',
    caPath: '/root/hvac-platform/ca.crt',
  });
  const certFlow = buildGatewayCertAutorenewFlow({
    renewUrl: 'https://hvac.example.com/gateway/v1/renew',
    certPath: '/root/hvac-platform/gateway.crt',
    keyPath: '/root/hvac-platform/gateway.key',
    caPath: '/root/hvac-platform/ca.crt',
  });
  const current = [
    { id: 'tab-et1010', type: 'tab', label: 'ET1010·机组启停', disabled: false },
    ...tunnelFlow,
    ...certFlow,
  ];
  validateProtectedFlowTabs(current, current);
  assert.ok(tunnelFlow.some((n) => n.type === 'tab' && n.label === '平台·常驻隧道'));
  assert.ok(certFlow.some((n) => n.type === 'tab' && n.label === '平台·证书'));

  const withoutEt1010 = current.filter((n) => n.id !== 'tab-et1010');
  assert.throws(() => validateProtectedFlowTabs(current, withoutEt1010), (err: unknown) => {
    assert.ok(err instanceof ProtectedFlowTabError);
    assert.deepEqual(err.missingLabels, ['ET1010·机组启停']);
    return true;
  });

  const disabledTunnel = current.map((n) =>
    n.label === '平台·常驻隧道' ? { ...n, disabled: true } : n,
  );
  assert.throws(() => validateProtectedFlowTabs(current, disabledTunnel), (err: unknown) => {
    assert.ok(err instanceof ProtectedFlowTabError);
    assert.deepEqual(err.missingLabels, ['平台·常驻隧道']);
    return true;
  });
});

test('tunnel and cert autorenew trust system root CAs alongside pinned gateway CA', async (t) => {
  const gatewayId = '01a14000-4000-7000-8000-000000000100';
  const gatewayPki = createTestPKI(gatewayId, 5);
  const publicCaKeys = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const srvKeys = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const now = Date.now();
  const publicCaPriv = publicCaKeys.privateKey.export({ type: 'pkcs8', format: 'pem' }) as string;
  const publicCaPem = issueSignedX509Cert({
    subjectCN: 'Mock Public Root CA',
    issuerCN: 'Mock Public Root CA',
    publicKeyPem: publicCaKeys.publicKey.export({ type: 'spki', format: 'pem' }) as string,
    signerKeyPem: publicCaPriv,
    notBefore: new Date(now - 3600_000),
    notAfter: new Date(now + 365 * 86400_000),
    isCA: true,
  });
  const publicSrvPem = issueSignedX509Cert({
    subjectCN: 'localhost',
    issuerCN: 'Mock Public Root CA',
    publicKeyPem: srvKeys.publicKey.export({ type: 'spki', format: 'pem' }) as string,
    signerKeyPem: publicCaPriv,
    notBefore: new Date(now - 3600_000),
    notAfter: new Date(now + 90 * 86400_000),
    sanLocalhost: true,
    extKeyUsageServer: true,
  });
  const renewedLeaf = issueSignedX509Cert({
    subjectCN: gatewayId,
    issuerCN: 'Test Gateway CA',
    publicKeyPem: gatewayPki.gwPub,
    signerKeyPem: gatewayPki.caPriv,
    notBefore: new Date(now - 60_000),
    notAfter: new Date(now + 90 * 86400_000),
    extKeyUsageClient: true,
  });

  const server = tls.createServer({
    cert: publicSrvPem,
    key: srvKeys.privateKey.export({ type: 'pkcs8', format: 'pem' }) as string,
    ca: gatewayPki.caPem,
    requestCert: true,
    rejectUnauthorized: true,
  });
  server.on('secureConnection', (sock) => {
    sock.once('data', (chunk) => {
      const req = chunk.toString('utf8');
      if (req.startsWith('POST /gateway/v1/renew ')) {
        const body = JSON.stringify({
          certificatePem: renewedLeaf,
          caPem: gatewayPki.caPem,
          expiresAt: new Date(now + 90 * 86400_000).toISOString(),
        });
        sock.write(
          `HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${Buffer.byteLength(body)}\r\nConnection: close\r\n\r\n${body}`,
        );
        sock.end();
        return;
      }
      sock.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n');
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise<void>((resolve) => server.close(() => resolve())));
  const addr = server.address();
  assert.ok(addr && typeof addr === 'object');

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'hvac-dual-ca-'));
  tempDirs.push(dir);
  const publicCaPath = path.join(dir, 'public-root.crt');
  const certPath = path.join(dir, 'gateway.crt');
  const keyPath = path.join(dir, 'gateway.key');
  const gatewayCaPath = path.join(dir, 'gateway-ca.crt');
  const scriptPath = path.join(dir, 'probe.ts');
  fs.writeFileSync(publicCaPath, publicCaPem);
  fs.writeFileSync(certPath, gatewayPki.gwPem);
  fs.writeFileSync(keyPath, gatewayPki.gwKey);
  fs.writeFileSync(gatewayCaPath, gatewayPki.caPem);

  const tunnelModUrl = pathToFileURL(path.resolve(process.cwd(), 'edge/node-red/gateway-nodered-tunnel.ts')).href;
  const renewModUrl = pathToFileURL(path.resolve(process.cwd(), 'edge/node-red/gateway-cert-autorenew.ts')).href;
  fs.writeFileSync(
    scriptPath,
    [
      `import assert from 'node:assert/strict';`,
      `import * as fs from 'node:fs';`,
      `import { GatewayTunnelClient } from ${JSON.stringify(tunnelModUrl)};`,
      `import { checkAndRenewGatewayCertificate } from ${JSON.stringify(renewModUrl)};`,
      `const port = ${addr.port};`,
      `const renewed = await checkAndRenewGatewayCertificate({`,
      `  renewUrl: 'https://localhost:' + port + '/gateway/v1/renew',`,
      `  certPath: ${JSON.stringify(certPath)},`,
      `  keyPath: ${JSON.stringify(keyPath)},`,
      `  caPath: ${JSON.stringify(gatewayCaPath)},`,
      `  rejectUnauthorized: true,`,
      `});`,
      `assert.equal(renewed.renewed, true);`,
      `await new Promise<void>((resolve, reject) => {`,
      `  const client = new GatewayTunnelClient({`,
      `    tunnelUrl: 'https://localhost:' + port + '/gateway/v1/tunnel',`,
      `    loadCredentials: () => ({`,
      `      certPem: fs.readFileSync(${JSON.stringify(certPath)}, 'utf8'),`,
      `      keyPem: fs.readFileSync(${JSON.stringify(keyPath)}, 'utf8'),`,
      `      caPem: fs.readFileSync(${JSON.stringify(gatewayCaPath)}, 'utf8'),`,
      `    }),`,
      `    rejectUnauthorized: true,`,
      `    onConnected: () => { client.stop(); resolve(); },`,
      `    onDisconnected: (err) => { if (err) { client.stop(); reject(err); } },`,
      `  });`,
      `  client.start();`,
      `});`,
    ].join('\n'),
  );

  await new Promise<void>((resolve, reject) => {
    const child = spawn(process.execPath, ['--experimental-strip-types', scriptPath], {
      env: { ...process.env, NODE_EXTRA_CA_CERTS: publicCaPath },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stderr = '';
    child.stderr.on('data', (chunk: Buffer) => {
      stderr += chunk.toString('utf8');
    });
    child.on('error', reject);
    child.on('close', (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`child exited ${code}: ${stderr}`));
      }
    });
  });
});

test('syncSongyaxuanEnvelopeViaApi handles 401 password-file token exchange, passwordless 200, and protected tab guards', async (t) => {
  const sampleEnvelope = '// test envelope\nreturn msg;\n';
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'hvac-nodered-sync-'));
  tempDirs.push(dir);
  const passwordFile = path.join(dir, 'nodered-admin');
  fs.writeFileSync(passwordFile, 'secret-pass-from-file\n', { mode: 0o600 });

  // Case (a): 401 on unauthenticated GET /flows exchanges /auth/token via passwordFile and hot-swaps only target func.
  {
    let requireAuth = true;
    let rev = 'rev-a-1';
    let postDeployHeader = '';
    let authCalls = 0;
    let flows = [
      { id: 'tab-et', type: 'tab', label: 'ET1010·机组启停', disabled: false },
      { id: 'tab-tun', type: 'tab', label: '平台·常驻隧道', disabled: false },
      { id: 'tab-edge', type: 'tab', label: 'Edge·采集与上报', disabled: false },
      { id: 'fn-env', type: 'function', z: 'tab-edge', name: '平台·形成信封', func: '// stale' },
      { id: 'mb-1', type: 'modbusRead', z: 'tab-edge', name: 'ET1010 UID31', uid: '31' },
    ];
    const srv = http.createServer(async (req, res) => {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(chunk);
      const rawBody = Buffer.concat(chunks).toString('utf8');

      if (req.method === 'POST' && req.url === '/auth/token') {
        authCalls += 1;
        const form = new URLSearchParams(rawBody);
        assert.equal(form.get('username'), 'admin');
        assert.equal(form.get('password'), 'secret-pass-from-file');
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ access_token: 'tok-abc', token_type: 'Bearer' }));
        return;
      }
      if (requireAuth && req.headers.authorization !== 'Bearer tok-abc') {
        res.writeHead(401, { 'Content-Type': 'text/plain' });
        res.end('Unauthorized');
        return;
      }
      if (req.method === 'GET' && req.url === '/flows') {
        assert.equal(req.headers['node-red-api-version'], 'v2');
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ rev, flows }));
        return;
      }
      if (req.method === 'POST' && req.url === '/flows') {
        assert.equal(req.headers['node-red-api-version'], 'v2');
        postDeployHeader = String(req.headers['node-red-deployment-type'] ?? '');
        const body = JSON.parse(rawBody) as { rev: string; flows: typeof flows };
        assert.equal(body.rev, rev);
        flows = body.flows;
        rev = 'rev-a-2';
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ rev }));
        return;
      }
      res.writeHead(404);
      res.end();
    });
    await new Promise<void>((resolve) => srv.listen(0, '127.0.0.1', resolve));
    t.after(() => new Promise<void>((resolve) => srv.close(() => resolve())));
    const addr = srv.address();
    assert.ok(addr && typeof addr === 'object');

    const resA = await syncSongyaxuanEnvelopeViaApi({
      baseUrl: `http://127.0.0.1:${addr.port}`,
      envelopeSource: sampleEnvelope,
      passwordFile,
    });
    assert.equal(authCalls, 1);
    assert.equal(postDeployHeader, 'nodes');
    assert.equal(resA.ok, true);
    assert.equal(resA.revBefore, 'rev-a-1');
    assert.equal(resA.revAfter, 'rev-a-2');
    assert.equal(resA.verifiedMatch, true);
    assert.deepEqual(flows.find((n) => n.id === 'mb-1'), {
      id: 'mb-1',
      type: 'modbusRead',
      z: 'tab-edge',
      name: 'ET1010 UID31',
      uid: '31',
    });

    // Case (b): Passwordless Node-RED (200 on initial GET /flows) completes with zero credentials or /auth/token calls.
    requireAuth = false;
    authCalls = 0;
    const resB = await syncSongyaxuanEnvelopeViaApi({
      baseUrl: `http://127.0.0.1:${addr.port}`,
      envelopeSource: '// second pass\nreturn null;\n',
    });
    assert.equal(authCalls, 0);
    assert.equal(resB.verifiedMatch, true);

    // Case (c): Missing or disabled protected tab aborts with ProtectedFlowTabError before POST /flows.
    let postAttempted = false;
    const badSrv = http.createServer((req, res) => {
      if (req.method === 'POST' && req.url === '/flows') {
        postAttempted = true;
      }
      if (req.method === 'GET' && req.url === '/flows') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(
          JSON.stringify({
            rev: 'rev-bad',
            flows: [
              { id: 'tab-et', type: 'tab', label: 'ET1010·机组启停', disabled: true },
              { id: 'tab-tun', type: 'tab', label: '平台·常驻隧道', disabled: false },
              { id: 'tab-edge', type: 'tab', label: 'Edge·采集与上报', disabled: false },
              { id: 'fn-env', type: 'function', z: 'tab-edge', name: '平台·形成信封', func: '// stale' },
            ],
          }),
        );
        return;
      }
      res.writeHead(404);
      res.end();
    });
    await new Promise<void>((resolve) => badSrv.listen(0, '127.0.0.1', resolve));
    t.after(() => new Promise<void>((resolve) => badSrv.close(() => resolve())));
    const badAddr = badSrv.address();
    assert.ok(badAddr && typeof badAddr === 'object');

    await assert.rejects(
      () =>
        syncSongyaxuanEnvelopeViaApi({
          baseUrl: `http://127.0.0.1:${badAddr.port}`,
          envelopeSource: sampleEnvelope,
        }),
      (err) => err instanceof ProtectedFlowTabError && err.missingLabels.includes('ET1010·机组启停'),
    );
    assert.equal(postAttempted, false);

    // Verify buildRemoteModulePayload produces syntactically valid ESM JS.
    const remoteScript = buildRemoteModulePayload({
      baseUrl: 'http://127.0.0.1:18800',
      envelopeSource: sampleEnvelope,
      username: 'admin',
      passwordFile,
    });
    await new Promise<void>((resolve, reject) => {
      const child = spawn(process.execPath, ['--input-type=module', '--check', '-'], {
        stdio: ['pipe', 'pipe', 'pipe'],
      });
      let stderr = '';
      child.stderr.on('data', (chunk: Buffer) => {
        stderr += chunk.toString('utf8');
      });
      child.on('error', reject);
      child.on('close', (code) => {
        if (code === 0) resolve();
        else reject(new Error(`remote bundle syntax check failed (${code}): ${stderr}`));
      });
      child.stdin.end(remoteScript);
    });
  }
});
