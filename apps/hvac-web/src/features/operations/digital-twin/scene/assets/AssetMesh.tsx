import { createContext, useContext, useLayoutEffect, useMemo, useRef } from 'react';
import * as THREE from 'three';
import type { Placement } from '../site-layout';
import type { Asset } from './kit';
import type { MaterialLibrary, MaterialSet } from './materials';

// Drawing assets: one mesh per material of a built model, or one instanced mesh per material for a
// model repeated many times. Which material variant is used comes from the scene: hovered and selected
// models lift, and when the pipework is brought forward everything else is ghosted.

export interface SceneMaterials {
  readonly library: MaterialLibrary;
  /** True while the pipe view washes out everything but water. */
  readonly ghosted: boolean;
  /** True when the viewer asked for reduced motion: moving parts stay put. */
  readonly still: boolean;
}

export const SceneMaterialsContext = createContext<SceneMaterials | null>(null);

export function useSceneMaterials(): SceneMaterials {
  const context = useContext(SceneMaterialsContext);
  if (!context) throw new Error('useSceneMaterials outside the twin scene');
  return context;
}

/** The material set a model draws with. */
export function useMaterialSet(lifted = false): MaterialSet {
  const { library, ghosted } = useSceneMaterials();
  if (ghosted) return library.ghost;
  return lifted ? library.hover : library.base;
}

export function AssetMesh({ asset, set, shadows = true }: { readonly asset: Asset; readonly set: MaterialSet; readonly shadows?: boolean }) {
  return (
    <>
      {[...asset.parts].map(([key, geometry]) => (
        <mesh key={key} geometry={geometry} material={set[key]} castShadow={shadows && key !== 'glass'} receiveShadow={key !== 'glass'} />
      ))}
    </>
  );
}

const dummy = new THREE.Object3D();

function InstancedPart({ geometry, material, placements, scale, shadows }: {
  readonly geometry: THREE.BufferGeometry;
  readonly material: THREE.Material;
  readonly placements: readonly Placement[];
  readonly scale: number;
  readonly shadows: boolean;
}) {
  const ref = useRef<THREE.InstancedMesh>(null);
  useLayoutEffect(() => {
    const mesh = ref.current;
    if (!mesh) return;
    placements.forEach((placement, index) => {
      dummy.position.set(...placement.at);
      dummy.rotation.set(0, placement.turn ?? 0, 0);
      dummy.scale.setScalar(scale);
      dummy.updateMatrix();
      mesh.setMatrixAt(index, dummy.matrix);
    });
    mesh.instanceMatrix.needsUpdate = true;
    mesh.computeBoundingSphere();
  }, [placements, scale]);
  return <instancedMesh ref={ref} args={[geometry, undefined, placements.length]} material={material} castShadow={shadows} receiveShadow frustumCulled={false} />;
}

/** A model drawn once per placement (scenery: it takes no pointer events). */
export function InstancedAsset({ asset, placements, scale = 1, shadows = true }: {
  readonly asset: Asset;
  readonly placements: readonly Placement[];
  readonly scale?: number;
  readonly shadows?: boolean;
}) {
  const set = useMaterialSet();
  const parts = useMemo(() => [...asset.parts], [asset]);
  if (!placements.length) return null;
  return (
    <group>
      {parts.map(([key, geometry]) => (
        <InstancedPart key={`${key}-${placements.length}`} geometry={geometry} material={set[key]} placements={placements} scale={scale} shadows={shadows && key !== 'glass'} />
      ))}
    </group>
  );
}
