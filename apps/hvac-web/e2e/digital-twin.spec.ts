import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the digital twin draws the registered plant equipment with its live state', async ({ page }) => {
  await page.goto(`/operations/digital-twin?site=${SITE_ID}`);
  await expect(page.getByRole('heading', { name: '数字孪生' })).toBeVisible();
  await expect(page.locator('canvas')).toBeVisible();
  // Machine labels are drawn into the scene from the Registry's chiller assets.
  await expect(page.getByText('CHILLER-01')).toBeVisible();
  await expect(page.getByText('冷站总功率')).toBeVisible();
  await expect(page.getByText('冷冻水', { exact: true })).toBeVisible();
});

test('the camera view is kept in the URL', async ({ page }) => {
  await page.goto(`/operations/digital-twin?site=${SITE_ID}`);
  await page.getByRole('radio', { name: '冷站' }).click();
  await expect(page).toHaveURL(/view=plant/);
});
