import { AlertTriangle } from 'lucide-react';
import type { GatewayConfigShadow } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import {
  CONFIG_SHADOW_APPLIED_LABELS,
  CONFIG_SHADOW_REASON_LABELS,
  CONFIG_SHADOW_SECTION_LABELS,
} from './gateway-config-shadow-labels';
import {
  AUTONOMY_POLICY_STATE_PRESENTATION,
  formatPointCopyTargets,
  summarizeAutonomyPolicyState,
  summarizeDriverPointCounts,
  summarizeEdgeRuleBundle,
  type ConcentratorShadowUnitSummary,
  type DriverPointCountSummary,
} from './gateway-config-shadow-model';

function formatDriverSummaryLabel(summary: DriverPointCountSummary): string {
  if (summary.driverCount === 0) {
    return '协议驱动 0 台';
  }
  const activeProtocols =
    (summary.modbusRegisterCount > 0 ? 1 : 0) +
    (summary.bacnetObjectCount > 0 ? 1 : 0) +
    (summary.opcuaNodeCount > 0 ? 1 : 0);
  if (activeProtocols <= 1) {
    if (summary.bacnetObjectCount > 0) {
      return `协议驱动 ${summary.driverCount} 台 · ${summary.bacnetObjectCount} 个 BACnet 对象`;
    }
    if (summary.opcuaNodeCount > 0) {
      return `协议驱动 ${summary.driverCount} 台 · ${summary.opcuaNodeCount} 个 OPC-UA 节点`;
    }
    return `协议驱动 ${summary.driverCount} 台 · ${summary.modbusRegisterCount} 寄存器`;
  }
  return `协议驱动 ${summary.driverCount} 台 · ${summary.totalPointCount} 项协议点位`;
}
import {
  AutonomyPolicySummaryBlock,
  DriverConfigsSummaryBlock,
  EdgeRuleBundleSummaryBlock,
} from './GatewayDriverAutonomySummary';

const SHADOW_DATETIME = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
});

function formatShadowTimestamp(value: string): string {
  const parsed = Date.parse(value);
  if (Number.isNaN(parsed)) return value;
  return SHADOW_DATETIME.format(parsed);
}

export function GatewayShadowSummaryCard({
  shadow,
  concentratorUnits,
  stopped,
}: {
  readonly shadow: GatewayConfigShadow;
  readonly concentratorUnits: readonly ConcentratorShadowUnitSummary[];
  readonly stopped: boolean;
}) {
  const desired = shadow.desired;
  const reported = shadow.reported;
  const driverSummary = summarizeDriverPointCounts(desired?.driverConfigs);
  const autonomyState = summarizeAutonomyPolicyState(desired?.autonomyPolicy);
  const autonomyLabel = AUTONOMY_POLICY_STATE_PRESENTATION[autonomyState].label;
  const edgeRuleSummary = summarizeEdgeRuleBundle(desired?.edgeRuleBundle);

  if (shadow.syncStatus === 'UNCONFIGURED' && !desired && !reported) {
    return (
      <div className="rounded-lg border border-dashed px-3.5 py-3 text-xs text-muted-foreground">
        该网关尚未配置云边配置影子；可点击「初始化配置影子」下发协议驱动点表、断网自治策略、点位映射与采集周期，或在「无线测温集中器」页面离线暂存参数与 EID。
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 rounded-lg border bg-muted/30 p-3.5 text-xs">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2.5">
        <div>
          <dt className="text-muted-foreground">期望修订</dt>
          <dd className="mt-0.5 font-mono font-semibold text-foreground tabular-nums">
            {desired ? `v${desired.desiredRevision}` : '未设置'}
          </dd>
          {desired ? (
            <dd className="mt-0.5 text-[11px] text-muted-foreground">
              更新于 <span className="font-mono tabular-nums">{formatShadowTimestamp(desired.updatedAt)}</span>
            </dd>
          ) : null}
        </div>
        <div>
          <dt className="text-muted-foreground">边缘已上报修订</dt>
          <dd className="mt-0.5 font-mono font-semibold text-foreground tabular-nums">
            {reported
              ? `v${reported.reportedRevision} · ${CONFIG_SHADOW_APPLIED_LABELS[reported.appliedStatus]}`
              : '等待网关上报'}
          </dd>
          {reported ? (
            <dd className="mt-0.5 text-[11px] text-muted-foreground">
              上报于 <span className="font-mono tabular-nums">{formatShadowTimestamp(reported.reportedAt)}</span>
            </dd>
          ) : null}
        </div>
      </dl>

      <div className="flex flex-wrap gap-2 border-t border-divider pt-2.5 text-[11px]">
        <span className="rounded border bg-background px-2 py-0.5 font-mono tabular-nums">
          点位映射 {desired?.pointMappings.length ?? 0} 条
        </span>
        <span className="rounded border bg-background px-2 py-0.5 font-mono tabular-nums">
          采集周期 {desired?.samplingPolicies.length ?? 0} 条
        </span>
        <span className="rounded border bg-background px-2 py-0.5 font-mono tabular-nums">
          {formatDriverSummaryLabel(driverSummary)}
        </span>
        <span className="rounded border bg-background px-2 py-0.5">
          断网自治 · {autonomyLabel}
        </span>
        <span className="rounded border bg-background px-2 py-0.5 font-mono tabular-nums">
          集中器暂存 {concentratorUnits.length} 个单元
        </span>
        <span className="rounded border bg-background px-2 py-0.5 font-mono tabular-nums">
          边缘规则包 {edgeRuleSummary.configured ? `${edgeRuleSummary.ruleCount} 条` : '未下发'}
        </span>
      </div>

      {desired && desired.pointMappings.length > 0 ? (
        <ul className="flex flex-col gap-1 border-t border-divider pt-2 text-[11px]">
          {desired.pointMappings.slice(0, 3).map((item, idx) => (
            <li key={`${item.sourceChannel}-${idx}`} className="flex items-center justify-between gap-2">
              <span className="shrink-0 font-mono text-foreground">{item.sourceChannel}</span>
              <span className="min-w-0 truncate font-mono text-muted-foreground">
                {item.primaryDeviceKey}:{item.primaryPointCode}
                {item.copyTargets?.length
                  ? ` (+ ${formatPointCopyTargets(item.copyTargets)})`
                  : ''}
              </span>
            </li>
          ))}
          {desired.pointMappings.length > 3 ? (
            <li className="text-muted-foreground">其余 {desired.pointMappings.length - 3} 条可在「编辑配置影子」中查看</li>
          ) : null}
        </ul>
      ) : null}

      <DriverConfigsSummaryBlock driverConfigs={desired?.driverConfigs} />

      <AutonomyPolicySummaryBlock autonomyPolicy={desired?.autonomyPolicy} />

      <EdgeRuleBundleSummaryBlock edgeRuleBundle={desired?.edgeRuleBundle} />

      {concentratorUnits.length > 0 ? (
        <div className="flex flex-wrap gap-1.5 border-t border-divider pt-2 text-[11px]">
          {concentratorUnits.map((unit) => (
            <span
              key={unit.unitId}
              className="rounded bg-secondary px-2 py-0.5 font-mono text-secondary-foreground tabular-nums"
            >
              UID {unit.unitId}
              {unit.hasParams ? ' · 参数' : ''}
              {unit.boundEidCount > 0 ? ` · ${unit.boundEidCount} 槽 EID` : ''}
            </span>
          ))}
        </div>
      ) : null}

      {reported?.errorMessage ? (
        <Alert variant="destructive">
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertDescription>{reported.errorMessage}</AlertDescription>
        </Alert>
      ) : null}

      {shadow.drift.length > 0 ? (
        <div className="flex flex-col gap-1.5 border-t border-divider pt-2.5">
          <span className="font-medium text-destructive-text">
            漂移明细（{shadow.drift.length} 项）
          </span>
          <ul className="flex flex-col gap-1.5">
            {shadow.drift.map((item, index) => (
              <li
                key={`${item.section}-${item.target}-${index}`}
                className="rounded border border-destructive/30 bg-destructive-soft/50 px-2.5 py-2"
              >
                <div className="flex items-center justify-between gap-2 font-medium text-destructive-text">
                  <span>
                    {CONFIG_SHADOW_SECTION_LABELS[item.section]} ·{' '}
                    <span className="font-mono">{item.target}</span>
                  </span>
                  <span className="shrink-0 text-[11px]">
                    {CONFIG_SHADOW_REASON_LABELS[item.reason]}
                  </span>
                </div>
                <p className="mt-0.5 text-[11px] text-foreground">{item.detail}</p>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {stopped ? (
        <p className="text-[11px] text-muted-foreground">
          该网关当前已停用，配置影子暂不可编辑或下发。
        </p>
      ) : null}
    </div>
  );
}
