import type { TwinBuildingPlan } from '../twin-building';
import { equipmentAsset } from './assets/equipment';
import type { V3 } from './assets/kit';

// The site as a model on a plinth: the building in the middle, the plant room on its left with the
// cooling towers on a pad in front of it, roads along the front and right, parking, lawns and trees.
// Plant positions come from how many of each machine the Registry has; water runs through headers in
// the plant room like Songyaxuan's: pump suction low, pump discharge at mid height, condenser return
// and chilled-water supply overhead, and the chilled-water mains crossing to the building's riser.
// Metres; x to the right, z towards the viewer, y up.

export type CameraView = 'overview' | 'plant' | 'terminal';

export interface Rect {
  readonly x: readonly [number, number];
  readonly z: readonly [number, number];
}

export interface Run {
  readonly points: readonly V3[];
  readonly radius: number;
}

export interface Fitting {
  readonly at: V3;
  /** The pipe's direction through the fitting. */
  readonly axis: V3;
  readonly radius: number;
}

export interface Placement {
  readonly at: V3;
  readonly turn?: number;
}

export interface WaterNetwork {
  readonly runs: readonly Run[];
  readonly valves: readonly Fitting[];
}

export interface SiteLayout {
  readonly plinth: Rect;
  readonly roads: readonly Rect[];
  readonly markings: readonly Rect[];
  readonly curbs: readonly Rect[];
  readonly lawns: readonly Rect[];
  readonly pavings: readonly Rect[];
  readonly trees: readonly Placement[];
  readonly shrubs: readonly Placement[];
  readonly cars: readonly Placement[];
  readonly lamps: readonly Placement[];
  /** The neighbourhood around the site: plain white blocks, centre and size. */
  readonly blocks: readonly (readonly [V3, V3])[];
  /** Fence runs around the site, start and end on the ground. */
  readonly fences: readonly (readonly [V3, V3])[];
  readonly plantRoom: Rect & { readonly height: number };
  readonly towerPad: Rect;
  readonly chillers: readonly V3[];
  readonly coolingPumps: readonly V3[];
  readonly chilledPumps: readonly V3[];
  readonly towers: readonly V3[];
  readonly cooling: WaterNetwork;
  readonly chilled: WaterNetwork;
  /** Steel posts carrying the mains between plant room and building: base and height. */
  readonly racks: readonly (readonly [V3, number])[];
  /** Where the building's back-left corner stands, if the site declares one. */
  readonly buildingOrigin: V3 | null;
  /** The chilled-water riser in the building's shaft: supply and return, world positions at the mains' height. */
  readonly riser: { readonly supply: V3; readonly return: V3 };
  readonly frames: Readonly<Record<CameraView, readonly [V3, V3]>>;
}

export interface EquipmentCounts {
  readonly chillers: number;
  readonly coolingPumps: number;
  readonly chilledPumps: number;
  readonly towers: number;
}

const ROW_PITCH = 3.0;
const TOWER_PITCH = 4.2;
const CHILLER_X = -10.5;
const COOLING_PUMP_X = -18.9;
const CHILLED_PUMP_X = -15.2;
const PLANT_X: readonly [number, number] = [-22.6, -7];
const PLANT_BACK = -12;
/** The building stands left of the plant room, a 3 m pipe bridge between them. */
const BUILDING_GAP = 3;
/** The riser's place in the building's shaft (building coordinates). */
export const RISER_LOCAL = { supply: [27.0, 7.0] as const, return: [26.5, 7.6] as const };

const add = (a: V3, b: V3): V3 => [a[0] + b[0], a[1] + b[1], a[2] + b[2]];

export function buildSiteLayout(counts: EquipmentCounts, plan: TwinBuildingPlan | null): SiteLayout {
  const rows = Math.max(counts.chillers, counts.coolingPumps, counts.chilledPumps, 1);
  const rowZ = (index: number) => PLANT_BACK + 2.4 + index * ROW_PITCH;
  const plantFront = PLANT_BACK + 2.4 + (rows - 1) * ROW_PITCH + 2.6;
  const chillers = Array.from({ length: counts.chillers }, (_, index): V3 => [CHILLER_X, 0.15, rowZ(index)]);
  const coolingPumps = Array.from({ length: counts.coolingPumps }, (_, index): V3 => [COOLING_PUMP_X, 0, rowZ(index)]);
  const chilledPumps = Array.from({ length: counts.chilledPumps }, (_, index): V3 => [CHILLED_PUMP_X, 0, rowZ(index)]);
  const towerZ = plantFront + 6.5;
  const plantCentre = (PLANT_X[0] + PLANT_X[1]) / 2;
  const towers = Array.from({ length: counts.towers }, (_, index): V3 => [plantCentre + (index - (counts.towers - 1) / 2) * TOWER_PITCH, 0.25, towerZ]);

  const chiller = equipmentAsset('chiller').ports;
  const pump = equipmentAsset('pump').ports;
  const tower = equipmentAsset('tower').ports;
  const firstZ = rowZ(0);
  const lastZ = rowZ(rows - 1);

  // ── Cooling water: towers → suction header → pumps → discharge header → condensers → return rack → towers.
  const cooling: { runs: Run[]; valves: Fitting[] } = { runs: [], valves: [] };
  const coolSuctionX = COOLING_PUMP_X - 1.9;
  const coolDischargeX = COOLING_PUMP_X + pump.discharge[0];
  const coolReturnX = -13.6;
  const collectorZ = towerZ - 2.9;
  const distributionZ = plantFront + 2.6;
  const towerXs = towers.map((position) => position[0]);
  if (towers.length) {
    for (const position of towers) {
      const out = add(position, tower.waterOut);
      cooling.runs.push({ points: [out, [out[0], out[1], collectorZ]], radius: 0.1 });
      const inlet = add(position, tower.waterIn);
      cooling.runs.push({ points: [[inlet[0], 3.2, distributionZ], [inlet[0], 3.2, inlet[2] - 0.6], [inlet[0], inlet[1], inlet[2] - 0.6], inlet], radius: 0.1 });
      cooling.valves.push({ at: [inlet[0], inlet[1], inlet[2] - 0.3], axis: [0, 0, 1], radius: 0.1 });
    }
    const collectorY = towers[0][1] + tower.waterOut[1];
    cooling.runs.push({ points: [[Math.max(...towerXs), collectorY, collectorZ], [coolSuctionX, collectorY, collectorZ], [coolSuctionX, collectorY, collectorZ - 1.2], [coolSuctionX, 0.64, collectorZ - 1.8], [coolSuctionX, 0.64, firstZ - 0.4]], radius: 0.13 });
    cooling.runs.push({ points: [[Math.min(...towerXs), 3.2, distributionZ], [Math.max(...towerXs), 3.2, distributionZ]], radius: 0.13 });
  }
  cooling.runs.push({ points: [[coolDischargeX, 2.4, firstZ - 0.6], [coolDischargeX, 2.4, lastZ + 0.4]], radius: 0.13 });
  cooling.runs.push({ points: [[coolReturnX, 3.2, firstZ - 0.6], [coolReturnX, 3.2, distributionZ]], radius: 0.13 });
  coolingPumps.forEach((position) => {
    const suction = add(position, pump.suction);
    cooling.runs.push({ points: [[coolSuctionX, suction[1], suction[2]], suction], radius: 0.09 });
    cooling.valves.push({ at: [suction[0] - 0.45, suction[1], suction[2]], axis: [1, 0, 0], radius: 0.09 });
    const discharge = add(position, pump.discharge);
    cooling.runs.push({ points: [discharge, [discharge[0], 2.4, discharge[2]]], radius: 0.08 });
    cooling.valves.push({ at: [discharge[0], 1.55, discharge[2]], axis: [0, 1, 0], radius: 0.08 });
  });
  chillers.forEach((position) => {
    const inlet = add(position, chiller.cwIn);
    cooling.runs.push({ points: [[coolDischargeX, 2.4, inlet[2]], [inlet[0] - 0.75, 2.4, inlet[2]], [inlet[0] - 0.75, inlet[1], inlet[2]], inlet], radius: 0.075 });
    cooling.valves.push({ at: [inlet[0] - 0.35, inlet[1], inlet[2]], axis: [1, 0, 0], radius: 0.075 });
    const outlet = add(position, chiller.cwOut);
    cooling.runs.push({ points: [outlet, [coolReturnX, outlet[1], outlet[2]], [coolReturnX, 3.2, outlet[2]]], radius: 0.075 });
  });

  // ── Chilled water: building return → suction header → pumps → discharge header → evaporators → supply rack → building.
  const chilled: { runs: Run[]; valves: Fitting[] } = { runs: [], valves: [] };
  const origin: V3 | null = plan ? [PLANT_X[0] - BUILDING_GAP - plan.width, 0, PLANT_BACK] : null;
  const riserSupply: V3 = origin ? [origin[0] + RISER_LOCAL.supply[0], 3.8, origin[2] + RISER_LOCAL.supply[1]] : [PLANT_X[0] - 1.5, 3.8, PLANT_BACK + 3];
  const riserReturn: V3 = origin ? [origin[0] + RISER_LOCAL.return[0], 4.4, origin[2] + RISER_LOCAL.return[1]] : [PLANT_X[0] - 1.5, 4.4, PLANT_BACK + 3.6];
  const chillSuctionX = CHILLED_PUMP_X - 1.7;
  const chillDischargeX = CHILLED_PUMP_X + pump.discharge[0];
  const chillSupplyX = -14.3;
  chilled.runs.push({ points: [[chillSuctionX, 0.64, firstZ - 0.4], [chillSuctionX, 0.64, lastZ + 0.4]], radius: 0.13 });
  chilled.runs.push({ points: [[chillSuctionX, 0.64, riserReturn[2]], [chillSuctionX, riserReturn[1], riserReturn[2]], riserReturn], radius: 0.13 });
  chilled.runs.push({ points: [[chillDischargeX, 2.0, firstZ - 0.4], [chillDischargeX, 2.0, lastZ + 0.6]], radius: 0.13 });
  chilled.runs.push({ points: [[chillSupplyX, 3.8, firstZ], [chillSupplyX, 3.8, lastZ + 0.6]], radius: 0.13 });
  chilled.runs.push({ points: [[chillSupplyX, 3.8, riserSupply[2]], riserSupply], radius: 0.13 });
  chilledPumps.forEach((position) => {
    const suction = add(position, pump.suction);
    chilled.runs.push({ points: [[chillSuctionX, suction[1], suction[2]], suction], radius: 0.09 });
    chilled.valves.push({ at: [suction[0] - 0.35, suction[1], suction[2]], axis: [1, 0, 0], radius: 0.09 });
    const discharge = add(position, pump.discharge);
    chilled.runs.push({ points: [discharge, [discharge[0], 2.0, discharge[2]]], radius: 0.08 });
    chilled.valves.push({ at: [discharge[0], 1.5, discharge[2]], axis: [0, 1, 0], radius: 0.08 });
  });
  chillers.forEach((position) => {
    const inlet = add(position, chiller.chwIn);
    chilled.runs.push({ points: [[chillDischargeX, 2.0, inlet[2]], [inlet[0] - 0.55, 2.0, inlet[2]], [inlet[0] - 0.55, inlet[1], inlet[2]], inlet], radius: 0.075 });
    chilled.valves.push({ at: [inlet[0] - 0.3, inlet[1], inlet[2]], axis: [1, 0, 0], radius: 0.075 });
    const outlet = add(position, chiller.chwOut);
    chilled.runs.push({ points: [outlet, [chillSupplyX, outlet[1], outlet[2]], [chillSupplyX, 3.8, outlet[2]]], radius: 0.075 });
  });

  // ── Grounds.
  const buildingRect: Rect | null = plan && origin ? { x: [origin[0], origin[0] + plan.width], z: [origin[2], origin[2] + plan.depth] } : null;
  const right = PLANT_X[1] + 6;
  const front = Math.max(towerZ + 6, buildingRect ? buildingRect.z[1] + 12 : 0);
  const plinth: Rect = { x: [(buildingRect?.x[0] ?? PLANT_X[0]) - 7, right + 6.5], z: [PLANT_BACK - 5, front + 6.5] };
  // The world runs past the frame: roads continue into the fog instead of ending at a plinth edge.
  const far = 160;
  const roads: Rect[] = [
    { x: [plinth.x[0] - far, plinth.x[1] + far], z: [front, front + 5.5] },
    { x: [right, right + 5.5], z: [plinth.z[0] - far, front] },
  ];
  const markings: Rect[] = [];
  for (let x = plinth.x[0] - far; x < plinth.x[1] + far; x += 3.2) markings.push({ x: [x, x + 1.6], z: [front + 2.68, front + 2.82] });
  for (let z = plinth.z[0] - far; z < front - 1; z += 3.2) markings.push({ x: [right + 2.68, right + 2.82], z: [z, z + 1.6] });
  const curbs: Rect[] = [
    { x: [plinth.x[0] - far, right], z: [front - 1.4, front] },
    { x: [right - 1.4, right], z: [plinth.z[0] - far, front - 1.4] },
    { x: [plinth.x[0] - far, plinth.x[1] + far], z: [front + 5.5, front + 6.9] },
  ];
  const pavings: Rect[] = [];
  const cars: Placement[] = [];
  const lawns: Rect[] = [
    { x: [plinth.x[0] + 0.6, right - 2], z: [plinth.z[0] + 0.6, PLANT_BACK - 1.6] },
    { x: [PLANT_X[1] + 1.2, right - 2], z: [PLANT_BACK - 1.6, front - 2] },
    { x: [PLANT_X[0] - 1, PLANT_X[1] + 1.2], z: [plantFront + 1.0, front - 2] },
  ];
  const trees: Placement[] = [];
  const shrubs: Placement[] = [];
  const lamps: Placement[] = [];
  for (let x = plinth.x[0] + 3; x < right - 2; x += 6.5) trees.push({ at: [x, 0, plinth.z[0] + 2.4], turn: x });
  // Low planting only beside the plant room: trees there would stand between the viewer and the machines.
  for (let z = PLANT_BACK + 1; z < front - 3; z += 3.2) shrubs.push({ at: [right - 3.2, 0, z], turn: z });
  for (let x = plinth.x[0] + 6; x < right; x += 12) lamps.push({ at: [x, 0, front - 0.7], turn: Math.PI / 2 });
  // The neighbourhood: rows of white blocks across the roads and behind the site, street trees and a
  // few cars, so the site sits in a town rather than on a tabletop. Sizes vary by a fixed hash.
  const blocks: [V3, V3][] = [];
  const hash = (n: number) => { const v = Math.sin(n * 12.9898) * 43758.5453; return v - Math.floor(v); };
  const row = (from: number, to: number, place: (along: number, width: number, depth: number) => V3, seed: number, depthRange: [number, number], storeys: [number, number], alongZ = false) => {
    let along = from;
    let index = 0;
    while (along < to) {
      const width = 11 + hash(seed + index) * 9;
      const depth = depthRange[0] + hash(seed + index + 50) * (depthRange[1] - depthRange[0]);
      const height = 0.6 + (storeys[0] + Math.round(hash(seed + index + 100) * (storeys[1] - storeys[0]))) * 3.4;
      const centre = place(along + width / 2, width, depth);
      blocks.push([centre, alongZ ? [depth, height, width] : [width, height, depth]]);
      along += width + 5 + hash(seed + index + 150) * 4;
      index += 1;
    }
  };
  const across = front + 7.4;
  // Between the viewer and the site only low shops stand; the towers are behind and to the left.
  row(plinth.x[0] - far * 0.5, plinth.x[1] + far * 0.5, (x, _w, d) => [x, 0, across + 2 + d / 2], 1, [9, 12], [1, 1]);
  row(plinth.x[0] - far * 0.5, right - 4, (x, _w, d) => [x, 0, plinth.z[0] - 4 - d / 2], 3, [12, 18], [3, 6]);
  row(plinth.z[0] - far * 0.4, front - 4, (z, _w, d) => [plinth.x[0] - 4 - d / 2, 0, z], 4, [12, 16], [2, 4], true);
  for (let x = plinth.x[0] - far * 0.5; x < plinth.x[1] + far * 0.5; x += 9) trees.push({ at: [x, 0, front + 6.2], turn: x });
  for (let z = plinth.z[0] - far * 0.4; z < front; z += 9) trees.push({ at: [right + 6.2, 0, z], turn: z * 3 });
  // Zebra crossings either side of the junction: bars run with the traffic.
  for (let z = front + 0.4; z < front + 5.2; z += 0.9) markings.push({ x: [right - 6, right - 3], z: [z, z + 0.45] });
  for (let x = right + 0.4; x < right + 5.2; x += 0.9) markings.push({ x: [x, x + 0.45], z: [front - 6, front - 3] });
  // A white fence along the site's street edges, open where the plaza and the car park meet the road.
  const fences: [V3, V3][] = [];
  const fenceZ = front - 1.7;
  const fenceX = right - 1.7;
  const gaps = buildingRect ? [[buildingRect.x[0] + 7, buildingRect.x[0] + 15], [buildingRect.x[0] + 16, buildingRect.x[0] + 27.5]] : [];
  let fenceStart = plinth.x[0] + 1;
  for (const [from, to] of gaps) {
    if (from > fenceStart) fences.push([[fenceStart, 0, fenceZ], [from, 0, fenceZ]]);
    fenceStart = to;
  }
  fences.push([[fenceStart, 0, fenceZ], [fenceX, 0, fenceZ]]);
  fences.push([[fenceX, 0, plinth.z[0] + 1], [fenceX, 0, fenceZ]]);
  cars.push({ at: [plinth.x[0] + 12, 0, front + 1.4], turn: 0 }, { at: [right - 30, 0, front + 4.1], turn: Math.PI }, { at: [right + 1.4, 0, PLANT_BACK - 8], turn: -Math.PI / 2 });
  if (buildingRect) {
    const [bx0, bx1] = buildingRect.x;
    const [bz0, bz1] = buildingRect.z;
    pavings.push({ x: [bx0 - 1.2, PLANT_X[0]], z: [bz0 - 1.2, bz1 + 1.2] });
    pavings.push({ x: [bx0 + 7, bx0 + 15], z: [bz1, front - 1.4] });
    lawns.push({ x: [plinth.x[0] + 0.6, bx0 + 6.4], z: [bz1 + 2, front - 2] });
    lawns.push({ x: [plinth.x[0] + 0.6, bx0 - 2], z: [bz0 - 1.6, bz1 + 2] });
    // Parking in front of the building's right half.
    for (let index = 0; index < 5; index += 1) {
      const x = bx0 + 16.2 + index * 2.7;
      markings.push({ x: [x - 0.06, x + 0.06], z: [front - 7.2, front - 2] });
      if (index < 4 && index !== 2) cars.push({ at: [x + 1.35, 0, front - 4.6], turn: Math.PI / 2 });
    }
    markings.push({ x: [bx0 + 16.14, bx0 + 27.06], z: [front - 7.26, front - 7.14] });
    for (const x of [bx0 - 4.2, bx0 + 1.2, bx0 + 4.6]) trees.push({ at: [x, 0, front - 3.6], turn: x * 3 });
    for (let z = bz0 + 1.5; z < bz1 + 4; z += 5) trees.push({ at: [bx0 - 4.2, 0, z], turn: z * 5 });
    for (const x of [bx0 + 6.6, bx0 + 15.4]) shrubs.push({ at: [x, 0, bz1 + 2.2], turn: x });
    for (let x = bx0 + 1; x < bx1; x += 4.5) shrubs.push({ at: [x, 0, bz0 - 2.6], turn: x * 7 });
  }

  const plantRoom = { x: PLANT_X, z: [PLANT_BACK, plantFront] as const, height: 4.8 };
  const towerPad: Rect = towers.length
    ? { x: [Math.min(...towerXs) - 2.5, Math.max(...towerXs) + 2.5], z: [collectorZ - 0.6, towerZ + 2.6] }
    : { x: [0, 0], z: [0, 0] };
  const racks: [V3, number][] = [];
  if (origin && plan) for (const x of [PLANT_X[0] - 0.9, origin[0] + plan.width + 0.9]) racks.push([[x, 0, (riserSupply[2] + riserReturn[2]) / 2], 4.4]);
  const height = plan ? plan.floors.reduce((sum, floor) => sum + floor.height, 0.3) + 1 : 5;
  return {
    plinth, roads, markings, curbs, lawns, pavings, trees, shrubs, cars, lamps, blocks, fences,
    plantRoom, towerPad, chillers, coolingPumps, chilledPumps, towers, cooling, chilled, racks,
    buildingOrigin: origin,
    riser: { supply: riserSupply, return: riserReturn },
    frames: {
      overview: [[plinth.x[0] + 4, 0, plinth.z[0] + 4], [plinth.x[1] - 4, height * 0.6, plinth.z[1] - 4]],
      plant: [[PLANT_X[0] - 1, 0, PLANT_BACK - 0.5], [PLANT_X[1] + 1, 4, towerZ + 2.5]],
      terminal: buildingRect ? [[buildingRect.x[0], 0, buildingRect.z[0]], [buildingRect.x[1], height, buildingRect.z[1]]] : [[PLANT_X[0], 0, PLANT_BACK], [PLANT_X[1], 4, plantFront]],
    },
  };
}
