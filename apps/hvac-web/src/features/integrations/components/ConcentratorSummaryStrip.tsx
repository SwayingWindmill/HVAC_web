import { FactTile } from '@/blocks/fact-list';
import { Panel } from '@/blocks/panel';
import { Skeleton } from '@/components/ui/skeleton';
import type { ConcentratorParams } from '@/api/generated/platformGateway.gen';
import {
  formatConcentratorBaudRate,
  type ConcentratorSummary,
} from '../concentrator-model';

function formatResponseTimestamp(iso: string | null): string {
  if (!iso) return '—';
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}

export interface ConcentratorSummaryStripProps {
  readonly summary: ConcentratorSummary | null;
  readonly params: ConcentratorParams | null;
  readonly respondedAt: string | null;
  readonly loading: boolean;
}

export function ConcentratorSummaryStrip({
  summary,
  params,
  respondedAt,
  loading,
}: ConcentratorSummaryStripProps) {
  return (
    <Panel aria-label="无线集中器概览">
      {loading || !summary || !params ? (
        <div className="grid grid-cols-1 gap-4 p-5 sm:grid-cols-2 lg:grid-cols-5">
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
        </div>
      ) : (
        <dl className="grid grid-cols-1 gap-4 divide-y divide-divider px-5 py-4 sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-5 lg:divide-x">
          <div className="lg:pr-4">
            <FactTile
              label="在线探头 / 已绑定通道"
              valueClassName="text-lg font-semibold tabular-nums"
            >
              {summary.onlineCount} / {summary.boundChannels} 路
            </FactTile>
          </div>

          <div className="pt-3 sm:pt-0 lg:px-4">
            <FactTile
              label="低电压通道 (<2600 mV)"
              valueClassName={
                summary.lowBatteryCount > 0
                  ? 'text-lg font-semibold tabular-nums text-warning-text'
                  : 'text-lg font-semibold tabular-nums text-foreground'
              }
            >
              <span>{summary.lowBatteryCount} 路</span>
              {summary.criticalBatteryCount > 0 ? (
                <span className="mt-0.5 block text-xs font-medium text-destructive-text">
                  含 {summary.criticalBatteryCount} 路严重欠压
                </span>
              ) : null}
            </FactTile>
          </div>

          <div className="pt-3 sm:pt-0 lg:px-4">
            <FactTile
              label="无线射频配置"
              valueClassName="font-mono text-sm font-semibold tabular-nums"
            >
              {params.rfFrequency} MHz · SF{params.rfSf} · {params.rfTxPower} dBm
            </FactTile>
          </div>

          <div className="pt-3 sm:pt-0 lg:px-4">
            <FactTile
              label="串口与上报周期"
              valueClassName="font-mono text-sm font-semibold tabular-nums"
            >
              UID {params.modbusId} · {formatConcentratorBaudRate(params.baudRateCode)} ·{' '}
              {params.dataUplinkCycle}s
            </FactTile>
          </div>

          <div className="pt-3 sm:pt-0 lg:pl-4">
            <FactTile
              label="最近网关响应"
              valueClassName="font-mono text-sm font-semibold tabular-nums"
            >
              {formatResponseTimestamp(respondedAt)}
            </FactTile>
          </div>
        </dl>
      )}
    </Panel>
  );
}
