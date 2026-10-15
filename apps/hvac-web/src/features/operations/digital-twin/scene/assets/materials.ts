import * as THREE from 'three';
import type { ScenePalette } from '../../use-scene-palette';

// One material library for the whole scene, so every model shares the same surfaces: warm white
// painted steel, brushed and galvanised metal, graphite machinery, matte insulation. Ground and status
// colours come from the design system's tokens; the model itself stays a light architectural white in
// both themes. Each surface exists in three variants: as is, hovered (a faint lift in the primary
// colour), and ghosted (washed out, for the view that brings the pipework forward).

export type MaterialKey =
  | 'paint' | 'panel' | 'paintDark' | 'steel' | 'darkSteel' | 'galvanized' | 'insulation' | 'rubber'
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

type Spec = THREE.MeshPhysicalMaterialParameters;

const mix = (from: string, to: string, amount: number) => `#${new THREE.Color(from).lerp(new THREE.Color(to), amount).getHexString()}`;

function specs(palette: ScenePalette): Record<MaterialKey, Spec> {
  const ground = (amount: number) => mix(palette.background, palette.foreground, amount);
  const top = ground(0.05);
  return {
    paint: { color: '#ebe9e4', roughness: 0.48, metalness: 0.18, clearcoat: 0.35, clearcoatRoughness: 0.4 },
    panel: { color: '#f7f6f3', roughness: 0.55, metalness: 0.05 },
    paintDark: { color: '#4b4f58', roughness: 0.42, metalness: 0.4, clearcoat: 0.2, clearcoatRoughness: 0.5 },
    steel: { color: '#c7cbd1', roughness: 0.28, metalness: 0.92 },
    darkSteel: { color: '#70747d', roughness: 0.38, metalness: 0.75 },
    galvanized: { color: '#d3d7dc', roughness: 0.36, metalness: 0.7 },
    insulation: { color: '#f2f1ed', roughness: 0.82, metalness: 0 },
    rubber: { color: '#2c2e33', roughness: 0.88, metalness: 0 },
    screen: { color: '#1b2030', roughness: 0.15, metalness: 0.2, emissive: palette.chilled, emissiveIntensity: 0.18 },
    dial: { color: '#fbfbfa', roughness: 0.5, metalness: 0 },
    concrete: { color: mix('#deded9', palette.background, 0.25), roughness: 0.95, metalness: 0 },
    glass: { color: '#dce8f1', roughness: 0.04, metalness: 0.1, transparent: true, opacity: 0.16, depthWrite: false, envMapIntensity: 1.4 },
    mullion: { color: '#5c6069', roughness: 0.36, metalness: 0.65 },
    wall: { color: '#f3f1ec', roughness: 0.9, metalness: 0 },
    slab: { color: '#e2e1dc', roughness: 0.88, metalness: 0 },
    floorStone: { color: '#eeece7', roughness: 0.42, metalness: 0, clearcoat: 0.3, clearcoatRoughness: 0.3 },
    floorWood: { color: '#e1cfb3', roughness: 0.62, metalness: 0 },
    floorCarpet: { color: '#d4d3d6', roughness: 1, metalness: 0 },
    wood: { color: '#c7a27a', roughness: 0.55, metalness: 0, clearcoat: 0.3, clearcoatRoughness: 0.35 },
    fabric: { color: '#e4ded5', roughness: 0.95, metalness: 0 },
    cloth: { color: '#fcfbf9', roughness: 0.9, metalness: 0 },
    leaf: { color: mix('#a9bf9b', palette.background, 0.1), roughness: 0.85, metalness: 0 },
    bark: { color: '#9a8977', roughness: 0.9, metalness: 0 },
    stainless: { color: '#d9dce0', roughness: 0.22, metalness: 0.9 },
    grille: { color: '#3a3d44', roughness: 0.6, metalness: 0.5 },
    plinth: { color: top, roughness: 0.95, metalness: 0 },
    plinthSide: { color: ground(0.12), roughness: 0.9, metalness: 0 },
    road: { color: ground(0.1), roughness: 0.95, metalness: 0 },
    marking: { color: mix(top, '#ffffff', 0.85), roughness: 0.8, metalness: 0 },
    lawn: { color: mix(top, '#86a874', 0.32), roughness: 1, metalness: 0 },
    curb: { color: ground(0.025), roughness: 0.9, metalness: 0 },
    paving: { color: ground(0.035), roughness: 0.85, metalness: 0 },
    ledRun: { color: palette.running, emissive: palette.running, emissiveIntensity: 1.6, roughness: 0.3, toneMapped: false },
    ledFault: { color: palette.fault, emissive: palette.fault, emissiveIntensity: 1.4, roughness: 0.3, toneMapped: false },
    ledOff: { color: '#3a3d44', roughness: 0.3, metalness: 0.2 },
    chilledAccent: { color: palette.chilled, roughness: 0.4, metalness: 0.2 },
    coolingAccent: { color: palette.cooling, roughness: 0.4, metalness: 0.2 },
  };
}

/** Clearcoat needs the physical shader; every other surface takes the cheaper standard one. */
function create(spec: Spec): THREE.Material {
  if (spec.clearcoat) return new THREE.MeshPhysicalMaterial(spec);
  const { clearcoat: _clearcoat, clearcoatRoughness: _roughness, ...standard } = spec;
  return new THREE.MeshStandardMaterial(standard);
}

export function createMaterialLibrary(palette: ScenePalette): MaterialLibrary {
  const all = specs(palette);
  const entries = Object.entries(all) as [MaterialKey, Spec][];
  const lift = new THREE.Color(palette.chilled);
  const base = Object.fromEntries(entries.map(([key, spec]) => [key, create(spec)])) as MaterialSet;
  const hover = Object.fromEntries(entries.map(([key, spec]) => [key, create({ ...spec, emissive: spec.emissive ?? lift, emissiveIntensity: spec.emissive ? spec.emissiveIntensity : 0.16 })])) as MaterialSet;
  const ghost = Object.fromEntries(entries.map(([key, spec]) => {
    if (key === 'glass') return [key, create({ ...spec, opacity: 0.05 })];
    const { emissive: _emissive, emissiveIntensity: _intensity, clearcoat: _clearcoat, clearcoatRoughness: _coat, ...rest } = spec;
    return [key, create({ ...rest, color: mix(spec.color as string, palette.background, 0.62), metalness: 0, roughness: 1 })];
  })) as MaterialSet;
  return { base, hover, ghost };
}

export function disposeMaterialLibrary(library: MaterialLibrary) {
  for (const set of [library.base, library.hover, library.ghost]) Object.values(set).forEach((material) => material.dispose());
}
