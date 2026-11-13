# Sign-in and MFA

The `/sign-in` route renders the OIDC login and one-time-code confirmation screens backed directly by `identity-service` (`/identity/login` and `/identity/mfa/totp`).

## Sub-features

- `sign-in-credentials` submits `用户名` and `密码` to `/identity/login`, preserves `用户名` while clearing `密码` when `/sign-in?error=invalid_credentials` returns, and shows an explicit alert (`用户名或密码不对，请重新输入。`).
- `sign-in-totp` renders `安全确认` on `/sign-in?step=totp`, posts the 6-digit `一次性验证码` to `/identity/mfa/totp`, and shows `验证码不对或已经用过，请输入认证器上新的验证码。` on invalid codes.
- `sign-in-expired-notice` renders `[data-testid="sign-in-notice"]` with `登录已超时` when `?error=expired` is returned.

## How to get to it (user POV)

- Visit any protected route while signed out so the platform redirects to `/sign-in?login_challenge=...`, or navigate directly to `/sign-in?login_challenge=<CHAL>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/sign-in.spec.ts` (stubs `GET /api/v1/principal` -> `401` and intercepts `**/identity/**`).

- **Submit invalid credentials.** On `/sign-in?login_challenge=chal-1`, fill `用户名`, `密码`, click `登录`, assert `alert` text, single POST to `/identity/login`, retained username, cleared password, and no username leak in the URL.
- **Submit one-time code step.** On `/sign-in?login_challenge=chal-2&step=totp`, fill `一次性验证码`, click `确认`, and assert POST to `/identity/mfa/totp`.
- **Verify expired challenge notice.** On `/sign-in?error=expired`, assert `[data-testid="sign-in-notice"]` shows `登录已超时`.
- **Proof.** Capture pass output and `/sign-in` screenshot under `.scratch/verify/<slug>/`.

## Gotchas

- Never query `/sign-in` without mocking `401 AUTHENTICATION_REQUIRED` on `/api/v1/principal` in Tier 1, or the recorded gateway immediately treats the browser as signed in and redirects into `/overview`.
