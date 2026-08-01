import { useCallback, useEffect, useMemo, useState } from 'react';
import { CopilotKit, useAgent } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  advanceSiteNightEnergyInvestigation,
  getSiteNightEnergyInvestigation,
  listSiteNightEnergyInvestigations,
  OperationsApiError,
  startSiteNightEnergyInvestigation,
  type OperationsAnalysisReference,
  type OperationsEvidence,
  type OperationsFinding,
  type OperationsInvestigationStateSnapshot,
  type OperationsInvestigationSummary,
  type OperationsRequiredNext,
  type OperationsToolReceipt,
} from '@/api/operations';
import {
  OperationsInvestigationAgent,
  type OperationsInvestigationConnectionState,
} from './operations/OperationsInvestigationAgent';
import type { ProtectedScopeResource } from './protected-scope';
import './operations-investigation.css';

interface OperationsInvestigationProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

function investigationFromLocation(): string {
  return new URLSearchParams(window.location.search).get('investigation')?.trim() ?? '';
}

function setInvestigationLocation(investigationId: string): void {
  const url = new URL(window.location.href);
  if (investigationId) url.searchParams.set('investigation', investigationId);
  else url.searchParams.delete('investigation');
  const nextLocation = `${url.pathname}${url.search}${url.hash}`;
  const currentLocation = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (nextLocation !== currentLocation) window.history.pushState(null, '', nextLocation);
}

function formatTimestamp(value: number, timezone: string): string {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      dateStyle: 'medium',
      timeStyle: 'medium',
    }).format(new Date(value));
  } catch {
    return new Date(value).toISOString();
  }
}

function statusLabel(status: OperationsInvestigationSummary['status'], outcome: OperationsInvestigationSummary['outcome']): string {
  if (status === 'COMPLETED' && outcome === 'UNABLE_TO_CONCLUDE') return 'UNABLE TO CONCLUDE';
  if (status === 'COMPLETED' && outcome === 'SUPPORTED_SITE_FINDING') return 'COMPLETED · SITE FINDING';
  return status;
}

function OperationsAgentRunner({ runRevision, onFailure }: {
  readonly runRevision: number;
  readonly onFailure: (error: Error) => void;
}) {
  const { agent } = useAgent({ throttleMs: 40 });
  useEffect(() => {
    let mounted = true;
    void agent.runAgent().catch((error: unknown) => {
      if (mounted) onFailure(error instanceof Error ? error : new Error(String(error)));
    });
    return () => {
      mounted = false;
      agent.abortRun();
    };
  }, [agent, onFailure, runRevision]);
  return null;
}

function SummaryList({
  investigations,
  selectedId,
  timezone,
  onOpen,
}: {
  readonly investigations: readonly OperationsInvestigationSummary[];
  readonly selectedId: string;
  readonly timezone: string;
  readonly onOpen: (investigationId: string) => void;
}) {
  return (
    <nav className="operations-list" aria-labelledby="operations-list-title">
      <div className="operations-section-heading">
        <div>
          <p className="real-shell-eyebrow">SITE-SCOPED INDEX</p>
          <h2 id="operations-list-title">Investigations</h2>
        </div>
        <strong>{investigations.length}</strong>
      </div>
      {investigations.length === 0 ? (
        <p className="operations-muted">当前 Site 尚无可见 Investigation。</p>
      ) : (
        <ul>
          {investigations.map((investigation) => (
            <li key={investigation.id}>
              <button
                type="button"
                className="operations-list-item"
                data-status={investigation.status}
                data-outcome={investigation.outcome ?? 'NONE'}
                aria-current={selectedId === investigation.id ? 'page' : undefined}
                onClick={() => onOpen(investigation.id)}
              >
                <span className="operations-list-item-topline">
                  <strong>{statusLabel(investigation.status, investigation.outcome)}</strong>
                  <span>r{investigation.revision}</span>
                </span>
                <span className="operations-list-id">{investigation.id}</span>
                <span>{formatTimestamp(investigation.createdAt, timezone)}</span>
                <span className="operations-list-counts">
                  E {investigation.evidenceCount} · A {investigation.analysisReferenceCount} · F {investigation.findingCount} · T {investigation.toolReceiptCount}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </nav>
  );
}

function SourceProvenance({ source, timezone }: {
  readonly source: OperationsEvidence['sources'][number];
  readonly timezone: string;
}) {
  return (
    <li className="operations-source">
      <div className="operations-card-heading">
        <strong>{source.owner}</strong>
        <span data-quality={source.quality.classification}>{source.quality.classification}</span>
      </div>
      <dl className="operations-provenance">
        <div><dt>Request</dt><dd>{source.requestId}</dd></div>
        <div><dt>Registry revision</dt><dd>{source.registryRevision ?? '—'}</dd></div>
        <div><dt>Dataset revision</dt><dd>{source.datasetRevision ?? '—'}</dd></div>
        <div><dt>Data watermark</dt><dd>{source.watermark.data ?? '—'}</dd></div>
        <div><dt>Aggregate watermark</dt><dd>{source.watermark.aggregate ?? '—'}</dd></div>
        <div><dt>Partial</dt><dd>{source.partial ? 'YES' : 'NO'}</dd></div>
        <div>
          <dt>Quality buckets</dt>
          <dd>{source.quality.valid} valid · {source.quality.suspect} suspect · {source.quality.invalid} invalid</dd>
        </div>
        <div><dt>Captured</dt><dd>{formatTimestamp(source.capturedAt, timezone)}</dd></div>
        <div><dt>Evaluated</dt><dd>{formatTimestamp(source.evaluatedAt, timezone)}</dd></div>
        <div><dt>Provenance digest</dt><dd className="operations-digest">{source.provenanceDigest}</dd></div>
      </dl>
    </li>
  );
}

function EvidenceCard({ record, timezone }: {
  readonly record: OperationsEvidence;
  readonly timezone: string;
}) {
  return (
    <article className="operations-record-card" data-record-type="EVIDENCE">
      <div className="operations-card-heading">
        <div>
          <p className="real-shell-eyebrow">{record.classification}</p>
          <h4>{record.evidenceKind}</h4>
        </div>
        <span>{formatTimestamp(record.recordedAt, timezone)}</span>
      </div>
      <p>{record.statement}</p>
      {record.analysisReferenceDigest ? (
        <p className="operations-digest"><strong>Analysis digest:</strong> {record.analysisReferenceDigest}</p>
      ) : null}
      <details>
        <summary>Provenance sources ({record.sources.length})</summary>
        <ul className="operations-source-list">
          {record.sources.map((source) => (
            <SourceProvenance key={`${source.owner}:${source.requestId}`} source={source} timezone={timezone} />
          ))}
        </ul>
      </details>
    </article>
  );
}

function AnalysisCard({ record, timezone }: {
  readonly record: OperationsAnalysisReference;
  readonly timezone: string;
}) {
  return (
    <article className="operations-record-card" data-record-type="ANALYSIS_REFERENCE">
      <div className="operations-card-heading">
        <div>
          <p className="real-shell-eyebrow">{record.authority}</p>
          <h4>{record.analysisKind}</h4>
        </div>
        <strong>{record.outcome}</strong>
      </div>
      <dl className="operations-provenance">
        <div><dt>Algorithm</dt><dd>{record.algorithmVersion}</dd></div>
        <div><dt>Policy</dt><dd>{record.policyVersion}</dd></div>
        <div><dt>Executed</dt><dd>{formatTimestamp(record.executedAt, timezone)}</dd></div>
        <div><dt>Input Evidence</dt><dd>{record.inputEvidenceIds.join(', ')}</dd></div>
        <div><dt>Parameter digest</dt><dd className="operations-digest">{record.parameterDigest}</dd></div>
        <div><dt>Result digest</dt><dd className="operations-digest">{record.resultDigest}</dd></div>
      </dl>
    </article>
  );
}

function RequiredNextCard({ requirement }: { readonly requirement: OperationsRequiredNext }) {
  return (
    <li className="operations-required-next-card">
      <div className="operations-card-heading">
        <div>
          <p className="real-shell-eyebrow">REQUIRED NEXT</p>
          <h5>{requirement.kind}</h5>
        </div>
        <strong>{requirement.owner}</strong>
      </div>
      <p><strong>Capability:</strong> {requirement.capability}</p>
      <p><strong>Equipment:</strong> {requirement.equipmentIds.length === 0 ? 'Site equipment set' : requirement.equipmentIds.join(', ')}</p>
      <dl className="operations-provenance">
        <div><dt>Target period</dt><dd>{requirement.targetPeriod.from} → {requirement.targetPeriod.to} ({requirement.targetPeriod.expectedBuckets} buckets)</dd></div>
        <div><dt>Baseline period</dt><dd>{requirement.baselinePeriod.from} → {requirement.baselinePeriod.to} ({requirement.baselinePeriod.expectedBuckets} buckets)</dd></div>
        <div><dt>Required metadata</dt><dd>{requirement.requiredMetadata.join(', ')}</dd></div>
      </dl>
    </li>
  );
}

function FindingCard({ record, timezone }: {
  readonly record: OperationsFinding;
  readonly timezone: string;
}) {
  const requiredNext = record.conclusion.status === 'UNABLE_TO_CONCLUDE'
    ? record.conclusion.requiredNext
    : undefined;
  return (
    <article
      className="operations-record-card operations-finding-card"
      data-record-type="FINDING"
      data-conclusion={record.conclusion.status}
    >
      <div className="operations-card-heading">
        <div>
          <p className="real-shell-eyebrow">{record.classification}</p>
          <h4>{record.findingKind}</h4>
        </div>
        <strong>{record.conclusion.status}</strong>
      </div>
      <p>{record.statement}</p>
      {record.conclusion.status === 'SUPPORTED' ? (
        <div className="operations-site-boundary" role="note">
          <strong>Site-only conclusion</strong>
          <span>该 Finding 的权威范围仅为 Site，不构成 Equipment root cause。</span>
        </div>
      ) : (
        <div className="operations-blocker" role="status">
          <strong>{record.conclusion.reasonCode}</strong>
          <span>{record.conclusion.detail}</span>
        </div>
      )}
      <dl className="operations-provenance">
        <div><dt>Recorded</dt><dd>{formatTimestamp(record.recordedAt, timezone)}</dd></div>
        <div><dt>Evidence</dt><dd>{record.evidenceIds.join(', ') || '—'}</dd></div>
        <div><dt>Analysis references</dt><dd>{record.analysisReferenceIds.join(', ') || '—'}</dd></div>
      </dl>
      {requiredNext?.length ? (
        <section aria-labelledby={`required-next-${record.id}`}>
          <h5 id={`required-next-${record.id}`}>Required next evidence</h5>
          <ul className="operations-required-next">
            {requiredNext.map((requirement) => (
              <RequiredNextCard key={`${requirement.owner}:${requirement.kind}`} requirement={requirement} />
            ))}
          </ul>
        </section>
      ) : null}
    </article>
  );
}

function ToolReceiptCard({ receipt, timezone }: {
  readonly receipt: OperationsToolReceipt;
  readonly timezone: string;
}) {
  const metadata = Object.entries(receipt.metadata).sort(([left], [right]) => left.localeCompare(right));
  return (
    <li className="operations-tool-receipt">
      <div className="operations-card-heading">
        <div>
          <strong>{receipt.logicalTool}</strong>
          <span>{receipt.owner}</span>
        </div>
        <strong>{receipt.resultCategory}</strong>
      </div>
      <dl className="operations-provenance">
        <div><dt>Request</dt><dd>{receipt.requestId}</dd></div>
        <div><dt>Run / Step</dt><dd>{receipt.runId} / {receipt.stepId}</dd></div>
        <div><dt>Started</dt><dd>{formatTimestamp(receipt.startedAt, timezone)}</dd></div>
        <div><dt>Completed</dt><dd>{formatTimestamp(receipt.completedAt, timezone)}</dd></div>
        {metadata.map(([key, value]) => (
          <div key={key}><dt>{key}</dt><dd>{value === null ? 'null' : String(value)}</dd></div>
        ))}
      </dl>
    </li>
  );
}

function SnapshotWorkspace({
  snapshot,
  toolReceipts,
  timezone,
}: {
  readonly snapshot: OperationsInvestigationStateSnapshot;
  readonly toolReceipts: readonly OperationsToolReceipt[];
  readonly timezone: string;
}) {
  const investigation = snapshot.investigation;
  return (
    <article
      className="operations-workspace"
      data-investigation-status={investigation.status}
      data-investigation-outcome={investigation.outcome ?? 'NONE'}
      aria-labelledby="operations-plan-title"
    >
      <section className="operations-summary">
        <div>
          <p className="real-shell-eyebrow">COMMITTED INVESTIGATION VIEW</p>
          <h2 id="operations-plan-title">{snapshot.plan.label}</h2>
          <p>
            Revision {investigation.revision} · {statusLabel(investigation.status, investigation.outcome)}
          </p>
        </div>
        <div className="operations-progress" role="status" aria-live="polite">
          <progress max={100} value={snapshot.plan.progressPercent} aria-label="Investigation progress" />
          <strong>{snapshot.plan.progressPercent}%</strong>
        </div>
      </section>

      <section aria-labelledby="operations-plan-steps-title">
        <h3 id="operations-plan-steps-title">Authoritative plan</h3>
        <ol className="operations-plan">
          {snapshot.plan.steps.map((step) => (
            <li key={step.id} data-step-status={step.status}>
              <span>{step.label}</span>
              <strong>{step.status}</strong>
            </li>
          ))}
        </ol>
      </section>

      <section aria-labelledby="operations-evidence-title">
        <div className="operations-section-heading">
          <h3 id="operations-evidence-title">Evidence</h3>
          <strong>{investigation.evidence.length}</strong>
        </div>
        {investigation.evidence.length === 0 ? <p className="operations-muted">尚无已提交 Evidence。</p> : (
          <div className="operations-card-list">
            {investigation.evidence.map((record) => (
              <EvidenceCard key={record.id} record={record} timezone={timezone} />
            ))}
          </div>
        )}
      </section>

      <section aria-labelledby="operations-analysis-title">
        <div className="operations-section-heading">
          <h3 id="operations-analysis-title">Analysis References</h3>
          <strong>{investigation.analysisReferences.length}</strong>
        </div>
        {investigation.analysisReferences.length === 0 ? <p className="operations-muted">尚无已提交 Analysis Reference。</p> : (
          <div className="operations-card-list">
            {investigation.analysisReferences.map((record) => (
              <AnalysisCard key={record.id} record={record} timezone={timezone} />
            ))}
          </div>
        )}
      </section>

      <section aria-labelledby="operations-findings-title">
        <div className="operations-section-heading">
          <h3 id="operations-findings-title">Findings</h3>
          <strong>{investigation.findings.length}</strong>
        </div>
        {investigation.findings.length === 0 ? <p className="operations-muted">尚无已提交 Finding。</p> : (
          <div className="operations-card-list">
            {investigation.findings.map((record) => (
              <FindingCard key={record.id} record={record} timezone={timezone} />
            ))}
          </div>
        )}
      </section>

      <section aria-labelledby="operations-tools-title">
        <div className="operations-section-heading">
          <div>
            <h3 id="operations-tools-title">Read-only Tool Receipts</h3>
            <p className="operations-muted">仅显示已提交 receipt 元数据，不展示 raw Tool payload。</p>
          </div>
          <strong>{toolReceipts.length}</strong>
        </div>
        {toolReceipts.length === 0 ? <p className="operations-muted">当前 revision 没有已提交 Tool Receipt。</p> : (
          <ul className="operations-tools">
            {toolReceipts.map((receipt) => (
              <ToolReceiptCard key={receipt.id} receipt={receipt} timezone={timezone} />
            ))}
          </ul>
        )}
      </section>
    </article>
  );
}

export function OperationsInvestigation({
  site,
  principal,
  registerProtectedResource,
}: OperationsInvestigationProps) {
  const [investigationId, setInvestigationId] = useState(investigationFromLocation);
  const [openValue, setOpenValue] = useState(investigationFromLocation);
  const [snapshot, setSnapshot] = useState<OperationsInvestigationStateSnapshot | null>(null);
  const [toolReceipts, setToolReceipts] = useState<readonly OperationsToolReceipt[]>([]);
  const [investigations, setInvestigations] = useState<readonly OperationsInvestigationSummary[]>([]);
  const [listRevision, setListRevision] = useState(0);
  const [listLoading, setListLoading] = useState(true);
  const [listFailure, setListFailure] = useState<Error | null>(null);
  const [runRevision, setRunRevision] = useState(0);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Error | null>(null);
  const [connection, setConnection] = useState<OperationsInvestigationConnectionState | null>(null);

  const requestOptions = useMemo(() => ({
    trustedOrganizationId: principal.context.actingOrganizationId,
    trustedSiteId: site.id,
  }), [principal.context.actingOrganizationId, site.id]);

  const openInvestigation = useCallback((nextId: string) => {
    const next = nextId.trim();
    if (!next) return;
    setInvestigationLocation(next);
    setInvestigationId(next);
    setOpenValue(next);
    setSnapshot(null);
    setToolReceipts([]);
    setFailure(null);
    setConnection(null);
    setRunRevision((value) => value + 1);
  }, []);

  useEffect(() => {
    const onPopState = () => {
      const next = investigationFromLocation();
      setInvestigationId(next);
      setOpenValue(next);
      setSnapshot(null);
      setToolReceipts([]);
      setFailure(null);
      setConnection(null);
      setRunRevision((value) => value + 1);
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setListLoading(true);
    setListFailure(null);
    void listSiteNightEnergyInvestigations({
      ...requestOptions,
      signal: controller.signal,
    }).then((result) => {
      setInvestigations(result.investigations);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) {
        setListFailure(error instanceof Error ? error : new Error(String(error)));
      }
    }).finally(() => {
      if (!controller.signal.aborted) setListLoading(false);
    });
    return () => controller.abort();
  }, [listRevision, requestOptions]);

  const agent = useMemo(() => investigationId ? new OperationsInvestigationAgent({
    organizationId: requestOptions.trustedOrganizationId,
    siteId: requestOptions.trustedSiteId,
    investigationId,
    onSnapshot: (next) => {
      setSnapshot(next);
      setFailure(null);
    },
    onConnectionState: setConnection,
  }) : null, [investigationId, requestOptions.trustedOrganizationId, requestOptions.trustedSiteId, runRevision]);

  const snapshotInvestigationId = snapshot?.investigation.id ?? '';
  const snapshotRevision = snapshot?.investigation.revision ?? -1;
  const snapshotStatus = snapshot?.investigation.status ?? null;
  const snapshotOutcome = snapshot?.investigation.outcome ?? null;

  useEffect(() => {
    if (!snapshotInvestigationId || snapshotRevision < 0 || !investigationId) return undefined;
    const controller = new AbortController();
    setToolReceipts([]);
    void getSiteNightEnergyInvestigation(investigationId, {
      ...requestOptions,
      signal: controller.signal,
    }).then((detail) => {
      if (detail.id === snapshotInvestigationId && detail.revision === snapshotRevision) {
        setToolReceipts(detail.toolReceipts);
      }
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) {
        setFailure(error instanceof Error ? error : new Error(String(error)));
      }
    });
    return () => controller.abort();
  }, [investigationId, requestOptions, snapshotInvestigationId, snapshotRevision]);

  useEffect(() => {
    if (snapshotRevision >= 0) setListRevision((value) => value + 1);
  }, [snapshotOutcome, snapshotRevision, snapshotStatus]);

  useEffect(() => {
    if (!investigationId || !agent) return undefined;
    return registerProtectedResource({
      id: `operations-investigation:${site.id}:${investigationId}`,
      kind: 'temporary-state',
      purge: () => {
        agent.abortRun();
        setSnapshot(null);
        setToolReceipts([]);
        setFailure(null);
        setConnection(null);
      },
    });
  }, [agent, investigationId, registerProtectedResource, site.id]);

  const start = async () => {
    setBusy(true);
    setFailure(null);
    setConnection(null);
    try {
      const created = await startSiteNightEnergyInvestigation(requestOptions);
      openInvestigation(created.id);
      setListRevision((value) => value + 1);
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(String(error)));
    } finally {
      setBusy(false);
    }
  };

  const advance = async () => {
    if (!investigationId) return;
    setBusy(true);
    setFailure(null);
    setConnection(null);
    try {
      await advanceSiteNightEnergyInvestigation(investigationId, requestOptions);
      setSnapshot(null);
      setToolReceipts([]);
      setRunRevision((value) => value + 1);
      setListRevision((value) => value + 1);
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(String(error)));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="operations-investigation" data-testid="real-site-route-operations">
      <header className="operations-header">
        <div>
          <p className="real-shell-eyebrow">REAL MODE · SITE OPERATIONS</p>
          <h1>Operations Investigations</h1>
          <p>Site {site.displayName} · 列表、Plan 和领域记录均来自已提交权威 projection。</p>
        </div>
        <button type="button" onClick={() => { void start(); }} disabled={busy}>
          {busy ? '处理中…' : '新建夜间能耗调查'}
        </button>
      </header>

      <section className="operations-open" aria-label="Open Investigation by identity">
        <label htmlFor="operations-investigation-id">Investigation ID</label>
        <input
          id="operations-investigation-id"
          value={openValue}
          onChange={(event) => setOpenValue(event.target.value)}
          maxLength={256}
          placeholder="输入已授权 Investigation ID"
        />
        <button type="button" onClick={() => openInvestigation(openValue)} disabled={!openValue.trim() || busy}>打开</button>
        <button type="button" onClick={() => { void advance(); }} disabled={!investigationId || busy}>推进</button>
      </section>

      {connection ? (
        <div
          className="operations-connection"
          data-connection-status={connection.status}
          role="status"
          aria-live="polite"
        >
          <strong>{connection.status}</strong>
          <span>
            {connection.status === 'RETRYING'
              ? '连接中断，正在重新授权并从权威位置恢复；不会触发新的业务写入。'
              : connection.status === 'LIVE'
                ? `${connection.recovery?.mode ?? 'FULL_SNAPSHOT'} · ${connection.recovery?.latestPosition ?? 'snapshot pending'}`
                : connection.status === 'TERMINAL'
                  ? 'Investigation 已进入稳定终态。'
                  : '正在读取当前授权下的权威 Investigation snapshot。'}
          </span>
        </div>
      ) : null}

      {failure ? (
        <div className="real-shell-problem" role="alert">
          <strong>
            {failure instanceof OperationsApiError && failure.status === 404
              ? 'Investigation not visible'
              : 'Operations Investigation unavailable'}
          </strong>
          <span>{failure.message}</span>
        </div>
      ) : null}

      <div className="operations-layout">
        <aside>
          {listLoading ? (
            <div className="real-shell-progress" role="status" aria-live="polite">正在读取 Site Investigation 列表…</div>
          ) : listFailure ? (
            <div className="real-shell-problem" role="alert">
              <strong>Investigation list unavailable</strong>
              <span>{listFailure.message}</span>
            </div>
          ) : (
            <SummaryList
              investigations={investigations}
              selectedId={investigationId}
              timezone={site.timezone}
              onOpen={openInvestigation}
            />
          )}
        </aside>

        <section className="operations-detail" aria-label="Investigation detail">
          {!investigationId ? (
            <section className="operations-empty">
              <h2>尚未选择 Investigation</h2>
              <p>从 Site 列表打开一个可见 Investigation，或新建夜间能耗调查。</p>
            </section>
          ) : null}

          {agent ? (
            <CopilotKit
              key={`${investigationId}:${runRevision}`}
              selfManagedAgents={{ operations: agent }}
              agent="operations"
              enableInspector={false}
              showDevConsole={false}
            >
              <OperationsAgentRunner runRevision={runRevision} onFailure={setFailure} />
              {snapshot ? (
                <SnapshotWorkspace
                  snapshot={snapshot}
                  toolReceipts={toolReceipts}
                  timezone={site.timezone}
                />
              ) : (
                <div className="real-shell-progress" role="status" aria-live="polite">
                  正在读取已提交 Investigation 事件…
                </div>
              )}
            </CopilotKit>
          ) : null}
        </section>
      </div>
    </div>
  );
}
