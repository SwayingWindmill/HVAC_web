import { useState, type ReactNode } from 'react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Spinner } from '@/components/ui/spinner';

export interface TimeField {
  readonly id: string;
  readonly label: string;
  readonly initial: string;
}

/**
 * A lifecycle step that takes one or two Site-local times: implementing freezes a baseline, a refit
 * replaces it, and ending stops verification, so each asks before it acts.
 */
export function MeasureTimeDialog({
  open,
  onOpenChange,
  title,
  description,
  fields,
  acknowledgement,
  validate,
  confirmLabel,
  pending,
  error,
  onConfirm,
}: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly title: string;
  readonly description: ReactNode;
  readonly fields: readonly TimeField[];
  readonly acknowledgement?: string;
  readonly validate?: (values: Record<string, string>) => string | null;
  readonly confirmLabel: string;
  readonly pending: boolean;
  readonly error: string | null;
  readonly onConfirm: (values: Record<string, string>) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [acknowledged, setAcknowledged] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);
  const value = (field: TimeField) => values[field.id] ?? field.initial;
  const complete = fields.every((field) => value(field) !== '');
  const ready = complete && (!acknowledgement || acknowledged);
  const shownError = localError ?? error;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && pending) return;
        if (!next) {
          setValues({});
          setAcknowledged(false);
          setLocalError(null);
        }
        onOpenChange(next);
      }}
    >
      <DialogContent
        showCloseButton={!pending}
        onEscapeKeyDown={(event) => {
          if (pending) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (pending) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (!ready || pending) return;
            const resolved = Object.fromEntries(fields.map((field) => [field.id, value(field)]));
            const validationError = validate?.(resolved) ?? null;
            if (validationError) {
              setLocalError(validationError);
              return;
            }
            setLocalError(null);
            onConfirm(resolved);
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            {fields.map((field) => (
              <div key={field.id} className="space-y-1.5">
                <Label htmlFor={`measure-${field.id}`}>{field.label}</Label>
                <Input
                  id={`measure-${field.id}`}
                  type="datetime-local"
                  disabled={pending}
                  className="tabular-nums"
                  value={value(field)}
                  onChange={(event) => {
                    setLocalError(null);
                    setValues((previous) => ({ ...previous, [field.id]: event.target.value }));
                  }}
                />
              </div>
            ))}
          </div>
          {acknowledgement ? (
            <div className="flex items-start gap-2 rounded-lg border border-divider bg-muted/40 p-3">
              <Checkbox
                id="measure-confirm-ack"
                checked={acknowledged}
                disabled={pending}
                onCheckedChange={(checked) => setAcknowledged(checked === true)}
                className="mt-0.5"
              />
              <Label htmlFor="measure-confirm-ack" className="text-sm font-normal">
                {acknowledgement}
              </Label>
            </div>
          ) : null}
          {shownError ? <p role="alert" className="text-sm text-destructive-text">{shownError}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={pending} onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" disabled={pending || !ready}>
              {pending ? <Spinner data-icon="inline-start" /> : null}
              {confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
