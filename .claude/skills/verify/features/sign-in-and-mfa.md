# Sign-in and MFA

The `/sign-in` route renders the OIDC login and one-time-code confirmation screens backed directly by `identity-service` (`/identity/login-context`, `/identity/login`, and `/identity/mfa/totp`). All challenge state and flash errors stay in same-origin `HttpOnly` cookies so `/sign-in` never carries query parameters.

## Sub-features

- `sign-in-credentials` reads `{ status: 'ready', step: 'password', csrfToken }` from `GET /identity/login-context`, submits `用户名`, `密码`, and `csrf_token` to `/identity/login`, preserves `用户名` while clearing `密码` when `login-context` returns the one-time `error: 'invalid_credentials'`, and shows an explicit alert (`用户名或密码不对，请重新输入。`).
- `sign-in-totp` renders `安全确认` when `login-context` returns `step: 'totp'`, posts the 6-digit `一次性验证码` and `csrf_token` to `/identity/mfa/totp`, and shows `验证码不对或已经用过，请输入认证器上新的验证码。` on invalid codes.
- `sign-in-expired-notice` renders `[data-testid="sign-in-notice"]` with `登录已超时` when `login-context` returns `status: 'expired'`, and normalizes logout landing `/?logged_out=1` to bare `/sign-in` with `已退出登录`.

## How to get to it (user POV)

- Visit any protected route while signed out so the platform starts `/api/v1/auth/login` -> `/identity/authorize` and lands on `/sign-in`, or bookmark/visit `/sign-in` directly (which auto-starts login when no active cookie exists).

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/sign-in.spec.ts` (stubs `GET /api/v1/principal` -> `401` and intercepts `**/identity/**`).

- **Submit invalid credentials.** With `GET /identity/login-context` returning `step: 'password'` (and `invalid_credentials` after failure), fill `用户名`, `密码`, click `登录`, assert `alert` text, single POST to `/identity/login` carrying `csrf_token`, retained username, cleared password, and bare `/sign-in` URL with empty query string.
- **Submit one-time code step.** With `GET /identity/login-context` returning `step: 'totp'`, fill `一次性验证码`, click `确认`, and assert POST to `/identity/mfa/totp` with `csrf_token` and bare `/sign-in` URL.
- **Verify expired challenge notice and clean logout URL.** With `login-context` returning `expired`, assert `[data-testid="sign-in-notice"]` shows `登录已超时`; visiting `/?logged_out=1` normalizes browser URL to `/sign-in` with `已退出登录`.
- **Proof.** Capture pass output and `/sign-in` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Never query `/sign-in` without mocking `401 AUTHENTICATION_REQUIRED` on `/api/v1/principal` in Tier 1, or the recorded gateway immediately treats the browser as signed in and redirects into `/overview`.
