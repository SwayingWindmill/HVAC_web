import { useEffect, useMemo } from 'react';
import { useFrame } from '@react-three/fiber';
import * as THREE from 'three';
import { mergeGeometries } from 'three/examples/jsm/utils/BufferGeometryUtils.js';
import { AssetMesh, useMaterialSet } from './assets/AssetMesh';
import { addValve, aim, Kit, tube, type V3 } from './assets/kit';
import type { DuctRun } from './building-layout';
import type { Fitting, Run } from './site-layout';

// Pipework and ductwork. Every run of a network is merged into one mesh whose vertices carry their
// distance along the run; the material's shader slides a soft band along that distance, so moving
// water (or air) costs no extra objects and stops dead when the loop is not running. Valves are built
// from the asset kit and drawn with the scene's materials.

export interface FlowMaterial {
  readonly material: THREE.MeshStandardMaterial;
  readonly uniforms: { readonly uTime: { value: number }; readonly uFlow: { value: number }; readonly uBand: { value: THREE.Color } };
}

export function createFlowMaterial(name: string, color: string, band: string, { spacing = 2.6, speed = 0.5, strength = 0.5, roughness = 0.4, metalness = 0.1 } = {}): FlowMaterial {
  const uniforms = { uTime: { value: 0 }, uFlow: { value: 0 }, uBand: { value: new THREE.Color(band) } };
  const material = new THREE.MeshStandardMaterial({ color, roughness, metalness });
  material.onBeforeCompile = (shader) => {
    Object.assign(shader.uniforms, uniforms);
    shader.vertexShader = shader.vertexShader
      .replace('#include <common>', '#include <common>\nattribute float aDistance;\nvarying float vDistance;')
      .replace('#include <begin_vertex>', '#include <begin_vertex>\nvDistance = aDistance;');
    shader.fragmentShader = shader.fragmentShader
      .replace('#include <common>', '#include <common>\nuniform float uTime;\nuniform float uFlow;\nuniform vec3 uBand;\nvarying float vDistance;')
      .replace(
        '#include <emissivemap_fragment>',
        `#include <emissivemap_fragment>\nfloat phase = fract(vDistance / ${spacing.toFixed(2)} - uTime * ${speed.toFixed(2)});\nfloat band = smoothstep(0.0, 0.4, phase) * (1.0 - smoothstep(0.4, 0.55, phase));\ntotalEmissiveRadiance += uBand * band * uFlow * ${strength.toFixed(2)};`,
      );
  };
  material.customProgramCacheKey = () => `hvac-flow-${name}`;
  return { material, uniforms };
}

/** Advances the bands of the given materials while their loops move. */
export function FlowClock({ materials }: { readonly materials: readonly FlowMaterial[] }) {
  useFrame((_, delta) => {
    for (const flow of materials) if (flow.uniforms.uFlow.value > 0) flow.uniforms.uTime.value += Math.min(delta, 0.1);
  });
  return null;
}

function withDistance(geometry: THREE.BufferGeometry, distanceAt: (point: THREE.Vector3) => number) {
  const part = geometry.index ? geometry.toNonIndexed() : geometry;
  for (const name of Object.keys(part.attributes)) if (name !== 'position' && name !== 'normal') part.deleteAttribute(name);
  const position = part.attributes.position;
  const distance = new Float32Array(position.count);
  const point = new THREE.Vector3();
  for (let index = 0; index < position.count; index += 1) distance[index] = distanceAt(point.fromBufferAttribute(position, index));
  part.setAttribute('aDistance', new THREE.BufferAttribute(distance, 1));
  return part;
}

/** A tube with rounded joints at its ends, its vertices carrying their distance along the run. */
function runGeometry(run: Run): THREE.BufferGeometry[] {
  const geometry = tube(run.points, run.radius, 14, run.radius * 2.6);
  const length = geometry.parameters.path.getLength();
  const tubular = geometry.parameters.tubularSegments;
  const radial = geometry.parameters.radialSegments;
  const distance = new Float32Array(geometry.attributes.position.count);
  for (let ring = 0; ring <= tubular; ring += 1) for (let side = 0; side <= radial; side += 1) distance[ring * (radial + 1) + side] = (ring / tubular) * length;
  geometry.setAttribute('aDistance', new THREE.BufferAttribute(distance, 1));
  const body = geometry.toNonIndexed();
  geometry.dispose();
  body.deleteAttribute('uv');
  const parts = [body];
  for (const [end, at] of [[run.points[0], 0], [run.points[run.points.length - 1], length]] as const) {
    const cap = new THREE.SphereGeometry(run.radius * 1.02, 14, 8);
    cap.translate(...end);
    parts.push(withDistance(cap, () => at));
  }
  return parts;
}

export function PipeNetwork({ runs, valves, flow, moving }: {
  readonly runs: readonly Run[];
  readonly valves: readonly Fitting[];
  readonly flow: FlowMaterial;
  readonly moving: boolean;
}) {
  const geometry = useMemo(() => {
    if (!runs.length) return null;
    const parts = runs.flatMap(runGeometry);
    const merged = mergeGeometries(parts, false);
    parts.forEach((part) => part.dispose());
    return merged;
  }, [runs]);
  const fittings = useMemo(() => {
    if (!valves.length) return null;
    const kit = new Kit();
    for (const valve of valves) addValve(kit, valve.radius, { at: valve.at, rotate: aim(valve.axis) });
    return kit.build();
  }, [valves]);
  const set = useMaterialSet();
  useEffect(() => { flow.uniforms.uFlow.value = moving ? 1 : 0; }, [flow, moving]);
  useEffect(() => () => { geometry?.dispose(); }, [geometry]);
  useEffect(() => () => { fittings?.parts.forEach((part) => part.dispose()); }, [fittings]);
  return (
    <group>
      {geometry ? <mesh geometry={geometry} material={flow.material} castShadow receiveShadow /> : null}
      {fittings ? <AssetMesh asset={fittings} set={set} /> : null}
    </group>
  );
}

const JOINT_PITCH = 1.2;

/** One duct run: box segments with filled corners and a flange joint every 1.2 m. */
function ductGeometry(run: DuctRun): THREE.BufferGeometry[] {
  const parts: THREE.BufferGeometry[] = [];
  let travelled = 0;
  for (let index = 0; index < run.points.length - 1; index += 1) {
    const a = new THREE.Vector3(...run.points[index]);
    const b = new THREE.Vector3(...run.points[index + 1]);
    const direction = b.clone().sub(a);
    const length = direction.length();
    if (length < 1e-3) continue;
    direction.normalize();
    const vertical = Math.abs(direction.y) > 0.9;
    const alongX = Math.abs(direction.x) > 0.9;
    const size = (extra: number, along: number): V3 => (vertical
      ? [run.width + extra, along, run.height + extra]
      : alongX ? [along, run.height + extra, run.width + extra] : [run.width + extra, run.height + extra, along]);
    const start = travelled;
    const distanceAt = (point: THREE.Vector3) => start + point.clone().sub(a).dot(direction);
    const body = new THREE.BoxGeometry(...size(0, length + (index < run.points.length - 2 ? run.width / 2 : 0)));
    body.translate(...a.clone().add(b).multiplyScalar(0.5).add(direction.clone().multiplyScalar(index < run.points.length - 2 ? run.width / 4 : 0)).toArray());
    parts.push(withDistance(body, distanceAt));
    for (let along = JOINT_PITCH; along < length - 0.2; along += JOINT_PITCH) {
      const joint = new THREE.BoxGeometry(...size(0.05, 0.04));
      joint.translate(...a.clone().add(direction.clone().multiplyScalar(along)).toArray());
      parts.push(withDistance(joint, distanceAt));
    }
    travelled += length;
  }
  return parts;
}

export function DuctNetwork({ runs, flow }: { readonly runs: readonly DuctRun[]; readonly flow: FlowMaterial }) {
  const geometry = useMemo(() => {
    if (!runs.length) return null;
    const parts = runs.flatMap(ductGeometry);
    const merged = mergeGeometries(parts, false);
    parts.forEach((part) => part.dispose());
    return merged;
  }, [runs]);
  useEffect(() => () => { geometry?.dispose(); }, [geometry]);
  return geometry ? <mesh geometry={geometry} material={flow.material} castShadow receiveShadow /> : null;
}
