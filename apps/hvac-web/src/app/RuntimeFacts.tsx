import type { RuntimeConfig } from './runtime-config';

export function RuntimeFacts({ config }: { config: RuntimeConfig }) {
  return (
    <dl className="divide-y divide-divider rounded-lg border border-border bg-muted/30 px-3.5 text-xs" aria-label="运行信息">
      <div className="flex items-center justify-between gap-4 py-2"><dt className="text-muted-foreground">版本</dt><dd className="font-mono text-foreground">{config.buildId}</dd></div>
      <div className="flex items-center justify-between gap-4 py-2"><dt className="text-muted-foreground">网关地址</dt><dd className="font-mono text-foreground">{config.gatewayBasePath}</dd></div>
      <div className="flex items-center justify-between gap-4 py-2"><dt className="text-muted-foreground">实时协议</dt><dd className="font-mono text-foreground">{config.realtimeProtocol}</dd></div>
    </dl>
  );
}
