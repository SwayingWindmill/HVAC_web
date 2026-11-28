import { useId } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { isOneOf } from '@/lib/typed';
import {
  MODBUS_CAPABILITY_PROFILES,
  SOUTHBOUND_DRIVER_TYPES,
  SOUTHBOUND_DRIVER_TYPE_LABELS,
  createEmptyModbusDriverDraft,
  createEmptyModbusRegisterDraft,
  type GatewayBACnetObjectDraft,
  type GatewayBACnetSpecDraft,
  type GatewayModbusDriverDraft,
  type GatewayModbusRegisterDraft,
  type GatewayOPCUANodeDraft,
  type GatewayOPCUASpecDraft,
} from './gateway-config-shadow-model';
import {
  BACnetDriverFields,
  CAPABILITY_PROFILE_OPTIONS,
  OPCUADriverFields,
  PROFILE_NONE_VALUE,
} from './GatewayProtocolDriverFields';
import {
  LabeledInput,
  LabeledSelect,
  RegisterMappingRow,
  mapLabeledOptions,
} from './GatewayRegisterMappingRow';

const DRIVER_TYPE_OPTIONS = mapLabeledOptions(
  SOUTHBOUND_DRIVER_TYPES,
  SOUTHBOUND_DRIVER_TYPE_LABELS,
);

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
                  <BACnetDriverFields
                    baseId={baseId}
                    driverPrefix={driverPrefix}
                    driverIdx={driverIdx}
                    driver={driver}
                    disabled={disabled}
                    updateDriver={updateDriver}
                    updateBACnet={updateBACnet}
                    updateBACnetObject={updateBACnetObject}
                  />
                ) : null}

                {driver.driverType === 'OPC_UA' ? (
                  <OPCUADriverFields
                    baseId={baseId}
                    driverPrefix={driverPrefix}
                    driverIdx={driverIdx}
                    driver={driver}
                    disabled={disabled}
                    updateDriver={updateDriver}
                    updateOPCUA={updateOPCUA}
                    updateOPCUANode={updateOPCUANode}
                  />
                ) : null}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
