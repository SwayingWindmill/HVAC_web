import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

test('enrollment code disappears on close and revocation requires explicit permanent confirmation', async ({ page }) => {
  const gatewayId = '018f3e00-4000-7000-8000-000000000100';
  const now = new Date().toISOString();
  const enrollmentCode = 'A'.repeat(43);
  let revoked = false;
  const revocations: string[] = [];
  await page.route('**/api/v1/principal', (route) => route.fulfill({ json: {
    ...principal, authorization: { ...principal.authorization, capabilities: [...principal.authorization.capabilities, 'device.write'] },
  } }));
  await page.route(`**/api/v1/sites/${SITE_ID}/devices*`, (route) => route.fulfill({ json: {
    items: [{ id: gatewayId, tenantId: '018f3d00-0000-7000-8000-000000000001', siteId: SITE_ID, code: 'test-gateway', displayName: '测试现场网关', deviceType: 'GATEWAY', status: 'ACTIVE', revision: 1, createdAt: now, updatedAt: now }],
    hasMore: false, nextCursor: null,
  } }));
  await page.route('**/api/v1/gateways/**', async (route) => {
    const request = route.request();
    if (request.url().endsWith('/revoke')) { revoked = true; revocations.push(request.headers()['x-csrf-token']); }
    const body = request.url().endsWith('/enrollment-code')
      ? { enrollmentCode, expiresAt: now }
      : { status: revoked ? 'REVOKED' : 'ACTIVE', expiresAt: revoked ? undefined : now };
    await route.fulfill({ json: body });
  });
  await page.goto(`/settings/integrations?site=${SITE_ID}`);
  await page.getByRole('button', { name: '接入凭据' }).click();
  await page.getByRole('button', { name: '生成接入码', exact: true }).click();
  await expect(page.getByRole('textbox', { name: '一次性接入码', exact: true })).toHaveValue(enrollmentCode);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: '接入凭据' }).click();
  await expect(page.getByRole('textbox', { name: '一次性接入码', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: '吊销网关身份', exact: true }).click();
  await expect(page.getByRole('alertdialog')).toContainText('该身份不能恢复');
  expect(revocations).toEqual([]);
  await page.getByRole('button', { name: '取消', exact: true }).click();
  expect(revocations).toEqual([]);
  await page.getByRole('button', { name: '吊销网关身份', exact: true }).click();
  await page.getByRole('button', { name: '永久吊销此网关', exact: true }).click();
  await expect(page.getByRole('dialog')).toContainText('已吊销');
  await expect(page.getByRole('button', { name: '生成接入码', exact: true })).toBeDisabled();
  expect(revocations).toEqual(['e2e-csrf-token']);
});
