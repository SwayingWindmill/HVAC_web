import { useEffect, useMemo, useState } from 'react';
import { CopilotKit, useAgent } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  advanceSiteNightEnergyInvestigation,
  OperationsApiError,
  startSiteNightEnergyInvestigation,
  type OperationsInvestigationStateSnapshot,
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
  window.history.replaceState(null, '', `${url.pathname}${url.search}${url.hash}`);
}

function recordStatement(record: Record<string, unknown>): string {
  return typeof record.statement === 'string' ? record.statement : String(record.id);
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

function SnapshotWorkspace({ snapshot }: { readonly snapshot: OperationsInvestigationStateSnapshot }) {
  const investigation = snapshot.investigation;
  return (
    <div className="operations-workspace" data-investigation-status={investigation.status}>
      <section className="operations-summary" aria-labelledby="operations-plan-title">
        <div>
          <p className="real-shell-eyebrow">COMMITTED INVESTIGATION VIEW</p>
          <h2 id="operations-plan-title">{snapshot.plan.label}</h2>
          <p>
            Revision {investigation.revision} · {investigation.status}
            {investigation.outcome ? ` · ${investigation.outcome}` : ''}
          </p>
        </div>
        <div className="operations-progress" role="status" aria-live="polite">
          <progress max={100} value={snapshot.plan.progressPercent} />
          <strong>{snapshot.plan.progressPercent}%</strong>
        </div>
      </section>

      <ol className="operations-plan" aria-label="Investigation plan">
        {snapshot.plan.steps.map((step) => (
          <li key={step.id} data-step-status={step.status}>
            <span>{step.label}</span>
            <strong>{step.status}</strong>
          </li>
        ))}
      </ol>

      <div className="operations-record-grid">
        <section aria-labelledby="operations-evidence-title">
          <h3 id="operations-evidence-title">Evidence</h3>
          {investigation.evidence.length === 0 ? <p>尚无已提交 Evidence。</p> : (
            <ul>
              {investigation.evidence.map((record) => (
                <li key={record.id}>{recordStatement(record)}</li>
              ))}
            </ul>
          )}
        </section>
        <section aria-labelledby="operations-findings-title">
          <h3 id="operations-findings-title">Findings</h3>
          {investigation.findings.length === 0 ? <p>尚无已提交 Finding。</p> : (
            <ul>
              {investigation.findings.map((record) => (
                <li key={record.id}>{recordStatement(record)}</li>
              ))}
            </ul>
          )}
        </section>
      </div>

      <section aria-labelledby="operations-tools-title">
        <h3 id="operations-tools-title">Read-only Tool activity</h3>
        {snapshot.toolActivities.length === 0 ? <p>当前 revision 没有已提交 Tool Receipt。</p> : (
          <ul className="operations-tools">
            {snapshot.toolActivities.map((activity) => (
              <li key={activity.recordId}>
                <strong>{activity.logicalTool}</strong>
                <span>{activity.owner}</span>
                <span>{activity.resultCategory}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
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
  const [runRevision, setRunRevision] = useState(0);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Error | null>(null);
  const [connection, setConnection] = useState<OperationsInvestigationConnectionState | null>(null);

  const requestOptions = useMemo(() => ({
    trustedOrganizationId: principal.context.actingOrganizationId,
    trustedSiteId: site.id,
  }), [principal.context.actingOrganizationId, site.id]);

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

  useEffect(() => {
    if (!investigationId || !agent) return undefined;
    return registerProtectedResource({
      id: `operations-investigation:${site.id}:${investigationId}`,
      kind: 'temporary-state',
      purge: () => {
        agent.abortRun();
        setSnapshot(null);
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
      setInvestigationLocation(created.id);
      setInvestigationId(created.id);
      setOpenValue(created.id);
      setSnapshot(null);
      setRunRevision((value) => value + 1);
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(String(error)));
    } finally {
      setBusy(false);
    }
  };

  const open = () => {
    const next = openValue.trim();
    if (!next) return;
    setInvestigationLocation(next);
    setInvestigationId(next);
    setSnapshot(null);
    setFailure(null);
    setConnection(null);
    setRunRevision((value) => value + 1);
  };

  const advance = async () => {
    if (!investigationId) return;
    setBusy(true);
    setFailure(null);
    setConnection(null);
    try {
      await advanceSiteNightEnergyInvestigation(investigationId, requestOptions);
      setSnapshot(null);
      setRunRevision((value) => value + 1);
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
          <h1>Operations Investigation</h1>
          <p>Site {site.displayName} · 仅展示已提交的权威 Investigation projection。</p>
        </div>
        <button type="button" onClick={() => { void start(); }} disabled={busy}>
          {busy ? '处理中…' : '新建夜间能耗调查'}
        </button>
      </header>

      <section className="operations-open" aria-label="Open Investigation">
        <label htmlFor="operations-investigation-id">Investigation ID</label>
        <input
          id="operations-investigation-id"
          value={openValue}
          onChange={(event) => setOpenValue(event.target.value)}
          maxLength={256}
          placeholder="输入已授权 Investigation ID"
        />
        <button type="button" onClick={open} disabled={!openValue.trim() || busy}>打开</button>
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

      {!investigationId ? (
        <section className="operations-empty">
          <h2>尚未选择 Investigation</h2>
          <p>新建 Site 夜间能耗调查，或打开一个当前 Principal 可见的 Investigation。</p>
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
          {snapshot ? <SnapshotWorkspace snapshot={snapshot} /> : (
            <div className="real-shell-progress" role="status" aria-live="polite">
              正在读取已提交 Investigation 事件…
            </div>
          )}
        </CopilotKit>
      ) : null}
    </div>
  );
}
