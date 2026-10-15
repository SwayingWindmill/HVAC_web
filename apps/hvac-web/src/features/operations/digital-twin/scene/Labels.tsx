import type { ReactNode } from 'react';
import { StatusBadge, statusDotClass, type StatusTone } from '@/components/common/StatusBadge';
import { cn } from '@/lib/utils';
import { TWIN_STATE_LABELS, type TwinEquipment, type TwinReading, type TwinState } from '../twin-model';

// Labels over the scene, in the design system's floating layer (popover surface, border, shadow-md),
// with its type scale and the shared status badge: a chiller's card (power, and the four water
// temperatures when hovered or selected), name tags for the other machines, the mains' readings,
// floor tags and room captions. A short stem points down to the anchor.

const FLOATING = 'rounded-lg border bg-popover text-popover-foreground shadow-md';

export const TWIN_STATUS_TONE: Readonly<Record<TwinState, StatusTone>> = {
  running: 'running',
  stopped: 'neutral',
  fault: 'alarm',
  offline: 'offline',
  unknown: 'neutral',
  unconnected: 'neutral',
};

/** A reading with its unit stepped down (DS §2.2): absent readings show a dash, never zero. */
function Reading({ reading, className }: { readonly reading: TwinReading | null | undefined; readonly className?: string }) {
  if (!reading) return <span className={cn('text-muted-foreground', className)}>—</span>;
  return (
    <span className={cn('tabular-nums', className)}>
      {reading.display}
      {reading.unit ? <span className="ml-1 text-xs font-medium text-subtle-foreground">{reading.unit}</span> : null}
    </span>
  );
}

function Stem() {
  return <span className="mx-auto block h-4 w-px bg-border" />;
}

const TEMPERATURES: readonly (readonly [string, string])[] = [
  ['chiller.leaving_chilled_water_temperature', '冷冻水出水'],
  ['chiller.entering_chilled_water_temperature', '冷冻水进水'],
  ['chiller.entering_cooling_water_temperature', '冷却水进水'],
  ['chiller.leaving_cooling_water_temperature', '冷却水出水'],
];

export function ChillerCard({ item, open, selected, onSelect }: {
  readonly item: TwinEquipment;
  readonly open: boolean;
  readonly selected: boolean;
  readonly onSelect: () => void;
}) {
  const connected = item.state !== 'unconnected';
  return (
    <div className="flex flex-col">
      <button
        type="button"
        onClick={onSelect}
        className={cn(
          FLOATING,
          'min-w-[176px] cursor-pointer px-3 py-2.5 text-left outline-none transition-colors duration-100 hover:border-input-hover focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring-soft',
          selected && 'border-ring ring-3 ring-ring-soft',
        )}
      >
        <span className="flex items-center justify-between gap-3">
          <span className="text-sm font-semibold whitespace-nowrap">{item.name}</span>
          <StatusBadge tone={TWIN_STATUS_TONE[item.state]} label={TWIN_STATE_LABELS[item.state]} />
        </span>
        {connected ? (
          <Reading reading={item.values['chiller.power']} className="mt-1 block text-base leading-[22px] font-semibold tracking-[-0.02em]" />
        ) : null}
        {open && connected ? (
          <dl className="mt-2 grid grid-cols-[auto_auto] gap-x-4 gap-y-1 border-t border-divider pt-2 text-xs whitespace-nowrap">
            {TEMPERATURES.map(([key, label]) => (
              <div key={key} className="contents">
                <dt className="text-muted-foreground">{label}</dt>
                <dd className="text-right font-medium"><Reading reading={item.values[key]} /></dd>
              </div>
            ))}
          </dl>
        ) : null}
      </button>
      <Stem />
    </div>
  );
}

/** A machine's name and state, as a compact chip. */
export function NameTag({ item, selected }: { readonly item: TwinEquipment; readonly selected: boolean }) {
  return (
    <div className="flex flex-col">
      <span className={cn(FLOATING, 'inline-flex h-7 items-center gap-1.5 px-2.5 text-xs whitespace-nowrap', selected && 'border-ring ring-3 ring-ring-soft')}>
        <span className="font-medium">{item.name}</span>
        <span aria-hidden="true" className={cn('size-1.5 rounded-full', statusDotClass(TWIN_STATUS_TONE[item.state]))} />
        <span className="text-muted-foreground">{TWIN_STATE_LABELS[item.state]}</span>
      </span>
      <Stem />
    </div>
  );
}

/** The chilled-water mains to the building: supply, return and flow. */
export function PipeTag({ title, swatch, items }: { readonly title: string; readonly swatch: string; readonly items: readonly (readonly [string, TwinReading | null])[] }) {
  return (
    <div className="flex flex-col">
      <div className={cn(FLOATING, 'px-3 py-2')}>
        <div className="flex items-center gap-1.5 text-xs font-medium whitespace-nowrap text-muted-foreground">
          <span aria-hidden="true" className="h-1 w-3 rounded-full" style={{ background: swatch }} />
          {title}
        </div>
        <dl className="mt-1.5 flex gap-4 whitespace-nowrap">
          {items.map(([label, reading]) => (
            <div key={label} className="flex flex-col">
              <dt className="text-xs text-muted-foreground">{label}</dt>
              <dd className="text-sm font-semibold"><Reading reading={reading} /></dd>
            </div>
          ))}
        </dl>
      </div>
      <Stem />
    </div>
  );
}

/** A floor's name beside the building's street corner. */
export function FloorTag({ children }: { readonly children: ReactNode }) {
  return <span className={cn(FLOATING, 'inline-flex h-6 translate-x-1/2 translate-y-1/2 items-center px-2 text-xs font-medium whitespace-nowrap text-muted-foreground')}>{children}</span>;
}

/** A caption lying on the ground beside an area, or on a room's floor. */
export function AreaCaption({ children }: { readonly children: ReactNode }) {
  return <span className="block translate-y-1/2 text-xs font-medium whitespace-nowrap text-subtle-foreground">{children}</span>;
}
