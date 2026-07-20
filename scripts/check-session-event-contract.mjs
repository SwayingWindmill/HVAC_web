import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const [protoText, lockText] = await Promise.all([
  readFile(resolve(root, 'contracts/events/session-audit.v1.proto'), 'utf8'),
  readFile(resolve(root, 'contracts/events/session-audit.v1.lock.json'), 'utf8'),
]);
const lock = JSON.parse(lockText);

function invariant(condition, message) {
  if (!condition) throw new Error(`Session event compatibility check failed: ${message}`);
}

const syntax = protoText.match(/syntax\s*=\s*"([^"]+)"\s*;/)?.[1];
const packageName = protoText.match(/package\s+([A-Za-z0-9_.]+)\s*;/)?.[1];
const goPackage = protoText.match(/option\s+go_package\s*=\s*"([^"]+)"\s*;/)?.[1];
invariant(syntax === lock.syntax, `syntax changed from ${lock.syntax} to ${syntax}`);
invariant(packageName === lock.package, `package changed from ${lock.package} to ${packageName}`);
invariant(goPackage === lock.goPackage, 'Go package changed');

const parsedMessages = {};
for (const messageMatch of protoText.matchAll(/message\s+([A-Za-z0-9_]+)\s*\{([\s\S]*?)\}/g)) {
  const [, messageName, body] = messageMatch;
  const fields = {};
  const usedNumbers = new Set();
  for (const fieldMatch of body.matchAll(/^\s*([A-Za-z0-9_.]+)\s+([a-z][a-z0-9_]*)\s*=\s*([0-9]+)\s*;/gm)) {
    const [, type, name, rawNumber] = fieldMatch;
    const number = Number(rawNumber);
    invariant(!usedNumbers.has(number), `${messageName} reuses field number ${number}`);
    usedNumbers.add(number);
    fields[name] = { type, number };
  }
  parsedMessages[messageName] = fields;
}

for (const [messageName, lockedFields] of Object.entries(lock.messages)) {
  const actualFields = parsedMessages[messageName];
  invariant(actualFields, `locked message ${messageName} was removed`);
  for (const [fieldName, lockedField] of Object.entries(lockedFields)) {
    const actualField = actualFields[fieldName];
    invariant(actualField, `${messageName}.${fieldName} was removed or renamed`);
    invariant(actualField.number === lockedField.number, `${messageName}.${fieldName} changed field number ${lockedField.number} -> ${actualField.number}`);
    invariant(actualField.type === lockedField.type, `${messageName}.${fieldName} changed type ${lockedField.type} -> ${actualField.type}`);
  }
}

for (const [messageName, fields] of Object.entries(parsedMessages)) {
  const lockedFields = lock.messages[messageName];
  invariant(lockedFields, `new message ${messageName} requires an explicit versioned compatibility decision`);
  for (const fieldName of Object.keys(fields)) {
    invariant(lockedFields[fieldName], `new field ${messageName}.${fieldName} requires updating the compatibility lock and schema version`);
  }
}

console.log('Session Audit Protobuf compatibility check passed.');
