import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the overview shows the Site summary and names what is not integrated', async ({ page }) => {
  await page.goto(`/overview?site=${SITE_ID}`);

  await expect(page.getByTestId('metric-实际用电')).toHaveText('1.8');
  for (const label of ['节省电量', '节能率', '节约费用']) {
    await expect(page.getByTestId(`metric-${label}`)).toHaveText('未接入');
  }
  await expect(page.getByText('1 条未结告警')).toBeVisible();

  const systems = page.getByRole('region', { name: '系统效率概况' });
  await expect(systems).toContainText('综合 COP');
  await expect(systems).toContainText('未接入');
  await expect(systems).toContainText('100.00');
  await expect(systems).toContainText('7 / 7 台在线');
});
