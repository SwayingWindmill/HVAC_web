# System users and roles

The `/system?tab=users` tab lets administrators manage accounts and role bindings while letting non-admin signed-in users inspect their own granted capabilities grouped into `查看`, `运维`, and `管理` with zero unnamed raw capability keys.

## Sub-features

- `system-users-admin-table` lists accounts (`用户与角色`), creates users via `新建用户`, validates password rules, and disables other accounts while blocking self-disable.
- `system-users-self-capabilities` renders the current user's grouped capabilities (`能力分组`: `查看`, `运维`, `管理`) using `apps/hvac-web/src/lib/capability-labels.ts` and `role-labels.ts`.
- `system-password-change` changes the signed-in user's password from the header user menu (`修改密码`).

## How to get to it (user POV)

- Open the user menu in the top-right header, click `系统管理`, and select the `用户与角色` tab (`/system?tab=users`).
- For non-admin capability view verification, sign in without `iam.admin` and open `/system?tab=users`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/users.spec.ts e2e/system.spec.ts`.
- Tier 2 (`hvac-local`): `localAdministratorBrowser('https://localhost:8443')` navigating to `/system?tab=users`.

- **Create and manage users as admin.** On `/system?tab=users`, assert table `用户与角色`, click `新建用户`, fill `姓名`, `用户名`, `邮箱`, `登录密码`, pick role `运维人员` and site `中央机房`, click `创建`, verify weak-password alert rejects `password1` and strong password creates row + CSRF-tagged `POST /api/v1/iam/members`.
- **Verify zero unnamed capabilities.** Inspect panel `<role> 的权限` (for both non-admin sessions and admin `/api/v1/principal` fixtures containing all 35 capabilities such as `session.revoke` and `template.manage`): assert the caption does NOT contain `未命名的权限` (`capabilityGroups(capabilities).unnamed === 0`).
- **Verify unsaved draft guard across tabs.** Run `e2e/system.spec.ts` to confirm editing `/system?tab=registry` JSON shows `有未保存修改` and prompts `离开会丢弃未保存内容` before leaving.
- **Proof.** Capture Playwright output and `1440×900` screenshot in `.scratch/verify/<slug>/` (`system-users.png`), and confirm the file persists after server shutdown.

## Gotchas

- Always run `npm run web:principal:contract` alongside browser checks when editing `capability-labels.ts` or `role-labels.ts` so both compile-time keys and runtime `ROLE_SUMMARIES` remain 1-to-1 with `platformGateway.gen.ts`.
- Leaving `/system?tab=registry` after typing into `登记导入 JSON` blocks navigation with an `alertdialog`; dismiss it intentionally if automating multi-tab walks.
