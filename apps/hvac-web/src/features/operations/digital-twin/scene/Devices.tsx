import { useRef, type ReactNode } from 'react';
import { useFrame, type ThreeEvent } from '@react-three/fiber';
import * as THREE from 'three';
import type { TwinEquipment } from '../twin-model';
import { AssetMesh, useMaterialSet } from './assets/AssetMesh';
import { equipmentAsset, type EquipmentAsset } from './assets/equipment';
import type { V3 } from './assets/kit';
import type { MaterialSet } from './assets/materials';

// A machine in the scene: its model from the asset library, a status lamp where the real unit has one,
// and its moving part turning only while the machine reports running. Hovering lifts it, clicking
// selects it; the camera and the labels respond in the scene above.

export interface DeviceEvents {
  readonly onSelect: (item: TwinEquipment) => void;
  readonly onHover: (item: TwinEquipment | null) => void;
}

interface DeviceProps extends DeviceEvents {
  readonly item: TwinEquipment;
  readonly asset: EquipmentAsset;
  readonly position: V3;
  readonly turn?: number;
  readonly scale?: number;
  readonly lifted: boolean;
  readonly children?: (set: MaterialSet) => ReactNode;
}

const lampKey = (item: TwinEquipment) => (item.state === 'running' ? 'ledRun' : item.state === 'fault' ? 'ledFault' : 'ledOff');
const lampGeometry = new THREE.BoxGeometry(0.014, 0.035, 0.12);

export function Device({ item, asset, position, turn = 0, scale = 1, lifted, onSelect, onHover, children }: DeviceProps) {
  const set = useMaterialSet(lifted);
  const model = equipmentAsset(asset);
  const lamp = model.ports.led;
  const over = (event: ThreeEvent<PointerEvent>) => { event.stopPropagation(); onHover(item); document.body.style.cursor = 'pointer'; };
  const out = () => { onHover(null); document.body.style.cursor = ''; };
  const click = (event: ThreeEvent<MouseEvent>) => { event.stopPropagation(); onSelect(item); };
  return (
    <group position={position} rotation={[0, turn, 0]} scale={scale} onPointerOver={over} onPointerOut={out} onClick={click}>
      <AssetMesh asset={model} set={set} />
      {lamp ? <mesh geometry={lampGeometry} material={set[lampKey(item)]} position={lamp} rotation={asset === 'chiller' ? [0, 0, 0] : [0, Math.PI / 2, 0]} /> : null}
      {children?.(set)}
    </group>
  );
}

/** A part turning about one axis while its machine runs; rev/s kept low and mechanical. */
function Spinner({ axis, speed, running, children, position }: { readonly axis: 'x' | 'y'; readonly speed: number; readonly running: boolean; readonly children: ReactNode; readonly position: V3 }) {
  const ref = useRef<THREE.Group>(null);
  useFrame((_, delta) => {
    if (running && ref.current) ref.current.rotation[axis] += Math.min(delta, 0.1) * speed;
  });
  return <group ref={ref} position={position}>{children}</group>;
}

type MachineProps = DeviceEvents & { readonly item: TwinEquipment; readonly position: V3; readonly lifted: boolean };

export function Chiller(props: MachineProps) {
  return <Device {...props} asset="chiller" />;
}

export function Pump(props: MachineProps) {
  const running = props.item.state === 'running';
  return (
    <Device {...props} asset="pump">
      {(set) => (
        <Spinner axis="x" speed={5} running={running} position={[0.1, 0.64, 0]}>
          <AssetMesh asset={equipmentAsset('coupling')} set={set} shadows={false} />
        </Spinner>
      )}
    </Device>
  );
}

export function Tower(props: MachineProps) {
  const running = props.item.state === 'running';
  return (
    <Device {...props} asset="tower">
      {(set) => (
        <Spinner axis="y" speed={1.4} running={running} position={[0, 3.36, 0]}>
          <AssetMesh asset={equipmentAsset('towerFan')} set={set} />
        </Spinner>
      )}
    </Device>
  );
}
