import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

const GATEWAY_ID = '018f3e00-4000-7000-8000-000000000100';

function grantDeviceWritePrincipal() {
  const base = structuredClone(principal);
  const existing: string[] = Array.isArray(base.authorization?.capabilities)
    ? [...base.authorization.capabilities]
    : [];
  if (!existing.includes('device.read')) existing.push('device.read');
  if (!existing.includes('device.write')) existing.push('device.write');
  base.authorization = { ...base.authorization, capabilities: existing };
  return base;
}

test('high-frequency waveform capture workbench and gateway drawer remote diagnostics work in light and dark themes at 1440px', async ({
  page,
  gateway,
}) => {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });
  page.on('pageerror', (err) => pageErrors.push(err.message));

  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = grantDeviceWritePrincipal();

  // 1. Open /operations/diagnostics & switch to 高频波形抓拍 view
  await page.goto(`/operations/diagnostics?site=${SITE_ID}&view=waveforms`);
  await expect(page.getByRole('region', { name: '高频波形触发规则' })).toBeVisible();
  await expect(page.getByRole('list', { name: '波形触发规则列表' })).toContainText('冷冻水供温突变监测');

  // 2. Create a new trigger rule
  await page.getByRole('button', { name: '新增触发规则' }).click();
  const createDlg = page.getByRole('dialog', { name: '新增波形触发规则' });
  await createDlg.getByLabel('规则名称').fill('主机瞬态电流越限抓拍');
  await createDlg.getByRole('button', { name: '保存触发规则' }).click();
  await expect(createDlg).toHaveCount(0);
  await expect(page.getByRole('list', { name: '波形触发规则列表' })).toContainText('主机瞬态电流越限抓拍');

  // 3. Trigger manual waveform capture
  await page.getByRole('button', { name: '手动发起抓拍' }).click();
  const trigDlg = page.getByRole('dialog', { name: '手动下发瞬态波形抓拍' });
  await trigDlg.getByLabel('抓拍原因').fill('演练验证手动秒级回流');
  await trigDlg.getByRole('button', { name: '确认下发抓拍' }).click();
  await expect(trigDlg).toHaveCount(0);
  await expect(page.getByRole('list', { name: '波形回流任务列表' })).toContainText('演练验证手动秒级回流');

  // 4. Open interactive 1Hz waveform viewer & export CSV
  await page.getByRole('list', { name: '波形回流任务列表' }).getByRole('button', { name: '查看波形曲线' }).first().click();
  const chartDlg = page.getByRole('dialog', { name: '瞬态高频波形曲线分析' });
  await expect(chartDlg.getByRole('img', { name: '瞬态高频波形多点位曲线图' })).toBeVisible();
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    chartDlg.getByRole('button', { name: '导出 CSV' }).click(),
  ]);
  expect(download.suggestedFilename()).toMatch(/\.csv$/);
  await page.keyboard.press('Escape');
  await expect(chartDlg).toHaveCount(0);

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  // 5. Switch to dark theme and inspect GatewaySheet host metrics + bounded redacted log pull
  await page.goto(`/settings/integrations?site=${SITE_ID}`);
  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('dark');

  await page.getByRole('button', { name: 'EG8200-COMMERCIAL-001' }).click();
  const sheet = page.getByRole('dialog');
  const hostSection = sheet.getByRole('region', { name: '网关宿主及链路健康' });
  await expect(hostSection).toContainText('18.4% / 42.1%');
  await expect(hostSection).toContainText('70.6 PPM');
  await expect(hostSection).toContainText('TLSv1.3');

  const logSection = sheet.getByRole('region', { name: '端侧日志按需拉取' });
  await logSection.locator('summary').click();
  await logSection.getByRole('button', { name: '按需拉取脱敏日志' }).click();
  await expect(logSection).toContainText('已脱敏 2 处凭据');
  await expect(logSection.getByRole('region', { name: '端侧脱敏日志内容' })).toContainText('Bearer [REDACTED]');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  // 6. Verify !device.write gating on direct log-pull request returns 403 cleanly
  const roPrincipal = structuredClone(principal);
  const roCaps = (Array.isArray(roPrincipal.authorization?.capabilities) ? roPrincipal.authorization.capabilities : [])
    .filter((c: string) => c !== 'device.write');
  if (!roCaps.includes('device.read')) roCaps.push('device.read');
  roPrincipal.authorization = { ...roPrincipal.authorization, capabilities: roCaps };
  gateway.principalOverride = roPrincipal;
  await page.reload();
  const roLogSection = page.getByRole('dialog').getByRole('region', { name: '端侧日志按需拉取' });
  await roLogSection.locator('summary').click();
  await expect(roLogSection.getByRole('button', { name: '按需拉取脱敏日志' })).toBeDisabled();
  expect(consoleErrors).toEqual([]);
  const deniedPayload = await page.evaluate(
    async ({ gwId, csrf }) => {
      const res = await fetch(`/api/v1/gateways/${gwId}/diagnostics/logs:pull`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
        body: JSON.stringify({ maxBytes: 4096, sinceMinutes: 15 }),
      });
      const json = (await res.json()) as { code?: string };
      return { status: res.status, code: json.code };
    },
    { gwId: GATEWAY_ID, csrf: principal.session.csrfToken },
  );
  expect(deniedPayload).toEqual({ status: 403, code: 'CREDENTIAL_FORBIDDEN' });

  expect(consoleErrors.filter((msg) => !msg.includes('403 (Forbidden)'))).toEqual([]);
  expect(pageErrors).toEqual([]);
});
