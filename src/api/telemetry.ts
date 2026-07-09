import { io, type Socket } from 'socket.io-client';
import { WS_PATH, USE_MOCK } from './config';
import { getToken } from './auth';
import { startMockPush, onMockPush } from './mock';
import type { PushHandler, StatusHandler, RealtimeStatus } from './types';

interface Transport {
  connect(): void;
  disconnect(): void;
  subscribe(deviceId: string, keys: string[]): void;
  unsubscribe(deviceId: string, keys: string[]): void;
  onPush(cb: PushHandler): void;
  onStatus(cb: StatusHandler): void;
}

// ---- real transport: Socket.IO /ws/telemetry ----
class SocketTransport implements Transport {
  private socket: Socket | null = null;
  private pushCbs: PushHandler[] = [];
  private statusCbs: StatusHandler[] = [];

  connect(): void {
    if (this.socket) return;
    const token = getToken();
    this.socket = io(WS_PATH, {
      query: { token: token ?? '' },
      transports: ['websocket'],
      reconnection: true,
      reconnectionAttempts: 5,
    });
    this.socket.on('connect', () => this.statusCbs.forEach((c) => c('open')));
    this.socket.on('disconnect', () => this.statusCbs.forEach((c) => c('closed')));
    this.socket.io.on('reconnect_failed', () => this.statusCbs.forEach((c) => c('degraded')));
    this.socket.on('telemetry', (p: { deviceId: string; key: string; value: number; ts: number }) => {
      this.pushCbs.forEach((cb) => cb(p.deviceId, p.key, p.value, p.ts));
    });
  }

  disconnect(): void {
    this.socket?.disconnect();
    this.socket = null;
  }

  subscribe(deviceId: string, keys: string[]): void {
    keys.forEach((k) => this.socket?.emit('subscribe', { deviceId, keys: [k] }));
  }

  unsubscribe(deviceId: string, keys: string[]): void {
    keys.forEach((k) => this.socket?.emit('unsubscribe', { deviceId, keys: [k] }));
  }

  onPush(cb: PushHandler): void {
    this.pushCbs.push(cb);
  }

  onStatus(cb: StatusHandler): void {
    this.statusCbs.push(cb);
  }
}

// ---- mock transport: drives the simulator in mock.ts ----
class MockTransport implements Transport {
  private pushCbs: PushHandler[] = [];
  private statusCbs: StatusHandler[] = [];
  private stop: (() => void) | null = null;
  private offPush: (() => void) | null = null;

  connect(): void {
    this.statusCbs.forEach((c) => c('connecting'));
    // simulate async connect
    setTimeout(() => {
      this.offPush = onMockPush((d, k, v, ts) => this.pushCbs.forEach((cb) => cb(d, k, v, ts)));
      this.stop = startMockPush();
      this.statusCbs.forEach((c) => c('open'));
    }, 200);
  }

  disconnect(): void {
    this.offPush?.();
    this.stop?.();
    this.offPush = null;
    this.stop = null;
  }

  // mock pushes every device/key globally, so subscribe/unsubscribe are no-ops
  subscribe(): void {}
  unsubscribe(): void {}
  onPush(cb: PushHandler): void {
    this.pushCbs.push(cb);
  }
  onStatus(cb: StatusHandler): void {
    this.statusCbs.push(cb);
  }
}

interface RegistryEntry {
  count: number;
  callbacks: Set<PushHandler>;
}

/**
 * Single realtime client. Connects once, shares one WS across components via a
 * reference-counted subscription registry (deviceId+key). Pushes are buffered and
 * flushed on requestAnimationFrame to avoid high-frequency re-renders. On reconnect
 * it re-issues every active subscription. (Per #8 spec.)
 */
export class TelemetryClient {
  private transport: Transport;
  private registry = new Map<string, Map<string, RegistryEntry>>();
  private buffer: { deviceId: string; key: string; value: number; ts: number }[] = [];
  private rafId: number | null = null;
  private status: RealtimeStatus = 'closed';
  private statusCbs: StatusHandler[] = [];
  private connected = false;

  constructor(transport: Transport) {
    this.transport = transport;
    this.transport.onPush((d, k, v, ts) => this.enqueue(d, k, v, ts));
    this.transport.onStatus((s) => this.handleStatus(s));
  }

  private handleStatus(s: RealtimeStatus): void {
    this.status = s;
    if (s === 'open') {
      this.connected = true;
      this.resubscribeAll();
    }
    this.statusCbs.forEach((c) => c(s));
  }

  private ensureEntry(deviceId: string, key: string): RegistryEntry {
    let dev = this.registry.get(deviceId);
    if (!dev) {
      dev = new Map();
      this.registry.set(deviceId, dev);
    }
    let e = dev.get(key);
    if (!e) {
      e = { count: 0, callbacks: new Set() };
      dev.set(key, e);
    }
    return e;
  }

  subscribe(deviceId: string, keys: string[], cb: PushHandler): void {
    let firstSub = false;
    keys.forEach((k) => {
      const e = this.ensureEntry(deviceId, k);
      if (e.count === 0) firstSub = true;
      e.count++;
      e.callbacks.add(cb);
    });
    // lazily connect on the first ever subscription
    if (!this.connected && this.status === 'closed') this.transport.connect();
    if (firstSub) this.transport.subscribe(deviceId, keys);
  }

  unsubscribe(deviceId: string, keys: string[], cb: PushHandler): void {
    const dev = this.registry.get(deviceId);
    if (!dev) return;
    keys.forEach((k) => {
      const e = dev.get(k);
      if (!e) return;
      e.callbacks.delete(cb);
      e.count--;
      if (e.count <= 0) {
        dev.delete(k);
        this.transport.unsubscribe(deviceId, [k]);
      }
    });
    if (dev.size === 0) this.registry.delete(deviceId);
  }

  private resubscribeAll(): void {
    this.registry.forEach((dev, deviceId) => {
      const keys = Array.from(dev.keys());
      if (keys.length) this.transport.subscribe(deviceId, keys);
    });
  }

  private enqueue(deviceId: string, key: string, value: number, ts: number): void {
    const existing = this.buffer.find((b) => b.deviceId === deviceId && b.key === key);
    if (existing) {
      existing.value = value;
      existing.ts = ts;
    } else {
      this.buffer.push({ deviceId, key, value, ts });
    }
    this.scheduleFlush();
  }

  private scheduleFlush(): void {
    if (this.rafId != null) return;
    const raf: (cb: FrameRequestCallback) => number =
      typeof window !== 'undefined' && window.requestAnimationFrame
        ? window.requestAnimationFrame.bind(window)
        : ((cb) => setTimeout(() => cb(performance.now()), 100) as unknown as number);
    this.rafId = raf(() => this.flush());
  }

  private flush(): void {
    this.rafId = null;
    const batch = this.buffer;
    this.buffer = [];
    batch.forEach((p) => {
      const dev = this.registry.get(p.deviceId);
      if (!dev) return;
      const e = dev.get(p.key);
      if (!e) return;
      e.callbacks.forEach((cb) => cb(p.deviceId, p.key, p.value, p.ts));
    });
  }

  onStatus(cb: StatusHandler): void {
    this.statusCbs.push(cb);
    cb(this.status);
  }

  getStatus(): RealtimeStatus {
    return this.status;
  }
}

export const telemetry = new TelemetryClient(USE_MOCK ? new MockTransport() : new SocketTransport());
