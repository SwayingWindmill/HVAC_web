import { readFileSync } from 'node:fs';
import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

const HOUR_MS = 3_600_000;
const BTU_METER_DEVICE_ID = '018f3e00-4000-7000-8000-000000000006';
const HVAC_METER_DEVICE_ID = '018f3e00-4000-7000-8000-000000000005';

const recordedBreakdown = JSON.parse(
  readFileSync(new URL('./fixtures/energy-breakdown.json', import.meta.url), 'utf8'),
).response;

function refreshSnapshotsWithCalibrationSignals(
  snapshots: Record<string, any>,
  nowIso: string,
) {
  for (const item of snapshots.items ?? []) {
    if (item.status !== 'OK' || !item.snapshot) continue;
    item.snapshot.evaluatedAt = nowIso;
    if (item.snapshot.presence) {
      item.snapshot.presence.lastSeenAt = nowIso;
      if (item.snapshot.presence.lastKnown) {
        item.snapshot.presence.lastKnown.lastSeenAt = nowIso;
        item.snapshot.presence.lastKnown.evaluatedAt = nowIso;
      }
    }
    for (const val of item.snapshot.values ?? []) {
      if (val.state !== 'PRESENT') continue;
      val.sampledAt = nowIso;
      val.receivedAt = nowIso;
      val.freshness = 'FRESH';

      if (
        item.deviceId === BTU_METER_DEVICE_ID &&
        val.key === 'supply_water_temperature'
      ) {
        val.probeOffsets = [
          {
            pairCode: 'header-chw',
            offsetK: 0.38,
            stddevK: 0.04,
            sampleCount: 18,
            windowStartedAt: '2026-10-10T01:00:00.000Z',
            windowEndedAt: '2026-10-10T02:30:00.000Z',
            suggested: true,
            applied: false,
            alarm: 'OFFSET',
          },
        ];
      }

      if (
        item.deviceId === BTU_METER_DEVICE_ID &&
        val.key === 'instant_cooling_capacity'
      ) {
        val.qualityReasons = ['LOW_CONFIDENCE'];
        val.measurement = {
          relative: 0.28,
          low: 295.3,
          high: 529.7,
          lowConfidence: true,
          deltaTK: 0.85,
          deltaTAccuracyK: 0.21,
        };
      }
    }
  }
}

function buildHourlyReconciliationHandler() {
  return (query: Record<string, unknown>) => {
    const fromMs = Date.parse(String(query.from));
    const toMs = Date.parse(String(query.to));
    const isWithSuspect = query.qualityPolicy === 'VALID_AND_SUSPECT';
    const isCooling = query.energyType === 'cooling';

    const points = [];
    let idx = 0;
    for (let cursor = fromMs; cursor < toMs; cursor += HOUR_MS) {
      const endMs = cursor + HOUR_MS;
      // Leave hour index 5 missing to verify missing-segment detection
      if (idx !== 5 && endMs <= Date.now()) {
        const baseKwh = isCooling ? 620 : 180;
        const suspectDelta = !isCooling && isWithSuspect && idx === 10 ? 25.5 : 0;
        points.push({
          periodStart: new Date(cursor).toISOString(),
          periodEnd: new Date(endMs).toISOString(),
          energyKWh: Number((baseKwh + suspectDelta).toFixed(2)),
        });
      }
      idx += 1;
    }

    return {
      body: {
        schemaVersion: 1,
        points,
        ...(query.breakdown === 'device'
          ? {
              devices: [
                {
                  deviceId: HVAC_METER_DEVICE_ID,
                  points,
                },
              ],
            }
          : {}),
        metadata: {
          ...recordedBreakdown.metadata,
          requestedGranularity: 'hour',
          actualGranularity: 'hour',
          dataWatermark: String(query.to),
          aggregateWatermark: String(query.to),
          partial: false,
          qualitySummary: { valid: 220, suspect: isWithSuspect ? 8 : 0, invalid: 0 },
        },
      },
    };
  };
}

test('integrations workspace supports gateway chain, hourly meter reconciliation, sensor zero-offset calibration, and deduplicated work orders at 1440px', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  refreshSnapshotsWithCalibrationSignals(gateway.snapshots, new Date().toISOString());
  gateway.energySeriesHandler = buildHourlyReconciliationHandler();

  // 1. Default tab: 现场网关 (L1)
  await page.goto(`/settings/integrations?site=${SITE_ID}`);
  await expect(page.getByRole('heading', { name: '集成管理' })).toBeVisible();
  await expect(page.getByRole('tab', { name: '现场网关', selected: true })).toBeVisible();
  await expect(page.getByLabel('数据链路')).toContainText('现场设备');
  await expect(page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' })).toBeVisible();

  const overflowGateways = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(overflowGateways).toBe(false);

  // 2. Switch to 电表数据核对 (?view=meters)
  await page.getByRole('tab', { name: '电表数据核对' }).click();
  await expect(page).toHaveURL(/view=meters/);
  await expect(page.getByLabel('电表核对概览')).toContainText('已接入表计');
  await expect(page.getByLabel('电表核对概览')).toContainText('有效计量时段');
  await expect(page.getByText('可疑差值 +25.50 kWh')).toBeVisible();

  // Toggle hourly reconciliation chart -> table -> chart
  const reconcileToggle = page.getByRole('radiogroup', { name: '逐时电量核对视图切换' });
  await reconcileToggle.getByRole('radio', { name: '表格' }).click();
  const reconcileTable = page.getByRole('table', { name: '近 24 小时逐时电量核对表格' });
  await expect(reconcileTable).toContainText('含可疑样本');
  const missingBucketRow = reconcileTable.getByRole('row').filter({ hasText: '缺数据' });
  await expect(missingBucketRow).toContainText('—');
  await expect(missingBucketRow).not.toContainText('0.00');
  await reconcileToggle.getByRole('radio', { name: '图表' }).click();

  const overflowMeters = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(overflowMeters).toBe(false);

  // Open L2 meter inspection drawer (?inspect=hvac-meter)
  const meterRow = page.getByRole('row').filter({ hasText: 'METER-HVAC-TOTAL' });
  await meterRow.getByRole('button', { name: '核查详情' }).click();
  await expect(page).toHaveURL(/inspect=hvac-meter/);

  const meterDrawer = page.getByRole('dialog');
  await expect(meterDrawer).toContainText('METER-HVAC-TOTAL');
  await expect(meterDrawer).toContainText('不在网页端手工覆盖历史电量');

  // Verify [图表 | 表格] toggle inside meter drawer
  await meterDrawer.getByRole('radio', { name: '表格' }).click();
  await expect(meterDrawer.getByRole('table')).toBeVisible();

  // Verify deep link to /operations/trends
  const trendsLink = meterDrawer.getByRole('link', { name: '查看当时趋势' });
  await expect(trendsLink).toHaveAttribute('href', /\/operations\/trends\?/);

  // Create EXTERNAL meter inspection work order and verify deduplication
  await meterDrawer.getByRole('button', { name: '创建核查工单' }).click();
  const createMeterOrderDialog = page.getByRole('dialog', { name: '创建计量核查工单？' });
  await expect(createMeterOrderDialog).toBeVisible();
  await createMeterOrderDialog.getByRole('button', { name: '创建工单' }).click();
  await expect(meterDrawer.getByRole('link', { name: '查看核查工单' })).toBeVisible();
  await expect(meterDrawer.getByRole('button', { name: '创建核查工单' })).toHaveCount(0);

  await page.keyboard.press('Escape');

  // 3. Switch to 传感器校准 (?view=sensors)
  await page.getByRole('tab', { name: '传感器校准' }).click();
  await expect(page).toHaveURL(/view=sensors/);
  await expect(page.getByLabel('传感器校准概览')).toContainText('零点偏移建议');
  await expect(page.getByLabel('传感器校准概览')).toContainText('已登记定检周期 8 项');
  await expect(page.getByText('低可信 · ±28%（295.3–529.7 kW）')).toBeVisible();

  // Filter to 零点建议 (?filter=PROBE_OFFSET)
  await page
    .getByRole('radiogroup', { name: '传感器状态筛选' })
    .getByRole('radio', { name: '零点建议' })
    .click();
  await expect(page).toHaveURL(/filter=PROBE_OFFSET/);
  await expect(page.getByText('建议减去 0.38 K · 未采用')).toBeVisible();
  await expect(page.getByText('冷冻水总管供回水')).toBeVisible();

  const overflowSensors = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(overflowSensors).toBe(false);

  // Open L2 sensor calibration drawer
  await page.getByRole('button', { name: '校准详情' }).first().click();
  await expect(page).toHaveURL(/inspect=btu-meter%3Asupply_water_temperature|inspect=btu-meter:supply_water_temperature/);

  const sensorDrawer = page.getByRole('dialog');
  await expect(sensorDrawer).toContainText('探头零点偏移评估（仅水泵运行稳态）');
  await expect(sensorDrawer).toContainText('建议减去 0.38 K · 未采用');
  await expect(sensorDrawer).toContainText('冷冻水总管供回水');
  await expect(sensorDrawer).toContainText('平台不在网页端直接覆盖原始温度曲线');

  // Create EXTERNAL sensor calibration work order and verify deduplication
  await sensorDrawer.getByRole('button', { name: '创建校准工单' }).click();
  const createSensorOrderDialog = page.getByRole('dialog', { name: '创建传感器校准工单？' });
  await expect(createSensorOrderDialog).toBeVisible();
  await createSensorOrderDialog.getByRole('button', { name: '创建工单' }).click();
  await expect(sensorDrawer.getByRole('link', { name: '查看校准工单' })).toBeVisible();
  await expect(sensorDrawer.getByRole('button', { name: '创建校准工单' })).toHaveCount(0);
});

test('integrations workspace degrades cleanly in Chinese for analytics denial, unregistered sensor calibration dates, and missing snapshot or asset permissions', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.analyticsDenied = true;
  gateway.assetModel = {
    ...gateway.assetModel,
    sensors: gateway.assetModel.sensors.map((s: Record<string, unknown>) => ({
      ...s,
      calibrationDueAt: null,
    })),
  };
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter(
        (cap: string) => cap !== 'telemetry.batch.read' && cap !== 'telemetry.snapshot.read',
      ),
    },
  };

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=meters`);
  await expect(
    page.getByText('当前账号没有实时读数查看权限，当前累计读数暂不可见，以下展示表计登记与逐时电量核对。'),
  ).toBeVisible();
  await expect(
    page.getByText('当前账号没有读取该站点接入或能耗统计数据的权限'),
  ).toBeVisible();

  await page.getByRole('tab', { name: '传感器校准' }).click();
  await expect(page).toHaveURL(/view=sensors/);
  await expect(
    page.getByText('当前账号没有实时读数查看权限，实时值、零点偏移建议与测量不确定度暂不可见，以下仅展示传感器与点位登记。'),
  ).toBeVisible();
  await expect(page.getByLabel('传感器校准概览')).toContainText('未登记');
  await expect(page.getByLabel('传感器校准概览')).toContainText('当前站点传感器尚未录入定检到期日');

  // Remove asset.read and verify explicit Chinese permission StateCards on both governance tabs
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter(
        (cap: string) => cap !== 'asset.read',
      ),
    },
  };

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=meters`);
  await expect(page.getByRole('heading', { name: '当前账号无权读取表计配置' })).toBeVisible();

  await page.getByRole('tab', { name: '传感器校准' }).click();
  await expect(
    page.getByRole('heading', { name: '当前账号无权读取传感器与点位配置' }),
  ).toBeVisible();
});
