import { Plus } from 'lucide-react';
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
  createEmptyBACnetObjectDraft,
  createEmptyOPCUANodeDraft,
  type GatewayBACnetObjectDraft,
  type GatewayBACnetSpecDraft,
  type GatewayModbusDriverDraft,
  type GatewayOPCUANodeDraft,
  type GatewayOPCUASpecDraft,
} from './gateway-config-shadow-model';
import {
  LabeledInput,
  LabeledSelect,
  type SelectOption,
  mapLabeledOptions,
} from './GatewayRegisterMappingRow';

export const PROFILE_NONE_VALUE = '__NONE__';

export const CAPABILITY_PROFILE_OPTIONS: readonly SelectOption[] = [
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

export function BACnetDriverFields({
  baseId,
  driverPrefix,
  driverIdx,
  driver,
  disabled,
  updateDriver,
  updateBACnet,
  updateBACnetObject,
}: {
  readonly baseId: string;
  readonly driverPrefix: string;
  readonly driverIdx: number;
  readonly driver: GatewayModbusDriverDraft;
  readonly disabled: boolean;
  readonly updateDriver: (driverId: string, patch: Partial<GatewayModbusDriverDraft>) => void;
  readonly updateBACnet: (driverId: string, patch: Partial<GatewayBACnetSpecDraft>) => void;
  readonly updateBACnetObject: (
    driverId: string,
    objId: string,
    patch: Partial<GatewayBACnetObjectDraft>,
  ) => void;
}) {
  return (
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
          onChangeValue={(deviceInstance) => updateBACnet(driver.draftId, { deviceInstance })}
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
          onChangeValue={(writePriority) => updateBACnet(driver.draftId, { writePriority })}
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
          onChangeValue={(networkNumber) => updateBACnet(driver.draftId, { networkNumber })}
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
              onUpdate={(patch) => updateBACnetObject(driver.draftId, obj.draftId, patch)}
              onDelete={() =>
                updateBACnet(driver.draftId, {
                  objects: driver.bacnet.objects.filter((item) => item.draftId !== obj.draftId),
                })
              }
            />
          ))}
        </div>
      </div>
    </>
  );
}

export function OPCUADriverFields({
  baseId,
  driverPrefix,
  driverIdx,
  driver,
  disabled,
  updateDriver,
  updateOPCUA,
  updateOPCUANode,
}: {
  readonly baseId: string;
  readonly driverPrefix: string;
  readonly driverIdx: number;
  readonly driver: GatewayModbusDriverDraft;
  readonly disabled: boolean;
  readonly updateDriver: (driverId: string, patch: Partial<GatewayModbusDriverDraft>) => void;
  readonly updateOPCUA: (driverId: string, patch: Partial<GatewayOPCUASpecDraft>) => void;
  readonly updateOPCUANode: (
    driverId: string,
    nodeId: string,
    patch: Partial<GatewayOPCUANodeDraft>,
  ) => void;
}) {
  return (
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
            onChangeValue={(credentialRef) => updateOPCUA(driver.draftId, { credentialRef })}
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
              onUpdate={(patch) => updateOPCUANode(driver.draftId, node.draftId, patch)}
              onDelete={() =>
                updateOPCUA(driver.draftId, {
                  nodes: driver.opcua.nodes.filter((item) => item.draftId !== node.draftId),
                })
              }
            />
          ))}
        </div>
      </div>
    </>
  );
}
