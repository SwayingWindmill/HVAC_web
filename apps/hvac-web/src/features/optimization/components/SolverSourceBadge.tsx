import { Badge } from '@/components/ui/badge';
import type { GovernanceSolverSource } from '../optimization-model';

const SOLVER_SOURCE_BADGE_VARIANT: Record<GovernanceSolverSource, 'secondary' | 'success' | 'default'> = {
  DEFAULT: 'secondary',
  DEPLOYMENT_BINDING: 'success',
  SITE_LOOP_CANARY: 'default',
};

export function SolverSourceBadge({
  source,
  label,
}: {
  readonly source: GovernanceSolverSource;
  readonly label: string;
}) {
  return <Badge variant={SOLVER_SOURCE_BADGE_VARIANT[source]}>{label}</Badge>;
}
