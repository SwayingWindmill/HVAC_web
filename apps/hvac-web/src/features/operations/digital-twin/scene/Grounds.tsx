import { useEffect, useMemo } from 'react';
import * as THREE from 'three';
import { AssetMesh, InstancedAsset, useMaterialSet } from './assets/AssetMesh';
import { furnitureAsset } from './assets/furniture';
import { box, Kit, tube, type Asset, type V3 } from './assets/kit';
import type { MaterialKey } from './assets/materials';
import type { Rect, SiteLayout } from './site-layout';

// The plinth and everything built on it that is not a machine or the building: roads with lane marks,
// kerbs, lawns, paving, the plant room (its street-facing walls cut low so the room reads from above),
// the tower pad with its railing, and the steel posts that carry the mains to the building.

function slab(kit: Kit, material: MaterialKey, rect: Rect, bottom: number, height: number, radius = 0.04) {
  const width = rect.x[1] - rect.x[0];
  const depth = rect.z[1] - rect.z[0];
  if (width <= 0 || depth <= 0) return;
  kit.add(material, box(width, height, depth, radius), { at: [(rect.x[0] + rect.x[1]) / 2, bottom + height / 2, (rect.z[0] + rect.z[1]) / 2] });
}

function railing(kit: Kit, rect: Rect, base: number) {
  const corners: V3[] = [[rect.x[0], base, rect.z[0]], [rect.x[1], base, rect.z[0]], [rect.x[1], base, rect.z[1]], [rect.x[0], base, rect.z[1]]];
  for (let index = 0; index < 4; index += 1) {
    const a = new THREE.Vector3(...corners[index]);
    const b = new THREE.Vector3(...corners[(index + 1) % 4]);
    const length = a.distanceTo(b);
    const posts = Math.max(1, Math.round(length / 1.6));
    for (let post = 0; post < posts; post += 1) {
      const point = a.clone().lerp(b, post / posts);
      kit.add('steel', new THREE.CylinderGeometry(0.025, 0.025, 1.1, 8), { at: [point.x, base + 0.55, point.z] });
    }
    for (const height of [0.55, 1.1]) kit.add('steel', tube([[a.x, base + height, a.z], [b.x, base + height, b.z]], height > 1 ? 0.028 : 0.016, 8, 0));
  }
}

export function buildGrounds(layout: SiteLayout): Asset {
  const kit = new Kit();
  slab(kit, 'plinthSide', layout.plinth, -1.2, 1.12, 0.3);
  slab(kit, 'plinth', layout.plinth, -0.14, 0.14, 0.06);
  for (const rect of layout.pavings) slab(kit, 'paving', rect, 0, 0.05, 0.02);
  for (const rect of layout.roads) slab(kit, 'road', rect, 0, 0.03, 0);
  for (const rect of layout.markings) slab(kit, 'marking', rect, 0.03, 0.012, 0);
  for (const rect of layout.curbs) slab(kit, 'curb', rect, 0, 0.14, 0.03);
  for (const rect of layout.lawns) slab(kit, 'lawn', rect, 0, 0.16, 0.08);

  // Plant room: epoxy floor, full walls at the back and left, street walls cut to sill height.
  const room = layout.plantRoom;
  const wall = 0.3;
  slab(kit, 'slab', { x: [room.x[0] - wall, room.x[1] + wall], z: [room.z[0] - wall, room.z[1] + wall] }, 0, 0.06, 0.02);
  slab(kit, 'floorStone', { x: [room.x[0] + 0.1, room.x[1] - 0.1], z: [room.z[0] + 0.1, room.z[1] - 0.1] }, 0.06, 0.01, 0);
  const walls: [Rect, number][] = [
    [{ x: [room.x[0] - wall, room.x[1] + wall], z: [room.z[0] - wall, room.z[0]] }, room.height],
    [{ x: [room.x[0] - wall, room.x[0]], z: [room.z[0], room.z[1] + wall] }, room.height],
    [{ x: [room.x[0], room.x[1] + wall], z: [room.z[1], room.z[1] + wall] }, 1.1],
    [{ x: [room.x[1], room.x[1] + wall], z: [room.z[0], room.z[1]] }, 1.1],
  ];
  for (const [rect, height] of walls) {
    slab(kit, 'wall', rect, 0.06, height, 0.03);
    slab(kit, 'panel', { x: [rect.x[0] - 0.02, rect.x[1] + 0.02], z: [rect.z[0] - 0.02, rect.z[1] + 0.02] }, 0.06 + height, 0.06, 0.02);
  }
  // Louvres high on the back wall, a door in the front wall's line.
  for (let x = room.x[0] + 2; x < room.x[1] - 2; x += 4) {
    kit.add('grille', box(1.6, 1.0, 0.04, 0.01), { at: [x, 3.4, room.z[0] + 0.03] });
    for (let slat = 0; slat < 6; slat += 1) kit.add('panel', box(1.6, 0.03, 0.08, 0.008), { at: [x, 2.98 + slat * 0.17, room.z[0] + 0.06], rotate: [0.5, 0, 0] });
  }
  for (const position of layout.chillers) kit.add('concrete', box(4.1, 0.15, 1.7, 0.03), { at: [position[0], 0.075, position[2]] });

  // Tower pad with its railing.
  if (layout.towers.length) {
    slab(kit, 'concrete', layout.towerPad, 0, 0.25, 0.05);
    railing(kit, { x: [layout.towerPad.x[0] + 0.15, layout.towerPad.x[1] - 0.15], z: [layout.towerPad.z[0] + 0.15, layout.towerPad.z[1] - 0.15] }, 0.25);
  }

  // Pipe rack posts between the plant room and the building.
  for (const [base, height] of layout.racks) {
    for (const dz of [-0.9, 0.9]) kit.add('darkSteel', box(0.18, height - 0.4, 0.18, 0.01), { at: [base[0], (height - 0.4) / 2, base[2] + dz] });
    for (const y of [3.62, 4.22]) kit.add('darkSteel', box(0.16, 0.12, 2.0, 0.01), { at: [base[0], y, base[2]] });
  }
  return kit.build();
}

export function Grounds({ layout }: { readonly layout: SiteLayout }) {
  const asset = useMemo(() => buildGrounds(layout), [layout]);
  const set = useMaterialSet();
  useEffect(() => () => { asset.parts.forEach((part) => part.dispose()); }, [asset]);
  return (
    <group>
      <AssetMesh asset={asset} set={set} />
      <InstancedAsset asset={furnitureAsset('tree')} placements={layout.trees} />
      <InstancedAsset asset={furnitureAsset('shrub')} placements={layout.shrubs} />
      <InstancedAsset asset={furnitureAsset('car')} placements={layout.cars} />
      <InstancedAsset asset={furnitureAsset('lamp')} placements={layout.lamps} />
    </group>
  );
}
