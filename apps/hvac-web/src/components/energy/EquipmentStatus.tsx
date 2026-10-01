import { Badge } from '@/components/ui/badge';
import { StatusBadge, type StatusTone } from '@/components/common/StatusBadge';
import { Cpu, Wind, Droplets, Flame, Activity } from 'lucide-react';
import { cn } from '@/lib/utils';

export type HvacEquipmentKind =
  | 'chiller'
  | 'pump'
  | 'tower'
  | 'ahu'
  | 'fcu'
  | 'boiler'
  | 'generic';

export type EquipmentOperationalState =
  | 'running'
  | 'standby'
  | 'fault'
  | 'offline';

export interface EquipmentStatusProps {
  readonly kind?: HvacEquipmentKind;
  readonly state: EquipmentOperationalState;
  readonly loadPercent?: number; // e.g. 78 -> 78%
  readonly name?: string;
  readonly powerKw?: number;
  readonly compact?: boolean;
  readonly className?: string;
}

const equipmentIcons: Record<HvacEquipmentKind, typeof Cpu> = {
  chiller: Activity,
  pump: Droplets,
  tower: Droplets,
  ahu: Wind,
  fcu: Wind,
  boiler: Flame,
  generic: Cpu,
};

const stateToneMap: Record<EquipmentOperationalState, StatusTone> = {
  running: 'running',
  standby: 'standby',
  fault: 'alarm',
  offline: 'offline',
};

export function EquipmentStatus({
  kind = 'generic',
  state,
  loadPercent,
  name,
  powerKw,
  compact = false,
  className,
}: EquipmentStatusProps) {
  const Icon = equipmentIcons[kind] ?? Cpu;
  const tone = stateToneMap[state] ?? 'neutral';

  if (compact) {
    return (
      <div className={cn('inline-flex items-center gap-1.5', className)}>
        <StatusBadge tone={tone} />
        {loadPercent !== undefined && state === 'running' && (
          <span className="font-mono text-[11px] text-muted-foreground">
            {loadPercent}% 负荷
          </span>
        )}
      </div>
    );
  }

  return (
    <div
      className={cn(
        'flex items-center justify-between rounded-md border border-border/70 bg-card p-3 hover:border-border transition-colors',
        className
      )}
    >
      <div className="flex items-center gap-2.5">
        <div className="flex size-8 items-center justify-center rounded-md bg-muted/60 text-muted-foreground">
          <Icon className="size-4" />
        </div>
        <div>
          {name && <div className="text-xs font-semibold text-foreground">{name}</div>}
          <div className="flex items-center gap-1.5 mt-0.5">
            <StatusBadge tone={tone} />
            {loadPercent !== undefined && state === 'running' && (
              <Badge variant="outline" className="text-[10px] h-4 px-1 font-mono font-normal">
                负荷 {loadPercent}%
              </Badge>
            )}
          </div>
        </div>
      </div>

      {powerKw !== undefined && (
        <div className="text-right text-xs">
          <div className="font-mono font-bold text-foreground">{powerKw.toFixed(1)} kW</div>
          <div className="text-[10px] text-muted-foreground">实时功率</div>
        </div>
      )}
    </div>
  );
}
