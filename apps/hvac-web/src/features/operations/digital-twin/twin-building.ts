import type { TwinEquipment } from './twin-model';

// The terminal side of a site: the building the cold side serves, its floors and rooms, and the air
// side equipment in it. Each site's building plan comes from Registry (Site.buildingPlan, seeded by
// deploy/sites/*.site.json); every terminal unit is shown as「未接入」until registered and reporting
// (docs/specs/digital-twin.md). A site without buildingPlan shows the cold plant only.
// Plans are in metres, x along the building's width and z along its depth, origin at the back-left.

export type RoomKind = 'hall' | 'private' | 'smallPrivate' | 'kitchen' | 'plantRoom' | 'service' | 'corridor' | 'core' | 'shaft';

export interface TwinRoom {
  readonly id: string;
  readonly name: string;
  readonly kind: RoomKind;
  readonly x: readonly [number, number];
  readonly z: readonly [number, number];
}

export interface TwinFloor {
  readonly level: number;
  readonly label: string;
  /** Floor to floor, metres. */
  readonly height: number;
  readonly rooms: readonly TwinRoom[];
}

export interface TwinBuildingPlan {
  readonly name: string;
  readonly width: number;
  readonly depth: number;
  readonly floors: readonly TwinFloor[];
}

export type TerminalKind = 'ahu' | 'fcu' | 'vav';

/** A terminal unit and where it sits: room-relative placement is resolved by the scene layout. */
export interface TwinTerminal {
  readonly equipment: TwinEquipment;
  readonly level: number;
  readonly room: TwinRoom;
  /** For VAV boxes: which hall branch, and which side of the main (-1 back, 1 front). */
  readonly branch?: { readonly index: number; readonly side: -1 | 1 };
}

export interface BuildingRiser {
  readonly supply: readonly [number, number];
  readonly return: readonly [number, number];
  readonly fresh: readonly [number, number];
  readonly shaftExitX: number;
}

export interface CorridorBand {
  readonly zSpan: readonly [number, number];
  readonly supplyZ: number;
  readonly returnZ: number;
  readonly freshZ: number;
  readonly farX: number;
}

export interface RoofAnchors {
  readonly bulkheadCenter: readonly [number, number];
  readonly bulkheadSize: readonly [number, number];
  readonly pau: readonly [number, number];
  readonly exhaustFans: readonly (readonly [number, number])[];
}

export interface FrontGrounds {
  readonly plaza: readonly [number, number];
  readonly parking: { readonly x0: number; readonly x1: number; readonly slotCount: number } | null;
  readonly gaps: readonly (readonly [number, number])[];
}

const clampRange = (value: number, min: number, max: number): number => (
  min > max ? (min + max) / 2 : Math.max(min, Math.min(max, value))
);

/** Resolve vertical shaft riser coordinates from the plan's shaft/plantRoom, clamped in-bounds. */
export function resolveBuildingRiser(plan: TwinBuildingPlan): BuildingRiser {
  const allRooms = plan.floors.flatMap((floor) => floor.rooms);
  const anchor = allRooms.find((room) => room.kind === 'shaft')
    ?? allRooms.find((room) => room.kind === 'plantRoom');

  if (!anchor) {
    const sx = clampRange(plan.width - 1.0, 0.5, plan.width - 0.5);
    const sz = clampRange(plan.depth * 0.39, 0.5, plan.depth - 0.5);
    return {
      supply: [sx, sz],
      return: [clampRange(sx - 0.5, 0.4, plan.width - 0.4), clampRange(sz + 0.6, 0.4, plan.depth - 0.4)],
      fresh: [sx, clampRange(sz - 0.65, 0.4, plan.depth - 0.4)],
      shaftExitX: clampRange(sx - 1.6, 0.4, plan.width - 0.4),
    };
  }

  const [x0, x1] = anchor.x;
  const [z0, z1] = anchor.z;
  const sx = clampRange(x1 - 1.0, x0 + 0.3, x1 - 0.3);
  const sz = clampRange(z0 + 1.0, z0 + 0.3, z1 - 0.3);
  const rx = clampRange(x1 - 1.5, x0 + 0.25, x1 - 0.25);
  const rz = clampRange(z0 + 1.6, z0 + 0.3, z1 - 0.3);
  const fx = clampRange(x1 - 1.0, x0 + 0.3, x1 - 0.3);
  const fz = clampRange(z0 + 0.35, z0 + 0.25, z1 - 0.25);
  const rightSide = (x0 + x1) / 2 >= plan.width / 2;
  const shaftExitX = rightSide
    ? clampRange(x0 - 0.6, 0.4, plan.width - 0.4)
    : clampRange(x1 + 0.6, 0.4, plan.width - 0.4);

  return {
    supply: [sx, sz],
    return: [rx, rz],
    fresh: [fx, fz],
    shaftExitX,
  };
}

/** Derive VAV branch x-positions across a hall room. */
export function resolveHallBranches(hall: TwinRoom): readonly number[] {
  const width = hall.x[1] - hall.x[0];
  const count = Math.max(1, Math.min(4, Math.round(width / 7.3)));
  if (count === 1) return [(hall.x[0] + hall.x[1]) / 2];
  const margin = Math.min(3.2, width * 0.16);
  const step = (width - margin * 2) / (count - 1);
  return Array.from({ length: count }, (_, index) => Number((hall.x[0] + margin + index * step).toFixed(2)));
}

/** Resolve street-facing main entrance x from the ground floor hall (fallback building center). */
export function resolveEntranceX(plan: TwinBuildingPlan): number {
  const groundHall = plan.floors[0]?.rooms.find((room) => room.kind === 'hall');
  return groundHall ? (groundHall.x[0] + groundHall.x[1]) / 2 : plan.width / 2;
}

/** Resolve horizontal corridor duct & chilled-water distribution band for a floor. */
export function resolveCorridorBand(floor: TwinFloor, plan: TwinBuildingPlan): CorridorBand {
  const corridors = floor.rooms.filter((room) => room.kind === 'corridor');
  const corridor = corridors.find((room) => room.x[1] - room.x[0] >= plan.width * 0.4)
    ?? corridors.sort((a, b) => (b.x[1] - b.x[0]) - (a.x[1] - a.x[0]))[0];
  const zSpan: readonly [number, number] = corridor
    ? corridor.z
    : [clampRange(plan.depth * 0.47, 0.5, plan.depth - 1), clampRange(plan.depth * 0.58, 1.0, plan.depth - 0.5)];
  const bandHeight = Math.max(0.4, zSpan[1] - zSpan[0]);
  const supplyZ = clampRange(zSpan[0] + Math.min(0.5, bandHeight * 0.25), zSpan[0] + 0.15, zSpan[1] - 0.15);
  const returnZ = clampRange(zSpan[0] + Math.min(0.85, bandHeight * 0.425), zSpan[0] + 0.15, zSpan[1] - 0.15);
  const freshZ = clampRange(zSpan[1] - Math.min(0.5, bandHeight * 0.25), zSpan[0] + 0.15, zSpan[1] - 0.15);
  const farX = corridor
    ? (corridor.x[0] <= 1 ? clampRange(corridor.x[0] + 0.6, 0.3, plan.width - 0.3) : clampRange(corridor.x[1] - 0.6, 0.3, plan.width - 0.3))
    : clampRange(0.6, 0.3, plan.width - 0.3);
  return { zSpan, supplyZ, returnZ, freshZ, farX };
}

/** Derive structural interior columns when a hall is large enough to need intermediate spans. */
export function resolveHallColumns(hall: TwinRoom): readonly (readonly [number, number])[] {
  const width = hall.x[1] - hall.x[0];
  const depth = hall.z[1] - hall.z[0];
  if (width < 10 || depth < 10) return [];
  const xs = [hall.x[0] + width / 3, hall.x[0] + (width * 2) / 3];
  const zs = [hall.z[0] + depth / 3, hall.z[0] + (depth * 2) / 3];
  return zs.flatMap((z) => xs.map((x): readonly [number, number] => [Number(x.toFixed(2)), Number(z.toFixed(2))]));
}

/** Resolve roof bulkhead, fresh-air unit (PAU), and kitchen/hall exhaust fans inside roof bounds. */
export function resolveRoofAnchors(plan: TwinBuildingPlan): RoofAnchors {
  const allRooms = plan.floors.flatMap((floor) => floor.rooms);
  const topCore = [...plan.floors].reverse().map((floor) => floor.rooms.find((room) => room.kind === 'core')).find(Boolean);
  const core = topCore ?? allRooms.find((room) => room.kind === 'core');

  const cw = core ? clampRange((core.x[1] - core.x[0]) * 0.7, 2.0, Math.min(4.2, plan.width - 1.2)) : Math.min(3.6, plan.width * 0.25);
  const cd = core ? clampRange((core.z[1] - core.z[0]) * 0.77, 2.0, Math.min(4.6, plan.depth - 1.2)) : Math.min(3.6, plan.depth * 0.25);
  const rawCx = core ? (core.x[0] + core.x[1]) / 2 : plan.width - cw / 2 - 1.0;
  const rawCz = core ? (core.z[0] + core.z[1]) / 2 : cd / 2 + 1.0;
  const bulkheadCenter: readonly [number, number] = [
    clampRange(rawCx, cw / 2 + 0.5, plan.width - cw / 2 - 0.5),
    clampRange(rawCz, cd / 2 + 0.5, plan.depth - cd / 2 - 0.5),
  ];

  const coreOnRight = bulkheadCenter[0] >= plan.width / 2;
  const pauMaxX = core && coreOnRight ? Math.max(2.2, core.x[0] - 2.2) : plan.width - 2.2;
  const pauMinX = core && !coreOnRight ? Math.min(plan.width - 2.2, core.x[1] + 2.2) : 2.2;
  const pau: readonly [number, number] = [
    clampRange(plan.width * 0.52, pauMinX, pauMaxX),
    clampRange(Math.min(3.7, plan.depth * 0.25), 1.8, plan.depth - 1.8),
  ];

  const exhaustFans: (readonly [number, number])[] = [];
  const ground = plan.floors[0];
  if (ground) {
    for (const room of ground.rooms) {
      if (room.kind === 'kitchen') {
        const w = room.x[1] - room.x[0];
        const z = clampRange((room.z[0] + room.z[1]) / 2, 1.5, plan.depth - 1.5);
        if (w >= 4) {
          exhaustFans.push(
            [clampRange(room.x[0] + w * 0.3, 1.5, plan.width - 1.5), z],
            [clampRange(room.x[0] + w * 0.7, 1.5, plan.width - 1.5), z],
          );
        } else {
          exhaustFans.push([clampRange((room.x[0] + room.x[1]) / 2, 1.5, plan.width - 1.5), z]);
        }
      }
      if (room.kind === 'hall') {
        exhaustFans.push([
          clampRange(room.x[0] + (room.x[1] - room.x[0]) * 0.36, 1.5, plan.width - 1.5),
          clampRange(plan.depth - 2.4, 1.5, plan.depth - 1.5),
        ]);
      }
    }
  }
  if (!exhaustFans.length) {
    exhaustFans.push([
      clampRange(plan.width * 0.75, 1.5, plan.width - 1.5),
      clampRange(plan.depth - 2.0, 1.5, plan.depth - 1.5),
    ]);
  }

  return { bulkheadCenter, bulkheadSize: [cw, cd], pau, exhaustFans };
}

/** Derive street plaza and parking layout in front of a building footprint. */
export function resolveFrontGrounds(plan: TwinBuildingPlan, bx0: number, bx1: number): FrontGrounds {
  const entrance = resolveEntranceX(plan);
  const plazaHalf = Math.min(4, Math.max(2, plan.width * 0.15));
  const plaza0 = clampRange(bx0 + entrance - plazaHalf, bx0 + 0.5, bx1 - 2.5);
  const plaza1 = clampRange(bx0 + entrance + plazaHalf, plaza0 + 2, bx1 - 0.5);
  const gaps: (readonly [number, number])[] = [[plaza0, plaza1]];

  const parkX0 = plaza1 + 1.0;
  const parkX1 = bx1 - 0.5;
  const slotCount = Math.floor((parkX1 - parkX0) / 2.7);
  const parking = slotCount >= 1
    ? { x0: parkX0, x1: parkX0 + slotCount * 2.7, slotCount }
    : null;
  if (parking) gaps.push([parking.x0, parking.x1]);

  return { plaza: [plaza0, plaza1], parking, gaps };
}

const unconnected = (assetId: string, name: string, kind: TerminalKind): TwinEquipment => ({
  assetId, name, kind, state: 'unconnected', device: null, readings: [], values: {},
});

/** Every terminal unit in a building: an AHU with VAV boxes for the hall, a fan coil per dining room. */
export function buildingTerminals(plan: TwinBuildingPlan): TwinTerminal[] {
  return plan.floors.flatMap((floor) => floor.rooms.flatMap((space): TwinTerminal[] => {
    if (space.kind === 'plantRoom') return [{ equipment: unconnected(`terminal:${space.id}`, `${floor.level}F 空调箱`, 'ahu'), level: floor.level, room: space }];
    if (space.kind === 'hall') {
      return resolveHallBranches(space).flatMap((_, index) => ([-1, 1] as const).map((side, sideIndex) => ({
        equipment: unconnected(`terminal:${space.id}:vav${index * 2 + sideIndex + 1}`, `大厅变风量末端 ${index * 2 + sideIndex + 1}`, 'vav'),
        level: floor.level,
        room: space,
        branch: { index, side },
      })));
    }
    if (space.kind === 'private' || space.kind === 'smallPrivate') {
      return [{ equipment: unconnected(`terminal:${space.id}`, `${space.name} 风机盘管`, 'fcu'), level: floor.level, room: space }];
    }
    return [];
  }));
}
