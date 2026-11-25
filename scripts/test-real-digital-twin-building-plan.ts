import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  buildingTerminals,
  resolveBuildingRiser,
  resolveCorridorBand,
  resolveEntranceX,
  resolveFrontGrounds,
  resolveHallBranches,
  resolveRoofAnchors,
  type TwinBuildingPlan,
} from '../apps/hvac-web/src/features/operations/digital-twin/twin-building.ts';

const songyaxuan = JSON.parse(
  readFileSync(new URL('../deploy/sites/songyaxuan.site.json', import.meta.url), 'utf8'),
) as { site: { buildingPlan?: TwinBuildingPlan } };

test('songyaxuan site manifest buildingPlan derives the expected 23 terminals and anchors', () => {
  const plan = songyaxuan.site.buildingPlan;
  assert.ok(plan);
  const terminals = buildingTerminals(plan);
  assert.equal(terminals.length, 23);
  assert.equal(terminals.filter((t) => t.equipment.kind === 'ahu').length, 1);
  assert.equal(terminals.filter((t) => t.equipment.kind === 'vav').length, 6);
  assert.equal(terminals.filter((t) => t.equipment.kind === 'fcu').length, 16);

  const riser = resolveBuildingRiser(plan);
  assert.deepEqual(riser.supply, [27, 7]);
  assert.deepEqual(riser.return, [26.5, 7.6]);
  assert.deepEqual(riser.fresh, [27, 6.35]);
  assert.equal(riser.shaftExitX, 25.4);
  assert.equal(resolveEntranceX(plan), 11);

  const f2Band = resolveCorridorBand(plan.floors[1]!, plan);
  assert.equal(f2Band.supplyZ, 9);
  assert.equal(f2Band.returnZ, 9.35);
  assert.equal(f2Band.freshZ, 10);
});

test('arbitrary smaller buildingPlan keeps all derived anchors strictly inside building bounds', () => {
  const siteB: TwinBuildingPlan = {
    name: '二号分店',
    width: 16,
    depth: 12,
    floors: [
      {
        level: 1,
        label: '1F',
        height: 4.5,
        rooms: [
          { id: '1-shaft', name: '管井', kind: 'shaft', x: [0, 2], z: [4, 6] },
          { id: '1-ahu', name: '机房', kind: 'plantRoom', x: [0, 4], z: [6, 10] },
          { id: '1-hall', name: '餐厅', kind: 'hall', x: [4, 16], z: [0, 12] },
        ],
      },
      {
        level: 2,
        label: '2F',
        height: 4.0,
        rooms: [
          { id: '2-shaft', name: '管井', kind: 'shaft', x: [0, 2], z: [4, 6] },
          { id: '2-corridor', name: '走廊', kind: 'corridor', x: [2, 16], z: [5, 7] },
          { id: '2-r1', name: '201', kind: 'private', x: [2, 9], z: [0, 5] },
          { id: '2-r2', name: '202', kind: 'smallPrivate', x: [9, 16], z: [7, 12] },
        ],
      },
    ],
  };

  const terminals = buildingTerminals(siteB);
  const hallBranches = resolveHallBranches(siteB.floors[0]!.rooms[2]!);
  assert.equal(hallBranches.length, 2);
  assert.equal(terminals.length, 1 + hallBranches.length * 2 + 2);
  for (const x of hallBranches) assert.ok(x > 4 && x < 16);

  const riser = resolveBuildingRiser(siteB);
  assert.ok(riser.supply[0] >= 0 && riser.supply[0] <= siteB.width);
  assert.ok(riser.supply[1] >= 0 && riser.supply[1] <= siteB.depth);
  assert.ok(riser.shaftExitX > 2 && riser.shaftExitX < siteB.width);

  const roof = resolveRoofAnchors(siteB);
  assert.ok(roof.pau[0] > 0 && roof.pau[0] < siteB.width);
  assert.ok(roof.exhaustFans.length >= 1);

  const grounds = resolveFrontGrounds(siteB, -41, -25);
  assert.ok(grounds.plaza[0] >= -41 && grounds.plaza[1] <= -25);
});
