import type { ReactNode } from 'react';
import { Link } from '@tanstack/react-router';
import { Building2, Cpu, Gauge, Layers, Lightbulb, Wrench } from 'lucide-react';
import { cn } from '@/lib/utils';

export type EntityType =
  | 'site'
  | 'building'
  | 'system'
  | 'equipment'
  | 'meter'
  | 'opportunity'
  | 'work';

export interface EntityLinkProps {
  readonly id: string;
  readonly name: ReactNode;
  readonly type?: EntityType;
  readonly href?: string;
  readonly onClick?: () => void;
  readonly className?: string;
}

const entityIcons: Record<EntityType, typeof Building2> = {
  site: Building2,
  building: Building2,
  system: Layers,
  equipment: Cpu,
  meter: Gauge,
  opportunity: Lightbulb,
  work: Wrench,
};

export function EntityLink({
  id,
  name,
  type = 'equipment',
  href,
  onClick,
  className,
}: EntityLinkProps) {
  const Icon = entityIcons[type] ?? Cpu;

  const content = (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 font-medium text-xs text-foreground hover:text-primary transition-colors cursor-pointer group',
        className
      )}
    >
      <Icon className="size-3.5 text-muted-foreground group-hover:text-primary shrink-0 transition-colors" />
      <span className="truncate underline-offset-4 group-hover:underline">
        {name ?? id}
      </span>
    </span>
  );

  if (href) {
    return (
      <Link to={href} className="inline-flex">
        {content}
      </Link>
    );
  }

  if (onClick) {
    return (
      <button type="button" onClick={onClick} className="text-left">
        {content}
      </button>
    );
  }

  return content;
}
