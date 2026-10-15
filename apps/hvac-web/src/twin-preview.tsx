import { StrictMode, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './global.css';
import { TwinScene, type CameraView } from './features/operations/digital-twin/components/TwinScene';
import { BUILDING_PLANS } from './features/operations/digital-twin/twin-building';
import { useScenePalette } from './features/operations/digital-twin/use-scene-palette';
import type { TwinEquipment, TwinPlant } from './features/operations/digital-twin/twin-model';

const params = new URLSearchParams(location.search);
if (params.get('theme') === 'dark') document.documentElement.classList.add('dark');
const r = (display: string, unit: string) => ({ label: '', display, unit });
const chiller = (id: string, name: string, state: TwinEquipment['state'], power: string, temps: [string, string, string, string]): TwinEquipment => {
  const values: Record<string, { label: string; display: string; unit: string }> = state === 'unconnected' ? {} : {
    'chiller.power': r(power, 'kW'),
    'chiller.leaving_chilled_water_temperature': r(temps[0], '℃'),
    'chiller.entering_chilled_water_temperature': r(temps[1], '℃'),
    'chiller.entering_cooling_water_temperature': r(temps[2], '℃'),
    'chiller.leaving_cooling_water_temperature': r(temps[3], '℃'),
  };
  return { assetId: id, name, kind: 'chiller', state, device: null, readings: Object.values(values), values };
};
const ghost = (id: string, name: string, kind: TwinEquipment['kind']): TwinEquipment => ({ assetId: id, name, kind, state: 'unconnected', device: null, readings: [], values: {} });
const pr = (display: string, unit: string) => ({ label: '', display, unit, numeric: Number(display), current: true, state: 'PRESENT' as const, freshness: 'FRESH' as const, sampledAt: null });
const plant: TwinPlant = {
  chillers: [
    chiller('a', 'A 机组', 'stopped', '0.2', ['12.1', '12.4', '26.0', '26.3']),
    chiller('b', 'B 机组', 'running', '118.6', ['7.2', '12.0', '30.1', '34.8']),
    chiller('c', 'C 机组', 'running', '96.4', ['7.4', '12.1', '30.3', '35.0']),
    chiller('d', 'D 机组', 'running', '62.9', ['7.9', '12.2', '30.2', '33.9']),
  ],
  towers: [1, 2, 3, 4].map((n) => ghost(`t${n}`, `${n} 号冷却塔`, 'tower')),
  chilledPumps: [1, 2, 3, 4].map((n) => ghost(`p${n}`, `${n} 号冷冻泵`, 'chilledPump')),
  coolingPumps: [1, 2, 3, 4].map((n) => ghost(`c${n}`, `${n} 号冷却泵`, 'coolingPump')),
  chilledFlow: params.get('flow') !== 'off',
  coolingFlow: params.get('flow') !== 'off',
  supply: pr('7.4', '℃'),
  return: pr('12.1', '℃'),
  flow: pr('132', 'm³/h'),
};

function Preview() {
  const palette = useScenePalette();
  const [selected, setSelected] = useState<string | null>(params.get('select'));
  return (
    <div style={{ padding: 24, background: 'var(--background)', height: '100vh', boxSizing: 'border-box' }}>
      <div style={{ position: 'relative', height: '100%', borderRadius: 10, overflow: 'hidden', border: '1px solid var(--border)' }} className="bg-[radial-gradient(ellipse_at_50%_35%,var(--card),var(--background))]">
        <TwinScene
          plant={plant}
          plan={BUILDING_PLANS.songyaxuan}
          palette={palette}
          view={(params.get('view') as CameraView) ?? 'overview'}
          cut={params.get('floor') ? Number(params.get('floor')) : null}
          shell={params.get('shell') !== 'off'}
          emphasis={params.get('pipes') === 'on'}
          selectedId={selected}
          onSelect={(item) => setSelected(item?.assetId ?? null)}
        />
      </div>
    </div>
  );
}

createRoot(document.getElementById('root')!).render(<StrictMode><Preview /></StrictMode>);
