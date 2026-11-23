import { useEffect, useImperativeHandle, useMemo, useRef, useState, useSyncExternalStore, type Ref } from 'react';
import { Canvas, useFrame, useThree } from '@react-three/fiber';
import { CameraControls } from '@react-three/drei';
import { EffectComposer, N8AO, ToneMapping } from '@react-three/postprocessing';
import { ToneMappingMode } from 'postprocessing';
import * as THREE from 'three';
import { RoomEnvironment } from 'three/examples/jsm/environments/RoomEnvironment.js';
import type { TwinBuildingPlan } from '../twin-building';
import type { TwinEquipment, TwinPlant } from '../twin-model';
import type { ScenePalette } from '../use-scene-palette';
import { SceneMaterialsContext } from '../scene/assets/AssetMesh';
import type { V3 } from '../scene/assets/kit';
import { createMaterialLibrary, disposeMaterialLibrary } from '../scene/assets/materials';
import { Building } from '../scene/Building';
import { buildBuildingLayout } from '../scene/building-layout';
import { Chiller, Pump, Tower } from '../scene/Devices';
import { Grounds } from '../scene/Grounds';
import { createFlowMaterial, FlowClock, PipeNetwork, type FlowMaterial } from '../scene/Pipes';
import { buildSiteLayout, type CameraView, type SiteLayout } from '../scene/site-layout';

// The site as a lit architectural model under a narrow perspective camera at the isometric angle. Light is
// a procedural room environment plus one sun whose shadow map is drawn once per layout; ambient
// occlusion and tone mapping run as one post pass. Frames are drawn on demand: only moving water,
// turning machines and camera moves request them (docs/specs/digital-twin.md 性能).

export type { CameraView };

const ANIMATION_FPS = 30;
const ISO_AZIMUTH = Math.PI / 4;
const ISO_POLAR = 0.92;
const FOV = 28;
const REDUCED_MOTION = '(prefers-reduced-motion: reduce)';

/** The viewer's reduced-motion setting: with it, water stands still, nothing turns, the camera cuts. */
function useReducedMotion(): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const query = window.matchMedia(REDUCED_MOTION);
      query.addEventListener('change', onChange);
      return () => query.removeEventListener('change', onChange);
    },
    () => window.matchMedia(REDUCED_MOTION).matches,
  );
}

function usePageVisible(): boolean {
  return useSyncExternalStore(
    (onChange) => {
      document.addEventListener('visibilitychange', onChange);
      return () => document.removeEventListener('visibilitychange', onChange);
    },
    () => document.visibilityState === 'visible',
  );
}

function isSoftwareWebGLRenderer(gl: THREE.WebGLRenderer): boolean {
  const context = gl.getContext();
  const debugInfo = context.getExtension('WEBGL_debug_renderer_info');
  const renderer = debugInfo
    ? context.getParameter(debugInfo.UNMASKED_RENDERER_WEBGL)
    : context.getParameter(context.RENDERER);
  return /swiftshader|llvmpipe|softpipe|mesa offscreen|microsoft basic render|software/i.test(String(renderer ?? ''));
}

const mix = (from: string, to: string, amount: number) => `#${new THREE.Color(from).lerp(new THREE.Color(to), amount).getHexString()}`;

function Lighting({ layout, highQuality }: { readonly layout: SiteLayout; readonly highQuality: boolean }) {
  const { gl, scene } = useThree();
  useEffect(() => {
    if (!highQuality) {
      scene.environment = null;
      return;
    }
    const generator = new THREE.PMREMGenerator(gl);
    const environment = generator.fromScene(new RoomEnvironment(), 0.04).texture;
    scene.environment = environment;
    scene.environmentIntensity = 0.7;
    return () => { scene.environment = null; environment.dispose(); generator.dispose(); };
  }, [gl, highQuality, scene]);
  useEffect(() => {
    gl.shadowMap.autoUpdate = false;
    gl.shadowMap.needsUpdate = true;
  }, [gl, highQuality, layout]);
  const cx = (layout.plinth.x[0] + layout.plinth.x[1]) / 2;
  const cz = (layout.plinth.z[0] + layout.plinth.z[1]) / 2;
  const half = Math.max(layout.plinth.x[1] - layout.plinth.x[0], layout.plinth.z[1] - layout.plinth.z[0]) / 2 + 6;
  return (
    <>
      {/* A blue-grey ground bounce: shade reads cool, like the world, never grey. */}
      <hemisphereLight args={['#ffffff', '#c3caf0', highQuality ? 0.85 : 1.15]} />
      <directionalLight
        key={highQuality ? 'hq' : 'lq'}
        position={[cx - 34, 60, cz + 22]}
        intensity={1.75}
        color="#fffaf2"
        castShadow
        shadow-mapSize={highQuality ? [2048, 2048] : [512, 512]}
        shadow-bias={-0.0003}
        shadow-normalBias={0.04}
        shadow-radius={4}
        shadow-camera-left={-half}
        shadow-camera-right={half}
        shadow-camera-top={half}
        shadow-camera-bottom={-half}
        shadow-camera-near={1}
        shadow-camera-far={160}
      >
        <object3D attach="target" position={[cx, 0, cz]} />
      </directionalLight>
    </>
  );
}

/**
 * Turns to the isometric angle and frames the view's part of the site, or glides to a selected machine.
 * Framing is computed here: CameraControls.fitToBox would snap the camera to the box's nearest axis.
 */
function CameraRig({ frame, focus, controls, reset, animate }: {
  readonly frame: readonly [V3, V3];
  readonly focus: V3 | null;
  readonly controls: CameraControls | null;
  /** Bumped by the toolbar's home button to frame the view again. */
  readonly reset: number;
  readonly animate: boolean;
}) {
  const size = useThree((state) => state.size);
  const framedRef = useRef(false);
  useEffect(() => {
    if (!controls) {
      framedRef.current = false;
      return;
    }
    const box = focus
      ? new THREE.Box3(new THREE.Vector3(focus[0] - 4, focus[1] - 2, focus[2] - 4), new THREE.Vector3(focus[0] + 4, focus[1] + 1.5, focus[2] + 4))
      : new THREE.Box3(new THREE.Vector3(...frame[0]), new THREE.Vector3(...frame[1]));
    const centre = box.getCenter(new THREE.Vector3());
    // The camera's right and up directions at the isometric angle; the box's extent along each sets the zoom.
    const view = new THREE.Object3D();
    view.position.copy(centre).add(new THREE.Vector3().setFromSphericalCoords(10, ISO_POLAR, ISO_AZIMUTH));
    view.lookAt(centre);
    view.updateMatrixWorld();
    const inverse = view.matrixWorld.clone().invert();
    const extent = new THREE.Box3();
    for (const x of [box.min.x, box.max.x]) for (const y of [box.min.y, box.max.y]) for (const z of [box.min.z, box.max.z]) extent.expandByPoint(new THREE.Vector3(x, y, z).applyMatrix4(inverse));
    // Perspective: back off until the box's extent across and up the view fits the field of view.
    const half = THREE.MathUtils.degToRad(FOV / 2);
    const across = (extent.max.x - extent.min.x) / 2 / (Math.tan(half) * (size.width / size.height));
    const up = (extent.max.y - extent.min.y) / 2 / Math.tan(half);
    // The overview crops into the town around the site; a part or a machine is framed whole.
    const distance = Math.max(across, up) * (focus ? 1.1 : 0.82) + (extent.max.z - extent.min.z) / 2;
    const eye = new THREE.Vector3().setFromSphericalCoords(distance, ISO_POLAR, ISO_AZIMUTH).add(centre);
    const transition = framedRef.current && animate;
    framedRef.current = true;
    void controls.setLookAt(eye.x, eye.y, eye.z, centre.x, centre.y, centre.z, transition);
  }, [animate, controls, focus, frame, reset, size.height, size.width]);
  return null;
}

/** Schedules the next frame only after the current one finishes, so slow GPUs never queue a backlog. */
function Ticker({ active, fps }: { readonly active: boolean; readonly fps: number }) {
  const invalidate = useThree((state) => state.invalidate);
  const activeRef = useRef(active);
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    activeRef.current = active;
    if (active) invalidate();
    return () => {
      activeRef.current = false;
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [active, fps, invalidate]);

  useFrame(() => {
    if (!activeRef.current || timerRef.current !== null) return;
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      if (activeRef.current) invalidate();
    }, Math.round(1000 / fps));
  });

  return null;
}

/**
 * Watches frame times while the scene animates; when a GPU cannot hold ~15 fps (or stalls on two
 * consecutive frames > 150 ms), it reports once so the scene drops AO and renders at DPR 1.
 */
function QualityGuard({ active, onLow }: { readonly active: boolean; readonly onLow: () => void }) {
  const samplesRef = useRef<number[]>([]);
  const slowStreakRef = useRef(0);
  const primedRef = useRef(false);
  const reportedRef = useRef(false);

  useEffect(() => {
    samplesRef.current = [];
    slowStreakRef.current = 0;
    primedRef.current = false;
  }, [active]);

  useFrame((_, delta) => {
    if (!active || reportedRef.current) return;
    if (!primedRef.current) {
      primedRef.current = true;
      return;
    }
    if (delta > 0.15) {
      slowStreakRef.current += 1;
      if (slowStreakRef.current >= 2) {
        reportedRef.current = true;
        onLow();
        return;
      }
    } else {
      slowStreakRef.current = 0;
    }
    samplesRef.current.push(delta);
    if (samplesRef.current.length < 8) return;
    const sorted = [...samplesRef.current].sort((a, b) => a - b);
    const median = sorted[Math.floor(sorted.length / 2)];
    samplesRef.current = [];
    if (median > 1 / 15) {
      reportedRef.current = true;
      onLow();
    }
  });
  return null;
}

interface Flows {
  readonly chilled: FlowMaterial;
  readonly cooling: FlowMaterial;
  readonly supplyAir: FlowMaterial;
  readonly freshAir: FlowMaterial;
}

function useFlows(palette: ScenePalette, emphasis: boolean): Flows {
  const flows = useMemo<Flows>(() => ({
    chilled: createFlowMaterial('chilled', palette.chilled, mix(palette.chilled, '#ffffff', 0.75), { strength: 0.55 }),
    cooling: createFlowMaterial('cooling', palette.cooling, mix(palette.cooling, '#ffffff', 0.75), { strength: 0.55 }),
    supplyAir: createFlowMaterial('supply-air', '#d5d9de', mix(palette.chilled, '#ffffff', 0.6), { spacing: 3.2, speed: 0.35, strength: 0.35, roughness: 0.38, metalness: 0.65 }),
    freshAir: createFlowMaterial('fresh-air', '#d9dce0', mix(palette.accent, '#ffffff', 0.6), { spacing: 3.2, speed: 0.35, strength: 0.35, roughness: 0.38, metalness: 0.65 }),
  }), [palette]);
  // Water reads as tinted insulation; brought forward, it takes its full colour.
  useEffect(() => {
    flows.chilled.material.color.set(emphasis ? palette.chilled : mix(palette.chilled, '#ffffff', 0.35));
    flows.cooling.material.color.set(emphasis ? palette.cooling : mix(palette.cooling, '#ffffff', 0.35));
    for (const flow of [flows.chilled, flows.cooling]) {
      flow.material.emissive.set(emphasis ? flow.material.color : '#000000');
      flow.material.emissiveIntensity = emphasis ? 0.25 : 0;
    }
  }, [emphasis, flows, palette]);
  useEffect(() => () => { Object.values(flows).forEach((flow) => flow.material.dispose()); }, [flows]);
  return flows;
}

export interface TwinSceneProps {
  readonly plant: TwinPlant;
  readonly plan: TwinBuildingPlan | null;
  readonly palette: ScenePalette;
  readonly view: CameraView;
  /** Floor cut: show floors up to this level. */
  readonly cut: number | null;
  readonly shell: boolean;
  readonly emphasis: boolean;
  readonly selectedId: string | null;
  readonly onSelect: (item: TwinEquipment | null) => void;
  /** The corner toolbar's handle on the camera. */
  readonly camera?: Ref<TwinCamera>;
}

export interface TwinCamera {
  zoom(step: 1 | -1): void;
  rotate(step: 1 | -1): void;
  reset(): void;
}

export function TwinScene({ plant, plan, palette, view, cut, shell, emphasis, selectedId, onSelect, camera }: TwinSceneProps) {
  const layout = useMemo(() => buildSiteLayout({
    chillers: plant.chillers.length,
    coolingPumps: plant.coolingPumps.length,
    chilledPumps: plant.chilledPumps.length,
    towers: plant.towers.length,
  }, plan), [plan, plant.chillers.length, plant.chilledPumps.length, plant.coolingPumps.length, plant.towers.length]);
  const building = useMemo(() => (plan ? buildBuildingLayout(plan) : null), [plan]);
  const library = useMemo(() => createMaterialLibrary(palette), [palette]);
  useEffect(() => () => disposeMaterialLibrary(library), [library]);
  const flows = useFlows(palette, emphasis);
  // State, not a ref: the rig must run again once the controls exist.
  const [controls, setControls] = useState<CameraControls | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [quality, setQuality] = useState<'pending' | 'high' | 'low'>('pending');
  const highQuality = quality === 'high';
  const lowQuality = quality === 'low';
  const still = useReducedMotion();
  const [reset, setReset] = useState(0);
  useImperativeHandle(camera, () => {
    const enableTransition = !still && !lowQuality;
    return {
      zoom: (step) => { void controls?.dolly(step * Math.max(4, controls.distance * 0.25), enableTransition); },
      rotate: (step) => { void controls?.rotate(step * (Math.PI / 6), 0, enableTransition); },
      reset: () => setReset((value) => value + 1),
    };
  }, [controls, lowQuality, still]);
  const onHover = (item: TwinEquipment | null) => setHoveredId(item?.assetId ?? null);
  const events = { onSelect, onHover };
  const lifted = (item: TwinEquipment) => item.assetId === selectedId || item.assetId === hoveredId;

  // Where each selectable thing is, for the camera to glide to.
  const origin = layout.buildingOrigin ?? [0, 0, 0];
  const markers = useMemo(() => {
    const at = (position: V3, height: number): V3 => [position[0], position[1] + height, position[2]];
    const markerEntry = (item: TwinEquipment, position: V3): [TwinEquipment, V3] => [item, position];
    const entries: [TwinEquipment, V3][] = [
      ...plant.chillers.map((item, index) => markerEntry(item, at(layout.chillers[index], 2.1))),
      ...plant.coolingPumps.map((item, index) => markerEntry(item, at(layout.coolingPumps[index], 1.3))),
      ...plant.chilledPumps.map((item, index) => markerEntry(item, at(layout.chilledPumps[index], 1.3))),
      ...plant.towers.map((item, index) => markerEntry(item, at(layout.towers[index], 4.0))),
      ...(building?.floors.flatMap((floor) => floor.terminals.map((placement) => markerEntry(placement.terminal.equipment, [origin[0] + placement.at[0], placement.at[1] + 0.55, origin[2] + placement.at[2]]))) ?? []),
    ];
    return new Map(entries.map(([item, position]) => [item.assetId, { item, position }]));
  }, [building, layout, origin, plant]);
  const focus = selectedId ? markers.get(selectedId)?.position ?? null : null;
  const frame = useMemo<readonly [V3, V3]>(() => {
    if (view !== 'terminal' || cut === null || !building) return layout.frames[view];
    const floor = building.floors.find((candidate) => candidate.level === cut) ?? building.floors[0];
    return [[origin[0], 0, origin[2]], [origin[0] + building.width, floor.y + floor.height, origin[2] + building.depth]];
  }, [building, cut, layout, origin, view]);

  const spinning = [...plant.towers, ...plant.coolingPumps, ...plant.chilledPumps].some((item) => item.state === 'running');
  const pageVisible = usePageVisible();
  const animating = !still && pageVisible && (plant.chilledFlow || plant.coolingFlow || spinning);

  return (
    <div className="relative h-full w-full">
      <Canvas
        shadows={lowQuality ? true : 'soft'}
        frameloop="demand"
        dpr={highQuality ? [1, 1.5] : 1}
        camera={{ position: [90, 80, 90], fov: FOV, near: 1, far: 900 }}
        onPointerMissed={() => onSelect(null)}
        onCreated={({ gl }) => {
          setQuality(isSoftwareWebGLRenderer(gl) ? 'low' : 'high');
        }}
        gl={{ antialias: true, alpha: true, powerPreference: 'high-performance', stencil: false, toneMapping: THREE.NeutralToneMapping }}
      >
        <SceneMaterialsContext.Provider value={{ library, ghosted: emphasis, still }}>
          <Ticker active={animating} fps={lowQuality ? 4 : ANIMATION_FPS} />
          <FlowClock materials={[flows.chilled, flows.cooling]} />
          <CameraControls ref={setControls} minDistance={10} maxDistance={320} minPolarAngle={0.5} maxPolarAngle={1.2} smoothTime={still || lowQuality ? 0 : 0.5} draggingSmoothTime={still || lowQuality ? 0 : 0.15} dollyToCursor />
          <CameraRig frame={frame} focus={focus} controls={controls} reset={reset} animate={!still && !lowQuality} />
          <Lighting layout={layout} highQuality={highQuality} />
          <fog attach="fog" args={[palette.background, 150, 460]} />
          <Grounds layout={layout} />

          <PipeNetwork runs={layout.cooling.runs} valves={layout.cooling.valves} flow={flows.cooling} moving={!still && plant.coolingFlow} />
          <PipeNetwork runs={layout.chilled.runs} valves={layout.chilled.valves} flow={flows.chilled} moving={!still && plant.chilledFlow} />

          {plant.chillers.map((item, index) => <Chiller key={item.assetId} {...events} item={item} position={layout.chillers[index]} lifted={lifted(item)} selected={item.assetId === selectedId} />)}
          {plant.coolingPumps.map((item, index) => <Pump key={item.assetId} {...events} item={item} position={layout.coolingPumps[index]} lifted={lifted(item)} selected={item.assetId === selectedId} />)}
          {plant.chilledPumps.map((item, index) => <Pump key={item.assetId} {...events} item={item} position={layout.chilledPumps[index]} lifted={lifted(item)} selected={item.assetId === selectedId} />)}
          {plant.towers.map((item, index) => <Tower key={item.assetId} {...events} item={item} position={layout.towers[index]} lifted={lifted(item)} selected={item.assetId === selectedId} />)}
          {building ? (
            <Building
              {...events}
              layout={building}
              origin={origin}
              cut={cut}
              shell={shell && !emphasis}
              flows={flows}
              chilledMoving={!still && plant.chilledFlow}
              liftedId={hoveredId ?? selectedId}
              selectedId={selectedId}
            />
          ) : null}

          {highQuality ? (
            <>
              <QualityGuard active={animating} onLow={() => setQuality('low')} />
              <EffectComposer multisampling={0} enableNormalPass={false}>
                <N8AO aoRadius={1.6} distanceFalloff={0.8} intensity={2.2} aoSamples={8} denoiseSamples={2} denoiseRadius={10} halfRes />
                <ToneMapping mode={ToneMappingMode.NEUTRAL} />
              </EffectComposer>
            </>
          ) : null}
        </SceneMaterialsContext.Provider>
      </Canvas>

    </div>
  );
}
