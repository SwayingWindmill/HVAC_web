export type ProtectedScopePurgeReason =
  | 'SITE_CHANGE'
  | 'SESSION_LOSS'
  | 'LOGOUT'
  | 'POLICY_CHANGE'
  | 'DISPOSE';

export type ProtectedScopeResourceKind =
  | 'realtime'
  | 'query-cache'
  | 'selection'
  | 'temporary-state';

export interface ProtectedScopeDraft {
  id: string;
  label: string;
  isDirty(): boolean;
}

export interface ProtectedScopeResource {
  id: string;
  kind: ProtectedScopeResourceKind;
  purge(reason: ProtectedScopePurgeReason): void | Promise<void>;
}

export interface ProtectedScopeFailure {
  code: 'PROTECTED_SCOPE_PURGE_FAILED';
  detail: string;
  retryable: false;
}

export interface ProtectedScopeSnapshot {
  state: 'idle' | 'purging' | 'failed';
  siteId?: string;
  generation: number;
  draftCount: number;
  resourceCount: number;
  failure?: ProtectedScopeFailure;
}

export interface DirtyProtectedScopeDraft {
  id: string;
  label: string;
}

export type ProtectedScopePurgeOutcome =
  | { status: 'completed' }
  | { status: 'busy' }
  | { status: 'failed'; failure: ProtectedScopeFailure };

export interface ProtectedScopeRequestToken {
  readonly siteId: string;
  readonly generation: number;
  readonly signal: AbortSignal;
  commit(commit: () => void): boolean;
}

export interface ProtectedScopeCoordinator {
  current(): ProtectedScopeSnapshot;
  activate(siteId: string): void;
  registerDraft(draft: ProtectedScopeDraft): () => void;
  registerResource(resource: ProtectedScopeResource): () => void;
  dirtyDrafts(): readonly DirtyProtectedScopeDraft[];
  requestToken(): ProtectedScopeRequestToken;
  purge(reason: ProtectedScopePurgeReason): Promise<ProtectedScopePurgeOutcome>;
}

const RESOURCE_PURGE_ORDER: readonly ProtectedScopeResourceKind[] = [
  'realtime',
  'query-cache',
  'selection',
  'temporary-state',
];

const PURGE_FAILURE: ProtectedScopeFailure = Object.freeze({
  code: 'PROTECTED_SCOPE_PURGE_FAILED',
  detail: 'One or more protected Site resources could not complete cleanup.',
  retryable: false,
});

class BrowserProtectedScopeCoordinator implements ProtectedScopeCoordinator {
  private state: ProtectedScopeSnapshot['state'] = 'idle';
  private siteId?: string;
  private generation = 0;
  private failure?: ProtectedScopeFailure;
  private requestController?: AbortController;
  private readonly drafts = new Map<string, ProtectedScopeDraft>();
  private readonly resources = new Map<ProtectedScopeResourceKind, Map<string, ProtectedScopeResource>>(
    RESOURCE_PURGE_ORDER.map((kind) => [kind, new Map()]),
  );

  current(): ProtectedScopeSnapshot {
    return {
      state: this.state,
      siteId: this.siteId,
      generation: this.generation,
      draftCount: this.drafts.size,
      resourceCount: this.resourceCount(),
      failure: this.failure,
    };
  }

  activate(siteId: string): void {
    if (!siteId) throw new Error('Protected Site scope requires a Site identity.');
    if (this.state === 'purging') throw new Error('Protected Site scope cannot activate while purge is active.');
    if (this.siteId === siteId && this.requestController && !this.requestController.signal.aborted) {
      this.state = 'idle';
      this.failure = undefined;
      return;
    }
    if (this.siteId && this.siteId !== siteId) {
      throw new Error('Protected Site scope must be purged before activating another Site.');
    }
    this.generation += 1;
    this.siteId = siteId;
    this.state = 'idle';
    this.failure = undefined;
    this.requestController = new AbortController();
  }

  registerDraft(draft: ProtectedScopeDraft): () => void {
    this.requireActiveSite();
    if (!draft.id || !draft.label) throw new Error('Protected draft requires an id and label.');
    if (this.drafts.has(draft.id)) throw new Error(`Protected draft already registered: ${draft.id}`);
    this.drafts.set(draft.id, draft);
    return () => {
      if (this.drafts.get(draft.id) === draft) this.drafts.delete(draft.id);
    };
  }

  registerResource(resource: ProtectedScopeResource): () => void {
    this.requireActiveSite();
    if (!resource.id) throw new Error('Protected resource requires an id.');
    const resources = this.resources.get(resource.kind);
    if (!resources) throw new Error(`Unsupported protected resource kind: ${resource.kind}`);
    if (resources.has(resource.id)) throw new Error(`Protected resource already registered: ${resource.id}`);
    resources.set(resource.id, resource);
    return () => {
      if (resources.get(resource.id) === resource) resources.delete(resource.id);
    };
  }

  dirtyDrafts(): readonly DirtyProtectedScopeDraft[] {
    const dirty: DirtyProtectedScopeDraft[] = [];
    for (const draft of this.drafts.values()) {
      if (draft.isDirty()) dirty.push(Object.freeze({ id: draft.id, label: draft.label }));
    }
    return Object.freeze(dirty);
  }

  requestToken(): ProtectedScopeRequestToken {
    this.requireActiveSite();
    const siteId = this.siteId!;
    const generation = this.generation;
    const signal = this.requestController!.signal;
    return Object.freeze({
      siteId,
      generation,
      signal,
      commit: (commit: () => void): boolean => {
        if (
          signal.aborted
          || this.state !== 'idle'
          || this.siteId !== siteId
          || this.generation !== generation
        ) {
          return false;
        }
        commit();
        return true;
      },
    });
  }

  async purge(reason: ProtectedScopePurgeReason): Promise<ProtectedScopePurgeOutcome> {
    if (this.state === 'purging') return { status: 'busy' };

    const resources = RESOURCE_PURGE_ORDER.flatMap((kind) => [
      ...(this.resources.get(kind)?.values() ?? []),
    ]);

    this.state = 'purging';
    this.failure = undefined;
    this.siteId = undefined;
    this.generation += 1;
    this.requestController?.abort();
    this.requestController = undefined;
    this.drafts.clear();
    for (const registered of this.resources.values()) registered.clear();

    let failed = false;
    for (const resource of resources) {
      try {
        await resource.purge(reason);
      } catch {
        failed = true;
      }
    }

    if (failed) {
      this.state = 'failed';
      this.failure = PURGE_FAILURE;
      return { status: 'failed', failure: PURGE_FAILURE };
    }

    this.state = 'idle';
    return { status: 'completed' };
  }

  private requireActiveSite(): void {
    if (
      this.state !== 'idle'
      || !this.siteId
      || !this.requestController
      || this.requestController.signal.aborted
    ) {
      throw new Error('Protected Site scope is not active.');
    }
  }

  private resourceCount(): number {
    let count = 0;
    for (const resources of this.resources.values()) count += resources.size;
    return count;
  }
}

export function createProtectedScopeCoordinator(): ProtectedScopeCoordinator {
  return new BrowserProtectedScopeCoordinator();
}
