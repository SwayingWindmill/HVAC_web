import { useEffect, useMemo, useState } from 'react';
import { Canvas, useFrame, useThree } from '@react-three/fiber';
import { CameraControls } from '@react-three/drei';
import { EffectComposer, N8AO, ToneMapping } from '@react-three/postprocessing';
import { ToneMappingMode } from 'postprocessing';
import * as THREE from 'three';
import { RoomEnvironment } from 'three/examples/jsm/environments/RoomEnvironment.js';
import type { TwinBuildingPlan } from '../twin-building';
import type { TwinEquipment, TwinPlant } from '../twin-model';
import type { PlantReading } from '../../realtime/plant-model';
import type { ScenePalette } from '../use-scene-palette';
import { SceneMaterialsContext } from '../scene/assets/AssetMesh';
import type { V3 } from '../scene/assets/kit';
import { createMaterialLibrary, disposeMaterialLibrary } from '../scene/assets/materials';
import { Building } from '../scene/Building';
import { buildBuildingLayout } from '../scene/building-layout';
import { Chiller, Pump, Tower } from '../scene/Devices';
import { Grounds } from '../scene/Grounds';
import { AreaCaption, ChillerCard, FloorTag, NameTag, PipeTag } from '../scene/Labels';
import { Label, LabelProjector, useLabelStore } from '../scene/label-layer';
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

const mix = (from: string, to: string, amount: number) => `#${new THREE.Color(from).lerp(new THREE.Color(to), amount).getHexString()}`;

function Lighting({ layout }: { readonly layout: SiteLayout }) {
  const { gl, scene } = useThree();
  useEffect(() => {
    const generator = new THREE.PMREMGenerator(gl);
    const environment = generator.fromScene(new RoomEnvironment(), 0.04).texture;
    scene.environment = environment;
    scene.environmentIntensity = 0.7;
    return () => { scene.environment = null; environment.dispose(); generator.dispose(); };
  }, [gl, scene]);
  useEffect(() => {
    gl.shadowMap.autoUpdate = false;
    gl.shadowMap.needsUpdate = true;
  }, [gl, layout]);
  const cx = (layout.plinth.x[0] + layout.plinth.x[1]) / 2;
  const cz = (layout.plinth.z[0] + layout.plinth.z[1]) / 2;
  const half = Math.max(layout.plinth.x[1] - layout.plinth.x[0], layout.plinth.z[1] - layout.plinth.z[0]) / 2 + 6;
  return (
    <>
      {/* A blue-grey ground bounce: shade reads cool, like the world, never grey. */}
      <hemisphereLight args={['#ffffff', '#c3caf0', 0.85]} />
      <directionalLight
        position={[cx - 34, 60, cz + 22]}
        intensity={1.75}
        color="#fffaf2"
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
function CameraRig({ frame, focus, controls }: {
  readonly frame: readonly [V3, V3];
  readonly focus: V3 | null;
  readonly controls: CameraControls | null;
}) {
  const size = useThree((state) => state.size);
  useEffect(() => {
    if (!controls) return;
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
    void controls.setLookAt(eye.x, eye.y, eye.z, centre.x, centre.y, centre.z, true);
  }, [controls, focus, frame, size.height, size.width]);
  return null;
}

/** Requests frames at 30 fps while anything moves; otherwise the scene is still and costs nothing. */
function Ticker({ active }: { readonly active: boolean }) {
  const invalidate = useThree((state) => state.invalidate);
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => invalidate(), 1000 / ANIMATION_FPS);
    return () => window.clearInterval(timer);
  }, [active, invalidate]);
  return null;
}

/**
 * Watches frame times while the scene animates; when a machine cannot hold ~15 fps (a weak or software
 * GPU), it reports once so the scene drops the ambient-occlusion pass and renders at device pixel ratio 1.
 * Gaps longer than half a second are idle time between on-demand frames, not slowness.
 */
function QualityGuard({ onLow }: { readonly onLow: () => void }) {
  const samples = useMemo<number[]>(() => [], []);
  useFrame((_, delta) => {
    if (delta > 0.5) return;
    samples.push(delta);
    if (samples.length < 45) return;
    const median = [...samples].sort((a, b) => a - b)[Math.floor(samples.length / 2)];
    samples.length = 0;
    if (median > 1 / 15) onLow();
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
}

/** A plant reading as the labels show it: present readings only. */
const reading = (value: PlantReading | null) => (value && value.state === 'PRESENT' ? { label: value.label, display: value.display, unit: value.unit } : null);

export function TwinScene({ plant, plan, palette, view, cut, shell, emphasis, selectedId, onSelect }: TwinSceneProps) {
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
  const labels = useLabelStore();
  // State, not a ref: the rig must run again once the controls exist.
  const [controls, setControls] = useState<CameraControls | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [lowQuality, setLowQuality] = useState(false);
  const onHover = (item: TwinEquipment | null) => setHoveredId(item?.assetId ?? null);
  const events = { onSelect, onHover };
  const lifted = (item: TwinEquipment) => item.assetId === selectedId || item.assetId === hoveredId;

  // Where each selectable thing is, for the camera and its label.
  const origin = layout.buildingOrigin ?? [0, 0, 0];
  const markers = useMemo(() => {
    const at = (position: V3, height: number): V3 => [position[0], position[1] + height, position[2]];
    const entries: [TwinEquipment, V3][] = [
      ...plant.chillers.map((item, index) => [item, at(layout.chillers[index], 2.1)] as [TwinEquipment, V3]),
      ...plant.coolingPumps.map((item, index) => [item, at(layout.coolingPumps[index], 1.3)] as [TwinEquipment, V3]),
      ...plant.chilledPumps.map((item, index) => [item, at(layout.chilledPumps[index], 1.3)] as [TwinEquipment, V3]),
      ...plant.towers.map((item, index) => [item, at(layout.towers[index], 4.0)] as [TwinEquipment, V3]),
      ...(building?.floors.flatMap((floor) => floor.terminals.map((placement) => [placement.terminal.equipment, [origin[0] + placement.at[0], placement.at[1] + 0.55, origin[2] + placement.at[2]]] as [TwinEquipment, V3])) ?? []),
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
  const animating = plant.chilledFlow || plant.coolingFlow || spinning;
  const close = view === 'plant' || focus !== null;
  const tags = [...markers.values()].filter(({ item }) => item.kind !== 'chiller' && (item.assetId === selectedId || item.assetId === hoveredId));
  const visibleFloors = building?.floors.filter((floor) => cut === null || floor.level <= cut) ?? [];
  const captionFloor = cut !== null ? visibleFloors[visibleFloors.length - 1] : null;
  const rackX = layout.racks.length ? (layout.racks[0][0][0] + layout.racks[layout.racks.length - 1][0][0]) / 2 : layout.riser.supply[0];

  return (
    <div className="relative h-full w-full">
      <Canvas
        shadows="soft"
        frameloop="demand"
        dpr={lowQuality ? 1 : [1, 1.5]}
        camera={{ position: [90, 80, 90], fov: FOV, near: 1, far: 900 }}
        onPointerMissed={() => onSelect(null)}
        gl={{ antialias: lowQuality, alpha: true, powerPreference: 'high-performance', stencil: false, toneMapping: THREE.NeutralToneMapping }}
      >
        <SceneMaterialsContext.Provider value={{ library, ghosted: emphasis }}>
          <Ticker active={animating} />
          <FlowClock materials={[flows.chilled, flows.cooling]} />
          <LabelProjector store={labels} />
          <CameraControls ref={setControls} minDistance={10} maxDistance={320} minPolarAngle={0.5} maxPolarAngle={1.2} smoothTime={0.5} draggingSmoothTime={0.15} dollyToCursor />
          <CameraRig frame={frame} focus={focus} controls={controls} />
          <Lighting layout={layout} />
          <fog attach="fog" args={[palette.background, 150, 460]} />
          <Grounds layout={layout} />

          <PipeNetwork runs={layout.cooling.runs} valves={layout.cooling.valves} flow={flows.cooling} moving={plant.coolingFlow} />
          <PipeNetwork runs={layout.chilled.runs} valves={layout.chilled.valves} flow={flows.chilled} moving={plant.chilledFlow} />

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
              chilledMoving={plant.chilledFlow}
              liftedId={hoveredId ?? selectedId}
              selectedId={selectedId}
            />
          ) : null}

          {lowQuality ? null : (
            <>
              <QualityGuard onLow={() => setLowQuality(true)} />
              <EffectComposer multisampling={4} enableNormalPass={false}>
                <N8AO aoRadius={1.6} distanceFalloff={0.8} intensity={2.2} aoSamples={12} denoiseSamples={4} denoiseRadius={10} halfRes />
                <ToneMapping mode={ToneMappingMode.NEUTRAL} />
              </EffectComposer>
            </>
          )}
        </SceneMaterialsContext.Provider>
      </Canvas>

      <div className="pointer-events-none absolute inset-0 overflow-hidden">
        <Label store={labels} id="area-plant" anchor={[(layout.plantRoom.x[0] + layout.plantRoom.x[1]) / 2, 0.1, layout.plantRoom.z[1] + 0.9]}><AreaCaption>冷站机房</AreaCaption></Label>
        {layout.towers.length ? <Label store={labels} id="area-towers" anchor={[(layout.towerPad.x[0] + layout.towerPad.x[1]) / 2, 0.3, layout.towerPad.z[1] + 0.8]}><AreaCaption>冷却塔</AreaCaption></Label> : null}
        {plant.chillers.map((item, index) => {
          // The whole site at once has no room for four cards; they appear near the plant or on hover.
          if (!close && !lifted(item)) return null;
          const [x, y, z] = layout.chillers[index];
          return (
            <Label key={item.assetId} store={labels} id={`chiller-${item.assetId}`} anchor={[x + 1.6, y + 2.0, z]} interactive>
              <ChillerCard item={item} open={lifted(item)} selected={selectedId === item.assetId} onSelect={() => onSelect(item)} />
            </Label>
          );
        })}
        {tags.map(({ item, position }) => (
          <Label key={item.assetId} store={labels} id={`tag-${item.assetId}`} anchor={position}>
            <NameTag item={item} selected={selectedId === item.assetId} />
          </Label>
        ))}
        <Label store={labels} id="mains" anchor={[rackX, 5.0, (layout.riser.supply[2] + layout.riser.return[2]) / 2]}>
          <PipeTag title="冷冻水 · 去末端" swatch={palette.chilled} items={[['供水', reading(plant.supply)], ['回水', reading(plant.return)], ['流量', reading(plant.flow)]]} />
        </Label>
        {building && view !== 'plant' && !focus ? visibleFloors.map((floor) => (
          <Label key={floor.level} store={labels} id={`floor-${floor.level}`} anchor={[origin[0] + building.width + 0.4, floor.y + 0.4, origin[2] + building.depth + 0.4]}>
            <FloorTag>{floor.label}</FloorTag>
          </Label>
        )) : null}
        {captionFloor ? captionFloor.captions.map(({ room, at }) => (
          <Label key={room.id} store={labels} id={`room-${room.id}`} anchor={[origin[0] + at[0], at[1], origin[2] + at[2]]}>
            <AreaCaption>{room.name}</AreaCaption>
          </Label>
        )) : null}
      </div>
    </div>
  );
}
