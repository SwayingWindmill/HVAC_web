import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/examples/jsm/geometries/RoundedBoxGeometry.js';
import { mergeGeometries } from 'three/examples/jsm/utils/BufferGeometryUtils.js';
import type { MaterialKey } from './materials';

// The asset library's building kit. Every model is assembled once from shaped parts (filleted lathes,
// bevelled boxes, swept tubes, bolt circles) in metres, then merged into one geometry per material,
// so a machine costs a handful of draw calls however detailed it is. Conventions: y is up, a model
// stands on y = 0, its length runs along x, and its front (doors, panels, gauges) faces +z.

export type V3 = readonly [number, number, number];

export interface Place {
  readonly at?: V3;
  readonly rotate?: V3;
  readonly scale?: V3;
}

/** A built model: one merged geometry per material, plus named points pipes connect to. */
export interface Asset {
  readonly parts: ReadonlyMap<MaterialKey, THREE.BufferGeometry>;
  readonly ports: Readonly<Record<string, V3>>;
}

const matrix = new THREE.Matrix4();
const quaternion = new THREE.Quaternion();
const euler = new THREE.Euler();

export class Kit {
  private readonly parts = new Map<MaterialKey, THREE.BufferGeometry[]>();
  readonly ports: Record<string, V3> = {};

  add(material: MaterialKey, geometry: THREE.BufferGeometry, place: Place = {}): this {
    const part = geometry.index ? geometry.toNonIndexed() : geometry;
    if (part !== geometry) geometry.dispose();
    for (const name of Object.keys(part.attributes)) if (name !== 'position' && name !== 'normal' && name !== 'uv') part.deleteAttribute(name);
    if (!part.attributes.uv) part.setAttribute('uv', new THREE.BufferAttribute(new Float32Array(part.attributes.position.count * 2), 2));
    const [x, y, z] = place.at ?? [0, 0, 0];
    const [rx, ry, rz] = place.rotate ?? [0, 0, 0];
    const [sx, sy, sz] = place.scale ?? [1, 1, 1];
    matrix.compose(new THREE.Vector3(x, y, z), quaternion.setFromEuler(euler.set(rx, ry, rz)), new THREE.Vector3(sx, sy, sz));
    part.applyMatrix4(matrix);
    const list = this.parts.get(material) ?? [];
    list.push(part);
    this.parts.set(material, list);
    return this;
  }

  /** Adds every part of another built asset, placed. */
  include(asset: Asset, place: Place = {}): this {
    for (const [material, geometry] of asset.parts) this.add(material, geometry.clone(), place);
    return this;
  }

  build(): Asset {
    const parts = new Map<MaterialKey, THREE.BufferGeometry>();
    for (const [material, list] of this.parts) {
      const merged = mergeGeometries(list, false);
      list.forEach((geometry) => geometry.dispose());
      merged.computeBoundingSphere();
      parts.set(material, merged);
    }
    return { parts, ports: { ...this.ports } };
  }
}

// ── Shapes ────────────────────────────────────────────────────────────────────────────────────────

/** A box with rounded edges; the radius is clamped so thin plates stay valid. */
export function box(width: number, height: number, depth: number, radius = 0.02, segments = 2) {
  const r = Math.min(radius, width / 2 - 1e-4, height / 2 - 1e-4, depth / 2 - 1e-4);
  return r <= 0.001 ? new THREE.BoxGeometry(width, height, depth) : new RoundedBoxGeometry(width, height, depth, segments, r);
}

/** A surface of revolution about y from [radius, y] points. */
export function lathe(profile: readonly (readonly [number, number])[], segments = 32) {
  return new THREE.LatheGeometry(profile.map(([r, y]) => new THREE.Vector2(Math.max(r, 1e-4), y)), segments);
}

function arc(cx: number, cy: number, radius: number, from: number, to: number, steps = 4): [number, number][] {
  return Array.from({ length: steps + 1 }, (_, index) => {
    const angle = from + ((to - from) * index) / steps;
    return [cx + radius * Math.cos(angle), cy + radius * Math.sin(angle)];
  });
}

/** A solid cylinder about y, from y = 0 to height, with filleted rims (a machined or moulded edge). */
export function drum(radius: number, height: number, fillet = 0.015, segments = 32) {
  const f = Math.min(fillet, radius / 2, height / 2);
  return lathe([
    [0, 0],
    ...arc(radius - f, f, f, -Math.PI / 2, 0),
    ...arc(radius - f, height - f, f, 0, Math.PI / 2),
    [0, height],
  ], segments);
}

/** A drum lying along x, centred on the origin. */
export function drumX(radius: number, length: number, fillet = 0.015, segments = 32) {
  const geometry = drum(radius, length, fillet, segments);
  geometry.translate(0, -length / 2, 0);
  geometry.rotateZ(-Math.PI / 2);
  return geometry;
}

/** A drum lying along z, centred on the origin. */
export function drumZ(radius: number, length: number, fillet = 0.015, segments = 32) {
  const geometry = drum(radius, length, fillet, segments);
  geometry.translate(0, -length / 2, 0);
  geometry.rotateX(Math.PI / 2);
  return geometry;
}

/** A dished head (the end of a pressure vessel), bulging up from y = 0. */
export function dome(radius: number, depth: number, segments = 32) {
  const profile: [number, number][] = Array.from({ length: 9 }, (_, index) => {
    const angle = (index / 8) * (Math.PI / 2);
    return [radius * Math.cos(angle), depth * Math.sin(angle)];
  });
  return lathe(profile, segments);
}

/** A tube along points with rounded corners. */
export function tube(points: readonly V3[], radius: number, radial = 12, corner = 0.12) {
  const vectors = points.map((point) => new THREE.Vector3(...point));
  const path = new THREE.CurvePath<THREE.Vector3>();
  let start = vectors[0].clone();
  for (let index = 1; index < vectors.length - 1; index += 1) {
    const [previous, joint, next] = [vectors[index - 1], vectors[index], vectors[index + 1]];
    const r = Math.min(corner, previous.distanceTo(joint) / 2, joint.distanceTo(next) / 2);
    const enter = joint.clone().add(previous.clone().sub(joint).normalize().multiplyScalar(r));
    const leave = joint.clone().add(next.clone().sub(joint).normalize().multiplyScalar(r));
    if (start.distanceTo(enter) > 1e-4) path.add(new THREE.LineCurve3(start, enter));
    path.add(new THREE.QuadraticBezierCurve3(enter, joint.clone(), leave));
    start = leave;
  }
  path.add(new THREE.LineCurve3(start, vectors[vectors.length - 1]));
  const segments = Math.max(4, Math.round(path.getLength() * 8) + (points.length - 2) * 6);
  return new THREE.TubeGeometry(path, segments, radius, radial, false);
}

/** A ring (washer) about y, centred at y = 0. */
export function ring(outer: number, inner: number, thickness: number, segments = 32) {
  return lathe([[inner, -thickness / 2], [outer, -thickness / 2], [outer, thickness / 2], [inner, thickness / 2], [inner, -thickness / 2]], segments);
}

/** A torus about y. */
export function torus(radius: number, tubeRadius: number, segments = 32) {
  const geometry = new THREE.TorusGeometry(radius, tubeRadius, 8, segments);
  geometry.rotateX(Math.PI / 2);
  return geometry;
}

/** A pipe flange: a filleted disc with a circle of bolt heads, facing +y, centred at y = 0. */
export function addFlange(kit: Kit, material: MaterialKey, radius: number, place: Place, bolts = 8) {
  const thickness = Math.max(0.025, radius * 0.22);
  const flange = drum(radius, thickness, thickness * 0.3, 28);
  flange.translate(0, -thickness / 2, 0);
  kit.add(material, flange, place);
  const boltGeometry = () => {
    const bolt = new THREE.CylinderGeometry(radius * 0.09, radius * 0.09, thickness * 1.7, 6);
    return bolt;
  };
  const boltParts: THREE.BufferGeometry[] = [];
  for (let index = 0; index < bolts; index += 1) {
    const angle = (index / bolts) * Math.PI * 2;
    const bolt = boltGeometry();
    bolt.translate(Math.cos(angle) * radius * 0.78, 0, Math.sin(angle) * radius * 0.78);
    boltParts.push(bolt.toNonIndexed());
    bolt.dispose();
  }
  kit.add('darkSteel', mergeGeometries(boltParts, false), place);
  boltParts.forEach((part) => part.dispose());
}

/** Radial fins around a drum lying along x (a motor's cooling ribs). */
export function addFins(kit: Kit, material: MaterialKey, radius: number, length: number, count: number, fin: number, place: Place) {
  for (let index = 0; index < count; index += 1) {
    const angle = (index / count) * Math.PI * 2;
    const geometry = box(length, fin, 0.012, 0.004, 1);
    geometry.translate(0, radius + fin / 2 - 0.004, 0);
    geometry.rotateX(angle);
    kit.add(material, geometry, place);
  }
}

/** Rotation that turns +y onto a direction. */
export function aim(direction: V3): V3 {
  const q = new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 1, 0), new THREE.Vector3(...direction).normalize());
  const e = new THREE.Euler().setFromQuaternion(q);
  return [e.x, e.y, e.z];
}

/** A butterfly valve on a pipe of the given radius: wafer body, flanges, gear operator and handwheel. Axis +y. */
export function addValve(kit: Kit, radius: number, place: Place, body: MaterialKey = 'paintDark') {
  const sub = new Kit();
  sub.add(body, drum(radius * 1.3, radius * 1.1, radius * 0.2), { at: [0, -radius * 0.55, 0] });
  addFlange(sub, 'steel', radius * 1.55, { at: [0, -radius * 0.7, 0] });
  addFlange(sub, 'steel', radius * 1.55, { at: [0, radius * 0.7, 0] });
  // Stem, gearbox and handwheel stand off the side (+z) of the pipe.
  sub.add('steel', new THREE.CylinderGeometry(radius * 0.16, radius * 0.16, radius * 1.1, 10), { at: [0, 0, radius * 1.6], rotate: [Math.PI / 2, 0, 0] });
  sub.add(body, box(radius * 1.1, radius * 0.9, radius * 0.75, radius * 0.12), { at: [0, 0, radius * 2.35] });
  sub.add('steel', torus(radius * 0.75, radius * 0.07, 28), { at: [0, 0, radius * 2.95], rotate: [Math.PI / 2, 0, 0] });
  for (let index = 0; index < 3; index += 1) {
    sub.add('steel', new THREE.CylinderGeometry(radius * 0.045, radius * 0.045, radius * 1.5, 6), { at: [0, 0, radius * 2.95], rotate: [0, 0, (index / 3) * Math.PI] });
  }
  kit.include(sub.build(), place);
}

/** A dial gauge on a short stem, its face toward +z. */
export function addGauge(kit: Kit, size: number, place: Place) {
  const sub = new Kit();
  sub.add('steel', new THREE.CylinderGeometry(size * 0.12, size * 0.12, size * 0.9, 8), { at: [0, size * 0.45, 0] });
  sub.add('steel', drumZ(size * 0.5, size * 0.28, size * 0.06, 24), { at: [0, size * 1.15, 0] });
  sub.add('dial', new THREE.CircleGeometry(size * 0.42, 24), { at: [0, size * 1.15, size * 0.141] });
  sub.add('paintDark', box(size * 0.04, size * 0.34, size * 0.01, 0), { at: [size * 0.08, size * 1.2, size * 0.15], rotate: [0, 0, -0.7] });
  kit.include(sub.build(), place);
}
