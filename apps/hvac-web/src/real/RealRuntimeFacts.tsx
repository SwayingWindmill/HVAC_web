import type { RealRuntimeConfig } from './runtime-config';

export function RealRuntimeFacts({ config }: { config: RealRuntimeConfig }) {
  return (
    <dl className="real-shell-facts" aria-label="Real runtime facts">
      <div><dt>Build identity</dt><dd>{config.buildId}</dd></div>
      <div><dt>Gateway</dt><dd>{config.gatewayBasePath}</dd></div>
      <div><dt>Realtime protocol</dt><dd>{config.realtimeProtocol}</dd></div>
    </dl>
  );
}
