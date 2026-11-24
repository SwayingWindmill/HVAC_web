import { expect, test, type Page } from '@playwright/test';

interface StubLoginContext {
  status: 'ready' | 'expired' | 'none';
  step?: 'password' | 'totp';
  csrfToken?: string;
  error?: string;
}

interface CapturedPost {
  pathname: string;
  body: string;
}

async function stubSignedOutSignIn(
  page: Page,
  initialContext: StubLoginContext,
  options: {
    onPost?: (pathname: string, params: URLSearchParams) => StubLoginContext;
    onAuthLogin?: () => StubLoginContext;
  } = {},
): Promise<{ posts: CapturedPost[]; getAuthLoginCount: () => number }> {
  let currentContext: StubLoginContext = { ...initialContext };
  const posts: CapturedPost[] = [];
  let authLoginCount = 0;

  await page.route('**/api/v1/principal', async (route) => {
    await route.fulfill({
      status: 401,
      contentType: 'application/problem+json',
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Authentication required',
        status: 401,
        detail: 'Authentication required.',
        instance: '/api/v1/principal',
        code: 'AUTHENTICATION_REQUIRED',
        traceId: '00000000000000000000000000000001',
        retryable: false,
      }),
    });
  });

  await page.route('**/api/v1/auth/login*', async (route) => {
    authLoginCount += 1;
    if (options.onAuthLogin) {
      currentContext = options.onAuthLogin();
    }
    await route.fulfill({ status: 303, headers: { location: '/sign-in' } });
  });

  await page.route('**/identity/login-context', async (route) => {
    const responseBody = { ...currentContext };
    if (currentContext.error) {
      currentContext = { ...currentContext, error: undefined };
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      headers: { 'cache-control': 'no-store' },
      body: JSON.stringify(responseBody),
    });
  });

  await page.route('**/identity/{login,mfa/totp}', async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const body = route.request().postData() ?? '';
    posts.push({ pathname, body });
    if (options.onPost) {
      currentContext = options.onPost(pathname, new URLSearchParams(body));
    }
    await route.fulfill({ status: 303, headers: { location: '/sign-in' } });
  });

  return {
    posts,
    getAuthLoginCount: () => authLoginCount,
  };
}

function assertBareSignInURL(rawUrl: string): void {
  const parsed = new URL(rawUrl);
  expect(parsed.pathname).toBe('/sign-in');
  expect(parsed.search).toBe('');
}

test('wrong password preserves username while keeping /sign-in URL clean', async ({ page }) => {
  const { posts } = await stubSignedOutSignIn(
    page,
    { status: 'ready', step: 'password', csrfToken: 'csrf-step-1' },
    {
      onPost: () => ({
        status: 'ready',
        step: 'password',
        csrfToken: 'csrf-step-1',
        error: 'invalid_credentials',
      }),
    },
  );

  await page.goto('/sign-in');
  assertBareSignInURL(page.url());
  await page.getByRole('textbox', { name: '用户名' }).fill('operator-1');
  await page.getByRole('textbox', { name: '密码' }).fill('wrong-password');
  await page.getByRole('button', { name: '登录' }).click();

  await expect(page.getByRole('alert')).toContainText('用户名或密码不对，请重新输入。');
  expect(posts).toHaveLength(1);
  expect(posts[0].pathname).toBe('/identity/login');
  const submitted = new URLSearchParams(posts[0].body);
  expect(submitted.get('csrf_token')).toBe('csrf-step-1');
  expect(submitted.get('challenge')).toBeNull();
  expect(submitted.get('username')).toBe('operator-1');
  await expect(page.getByRole('textbox', { name: '用户名' })).toHaveValue('operator-1');
  await expect(page.getByRole('textbox', { name: '密码' })).toHaveValue('');
  assertBareSignInURL(page.url());
});

test('TOTP step submits csrf_token to /identity/mfa/totp without URL parameters', async ({ page }) => {
  const { posts } = await stubSignedOutSignIn(
    page,
    { status: 'ready', step: 'totp', csrfToken: 'csrf-step-2' },
    {
      onPost: () => ({
        status: 'ready',
        step: 'totp',
        csrfToken: 'csrf-step-2',
        error: 'totp_invalid',
      }),
    },
  );

  await page.goto('/sign-in');
  assertBareSignInURL(page.url());
  await expect(page.getByRole('heading', { name: '安全确认' })).toBeVisible();
  await page.getByLabel('一次性验证码').fill('123456');
  await page.getByRole('button', { name: '确认' }).click();

  await expect(page.getByRole('alert')).toContainText('验证码不对或已经用过，请输入认证器上新的验证码。');
  expect(posts).toHaveLength(1);
  expect(posts[0].pathname).toBe('/identity/mfa/totp');
  const submitted = new URLSearchParams(posts[0].body);
  expect(submitted.get('csrf_token')).toBe('csrf-step-2');
  expect(submitted.get('challenge')).toBeNull();
  expect(submitted.get('code')).toBe('123456');
  assertBareSignInURL(page.url());
});

test('expired challenge and logout landing render notices at a clean /sign-in URL', async ({ page }) => {
  await stubSignedOutSignIn(page, { status: 'expired' });
  await page.goto('/sign-in');
  await expect(page.getByTestId('sign-in-notice')).toContainText('登录已超时');
  assertBareSignInURL(page.url());

  await page.goto('/?logged_out=1');
  await expect(page.getByTestId('sign-in-notice')).toContainText('已退出登录');
  await expect(page).toHaveURL(/\/sign-in$/);
  assertBareSignInURL(page.url());
});

test('direct bookmark visit to /sign-in without cookie auto-starts OIDC login once', async ({ page }) => {
  const { getAuthLoginCount } = await stubSignedOutSignIn(
    page,
    { status: 'none' },
    {
      onAuthLogin: () => ({ status: 'ready', step: 'password', csrfToken: 'csrf-fresh' }),
    },
  );

  await page.goto('/sign-in');
  await expect(page.getByRole('textbox', { name: '用户名' })).toBeVisible();
  expect(getAuthLoginCount()).toBe(1);
  assertBareSignInURL(page.url());
});
