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
  await expect(users.getByRole('row', { name: /值班工程师/ })).toContainText('已开通');
  await expect(users.getByRole('row', { name: /值班工程师/ })).not.toContainText('提交不了高风险指令');
  const zhouMin = users.getByRole('row', { name: /周敏/ });
  await expect(zhouMin).toContainText('运维人员中央机房');
  await expect(zhouMin).toContainText('未开通');
  await expect(zhouMin).not.toContainText('提交不了高风险指令');

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

test('users who can submit high-risk commands without two-factor verification show a warning until disabled', async ({ page }) => {
  await signInAsAdministrator(page);
  await page.goto('/system?tab=users');
  const users = page.getByRole('table', { name: '用户与角色' });

  await page.getByRole('button', { name: '新建用户', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '新建用户' });
  await dialog.getByLabel('姓名').fill('陈强');
  await dialog.getByLabel('用户名').fill('chen.qiang');
  await dialog.getByLabel('邮箱').fill('chen.qiang@example.invalid');
  await dialog.getByLabel('登录密码').fill('night-shift-chiller');
  await dialog.getByRole('combobox', { name: '角色' }).click();
  await page.getByRole('option', { name: '运维主管', exact: true }).click();
  await dialog.getByRole('combobox', { name: '站点' }).click();
  await page.getByRole('option', { name: '中央机房', exact: true }).click();
  await dialog.getByRole('button', { name: '创建', exact: true }).click();
  await expect(dialog).toBeHidden();

  const supervisorRow = users.getByRole('row', { name: /陈强/ });
  await expect(supervisorRow).toContainText('运维主管中央机房');
  await expect(supervisorRow).toContainText('▲');
  await expect(supervisorRow).toContainText('未开通');
  await expect(supervisorRow).toContainText('提交不了高风险指令');

  await users.getByRole('button', { name: '陈强 的操作' }).click();
  await page.getByRole('menuitem', { name: '停用' }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: '停用', exact: true }).click();
  await expect(supervisorRow).toContainText('已停用');
  await expect(supervisorRow).not.toContainText('提交不了高风险指令');
});

test('an administrator assigns a temporary maintenance role with validTo and sees plain-Chinese expiry', async ({ page, gateway }) => {
  await signInAsAdministrator(page);
  await page.goto('/system?tab=users');
  const users = page.getByRole('table', { name: '用户与角色' });

  await users.getByRole('button', { name: '周敏 的操作' }).click();
  await page.getByRole('menuitem', { name: '角色与站点' }).click();

  const dialog = page.getByRole('dialog');
  await dialog.getByRole('combobox', { name: '角色' }).click();
  await page.getByRole('option', { name: '运维主管' }).click();
  await dialog.getByRole('combobox', { name: '站点' }).click();
  await page.getByRole('option', { name: '中央机房' }).click();

  await dialog.getByRole('combobox', { name: '授权有效期' }).click();
  await page.getByRole('option', { name: '自定义到期时间' }).click();
  await dialog.getByLabel('自定义到期时间').fill('2020-01-01T10:00');
  await dialog.getByRole('button', { name: '添加角色' }).click();
  await expect(dialog).toContainText('到期时间必须晚于当前时间。');

  await dialog.getByRole('combobox', { name: '授权有效期' }).click();
  await page.getByRole('option', { name: '临时 4 小时' }).click();
  await dialog.getByRole('button', { name: '添加角色' }).click();

  const zhouMinMember = gateway.members.find((member) => member.username === 'zhou.min');
  const tempAssignment = zhouMinMember?.assignments.find((item: { roleKey: string; validTo?: string }) => item.roleKey === 'site-manager');
  expect(typeof tempAssignment?.validTo).toBe('string');
  expect(Date.parse(String(tempAssignment?.validTo))).toBeGreaterThan(Date.now());

  await expect(dialog).toContainText('运维主管');
  await expect(dialog).toContainText('至 ');
  await expect(dialog).not.toContainText(String(tempAssignment?.validTo));
  await page.keyboard.press('Escape');

  const zhouMinRow = users.getByRole('row', { name: /周敏/ });
  await expect(zhouMinRow).toContainText('运维人员中央机房长期');
  await expect(zhouMinRow).toContainText('运维主管中央机房至 ');
});

test('multi-tenant switcher switches organizations, clears site/inspect params, and hides for single-tenant users', async ({ page, gateway }) => {
  const adminPrincipal = {
    ...principal,
    principal: { ...principal.principal, roles: ['admin'] },
    authorization: { ...principal.authorization, capabilities: [...principal.authorization.capabilities, 'iam.admin'] },
  };
  gateway.principalOverride = adminPrincipal;
  await page.goto('/system?tab=users');
  await expect(page.getByRole('button', { name: '切换所属组织' })).toHaveCount(0);

  const tenantA = String(principal.context.tenantId);
  const tenantB = '018f3d00-0000-7000-8000-000000000002';
  const siteB = '018f3e00-1000-7000-8000-000000000099';
  const availableTenants = [
    { tenantId: tenantA, code: 'songyaxuan', displayName: '松雅轩' },
    { tenantId: tenantB, code: 'lugu-plant', displayName: '麓谷冷站' },
  ];
  gateway.principalOverride = { ...adminPrincipal, availableTenants };
  gateway.tenantPrincipals.set(tenantB, {
    ...adminPrincipal,
    context: { ...adminPrincipal.context, tenantId: tenantB },
    availableTenants,
  });
  gateway.tenantSites.set(tenantB, [
    {
      id: siteB,
      tenantId: tenantB,
      code: 'lugu-central',
      displayName: '麓谷一号冷站',
      timezone: 'Asia/Shanghai',
      status: 'ACTIVE',
      revision: 1,
      createdAt: '2026-09-28T07:11:40.006Z',
      updatedAt: '2026-09-29T08:51:35.732Z',
      floorAreaM2: 18000,
      climateZone: 'HOT_SUMMER_COLD_WINTER',
    },
  ]);

  await page.goto(`/system?tab=users&site=${SITE_ID}&inspect=chiller-1`);
  const switcher = page.getByRole('button', { name: '切换所属组织' });
  await expect(switcher).toContainText('松雅轩');
  await expect(switcher).not.toContainText(tenantA);

  await switcher.click();
  const menu = page.getByRole('menu');
  await expect(menu).toContainText('松雅轩');
  await expect(menu).toContainText('songyaxuan');
  await expect(menu).toContainText('麓谷冷站');
  await expect(menu).toContainText('lugu-plant');
  await expect(menu).not.toContainText(tenantA);
  await expect(menu).not.toContainText(tenantB);

  await page.getByRole('menuitem', { name: /麓谷冷站/ }).click();

  await expect.poll(() => gateway.tenantSwitchRequests.slice()).toEqual([tenantB]);
  await expect(switcher).toContainText('麓谷冷站');
  await expect(page).toHaveURL(/\/system\?tab=users$/u);
  await expect(page.getByRole('link', { name: '总览', exact: true })).toHaveAttribute('href', '/overview');
  await page.locator('header').getByRole('combobox').click();
  await expect(page.getByText('麓谷一号冷站')).toBeVisible();
});
