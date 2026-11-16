import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { cn } from '@/lib/utils';
import { MODE_PRESENTATION, siteControlStateQuery } from '../control-state';

/**
 * The top bar's reminder that the platform is not dispatching: shown on every Site page while the
 * Site is under manual control or has exited remote control, and links to the control page.
 */
export function ControlModeChip({ siteId }: { readonly siteId: string }) {
  const state = useQuery(siteControlStateQuery(siteId));
  const mode = state.data?.controlMode;
  if (!mode || mode === 'REMOTE_AUTO') return null;
  const presentation = MODE_PRESENTATION[mode];
  return (
    <Link
      to="/operations/control"
      search={{ site: siteId }}
      title={`${presentation.label}中，平台不下发指令${state.data?.reason ? `：${state.data.reason}` : ''}`}
      className={cn(
        'inline-flex h-[22px] shrink-0 items-center gap-1 rounded-full px-2 text-xs font-medium outline-none focus-visible:ring-3 focus-visible:ring-ring-soft',
        mode === 'SAFE_SHUTDOWN_EXIT' ? 'bg-destructive-soft text-destructive-text' : 'bg-warning-soft text-warning-text',
      )}
    >
      <span aria-hidden="true" className="text-[10px]">{presentation.glyph}</span>
      {presentation.label}
    </Link>
  );
}
