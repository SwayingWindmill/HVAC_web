import type { RuntimeConfig } from './runtime-config';

export function RuntimeFacts({ config }: { config: RuntimeConfig }) {
  return (
    <dl className="real-shell-facts" aria-label="Runtime facts">
      <div><dt>Build identity</dt><dd>{config.buildId}</dd></div>
      <div><dt>Gateway</dt><dd>{config.gatewayBasePath}</dd></div>
      <div><dt>Realtime protocol</dt><dd>{config.realtimeProtocol}</dd></div>
    </dl>
  );
}
