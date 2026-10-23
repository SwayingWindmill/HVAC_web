import { useEffect, useLayoutEffect, useMemo, useRef, type ReactNode } from 'react';
import { advance, createRoot, extend, flushSync, useFrame, useThree } from '@react-three/fiber';
import { EffectComposer, N8AO, ToneMapping } from '@react-three/postprocessing';
import { ToneMappingMode } from 'postprocessing';
import * as THREE from 'three';
import { RoomEnvironment } from 'three/examples/jsm/environments/RoomEnvironment.js';
import { BUILDING_PLANS } from '@/features/operations/digital-twin/twin-building';
import type { TwinEquipment, TwinKind, TwinState } from '@/features/operations/digital-twin/twin-model';
import type { ScenePalette } from '@/features/operations/digital-twin/use-scene-palette';
import { AssetMesh, SceneMaterialsContext } from '@/features/operations/digital-twin/scene/assets/AssetMesh';
import { equipmentAsset } from '@/features/operations/digital-twin/scene/assets/equipment';
import type { V3 } from '@/features/operations/digital-twin/scene/assets/kit';
import { createMaterialLibrary, type MaterialLibrary } from '@/features/operations/digital-twin/scene/assets/materials';
import { Building } from '@/features/operations/digital-twin/scene/Building';
import { buildBuildingLayout } from '@/features/operations/digital-twin/scene/building-layout';
import { Chiller, Device } from '@/features/operations/digital-twin/scene/Devices';
import { Grounds } from '@/features/operations/digital-twin/scene/Grounds';
import { createFlowMaterial, PipeNetwork, type FlowMaterial } from '@/features/operations/digital-twin/scene/Pipes';
import { buildSiteLayout, type SiteLayout } from '@/features/operations/digital-twin/scene/site-layout';
import { filmDirection, placesOf, type Direction, type FilmState } from './direction';

// The film's clock, for index.html (HVACTwin.cues): one copy, bundled with the shots it times.
export { CUES as cues } from '../film/cues';

// The digital twin as a film set. The product's scene code draws the site; this file replaces only what
// the product drives from wall-clock time (turning parts, moving water, the camera, the shadow map) with
// values computed from film time, and adds the sun's day. The film page calls renderAt(t) per frame.

const PALETTE: ScenePalette = {
  background: '#f7f7f8',
  card: '#ffffff',
  border: '#e7e7ea',
  muted: '#f7f7f9',
  foreground: '#18181b',
  chilled: '#3f5bd9',
  cooling: '#0e9384',
  accent: '#0e9384',
  running: '#1f9d55',
  fault: '#d92d20',
  inactive: '#c4c4cc',
};

const clock = { t: 0 };

const mix = (from: string, to: string, amount: number) => `#${new THREE.Color(from).lerp(new THREE.Color(to), amount).getHexString()}`;
const smooth = (edge0: number, edge1: number, x: number) => {
  const k = Math.min(1, Math.max(0, (x - edge0) / (edge1 - edge0)));
  return k * k * (3 - 2 * k);
};

/** The light of one hour: sun direction, colour and strength; sky; how lit the windows are. */
export function daylight(clockHour: number) {
  const hour = ((clockHour % 24) + 24) % 24;
  const arc = ((hour - 6) / 12) * Math.PI;
  const elevation = Math.sin(arc);
  const day = smooth(-0.08, 0.25, elevation);
  const low = 1 - smooth(0.05, 0.55, elevation);
  const east = new THREE.Vector3(1, 0, -0.45).normalize();
  const direction = east.multiplyScalar(Math.cos(arc)).add(new THREE.Vector3(0, Math.max(elevation, 0.12) * 1.1, 0.55)).normalize();
  const night = 1 - day;
  return {
    direction,
    sun: new THREE.Color(mix('#fffaf2', '#ffb27a', low * 0.85)),
    sunIntensity: 2.3 * day * (0.6 + 0.4 * (1 - low)),
    sky: mix(mix(PALETTE.background, '#f6ede4', low * day), '#141b33', night * 0.94),
    hemisphere: 0.62 * day + 0.16 * night,
    hemisphereSky: mix('#ffffff', '#6f82d6', night),
    hemisphereGround: mix('#c3caf0', '#141a30', night),
    environment: 0.55 * day + 0.06 * night,
    windows: night,
    /** Moonlight from high behind the site, so night shapes still read. */
    moon: 0.7 * night,
  };
}

const machine = (assetId: string, name: string, kind: TwinKind, state: TwinState): TwinEquipment => ({
  assetId, name, kind, state, device: null, readings: [], values: {},
});

function plantOf(state: FilmState) {
  return {
    chillers: state.chillers.map((value, index) => machine(`chiller-${index + 1}`, `${index + 1}# 冷水机组`, 'chiller', value)),
    chilledPumps: state.chilledPumps.map((value, index) => machine(`chwp-${index + 1}`, `${index + 1}# 冷冻泵`, 'chilledPump', value)),
    coolingPumps: state.coolingPumps.map((value, index) => machine(`cwp-${index + 1}`, `${index + 1}# 冷却泵`, 'coolingPump', value)),
    towers: state.towers.map((value, index) => machine(`tower-${index + 1}`, `${index + 1}# 冷却塔`, 'tower', value)),
  };
}

const none = () => {};
const events = { onSelect: none, onHover: none };

/** A part turning while its machine runs, its angle a function of film time. */
function Turning({ axis, speed, running, position, children }: { readonly axis: 'x' | 'y'; readonly speed: number; readonly running: boolean; readonly position: V3; readonly children: ReactNode }) {
  const ref = useRef<THREE.Group>(null);
  useFrame(() => {
    if (ref.current) ref.current.rotation[axis] = running ? clock.t * speed : 0;
  });
  return <group ref={ref} position={position}>{children}</group>;
}

function FilmPump({ item, position, selected }: { readonly item: TwinEquipment; readonly position: V3; readonly selected: boolean }) {
  return (
    <Device {...events} item={item} asset="pump" position={position} lifted={false} selected={selected}>
      {(set) => (
        <Turning axis="x" speed={5} running={item.state === 'running'} position={[0.1, 0.64, 0]}>
          <AssetMesh asset={equipmentAsset('coupling')} set={set} shadows={false} />
        </Turning>
      )}
    </Device>
  );
}

function FilmTower({ item, position, selected }: { readonly item: TwinEquipment; readonly position: V3; readonly selected: boolean }) {
  return (
    <Device {...events} item={item} asset="tower" position={position} lifted={false} selected={selected}>
      {(set) => (
        <Turning axis="y" speed={1.4} running={item.state === 'running'} position={[0, 3.36, 0]}>
          <AssetMesh asset={equipmentAsset('towerFan')} set={set} />
        </Turning>
      )}
    </Device>
  );
}

interface Flows {
  readonly chilled: FlowMaterial;
  readonly cooling: FlowMaterial;
  readonly supplyAir: FlowMaterial;
  readonly freshAir: FlowMaterial;
}

function createFlows(): Flows {
  return {
    chilled: createFlowMaterial('chilled', PALETTE.chilled, mix(PALETTE.chilled, '#ffffff', 0.75), { strength: 0.55 }),
    cooling: createFlowMaterial('cooling', PALETTE.cooling, mix(PALETTE.cooling, '#ffffff', 0.75), { strength: 0.55 }),
    supplyAir: createFlowMaterial('supply-air', '#d5d9de', mix(PALETTE.chilled, '#ffffff', 0.6), { spacing: 3.2, speed: 0.35, strength: 0.35, roughness: 0.38, metalness: 0.65 }),
    freshAir: createFlowMaterial('fresh-air', '#d9dce0', mix(PALETTE.accent, '#ffffff', 0.6), { spacing: 3.2, speed: 0.35, strength: 0.35, roughness: 0.38, metalness: 0.65 }),
  };
}

const planeCache = new Map<string, THREE.Plane[]>();
/** Four upright planes that keep x0 ≤ x ≤ x1 and z0 ≤ z ≤ z1. */
function studioPlanes([x0, x1, z0, z1]: readonly [number, number, number, number]): THREE.Plane[] {
  const key = `${x0},${x1},${z0},${z1}`;
  let planes = planeCache.get(key);
  if (!planes) {
    planes = [
      new THREE.Plane(new THREE.Vector3(1, 0, 0), -x0),
      new THREE.Plane(new THREE.Vector3(-1, 0, 0), x1),
      new THREE.Plane(new THREE.Vector3(0, 0, 1), -z0),
      new THREE.Plane(new THREE.Vector3(0, 0, -1), z1),
    ];
    planeCache.set(key, planes);
  }
  return planes;
}

/** Camera, sun, sky and water, set from film time before each frame is drawn. */
function Director({ direction, layout, flows, library }: { readonly direction: Direction; readonly layout: SiteLayout; readonly flows: Flows; readonly library: MaterialLibrary }) {
  const { gl, scene, camera } = useThree();
  const sun = useRef<THREE.DirectionalLight>(null);
  const moon = useRef<THREE.DirectionalLight>(null);
  const hemisphere = useRef<THREE.HemisphereLight>(null);
  const alarm = useRef<THREE.PointLight>(null);
  const lamps = useRef<THREE.Group>(null);
  const cx = (layout.plinth.x[0] + layout.plinth.x[1]) / 2;
  const cz = (layout.plinth.z[0] + layout.plinth.z[1]) / 2;
  const half = Math.max(layout.plinth.x[1] - layout.plinth.x[0], layout.plinth.z[1] - layout.plinth.z[0]) / 2 + 6;
  // Night lights: the plant room's ceiling lamps (cool) and each dining floor's (warm).
  const room = layout.plantRoom;
  const roomZ = (room.z[0] + room.z[1]) / 2;
  const plantLamps: V3[] = [0.2, 0.5, 0.8].map((k) => [room.x[0] + (room.x[1] - room.x[0]) * k, room.height - 0.4, roomZ]);
  const origin = layout.buildingOrigin ?? [0, 0, 0];
  const plan = BUILDING_PLANS.songyaxuan;
  const floorLamps: V3[] = [];
  let floorY = 0;
  for (const floor of plan.floors) {
    for (const k of [0.25, 0.7]) floorLamps.push([origin[0] + plan.width * k, floorY + floor.height - 0.8, origin[2] + plan.depth / 2]);
    floorY += floor.height;
  }
  useEffect(() => {
    const generator = new THREE.PMREMGenerator(gl);
    const environment = generator.fromScene(new RoomEnvironment(), 0.04).texture;
    scene.environment = environment;
    scene.fog = new THREE.Fog(PALETTE.background, 150, 460);
    scene.background = new THREE.Color(PALETTE.background);
    gl.shadowMap.autoUpdate = true;
    return () => { scene.environment = null; environment.dispose(); generator.dispose(); };
  }, [gl, scene]);
  useFrame(() => {
    const t = clock.t;
    const pose = direction.camera(t);
    const perspective = camera as THREE.PerspectiveCamera;
    perspective.position.set(...pose.eye);
    perspective.fov = pose.fov;
    if (pose.shift) perspective.setViewOffset(1920, 1080, -pose.shift[0] * 1920, -pose.shift[1] * 1080, 1920, 1080);
    else perspective.clearViewOffset();
    perspective.lookAt(...pose.target);
    perspective.updateProjectionMatrix();

    const light = daylight(direction.hour(t));
    if (sun.current) {
      sun.current.position.set(cx, 0, cz).addScaledVector(light.direction, 80);
      sun.current.color.copy(light.sun);
      sun.current.intensity = light.sunIntensity;
    }
    if (hemisphere.current) {
      hemisphere.current.intensity = light.hemisphere;
      hemisphere.current.color.set(light.hemisphereSky);
      hemisphere.current.groundColor.set(light.hemisphereGround);
    }
    // In the studio the site stands alone as a model: everything outside its box is cut away.
    const box = direction.studio(t);
    gl.localClippingEnabled = false;
    gl.clippingPlanes = box ? studioPlanes(box) : [];
    if (moon.current) moon.current.intensity = light.moon;
    lamps.current?.children.forEach((child) => {
      const lamp = child as THREE.PointLight;
      lamp.intensity = light.windows * (lamp.userData.warm ? 26 : 34);
    });
    const fault = direction.alarm(t);
    if (alarm.current) {
      alarm.current.visible = fault !== null;
      if (fault) {
        alarm.current.position.set(...fault);
        alarm.current.intensity = 9 + 6 * Math.sin(t * Math.PI * 2 * 1.2);
      }
    }
    scene.environmentIntensity = light.environment;
    (scene.background as THREE.Color).set(light.sky);
    const fog = scene.fog as THREE.Fog | null;
    if (fog) {
      fog.color.set(light.sky);
      // Far shots push the fog back so the town stays in the picture.
      const reach = new THREE.Vector3(...pose.eye).distanceTo(new THREE.Vector3(...pose.target));
      fog.near = Math.max(150, reach * 0.95);
      fog.far = fog.near + 420;
    }
    const windows = library.base.window as THREE.MeshStandardMaterial;
    windows.emissive.set('#ffd49a');
    windows.emissiveIntensity = light.windows * 0.9;

    flows.chilled.uniforms.uFlow.value = direction.chilledFlow(t) ? 1 : 0;
    flows.cooling.uniforms.uFlow.value = direction.coolingFlow(t) ? 1 : 0;
    flows.supplyAir.uniforms.uFlow.value = 1;
    flows.freshAir.uniforms.uFlow.value = 1;
    for (const flow of Object.values(flows)) flow.uniforms.uTime.value = t;
  }, -1);
  return (
    <>
      <hemisphereLight ref={hemisphere} args={['#ffffff', '#c3caf0', 0.85]} />
      <directionalLight
        ref={sun}
        intensity={1.75}
        castShadow
        shadow-mapSize={[2048, 2048]}
        shadow-bias={-0.0003}
        shadow-normalBias={0.04}
        shadow-radius={4}
        shadow-camera-left={-half}
        shadow-camera-right={half}
        shadow-camera-top={half}
        shadow-camera-bottom={-half}
        shadow-camera-near={1}
        shadow-camera-far={200}
      >
        <object3D attach="target" position={[cx, 0, cz]} />
      </directionalLight>
      <directionalLight ref={moon} position={[cx - 40, 70, cz - 50]} color="#a9b8ff" intensity={0}>
        <object3D attach="target" position={[cx, 0, cz]} />
      </directionalLight>
      <group ref={lamps}>
        {plantLamps.map((at, index) => <pointLight key={`p${index}`} position={at} color="#e9efff" intensity={0} distance={14} decay={2} />)}
        {floorLamps.map((at, index) => <pointLight key={`f${index}`} position={at} color="#ffd29a" intensity={0} distance={16} decay={2} userData={{ warm: true }} />)}
      </group>
      <pointLight ref={alarm} color="#ff3b30" intensity={0} distance={7} decay={2} visible={false} />
    </>
  );
}

function Film({ state, direction, layout }: { readonly state: FilmState; readonly direction: Direction; readonly layout: SiteLayout }) {
  const plan = BUILDING_PLANS.songyaxuan;
  const building = useMemo(() => buildBuildingLayout(plan), [plan]);
  const library = useMemo(() => createMaterialLibrary(PALETTE), []);
  const flows = useMemo(createFlows, []);
  const plant = plantOf(state);
  const origin = layout.buildingOrigin ?? [0, 0, 0];
  // The product's 「突出管路」: water takes its full colour while everything else washes out.
  useLayoutEffect(() => {
    flows.chilled.material.color.set(state.emphasis ? PALETTE.chilled : mix(PALETTE.chilled, '#ffffff', 0.35));
    flows.cooling.material.color.set(state.emphasis ? PALETTE.cooling : mix(PALETTE.cooling, '#ffffff', 0.35));
    for (const flow of [flows.chilled, flows.cooling]) {
      flow.material.emissive.set(state.emphasis ? flow.material.color : '#000000');
      flow.material.emissiveIntensity = state.emphasis ? 0.25 : 0;
    }
  }, [flows, state.emphasis]);
  const chilledMoving = plant.chilledPumps.some((item) => item.state === 'running');
  const coolingMoving = plant.coolingPumps.some((item) => item.state === 'running');
  return (
    <SceneMaterialsContext.Provider value={{ library, ghosted: state.emphasis, still: false }}>
      <Director direction={direction} layout={layout} flows={flows} library={library} />
      <Grounds layout={layout} />
      <PipeNetwork runs={layout.cooling.runs} valves={layout.cooling.valves} flow={flows.cooling} moving={coolingMoving} />
      <PipeNetwork runs={layout.chilled.runs} valves={layout.chilled.valves} flow={flows.chilled} moving={chilledMoving} />
      {plant.chillers.map((item, index) => <Chiller key={item.assetId} {...events} item={item} position={layout.chillers[index]} lifted={false} selected={item.assetId === state.selected} />)}
      {plant.coolingPumps.map((item, index) => <FilmPump key={item.assetId} item={item} position={layout.coolingPumps[index]} selected={item.assetId === state.selected} />)}
      {plant.chilledPumps.map((item, index) => <FilmPump key={item.assetId} item={item} position={layout.chilledPumps[index]} selected={item.assetId === state.selected} />)}
      {plant.towers.map((item, index) => <FilmTower key={item.assetId} item={item} position={layout.towers[index]} selected={item.assetId === state.selected} />)}
      <Building
        {...events}
        layout={building}
        origin={origin}
        cut={state.cut}
        shell={state.shell && !state.emphasis}
        flows={flows}
        chilledMoving={chilledMoving}
        liftedId={null}
        selectedId={state.selected}
      />
      <EffectComposer multisampling={4} enableNormalPass={false}>
        <N8AO aoRadius={1.6} distanceFalloff={0.8} intensity={2.2} aoSamples={12} denoiseSamples={4} denoiseRadius={10} halfRes />
        <ToneMapping mode={ToneMappingMode.NEUTRAL} />
      </EffectComposer>
    </SceneMaterialsContext.Provider>
  );
}

export interface MountOptions {
  readonly width: number;
  readonly height: number;
  readonly dpr?: number;
}

export interface TwinFilm {
  readonly duration: number;
  renderAt(t: number): void;
}

/** Where things stand on the site, for writing camera moves. */
export function places() {
  const plan = BUILDING_PLANS.songyaxuan;
  const layout = buildSiteLayout({ chillers: 3, coolingPumps: 3, chilledPumps: 3, towers: 3 }, plan);
  return { ...placesOf(layout, [plan.width, plan.floors.reduce((sum, floor) => sum + floor.height, 0), plan.depth]), plinth: layout.plinth, plantRoom: layout.plantRoom, origin: layout.buildingOrigin, frames: layout.frames };
}

export async function mount(canvas: HTMLCanvasElement, { width, height, dpr = 1 }: MountOptions): Promise<TwinFilm> {
  const plan = BUILDING_PLANS.songyaxuan;
  const counts = { chillers: 3, coolingPumps: 3, chilledPumps: 3, towers: 3 };
  const layout = buildSiteLayout(counts, plan);
  const direction = filmDirection(placesOf(layout, [plan.width, plan.floors.reduce((sum, floor) => sum + floor.height, 0), plan.depth]));
  // <Canvas> registers three's classes itself; a bare root needs them named.
  extend(THREE as unknown as Parameters<typeof extend>[0]);
  const root = createRoot(canvas);
  await root.configure({
    size: { width, height, top: 0, left: 0 },
    dpr,
    frameloop: 'never',
    shadows: 'soft',
    camera: { fov: 28, near: 1, far: 900, position: [90, 80, 90] },
    gl: { antialias: false, alpha: false, preserveDrawingBuffer: true, powerPreference: 'high-performance', stencil: false, toneMapping: THREE.NeutralToneMapping },
  });
  let shown = '';
  const show = (t: number) => {
    const state = direction.state(t);
    const key = JSON.stringify(state);
    if (key === shown) return;
    shown = key;
    flushSync(() => root.render(<Film state={state} direction={direction} layout={layout} />));
  };
  const renderAt = (t: number) => {
    if (!direction.visible(t)) return;
    clock.t = t;
    show(t);
    advance(t * 1000);
  };
  renderAt(0);
  return { duration: direction.duration, renderAt };
}
