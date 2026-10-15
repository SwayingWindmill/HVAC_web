import { useEffect } from 'react';
import { AssetMesh, InstancedAsset, useMaterialSet } from './assets/AssetMesh';
import { equipmentAsset } from './assets/equipment';
import { furnitureAsset } from './assets/furniture';
import type { V3 } from './assets/kit';
import type { BuildingLayout, FloorLayout } from './building-layout';
import { Device, type DeviceEvents } from './Devices';
import { DuctNetwork, PipeNetwork, type FlowMaterial } from './Pipes';

// The building, drawn floor by floor so a floor cut hides everything above it: structure, the street
// facade (hidden with the shell switch), furniture, the air side, and the chilled water reaching each
// floor's units. Terminal units are selectable like the plant's machines.

export interface BuildingFlows {
  readonly chilled: FlowMaterial;
  readonly supplyAir: FlowMaterial;
  readonly freshAir: FlowMaterial;
}

interface BuildingProps extends DeviceEvents {
  readonly layout: BuildingLayout;
  readonly origin: V3;
  /** Show floors up to this level; null shows the whole building with its roof. */
  readonly cut: number | null;
  readonly shell: boolean;
  readonly flows: BuildingFlows;
  readonly chilledMoving: boolean;
  readonly liftedId: string | null;
  readonly selectedId: string | null;
}

function Floor({ floor, shell, flows, chilledMoving, liftedId, selectedId, onSelect, onHover }: Omit<BuildingProps, 'layout' | 'origin' | 'cut'> & { readonly floor: FloorLayout }) {
  const set = useMaterialSet();
  return (
    <group>
      <AssetMesh asset={floor.structure} set={set} />
      {shell ? <AssetMesh asset={floor.facade} set={set} /> : null}
      {[...floor.furniture].map(([name, placements]) => <InstancedAsset key={name} asset={furnitureAsset(name)} placements={placements} />)}
      <InstancedAsset asset={equipmentAsset('diffuser')} placements={floor.diffusers} shadows={false} />
      <InstancedAsset asset={equipmentAsset('returnGrille')} placements={floor.grilles} shadows={false} />
      <DuctNetwork runs={floor.supplyDucts} flow={flows.supplyAir} />
      <DuctNetwork runs={floor.freshDucts} flow={flows.freshAir} />
      <PipeNetwork runs={floor.pipes} valves={[]} flow={flows.chilled} moving={chilledMoving} />
      {floor.terminals.map((placement) => (
        <Device
          key={placement.terminal.equipment.assetId}
          item={placement.terminal.equipment}
          asset={placement.asset}
          position={placement.at}
          turn={placement.turn}
          lifted={liftedId === placement.terminal.equipment.assetId}
          selected={selectedId === placement.terminal.equipment.assetId}
          onSelect={onSelect}
          onHover={onHover}
        />
      ))}
    </group>
  );
}

export function Building({ layout, origin, cut, ...rest }: BuildingProps) {
  const set = useMaterialSet();
  useEffect(() => () => {
    for (const floor of layout.floors) [floor.structure, floor.facade].forEach((asset) => asset.parts.forEach((part) => part.dispose()));
    layout.roof.structure.parts.forEach((part) => part.dispose());
  }, [layout]);
  return (
    <group position={origin}>
      {layout.floors.filter((floor) => cut === null || floor.level <= cut).map((floor) => <Floor key={floor.level} floor={floor} {...rest} />)}
      {cut === null ? (
        <group>
          <AssetMesh asset={layout.roof.structure} set={set} />
          {layout.roof.equipment.map((item, index) => (
            <group key={index} position={item.at} rotation={[0, item.turn, 0]} scale={item.scale}>
              <AssetMesh asset={equipmentAsset(item.asset)} set={set} />
            </group>
          ))}
          <DuctNetwork runs={layout.roof.ducts} flow={rest.flows.freshAir} />
        </group>
      ) : null}
    </group>
  );
}
