import * as THREE from 'three';
import type { TwinState } from '@/features/operations/digital-twin/twin-model';
import type { V3 } from '@/features/operations/digital-twin/scene/assets/kit';
import type { SiteLayout } from '@/features/operations/digital-twin/scene/site-layout';
import { CUES } from '../film/cues';

// What the twin does at every moment of the film: where the camera is, what hour the sun says, which
// machines run, and which of the product's own view switches (floor cut, facade, pipes forward,
// selection) are on. Every value is a pure function of film time, so any frame renders on its own.

export interface FilmState {
  readonly chillers: readonly TwinState[];
  readonly chilledPumps: readonly TwinState[];
  readonly coolingPumps: readonly TwinState[];
  readonly towers: readonly TwinState[];
  /** Floor cut as in the product: floors up to this level; null is the whole building. */
  readonly cut: number | null;
  readonly shell: boolean;
  /** The product's 「突出管路」: everything but the water washes out. */
  readonly emphasis: boolean;
  /** An asset id; the product's corner brackets mark it. */
  readonly selected: string | null;
}

export interface CameraPose {
  readonly eye: V3;
  readonly target: V3;
  readonly fov: number;
  /** Moves the picture across the frame, as a share of its width (+ right) and height (+ down). */
  readonly shift?: readonly [number, number];
}

export interface Places {
  readonly centre: V3;
  readonly plant: V3;
  readonly building: V3;
  readonly chillers: readonly V3[];
  readonly towers: readonly V3[];
  readonly chilledPumps: readonly V3[];
  readonly coolingPumps: readonly V3[];
}

export function placesOf(layout: SiteLayout, buildingSize: readonly [number, number, number] | null): Places {
  const mid = (rect: { x: readonly [number, number]; z: readonly [number, number] }, y = 0): V3 => [(rect.x[0] + rect.x[1]) / 2, y, (rect.z[0] + rect.z[1]) / 2];
  const origin = layout.buildingOrigin ?? [0, 0, 0];
  return {
    centre: mid(layout.plinth),
    plant: mid(layout.plantRoom, layout.plantRoom.height / 2),
    building: buildingSize ? [origin[0] + buildingSize[0] / 2, buildingSize[1] / 2, origin[2] + buildingSize[2] / 2] : mid(layout.plinth),
    chillers: layout.chillers,
    towers: layout.towers,
    chilledPumps: layout.chilledPumps,
    coolingPumps: layout.coolingPumps,
  };
}

/** A camera looking at a point from a distance, at a polar angle from straight up and an azimuth. */
export function orbit(target: V3, distance: number, polar: number, azimuth: number, fov = 28): CameraPose {
  const eye = new THREE.Vector3().setFromSphericalCoords(distance, polar, azimuth).add(new THREE.Vector3(...target));
  return { eye: eye.toArray() as V3, target, fov };
}

export type Ease = 'inOut' | 'out' | 'in' | 'linear';

export interface CameraKey {
  readonly t: number;
  readonly pose: CameraPose;
  /** The ease of the move that arrives at this key. */
  readonly ease?: Ease;
}

const EASES: Readonly<Record<Ease, (x: number) => number>> = {
  linear: (x) => x,
  in: (x) => x * x * x,
  out: (x) => 1 - (1 - x) ** 4,
  inOut: (x) => (x < 0.5 ? 4 * x * x * x : 1 - (-2 * x + 2) ** 3 / 2),
};

/** Shots joined by hard cuts: each shot is its own path, and between shots the earlier one holds. */
export function cutPath(shots: readonly (readonly CameraKey[])[]): (t: number) => CameraPose {
  const paths = shots.map((keys) => ({ from: keys[0].t, path: cameraPath(keys) }));
  return (t) => {
    let current = paths[0];
    for (const shot of paths) if (t >= shot.from) current = shot;
    return current.path(t);
  };
}

/** Keys joined by centripetal Catmull-Rom curves, each move on its own ease. */
export function cameraPath(keys: readonly CameraKey[]): (t: number) => CameraPose {
  if (keys.length === 1) return () => keys[0].pose;
  const eyes = new THREE.CatmullRomCurve3(keys.map((key) => new THREE.Vector3(...key.pose.eye)), false, 'centripetal');
  const targets = new THREE.CatmullRomCurve3(keys.map((key) => new THREE.Vector3(...key.pose.target)), false, 'centripetal');
  const span = keys.length - 1;
  return (t) => {
    if (t <= keys[0].t) return keys[0].pose;
    if (t >= keys[span].t) return keys[span].pose;
    let index = 0;
    while (keys[index + 1].t < t) index += 1;
    const from = keys[index];
    const to = keys[index + 1];
    const local = EASES[to.ease ?? 'inOut']((t - from.t) / (to.t - from.t));
    // Catmull-Rom points are spaced evenly in key index; uniform u across keys keeps each move on its key.
    const u = (index + local) / span;
    const fov = from.pose.fov + (to.pose.fov - from.pose.fov) * local;
    return { eye: eyes.getPoint(u).toArray() as V3, target: targets.getPoint(u).toArray() as V3, fov, shift: to.pose.shift };
  };
}

export interface Direction {
  readonly duration: number;
  /** False while a product page fills the frame: the twin skips drawing. */
  visible(t: number): boolean;
  camera(t: number): CameraPose;
  /** Hour of the day on a 24 h clock, which places the sun and sets the light. */
  hour(t: number): number;
  chilledFlow(t: number): boolean;
  coolingFlow(t: number): boolean;
  /** In the studio the site stands alone as a model: the box [x0, x1, z0, z1] it is cut to, or null. */
  studio(t: number): readonly [number, number, number, number] | null;
  /** Where a machine in fault stands, for the red light on it. */
  alarm(t: number): V3 | null;
  state(t: number): FilmState;
}

const run = (count: number, running: number): TwinState[] => Array.from({ length: count }, (_, index) => (index < running ? 'running' : 'stopped'));

/** Piecewise-linear lookup through (time, value) keys. */
function keyed(keys: readonly (readonly [number, number])[]): (t: number) => number {
  return (t) => {
    if (t <= keys[0][0]) return keys[0][1];
    for (let index = 1; index < keys.length; index += 1) {
      const [t1, v1] = keys[index];
      if (t <= t1) {
        const [t0, v0] = keys[index - 1];
        return v0 + ((v1 - v0) * (t - t0)) / (t1 - t0);
      }
    }
    return keys[keys.length - 1][1];
  };
}

const add = (a: V3, b: V3): V3 => [a[0] + b[0], a[1] + b[1], a[2] + b[2]];

/**
 * 冷站的一天 (film/SPEC.md), on the times named in film/cues.ts. The twin is drawn only while it is
 * on screen; product pages, chapter words and the end card cover the rest.
 */
export function filmDirection(places: Places): Direction {
  const C = CUES;
  const az = Math.PI / 4;
  const overview = add(places.centre, [1, 3, -0.5]);
  const wide = add(places.centre, [0, 2, 0]);
  // The model on the cover, its centre lifted so the type above it has room.
  const diorama: V3 = [-30, 3, -3];
  const camera = cutPath([
    // The cover: the plant room, its towers and the building in morning light, pushing slowly in.
    [
      { t: 0, pose: { ...orbit(diorama, 100, 1.02, az + 0.44, 30), shift: [0.16, -0.07] } },
      { t: C.forecast, pose: { ...orbit(diorama, 90, 1.0, az + 0.3, 30), shift: [0.16, -0.07] }, ease: 'linear' },
    ],
    // Behind the live figures, defocused: the product's own angle, drifting.
    [
      { t: C.see, pose: orbit(overview, 100, 0.92, az + 0.12, 28) },
      { t: C.floors, pose: orbit(overview, 96, 0.92, az - 0.08, 28), ease: 'linear' },
    ],
    // The building opens floor by floor.
    [
      { t: C.floors, pose: orbit(add(places.building, [0, -2, 0]), 46, 0.98, az + 0.6, 30) },
      { t: C.flow, pose: orbit(add(places.building, [0, -1, 0]), 38, 0.9, az + 0.05, 30), ease: 'out' },
    ],
    // Every pipe brought forward.
    [
      { t: C.pipes, pose: orbit(add(places.plant, [-1, 0, 4]), 48, 0.86, az + 0.28, 30) },
      { t: C.think, pose: orbit(add(places.plant, [-1, 0, 4]), 42, 0.84, az + 0.16, 30), ease: 'out' },
    ],
    // Behind the assistant and the suggestion, defocused: round the plant room.
    [
      { t: C.ask, pose: orbit(add(places.plant, [-1, 0, 2]), 62, 0.9, az + 0.45, 30) },
      { t: C.chiller, pose: orbit(add(places.plant, [-1, 0, 2]), 54, 0.92, az - 0.1, 30), ease: 'linear' },
    ],
    // The setpoint reaches 1# chiller.
    [
      { t: C.chiller, pose: orbit(add(places.chillers[0], [0, 1.1, 0]), 9.5, 1.12, az - 0.22, 34) },
      { t: C.verify, pose: orbit(add(places.chillers[0], [0, 1.1, 0]), 7.4, 1.08, az - 0.1, 34), ease: 'out' },
    ],
    // Half an hour later, defocused behind the check: the plant room from above.
    [
      { t: C.verify, pose: orbit(add(places.plant, [-1, 0, 2]), 46, 0.8, az - 0.15, 30) },
      { t: C.night, pose: orbit(add(places.plant, [-1, 0, 2]), 42, 0.8, az - 0.3, 30), ease: 'linear' },
    ],
    // The clock runs into the night and down onto the pumps (defocused under the diagnosis).
    [
      { t: C.clockRun, pose: orbit(add(places.centre, [3, 2, -1]), 90, 0.74, az - 0.25, 28) },
      { t: C.fault, pose: orbit(add(places.chilledPumps[1], [0, 0.8, 0]), 15, 1.0, az + 0.38, 32), ease: 'inOut' },
      { t: C.save, pose: orbit(add(places.chilledPumps[1], [0, 0.8, 0]), 11, 1.02, az + 0.65, 32), ease: 'linear' },
    ],
    // Morning over the whole site, the phone and yesterday's card in front of it.
    [
      { t: C.daily, pose: orbit(wide, 118, 1.02, az + 0.35, 30) },
      { t: C.wall, pose: orbit(wide, 108, 1.05, az + 0.6, 30), ease: 'linear' },
    ],
  ]);
  const hour = keyed([
    [0, 8.4], [C.forecast, 8.6], [C.see, 10.0], [C.chiller, 10.6], [C.verify, 11.1], [C.night, 11.2],
    [C.clockRun, 13.0], [C.fault, 27.2], [C.save, 27.5], [C.daily, 31.7], [C.wall, 32.0], [C.duration, 32.4],
  ]);
  // Full-frame pictures (the forecast, the flow, the chapter words, the year and the mark) cover the
  // twin: it skips drawing under them. Cards sit over it defocused, so it keeps drawing there.
  const covered: readonly (readonly [number, number])[] = [
    [C.forecast + 0.3, C.live], [C.flow, C.pipes], [C.think, C.ask], [C.night, C.clockRun], [C.save, C.daily],
    [C.wall + 0.6, C.duration + 1],
  ];
  const night = (t: number) => t >= 31.4 && t < C.save;
  return {
    duration: C.duration,
    visible: (t) => !covered.some(([from, to]) => t >= from && t < to),
    camera,
    hour,
    studio: (t) => (t < C.forecast + 0.3 ? [-57, -3, -16, 10] : null),
    chilledFlow: () => true,
    coolingFlow: () => true,
    alarm: (t) => (t >= C.fault && t < C.cleared ? add(places.chilledPumps[1], [0, 1.2, 0]) : null),
    state: (t) => {
      const day = !night(t);
      const pumpTwo: TwinState = t < C.fault ? 'running' : t < C.cleared ? 'fault' : 'stopped';
      const chilledPumps: TwinState[] = night(t)
        ? ['running', pumpTwo, t >= C.tapDispatch + 0.5 ? 'running' : 'stopped']
        : run(3, day ? 2 : 1);
      const floor = t >= C.floors && t < C.flow ? (t < C.floors + 1 ? 1 : t < C.floors + 2 ? 2 : 3) : null;
      return {
        chillers: run(3, day ? 2 : 1),
        chilledPumps,
        coolingPumps: run(3, day ? 2 : 1),
        towers: run(3, day ? 2 : 1),
        cut: floor,
        shell: floor === null,
        emphasis: t >= C.pipes && t < C.think,
        selected: t >= C.chiller && t < C.verify ? 'chiller-1' : t >= C.fault && t < C.fdd ? 'chwp-2' : null,
      };
    },
  };
}
