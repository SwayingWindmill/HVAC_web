import { useId } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { isOneOf } from '@/lib/typed';
import { BACnetObjectMappingRow } from './GatewayBACnetObjectRow';
import { OPCUANodeMappingRow } from './GatewayOPCUANodeRow';
import {
  MODBUS_CAPABILITY_PROFILES,
  MODBUS_CAPABILITY_PROFILE_LABELS,
  OPCUA_AUTH_MODES,
  OPCUA_AUTH_MODE_LABELS,
  OPCUA_SECURITY_MODES,
  OPCUA_SECURITY_MODE_LABELS,
  OPCUA_SECURITY_POLICIES,
  OPCUA_SECURITY_POLICY_LABELS,
  SOUTHBOUND_DRIVER_TYPES,
  SOUTHBOUND_DRIVER_TYPE_LABELS,
  createEmptyBACnetObjectDraft,
  createEmptyModbusDriverDraft,
  createEmptyModbusRegisterDraft,
  createEmptyOPCUANodeDraft,
  type GatewayBACnetObjectDraft,
  type GatewayBACnetSpecDraft,
  type GatewayModbusDriverDraft,
  type GatewayModbusRegisterDraft,
  type GatewayOPCUANodeDraft,
  type GatewayOPCUASpecDraft,
} from './gateway-config-shadow-model';
import {
  LabeledInput,
  LabeledSelect,
  RegisterMappingRow,
  type SelectOption,
  mapLabeledOptions,
} from './GatewayRegisterMappingRow';

const PROFILE_NONE_VALUE = '__NONE__';

const DRIVER_TYPE_OPTIONS = mapLabeledOptions(
  SOUTHBOUND_DRIVER_TYPES,
  SOUTHBOUND_DRIVER_TYPE_LABELS,
);
const CAPABILITY_PROFILE_OPTIONS: readonly SelectOption[] = [
  { value: PROFILE_NONE_VALUE, label: '不绑定能力档位' },
  ...mapLabeledOptions(MODBUS_CAPABILITY_PROFILES, MODBUS_CAPABILITY_PROFILE_LABELS),
];
const SECURITY_POLICY_OPTIONS = mapLabeledOptions(
  OPCUA_SECURITY_POLICIES,
  OPCUA_SECURITY_POLICY_LABELS,
);
const SECURITY_MODE_OPTIONS = mapLabeledOptions(
  OPCUA_SECURITY_MODES,
  OPCUA_SECURITY_MODE_LABELS,
);
const AUTH_MODE_OPTIONS = mapLabeledOptions(OPCUA_AUTH_MODES, OPCUA_AUTH_MODE_LABELS);

function endpointPlaceholderForDriverType(
  driverType: GatewayModbusDriverDraft['driverType'],
): string {
  switch (driverType) {
    case 'MODBUS_RTU':
      return '/dev/ttyRS485-1';
    case 'BACNET_IP':
      return '192.168.10.30:47808';
    case 'OPC_UA':
      return 'opc.tcp://192.168.10.40:4840';
    case 'MODBUS_TCP':
      return '192.168.1.20:502';
  }
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

  const updateDriver = (driverId: string, patch: Partial<GatewayModbusDriverDraft>) => {
    onChange(
      driverConfigs.map((item) => (item.draftId === driverId ? { ...item, ...patch } : item)),
    );
  };

  const updateRegister = (
    driverId: string,
    regId: string,
    patch: Partial<GatewayModbusRegisterDraft>,
  ) => {
    const targetDriver = driverConfigs.find((item) => item.draftId === driverId);
    if (!targetDriver) return;
    updateDriver(driverId, {
      registers: targetDriver.registers.map((reg) =>
        reg.draftId === regId ? { ...reg, ...patch } : reg,
      ),
    });
  };

  const updateBACnet = (driverId: string, patch: Partial<GatewayBACnetSpecDraft>) => {
    const targetDriver = driverConfigs.find((item) => item.draftId === driverId);
    if (!targetDriver) return;
    updateDriver(driverId, {
      bacnet: { ...targetDriver.bacnet, ...patch },
    });
  };

  const updateBACnetObject = (
    driverId: string,
    objId: string,
    patch: Partial<GatewayBACnetObjectDraft>,
  ) => {
    const targetDriver = driverConfigs.find((item) => item.draftId === driverId);
    if (!targetDriver) return;
    updateBACnet(driverId, {
      objects: targetDriver.bacnet.objects.map((obj) =>
        obj.draftId === objId ? { ...obj, ...patch } : obj,
      ),
    });
  };

  const updateOPCUA = (driverId: string, patch: Partial<GatewayOPCUASpecDraft>) => {
    const targetDriver = driverConfigs.find((item) => item.draftId === driverId);
    if (!targetDriver) return;
    updateDriver(driverId, {
      opcua: { ...targetDriver.opcua, ...patch },
    });
  };

  const updateOPCUANode = (
    driverId: string,
    nodeId: string,
    patch: Partial<GatewayOPCUANodeDraft>,
  ) => {
    const targetDriver = driverConfigs.find((item) => item.draftId === driverId);
    if (!targetDriver) return;
    updateOPCUA(driverId, {
      nodes: targetDriver.opcua.nodes.map((node) =>
        node.draftId === nodeId ? { ...node, ...patch } : node,
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
          暂无协议驱动点表；点击「新增协议驱动」配置冷机、电表或空调箱的 Modbus、BACnet/IP 或 OPC-UA 采集参数与点位映射。
        </p>
      ) : (
        <div className="flex flex-col gap-3">
          {driverConfigs.map((driver, driverIdx) => {
            const driverPrefix = `${baseId}-${driver.draftId}`;
            return (
              <div
                key={driver.draftId}
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
                    onClick={() =>
                      onChange(driverConfigs.filter((item) => item.draftId !== driver.draftId))
                    }
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                  </Button>
                </div>

                <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                  <LabeledInput
                    id={`${driverPrefix}-device-key`}
                    label="本地设备标识"
                    value={driver.deviceKey}
                    placeholder="chiller-01"
                    disabled={disabled}
                    onChangeValue={(deviceKey) => updateDriver(driver.draftId, { deviceKey })}
                  />
                  <LabeledSelect
                    id={`${driverPrefix}-driver-type`}
                    label="协议类型"
                    value={driver.driverType}
                    disabled={disabled}
                    options={DRIVER_TYPE_OPTIONS}
                    onValueChange={(value) => {
                      if (isOneOf(SOUTHBOUND_DRIVER_TYPES, value)) {
                        updateDriver(driver.draftId, { driverType: value });
                      }
                    }}
                  />
                  <LabeledInput
                    id={`${driverPrefix}-endpoint`}
                    label="采集地址"
                    value={driver.endpoint}
                    placeholder={endpointPlaceholderForDriverType(driver.driverType)}
                    disabled={disabled}
                    onChangeValue={(endpoint) => updateDriver(driver.draftId, { endpoint })}
                  />
                </div>

                {driver.driverType === 'MODBUS_TCP' || driver.driverType === 'MODBUS_RTU' ? (
                  <>
                    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                      <LabeledInput
                        id={`${driverPrefix}-uid`}
                        label="从站地址 (UID)"
                        type="number"
                        min={1}
                        max={247}
                        value={driver.unitId}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(unitId) => updateDriver(driver.draftId, { unitId })}
                      />
                      <LabeledInput
                        id={`${driverPrefix}-timeout`}
                        label="超时时间 (ms)"
                        type="number"
                        min={100}
                        max={30000}
                        value={driver.timeoutMs}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(timeoutMs) => updateDriver(driver.draftId, { timeoutMs })}
                      />
                      <LabeledInput
                        id={`${driverPrefix}-retries`}
                        label="重试次数"
                        type="number"
                        min={0}
                        max={10}
                        value={driver.retries}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(retries) => updateDriver(driver.draftId, { retries })}
                      />
                      <LabeledSelect
                        id={`${driverPrefix}-cap`}
                        label="能力档位（可选）"
                        value={driver.capabilityProfile || PROFILE_NONE_VALUE}
                        disabled={disabled}
                        options={CAPABILITY_PROFILE_OPTIONS}
                        onValueChange={(value) => {
                          if (value === PROFILE_NONE_VALUE) {
                            updateDriver(driver.draftId, { capabilityProfile: '' });
                          } else if (isOneOf(MODBUS_CAPABILITY_PROFILES, value)) {
                            updateDriver(driver.draftId, { capabilityProfile: value });
                          }
                        }}
                      />
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
                            updateDriver(driver.draftId, {
                              registers: [...driver.registers, createEmptyModbusRegisterDraft()],
                            })
                          }
                        >
                          <Plus data-icon="inline-start" aria-hidden="true" />
                          新增寄存器
                        </Button>
                      </div>

                      <div className="flex flex-col gap-2">
                        {driver.registers.map((reg, regIdx) => (
                          <RegisterMappingRow
                            key={reg.draftId}
                            reg={reg}
                            driverIndex={driverIdx}
                            regIndex={regIdx}
                            regPrefix={`${baseId}-${reg.draftId}`}
                            disabled={disabled}
                            canDelete={driver.registers.length > 1}
                            onUpdate={(patch) => updateRegister(driver.draftId, reg.draftId, patch)}
                            onDelete={() =>
                              updateDriver(driver.draftId, {
                                registers: driver.registers.filter(
                                  (item) => item.draftId !== reg.draftId,
                                ),
                              })
                            }
                          />
                        ))}
                      </div>
                    </div>
                  </>
                ) : null}

                {driver.driverType === 'BACNET_IP' ? (
                  <>
                    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                      <LabeledInput
                        id={`${driverPrefix}-bac-dev`}
                        label="BACnet 设备实例号"
                        type="number"
                        min={0}
                        max={4194302}
                        value={driver.bacnet.deviceInstance}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(deviceInstance) =>
                          updateBACnet(driver.draftId, { deviceInstance })
                        }
                      />
                      <LabeledInput
                        id={`${driverPrefix}-bac-prio`}
                        label="写优先级 (1–16)"
                        type="number"
                        min={1}
                        max={16}
                        value={driver.bacnet.writePriority}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(writePriority) =>
                          updateBACnet(driver.draftId, { writePriority })
                        }
                      />
                      <LabeledInput
                        id={`${driverPrefix}-timeout`}
                        label="超时时间 (ms)"
                        type="number"
                        min={100}
                        max={30000}
                        value={driver.timeoutMs}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(timeoutMs) => updateDriver(driver.draftId, { timeoutMs })}
                      />
                      <LabeledInput
                        id={`${driverPrefix}-retries`}
                        label="重试次数"
                        type="number"
                        min={0}
                        max={10}
                        value={driver.retries}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(retries) => updateDriver(driver.draftId, { retries })}
                      />
                      <LabeledInput
                        id={`${driverPrefix}-bac-net`}
                        label="网络号（可选）"
                        type="number"
                        min={0}
                        max={65534}
                        value={driver.bacnet.networkNumber}
                        placeholder="直连留空"
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(networkNumber) =>
                          updateBACnet(driver.draftId, { networkNumber })
                        }
                      />
                      <LabeledInput
                        id={`${driverPrefix}-bac-cov`}
                        label="COV 订阅时长 (s)"
                        type="number"
                        min={0}
                        max={86400}
                        value={driver.bacnet.covLifetimeSeconds}
                        placeholder="0 表示轮询"
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(covLifetimeSeconds) =>
                          updateBACnet(driver.draftId, { covLifetimeSeconds })
                        }
                      />
                      <LabeledSelect
                        id={`${driverPrefix}-cap`}
                        label="能力档位（可选）"
                        className="col-span-2"
                        value={driver.capabilityProfile || PROFILE_NONE_VALUE}
                        disabled={disabled}
                        options={CAPABILITY_PROFILE_OPTIONS}
                        onValueChange={(value) => {
                          if (value === PROFILE_NONE_VALUE) {
                            updateDriver(driver.draftId, { capabilityProfile: '' });
                          } else if (isOneOf(MODBUS_CAPABILITY_PROFILES, value)) {
                            updateDriver(driver.draftId, { capabilityProfile: value });
                          }
                        }}
                      />
                    </div>

                    <div className="flex flex-col gap-2 border-t border-divider pt-2.5">
                      <div className="flex items-center justify-between gap-2">
                        <span className="text-xs font-medium text-foreground">
                          BACnet 对象映射（{driver.bacnet.objects.length} 项）
                        </span>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={disabled || driver.bacnet.objects.length >= 256}
                          onClick={() =>
                            updateBACnet(driver.draftId, {
                              objects: [...driver.bacnet.objects, createEmptyBACnetObjectDraft()],
                            })
                          }
                        >
                          <Plus data-icon="inline-start" aria-hidden="true" />
                          新增 BACnet 对象
                        </Button>
                      </div>

                      <div className="flex flex-col gap-2">
                        {driver.bacnet.objects.map((obj, objIdx) => (
                          <BACnetObjectMappingRow
                            key={obj.draftId}
                            obj={obj}
                            driverIndex={driverIdx}
                            objIndex={objIdx}
                            objPrefix={`${baseId}-${obj.draftId}`}
                            disabled={disabled}
                            canDelete={driver.bacnet.objects.length > 1}
                            onUpdate={(patch) =>
                              updateBACnetObject(driver.draftId, obj.draftId, patch)
                            }
                            onDelete={() =>
                              updateBACnet(driver.draftId, {
                                objects: driver.bacnet.objects.filter(
                                  (item) => item.draftId !== obj.draftId,
                                ),
                              })
                            }
                          />
                        ))}
                      </div>
                    </div>
                  </>
                ) : null}

                {driver.driverType === 'OPC_UA' ? (
                  <>
                    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                      <LabeledSelect
                        id={`${driverPrefix}-ua-policy`}
                        label="安全策略"
                        value={driver.opcua.securityPolicy}
                        disabled={disabled}
                        options={SECURITY_POLICY_OPTIONS}
                        onValueChange={(value) => {
                          if (!isOneOf(OPCUA_SECURITY_POLICIES, value)) return;
                          const nextMode =
                            value === 'None'
                              ? ('None' as const)
                              : driver.opcua.securityMode === 'None'
                                ? ('SignAndEncrypt' as const)
                                : driver.opcua.securityMode;
                          updateOPCUA(driver.draftId, {
                            securityPolicy: value,
                            securityMode: nextMode,
                          });
                        }}
                      />
                      <LabeledSelect
                        id={`${driverPrefix}-ua-mode`}
                        label="安全模式"
                        value={driver.opcua.securityMode}
                        disabled={disabled}
                        options={SECURITY_MODE_OPTIONS}
                        onValueChange={(value) => {
                          if (isOneOf(OPCUA_SECURITY_MODES, value)) {
                            updateOPCUA(driver.draftId, { securityMode: value });
                          }
                        }}
                      />
                      <LabeledSelect
                        id={`${driverPrefix}-ua-auth`}
                        label="认证方式"
                        value={driver.opcua.authMode}
                        disabled={disabled}
                        options={AUTH_MODE_OPTIONS}
                        onValueChange={(value) => {
                          if (!isOneOf(OPCUA_AUTH_MODES, value)) return;
                          updateOPCUA(
                            driver.draftId,
                            value === 'ANONYMOUS'
                              ? { authMode: value, username: '', credentialRef: '' }
                              : { authMode: value },
                          );
                        }}
                      />
                      <LabeledInput
                        id={`${driverPrefix}-ua-sub`}
                        label="订阅间隔 (ms)"
                        type="number"
                        min={50}
                        max={60000}
                        value={driver.opcua.subscriptionIntervalMs}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(subscriptionIntervalMs) =>
                          updateOPCUA(driver.draftId, { subscriptionIntervalMs })
                        }
                      />
                      <LabeledInput
                        id={`${driverPrefix}-timeout`}
                        label="超时时间 (ms)"
                        type="number"
                        min={100}
                        max={30000}
                        value={driver.timeoutMs}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(timeoutMs) => updateDriver(driver.draftId, { timeoutMs })}
                      />
                      <LabeledInput
                        id={`${driverPrefix}-retries`}
                        label="重试次数"
                        type="number"
                        min={0}
                        max={10}
                        value={driver.retries}
                        disabled={disabled}
                        tabularNums
                        onChangeValue={(retries) => updateDriver(driver.draftId, { retries })}
                      />
                      <LabeledSelect
                        id={`${driverPrefix}-cap`}
                        label="能力档位（可选）"
                        className="col-span-2"
                        value={driver.capabilityProfile || PROFILE_NONE_VALUE}
                        disabled={disabled}
                        options={CAPABILITY_PROFILE_OPTIONS}
                        onValueChange={(value) => {
                          if (value === PROFILE_NONE_VALUE) {
                            updateDriver(driver.draftId, { capabilityProfile: '' });
                          } else if (isOneOf(MODBUS_CAPABILITY_PROFILES, value)) {
                            updateDriver(driver.draftId, { capabilityProfile: value });
                          }
                        }}
                      />
                    </div>

                    {driver.opcua.authMode === 'USERNAME' ? (
                      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                        <LabeledInput
                          id={`${driverPrefix}-ua-user`}
                          label="用户名"
                          value={driver.opcua.username}
                          placeholder="hvac_edge"
                          disabled={disabled}
                          onChangeValue={(username) => updateOPCUA(driver.draftId, { username })}
                        />
                        <LabeledInput
                          id={`${driverPrefix}-ua-cred`}
                          label="凭据引用（Credential Ref，不存明文密码）"
                          value={driver.opcua.credentialRef}
                          placeholder="edge-secret:opcua-chiller-01"
                          disabled={disabled}
                          onChangeValue={(credentialRef) =>
                            updateOPCUA(driver.draftId, { credentialRef })
                          }
                        />
                      </div>
                    ) : null}

                    <div className="flex flex-col gap-2 border-t border-divider pt-2.5">
                      <div className="flex items-center justify-between gap-2">
                        <span className="text-xs font-medium text-foreground">
                          OPC-UA 节点映射（{driver.opcua.nodes.length} 项）
                        </span>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={disabled || driver.opcua.nodes.length >= 256}
                          onClick={() =>
                            updateOPCUA(driver.draftId, {
                              nodes: [...driver.opcua.nodes, createEmptyOPCUANodeDraft()],
                            })
                          }
                        >
                          <Plus data-icon="inline-start" aria-hidden="true" />
                          新增 OPC-UA 节点
                        </Button>
                      </div>

                      <div className="flex flex-col gap-2">
                        {driver.opcua.nodes.map((node, nodeIdx) => (
                          <OPCUANodeMappingRow
                            key={node.draftId}
                            node={node}
                            driverIndex={driverIdx}
                            nodeIndex={nodeIdx}
                            nodePrefix={`${baseId}-${node.draftId}`}
                            disabled={disabled}
                            canDelete={driver.opcua.nodes.length > 1}
                            onUpdate={(patch) =>
                              updateOPCUANode(driver.draftId, node.draftId, patch)
                            }
                            onDelete={() =>
                              updateOPCUA(driver.draftId, {
                                nodes: driver.opcua.nodes.filter(
                                  (item) => item.draftId !== node.draftId,
                                ),
                              })
                            }
                          />
                        ))}
                      </div>
                    </div>
                  </>
                ) : null}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
