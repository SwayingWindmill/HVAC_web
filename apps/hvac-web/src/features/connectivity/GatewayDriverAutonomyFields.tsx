import { useId } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import type {
  GatewayAutonomyPolicy,
  GatewayModbusDriverConfig,
  GatewayModbusRegisterMapping,
} from '@/api/generated/platformGateway.gen';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { isOneOf } from '@/lib/typed';
import { cn } from '@/lib/utils';
import {
  AUTONOMY_POLICY_BOUNDS,
  AUTONOMY_POLICY_STATE_PRESENTATION,
  MODBUS_BYTE_ORDER_LABELS,
  MODBUS_DRIVER_TYPES,
  MODBUS_DRIVER_TYPE_LABELS,
  MODBUS_ENDIAN_OPTIONS,
  MODBUS_FUNCTION_CODE_LABELS,
  MODBUS_POLL_PRIORITIES,
  MODBUS_POLL_PRIORITY_PRESENTATION,
  MODBUS_RAW_TYPES,
  MODBUS_RAW_TYPE_LABELS,
  MODBUS_READ_FUNCTION_CODES,
  MODBUS_WORD_ORDER_LABELS,
  MODBUS_WRITE_FUNCTION_CODE_LABELS,
  createDefaultAutonomyPolicyDraft,
  createEmptyModbusDriverDraft,
  createEmptyModbusRegisterDraft,
  defaultRegisterCountForRawType,
  summarizeAutonomyPolicyState,
  type GatewayAutonomyPolicyDraft,
  type GatewayModbusDriverDraft,
  type GatewayModbusRegisterDraft,
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

function PriorityChip({ priority }: { readonly priority: GatewayModbusRegisterMapping['pollPriority'] }) {
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
          暂未下发声明式协议驱动点表，可通过「编辑配置影子」为边缘网关配置串口或网络采集点表。
        </p>
      ) : (
        <div className="flex flex-col gap-2.5">
          {drivers.map((driver, driverIdx) => (
            <div
              key={`${driver.deviceKey}-${driver.unitId}-${driverIdx}`}
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
                    <span className="rounded bg-secondary px-1.5 py-0.5 font-mono text-[11px] text-secondary-foreground">
                      {driver.capabilityProfile}
                    </span>
                  ) : null}
                </div>
                <span className="font-mono text-[11px] text-muted-foreground tabular-nums">
                  从站 UID {driver.unitId} · 超时 {driver.timeoutMs} ms · 重试 {driver.retries} 次
                </span>
              </div>

              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                <span>
                  采集地址 <span className="font-mono text-foreground">{driver.endpoint}</span>
                </span>
                <span className="tabular-nums">寄存器 {driver.registers.length} 条</span>
              </div>

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
                    {driver.registers.map((reg, regIdx) => (
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
                        <TableCell className="px-2 py-1.5 whitespace-nowrap text-foreground">
                          <div>{MODBUS_RAW_TYPE_LABELS[reg.rawType]}</div>
                          {reg.rawType === 'BIT_BOOL' && reg.bitIndex !== undefined ? (
                            <div className="font-mono text-[10px] text-muted-foreground tabular-nums">
                              第 {reg.bitIndex} 位
                            </div>
                          ) : null}
                        </TableCell>
                        <TableCell className="px-2 py-1.5 font-mono whitespace-nowrap text-muted-foreground tabular-nums">
                          ×{reg.scale} / +{reg.offset}
                        </TableCell>
                        <TableCell className="px-2 py-1.5 whitespace-nowrap">
                          <PriorityChip priority={reg.pollPriority} />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </table>
              </div>
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

export function DriverConfigsEditorSection({
  driverConfigs,
  disabled,
  onChange,
}: {
  readonly driverConfigs: readonly GatewayModbusDriverDraft[];
  readonly disabled: boolean;
  readonly onChange: (next: readonly GatewayModbusDriverDraft[]) => void;
}) {
  const baseId = useId();

  const updateDriver = (driverIndex: number, patch: Partial<GatewayModbusDriverDraft>) => {
    onChange(
      driverConfigs.map((item, index) => (index === driverIndex ? { ...item, ...patch } : item)),
    );
  };

  const updateRegister = (
    driverIndex: number,
    regIndex: number,
    patch: Partial<GatewayModbusRegisterDraft>,
  ) => {
    const targetDriver = driverConfigs[driverIndex];
    if (!targetDriver) return;
    updateDriver(driverIndex, {
      registers: targetDriver.registers.map((reg, idx) =>
        idx === regIndex ? { ...reg, ...patch } : reg,
      ),
    });
  };

  return (
    <section aria-label="协议驱动点表设置" className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h4 className="text-xs font-semibold text-foreground">
          协议驱动点表（{driverConfigs.length} 台设备）
        </h4>
        <div className="flex items-center gap-1.5">
          {driverConfigs.length > 0 ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={disabled}
              onClick={() => onChange([])}
            >
              清空驱动点表
            </Button>
          ) : null}
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => onChange([...driverConfigs, createEmptyModbusDriverDraft()])}
          >
            <Plus data-icon="inline-start" aria-hidden="true" />
            新增协议驱动
          </Button>
        </div>
      </div>

      {driverConfigs.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
          暂无协议驱动点表；点击「新增协议驱动」配置冷机、电表或变频器的 Modbus 采集参数与寄存器映射。
        </p>
      ) : (
        <div className="flex flex-col gap-3">
          {driverConfigs.map((driver, driverIdx) => {
            const driverPrefix = `${baseId}-drv-${driverIdx}`;
            return (
              <div
                key={driverIdx}
                role="group"
                aria-label={`协议驱动 ${driverIdx + 1}`}
                className="flex flex-col gap-3 rounded-lg border bg-muted/30 p-3"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-semibold text-foreground">
                    协议驱动 #{driverIdx + 1}
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    aria-label={`删除协议驱动 ${driverIdx + 1}`}
                    disabled={disabled}
                    onClick={() => onChange(driverConfigs.filter((_, idx) => idx !== driverIdx))}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                  </Button>
                </div>

                <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                  <div className="grid gap-1">
                    <Label htmlFor={`${driverPrefix}-device-key`} className="text-xs text-muted-foreground">
                      本地设备标识
                    </Label>
                    <Input
                      id={`${driverPrefix}-device-key`}
                      value={driver.deviceKey}
                      placeholder="chiller-01"
                      disabled={disabled}
                      className="h-8 font-mono text-xs"
                      onChange={(event) =>
                        updateDriver(driverIdx, { deviceKey: event.currentTarget.value })
                      }
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${driverPrefix}-driver-type`} className="text-xs text-muted-foreground">
                      协议类型
                    </Label>
                    <Select
                      value={driver.driverType}
                      disabled={disabled}
                      onValueChange={(value) => {
                        if (isOneOf(MODBUS_DRIVER_TYPES, value)) {
                          updateDriver(driverIdx, { driverType: value });
                        }
                      }}
                    >
                      <SelectTrigger id={`${driverPrefix}-driver-type`} className="h-8 text-xs">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {MODBUS_DRIVER_TYPES.map((typeOption) => (
                          <SelectItem key={typeOption} value={typeOption}>
                            {MODBUS_DRIVER_TYPE_LABELS[typeOption]}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${driverPrefix}-endpoint`} className="text-xs text-muted-foreground">
                      采集地址
                    </Label>
                    <Input
                      id={`${driverPrefix}-endpoint`}
                      value={driver.endpoint}
                      placeholder={
                        driver.driverType === 'MODBUS_RTU' ? '/dev/ttyRS485-1' : '192.168.1.20:502'
                      }
                      disabled={disabled}
                      className="h-8 font-mono text-xs"
                      onChange={(event) =>
                        updateDriver(driverIdx, { endpoint: event.currentTarget.value })
                      }
                    />
                  </div>
                </div>

                <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                  <div className="grid gap-1">
                    <Label htmlFor={`${driverPrefix}-uid`} className="text-xs text-muted-foreground">
                      从站地址 (UID)
                    </Label>
                    <Input
                      id={`${driverPrefix}-uid`}
                      type="number"
                      min={1}
                      max={247}
                      value={driver.unitId}
                      disabled={disabled}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(event) =>
                        updateDriver(driverIdx, { unitId: event.currentTarget.value })
                      }
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${driverPrefix}-timeout`} className="text-xs text-muted-foreground">
                      超时时间 (ms)
                    </Label>
                    <Input
                      id={`${driverPrefix}-timeout`}
                      type="number"
                      min={100}
                      max={30000}
                      value={driver.timeoutMs}
                      disabled={disabled}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(event) =>
                        updateDriver(driverIdx, { timeoutMs: event.currentTarget.value })
                      }
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${driverPrefix}-retries`} className="text-xs text-muted-foreground">
                      重试次数
                    </Label>
                    <Input
                      id={`${driverPrefix}-retries`}
                      type="number"
                      min={0}
                      max={10}
                      value={driver.retries}
                      disabled={disabled}
                      className="h-8 font-mono text-xs tabular-nums"
                      onChange={(event) =>
                        updateDriver(driverIdx, { retries: event.currentTarget.value })
                      }
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor={`${driverPrefix}-cap`} className="text-xs text-muted-foreground">
                      能力规格（可选）
                    </Label>
                    <Input
                      id={`${driverPrefix}-cap`}
                      value={driver.capabilityProfile}
                      placeholder="chiller_v1"
                      disabled={disabled}
                      className="h-8 font-mono text-xs"
                      onChange={(event) =>
                        updateDriver(driverIdx, { capabilityProfile: event.currentTarget.value })
                      }
                    />
                  </div>
                </div>

                <div className="flex flex-col gap-2 border-t border-divider pt-2.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-xs font-medium text-foreground">
                      寄存器映射（{driver.registers.length} 条）
                    </span>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={disabled || driver.registers.length >= 256}
                      onClick={() =>
                        updateDriver(driverIdx, {
                          registers: [...driver.registers, createEmptyModbusRegisterDraft()],
                        })
                      }
                    >
                      <Plus data-icon="inline-start" aria-hidden="true" />
                      新增寄存器
                    </Button>
                  </div>

                  <div className="flex flex-col gap-2">
                    {driver.registers.map((reg, regIdx) => {
                      const regPrefix = `${driverPrefix}-reg-${regIdx}`;
                      return (
                        <div
                          key={regIdx}
                          role="group"
                          aria-label={`寄存器 ${regIdx + 1}`}
                          className="flex flex-col gap-2 rounded-md border bg-background p-2.5"
                        >
                          <div className="flex items-center justify-between gap-2">
                            <span className="text-[11px] font-medium text-muted-foreground">
                              寄存器 #{regIdx + 1}
                            </span>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon-xs"
                              aria-label={`删除寄存器 ${driverIdx + 1}-${regIdx + 1}`}
                              disabled={disabled || driver.registers.length <= 1}
                              onClick={() =>
                                updateDriver(driverIdx, {
                                  registers: driver.registers.filter((_, idx) => idx !== regIdx),
                                })
                              }
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                            </Button>
                          </div>

                          <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-point`} className="text-[11px] text-muted-foreground">
                                点位编码
                              </Label>
                              <Input
                                id={`${regPrefix}-point`}
                                value={reg.pointCode}
                                placeholder="chw_supply_temp"
                                disabled={disabled}
                                className="h-8 font-mono text-xs"
                                onChange={(event) =>
                                  updateRegister(driverIdx, regIdx, {
                                    pointCode: event.currentTarget.value,
                                  })
                                }
                              />
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-fc`} className="text-[11px] text-muted-foreground">
                                读功能码
                              </Label>
                              <Select
                                value={reg.functionCode}
                                disabled={disabled}
                                onValueChange={(value) => {
                                  if (isOneOf(MODBUS_READ_FUNCTION_CODES, value)) {
                                    updateRegister(driverIdx, regIdx, { functionCode: value });
                                  }
                                }}
                              >
                                <SelectTrigger id={`${regPrefix}-fc`} className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  <SelectItem value="3">{MODBUS_FUNCTION_CODE_LABELS[3]}</SelectItem>
                                  <SelectItem value="4">{MODBUS_FUNCTION_CODE_LABELS[4]}</SelectItem>
                                </SelectContent>
                              </Select>
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-wfc`} className="text-[11px] text-muted-foreground">
                                写功能码（可选）
                              </Label>
                              <Select
                                value={reg.writeFunctionCode || 'NONE'}
                                disabled={disabled}
                                onValueChange={(value) => {
                                  if (value === 'NONE') {
                                    updateRegister(driverIdx, regIdx, { writeFunctionCode: '' });
                                  } else if (value === '6' || value === '16') {
                                    updateRegister(driverIdx, regIdx, { writeFunctionCode: value });
                                  }
                                }}
                              >
                                <SelectTrigger id={`${regPrefix}-wfc`} className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  <SelectItem value="NONE">仅读取（不写）</SelectItem>
                                  <SelectItem value="6">{MODBUS_WRITE_FUNCTION_CODE_LABELS['6']}</SelectItem>
                                  <SelectItem value="16">{MODBUS_WRITE_FUNCTION_CODE_LABELS['16']}</SelectItem>
                                </SelectContent>
                              </Select>
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-priority`} className="text-[11px] text-muted-foreground">
                                轮询优先级
                              </Label>
                              <Select
                                value={reg.pollPriority}
                                disabled={disabled}
                                onValueChange={(value) => {
                                  if (isOneOf(MODBUS_POLL_PRIORITIES, value)) {
                                    updateRegister(driverIdx, regIdx, { pollPriority: value });
                                  }
                                }}
                              >
                                <SelectTrigger id={`${regPrefix}-priority`} className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {MODBUS_POLL_PRIORITIES.map((priorityOption) => (
                                    <SelectItem key={priorityOption} value={priorityOption}>
                                      {MODBUS_POLL_PRIORITY_PRESENTATION[priorityOption].label}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </div>
                          </div>

                          <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-addr`} className="text-[11px] text-muted-foreground">
                                寄存器地址
                              </Label>
                              <Input
                                id={`${regPrefix}-addr`}
                                type="number"
                                min={0}
                                max={65535}
                                value={reg.address}
                                disabled={disabled}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(event) =>
                                  updateRegister(driverIdx, regIdx, {
                                    address: event.currentTarget.value,
                                  })
                                }
                              />
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-count`} className="text-[11px] text-muted-foreground">
                                寄存器数量
                              </Label>
                              <Input
                                id={`${regPrefix}-count`}
                                type="number"
                                min={1}
                                max={4}
                                value={reg.registerCount}
                                disabled={disabled}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(event) =>
                                  updateRegister(driverIdx, regIdx, {
                                    registerCount: event.currentTarget.value,
                                  })
                                }
                              />
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-raw`} className="text-[11px] text-muted-foreground">
                                原始类型
                              </Label>
                              <Select
                                value={reg.rawType}
                                disabled={disabled}
                                onValueChange={(value) => {
                                  if (!isOneOf(MODBUS_RAW_TYPES, value)) return;
                                  updateRegister(driverIdx, regIdx, {
                                    rawType: value,
                                    registerCount: defaultRegisterCountForRawType(value),
                                    bitIndex:
                                      value === 'BIT_BOOL' ? reg.bitIndex || '0' : '',
                                  });
                                }}
                              >
                                <SelectTrigger id={`${regPrefix}-raw`} className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {MODBUS_RAW_TYPES.map((rawOption) => (
                                    <SelectItem key={rawOption} value={rawOption}>
                                      {MODBUS_RAW_TYPE_LABELS[rawOption]}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-scale`} className="text-[11px] text-muted-foreground">
                                倍率
                              </Label>
                              <Input
                                id={`${regPrefix}-scale`}
                                inputMode="decimal"
                                value={reg.scale}
                                disabled={disabled}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(event) =>
                                  updateRegister(driverIdx, regIdx, {
                                    scale: event.currentTarget.value,
                                  })
                                }
                              />
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-offset`} className="text-[11px] text-muted-foreground">
                                偏置
                              </Label>
                              <Input
                                id={`${regPrefix}-offset`}
                                inputMode="decimal"
                                value={reg.offset}
                                disabled={disabled}
                                className="h-8 font-mono text-xs tabular-nums"
                                onChange={(event) =>
                                  updateRegister(driverIdx, regIdx, {
                                    offset: event.currentTarget.value,
                                  })
                                }
                              />
                            </div>
                          </div>

                          <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-byte-order`} className="text-[11px] text-muted-foreground">
                                字节序
                              </Label>
                              <Select
                                value={reg.byteOrder}
                                disabled={disabled}
                                onValueChange={(value) => {
                                  if (isOneOf(MODBUS_ENDIAN_OPTIONS, value)) {
                                    updateRegister(driverIdx, regIdx, { byteOrder: value });
                                  }
                                }}
                              >
                                <SelectTrigger id={`${regPrefix}-byte-order`} className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {MODBUS_ENDIAN_OPTIONS.map((orderOption) => (
                                    <SelectItem key={orderOption} value={orderOption}>
                                      {MODBUS_BYTE_ORDER_LABELS[orderOption]}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-word-order`} className="text-[11px] text-muted-foreground">
                                字序
                              </Label>
                              <Select
                                value={reg.wordOrder}
                                disabled={disabled}
                                onValueChange={(value) => {
                                  if (isOneOf(MODBUS_ENDIAN_OPTIONS, value)) {
                                    updateRegister(driverIdx, regIdx, { wordOrder: value });
                                  }
                                }}
                              >
                                <SelectTrigger id={`${regPrefix}-word-order`} className="h-8 text-xs">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {MODBUS_ENDIAN_OPTIONS.map((orderOption) => (
                                    <SelectItem key={orderOption} value={orderOption}>
                                      {MODBUS_WORD_ORDER_LABELS[orderOption]}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </div>
                            <div className="grid gap-1">
                              <Label htmlFor={`${regPrefix}-semantic`} className="text-[11px] text-muted-foreground">
                                语义通道（可选）
                              </Label>
                              <Input
                                id={`${regPrefix}-semantic`}
                                value={reg.semanticChannel}
                                placeholder="chw.supply_temp"
                                disabled={disabled}
                                className="h-8 font-mono text-xs"
                                onChange={(event) =>
                                  updateRegister(driverIdx, regIdx, {
                                    semanticChannel: event.currentTarget.value,
                                  })
                                }
                              />
                            </div>
                            {reg.rawType === 'BIT_BOOL' ? (
                              <div className="grid gap-1">
                                <Label htmlFor={`${regPrefix}-bit`} className="text-[11px] text-muted-foreground">
                                  位索引 (0 至 15)
                                </Label>
                                <Input
                                  id={`${regPrefix}-bit`}
                                  type="number"
                                  min={0}
                                  max={15}
                                  value={reg.bitIndex}
                                  disabled={disabled}
                                  className="h-8 font-mono text-xs tabular-nums"
                                  onChange={(event) =>
                                    updateRegister(driverIdx, regIdx, {
                                      bitIndex: event.currentTarget.value,
                                    })
                                  }
                                />
                              </div>
                            ) : null}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

export function AutonomyPolicyEditorSection({
  autonomyPolicy,
  disabled,
  onChange,
}: {
  readonly autonomyPolicy: GatewayAutonomyPolicyDraft | null;
  readonly disabled: boolean;
  readonly onChange: (next: GatewayAutonomyPolicyDraft | null) => void;
}) {
  const baseId = useId();

  const updatePolicy = (patch: Partial<GatewayAutonomyPolicyDraft>) => {
    if (!autonomyPolicy) return;
    onChange({ ...autonomyPolicy, ...patch });
  };

  return (
    <section aria-label="断网自治策略设置" className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h4 className="text-xs font-semibold text-foreground">断网自治策略</h4>
        {autonomyPolicy ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => onChange(null)}
          >
            清除自治策略
          </Button>
        ) : (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => onChange(createDefaultAutonomyPolicyDraft())}
          >
            <Plus data-icon="inline-start" aria-hidden="true" />
            配置断网自治策略
          </Button>
        )}
      </div>

      {!autonomyPolicy ? (
        <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
          当前处于未配置状态；点击「配置断网自治策略」可设置断网宽限、默认冷冻水供水温度与水泵频率安全边界。
        </p>
      ) : (
        <div className="flex flex-col gap-3 rounded-lg border bg-muted/30 p-3">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-divider pb-2.5">
            <div className="flex items-center gap-2.5">
              <Switch
                id={`${baseId}-enabled`}
                checked={autonomyPolicy.enabled}
                disabled={disabled}
                onCheckedChange={(checked) => updatePolicy({ enabled: checked })}
              />
              <Label htmlFor={`${baseId}-enabled`} className="cursor-pointer text-xs font-medium">
                启用断网本地自治
              </Label>
            </div>
            <span
              className={cn(
                'inline-flex h-5.5 items-center rounded-md px-1.5 text-[11px] font-medium',
                autonomyPolicy.enabled
                  ? 'bg-success-soft text-success-text'
                  : 'bg-secondary text-secondary-foreground',
              )}
            >
              {autonomyPolicy.enabled ? '已启用本地自治' : '仅云端受控'}
            </span>
          </div>

          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            <div className="grid gap-1">
              <Label htmlFor={`${baseId}-grace`} className="text-xs text-muted-foreground">
                断网宽限 (秒，{AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds.min} 至{' '}
                {AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds.max})
              </Label>
              <Input
                id={`${baseId}-grace`}
                type="number"
                min={AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds.min}
                max={AUTONOMY_POLICY_BOUNDS.offlineGraceSeconds.max}
                value={autonomyPolicy.offlineGraceSeconds}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) =>
                  updatePolicy({ offlineGraceSeconds: event.currentTarget.value })
                }
              />
            </div>

            <div className="grid gap-1">
              <Label htmlFor={`${baseId}-setpoint`} className="text-xs text-muted-foreground">
                冷冻水供水设定值 (°C，{AUTONOMY_POLICY_BOUNDS.defaultChilledWaterSupplySetpointC.min} 至{' '}
                {AUTONOMY_POLICY_BOUNDS.defaultChilledWaterSupplySetpointC.max})
              </Label>
              <Input
                id={`${baseId}-setpoint`}
                inputMode="decimal"
                value={autonomyPolicy.defaultChilledWaterSupplySetpointC}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) =>
                  updatePolicy({ defaultChilledWaterSupplySetpointC: event.currentTarget.value })
                }
              />
            </div>
          </div>

          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-3">
            <div className="grid gap-1">
              <Label htmlFor={`${baseId}-min-freq`} className="text-xs text-muted-foreground">
                水泵最小频率 (Hz，15 至 50)
              </Label>
              <Input
                id={`${baseId}-min-freq`}
                inputMode="decimal"
                value={autonomyPolicy.minPumpFrequencyHz}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) =>
                  updatePolicy({ minPumpFrequencyHz: event.currentTarget.value })
                }
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor={`${baseId}-default-freq`} className="text-xs text-muted-foreground">
                水泵默认频率 (Hz，15 至 50)
              </Label>
              <Input
                id={`${baseId}-default-freq`}
                inputMode="decimal"
                value={autonomyPolicy.defaultPumpFrequencyHz}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) =>
                  updatePolicy({ defaultPumpFrequencyHz: event.currentTarget.value })
                }
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor={`${baseId}-max-freq`} className="text-xs text-muted-foreground">
                水泵最大频率 (Hz，15 至 50)
              </Label>
              <Input
                id={`${baseId}-max-freq`}
                inputMode="decimal"
                value={autonomyPolicy.maxPumpFrequencyHz}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) =>
                  updatePolicy({ maxPumpFrequencyHz: event.currentTarget.value })
                }
              />
            </div>
          </div>

          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-3">
            <div className="grid gap-1">
              <Label htmlFor={`${baseId}-min-run`} className="text-xs text-muted-foreground">
                最短运行保护 (秒，0 至 3600)
              </Label>
              <Input
                id={`${baseId}-min-run`}
                type="number"
                min={AUTONOMY_POLICY_BOUNDS.minRunSeconds.min}
                max={AUTONOMY_POLICY_BOUNDS.minRunSeconds.max}
                value={autonomyPolicy.minRunSeconds}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) => updatePolicy({ minRunSeconds: event.currentTarget.value })}
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor={`${baseId}-min-stop`} className="text-xs text-muted-foreground">
                最短停机保护 (秒，0 至 3600)
              </Label>
              <Input
                id={`${baseId}-min-stop`}
                type="number"
                min={AUTONOMY_POLICY_BOUNDS.minStopSeconds.min}
                max={AUTONOMY_POLICY_BOUNDS.minStopSeconds.max}
                value={autonomyPolicy.minStopSeconds}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) => updatePolicy({ minStopSeconds: event.currentTarget.value })}
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor={`${baseId}-snapshot`} className="text-xs text-muted-foreground">
                全量快照周期 (秒，30 至 3600)
              </Label>
              <Input
                id={`${baseId}-snapshot`}
                type="number"
                min={AUTONOMY_POLICY_BOUNDS.snapshotIntervalSeconds.min}
                max={AUTONOMY_POLICY_BOUNDS.snapshotIntervalSeconds.max}
                value={autonomyPolicy.snapshotIntervalSeconds}
                disabled={disabled}
                className="h-8 font-mono text-xs tabular-nums"
                onChange={(event) =>
                  updatePolicy({ snapshotIntervalSeconds: event.currentTarget.value })
                }
              />
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
