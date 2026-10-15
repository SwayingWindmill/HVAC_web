import * as THREE from 'three';
import type { ScenePalette } from '../../use-scene-palette';

// One material library for the whole scene, so every model shares the same surfaces: warm white
// painted steel, brushed and galvanised metal, graphite machinery, matte insulation. Ground and status
// colours come from the design system's tokens; the model itself stays a light architectural white in
// both themes. Each surface exists in three variants: as is, hovered (a faint lift in the primary
// colour), and ghosted (washed out, for the view that brings the pipework forward).

export type MaterialKey =
  | 'brand' | 'window' | 'paint' | 'panel' | 'paintDark' | 'steel' | 'darkSteel' | 'galvanized' | 'insulation' | 'rubber'
  | 'screen' | 'dial' | 'concrete' | 'glass' | 'mullion' | 'wall' | 'slab' | 'floorStone' | 'floorWood'
  | 'floorCarpet' | 'wood' | 'fabric' | 'cloth' | 'leaf' | 'bark' | 'stainless' | 'grille'
  | 'plinth' | 'plinthSide' | 'road' | 'marking' | 'lawn' | 'curb' | 'paving'
  | 'ledRun' | 'ledFault' | 'ledOff' | 'chilledAccent' | 'coolingAccent';

export type MaterialSet = Readonly<Record<MaterialKey, THREE.Material>>;

export interface MaterialLibrary {
  readonly base: MaterialSet;
  readonly hover: MaterialSet;
  readonly ghost: MaterialSet;
}

type Spec = THREE.MeshStandardMaterialParameters;

const mix = (from: string, to: string, amount: number) => `#${new THREE.Color(from).lerp(new THREE.Color(to), amount).getHexString()}`;

function specs(palette: ScenePalette): Record<MaterialKey, Spec> {
  // A cool, pale world tinted toward the primary colour; the machines a viewer can click wear the
  // primary itself, white and silver carry the rest, and only furniture and timber stay warm.
  const world = (amount: number) => mix(palette.background, palette.chilled, amount);
  const ground = world(0.08);
  return {
    brand: { color: palette.chilled, roughness: 0.42, metalness: 0.12 },
    window: { color: mix('#d3defb', palette.background, 0.3), roughness: 0.25, metalness: 0.15 },
    paint: { color: '#f4f5f9', roughness: 0.46, metalness: 0.1 },
    panel: { color: '#fbfbfd', roughness: 0.55, metalness: 0.04 },
    paintDark: { color: '#3d4356', roughness: 0.42, metalness: 0.35 },
    steel: { color: '#c9cedb', roughness: 0.28, metalness: 0.9 },
    darkSteel: { color: '#727a8f', roughness: 0.38, metalness: 0.72 },
    galvanized: { color: '#d8dce7', roughness: 0.36, metalness: 0.68 },
    insulation: { color: '#f6f7fa', roughness: 0.8, metalness: 0 },
    rubber: { color: '#2b2f3c', roughness: 0.88, metalness: 0 },
    screen: { color: '#1a2036', roughness: 0.15, metalness: 0.2, emissive: palette.chilled, emissiveIntensity: 0.25 },
    dial: { color: '#fbfbfd', roughness: 0.5, metalness: 0 },
    concrete: { color: world(0.08), roughness: 0.95, metalness: 0 },
    glass: { color: mix('#d6e2ff', palette.background, 0.2), roughness: 0.04, metalness: 0.1, transparent: true, opacity: 0.24, depthWrite: false, envMapIntensity: 1.4 },
    mullion: { color: '#b9c1d9', roughness: 0.36, metalness: 0.5 },
    wall: { color: '#fbfbfd', roughness: 0.88, metalness: 0 },
    slab: { color: '#eceef5', roughness: 0.88, metalness: 0 },
    floorStone: { color: '#f2f3f7', roughness: 0.42, metalness: 0 },
    floorWood: { color: '#e6d6bd', roughness: 0.62, metalness: 0 },
    floorCarpet: { color: '#dadced', roughness: 1, metalness: 0 },
    wood: { color: '#d0ac83', roughness: 0.55, metalness: 0 },
    fabric: { color: '#e8e4f0', roughness: 0.95, metalness: 0 },
    cloth: { color: '#fdfdff', roughness: 0.9, metalness: 0 },
    leaf: { color: mix('#6fc39a', palette.background, 0.08), roughness: 0.82, metalness: 0 },
    bark: { color: '#b8a58f', roughness: 0.9, metalness: 0 },
    stainless: { color: '#dde1ea', roughness: 0.22, metalness: 0.9 },
    grille: { color: '#363b4d', roughness: 0.6, metalness: 0.5 },
    plinth: { color: ground, roughness: 0.96, metalness: 0 },
    plinthSide: { color: world(0.1), roughness: 0.9, metalness: 0 },
    road: { color: world(0.2), roughness: 0.95, metalness: 0 },
    marking: { color: mix(ground, '#ffffff', 0.92), roughness: 0.8, metalness: 0 },
    lawn: { color: mix(ground, '#8fd3a8', 0.42), roughness: 1, metalness: 0 },
    curb: { color: mix(ground, '#ffffff', 0.7), roughness: 0.9, metalness: 0 },
    paving: { color: mix(ground, '#ffffff', 0.45), roughness: 0.85, metalness: 0 },
    ledRun: { color: palette.running, emissive: palette.running, emissiveIntensity: 1.6, roughness: 0.3, toneMapped: false },
    ledFault: { color: palette.fault, emissive: palette.fault, emissiveIntensity: 1.4, roughness: 0.3, toneMapped: false },
    ledOff: { color: '#3a3d4a', roughness: 0.3, metalness: 0.2 },
    chilledAccent: { color: palette.chilled, roughness: 0.4, metalness: 0.2 },
    coolingAccent: { color: palette.cooling, roughness: 0.4, metalness: 0.2 },
  };
}

/** Every surface uses the standard material, so the whole scene compiles a handful of shader programs. */
function create(spec: Spec): THREE.Material {
  return new THREE.MeshStandardMaterial(spec);
}

export function createMaterialLibrary(palette: ScenePalette): MaterialLibrary {
  const all = specs(palette);
  const entries = Object.entries(all) as [MaterialKey, Spec][];
  const lift = new THREE.Color(palette.chilled);
  const base = Object.fromEntries(entries.map(([key, spec]) => [key, create(spec)])) as MaterialSet;
  const hover = Object.fromEntries(entries.map(([key, spec]) => [key, create({ ...spec, emissive: spec.emissive ?? lift, emissiveIntensity: spec.emissive ? spec.emissiveIntensity : 0.07 })])) as MaterialSet;
  const ghost = Object.fromEntries(entries.map(([key, spec]) => {
    if (key === 'glass') return [key, create({ ...spec, opacity: 0.05 })];
    const { emissive: _emissive, emissiveIntensity: _intensity, ...rest } = spec;
    return [key, create({ ...rest, color: mix(spec.color as string, palette.background, 0.62), metalness: 0, roughness: 1 })];
  })) as MaterialSet;
  return { base, hover, ghost };
}

export function disposeMaterialLibrary(library: MaterialLibrary) {
  for (const set of [library.base, library.hover, library.ghost]) Object.values(set).forEach((material) => material.dispose());
}
