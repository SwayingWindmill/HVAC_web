import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the plant view shows current plant figures from the Snapshot', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  const plant = page.getByRole('region', { name: '冷站运行' });
  await expect(plant.getByRole('tab', { name: /冷站 COP/ })).toHaveAttribute('aria-selected', 'true');
  await expect(plant.getByRole('tab', { name: /冷站 COP/ })).toContainText(/\d\.\d{2}/);
  await expect(page.getByRole('link', { name: /主要设备 4 \/ 4 运行/ })).toBeVisible();
  const loops = page.getByRole('table', { name: '水系统与室内外环境' });
  await expect(loops.getByRole('row', { name: /室内/ })).toContainText(/24\.1\s*°C/);
  // Without a realtime grant the page says it is polling rather than pretending to stream.
  await expect(page.getByText('每 30 秒刷新')).toBeVisible();
});

test('choosing a metric keeps it in the URL', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('region', { name: '冷站运行' }).getByRole('tab', { name: /^冷站总功率/ }).click();
  await expect(page).toHaveURL(/metric=power/);
  await page.getByRole('radio', { name: '24 小时' }).click();
  await expect(page).toHaveURL(/window=24h/);
});

test('the plant machines open from the running count in the equipment ledger', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('link', { name: /主要设备/ }).click();
  await expect(page).toHaveURL(/\/operations\/systems-devices\?/);
  await expect(page.getByRole('button', { name: /CHWP-01/ })).toBeVisible();
});
