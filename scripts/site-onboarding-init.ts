import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { checkSiteFile, type OnboardingProfile } from './site-onboarding.mjs';

export interface SiteScaffoldOptions {
  readonly code: string;
  readonly name: string;
  readonly prefix: string;
  readonly tenantId?: string;
  readonly timezone?: string;
  readonly chillers?: number;
  readonly chwp?: number;
  readonly cwp?: number;
  readonly towers?: number;
  readonly floors?: number;
  readonly width?: number;
  readonly depth?: number;
}

export interface GeneratedSiteBundle {
  readonly site: Record<string, unknown>;
  readonly edgePlant: Record<string, unknown>;
  readonly alarmRules: Record<string, unknown>;
}

const DEFAULT_TENANT_ID = '01900000-0000-7000-8000-000000000001';

function siteUuid(prefix: string, kind: string, index: number): string {
  return `${prefix}-${kind}-7000-8000-${index.toString(16).padStart(12, '0')}`;
}

function assertPositiveInt(value: number, label: string, min: number, max: number): number {
  if (!Number.isInteger(value) || value < min || value > max) {
    throw new Error(`${label} must be an integer in ${min}..${max}, got ${value}`);
  }
  return value;
}

/** Generate a validated site.json (with templates + buildingPlan), edge-plant.json, and alarm-rules.json. */
export function generateSiteBundle(options: SiteScaffoldOptions): GeneratedSiteBundle {
  const code = options.code.trim();
  const name = options.name.trim();
  const prefix = options.prefix.trim().toLowerCase();
  if (!/^[a-z][a-z0-9-]{1,63}$/.test(code)) throw new Error(`invalid site code ${code}`);
  if (!name || name.length > 128) throw new Error('site name is required');
  if (!/^[0-9a-f]{8}$/.test(prefix)) throw new Error(`prefix must be 8 lowercase hex characters, got ${prefix}`);

  const tenantId = options.tenantId?.trim() || DEFAULT_TENANT_ID;
  const timezone = options.timezone?.trim() || 'Asia/Shanghai';
  const chillers = assertPositiveInt(options.chillers ?? 2, 'chillers', 1, 8);
  const chwp = assertPositiveInt(options.chwp ?? chillers, 'chwp', 1, 8);
  const cwp = assertPositiveInt(options.cwp ?? chillers, 'cwp', 1, 8);
  const towers = assertPositiveInt(options.towers ?? chillers, 'towers', 1, 8);
  const floorsCount = assertPositiveInt(options.floors ?? 2, 'floors', 1, 20);
  const width = options.width ?? 24;
  const depth = options.depth ?? 16;
  if (!Number.isFinite(width) || width < 10 || width > 1000) throw new Error('width must be in [10, 1000]');
  if (!Number.isFinite(depth) || depth < 10 || depth > 1000) throw new Error('depth must be in [10, 1000]');

  const serviceX = Number(Math.max(4, Math.min(6, width * 0.22)).toFixed(1));
  const coreX0 = Number((width - serviceX).toFixed(1));
  const shaftX0 = Number((width - 2).toFixed(1));
  const midZ0 = Number((depth * 0.45).toFixed(1));
  const midZ1 = Number((depth * 0.58).toFixed(1));

  const buildingFloors: Record<string, unknown>[] = [
    {
      level: 1,
      label: '1F 大厅',
      height: 4.5,
      rooms: [
        { id: '1-core', name: '楼梯 · 电梯', kind: 'core', x: [coreX0, width], z: [0, Number((depth * 0.32).toFixed(1))] },
        { id: '1-shaft', name: '管井', kind: 'shaft', x: [shaftX0, width], z: [Number((depth * 0.32).toFixed(1)), midZ0] },
        { id: '1-ahu', name: '空调机房', kind: 'plantRoom', x: [coreX0, width], z: [midZ0, depth] },
        { id: '1-hall', name: '大厅', kind: 'hall', x: [0, coreX0], z: [0, depth] },
      ],
    },
  ];
  for (let level = 2; level <= floorsCount; level += 1) {
    const midX = Number((coreX0 / 2).toFixed(1));
    buildingFloors.push({
      level,
      label: `${level}F 房间`,
      height: 4.0,
      rooms: [
        { id: `${level}-core`, name: '楼梯 · 电梯', kind: 'core', x: [coreX0, width], z: [0, Number((depth * 0.32).toFixed(1))] },
        { id: `${level}-shaft`, name: '管井', kind: 'shaft', x: [shaftX0, width], z: [Number((depth * 0.32).toFixed(1)), midZ0] },
        { id: `${level}-corridor`, name: '走廊', kind: 'corridor', x: [0, shaftX0], z: [midZ0, midZ1] },
        { id: `${level}-r1`, name: `${level}01 房间`, kind: 'private', x: [0, midX], z: [0, midZ0] },
        { id: `${level}-r2`, name: `${level}02 房间`, kind: 'private', x: [midX, coreX0], z: [0, midZ0] },
        { id: `${level}-r3`, name: `${level}03 房间`, kind: 'smallPrivate', x: [0, midX], z: [midZ1, depth] },
        { id: `${level}-r4`, name: `${level}04 房间`, kind: 'smallPrivate', x: [midX, width], z: [midZ1, depth] },
      ],
    });
  }

  const assets: Record<string, unknown>[] = [];
  const assetRelationships: Record<string, unknown>[] = [];
  for (let i = 1; i <= chillers; i += 1) {
    assets.push({ id: siteUuid(prefix, '3000', i), code: `chiller-${i}`, name: `${i} 号冷水机组`, type: 'CHILLER' });
  }
  const addSupportAssets = (count: number, offset: number, prefixCode: string, label: string, type: string) => {
    for (let i = 1; i <= count; i += 1) {
      const codeItem = `${prefixCode}-${i}`;
      assets.push({ id: siteUuid(prefix, '3000', offset + i), code: codeItem, name: `${i} 号${label}`, type });
      const targetChiller = ((i - 1) % chillers) + 1;
      assetRelationships.push({ from: codeItem, to: `chiller-${targetChiller}`, role: 'SERVES' });
    }
  };
  addSupportAssets(chwp, 100, 'chwp', '冷冻水泵', 'CHILLED_WATER_PUMP');
  addSupportAssets(cwp, 200, 'cwp', '冷却水泵', 'COOLING_WATER_PUMP');
  addSupportAssets(towers, 300, 'ct', '冷却塔', 'COOLING_TOWER');

  const devices: Record<string, unknown>[] = [];
  const branchMeters: string[] = [];
  const edgeMeterDevices: Record<string, unknown>[] = [];
  let devIdx = 1;

  for (let i = 1; i <= chillers; i += 1) {
    const meterSourceKey = `ammeter-unit${i}`;
    const ctrlSourceKey = `ctrl-chiller-${i}`;
    branchMeters.push(meterSourceKey);
    devices.push({
      id: siteUuid(prefix, '4000', devIdx++),
      sourceKey: meterSourceKey,
      code: `meter-u${i}`,
      name: `${i} 号机组电表`,
      asset: siteUuid(prefix, '3000', i),
      template: 'hvac.power-meter.acrel-dtsd1352',
      pointOverrides: {
        power_total: { address: `UID${i} 保持寄存器 36` },
        active_energy_total: { address: `UID${i} 保持寄存器 60` },
      },
    });
    devices.push({
      id: siteUuid(prefix, '4000', devIdx++),
      sourceKey: ctrlSourceKey,
      code: `ctrl-chiller-${i}`,
      name: `${i} 号机组控制器`,
      asset: siteUuid(prefix, '3000', i),
      template: 'hvac.chiller.modbus-controller',
    });
    edgeMeterDevices.push({
      unitId: i,
      deviceKey: meterSourceKey,
      controllerDeviceKey: ctrlSourceKey,
      powerRegisterAddr: 36,
      powerScale: 0.1,
      energyRegisterAddr: 60,
      energyScale: 1,
      startKw: 12.75,
      stopKw: 5,
    });
  }

  devices.push({
    id: siteUuid(prefix, '4000', devIdx++),
    sourceKey: 'ammeter-total',
    code: 'meter-total',
    name: '冷站电表总有功功率汇总',
    template: 'hvac.power-meter.acrel-dtsd1352',
    pointOverrides: {
      power_total: { sourceKey: 'hvac_meter.active_power', critical: false },
      active_energy_total: { omit: true },
      run_state: { omit: true },
    },
  });
  devices.push({
    id: siteUuid(prefix, '4000', devIdx++),
    sourceKey: 'waterflow',
    code: 'waterflow',
    name: '冷冻水总管流量计',
    template: 'hvac.btu-meter.ultrasonic',
    pointOverrides: {
      supply_water_temperature: { omit: true },
      return_water_temperature: { omit: true },
      instant_cooling_capacity: { omit: true },
      accumulated_cooling_energy: { omit: true },
    },
  });
  devices.push({
    id: siteUuid(prefix, '4000', devIdx++),
    sourceKey: 'chwp-main',
    code: 'chwp-main',
    name: '冷冻泵运行与流量映射',
    asset: siteUuid(prefix, '3000', 101),
    template: 'hvac.pump.vfd-modbus',
    pointOverrides: {
      frequency: { omit: true },
      power_total: { omit: true },
      set_frequency: { omit: true },
    },
    morePoints: [
      { code: 'flow_rate', name: '冷冻水总管流量', unit: 'm3/h', pointType: 'TELEMETRY', valueType: 'NUMBER', sourceKey: 'chwp.flow_rate', semanticRole: 'WATER_FLOW' },
    ],
  });
  devices.push({
    id: siteUuid(prefix, '4000', devIdx++),
    sourceKey: 'temp',
    code: 'temp-uid20',
    name: '温度采集模块（UID 20）',
    template: 'hvac.temp-module.8ch',
    pointOverrides: {
      t1: { name: '冷冻水总供水温度', address: 'UID20 输入寄存器 30401' },
      t2: { name: '冷冻水总回水温度', address: 'UID20 输入寄存器 30402' },
      t3: { name: '冷却水总进水温度', address: 'UID20 输入寄存器 30403' },
      t4: { name: '冷却水总出水温度', address: 'UID20 输入寄存器 30404' },
      t5: { name: '室外环境温度', address: 'UID20 输入寄存器 30405' },
      t6: { name: '大厅室内温度', address: 'UID20 输入寄存器 30406' },
      t7: { status: 'INACTIVE' },
      t8: { status: 'INACTIVE' },
    },
  });
  devices.push({
    id: siteUuid(prefix, '4000', devIdx++),
    sourceKey: 'btu-calc',
    code: 'btu-calc',
    name: '冷量计算设备',
    template: 'hvac.btu-meter.ultrasonic',
  });

  const site: Record<string, unknown> = {
    tenantId,
    site: {
      id: siteUuid(prefix, '1000', 1),
      code,
      name,
      timezone,
      buildingPlan: {
        name,
        width,
        depth,
        floors: buildingFloors,
      },
    },
    space: {
      id: siteUuid(prefix, '2000', 1),
      code: 'plant-room',
      name: '空调机房',
      type: 'PLANT_ROOM',
    },
    gateway: {
      id: siteUuid(prefix, '4000', 99),
      code: `${code}-edge`,
      name: `${name}边缘网关`,
    },
    devices,
    assets,
    assetRelationships,
    energy: {
      plantName: '空调机房',
      totalMeter: { kind: 'sum-of-sub-meters' },
      branchMeters,
      coolingMeter: 'btu-calc',
      tariff: {
        code: 'grid_import_tou',
        name: '工商业分时电价（示例）',
        timezone,
        rates: {
          PEAK: { energyRatePerKWh: 1.03 },
          FLAT: { energyRatePerKWh: 0.62 },
          VALLEY: { energyRatePerKWh: 0.24 },
        },
        slices: [
          ['VALLEY', 0, 480],
          ['FLAT', 480, 600],
          ['PEAK', 600, 720],
          ['FLAT', 720, 840],
          ['PEAK', 840, 1140],
          ['FLAT', 1140, 1440],
        ],
      },
    },
    intelligence: {
      alarmRules: `${code}.alarm-rules.json`,
      diagnosis: {
        lowDeltaT: {
          asset: 'chiller-1',
          device: 'btu-calc',
          cron: '*/15 * * * *',
          enabled: false,
          firstRevision: { minimumDeltaTC: 1.5, windowMinutes: 15 },
          note: '新站点首次接入默认暂不启用，现场标定后改 enabled 为 true。',
        },
      },
    },
  };

  const edgePlant: Record<string, unknown> = {
    enabled: true,
    serialPort: '/dev/ttyAS3',
    requestTimeoutMs: 1000,
    meters: {
      pollIntervalSec: 300,
      freshWindowSec: 900,
      devices: edgeMeterDevices,
      synthesizedTotal: {
        deviceKey: 'ammeter-total',
        sourceDeviceKeys: branchMeters,
      },
    },
    flowMeter: {
      pollIntervalSec: 60,
      freshWindowSec: 180,
      unitId: 10,
      deviceKey: 'waterflow',
      registerBase: 1,
      registerCount: 5,
      flowRegisterOffset: 0,
      flowScale: 1,
      flowDivisor: 35.315,
      velocityRegisterOffset: 4,
      velocityScale: 0.3048,
      velocityDivisor: 3600,
      pumpMirror: {
        deviceKey: 'chwp-main',
        flowPointCode: 'flow_rate',
        runningThreshold: 10,
      },
    },
    concentrator: {
      pollIntervalSec: 60,
      unitId: 20,
      activeChannels: [1, 2, 3, 4, 5, 6],
      tempDeviceKey: 'temp',
      voltageDeviceKey: 'temp-voltage',
      rssiDeviceKey: 'temp-rssi',
      statusDeviceKey: 'temp-status',
      lowBatteryMv: 2700,
      copyRoutes: [
        {
          sourceKey: 't1',
          targets: [{ deviceKey: 'btu-calc', pointCode: 'supply_water_temperature' }],
        },
        {
          sourceKey: 't2',
          targets: [{ deviceKey: 'btu-calc', pointCode: 'return_water_temperature' }],
        },
      ],
    },
    btuCalculator: {
      deviceKey: 'btu-calc',
      supplyTempKey: 't1',
      returnTempKey: 't2',
      specificHeatFactor: 1.163,
      minPlantPowerKw: 20,
      integrationMaxGapSec: 600,
    },
    doControl: {
      enabled: false,
    },
  };

  const alarmRules: Record<string, unknown> = {
    readingGaps: {
      title: '关键电表或冷量读数中断',
      summary: '超过 10 分钟未收到关键点位有效读数，请检查网关采集与总线通信。',
      severity: 'MAJOR',
      ageSeconds: 600,
    },
    rules: [
      {
        code: `${code}-chw-supply-temp-high`,
        title: '冷冻水供水温度偏高',
        severity: 'MINOR',
        raise: {
          kind: 'THRESHOLD',
          input: 'btu-calc/supply_water_temperature',
          operator: 'GT',
          value: 11,
          windowSeconds: 600,
        },
        clear: {
          kind: 'THRESHOLD',
          input: 'btu-calc/supply_water_temperature',
          operator: 'LTE',
          value: 9,
          windowSeconds: 300,
        },
      },
    ],
  };

  const problems = checkSiteFile(site as unknown as OnboardingProfile, alarmRules);
  if (problems.length > 0) {
    throw new Error(`generated invalid site bundle:\n${problems.join('\n')}`);
  }
  return { site, edgePlant, alarmRules };
}

function parseCliArgs(argv: string[]): Record<string, string | boolean> {
  const result: Record<string, string | boolean> = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i]!;
    if (!arg.startsWith('--')) continue;
    const eq = arg.indexOf('=');
    if (eq !== -1) {
      result[arg.slice(2, eq)] = arg.slice(eq + 1);
    } else {
      const next = argv[i + 1];
      if (next && !next.startsWith('--')) {
        result[arg.slice(2)] = next;
        i += 1;
      } else {
        result[arg.slice(2)] = true;
      }
    }
  }
  return result;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const args = parseCliArgs(process.argv.slice(2));
  const code = String(args.code ?? '');
  const name = String(args.name ?? '');
  const prefix = String(args.prefix ?? '');
  if (!code || !name || !prefix) {
    console.error('usage: node --experimental-strip-types scripts/site-onboarding-init.ts --code=<code> --name=<name> --prefix=<8-hex> [--chillers=2] [--floors=2] [--dry-run]');
    process.exit(1);
  }
  const outDir = resolve(String(args.out ?? 'deploy/sites'));
  const force = Boolean(args.force);
  const dryRun = Boolean(args['dry-run']);

  if (existsSync(outDir)) {
    for (const file of readdirSync(outDir).filter((f) => f.endsWith('.site.json'))) {
      const existing = JSON.parse(readFileSync(join(outDir, file), 'utf8')) as { site?: { id?: string; code?: string } };
      if (!force && existing.site?.id?.toLowerCase().startsWith(`${prefix.toLowerCase()}-`) && existing.site.code !== code) {
        console.error(`error: UUID prefix ${prefix} already used by ${file} (${existing.site.code})`);
        process.exit(1);
      }
    }
  }

  const bundle = generateSiteBundle({
    code,
    name,
    prefix,
    tenantId: typeof args.tenant === 'string' ? args.tenant : undefined,
    timezone: typeof args.timezone === 'string' ? args.timezone : undefined,
    chillers: typeof args.chillers === 'string' ? Number(args.chillers) : undefined,
    chwp: typeof args.chwp === 'string' ? Number(args.chwp) : undefined,
    cwp: typeof args.cwp === 'string' ? Number(args.cwp) : undefined,
    towers: typeof args.towers === 'string' ? Number(args.towers) : undefined,
    floors: typeof args.floors === 'string' ? Number(args.floors) : undefined,
    width: typeof args.width === 'string' ? Number(args.width) : undefined,
    depth: typeof args.depth === 'string' ? Number(args.depth) : undefined,
  });

  if (dryRun) {
    console.log(JSON.stringify({ ok: true, outDir, files: [`${code}.site.json`, `${code}.edge-plant.json`, `${code}.alarm-rules.json`] }, null, 2));
  } else {
    mkdirSync(outDir, { recursive: true });
    const targets: Array<[string, Record<string, unknown>]> = [
      [join(outDir, `${code}.site.json`), bundle.site],
      [join(outDir, `${code}.edge-plant.json`), bundle.edgePlant],
      [join(outDir, `${code}.alarm-rules.json`), bundle.alarmRules],
    ];
    for (const [path] of targets) {
      if (!force && existsSync(path)) {
        console.error(`error: ${path} already exists (pass --force to overwrite)`);
        process.exit(1);
      }
    }
    for (const [path, data] of targets) {
      writeFileSync(path, `${JSON.stringify(data, null, 2)}\n`, 'utf8');
      console.log(`wrote ${path}`);
    }
  }
}
