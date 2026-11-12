import assert from 'node:assert/strict';
import test from 'node:test';
import type { SiteDataGap } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import {
  formatDataGapSince,
  summarizeDataGaps,
} from '../apps/hvac-web/src/features/data-gaps/model.ts';

const TIMEZONE = 'Asia/Shanghai';

function makeGap(overrides: Partial<SiteDataGap> = {}): SiteDataGap {
  return {
    alarmId: '01a15000-0000-7000-8000-000000000001',
    deviceId: '01a14000-4000-7000-8000-000000000004',
    deviceName: 'D 机组电表',
    pointId: '01a14000-5000-7000-8000-000000000011',
    pointCode: 'active_energy_total',
    pointName: '有功总电能',
    since: '2026-11-08T08:13:00Z',
    affectedFigures: ['空调用电', '综合能效', '估算电费'],
    excludesDeviceFromTotal: true,
    ...overrides,
  };
}

test('formatDataGapSince formats UTC instant in site local wall time', () => {
  assert.equal(formatDataGapSince('2026-11-08T08:13:00Z', TIMEZONE), '11/8 16:13');
});

test('summarizeDataGaps returns null when there are no active reading gaps', () => {
  assert.equal(summarizeDataGaps([], TIMEZONE, 'overview'), null);
});

test('summarizeDataGaps collapses multiple points on a synthesized-total sub-meter into one clear sentence', () => {
  const summary = summarizeDataGaps(
    [
      makeGap({
        pointCode: 'active_energy_total',
        pointName: '有功总电能',
        since: '2026-11-08T08:13:00Z',
        excludesDeviceFromTotal: true,
      }),
      makeGap({
        alarmId: '01a15000-0000-7000-8000-000000000002',
        pointId: '01a14000-5000-7000-8000-000000000012',
        pointCode: 'power_total',
        pointName: '总有功功率',
        since: '2026-11-08T08:14:00Z',
        affectedFigures: ['设备实时功率', '用电负荷'],
        excludesDeviceFromTotal: false,
      }),
    ],
    TIMEZONE,
    'overview',
  );

  assert.deepEqual(summary, {
    message: 'D 机组电表 11/8 16:13 起没有读数，今日空调用电不含这台',
  });

  const general = summarizeDataGaps([makeGap()], TIMEZONE, 'general');
  assert.equal(general?.message, 'D 机组电表 11/8 16:13 起没有读数，空调用电不含这台');
});

test('summarizeDataGaps formats non-total point gaps and caps summary at two devices', () => {
  const sensor1 = makeGap({
    deviceId: '01a14000-4000-7000-8000-000000000021',
    deviceName: '冷冻水供水温度计',
    pointCode: 'chilled_supply_temp',
    pointName: '冷冻水供水温度',
    since: '2026-11-12T01:05:00Z',
    affectedFigures: ['供冷量', '综合能效'],
    excludesDeviceFromTotal: false,
  });
  const sensor2 = makeGap({
    alarmId: '01a15000-0000-7000-8000-000000000003',
    deviceId: '01a14000-4000-7000-8000-000000000022',
    deviceName: '冷冻水回水温度计',
    pointCode: 'chilled_return_temp',
    pointName: '冷冻水回水温度',
    since: '2026-11-12T01:06:00Z',
    affectedFigures: ['供冷量', '综合能效'],
    excludesDeviceFromTotal: false,
  });
  const sensor3 = makeGap({
    alarmId: '01a15000-0000-7000-8000-000000000004',
    deviceId: '01a14000-4000-7000-8000-000000000023',
    deviceName: '室外温湿度站',
    pointCode: 'outdoor_temp',
    pointName: '室外干球温度',
    since: '2026-11-12T01:07:00Z',
    affectedFigures: ['实时运行数据'],
    excludesDeviceFromTotal: false,
  });

  const summary = summarizeDataGaps([sensor1, sensor2, sensor3], TIMEZONE, 'general');
  assert.equal(
    summary?.message,
    '冷冻水供水温度计（冷冻水供水温度）11/12 09:05 起没有读数，影响供冷量、综合能效；冷冻水回水温度计（冷冻水回水温度）11/12 09:06 起没有读数，影响供冷量、综合能效（另有 1 台设备读数中断）',
  );
});
