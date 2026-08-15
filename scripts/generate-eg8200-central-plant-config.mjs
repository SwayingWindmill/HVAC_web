import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { buildCentralPlantSimulatorConfig } from './central-plant-spatial-model.mjs';

const root = process.cwd();
const check = process.argv.includes('--check');
const pointContractPath = resolve(root, 'contracts/registry/central-plant-device-points.v2.json');
const outputPath = resolve(root, 'tools/eg8200-simulator/configs/central-plant.local.json');
const pointContract = JSON.parse(await readFile(pointContractPath, 'utf8'));
const expected = `${JSON.stringify(buildCentralPlantSimulatorConfig(pointContract), null, 2)}\n`;

if (check) {
  const actual = await readFile(outputPath, 'utf8');
  if (actual !== expected) {
    throw new Error('EG8200 central plant config is stale; run npm run eg8200:config:generate');
  }
  console.log('EG8200 central plant spatial Sensor/Point config is current.');
} else {
  await writeFile(outputPath, expected, 'utf8');
  console.log(`Generated ${outputPath}`);
}
