import { SITE_ID, principal } from './gateway';
import { captureVerifyScreenshot, expect, test } from './test';

const GATEWAY_ID = '018f3e00-4000-7000-8000-000000000100';

function grantDeviceWritePrincipal() {
  return {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: [...principal.authorization.capabilities, 'device.write'],
    },
  };
}

test('Lane 1 & 2: default gateways tab regression and light theme concentrators KPI + 23-channel table', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}`);
  await expect(page.getByRole('tab', { name: '现场网关' })).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByRole('tab', { name: '无线集中器' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' })).toBeVisible();

  const gatewaysOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(gatewaysOverflow).toBe(false);
  await captureVerifyScreenshot(page, 'pr4/lane1-gateways-light.png');

  await page.getByRole('tab', { name: '无线集中器' }).click();
  await expect(page).toHaveURL(/view=concentrators/);

  const overview = page.getByLabel('无线集中器概览');
  await expect(overview).toContainText('22 / 23 路');
  await expect(overview).toContainText('2 路');
  await expect(overview).toContainText('含 1 路严重欠压');
  await expect(overview).toContainText('433 MHz · SF9 · 20 dBm');
  await expect(overview).toContainText('UID 1 · 9600 bps · 10s');

  const channelsTable = page.getByRole('table', { name: '无线探头通道' });
  await expect(channelsTable).toContainText('CH 01');
  await expect(channelsTable).toContainText('CH 02');
  await expect(channelsTable).toContainText('CH 03');
  await expect(channelsTable).toContainText('CH 08');
  await expect(channelsTable).toContainText('严重欠压 (<2450 mV)');

  // Verify 200-slot EID pagination caps DOM rows <= 50 simultaneously
  const eidDomRows = await page.locator('table[aria-label="200 槽 EID 映射"] tbody tr').count();
  expect(eidDomRows).toBeLessThanOrEqual(50);
  expect(eidDomRows).toBe(25);

  const concentratorsOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(concentratorsOverflow).toBe(false);
  await captureVerifyScreenshot(page, 'pr4/lane2-concentrators-light.png');
});

test('Lane 3: dark theme maintains color-scheme sync and crisp contrast on 无线集中器', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=concentrators`);
  await expect(page.getByLabel('无线集中器概览')).toContainText('22 / 23 路');

  const appearance = page.getByRole('radiogroup', { name: '外观' });
  await appearance.getByRole('radio', { name: '深色' }).click();
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'dark');
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe('dark');

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(overflow).toBe(false);
  await captureVerifyScreenshot(page, 'pr4/lane3-concentrators-dark.png');
});

test('Lane 4: channel low-battery filter, IME-safe Enter search, and detail drawer', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=concentrators`);
  await page
    .getByRole('radiogroup', { name: '通道状态筛选' })
    .getByRole('radio', { name: '低电压' })
    .click();

  const channelsTable = page.getByRole('table', { name: '无线探头通道' });
  await expect(channelsTable).toContainText('CH 02');
  await expect(channelsTable).toContainText('CH 03');
  await expect(channelsTable).not.toContainText('CH 01');

  await page
    .getByRole('radiogroup', { name: '通道状态筛选' })
    .getByRole('radio', { name: '全部通道' })
    .click();

  const searchBox = page.getByLabel('搜索通道号、探头 EID 或传感器型号');
  await searchBox.fill('0x00000037');
  await searchBox.press('Enter');

  await expect(channelsTable.locator('tbody tr')).toHaveCount(1);
  await expect(channelsTable).toContainText('CH 03');

  await channelsTable.getByRole('button', { name: '通道详情' }).click();
  const drawer = page.getByRole('dialog');
  await expect(drawer).toContainText('通道 CH 03');
  await expect(drawer).toContainText('2410 mV');
  await expect(drawer).toContainText('55 (0x00000037)');
  await expect(drawer).toContainText('0x000A');
  await captureVerifyScreenshot(page, 'pr4/lane4-channel-filter.png');
});

test('Lane 5: protected RS485 UIDs {2,3,4,5,10,31} block reads and param edits inline', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  let protectedUidRequests = 0;
  page.on('request', (request) => {
    if (request.url().includes('/concentrators/2/') || request.url().includes('/concentrators/5/')) {
      protectedUidRequests += 1;
    }
  });

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=concentrators`);
  await expect(page.getByLabel('无线集中器概览')).toBeVisible();

  const unitIdInput = page.getByLabel('RS485 从站地址 (Unit ID, 1–247)');
  await unitIdInput.fill('2');
  await expect(page.getByText(/从站地址 2 为冷机 \/ 总电表 \/ 冷冻泵变频器/)).toBeVisible();
  await expect(page.getByLabel('无线集中器概览')).toHaveCount(0);
  expect(protectedUidRequests).toBe(0);

  await page.getByRole('button', { name: '恢复 UID 1' }).click();
  await expect(page.getByLabel('无线集中器概览')).toBeVisible();

  const modbusIdInput = page.getByLabel(/Modbus 从站地址/);
  await modbusIdInput.fill('5');
  await expect(
    page.getByRole('alert').filter({ hasText: /从站地址 5 为冷机 \/ 总电表 \/ 冷冻泵变频器/ }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: '预览参数变更' })).toBeDisabled();
  expect(gateway.writes.filter((entry) => entry.path.includes('/concentrators/'))).toEqual([]);
  await captureVerifyScreenshot(page, 'pr4/lane5-protected-uid.png');
});

test('Lane 6: parameter edit -> diff dialog -> CSRF-verified Flash commit and audit refresh', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=concentrators`);
  await expect(page.getByLabel('无线集中器概览')).toBeVisible();

  await page.getByLabel(/数据主动上报周期/).fill('15');
  await page.getByLabel(/射频发射功率/).fill('22');

  const previewBtn = page.getByRole('button', { name: '预览参数变更' });
  await expect(previewBtn).toBeEnabled();
  await previewBtn.click();

  const dialog = page.getByRole('dialog', { name: '确认下发串口与射频参数到集中器 Flash？' });
  await expect(dialog).toContainText('10 秒');
  await expect(dialog).toContainText('15 秒');
  await expect(dialog).toContainText('20 dBm');
  await expect(dialog).toContainText('22 dBm');

  const submitBtn = dialog.getByRole('button', { name: '确认写入 Flash' });
  await expect(submitBtn).toBeDisabled();

  await dialog.getByLabel('变更原因（必填）').fill('松雅轩现场提高 LoRa 发射功率至 22 dBm');
  await expect(submitBtn).toBeEnabled();
  await captureVerifyScreenshot(page, 'pr4/lane6-params-diff.png');
  await submitBtn.click();

  await expect(dialog).toHaveCount(0);
  await expect(page.getByText('已将 2 项参数写入集中器 Flash')).toBeVisible();
  await expect(page.getByLabel('无线集中器概览')).toContainText('433 MHz · SF9 · 22 dBm');
  await expect(page.getByRole('table', { name: '近期集中器变更记录' })).toContainText(
    '松雅轩现场提高 LoRa 发射功率至 22 dBm',
  );

  const paramWrite = gateway.writes.find(
    (entry) =>
      entry.method === 'PUT' &&
      entry.path === `/api/v1/gateways/${GATEWAY_ID}/concentrators/1/params`,
  );
  expect(paramWrite).toBeDefined();
  expect(paramWrite?.csrfToken).toBe('e2e-csrf-token');
  expect(paramWrite?.body).toEqual({
    params: { dataUplinkCycle: 15, rfTxPower: 22 },
    reason: '松雅轩现场提高 LoRa 发射功率至 22 dBm',
  });
});

test('Lane 7 & 8: 200-slot EID duplicate guard, batch fill, diff dialog commit, and audit log', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=concentrators`);
  await expect(page.getByRole('table', { name: '200 槽 EID 映射' })).toBeVisible();

  // Lane 7: Duplicate non-zero EID (50 already bound on slot 1) triggers conflict alert & disables write
  const slot24Input = page.getByLabel('槽位 24 EID');
  await slot24Input.fill('50');
  const conflictAlert = page
    .getByRole('alert')
    .filter({ hasText: /存在重复的非零探头 EID，已阻止写入 Flash：EID 50 重复出现在槽位 1、24/ });
  await expect(conflictAlert).toBeVisible();
  await expect(page.getByRole('button', { name: /预览映射变更/ })).toBeDisabled();
  await conflictAlert.scrollIntoViewIfNeeded();
  await captureVerifyScreenshot(page, 'pr4/lane7-eid-conflict.png');

  // Lane 8: Batch range fill resolves duplicate cleanly and commits 3 slots (24..26 -> 101..103)
  await page.getByLabel('起始槽位 (1–200)').fill('24');
  await page.getByLabel('连续数量').fill('3');
  await page.getByLabel('起始 EID').fill('101');
  await page.getByLabel('递增步长').fill('1');
  await page.getByRole('button', { name: '批量填充区间' }).click();

  await expect(slot24Input).toHaveValue('101');
  await expect(page.getByLabel('槽位 25 EID')).toHaveValue('102');

  const previewEidBtn = page.getByRole('button', { name: '预览映射变更（3）' });
  await expect(previewEidBtn).toBeEnabled();
  await previewEidBtn.click();

  const dialog = page.getByRole('dialog', { name: '确认写入 EID 槽位映射并持久化到 Flash？' });
  await expect(dialog).toContainText('槽位 24');
  await expect(dialog).toContainText('101 (0x00000065)');
  await dialog.getByLabel('变更原因（必填）').fill('松雅轩补绑 24 至 26 槽无线温度探头 EID');
  await dialog.getByRole('button', { name: '确认写入 EID 到 Flash' }).click();

  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('table', { name: '近期集中器变更记录' })).toContainText(
    '松雅轩补绑 24 至 26 槽无线温度探头 EID',
  );

  const eidWrite = gateway.writes.find(
    (entry) =>
      entry.method === 'PUT' &&
      entry.path === `/api/v1/gateways/${GATEWAY_ID}/concentrators/1/eids`,
  );
  expect(eidWrite).toBeDefined();
  expect(eidWrite?.csrfToken).toBe('e2e-csrf-token');
  expect(eidWrite?.body?.slots).toHaveLength(200);
  expect(eidWrite?.body?.slots[23]).toEqual({ slot: 24, eid: 101 });
  expect(eidWrite?.body?.slots[25]).toEqual({ slot: 26, eid: 103 });

  const domRowsAfter = await page.locator('table[aria-label="200 槽 EID 映射"] tbody tr').count();
  expect(domRowsAfter).toBeLessThanOrEqual(50);

  await captureVerifyScreenshot(page, 'pr4/lane8-eid-commit.png');
});

test('Lane 9 & 10: read-only / no-read operator guard and full-page visual audit baseline', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });

  // Verify missing device.read renders permission StateCard without issuing concentrator GETs
  let concentratorGetCount = 0;
  page.on('request', (request) => {
    if (request.url().includes('/concentrators/')) {
      concentratorGetCount += 1;
    }
  });
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter((cap: string) => cap !== 'device.read'),
    },
  };
  await page.goto(`/settings/integrations?site=${SITE_ID}&view=concentrators`);
  await expect(page.getByText('当前账号无权读取无线集中器')).toBeVisible();
  expect(concentratorGetCount).toBe(0);

  // Default principal.json includes device.read without device.write
  gateway.principalOverride = null;
  await page.goto(`/settings/integrations?site=${SITE_ID}&view=concentrators`);
  await expect(
    page.getByText(/当前账号仅有查看权限（缺少 device.write）/),
  ).toBeVisible();
  await expect(page.getByLabel('无线集中器概览')).toContainText('22 / 23 路');
  await expect(page.getByLabel(/射频发射功率/)).toBeDisabled();
  await expect(page.getByLabel('槽位 1 EID')).toBeDisabled();
  await expect(page.getByRole('button', { name: '批量填充区间' })).toBeDisabled();
  await expect(page.getByRole('button', { name: '预览参数变更' })).toBeDisabled();
  expect(gateway.writes.filter((entry) => entry.path.includes('/concentrators/'))).toEqual([]);

  await captureVerifyScreenshot(page, 'pr4/lane9-readonly.png');

  // Lane 10: Capture full-page visual audit in writable mode for comprehensive visual inspection
  gateway.principalOverride = grantDeviceWritePrincipal();
  await page.reload();
  await expect(page.getByLabel('无线集中器概览')).toContainText('433 MHz · SF9 · 20 dBm');
  const fullOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  );
  expect(fullOverflow).toBe(false);
  await captureVerifyScreenshot(page, 'pr4/lane10-visual-audit.png', { fullPage: true });
});

test('Lane 11: offline gateway stages concentrator params into config shadow when stageIfOffline is checked', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();
  gateway.setGatewayOffline(GATEWAY_ID, true);

  await page.goto(`/settings/integrations?site=${SITE_ID}&view=concentrators`);
  await expect(page.getByLabel('无线集中器概览')).toContainText('433 MHz · SF9 · 20 dBm');

  await page.getByLabel(/射频发射功率/).fill('22');
  await page.getByRole('button', { name: '预览参数变更' }).click();
  const dialog = page.getByRole('dialog', { name: '确认下发串口与射频参数到集中器 Flash？' });

  // Without stageIfOffline checked, offline transport reports error clearly.
  await dialog.getByLabel('变更原因（必填）').fill('尝试离线直接写参数');
  await dialog.getByRole('button', { name: '确认写入 Flash' }).click();
  await expect(dialog.getByRole('alert')).toContainText('现场网关远程连接通道暂未就绪');

  // Checking stageIfOffline stages the delta into the gateway Config Shadow instead.
  await dialog.getByRole('checkbox', { name: /若网关离线，暂存至配置影子/ }).check();
  await dialog.getByRole('button', { name: '确认写入或离线暂存' }).click();
  await expect(dialog).toHaveCount(0);

  await expect(page.getByRole('table', { name: '近期集中器变更记录' })).toContainText('已暂存待同步');
  await captureVerifyScreenshot(page, 'p1-web-governance/p1-concentrator-staged-light.png');

  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe('dark');
  await page.getByRole('tab', { name: '现场网关' }).click();
  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();
  const sheet = page.getByRole('dialog');
  await expect(sheet.getByRole('region', { name: '配置影子' })).toContainText('待同步下发');
  await expect(sheet.getByRole('region', { name: '配置影子' })).toContainText('UID 1 · 参数');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await captureVerifyScreenshot(page, 'p1-web-governance/p1-concentrator-staged-shadow-dark.png');
});
