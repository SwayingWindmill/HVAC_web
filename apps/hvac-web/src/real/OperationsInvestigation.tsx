import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CopilotKit, useAgent } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import { useLocation, useNavigate } from 'react-router';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  advanceSiteNightEnergyInvestigation,
  cancelSiteNightEnergyInvestigation,
  getSiteNightEnergyInvestigation,
  listSiteNightEnergyInvestigations,
  OperationsApiError,
  startSiteNightEnergyInvestigation,
  submitSiteNightEnergyOperatorInput,
  type OperationsAnalysisReference,
  type OperationsEvidence,
  type OperationsFinding,
  type OperationsInvestigationStateSnapshot,
  type OperationsInvestigationSummary,
  type OperationsOperatorInputAccepted,
  type OperationsOperatorInputRequest,
  type OperationsOperatorInputValues,
  type OperationsRequiredNext,
  type OperationsRunResourceBudget,
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
  readonly embedded?: boolean;
}

function investigationFromLocation(search: string): string {
  return new URLSearchParams(search).get('investigation')?.trim() ?? '';
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
  if (status === 'WAITING_FOR_OPERATOR_INPUT') return 'WAITING · OPERATOR INPUT';
  if (status === 'COMPLETED' && outcome === 'UNABLE_TO_CONCLUDE') return 'UNABLE TO CONCLUDE';
  if (status === 'COMPLETED' && outcome === 'SUPPORTED_SITE_FINDING') return 'COMPLETED · SITE FINDING';
  return status;
}

function isTerminalStatus(status: OperationsInvestigationSummary['status'] | null): boolean {
  return status === 'COMPLETED' || status === 'FAILED' || status === 'CANCELLED';
}

function budgetDimensionLabel(dimension: OperationsRunResourceBudget['exhaustedDimension']): string {
  return ({
    MODEL_INVOCATIONS: 'Model 调用次数',
    TOOL_REQUESTS: 'Tool 请求数',
    WALL_CLOCK_MS: '运行时长',
    QUERY_RANGE_MS: '查询时间范围',
    QUERY_BUCKETS: '查询 buckets',
    OWNER_RECORDS: 'Owner records',
    PAYLOAD_BYTES: 'Payload bytes',
  } as const)[dimension];
}

function createOperatorInputIdempotencyKey(): string {
  const randomIdentity = globalThis.crypto?.randomUUID?.()
    ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `operator-input-${randomIdentity}`.slice(0, 256);
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
                  E {investigation.evidenceCount} · A {investigation.analysisReferenceCount} · F {investigation.findingCount} · T {investigation.toolReceiptCount} · O {investigation.acceptedOperatorInputCount}
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

function OperatorInputPanel({
  request,
  acceptedInputs,
  timezone,
  busy,
  onSubmit,
}: {
  readonly request: OperationsOperatorInputRequest | null;
  readonly acceptedInputs: readonly OperationsOperatorInputAccepted[];
  readonly timezone: string;
  readonly busy: boolean;
  readonly onSubmit: (values: OperationsOperatorInputValues) => Promise<void>;
}) {
  const [analysisScope, setAnalysisScope] = useState<OperationsOperatorInputValues['analysisScope']>('SITE_ONLY');
  const [operatorNote, setOperatorNote] = useState('');

  useEffect(() => {
    setAnalysisScope('SITE_ONLY');
    setOperatorNote('');
  }, [request?.id]);

  if (request === null && acceptedInputs.length === 0) return null;
  return (
    <section className="operations-operator-input" aria-labelledby="operations-operator-input-title">
      <div className="operations-section-heading">
        <div>
          <p className="real-shell-eyebrow">OPERATOR INPUT · COMMITTED INTERRUPT</p>
          <h3 id="operations-operator-input-title">Operator decision</h3>
        </div>
        <strong>{request ? 'ACTION REQUIRED' : `${acceptedInputs.length} ACCEPTED`}</strong>
      </div>

      {request ? (
        <form
          className="operations-operator-input-form"
          onSubmit={(event) => {
            event.preventDefault();
            void onSubmit({
              analysisScope,
              operatorNote: operatorNote.trim() || null,
            });
          }}
        >
          <div className="operations-operator-input-context" role="note">
            <strong>{request.kind}</strong>
            <span>Request {request.id} · Run {request.runId}</span>
            <span>Policy {request.policyVersion} · {formatTimestamp(request.requestedAt, timezone)}</span>
          </div>
          <fieldset disabled={busy}>
            <legend>Analysis authority</legend>
            {request.fields[0].options.map((option) => (
              <label key={option} className="operations-operator-input-choice">
                <input
                  type="radio"
                  name="operations-analysis-scope"
                  value={option}
                  checked={analysisScope === option}
                  onChange={() => setAnalysisScope(option)}
                />
                <span>
                  <strong>{option === 'SITE_ONLY' ? 'Proceed with Site-only authority' : 'Defer the conclusion'}</strong>
                  <small>
                    {option === 'SITE_ONLY'
                      ? '继续同一 Agent Run，但不会把 Site Evidence 提升为 Equipment root cause。'
                      : '保留当前已提交记录并推迟结论。'}
                  </small>
                </span>
              </label>
            ))}
          </fieldset>
          <label className="operations-operator-input-note" htmlFor="operations-operator-note">
            <span>Operator note <small>optional · max {request.fields[1].maximumLength}</small></span>
            <textarea
              id="operations-operator-note"
              value={operatorNote}
              maxLength={request.fields[1].maximumLength}
              rows={4}
              disabled={busy}
              onChange={(event) => setOperatorNote(event.target.value)}
            />
            <small>{operatorNote.length}/{request.fields[1].maximumLength}</small>
          </label>
          <button type="submit" disabled={busy}>
            {busy ? '正在原子提交并恢复…' : '提交 Operator Input 并恢复同一 Run'}
          </button>
        </form>
      ) : null}

      {acceptedInputs.length > 0 ? (
        <div className="operations-operator-input-history">
          <h4>Accepted input history</h4>
          <ul>
            {acceptedInputs.map((record) => (
              <li key={record.id}>
                <div className="operations-card-heading">
                  <strong>{record.values.analysisScope}</strong>
                  <span>{formatTimestamp(record.recordedAt, timezone)}</span>
                </div>
                {record.values.operatorNote ? <p>{record.values.operatorNote}</p> : null}
                <dl className="operations-provenance">
                  <div><dt>Request / Run</dt><dd>{record.requestId} / {record.runId}</dd></div>
                  <div><dt>Decision</dt><dd>{record.provenance.authorizationDecisionId}</dd></div>
                  <div><dt>Policy</dt><dd>{record.provenance.policyRevision}</dd></div>
                  <div><dt>Input digest</dt><dd className="operations-digest">{record.inputDigest}</dd></div>
                </dl>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}

function SnapshotWorkspace({
  snapshot,
  toolReceipts,
  timezone,
  operatorInputBusy,
  onSubmitOperatorInput,
}: {
  readonly snapshot: OperationsInvestigationStateSnapshot;
  readonly toolReceipts: readonly OperationsToolReceipt[];
  readonly timezone: string;
  readonly operatorInputBusy: boolean;
  readonly onSubmitOperatorInput: (values: OperationsOperatorInputValues) => Promise<void>;
}) {
  const investigation = snapshot.investigation;
  return (
    <article
      className="operations-workspace"
      data-investigation-status={investigation.status}
      data-investigation-outcome={investigation.outcome ?? 'NONE'}
      data-resource-budget={investigation.resourceBudget?.exhaustedDimension ?? 'AVAILABLE'}
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

      {investigation.resourceBudget ? (
        <div className="real-shell-problem" role="status" data-testid="operations-resource-budget">
          <strong>Agent Run 资源预算已耗尽</strong>
          <span>
            {budgetDimensionLabel(investigation.resourceBudget.exhaustedDimension)} ·
            {' '}{investigation.resourceBudget.consumed} / {investigation.resourceBudget.limit} ·
            {' '}{investigation.resourceBudget.outcome === 'PARTIAL'
              ? '保留已提交部分结果，不再执行新的外部工作。'
              : '当前证据不足，无法得出结论。'}
          </span>
        </div>
      ) : null}

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

      <OperatorInputPanel
        request={investigation.operatorInputRequest}
        acceptedInputs={investigation.acceptedOperatorInputs}
        timezone={timezone}
        busy={operatorInputBusy}
        onSubmit={onSubmitOperatorInput}
      />

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
  embedded = false,
}: OperationsInvestigationProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const [investigationId, setInvestigationId] = useState(() => investigationFromLocation(location.search));
  const [openValue, setOpenValue] = useState(() => investigationFromLocation(location.search));
  const [snapshot, setSnapshot] = useState<OperationsInvestigationStateSnapshot | null>(null);
  const [toolReceipts, setToolReceipts] = useState<readonly OperationsToolReceipt[]>([]);
  const [investigations, setInvestigations] = useState<readonly OperationsInvestigationSummary[]>([]);
  const [listRevision, setListRevision] = useState(0);
  const [listLoading, setListLoading] = useState(true);
  const [listFailure, setListFailure] = useState<Error | null>(null);
  const [runRevision, setRunRevision] = useState(0);
  const [busy, setBusy] = useState(false);
  const [operatorInputBusy, setOperatorInputBusy] = useState(false);
  const [failure, setFailure] = useState<Error | null>(null);
  const operatorInputIdempotencyKeys = useRef(new Map<string, string>());
  const [connection, setConnection] = useState<OperationsInvestigationConnectionState | null>(null);

  const requestOptions = useMemo(() => ({
    trustedTenantId: principal.context.tenantId,
    trustedSiteId: site.id,
  }), [principal.context.tenantId, site.id]);

  const openInvestigation = useCallback((nextId: string) => {
    const next = nextId.trim();
    if (!next) return;
    const parameters = new URLSearchParams(location.search);
    parameters.set('investigation', next);
    navigate(`${location.pathname}?${parameters.toString()}${location.hash}`);
    setInvestigationId(next);
    setOpenValue(next);
    setSnapshot(null);
    setToolReceipts([]);
    setFailure(null);
    setConnection(null);
    operatorInputIdempotencyKeys.current.clear();
    setRunRevision((value) => value + 1);
  }, [location.hash, location.pathname, location.search, navigate]);

  useEffect(() => {
    const next = investigationFromLocation(location.search);
    setInvestigationId(next);
    setOpenValue(next);
    setSnapshot(null);
    setToolReceipts([]);
    setFailure(null);
    setConnection(null);
    operatorInputIdempotencyKeys.current.clear();
    setRunRevision((value) => value + 1);
  }, [location.search]);

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
    tenantId: requestOptions.trustedTenantId,
    siteId: requestOptions.trustedSiteId,
    investigationId,
    onSnapshot: (next) => {
      setSnapshot(next);
      setFailure(null);
    },
    onConnectionState: setConnection,
  }) : null, [investigationId, requestOptions.trustedTenantId, requestOptions.trustedSiteId, runRevision]);

  const snapshotInvestigationId = snapshot?.investigation.id ?? '';
  const snapshotRevision = snapshot?.investigation.revision ?? -1;
  const snapshotStatus = snapshot?.investigation.status ?? null;
  const snapshotOutcome = snapshot?.investigation.outcome ?? null;
  const snapshotBudget = snapshot?.investigation.resourceBudget ?? null;

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
    const unregister = registerProtectedResource({
      id: `operations-investigation:${site.id}:${investigationId}`,
      kind: 'temporary-state',
      purge: () => {
        agent.abortRun();
        agent.purgeSiteRecoveryPositions();
        setSnapshot(null);
        setToolReceipts([]);
        setFailure(null);
        setConnection(null);
        operatorInputIdempotencyKeys.current.clear();
      },
    });
    return () => {
      unregister();
      agent.purgeSiteRecoveryPositions();
    };
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
    if (!investigationId || snapshotStatus === 'WAITING_FOR_OPERATOR_INPUT' || snapshotBudget !== null) return;
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

  const cancel = async () => {
    if (!investigationId || !snapshotStatus || isTerminalStatus(snapshotStatus)) return;
    setBusy(true);
    setFailure(null);
    setConnection(null);
    try {
      await cancelSiteNightEnergyInvestigation(investigationId, requestOptions);
      setSnapshot(null);
      setToolReceipts([]);
      setRunRevision((value) => value + 1);
      setListRevision((value) => value + 1);
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(String(error)));
      if (error instanceof OperationsApiError && error.status === 409) {
        setSnapshot(null);
        setToolReceipts([]);
        setRunRevision((value) => value + 1);
      }
    } finally {
      setBusy(false);
    }
  };

  const submitOperatorInput = async (values: OperationsOperatorInputValues) => {
    const request = snapshot?.investigation.operatorInputRequest;
    if (!investigationId || !request || snapshot.investigation.status !== 'WAITING_FOR_OPERATOR_INPUT') return;
    const idempotencyKey = operatorInputIdempotencyKeys.current.get(request.id)
      ?? createOperatorInputIdempotencyKey();
    operatorInputIdempotencyKeys.current.set(request.id, idempotencyKey);
    setOperatorInputBusy(true);
    setFailure(null);
    setConnection(null);
    try {
      await submitSiteNightEnergyOperatorInput(investigationId, {
        requestId: request.id,
        expectedRevision: snapshot.investigation.revision,
        idempotencyKey,
        values,
      }, requestOptions);
      operatorInputIdempotencyKeys.current.delete(request.id);
      setSnapshot(null);
      setToolReceipts([]);
      setRunRevision((value) => value + 1);
      setListRevision((value) => value + 1);
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(String(error)));
      if (error instanceof OperationsApiError && error.status === 409) {
        setSnapshot(null);
        setToolReceipts([]);
        setRunRevision((value) => value + 1);
      }
    } finally {
      setOperatorInputBusy(false);
    }
  };

  return (
    <div
      className="operations-investigation"
      data-testid="real-site-route-operations"
      data-primary-agent-experience="true"
    >
      <header className={embedded ? 'operations-header operations-header--embedded' : 'operations-header'}>
        {!embedded ? (
          <div>
            <p className="real-shell-eyebrow">REAL MODE · PRIMARY AGENT EXPERIENCE</p>
            <h1>Operations Workspace</h1>
            <p>Site {site.displayName} · Investigation 列表、Plan 和领域记录均来自已提交权威 projection。</p>
          </div>
        ) : <span />}
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
        <button type="button" onClick={() => openInvestigation(openValue)} disabled={!openValue.trim() || busy || operatorInputBusy}>打开</button>
        <button
          type="button"
          onClick={() => { void advance(); }}
          disabled={!investigationId || busy || operatorInputBusy || snapshotStatus === 'WAITING_FOR_OPERATOR_INPUT' || snapshotBudget !== null || isTerminalStatus(snapshotStatus)}
          title={snapshotStatus === 'WAITING_FOR_OPERATOR_INPUT'
            ? '先提交当前 Operator Input。'
            : snapshotBudget !== null
              ? 'Agent Run 资源预算已耗尽，不能继续推进。'
              : undefined}
          data-testid="operations-advance"
        >推进</button>
        <button
          type="button"
          className="operations-cancel"
          onClick={() => { void cancel(); }}
          disabled={!investigationId || !snapshotStatus || busy || operatorInputBusy || isTerminalStatus(snapshotStatus)}
          data-testid="operations-cancel"
        >
          {busy ? '处理中…' : '取消 Investigation'}
        </button>
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
                  operatorInputBusy={operatorInputBusy}
                  onSubmitOperatorInput={submitOperatorInput}
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
