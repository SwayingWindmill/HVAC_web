import { existsSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

const GATEWAY_ID = '018f3e00-4000-7000-8000-000000000100';
const INITIAL_CERT_ID = '0193f100-0000-7000-8000-000000000001';
const ROOT_REPO_VERIFY_DIR = '/mnt/e/Code/HVAC_web/.scratch/verify/connectivity-p1-gateway-diagnostics';
const SCREENSHOT_DIR = existsSync('/mnt/e/Code/HVAC_web/.git')
  ? ROOT_REPO_VERIFY_DIR
  : join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..', '.scratch', 'verify', 'connectivity-p1-gateway-diagnostics');
const SHADOW_SHOT_DIR = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  '..',
  '..',
  '.scratch',
  'verify',
  'p1-web-governance',
);

function screenshotPath(fileName: string): string {
  mkdirSync(SCREENSHOT_DIR, { recursive: true });
  return join(SCREENSHOT_DIR, fileName);
}

function shadowShot(fileName: string): string {
  mkdirSync(SHADOW_SHOT_DIR, { recursive: true });
  return join(SHADOW_SHOT_DIR, fileName);
}

function grantDeviceWritePrincipal() {
  return {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: [...principal.authorization.capabilities, 'device.write'],
    },
  };
}

test('gateway credential lifecycle supports code disposal, lossless rotation, single-cert revocation, gateway stop, and re-enable across themes at 1440px', async ({
  page,
  gateway,
}) => {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}`);
  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();

  const sheet = page.getByRole('dialog');
  const certList = sheet.getByRole('list', { name: '已签发证书' });
  const initialCertItem = certList.getByRole('listitem').filter({ hasText: '序列号 1a2b3c4d5e6f7a8b' });
  await expect(initialCertItem).toBeVisible();
  await expect(initialCertItem).toContainText('有效');

  // 1. Generate one-time enrollment code and verify disposal on Escape close.
  await sheet.getByRole('button', { name: '生成接入码', exact: true }).click();
  const codeInput = sheet.getByRole('textbox', { name: '一次性接入码', exact: true });
  await expect(codeInput).toHaveValue('0001' + 'A'.repeat(39));
  await page.keyboard.press('Escape');
  await expect(sheet).toHaveCount(0);

  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();
  await expect(sheet.getByRole('textbox', { name: '一次性接入码', exact: true })).toHaveCount(0);

  // 2. Rotate key cleanly without invalidating the existing active certificate.
  await sheet.getByRole('button', { name: '轮换密钥', exact: true }).click();
  await expect(sheet.getByRole('textbox', { name: '一次性接入码', exact: true })).toHaveValue(
    '0002' + 'R'.repeat(39),
  );
  await expect(initialCertItem).toContainText('有效');

  // Simulate field gateway enrolling with the rotated code and refresh status.
  gateway.issueGatewayCertificate(GATEWAY_ID, { serialNumber: '2b3c4d5e6f7a8b9c' });
  await sheet.getByRole('button', { name: '刷新状态', exact: true }).click();
  const newerCertItem = certList.getByRole('listitem').filter({ hasText: '序列号 2b3c4d5e6f7a8b9c' });
  await expect(newerCertItem).toContainText('有效');
  await expect(initialCertItem).toContainText('有效');

  // 3. Revoke only the older active certificate with a reason while keeping the newer one valid.
  await initialCertItem.getByRole('button', { name: '吊销证书…', exact: true }).click();
  const certAlert = page.getByRole('alertdialog');
  await expect(certAlert).toContainText('同网关上其他仍在有效期内的轮换证书会继续工作');
  await certAlert.getByLabel('吊销原因（可选）').fill('证书私钥疑似泄露');
  await certAlert.getByRole('button', { name: '确认吊销', exact: true }).click();

  await expect(certAlert).toHaveCount(0);
  await expect(initialCertItem).toContainText('已吊销');
  await expect(initialCertItem).toContainText('原因：证书私钥疑似泄露');
  await expect(initialCertItem.getByRole('button', { name: '吊销证书…', exact: true })).toHaveCount(0);
  await expect(newerCertItem).toContainText('有效');
  await expect(sheet).toContainText('证书有效');

  // 4. Verify zero horizontal overflow in light and dark themes at 1440x900.
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.keyboard.press('Escape');
  await expect(sheet).toHaveCount(0);

  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe('dark');
  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();
  await expect(sheet).toContainText('证书有效');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );

  // 5. Stop the gateway with explicit confirmation, then cancel once, then confirm.
  const stopWrites = () => gateway.writes.filter((entry) => entry.path === `/api/v1/gateways/${GATEWAY_ID}/revoke`);
  await sheet.getByRole('button', { name: '停用网关…', exact: true }).click();
  const stopAlert = page.getByRole('alertdialog');
  await expect(stopAlert).toContainText('将作废 EG8200-COMMERCIAL-001 的全部有效证书和未使用的接入码');
  await expect(stopAlert).toContainText('重新启用并生成接入码');
  expect(stopWrites()).toHaveLength(0);
  await stopAlert.getByRole('button', { name: '取消', exact: true }).click();
  expect(stopWrites()).toHaveLength(0);

  await sheet.getByRole('button', { name: '停用网关…', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: '停用此网关', exact: true }).click();
  await expect(sheet).toContainText('已停用');
  await expect(newerCertItem).toContainText('已吊销');
  await expect(sheet.getByRole('button', { name: '生成接入码', exact: true })).toHaveCount(0);

  // 6. Re-enable the stopped gateway via rotation while historical certificates stay revoked.
  const reenableButton = sheet.getByRole('button', { name: '重新启用并生成接入码', exact: true });
  await expect(reenableButton).toBeEnabled();
  await reenableButton.click();
  await expect(sheet.getByRole('textbox', { name: '一次性接入码', exact: true })).toHaveValue(
    '0003' + 'R'.repeat(39),
  );
  await expect(sheet).toContainText('等待接入');
  await expect(initialCertItem).toContainText('已吊销');
  await expect(newerCertItem).toContainText('已吊销');

  const gatewayWrites = gateway.writes.filter((entry) => entry.path.startsWith(`/api/v1/gateways/${GATEWAY_ID}/`));
  expect(gatewayWrites).toEqual([
    { method: 'POST', path: `/api/v1/gateways/${GATEWAY_ID}/enrollment-code`, body: null, csrfToken: 'e2e-csrf-token' },
    { method: 'POST', path: `/api/v1/gateways/${GATEWAY_ID}/rotate`, body: null, csrfToken: 'e2e-csrf-token' },
    {
      method: 'POST',
      path: `/api/v1/gateways/${GATEWAY_ID}/credentials/${INITIAL_CERT_ID}/revoke`,
      body: { reason: '证书私钥疑似泄露' },
      csrfToken: 'e2e-csrf-token',
    },
    { method: 'POST', path: `/api/v1/gateways/${GATEWAY_ID}/revoke`, body: null, csrfToken: 'e2e-csrf-token' },
    { method: 'POST', path: `/api/v1/gateways/${GATEWAY_ID}/rotate`, body: null, csrfToken: 'e2e-csrf-token' },
  ]);
  expect(consoleErrors).toEqual([]);
  expect(pageErrors).toEqual([]);
});

test('gateway credential sheet handles empty certificate list, fingerprint fallback, read-only gating, and error retry', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.seedGatewayCredentials(GATEWAY_ID, { revoked: false, records: [] });

  let failCredentials = true;
  await page.route(`**/api/v1/gateways/${GATEWAY_ID}/credentials`, async (route) => {
    if (failCredentials) {
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ code: 'CREDENTIAL_SERVICE_UNAVAILABLE' }),
      });
      return;
    }
    await route.fallback();
  });

  await page.goto(`/settings/integrations?site=${SITE_ID}`);
  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();

  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('等待接入');
  await expect(sheet.getByRole('alert')).toContainText('已签发证书列表暂时读不到');

  failCredentials = false;
  await sheet.getByRole('button', { name: '重试', exact: true }).click();
  await expect(sheet).toContainText('暂无已签发的网关证书');

  // Verify read-only principal cannot trigger enrollment, rotation, or stop actions.
  await expect(sheet.getByRole('button', { name: '生成接入码', exact: true })).toBeDisabled();
  await expect(sheet.getByRole('button', { name: '轮换密钥', exact: true })).toBeDisabled();
  await expect(sheet.getByRole('button', { name: '停用网关…', exact: true })).toBeDisabled();

  // Verify fingerprint fallback when a certificate record omits serialNumber.
  gateway.seedGatewayCredentials(GATEWAY_ID, {
    revoked: false,
    records: [
      {
        id: INITIAL_CERT_ID,
        gatewayId: GATEWAY_ID,
        fingerprintSha256: '00000001a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718deadbeef',
        serialNumber: null,
        status: 'ACTIVE',
        validFrom: '2026-10-01T00:00:00.000Z',
        validUntil: '2099-01-01T00:00:00.000Z',
        revokedAt: null,
        revocationReason: null,
        createdAt: '2026-10-01T00:00:00.000Z',
      },
    ],
  });
  await sheet.getByRole('button', { name: '刷新状态', exact: true }).click();
  await expect(sheet).toContainText('指纹末 8 位 deadbeef');
  await expect(sheet.getByRole('button', { name: '吊销证书…', exact: true })).toBeDisabled();
  await expect(sheet.getByRole('button', { name: '开启抓包', exact: true })).toBeDisabled();
  await expect(sheet).toContainText('当前账号仅有查看权限，不可启停抓包。');
});

test('gateway diagnostics support packet capture lifecycle, direction filtering, quarantine inspection, and light/dark screenshots at 1440px', async ({
  page,
  gateway,
}) => {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}`);
  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();

  const sheet = page.getByRole('dialog');
  const captureSection = sheet.locator('section[aria-label="通信抓包"]');
  const quarantineSection = sheet.locator('section[aria-label="接收隔离"]');

  await expect(quarantineSection).toBeVisible();
  await expect(quarantineSection).toContainText('报文格式或字段不合法');
  await expect(quarantineSection).toContainText('证书未生效或已吊销');
  await expect(quarantineSection).toContainText('累计 4 次');
  await expect(quarantineSection).toContainText('累计 12 次');

  const quarantineList = quarantineSection.getByRole('list', { name: '接收隔离记录' });
  const invalidItem = quarantineList.getByRole('listitem').filter({ hasText: '报文格式或字段不合法' });
  await invalidItem.locator('summary', { hasText: '查看原始报文片段' }).click();
  await expect(invalidItem.getByRole('region', { name: '隔离报文 0193f200-0000-7000-8000-000000000001 原始内容' })).toContainText('"deviceCode":"CHILLER-01"');

  await expect(captureSection).toContainText('未开启');
  await expect(captureSection).toContainText('暂无抓包记录');

  await captureSection.getByRole('combobox', { name: '抓包时长' }).click();
  await page.getByRole('option', { name: '1 分钟', exact: true }).click();
  await captureSection.getByRole('button', { name: '开启抓包', exact: true }).click();

  await expect(captureSection).toContainText('抓包中');
  await expect(captureSection).toContainText('剩余 0');

  const packetList = captureSection.getByRole('list', { name: '抓包记录' });
  await expect(packetList.getByRole('listitem')).toHaveCount(3);
  await expect(packetList).toContainText('上行读数');
  await expect(packetList).toContainText('下行指令');
  await expect(packetList).toContainText('接收隔离');

  const firstPacket = packetList.getByRole('listitem').first();
  await firstPacket.locator('summary').click();
  await expect(firstPacket.getByRole('region', { name: '报文 1 原始内容' })).toContainText('"chw_supply_temp":7.1');
  await expect(firstPacket.getByRole('button', { name: '复制报文 1', exact: true })).toBeVisible();

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await captureSection.scrollIntoViewIfNeeded();
  await page.screenshot({ path: screenshotPath('gateway-diagnostics-light.png') });

  const filterGroup = captureSection.getByRole('radiogroup', { name: '报文方向筛选' });
  await filterGroup.getByRole('radio', { name: '下行', exact: true }).click();
  await expect(packetList.getByRole('listitem')).toHaveCount(1);
  await expect(packetList).toContainText('下行指令');

  await filterGroup.getByRole('radio', { name: '接收隔离', exact: true }).click();
  await expect(packetList.getByRole('listitem')).toHaveCount(1);
  await expect(packetList).toContainText('devices/eg8200-commercial-001/telemetry/unknown-point');

  await filterGroup.getByRole('radio', { name: '全部', exact: true }).click();
  await expect(packetList.getByRole('listitem')).toHaveCount(3);

  await captureSection.getByRole('button', { name: '停止抓包', exact: true }).click();
  await expect(captureSection).toContainText('未开启');
  await expect(packetList.getByRole('listitem')).toHaveCount(3);

  await page.keyboard.press('Escape');
  await expect(sheet).toHaveCount(0);

  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe('dark');

  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();
  await expect(quarantineSection).toContainText('报文格式或字段不合法');
  await expect(packetList.getByRole('listitem')).toHaveCount(3);
  await quarantineSection
    .getByRole('listitem')
    .filter({ hasText: '报文格式或字段不合法' })
    .locator('summary', { hasText: '查看原始报文片段' })
    .click();
  await expect(
    quarantineSection.getByRole('region', { name: '隔离报文 0193f200-0000-7000-8000-000000000001 原始内容' }),
  ).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await quarantineSection.scrollIntoViewIfNeeded();
  await page.screenshot({ path: screenshotPath('gateway-diagnostics-dark.png') });

  const captureWrites = gateway.writes.filter((entry) => entry.path.startsWith(`/api/v1/gateways/${GATEWAY_ID}/packet-capture/`));
  expect(captureWrites).toEqual([
    {
      method: 'POST',
      path: `/api/v1/gateways/${GATEWAY_ID}/packet-capture/start`,
      body: { durationSeconds: 60 },
      csrfToken: 'e2e-csrf-token',
    },
    {
      method: 'POST',
      path: `/api/v1/gateways/${GATEWAY_ID}/packet-capture/stop`,
      body: null,
      csrfToken: 'e2e-csrf-token',
    },
  ]);
  expect(consoleErrors).toEqual([]);
  expect(pageErrors).toEqual([]);
});

test('gateway config shadow initializes mappings, recovers from 409 conflicts, and shows drift in light and dark themes', async ({
  page,
  gateway,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  await page.goto(`/settings/integrations?site=${SITE_ID}`);
  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();

  const sheet = page.getByRole('dialog');
  const shadowSection = sheet.getByRole('region', { name: '配置影子' });
  await expect(shadowSection).toContainText('未配置');

  await shadowSection.getByRole('button', { name: '初始化配置影子' }).click();
  const editor = page.getByRole('dialog', { name: '编辑网关配置影子' });
  await editor.getByRole('button', { name: '新增点位映射' }).click();
  await editor.getByPlaceholder('rs485-u1:reg1').fill('rs485-u1:r1');
  await editor.getByPlaceholder('chiller-01').first().fill('chiller-01');
  await editor.getByLabel('主点位编码').fill('chw_supply_temp');
  await editor.getByPlaceholder('chiller-02:supply_temp').fill('chiller-02:chw_supply_temp');

  await editor.getByRole('button', { name: '新增采集周期' }).click();
  await editor.getByPlaceholder('chiller-01').last().fill('chiller-01');
  await editor.getByPlaceholder('全部点位').fill('chw_supply_temp');
  await editor.getByLabel('变更原因（必填）').fill('初始化 1 号冷机回水复用映射与采样周期');
  await editor.getByRole('button', { name: '保存期望配置' }).click();

  await expect(editor).toHaveCount(0);
  await expect(shadowSection).toContainText('待同步下发');
  await expect(shadowSection).toContainText('v1');
  await expect(shadowSection).toContainText('点位映射 1 条');
  await expect(shadowSection).toContainText('采集周期 1 条');
  await page.screenshot({ path: shadowShot('p1-shadow-light.png') });

  // Simulate concurrent revision advance to verify 409 conflict & reload button inside editor.
  await shadowSection.getByRole('button', { name: '编辑配置影子' }).click();
  const staleEditor = page.getByRole('dialog', { name: '编辑网关配置影子' });
  gateway.setGatewayShadowState(GATEWAY_ID, {
    tenantId: '018f3d00-0000-7000-8000-000000000001',
    gatewayId: GATEWAY_ID,
    syncStatus: 'DRIFTED',
    desired: {
      desiredRevision: 2,
      pointMappings: [
        {
          sourceChannel: 'rs485-u1:r1',
          primaryDeviceKey: 'chiller-01',
          primaryPointCode: 'chw_supply_temp',
        },
      ],
      samplingPolicies: [],
      concentratorConfigs: {},
      updatedBy: '018f3e00-2000-7000-8000-000000000001',
      reason: '其他管理员已调整配置',
      createdAt: '2026-10-10T00:00:00.000Z',
      updatedAt: '2026-10-10T01:00:00.000Z',
    },
    reported: {
      reportedRevision: 2,
      appliedStatus: 'PARTIAL',
      pointMappings: [],
      samplingPolicies: [],
      concentratorConfigs: {},
      driftDetails: [],
      errorMessage: '边缘节点串口通道 rs485-u1 应用失败',
      reportedAt: '2026-10-10T01:05:00.000Z',
      updatedAt: '2026-10-10T01:05:00.000Z',
    },
    drift: [
      {
        section: 'POINT_MAPPINGS',
        target: 'rs485-u1:r1',
        reason: 'EDGE_APPLY_FAILED',
        detail: '寄存器偏移未响应',
      },
    ],
  });

  await staleEditor.getByLabel('变更原因（必填）').fill('尝试覆盖保存');
  await staleEditor.getByRole('button', { name: '保存期望配置' }).click();
  await expect(staleEditor.getByRole('alert')).toContainText('配置影子的期望修订已被其他操作更新');
  await staleEditor.getByRole('button', { name: '加载最新影子修订' }).click();
  await staleEditor.getByRole('button', { name: '取消' }).click();

  await shadowSection.getByRole('button', { name: '刷新影子' }).click();
  await expect(shadowSection).toContainText('配置存在漂移');
  await expect(shadowSection).toContainText('部分生效');
  await expect(shadowSection).toContainText('寄存器偏移未响应');
  await expect(shadowSection).toContainText('边缘节点串口通道 rs485-u1 应用失败');

  await page.keyboard.press('Escape');
  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme))
    .toBe('dark');
  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();
  await expect(sheet.getByRole('region', { name: '配置影子' })).toContainText('配置存在漂移');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: shadowShot('p1-shadow-dark.png') });
});
