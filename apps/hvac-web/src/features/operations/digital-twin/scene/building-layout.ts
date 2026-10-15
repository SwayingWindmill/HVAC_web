import { buildingTerminals, HALL_BRANCHES, type RoomKind, type TwinBuildingPlan, type TwinFloor, type TwinRoom, type TwinTerminal } from '../twin-building';
import type { EquipmentAsset } from './assets/equipment';
import type { FurnitureAsset } from './assets/furniture';
import { box, Kit, type Asset, type V3 } from './assets/kit';
import type { MaterialKey } from './assets/materials';
import { RISER_LOCAL, type Placement, type Run } from './site-layout';

// The building, floor by floor, in its own coordinates (origin at the back-left corner, at ground).
// Each floor is a slab with its finishes, the solid back walls with ribbon windows, the curtain wall on
// the two street faces (a separate part, so the facade can be hidden), partitions cut below the ceiling
// so the rooms read from above, furniture, and the air side: ducts, diffusers and terminal units hung
// under the slab above, with the chilled-water riser and branches from the shaft.

export interface DuctRun {
  readonly points: readonly V3[];
  readonly width: number;
  readonly height: number;
}

export interface TerminalPlacement {
  readonly terminal: TwinTerminal;
  readonly asset: EquipmentAsset;
  readonly at: V3;
  readonly turn: number;
}

export interface FloorLayout {
  readonly level: number;
  readonly label: string;
  /** Finished floor level and floor-to-floor height. */
  readonly y: number;
  readonly height: number;
  readonly structure: Asset;
  readonly facade: Asset;
  readonly furniture: ReadonlyMap<FurnitureAsset, readonly Placement[]>;
  readonly diffusers: readonly Placement[];
  readonly grilles: readonly Placement[];
  readonly terminals: readonly TerminalPlacement[];
  readonly supplyDucts: readonly DuctRun[];
  readonly freshDucts: readonly DuctRun[];
  readonly pipes: readonly Run[];
  readonly captions: readonly { readonly room: TwinRoom; readonly at: V3 }[];
}

export interface BuildingLayout {
  readonly width: number;
  readonly depth: number;
  readonly floors: readonly FloorLayout[];
  readonly roof: {
    readonly y: number;
    readonly structure: Asset;
    readonly equipment: readonly { readonly asset: EquipmentAsset; readonly at: V3; readonly turn: number; readonly scale: number }[];
    readonly ducts: readonly DuctRun[];
  };
  readonly height: number;
}

const PODIUM = 0.3;
const SLAB = 0.3;
const WALL = 0.12;
const SKIN = 0.28;
const MULLION_PITCH = 1.5;

const FLOOR_FINISH: Readonly<Record<RoomKind, MaterialKey>> = {
  hall: 'floorStone',
  private: 'floorWood',
  smallPrivate: 'floorWood',
  kitchen: 'panel',
  plantRoom: 'slab',
  service: 'slab',
  corridor: 'floorCarpet',
  core: 'slab',
};

const WALLED: ReadonlySet<RoomKind> = new Set(['private', 'smallPrivate', 'kitchen', 'plantRoom', 'service', 'core']);
const OPEN: ReadonlySet<RoomKind> = new Set(['hall', 'corridor']);

const centre = (space: TwinRoom) => [(space.x[0] + space.x[1]) / 2, (space.z[0] + space.z[1]) / 2] as const;
const inside = (space: TwinRoom, x: number, z: number) => x > space.x[0] && x < space.x[1] && z > space.z[0] && z < space.z[1];

/** A wall segment from a to b (axis aligned) with an optional door gap in the middle. */
function addWall(kit: Kit, material: MaterialKey, a: readonly [number, number], b: readonly [number, number], y: number, height: number, thickness: number, door = 0) {
  const alongX = a[1] === b[1];
  const length = alongX ? Math.abs(b[0] - a[0]) : Math.abs(b[1] - a[1]);
  const pieces: [number, number][] = door > 0 && length > door + 0.6
    ? [[0, length / 2 - door / 2], [length / 2 + door / 2, length]]
    : [[0, length]];
  const start = alongX ? Math.min(a[0], b[0]) : Math.min(a[1], b[1]);
  for (const [from, to] of pieces) {
    const mid = start + (from + to) / 2;
    const size = to - from;
    kit.add(material, alongX ? box(size, height, thickness, 0.012) : box(thickness, height, size, 0.012), {
      at: alongX ? [mid, y + height / 2, a[1]] : [a[0], y + height / 2, mid],
    });
  }
}

function partitions(kit: Kit, plan: TwinBuildingPlan, floor: TwinFloor, y: number, height: number) {
  const seen = new Set<string>();
  for (const space of floor.rooms) {
    if (!WALLED.has(space.kind)) continue;
    const [x0, x1] = space.x;
    const [z0, z1] = space.z;
    const edges: [readonly [number, number], readonly [number, number], readonly [number, number]][] = [
      [[x0, z0], [x1, z0], [0, -1]],
      [[x0, z1], [x1, z1], [0, 1]],
      [[x0, z0], [x0, z1], [-1, 0]],
      [[x1, z0], [x1, z1], [1, 0]],
    ];
    for (const [a, b, normal] of edges) {
      const onPerimeter = (a[0] === b[0] && (a[0] === 0 || a[0] === plan.width)) || (a[1] === b[1] && (a[1] === 0 || a[1] === plan.depth));
      if (onPerimeter) continue;
      const key = [a, b].map((point) => point.join(',')).join('|');
      if (seen.has(key)) continue;
      seen.add(key);
      const mid = [(a[0] + b[0]) / 2 + normal[0] * 0.3, (a[1] + b[1]) / 2 + normal[1] * 0.3] as const;
      const neighbour = floor.rooms.find((other) => other !== space && inside(other, mid[0], mid[1]));
      const door = neighbour && OPEN.has(neighbour.kind) ? 1.0 : 0;
      const full = space.kind === 'core' || space.kind === 'service';
      addWall(kit, 'wall', a, b, y, full ? height - SLAB : Math.min(height - 1.2, 3.2), WALL, door);
    }
  }
}

/** The two solid back walls (x = 0 and z = 0): spandrels with a glazed ribbon between mullions. */
function backWalls(kit: Kit, plan: TwinBuildingPlan, y: number, height: number) {
  const sill = 0.95;
  const head = height - SLAB - 0.75;
  const sides: { length: number; at: (along: number, up: number) => V3; size: (along: number, up: number) => V3 }[] = [
    { length: plan.width, at: (along, up) => [along, up, SKIN / 2], size: (along, up) => [along, up, SKIN] },
    { length: plan.depth, at: (along, up) => [SKIN / 2, up, along], size: (along, up) => [SKIN, up, along] },
  ];
  for (const side of sides) {
    const parts: [number, number, MaterialKey][] = [[0, sill, 'wall'], [head, height - SLAB, 'wall']];
    for (const [from, to, material] of parts) {
      const [sx, sy, sz] = side.size(side.length, to - from);
      kit.add(material, box(sx, sy, sz, 0.02), { at: side.at(side.length / 2, y + (from + to) / 2) });
    }
    const [gx, gy, gz] = side.size(side.length, head - sill);
    kit.add('glass', box(gx === SKIN ? 0.02 : gx, gy, gz === SKIN ? 0.02 : gz, 0), { at: side.at(side.length / 2, y + (sill + head) / 2) });
    for (let along = MULLION_PITCH; along < side.length; along += MULLION_PITCH) {
      const [mx, my, mz] = side.size(0.06, head - sill);
      kit.add('mullion', box(mx === SKIN ? 0.1 : mx, my, mz === SKIN ? 0.1 : mz, 0.01), { at: side.at(along, y + (sill + head) / 2) });
    }
  }
}

/** The street faces (x = width and z = depth): floor-height glass between slim mullions. */
function curtainWall(kit: Kit, plan: TwinBuildingPlan, y: number, height: number, entrance: boolean) {
  const glassHeight = height - 0.45;
  const mid = y + 0.15 + glassHeight / 2;
  kit.add('glass', box(0.02, glassHeight, plan.depth, 0), { at: [plan.width - 0.08, mid, plan.depth / 2] });
  kit.add('glass', box(plan.width, glassHeight, 0.02, 0), { at: [plan.width / 2, mid, plan.depth - 0.08] });
  for (let along = MULLION_PITCH; along < plan.depth; along += MULLION_PITCH) {
    kit.add('mullion', box(0.1, glassHeight, 0.06, 0.01), { at: [plan.width - 0.06, mid, along] });
  }
  for (let along = MULLION_PITCH; along < plan.width; along += MULLION_PITCH) {
    if (entrance && along > 14.5 && along < 19.5) continue;
    kit.add('mullion', box(0.06, glassHeight, 0.1, 0.01), { at: [along, mid, plan.depth - 0.06] });
  }
  const transom = y + 0.15 + glassHeight - 0.7;
  kit.add('mullion', box(0.08, 0.06, plan.depth, 0.01), { at: [plan.width - 0.06, transom, plan.depth / 2] });
  kit.add('mullion', box(plan.width, 0.06, 0.08, 0.01), { at: [plan.width / 2, transom, plan.depth - 0.06] });
}

function ring(count: number, radius: number, cx: number, cz: number, y: number, offset = 0): Placement[] {
  return Array.from({ length: count }, (_, index) => {
    const angle = offset + (index / count) * Math.PI * 2;
    return { at: [cx + Math.sin(angle) * radius, y, cz + Math.cos(angle) * radius] as V3, turn: angle + Math.PI };
  });
}

function furnish(floor: TwinFloor, y: number): Map<FurnitureAsset, Placement[]> {
  const items = new Map<FurnitureAsset, Placement[]>();
  const put = (asset: FurnitureAsset, ...placements: Placement[]) => items.set(asset, [...(items.get(asset) ?? []), ...placements]);
  for (const space of floor.rooms) {
    const [cx, cz] = centre(space);
    const back = space.z[1] <= 8.5;
    if (space.kind === 'hall') {
      for (const x of [8.4, 11.2, 15.8, 18.4, 22.8, 25.6]) {
        for (const z of [2.4, 5.2, 8.0, 10.8, 13.6]) {
          if (x > 14 && x < 20 && z > 12) continue;
          put('hallTable', { at: [x, y, z] });
          put('chair', ...ring(4, 0.82, x, z, y, Math.PI / 4));
        }
      }
      put('reception', { at: [17, y, 15.6] });
      put('plant', { at: [6.7, y, 0.7] }, { at: [27.3, y, 0.7] }, { at: [27.3, y, 17.3] }, { at: [13.2, y, 17.3] }, { at: [20.8, y, 17.3] });
    }
    if (space.kind === 'private') {
      const tableZ = back ? space.z[1] - 3.5 : space.z[0] + 2.9;
      put('banquetTable', { at: [cx, y, tableZ] });
      put('chair', ...ring(10, 1.32, cx, tableZ, y));
      put('sofa', { at: [cx, y, back ? 1.0 : space.z[1] - 1.0], turn: back ? 0 : Math.PI });
      put('lowTable', { at: [cx, y, back ? 2.0 : space.z[1] - 2.0] });
      put('sideboard', { at: [space.x[0] + 0.36, y, tableZ], turn: Math.PI / 2 });
      put('plant', { at: [space.x[1] - 0.5, y, back ? 0.6 : space.z[1] - 0.6] });
    }
    if (space.kind === 'smallPrivate') {
      put('roomTable', { at: [cx, y, cz] });
      put('chair', ...ring(8, 1.08, cx, cz, y, Math.PI / 8));
      put('plant', { at: [space.x[1] - 0.45, y, back ? 0.5 : space.z[1] - 0.5] });
    }
    if (space.kind === 'kitchen') {
      put('counter', { at: [cx, y, space.z[0] + 0.7] }, { at: [cx, y, space.z[1] - 1.4] });
      put('hood', { at: [cx, y + 2.1, space.z[0] + 0.7] });
    }
    if (space.kind === 'service' && space.name === '备餐间') put('counter', { at: [cx, y, cz - 1.5], turn: Math.PI / 2 });
    if (space.kind === 'core') {
      put('stairFlight', { at: [0.7, y, 1.2] }, { at: [4.3, y + 2.1, 2.5], turn: Math.PI });
    }
    if (space.kind === 'corridor' && space.x[1] - space.x[0] > 10) put('plant', { at: [space.x[1] - 0.5, y, cz] });
  }
  return items;
}

export function buildBuildingLayout(plan: TwinBuildingPlan): BuildingLayout {
  const terminals = buildingTerminals(plan);
  let y = PODIUM;
  const floors: FloorLayout[] = plan.floors.map((floor, floorIndex) => {
    const floorY = y;
    const height = floor.height;
    y += height;
    const top = floorIndex === plan.floors.length - 1;
    const structure = new Kit();
    const facade = new Kit();
    // Slab (the podium under the ground floor), its edge band on the street faces, and finishes.
    structure.add(floorIndex === 0 ? 'plinthSide' : 'slab', box(plan.width, SLAB, plan.depth, 0.03), { at: [plan.width / 2, floorY - SLAB / 2, plan.depth / 2] });
    structure.add('panel', box(plan.width + 0.04, 0.42, 0.06, 0.012), { at: [plan.width / 2, floorY - 0.12, plan.depth + 0.01] });
    structure.add('panel', box(0.06, 0.42, plan.depth + 0.04, 0.012), { at: [plan.width + 0.01, floorY - 0.12, plan.depth / 2] });
    for (const space of floor.rooms) {
      const w = space.x[1] - space.x[0];
      const d = space.z[1] - space.z[0];
      structure.add(FLOOR_FINISH[space.kind], box(w - 0.04, 0.02, d - 0.04, 0), { at: [space.x[0] + w / 2, floorY + 0.01, space.z[0] + d / 2] });
    }
    backWalls(structure, plan, floorY, height);
    partitions(structure, plan, floor, floorY, height);
    // Columns: the street corners full height; a grid through the open hall.
    for (const [x, z] of [[plan.width - 0.25, plan.depth - 0.25], [plan.width - 0.25, 0.25], [0.25, plan.depth - 0.25]]) {
      structure.add('panel', box(0.5, height - SLAB, 0.5, 0.03), { at: [x, floorY + (height - SLAB) / 2, z] });
    }
    if (floor.rooms.some((space) => space.kind === 'hall')) {
      for (const [x, z] of [[13.5, 6], [20.5, 6], [13.5, 12], [20.5, 12]]) structure.add('panel', box(0.5, height - SLAB, 0.5, 0.03), { at: [x, floorY + (height - SLAB) / 2, z] });
    }
    // Lift car in the core.
    const core = floor.rooms.find((space) => space.kind === 'core');
    if (core) {
      structure.add('steel', box(1.7, 2.4, 1.7, 0.03), { at: [core.x[1] - 1.3, floorY + 1.2, core.z[1] - 1.3] });
      structure.add('wall', box(0.12, height - SLAB, 2.4, 0.01), { at: [core.x[1] - 2.5, floorY + (height - SLAB) / 2, core.z[1] - 1.3] });
    }
    curtainWall(facade, plan, floorY, height, floorIndex === 0);
    if (floorIndex === 0) {
      structure.add('panel', box(8.4, 0.24, 2.8, 0.04), { at: [17, floorY + 3.9, plan.depth + 1.4] });
      structure.add('mullion', box(8.44, 0.06, 2.84, 0.01), { at: [17, floorY + 3.76, plan.depth + 1.4] });
      for (const x of [13.2, 20.8]) structure.add('mullion', box(0.12, 3.8, 0.12, 0.02), { at: [x, floorY + 1.9, plan.depth + 2.6] });
    }

    // Air side and water branches, hung under the slab above.
    const ceiling = floorY + height - SLAB;
    const supplyDucts: DuctRun[] = [];
    const freshDucts: DuctRun[] = [];
    const diffusers: Placement[] = [];
    const grilles: Placement[] = [];
    const pipes: Run[] = [];
    const placed: TerminalPlacement[] = [];
    const riserTop = top ? ceiling - 0.45 : floorY + height;
    pipes.push({ points: [[RISER_LOCAL.supply[0], floorIndex === 0 ? 3.8 : floorY - SLAB, RISER_LOCAL.supply[1]], [RISER_LOCAL.supply[0], riserTop, RISER_LOCAL.supply[1]]], radius: 0.09 });
    pipes.push({ points: [[RISER_LOCAL.return[0], floorIndex === 0 ? 4.4 : floorY - SLAB, RISER_LOCAL.return[1]], [RISER_LOCAL.return[0], riserTop, RISER_LOCAL.return[1]]], radius: 0.09 });
    const floorTerminals = terminals.filter((terminal) => terminal.level === floor.level);
    for (const terminal of floorTerminals) {
      const space = terminal.room;
      const [cx, cz] = centre(space);
      if (terminal.equipment.kind === 'ahu') {
        const at: V3 = [cx, floorY, cz];
        placed.push({ terminal, asset: 'ahu', at, turn: 0 });
        const duct = ceiling - 0.5;
        supplyDucts.push({ points: [[cx + 1.45, floorY + 1.8, cz], [cx + 1.45, duct, cz], [6.8, duct, cz], [6.8, duct, 9], [26.6, duct, 9]], width: 1.0, height: 0.5 });
        pipes.push({ points: [[RISER_LOCAL.supply[0], 3.8, RISER_LOCAL.supply[1]], [RISER_LOCAL.supply[0], 3.8, cz + 1.65], [cx, 3.8, cz + 1.65], [cx, floorY + 0.45, cz + 1.65], [cx, floorY + 0.45, cz + 1.08]], radius: 0.06 });
        pipes.push({ points: [[RISER_LOCAL.return[0], 4.4, RISER_LOCAL.return[1]], [RISER_LOCAL.return[0], 4.4, cz + 2.1], [cx + 0.0, 4.4, cz + 2.1], [cx, floorY + 0.85, cz + 2.1], [cx, floorY + 0.85, cz + 1.08]], radius: 0.06 });
      }
      if (terminal.equipment.kind === 'vav' && terminal.branch) {
        const x = HALL_BRANCHES[terminal.branch.index];
        const side = terminal.branch.side;
        const duct = ceiling - 0.5;
        const end = side < 0 ? 2.2 : 15.8;
        supplyDucts.push({ points: [[x, duct, 9 + side * 0.5], [x, duct, end]], width: 0.5, height: 0.32 });
        placed.push({ terminal, asset: 'vav', at: [x, duct - 0.16, 9 + side * 1.6], turn: side < 0 ? Math.PI / 2 : -Math.PI / 2 });
        for (const z of side < 0 ? [5.2, 2.4] : [12.8, 15.6]) diffusers.push({ at: [x, duct - 0.42, z] });
      }
      if (terminal.equipment.kind === 'fcu') {
        const back = space.z[1] <= 8.5;
        const edge = back ? space.z[1] - 0.95 : space.z[0] + 0.95;
        const fcuY = ceiling - 0.42;
        const turn = back ? Math.PI / 2 : -Math.PI / 2;
        const dir = back ? -1 : 1;
        placed.push({ terminal, asset: 'fcu', at: [cx, fcuY, edge], turn });
        const reach = space.kind === 'private' ? 5.2 : 3.0;
        supplyDucts.push({ points: [[cx, fcuY + 0.15, edge + dir * 0.62], [cx, fcuY + 0.15, edge + dir * reach]], width: 0.42, height: 0.22 });
        const spots = space.kind === 'private' ? [edge + dir * 2.2, edge + dir * 4.8] : [edge + dir * 2.6];
        for (const z of spots) diffusers.push({ at: [cx, fcuY - 0.21, z] });
        grilles.push({ at: [cx - 1.4, fcuY - 0.02, edge] });
        const port = edge - dir * 0.3;
        const branchY = ceiling - 0.5;
        pipes.push({ points: [[cx + dir * 0.42, branchY, 9.0], [cx + dir * 0.42, branchY, port], [cx + dir * 0.42, fcuY + 0.15, port]], radius: 0.03 });
        pipes.push({ points: [[cx + dir * 0.62, branchY - 0.28, 9.35], [cx + dir * 0.62, branchY - 0.28, port], [cx + dir * 0.62, fcuY + 0.15, port]], radius: 0.03 });
      }
    }
    if (floorTerminals.some((terminal) => terminal.equipment.kind === 'fcu')) {
      const branchY = ceiling - 0.5;
      pipes.push({ points: [[RISER_LOCAL.supply[0], branchY, RISER_LOCAL.supply[1]], [RISER_LOCAL.supply[0], branchY, 9.0], [27.4, branchY, 9.0]], radius: 0.06 });
      pipes.push({ points: [[RISER_LOCAL.return[0], branchY - 0.28, RISER_LOCAL.return[1]], [RISER_LOCAL.return[0], branchY - 0.28, 9.35], [27.4, branchY - 0.28, 9.35]], radius: 0.06 });
      const freshY = ceiling - 0.75;
      freshDucts.push({ points: [[1.0, floorY - SLAB, 6.35], [1.0, top ? freshY : floorY + height, 6.35]], width: 0.4, height: 0.3 });
      freshDucts.push({ points: [[1.0, freshY, 6.35], [2.6, freshY, 6.35], [2.6, freshY, 10.0], [27.4, freshY, 10.0]], width: 0.44, height: 0.28 });
    }
    const captions = floor.rooms
      .filter((space) => space.kind !== 'corridor' && space.kind !== 'service' && space.kind !== 'core')
      .map((space) => {
        const [cx, cz] = centre(space);
        return { room: space, at: [cx, floorY + 0.05, space.kind === 'hall' ? 16.9 : cz] as V3 };
      });
    return {
      level: floor.level,
      label: floor.label,
      y: floorY,
      height,
      structure: structure.build(),
      facade: facade.build(),
      furniture: furnish(floor, floorY),
      diffusers,
      grilles,
      terminals: placed,
      supplyDucts,
      freshDucts,
      pipes,
      captions,
    };
  });

  // Roof: slab, parapet, fresh air unit and kitchen exhaust fans, the fresh air duct down the shaft.
  const roofY = y;
  const roof = new Kit();
  roof.add('slab', box(plan.width, SLAB, plan.depth, 0.03), { at: [plan.width / 2, roofY - SLAB / 2, plan.depth / 2] });
  roof.add('paving', box(plan.width - 0.6, 0.04, plan.depth - 0.6, 0), { at: [plan.width / 2, roofY + 0.02, plan.depth / 2] });
  roof.add('panel', box(plan.width + 0.04, 0.42, 0.06, 0.012), { at: [plan.width / 2, roofY - 0.12, plan.depth + 0.01] });
  roof.add('panel', box(0.06, 0.42, plan.depth + 0.04, 0.012), { at: [plan.width + 0.01, roofY - 0.12, plan.depth / 2] });
  for (const [a, b] of [[[0, 0.14], [plan.width, 0.14]], [[0, plan.depth - 0.14], [plan.width, plan.depth - 0.14]], [[0.14, 0], [0.14, plan.depth]], [[plan.width - 0.14, 0], [plan.width - 0.14, plan.depth]]] as const) {
    addWall(roof, 'panel', a, b, roofY, 0.95, 0.28);
  }
  roof.add('wall', box(4.2, 2.6, 4.6, 0.04), { at: [3.1, roofY + 1.3, 3.0] });
  roof.add('paintDark', box(4.4, 0.12, 4.8, 0.02), { at: [3.1, roofY + 2.66, 3.0] });
  for (const [x, z] of [[9, 1.4], [9, 6.0], [20, 1.4], [20, 6.0]]) roof.add('concrete', box(3.6, 0.2, 0.3, 0.02), { at: [x + 1.5, roofY + 0.1, z] });
  const pau: V3 = [10.5, roofY + 0.2, 3.7];
  const roofDuctY = roofY + 1.75;
  const highest = floors[floors.length - 1];
  return {
    width: plan.width,
    depth: plan.depth,
    floors,
    roof: {
      y: roofY,
      structure: roof.build(),
      equipment: [
        { asset: 'ahu', at: pau, turn: 0, scale: 0.8 },
        { asset: 'exhaustFan', at: [2.2, roofY, 15.4], turn: 0, scale: 1 },
        { asset: 'exhaustFan', at: [4.4, roofY, 15.4], turn: 0, scale: 1 },
        { asset: 'exhaustFan', at: [20, roofY, 15.6], turn: 0, scale: 1 },
      ],
      ducts: [{ points: [[pau[0] + 1.16, pau[1] + 1.5, pau[2]], [pau[0] + 1.16, roofDuctY, pau[2]], [pau[0] + 1.16, roofDuctY, 6.35], [1.0, roofDuctY, 6.35], [1.0, highest ? highest.y + highest.height - SLAB - 0.75 : roofY, 6.35]], width: 0.5, height: 0.32 }],
    },
    height: roofY + 1,
  };
}

