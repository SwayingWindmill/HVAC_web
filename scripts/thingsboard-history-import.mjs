// Imports a Site's ThingsBoard telemetry history through Telemetry Runtime's Historical Replay path,
// before ThingsBoard is retired. Two steps, because ThingsBoard and the platform run side by side:
//
//   1. export (on the server):  node scripts/thingsboard-history-import.mjs export <site file> --until <instant> --out <dir>
//      reads ThingsBoard's ts_kv for every device the site file names, up to the moment live
//      platform telemetry began, into one CSV per device.
//   2. import (inside the platform's application network, with the historical-replay-runner identity):
//      node scripts/thingsboard-history-import.mjs import <site file> --in <dir> --dataset <uuidv7> \
//        --telemetry-url https://telemetry-runtime-service:8446 --cert … --key … --ca … [--rate <per second>] [--resume <file>]
//
// --resume takes a JSON object of Device id → last offset already replayed (the maximum source_offset
// of the dataset's partition for that device), so an interrupted import continues where it stopped.
//
// Each device replays in its own partition in sampled order, so offsets only grow; a rerun with the
// same dataset is refused as duplicates rather than doubled. Key names and the unconnected-probe rule
// match the Gateway's Node-RED function (edge/node-red/songyaxuan-platform-envelope.js).
import { createReadStream, mkdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import https from 'node:https';
import path from 'node:path';
import readline from 'node:readline';
import { parseArgs } from 'node:util';
import { expandPoints } from './site-onboarding.mjs';

// ThingsBoard key → platform point code, per device kind; keys absent here are not imported.
const KEY_RENAMES = {
  flowMeter: 'flow_meter',
  flowVelocity: 'flow_velocity',
  powerTotal: 'power_total',
  combinedActiveTotalElectricalEnergy: 'active_energy_total',
};

export function platformObservation(device, point, key, value) {
  if (!Number.isFinite(value)) return null;
  // An unconnected temperature input reads exactly 0; it is not a 0 °C measurement.
  const wireQuality = device.type === 'TEMPERATURE_MODULE' && value === 0 ? 1 : 0;
  return { telemetryKey: point.code, value, valueType: point.valueType, unit: point.unit ?? null, wireQuality };
}

function siteDevices(profile) {
  return profile.devices.map((device) => {
    const points = new Map(expandPoints(device, profile.meterPoints).map((point) => [point.code, point]));
    return { ...device, pointFor: (key) => points.get(KEY_RENAMES[key] ?? key) };
  });
}

function exportHistory(profile, until, out) {
  mkdirSync(out, { recursive: true });
  for (const device of siteDevices(profile)) {
    const sql = `COPY (SELECT t.ts, k.key, COALESCE(t.dbl_v, t.long_v::double precision) FROM ts_kv t JOIN device d ON d.id = t.entity_id JOIN key_dictionary k ON k.key_id = t.key WHERE d.name = '${device.sourceKey}' AND t.ts < ${until.getTime()} AND COALESCE(t.dbl_v, t.long_v::double precision) IS NOT NULL ORDER BY t.ts, k.key) TO STDOUT WITH CSV`;
    const result = spawnSync('docker', ['exec', 'thingsboard-ce', 'psql', '-h', 'localhost', '-U', 'thingsboard', '-d', 'thingsboard', '-c', sql], { maxBuffer: 1 << 30 });
    if (result.status !== 0) throw new Error(`export ${device.sourceKey}: ${result.stderr}`);
    writeFileSync(path.join(out, `${device.sourceKey}.csv`), result.stdout);
    console.log(`${device.sourceKey}: ${result.stdout.toString().split('\n').filter(Boolean).length} rows`);
  }
}

async function importHistory(profile, options) {
  const agent = new https.Agent({ keepAlive: true, maxSockets: 16, cert: readFileSync(options.cert), key: readFileSync(options.key), ca: readFileSync(options.ca), servername: options.serverName });
  const endpoint = new URL('/internal/v1/telemetry/history-replay/observations:accept', options.telemetryUrl);
  const post = (body) => new Promise((resolve, reject) => {
    const request = https.request(endpoint, { method: 'POST', agent, headers: { 'Content-Type': 'application/json' } }, (response) => {
      let text = '';
      response.on('data', (chunk) => { text += chunk; });
      response.on('end', () => resolve({ status: response.statusCode, text }));
    });
    request.on('error', reject);
    request.end(JSON.stringify(body));
  });
  // --rate caps observations per second across all devices, so live ingestion on a small server keeps up.
  let nextSlot = 0;
  const pace = async () => {
    if (!options.rate) return;
    const now = Date.now();
    const at = Math.max(now, nextSlot);
    nextSlot = at + 1000 / options.rate;
    if (at > now) await new Promise((resolve) => setTimeout(resolve, at - now));
  };
  const send = async (body) => {
    for (let attempt = 1; ; attempt += 1) {
      await pace();
      let reply;
      try {
        reply = await post(body);
      } catch (error) {
        // The service closes idle keep-alive connections; a dropped connection is sent again.
        if (attempt === 40) throw error;
        await new Promise((resolve) => setTimeout(resolve, Math.min(5000, attempt * 250)));
        continue;
      }
      const { status, text } = reply;
      if (status === 200) return JSON.parse(text);
      if ((status !== 429 && status !== 503) || attempt === 40) throw new Error(`replay refused (HTTP ${status}): ${text.slice(0, 300)}`);
      await new Promise((resolve) => setTimeout(resolve, Math.min(5000, attempt * 250)));
    }
  };

  const tally = {};
  await Promise.all(siteDevices(profile).map(async (device) => {
    const file = path.join(options.in, `${device.sourceKey}.csv`);
    if (!existsSync(file)) return;
    const counts = tally[device.sourceKey] = { sent: 0, skipped: 0, resumed: 0, outcomes: {} };
    // --resume names the last offset each device already reached, so a restart continues there.
    const reached = options.resume?.[device.id] ?? 0;
    let offset = 0;
    for await (const line of readline.createInterface({ input: createReadStream(file) })) {
      const [ts, key, raw] = line.split(',');
      const point = device.pointFor(key);
      const observation = point ? platformObservation(device, point, key, Number(raw)) : null;
      if (!observation) { counts.skipped += 1; continue; }
      offset += 1;
      if (offset <= reached) { counts.resumed += 1; continue; }
      const result = await send({ replayDatasetId: options.dataset, deviceId: device.id, ...observation, sampledAt: new Date(Number(ts)).toISOString(), offset });
      const outcome = result.status ?? result.outcome ?? 'OK';
      counts.outcomes[outcome] = (counts.outcomes[outcome] ?? 0) + 1;
      counts.sent += 1;
      if (counts.sent % 50000 === 0) console.log(`${device.sourceKey}: ${counts.sent} sent`);
    }
    console.log(`${device.sourceKey}: done`, JSON.stringify(counts));
  }));
  console.log(JSON.stringify(tally));
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const [step, siteFile, ...rest] = process.argv.slice(2);
  const { values } = parseArgs({ args: rest, options: {
    until: { type: 'string' }, out: { type: 'string' }, in: { type: 'string' }, dataset: { type: 'string' },
    'telemetry-url': { type: 'string' }, 'server-name': { type: 'string', default: 'telemetry-runtime-service' },
    cert: { type: 'string' }, key: { type: 'string' }, ca: { type: 'string' },
    resume: { type: 'string' }, rate: { type: 'string' },
  } });
  const profile = JSON.parse(readFileSync(siteFile, 'utf8'));
  if (step === 'export') {
    const until = new Date(values.until ?? '');
    if (Number.isNaN(until.getTime()) || !values.out) throw new Error('export needs --until <instant> and --out <dir>');
    exportHistory(profile, until, values.out);
  } else if (step === 'import') {
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(values.dataset ?? '')) throw new Error('import needs --dataset <uuidv7>');
    await importHistory(profile, { in: values.in, dataset: values.dataset, telemetryUrl: values['telemetry-url'], serverName: values['server-name'], cert: values.cert, key: values.key, ca: values.ca,
      resume: values.resume ? JSON.parse(readFileSync(values.resume, 'utf8')) : undefined, rate: values.rate ? Number(values.rate) : undefined });
  } else {
    throw new Error('usage: thingsboard-history-import.mjs export|import <site file> …');
  }
}
