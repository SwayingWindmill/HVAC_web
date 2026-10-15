import * as THREE from 'three';
import { box, dome, drum, Kit, lathe, type Asset } from './kit';

// Furniture and site props at real sizes, drawn instanced: a few dozen chairs cost one draw call per
// material. Kept quiet in colour (warm white, light oak, linen) so the HVAC stays the subject.

function chair(): Asset {
  const kit = new Kit();
  kit.add('fabric', box(0.44, 0.08, 0.44, 0.03), { at: [0, 0.46, 0] });
  kit.add('fabric', box(0.44, 0.48, 0.07, 0.03), { at: [0, 0.76, -0.2], rotate: [-0.1, 0, 0] });
  for (const [x, z] of [[-0.18, -0.18], [-0.18, 0.18], [0.18, -0.18], [0.18, 0.18]]) {
    kit.add('wood', new THREE.CylinderGeometry(0.018, 0.014, 0.44, 8), { at: [x, 0.22, z] });
  }
  return kit.build();
}

function roundTable(radius: number) {
  return () => {
    const kit = new Kit();
    kit.add('wood', lathe([[0.32, 0], [0.32, 0.03], [0.07, 0.06], [0.06, 0.7], [0.12, 0.72], [0, 0.72]], 28));
    kit.add('cloth', drum(radius, 0.05, 0.02, 48), { at: [0, 0.72, 0] });
    kit.add('cloth', lathe([[radius, 0.72], [radius + 0.015, 0.6], [radius + 0.01, 0.6]], 48));
    if (radius > 0.7) kit.add('wood', drum(radius * 0.45, 0.02, 0.008, 40), { at: [0, 0.77, 0] });
    return kit.build();
  };
}

function sofa(): Asset {
  const kit = new Kit();
  kit.add('fabric', box(1.9, 0.42, 0.82, 0.08), { at: [0, 0.25, 0] });
  kit.add('fabric', box(1.9, 0.45, 0.2, 0.08), { at: [0, 0.62, -0.31] });
  for (const x of [-0.88, 0.88]) kit.add('fabric', box(0.18, 0.56, 0.82, 0.07), { at: [x, 0.32, 0] });
  for (const x of [-0.42, 0.42]) kit.add('cloth', box(0.8, 0.1, 0.6, 0.04), { at: [x, 0.5, 0.06] });
  return kit.build();
}

function lowTable(): Asset {
  const kit = new Kit();
  kit.add('wood', box(1.0, 0.05, 0.55, 0.02), { at: [0, 0.42, 0] });
  for (const [x, z] of [[-0.44, -0.22], [-0.44, 0.22], [0.44, -0.22], [0.44, 0.22]]) kit.add('wood', box(0.04, 0.4, 0.04, 0.01), { at: [x, 0.2, z] });
  return kit.build();
}

function sideboard(): Asset {
  const kit = new Kit();
  kit.add('wood', box(1.8, 0.82, 0.48, 0.02), { at: [0, 0.45, 0] });
  for (const x of [-0.6, 0, 0.6]) kit.add('panel', box(0.56, 0.66, 0.012, 0.006), { at: [x, 0.47, 0.245] });
  kit.add('paintDark', box(1.76, 0.04, 0.44, 0.01), { at: [0, 0.02, 0] });
  return kit.build();
}

function plant(): Asset {
  const kit = new Kit();
  kit.add('panel', lathe([[0, 0], [0.16, 0], [0.2, 0.38], [0.18, 0.38], [0, 0.36]], 24));
  const leaves = new THREE.IcosahedronGeometry(0.34, 1);
  kit.add('leaf', leaves, { at: [0, 0.72, 0], scale: [1, 1.15, 1] });
  kit.add('leaf', new THREE.IcosahedronGeometry(0.22, 1), { at: [0.12, 1.02, 0.05] });
  return kit.build();
}

function reception(): Asset {
  const kit = new Kit();
  kit.add('panel', box(2.6, 1.05, 0.62, 0.04), { at: [0, 0.525, 0] });
  kit.add('wood', box(2.7, 0.05, 0.72, 0.02), { at: [0, 1.08, 0] });
  kit.add('paintDark', box(2.5, 0.08, 0.5, 0.01), { at: [0, 0.04, 0] });
  return kit.build();
}

function counter(): Asset {
  const kit = new Kit();
  kit.add('stainless', box(2.4, 0.86, 0.7, 0.02), { at: [0, 0.45, 0] });
  kit.add('stainless', box(2.44, 0.04, 0.74, 0.01), { at: [0, 0.9, 0] });
  for (const x of [-0.8, 0, 0.8]) kit.add('steel', box(0.7, 0.6, 0.012, 0.004), { at: [x, 0.45, 0.356] });
  return kit.build();
}

function hood(): Asset {
  const kit = new Kit();
  kit.add('stainless', lathe([[1.1, 0], [0.4, 0.5], [0.001, 0.5]], 4), { rotate: [0, Math.PI / 4, 0], scale: [1.1, 1, 0.45] });
  kit.add('stainless', box(0.42, 1.2, 0.42, 0.02), { at: [0, 1.1, 0] });
  return kit.build();
}

function tree(): Asset {
  const kit = new Kit();
  kit.add('bark', lathe([[0.13, 0], [0.09, 0.5], [0.07, 2.1], [0.001, 2.2]], 10));
  kit.add('leaf', new THREE.IcosahedronGeometry(1.25, 2), { at: [0, 2.9, 0], scale: [1, 1.08, 1] });
  kit.add('leaf', new THREE.IcosahedronGeometry(0.85, 2), { at: [0.55, 3.55, 0.25] });
  kit.add('leaf', new THREE.IcosahedronGeometry(0.8, 2), { at: [-0.5, 3.45, -0.35] });
  return kit.build();
}

function shrub(): Asset {
  const kit = new Kit();
  kit.add('leaf', new THREE.IcosahedronGeometry(0.55, 2), { at: [0, 0.38, 0], scale: [1.3, 0.8, 1] });
  kit.add('leaf', new THREE.IcosahedronGeometry(0.4, 2), { at: [0.45, 0.32, 0.1], scale: [1.2, 0.8, 1] });
  return kit.build();
}

/** A compact car, 4.4 m: body, glasshouse, wheels. */
function car(): Asset {
  const kit = new Kit();
  kit.add('panel', box(4.4, 0.62, 1.78, 0.22, 3), { at: [0, 0.6, 0] });
  kit.add('panel', box(2.3, 0.56, 1.58, 0.22, 3), { at: [-0.25, 1.12, 0] });
  kit.add('screen', box(2.22, 0.44, 1.6, 0.18, 3), { at: [-0.25, 1.12, 0] });
  kit.add('panel', box(2.0, 0.08, 1.5, 0.04), { at: [-0.25, 1.4, 0] });
  for (const [x, z] of [[-1.35, -0.8], [-1.35, 0.8], [1.35, -0.8], [1.35, 0.8]]) {
    const wheel = drum(0.33, 0.24, 0.06, 20);
    wheel.translate(0, -0.12, 0);
    wheel.rotateX(Math.PI / 2);
    kit.add('rubber', wheel, { at: [x, 0.33, z] });
  }
  return kit.build();
}

/** A stair flight, 1.2 m wide, rising 2.1 m over 3.6 m along x. */
function stairFlight(): Asset {
  const kit = new Kit();
  for (let step = 0; step < 12; step += 1) kit.add('slab', box(0.3, 0.175 * (step + 1), 1.2, 0.006), { at: [step * 0.3 + 0.15, (0.175 * (step + 1)) / 2, 0] });
  return kit.build();
}

function lamp(): Asset {
  const kit = new Kit();
  kit.add('paintDark', drum(0.05, 4.2, 0.01, 10));
  kit.add('paintDark', box(0.7, 0.06, 0.16, 0.02), { at: [0.3, 4.2, 0] });
  kit.add('panel', dome(0.12, 0.04, 16), { at: [0.55, 4.16, 0], rotate: [Math.PI, 0, 0] });
  return kit.build();
}

const builders = {
  chair,
  banquetTable: roundTable(0.95),
  roomTable: roundTable(0.72),
  hallTable: roundTable(0.55),
  sofa,
  lowTable,
  sideboard,
  plant,
  reception,
  counter,
  hood,
  tree,
  shrub,
  car,
  stairFlight,
  lamp,
} satisfies Record<string, () => Asset>;

export type FurnitureAsset = keyof typeof builders;

const cache = new Map<FurnitureAsset, Asset>();

export function furnitureAsset(name: FurnitureAsset): Asset {
  const cached = cache.get(name);
  if (cached) return cached;
  const asset = builders[name]();
  cache.set(name, asset);
  return asset;
}
