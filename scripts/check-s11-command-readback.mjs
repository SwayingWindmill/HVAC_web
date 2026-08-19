import { readFileSync } from 'node:fs';

function read(path) {
  return readFileSync(path, 'utf8');
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

const model = read('libs/commandmodel/model.go');
const edgeMQTT = read('tools/eg8200-simulator/internal/simulator/mqtt_command.go');
const connector = read('services/command-dispatcher/pkg/mqttconnector/connector.go');
const verifier = read('services/command-dispatcher/pkg/commanddispatcher/verification.go');
const service = read('services/command-service/pkg/commandservice/service.go');
const postgresVerification = read('services/command-service/pkg/commandservice/postgres_verification.go');
const connectivityMigration = read('infra/connectivity/postgres/init/002-s11-command-reply-evidence.sql');
const commandMigration = read('services/command-service/migrations/005_s11_edge_execution_evidence.sql');

assert(model.includes('ConnectorExecutionRejected'), 'S11 requires an explicit proven-no-execution connector phase');
assert(connector.includes('const commandSchemaVersion = "2.0"') && edgeMQTT.includes('const mqttCommandSchemaVersion = "2.0"'), 'breaking S11 MQTT command evidence contract must use schemaVersion 2.0 on both ends');
assert(model.includes('type EdgeExecutionEvidence struct') && model.includes('WinnerControllerID') && model.includes('Cycle'), 'S11 requires requested/effective/constraint/winner/cycle Edge evidence');
assert(model.includes('ExpectedVerificationValue') && model.includes('edge.Applied') && model.includes('edge.Effective'), 'Cloud verification must use governed Edge effective/applied numeric value');

assert(edgeMQTT.includes('edgeCommandMayExecute') && edgeMQTT.includes('EDGE_OUTCOME_UNKNOWN'), 'Edge command recovery must persist a MAY_EXECUTE commit point and refuse blind replay');
const reserveIndex = edgeMQTT.indexOf('handler.results[base.CommandID] = edgeCommandRecord');
const durableIndex = edgeMQTT.indexOf('persistEdgeCommandLedger(handler.ledgerPath, handler.results)', reserveIndex);
const submitIndex = edgeMQTT.indexOf('handler.edgeRuntime.SubmitCommand', reserveIndex);
assert(reserveIndex >= 0 && durableIndex > reserveIndex && submitIndex > durableIndex, 'Edge durable commit point must precede physical execution scheduling');
assert(edgeMQTT.includes('request.ExecutionFence <= handler.maxFenceByDevice[deviceID]'), 'Edge must reject stale or same-fence different commands');
assert(!edgeMQTT.includes('base.EdgeStatus = "VERIFIED"'), 'Edge execution evidence must never declare Cloud business verification');
assert(!edgeMQTT.includes('Reported map[string]float64'), 'legacy unstructured Edge reported-value shortcut must remain removed');

assert(connector.includes('case "EXECUTED":') && connector.includes('ConnectorAcknowledged'), 'only governed EXECUTED evidence may enter Cloud acknowledgement');
assert(connector.includes('case "REJECTED", "EXPIRED":') && connector.includes('ConnectorExecutionRejected'), 'proven Edge rejection/expiry must be distinguished from unknown outcome');
assert(connector.includes('case "FAILED", "TIMEOUT":') && connector.includes('ConnectorRequestCommitted'), 'ambiguous Edge failure/timeout must remain OUTCOME_UNKNOWN-capable');
assert(!connector.includes('case "DEVICE_ACK", "EXECUTED", "VERIFIED":'), 'transport/device ACK or Edge VERIFIED must not be accepted as business progress');

assert(verifier.includes('ExpectedVerificationValue(envelope.Capability, envelope.Parameters, envelope.EdgeExecution)'), 'independent verifier must use durable Edge execution target');
assert(service.includes('result.EdgeExecution == nil') && service.includes('IntentFailed'), 'Cloud must reject naked ACK and represent proven Edge rejection without rewriting approval facts');
assert(postgresVerification.includes('ce.edge_execution_evidence') && postgresVerification.includes('envelope.EdgeExecution = &edgeExecution'), 'restart recovery must reload Edge execution evidence before readback verification');

assert(connectivityMigration.includes('reply_execution_evidence jsonb'), 'Connectivity must durably persist Edge reply execution evidence');
assert(commandMigration.includes('edge_execution_evidence jsonb') && commandMigration.includes("'EXECUTION_REJECTED'"), 'Command evidence must persist Edge execution evidence and proven rejection phase');

console.log('S11 governed command/readback architecture check passed.');
