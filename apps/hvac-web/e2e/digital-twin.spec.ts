import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the digital twin draws the registered plant and keeps its view in the URL', async ({ page }) => {
  test.slow(); // the scene renders on a software GPU here
  await page.goto(`/operations/digital-twin?site=${SITE_ID}`);
  await expect(page.getByRole('heading', { name: '数字孪生' })).toBeVisible();
  await expect(page.locator('canvas')).toBeVisible();
  await expect(page.getByText('冷站总功率')).toBeVisible();
  await expect(page.getByText('冷冻水', { exact: true })).toBeVisible();
  // Close to the plant, each Registry chiller carries its card.
  await page.getByRole('radio', { name: '冷站' }).click();
  await expect(page).toHaveURL(/view=plant/);
  await expect(page.getByText('CHILLER-01')).toBeVisible();
});
