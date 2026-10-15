import { cn } from '@/lib/utils';
import { TWIN_STATE_LABELS, type TwinEquipment, type TwinReading, type TwinState } from '../twin-model';

// Label contents, in the design system's card style with a stem down to the anchor. Chillers always
// show their card and open it to the four water temperatures when the view is close or the machine is
// hovered or selected; pumps and towers name themselves on hover, on selection, or in their own view.

const STATE_DOT: Readonly<Record<TwinState, string>> = {
  running: 'bg-success',
  stopped: 'bg-chart-inactive',
  fault: 'bg-destructive',
  offline: 'bg-chart-inactive',
  unknown: 'bg-chart-inactive',
  unconnected: 'bg-chart-inactive',
};

const CARD = 'rounded-lg border bg-card/95 shadow-[0_8px_24px_-12px_rgba(24,24,27,0.35)] backdrop-blur-sm';

const value = (reading: TwinReading | undefined) => (reading
  ? <>{reading.display}<span className="ml-0.5 text-[10px] font-normal text-muted-foreground">{reading.unit}</span></>
  : <span className="text-muted-foreground">—</span>);

function Stem() {
  return (
    <div className="flex flex-col items-center">
      <span className="h-5 w-px bg-gradient-to-b from-border to-transparent" />
    </div>
  );
}

function State({ state }: { readonly state: TwinState }) {
  return (
    <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
      <span className={cn('size-1.5 rounded-full', STATE_DOT[state], state === 'running' && 'shadow-[0_0_0_3px_color-mix(in_oklab,var(--success)_25%,transparent)]')} />
      {TWIN_STATE_LABELS[state]}
    </span>
  );
}

export function ChillerCard({ item, open, selected, onSelect }: {
  readonly item: TwinEquipment;
  readonly open: boolean;
  readonly selected: boolean;
  readonly onSelect: () => void;
}) {
  const v = item.values;
  return (
    <div className="flex flex-col items-center">
      <button
        type="button"
        onClick={onSelect}
        className={cn(CARD, 'min-w-[156px] cursor-pointer px-3 py-2 text-left transition-[border-color,box-shadow] hover:border-primary/60', selected && 'border-primary ring-[3px] ring-primary/15')}
      >
        <span className="flex items-center justify-between gap-3">
          <span className="text-[13px] font-semibold whitespace-nowrap text-foreground">{item.name}</span>
          <State state={item.state} />
        </span>
        {item.state !== 'unconnected' ? (
          <span className="mt-1 block text-[17px] leading-none font-semibold tabular-nums text-foreground">{value(v['chiller.power'])}</span>
        ) : null}
        {open && item.state !== 'unconnected' ? (
          <span className="mt-2 grid grid-cols-[auto_auto] gap-x-4 gap-y-1 border-t pt-2 text-[11px] whitespace-nowrap tabular-nums">
            <span className="flex items-center gap-1.5 text-muted-foreground"><i className="h-2 w-0.5 rounded-full bg-primary" />冷冻水出</span><span className="text-right font-medium text-foreground">{value(v['chiller.leaving_chilled_water_temperature'])}</span>
            <span className="flex items-center gap-1.5 text-muted-foreground"><i className="h-2 w-0.5 rounded-full bg-primary/50" />冷冻水进</span><span className="text-right font-medium text-foreground">{value(v['chiller.entering_chilled_water_temperature'])}</span>
            <span className="flex items-center gap-1.5 text-muted-foreground"><i className="h-2 w-0.5 rounded-full bg-warning/50" />冷却水进</span><span className="text-right font-medium text-foreground">{value(v['chiller.entering_cooling_water_temperature'])}</span>
            <span className="flex items-center gap-1.5 text-muted-foreground"><i className="h-2 w-0.5 rounded-full bg-warning" />冷却水出</span><span className="text-right font-medium text-foreground">{value(v['chiller.leaving_cooling_water_temperature'])}</span>
          </span>
        ) : null}
      </button>
      <Stem />
    </div>
  );
}

export function NameTag({ item, selected }: { readonly item: TwinEquipment; readonly selected: boolean }) {
  return (
    <div className="flex flex-col items-center">
      <div className={cn(CARD, 'flex items-center gap-2 px-2 py-1', selected && 'border-primary')}>
        <span className="text-xs font-medium whitespace-nowrap text-foreground">{item.name}</span>
        <State state={item.state} />
      </div>
      <Stem />
    </div>
  );
}

export function PipeTag({ title, items }: { readonly title: string; readonly items: readonly (readonly [string, React.ReactNode])[] }) {
  return (
    <div className="flex flex-col items-center">
      <div className={cn(CARD, 'px-3 py-2')}>
        <div className="flex items-center gap-1.5 text-[11px] font-medium whitespace-nowrap text-muted-foreground"><i className="h-2 w-0.5 rounded-full bg-primary" />{title}</div>
        <div className="mt-1.5 flex gap-4 whitespace-nowrap">
          {items.map(([label, content]) => (
            <div key={label} className="flex flex-col">
              <span className="text-[10px] text-muted-foreground">{label}</span>
              <span className="text-sm font-semibold tabular-nums text-foreground">{content}</span>
            </div>
          ))}
        </div>
      </div>
      <Stem />
    </div>
  );
}

/** A caption lying on the floor beside an area of the plant. */
export function AreaCaption({ children }: { readonly children: React.ReactNode }) {
  return <span className="block translate-y-1/2 text-[11px] font-medium tracking-wide whitespace-nowrap text-muted-foreground/80">{children}</span>;
}

/** A floor's name beside the building's street corner. */
export function FloorTag({ children }: { readonly children: React.ReactNode }) {
  return <span className={cn(CARD, 'block translate-x-1/2 translate-y-1/2 px-2 py-0.5 text-[11px] font-medium whitespace-nowrap text-muted-foreground')}>{children}</span>;
}
