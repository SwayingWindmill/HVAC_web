import { useCallback, useRef, useState } from 'react';
import { useBlocker } from '@tanstack/react-router';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';

export interface UnsavedDraft {
  id: string;
  label: string;
  isDirty(): boolean;
}

export type RegisterUnsavedDraft = (draft: UnsavedDraft) => () => void;

// System management is platform-scoped: no Site scope is active, so its forms register their drafts
// with the page. Leaving the page, switching its tab (which unmounts the form) or closing the browser
// asks before unsaved work is dropped.
export function useUnsavedDraftGuard() {
  const drafts = useRef(new Map<string, UnsavedDraft>());
  const [blockedLabels, setBlockedLabels] = useState<readonly string[]>([]);

  const dirtyLabels = useCallback(
    () => Array.from(drafts.current.values()).filter((draft) => draft.isDirty()).map((draft) => draft.label),
    [],
  );

  const registerUnsavedDraft = useCallback<RegisterUnsavedDraft>((draft) => {
    if (drafts.current.has(draft.id)) throw new Error(`Unsaved draft already registered: ${draft.id}`);
    drafts.current.set(draft.id, draft);
    return () => {
      if (drafts.current.get(draft.id) === draft) drafts.current.delete(draft.id);
    };
  }, []);

  const blocker = useBlocker({
    shouldBlockFn: () => {
      const labels = dirtyLabels();
      if (labels.length === 0) return false;
      setBlockedLabels(labels);
      return true;
    },
    enableBeforeUnload: () => dirtyLabels().length > 0,
    withResolver: true,
  });

  const confirmation = blocker.status === 'blocked' ? (
    <AlertDialog open onOpenChange={(open) => { if (!open) blocker.reset(); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>离开会丢弃未保存内容</AlertDialogTitle>
          <AlertDialogDescription>以下内容尚未保存，离开后不能恢复。</AlertDialogDescription>
        </AlertDialogHeader>
        <ul className="space-y-1 rounded-md border bg-muted/30 px-3 py-2.5 text-sm text-muted-foreground">
          {blockedLabels.map((label) => <li key={label}>• {label}</li>)}
        </ul>
        <AlertDialogFooter>
          <AlertDialogCancel>继续编辑</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={(event) => { event.preventDefault(); blocker.proceed(); }}>丢弃并离开</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  ) : null;

  return { registerUnsavedDraft, confirmation };
}
