import { useMemo, useRef } from 'react';
import { Canvas, useFrame } from '@react-three/fiber';
import { ContactShadows, Edges, Grid, OrbitControls, RoundedBox } from '@react-three/drei';
import * as THREE from 'three';
import { COP_GOOD, STATUS } from '@/theme/tokens';

export interface System3DProps {
  cop: number;
  chillerRun: number;
  chillerTotal: number;
  towerRun: number;
  towerTotal: number;
  pumpRun: number;
  pumpTotal: number;
  load: number;
}

type V3 = [number, number, number];

const CHILLED = '#3b82f6';
const CONDENSER = '#2fc98f';
const STEEL = '#465b73';
const STEEL_LIGHT = '#8ea2b9';
const PANEL = '#d8e1ea';
const PANEL_DARK = '#a9b7c7';
const FLOOR = '#152236';

function activeAt(index: number, displayCount: number, running: number, total: number) {
  if (displayCount <= 0 || total <= 0 || running <= 0) return false;
  return index < Math.max(1, Math.round((running / total) * displayCount));
}

function StatusBeacon({ active, color, position }: { active: boolean; color: string; position: V3 }) {
  return (
    <group position={position}>
      <mesh castShadow>
        <cylinderGeometry args={[0.08, 0.1, 0.08, 20]} />
        <meshStandardMaterial color="#172130" metalness={0.7} roughness={0.35} />
      </mesh>
      <mesh position={[0, 0.055, 0]}>
        <sphereGeometry args={[0.055, 16, 12]} />
        <meshStandardMaterial
          color={active ? color : '#5f6b7a'}
          emissive={active ? color : '#000000'}
          emissiveIntensity={active ? 2.3 : 0}
          toneMapped={false}
        />
      </mesh>
      {active ? <pointLight position={[0, 0.13, 0]} color={color} intensity={0.32} distance={1.8} /> : null}
    </group>
  );
}

function PipeRun({ points, color, radius = 0.065 }: { points: V3[]; color: string; radius?: number }) {
  const segments = useMemo(() => points.slice(0, -1).map((point, index) => {
    const start = new THREE.Vector3(...point);
    const end = new THREE.Vector3(...points[index + 1]);
    const direction = end.clone().sub(start);
    const length = direction.length();
    const center = start.clone().add(end).multiplyScalar(0.5);
    const quaternion = new THREE.Quaternion().setFromUnitVectors(
      new THREE.Vector3(0, 1, 0),
      direction.clone().normalize(),
    );
    return { start, end, center, quaternion, length };
  }).filter((segment) => segment.length > 0.001), [points]);

  return (
    <group>
      {segments.map((segment, index) => (
        <group key={index}>
          <mesh
            castShadow
            position={[segment.center.x, segment.center.y, segment.center.z]}
            quaternion={[segment.quaternion.x, segment.quaternion.y, segment.quaternion.z, segment.quaternion.w]}
          >
            <cylinderGeometry args={[radius, radius, segment.length, 20]} />
            <meshPhysicalMaterial
              color={color}
              metalness={0.42}
              roughness={0.32}
              clearcoat={0.55}
              clearcoatRoughness={0.28}
            />
          </mesh>
          {[segment.start, segment.end].map((point, flangeIndex) => (
            <mesh
              key={flangeIndex}
              castShadow
              position={[point.x, point.y, point.z]}
              quaternion={[segment.quaternion.x, segment.quaternion.y, segment.quaternion.z, segment.quaternion.w]}
            >
              <cylinderGeometry args={[radius * 1.42, radius * 1.42, 0.045, 20]} />
              <meshStandardMaterial color={STEEL_LIGHT} metalness={0.72} roughness={0.28} />
            </mesh>
          ))}
        </group>
      ))}
      {points.slice(1, -1).map((point, index) => (
        <mesh key={index} castShadow position={point}>
          <sphereGeometry args={[radius * 1.06, 18, 14]} />
          <meshPhysicalMaterial color={color} metalness={0.42} roughness={0.32} clearcoat={0.5} />
        </mesh>
      ))}
    </group>
  );
}

function FlowAlongPath({ points, color, speed, count = 7 }: {
  points: V3[];
  color: string;
  speed: number;
  count?: number;
}) {
  const ref = useRef<THREE.Group>(null);
  const curve = useMemo(() => {
    const path = new THREE.CurvePath<THREE.Vector3>();
    points.slice(0, -1).forEach((point, index) => {
      path.add(new THREE.LineCurve3(new THREE.Vector3(...point), new THREE.Vector3(...points[index + 1])));
    });
    return path;
  }, [points]);

  useFrame((state) => {
    if (!ref.current) return;
    const offset = (state.clock.elapsedTime * speed) % 1;
    ref.current.children.forEach((child, index) => {
      child.position.copy(curve.getPointAt((offset + index / count) % 1));
    });
  });

  return (
    <group ref={ref}>
      {Array.from({ length: count }).map((_, index) => (
        <mesh key={index}>
          <sphereGeometry args={[0.055, 12, 10]} />
          <meshStandardMaterial color={color} emissive={color} emissiveIntensity={2.2} toneMapped={false} />
        </mesh>
      ))}
    </group>
  );
}

function PipeCircuit({ supply, returnPath, color, speed }: {
  supply: V3[];
  returnPath: V3[];
  color: string;
  speed: number;
}) {
  return (
    <>
      <PipeRun points={supply} color={color} />
      <PipeRun points={returnPath} color={color} />
      <FlowAlongPath points={supply} color={color} speed={speed} />
      <FlowAlongPath points={returnPath} color={color} speed={speed * 0.88} />
    </>
  );
}

function Valve({ position, rotation = [0, 0, 0], color }: {
  position: V3;
  rotation?: V3;
  color: string;
}) {
  return (
    <group position={position} rotation={rotation}>
      <mesh castShadow>
        <cylinderGeometry args={[0.13, 0.13, 0.18, 20]} />
        <meshStandardMaterial color={STEEL_LIGHT} metalness={0.78} roughness={0.24} />
      </mesh>
      <mesh position={[0, 0.16, 0]} rotation={[Math.PI / 2, 0, 0]}>
        <torusGeometry args={[0.12, 0.02, 10, 24]} />
        <meshStandardMaterial color={color} metalness={0.45} roughness={0.32} />
      </mesh>
      <mesh position={[0, 0.1, 0]}>
        <cylinderGeometry args={[0.025, 0.025, 0.14, 10]} />
        <meshStandardMaterial color={STEEL_LIGHT} metalness={0.7} roughness={0.3} />
      </mesh>
    </group>
  );
}

function BaseSkid({ size, position = [0, 0, 0] }: { size: V3; position?: V3 }) {
  return (
    <RoundedBox args={size} radius={0.055} smoothness={4} position={position} castShadow receiveShadow>
      <meshStandardMaterial color="#111a27" metalness={0.68} roughness={0.42} />
      <Edges color="#34465c" threshold={18} />
    </RoundedBox>
  );
}

function ChillerUnit({ position, active, color }: { position: V3; active: boolean; color: string }) {
  const shellColor = active ? '#3b5871' : '#33485d';
  return (
    <group position={position}>
      <BaseSkid size={[2.45, 0.16, 1.2]} position={[0, 0.08, 0]} />

      {[0.43, 0.88].map((y, index) => (
        <group key={y} position={[0, y, 0]}>
          <mesh castShadow rotation={[0, 0, Math.PI / 2]}>
            <cylinderGeometry args={[index === 0 ? 0.34 : 0.3, index === 0 ? 0.34 : 0.3, 1.92, 36]} />
            <meshPhysicalMaterial
              color={shellColor}
              metalness={0.56}
              roughness={0.27}
              clearcoat={0.62}
              clearcoatRoughness={0.24}
              emissive={active ? color : '#000000'}
              emissiveIntensity={active ? 0.055 : 0}
            />
          </mesh>
          {[-0.99, 0.99].map((x) => (
            <group key={x} position={[x, 0, 0]} rotation={[0, 0, Math.PI / 2]}>
              <mesh castShadow>
                <cylinderGeometry args={[index === 0 ? 0.39 : 0.35, index === 0 ? 0.39 : 0.35, 0.07, 36]} />
                <meshStandardMaterial color={STEEL_LIGHT} metalness={0.72} roughness={0.25} />
              </mesh>
              <mesh position={[0, x > 0 ? 0.042 : -0.042, 0]} rotation={[Math.PI / 2, 0, 0]}>
                <circleGeometry args={[index === 0 ? 0.29 : 0.25, 36]} />
                <meshStandardMaterial color="#1a2533" metalness={0.45} roughness={0.42} side={THREE.DoubleSide} />
              </mesh>
            </group>
          ))}
        </group>
      ))}

      <RoundedBox args={[1.18, 0.52, 0.7]} radius={0.1} smoothness={5} position={[0.15, 1.28, -0.03]} castShadow>
        <meshPhysicalMaterial color="#30485f" metalness={0.54} roughness={0.28} clearcoat={0.7} clearcoatRoughness={0.2} />
        <Edges color="#496078" threshold={20} />
      </RoundedBox>

      <RoundedBox args={[0.42, 0.52, 0.08]} radius={0.025} smoothness={4} position={[-0.74, 1.18, 0.37]} castShadow>
        <meshStandardMaterial color={PANEL} metalness={0.28} roughness={0.38} />
      </RoundedBox>
      {[0.14, 0, -0.14].map((y, index) => (
        <mesh key={y} position={[-0.74, 1.18 + y, 0.416]}>
          <boxGeometry args={[0.28, 0.025, 0.014]} />
          <meshStandardMaterial color={index === 0 && active ? color : '#66778a'} emissive={index === 0 && active ? color : '#000000'} emissiveIntensity={1.4} toneMapped={false} />
        </mesh>
      ))}

      {[-0.82, 0.82].map((x) => (
        <group key={x} position={[x, 0.22, 0]}>
          <mesh castShadow>
            <boxGeometry args={[0.16, 0.3, 0.82]} />
            <meshStandardMaterial color="#1b2634" metalness={0.62} roughness={0.36} />
          </mesh>
        </group>
      ))}

      <StatusBeacon active={active} color={color} position={[0.82, 1.62, 0.02]} />
    </group>
  );
}

function PumpUnit({ position, active, color, rotation = [0, 0, 0] }: {
  position: V3;
  active: boolean;
  color: string;
  rotation?: V3;
}) {
  return (
    <group position={position} rotation={rotation}>
      <BaseSkid size={[1.55, 0.12, 0.72]} position={[0, 0.06, 0]} />

      <mesh castShadow position={[0.36, 0.42, 0]} rotation={[0, 0, Math.PI / 2]}>
        <cylinderGeometry args={[0.28, 0.28, 0.78, 28]} />
        <meshPhysicalMaterial color="#526a84" metalness={0.62} roughness={0.3} clearcoat={0.48} />
      </mesh>
      <mesh castShadow position={[0.78, 0.42, 0]} rotation={[0, 0, Math.PI / 2]}>
        <cylinderGeometry args={[0.18, 0.22, 0.08, 28]} />
        <meshStandardMaterial color={PANEL_DARK} metalness={0.62} roughness={0.3} />
      </mesh>
      {[-0.1, 0.08, 0.26, 0.44, 0.62].map((x) => (
        <mesh key={x} position={[x, 0.42, 0]} rotation={[0, Math.PI / 2, 0]}>
          <torusGeometry args={[0.285, 0.018, 8, 28]} />
          <meshStandardMaterial color="#65778b" metalness={0.68} roughness={0.26} />
        </mesh>
      ))}

      <mesh castShadow position={[-0.38, 0.42, 0]} rotation={[0, Math.PI / 2, 0]}>
        <torusGeometry args={[0.27, 0.13, 18, 40]} />
        <meshPhysicalMaterial
          color={active ? color : STEEL}
          metalness={0.48}
          roughness={0.32}
          clearcoat={0.5}
          emissive={active ? color : '#000000'}
          emissiveIntensity={active ? 0.08 : 0}
        />
      </mesh>
      <mesh castShadow position={[-0.38, 0.42, 0]} rotation={[0, Math.PI / 2, 0]}>
        <cylinderGeometry args={[0.14, 0.14, 0.3, 24]} />
        <meshStandardMaterial color="#243244" metalness={0.6} roughness={0.34} />
      </mesh>
      <mesh castShadow position={[-0.38, 0.74, 0]}>
        <cylinderGeometry args={[0.12, 0.12, 0.42, 20]} />
        <meshPhysicalMaterial color={color} metalness={0.5} roughness={0.3} clearcoat={0.5} />
      </mesh>
      <StatusBeacon active={active} color={color} position={[0.66, 0.79, 0]} />
    </group>
  );
}

function TowerFan({ active, color }: { active: boolean; color: string }) {
  const ref = useRef<THREE.Group>(null);
  useFrame((_, delta) => {
    if (ref.current && active) ref.current.rotation.y += delta * 1.3;
  });

  return (
    <group position={[0, 1.4, 0]}>
      <mesh rotation={[Math.PI / 2, 0, 0]}>
        <torusGeometry args={[0.48, 0.055, 16, 40]} />
        <meshStandardMaterial color={STEEL_LIGHT} metalness={0.7} roughness={0.28} />
      </mesh>
      <group ref={ref}>
        {[0, 1, 2, 3, 4].map((index) => (
          <group key={index} rotation={[0, (index * Math.PI * 2) / 5, 0]}>
            <mesh position={[0.25, 0, 0]}>
              <boxGeometry args={[0.48, 0.035, 0.13]} />
              <meshStandardMaterial color={active ? color : '#596779'} metalness={0.42} roughness={0.38} />
            </mesh>
          </group>
        ))}
        <mesh>
          <cylinderGeometry args={[0.11, 0.11, 0.12, 20]} />
          <meshStandardMaterial color="#26374a" metalness={0.62} roughness={0.32} />
        </mesh>
      </group>
    </group>
  );
}

function CoolingTowerUnit({ position, active, color }: { position: V3; active: boolean; color: string }) {
  return (
    <group position={position}>
      <BaseSkid size={[1.45, 0.14, 1.45]} position={[0, 0.07, 0]} />
      <mesh castShadow position={[0, 0.77, 0]} rotation={[0, Math.PI / 4, 0]}>
        <cylinderGeometry args={[0.73, 0.9, 1.36, 4]} />
        <meshPhysicalMaterial
          color={active ? '#49657b' : '#405365'}
          metalness={0.4}
          roughness={0.34}
          clearcoat={0.42}
          emissive={active ? color : '#000000'}
          emissiveIntensity={active ? 0.04 : 0}
        />
        <Edges color="#60738a" threshold={12} />
      </mesh>

      {[-0.5, -0.25, 0, 0.25, 0.5].map((y) => (
        <group key={y}>
          <mesh position={[0, 0.72 + y, 0.665]}>
            <boxGeometry args={[1.05, 0.055, 0.04]} />
            <meshStandardMaterial color="#7f91a4" metalness={0.55} roughness={0.3} />
          </mesh>
          <mesh position={[0.665, 0.72 + y, 0]} rotation={[0, Math.PI / 2, 0]}>
            <boxGeometry args={[1.05, 0.055, 0.04]} />
            <meshStandardMaterial color="#7f91a4" metalness={0.55} roughness={0.3} />
          </mesh>
        </group>
      ))}

      <RoundedBox args={[1.22, 0.16, 1.22]} radius={0.05} smoothness={4} position={[0, 1.35, 0]} castShadow>
        <meshStandardMaterial color="#36506a" metalness={0.55} roughness={0.31} />
        <Edges color="#52677f" threshold={16} />
      </RoundedBox>
      <TowerFan active={active} color={color} />
      <StatusBeacon active={active} color={color} position={[0.52, 1.52, 0.52]} />
    </group>
  );
}

function LoadModule({ load }: { load: number }) {
  const active = load > 0;
  return (
    <group position={[0, 0, -3.25]}>
      <BaseSkid size={[3.15, 0.14, 0.92]} position={[0, 0.07, 0]} />
      <RoundedBox args={[2.92, 1.16, 0.72]} radius={0.08} smoothness={5} position={[0, 0.72, 0]} castShadow>
        <meshPhysicalMaterial color="#304a63" metalness={0.48} roughness={0.34} clearcoat={0.46} />
        <Edges color="#425a72" threshold={18} />
      </RoundedBox>
      {[-1.05, -0.52, 0, 0.52, 1.05].map((x) => (
        <mesh key={x} position={[x, 0.72, 0.37]}>
          <boxGeometry args={[0.32, 0.82, 0.035]} />
          <meshStandardMaterial color="#53677d" metalness={0.58} roughness={0.3} />
        </mesh>
      ))}
      <mesh position={[0, 1.15, 0.39]}>
        <boxGeometry args={[2.45 * Math.max(0.12, load / 100), 0.055, 0.025]} />
        <meshStandardMaterial color={CHILLED} emissive={active ? CHILLED : '#000000'} emissiveIntensity={active ? 1.1 : 0} toneMapped={false} />
      </mesh>
    </group>
  );
}

function RaisedZone({ position, size, accent }: { position: V3; size: [number, number]; accent: string }) {
  return (
    <group position={position}>
      <RoundedBox args={[size[0], 0.055, size[1]]} radius={0.08} smoothness={4} receiveShadow>
        <meshStandardMaterial
          color={FLOOR}
          emissive={accent}
          emissiveIntensity={0.018}
          metalness={0.22}
          roughness={0.72}
        />
      </RoundedBox>
    </group>
  );
}

function Plant({ cop, chillerRun, chillerTotal, towerRun, towerTotal, pumpRun, pumpTotal, load }: System3DProps) {
  const chillerColor = cop >= COP_GOOD ? CHILLED : STATUS.warn;
  const flowSpeed = 0.16 + (load / 100) * 0.3;

  const chillerPositions: V3[] = [
    [-3.2, 0.03, -1.65],
    [-3.2, 0.03, 0],
    [-3.2, 0.03, 1.65],
  ];
  const towerPositions: V3[] = [
    [3.4, 0.03, -1.65],
    [3.4, 0.03, 0],
    [3.4, 0.03, 1.65],
  ];
  const pumpPositions: Array<{ position: V3; color: string; rotation?: V3 }> = [
    { position: [-1.15, 0.03, -1.45], color: CHILLED },
    { position: [-1.15, 0.03, 0.1], color: CHILLED },
    { position: [1.18, 0.03, 0.1], color: CONDENSER, rotation: [0, Math.PI, 0] },
    { position: [1.18, 0.03, 1.65], color: CONDENSER, rotation: [0, Math.PI, 0] },
  ];

  const chilledSupply: V3[] = [
    [-2.05, 0.54, -1.65],
    [-1.55, 0.54, -1.65],
    [-0.75, 0.54, -1.65],
    [-0.75, 0.54, -2.45],
    [0, 0.54, -2.45],
    [0, 0.54, -2.86],
  ];
  const chilledReturn: V3[] = [
    [0.36, 0.76, -2.86],
    [0.36, 0.76, -2.2],
    [-0.34, 0.76, -2.2],
    [-0.34, 0.76, 1.65],
    [-2.05, 0.76, 1.65],
  ];

  const condenserSupply: V3[] = [
    [-2.05, 1.14, -1.65],
    [-0.1, 1.14, -1.65],
    [2.45, 1.14, -1.65],
    [2.45, 0.78, -1.65],
  ];
  const condenserReturn: V3[] = [
    [2.45, 1.02, 1.65],
    [2.45, 1.34, 1.65],
    [0.1, 1.34, 1.65],
    [-2.05, 1.34, 1.65],
  ];

  return (
    <>
      <ambientLight intensity={0.34} />
      <hemisphereLight intensity={0.88} color="#eff8ff" groundColor="#0c1724" />
      <directionalLight
        position={[5.5, 9, 6.5]}
        intensity={2.15}
        castShadow
        shadow-mapSize={[2048, 2048]}
        shadow-camera-left={-8}
        shadow-camera-right={8}
        shadow-camera-top={8}
        shadow-camera-bottom={-8}
      />
      <spotLight position={[-5, 6, 4]} angle={0.5} penumbra={0.7} intensity={1.05} color="#a7d7ff" distance={18} />
      <spotLight position={[5, 5, -3]} angle={0.55} penumbra={0.8} intensity={0.9} color="#8df4d3" distance={18} />

      <Grid
        args={[24, 24]}
        cellSize={0.6}
        cellThickness={0.45}
        cellColor="#17283b"
        sectionSize={3}
        sectionThickness={0.78}
        sectionColor="#244159"
        fadeDistance={20}
        fadeStrength={1.75}
        infiniteGrid
        position={[0, 0.002, 0]}
      />
      <ContactShadows position={[0, 0.012, 0]} opacity={0.42} scale={19} blur={3.1} far={5.5} color="#000000" />

      <RaisedZone position={[-3.2, 0.015, 0]} size={[2.85, 5.45]} accent={CHILLED} />
      <RaisedZone position={[0, 0.015, 0.12]} size={[3.65, 5.25]} accent="#64748b" />
      <RaisedZone position={[3.4, 0.015, 0]} size={[2.7, 5.45]} accent={CONDENSER} />

      <LoadModule load={load} />

      {chillerPositions.map((position, index) => (
        <ChillerUnit
          key={index}
          position={position}
          active={activeAt(index, chillerPositions.length, chillerRun, chillerTotal)}
          color={chillerColor}
        />
      ))}
      {towerPositions.map((position, index) => (
        <CoolingTowerUnit
          key={index}
          position={position}
          active={activeAt(index, towerPositions.length, towerRun, towerTotal)}
          color={CONDENSER}
        />
      ))}
      {pumpPositions.map((pump, index) => (
        <PumpUnit
          key={index}
          position={pump.position}
          rotation={pump.rotation}
          active={activeAt(index, pumpPositions.length, pumpRun, pumpTotal)}
          color={pump.color}
        />
      ))}

      <PipeCircuit supply={chilledSupply} returnPath={chilledReturn} color={CHILLED} speed={flowSpeed} />
      <PipeCircuit supply={condenserSupply} returnPath={condenserReturn} color={CONDENSER} speed={flowSpeed * 0.92} />

      {[-1.65, 0, 1.65].map((z) => (
        <group key={`chiller-branch-${z}`}>
          <PipeRun points={[[-2.05, 0.54, z], [-1.82, 0.54, z]]} color={CHILLED} radius={0.052} />
          <PipeRun points={[[-2.05, 1.14, z], [-1.72, 1.14, z]]} color={CONDENSER} radius={0.052} />
        </group>
      ))}
      {[-1.65, 0, 1.65].map((z) => (
        <group key={`tower-branch-${z}`}>
          <PipeRun points={[[2.45, 0.78, z], [2.72, 0.78, z]]} color={CONDENSER} radius={0.052} />
          <PipeRun points={[[2.45, 1.02, z], [2.72, 1.02, z]]} color={CONDENSER} radius={0.052} />
        </group>
      ))}

      <Valve position={[-0.75, 0.69, -2.45]} color={CHILLED} />
      <Valve position={[-0.34, 0.91, -0.72]} color={CHILLED} />
      <Valve position={[0.2, 1.29, -1.65]} color={CONDENSER} />
      <Valve position={[0.2, 1.49, 1.65]} color={CONDENSER} />
    </>
  );
}

export default function System3D(props: System3DProps & { style?: React.CSSProperties }) {
  return (
    <Canvas
      shadows
      dpr={[1, 1.8]}
      camera={{ position: [8.8, 7.2, 11.6], fov: 38 }}
      gl={{ antialias: true, alpha: true, powerPreference: 'high-performance' }}
      onCreated={({ gl }) => {
        gl.toneMapping = THREE.ACESFilmicToneMapping;
        gl.toneMappingExposure = 1.22;
      }}
      style={{ width: '100%', height: '100%', background: 'transparent', ...props.style }}
    >
      <Plant {...props} />
      <OrbitControls
        target={[0, 0.72, -0.15]}
        enableDamping
        dampingFactor={0.075}
        enableZoom
        zoomSpeed={0.72}
        minDistance={10.5}
        maxDistance={18.5}
        enablePan={false}
        minAzimuthAngle={-0.48}
        maxAzimuthAngle={0.48}
        minPolarAngle={0.54}
        maxPolarAngle={1.02}
      />
    </Canvas>
  );
}
