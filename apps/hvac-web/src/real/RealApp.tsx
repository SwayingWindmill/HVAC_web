import { useEffect, useMemo, useState } from 'react';
import { createPlatformGatewayClient, type HealthResponse } from '@/api/generated/platformGateway.gen';
import type { RealRuntimeConfig, RealRuntimeConfigFailure } from './runtime-config';
import './real-shell.css';

const REAL_GRAPH_MARKER = 'HVAC_WEB_REAL_GRAPH_V1';

interface RealAppProps {
  config: RealRuntimeConfig;
}

interface GatewayCheck {
  state: 'checking' | 'available' | 'unavailable';
  health?: HealthResponse;
  detail?: string;
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

export default function RealApp({ config }: RealAppProps) {
  const client = useMemo(() => createPlatformGatewayClient(), []);
  const [gatewayCheck, setGatewayCheck] = useState<GatewayCheck>({ state: 'checking' });

  useEffect(() => {
    const controller = new AbortController();

    client.getHealth({ includeBuild: true }, { signal: controller.signal })
      .then((response) => setGatewayCheck({ state: 'available', health: response.data }))
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setGatewayCheck({
          state: 'unavailable',
          detail: error instanceof Error ? error.message : 'Platform Gateway health check failed.',
        });
      });

    return () => controller.abort();
  }, [client]);

  return (
    <main
      className="real-shell-state"
      data-build-graph={REAL_GRAPH_MARKER}
      data-shell-state={gatewayCheck.state === 'available' ? 'READY' : 'UNAVAILABLE'}
    >
      <section className="real-shell-card" aria-labelledby="real-shell-title">
        <p className="real-shell-eyebrow">REAL MODE · AUTHORITATIVE SHELL</p>
        <h1 id="real-shell-title">Real Mode Shell 基础已启动</h1>
        <p>该构建图不包含 Demo 页面、Mock 业务数据、本地角色模拟或 Mock AI。业务路由将在后续 Shell Tickets 中按认证和 Capability 逐步启用。</p>

        <dl className="real-shell-facts">
          <div><dt>Build identity</dt><dd>{config.buildId}</dd></div>
          <div><dt>Gateway</dt><dd>{config.gatewayBasePath}</dd></div>
          <div><dt>Realtime protocol</dt><dd>{config.realtimeProtocol}</dd></div>
          <div>
            <dt>Gateway state</dt>
            <dd>{gatewayCheck.state}</dd>
          </div>
        </dl>

        {gatewayCheck.state === 'available' && gatewayCheck.health ? (
          <p className="real-shell-success">Gateway {gatewayCheck.health.service} 已响应，checkedAt {gatewayCheck.health.checkedAt}</p>
        ) : null}
        {gatewayCheck.state === 'unavailable' ? (
          <p className="real-shell-error" role="alert">Gateway 不可用：{gatewayCheck.detail}</p>
        ) : null}
      </section>
    </main>
  );
}
