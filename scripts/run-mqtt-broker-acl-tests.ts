import { spawnSync } from 'node:child_process';
import { chmod, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import tls from 'node:tls';
import { runDockerCompose } from './lib/docker-cli.ts';

const { concat, from } = Buffer;
const outDir = resolve(process.env.MQTT_ACL_REPORT_DIR ?? 'out/mqtt-broker-acl');
const pkiDir = join(outDir, 'pki');
const rpt = join(outDir, 'acl.json');
const cf = ['-p', `hvac-mqtt-acl-${process.pid}`, '-f', resolve('infra/telemetry/mqtt/compose.yaml')];
const [GW, ADP, CMD] = ['mqtt-gateway', 'mqtt-adapter', 'command-dispatcher'], GWB = `${GW}-b`;
const [UT, UC, UR, US, UO, DC, DN, DS, DO] = ['up/telemetry', 'up/concentrator', 'up/reply', 'up/shadow', 'up/ota', 'down/command', 'down/concentrator', 'down/shadow', 'down/ota'];
const tp = (n: number, s: string): string => `hvac/v1/018f3e00-4000-7000-8000-000000000${n}00/${s}`;
const env = { ...process.env, MQTT_PKI_DIR: pkiDir, MQTT_HOST_PORT: '0' };
const now = (): string => new Date().toISOString();
const eq = (x: string[], y: string[], m: string): void => { if (x.length !== y.length || x.some((v, i) => v !== y[i])) throw new Error(`${m}: ${x}`); };
const run = (c: string, a: string[]): string => {
  const r = spawnSync(c, a, { env, encoding: 'utf8', windowsHide: true });
  if (r.error || r.status) throw new Error(`${c}: ${r.error?.message ?? r.stderr?.trim() ?? r.status}`);
  return (r.stdout ?? '').trim();
};
const compose = (args: string[]): string => runDockerCompose(run, [...cf, ...args]);
const down = (): void => { try { compose(['down', '-v', '--remove-orphans']); } catch {} };
const rd = (f: string): Promise<Buffer> => readFile(join(pkiDir, f));
const u8 = (s: string): number[] => [0, s.length, ...from(s)];
const pkt = (t: number, b: number[]): Buffer => from([t, b.length, ...b]);
const socks = new Set<tls.TLSSocket>();
const creds: Record<string, { cert: Buffer; key: Buffer }> = {};
let port: number;
let ca: Buffer;

async function mqtt(
  id: string,
  initPkt: Buffer,
  onPkt: (type: number, flags: number, payload: Buffer, socket: tls.TLSSocket, error?: unknown) => void,
): Promise<void> {
  const c = creds[id] ??= { cert: await rd(`${id}-cert.pem`), key: await rd(`${id}-key.pem`) };
  let buf = Buffer.alloc(0);
  const s = tls.connect({ host: '127.0.0.1', port, ca, ...c, servername: 'localhost' }, () =>
    s.write(pkt(16, [...u8('MQTT'), 5, 2, 0, 60, 0, ...u8(id + Math.random())])),
  );
  socks.add(s);
  s.on('error', (e) => onPkt(0, 0, Buffer.alloc(0), s, e));
  s.on('data', (d) => {
    for (buf = concat([buf, d]); buf.length >= 2;) {
      let i = 1, m = 1, l = 0, b: number;
      do { if (i >= buf.length) return; b = buf[i++]; l += (b & 127) * m; m *= 128; } while (b & 128);
      if (buf.length < i + l) return;
      const [h, p] = [buf[0], buf.subarray(i, i + l)];
      buf = buf.subarray(i + l);
      if (h === 32) s.write(initPkt); else onPkt(h >> 4, h & 15, p, s);
    }
  });
}

const pub = (id: string, t: string): Promise<number> => new Promise((res, rej) => mqtt(id, pkt(0x32, [...u8(t), 0, 1, 0, 123, 125]),
  (tp, _f, p, s, err) => !tp ? rej(err) : tp === 4 && (s.end(), res(p[2] ?? 0)),
).catch(rej));

async function sub(id: string, filter: string, n: number, ms: number): Promise<[Promise<string[]>]> {
  let ok!: () => void;
  let rej!: (reason?: unknown) => void;
  let fin!: (value: string[]) => void;
  let tm: ReturnType<typeof setTimeout> | undefined;
  const got: string[] = [];
  const subacked = new Promise<void>((r, j) => { ok = r; rej = j; });
  const done = new Promise<string[]>((r) => { fin = r; });
  await mqtt(id, pkt(0x82, [0, 1, 0, ...u8(filter), 1]), (t, fl, p, s, err) => {
    const end = () => { clearTimeout(tm); s.end(); fin(got.sort()); };
    if (!t) { rej(err); end(); }
    else if (t === 9) { tm = setTimeout(end, ms); ok(); }
    else if (t === 3) {
      const tl = p.readUInt16BE(0);
      got.push(p.toString('utf8', 2, 2 + tl));
      if (fl & 6) s.write(from([64, 2, p[2 + tl], p[3 + tl]]));
      if (got.length >= n) end();
    }
  });
  await subacked;
  return [done];
}

const report: {
  schemaVersion: number;
  status: string;
  startedAt: string;
  finishedAt?: string;
  error?: string;
  brokerLogs?: string;
  assertions: Record<string, unknown>;
} = { schemaVersion: 1, status: 'failed', startedAt: now(), assertions: {} };
try {
  await rm(outDir, { recursive: true, force: true });
  await mkdir(pkiDir, { recursive: true });
  run(process.execPath, ['--experimental-strip-types', 'scripts/run-go.ts', 'run', './tools/s0-auth-fixture/cmd/generate-central-plant-pki', pkiDir]);
  ca = await rd('ca.pem');
  await chmod(join(pkiDir, 'mqtt-broker-key.pem'), 0o644);
  down();
  compose(['up', '-d', '--build', '--wait', 'mqtt-broker']);
  port = Number(compose(['port', 'mqtt-broker', '8883']).split(':').pop());

  const a = report.assertions, base = 'hvac/v1/+/up', [sUp, sRep] = [`$share/connectivity/${base}/#`, `$share/command-dispatcher/${base}/reply`];
  const uplinks: Array<[string, string]> = [
    [GW, tp(1, UT)],
    [GW, tp(1, UC)],
    [GWB, tp(2, 'up/event')],
    [GWB, tp(2, 'up/status')],
    [GW, tp(1, US)],
    [GW, tp(1, UO)],
  ];
  const expected = uplinks.map(([, t]) => t).sort();
  const [s0] = await sub(ADP, `${base}/#`, expected.length, 15000);
  const [sSysLog] = await sub(ADP, '$SYS/broker/log/N', 1, 5000);
  a.gatewayBReplyWhileAdapterWildcard = await pub(GWB, tp(2, UR));
  if (a.gatewayBReplyWhileAdapterWildcard !== 0 && a.gatewayBReplyWhileAdapterWildcard !== 16) {
    throw new Error(`gatewayBReplyWhileAdapterWildcard: ${a.gatewayBReplyWhileAdapterWildcard}`);
  }
  const aclCases: Array<[string, string, string, number]> = [
    ['gatewayAOwnTopic', ...uplinks[0], 0], ['gatewayAOwnConcentrator', ...uplinks[1], 0], ['gatewayBOwnTopic', ...uplinks[2], 0],
    ['gatewayBOwnStatus', ...uplinks[3], 0], ['gatewayAOwnShadow', ...uplinks[4], 0], ['gatewayAOwnOta', ...uplinks[5], 0],
    ['gatewayAIntoGatewayB', GW, tp(2, UT), 135], ['gatewayAConcIntoB', GW, tp(2, UC), 135], ['gatewayAOtaIntoB', GW, tp(2, UO), 135], ['gatewayAIntoDownlink', GW, tp(1, DC), 135],
    ['gatewayAIntoConcDown', GW, tp(1, DN), 135], ['gatewayAIntoOtaDown', GW, tp(1, DO), 135], ['adapterConcToGatewayB', ADP, tp(2, DN), 16], ['adapterShadowToGatewayB', ADP, tp(2, DS), 16], ['adapterOtaToGatewayB', ADP, tp(2, DO), 16],
    ['commandsToGatewayB', CMD, tp(2, DC), 16], ['commandsIntoShadow', CMD, tp(2, DS), 135], ['commandsIntoOtaDown', CMD, tp(2, DO), 135], ['commandsIntoUplink', CMD, tp(1, UT), 135],
    ['gatewayAIntoControlCrl', GW, '$CONTROL/hvac-crl/v1', 135],
  ];
  for (const [k, id, t, want] of aclCases) if ((a[k] = await pub(id, t)) !== want) throw new Error(`${k}: ${a[k]}`);
  const s0Received = await s0;
  a.connectivityReceived = s0Received;
  eq(s0Received, expected, 'uplinks');
  if ((a.brokerSysLogCount = (await sSysLog).length) < 1) throw new Error('expected at least 1 $SYS/broker/log/N notice');

  const [sAdpRep] = await sub(ADP, `${base}/reply`, 1, 1500);
  await pub(GWB, tp(2, UR));
  const adpRepReceived = await sAdpRep;
  a.adapterReplyBlocked = adpRepReceived;
  eq(adpRepReceived, [], 'adapterReplyBlocked');

  const sharedCases: Array<[string, string, string, Array<[string, string]>, string[]]> = [
    ['sharedConnectivityReceived', ADP, sUp, uplinks, expected],
    ['sharedDispatcherReceived', CMD, sRep, [[GWB, tp(2, UR)]], [tp(2, UR)]],
  ];
  for (const [k, id, f, pubs, want] of sharedCases) {
    const [done] = await sub(id, f, want.length, 15000);
    for (const [pId, t] of pubs) if ((await pub(pId, t)) !== 0) throw new Error(`${k}: ${t}`);
    const received = await done;
    a[k] = received;
    eq(received, want, k);
  }

  const [sGwShadow] = await sub(GWB, tp(2, DS), 1, 5000);
  if ((a.adapterShadowDelivered = await pub(ADP, tp(2, DS))) !== 0) throw new Error(`adapterShadowDelivered: ${a.adapterShadowDelivered}`);
  const gwShadow = await sGwShadow;
  a.gatewayBShadowReceived = gwShadow;
  eq(gwShadow, [tp(2, DS)], 'shadowDownlink');

  const [sGwOta] = await sub(GWB, tp(2, DO), 1, 5000);
  if ((a.adapterOtaDelivered = await pub(ADP, tp(2, DO))) !== 0) throw new Error(`adapterOtaDelivered: ${a.adapterOtaDelivered}`);
  const gwOta = await sGwOta;
  a.gatewayBOtaReceived = gwOta;
  eq(gwOta, [tp(2, DO)], 'otaDownlink');

  const steals = await Promise.all([sUp, sRep, `$share/steal/${tp(2, 'down/#')}`].map((f) => sub(GW, f, 1, 1500)));
  for (const [id, t] of [...uplinks, [GWB, tp(2, UR)], [CMD, tp(2, DC)]]) await pub(id, t);
  const stealReceived = (await Promise.all(steals.map(([d]) => d))).flat();
  a.gatewaySharedStealReceived = stealReceived;
  eq(stealReceived, [], 'steal');

  report.status = 'passed';
  console.log(`MQTT ACL passed: ${rpt}`);
} catch (error) {
  report.error = String(error);
  try { report.brokerLogs = compose(['logs']); } catch {}
  throw error;
} finally {
  for (const s of socks) s.destroy();
  down();
  report.finishedAt = now();
  await mkdir(outDir, { recursive: true });
  await writeFile(rpt, `${JSON.stringify(report, null, 2)}\n`);
}
