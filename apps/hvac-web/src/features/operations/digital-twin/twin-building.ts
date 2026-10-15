import type { TwinEquipment } from './twin-model';

// The terminal side of a site: the building the cold side serves, its floors and rooms, and the air
// side equipment in it. The Registry holds no rooms or terminal units yet, so a site's building is
// declared here and every terminal unit is shown as「未接入」until it is registered and reports
// (docs/specs/digital-twin.md). A site without a declaration shows the cold side only.
// Plans are in metres, x along the building's width and z along its depth, origin at the back-left.

export type RoomKind = 'hall' | 'private' | 'smallPrivate' | 'kitchen' | 'plantRoom' | 'service' | 'corridor' | 'core';

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

const room = (id: string, name: string, kind: RoomKind, x: readonly [number, number], z: readonly [number, number]): TwinRoom => ({ id, name, kind, x, z });

function diningFloor(level: number): TwinFloor {
  const n = (index: number) => `${level}0${index}`;
  return {
    level,
    label: `${level}F 包间`,
    height: 4.2,
    rooms: [
      room(`${level}-core`, '楼梯 · 电梯', 'core', [0, 6], [0, 6]),
      room(`${level}-shaft`, '管井', 'service', [0, 2], [6, 8.5]),
      room(`${level}-corridor`, '走廊', 'corridor', [2, 28], [8.5, 10.5]),
      room(`${level}-lobby`, '电梯厅', 'corridor', [2, 6], [6, 8.5]),
      room(`${level}-r1`, `${n(1)} 包间`, 'private', [6, 12], [0, 8.5]),
      room(`${level}-r2`, `${n(2)} 包间`, 'private', [12, 18], [0, 8.5]),
      room(`${level}-r3`, `${n(3)} 小包间`, 'smallPrivate', [18, 23], [0, 8.5]),
      room(`${level}-r4`, `${n(4)} 小包间`, 'smallPrivate', [23, 28], [0, 8.5]),
      room(`${level}-pantry`, '备餐间', 'service', [0, 4], [10.5, 18]),
      room(`${level}-r5`, `${n(5)} 小包间`, 'smallPrivate', [4, 8.5], [10.5, 18]),
      room(`${level}-r6`, `${n(6)} 小包间`, 'smallPrivate', [8.5, 13], [10.5, 18]),
      room(`${level}-r7`, `${n(7)} 包间`, 'private', [13, 20.5], [10.5, 18]),
      room(`${level}-r8`, `${n(8)} 包间`, 'private', [20.5, 28], [10.5, 18]),
    ],
  };
}

export const BUILDING_PLANS: Readonly<Record<string, TwinBuildingPlan>> = {
  songyaxuan: {
    name: '松雅轩',
    width: 28,
    depth: 18,
    floors: [
      {
        level: 1,
        label: '1F 大厅',
        height: 4.8,
        rooms: [
          room('1-core', '楼梯 · 电梯', 'core', [0, 6], [0, 6]),
          room('1-shaft', '管井', 'service', [0, 2], [6, 8.5]),
          room('1-passage', '通道', 'corridor', [2, 6], [6, 8.5]),
          room('1-ahu', '空调机房', 'plantRoom', [0, 6], [8.5, 13]),
          room('1-kitchen', '后厨', 'kitchen', [0, 6], [13, 18]),
          room('1-hall', '大厅', 'hall', [6, 28], [0, 18]),
        ],
      },
      diningFloor(2),
      diningFloor(3),
    ],
  },
};

export type TerminalKind = 'ahu' | 'fcu' | 'vav';

/** A terminal unit and where it sits: room-relative placement is resolved by the scene layout. */
export interface TwinTerminal {
  readonly equipment: TwinEquipment;
  readonly level: number;
  readonly room: TwinRoom;
  /** For VAV boxes: which hall branch, and which side of the main (-1 back, 1 front). */
  readonly branch?: { readonly index: number; readonly side: -1 | 1 };
}

export const HALL_BRANCHES = [9.5, 14.5, 19.5, 24.5] as const;

const unconnected = (assetId: string, name: string, kind: TerminalKind): TwinEquipment => ({
  assetId, name, kind, state: 'unconnected', device: null, readings: [], values: {},
});

/** Every terminal unit in a building: an AHU with VAV boxes for the hall, a fan coil per dining room. */
export function buildingTerminals(plan: TwinBuildingPlan): TwinTerminal[] {
  return plan.floors.flatMap((floor) => floor.rooms.flatMap((space): TwinTerminal[] => {
    if (space.kind === 'plantRoom') return [{ equipment: unconnected(`terminal:${space.id}`, `${floor.level}F 空调箱`, 'ahu'), level: floor.level, room: space }];
    if (space.kind === 'hall') {
      return HALL_BRANCHES.flatMap((_, index) => ([-1, 1] as const).map((side, sideIndex) => ({
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
