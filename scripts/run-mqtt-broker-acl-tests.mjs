import { spawn, spawnSync } from 'node:child_process';
import { chmod, mkdir, rm, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const outputDir = resolve(root, process.env.MQTT_ACL_REPORT_DIR ?? 'out/mqtt-broker-acl');
const pkiDir = join(outputDir, 'pki');
const reportPath = join(outputDir, 'acl.json');
const composePath = resolve(root, 'infra/telemetry/mqtt/compose.yaml');
const projectName = `hvac-mqtt-acl-${process.pid}`;
const gatewayA = '018f3e00-4000-7000-8000-000000000100';
const gatewayB = '018f3e00-4000-7000-8000-000000000200';
const composeEnvironment = { ...process.env, MQTT_PKI_DIR: pkiDir, MQTT_HOST_PORT: '0' };

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr?.trim() ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function clientArgs(identity) {
  return [
    '-h', 'localhost', '-p', '8883', '-V', 'mqttv5', '-q', '1',
    '--cafile', '/mosquitto/config/pki/ca.pem',
    '--cert', `/mosquitto/config/pki/${identity}-cert.pem`,
    '--key', `/mosquitto/config/pki/${identity}-key.pem`,
  ];
}

function publish(identity, topic) {
  const result = spawnSync('docker', ['compose', '-p', projectName, '-f', composePath, 'exec', '-T', 'mqtt-broker',
    'mosquitto_pub', '-d', ...clientArgs(identity), '-t', topic, '-m', '{}'], { cwd: root, env: composeEnvironment, encoding: 'utf8', windowsHide: true });
  const output = `${result.stdout ?? ''}\n${result.stderr ?? ''}`;
  const reason = /PUBACK[^\n]*?\(?RC:?\s*(\d+)/i.exec(output) ?? /Received PUBACK \(Mid: \d+, RC:(\d+)\)/.exec(output);
  if (!reason) throw new Error(`no PUBACK from ${identity} publishing ${topic}:\n${output}`);
  return Number(reason[1]);
}

const report = { schemaVersion: 1, status: 'failed', startedAt: new Date().toISOString(), assertions: {} };
let subscriber;
try {
  await rm(outputDir, { recursive: true, force: true });
  await mkdir(pkiDir, { recursive: true });
  run(process.execPath, ['scripts/run-go.mjs', 'run', './tools/s0-auth-fixture/cmd/generate-central-plant-pki', pkiDir]);
  await chmod(join(pkiDir, 'mqtt-broker-key.pem'), 0o644);
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', '--wait', 'mqtt-broker']);

  subscriber = spawn('docker', ['compose', '-p', projectName, '-f', composePath, 'exec', '-T', 'mqtt-broker',
    'mosquitto_sub', ...clientArgs('mqtt-adapter'), '-t', 'hvac/v1/+/up/#', '-v', '-C', '3', '-W', '20'], { cwd: root, env: composeEnvironment, windowsHide: true });
  let received = '';
  subscriber.stdout.on('data', (chunk) => { received += chunk.toString(); });
  const subscriberDone = new Promise((resolveDone) => subscriber.on('exit', resolveDone));
  await new Promise((resolveWait) => setTimeout(resolveWait, 1500));

  report.assertions.gatewayAOwnTopic = publish('mqtt-gateway', `hvac/v1/${gatewayA}/up/telemetry`);
  report.assertions.gatewayAOwnConcentrator = publish('mqtt-gateway', `hvac/v1/${gatewayA}/up/concentrator`);
  report.assertions.gatewayBOwnTopic = publish('mqtt-gateway-b', `hvac/v1/${gatewayB}/up/event`);
  report.assertions.gatewayAIntoGatewayB = publish('mqtt-gateway', `hvac/v1/${gatewayB}/up/telemetry`);
  report.assertions.gatewayAConcIntoB = publish('mqtt-gateway', `hvac/v1/${gatewayB}/up/concentrator`);
  report.assertions.gatewayAIntoDownlink = publish('mqtt-gateway', `hvac/v1/${gatewayA}/down/command`);
  report.assertions.gatewayAIntoConcDown = publish('mqtt-gateway', `hvac/v1/${gatewayA}/down/concentrator`);
  report.assertions.adapterConcToGatewayB = publish('mqtt-adapter', `hvac/v1/${gatewayB}/down/concentrator`);
  report.assertions.commandsToGatewayB = publish('command-dispatcher', `hvac/v1/${gatewayB}/down/command`);
  report.assertions.commandsIntoUplink = publish('command-dispatcher', `hvac/v1/${gatewayA}/up/telemetry`);
  await subscriberDone;
  report.assertions.connectivityReceived = received.trim().split('\n').map((line) => line.split(' ')[0]).sort();

  const a = report.assertions;
  if (a.gatewayAOwnTopic !== 0 || a.gatewayAOwnConcentrator !== 0 || a.gatewayBOwnTopic !== 0) {
    throw new Error(`gateway uplink denied: ${JSON.stringify(a)}`);
  }
  if (a.gatewayAIntoGatewayB !== 135 || a.gatewayAConcIntoB !== 135 || a.gatewayAIntoDownlink !== 135 || a.gatewayAIntoConcDown !== 135) {
    throw new Error(`gateway escaped confinement: ${JSON.stringify(a)}`);
  }
  if (a.adapterConcToGatewayB !== 16 || a.commandsToGatewayB !== 16 || a.commandsIntoUplink !== 135) {
    throw new Error(`connectivity downlink ACL broken: ${JSON.stringify(a)}`);
  }
  const expected = [`hvac/v1/${gatewayA}/up/concentrator`, `hvac/v1/${gatewayA}/up/telemetry`, `hvac/v1/${gatewayB}/up/event`];
  if (JSON.stringify(a.connectivityReceived) !== JSON.stringify(expected)) {
    throw new Error(`adapter missed expected uplinks: ${received}`);
  }
  report.status = 'passed';
  console.log(`MQTT broker ACL evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  try { report.brokerLogs = compose(['logs', '--no-color', 'mqtt-broker']); } catch {}
  throw error;
} finally {
  subscriber?.kill();
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
}
