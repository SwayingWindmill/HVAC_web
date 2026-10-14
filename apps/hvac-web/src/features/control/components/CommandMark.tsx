import type { CommandStatus } from '../command-api';

/**
 * A command's state by shape: ● executed, ▲ red when it failed or was refused, ▲ amber while it
 * waits on a person, ◐ while it moves toward the plant, ○ when it lapsed or was withdrawn.
 */
export function CommandMark({ status }: { readonly status: CommandStatus }) {
  const settledOk = status === 'SUCCEEDED';
  const failed = status === 'FAILED' || status === 'REJECTED';
  const waiting = status === 'AWAITING_APPROVAL' || status === 'OUTCOME_UNKNOWN';
  const lapsed = status === 'CANCELLED' || status === 'EXPIRED';
  return (
    <svg viewBox="0 0 10 10" className="size-2.5 shrink-0" aria-hidden="true">
      {settledOk ? <circle cx="5" cy="5" r="5" className="fill-success" />
        : failed ? <path d="M5 0.5 9.5 9H0.5z" className="fill-destructive" />
        : waiting ? <path d="M5 0.5 9.5 9H0.5z" className="fill-warning" />
        : lapsed ? <circle cx="5" cy="5" r="3.8" fill="none" strokeWidth="1.5" className="stroke-muted-foreground" />
        : (
          <>
            <circle cx="5" cy="5" r="3.8" fill="none" strokeWidth="1.5" className="stroke-muted-foreground" />
            <path d="M5 1.2a3.8 3.8 0 0 1 0 7.6z" className="fill-muted-foreground" />
          </>
        )}
    </svg>
  );
}
