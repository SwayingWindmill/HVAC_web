import { useEffect, useMemo, useState } from 'react';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import { createShellRuntime, type ShellSnapshot } from './shell-runtime';
import type { RealRuntimeConfig, RealRuntimeConfigFailure } from './runtime-config';
import './real-shell.css';

const REAL_GRAPH_MARKER = 'HVAC_WEB_REAL_GRAPH_V1';
const REAL_SHELL_MARKER = 'REAL MODE · AUTHORITATIVE SHELL';

interface RealAppProps {
  config: RealRuntimeConfig;
}

function RuntimeFacts({ config }: { config: RealRuntimeConfig }) {
  return (
    <dl className="real-shell-facts" aria-label="Real runtime facts">
      <div><dt>Build identity</dt><dd>{config.buildId}</dd></div>
      <div><dt>Gateway</dt><dd>{config.gatewayBasePath}</dd></div>
      <div><dt>Realtime protocol</dt><dd>{config.realtimeProtocol}</dd></div>
    </dl>
  );
}

export function RealConfigurationBlocked({ failures }: { failures: RealRuntimeConfigFailure[] }) {
  return (
    <main className="real-shell-state" data-build-graph={REAL_GRAPH_MARKER} data-shell-state="UNAVAILABLE">
      <section className="real-shell-card" aria-labelledby="real-config-title">
        <p className="real-shell-eyebrow">REAL MODE · STARTUP BLOCKED</p>
        <h1 id="real-config-title">Real 配置无效</h1>
        <p>应用已按失败关闭策略停止，未挂载业务路由、Demo 数据或 Mock 服务。</p>
        <ul className="real-shell-failures">
          {failures.map((failure) => (
            <li key={failure.code}>
              <strong>{failure.code}</strong>
              <span>{failure.detail}</span>
            </li>
          ))}
        </ul>
      </section>
    </main>
  );
}

function BootstrappingState({ config }: { config: RealRuntimeConfig }) {
  return (
    <section className="real-shell-card" aria-labelledby="real-bootstrap-title" data-testid="real-shell-bootstrapping">
      <p className="real-shell-eyebrow">REAL MODE · BOOTSTRAPPING</p>
      <h1 id="real-bootstrap-title">正在建立可信会话</h1>
      <p>Shell 正在读取服务器 Principal 与 Session。完成前不会挂载业务路由或 realtime 订阅。</p>
      <div className="real-shell-progress" role="status" aria-live="polite">正在验证身份边界…</div>
      <RuntimeFacts config={config} />
    </section>
  );
}

function LoginRequiredState({
  config,
  snapshot,
  beginLogin,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  beginLogin: () => void;
}) {
  const loggedOut = snapshot.reason === 'LOGOUT_COMPLETED' || snapshot.reason === 'SESSION_ALREADY_INVALID';
  return (
    <section className="real-shell-card" aria-labelledby="real-login-title" data-testid="real-shell-login-required">
      <p className="real-shell-eyebrow">REAL MODE · LOGIN REQUIRED</p>
      <h1 id="real-login-title">{loggedOut ? '服务器 Session 已撤销' : '需要登录'}</h1>
      <p>
        {loggedOut
          ? '受保护内存已清除。重新进入时将使用 Gateway 发起 OIDC 登录。'
          : '未发现可用的 BFF Session。应用不会在浏览器中收集用户名、密码或令牌。'}
      </p>
      <div className="real-shell-actions">
        <button type="button" onClick={beginLogin}>通过身份提供方登录</button>
      </div>
      <RuntimeFacts config={config} />
    </section>
  );
}

function UnavailableState({
  config,
  snapshot,
  retry,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  retry: () => void;
}) {
  return (
    <section className="real-shell-card" aria-labelledby="real-unavailable-title" data-testid="real-shell-unavailable">
      <p className="real-shell-eyebrow">REAL MODE · UNAVAILABLE</p>
      <h1 id="real-unavailable-title">无法建立可信 Principal</h1>
      <p>业务路由保持未挂载。系统不会使用 Demo、Mock 或缓存身份作为替代。</p>
      {snapshot.failure ? (
        <div className="real-shell-problem" role="alert" data-retryable={String(snapshot.failure.retryable)}>
          <strong>{snapshot.failure.code}</strong>
          <span>{snapshot.failure.detail}</span>
          {snapshot.failure.traceId ? <code>traceId {snapshot.failure.traceId}</code> : null}
        </div>
      ) : null}
      <div className="real-shell-actions">
        <button type="button" onClick={retry}>重试 Principal bootstrap</button>
      </div>
      <RuntimeFacts config={config} />
    </section>
  );
}

function ReadyState({
  config,
  snapshot,
  logout,
}: {
  config: RealRuntimeConfig;
  snapshot: ShellSnapshot;
  logout: () => void;
}) {
  const principal = snapshot.principal!;
  const submitting = snapshot.logout?.status === 'submitting';
  return (
    <section
      className="real-shell-card"
      aria-labelledby="real-ready-title"
      data-testid="real-protected-shell"
      data-protected-route-mounted="true"
      data-policy-revision={principal.authorization.policyRevision}
      data-capability-count={String(principal.authorization.capabilities.length)}
    >
      <p className="real-shell-eyebrow">REAL MODE · AUTHENTICATED</p>
      <h1 id="real-ready-title">可信 Shell 已就绪</h1>
      <p>Principal 与服务器授权快照仅保存在当前页面内存中。Site 路由将在后续 Ticket 接入。</p>

      <dl className="real-shell-facts real-shell-principal-facts">
        <div><dt>Principal</dt><dd>{principal.principal.displayName}</dd></div>
        <div><dt>Subject</dt><dd>{principal.principal.subject}</dd></div>
        <div><dt>Acting Organization</dt><dd>{principal.context.actingOrganizationId}</dd></div>
        <div><dt>IAM policy revision</dt><dd>{principal.authorization.policyRevision}</dd></div>
        <div><dt>Session expires</dt><dd>{principal.session.expiresAt}</dd></div>
        <div><dt>描述性角色</dt><dd>{principal.principal.roles.join(', ') || 'none'}</dd></div>
      </dl>

      <div className="real-shell-capabilities" aria-label="Effective capabilities">
        <strong>Effective capabilities ({principal.authorization.capabilities.length})</strong>
        {principal.authorization.capabilities.length ? (
          <ul>{principal.authorization.capabilities.map((capability) => <li key={capability}><code>{capability}</code></li>)}</ul>
        ) : <p>当前 Principal 没有已发布 Capability。</p>}
      </div>

      {snapshot.logout?.status === 'failed' ? (
        <div className="real-shell-problem" role="alert" data-testid="real-logout-failure" data-retryable={String(snapshot.logout.retryable)}>
          <strong>{snapshot.logout.code}</strong>
          <span>{snapshot.logout.detail}</span>
          {snapshot.logout.traceId ? <code>traceId {snapshot.logout.traceId}</code> : null}
        </div>
      ) : null}

      <div className="real-shell-actions">
        <button type="button" onClick={logout} disabled={submitting} data-testid="real-logout-button">
          {submitting ? '正在撤销服务器 Session…' : '退出登录'}
        </button>
      </div>
      <RuntimeFacts config={config} />
    </section>
  );
}

export default function RealApp({ config }: RealAppProps) {
  const client = useMemo(() => createPlatformGatewayClient(), []);
  const runtime = useMemo(() => createShellRuntime(client), [client]);
  const [snapshot, setSnapshot] = useState<ShellSnapshot>(() => runtime.current());

  useEffect(() => {
    const unsubscribe = runtime.subscribe(setSnapshot);
    void runtime.bootstrap(window.location.href);
    return () => {
      unsubscribe();
      runtime.dispose();
    };
  }, [runtime]);

  return (
    <main
      className="real-shell-state"
      aria-label={REAL_SHELL_MARKER}
      data-build-graph={REAL_GRAPH_MARKER}
      data-shell-state={snapshot.state}
      data-protected-route-mounted={snapshot.state === 'READY' ? 'true' : 'false'}
    >
      {snapshot.state === 'BOOTSTRAPPING' ? <BootstrappingState config={config} /> : null}
      {snapshot.state === 'LOGIN_REQUIRED' ? (
        <LoginRequiredState config={config} snapshot={snapshot} beginLogin={() => runtime.beginLogin()} />
      ) : null}
      {snapshot.state === 'UNAVAILABLE' ? (
        <UnavailableState config={config} snapshot={snapshot} retry={() => { void runtime.retry(); }} />
      ) : null}
      {snapshot.state === 'READY' ? (
        <ReadyState config={config} snapshot={snapshot} logout={() => { void runtime.logout(); }} />
      ) : null}
    </main>
  );
}
