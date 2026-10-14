import type { Page } from '@playwright/test';
import { expect, test } from './test';

// Signed out: the Gateway answers the principal bootstrap with 401, as it does before login.
async function signedOut(page: Page) {
  await page.route('**/api/v1/principal', (route) => route.fulfill({
    status: 401, contentType: 'application/problem+json',
    body: JSON.stringify({ status: 401, code: 'AUTHENTICATION_REQUIRED', title: 'Authentication required', detail: 'Sign in.', retryable: false }),
  }));
}

// Stands in for the identity service: records the posted form and redirects like it does.
async function identityService(page: Page, location: string) {
  const posts: { path: string; form: URLSearchParams }[] = [];
  await page.route('**/identity/**', (route) => {
    const request = route.request();
    posts.push({ path: new URL(request.url()).pathname, form: new URLSearchParams(request.postData() ?? '') });
    return route.fulfill({ status: 303, headers: { Location: location } });
  });
  return posts;
}

test('the password goes straight to the identity service, and a wrong one comes back with the name kept', async ({ page }) => {
  await signedOut(page);
  const posts = await identityService(page, '/sign-in?login_challenge=chal-1&error=invalid_credentials');
  await page.goto('/sign-in?login_challenge=chal-1');
  await expect(page.getByText('使用平台账号登录')).toBeVisible();

  await page.getByLabel('用户名').fill('zhou.min');
  await page.getByLabel('密码').fill('wrong password');
  await page.getByRole('button', { name: '登录', exact: true }).click();

  await expect(page.getByRole('alert')).toHaveText('用户名或密码不对，请重新输入。');
  expect(posts).toHaveLength(1);
  expect(posts[0].path).toBe('/identity/login');
  expect(Object.fromEntries(posts[0].form)).toEqual({ challenge: 'chal-1', username: 'zhou.min', password: 'wrong password' });
  await expect(page.getByLabel('用户名')).toHaveValue('zhou.min');
  await expect(page.getByLabel('密码')).toHaveValue('');
  expect(page.url()).not.toContain('zhou.min');
});

test('the one-time code step posts the code to the identity service', async ({ page }) => {
  await signedOut(page);
  const posts = await identityService(page, '/sign-in?login_challenge=chal-2&step=totp&error=totp_invalid');
  await page.goto('/sign-in?login_challenge=chal-2&step=totp');
  await expect(page.getByText('安全确认')).toBeVisible();

  await page.getByLabel('一次性验证码').fill('123456');
  await page.getByRole('button', { name: '确认', exact: true }).click();

  await expect(page.getByRole('alert')).toHaveText('验证码不对或已经用过，请输入认证器上新的验证码。');
  expect(posts[0].path).toBe('/identity/mfa/totp');
  expect(Object.fromEntries(posts[0].form)).toEqual({ challenge: 'chal-2', code: '123456' });
});

test('an expired login request offers to start again', async ({ page }) => {
  await signedOut(page);
  await page.goto('/sign-in?error=expired');
  const notice = page.getByTestId('sign-in-notice');
  await expect(notice).toContainText('登录已超时');
  await expect(notice.getByRole('button', { name: '重新登录' })).toBeVisible();
});
