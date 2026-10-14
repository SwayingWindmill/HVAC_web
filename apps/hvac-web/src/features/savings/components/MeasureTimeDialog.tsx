import { useState, type ReactNode } from 'react';
import { Button } from '@/components/ui/button';
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
export function MeasureTimeDialog({ open, onOpenChange, title, description, fields, confirmLabel, pending, error, onConfirm }: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly title: string;
  readonly description: ReactNode;
  readonly fields: readonly TimeField[];
  readonly confirmLabel: string;
  readonly pending: boolean;
  readonly error: string | null;
  readonly onConfirm: (values: Record<string, string>) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const value = (field: TimeField) => values[field.id] ?? field.initial;
  const complete = fields.every((field) => value(field) !== '');
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) setValues({}); onOpenChange(next); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            onConfirm(Object.fromEntries(fields.map((field) => [field.id, value(field)])));
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            {fields.map((field) => (
              <div key={field.id} className="space-y-1.5">
                <Label htmlFor={`measure-${field.id}`}>{field.label}</Label>
                <Input
                  id={`measure-${field.id}`}
                  type="datetime-local"
                  className="tabular-nums"
                  value={value(field)}
                  onChange={(event) => setValues((previous) => ({ ...previous, [field.id]: event.target.value }))}
                />
              </div>
            ))}
          </div>
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" disabled={pending || !complete}>
              {pending ? <Spinner data-icon="inline-start" /> : null}
              {confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
