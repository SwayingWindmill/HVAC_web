import type { RuntimeConfig } from './runtime-config';

export function RuntimeFacts({ config }: { config: RuntimeConfig }) {
  return (
    <dl className="real-shell-facts" aria-label="运行信息">
      <div><dt>版本</dt><dd>{config.buildId}</dd></div>
      <div><dt>网关地址</dt><dd>{config.gatewayBasePath}</dd></div>
      <div><dt>实时协议</dt><dd>{config.realtimeProtocol}</dd></div>
    </dl>
  );
}
