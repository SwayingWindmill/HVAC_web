import type {
  GatewayBACnetDriverSpec,
  GatewayBACnetObjectMapping,
  GatewayModbusDriverConfig,
  GatewayModbusRegisterMapping,
} from '../../api/generated/platformGateway.gen.ts';
import { isOneOf } from '../../lib/typed.ts';
import {
  BACNET_OBJECT_TYPES,
  BACNET_OBJECT_TYPE_LABELS,
  DRIVER_CHANNEL_DATA_TYPES,
  MODBUS_ENDIAN_OPTIONS,
  MODBUS_POLL_PRIORITIES,
  MODBUS_RAW_TYPES,
  MODBUS_RAW_TYPE_LABELS,
  POINT_CODE_PATTERN,
  SAFETY_BOUNDARY_OPTIONS,
  isBACnetInputOnlyObjectType,
  normalizeZero,
  type DriverChannelDataType,
  type DriverSafetyBoundary,
  type GatewayBACnetSpecDraft,
  type GatewayModbusDriverDraft,
  type ModbusCapabilityProfile,
} from './gateway-driver-constants.ts';
import {
  defaultRegisterCountForRawType,
  parseModbusEnumMap,
} from './gateway-driver-drafts.ts';
import {
  defaultBACnetDataType,
  isHardRealtimeProtectionPoint,
  isValidHostPortEndpoint,
} from './gateway-driver-safety.ts';

export function parseIntegerInRange(
  raw: string,
  min: number,
  max: number,
  label: string,
): { readonly ok: true; readonly value: number } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: false, error: `${label}不能为空` };
  }
  const value = Number(trimmed);
  if (!Number.isInteger(value) || value < min || value > max) {
    return { ok: false, error: `${label}须为 ${min} 至 ${max} 之间的整数` };
  }
  return { ok: true, value };
}

export function parseNumberInRange(
  raw: string,
  min: number,
  max: number,
  unit: string,
  label: string,
): { readonly ok: true; readonly value: number } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: false, error: `${label}不能为空` };
  }
  const value = Number(trimmed);
  if (!Number.isFinite(value) || value < min || value > max) {
    return { ok: false, error: `${label}须在 ${min} 至 ${max}${unit}范围内` };
  }
  return { ok: true, value };
}

export function parseFiniteNumber(
  raw: string,
  label: string,
): { readonly ok: true; readonly value: number } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { ok: false, error: `${label}不能为空` };
  }
  const value = Number(trimmed);
  if (!Number.isFinite(value)) {
    return { ok: false, error: `${label}须为有效数字` };
  }
  return { ok: true, value };
}

export function validateCommonPointMeta(
  rowPrefix: string,
  driverPrefix: string,
  rawPointCode: string,
  rawSemantic: string,
  rawSafetyBoundary: DriverSafetyBoundary,
  rawUnit: string,
  writable: boolean,
  seenPointCodes: Set<string>,
  seenSemantics: Set<string>,
):
  | {
      readonly ok: true;
      readonly pointCode: string;
      readonly semanticChannel: string;
      readonly safetyBoundary: '' | 'SOFT_REALTIME' | 'HARD_REALTIME_SAFETY';
      readonly unit: string;
    }
  | { readonly ok: false; readonly error: string } {
  const pointCode = rawPointCode.trim();
  const semanticChannel = rawSemantic.trim();
  const unit = rawUnit.trim();

  if (!POINT_CODE_PATTERN.test(pointCode)) {
    return {
      ok: false,
      error: `${rowPrefix}的点位编码 "${pointCode || '(空)'}" 须以小写字母开头，仅含小写字母、数字和下划线`,
    };
  }
  if (seenPointCodes.has(pointCode)) {
    return { ok: false, error: `${driverPrefix}中点位编码 "${pointCode}" 重复` };
  }
  seenPointCodes.add(pointCode);

  if (semanticChannel.length > 64 || semanticChannel.includes('/')) {
    return { ok: false, error: `${rowPrefix}的语义通道不能超过 64 个字符且不能包含 "/"` };
  }
  if (semanticChannel) {
    if (seenSemantics.has(semanticChannel)) {
      return { ok: false, error: `${driverPrefix}中语义通道 "${semanticChannel}" 重复` };
    }
    seenSemantics.add(semanticChannel);
  }

  let safetyBoundary: '' | 'SOFT_REALTIME' | 'HARD_REALTIME_SAFETY' = '';
  if (rawSafetyBoundary) {
    if (!isOneOf(SAFETY_BOUNDARY_OPTIONS, rawSafetyBoundary)) {
      return { ok: false, error: `${rowPrefix}的安全边界类型无效` };
    }
    safetyBoundary = rawSafetyBoundary;
  }

  if (writable && isHardRealtimeProtectionPoint(pointCode, semanticChannel, safetyBoundary)) {
    return {
      ok: false,
      error: `${rowPrefix}（${pointCode}）属于 EDGE-003 硬实时安全保护点位，禁止配置为可写`,
    };
  }

  if (unit.length > 32) {
    return { ok: false, error: `${rowPrefix}的单位不能超过 32 个字符` };
  }

  return { ok: true, pointCode, semanticChannel, safetyBoundary, unit };
}

export function validateModbusDriverEntry(
  prefix: string,
  deviceKey: string,
  driverType: 'MODBUS_TCP' | 'MODBUS_RTU',
  endpoint: string,
  capabilityProfile: ModbusCapabilityProfile | undefined,
  timeoutMs: number,
  retries: number,
  driver: GatewayModbusDriverDraft,
):
  | { readonly ok: true; readonly config: GatewayModbusDriverConfig }
  | { readonly ok: false; readonly error: string } {
  if (driverType === 'MODBUS_TCP' && !isValidHostPortEndpoint(endpoint)) {
    return { ok: false, error: `${prefix}的采集地址须为 host:port 格式且端口在 1 至 65535 之间` };
  }

  const unitIdRes = parseIntegerInRange(driver.unitId, 1, 247, `${prefix}的从站地址 (UID)`);
  if (!unitIdRes.ok) return unitIdRes;

  if (driver.registers.length === 0 || driver.registers.length > 256) {
    return { ok: false, error: `${prefix}至少需要配置 1 条寄存器映射，最多 256 条` };
  }

  const seenPointCodes = new Set<string>();
  const seenBitKeys = new Set<string>();
  const registers: GatewayModbusRegisterMapping[] = [];
  for (let rIndex = 0; rIndex < driver.registers.length; rIndex += 1) {
    const reg = driver.registers[rIndex]!;
    const regPrefix = `${prefix}第 ${rIndex + 1} 条寄存器`;
    const pointCode = reg.pointCode.trim();
    const semanticChannel = reg.semanticChannel.trim();

    if (!POINT_CODE_PATTERN.test(pointCode)) {
      return {
        ok: false,
        error: `${regPrefix}的点位编码 "${pointCode || '(空)'}" 须以小写字母开头，仅含小写字母、数字和下划线`,
      };
    }
    if (seenPointCodes.has(pointCode)) {
      return { ok: false, error: `${prefix}中点位编码 "${pointCode}" 重复` };
    }
    seenPointCodes.add(pointCode);

    if (semanticChannel.length > 64) {
      return { ok: false, error: `${regPrefix}的语义通道不能超过 64 个字符` };
    }

    const functionCode = reg.functionCode === '4' ? (4 as const) : (3 as const);
    const writeFunctionCode =
      reg.writeFunctionCode === '6'
        ? (6 as const)
        : reg.writeFunctionCode === '16'
          ? (16 as const)
          : undefined;

    if (functionCode === 4 && writeFunctionCode !== undefined) {
      return { ok: false, error: `${regPrefix}使用输入寄存器 (FC4) 时为只读，不能配置写功能码` };
    }

    if (!isOneOf(MODBUS_RAW_TYPES, reg.rawType)) {
      return { ok: false, error: `${regPrefix}的数据类型无效` };
    }

    const addressRes = parseIntegerInRange(reg.address, 0, 65535, `${regPrefix}的寄存器地址`);
    if (!addressRes.ok) return addressRes;

    const countRes = parseIntegerInRange(reg.registerCount, 1, 4, `${regPrefix}的寄存器数量`);
    if (!countRes.ok) return countRes;

    const expectedCount = Number(defaultRegisterCountForRawType(reg.rawType));
    if (countRes.value !== expectedCount) {
      return {
        ok: false,
        error: `${regPrefix}的寄存器数量须为 ${expectedCount}（匹配${MODBUS_RAW_TYPE_LABELS[reg.rawType]}）`,
      };
    }
    if (addressRes.value + countRes.value > 65536) {
      return {
        ok: false,
        error: `${regPrefix}的寄存器地址与数量之和不能超过 65536`,
      };
    }
    if (writeFunctionCode === 6 && countRes.value !== 1) {
      return {
        ok: false,
        error: `${regPrefix}使用单写 (FC6) 时仅支持 1 个寄存器`,
      };
    }

    if (!isOneOf(MODBUS_ENDIAN_OPTIONS, reg.byteOrder)) {
      return { ok: false, error: `${regPrefix}的字节序无效` };
    }
    if (!isOneOf(MODBUS_ENDIAN_OPTIONS, reg.wordOrder)) {
      return { ok: false, error: `${regPrefix}的字序无效` };
    }
    if (!isOneOf(MODBUS_POLL_PRIORITIES, reg.pollPriority)) {
      return { ok: false, error: `${regPrefix}的轮询优先级无效` };
    }

    let bitIndex: number | undefined;
    if (reg.rawType === 'BIT_BOOL') {
      const bitRes = parseIntegerInRange(reg.bitIndex, 0, 15, `${regPrefix}的位布尔索引`);
      if (!bitRes.ok) return bitRes;
      bitIndex = bitRes.value;
      const bitKey = `${functionCode}:${addressRes.value}:${bitIndex}`;
      if (seenBitKeys.has(bitKey)) {
        return {
          ok: false,
          error: `${prefix}中读功能码 FC${functionCode} 地址 ${addressRes.value} 第 ${bitIndex} 位的位布尔映射重复`,
        };
      }
      seenBitKeys.add(bitKey);
    } else if (reg.bitIndex.trim() !== '') {
      return { ok: false, error: `${regPrefix}仅位布尔类型可配置位索引` };
    }

    const scaleRes = parseFiniteNumber(reg.scale, `${regPrefix}的倍率`);
    if (!scaleRes.ok) return scaleRes;
    if (scaleRes.value === 0) {
      return { ok: false, error: `${regPrefix}的倍率不能为 0` };
    }

    const offsetRes = parseFiniteNumber(reg.offset, `${regPrefix}的偏置`);
    if (!offsetRes.ok) return offsetRes;

    let enumMap: Record<string, string> | undefined;
    if (reg.rawType === 'ENUM16') {
      const enumRes = parseModbusEnumMap(reg.enumMapText);
      if (!enumRes.ok) {
        return { ok: false, error: `${regPrefix}：${enumRes.error}` };
      }
      enumMap = enumRes.enumMap;
    } else if (reg.enumMapText.trim() !== '') {
      return { ok: false, error: `${regPrefix}仅 16 位枚举类型可配置枚举映射` };
    }

    registers.push({
      pointCode,
      ...(semanticChannel ? { semanticChannel } : {}),
      functionCode,
      ...(writeFunctionCode !== undefined ? { writeFunctionCode } : {}),
      address: addressRes.value,
      registerCount: countRes.value,
      rawType: reg.rawType,
      byteOrder: reg.byteOrder,
      wordOrder: reg.wordOrder,
      ...(bitIndex !== undefined ? { bitIndex } : {}),
      scale: normalizeZero(scaleRes.value),
      offset: normalizeZero(offsetRes.value),
      pollPriority: reg.pollPriority,
      ...(enumMap ? { enumMap } : {}),
    });
  }

  return {
    ok: true,
    config: {
      deviceKey,
      driverType,
      endpoint,
      unitId: unitIdRes.value,
      ...(capabilityProfile ? { capabilityProfile } : {}),
      timeoutMs,
      retries,
      registers,
    },
  };
}

export function validateBACnetDriverEntry(
  prefix: string,
  deviceKey: string,
  endpoint: string,
  capabilityProfile: ModbusCapabilityProfile | undefined,
  timeoutMs: number,
  retries: number,
  spec: GatewayBACnetSpecDraft,
):
  | { readonly ok: true; readonly config: GatewayModbusDriverConfig }
  | { readonly ok: false; readonly error: string } {
  if (!isValidHostPortEndpoint(endpoint)) {
    return { ok: false, error: `${prefix}的 BACnet/IP 地址须为 host:port 格式且端口在 1 至 65535 之间` };
  }

  const deviceInstanceRes = parseIntegerInRange(
    spec.deviceInstance,
    0,
    4_194_302,
    `${prefix}的 BACnet 设备实例号`,
  );
  if (!deviceInstanceRes.ok) return deviceInstanceRes;

  let networkNumber: number | undefined;
  if (spec.networkNumber.trim() !== '') {
    const netRes = parseIntegerInRange(spec.networkNumber, 0, 65534, `${prefix}的 BACnet 网络号`);
    if (!netRes.ok) return netRes;
    if (netRes.value > 0) networkNumber = netRes.value;
  }

  let covLifetimeSeconds: number | undefined;
  if (spec.covLifetimeSeconds.trim() !== '') {
    const covRes = parseIntegerInRange(
      spec.covLifetimeSeconds,
      0,
      86400,
      `${prefix}的 COV 订阅时长`,
    );
    if (!covRes.ok) return covRes;
    if (covRes.value > 0) covLifetimeSeconds = covRes.value;
  }

  let writePriority = 8;
  if (spec.writePriority.trim() !== '') {
    const prioRes = parseIntegerInRange(spec.writePriority, 1, 16, `${prefix}的写优先级`);
    if (!prioRes.ok) return prioRes;
    writePriority = prioRes.value;
  }

  if (spec.objects.length === 0 || spec.objects.length > 256) {
    return { ok: false, error: `${prefix}至少需要配置 1 个 BACnet 对象，最多 256 个` };
  }

  const seenPointCodes = new Set<string>();
  const seenSemantics = new Set<string>();
  const seenObjects = new Set<string>();
  const objects: GatewayBACnetObjectMapping[] = [];

  for (let oIndex = 0; oIndex < spec.objects.length; oIndex += 1) {
    const obj = spec.objects[oIndex]!;
    const rowPrefix = `${prefix}第 ${oIndex + 1} 个 BACnet 对象`;
    const meta = validateCommonPointMeta(
      rowPrefix,
      prefix,
      obj.pointCode,
      obj.semanticChannel,
      obj.safetyBoundary,
      obj.unit,
      obj.writable,
      seenPointCodes,
      seenSemantics,
    );
    if (!meta.ok) return meta;

    if (!isOneOf(BACNET_OBJECT_TYPES, obj.objectType)) {
      return { ok: false, error: `${rowPrefix}的对象类型无效` };
    }
    const instRes = parseIntegerInRange(obj.objectInstance, 0, 4_194_302, `${rowPrefix}的对象实例号`);
    if (!instRes.ok) return instRes;

    let propertyId = 85;
    if (obj.propertyId.trim() !== '') {
      const propRes = parseIntegerInRange(obj.propertyId, 0, 4_194_303, `${rowPrefix}的属性 ID`);
      if (!propRes.ok) return propRes;
      propertyId = propRes.value;
    }

    if (obj.writable) {
      if (isBACnetInputOnlyObjectType(obj.objectType)) {
        return {
          ok: false,
          error: `${rowPrefix}（${BACNET_OBJECT_TYPE_LABELS[obj.objectType]}）为只读对象，不能勾选可写`,
        };
      }
      if (propertyId !== 85) {
        return {
          ok: false,
          error: `${rowPrefix}配置可写时，属性 ID 须为 85（Present_Value）`,
        };
      }
    }

    const objKey = `${obj.objectType}:${instRes.value}:${propertyId}`;
    if (seenObjects.has(objKey)) {
      return {
        ok: false,
        error: `${prefix}中存在重复的 BACnet 对象映射（${obj.objectType}:${instRes.value} 属性 ${propertyId}）`,
      };
    }
    seenObjects.add(objKey);

    if (!isOneOf(MODBUS_POLL_PRIORITIES, obj.pollPriority)) {
      return { ok: false, error: `${rowPrefix}的轮询优先级无效` };
    }

    let enumMap: Record<string, string> | undefined;
    if (obj.enumMapText.trim() !== '') {
      const enumRes = parseModbusEnumMap(obj.enumMapText);
      if (!enumRes.ok) {
        return { ok: false, error: `${rowPrefix}：${enumRes.error}` };
      }
      enumMap = enumRes.enumMap;
    }

    let dataType: DriverChannelDataType = enumMap ? 'STRING' : defaultBACnetDataType(obj.objectType);
    if (obj.dataType) {
      if (!isOneOf(DRIVER_CHANNEL_DATA_TYPES, obj.dataType)) {
        return { ok: false, error: `${rowPrefix}的数据类型无效` };
      }
      dataType = obj.dataType;
    }

    const scaleRes = parseFiniteNumber(obj.scale, `${rowPrefix}的倍率`);
    if (!scaleRes.ok) return scaleRes;
    if (scaleRes.value === 0) {
      return { ok: false, error: `${rowPrefix}的倍率不能为 0` };
    }

    const offsetRes = parseFiniteNumber(obj.offset, `${rowPrefix}的偏置`);
    if (!offsetRes.ok) return offsetRes;

    objects.push({
      pointCode: meta.pointCode,
      ...(meta.semanticChannel ? { semanticChannel: meta.semanticChannel } : {}),
      ...(meta.safetyBoundary ? { safetyBoundary: meta.safetyBoundary } : {}),
      objectType: obj.objectType,
      objectInstance: instRes.value,
      propertyId,
      dataType,
      ...(obj.writable ? { writable: true } : {}),
      ...(obj.covSubscribe ? { covSubscribe: true } : {}),
      scale: normalizeZero(scaleRes.value),
      offset: normalizeZero(offsetRes.value),
      pollPriority: obj.pollPriority,
      ...(meta.unit ? { unit: meta.unit } : {}),
      ...(enumMap ? { enumMap } : {}),
    });
  }

  const bacnetSpec: GatewayBACnetDriverSpec = {
    deviceInstance: deviceInstanceRes.value,
    ...(networkNumber !== undefined ? { networkNumber } : {}),
    ...(covLifetimeSeconds !== undefined ? { covLifetimeSeconds } : {}),
    writePriority,
    objects,
  };

  return {
    ok: true,
    config: {
      deviceKey,
      driverType: 'BACNET_IP',
      endpoint,
      ...(capabilityProfile ? { capabilityProfile } : {}),
      timeoutMs,
      retries,
      bacnet: bacnetSpec,
    },
  };
}
