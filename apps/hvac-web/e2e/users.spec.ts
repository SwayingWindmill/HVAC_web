import type { Page } from '@playwright/test';
import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

async function signInAsAdministrator(page: Page) {
  await page.route('**/api/v1/principal', (route) => route.fulfill({ json: {
    ...principal,
    principal: { ...principal.principal, roles: ['admin'] },
    authorization: { ...principal.authorization, capabilities: [...principal.authorization.capabilities, 'iam.admin'] },
  } }));
}

test('an administrator creates a user with a role on one site', async ({ page, gateway }) => {
  await signInAsAdministrator(page);
  await page.goto('/system?tab=users');
  const users = page.getByRole('table', { name: '用户与角色' });
  await expect(users).toContainText('周敏');
  await expect(users.getByRole('row', { name: /周敏/ })).toContainText('运维人员中央机房');

  await page.getByRole('button', { name: '新建用户', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '新建用户' });
  await dialog.getByLabel('姓名').fill('李娜');
  await dialog.getByLabel('用户名').fill('li.na');
  await dialog.getByLabel('邮箱').fill('li.na@example.invalid');
  await dialog.getByLabel('登录密码').fill('password1');
  await dialog.getByRole('combobox', { name: '角色' }).click();
  await page.getByRole('option', { name: '运维人员', exact: true }).click();
  await dialog.getByRole('combobox', { name: '站点' }).click();
  await page.getByRole('option', { name: '中央机房', exact: true }).click();
  await dialog.getByRole('button', { name: '创建', exact: true }).click();
  await expect(dialog.getByRole('alert')).toHaveText('这个密码太常见，容易被猜到，请换一个');

  await dialog.getByLabel('登录密码').fill('night-shift-chiller');
  await dialog.getByRole('button', { name: '创建', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(users.getByRole('row', { name: /李娜/ })).toContainText('运维人员中央机房');
  const created = gateway.writes.find((write) => write.method === 'POST' && write.path === '/api/v1/iam/members' && write.body?.password === 'night-shift-chiller');
  expect(created?.body).toMatchObject({ username: 'li.na', assignments: [{ roleKey: 'operator', siteId: SITE_ID }] });
  expect(created?.csrfToken).toBe(principal.session.csrfToken);
});

test('an administrator disables someone else but never themselves', async ({ page }) => {
  await signInAsAdministrator(page);
  await page.goto('/system?tab=users');
  const users = page.getByRole('table', { name: '用户与角色' });

  await users.getByRole('button', { name: `${principal.principal.displayName} 的操作` }).click();
  await expect(page.getByRole('menuitem', { name: '停用' })).toHaveCount(0);
  await page.keyboard.press('Escape');

  await users.getByRole('button', { name: '周敏 的操作' }).click();
  await page.getByRole('menuitem', { name: '停用' }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: '停用', exact: true }).click();
  await expect(users.getByRole('row', { name: /周敏/ })).toContainText('已停用');
});

test('a signed-in user changes their own password from the user menu', async ({ page }) => {
  await page.goto('/system');
  await page.getByRole('button', { name: new RegExp(principal.principal.displayName) }).click();
  await page.getByRole('menuitem', { name: '修改密码', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '修改密码' });
  await dialog.getByLabel('当前密码').fill('wrong-secret');
  await dialog.getByLabel('新密码', { exact: true }).fill('night-shift-chiller');
  await dialog.getByLabel('再输一次新密码').fill('night-shift-chiller');
  await dialog.getByRole('button', { name: '修改密码', exact: true }).click();
  await expect(dialog.getByRole('alert')).toHaveText('当前密码不对。连续输错 5 次会锁定 15 分钟');

  await dialog.getByLabel('当前密码').fill('current-secret');
  await dialog.getByRole('button', { name: '修改密码', exact: true }).click();
  await expect(dialog).toBeHidden();
});
