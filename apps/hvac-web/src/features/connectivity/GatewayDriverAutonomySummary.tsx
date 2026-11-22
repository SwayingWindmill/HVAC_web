import type {
  EdgeRuleBundle,
  GatewayAutonomyPolicy,
  GatewayModbusDriverConfig,
  GatewayModbusRegisterMapping,
} from '@/api/generated/platformGateway.gen';
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import {
  AUTONOMY_POLICY_STATE_PRESENTATION,
  BACNET_OBJECT_TYPE_LABELS,
  MODBUS_CAPABILITY_PROFILE_LABELS,
  MODBUS_DRIVER_TYPE_LABELS,
  MODBUS_FUNCTION_CODE_LABELS,
  MODBUS_POLL_PRIORITY_PRESENTATION,
  MODBUS_RAW_TYPE_LABELS,
  MODBUS_WRITE_FUNCTION_CODE_LABELS,
  OPCUA_AUTH_MODE_LABELS,
  formatModbusEnumMap,
  summarizeAutonomyPolicyState,
  summarizeEdgeRuleBundle,
} from './gateway-config-shadow-model';

const PRIORITY_BADGE_CLASSES = {
  info: 'bg-primary-soft text-primary',
  warning: 'bg-warning-soft text-warning-text',
  neutral: 'bg-secondary text-secondary-foreground',
} as const;

const AUTONOMY_BADGE_CLASSES = {
  success: { chip: 'bg-success-soft text-success-text', dot: 'bg-success' },
  neutral: { chip: 'bg-secondary text-secondary-foreground', dot: 'bg-subtle-foreground' },
} as const;

function PriorityChip({
  priority,
}: {
  readonly priority: GatewayModbusRegisterMapping['pollPriority'];
}) {
  const meta = MODBUS_POLL_PRIORITY_PRESENTATION[priority];
  return (
    <span
      className={cn(
        'inline-flex h-5 items-center rounded px-1.5 text-[11px] font-medium whitespace-nowrap',
        PRIORITY_BADGE_CLASSES[meta.tone],
      )}
    >
      {meta.label}
    </span>
  );
}

function formatDriverHeaderMeta(driver: GatewayModbusDriverConfig): string {
  switch (driver.driverType) {
    case 'BACNET_IP': {
      const devInst = driver.bacnet?.deviceInstance ?? '—';
      const prio = driver.bacnet?.writePriority ?? 8;
      return `设备实例 ${devInst} · 写优先级 ${prio} · 超时 ${driver.timeoutMs} ms · 重试 ${driver.retries} 次`;
    }
    case 'OPC_UA': {
      const policy = driver.opcua?.securityPolicy ?? 'None';
      const mode = driver.opcua?.securityMode ?? 'None';
      const interval = driver.opcua?.subscriptionIntervalMs ?? 1000;
      return `${policy} / ${mode} · 订阅 ${interval} ms · 超时 ${driver.timeoutMs} ms`;
    }
    case 'MODBUS_TCP':
    case 'MODBUS_RTU':
      return `从站 UID ${driver.unitId ?? '—'} · 超时 ${driver.timeoutMs} ms · 重试 ${driver.retries} 次`;
  }
}

function formatDriverPointCountLabel(driver: GatewayModbusDriverConfig): string {
  switch (driver.driverType) {
    case 'BACNET_IP':
      return `BACnet 对象 ${driver.bacnet?.objects.length ?? 0} 项`;
    case 'OPC_UA':
      return `OPC-UA 节点 ${driver.opcua?.nodes.length ?? 0} 项`;
    case 'MODBUS_TCP':
    case 'MODBUS_RTU':
      return `寄存器 ${driver.registers?.length ?? 0} 条`;
  }
}

function ModbusRegistersSummaryTable({
  driver,
}: {
  readonly driver: GatewayModbusDriverConfig;
}) {
  const registers = driver.registers ?? [];
  return (
    <div
      role="region"
      aria-label={`${driver.deviceKey} 协议驱动寄存器表`}
      tabIndex={0}
      className="relative w-full overflow-x-auto rounded border bg-card outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring-soft"
    >
      <table className="w-full caption-bottom text-xs">
        <TableHeader>
          <TableRow>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              点位
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              功能码
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              地址
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              类型
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              倍率 / 偏置
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              优先级
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {registers.map((reg, regIdx) => (
            <TableRow key={`${reg.pointCode}-${reg.address}-${regIdx}`} className="text-[11px]">
              <TableCell className="px-2 py-1.5 font-mono text-foreground">
                <div className="whitespace-nowrap">{reg.pointCode}</div>
                {reg.semanticChannel ? (
                  <div className="whitespace-nowrap text-[10px] text-muted-foreground">
                    {reg.semanticChannel}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="px-2 py-1.5 whitespace-nowrap text-foreground">
                <div>{MODBUS_FUNCTION_CODE_LABELS[reg.functionCode]}</div>
                {reg.writeFunctionCode ? (
                  <div className="text-[10px] text-muted-foreground">
                    {MODBUS_WRITE_FUNCTION_CODE_LABELS[reg.writeFunctionCode === 6 ? '6' : '16']}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="px-2 py-1.5 font-mono whitespace-nowrap text-foreground tabular-nums">
                <div>{reg.address}</div>
                <div className="text-[10px] text-muted-foreground">
                  {reg.registerCount} 寄存器
                </div>
              </TableCell>
              <TableCell className="px-2 py-1.5 text-foreground">
                <div className="whitespace-nowrap">{MODBUS_RAW_TYPE_LABELS[reg.rawType]}</div>
                {reg.rawType === 'BIT_BOOL' && reg.bitIndex !== undefined ? (
                  <div className="font-mono text-[10px] text-muted-foreground tabular-nums">
                    第 {reg.bitIndex} 位
                  </div>
                ) : reg.rawType === 'ENUM16' && reg.enumMap ? (
                  <div className="text-[10px] text-muted-foreground">
                    {formatModbusEnumMap(reg.enumMap)}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="px-2 py-1.5 font-mono whitespace-nowrap text-foreground tabular-nums">
                ×{reg.scale} / {reg.offset >= 0 ? `+${reg.offset}` : reg.offset}
              </TableCell>
              <TableCell className="px-2 py-1.5 whitespace-nowrap">
                <PriorityChip priority={reg.pollPriority} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </table>
    </div>
  );
}

function BACnetObjectsSummaryTable({
  driver,
}: {
  readonly driver: GatewayModbusDriverConfig;
}) {
  const objects = driver.bacnet?.objects ?? [];
  return (
    <div
      role="region"
      aria-label={`${driver.deviceKey} BACnet 对象表`}
      tabIndex={0}
      className="relative w-full overflow-x-auto rounded border bg-card outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring-soft"
    >
      <table className="w-full caption-bottom text-xs">
        <TableHeader>
          <TableRow>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              点位
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              对象 / 实例
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              模式 / 边界
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              倍率 / 偏置
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              优先级
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {objects.map((obj, idx) => (
            <TableRow key={`${obj.pointCode}-${obj.objectInstance}-${idx}`} className="text-[11px]">
              <TableCell className="px-2 py-1.5 font-mono text-foreground">
                <div className="whitespace-nowrap">{obj.pointCode}</div>
                {obj.semanticChannel ? (
                  <div className="whitespace-nowrap text-[10px] text-muted-foreground">
                    {obj.semanticChannel}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="px-2 py-1.5 text-foreground tabular-nums">
                <div className="whitespace-nowrap">
                  {BACNET_OBJECT_TYPE_LABELS[obj.objectType]} · #{obj.objectInstance}
                </div>
                <div className="font-mono text-[10px] text-muted-foreground">
                  属性 {obj.propertyId ?? 85}
                  {obj.unit ? ` · ${obj.unit}` : ''}
                </div>
              </TableCell>
              <TableCell className="px-2 py-1.5 text-foreground">
                <div className="whitespace-nowrap">
                  {obj.writable ? '可写' : '只读'}
                  {obj.covSubscribe ? ' · COV 订阅' : ' · 轮询'}
                </div>
                {obj.safetyBoundary === 'HARD_REALTIME_SAFETY' ? (
                  <div className="text-[10px] text-warning-text">硬实时保护点位</div>
                ) : null}
              </TableCell>
              <TableCell className="px-2 py-1.5 font-mono whitespace-nowrap text-foreground tabular-nums">
                ×{obj.scale ?? 1} / {(obj.offset ?? 0) >= 0 ? `+${obj.offset ?? 0}` : obj.offset}
              </TableCell>
              <TableCell className="px-2 py-1.5 whitespace-nowrap">
                <PriorityChip priority={obj.pollPriority} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </table>
    </div>
  );
}

function OPCUANodesSummaryTable({
  driver,
}: {
  readonly driver: GatewayModbusDriverConfig;
}) {
  const nodes = driver.opcua?.nodes ?? [];
  return (
    <div
      role="region"
      aria-label={`${driver.deviceKey} OPC-UA 节点表`}
      tabIndex={0}
      className="relative w-full overflow-x-auto rounded border bg-card outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring-soft"
    >
      <table className="w-full caption-bottom text-xs">
        <TableHeader>
          <TableRow>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              点位
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              节点 ID (NodeId)
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              模式 / 边界
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              倍率 / 偏置
            </TableHead>
            <TableHead scope="col" className="h-8 px-2 text-[11px]">
              优先级
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {nodes.map((node, idx) => (
            <TableRow key={`${node.pointCode}-${idx}`} className="text-[11px]">
              <TableCell className="px-2 py-1.5 font-mono text-foreground">
                <div className="whitespace-nowrap">{node.pointCode}</div>
                {node.semanticChannel ? (
                  <div className="whitespace-nowrap text-[10px] text-muted-foreground">
                    {node.semanticChannel}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="px-2 py-1.5 font-mono text-foreground">
                <div className="whitespace-nowrap">{node.nodeId}</div>
                {node.unit ? (
                  <div className="text-[10px] text-muted-foreground">{node.unit}</div>
                ) : null}
              </TableCell>
              <TableCell className="px-2 py-1.5 text-foreground">
                <div className="whitespace-nowrap">
                  {node.writable ? '可写' : '只读'}
                  {node.subscribe ? ' · 订阅推送' : ' · 轮询'}
                </div>
                {node.safetyBoundary === 'HARD_REALTIME_SAFETY' ? (
                  <div className="text-[10px] text-warning-text">硬实时保护点位</div>
                ) : null}
              </TableCell>
              <TableCell className="px-2 py-1.5 font-mono whitespace-nowrap text-foreground tabular-nums">
                ×{node.scale ?? 1} / {(node.offset ?? 0) >= 0 ? `+${node.offset ?? 0}` : node.offset}
              </TableCell>
              <TableCell className="px-2 py-1.5 whitespace-nowrap">
                <PriorityChip priority={node.pollPriority} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </table>
    </div>
  );
}

export function DriverConfigsSummaryBlock({
  driverConfigs,
}: {
  readonly driverConfigs: readonly GatewayModbusDriverConfig[] | undefined;
}) {
  const drivers = driverConfigs ?? [];

  return (
    <div className="flex flex-col gap-2 border-t border-divider pt-2.5">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-foreground">协议驱动点表</span>
        <span className="font-mono text-[11px] text-muted-foreground tabular-nums">
          {drivers.length > 0 ? `${drivers.length} 台本地设备` : '未配置'}
        </span>
      </div>

      {drivers.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">
          暂未下发声明式协议驱动点表，可通过「编辑配置影子」为边缘网关配置串口、Modbus、BACnet/IP 或 OPC-UA 采集点表。
        </p>
      ) : (
        <div className="flex flex-col gap-2.5">
          {drivers.map((driver, driverIdx) => (
            <div
              key={`${driver.deviceKey}-${driver.driverType}-${driverIdx}`}
              className="flex flex-col gap-2 rounded-md border bg-background p-2.5"
            >
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="font-mono text-xs font-semibold text-foreground">
                    {driver.deviceKey}
                  </span>
                  <span className="rounded bg-primary-soft px-1.5 py-0.5 text-[11px] font-medium text-primary">
                    {MODBUS_DRIVER_TYPE_LABELS[driver.driverType]}
                  </span>
                  {driver.capabilityProfile ? (
                    <span className="rounded bg-secondary px-1.5 py-0.5 text-[11px] font-medium text-secondary-foreground">
                      {MODBUS_CAPABILITY_PROFILE_LABELS[driver.capabilityProfile]}
                    </span>
                  ) : null}
                </div>
                <span className="font-mono text-[11px] text-muted-foreground tabular-nums">
                  {formatDriverHeaderMeta(driver)}
                </span>
              </div>

              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                <span>
                  采集地址 <span className="font-mono text-foreground">{driver.endpoint}</span>
                </span>
                <span className="tabular-nums">{formatDriverPointCountLabel(driver)}</span>
                {driver.driverType === 'OPC_UA' && driver.opcua ? (
                  <span>{OPCUA_AUTH_MODE_LABELS[driver.opcua.authMode ?? 'ANONYMOUS']}</span>
                ) : null}
              </div>

              {driver.driverType === 'BACNET_IP' ? (
                <BACnetObjectsSummaryTable driver={driver} />
              ) : driver.driverType === 'OPC_UA' ? (
                <OPCUANodesSummaryTable driver={driver} />
              ) : (
                <ModbusRegistersSummaryTable driver={driver} />
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function AutonomyPolicySummaryBlock({
  autonomyPolicy,
}: {
  readonly autonomyPolicy: GatewayAutonomyPolicy | null | undefined;
}) {
  const state = summarizeAutonomyPolicyState(autonomyPolicy);
  const meta = AUTONOMY_POLICY_STATE_PRESENTATION[state];
  const badgeStyle = AUTONOMY_BADGE_CLASSES[meta.tone];

  return (
    <div className="flex flex-col gap-2 border-t border-divider pt-2.5">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-foreground">断网自治策略</span>
        <span
          className={cn(
            'inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-[11px] font-medium whitespace-nowrap',
            badgeStyle.chip,
          )}
        >
          <span className={cn('size-1.5 rounded-full', badgeStyle.dot)} aria-hidden="true" />
          {meta.label}
        </span>
      </div>

      {!autonomyPolicy ? (
        <p className="text-[11px] text-muted-foreground">
          未配置断网自治策略；网关离线时将保留原有运行设定，可在「编辑配置影子」中设置宽限时间与水泵安全频率。
        </p>
      ) : (
        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 rounded-md border bg-background p-2.5 text-[11px]">
          <div>
            <dt className="text-muted-foreground">断网宽限</dt>
            <dd className="mt-0.5 font-mono font-medium text-foreground tabular-nums">
              {autonomyPolicy.offlineGraceSeconds} 秒
            </dd>
          </div>
          <div>
            <dt className="text-muted-foreground">冷冻水供水设定值</dt>
            <dd className="mt-0.5 font-mono font-medium text-foreground tabular-nums">
              {autonomyPolicy.defaultChilledWaterSupplySetpointC} °C
            </dd>
          </div>
          <div>
            <dt className="text-muted-foreground">水泵默认 / 上下限频率</dt>
            <dd className="mt-0.5 font-mono font-medium text-foreground tabular-nums">
              {autonomyPolicy.defaultPumpFrequencyHz} Hz（{autonomyPolicy.minPumpFrequencyHz} 至{' '}
              {autonomyPolicy.maxPumpFrequencyHz} Hz）
            </dd>
          </div>
          <div>
            <dt className="text-muted-foreground">最短运行 / 停机保护</dt>
            <dd className="mt-0.5 font-mono font-medium text-foreground tabular-nums">
              {autonomyPolicy.minRunSeconds} / {autonomyPolicy.minStopSeconds} 秒
            </dd>
          </div>
          <div className="col-span-2">
            <dt className="text-muted-foreground">全量快照周期</dt>
            <dd className="mt-0.5 font-mono font-medium text-foreground tabular-nums">
              {autonomyPolicy.snapshotIntervalSeconds} 秒
            </dd>
          </div>
        </dl>
      )}
    </div>
  );
}

export function EdgeRuleBundleSummaryBlock({
  edgeRuleBundle,
}: {
  readonly edgeRuleBundle: EdgeRuleBundle | null | undefined;
}) {
  const summary = summarizeEdgeRuleBundle(edgeRuleBundle);

  return (
    <div className="flex flex-col gap-2 border-t border-divider pt-2.5">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-foreground">边缘规则包</span>
        <span
          className={cn(
            'inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-[11px] font-medium whitespace-nowrap',
            summary.configured
              ? 'bg-success-soft text-success-text'
              : 'bg-secondary text-secondary-foreground',
          )}
        >
          <span
            className={cn('size-1.5 rounded-full', summary.configured ? 'bg-success' : 'bg-subtle-foreground')}
            aria-hidden="true"
          />
          {summary.configured ? `已下发 ${summary.ruleCount} 条` : '未下发'}
        </span>
      </div>

      {!edgeRuleBundle ? (
        <p className="text-[11px] text-muted-foreground">
          暂未下发云边统一编译的边缘规则包，可在「运维 → 告警 → 正在生效的规则 → 边缘编译下发」中一键推送。
        </p>
      ) : (
        <div className="flex flex-col gap-2 rounded-md border bg-background p-2.5 text-[11px]">
          <dl className="grid grid-cols-3 gap-x-3 gap-y-1">
            <div>
              <dt className="text-muted-foreground">来源版本</dt>
              <dd className="mt-0.5 font-mono font-medium text-foreground tabular-nums">
                v{summary.bundleRevision}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">规则数</dt>
              <dd className="mt-0.5 font-mono font-medium text-foreground tabular-nums">
                {summary.ruleCount} 条
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">边缘摘要</dt>
              <dd className="mt-0.5 font-mono font-medium text-foreground">
                {summary.shortDigest}
              </dd>
            </div>
          </dl>
          {edgeRuleBundle.rules.length > 0 ? (
            <ul className="flex flex-col gap-1 border-t border-divider pt-1.5">
              {edgeRuleBundle.rules.slice(0, 3).map((rule) => (
                <li key={rule.ruleId} className="truncate text-foreground">
                  {rule.title}
                </li>
              ))}
              {edgeRuleBundle.rules.length > 3 ? (
                <li className="text-muted-foreground">其余 {edgeRuleBundle.rules.length - 3} 条将在网关本地闭环执行</li>
              ) : null}
            </ul>
          ) : null}
        </div>
      )}
    </div>
  );
}
