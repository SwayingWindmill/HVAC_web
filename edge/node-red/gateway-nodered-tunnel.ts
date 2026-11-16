import { randomInt } from 'node:crypto';
import * as net from 'node:net';
import * as tls from 'node:tls';

export const TUNNEL_PROTOCOL_NAME = 'hvac-gateway-tunnel/v1';
export const ALLOWED_TARGET_PORT = 1880;
export const MAX_FRAME_PAYLOAD = 64 * 1024;
export const DEFAULT_PING_INTERVAL_MS = 25_000;
export const DEFAULT_READ_TIMEOUT_MS = 75_000;
export const PROTECTED_FLOW_TAB_LABELS = ['ET1010·机组启停', '平台·常驻隧道'] as const;

export const FrameType = {
  OPEN: 0x01,
  OPEN_OK: 0x02,
  OPEN_ERR: 0x03,
  DATA: 0x04,
  EOF: 0x05,
  RST: 0x06,
  PING: 0x07,
  PONG: 0x08,
} as const;

export type FrameTypeCode = (typeof FrameType)[keyof typeof FrameType];

export interface TunnelFrame {
  type: number;
  streamId: number;
  payload: Buffer;
}

export interface TunnelCredentials {
  certPem: string | Buffer;
  keyPem: string | Buffer;
  caPem?: string | Buffer;
}

export function mergeTransportCaCertificates(caPem?: string | Buffer): Array<string | Buffer> | undefined {
  if (!caPem) return undefined;
  const maybeGetCa = Reflect.get(tls, 'getCACertificates');
  const roots =
    typeof maybeGetCa === 'function' ? maybeGetCa.call(tls, 'default') : tls.rootCertificates;
  return Array.isArray(roots) ? [...roots, caPem] : [...tls.rootCertificates, caPem];
}

export interface GatewayTunnelOptions {
  tunnelUrl: string;
  loadCredentials: () => Promise<TunnelCredentials> | TunnelCredentials;
  targetHost?: string;
  targetPort?: number;
  dialTarget?: (port: number, host: string) => net.Socket;
  pingIntervalMs?: number;
  readTimeoutMs?: number;
  minReconnectDelayMs?: number;
  maxReconnectDelayMs?: number;
  rejectUnauthorized?: boolean;
  onConnected?: () => void;
  onDisconnected?: (err?: Error) => void;
  onLog?: (level: 'info' | 'warn' | 'error', event: string, detail?: Record<string, unknown>) => void;
}

export interface NodeRedFlowItem {
  id: string;
  type: string;
  label?: string;
  name?: string;
  disabled?: boolean;
  [key: string]: unknown;
}

export class ProtectedFlowTabError extends Error {
  missingLabels: string[];

  constructor(missingLabels: string[]) {
    super(`protected Node-RED tabs cannot be removed or disabled: ${missingLabels.join(', ')}`);
    this.name = 'ProtectedFlowTabError';
    this.missingLabels = missingLabels;
  }
}

export function validateProtectedFlowTabs(
  currentFlows: readonly NodeRedFlowItem[],
  candidateFlows: readonly NodeRedFlowItem[],
  protectedLabels: readonly string[] = PROTECTED_FLOW_TAB_LABELS,
): void {
  const activeCurrent = new Set(
    currentFlows
      .filter((item) => item.type === 'tab' && !item.disabled && typeof item.label === 'string')
      .map((item) => item.label as string),
  );
  const activeCandidate = new Set(
    candidateFlows
      .filter((item) => item.type === 'tab' && !item.disabled && typeof item.label === 'string')
      .map((item) => item.label as string),
  );
  const missing = protectedLabels.filter((label) => activeCurrent.has(label) && !activeCandidate.has(label));
  if (missing.length > 0) {
    throw new ProtectedFlowTabError(missing);
  }
}

export function encodeFrame(frame: TunnelFrame): Buffer {
  if (frame.payload.length > MAX_FRAME_PAYLOAD) {
    throw new RangeError(`tunnel frame payload exceeds ${MAX_FRAME_PAYLOAD} bytes`);
  }
  const out = Buffer.allocUnsafe(9 + frame.payload.length);
  out.writeUInt8(frame.type, 0);
  out.writeUInt32BE(frame.streamId >>> 0, 1);
  out.writeUInt32BE(frame.payload.length >>> 0, 5);
  frame.payload.copy(out, 9);
  return out;
}

export function decodeFramesFromBuffer(buf: Buffer): { frames: TunnelFrame[]; remainder: Buffer } {
  const frames: TunnelFrame[] = [];
  let offset = 0;
  while (offset + 9 <= buf.length) {
    const length = buf.readUInt32BE(offset + 5);
    if (length > MAX_FRAME_PAYLOAD) {
      throw new RangeError(`incoming frame payload exceeds ${MAX_FRAME_PAYLOAD} bytes: ${length}`);
    }
    if (offset + 9 + length > buf.length) {
      break;
    }
    const payload = length > 0 ? Buffer.from(buf.subarray(offset + 9, offset + 9 + length)) : Buffer.alloc(0);
    frames.push({
      type: buf.readUInt8(offset),
      streamId: buf.readUInt32BE(offset + 1),
      payload,
    });
    offset += 9 + length;
  }
  return {
    frames,
    remainder: offset === 0 ? buf : Buffer.from(buf.subarray(offset)),
  };
}

export class GatewayTunnelClient {
  private readonly options: Required<
    Pick<
      GatewayTunnelOptions,
      | 'tunnelUrl'
      | 'loadCredentials'
      | 'targetHost'
      | 'targetPort'
      | 'pingIntervalMs'
      | 'readTimeoutMs'
      | 'minReconnectDelayMs'
      | 'maxReconnectDelayMs'
      | 'rejectUnauthorized'
    >
  > &
    Pick<GatewayTunnelOptions, 'dialTarget' | 'onConnected' | 'onDisconnected' | 'onLog'>;

  private stopped = false;
  private activeSocket: tls.TLSSocket | null = null;
  private reconnectTimer: NodeJS.Timeout | null = null;
  private attempt = 0;
  private connecting: Promise<void> | null = null;

  constructor(options: GatewayTunnelOptions) {
    this.options = {
      tunnelUrl: options.tunnelUrl,
      loadCredentials: options.loadCredentials,
      targetHost: options.targetHost ?? '127.0.0.1',
      targetPort: options.targetPort ?? ALLOWED_TARGET_PORT,
      pingIntervalMs: options.pingIntervalMs ?? DEFAULT_PING_INTERVAL_MS,
      readTimeoutMs: options.readTimeoutMs ?? DEFAULT_READ_TIMEOUT_MS,
      minReconnectDelayMs: options.minReconnectDelayMs ?? 1_000,
      maxReconnectDelayMs: options.maxReconnectDelayMs ?? 30_000,
      rejectUnauthorized: options.rejectUnauthorized ?? true,
      dialTarget: options.dialTarget,
      onConnected: options.onConnected,
      onDisconnected: options.onDisconnected,
      onLog: options.onLog,
    };
  }

  start(): void {
    this.stopped = false;
    this.scheduleConnect(0);
  }

  async stop(): Promise<void> {
    this.stopped = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.activeSocket) {
      this.activeSocket.destroy();
      this.activeSocket = null;
    }
    if (this.connecting) {
      await this.connecting.catch(() => undefined);
    }
  }

  async reloadCredentials(): Promise<void> {
    if (this.stopped) {
      return;
    }
    this.options.onLog?.('info', 'tunnel_reload_credentials');
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.attempt = 0;
    if (this.activeSocket) {
      this.activeSocket.destroy();
      this.activeSocket = null;
    } else {
      this.scheduleConnect(0);
    }
  }

  private scheduleConnect(delayMs: number): void {
    if (this.stopped || this.reconnectTimer) {
      return;
    }
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (this.stopped) {
        return;
      }
      this.connecting = this.connectOnce().finally(() => {
        this.connecting = null;
      });
    }, delayMs);
  }

  private nextBackoffMs(): number {
    const exp = Math.min(this.options.maxReconnectDelayMs, this.options.minReconnectDelayMs * 2 ** Math.min(this.attempt, 8));
    this.attempt += 1;
    const jitter = randomInt(0, Math.max(1, Math.floor(exp * 0.25)));
    return Math.min(this.options.maxReconnectDelayMs, Math.floor(exp * 0.85) + jitter);
  }

  private async connectOnce(): Promise<void> {
    if (this.stopped) {
      return;
    }
    try {
      const creds = await this.options.loadCredentials();
      const target = new URL(this.options.tunnelUrl);
      const port = target.port ? Number(target.port) : 443;
      const pathAndQuery = `${target.pathname || '/gateway/v1/tunnel'}${target.search}`;

      const socket = await new Promise<tls.TLSSocket>((resolve, reject) => {
        const s = tls.connect({
          host: target.hostname,
          port,
          ...(net.isIP(target.hostname) === 0 ? { servername: target.hostname } : {}),
          cert: creds.certPem,
          key: creds.keyPem,
          ...(creds.caPem ? { ca: mergeTransportCaCertificates(creds.caPem) } : {}),
          ALPNProtocols: ['http/1.1'],
          rejectUnauthorized: this.options.rejectUnauthorized,
        });
        const onErr = (err: Error) => {
          s.removeAllListeners('secureConnect');
          s.destroy();
          reject(err);
        };
        s.once('error', onErr);
        s.once('secureConnect', () => {
          s.removeListener('error', onErr);
          s.setNoDelay(true);
          resolve(s);
        });
      });

      if (this.stopped) {
        socket.destroy();
        return;
      }

      const initialBuffer = await new Promise<Buffer>((resolve, reject) => {
        let headerBuf = Buffer.alloc(0);
        const onData = (chunk: Buffer) => {
          headerBuf = Buffer.concat([headerBuf, chunk]);
          const marker = headerBuf.indexOf('\r\n\r\n');
          if (marker === -1) {
            if (headerBuf.length > 16 * 1024) {
              cleanup();
              reject(new Error('HTTP upgrade response headers too large'));
            }
            return;
          }
          cleanup();
          const head = headerBuf.subarray(0, marker).toString('latin1');
          const statusLine = head.split('\r\n', 1)[0] ?? '';
          if (!/^HTTP\/1\.[01]\s+101\b/i.test(statusLine)) {
            reject(new Error(`tunnel upgrade rejected: ${statusLine}`));
            return;
          }
          resolve(Buffer.from(headerBuf.subarray(marker + 4)));
        };
        const onErr = (err: Error) => {
          cleanup();
          reject(err);
        };
        const onClose = () => {
          cleanup();
          reject(new Error('tunnel closed before 101 switching protocols'));
        };
        const cleanup = () => {
          socket.off('data', onData);
          socket.off('error', onErr);
          socket.off('close', onClose);
        };
        socket.on('data', onData);
        socket.once('error', onErr);
        socket.once('close', onClose);
        const reqHead =
          `GET ${pathAndQuery} HTTP/1.1\r\n` +
          `Host: ${target.host}\r\n` +
          `Connection: Upgrade\r\n` +
          `Upgrade: ${TUNNEL_PROTOCOL_NAME}\r\n\r\n`;
        socket.write(reqHead);
      });

      this.runSession(socket, initialBuffer);
    } catch (err) {
      const error = err instanceof Error ? err : new Error(String(err));
      this.options.onDisconnected?.(error);
      this.options.onLog?.('warn', 'tunnel_connect_failed', { error: error.message });
      if (!this.stopped) {
        this.scheduleConnect(this.nextBackoffMs());
      }
    }
  }

  private runSession(socket: tls.TLSSocket, initialBuffer: Buffer): void {
    this.activeSocket = socket;
    this.attempt = 0;
    this.options.onConnected?.();

    const streams = new Map<number, net.Socket>();
    let rxBuffer = initialBuffer;
    let lastRxAt = Date.now();
    let closed = false;

    const sendFrame = (frame: TunnelFrame) => {
      if (!closed && !socket.destroyed) {
        socket.write(encodeFrame(frame));
      }
    };

    const closeAll = (err?: Error) => {
      if (closed) {
        return;
      }
      closed = true;
      clearInterval(pingTimer);
      if (this.activeSocket === socket) {
        this.activeSocket = null;
      }
      for (const local of streams.values()) {
        local.destroy();
      }
      streams.clear();
      socket.destroy();
      this.options.onDisconnected?.(err);
      if (!this.stopped) {
        this.scheduleConnect(this.nextBackoffMs());
      }
    };

    const pingTimer = setInterval(() => {
      if (Date.now() - lastRxAt > this.options.readTimeoutMs) {
        closeAll(new Error('tunnel read timeout'));
        return;
      }
      sendFrame({ type: FrameType.PING, streamId: 0, payload: Buffer.alloc(0) });
    }, this.options.pingIntervalMs);
    pingTimer.unref?.();

    const handleOpen = (frame: TunnelFrame) => {
      if (frame.payload.length !== 2) {
        sendFrame({
          type: FrameType.OPEN_ERR,
          streamId: frame.streamId,
          payload: Buffer.from('invalid open payload'),
        });
        return;
      }
      const requestedPort = frame.payload.readUInt16BE(0);
      if (requestedPort !== ALLOWED_TARGET_PORT) {
        sendFrame({
          type: FrameType.OPEN_ERR,
          streamId: frame.streamId,
          payload: Buffer.from('tunnel target port forbidden'),
        });
        return;
      }

      const dialPort = this.options.targetPort;
      const localSocket = this.options.dialTarget
        ? this.options.dialTarget(dialPort, this.options.targetHost)
        : net.createConnection({
            host: this.options.targetHost,
            port: dialPort,
            allowHalfOpen: true,
            noDelay: true,
          });
      localSocket.setNoDelay?.(true);

      let opened = false;
      let sentEOF = false;
      localSocket.once('connect', () => {
        if (closed) {
          localSocket.destroy();
          return;
        }
        opened = true;
        streams.set(frame.streamId, localSocket);
        sendFrame({ type: FrameType.OPEN_OK, streamId: frame.streamId, payload: Buffer.alloc(0) });
      });

      localSocket.on('data', (chunk: Buffer) => {
        for (let off = 0; off < chunk.length; off += MAX_FRAME_PAYLOAD) {
          sendFrame({
            type: FrameType.DATA,
            streamId: frame.streamId,
            payload: chunk.subarray(off, Math.min(chunk.length, off + MAX_FRAME_PAYLOAD)),
          });
        }
      });

      localSocket.on('end', () => {
        sentEOF = true;
        sendFrame({ type: FrameType.EOF, streamId: frame.streamId, payload: Buffer.alloc(0) });
      });

      localSocket.on('error', (err) => {
        streams.delete(frame.streamId);
        if (!opened) {
          sendFrame({
            type: FrameType.OPEN_ERR,
            streamId: frame.streamId,
            payload: Buffer.from(err.message),
          });
          return;
        }
        sendFrame({ type: FrameType.RST, streamId: frame.streamId, payload: Buffer.alloc(0) });
      });

      localSocket.on('close', (hadError) => {
        if (streams.delete(frame.streamId) && (hadError || !sentEOF)) {
          sendFrame({ type: FrameType.RST, streamId: frame.streamId, payload: Buffer.alloc(0) });
        }
      });
    };

    const handleFrame = (frame: TunnelFrame) => {
      switch (frame.type) {
        case FrameType.OPEN:
          handleOpen(frame);
          break;
        case FrameType.DATA: {
          const local = streams.get(frame.streamId);
          if (local && !local.destroyed) {
            local.write(frame.payload);
          }
          break;
        }
        case FrameType.EOF: {
          const local = streams.get(frame.streamId);
          if (local && !local.destroyed) {
            local.end();
          }
          break;
        }
        case FrameType.RST: {
          const local = streams.get(frame.streamId);
          streams.delete(frame.streamId);
          if (local) {
            local.destroy();
          }
          break;
        }
        case FrameType.PING:
          sendFrame({ type: FrameType.PONG, streamId: frame.streamId, payload: frame.payload });
          break;
        case FrameType.PONG:
          break;
      }
    };

    const drainBuffer = () => {
      const decoded = decodeFramesFromBuffer(rxBuffer);
      rxBuffer = decoded.remainder;
      for (const frame of decoded.frames) {
        handleFrame(frame);
      }
    };

    socket.on('data', (chunk: Buffer) => {
      lastRxAt = Date.now();
      rxBuffer = rxBuffer.length === 0 ? chunk : Buffer.concat([rxBuffer, chunk]);
      try {
        drainBuffer();
      } catch (err) {
        closeAll(err instanceof Error ? err : new Error(String(err)));
      }
    });

    socket.once('error', (err) => closeAll(err));
    socket.once('close', () => closeAll());

    if (rxBuffer.length > 0) {
      try {
        drainBuffer();
      } catch (err) {
        closeAll(err instanceof Error ? err : new Error(String(err)));
      }
    }
  }
}

export function buildGatewayTunnelFlow(config: {
  tunnelUrl: string;
  certPath: string;
  keyPath: string;
  caPath: string;
  modulePath?: string;
}): NodeRedFlowItem[] {
  const tabId = 'tab-platform-tunnel';
  const injectId = 'inject-platform-tunnel-start';
  const fnId = 'fn-platform-tunnel-client';
  const moduleRequire = config.modulePath ?? '/root/hvac-platform/gateway-nodered-tunnel.js';
  const funcLines = [
    `const fs = global.get('fs') || require('fs');`,
    `const { GatewayTunnelClient } = require(${JSON.stringify(moduleRequire)});`,
    `let client = context.flow.get('hvacTunnelClient');`,
    `if (msg.topic === 'reload' && client) { await client.reloadCredentials(); node.status({ fill: 'blue', shape: 'dot', text: 'reloading' }); return null; }`,
    `if (!client) {`,
    `  client = new GatewayTunnelClient({`,
    `    tunnelUrl: ${JSON.stringify(config.tunnelUrl)},`,
    `    loadCredentials: () => ({`,
    `      certPem: fs.readFileSync(${JSON.stringify(config.certPath)}, 'utf8'),`,
    `      keyPem: fs.readFileSync(${JSON.stringify(config.keyPath)}, 'utf8'),`,
    `      caPem: fs.readFileSync(${JSON.stringify(config.caPath)}, 'utf8'),`,
    `    }),`,
    `    onConnected: () => node.status({ fill: 'green', shape: 'dot', text: '1880 tunnel ready' }),`,
    `    onDisconnected: (e) => node.status({ fill: 'red', shape: 'ring', text: e ? e.message : 'disconnected' }),`,
    `  });`,
    `  context.flow.set('hvacTunnelClient', client);`,
    `  global.set('hvacTunnelClient', client);`,
    `  client.start();`,
    `}`,
    `return null;`,
  ];

  return [
    { id: tabId, type: 'tab', label: '平台·常驻隧道', disabled: false, info: '1880 reverse mTLS tunnel' },
    {
      id: injectId,
      type: 'inject',
      z: tabId,
      name: '开机自启隧道',
      props: [{ p: 'topic', vt: 'str', v: 'ensure' }],
      repeat: '300',
      once: true,
      onceDelay: 1,
      wires: [[fnId]],
    },
    {
      id: fnId,
      type: 'function',
      z: tabId,
      name: 'GatewayTunnelClient(1880)',
      func: funcLines.join('\n'),
      outputs: 1,
      cleanup: 'const c = context.flow.get("hvacTunnelClient"); if (c) { c.stop(); } global.set("hvacTunnelClient", undefined);',
      wires: [[]],
    },
  ];
}
