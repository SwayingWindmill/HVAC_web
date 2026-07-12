import { useMemo, useRef } from 'react';
import { Canvas, useFrame } from '@react-three/fiber';
import { OrbitControls, Grid, ContactShadows } from '@react-three/drei';
import * as THREE from 'three';
import { STATUS, COP_GOOD } from '@/theme/tokens';

// ---- commercial central-AC plant room, stylized as a 3D system diagram ----
// Layout (top-down view):
//   [Building Load] (back center)
//          |
//   [Chiller] [Chiller]  ----condenser pipes---->  [Tower] [Tower]
//       |                                                |
//   [ChillPump]                                    [CondPump]

export interface System3DProps {
  cop: number;
  chillerRun: number;
  chillerTotal: number;
  towerRun: number;
  towerTotal: number;
  pumpRun: number;
  pumpTotal: number;
  load: number; // 0..100, drives flow speed
}

type V3 = [number, number, number];

/* Pipe colors — must match index.tsx ACCENT palette exactly */
const CHILLED  = '#3b82f6';   /* primary blue: chilled water supply/return */
const CONDENSER = '#22c55e'; /* green: cooling water (per reference image) */

// ---- orthogonal pipe routing ----
function LPipe({ points, color, radius = 0.055 }: { points: V3[]; color: string; radius?: number }) {
  const geometries = useMemo(() => {
    const geoms: { pos: THREE.Vector3; quat: THREE.Quaternion; len: number }[] = [];
    for (let i = 0; i < points.length - 1; i++) {
      const a = new THREE.Vector3(...points[i]);
      const b = new THREE.Vector3(...points[i + 1]);
      const m = a.clone().add(b).multiplyScalar(0.5);
      const dir = b.clone().sub(a);
      const l = dir.length();
      if (l < 0.001) continue;
      const q = new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 1, 0), dir.normalize());
      geoms.push({ pos: m, quat: q, len: l });
    }
    return geoms;
  }, [points]);

  return (
    <>
      {geometries.map((g, i) => (
        <mesh key={i} position={[g.pos.x, g.pos.y, g.pos.z]} quaternion={[g.quat.x, g.quat.y, g.quat.z, g.quat.w]}>
          <cylinderGeometry args={[radius, radius, g.len, 12]} />
          <meshStandardMaterial color={color} metalness={0.25} roughness={0.7} />
        </mesh>
      ))}
    </>
  );
}

// Flow particles traveling along path
function FlowAlongPath({ points, color, speed = 0.28, count = 8, size = 0.07 }: {
  points: V3[]; color: string; speed?: number; count?: number; size?: number;
}) {
  const ref = useRef<THREE.Group>(null);
  const curve = useMemo(() => new THREE.CatmullRomCurve3(points.map((p) => new THREE.Vector3(...p))), [points]);
  useFrame((state) => {
    const g = ref.current;
    if (!g) return;
    const t0 = (state.clock.elapsedTime * speed) % 1;
    for (let i = 0; i < g.children.length; i++) {
      const t = (t0 + i / count) % 1;
      const p = curve.getPointAt(t);
      g.children[i].position.copy(p);
    }
  });
  return (
    <group ref={ref}>
      {Array.from({ length: count }).map((_, i) => (
        <mesh key={i}>
          <sphereGeometry args={[size, 10, 10]} />
          <meshStandardMaterial color={color} emissive={color} emissiveIntensity={1.6} toneMapped={false} />
        </mesh>
      ))}
    </group>
  );
}

function PipePair({ pointsA, pointsB, color, speed }: { pointsA: V3[]; pointsB: V3[]; color: string; speed: number }) {
  return (
    <>
      <LPipe points={pointsA} color={color} />
      <LPipe points={pointsB} color={color} />
      <FlowAlongPath points={pointsA} color={color} speed={speed} />
      <FlowAlongPath points={pointsB} color={color} speed={speed} />
    </>
  );
}

function Chiller({ position, color, lit }: { position: V3; color: string; lit: boolean }) {
  return (
    <group position={position}>
      <mesh castShadow>
        <boxGeometry args={[1.6, 1.35, 1.45]} />
        <meshStandardMaterial
          color={lit ? '#182430' : '#262e38'}
          metalness={0.4} roughness={0.5}
          emissive={lit ? color : '#000000'} emissiveIntensity={lit ? 0.3 : 0}
        />
      </mesh>
      {/* base plinth */}
      <mesh position={[0, -0.78, 0]}>
        <boxGeometry args={[1.75, 0.16, 1.58]} />
        <meshStandardMaterial color="#10161e" />
      </mesh>
      {/* front LED strip */}
      <mesh position={[0, 0.15, 0.74]}>
        <boxGeometry args={[1.3, 0.1, 0.03]} />
        <meshStandardMaterial color={lit ? color : '#3a4654'} emissive={lit ? color : '#000'} emissiveIntensity={lit ? 0.9 : 0} toneMapped={false} />
      </mesh>
    </group>
  );
}

function Fan({ color }: { color: string }) {
  const ref = useRef<THREE.Group>(null);
  useFrame((_, delta) => {
    if (ref.current) ref.current.rotation.y += delta * 1.8;
  });
  return (
    <group ref={ref} position={[0, 1.02, 0]}>
      {[0, 1, 2, 3].map((i) => (
        <group key={i} rotation={[0, (i * Math.PI) / 2, 0]}>
          <mesh position={[0.44, 0, 0]}>
            <boxGeometry args={[0.86, 0.04, 0.16]} />
            <meshStandardMaterial color={color} metalness={0.4} roughness={0.5} />
          </mesh>
        </group>
      ))}
      <mesh><cylinderGeometry args={[0.1, 0.1, 0.12, 12]} /><meshStandardMaterial color={color} /></mesh>
    </group>
  );
}

function CoolingTower({ position, color, lit }: { position: V3; color: string; lit: boolean }) {
  return (
    <group position={position}>
      <mesh castShadow>
        <cylinderGeometry args={[0.75, 0.88, 2.0, 24]} />
        <meshStandardMaterial
          color={lit ? '#22303a' : '#2c2a28'}
          metalness={0.2} roughness={0.7}
          emissive={lit ? color : '#000'} emissiveIntensity={lit ? 0.16 : 0}
        />
      </mesh>
      <mesh position={[0, 1.01, 0]}>
        <torusGeometry args={[0.69, 0.05, 12, 24]} />
        <meshStandardMaterial color={color} emissive={color} emissiveIntensity={0.35} toneMapped={false} />
      </mesh>
      <Fan color={color} />
    </group>
  );
}

function Pump({ position, color, lit }: { position: V3; color: string; lit: boolean }) {
  return (
    <group position={position}>
      <mesh castShadow>
        <cylinderGeometry args={[0.32, 0.32, 0.58, 20]} />
        <meshStandardMaterial
          color={lit ? '#1a2632' : '#292f38'}
          metalness={0.35} roughness={0.55}
          emissive={lit ? color : '#000'} emissiveIntensity={lit ? 0.28 : 0}
        />
      </mesh>
      <mesh position={[0.34, 0, 0]} rotation={[0, 0, Math.PI / 2]}>
        <cylinderGeometry args={[0.17, 0.17, 0.36, 16]} />
        <meshStandardMaterial color="#3a4856" />
      </mesh>
    </group>
  );
}

function Plant({ cop, chillerRun, towerRun, pumpRun, pumpTotal, load }: System3DProps) {
  const chillerColor = cop >= COP_GOOD ? CHILLED : STATUS.warn;
  const flowSpeed = 0.22 + (load / 100) * 0.38;

  // --- device positions ---
  const c1: V3 = [-3.2, 0.75, -0.95];  // chiller 1
  const c2: V3 = [-3.2, 0.75, 0.95];   // chiller 2
  const t1: V3 = [3.2, 1.0, -0.95];     // tower 1
  const t2: V3 = [3.2, 1.0, 0.95];      // tower 2
  const cp1: V3 = [-1.3, 0.29, -0.95];  // chilled pump
  const cp2: V3 = [-1.3, 0.29, 0.95];   // condenser pump
  const bldgPos: V3 = [0, 2.55, -3.0];   // building slab

  // --- pipe routing elevations ---
  const CH_Y = 1.52;   // condenser header (chiller top)
  const CW_Y = 0.65;   // chilled header (mid-chiller)
  const BL_Y = 2.1;    // building connection

  // Condenser loops: chiller tops ↔ tower sides
  const condSupplyFwd: V3[] = [[-2.4, CH_Y, -0.95], [0, CH_Y, -0.95], [2.4, CH_Y, -0.95], [2.4, CH_Y - 0.3, -0.95]];
  const condReturnFwd: V3[] = [[-2.4, CH_Y + 0.18, -0.95], [0, CH_Y + 0.18, -0.95], [2.4, CH_Y + 0.18, -0.95], [2.4, CH_Y + 0.18 - 0.3, -0.95]];
  const condSupplyBack: V3[] = [[-2.4, CH_Y, 0.95], [0, CH_Y, 0.95], [2.4, CH_Y, 0.95], [2.4, CH_Y - 0.3, 0.95]];
  const condReturnBack: V3[] = [[-2.4, CH_Y + 0.18, 0.95], [0, CH_Y + 0.18, 0.95], [2.4, CH_Y + 0.18, 0.95], [2.4, CH_Y + 0.18 - 0.3, 0.95]];

  // Chilled loop: chiller → pump → building
  const chillSupFwd: V3[] = [[-2.4, CW_Y, -0.95], [-1.65, CW_Y, -0.95], [-1.3, 0.62, -0.95]];
  const chillRetFwd: V3[] = [[-2.4, CW_Y + 0.2, -0.95], [-1.65, CW_Y + 0.2, -0.95], [-1.3, 0.82, -0.95]];
  const chillToBldg: V3[] = [[-1.1, 0.72, -0.95], [0, 0.72, -0.95], [0, 0.72, -2.3], [0, BL_Y, -2.3], [0, BL_Y, -2.9]];
  const chillFromBldg: V3[] = [[0.38, BL_Y + 0.2, -2.9], [0.38, BL_Y + 0.2, -2.3], [0.38, 0.92, -2.3], [0.38, 0.92, 0.95], [-1.65, 0.92, 0.95], [-2.4, CW_Y + 0.2, 0.95]];

  const litC = (i: number) => i < chillerRun;
  const litT = (i: number) => i < towerRun;
  const litP = (i: number) => i < Math.min(pumpTotal, Math.ceil((pumpRun / pumpTotal) * 2));

  return (
    <>
      <ambientLight intensity={0.55} />
      <directionalLight position={[6, 10, 5]} intensity={1.3} castShadow shadow-mapSize={[1024, 1024]} />
      <pointLight position={[0, 4.5, 0]} color={'#3b82f6'} intensity={0.45} distance={24} />

      <Grid
        args={[26, 26]}
        cellSize={0.6} cellThickness={0.6} cellColor="#1b2a3a"
        sectionSize={3} sectionThickness={1} sectionColor="#23445a"
        fadeDistance={26} fadeStrength={1.4} infiniteGrid position={[0, 0.005, 0]}
      />
      <ContactShadows position={[0, 0.02, 0]} opacity={0.48} scale={24} blur={2.2} far={5} color="#000000" />

      {/* building / cooled-load slab */}
      <mesh position={bldgPos}>
        <boxGeometry args={[5, 1.2, 0.35]} />
        <meshStandardMaterial
          color={'#3b82f6'} transparent opacity={0.08}
          emissive={'#3b82f6'} emissiveIntensity={0.12}
        />
      </mesh>

      {/* devices */}
      <Chiller position={c1} color={chillerColor} lit={litC(0)} />
      <Chiller position={c2} color={chillerColor} lit={litC(1)} />
      <CoolingTower position={t1} color={CONDENSER} lit={litT(0)} />
      <CoolingTower position={t2} color={CONDENSER} lit={litT(1)} />
      <Pump position={cp1} color={CHILLED} lit={litP(0)} />
      <Pump position={cp2} color={CONDENSER} lit={litP(1)} />

      {/* condenser water loops */}
      <PipePair pointsA={condSupplyFwd} pointsB={condReturnFwd} color={CONDENSER} speed={flowSpeed} />
      <PipePair pointsA={condSupplyBack} pointsB={condReturnBack} color={CONDENSER} speed={flowSpeed} />

      {/* chilled water: chiller → pump → building */}
      <LPipe points={chillSupFwd} color={CHILLED} />
      <LPipe points={chillRetFwd} color={CHILLED} />
      <FlowAlongPath points={chillSupFwd} color={CHILLED} speed={flowSpeed} />
      <FlowAlongPath points={chillRetFwd} color={CHILLED} speed={flowSpeed * 0.9} />
      <LPipe points={chillToBldg} color={CHILLED} />
      <LPipe points={chillFromBldg} color={CHILLED} />
      <FlowAlongPath points={chillToBldg} color={CHILLED} speed={flowSpeed} />
      <FlowAlongPath points={chillFromBldg} color={CHILLED} speed={flowSpeed * 0.9} />

      {/* Labels removed — device identification handled by DevCard overlays in parent index.tsx.
          Keeping 3D Html labels conflicts with autoRotating camera + absolute-positioned DevCards,
          causing persistent overlap that cannot be resolved by CSS positioning alone. */}
    </>
  );
}

export default function System3D(props: System3DProps & { style?: React.CSSProperties }) {
  return (
    <Canvas
      shadows
      dpr={[1, 2]}
      camera={{ position: [7, 6.5, 11], fov: 42 }}
      gl={{ antialias: true, alpha: true }}
      style={{ width: '100%', height: '100%', background: 'transparent', ...props.style }}
    >
      <Plant {...props} />
      <OrbitControls
        target={[0, 0.9, -0.3]}
        enableDamping
        dampingFactor={0.08}
        enableZoom={false}
        enablePan={false}
        minAzimuthAngle={-0.72}
        maxAzimuthAngle={0.72}
        minPolarAngle={0.45}
        maxPolarAngle={1.18}
      />
    </Canvas>
  );
}
